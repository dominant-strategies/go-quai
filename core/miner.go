// Copyright 2014 The go-ethereum Authors
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

// Package miner implements Quai block creation and mining.
package core

import (
	"fmt"
	"runtime"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
)

// Miner creates blocks and searches for proof-of-work values.
type Miner struct {
	worker  *worker
	hc      *HeaderChain
	engines []consensus.Engine
	startCh chan []common.Address
	logger  *log.Logger
}

func New(hc *HeaderChain, txPool *TxPool, config *Config, db ethdb.Database, chainConfig *params.ChainConfig, engines []consensus.Engine, processingState bool, logger *log.Logger) *Miner {
	miner := &Miner{
		hc:      hc,
		engines: engines,
		startCh: make(chan []common.Address, 2),
		worker:  newWorker(config, chainConfig, db, engines, hc, txPool, true, processingState, logger),
		logger:  logger,
	}

	miner.SetExtra(miner.MakeExtraData(config.ExtraData))

	return miner
}

func (miner *Miner) Stop() {
	miner.worker.stop()
}

func (miner *Miner) Mining() bool {
	return miner.worker.isRunning()
}

func (miner *Miner) StopMining() {
	// Update the thread count within the consensus engine
	type threaded interface {
		SetThreads(threads int)
	}
	// Use the first engine for thread configuration
	if len(miner.engines) > 0 {
		if th, ok := miner.engines[0].(threaded); ok {
			th.SetThreads(-1)
		}
	}
	// Stop the block creating itself
	miner.Stop()
}

func (miner *Miner) SetExtra(extra []byte) error {
	if uint64(len(extra)) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra exceeds max length. %d > %v", len(extra), params.MaximumExtraDataSize)
	}
	miner.worker.setExtra(extra)
	return nil
}

func (miner *Miner) MakeExtraData(extra []byte) []byte {
	if len(extra) == 0 {
		// create default extradata
		extra, _ = rlp.EncodeToBytes([]interface{}{
			params.Version.Short(),
			runtime.Version(),
			runtime.GOOS,
		})
	}
	if uint64(len(extra)) > params.MaximumExtraDataSize {
		miner.logger.WithFields(log.Fields{
			"extra": hexutil.Bytes(extra),
			"limit": params.MaximumExtraDataSize,
		}).Warn("Miner extra data exceed limit")
		extra = nil
	}
	return extra
}

// SetRecommitInterval sets the interval for sealing work resubmitting.
func (miner *Miner) SetRecommitInterval(interval time.Duration) {
	miner.worker.setRecommitInterval(interval)
}

// Pending returns the currently pending block and associated state.
func (miner *Miner) Pending() *types.WorkObject {
	return miner.worker.pending()
}

// PendingBlock returns the currently pending block.
//
// Note, to access both the pending block and the pending state
// simultaneously, please use Pending(), as the pending state can
// change between multiple method calls
func (miner *Miner) PendingBlock() *types.WorkObject {
	return miner.worker.pendingBlock()
}

// PendingBlockAndReceipts returns the currently pending block and corresponding receipts.
func (miner *Miner) PendingBlockAndReceipts() (*types.WorkObject, types.Receipts) {
	return miner.worker.pendingBlockAndReceipts()
}

func (miner *Miner) SetPrimaryCoinbase(addr common.Address) {
	miner.worker.setPrimaryCoinbase(addr)
}

// SetGasCeil sets the gaslimit to strive for when mining blocks.
func (miner *Miner) SetGasCeil(ceil uint64) {
	miner.worker.setGasCeil(ceil)
}

// EnablePreseal turns on the preseal mining feature. It's enabled by default.
// Note this function shouldn't be exposed to API, it's unnecessary for users
// (miners) to actually know the underlying detail. It's only for outside project
// which uses this library.
func (miner *Miner) EnablePreseal() {
	miner.worker.enablePreseal()
}

// DisablePreseal turns off the preseal mining feature. It's necessary for some
// fake consensus engine which can seal blocks instantaneously.
// Note this function shouldn't be exposed to API, it's unnecessary for users
// (miners) to actually know the underlying detail. It's only for outside project
// which uses this library.
func (miner *Miner) DisablePreseal() {
	miner.worker.disablePreseal()
}

// SubscribePendingLogs starts delivering logs from pending transactions
// to the given channel.
func (miner *Miner) SubscribePendingLogs(ch chan<- []*types.Log) event.Subscription {
	return miner.worker.pendingLogsFeed.Subscribe(ch)
}

// SubscribePendingBlock starts delivering the pending block to the given channel.
func (miner *Miner) SubscribePendingHeader(ch chan<- *types.WorkObject) event.Subscription {
	return miner.worker.pendingHeaderFeed.Subscribe(ch)
}
