package core

import (
	"errors"
	"sync"

	lru "github.com/hashicorp/golang-lru"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/event"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/params"
)

const (
	bodyCacheLimit     = 256
	blockCacheLimit    = 256
	maxHeadsQueueLimit = 1024
)

type BlockChain struct {
	chainConfig *params.ChainConfig // Chain & network configuration

	db ethdb.Database // Low level persistent database to store final content in

	chainFeed     event.Feed
	chainSideFeed event.Feed
	rmLogsFeed    event.Feed
	logsFeed      event.Feed
	blockProcFeed event.Feed
	scope         event.SubscriptionScope

	engine       consensus.Engine
	chainmu      sync.RWMutex
	blockCache   *lru.Cache
	bodyCache    *lru.Cache
	bodyRLPCache *lru.Cache
	processor    *StateProcessor
}

func NewBlockChain(db ethdb.Database, engine consensus.Engine, hc *HeaderChain, chainConfig *params.ChainConfig, cacheConfig *CacheConfig, vmConfig vm.Config) (*BlockChain, error) {

	blockCache, _ := lru.New(blockCacheLimit)
	bodyCache, _ := lru.New(bodyCacheLimit)
	bodyRLPCache, _ := lru.New(bodyCacheLimit)

	bc := &BlockChain{
		chainConfig:  chainConfig,
		engine:       engine,
		db:           db,
		blockCache:   blockCache,
		bodyCache:    bodyCache,
		bodyRLPCache: bodyRLPCache,
	}

	bc.processor = NewStateProcessor(chainConfig, hc, engine, vmConfig, cacheConfig)

	return bc, nil
}

// Append
func (bc *BlockChain) Append(batch ethdb.Batch, block *types.Block) ([]*types.Log, error) {
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	// Process our block and retrieve external blocks.
	logs, err := bc.processor.Apply(batch, block)
	if err != nil {
		return nil, err
	}

	if block.Hash() != block.Header().Hash() {
		log.Info("BlockChain Append, Roots Mismatch:", "block.Hash:", block.Hash(), "block.Header.Hash", block.Header().Hash(), "parentHeader.Number:", block.NumberU64())
		bc.chainSideFeed.Send(ChainSideEvent{Block: block})
		return nil, errors.New("state roots do not match header, append fail")
	}
	rawdb.WriteBlock(batch, block)
	rawdb.WriteTxLookupEntriesByBlock(batch, block)

	return logs, nil
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

func (bc *BlockChain) Processor() *StateProcessor {
	return bc.processor
}

// Config retrieves the chain's fork configuration.
func (bc *BlockChain) Config() *params.ChainConfig { return bc.chainConfig }

// SubscribeChainEvent registers a subscription of ChainEvent.
func (bc *BlockChain) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return bc.scope.Track(bc.chainFeed.Subscribe(ch))
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (bc *BlockChain) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return bc.scope.Track(bc.chainSideFeed.Subscribe(ch))
}

// SubscribeRemovedLogsEvent registers a subscription of RemovedLogsEvent.
func (bc *BlockChain) SubscribeRemovedLogsEvent(ch chan<- RemovedLogsEvent) event.Subscription {
	return bc.scope.Track(bc.rmLogsFeed.Subscribe(ch))
}

// SubscribeLogsEvent registers a subscription of []*types.Log.
func (bc *BlockChain) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return bc.scope.Track(bc.logsFeed.Subscribe(ch))
}

// SubscribeBlockProcessingEvent registers a subscription of bool where true means
// block processing has started while false means it has stopped.
func (bc *BlockChain) SubscribeBlockProcessingEvent(ch chan<- bool) event.Subscription {
	return bc.scope.Track(bc.blockProcFeed.Subscribe(ch))
}

func (bc *BlockChain) HasBlockAndState(hash common.Hash, number uint64) bool {
	return bc.processor.HasBlockAndState(hash, number)
}
