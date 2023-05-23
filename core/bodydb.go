package core

import (
	"fmt"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru"
)

const (
	bodyCacheLimit     = 256
	blockCacheLimit    = 256
	maxHeadsQueueLimit = 1024
)

type BodyDb struct {
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

func NewBodyDb(db ethdb.Database, engine consensus.Engine, hc *HeaderChain, chainConfig *params.ChainConfig, cacheConfig *CacheConfig, vmConfig vm.Config) (*BodyDb, error) {
	nodeCtx := common.NodeLocation.Context()
	blockCache, _ := lru.New(blockCacheLimit)
	bodyCache, _ := lru.New(bodyCacheLimit)
	bodyRLPCache, _ := lru.New(bodyCacheLimit)

	bc := &BodyDb{
		chainConfig:  chainConfig,
		engine:       engine,
		db:           db,
		blockCache:   blockCache,
		bodyCache:    bodyCache,
		bodyRLPCache: bodyRLPCache,
	}

	// only start the state processor in zone
	if nodeCtx == common.ZONE_CTX {
		bc.processor = NewStateProcessor(chainConfig, hc, engine, vmConfig, cacheConfig)
	}

	return bc, nil
}

// Append
func (bc *BodyDb) Append(batch ethdb.Batch, block *types.Block, newInboundEtxs types.Transactions) ([]*types.Log, error) {
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()
	fmt.Println("instrmnts:::: bodydb.blockCache: ", bc.blockCache.Len())
	fmt.Println("instrmnts:::: bodydb.bodyCache: ", bc.bodyCache.Len())
	fmt.Println("instrmnts:::: bodydb.bodyRLPCache: ", bc.bodyRLPCache.Len())

	stateApply := time.Now()
	nodeCtx := common.NodeLocation.Context()
	var logs []*types.Log
	var err error
	if nodeCtx == common.ZONE_CTX {
		// Process our block
		logs, err = bc.processor.Apply(batch, block, newInboundEtxs)
		if err != nil {
			return nil, err
		}
		rawdb.WriteTxLookupEntriesByBlock(batch, block)
	}
	log.Info("Time taken to", "apply state:", common.PrettyDuration(time.Since(stateApply)))

	rawdb.WriteBlock(batch, block)
	return logs, nil
}

// WriteBlock write the block to the bodydb database
func (bc *BodyDb) WriteBlock(block *types.Block) {
	// add the block to the cache as well
	bc.blockCache.Add(block.Hash(), block)
	rawdb.WriteBlock(bc.db, block)
}

// HasBlock checks if a block is fully present in the database or not.
func (bc *BodyDb) HasBlock(hash common.Hash, number uint64) bool {
	if bc.blockCache.Contains(hash) {
		return true
	}
	return rawdb.HasBody(bc.db, hash, number)
}

// Engine retreives the BodyDb consensus engine.
func (bc *BodyDb) Engine() consensus.Engine {
	return bc.engine
}

// GetBlock retrieves a block from the database by hash and number,
// caching it if found.
func (bc *BodyDb) GetBlock(hash common.Hash, number uint64) *types.Block {
	termini := rawdb.ReadTermini(bc.db, hash)
	if termini == nil {
		return nil
	}
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

// GetBlockOrCandidate retrieves any known block from the database by hash and number,
// caching it if found.
func (bc *BodyDb) GetBlockOrCandidate(hash common.Hash, number uint64) *types.Block {
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

func (bc *BodyDb) Processor() *StateProcessor {
	return bc.processor
}

// Config retrieves the chain's fork configuration.
func (bc *BodyDb) Config() *params.ChainConfig { return bc.chainConfig }

// SubscribeChainEvent registers a subscription of ChainEvent.
func (bc *BodyDb) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return bc.scope.Track(bc.chainFeed.Subscribe(ch))
}

// SubscribeRemovedLogsEvent registers a subscription of RemovedLogsEvent.
func (bc *BodyDb) SubscribeRemovedLogsEvent(ch chan<- RemovedLogsEvent) event.Subscription {
	return bc.scope.Track(bc.rmLogsFeed.Subscribe(ch))
}

// SubscribeLogsEvent registers a subscription of []*types.Log.
func (bc *BodyDb) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return bc.scope.Track(bc.logsFeed.Subscribe(ch))
}

// SubscribeBlockProcessingEvent registers a subscription of bool where true means
// block processing has started while false means it has stopped.
func (bc *BodyDb) SubscribeBlockProcessingEvent(ch chan<- bool) event.Subscription {
	return bc.scope.Track(bc.blockProcFeed.Subscribe(ch))
}

func (bc *BodyDb) HasBlockAndState(hash common.Hash, number uint64) bool {
	return bc.processor.HasBlockAndState(hash, number)
}
