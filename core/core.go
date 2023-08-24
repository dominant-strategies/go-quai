package core

import (
	"context"
	"errors"
	"io"
	"math/big"
	"sort"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/state/snapshot"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hnlq715/golang-lru"
)

const (
	c_maxAppendQueue                    = 1000000 // Maximum number of future headers we can store in cache
	c_maxFutureTime                     = 30      // Max time into the future (in seconds) we will accept a block
	c_appendQueueRetryPeriod            = 1       // Time (in seconds) before retrying to append from AppendQueue
	c_appendQueueThreshold              = 1000    // Number of blocks to load from the disk to ram on every proc of append queue
	c_processingCache                   = 10      // Number of block hashes held to prevent multi simultaneous appends on a single block hash
	c_primeRetryThreshold               = 900     // Number of times a block is retry to be appended before eviction from append queue in Prime
	c_regionRetryThreshold              = 300     // Number of times a block is retry to be appended before eviction from append queue in Region
	c_zoneRetryThreshold                = 100     // Number of times a block is retry to be appended before eviction from append queue in Zone
	c_maxFutureBlocks                   = 15      // Number of blocks ahead of the current block to be put in the hashNumberList
	c_appendQueueRetryPriorityThreshold = 5       // If retry counter for a block is less than this number,  then its put in the special list that is tried first to be appended
	c_appendQueueRemoveThreshold        = 10      // Number of blocks behind the block should be from the current header to be eligble for removal from the append queue
)

type blockNumberAndRetryCounter struct {
	number uint64
	retry  uint64
}

type Core struct {
	sl     *Slice
	engine consensus.Engine

	appendQueue     *lru.Cache
	processingCache *lru.Cache

	quit chan struct{} // core quit channel
}

func NewCore(db ethdb.Database, config *Config, isLocalBlock func(block *types.Header) bool, txConfig *TxPoolConfig, txLookupLimit *uint64, chainConfig *params.ChainConfig, slicesRunning []common.Location, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis) (*Core, error) {
	slice, err := NewSlice(db, config, txConfig, txLookupLimit, isLocalBlock, chainConfig, slicesRunning, domClientUrl, subClientUrls, engine, cacheConfig, vmConfig, genesis)
	if err != nil {
		return nil, err
	}

	c := &Core{
		sl:     slice,
		engine: engine,
		quit:   make(chan struct{}),
	}

	appendQueue, _ := lru.New(c_maxAppendQueue)
	c.appendQueue = appendQueue

	proccesingCache, _ := lru.NewWithExpire(c_processingCache, time.Second*60)
	c.processingCache = proccesingCache

	go c.updateAppendQueue()
	return c, nil
}

// InsertChain attempts to append a list of blocks to the slice, optionally
// caching any pending blocks which cannot yet be appended. InsertChain return
// the number of blocks which were successfully consumed (either appended, or
// cached), and an error.
func (c *Core) InsertChain(blocks types.Blocks) (int, error) {
	nodeCtx := common.NodeLocation.Context()
	for idx, block := range blocks {
		// Only attempt to append a block, if it is not coincident with our dominant
		// chain. If it is dom coincident, then the dom chain node in our slice needs
		// to initiate the append.
		_, order, err := c.CalcOrder(block.Header())
		if err != nil {
			return idx, err
		}

		if order == nodeCtx {
			if !c.processingCache.Contains(block.Hash()) {
				c.processingCache.Add(block.Hash(), 1)
			} else {
				log.Info("Already proccessing block:", "Number:", block.Header().NumberArray(), "Hash:", block.Hash())
				return idx, errors.New("Already in process of appending this block")
			}
			newPendingEtxs, _, err := c.sl.Append(block.Header(), types.EmptyHeader(), common.Hash{}, false, nil)
			c.processingCache.Remove(block.Hash())
			if err == nil {
				// If we have a dom, send the dom any pending ETXs which will become
				// referencable by this block. When this block is referenced in the dom's
				// subordinate block manifest, then ETXs produced by this block and the rollup
				// of ETXs produced by subordinate chain(s) will become referencable.
				if nodeCtx > common.PRIME_CTX {
					pendingEtx := types.PendingEtxs{block.Header(), newPendingEtxs}
					// Only send the pending Etxs to dom if valid, because in the case of running a slice, for the zones that the node doesn't run, it cannot have the etxs generated
					if pendingEtx.IsValid(trie.NewStackTrie(nil)) {
						if err := c.SendPendingEtxsToDom(pendingEtx); err != nil {
							log.Error("failed to send ETXs to domclient", "block: ", block.Hash(), "err", err)
						}
					}
				}
				c.removeFromAppendQueue(block)
			} else if err.Error() == consensus.ErrFutureBlock.Error() ||
				err.Error() == ErrBodyNotFound.Error() ||
				err.Error() == ErrPendingEtxNotFound.Error() ||
				err.Error() == consensus.ErrPrunedAncestor.Error() ||
				err.Error() == consensus.ErrUnknownAncestor.Error() ||
				err.Error() == ErrSubNotSyncedToDom.Error() ||
				err.Error() == ErrDomClientNotUp.Error() {
				if c.sl.CurrentInfo(block.Header()) {
					log.Info("Cannot append yet.", "hash", block.Hash(), "err", err)
				} else {
					log.Debug("Cannot append yet.", "hash", block.Hash(), "err", err)
				}
				return idx, ErrPendingBlock
			} else if err.Error() != ErrKnownBlock.Error() {
				log.Info("Append failed.", "hash", block.Hash(), "err", err)
			}
			c.removeFromAppendQueue(block)
		}
	}
	return len(blocks), nil
}

// procAppendQueue sorts the append queue and attempts to append
func (c *Core) procAppendQueue() {
	// Sort the blocks by number and retry attempts and try to insert them
	// blocks will be aged out of the append queue after the retry threhsold
	var hashNumberList []types.HashAndNumber
	var hashNumberPriorityList []types.HashAndNumber
	for _, hash := range c.appendQueue.Keys() {
		if value, exist := c.appendQueue.Peek(hash); exist {
			hashNumber := types.HashAndNumber{Hash: hash.(common.Hash), Number: value.(blockNumberAndRetryCounter).number}
			if value.(blockNumberAndRetryCounter).retry < c_appendQueueRetryPriorityThreshold {
				hashNumberPriorityList = append(hashNumberPriorityList, hashNumber)
			}
			if hashNumber.Number < c.CurrentHeader().NumberU64()+c_maxFutureBlocks {
				hashNumberList = append(hashNumberList, hashNumber)
			}
		}
	}

	c.serviceBlocks(hashNumberPriorityList)
	if len(hashNumberPriorityList) > 0 {
		log.Info("Size of hashNumberPriorityList", "len", len(hashNumberPriorityList), "first entry", hashNumberPriorityList[0].Number, "last entry", hashNumberPriorityList[len(hashNumberPriorityList)-1].Number)
	}
	c.serviceBlocks(hashNumberList)
	if len(hashNumberList) > 0 {
		log.Info("Size of hashNumberList", "len", len(hashNumberList), "first entry", hashNumberList[0].Number, "last entry", hashNumberList[len(hashNumberList)-1].Number)
	}
}

func (c *Core) serviceBlocks(hashNumberList []types.HashAndNumber) {
	sort.Slice(hashNumberList, func(i, j int) bool {
		return hashNumberList[i].Number < hashNumberList[j].Number
	})

	var retryThreshold uint64
	switch common.NodeLocation.Context() {
	case common.PRIME_CTX:
		retryThreshold = c_primeRetryThreshold
	case common.REGION_CTX:
		retryThreshold = c_regionRetryThreshold
	case common.ZONE_CTX:
		retryThreshold = c_zoneRetryThreshold
	}

	// Attempt to service the sorted list
	for _, hashAndNumber := range hashNumberList {
		header := c.GetHeaderOrCandidateByHash(hashAndNumber.Hash)
		if header != nil {
			var numberAndRetryCounter blockNumberAndRetryCounter
			if value, exist := c.appendQueue.Peek(header.Hash()); exist {
				numberAndRetryCounter = value.(blockNumberAndRetryCounter)
				numberAndRetryCounter.retry += 1
				if numberAndRetryCounter.retry > retryThreshold && numberAndRetryCounter.number+c_appendQueueRemoveThreshold < c.CurrentHeader().NumberU64() {
					c.appendQueue.Remove(header.Hash())
				} else {
					c.appendQueue.Add(header.Hash(), numberAndRetryCounter)
				}
			}
			parentHeader := c.GetHeader(header.ParentHash(), header.NumberU64()-1)
			if parentHeader != nil {
				// If parent header is dom, send a signal to dom to request for the block if it doesnt have it
				_, parentHeaderOrder, err := c.sl.engine.CalcOrder(parentHeader)
				if err != nil {
					log.Warn("Error calculating the parent block order in serviceBlocks", "Hash", parentHeader.Hash(), "Number", parentHeader.NumberArray())
					continue
				}
				nodeCtx := common.NodeLocation.Context()
				if parentHeaderOrder < nodeCtx && c.GetHeaderByHash(parentHeader.Hash()) == nil {
					log.Info("Requesting the dom to get the block if it doesnt have and try to append", "Hash", parentHeader.Hash(), "Order", parentHeaderOrder)
					// send a signal to the required dom to fetch the block if it doesnt have it, or its not in its appendqueue
					go c.sl.domClient.RequestDomToAppendOrFetch(context.Background(), parentHeader.Hash(), parentHeaderOrder)
				}
				// Using a empty block to append here because append only takes in a
				// header and we read the block inside append again, so to save the
				// ram, we are using the header
				c.InsertChain([]*types.Block{types.NewBlockWithHeader(header)})
			} else {
				if !c.HasHeader(header.ParentHash(), header.NumberU64()-1) {
					c.sl.missingParentFeed.Send(header.ParentHash())
				}
			}
		} else {
			log.Warn("Entry in the FH cache without being in the db: ", "Hash: ", hashAndNumber.Hash)
		}
	}
}

func (c *Core) RequestDomToAppendOrFetch(hash common.Hash, order int) {
	// TODO: optimize to check if the block is in the appendqueue or already
	// appended to reduce the network bandwidth utilization
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.PRIME_CTX {
		// If prime all you can do it to ask for the block
		_, exists := c.appendQueue.Get(hash)
		if !exists {
			log.Warn("Block sub asked doesnt exist in append queue, so request the peers for it", "Hash", hash, "Order", order)
			c.sl.missingParentFeed.Send(hash) // Using the missing parent feed to ask for the block
		}
	} else if nodeCtx == common.REGION_CTX {
		if order < nodeCtx { // Prime block
			go c.sl.domClient.RequestDomToAppendOrFetch(context.Background(), hash, order)
		}
		_, exists := c.appendQueue.Get(hash)
		if !exists {
			log.Warn("Block sub asked doesnt exist in append queue, so request the peers for it", "Hash", hash, "Order", order)
			c.sl.missingParentFeed.Send(hash) // Using the missing parent feed to ask for the block
		}
	}

}

// addToAppendQueue adds a block to the append queue
func (c *Core) addToAppendQueue(block *types.Block) error {
	c.appendQueue.ContainsOrAdd(block.Hash(), blockNumberAndRetryCounter{block.NumberU64(), 0})
	return nil
}

// removeFromAppendQueue removes a block from the append queue
func (c *Core) removeFromAppendQueue(block *types.Block) {
	c.appendQueue.Remove(block.Hash())
}

// updateAppendQueue is a time to procAppendQueue
func (c *Core) updateAppendQueue() {
	futureTimer := time.NewTicker(c_appendQueueRetryPeriod * time.Second)
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			c.procAppendQueue()
		case <-c.quit:
			return
		}
	}
}

func (c *Core) BadHashExistsInChain() bool {
	nodeCtx := common.NodeLocation.Context()
	// Lookup the bad hashes list to see if we have it in the database
	for _, fork := range BadHashes {
		switch nodeCtx {
		case common.PRIME_CTX:
			if c.GetBlockByHash(fork.PrimeContext) != nil {
				return true
			}
		case common.REGION_CTX:
			if c.GetBlockByHash(fork.RegionContext[common.NodeLocation.Region()]) != nil {
				return true
			}
		case common.ZONE_CTX:
			if c.GetBlockByHash(fork.ZoneContext[common.NodeLocation.Region()][common.NodeLocation.Zone()]) != nil {
				return true
			}
		}
	}
	return false
}

func (c *Core) SubscribeMissingParentEvent(ch chan<- common.Hash) event.Subscription {
	return c.sl.SubscribeMissingParentEvent(ch)
}

// InsertChainWithoutSealVerification works exactly the same
// except for seal verification, seal verification is omitted
func (c *Core) InsertChainWithoutSealVerification(block *types.Block) (int, error) {
	return 0, nil
}

func (c *Core) Processor() *StateProcessor {
	return c.sl.hc.bc.processor
}

func (c *Core) Config() *params.ChainConfig {
	return c.sl.hc.bc.chainConfig
}

// Engine retreives the blake3 consensus engine.
func (c *Core) Engine() consensus.Engine {
	return c.engine
}

// Slice retrieves the slice struct.
func (c *Core) Slice() *Slice {
	return c.sl
}

func (c *Core) TxPool() *TxPool {
	return c.sl.txPool
}

func (c *Core) Stop() {
	// Delete the append queue
	c.appendQueue.Purge()
	close(c.quit)
	c.sl.Stop()
}

//---------------//
// Slice methods //
//---------------//

// WriteBlock write the block to the bodydb database
func (c *Core) WriteBlock(block *types.Block) {
	if c.sl.IsBlockHashABadHash(block.Hash()) {
		return
	}
	if c.GetHeaderByHash(block.Hash()) == nil {
		// Only add non dom blocks to the append queue
		_, order, err := c.CalcOrder(block.Header())
		if err != nil {
			return
		}
		nodeCtx := common.NodeLocation.Context()
		if order == nodeCtx {
			c.addToAppendQueue(block)
			// If a dom block comes in and we havent appended it yet
		} else if order < nodeCtx && c.GetHeaderByHash(block.Hash()) == nil {
			go c.sl.domClient.RequestDomToAppendOrFetch(context.Background(), block.Hash(), order)
		}
	}
	if c.GetHeaderOrCandidateByHash(block.Hash()) == nil {
		c.sl.WriteBlock(block)
	}
}

func (c *Core) Append(header *types.Header, domPendingHeader *types.Header, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, bool, error) {
	newPendingEtxs, subReorg, err := c.sl.Append(header, domPendingHeader, domTerminus, domOrigin, newInboundEtxs)
	if err != nil {
		if err.Error() == ErrBodyNotFound.Error() || err.Error() == consensus.ErrUnknownAncestor.Error() || err.Error() == ErrSubNotSyncedToDom.Error() {
			c.sl.missingParentFeed.Send(header.ParentHash())
		}
	}
	return newPendingEtxs, subReorg, err
}

// ConstructLocalBlock takes a header and construct the Block locally
func (c *Core) ConstructLocalMinedBlock(header *types.Header) (*types.Block, error) {
	return c.sl.ConstructLocalMinedBlock(header)
}

func (c *Core) SubRelayPendingHeader(slPendingHeader types.PendingHeader, location common.Location, subReorg bool) {
	c.sl.SubRelayPendingHeader(slPendingHeader, location, subReorg)
}

func (c *Core) UpdateDom(oldTerminus common.Hash, newTerminus common.Hash, location common.Location) {
	c.sl.UpdateDom(oldTerminus, newTerminus, location)
}

func (c *Core) NewGenesisPendigHeader(pendingHeader *types.Header) {
	c.sl.NewGenesisPendingHeader(pendingHeader)
}

func (c *Core) GetPendingHeader() (*types.Header, error) {
	return c.sl.GetPendingHeader()
}

func (c *Core) GetManifest(blockHash common.Hash) (types.BlockManifest, error) {
	return c.sl.GetManifest(blockHash)
}

func (c *Core) GetSubManifest(slice common.Location, blockHash common.Hash) (types.BlockManifest, error) {
	return c.sl.GetSubManifest(slice, blockHash)
}

func (c *Core) GetPendingEtxs(hash common.Hash) *types.PendingEtxs {
	return rawdb.ReadPendingEtxs(c.sl.sliceDb, hash)
}

func (c *Core) GetPendingEtxsRollup(hash common.Hash) *types.PendingEtxsRollup {
	return rawdb.ReadPendingEtxsRollup(c.sl.sliceDb, hash)
}

func (c *Core) HasPendingEtxs(hash common.Hash) bool {
	return c.GetPendingEtxs(hash) != nil
}

func (c *Core) SendPendingEtxsToDom(pEtxs types.PendingEtxs) error {
	return c.sl.SendPendingEtxsToDom(pEtxs)
}

func (c *Core) SubscribePendingEtxs(ch chan<- types.PendingEtxs) event.Subscription {
	return c.sl.SubscribePendingEtxs(ch)
}

func (c *Core) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	return c.sl.AddPendingEtxs(pEtxs)
}

func (c *Core) AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error {
	return c.sl.AddPendingEtxsRollup(pEtxsRollup)
}

func (c *Core) SubscribePendingEtxsRollup(ch chan<- types.PendingEtxsRollup) event.Subscription {
	return c.sl.SubscribePendingEtxsRollup(ch)
}

func (c *Core) GenerateRecoveryPendingHeader(pendingHeader *types.Header, checkpointHashes types.Termini) error {
	return c.sl.GenerateRecoveryPendingHeader(pendingHeader, checkpointHashes)
}

func (c *Core) IsBlockHashABadHash(hash common.Hash) bool {
	return c.sl.IsBlockHashABadHash(hash)
}

func (c *Core) ProcessingState() bool {
	return c.sl.ProcessingState()
}

//---------------------//
// HeaderChain methods //
//---------------------//

// GetBlock retrieves a block from the database by hash and number,
// caching it if found.
func (c *Core) GetBlock(hash common.Hash, number uint64) *types.Block {
	return c.sl.hc.GetBlock(hash, number)
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (c *Core) GetBlockByHash(hash common.Hash) *types.Block {
	return c.sl.hc.GetBlockByHash(hash)
}

// GetBlockOrCandidateByHash retrieves a block from the database by hash, caching it if found.
func (c *Core) GetBlockOrCandidateByHash(hash common.Hash) *types.Block {
	return c.sl.hc.GetBlockOrCandidateByHash(hash)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (c *Core) GetHeaderByNumber(number uint64) *types.Header {
	return c.sl.hc.GetHeaderByNumber(number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (c *Core) GetBlockByNumber(number uint64) *types.Block {
	return c.sl.hc.GetBlockByNumber(number)
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (c *Core) GetBlocksFromHash(hash common.Hash, n int) []*types.Block {
	return c.sl.hc.GetBlocksFromHash(hash, n)
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (c *Core) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	return c.sl.hc.GetUnclesInChain(block, length)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (c *Core) GetGasUsedInChain(block *types.Block, length int) int64 {
	return c.sl.hc.GetGasUsedInChain(block, length)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (c *Core) CalculateBaseFee(header *types.Header) *big.Int {
	return c.sl.hc.CalculateBaseFee(header)
}

// CurrentBlock returns the block for the current header.
func (c *Core) CurrentBlock() *types.Block {
	return c.sl.hc.CurrentBlock()
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (c *Core) CurrentHeader() *types.Header {
	return c.sl.hc.CurrentHeader()
}

// CurrentLogEntropy returns the logarithm of the total entropy reduction since genesis for our current head block
func (c *Core) CurrentLogEntropy() *big.Int {
	return c.engine.TotalLogS(c.sl.hc.CurrentHeader())
}

// TotalLogS returns the total entropy reduction if the chain since genesis to the given header
func (c *Core) TotalLogS(header *types.Header) *big.Int {
	return c.engine.TotalLogS(header)
}

// CalcOrder returns the order of the block within the hierarchy of chains
func (c *Core) CalcOrder(header *types.Header) (*big.Int, int, error) {
	return c.engine.CalcOrder(header)
}

// GetHeader retrieves a block header from the database by hash and number,
// caching it if found.
func (c *Core) GetHeader(hash common.Hash, number uint64) *types.Header {
	return c.sl.hc.GetHeader(hash, number)
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (c *Core) GetHeaderByHash(hash common.Hash) *types.Header {
	return c.sl.hc.GetHeaderByHash(hash)
}

// GetHeaderOrCandidate retrieves a block header from the database by hash and number,
// caching it if found.
func (c *Core) GetHeaderOrCandidate(hash common.Hash, number uint64) *types.Header {
	return c.sl.hc.GetHeaderOrCandidate(hash, number)
}

// GetHeaderOrCandidateByHash retrieves a block header from the database by hash, caching it if
// found.
func (c *Core) GetHeaderOrCandidateByHash(hash common.Hash) *types.Header {
	return c.sl.hc.GetHeaderOrCandidateByHash(hash)
}

// HasHeader checks if a block header is present in the database or not, caching
// it if present.
func (c *Core) HasHeader(hash common.Hash, number uint64) bool {
	return c.sl.hc.HasHeader(hash, number)
}

// GetCanonicalHash returns the canonical hash for a given block number
func (c *Core) GetCanonicalHash(number uint64) common.Hash {
	return c.sl.hc.GetCanonicalHash(number)
}

// GetBlockHashesFromHash retrieves a number of block hashes starting at a given
// hash, fetching towards the genesis block.
func (c *Core) GetBlockHashesFromHash(hash common.Hash, max uint64) []common.Hash {
	return c.sl.hc.GetBlockHashesFromHash(hash, max)
}

// GetAncestor retrieves the Nth ancestor of a given block. It assumes that either the given block or
// a close ancestor of it is canonical. maxNonCanonical points to a downwards counter limiting the
// number of blocks to be individually checked before we reach the canonical chain.
//
// Note: ancestor == 0 returns the same block, 1 returns its parent and so on.
func (c *Core) GetAncestor(hash common.Hash, number, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	return c.sl.hc.GetAncestor(hash, number, ancestor, maxNonCanonical)
}

// Genesis retrieves the chain's genesis block.
func (c *Core) Genesis() *types.Block {
	return c.GetBlockByHash(c.sl.hc.genesisHeader.Hash())
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (c *Core) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return c.sl.hc.SubscribeChainHeadEvent(ch)
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (c *Core) GetBody(hash common.Hash) *types.Body {
	return c.sl.hc.GetBody(hash)
}

// GetBodyRLP retrieves a block body in RLP encoding from the database by hash,
// caching it if found.
func (c *Core) GetBodyRLP(hash common.Hash) rlp.RawValue {
	return c.sl.hc.GetBodyRLP(hash)
}

// GetTerminiByHash retrieves the termini stored for a given header hash
func (c *Core) GetTerminiByHash(hash common.Hash) *types.Termini {
	return c.sl.hc.GetTerminiByHash(hash)
}

func (c *Core) SubscribeMissingPendingEtxsEvent(ch chan<- types.HashAndLocation) event.Subscription {
	return c.sl.hc.SubscribeMissingPendingEtxsEvent(ch)
}

func (c *Core) SubscribeMissingPendingEtxsRollupEvent(ch chan<- common.Hash) event.Subscription {
	return c.sl.hc.SubscribeMissingPendingEtxsRollupEvent(ch)
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (c *Core) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return c.sl.hc.SubscribeChainSideEvent(ch)
}

//--------------------//
// BlockChain methods //
//--------------------//

// HasBlock checks if a block is fully present in the database or not.
func (c *Core) HasBlock(hash common.Hash, number uint64) bool {
	return c.sl.hc.bc.HasBlock(hash, number)
}

// SubscribeChainEvent registers a subscription of ChainEvent.
func (c *Core) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return c.sl.hc.bc.SubscribeChainEvent(ch)
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (c *Core) SubscribeRemovedLogsEvent(ch chan<- RemovedLogsEvent) event.Subscription {
	return c.sl.hc.bc.SubscribeRemovedLogsEvent(ch)
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (c *Core) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return c.sl.hc.bc.SubscribeLogsEvent(ch)
}

// SubscribeBlockProcessingEvent registers a subscription of bool where true means
// block processing has started while false means it has stopped.
func (c *Core) SubscribeBlockProcessingEvent(ch chan<- bool) event.Subscription {
	return c.sl.hc.bc.SubscribeBlockProcessingEvent(ch)
}

// Export writes the active chain to the given writer.
func (c *Core) Export(w io.Writer) error {
	return c.sl.hc.Export(w)
}

// ExportN writes a subset of the active chain to the given writer.
func (c *Core) ExportN(w io.Writer, first uint64, last uint64) error {
	return c.sl.hc.ExportN(w, first, last)
}

// Snapshots returns the blockchain snapshot tree.
func (c *Core) Snapshots() *snapshot.Tree {
	return nil
}

func (c *Core) TxLookupLimit() uint64 {
	return 0
}

func (c *Core) SubscribeNewTxsEvent(ch chan<- NewTxsEvent) event.Subscription {
	return c.sl.txPool.SubscribeNewTxsEvent(ch)
}

func (c *Core) SetExtra(extra []byte) error {
	return c.sl.miner.SetExtra(extra)
}

//---------------//
// Miner methods //
//---------------//

func (c *Core) Miner() *Miner {
	return c.sl.Miner()
}

func (c *Core) Hashrate() uint64 {
	if pow, ok := c.sl.engine.(consensus.PoW); ok {
		return uint64(pow.Hashrate())
	}
	return 0
}

func (c *Core) SetRecommitInterval(interval time.Duration) {
	c.sl.miner.SetRecommitInterval(interval)
}

// SetGasCeil sets the gaslimit to strive for when mining blocks.
func (c *Core) SetGasCeil(ceil uint64) {
	c.sl.miner.SetGasCeil(ceil)
}

// EnablePreseal turns on the preseal mining feature. It's enabled by default.
// Note this function shouldn't be exposed to API, it's unnecessary for users
// (miners) to actually know the underlying detail. It's only for outside project
// which uses this library.
func (c *Core) EnablePreseal() {
	c.sl.miner.EnablePreseal()
}

// DisablePreseal turns off the preseal mining feature. It's necessary for some
// fake consensus engine which can seal blocks instantaneously.
// Note this function shouldn't be exposed to API, it's unnecessary for users
// (miners) to actually know the underlying detail. It's only for outside project
// which uses this library.
func (c *Core) DisablePreseal() {
	c.sl.miner.DisablePreseal()
}

func (c *Core) StopMining() {
	c.sl.miner.StopMining()
}

// Pending returns the currently pending block and associated state.
func (c *Core) Pending() *types.Block {
	return c.sl.miner.Pending()
}

// PendingBlock returns the currently pending block.
//
// Note, to access both the pending block and the pending state
// simultaneously, please use Pending(), as the pending state can
// change between multiple method calls
func (c *Core) PendingBlock() *types.Block {
	return c.sl.miner.PendingBlock()
}

// PendingBlockAndReceipts returns the currently pending block and corresponding receipts.
func (c *Core) PendingBlockAndReceipts() (*types.Block, types.Receipts) {
	return c.sl.miner.PendingBlockAndReceipts()
}

func (c *Core) SetEtherbase(addr common.Address) {
	c.sl.miner.SetEtherbase(addr)
}

// SubscribePendingLogs starts delivering logs from pending transactions
// to the given channel.
func (c *Core) SubscribePendingLogs(ch chan<- []*types.Log) event.Subscription {
	return c.sl.miner.worker.pendingLogsFeed.Subscribe(ch)
}

// SubscribePendingBlock starts delivering the pending block to the given channel.
func (c *Core) SubscribePendingHeader(ch chan<- *types.Header) event.Subscription {
	return c.sl.miner.SubscribePendingHeader(ch)
}

func (c *Core) IsMining() bool { return c.sl.miner.Mining() }

//-------------------------//
// State Processor methods //
//-------------------------//

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (c *Core) GetReceiptsByHash(hash common.Hash) types.Receipts {
	return c.sl.hc.bc.processor.GetReceiptsByHash(hash)
}

// GetVMConfig returns the block chain VM config.
func (c *Core) GetVMConfig() *vm.Config {
	return &c.sl.hc.bc.processor.vmConfig
}

// GetTransactionLookup retrieves the lookup associate with the given transaction
// hash from the cache or database.
func (c *Core) GetTransactionLookup(hash common.Hash) *rawdb.LegacyTxLookupEntry {
	return c.sl.hc.bc.processor.GetTransactionLookup(hash)
}

func (c *Core) HasBlockAndState(hash common.Hash, number uint64) bool {
	return c.Processor().HasBlockAndState(hash, number)
}

// ContractCode retrieves a blob of data associated with a contract hash
// either from ephemeral in-memory cache, or from persistent storage.
func (c *Core) ContractCode(hash common.Hash) ([]byte, error) {
	return c.sl.hc.bc.processor.ContractCode(hash)
}

// State returns a new mutable state based on the current HEAD block.
func (c *Core) State() (*state.StateDB, error) {
	return c.sl.hc.bc.processor.State()
}

// StateAt returns a new mutable state based on a particular point in time.
func (c *Core) StateAt(root common.Hash) (*state.StateDB, error) {
	return c.sl.hc.bc.processor.StateAt(root)
}

// StateCache returns the caching database underpinning the blockchain instance.
func (c *Core) StateCache() state.Database {
	return c.sl.hc.bc.processor.stateCache
}

// ContractCodeWithPrefix retrieves a blob of data associated with a contract
// hash either from ephemeral in-memory cache, or from persistent storage.
//
// If the code doesn't exist in the in-memory cache, check the storage with
// new code scheme.
func (c *Core) ContractCodeWithPrefix(hash common.Hash) ([]byte, error) {
	return c.sl.hc.bc.processor.ContractCodeWithPrefix(hash)
}
func (c *Core) StateAtBlock(block *types.Block, reexec uint64, base *state.StateDB, checkLive bool) (statedb *state.StateDB, err error) {
	return c.sl.hc.bc.processor.StateAtBlock(block, reexec, base, checkLive)
}

func (c *Core) StateAtTransaction(block *types.Block, txIndex int, reexec uint64) (Message, vm.BlockContext, *state.StateDB, error) {
	return c.sl.hc.bc.processor.StateAtTransaction(block, txIndex, reexec)
}

func (c *Core) TrieNode(hash common.Hash) ([]byte, error) {
	return c.sl.hc.bc.processor.TrieNode(hash)
}

//----------------//
// TxPool methods //
//----------------//

func (c *Core) SetGasPrice(price *big.Int) {
	c.sl.txPool.SetGasPrice(price)
}

func (c *Core) AddLocal(tx *types.Transaction) error {
	return c.sl.txPool.AddLocal(tx)
}

func (c *Core) TxPoolPending(enforceTips bool) (map[common.AddressBytes]types.Transactions, error) {
	return c.sl.txPool.TxPoolPending(enforceTips, nil)
}

func (c *Core) Get(hash common.Hash) *types.Transaction {
	return c.sl.txPool.Get(hash)
}

func (c *Core) Nonce(addr common.Address) uint64 {
	internal, err := addr.InternalAddress()
	if err != nil {
		return 0
	}
	return c.sl.txPool.Nonce(internal)
}

func (c *Core) Stats() (int, int) {
	return c.sl.txPool.Stats()
}

func (c *Core) Content() (map[common.InternalAddress]types.Transactions, map[common.InternalAddress]types.Transactions) {
	return c.sl.txPool.Content()
}

func (c *Core) ContentFrom(addr common.Address) (types.Transactions, types.Transactions) {
	internal, err := addr.InternalAddress()
	if err != nil {
		return nil, nil
	}
	return c.sl.txPool.ContentFrom(internal)
}
