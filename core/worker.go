package core

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	mapset "github.com/deckarep/golang-set"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru"
	expireLru "github.com/hnlq715/golang-lru"
)

const (
	// resultQueueSize is the size of channel listening to sealing result.
	resultQueueSize = 10

	// resubmitAdjustChanSize is the size of resubmitting interval adjustment channel.
	resubmitAdjustChanSize = 10

	// sealingLogAtDepth is the number of confirmations before logging successful sealing.
	sealingLogAtDepth = 7

	// minRecommitInterval is the minimal time interval to recreate the sealing block with
	// any newly arrived transactions.
	minRecommitInterval = 1 * time.Second

	// staleThreshold is the maximum depth of the acceptable stale block.
	staleThreshold = 7

	// pendingBlockBodyLimit is maximum number of pending block bodies to be kept in cache.
	pendingBlockBodyLimit = 320

	// c_headerPrintsExpiryTime is how long a header hash is kept in the cache, so that currentInfo
	// is not printed on a Proc frequency
	c_headerPrintsExpiryTime = 2 * time.Minute

	// c_chainSideChanSize is the size of the channel listening to uncle events
	chainSideChanSize = 10
)

// environment is the worker's current environment and holds all
// information of the sealing block generation.
type environment struct {
	signer types.Signer

	state     *state.StateDB // apply state changes here
	ancestors mapset.Set     // ancestor set (used for checking uncle parent validity)
	family    mapset.Set     // family set (used for checking uncle invalidity)
	tcount    int            // tx count in cycle
	gasPool   *types.GasPool // available gas used to pack transactions
	coinbase  common.Address
	etxRLimit int // Remaining number of cross-region ETXs that can be included
	etxPLimit int // Remaining number of cross-prime ETXs that can be included

	wo          *types.WorkObject
	txs         []*types.Transaction
	etxs        []*types.Transaction
	utxoFees    *big.Int
	subManifest types.BlockManifest
	receipts    []*types.Receipt
	uncleMu     sync.RWMutex
	uncles      map[common.Hash]*types.WorkObjectHeader
}

// copy creates a deep copy of environment.
func (env *environment) copy(processingState bool, nodeCtx int) *environment {
	if nodeCtx == common.ZONE_CTX && processingState {
		cpy := &environment{
			signer:    env.signer,
			state:     env.state.Copy(),
			ancestors: env.ancestors.Clone(),
			family:    env.family.Clone(),
			tcount:    env.tcount,
			coinbase:  env.coinbase,
			etxRLimit: env.etxRLimit,
			etxPLimit: env.etxPLimit,
			wo:        types.CopyWorkObject(env.wo),
			receipts:  copyReceipts(env.receipts),
			utxoFees:  new(big.Int).Set(env.utxoFees),
		}
		if env.gasPool != nil {
			gasPool := *env.gasPool
			cpy.gasPool = &gasPool
		}
		// The content of txs and uncles are immutable, unnecessary
		// to do the expensive deep copy for them.
		cpy.txs = make([]*types.Transaction, len(env.txs))
		copy(cpy.txs, env.txs)
		cpy.etxs = make([]*types.Transaction, len(env.etxs))
		copy(cpy.etxs, env.etxs)

		env.uncleMu.Lock()
		cpy.uncles = make(map[common.Hash]*types.WorkObjectHeader)
		for hash, uncle := range env.uncles {
			cpy.uncles[hash] = uncle
		}
		env.uncleMu.Unlock()
		return cpy
	} else {
		return &environment{wo: types.CopyWorkObject(env.wo)}
	}
}

// unclelist returns the contained uncles as the list format.
func (env *environment) unclelist() []*types.WorkObjectHeader {
	env.uncleMu.RLock()
	defer env.uncleMu.RUnlock()
	var uncles []*types.WorkObjectHeader
	for _, uncle := range env.uncles {
		uncles = append(uncles, uncle)
	}
	return uncles
}

// discard terminates the background prefetcher go-routine. It should
// always be called for all created environment instances otherwise
// the go-routine leak can happen.
func (env *environment) discard() {
	if env.state == nil {
		return
	}
	env.state.StopPrefetcher()
}

// task contains all information for consensus engine sealing and result submitting.
type task struct {
	receipts  []*types.Receipt
	state     *state.StateDB
	block     *types.WorkObject
	createdAt time.Time
}

const (
	commitInterruptNone int32 = iota
	commitInterruptNewHead
	commitInterruptResubmit
)

// intervalAdjust represents a resubmitting interval adjustment.
type intervalAdjust struct {
	ratio float64
	inc   bool
}

// Config is the configuration parameters of mining.
type Config struct {
	Etherbase  common.Address `toml:",omitempty"` // Public address for block mining rewards (default = first account)
	Notify     []string       `toml:",omitempty"` // HTTP URL list to be notified of new work packages (only useful in ethash).
	NotifyFull bool           `toml:",omitempty"` // Notify with pending block headers instead of work packages
	ExtraData  hexutil.Bytes  `toml:",omitempty"` // Block extra data set by the miner
	GasFloor   uint64         // Target gas floor for mined blocks.
	GasCeil    uint64         // Target gas ceiling for mined blocks.
	GasPrice   *big.Int       // Minimum gas price for mining a transaction
	Recommit   time.Duration  // The time interval for miner to re-create mining work.
	Noverify   bool           // Disable remote mining solution verification(only useful in ethash).
}

// worker is the main object which takes care of submitting new work to consensus engine
// and gathering the sealing result.
type worker struct {
	config       *Config
	chainConfig  *params.ChainConfig
	engine       consensus.Engine
	hc           *HeaderChain
	txPool       *TxPool
	ephemeralKey *secp256k1.PrivateKey
	// Feeds
	pendingLogsFeed   event.Feed
	pendingHeaderFeed event.Feed

	// Subscriptions
	chainHeadCh  chan ChainHeadEvent
	chainHeadSub event.Subscription

	chainSideCh  chan ChainSideEvent
	chainSideSub event.Subscription

	// Channels
	taskCh                         chan *task
	resultCh                       chan *types.WorkObject
	exitCh                         chan struct{}
	resubmitIntervalCh             chan time.Duration
	resubmitAdjustCh               chan *intervalAdjust
	fillTransactionsRollingAverage *RollingAverage

	interrupt   chan struct{}
	asyncPhFeed event.Feed // asyncPhFeed sends an event after each state root update
	scope       event.SubscriptionScope

	wg sync.WaitGroup

	localUncles  map[common.Hash]*types.WorkObjectHeader // A set of side blocks generated locally as the possible uncle blocks.
	remoteUncles map[common.Hash]*types.WorkObjectHeader // A set of side blocks as the possible uncle blocks.
	uncleMu      sync.RWMutex

	mu       sync.RWMutex // The lock used to protect the coinbase and extra fields
	coinbase common.Address
	extra    []byte

	workerDb ethdb.Database

	pendingBlockBody *lru.Cache

	snapshotMu    sync.RWMutex // The lock used to protect the snapshots below
	snapshotBlock *types.WorkObject

	headerPrints *expireLru.Cache

	// atomic status counters
	running int32 // The indicator whether the consensus engine is running or not.
	newTxs  int32 // New arrival transaction count since last sealing work submitting.

	// noempty is the flag used to control whether the feature of pre-seal empty
	// block is enabled. The default value is false(pre-seal is enabled by default).
	// But in some special scenario the consensus engine will seal blocks instantaneously,
	// in this case this feature will add all empty blocks into canonical chain
	// non-stop and no real transaction will be included.
	noempty uint32

	// External functions
	isLocalBlock func(header *types.WorkObject) bool // Function used to determine whether the specified block is mined by local miner.

	// Test hooks
	newTaskHook  func(*task) // Method to call upon receiving a new sealing task.
	fullTaskHook func()      // Method to call before pushing the full sealing task.

	logger *log.Logger
}

type RollingAverage struct {
	windowSize int
	durations  []time.Duration
	sum        time.Duration
}

func (ra *RollingAverage) Add(d time.Duration) {
	if len(ra.durations) == ra.windowSize {
		// Remove the oldest duration from the sum
		ra.sum -= ra.durations[0]
		ra.durations = ra.durations[1:]
	}
	ra.durations = append(ra.durations, d)
	ra.sum += d
}
func (ra *RollingAverage) Average() time.Duration {
	if len(ra.durations) == 0 {
		return 0
	}
	return ra.sum / time.Duration(len(ra.durations))
}

func newWorker(config *Config, chainConfig *params.ChainConfig, db ethdb.Database, engine consensus.Engine, headerchain *HeaderChain, txPool *TxPool, isLocalBlock func(header *types.WorkObject) bool, init bool, processingState bool, logger *log.Logger) *worker {
	worker := &worker{
		config:                         config,
		chainConfig:                    chainConfig,
		engine:                         engine,
		hc:                             headerchain,
		txPool:                         txPool,
		coinbase:                       config.Etherbase,
		isLocalBlock:                   isLocalBlock,
		workerDb:                       db,
		localUncles:                    make(map[common.Hash]*types.WorkObjectHeader),
		remoteUncles:                   make(map[common.Hash]*types.WorkObjectHeader),
		chainHeadCh:                    make(chan ChainHeadEvent, chainHeadChanSize),
		chainSideCh:                    make(chan ChainSideEvent, chainSideChanSize),
		taskCh:                         make(chan *task),
		resultCh:                       make(chan *types.WorkObject, resultQueueSize),
		exitCh:                         make(chan struct{}),
		interrupt:                      make(chan struct{}),
		resubmitIntervalCh:             make(chan time.Duration),
		resubmitAdjustCh:               make(chan *intervalAdjust, resubmitAdjustChanSize),
		fillTransactionsRollingAverage: &RollingAverage{windowSize: 100},
		logger:                         logger,
	}
	// Set the GasFloor of the worker to the minGasLimit
	worker.config.GasFloor = params.MinGasLimit

	phBodyCache, _ := lru.New(pendingBlockBodyLimit)
	worker.pendingBlockBody = phBodyCache

	// Sanitize recommit interval if the user-specified one is too short.
	recommit := worker.config.Recommit
	if recommit < minRecommitInterval {
		logger.WithFields(log.Fields{
			"provided": recommit,
			"updated":  minRecommitInterval,
		}).Warn("Sanitizing miner recommit interval")
		recommit = minRecommitInterval
	}

	headerPrints, _ := expireLru.NewWithExpire(1, c_headerPrintsExpiryTime)
	worker.headerPrints = headerPrints

	nodeCtx := headerchain.NodeCtx()
	if headerchain.ProcessingState() && nodeCtx == common.ZONE_CTX {
		worker.chainHeadSub = worker.hc.SubscribeChainHeadEvent(worker.chainHeadCh)
		worker.chainSideSub = worker.hc.SubscribeChainSideEvent(worker.chainSideCh)
		worker.wg.Add(1)
		go worker.asyncStateLoop()
	}

	worker.ephemeralKey, _ = secp256k1.GeneratePrivateKey()

	return worker
}

// setEtherbase sets the etherbase used to initialize the block coinbase field.
func (w *worker) setEtherbase(addr common.Address) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.coinbase = addr
}

func (w *worker) setGasCeil(ceil uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.config.GasCeil = ceil
}

// setExtra sets the content used to initialize the block extra field.
func (w *worker) setExtra(extra []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.extra = extra
}

// setRecommitInterval updates the interval for miner sealing work recommitting.
func (w *worker) setRecommitInterval(interval time.Duration) {
	select {
	case w.resubmitIntervalCh <- interval:
	case <-w.exitCh:
	}
}

// disablePreseal disables pre-sealing feature
func (w *worker) disablePreseal() {
	atomic.StoreUint32(&w.noempty, 1)
}

// enablePreseal enables pre-sealing feature
func (w *worker) enablePreseal() {
	atomic.StoreUint32(&w.noempty, 0)
}

// pending returns the pending state and corresponding block.
func (w *worker) pending() *types.WorkObject {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return w.snapshotBlock
}

// pendingBlock returns pending block.
func (w *worker) pendingBlock() *types.WorkObject {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return w.snapshotBlock
}

// pendingBlockAndReceipts returns pending block and corresponding receipts.
func (w *worker) pendingBlockAndReceipts() (*types.WorkObject, types.Receipts) {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	// snapshot receipts are not stored in the worker anymore, so pending receipts is nil
	return w.snapshotBlock, nil
}

// start sets the running status as 1 and triggers new work submitting.
func (w *worker) start() {
	atomic.StoreInt32(&w.running, 1)
}

// stop sets the running status as 0.
func (w *worker) stop() {
	if w.hc.ProcessingState() && w.hc.NodeCtx() == common.ZONE_CTX {
		w.chainHeadSub.Unsubscribe()
		w.chainSideSub.Unsubscribe()
	}
	atomic.StoreInt32(&w.running, 0)
}

// isRunning returns an indicator whether worker is running or not.
func (w *worker) isRunning() bool {
	return atomic.LoadInt32(&w.running) == 1
}

// close terminates all background threads maintained by the worker.
// Note the worker does not support being closed multiple times.
func (w *worker) close() {
	atomic.StoreInt32(&w.running, 0)
	close(w.exitCh)
	w.scope.Close()
	w.wg.Wait()
}

func (w *worker) LoadPendingBlockBody() {
	pendingBlockBodykeys := rawdb.ReadPbBodyKeys(w.workerDb)
	for _, key := range pendingBlockBodykeys {
		if key == types.EmptyBodyHash {
			w.pendingBlockBody.Add(key, &types.WorkObject{})
		} else {
			w.pendingBlockBody.Add(key, rawdb.ReadPbCacheBody(w.workerDb, key))
		}
		// Remove the entry from the database so that body is not accumulated over multiple stops
		rawdb.DeletePbCacheBody(w.workerDb, key)
	}
	rawdb.DeleteAllPbBodyKeys(w.workerDb)
}

// StorePendingBlockBody stores the pending block body cache into the db
func (w *worker) StorePendingBlockBody() {
	// store the pendingBodyCache body
	var pendingBlockBodyKeys common.Hashes
	pendingBlockBody := w.pendingBlockBody
	for _, key := range pendingBlockBody.Keys() {
		if value, exist := pendingBlockBody.Peek(key); exist {
			pendingBlockBodyKeys = append(pendingBlockBodyKeys, key.(common.Hash))
			if key.(common.Hash) != types.EmptyBodyHash {
				rawdb.WritePbCacheBody(w.workerDb, key.(common.Hash), value.(*types.WorkObject))
			}
		}
	}
	rawdb.WritePbBodyKeys(w.workerDb, pendingBlockBodyKeys)
}

// asyncStateLoop updates the state root for a block and returns the state udpate in a channel
func (w *worker) asyncStateLoop() {
	defer func() {
		if r := recover(); r != nil {
			w.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	defer w.wg.Done() // decrement the wait group after the close of the loop

	for {
		select {
		case head := <-w.chainHeadCh:

			w.interruptAsyncPhGen()

			go func() {
				defer func() {
					if r := recover(); r != nil {
						w.logger.WithFields(log.Fields{
							"error":      r,
							"stacktrace": string(debug.Stack()),
						}).Error("Go-Quai Panicked")
					}
				}()
				select {
				case <-w.interrupt:
					w.interrupt = make(chan struct{})
					return
				default:
					wo := head.Block
					header, err := w.GeneratePendingHeader(wo, true)
					if err != nil {
						w.logger.WithField("err", err).Error("Error generating pending header")
						return
					}
					// Send the updated pendingHeader in the asyncPhFeed
					w.asyncPhFeed.Send(header)
					return
				}
			}()
		case side := <-w.chainSideCh:
			go func() {
				defer func() {
					if r := recover(); r != nil {
						w.logger.WithFields(log.Fields{
							"error":      r,
							"stacktrace": string(debug.Stack()),
						}).Fatal("Go-Quai Panicked")
					}
				}()
				if side.ResetUncles {
					w.uncleMu.Lock()
					w.localUncles = make(map[common.Hash]*types.WorkObjectHeader)
					w.remoteUncles = make(map[common.Hash]*types.WorkObjectHeader)
					w.uncleMu.Unlock()
				}
				for _, wo := range side.Blocks {

					// Short circuit for duplicate side blocks
					w.uncleMu.RLock()
					if _, exists := w.localUncles[wo.Hash()]; exists {
						w.uncleMu.RUnlock()
						continue
					}
					if _, exists := w.remoteUncles[wo.Hash()]; exists {
						w.uncleMu.RUnlock()
						continue
					}
					w.uncleMu.RUnlock()
					if w.isLocalBlock != nil && w.isLocalBlock(wo) {
						w.uncleMu.Lock()
						w.localUncles[wo.Hash()] = wo.WorkObjectHeader()
						w.uncleMu.Unlock()
					} else {
						w.uncleMu.Lock()
						w.remoteUncles[wo.Hash()] = wo.WorkObjectHeader()
						w.uncleMu.Unlock()
					}
				}
			}()
		case <-w.exitCh:
			return
		case <-w.chainHeadSub.Err():
			return
		case <-w.chainSideSub.Err():
			return
		}
	}
}

// GeneratePendingBlock generates pending block given a commited block.
func (w *worker) GeneratePendingHeader(block *types.WorkObject, fill bool) (*types.WorkObject, error) {
	nodeCtx := w.hc.NodeCtx()

	w.interruptAsyncPhGen()

	var (
		interrupt *int32
		timestamp int64 // timestamp for each round of sealing.
	)

	if interrupt != nil {
		atomic.StoreInt32(interrupt, commitInterruptNewHead)
	}
	interrupt = new(int32)
	atomic.StoreInt32(&w.newTxs, 0)

	start := time.Now()
	// Set the coinbase if the worker is running or it's required
	var coinbase common.Address
	if w.hc.NodeCtx() == common.ZONE_CTX && w.coinbase.Equal(common.Address{}) && w.hc.ProcessingState() {
		w.logger.Error("Refusing to mine without etherbase")
		return nil, errors.New("etherbase not found")
	} else if w.coinbase.Equal(common.Address{}) {
		w.coinbase = common.Zero
	}
	coinbase = w.coinbase // Use the preset address as the fee recipient

	work, err := w.prepareWork(&generateParams{
		timestamp: uint64(timestamp),
		coinbase:  coinbase,
	}, block)
	if err != nil {
		return nil, err
	}

	if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
		if coinbase.IsInQiLedgerScope() {
			work.txs = append(work.txs, types.NewTx(&types.QiTx{})) // placeholder
		} else if coinbase.IsInQuaiLedgerScope() {
			work.txs = append(work.txs, types.NewTx(&types.QuaiTx{})) // placeholder
		}
		// Fill pending transactions from the txpool
		w.adjustGasLimit(work, block)
		work.utxoFees = big.NewInt(0)
		start := time.Now()
		etxSet := w.fillTransactions(interrupt, work, block, fill)
		if fill {
			w.fillTransactionsRollingAverage.Add(time.Since(start))
			w.logger.WithFields(log.Fields{
				"count":   len(work.txs),
				"elapsed": common.PrettyDuration(time.Since(start)),
				"average": common.PrettyDuration(w.fillTransactionsRollingAverage.Average()),
			}).Info("Filled and sorted pending transactions")
		}
		// Set the etx set commitment in the header
		if etxSet != nil {
			work.wo.Header().SetEtxSetHash(etxSet.Hash())
		} else {
			work.wo.Header().SetEtxSetHash(types.EmptyEtxSetHash)
		}
		if coinbase.IsInQiLedgerScope() {
			coinbaseTx, err := createQiCoinbaseTxWithFees(work.wo, work.utxoFees, work.state, work.signer, w.ephemeralKey)
			if err != nil {
				return nil, err
			}
			work.txs[0] = coinbaseTx
		} else if coinbase.IsInQuaiLedgerScope() {
			coinbaseTx, err := createQuaiCoinbaseTx(work.state, work.wo, w.logger, work.signer, w.ephemeralKey.ToECDSA())
			if err != nil {
				return nil, err
			}
			work.txs[0] = coinbaseTx
		}
	}

	// Create a local environment copy, avoid the data race with snapshot state.
	newWo, err := w.FinalizeAssemble(w.hc, work.wo, block, work.state, work.txs, work.unclelist(), work.etxs, work.subManifest, work.receipts)
	if err != nil {
		return nil, err
	}

	work.wo = newWo

	w.printPendingHeaderInfo(work, newWo, start)

	return newWo, nil
}

// printPendingHeaderInfo logs the pending header information
func (w *worker) printPendingHeaderInfo(work *environment, block *types.WorkObject, start time.Time) {
	work.uncleMu.RLock()
	if w.CurrentInfo(block) {
		w.logger.WithFields(log.Fields{
			"number":     block.Number(w.hc.NodeCtx()),
			"parent":     block.ParentHash(w.hc.NodeCtx()),
			"sealhash":   block.SealHash(),
			"uncles":     len(work.uncles),
			"txs":        len(work.txs),
			"etxs":       len(block.ExtTransactions()),
			"gas":        block.GasUsed(),
			"fees":       totalFees(block, work.receipts),
			"elapsed":    common.PrettyDuration(time.Since(start)),
			"utxoRoot":   block.UTXORoot(),
			"etxSetHash": block.EtxSetHash(),
		}).Info("Commit new sealing work")
	} else {
		w.logger.WithFields(log.Fields{
			"number":     block.Number(w.hc.NodeCtx()),
			"parent":     block.ParentHash(w.hc.NodeCtx()),
			"sealhash":   block.SealHash(),
			"uncles":     len(work.uncles),
			"txs":        len(work.txs),
			"etxs":       len(block.ExtTransactions()),
			"gas":        block.GasUsed(),
			"fees":       totalFees(block, work.receipts),
			"elapsed":    common.PrettyDuration(time.Since(start)),
			"utxoRoot":   block.UTXORoot(),
			"etxSetHash": block.EtxSetHash(),
		}).Debug("Commit new sealing work")
	}
	work.uncleMu.RUnlock()
}

// interruptAsyncPhGen kills any async ph generation running
func (w *worker) interruptAsyncPhGen() {
	if w.interrupt != nil {
		close(w.interrupt)
		w.interrupt = nil
	}
}

func (w *worker) eventExitLoop() {
	for {
		select {
		case <-w.exitCh:
			return
		}
	}
}

// makeEnv creates a new environment for the sealing block.
func (w *worker) makeEnv(parent *types.WorkObject, proposedWo *types.WorkObject, coinbase common.Address) (*environment, error) {
	// Retrieve the parent state to execute on top and start a prefetcher for
	// the miner to speed block sealing up a bit.
	evmRoot := parent.EVMRoot()
	utxoRoot := parent.UTXORoot()
	if w.hc.IsGenesisHash(parent.Hash()) {
		evmRoot = types.EmptyRootHash
		utxoRoot = types.EmptyRootHash
	}
	state, err := w.hc.bc.processor.StateAt(evmRoot, utxoRoot)
	if err != nil {
		return nil, err
	}

	etxRLimit := len(parent.Transactions()) / params.ETXRegionMaxFraction
	if etxRLimit < params.ETXRLimitMin {
		etxRLimit = params.ETXRLimitMin
	}
	etxPLimit := len(parent.Transactions()) / params.ETXPrimeMaxFraction
	if etxPLimit < params.ETXPLimitMin {
		etxPLimit = params.ETXPLimitMin
	}
	// Note the passed coinbase may be different with header.Coinbase.
	env := &environment{
		signer:    types.MakeSigner(w.chainConfig, proposedWo.Number(w.hc.NodeCtx())),
		state:     state,
		coinbase:  coinbase,
		ancestors: mapset.NewSet(),
		family:    mapset.NewSet(),
		wo:        proposedWo,
		uncles:    make(map[common.Hash]*types.WorkObjectHeader),
		etxRLimit: etxRLimit,
		etxPLimit: etxPLimit,
	}
	// when 08 is processed ancestors contain 07 (quick block)
	for _, ancestor := range w.hc.GetBlocksFromHash(parent.Header().Hash(), 7) {
		for _, uncle := range ancestor.Uncles() {
			env.family.Add(uncle.Hash())
		}
		env.family.Add(ancestor.Hash())
		env.ancestors.Add(ancestor.Hash())
	}
	// Keep track of transactions which return errors so they can be removed
	env.tcount = 0
	return env, nil
}

// commitUncle adds the given block to uncle block set, returns error if failed to add.
func (w *worker) commitUncle(env *environment, uncle *types.WorkObjectHeader) error {
	env.uncleMu.Lock()
	defer env.uncleMu.Unlock()
	hash := uncle.Hash()

	// when 08 is processed ancestors contain 07 (quick block)
	for _, ancestor := range w.hc.GetBlocksFromHash(env.wo.ParentHash(common.ZONE_CTX), 7) {
		for _, uncle := range ancestor.Uncles() {
			env.family.Add(uncle.Hash())
		}
		env.family.Add(ancestor.Hash())
		env.ancestors.Add(ancestor.Hash())
	}

	if _, exist := env.uncles[hash]; exist {
		return errors.New("uncle not unique")
	}
	if env.wo.ParentHash(w.hc.NodeCtx()) == uncle.ParentHash() {
		return errors.New("uncle is sibling")
	}
	if !env.ancestors.Contains(uncle.ParentHash()) {
		return errors.New("uncle's parent unknown")
	}
	if env.family.Contains(hash) {
		return errors.New("uncle already included")
	}
	env.uncles[hash] = uncle
	return nil
}

func (w *worker) commitTransaction(env *environment, parent *types.WorkObject, tx *types.Transaction) ([]*types.Log, error) {
	if tx != nil {
		if tx.Type() == types.ExternalTxType && tx.To().IsInQiLedgerScope() {
			gasUsed := env.wo.GasUsed()
			txGas := tx.Gas()
			if tx.ETXSender().Location().Equal(*tx.To().Location()) { // Quai->Qi conversion
				lock := new(big.Int).Add(env.wo.Number(w.hc.NodeCtx()), big.NewInt(params.ConversionLockPeriod))
				primeTerminus := w.hc.GetPrimeTerminus(env.wo)
				if primeTerminus == nil {
					return nil, errors.New("prime terminus not found")
				}
				value := misc.QuaiToQi(primeTerminus, tx.Value())
				denominations := misc.FindMinDenominations(value)
				outputIndex := uint16(0)

				// Iterate over the denominations in descending order
				for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
					// If the denomination count is zero, skip it
					if denominations[uint8(denomination)] == 0 {
						continue
					}
					for j := uint8(0); j < denominations[uint8(denomination)]; j++ {
						if txGas < params.CallValueTransferGas || outputIndex >= types.MaxOutputIndex {
							// No more gas, the rest of the denominations are lost but the tx is still valid
							break
						}
						txGas -= params.CallValueTransferGas
						if err := env.gasPool.SubGas(params.CallValueTransferGas); err != nil {
							return nil, err
						}
						gasUsed += params.CallValueTransferGas
						// the ETX hash is guaranteed to be unique
						if err := env.state.CreateUTXO(tx.Hash(), outputIndex, types.NewUtxoEntry(types.NewTxOut(uint8(denomination), tx.To().Bytes(), lock))); err != nil {
							return nil, err
						}
						outputIndex++
					}
				}
			} else {
				// This Qi ETX should cost more gas
				if err := env.gasPool.SubGas(params.CallValueTransferGas); err != nil {
					return nil, err
				}
				if err := env.state.CreateUTXO(tx.OriginatingTxHash(), tx.ETXIndex(), types.NewUtxoEntry(types.NewTxOut(uint8(tx.Value().Uint64()), tx.To().Bytes(), big.NewInt(0)))); err != nil {
					return nil, err
				}
				gasUsed += params.CallValueTransferGas
			}
			env.wo.Header().SetGasUsed(gasUsed)
			env.txs = append(env.txs, tx)
			return []*types.Log{}, nil // need to make sure this does not modify receipt hash
		}
		snap := env.state.Snapshot()
		// retrieve the gas used int and pass in the reference to the ApplyTransaction
		gasUsed := env.wo.GasUsed()
		receipt, err := ApplyTransaction(w.chainConfig, parent, w.hc, &env.coinbase, env.gasPool, env.state, env.wo, tx, &gasUsed, *w.hc.bc.processor.GetVMConfig(), &env.etxRLimit, &env.etxPLimit, w.logger)
		if err != nil {
			w.logger.WithFields(log.Fields{
				"err":     err,
				"tx":      tx.Hash().Hex(),
				"block":   env.wo.Number(w.hc.NodeCtx()),
				"gasUsed": gasUsed,
			}).Debug("Error playing transaction in worker")
			env.state.RevertToSnapshot(snap)
			return nil, err
		}
		if receipt.Status == types.ReceiptStatusSuccessful {
			env.etxs = append(env.etxs, receipt.Etxs...)
		}
		// once the gasUsed pointer is updated in the ApplyTransaction it has to be set back to the env.Header.GasUsed
		// This extra step is needed because previously the GasUsed was a public method and direct update of the value
		// was possible.
		env.wo.Header().SetGasUsed(gasUsed)
		env.txs = append(env.txs, tx)
		env.receipts = append(env.receipts, receipt)
		return receipt.Logs, nil
	}
	return nil, errors.New("error finding transaction")
}

func (w *worker) commitTransactions(env *environment, parent *types.WorkObject, etxs []*types.Transaction, txs *types.TransactionsByPriceAndNonce, etxSet *types.EtxSet, interrupt *int32) bool {
	qiTxsToRemove := make([]*common.Hash, 0)
	gasLimit := env.wo.GasLimit
	if env.gasPool == nil {
		env.gasPool = new(types.GasPool).AddGas(gasLimit())
	}
	var coalescedLogs []*types.Log
	minEtxGas := gasLimit() / params.MinimumEtxGasDivisor
	for _, tx := range etxs {
		if interrupt != nil && atomic.LoadInt32(interrupt) != commitInterruptNone {
			return atomic.LoadInt32(interrupt) == commitInterruptNewHead
		}
		if env.gasPool.Gas() < params.TxGas {
			w.logger.WithFields(log.Fields{
				"have": env.gasPool,
				"want": params.TxGas,
			}).Trace("Not enough gas for further transactions")
			break
		}
		// Add ETXs until minimum gas is used
		if env.wo.GasUsed() >= minEtxGas {
			break
		}
		if env.wo.GasUsed() > minEtxGas*params.MaximumEtxGasMultiplier { // sanity check, this should never happen
			w.logger.WithField("Gas Used", env.wo.GasUsed()).Error("Block uses more gas than maximum ETX gas")
			return true
		}
		hash := etxSet.Pop()
		if hash != tx.Hash() { // sanity check, this should never happen
			w.logger.Errorf("ETX hash from set %032x does not match transaction hash %032x", hash, tx.Hash())
			return true
		}
		env.state.Prepare(tx.Hash(), env.tcount)
		logs, err := w.commitTransaction(env, parent, tx)
		if err == nil {
			coalescedLogs = append(coalescedLogs, logs...)
			env.tcount++
		}
	}
	for {
		// In the following three cases, we will interrupt the execution of the transaction.
		// (1) new head block event arrival, the interrupt signal is 1
		// (2) worker start or restart, the interrupt signal is 1
		// (3) worker recreate the sealing block with any newly arrived transactions, the interrupt signal is 2.
		// For the first two cases, the semi-finished work will be discarded.
		// For the third case, the semi-finished work will be submitted to the consensus engine.
		if interrupt != nil && atomic.LoadInt32(interrupt) != commitInterruptNone {
			// Notify resubmit loop to increase resubmitting interval due to too frequent commits.
			if atomic.LoadInt32(interrupt) == commitInterruptResubmit {
				ratio := float64(gasLimit()-env.gasPool.Gas()) / float64(gasLimit())
				if ratio < 0.1 {
					ratio = 0.1
				}
				w.resubmitAdjustCh <- &intervalAdjust{
					ratio: ratio,
					inc:   true,
				}
			}
			return atomic.LoadInt32(interrupt) == commitInterruptNewHead
		}
		// If we don't have enough gas for any further transactions then we're done
		if env.gasPool.Gas() < params.TxGas {
			w.logger.WithFields(log.Fields{
				"have": env.gasPool,
				"want": params.TxGas,
			}).Trace("Not enough gas for further transactions")
			break
		}
		// Retrieve the next transaction and abort if all done
		tx := txs.Peek()
		if tx == nil {
			break
		}
		if tx.Type() == types.QiTxType {
			txGas := types.CalculateBlockQiTxGas(tx, w.hc.NodeLocation())
			if env.gasPool.Gas() < txGas {
				w.logger.WithFields(log.Fields{
					"have": env.gasPool,
					"want": txGas,
				}).Trace("Not enough gas for further transactions")
				break
			}
			if err := w.processQiTx(tx, env); err != nil {
				hash := tx.Hash()
				w.logger.WithFields(log.Fields{
					"err": err,
					"tx":  hash.Hex(),
				}).Error("Error processing QiTx")
				// It's unlikely that this transaction will be valid in the future so remove it asynchronously
				qiTxsToRemove = append(qiTxsToRemove, &hash)
			}
			txs.PopNoSort()
			continue
		}
		// Error may be ignored here. The error has already been checked
		// during transaction acceptance is the transaction pool.
		//
		// We use the signer regardless of the current hf.
		from, err := types.Sender(env.signer, tx)
		if err != nil {
			continue
		}
		// Start executing the transaction
		env.state.Prepare(tx.Hash(), env.tcount)

		logs, err := w.commitTransaction(env, parent, tx)
		switch {
		case errors.Is(err, types.ErrGasLimitReached):
			// Pop the current out-of-gas transaction without shifting in the next from the account
			w.logger.WithField("sender", from).Trace("Gas limit exceeded for current block")
			txs.PopNoSort()

		case errors.Is(err, ErrEtxLimitReached):
			// Pop the current transaction without shifting in the next from the account
			w.logger.WithField("sender", from).Trace("Etx limit exceeded for current block")
			txs.PopNoSort()

		case errors.Is(err, ErrNonceTooLow):
			// New head notification data race between the transaction pool and miner, shift
			w.logger.WithFields(log.Fields{
				"sender": from,
				"nonce":  tx.Nonce(),
			}).Trace("Skipping transaction with low nonce")
			txs.Shift(from.Bytes20(), false)

		case errors.Is(err, ErrNonceTooHigh):
			// Reorg notification data race between the transaction pool and miner, skip account =
			w.logger.WithFields(log.Fields{
				"sender": from,
				"nonce":  tx.Nonce(),
			}).Trace("Skipping account with high nonce")
			txs.PopNoSort()

		case errors.Is(err, nil):
			// Everything ok, collect the logs and shift in the next transaction from the same account
			coalescedLogs = append(coalescedLogs, logs...)
			env.tcount++
			txs.PopNoSort()

		case errors.Is(err, ErrTxTypeNotSupported):
			// Pop the unsupported transaction without shifting in the next from the account
			w.logger.WithFields(log.Fields{
				"sender": from,
				"type":   tx.Type(),
			}).Error("Skipping unsupported transaction type")
			txs.PopNoSort()

		case strings.Contains(err.Error(), "emits too many cross"): // This is ErrEtxLimitReached with more info
			// Pop the unsupported transaction without shifting in the next from the account
			w.logger.WithFields(log.Fields{
				"sender": from,
				"err":    err,
			}).Trace("Etx limit exceeded for current block")
			txs.PopNoSort()

		default:
			// Strange error, discard the transaction and get the next in line (note, the
			// nonce-too-high clause will prevent us from executing in vain).
			w.logger.WithFields(log.Fields{
				"hash":   tx.Hash(),
				"sender": from,
				"err":    err,
			}).Error("Transaction failed, account skipped")
			txs.Shift(from.Bytes20(), false)
		}
	}
	if len(qiTxsToRemove) > 0 {
		go w.txPool.RemoveQiTxs(qiTxsToRemove)
	}
	if !w.isRunning() && len(coalescedLogs) > 0 {
		// We don't push the pendingLogsEvent while we are sealing. The reason is that
		// when we are sealing, the worker will regenerate a sealing block every 3 seconds.
		// In order to avoid pushing the repeated pendingLog, we disable the pending log pushing.

		// make a copy, the state caches the logs and these logs get "upgraded" from pending to mined
		// logs by filling in the block hash when the block was mined by the local miner. This can
		// cause a race condition if a log was "upgraded" before the PendingLogsEvent is processed.
		cpy := make([]*types.Log, len(coalescedLogs))
		for i, l := range coalescedLogs {
			cpy[i] = new(types.Log)
			*cpy[i] = *l
		}
		w.pendingLogsFeed.Send(cpy)
	}
	return false
}

// generateParams wraps various of settings for generating sealing task.
type generateParams struct {
	timestamp uint64         // The timstamp for sealing task
	forceTime bool           // Flag whether the given timestamp is immutable or not
	coinbase  common.Address // The fee recipient address for including transaction
}

// prepareWork constructs the sealing task according to the given parameters,
// either based on the last chain head or specified parent. In this function
// the pending transactions are not filled yet, only the empty task returned.
func (w *worker) prepareWork(genParams *generateParams, wo *types.WorkObject) (*environment, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	nodeCtx := w.hc.NodeCtx()

	// Find the parent block for sealing task
	parent := wo
	// Sanity check the timestamp correctness, recap the timestamp
	// to parent+1 if the mutation is allowed.
	timestamp := genParams.timestamp
	if parent.WorkObjectHeader().Time() >= timestamp {
		if genParams.forceTime {
			return nil, fmt.Errorf("invalid timestamp, parent %d given %d", parent.WorkObjectHeader().Time(), timestamp)
		}
		timestamp = parent.WorkObjectHeader().Time() + 1
	}
	// Construct the sealing block header, set the extra field if it's allowed
	num := parent.Number(nodeCtx)
	newWo := types.EmptyHeader(nodeCtx)
	newWo.SetParentHash(wo.Hash(), nodeCtx)
	if w.hc.IsGenesisHash(parent.Hash()) {
		newWo.SetNumber(big.NewInt(1), nodeCtx)
	} else {
		newWo.SetNumber(big.NewInt(int64(num.Uint64())+1), nodeCtx)
	}
	newWo.WorkObjectHeader().SetTime(timestamp)
	newWo.WorkObjectHeader().SetLocation(w.hc.NodeLocation())

	// Only calculate entropy if the parent is not the genesis block
	_, order, err := w.engine.CalcOrder(parent)
	if err != nil {
		return nil, err
	}
	if !w.hc.IsGenesisHash(parent.Hash()) {
		// Set the parent delta S prior to sending to sub
		if nodeCtx != common.PRIME_CTX {
			if order < nodeCtx {
				newWo.Header().SetParentDeltaS(big.NewInt(0), nodeCtx)
			} else {
				newWo.Header().SetParentDeltaS(w.engine.DeltaLogS(w.hc, parent), nodeCtx)
			}
		}
		newWo.Header().SetParentEntropy(w.engine.TotalLogS(w.hc, parent), nodeCtx)
	} else {
		newWo.Header().SetParentEntropy(big.NewInt(0), nodeCtx)
		newWo.Header().SetParentDeltaS(big.NewInt(0), nodeCtx)
	}

	// Only calculate entropy if the parent is not the genesis block
	if !w.hc.IsGenesisHash(parent.Hash()) {
		// Set the parent delta S prior to sending to sub
		if nodeCtx != common.PRIME_CTX {
			if order < nodeCtx {
				newWo.Header().SetParentUncledSubDeltaS(big.NewInt(0), nodeCtx)
			} else {
				newWo.Header().SetParentUncledSubDeltaS(w.engine.UncledSubDeltaLogS(w.hc, parent), nodeCtx)
			}
		}
	} else {
		newWo.Header().SetParentUncledSubDeltaS(big.NewInt(0), nodeCtx)
	}

	// calculate the expansion values - except for the etxEligibleSlices, the
	// zones cannot modify any of the other fields its done in prime
	if nodeCtx == common.PRIME_CTX {
		if parent.NumberU64(common.PRIME_CTX) == 0 {
			newWo.Header().SetEfficiencyScore(0)
			newWo.Header().SetThresholdCount(0)
			newWo.Header().SetExpansionNumber(0)
		} else {
			// compute the efficiency score at each prime block
			efficiencyScore := w.hc.ComputeEfficiencyScore(parent)
			newWo.Header().SetEfficiencyScore(efficiencyScore)

			// If the threshold count is zero we have not started considering for the
			// expansion
			if parent.Header().ThresholdCount() == 0 {
				if efficiencyScore > params.TREE_EXPANSION_THRESHOLD {
					newWo.Header().SetThresholdCount(parent.Header().ThresholdCount() + 1)
				} else {
					newWo.Header().SetThresholdCount(0)
				}
			} else {
				// If the efficiency score goes below the threshold,  and we still have
				// not triggered the expansion, reset the threshold count or if we go
				// past the tree expansion trigger window we have to reset the
				// threshold count
				if (parent.Header().ThresholdCount() < params.TREE_EXPANSION_TRIGGER_WINDOW && efficiencyScore < params.TREE_EXPANSION_THRESHOLD) ||
					parent.Header().ThresholdCount() >= params.TREE_EXPANSION_TRIGGER_WINDOW+params.TREE_EXPANSION_WAIT_COUNT {
					newWo.Header().SetThresholdCount(0)
				} else {
					newWo.Header().SetThresholdCount(parent.Header().ThresholdCount() + 1)
				}
			}

			// Expansion happens when the threshold count is greater than the
			// expansion threshold and we cross the tree expansion trigger window
			if parent.Header().ThresholdCount() >= params.TREE_EXPANSION_TRIGGER_WINDOW+params.TREE_EXPANSION_WAIT_COUNT {
				newWo.Header().SetExpansionNumber(parent.Header().ExpansionNumber() + 1)
			} else {
				newWo.Header().SetExpansionNumber(parent.Header().ExpansionNumber())
			}
		}
	}

	// Compute the Prime Terminus
	if nodeCtx == common.ZONE_CTX {
		if order == common.PRIME_CTX {
			// Set the prime terminus
			newWo.Header().SetPrimeTerminus(parent.Hash())
		} else {
			if w.hc.IsGenesisHash(parent.Hash()) {
				newWo.Header().SetPrimeTerminus(parent.Hash())
			} else {
				// carry the prime terminus from the parent block
				newWo.Header().SetPrimeTerminus(parent.Header().PrimeTerminus())
			}
		}
	}

	if nodeCtx == common.PRIME_CTX {
		if w.hc.IsGenesisHash(parent.Hash()) {
			newWo.Header().SetEtxEligibleSlices(parent.Header().EtxEligibleSlices())
		} else {
			newWo.Header().SetEtxEligibleSlices(w.hc.UpdateEtxEligibleSlices(parent, parent.Location()))
		}
	}

	var interlinkHashes common.Hashes
	if nodeCtx == common.PRIME_CTX {
		if w.hc.IsGenesisHash(parent.Hash()) {
			// On genesis, the interlink hashes are all the same and should start with genesis hash
			interlinkHashes = common.Hashes{parent.Hash(), parent.Hash(), parent.Hash(), parent.Hash()}
		} else {
			// check if parent belongs to any interlink level
			rank, err := w.engine.CalcRank(w.hc, parent)
			if err != nil {
				return nil, err
			}
			if rank == 0 { // No change in the interlink hashes, so carry
				interlinkHashes = parent.InterlinkHashes()
			} else if rank > 0 && rank <= common.InterlinkDepth {
				interlinkHashes = parent.InterlinkHashes()
				// update the interlink hashes for each level below the rank
				for i := 0; i < rank; i++ {
					interlinkHashes[i] = parent.Hash()
				}
			} else {
				w.logger.Error("Not possible to find rank greater than the max interlink levels")
			}
		}
		// Store the interlink hashes in the database
		rawdb.WriteInterlinkHashes(w.workerDb, parent.Hash(), interlinkHashes)
		interlinkRootHash := types.DeriveSha(interlinkHashes, trie.NewStackTrie(nil))
		newWo.Header().SetInterlinkRootHash(interlinkRootHash)
	}

	// Only zone should calculate state
	if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
		newWo.Header().SetExtra(w.extra)
		newWo.Header().SetBaseFee(misc.CalcBaseFee(w.chainConfig, parent))
		if w.isRunning() {
			if w.coinbase.Equal(common.Zero) {
				w.logger.Error("Refusing to mine without etherbase")
				return nil, errors.New("refusing to mine without etherbase")
			}
			newWo.Header().SetCoinbase(w.coinbase)
		}

		// Run the consensus preparation with the default or customized consensus engine.
		if err := w.engine.Prepare(w.hc, newWo, wo); err != nil {
			w.logger.WithField("err", err).Error("Failed to prepare header for sealing")
			return nil, err
		}
		proposedWoHeader := types.NewWorkObjectHeader(newWo.Hash(), newWo.ParentHash(nodeCtx), newWo.Number(nodeCtx), newWo.Difficulty(), types.EmptyRootHash, newWo.Nonce(), newWo.Time(), newWo.Location())
		proposedWoBody, err := types.NewWorkObjectBody(newWo.Header(), nil, nil, nil, nil, nil, nil, nodeCtx)
		if err != nil {
			return nil, err
		}
		proposedWo := types.NewWorkObject(proposedWoHeader, proposedWoBody, nil)
		env, err := w.makeEnv(parent, proposedWo, w.coinbase)
		if err != nil {
			w.logger.WithField("err", err).Error("Failed to create sealing context")
			return nil, err
		}
		// Accumulate the uncles for the sealing work.
		commitUncles := func(wos map[common.Hash]*types.WorkObjectHeader) {
			for hash, uncle := range wos {
				env.uncleMu.RLock()
				if len(env.uncles) == 2 {
					env.uncleMu.RUnlock()
					break
				}
				env.uncleMu.RUnlock()
				if err := w.commitUncle(env, uncle); err != nil {
					w.logger.WithFields(log.Fields{
						"hash":   hash,
						"reason": err,
					}).Trace("Possible uncle rejected")
				} else {
					w.logger.WithField("hash", hash).Debug("Committing new uncle to block")
				}
			}
		}
		if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
			w.uncleMu.RLock()
			// Prefer to locally generated uncle
			commitUncles(w.localUncles)
			commitUncles(w.remoteUncles)
			w.uncleMu.RUnlock()
		}
		return env, nil
	} else {
		proposedWoHeader := types.NewWorkObjectHeader(newWo.Hash(), newWo.ParentHash(nodeCtx), newWo.Number(nodeCtx), newWo.Difficulty(), types.EmptyRootHash, newWo.Nonce(), newWo.Time(), newWo.Location())
		proposedWoBody, err := types.NewWorkObjectBody(newWo.Header(), nil, nil, nil, nil, nil, nil, nodeCtx)
		if err != nil {
			return nil, err
		}
		proposedWo := types.NewWorkObject(proposedWoHeader, proposedWoBody, nil)
		return &environment{wo: proposedWo}, nil
	}

}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (w *worker) fillTransactions(interrupt *int32, env *environment, block *types.WorkObject, fill bool) *types.EtxSet {
	// Split the pending transactions into locals and remotes
	// Fill the block with all available pending transactions.
	etxs := make([]*types.Transaction, 0)
	etxSet := rawdb.ReadEtxSet(w.hc.bc.db, block.Hash(), block.NumberU64(w.hc.NodeCtx()))
	if etxSet != nil {
		etxs = make([]*types.Transaction, 0, len(etxSet.ETXHashes)/common.HashLength)
		maxEtxGas := (env.wo.GasLimit() / params.MinimumEtxGasDivisor) * params.MaximumEtxGasMultiplier
		totalGasEstimate := uint64(0)
		index := 0
		for {
			hash := etxSet.GetHashAtIndex(index)
			if (hash == common.Hash{}) { // no more ETXs
				break
			}
			entry := rawdb.ReadETX(w.hc.bc.db, hash)
			if entry == nil {
				w.logger.Errorf("ETX %s not found in the database!", hash.String())
				break
			}
			etxs = append(etxs, entry)
			if totalGasEstimate += entry.Gas(); totalGasEstimate > maxEtxGas { // We don't need to load any more ETXs after this limit
				break
			}
			index++
		}
	}
	if !fill {
		if len(etxs) > 0 {
			w.commitTransactions(env, block, etxs, &types.TransactionsByPriceAndNonce{}, etxSet, interrupt)
		}
		return etxSet
	}

	pending, err := w.txPool.TxPoolPending(false)
	if err != nil {
		return nil
	}

	pendingQiTxs := w.txPool.QiPoolPending()

	if len(pending) > 0 || len(pendingQiTxs) > 0 || len(etxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, pendingQiTxs, pending, env.wo.BaseFee(), true)
		if w.commitTransactions(env, block, etxs, txs, etxSet, interrupt) {
			return etxSet
		}
	}
	return etxSet
}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (w *worker) adjustGasLimit(env *environment, parent *types.WorkObject) {
	env.wo.Header().SetGasLimit(CalcGasLimit(parent, w.config.GasCeil))
}

// ComputeManifestHash given a header computes the manifest hash for the header
// and stores it in the database
func (w *worker) ComputeManifestHash(header *types.WorkObject) common.Hash {
	manifest := rawdb.ReadManifest(w.workerDb, header.Hash())
	if manifest == nil {
		nodeCtx := w.hc.NodeCtx()
		// Compute and set manifest hash
		manifest = types.BlockManifest{}
		if nodeCtx == common.PRIME_CTX {
			// Nothing to do for prime chain
			manifest = types.BlockManifest{}
		} else if w.engine.IsDomCoincident(w.hc, header) {
			manifest = types.BlockManifest{header.Hash()}
		} else {
			parentManifest := rawdb.ReadManifest(w.workerDb, header.ParentHash(nodeCtx))
			manifest = append(parentManifest, header.Hash())
		}
		// write the manifest into the disk
		rawdb.WriteManifest(w.workerDb, header.Hash(), manifest)
	}
	manifestHash := types.DeriveSha(manifest, trie.NewStackTrie(nil))

	return manifestHash
}

func (w *worker) FinalizeAssemble(chain consensus.ChainHeaderReader, newWo *types.WorkObject, parent *types.WorkObject, state *state.StateDB, txs []*types.Transaction, uncles []*types.WorkObjectHeader, etxs []*types.Transaction, subManifest types.BlockManifest, receipts []*types.Receipt) (*types.WorkObject, error) {
	nodeCtx := w.hc.NodeCtx()
	wo, err := w.engine.FinalizeAndAssemble(chain, newWo, state, txs, uncles, etxs, subManifest, receipts)
	if err != nil {
		return nil, err
	}

	// Once the uncles list is assembled in the block
	if nodeCtx == common.ZONE_CTX {
		wo.Header().SetUncledS(w.engine.UncledLogS(wo))
	}

	manifestHash := w.ComputeManifestHash(parent)

	if w.hc.ProcessingState() {
		wo.Header().SetManifestHash(manifestHash, nodeCtx)
		if nodeCtx == common.ZONE_CTX {
			// Compute and set etx rollup hash
			var etxRollup types.Transactions
			if w.engine.IsDomCoincident(w.hc, parent) {
				etxRollup = parent.ExtTransactions()
			} else {
				etxRollup, err = w.hc.CollectEtxRollup(parent)
				if err != nil {
					return nil, err
				}
				etxRollup = append(etxRollup, parent.ExtTransactions()...)
			}
			etxRollupHash := types.DeriveSha(etxRollup, trie.NewStackTrie(nil))
			wo.Header().SetEtxRollupHash(etxRollupHash)
		}
	}

	return wo, nil
}

// AddPendingBlockBody adds an entry in the lru cache for the given pendingBodyKey
// maps it to body.
func (w *worker) AddPendingWorkObjectBody(wo *types.WorkObject) {
	w.pendingBlockBody.Add(wo.SealHash(), wo)
}

// GetPendingBlockBody gets the block body associated with the given header.
func (w *worker) GetPendingBlockBody(woHeader *types.WorkObject) (*types.WorkObject, error) {
	body, ok := w.pendingBlockBody.Get(woHeader.SealHash())
	if ok {
		return body.(*types.WorkObject), nil
	}
	w.logger.WithField("key", woHeader.SealHash()).Warn("pending block body not found for header")
	return nil, errors.New("pending block body not found")
}

func (w *worker) SubscribeAsyncPendingHeader(ch chan *types.WorkObject) event.Subscription {
	return w.scope.Track(w.asyncPhFeed.Subscribe(ch))
}

// copyReceipts makes a deep copy of the given receipts.
func copyReceipts(receipts []*types.Receipt) []*types.Receipt {
	result := make([]*types.Receipt, len(receipts))
	for i, l := range receipts {
		cpy := *l
		result[i] = &cpy
	}
	return result
}

// totalFees computes total consumed miner fees in ETH. Block transactions and receipts have to have the same order.
func totalFees(block *types.WorkObject, receipts []*types.Receipt) *big.Float {
	feesWei := new(big.Int)
	for i, tx := range block.QuaiTransactionsWithoutCoinbase() {
		minerFee, _ := tx.EffectiveGasTip(block.BaseFee())
		feesWei.Add(feesWei, new(big.Int).Mul(new(big.Int).SetUint64(receipts[i].GasUsed), minerFee))
	}
	return new(big.Float).Quo(new(big.Float).SetInt(feesWei), new(big.Float).SetInt(big.NewInt(params.Ether)))
}

func (w *worker) CurrentInfo(header *types.WorkObject) bool {
	if w.headerPrints.Contains(header.Hash()) {
		return false
	}

	w.headerPrints.Add(header.Hash(), nil)
	return header.NumberU64(w.hc.NodeCtx())+c_startingPrintLimit > w.hc.CurrentHeader().NumberU64(w.hc.NodeCtx())
}

func (w *worker) processQiTx(tx *types.Transaction, env *environment) error {
	location := w.hc.NodeLocation()
	if tx.Type() != types.QiTxType {
		return fmt.Errorf("tx %032x is not a QiTx", tx.Hash())
	}
	if types.IsCoinBaseTx(tx, env.wo.ParentHash(w.hc.NodeCtx()), location) {
		return fmt.Errorf("tx %032x is a coinbase QiTx", tx.Hash())
	}
	if tx.ChainId().Cmp(w.chainConfig.ChainID) != 0 {
		return fmt.Errorf("tx %032x has wrong chain ID", tx.Hash())
	}
	gasUsed := env.wo.GasUsed()
	intrinsicGas := types.CalculateIntrinsicQiTxGas(tx)
	gasUsed += intrinsicGas // the amount of block gas used in this transaction is only the txGas, regardless of ETXs emitted
	if err := env.gasPool.SubGas(intrinsicGas); err != nil {
		return err
	}
	if gasUsed > env.wo.GasLimit() {
		return fmt.Errorf("tx %032x uses too much gas, have used %d out of %d", tx.Hash(), gasUsed, env.wo.GasLimit())
	}

	addresses := make(map[common.AddressBytes]struct{})
	totalQitIn := big.NewInt(0)
	utxosDelete := make([]types.OutPoint, 0)
	inputDenominations := make(map[uint8]uint64)
	for _, txIn := range tx.TxIn() {
		utxo := env.state.GetUTXO(txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		if utxo == nil {
			return fmt.Errorf("tx %032x spends non-existent UTXO %032x:%d", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		}
		if utxo.Lock != nil && utxo.Lock.Cmp(env.wo.Number(w.hc.NodeCtx())) > 0 {
			return fmt.Errorf("tx %032x spends locked UTXO %032x:%d locked until %s", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, utxo.Lock.String())
		}

		// Perform some spend processing logic
		denomination := utxo.Denomination
		if denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				denomination,
				types.MaxDenomination)
			return errors.New(str)
		}
		inputDenominations[denomination] += 1
		// Check for duplicate addresses. This also checks for duplicate inputs.
		if _, exists := addresses[common.AddressBytes(utxo.Address)]; exists {
			return errors.New("Duplicate address in QiTx inputs: " + common.AddressBytes(utxo.Address).String())
		}
		addresses[common.AddressBytes(utxo.Address)] = struct{}{}
		totalQitIn.Add(totalQitIn, types.Denominations[denomination])
		utxosDelete = append(utxosDelete, txIn.PreviousOutPoint)
	}
	var ETXRCount int
	var ETXPCount int
	etxs := make([]*types.ExternalTx, 0)
	totalQitOut := big.NewInt(0)
	totalConvertQitOut := big.NewInt(0)
	utxosCreate := make(map[types.OutPoint]*types.UtxoEntry)
	conversion := false
	var convertAddress common.Address
	outputDenominations := make(map[uint8]uint64)
	for txOutIdx, txOut := range tx.TxOut() {
		if txOutIdx > types.MaxOutputIndex {
			return errors.New("transaction has too many outputs")
		}
		if txOut.Denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				txOut.Denomination,
				types.MaxDenomination)
			return errors.New(str)
		}
		outputDenominations[txOut.Denomination] += 1
		totalQitOut.Add(totalQitOut, types.Denominations[txOut.Denomination])
		toAddr := common.BytesToAddress(txOut.Address, location)
		if _, exists := addresses[toAddr.Bytes20()]; exists {
			return errors.New("Duplicate address in QiTx outputs: " + toAddr.String())
		}
		addresses[toAddr.Bytes20()] = struct{}{}

		if toAddr.Location().Equal(location) && toAddr.IsInQuaiLedgerScope() { // Qi->Quai conversion
			conversion = true
			convertAddress = toAddr
			if txOut.Denomination < params.MinQiConversionDenomination {
				w.logger.Error(fmt.Errorf("tx %032x emits convert UTXO with value %d less than minimum conversion denomination", tx.Hash(), txOut.Denomination))
				return fmt.Errorf("tx %032x emits convert UTXO with value %d less than minimum conversion denomination", tx.Hash(), txOut.Denomination)
			}
			totalConvertQitOut.Add(totalConvertQitOut, types.Denominations[txOut.Denomination]) // Add to total conversion output for aggregation
			outputDenominations[txOut.Denomination] -= 1                                        // This output no longer exists because it has been aggregated
			delete(addresses, toAddr.Bytes20())
			continue
		}

		if !toAddr.Location().Equal(location) { // This output creates an ETX
			// Cross-region?
			if toAddr.Location().CommonDom(location).Context() == common.REGION_CTX {
				ETXRCount++
			}
			// Cross-prime?
			if toAddr.Location().CommonDom(location).Context() == common.PRIME_CTX {
				ETXPCount++
			}
			if ETXRCount > env.etxRLimit {
				w.logger.Error(fmt.Errorf("tx %032x emits too many cross-region ETXs for block. emitted: %d, limit: %d", tx.Hash(), ETXRCount, env.etxRLimit))
				return fmt.Errorf("tx [%v] emits too many cross-region ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXRCount, env.etxRLimit)
			}
			if ETXPCount > env.etxPLimit {
				w.logger.Error(fmt.Errorf("tx %032x emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash(), ETXPCount, env.etxPLimit))
				return fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXPCount, env.etxPLimit)
			}
			if !toAddr.IsInQiLedgerScope() {
				w.logger.Error(fmt.Errorf("tx %032x emits UTXO with To address not in the Qi ledger scope", tx.Hash()))
				return fmt.Errorf("tx [%v] emits UTXO with To address not in the Qi ledger scope", tx.Hash().Hex())
			}

			// We should require some kind of extra fee here
			etxInner := types.ExternalTx{Value: big.NewInt(int64(txOut.Denomination)), To: &toAddr, Sender: common.ZeroAddress(location), OriginatingTxHash: tx.Hash(), ETXIndex: uint16(txOutIdx), Gas: params.TxGas}
			gasUsed += params.ETXGas
			if err := env.gasPool.SubGas(params.ETXGas); err != nil {
				return err
			}
			primeTerminus := w.hc.GetPrimeTerminus(env.wo)
			if !w.hc.CheckIfEtxIsEligible(primeTerminus.EtxEligibleSlices(), *toAddr.Location()) {
				return fmt.Errorf("etx emitted by tx [%v] going to a slice that is not eligible to receive etx %v", tx.Hash().Hex(), *toAddr.Location())
			}
			etxs = append(etxs, &etxInner)
			w.logger.Debug("Added UTXO ETX to block")
		} else {
			// This output creates a normal UTXO
			utxo := types.NewUtxoEntry(&txOut)
			utxosCreate[types.OutPoint{TxHash: tx.Hash(), Index: uint16(txOutIdx)}] = utxo
		}
	}
	// Ensure the transaction does not spend more than its inputs.
	if totalQitOut.Cmp(totalQitIn) > 0 {
		str := fmt.Sprintf("total value of all transaction inputs for "+
			"transaction %v is %v which is less than the amount "+
			"spent of %v", tx.Hash(), totalQitIn, totalQitOut)
		return errors.New(str)
	}
	txFeeInQit := new(big.Int).Sub(totalQitIn, totalQitOut)
	// Check tx against required base fee and gas
	requiredGas := intrinsicGas + (uint64(len(etxs)) * (params.TxGas + params.ETXGas)) // Each ETX costs extra gas that is paid in the origin
	if requiredGas < intrinsicGas {
		// overflow
		return fmt.Errorf("tx %032x has too many ETXs to calculate required gas", tx.Hash())
	}
	minimumFeeInQuai := new(big.Int).Mul(big.NewInt(int64(requiredGas)), env.wo.BaseFee())
	minimumFee := misc.QuaiToQi(env.wo, minimumFeeInQuai)
	if txFeeInQit.Cmp(minimumFee) < 0 {
		return fmt.Errorf("tx %032x has insufficient fee for base fee * gas, have %d want %d", tx.Hash(), txFeeInQit.Uint64(), minimumFee.Uint64())
	}
	// Miner gets remainder of fee after base fee, except in the convert case
	txFeeInQit.Sub(txFeeInQit, minimumFee)

	if conversion {
		// Since this transaction contains a conversion, the rest of the tx gas is given to conversion
		remainingTxFeeInQuai := misc.QiToQuai(env.wo, txFeeInQit)
		// Fee is basefee * gas, so gas remaining is fee remaining / basefee
		remainingGas := new(big.Int).Div(remainingTxFeeInQuai, env.wo.BaseFee())
		if remainingGas.Uint64() > (env.wo.GasLimit() / params.MinimumEtxGasDivisor) {
			// Limit ETX gas to max ETX gas limit (the rest is burned)
			remainingGas = new(big.Int).SetUint64(env.wo.GasLimit() / params.MinimumEtxGasDivisor)
		}
		ETXPCount++ // conversion is technically a cross-prime ETX
		if ETXPCount > env.etxPLimit {
			w.logger.Error(fmt.Errorf("tx %032x emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash(), ETXPCount, env.etxPLimit))
			return fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXPCount, env.etxPLimit)
		}
		etxInner := types.ExternalTx{Value: totalConvertQitOut, To: &convertAddress, Sender: common.ZeroAddress(location), OriginatingTxHash: tx.Hash(), Gas: remainingGas.Uint64()} // Value is in Qits not Denomination
		gasUsed += params.ETXGas
		if err := env.gasPool.SubGas(params.ETXGas); err != nil {
			return err
		}
		etxs = append(etxs, &etxInner)
		txFeeInQit.Sub(txFeeInQit, txFeeInQit) // Fee goes entirely to gas to pay for conversion
	}
	env.wo.Header().SetGasUsed(gasUsed)
	env.etxRLimit -= ETXRCount
	env.etxPLimit -= ETXPCount
	for _, etx := range etxs {
		env.etxs = append(env.etxs, types.NewTx(etx))
	}
	env.txs = append(env.txs, tx)
	env.utxoFees.Add(env.utxoFees, txFeeInQit)
	for _, utxo := range utxosDelete {
		env.state.DeleteUTXO(utxo.TxHash, utxo.Index)
	}
	for outPoint, utxo := range utxosCreate {
		if err := env.state.CreateUTXO(outPoint.TxHash, outPoint.Index, utxo); err != nil {
			// This should never happen and will invalidate the block
			log.Global.Errorf("Failed to create UTXO %032x:%d: %v", outPoint.TxHash, outPoint.Index, err)
			return err
		}
	}
	// We could add signature verification here, but it's already checked in the mempool and the signature can't be changed, so duplication is largely unnecessary
	return nil
}

// createQiCoinbaseTxWithFees returns a coinbase transaction paying an appropriate subsidy
// based on the passed block height to the provided address. The transaction is signed
// with an ephemeral key generated in the worker.
func createQiCoinbaseTxWithFees(header *types.WorkObject, fees *big.Int, state *state.StateDB, signer types.Signer, ephemeralKey *secp256k1.PrivateKey) (*types.Transaction, error) {
	parentHash := header.ParentHash(header.Location().Context()) // all blocks should have zone location and context

	// The coinbase transaction input must be the parent hash encoded with the proper origin location
	origin := (uint8(header.Location()[0]) * 16) + uint8(header.Location()[1])
	parentHash[0] = origin
	parentHash[1] = origin
	in := types.TxIn{
		// Coinbase transaction input is parent hash with max output index
		PreviousOutPoint: *types.NewOutPoint(&parentHash, types.MaxOutputIndex),
		PubKey:           ephemeralKey.PubKey().SerializeUncompressed(),
	}

	reward := misc.CalculateReward(header)
	reward.Add(reward, fees)
	denominations := misc.FindMinDenominations(reward)
	outs := make([]types.TxOut, 0, len(denominations))

	// Iterate over the denominations in descending order (by key)
	for i := types.MaxDenomination; i >= 0; i-- {
		// If the denomination count is zero, skip it
		if denominations[uint8(i)] == 0 {
			continue
		}
		for j := uint8(0); j < denominations[uint8(i)]; j++ {
			// Create the output for the denomination
			out := types.TxOut{
				Denomination: uint8(i),
				Address:      header.Coinbase().Bytes(),
			}
			outs = append(outs, out)
		}
	}

	qiTx := &types.QiTx{
		TxIn:  []types.TxIn{in},
		TxOut: outs,
	}
	txDigestHash := signer.Hash(types.NewTx(qiTx))
	sig, err := schnorr.Sign(ephemeralKey, txDigestHash[:])
	if err != nil {
		return nil, err
	}
	qiTx.Signature = sig
	if !sig.Verify(txDigestHash[:], ephemeralKey.PubKey()) {
		return nil, fmt.Errorf("ephemeral key signature verification failed")
	}
	tx := types.NewTx(qiTx)
	for i, out := range qiTx.TxOut {
		// this may be unnecessary
		if err := state.CreateUTXO(tx.Hash(), uint16(i), types.NewUtxoEntry(&out)); err != nil {
			return nil, err
		}
	}
	return tx, nil
}

// createQuaiCoinbaseTx returns a coinbase transaction paying an appropriate subsidy
// based on the passed block height to the provided address.
func createQuaiCoinbaseTx(state *state.StateDB, header *types.WorkObject, logger *log.Logger, signer types.Signer, key *ecdsa.PrivateKey) (*types.Transaction, error) {
	// Select the correct block reward based on chain progression
	blockReward := misc.CalculateReward(header)
	coinbase := header.Coinbase()
	internal, err := coinbase.InternalAndQuaiAddress()
	if err != nil {
		logger.WithFields(log.Fields{
			"Address": header.Coinbase().String(),
			"Hash":    header.Hash().String(),
		}).Error("Block has out of scope coinbase, skipping block reward")
		return nil, fmt.Errorf("block has out of scope coinbase, skipping block reward")
	}
	tx := types.NewTx(&types.QuaiTx{To: &coinbase, Value: blockReward, Data: common.Hex2Bytes("Quai block reward")})
	tx, err = types.SignTx(tx, signer, key)
	if err != nil {
		return nil, err
	}
	state.AddBalance(internal, blockReward)
	return tx, nil
}
