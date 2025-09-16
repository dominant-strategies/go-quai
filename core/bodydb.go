package core

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
)

const (
	bodyCacheLimit  = 25
	blockCacheLimit = 25
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

	engine         []consensus.Engine
	blockCache     *lru.Cache[common.Hash, types.WorkObject]
	bodyCache      *lru.Cache[common.Hash, types.WorkObject]
	bodyProtoCache *lru.Cache[common.Hash, rlp.RawValue]
	woCache        *lru.Cache[common.Hash, types.WorkObject]
	processor      *StateProcessor

	slicesRunning   []common.Location
	processingState bool

	logger *log.Logger
}

func NewTestBodyDb(db ethdb.Database) *BodyDb {
	bc := &BodyDb{db: db}
	blockCache, _ := lru.New[common.Hash, types.WorkObject](blockCacheLimit)
	bodyCache, _ := lru.New[common.Hash, types.WorkObject](bodyCacheLimit)

	bc.blockCache = blockCache
	bc.bodyCache = bodyCache
	return bc
}

func NewBodyDb(db ethdb.Database, engine []consensus.Engine, hc *HeaderChain, chainConfig *params.ChainConfig, cacheConfig *CacheConfig, txLookupLimit *uint64, vmConfig vm.Config, slicesRunning []common.Location) (*BodyDb, error) {
	nodeCtx := chainConfig.Location.Context()

	bc := &BodyDb{
		chainConfig:   chainConfig,
		engine:        engine,
		db:            db,
		slicesRunning: slicesRunning,
		logger:        hc.logger,
	}

	blockCache, _ := lru.New[common.Hash, types.WorkObject](blockCacheLimit)
	bodyCache, _ := lru.New[common.Hash, types.WorkObject](bodyCacheLimit)
	bodyRLPCache, _ := lru.New[common.Hash, rlp.RawValue](bodyCacheLimit)
	woCache, _ := lru.New[common.Hash, types.WorkObject](bodyCacheLimit)
	bc.blockCache = blockCache
	bc.bodyCache = bodyCache
	bc.bodyProtoCache = bodyRLPCache
	bc.woCache = woCache

	// only start the state processor in zone
	if nodeCtx == common.ZONE_CTX && bc.ProcessingState() {
		bc.processor = NewStateProcessor(chainConfig, hc, engine, vmConfig, cacheConfig, txLookupLimit)
		vm.InitializePrecompiles(chainConfig.Location)
	}

	return bc, nil
}

// Append
func (bc *BodyDb) Append(block *types.WorkObject) ([]*types.Log, []common.Unlock, error) {
	startLock := time.Now()

	batch := bc.db.NewBatch()
	stateApply := time.Now()
	locktime := time.Since(startLock)
	nodeCtx := bc.NodeCtx()
	var logs []*types.Log
	var unlocks []common.Unlock
	var err error
	if nodeCtx == common.ZONE_CTX && bc.ProcessingState() {
		// Process our block
		logs, unlocks, err = bc.processor.Apply(batch, block)
		if err != nil {
			return nil, nil, err
		}
		rawdb.WriteTxLookupEntriesByBlock(batch, block, nodeCtx)
	}
	bc.logger.WithFields(log.Fields{
		"block":      block.Number,
		"hash":       block.Hash(),
		"startLock":  common.PrettyDuration(locktime),
		"stateApply": common.PrettyDuration(time.Since(stateApply)),
	}).Info("Time taken to write block body")

	bc.logger.WithField("apply state", common.PrettyDuration(time.Since(stateApply))).Debug("Time taken to")
	if err = batch.Write(); err != nil {
		return nil, nil, err
	}
	return logs, unlocks, nil
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
	bc.blockCache.Add(block.Hash(), *block)
	rawdb.WriteWorkObject(bc.db, block.Hash(), block, types.BlockObject, nodeCtx)
}

// HasBlock checks if a block is fully present in the database or not.
func (bc *BodyDb) HasBlock(hash common.Hash, number uint64) bool {
	if bc.blockCache.Contains(hash) {
		return true
	}
	return rawdb.HasBody(bc.db, hash, number)
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
		return &block
	}
	block := rawdb.ReadWorkObject(bc.db, number, hash, types.BlockObject)
	if block == nil {
		return nil
	}
	// Cache the found block for next time and return
	bc.blockCache.Add(block.Hash(), *block)
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
		return &wo
	}

	number := rawdb.ReadHeaderNumber(bc.db, hash)
	if number == nil {
		return nil
	}
	wo := rawdb.ReadWorkObject(bc.db, *number, hash, types.BlockObject)
	if wo == nil {
		return nil
	}
	// Cache the found block for next time and return
	bc.woCache.Add(wo.Hash(), *wo)
	return wo
}

// GetWorkObjectWithWorkShares retrieves a workObject with workshares from the database by hash,
// caching it if found.
func (bc *BodyDb) GetWorkObjectWithWorkShares(hash common.Hash) *types.WorkObject {
	termini := rawdb.ReadTermini(bc.db, hash)
	if termini == nil {
		return nil
	}
	number := rawdb.ReadHeaderNumber(bc.db, hash)
	if number == nil {
		return nil
	}
	wo := rawdb.ReadWorkObjectWithWorkShares(bc.db, *number, hash)
	if wo == nil {
		return nil
	}
	return wo
}

// GetBlockOrCandidate retrieves any known block from the database by hash and number,
// caching it if found.
func (bc *BodyDb) GetBlockOrCandidate(hash common.Hash, number uint64) *types.WorkObject {
	if block, ok := bc.blockCache.Get(hash); ok {
		return &block
	}

	block := rawdb.ReadWorkObject(bc.db, number, hash, types.BlockObject)
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
