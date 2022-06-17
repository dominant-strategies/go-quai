// Copyright 2015 The go-ethereum Authors
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

// Package filters implements an ethereum filtering system for block,
// transactions and log events.
package filters

import (
	"context"
	"fmt"
	"sync"
	"time"

	ethereum "github.com/spruce-solutions/go-quai"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/core"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/event"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/rpc"
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
	// PendingBlockSubscription queries for pending blocks
	PendingBlockSubscription
	// MinedAndPendingLogsSubscription queries for logs in mined and pending blocks.
	MinedAndPendingLogsSubscription
	// PendingTransactionsSubscription queries tx hashes for pending
	// transactions entering the pending state
	PendingTransactionsSubscription
	// BlocksSubscription queries hashes for blocks that are imported
	BlocksSubscription
	// LastSubscription keeps track of the last index
	LastIndexSubscription
	// ReOrg subscription sends in the data from the reorg event
	ReOrgSubscription
	// MissingExternalBlock subscription sends in the data from the missingExternalBlock event
	MissingExternalBlockSubscription
	// uncleChSubscription writes the header of a block that is notified as an uncle.
	uncleChSubscription
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
	chainEvChanSize = 10
	// chainuncleChanSize is the size of channel listening to ChainuncleEvent.
	chainuncleChanSize = 1
)

type subscription struct {
	id              rpc.ID
	typ             Type
	created         time.Time
	logsCrit        ethereum.FilterQuery
	logs            chan []*types.Log
	hashes          chan []common.Hash
	headers         chan *types.Header
	block           chan *types.Header
	installed       chan struct{} // closed when the filter is installed
	err             chan error    // closed when the filter is uninstalled
	reOrg           chan core.ReOrgRollup
	uncleEvent      chan *types.Header
	missingExtBlock chan core.MissingExternalBlock
}

// EventSystem creates subscriptions, processes events and broadcasts them to the
// subscription which match the subscription criteria.
type EventSystem struct {
	backend   Backend
	lightMode bool
	lastHead  *types.Header

	// Subscriptions
	txsSub                  event.Subscription // Subscription for new transaction event
	logsSub                 event.Subscription // Subscription for new log event
	rmLogsSub               event.Subscription // Subscription for removed log event
	pendingLogsSub          event.Subscription // Subscription for pending log event
	pendingBlockSub         event.Subscription // Subscription for pending block event
	chainSub                event.Subscription // Subscription for new chain event
	reOrgSub                event.Subscription // Subscription for reorg event
	uncleChSub              event.Subscription // Subscription for side chain event
	missingExternalBlockSub event.Subscription // Subscription for missingBlock event

	// Channels
	install                chan *subscription             // install filter for event notification
	uninstall              chan *subscription             // remove filter for event notification
	txsCh                  chan core.NewTxsEvent          // Channel to receive new transactions event
	logsCh                 chan []*types.Log              // Channel to receive new log event
	pendingLogsCh          chan []*types.Log              // Channel to receive new log event
	pendingBlockCh         chan *types.Header             // Channel to receive new pending block event
	rmLogsCh               chan core.RemovedLogsEvent     // Channel to receive removed log event
	chainCh                chan core.ChainEvent           // Channel to receive new chain event
	reOrgCh                chan core.ReOrgRollup          // Channel to receive reorg event data
	uncleCh                chan *types.Header             // Channel to receive side chain event data
	missingExternalBlockCh chan core.MissingExternalBlock // Channel to receive the missing external block event
}

// NewEventSystem creates a new manager that listens for event on the given mux,
// parses and filters them. It uses the all map to retrieve filter changes. The
// work loop holds its own index that is used to forward events to filters.
//
// The returned manager has a loop that needs to be stopped with the Stop function
// or by stopping the given mux.
func NewEventSystem(backend Backend, lightMode bool) *EventSystem {
	m := &EventSystem{
		backend:                backend,
		lightMode:              lightMode,
		install:                make(chan *subscription),
		uninstall:              make(chan *subscription),
		txsCh:                  make(chan core.NewTxsEvent, txChanSize),
		logsCh:                 make(chan []*types.Log, logsChanSize),
		rmLogsCh:               make(chan core.RemovedLogsEvent, rmLogsChanSize),
		pendingLogsCh:          make(chan []*types.Log, logsChanSize),
		pendingBlockCh:         make(chan *types.Header),
		chainCh:                make(chan core.ChainEvent, chainEvChanSize),
		reOrgCh:                make(chan core.ReOrgRollup),
		uncleCh:                make(chan *types.Header),
		missingExternalBlockCh: make(chan core.MissingExternalBlock),
	}

	// Subscribe events
	m.txsSub = m.backend.SubscribeNewTxsEvent(m.txsCh)
	m.logsSub = m.backend.SubscribeLogsEvent(m.logsCh)
	m.rmLogsSub = m.backend.SubscribeRemovedLogsEvent(m.rmLogsCh)
	m.chainSub = m.backend.SubscribeChainEvent(m.chainCh)
	m.pendingLogsSub = m.backend.SubscribePendingLogsEvent(m.pendingLogsCh)
	m.pendingBlockSub = m.backend.SubscribePendingBlockEvent(m.pendingBlockCh)
	m.reOrgSub = m.backend.SubscribeReOrgEvent(m.reOrgCh)
	m.missingExternalBlockSub = m.backend.SubscribeMissingExternalBlockEvent(m.missingExternalBlockCh)
	m.uncleChSub = m.backend.SubscribeChainUncleEvent(m.uncleCh)

	// Make sure none of the subscriptions are empty
	if m.txsSub == nil || m.logsSub == nil || m.rmLogsSub == nil || m.chainSub == nil || m.pendingLogsSub == nil || m.reOrgSub == nil || m.uncleChSub == nil {
		log.Crit("Subscribe for event system failed")
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
func (es *EventSystem) SubscribeLogs(crit ethereum.FilterQuery, logs chan []*types.Log) (*Subscription, error) {
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
func (es *EventSystem) subscribeMinedPendingLogs(crit ethereum.FilterQuery, logs chan []*types.Log) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             MinedAndPendingLogsSubscription,
		logsCrit:        crit,
		created:         time.Now(),
		logs:            logs,
		hashes:          make(chan []common.Hash),
		headers:         make(chan *types.Header),
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           make(chan core.ReOrgRollup),
		uncleEvent:      make(chan *types.Header),
		missingExtBlock: make(chan core.MissingExternalBlock),
	}
	return es.subscribe(sub)
}

// subscribeLogs creates a subscription that will write all logs matching the
// given criteria to the given logs channel.
func (es *EventSystem) subscribeLogs(crit ethereum.FilterQuery, logs chan []*types.Log) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             LogsSubscription,
		logsCrit:        crit,
		created:         time.Now(),
		logs:            logs,
		hashes:          make(chan []common.Hash),
		headers:         make(chan *types.Header),
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           make(chan core.ReOrgRollup),
		uncleEvent:      make(chan *types.Header),
		missingExtBlock: make(chan core.MissingExternalBlock),
	}
	return es.subscribe(sub)
}

// subscribePendingLogs creates a subscription that writes transaction hashes for
// transactions that enter the transaction pool.
func (es *EventSystem) subscribePendingLogs(crit ethereum.FilterQuery, logs chan []*types.Log) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             PendingLogsSubscription,
		logsCrit:        crit,
		created:         time.Now(),
		logs:            logs,
		hashes:          make(chan []common.Hash),
		headers:         make(chan *types.Header),
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           make(chan core.ReOrgRollup),
		uncleEvent:      make(chan *types.Header),
		missingExtBlock: make(chan core.MissingExternalBlock),
	}
	return es.subscribe(sub)
}

// SubscribePendingBlock creates a subscription that writes pending block that are created in the miner.
func (es *EventSystem) SubscribePendingBlock(block chan *types.Header) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             PendingBlockSubscription,
		created:         time.Now(),
		logs:            make(chan []*types.Log),
		hashes:          make(chan []common.Hash),
		headers:         make(chan *types.Header),
		block:           block,
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           make(chan core.ReOrgRollup),
		uncleEvent:      make(chan *types.Header),
		missingExtBlock: make(chan core.MissingExternalBlock),
	}
	return es.subscribe(sub)
}

// SubscribeNewHeads creates a subscription that writes the header of a block that is
// imported in the chain.
func (es *EventSystem) SubscribeNewHeads(headers chan *types.Header) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             BlocksSubscription,
		created:         time.Now(),
		logs:            make(chan []*types.Log),
		hashes:          make(chan []common.Hash),
		headers:         headers,
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           make(chan core.ReOrgRollup),
		uncleEvent:      make(chan *types.Header),
		missingExtBlock: make(chan core.MissingExternalBlock),
	}
	return es.subscribe(sub)
}

// SubscribePendingTxs creates a subscription that writes transaction hashes for
// transactions that enter the transaction pool.
func (es *EventSystem) SubscribePendingTxs(hashes chan []common.Hash) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             PendingTransactionsSubscription,
		created:         time.Now(),
		logs:            make(chan []*types.Log),
		hashes:          hashes,
		headers:         make(chan *types.Header),
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           make(chan core.ReOrgRollup),
		uncleEvent:      make(chan *types.Header),
		missingExtBlock: make(chan core.MissingExternalBlock),
	}
	return es.subscribe(sub)
}

// SubscribeReOrg creates a subscription that write the header and array of headers
// of the newChain from a newly found chain.
func (es *EventSystem) SubscribeReOrg(reOrg chan core.ReOrgRollup) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             ReOrgSubscription,
		created:         time.Now(),
		logs:            make(chan []*types.Log),
		hashes:          make(chan []common.Hash),
		headers:         make(chan *types.Header),
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           reOrg,
		uncleEvent:      make(chan *types.Header),
		missingExtBlock: make(chan core.MissingExternalBlock),
	}
	return es.subscribe(sub)
}

// SubscribeChainUncleEvent creates a subscription that writes the header of a block that is
// notified as an uncle.
func (es *EventSystem) SubscribeChainUncleEvent(uncleEvent chan *types.Header) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             uncleChSubscription,
		created:         time.Now(),
		logs:            make(chan []*types.Log),
		hashes:          make(chan []common.Hash),
		headers:         make(chan *types.Header),
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           make(chan core.ReOrgRollup),
		uncleEvent:      uncleEvent,
		missingExtBlock: make(chan core.MissingExternalBlock),
	}
	return es.subscribe(sub)
}

// SubscribeNewHeads creates a subscription that writes the header of a block that is
// imported in the chain.
func (es *EventSystem) SubscribeMissingExternalBlock(extBlockMiss chan core.MissingExternalBlock) *Subscription {
	sub := &subscription{
		id:              rpc.NewID(),
		typ:             MissingExternalBlockSubscription,
		created:         time.Now(),
		logs:            make(chan []*types.Log),
		hashes:          make(chan []common.Hash),
		headers:         make(chan *types.Header),
		installed:       make(chan struct{}),
		err:             make(chan error),
		reOrg:           make(chan core.ReOrgRollup),
		missingExtBlock: extBlockMiss,
	}
	return es.subscribe(sub)
}

type filterIndex map[Type]map[rpc.ID]*subscription

func (es *EventSystem) handleLogs(filters filterIndex, ev []*types.Log) {
	if len(ev) == 0 {
		return
	}
	for _, f := range filters[LogsSubscription] {
		matchedLogs := filterLogs(ev, f.logsCrit.FromBlock, f.logsCrit.ToBlock, f.logsCrit.Addresses, f.logsCrit.Topics)
		if len(matchedLogs) > 0 {
			f.logs <- matchedLogs
		}
	}
}

func (es *EventSystem) handlePendingLogs(filters filterIndex, ev []*types.Log) {
	if len(ev) == 0 {
		return
	}
	for _, f := range filters[PendingLogsSubscription] {
		matchedLogs := filterLogs(ev, nil, f.logsCrit.ToBlock, f.logsCrit.Addresses, f.logsCrit.Topics)
		if len(matchedLogs) > 0 {
			f.logs <- matchedLogs
		}
	}
}

func (es *EventSystem) handlePendingBlock(filters filterIndex, ev *types.Header) {
	for _, f := range filters[PendingBlockSubscription] {
		f.block <- ev
	}
}

func (es *EventSystem) handleReOrg(filters filterIndex, ev core.ReOrgRollup) {
	for _, f := range filters[ReOrgSubscription] {
		f.reOrg <- ev
	}
}

func (es *EventSystem) handleMissingExternalBlock(filters filterIndex, ev core.MissingExternalBlock) {
	for _, f := range filters[MissingExternalBlockSubscription] {
		f.missingExtBlock <- ev
	}
}
func (es *EventSystem) handleUncleCh(filters filterIndex, ev *types.Header) {
	for _, f := range filters[uncleChSubscription] {
		f.uncleEvent <- ev
	}
}

func (es *EventSystem) handleRemovedLogs(filters filterIndex, ev core.RemovedLogsEvent) {
	for _, f := range filters[LogsSubscription] {
		matchedLogs := filterLogs(ev.Logs, f.logsCrit.FromBlock, f.logsCrit.ToBlock, f.logsCrit.Addresses, f.logsCrit.Topics)
		if len(matchedLogs) > 0 {
			f.logs <- matchedLogs
		}
	}
}

func (es *EventSystem) handleTxsEvent(filters filterIndex, ev core.NewTxsEvent) {
	hashes := make([]common.Hash, 0, len(ev.Txs))
	for _, tx := range ev.Txs {
		hashes = append(hashes, tx.Hash())
	}
	for _, f := range filters[PendingTransactionsSubscription] {
		f.hashes <- hashes
	}
}

func (es *EventSystem) handleChainEvent(filters filterIndex, ev core.ChainEvent) {
	for _, f := range filters[BlocksSubscription] {
		f.headers <- ev.Block.Header()
	}
	if es.lightMode && len(filters[LogsSubscription]) > 0 {
		es.lightFilterNewHead(ev.Block.Header(), func(header *types.Header, remove bool) {
			for _, f := range filters[LogsSubscription] {
				if matchedLogs := es.lightFilterLogs(header, f.logsCrit.Addresses, f.logsCrit.Topics, remove); len(matchedLogs) > 0 {
					f.logs <- matchedLogs
				}
			}
		})
	}
}

func (es *EventSystem) lightFilterNewHead(newHeader *types.Header, callBack func(*types.Header, bool)) {
	oldh := es.lastHead
	es.lastHead = newHeader
	if oldh == nil {
		return
	}
	newh := newHeader
	// find common ancestor, create list of rolled back and new block hashes
	var oldHeaders, newHeaders []*types.Header
	for oldh.Hash() != newh.Hash() {
		if oldh.Number[types.QuaiNetworkContext].Uint64() >= newh.Number[types.QuaiNetworkContext].Uint64() {
			oldHeaders = append(oldHeaders, oldh)
			oldh = rawdb.ReadHeader(es.backend.ChainDb(), oldh.ParentHash[types.QuaiNetworkContext], oldh.Number[types.QuaiNetworkContext].Uint64()-1)
		}
		if oldh.Number[types.QuaiNetworkContext].Uint64() < newh.Number[types.QuaiNetworkContext].Uint64() {
			newHeaders = append(newHeaders, newh)
			newh = rawdb.ReadHeader(es.backend.ChainDb(), newh.ParentHash[types.QuaiNetworkContext], newh.Number[types.QuaiNetworkContext].Uint64()-1)
			if newh == nil {
				// happens when CHT syncing, nothing to do
				newh = oldh
			}
		}
	}
	// roll back old blocks
	for _, h := range oldHeaders {
		callBack(h, true)
	}
	// check new blocks (array is in reverse order)
	for i := len(newHeaders) - 1; i >= 0; i-- {
		callBack(newHeaders[i], false)
	}
}

// filter logs of a single header in light client mode
func (es *EventSystem) lightFilterLogs(header *types.Header, addresses []common.Address, topics [][]common.Hash, remove bool) []*types.Log {
	if bloomFilter(header.Bloom[types.QuaiNetworkContext], addresses, topics) {
		// Get the logs of the block
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		logsList, err := es.backend.GetLogs(ctx, header.Hash())
		if err != nil {
			return nil
		}
		var unfiltered []*types.Log
		for _, logs := range logsList {
			for _, log := range logs {
				logcopy := *log
				logcopy.Removed = remove
				unfiltered = append(unfiltered, &logcopy)
			}
		}
		logs := filterLogs(unfiltered, nil, nil, addresses, topics)
		if len(logs) > 0 && logs[0].TxHash == (common.Hash{}) {
			// We have matching but non-derived logs
			receipts, err := es.backend.GetReceipts(ctx, header.Hash())
			if err != nil {
				return nil
			}
			unfiltered = unfiltered[:0]
			for _, receipt := range receipts {
				for _, log := range receipt.Logs {
					logcopy := *log
					logcopy.Removed = remove
					unfiltered = append(unfiltered, &logcopy)
				}
			}
			logs = filterLogs(unfiltered, nil, nil, addresses, topics)
		}
		return logs
	}
	return nil
}

// eventLoop (un)installs filters and processes mux events.
func (es *EventSystem) eventLoop() {
	// Ensure all subscriptions get cleaned up
	defer func() {
		es.txsSub.Unsubscribe()
		es.logsSub.Unsubscribe()
		es.rmLogsSub.Unsubscribe()
		es.pendingLogsSub.Unsubscribe()
		es.pendingBlockSub.Unsubscribe()
		es.chainSub.Unsubscribe()
		es.reOrgSub.Unsubscribe()
		es.missingExternalBlockSub.Unsubscribe()
		es.uncleChSub.Unsubscribe()
	}()

	index := make(filterIndex)
	for i := UnknownSubscription; i <= uncleChSubscription; i++ {
		index[i] = make(map[rpc.ID]*subscription)
	}

	for {
		select {
		case ev := <-es.txsCh:
			es.handleTxsEvent(index, ev)
		case ev := <-es.logsCh:
			es.handleLogs(index, ev)
		case ev := <-es.rmLogsCh:
			es.handleRemovedLogs(index, ev)
		case ev := <-es.pendingLogsCh:
			es.handlePendingLogs(index, ev)
		case ev := <-es.pendingBlockCh:
			es.handlePendingBlock(index, ev)
		case ev := <-es.chainCh:
			es.handleChainEvent(index, ev)
		case ev := <-es.reOrgCh:
			es.handleReOrg(index, ev)
		case ev := <-es.missingExternalBlockCh:
			es.handleMissingExternalBlock(index, ev)
		case ev := <-es.uncleCh:
			es.handleUncleCh(index, ev)

		case f := <-es.install:
			if f.typ == MinedAndPendingLogsSubscription {
				// the type are logs and pending logs subscriptions
				index[LogsSubscription][f.id] = f
				index[PendingLogsSubscription][f.id] = f
			} else {
				index[f.typ][f.id] = f
			}
			close(f.installed)

		case f := <-es.uninstall:
			if f.typ == MinedAndPendingLogsSubscription {
				// the type are logs and pending logs subscriptions
				delete(index[LogsSubscription], f.id)
				delete(index[PendingLogsSubscription], f.id)
			} else {
				delete(index[f.typ], f.id)
			}
			close(f.err)

		// System stopped
		case <-es.txsSub.Err():
			return
		case <-es.logsSub.Err():
			return
		case <-es.rmLogsSub.Err():
			return
		case <-es.chainSub.Err():
			return
		case <-es.reOrgSub.Err():
			return
		case <-es.uncleChSub.Err():
			return
		case <-es.missingExternalBlockSub.Err():
			return
		}
	}
}
