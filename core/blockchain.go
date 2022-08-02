package core

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

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

	db ethdb.Database // Low level persistent database to store final content in

	chainFeed     event.Feed
	blockProcFeed event.Feed
	scope         event.SubscriptionScope

	engine        consensus.Engine
	chainmu       sync.RWMutex // blockchain insertion lock
	futureBlocks  *lru.Cache   // future blocks are blocks added for later processing
	blockCache    *lru.Cache
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
func NewBlockChain(db ethdb.Database, engine consensus.Engine, chainConfig *params.ChainConfig, vmConfig vm.Config) (*BlockChain, error) {

	blockCache, _ := lru.New(blockCacheLimit)

	bc := &BlockChain{
		chainConfig: chainConfig,
		engine:      engine,
		db:          db,
		quit:        make(chan struct{}),
		blockCache:  blockCache,
	}

	bc.processor = NewStateProcessor(chainConfig, bc, engine, vmConfig)

	return bc, nil
}

// Append
func (bc *BlockChain) Append(block *types.Block) error {
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	// Process our block and retrieve external blocks.
	err := bc.processor.Apply(block)
	if err != nil {
		bc.reportBlock(block, err)
		bc.futureBlocks.Remove(block.Hash())
		return err
	}

	batch := bc.db.NewBatch()
	rawdb.WriteBlock(batch, block)
	if err := batch.Write(); err != nil {
		return err
	}

	return nil
}

// Trim
func (bc *BlockChain) Trim(header *types.Header) {
	rawdb.DeleteBlock(bc.db, header.Hash(), header.Number[types.QuaiNetworkContext].Uint64())
}

// HasBlock checks if a block is fully present in the database or not.
func (bc *BlockChain) HasBlock(hash common.Hash, number uint64) bool {
	if bc.blockCache.Contains(hash) {
		return true
	}
	return rawdb.HasBody(bc.db, hash, number)
}

// Engine retreives the blockchain consensus engine.
func (bc *BlockChain) Engine() consensus.Engine {
	return bc.engine
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

// reportBlock logs a bad block error.
func (bc *BlockChain) reportBlock(block *types.Block, err error) {
	rawdb.WriteBadBlock(bc.db, block)

	log.Error(fmt.Sprintf(`
########## BAD BLOCK #########
Chain config: %v
Number: %v
Hash: 0x%x
Error: %v
##############################
`, bc.chainConfig, block.Number(), block.Hash(), err))
}
