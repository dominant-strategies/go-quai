package core

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
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

	engine         consensus.Engine
	chainmu        sync.RWMutex
	blockCache     *lru.Cache
	bodyCache      *lru.Cache
	bodyProtoCache *lru.Cache
	woCache        *lru.Cache
	processor      *StateProcessor

	slicesRunning   []common.Location
	processingState bool

	logger *log.Logger
}

func NewBodyDb(db ethdb.Database, engine consensus.Engine, hc *HeaderChain, chainConfig *params.ChainConfig, cacheConfig *CacheConfig, txLookupLimit *uint64, vmConfig vm.Config, slicesRunning []common.Location) (*BodyDb, error) {
	nodeCtx := chainConfig.Location.Context()

	bc := &BodyDb{
		chainConfig:   chainConfig,
		engine:        engine,
		db:            db,
		slicesRunning: slicesRunning,
		logger:        hc.logger,
	}

	// Limiting the number of blocks to be stored in the cache in the case of
	// slices that are not being processed by the node. This helps lower the RAM
	// requirement on the slice nodes
	if bc.ProcessingState() {
		blockCache, _ := lru.New(blockCacheLimit)
		bodyCache, _ := lru.New(bodyCacheLimit)
		bodyRLPCache, _ := lru.New(bodyCacheLimit)
		bc.blockCache = blockCache
		bc.bodyCache = bodyCache
		bc.bodyProtoCache = bodyRLPCache
		bc.woCache, _ = lru.New(bodyCacheLimit)
	} else {
		blockCache, _ := lru.New(10)
		bodyCache, _ := lru.New(10)
		bodyRLPCache, _ := lru.New(10)
		bc.blockCache = blockCache
		bc.bodyCache = bodyCache
		bc.bodyProtoCache = bodyRLPCache
		bc.woCache, _ = lru.New(10)
	}

	// only start the state processor in zone
	if nodeCtx == common.ZONE_CTX && bc.ProcessingState() {
		bc.processor = NewStateProcessor(chainConfig, hc, engine, vmConfig, cacheConfig, txLookupLimit)
		vm.InitializePrecompiles(chainConfig.Location)
	}

	return bc, nil
}

// Append
func (bc *BodyDb) Append(block *types.WorkObject, newInboundEtxs types.Transactions) ([]*types.Log, error) {
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	batch := bc.db.NewBatch()
	stateApply := time.Now()
	nodeCtx := bc.NodeCtx()
	var logs []*types.Log
	var err error
	if nodeCtx == common.ZONE_CTX && bc.ProcessingState() {
		// Process our block
		logs, err = bc.processor.Apply(batch, block, newInboundEtxs)
		if err != nil {
			return nil, err
		}
		rawdb.WriteTxLookupEntriesByBlock(batch, block, nodeCtx)
	}
	bc.logger.WithField("apply state", common.PrettyDuration(time.Since(stateApply))).Debug("Time taken to")
	if err = batch.Write(); err != nil {
		return nil, err
	}
	return logs, nil
}

func (bc *BodyDb) ProcessingState() bool {
	nodeCtx := bc.NodeCtx()
	for _, slice := range bc.slicesRunning {
		switch nodeCtx {
		case common.PRIME_CTX:
			return true
		case common.REGION_CTX:
			if slice.Region() == bc.NodeLocation().Region() {
				return true
			}
		case common.ZONE_CTX:
			if slice.Equal(bc.NodeLocation()) {
				return true
			}
		}
	}
	return false
}

// WriteBlock write the block to the bodydb database
func (bc *BodyDb) WriteBlock(block *types.WorkObject, nodeCtx int) {
	// add the block to the cache as well
	bc.blockCache.Add(block.Hash(), block)
	rawdb.WriteWorkObject(bc.db, block.Hash(), block, types.BlockObject, nodeCtx)
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
func (bc *BodyDb) GetBlock(hash common.Hash, number uint64) *types.WorkObject {
	termini := rawdb.ReadTermini(bc.db, hash)
	if termini == nil {
		return nil
	}
	// Short circuit if the block's already in the cache, retrieve otherwise
	if block, ok := bc.blockCache.Get(hash); ok {
		return block.(*types.WorkObject)
	}
	block := rawdb.ReadWorkObject(bc.db, hash, types.BlockObject)
	if block == nil {
		return nil
	}
	// Cache the found block for next time and return
	bc.blockCache.Add(block.Hash(), block)
	return block
}

// GetWorkObject retrieves a workObject from the database by hash,
// caching it if found.
func (bc *BodyDb) GetWorkObject(hash common.Hash) *types.WorkObject {
	termini := rawdb.ReadTermini(bc.db, hash)
	if termini == nil {
		return nil
	}
	// Short circuit if the block's already in the cache, retrieve otherwise
	if wo, ok := bc.woCache.Get(hash); ok {
		return wo.(*types.WorkObject)
	}
	wo := rawdb.ReadWorkObject(bc.db, hash, types.BlockObject)
	if wo == nil {
		return nil
	}
	// Cache the found block for next time and return
	bc.woCache.Add(wo.Hash(), wo)
	return wo
}

// GetBlockOrCandidate retrieves any known block from the database by hash and number,
// caching it if found.
func (bc *BodyDb) GetBlockOrCandidate(hash common.Hash, number uint64) *types.WorkObject {
	block := rawdb.ReadWorkObject(bc.db, hash, types.BlockObject)
	if block == nil {
		return nil
	}
	return block
}

func (bc *BodyDb) Processor() *StateProcessor {
	return bc.processor
}

func (bc *BodyDb) NodeLocation() common.Location {
	return bc.chainConfig.Location
}

func (bc *BodyDb) NodeCtx() int {
	return bc.chainConfig.Location.Context()
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
