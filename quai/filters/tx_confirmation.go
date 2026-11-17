package filters

import (
	"runtime/debug"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
)

// TxConfirmationResult is the notification payload sent to the client
// when a watched transaction is included in a block.
type TxConfirmationResult struct {
	TxHash      common.Hash `json:"txHash"`
	BlockHash   common.Hash `json:"blockHash"`
	BlockNumber uint64      `json:"blockNumber"`
}

// txConfirmWatcher represents a single RPC subscriber waiting for a
// specific transaction to confirm.
type txConfirmWatcher struct {
	notifier *rpc.Notifier
	subID    rpc.ID
	created  time.Time
}

// TxConfirmationManager holds a single ChainHeadEvent subscription and
// maintains a map of tx hashes to watchers. On each new block it
// iterates the block's transactions once and does O(1) map lookups to
// find matching watchers. This is O(block_txs) per block, independent
// of the number of active subscriptions.
type TxConfirmationManager struct {
	backend Backend
	events  *EventSystem
	logger  *log.Logger

	mu       sync.RWMutex
	watchers map[common.Hash][]*txConfirmWatcher

	started  bool
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewTxConfirmationManager creates a new manager. The event loop is
// started lazily on the first Watch call.
func NewTxConfirmationManager(backend Backend, events *EventSystem) *TxConfirmationManager {
	return &TxConfirmationManager{
		backend:  backend,
		events:   events,
		logger:   backend.Logger(),
		watchers: make(map[common.Hash][]*txConfirmWatcher),
		stopCh:   make(chan struct{}),
	}
}

// Watch registers a watcher for txHash. If the transaction is already
// confirmed in the database the notification is sent immediately and
// true is returned. Otherwise the watcher is added to the map and the
// event loop is started if it hasn't been already.
func (m *TxConfirmationManager) Watch(txHash common.Hash, notifier *rpc.Notifier, subID rpc.ID) bool {
	// Fast path: tx already confirmed.
	if m.notifyIfConfirmed(txHash, notifier, subID) {
		return true
	}

	w := &txConfirmWatcher{
		notifier: notifier,
		subID:    subID,
		created:  time.Now(),
	}

	m.mu.Lock()
	m.watchers[txHash] = append(m.watchers[txHash], w)
	if !m.started {
		m.started = true
		go m.eventLoop()
		go m.cleanupLoop()
	}
	m.mu.Unlock()

	// Double-check: the tx may have confirmed between the first db read
	// and inserting into the map. Tx lookup entries are written before
	// ChainHeadEvent fires, so if the tx confirmed in that window we
	// will find it now.
	if m.notifyIfConfirmed(txHash, notifier, subID) {
		m.Unwatch(txHash, subID)
		return true
	}

	return false
}

// Unwatch removes a specific watcher identified by subID.
func (m *TxConfirmationManager) Unwatch(txHash common.Hash, subID rpc.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeWatcherLocked(txHash, subID)
}

// Stop shuts down the event and cleanup loops.
func (m *TxConfirmationManager) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// notifyIfConfirmed checks the database for txHash and sends a
// notification if it is already included in a block. Returns true if
// the notification was sent.
func (m *TxConfirmationManager) notifyIfConfirmed(txHash common.Hash, notifier *rpc.Notifier, subID rpc.ID) bool {
	blockNumber := rawdb.ReadTxLookupEntry(m.backend.ChainDb(), txHash)
	if blockNumber == nil {
		return false
	}
	blockHash := rawdb.ReadCanonicalHash(m.backend.ChainDb(), *blockNumber)
	notifier.Notify(subID, TxConfirmationResult{
		TxHash:      txHash,
		BlockHash:   blockHash,
		BlockNumber: *blockNumber,
	})
	return true
}

// eventLoop subscribes to ChainHeadEvent and dispatches blocks to the
// processWorker via a non-blocking send. This guarantees the
// ChainHeadEvent feed is never stalled regardless of how long
// processBlock takes.
func (m *TxConfirmationManager) eventLoop() {
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()

	headers := make(chan *types.WorkObject, 10)
	sub := m.events.SubscribeChainHeadEvent(headers)
	defer sub.Unsubscribe()

	workCh := make(chan *types.WorkObject, 10)
	go m.processWorker(workCh)

	for {
		select {
		case block := <-headers:
			select {
			case workCh <- block:
			default:
				m.logger.Error("TxConfirmationManager: dropped block, process worker is behind")
			}
		case <-m.stopCh:
			close(workCh)
			return
		}
	}
}

// processWorker drains the work channel and calls processBlock for
// each block sequentially.
func (m *TxConfirmationManager) processWorker(workCh <-chan *types.WorkObject) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()

	for block := range workCh {
		m.processBlock(block)
	}
}

// processBlock iterates the block's transactions once. For each tx it
// does an O(1) map lookup. Confirmed watchers are notified and removed.
func (m *TxConfirmationManager) processBlock(block *types.WorkObject) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()

	nodeCtx := m.backend.NodeCtx()
	blockHash := block.Hash()
	blockNumber := block.NumberU64(nodeCtx)

	// Phase 1: read-lock to find matches.
	type match struct {
		txHash   common.Hash
		watchers []*txConfirmWatcher
	}
	var matches []match

	m.mu.RLock()
	for _, tx := range block.Transactions() {
		h := tx.Hash()
		if ws, ok := m.watchers[h]; ok {
			matches = append(matches, match{txHash: h, watchers: ws})
		}
	}
	m.mu.RUnlock()

	if len(matches) == 0 {
		return
	}

	// Phase 2: notify watchers and remove confirmed entries.
	m.mu.Lock()
	for _, mt := range matches {
		for _, w := range mt.watchers {
			w.notifier.Notify(w.subID, TxConfirmationResult{
				TxHash:      mt.txHash,
				BlockHash:   blockHash,
				BlockNumber: blockNumber,
			})
		}
		delete(m.watchers, mt.txHash)
	}
	m.mu.Unlock()
}

// cleanupLoop periodically removes stale watchers (TTL exceeded or
// client disconnected).
func (m *TxConfirmationManager) cleanupLoop() {
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.cleanup()
		case <-m.stopCh:
			return
		}
	}
}

func (m *TxConfirmationManager) cleanup() {
	defer func() {
		if r := recover(); r != nil {
			m.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()

	const ttl = 30 * time.Minute
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	for txHash, ws := range m.watchers {
		var kept []*txConfirmWatcher
		for _, w := range ws {
			if now.Sub(w.created) >= ttl {
				continue
			}
			// Check if the client connection is still alive.
			select {
			case <-w.notifier.Closed():
				continue
			default:
			}
			kept = append(kept, w)
		}
		if len(kept) == 0 {
			delete(m.watchers, txHash)
		} else {
			m.watchers[txHash] = kept
		}
	}
}

func (m *TxConfirmationManager) removeWatcherLocked(txHash common.Hash, subID rpc.ID) {
	ws := m.watchers[txHash]
	for i, w := range ws {
		if w.subID == subID {
			m.watchers[txHash] = append(ws[:i], ws[i+1:]...)
			if len(m.watchers[txHash]) == 0 {
				delete(m.watchers, txHash)
			}
			return
		}
	}
}
