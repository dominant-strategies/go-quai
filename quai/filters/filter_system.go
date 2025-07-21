// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package filters implements an quai filtering system for block,
// transactions and log events.
package filters

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	quai "github.com/dominant-strategies/go-quai"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
)

// Type determines the kind of filter and is used to put the filter in to
// the correct bucket when added.
type Type byte

const (
	// UnknownSubscription indicates an unknown subscription type
	UnknownSubscription Type = iota
	// LogsSubscription queries for new or removed (chain reorg) logs
	LogsSubscription
	// PendingLogsSubscription queries for logs in pending blocks
	PendingLogsSubscription
	// MinedAndPendingLogsSubscription queries for logs in mined and pending blocks.
	MinedAndPendingLogsSubscription
	// PendingTransactionsSubscription queries tx hashes for pending
	// transactions entering the pending state
	PendingTransactionsSubscription
	// BlocksSubscription queries hashes for blocks that are imported
	BlocksSubscription
	// UnlocksSubscription queries balances that are recently unlocked
	UnlocksSubscription
	// ChainHeadSubscription queries for the chain head block
	ChainHeadSubscription
	// WorkshareSubscription queries for new workshares received via P2P
	WorkshareSubscription
	// LastSubscription keeps track of the last index
	LastIndexSubscription
)

const (
	// txChanSize is the size of channel listening to NewTxsEvent.
	// The number is referenced from the size of tx pool.
	txChanSize = 4096
	// rmLogsChanSize is the size of channel listening to RemovedLogsEvent.
	rmLogsChanSize = 10
	// logsChanSize is the size of channel listening to LogsEvent.
	logsChanSize = 10
	// chainEvChanSize is the size of channel listening to ChainEvent.
	chainEvChanSize      = 10
	unlocksEvChanSize    = 10
	workshareEvChanSize = 100
)

type subscription struct {
	id        rpc.ID
	typ       Type
	created   time.Time
	logsCrit  quai.FilterQuery
	logs       chan []*types.Log
	hashes     chan []common.Hash
	headers    chan *types.WorkObject
	unlocks    chan core.UnlocksEvent
	header     chan *types.WorkObject
	workshares chan *types.WorkObject
	installed chan struct{} // closed when the filter is installed
	err       chan error    // closed when the filter is uninstalled
}

// EventSystem creates subscriptions, processes events and broadcasts them to the
// subscription which match the subscription criteria.
type EventSystem struct {
	backend   Backend
	lightMode bool
	lastHead  *types.Header

	// Subscriptions
	logsSub        event.Subscription // Subscription for new log event
	rmLogsSub      event.Subscription // Subscription for removed log event
	pendingLogsSub event.Subscription // Subscription for pending log event
	chainSub       event.Subscription // Subscription for new chain event
	unlocksSub     event.Subscription // Subscription for new unlocks event
	chainHeadSub   event.Subscription // Subscription for new head event
	workshareSub   event.Subscription // Subscription for new workshare event

	// Channels
	install       chan *subscription         // install filter for event notification
	uninstall     chan *subscription         // remove filter for event notification
	txsCh         chan core.NewTxsEvent      // Channel to receive new transactions event
	logsCh        chan []*types.Log          // Channel to receive new log event
	pendingLogsCh chan []*types.Log          // Channel to receive new log event
	rmLogsCh      chan core.RemovedLogsEvent // Channel to receive removed log event
	chainCh       chan core.ChainEvent       // Channel to receive new chain event
	unlocksCh     chan core.UnlocksEvent       // Channel to receive newly unlocked coinbases
	chainHeadCh   chan core.ChainHeadEvent     // Channel to receive new chain event
	workshareCh   chan core.NewWorkshareEvent // Channel to receive new workshare event
}

// NewEventSystem creates a new manager that listens for event on the given mux,
// parses and filters them. It uses the all map to retrieve filter changes. The
// work loop holds its own index that is used to forward events to filters.
//
// The returned manager has a loop that needs to be stopped with the Stop function
// or by stopping the given mux.
func NewEventSystem(backend Backend) *EventSystem {
	m := &EventSystem{
		backend:       backend,
		install:       make(chan *subscription),
		uninstall:     make(chan *subscription),
		txsCh:         make(chan core.NewTxsEvent, txChanSize),
		logsCh:        make(chan []*types.Log, logsChanSize),
		rmLogsCh:      make(chan core.RemovedLogsEvent, rmLogsChanSize),
		pendingLogsCh: make(chan []*types.Log, logsChanSize),
		chainCh:       make(chan core.ChainEvent, chainEvChanSize),
		unlocksCh:     make(chan core.UnlocksEvent, unlocksEvChanSize),
		chainHeadCh:   make(chan core.ChainHeadEvent, chainEvChanSize),
		workshareCh:   make(chan core.NewWorkshareEvent, workshareEvChanSize),
	}

	nodeCtx := backend.NodeCtx()
	// Subscribe events
	if nodeCtx == common.ZONE_CTX && backend.ProcessingState() {
		m.logsSub = m.backend.SubscribeLogsEvent(m.logsCh)
		m.rmLogsSub = m.backend.SubscribeRemovedLogsEvent(m.rmLogsCh)
		m.pendingLogsSub = m.backend.SubscribePendingLogsEvent(m.pendingLogsCh)
		m.unlocksSub = m.backend.SubscribeUnlocksEvent(m.unlocksCh)
	}
	m.chainSub = m.backend.SubscribeChainEvent(m.chainCh)

	m.chainHeadSub = m.backend.SubscribeChainHeadEvent(m.chainHeadCh)
	m.workshareSub = m.backend.SubscribeNewWorkshareEvent(m.workshareCh)

	// Make sure none of the subscriptions are empty
	if nodeCtx == common.ZONE_CTX && backend.ProcessingState() {
		if m.logsSub == nil || m.rmLogsSub == nil || m.chainSub == nil || m.pendingLogsSub == nil || m.chainHeadSub == nil || m.workshareSub == nil {
			backend.Logger().Fatal("Subscribe for event system failed")
		}
	} else {
		if m.chainSub == nil || m.chainHeadSub == nil || m.workshareSub == nil {
			backend.Logger().Fatal("Subscribe for event system failed")
		}
	}

	go m.eventLoop()
	return m
}

// Subscription is created when the client registers itself for a particular event.
type Subscription struct {
	ID        rpc.ID
	f         *subscription
	es        *EventSystem
	unsubOnce sync.Once
}

// Err returns a channel that is closed when unsubscribed.
func (sub *Subscription) Err() <-chan error {
	return sub.f.err
}

// Unsubscribe uninstalls the subscription from the event broadcast loop.
func (sub *Subscription) Unsubscribe() {
	sub.unsubOnce.Do(func() {
	uninstallLoop:
		for {
			// write uninstall request and consume logs/hashes. This prevents
			// the eventLoop broadcast method to deadlock when writing to the
			// filter event channel while the subscription loop is waiting for
			// this method to return (and thus not reading these events).
			select {
			case sub.es.uninstall <- sub.f:
				break uninstallLoop
			case <-sub.f.logs:
			case <-sub.f.hashes:
			case <-sub.f.headers:
			case <-sub.f.unlocks:
			}
		}

		// wait for filter to be uninstalled in work loop before returning
		// this ensures that the manager won't use the event channel which
		// will probably be closed by the client asap after this method returns.
		<-sub.Err()
	})
}

// subscribe installs the subscription in the event broadcast loop.
func (es *EventSystem) subscribe(sub *subscription) *Subscription {
	es.install <- sub
	<-sub.installed
	return &Subscription{ID: sub.id, f: sub, es: es}
}

// SubscribeLogs creates a subscription that will write all logs matching the
// given criteria to the given logs channel. Default value for the from and to
// block is "latest". If the fromBlock > toBlock an error is returned.
func (es *EventSystem) SubscribeLogs(crit quai.FilterQuery, logs chan []*types.Log) (*Subscription, error) {
	var from, to rpc.BlockNumber
	if crit.FromBlock == nil {
		from = rpc.LatestBlockNumber
	} else {
		from = rpc.BlockNumber(crit.FromBlock.Int64())
	}
	if crit.ToBlock == nil {
		to = rpc.LatestBlockNumber
	} else {
		to = rpc.BlockNumber(crit.ToBlock.Int64())
	}

	// Enforce max range of 10,000 blocks
	if from >= 0 && to >= 0 && to > from && to-from > MaxFilterRange {
		return nil, fmt.Errorf("invalid from and to block combination: block range > %d", MaxFilterRange)
	}

	// only interested in pending logs
	if from == rpc.PendingBlockNumber && to == rpc.PendingBlockNumber {
		return es.subscribePendingLogs(crit, logs), nil
	}
	// only interested in new mined logs
	if from == rpc.LatestBlockNumber && to == rpc.LatestBlockNumber {
		return es.subscribeLogs(crit, logs), nil
	}
	// only interested in mined logs within a specific block range
	if from >= 0 && to >= 0 && to >= from {
		return es.subscribeLogs(crit, logs), nil
	}
	// interested in mined logs from a specific block number, new logs and pending logs
	if from >= rpc.LatestBlockNumber && to == rpc.PendingBlockNumber {
		return es.subscribeMinedPendingLogs(crit, logs), nil
	}
	// interested in logs from a specific block number to new mined blocks
	if from >= 0 && to == rpc.LatestBlockNumber {
		return es.subscribeLogs(crit, logs), nil
	}
	return nil, fmt.Errorf("invalid from and to block combination: from > to")
}

// subscribeMinedPendingLogs creates a subscription that returned mined and
// pending logs that match the given criteria.
func (es *EventSystem) subscribeMinedPendingLogs(crit quai.FilterQuery, logs chan []*types.Log) *Subscription {
	sub := &subscription{
		id:        rpc.NewID(),
		typ:       MinedAndPendingLogsSubscription,
		logsCrit:  crit,
		created:   time.Now(),
		logs:      logs,
		hashes:    make(chan []common.Hash),
		headers:   make(chan *types.WorkObject),
		installed: make(chan struct{}),
		err:       make(chan error),
	}
	return es.subscribe(sub)
}

// subscribeLogs creates a subscription that will write all logs matching the
// given criteria to the given logs channel.
func (es *EventSystem) subscribeLogs(crit quai.FilterQuery, logs chan []*types.Log) *Subscription {
	sub := &subscription{
		id:        rpc.NewID(),
		typ:       LogsSubscription,
		logsCrit:  crit,
		created:   time.Now(),
		logs:      logs,
		hashes:    make(chan []common.Hash),
		headers:   make(chan *types.WorkObject),
		installed: make(chan struct{}),
		err:       make(chan error),
	}
	return es.subscribe(sub)
}

// subscribePendingLogs creates a subscription that writes transaction hashes for
// transactions that enter the transaction pool.
func (es *EventSystem) subscribePendingLogs(crit quai.FilterQuery, logs chan []*types.Log) *Subscription {
	sub := &subscription{
		id:        rpc.NewID(),
		typ:       PendingLogsSubscription,
		logsCrit:  crit,
		created:   time.Now(),
		logs:      logs,
		hashes:    make(chan []common.Hash),
		headers:   make(chan *types.WorkObject),
		installed: make(chan struct{}),
		err:       make(chan error),
	}
	return es.subscribe(sub)
}

// SubscribeNewHeads creates a subscription that writes the header of a block that is
// imported in the chain.
func (es *EventSystem) SubscribeNewHeads(headers chan *types.WorkObject) *Subscription {
	sub := &subscription{
		id:        rpc.NewID(),
		typ:       BlocksSubscription,
		created:   time.Now(),
		logs:      make(chan []*types.Log),
		hashes:    make(chan []common.Hash),
		headers:   headers,
		installed: make(chan struct{}),
		err:       make(chan error),
	}
	return es.subscribe(sub)
}

// SubscribeNewWorkshares creates a subscription that writes workshares received via P2P.
func (es *EventSystem) SubscribeNewWorkshares(workshares chan *types.WorkObject) *Subscription {
	sub := &subscription{
		id:         rpc.NewID(),
		typ:        WorkshareSubscription,
		created:    time.Now(),
		logs:       make(chan []*types.Log),
		hashes:     make(chan []common.Hash),
		headers:    make(chan *types.WorkObject),
		workshares: workshares,
		installed:  make(chan struct{}),
		err:        make(chan error),
	}
	return es.subscribe(sub)
}

// SubscribeUnlocks creates a subscription that writes the recently unlocked balances
func (es *EventSystem) SubscribeUnlocks(unlocks chan core.UnlocksEvent) *Subscription {
	sub := &subscription{
		id:        rpc.NewID(),
		typ:       UnlocksSubscription,
		created:   time.Now(),
		logs:      make(chan []*types.Log),
		hashes:    make(chan []common.Hash),
		installed: make(chan struct{}),
		err:       make(chan error),
		unlocks:   unlocks,
	}
	return es.subscribe(sub)
}

// SubscribeChainHeadEvent subscribes to the chain head feed
func (es *EventSystem) SubscribeChainHeadEvent(headers chan *types.WorkObject) *Subscription {
	sub := &subscription{
		id:        rpc.NewID(),
		typ:       ChainHeadSubscription,
		created:   time.Now(),
		logs:      make(chan []*types.Log),
		hashes:    make(chan []common.Hash),
		headers:   headers,
		installed: make(chan struct{}),
		err:       make(chan error),
	}
	return es.subscribe(sub)
}

// SubscribePendingTxs creates a subscription that writes transaction hashes for
// transactions that enter the transaction pool.
func (es *EventSystem) SubscribePendingTxs(hashes chan []common.Hash) *Subscription {
	sub := &subscription{
		id:        rpc.NewID(),
		typ:       PendingTransactionsSubscription,
		created:   time.Now(),
		logs:      make(chan []*types.Log),
		hashes:    hashes,
		headers:   make(chan *types.WorkObject),
		installed: make(chan struct{}),
		err:       make(chan error),
	}
	return es.subscribe(sub)
}

type filterIndex map[Type]map[rpc.ID]*subscription

func (es *EventSystem) handleLogs(filters filterIndex, ev []*types.Log) {
	if len(ev) == 0 {
		return
	}
	for _, f := range filters[LogsSubscription] {
		var addresses []common.Address
		if f.logsCrit.Addresses != nil {
			addresses = make([]common.Address, len(f.logsCrit.Addresses))
			for i, addr := range f.logsCrit.Addresses {
				addresses[i] = common.Bytes20ToAddress(addr, es.backend.NodeLocation())
			}
		}
		matchedLogs := filterLogs(ev, f.logsCrit.FromBlock, f.logsCrit.ToBlock, addresses, f.logsCrit.Topics)
		if len(matchedLogs) > 0 {
			select {
			case f.logs <- matchedLogs:
				es.backend.Logger().Error("Failed to deliver logs event to a subscriber")
			default:
			}
		}
	}
}

func (es *EventSystem) handlePendingLogs(filters filterIndex, ev []*types.Log) {
	if len(ev) == 0 {
		return
	}
	for _, f := range filters[PendingLogsSubscription] {
		var addresses []common.Address
		if f.logsCrit.Addresses != nil {
			addresses = make([]common.Address, len(f.logsCrit.Addresses))
			for i, addr := range f.logsCrit.Addresses {
				addresses[i] = common.Bytes20ToAddress(addr, es.backend.NodeLocation())
			}
		}
		matchedLogs := filterLogs(ev, nil, f.logsCrit.ToBlock, addresses, f.logsCrit.Topics)
		if len(matchedLogs) > 0 {
			select {
			case f.logs <- matchedLogs:
			default:
				es.backend.Logger().Error("Failed to deliver pending logs event to a subscriber")
			}
		}
	}
}

func (es *EventSystem) handleRemovedLogs(filters filterIndex, ev core.RemovedLogsEvent) {
	for _, f := range filters[LogsSubscription] {
		var addresses []common.Address
		if f.logsCrit.Addresses != nil {
			addresses = make([]common.Address, len(f.logsCrit.Addresses))
			for i, addr := range f.logsCrit.Addresses {
				addresses[i] = common.Bytes20ToAddress(addr, es.backend.NodeLocation())
			}
		}
		matchedLogs := filterLogs(ev.Logs, f.logsCrit.FromBlock, f.logsCrit.ToBlock, addresses, f.logsCrit.Topics)
		if len(matchedLogs) > 0 {
			select {
			case f.logs <- matchedLogs:
			default:
				es.backend.Logger().Error("Failed to deliver removed logs event to a subscriber")
			}
		}
	}
}

func (es *EventSystem) handleTxsEvent(filters filterIndex, ev core.NewTxsEvent) {
	hashes := make([]common.Hash, 0, len(ev.Txs))
	for _, tx := range ev.Txs {
		hashes = append(hashes, tx.Hash())
	}
	for _, f := range filters[PendingTransactionsSubscription] {
		select {
		case f.hashes <- hashes:
		default:
			es.backend.Logger().Error("Failed to deliver txs event to a subscriber")
		}
	}
}

func (es *EventSystem) handleChainEvent(filters filterIndex, ev core.ChainEvent) {
	for _, f := range filters[BlocksSubscription] {
		select {
		case f.headers <- ev.Block:
		default:
			es.backend.Logger().Error("Failed to deliver chain event to a subscriber")
		}
	}
}

func (es *EventSystem) handleUnlocksEvent(filters filterIndex, ev core.UnlocksEvent) {
	for _, f := range filters[UnlocksSubscription] {
		select {
		case f.unlocks <- ev:
		default:
			es.backend.Logger().Error("Failed to deliver unlocks event to a subscriber")
		}
	}
}

func (es *EventSystem) handleChainHeadEvent(filters filterIndex, ev core.ChainHeadEvent) {
	for _, f := range filters[ChainHeadSubscription] {
		select {
		case f.headers <- ev.Block:
		default:
			es.backend.Logger().Error("Failed to deliver head feed event to a subscriber")
		}
	}
}

func (es *EventSystem) handleWorkshareEvent(filters filterIndex, ev core.NewWorkshareEvent) {
	for _, f := range filters[WorkshareSubscription] {
		select {
		case f.workshares <- ev.Workshare:
		default:
			es.backend.Logger().Error("Failed to deliver workshare event to a subscriber")
		}
	}
}

// eventLoop (un)installs filters and processes mux events.
func (es *EventSystem) eventLoop() {
	defer func() {
		if r := recover(); r != nil {
			es.backend.Logger().WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	nodeCtx := es.backend.NodeCtx()
	// Ensure all subscriptions get cleaned up
	defer func() {
		if nodeCtx == common.ZONE_CTX && es.backend.ProcessingState() {
			es.logsSub.Unsubscribe()
			es.rmLogsSub.Unsubscribe()
			es.pendingLogsSub.Unsubscribe()
			es.unlocksSub.Unsubscribe()
		}
		es.chainSub.Unsubscribe()
		es.chainHeadSub.Unsubscribe()
		es.workshareSub.Unsubscribe()

	}()

	index := make(filterIndex)
	for i := UnknownSubscription; i < LastIndexSubscription; i++ {
		index[i] = make(map[rpc.ID]*subscription)
	}

	if nodeCtx == common.ZONE_CTX && es.backend.ProcessingState() {
		for {
			select {
			case ev := <-es.chainCh:
				es.handleChainEvent(index, ev)
			case ev := <-es.chainHeadCh:
				es.handleChainHeadEvent(index, ev)
			case ev := <-es.txsCh:
				es.handleTxsEvent(index, ev)
			case ev := <-es.logsCh:
				es.handleLogs(index, ev)
			case ev := <-es.rmLogsCh:
				es.handleRemovedLogs(index, ev)
			case ev := <-es.pendingLogsCh:
				es.handlePendingLogs(index, ev)
			case ev := <-es.unlocksCh:
				es.handleUnlocksEvent(index, ev)
			case ev := <-es.workshareCh:
				es.handleWorkshareEvent(index, ev)
			case f := <-es.install:
				index[f.typ][f.id] = f
				close(f.installed)

			case f := <-es.uninstall:
				delete(index[f.typ], f.id)
				close(f.err)
			// System stopped
			case <-es.logsSub.Err():
				return
			case <-es.rmLogsSub.Err():
				return
			case <-es.pendingLogsSub.Err():
				return
			case <-es.chainSub.Err():
				return
			case <-es.chainHeadSub.Err():
				return
			case <-es.workshareSub.Err():
				return
			}
		}
	} else {
		for {
			select {
			case ev := <-es.chainCh:
				es.handleChainEvent(index, ev)

			case ev := <-es.chainHeadCh:
				es.handleChainHeadEvent(index, ev)

			case ev := <-es.workshareCh:
				es.handleWorkshareEvent(index, ev)

			case f := <-es.install:
				index[f.typ][f.id] = f
				close(f.installed)

			case f := <-es.uninstall:
				delete(index[f.typ], f.id)
				close(f.err)

			case <-es.chainSub.Err():
				return
			case <-es.chainHeadSub.Err():
				return
			case <-es.workshareSub.Err():
				return
			}
		}
	}
}
