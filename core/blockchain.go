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

// Package core implements the Ethereum consensus protocol.
package core

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/event"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/metrics"
	"github.com/spruce-solutions/go-quai/params"
)

const (
	maxHeadsQueueLimit = 1024
)

type List struct {
}

var (
	blockInsertTimer     = metrics.NewRegisteredTimer("chain/inserts", nil)
	blockValidationTimer = metrics.NewRegisteredTimer("chain/validation", nil)
	blockExecutionTimer  = metrics.NewRegisteredTimer("chain/execution", nil)
	blockWriteTimer      = metrics.NewRegisteredTimer("chain/write", nil)

	errInsertionInterrupted = errors.New("insertion is interrupted")
	errChainStopped         = errors.New("blockchain is stopped")
)

// BlockChain represents the canonical chain given a database with a genesis
// block. The Blockchain manages chain imports, reverts, chain reorganisations.
//
// Importing blocks in to the block chain happens according to the set of rules
// defined by the two stage Validator. Processing of blocks is done using the
// Processor which processes the included transaction. The validation of the state
// is done in the second part of the Validator. Failing results in aborting of
// the import.
//
// The BlockChain also helps in returning blocks from **any** chain included
// in the database as well as blocks that represents the canonical chain. It's
// important to note that GetBlock can return any block and does not need to be
// included in the canonical one where as GetBlockByNumber always represents the
// canonical chain.
type BlockChain struct {
	chainConfig *params.ChainConfig // Chain & network configuration
	cacheConfig *CacheConfig        // Cache configuration for pruning

	db ethdb.Database // Low level persistent database to store final content in

	chainFeed     event.Feed
	blockProcFeed event.Feed
	scope         event.SubscriptionScope
	genesisBlock  *types.Block

	chainmu       sync.RWMutex    // blockchain insertion lock
	futureBlocks  *lru.Cache      // future blocks are blocks added for later processing
	heads         []*types.Header // heads are tips of blockchain branches
	quit          chan struct{}   // blockchain quit channel
	wg            sync.WaitGroup  // chain processing wait group for shutting down
	running       int32           // 0 if chain is running, 1 when stopped
	procInterrupt int32           // interrupt signaler for block processing
	processor     Processor       // Block transaction processor interface
}

// NewBlockChain returns a fully initialised block chain using information
// available in the database. It initialises the default Ethereum Validator and
// Processor.
func NewBlockChain(db ethdb.Database, cacheConfig *CacheConfig, chainConfig *params.ChainConfig, engine consensus.Engine, vmConfig vm.Config) (*BlockChain, error) {
	if cacheConfig == nil {
		cacheConfig = defaultCacheConfig
	}

	futureBlocks, _ := lru.New(maxFutureBlocks)

	bc := &BlockChain{
		chainConfig: chainConfig,
		cacheConfig: cacheConfig,
		db:          db,
		quit:        make(chan struct{}),
	}

	bc.processor = NewStateProcessor(chainConfig, bc, engine)

	bc.genesisBlock = bc.GetBlockByNumber(0)
	if bc.genesisBlock == nil {
		return nil, ErrNoGenesis
	}

	heads := make([]*types.Header, maxHeadsQueueLimit)
	bc.heads = heads

	if err := bc.loadLastState(); err != nil {
		return nil, err
	}

	return bc, nil
}

// loadLastState loads the last known chain state from the database. This method
// assumes that the chain manager mutex is held.
func (bc *BlockChain) loadLastState() error {
	// TODO: create function to find highest block number and fill Head FIFO
	headsHashes := rawdb.ReadHeadsHashes(bc.db)

	heads := make([]*types.Header, maxHeadsQueueLimit)
	for i, hash := range headsHashes {
		heads[i] = bc.GetHeaderByHash(hash)
	}
	bc.heads = heads

	return nil
}

// Append
func (bc *BlockChain) Append(block *types.Block) error {
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	// Process our block and retrieve external blocks.
	_, _, _, err := bc.processor.Process(block)
	if err != nil {
		bc.futureBlocks.Remove(block.Hash())
		return err
	}

	batch := bc.db.NewBatch()
	rawdb.WriteBlock(batch, block)
	if err := batch.Write(); err != nil {
		log.Crit("Failed to write block into disk", "err", err)
	}

	var nilHeader *types.Header
	// check if the size of the queue is at the maxHeadsQueueLimit
	if len(bc.heads) == maxHeadsQueueLimit {

		// Trim the branch before dequeueing
		err = bc.trimBranch(bc.heads[0], bc.heads[maxHeadsQueueLimit-1])
		if err != nil {
			return err
		}

		// dequeue
		bc.heads[0] = nilHeader
		bc.heads = bc.heads[1:]
	}
	// Add to the heads queue
	bc.heads = append(bc.heads, block.Header())

	// Sort the heads by number
	sort.Slice(bc.heads, func(i, j int) bool {
		return bc.heads[i].Number[types.QuaiNetworkContext].Uint64() < bc.heads[j].Number[types.QuaiNetworkContext].Uint64()
	})

	return nil
}

// Trim
func (bc *BlockChain) trim(commonBlock *types.Block, startBlock *types.Block) error {
	parent := startBlock
	// Delete each block unitl common is found
	for {
		if parent.Hash() == commonBlock.Hash() {
			break
		}
		rawdb.DeleteBlock(bc.db, parent.Hash(), parent.Header().Number[types.QuaiNetworkContext].Uint64())
		parent = bc.GetBlockByHash(parent.Header().ParentHash[types.QuaiNetworkContext])

		if parent == nil {
			log.Warn("unable to trim blockchain state, one of trimmed blocks not found")
			return nil
		}
	}
	return nil
}

// TrimBranch
func (bc *BlockChain) trimBranch(oldHeader *types.Header, newHeader *types.Header) error {
	startIndex := oldHeader.Number64()
	startBlock := bc.GetBlock(oldHeader.Hash(), oldHeader.Number64())

	newBlock := bc.GetBlock(newHeader.Parent(), startIndex)
	newHeader = newBlock.Header()
	var commonBlock *types.Block

	// Both sides of the reorg are at the same number, reduce both until the common
	// ancestor is found
	for {
		// If the common ancestor was found, bail out
		if oldHeader.Hash() == newHeader.Hash() {
			commonBlock = bc.GetBlock(oldHeader.Hash(), oldHeader.Number64())
			break
		}

		// Step back with both chains
		oldBlock := bc.GetBlock(oldHeader.Parent(), oldHeader.Number64()-1)
		if oldHeader == nil {
			return fmt.Errorf("invalid old chain")
		}
		oldHeader = oldBlock.Header()

		newBlock := bc.GetBlock(newHeader.Parent(), newHeader.Number64()-1)
		if newBlock == nil {
			return fmt.Errorf("invalid new chain")
		}
		newHeader = newBlock.Header()

	}
	err := bc.trim(commonBlock, startBlock)

	return err
}

// Reset purges the entire blockchain, restoring it to its genesis state.
func (bc *BlockChain) Reset() error {
	return bc.ResetWithGenesisBlock(bc.genesisBlock)
}

// ResetWithGenesisBlock purges the entire blockchain, restoring it to the
// specified genesis state.
func (bc *BlockChain) ResetWithGenesisBlock(genesis *types.Block) error {

	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	//Iterate through my heads and trim each back to genesis
	for _, head := range bc.heads {
		bc.trim(bc.genesisBlock, bc.GetBlock(head.Hash(), head.Number64()))
	}

	return nil
}

// Export writes the active chain to the given writer.
func (bc *BlockChain) Export(w io.Writer) error {
	return bc.ExportN(w, uint64(0), bc.CurrentBlock().NumberU64())
}

// ExportN writes a subset of the active chain to the given writer.
func (bc *BlockChain) ExportN(w io.Writer, first uint64, last uint64) error {
	bc.chainmu.RLock()
	defer bc.chainmu.RUnlock()

	if first > last {
		return fmt.Errorf("export failed: first (%d) is greater than last (%d)", first, last)
	}
	log.Info("Exporting batch of blocks", "count", last-first+1)

	start, reported := time.Now(), time.Now()
	for nr := first; nr <= last; nr++ {
		block := bc.GetBlockByNumber(nr)
		if block == nil {
			return fmt.Errorf("export failed on #%d: not found", nr)
		}
		if err := block.EncodeRLP(w); err != nil {
			return err
		}
		if time.Since(reported) >= statsReportLimit {
			log.Info("Exporting blocks", "exported", block.NumberU64()-first, "elapsed", common.PrettyDuration(time.Since(start)))
			reported = time.Now()
		}
	}
	return nil
}

// Genesis retrieves the chain's genesis block.
func (bc *BlockChain) Genesis() *types.Block {
	return bc.genesisBlock
}

// HasBlock checks if a block is fully present in the database or not.
func (bc *BlockChain) HasBlock(hash common.Hash, number uint64) bool {
	if bc.blockCache.Contains(hash) {
		return true
	}
	return rawdb.HasBody(bc.db, hash, number)
}

// GetBlock retrieves a block from the database by hash and number,
// caching it if found.
func (bc *BlockChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	// Short circuit if the block's already in the cache, retrieve otherwise
	if block, ok := bc.blockCache.Get(hash); ok {
		return block.(*types.Block)
	}
	block := rawdb.ReadBlock(bc.db, hash, number)
	if block == nil {
		return nil
	}
	// Cache the found block for next time and return
	bc.blockCache.Add(block.Hash(), block)
	return block
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (bc *BlockChain) GetBlockByHash(hash common.Hash) *types.Block {
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return bc.GetBlock(hash, *number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (bc *BlockChain) GetBlockByNumber(number uint64) *types.Block {
	hash := rawdb.ReadCanonicalHash(bc.db, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return bc.GetBlock(hash, number)
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (bc *BlockChain) GetBlocksFromHash(hash common.Hash, n int) (blocks []*types.Block) {
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := bc.GetBlock(hash, *number)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash()
		*number--
	}
	return
}

// Stop stops the blockchain service. If any imports are currently in progress
// it will abort them using the procInterrupt.
func (bc *BlockChain) Stop() {
	if !atomic.CompareAndSwapInt32(&bc.running, 0, 1) {
		return
	}
	// Unsubscribe all subscriptions registered from blockchain
	bc.scope.Close()
	close(bc.quit)
	bc.StopInsert()
	bc.wg.Wait()

	log.Info("Blockchain stopped")
}

// StopInsert interrupts all insertion methods, causing them to return
// errInsertionInterrupted as soon as possible. Insertion is permanently disabled after
// calling this method.
func (bc *BlockChain) StopInsert() {
	atomic.StoreInt32(&bc.procInterrupt, 1)
}

// insertStopped returns true after StopInsert has been called.
func (bc *BlockChain) insertStopped() bool {
	return atomic.LoadInt32(&bc.procInterrupt) == 1
}

// Config retrieves the chain's fork configuration.
func (bc *BlockChain) Config() *params.ChainConfig { return bc.chainConfig }

// SubscribeChainEvent registers a subscription of ChainEvent.
func (bc *BlockChain) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return bc.scope.Track(bc.chainFeed.Subscribe(ch))
}
