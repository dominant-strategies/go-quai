package core

import (
	"errors"
	"io"
	"math/big"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	expireLru "github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
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
	"github.com/dominant-strategies/go-quai/trie"
)

const (
	c_maxAppendQueue                           = 3000 // Maximum number of future headers we can store in cache
	c_maxFutureTime                            = 30   // Max time into the future (in seconds) we will accept a block
	c_appendQueueRetryPeriod                   = 1    // Time (in seconds) before retrying to append from AppendQueue
	c_appendQueueThreshold                     = 200  // Number of blocks to load from the disk to ram on every proc of append queue
	c_processingCache                          = 10   // Number of block hashes held to prevent multi simultaneous appends on a single block hash
	c_primeRetryThreshold                      = 1800 // Number of times a block is retry to be appended before eviction from append queue in Prime
	c_regionRetryThreshold                     = 1200 // Number of times a block is retry to be appended before eviction from append queue in Region
	c_zoneRetryThreshold                       = 600  // Number of times a block is retry to be appended before eviction from append queue in Zone
	c_maxFutureBlocksPrime              uint64 = 3    // Number of blocks ahead of the current block to be put in the hashNumberList
	c_maxFutureBlocksRegion             uint64 = 150
	c_maxFutureBlocksZone               uint64 = 2000
	c_appendQueueRetryPriorityThreshold        = 5  // If retry counter for a block is less than this number,  then its put in the special list that is tried first to be appended
	c_appendQueueRemoveThreshold               = 10 // Number of blocks behind the block should be from the current header to be eligble for removal from the append queue
	c_normalListProcCounter                    = 1  // Ratio of Number of times the PriorityList is serviced over the NormalList
	c_statsPrintPeriod                         = 60 // Time between stats prints
	c_appendQueuePrintSize                     = 10
	c_normalListBackoffThreshold               = 5 // Max multiple on the c_normalListProcCounter
	c_maxRemoteTxQueue                         = 50000
	c_remoteTxProcPeriod                       = 2 // Time between remote tx pool processing
	c_asyncWorkShareTimer                      = 1 * time.Second
)

type blockNumberAndRetryCounter struct {
	number uint64
	retry  uint64
}

type Core struct {
	sl     *Slice
	engine consensus.Engine

	appendQueue     *lru.Cache[common.Hash, blockNumberAndRetryCounter]
	processingCache *expireLru.LRU[common.Hash, interface{}]
	remoteTxQueue   *lru.Cache[common.Hash, types.Transaction]

	writeBlockLock sync.RWMutex

	procCounter int

	normalListBackoff uint64 // normalListBackoff is the multiple on c_normalListProcCounter which delays the proc on normal list

	quit chan struct{} // core quit channel

	logger *log.Logger
}

func NewCore(db ethdb.Database, config *Config, isLocalBlock func(block *types.WorkObject) bool, txConfig *TxPoolConfig, txLookupLimit *uint64, chainConfig *params.ChainConfig, slicesRunning []common.Location, currentExpansionNumber uint8, genesisBlock *types.WorkObject, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis, logger *log.Logger) (*Core, error) {
	slice, err := NewSlice(db, config, txConfig, txLookupLimit, isLocalBlock, chainConfig, slicesRunning, currentExpansionNumber, genesisBlock, engine, cacheConfig, vmConfig, genesis, logger)
	if err != nil {
		return nil, err
	}

	c := &Core{
		sl:                slice,
		engine:            engine,
		quit:              make(chan struct{}),
		procCounter:       0,
		normalListBackoff: 1,
		logger:            logger,
	}

	// Initialize the sync target to current header parent entropy
	appendQueue, _ := lru.New[common.Hash, blockNumberAndRetryCounter](c_maxAppendQueue)
	c.appendQueue = appendQueue

	c.processingCache = expireLru.NewLRU[common.Hash, interface{}](c_processingCache, nil, time.Second*60)

	remoteTxQueue, _ := lru.New[common.Hash, types.Transaction](c_maxRemoteTxQueue)
	c.remoteTxQueue = remoteTxQueue

	go c.updateAppendQueue()
	go c.startStatsTimer()
	if c.NodeCtx() == common.ZONE_CTX && c.ProcessingState() {
		go c.startRemoteTxQueue()
	}

	return c, nil
}

// InsertChain attempts to append a list of blocks to the slice, optionally
// caching any pending blocks which cannot yet be appended. InsertChain return
// the number of blocks which were successfully consumed (either appended, or
// cached), and an error.
func (c *Core) InsertChain(blocks types.WorkObjects) (int, error) {
	nodeCtx := c.NodeCtx()
	for idx, block := range blocks {
		// Only attempt to append a block, if it is not coincident with our dominant
		// chain. If it is dom coincident, then the dom chain node in our slice needs
		// to initiate the append.
		_, order, err := c.CalcOrder(block)
		if err != nil {
			return idx, err
		}

		if order == nodeCtx {
			if !c.processingCache.Contains(block.Hash()) {
				c.processingCache.Add(block.Hash(), 1)
			} else {
				c.logger.WithFields(log.Fields{
					"Number": block.NumberArray(),
					"Hash":   block.Hash(),
				}).Info("Already processing block")
				return idx, errors.New("Already in process of appending this block")
			}
			newPendingEtxs, _, err := c.sl.Append(block, types.EmptyWorkObject(c.NodeCtx()), common.Hash{}, false, nil)
			c.processingCache.Remove(block.Hash())
			if err == nil {
				// If we have a dom, send the dom any pending ETXs which will become
				// referencable by this block. When this block is referenced in the dom's
				// subordinate block manifest, then ETXs produced by this block and the rollup
				// of ETXs produced by subordinate chain(s) will become referencable.
				if nodeCtx > common.PRIME_CTX {
					pendingEtx := types.PendingEtxs{Header: block.ConvertToPEtxView(), Etxs: newPendingEtxs}
					// Only send the pending Etxs to dom if valid, because in the case of running a slice, for the zones that the node doesn't run, it cannot have the etxs generated
					if pendingEtx.IsValid(trie.NewStackTrie(nil)) {
						if err := c.SendPendingEtxsToDom(pendingEtx); err != nil {
							c.logger.WithFields(log.Fields{
								"blockHash": block.Hash(),
								"err":       err,
							}).Error("failed to send ETXs to domclient")
						}
					}
				}
				c.removeFromAppendQueue(block)
			} else if err.Error() == ErrKnownBlock.Error() {
				c.removeFromAppendQueue(block)
			} else if err.Error() == consensus.ErrFutureBlock.Error() ||
				err.Error() == ErrBodyNotFound.Error() ||
				err.Error() == ErrPendingEtxNotFound.Error() ||
				err.Error() == consensus.ErrPrunedAncestor.Error() ||
				err.Error() == consensus.ErrUnknownAncestor.Error() ||
				err.Error() == ErrSubNotSyncedToDom.Error() ||
				err.Error() == ErrDomClientNotUp.Error() {
				if c.sl.CurrentInfo(block) {
					c.logger.WithFields(log.Fields{
						"Number": block.NumberArray(),
						"Hash":   block.Hash(),
						"err":    err,
					}).Info("Cannot append yet.")
				} else {
					c.logger.WithFields(log.Fields{
						"loc":    c.NodeLocation().Name(),
						"Number": block.NumberArray(),
						"Hash":   block.Hash(),
						"err":    err,
					}).Debug("Cannot append yet.")
				}
				if err.Error() == ErrSubNotSyncedToDom.Error() ||
					err.Error() == ErrPendingEtxNotFound.Error() {
					if nodeCtx != common.ZONE_CTX && c.sl.subInterface[block.Location().SubIndex(c.NodeCtx())] != nil {
						c.sl.subInterface[block.Location().SubIndex(c.NodeCtx())].DownloadBlocksInManifest(block.Hash(), block.Manifest(), block.ParentEntropy(nodeCtx))
					}
				}
				return idx, ErrPendingBlock
			} else if err.Error() != ErrKnownBlock.Error() {
				c.logger.WithFields(log.Fields{
					"Hash": block.Hash(),
					"err":  err,
				}).Info("Append failed.")
			}
			if err != nil && strings.Contains(err.Error(), "connection refused") {
				c.logger.Error("Append failed because of connection refused error")
			} else {
				c.removeFromAppendQueue(block)
			}
		}
	}
	return len(blocks), nil
}

// procAppendQueue sorts the append queue and attempts to append
func (c *Core) procAppendQueue() {
	nodeCtx := c.NodeLocation().Context()

	maxFutureBlocks := c_maxFutureBlocksPrime
	if nodeCtx == common.REGION_CTX {
		maxFutureBlocks = c_maxFutureBlocksRegion
	} else if nodeCtx == common.ZONE_CTX {
		maxFutureBlocks = c_maxFutureBlocksZone
	}

	// Sort the blocks by number and retry attempts and try to insert them
	// blocks will be aged out of the append queue after the retry threhsold
	var hashNumberList []types.HashAndNumber
	var hashNumberPriorityList []types.HashAndNumber
	for _, hash := range c.appendQueue.Keys() {
		if value, exist := c.appendQueue.Peek(hash); exist {
			hashNumber := types.HashAndNumber{Hash: hash, Number: value.number}
			if hashNumber.Number < c.CurrentHeader().NumberU64(nodeCtx)+maxFutureBlocks {
				if value.retry < c_appendQueueRetryPriorityThreshold {
					hashNumberPriorityList = append(hashNumberPriorityList, hashNumber)
				} else {
					hashNumberList = append(hashNumberList, hashNumber)
				}
			}
		}
	}

	c.serviceBlocks(hashNumberPriorityList)
	if len(hashNumberPriorityList) > 0 {
		c.logger.WithFields(log.Fields{
			"len":        len(hashNumberPriorityList),
			"firstEntry": hashNumberPriorityList[0].Number,
			"lastEntry":  hashNumberPriorityList[len(hashNumberPriorityList)-1].Number,
		}).Info("Size of hashNumberPriorityList")
	}

	normalListProcCounter := c.normalListBackoff * c_normalListProcCounter
	if len(c.appendQueue.Keys()) < c_appendQueueThreshold || c.procCounter%int(normalListProcCounter) == 0 {
		c.procCounter = 0
		c.serviceBlocks(hashNumberList)
		if len(hashNumberList) > 0 {
			c.logger.WithFields(log.Fields{
				"len":        len(hashNumberList),
				"firstEntry": hashNumberList[0].Number,
				"lastEntry":  hashNumberList[len(hashNumberList)-1].Number,
			}).Info("Size of hashNumberList")
		}
	}
	c.procCounter++
}

func (c *Core) serviceBlocks(hashNumberList []types.HashAndNumber) {
	sort.Slice(hashNumberList, func(i, j int) bool {
		return hashNumberList[i].Number < hashNumberList[j].Number
	})

	var retryThreshold uint64
	switch c.NodeLocation().Context() {
	case common.PRIME_CTX:
		retryThreshold = c_primeRetryThreshold
	case common.REGION_CTX:
		retryThreshold = c_regionRetryThreshold
	case common.ZONE_CTX:
		retryThreshold = c_zoneRetryThreshold
	}

	// Attempt to service the sorted list
	for i, hashAndNumber := range hashNumberList {
		block := c.GetBlockOrCandidateByHash(hashAndNumber.Hash)
		if block != nil {
			var numberAndRetryCounter blockNumberAndRetryCounter
			if value, exist := c.appendQueue.Peek(block.Hash()); exist {
				numberAndRetryCounter = value
				numberAndRetryCounter.retry += 1
				if numberAndRetryCounter.retry > retryThreshold && numberAndRetryCounter.number+c_appendQueueRemoveThreshold < c.CurrentHeader().NumberU64(c.NodeCtx()) {
					c.appendQueue.Remove(block.Hash())
				} else {
					c.appendQueue.Add(block.Hash(), numberAndRetryCounter)
				}
			}
			parentBlock := c.sl.hc.GetBlockOrCandidate(block.ParentHash(c.NodeCtx()), block.NumberU64(c.NodeCtx())-1)
			if parentBlock != nil {
				// If parent header is dom, send a signal to dom to request for the block if it doesnt have it
				_, parentHeaderOrder, err := c.CalcOrder(parentBlock)
				if err != nil {
					c.logger.WithFields(log.Fields{
						"Hash":   parentBlock.Hash(),
						"Number": parentBlock.NumberArray(),
					}).Info("Error calculating the parent block order in serviceBlocks")
					continue
				}
				nodeCtx := c.NodeLocation().Context()
				if parentHeaderOrder < nodeCtx && c.GetHeaderByHash(parentBlock.Hash()) == nil {
					c.logger.WithFields(log.Fields{
						"Hash":  parentBlock.Hash(),
						"Order": parentHeaderOrder,
					}).Info("Requesting the dom to get the block if it doesnt have and try to append")
					if c.sl.domInterface != nil {
						// send a signal to the required dom to fetch the block if it doesnt have it, or its not in its appendqueue
						go c.sl.domInterface.RequestDomToAppendOrFetch(parentBlock.Hash(), parentBlock.ParentEntropy(c.NodeCtx()), parentHeaderOrder)
					}
				}
				c.addToQueueIfNotAppended(parentBlock)
				_, err = c.InsertChain([]*types.WorkObject{block})
				if err != nil && err.Error() == ErrPendingBlock.Error() {
					// Best check here would be to check the first hash in each Fork, until we do that
					// checking the first item in the sorted hashNumberList will do
					if i == 0 && c.normalListBackoff < c_normalListBackoffThreshold {
						c.normalListBackoff++
					}
				} else {
					c.normalListBackoff = 1
				}
			} else {
				c.sl.missingBlockFeed.Send(types.BlockRequest{Hash: block.ParentHash(c.NodeCtx()), Entropy: block.ParentEntropy(c.NodeCtx())})
			}
		} else {
			c.logger.WithField("hash", hashAndNumber.Hash).Info("Entry in the FH cache without being in the db")
		}
	}
}

func (c *Core) RequestDomToAppendOrFetch(hash common.Hash, entropy *big.Int, order int) {
	// TODO: optimize to check if the block is in the appendqueue or already
	// appended to reduce the network bandwidth utilization
	nodeCtx := c.NodeLocation().Context()
	if nodeCtx == common.PRIME_CTX {
		// If prime all you can do it to ask for the block
		_, exists := c.appendQueue.Get(hash)
		if !exists {
			c.logger.WithFields(log.Fields{
				"Hash":  hash,
				"Order": order,
			}).Debug("Block sub asked doesnt exist in append queue, so request the peers for it")
			block := c.GetBlockOrCandidateByHash(hash)
			if block == nil {
				c.sl.missingBlockFeed.Send(types.BlockRequest{Hash: hash, Entropy: entropy}) // Using the missing parent feed to ask for the block
			} else {
				c.addToQueueIfNotAppended(block)
			}
		}
	} else if nodeCtx == common.REGION_CTX {
		if order < nodeCtx { // Prime block
			if c.sl.domInterface != nil {
				go c.sl.domInterface.RequestDomToAppendOrFetch(hash, entropy, order)
			}
		}
		_, exists := c.appendQueue.Get(hash)
		if !exists {
			c.logger.WithFields(log.Fields{
				"Hash":  hash,
				"Order": order,
			}).Debug("Block sub asked doesnt exist in append queue, so request the peers for it")
			block := c.GetBlockByHash(hash)
			if block == nil {
				c.sl.missingBlockFeed.Send(types.BlockRequest{Hash: hash, Entropy: entropy}) // Using the missing parent feed to ask for the block
			} else {
				c.addToQueueIfNotAppended(block)
			}
		}
	}

}

// addToQueueIfNotAppended checks if block is appended and if its not adds the block to appendqueue
func (c *Core) addToQueueIfNotAppended(block *types.WorkObject) {
	// Check if the hash is in the blockchain, otherwise add it to the append queue
	if c.GetHeaderByHash(block.Hash()) == nil {
		c.addToAppendQueue(block)
	}
}

// addToAppendQueue adds a block to the append queue
func (c *Core) addToAppendQueue(block *types.WorkObject) error {
	nodeCtx := c.NodeLocation().Context()
	_, order, err := c.CalcOrder(block)
	if err != nil {
		return err
	}
	if order == nodeCtx {
		c.appendQueue.ContainsOrAdd(block.Hash(), blockNumberAndRetryCounter{block.NumberU64(c.NodeCtx()), 0})
	}
	return nil
}

// removeFromAppendQueue removes a block from the append queue
func (c *Core) removeFromAppendQueue(block *types.WorkObject) {
	c.appendQueue.Remove(block.Hash())
}

// updateAppendQueue is a time to procAppendQueue
func (c *Core) updateAppendQueue() {
	defer func() {
		if r := recover(); r != nil {
			c.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
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

// Insert remotes into the tx pool
func (c *Core) startRemoteTxQueue() {
	defer func() {
		if r := recover(); r != nil {
			c.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	futureTimer := time.NewTicker(c_remoteTxProcPeriod * time.Second)
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			txs := make([]*types.Transaction, 0, c.remoteTxQueue.Len())
			for _, tx := range c.remoteTxQueue.Keys() {
				if value, exist := c.remoteTxQueue.Peek(tx); exist {
					txs = append(txs, &value)
					c.remoteTxQueue.Remove(tx)
				}
			}
			c.sl.txPool.AddRemotes(txs)
		case <-c.quit:
			return
		}
	}
}

func (c *Core) startStatsTimer() {
	futureTimer := time.NewTicker(c_statsPrintPeriod * time.Second)
	defer futureTimer.Stop()
	defer func() {
		if r := recover(); r != nil {
			c.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case <-futureTimer.C:
			c.printStats()
		case <-c.quit:
			return
		}
	}
}

// printStats displays stats on syncing, latestHeight, etc.
func (c *Core) printStats() {
	c.logger.WithFields(log.Fields{
		"loc":              c.NodeLocation().Name(),
		"len(appendQueue)": len(c.appendQueue.Keys()),
	}).Info("Blocks waiting to be appended")

	// Print hashes & heights of all queue entries.
	for _, hash := range c.appendQueue.Keys()[:math.Min(len(c.appendQueue.Keys()), c_appendQueuePrintSize)] {
		if value, exist := c.appendQueue.Peek(hash); exist {
			hashNumber := types.HashAndNumber{Hash: hash, Number: value.number}
			c.logger.WithFields(log.Fields{
				"Number": strconv.FormatUint(hashNumber.Number, 10),
				"Hash":   hashNumber.Hash.String(),
			}).Debug("AppendQueue entry")
		}
	}

}

func (c *Core) BadHashExistsInChain() bool {
	nodeCtx := c.NodeLocation().Context()
	// Lookup the bad hashes list to see if we have it in the database
	for _, fork := range BadHashes {
		switch nodeCtx {
		case common.PRIME_CTX:
			if c.GetBlockByHash(fork.PrimeContext) != nil {
				return true
			}
		case common.REGION_CTX:
			if c.GetBlockByHash(fork.RegionContext[c.NodeLocation().Region()]) != nil {
				return true
			}
		case common.ZONE_CTX:
			if c.GetBlockByHash(fork.ZoneContext[c.NodeLocation().Region()][c.NodeLocation().Zone()]) != nil {
				return true
			}
		}
	}
	return false
}

func (c *Core) SubscribeMissingBlockEvent(ch chan<- types.BlockRequest) event.Subscription {
	return c.sl.SubscribeMissingBlockEvent(ch)
}

// InsertChainWithoutSealVerification works exactly the same
// except for seal verification, seal verification is omitted
func (c *Core) InsertChainWithoutSealVerification(block *types.WorkObject) (int, error) {
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

func (c *Core) CheckIfValidWorkShare(workShare *types.WorkObjectHeader) bool {
	return c.engine.CheckIfValidWorkShare(workShare)
}

// Slice retrieves the slice struct.
func (c *Core) Slice() *Slice {
	return c.sl
}

func (c *Core) TxPool() *TxPool {
	return c.sl.txPool
}

func (c *Core) GetTxsFromBroadcastSet(hash common.Hash) (types.Transactions, error) {
	return c.sl.GetTxsFromBroadcastSet(hash)
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
func (c *Core) WriteBlock(block *types.WorkObject) {
	nodeCtx := c.NodeCtx()

	if block.Location() == nil {
		c.logger.Errorf("Block %d has nil location in %d context", block.NumberU64(c.sl.NodeCtx()), c.NodeCtx())
		return
	}

	if c.sl.IsBlockHashABadHash(block.Hash()) {
		return
	}
	if c.GetHeaderByHash(block.Hash()) == nil {
		// Only add non dom blocks to the append queue
		_, order, err := c.CalcOrder(block)
		if err != nil {
			return
		}
		if order == nodeCtx {
			parentHeader := c.GetHeaderByHash(block.ParentHash(nodeCtx))
			if parentHeader != nil {
				c.sl.WriteBlock(block)
				go c.InsertChain([]*types.WorkObject{block})
			}
			c.addToAppendQueue(block)
			// If a dom block comes in and we havent appended it yet
		} else if order < nodeCtx && c.GetHeaderByHash(block.Hash()) == nil {
			if c.sl.domInterface != nil {
				go c.sl.domInterface.RequestDomToAppendOrFetch(block.Hash(), block.ParentEntropy(nodeCtx), order)
			}
		}
	}

	if c.GetHeaderOrCandidateByHash(block.Hash()) == nil {
		c.sl.WriteBlock(block)
	}

}

func (c *Core) Append(header *types.WorkObject, manifest types.BlockManifest, domPendingHeader *types.WorkObject, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, bool, error) {
	nodeCtx := c.NodeCtx()
	// Set the coinbase into the right interface before calling append in the sub
	header.WorkObjectHeader().SetCoinbase(common.BytesToAddress(header.Coinbase().Bytes(), c.NodeLocation()))
	newPendingEtxs, setHead, err := c.sl.Append(header, domPendingHeader, domTerminus, domOrigin, newInboundEtxs)
	if err != nil {
		if err.Error() == ErrBodyNotFound.Error() || err.Error() == consensus.ErrUnknownAncestor.Error() || err.Error() == ErrSubNotSyncedToDom.Error() {
			// Fetch the blocks for each hash in the manifest
			block := c.GetBlockOrCandidateByHash(header.Hash())
			if block == nil {
				c.sl.missingBlockFeed.Send(types.BlockRequest{Hash: header.Hash(), Entropy: header.ParentEntropy(nodeCtx)})
			} else {
				c.addToQueueIfNotAppended(block)
			}
			for _, m := range manifest {
				block := c.GetBlockOrCandidateByHash(m)
				if block == nil {
					c.sl.missingBlockFeed.Send(types.BlockRequest{Hash: m, Entropy: header.ParentEntropy(nodeCtx)})
				} else {
					c.addToQueueIfNotAppended(block)
				}
			}
			block = c.GetBlockOrCandidateByHash(header.ParentHash(nodeCtx))
			if block == nil {
				c.sl.missingBlockFeed.Send(types.BlockRequest{Hash: header.ParentHash(nodeCtx), Entropy: header.ParentEntropy(nodeCtx)})
			} else {
				c.addToQueueIfNotAppended(block)
			}
		}
	}
	return newPendingEtxs, setHead, err
}

func (c *Core) DownloadBlocksInManifest(blockHash common.Hash, manifest types.BlockManifest, entropy *big.Int) {
	// Fetch the blocks for each hash in the manifest
	for _, m := range manifest {
		block := c.GetBlockOrCandidateByHash(m)
		if block == nil {
			c.sl.missingBlockFeed.Send(types.BlockRequest{Hash: m, Entropy: entropy})
		} else {
			c.addToQueueIfNotAppended(block)
		}
	}
	if c.NodeLocation().Context() == common.REGION_CTX {
		block := c.GetBlockOrCandidateByHash(blockHash)
		if block != nil {
			// If a prime block comes in
			if c.sl.subInterface[block.Location().SubIndex(c.NodeCtx())] != nil {
				c.sl.subInterface[block.Location().SubIndex(c.NodeCtx())].DownloadBlocksInManifest(block.Hash(), block.Manifest(), block.ParentEntropy(c.NodeCtx()))
			}
		}
	}
}

// ConstructLocalBlock takes a header and construct the Block locally
func (c *Core) ConstructLocalMinedBlock(woHeader *types.WorkObject) (*types.WorkObject, error) {
	return c.sl.ConstructLocalMinedBlock(woHeader)
}

func (c *Core) GetPendingBlockBody(woHeader *types.WorkObjectHeader) *types.WorkObject {
	return c.sl.GetPendingBlockBody(woHeader)
}

func (c *Core) SubRelayPendingHeader(slPendingHeader types.PendingHeader, newEntropy *big.Int, location common.Location, subReorg bool, order int, updateDomLocation common.Location) {
	c.sl.SubRelayPendingHeader(slPendingHeader, newEntropy, location, subReorg, order, updateDomLocation)
}

func (c *Core) UpdateDom(oldDomReference common.Hash, pendingHeader *types.WorkObject, location common.Location) {
	c.sl.UpdateDom(oldDomReference, pendingHeader, location)
}

func (c *Core) NewGenesisPendigHeader(pendingHeader *types.WorkObject, domTerminus common.Hash, genesisHash common.Hash) error {
	return c.sl.NewGenesisPendingHeader(pendingHeader, domTerminus, genesisHash)
}

func (c *Core) SetCurrentExpansionNumber(expansionNumber uint8) {
	c.sl.SetCurrentExpansionNumber(expansionNumber)
}

func (c *Core) WriteGenesisBlock(block *types.WorkObject, location common.Location) {
	c.sl.WriteGenesisBlock(block, location)
}

func (c *Core) GetPendingHeader() (*types.WorkObject, error) {
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

func (c *Core) GetPendingEtxsRollup(hash common.Hash, location common.Location) *types.PendingEtxsRollup {
	return rawdb.ReadPendingEtxsRollup(c.sl.sliceDb, hash)
}

func (c *Core) GetPendingEtxsRollupFromSub(hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	return c.sl.GetPendingEtxsRollupFromSub(hash, location)
}

func (c *Core) GetPendingEtxsFromSub(hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	return c.sl.GetPendingEtxsFromSub(hash, location)
}

func (c *Core) HasPendingEtxs(hash common.Hash) bool {
	return c.GetPendingEtxs(hash) != nil
}

func (c *Core) SendPendingEtxsToDom(pEtxs types.PendingEtxs) error {
	return c.sl.SendPendingEtxsToDom(pEtxs)
}

func (c *Core) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	return c.sl.AddPendingEtxs(pEtxs)
}

func (c *Core) AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error {
	return c.sl.AddPendingEtxsRollup(pEtxsRollup)
}

func (c *Core) GenerateRecoveryPendingHeader(pendingHeader *types.WorkObject, checkpointHashes types.Termini) error {
	return c.sl.GenerateRecoveryPendingHeader(pendingHeader, checkpointHashes)
}

func (c *Core) IsBlockHashABadHash(hash common.Hash) bool {
	return c.sl.IsBlockHashABadHash(hash)
}

func (c *Core) ProcessingState() bool {
	return c.sl.ProcessingState()
}

func (c *Core) NodeLocation() common.Location {
	return c.sl.NodeLocation()
}

func (c *Core) NodeCtx() int {
	return c.sl.NodeCtx()
}

func (c *Core) GetSlicesRunning() []common.Location {
	return c.sl.GetSlicesRunning()
}

func (c *Core) SetSubInterface(subInterface CoreBackend, location common.Location) {
	c.sl.SetSubInterface(subInterface, location)
}

func (c *Core) AddGenesisPendingEtxs(block *types.WorkObject) {
	c.sl.AddGenesisPendingEtxs(block)
}

func (c *Core) SubscribeExpansionEvent(ch chan<- ExpansionEvent) event.Subscription {
	return c.sl.SubscribeExpansionEvent(ch)
}

func (c *Core) SetDomInterface(domInterface CoreBackend) {
	c.sl.SetDomInterface(domInterface)
}

func (c *Core) SanityCheckWorkObjectBlockViewBody(wo *types.WorkObject) error {
	return c.sl.validator.SanityCheckWorkObjectBlockViewBody(wo)
}

func (c *Core) SanityCheckWorkObjectHeaderViewBody(wo *types.WorkObject) error {
	return c.sl.validator.SanityCheckWorkObjectHeaderViewBody(wo)
}

func (c *Core) SanityCheckWorkObjectShareViewBody(wo *types.WorkObject) error {
	return c.sl.validator.SanityCheckWorkObjectShareViewBody(wo)
}

//---------------------//
// HeaderChain methods //
//---------------------//

// GetBlock retrieves a block from the database by hash and number,
// caching it if found.
func (c *Core) GetBlock(hash common.Hash, number uint64) *types.WorkObject {
	return c.sl.hc.GetBlock(hash, number)
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (c *Core) GetBlockByHash(hash common.Hash) *types.WorkObject {
	return c.sl.hc.GetBlockByHash(hash)
}

// GetBlockOrCandidateByHash retrieves a block from the database by hash, caching it if found.
func (c *Core) GetBlockOrCandidateByHash(hash common.Hash) *types.WorkObject {
	return c.sl.hc.GetBlockOrCandidateByHash(hash)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (c *Core) GetHeaderByNumber(number uint64) *types.WorkObject {
	return c.sl.hc.GetHeaderByNumber(number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (c *Core) GetBlockByNumber(number uint64) *types.WorkObject {
	return c.sl.hc.GetBlockByNumber(number)
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (c *Core) GetBlocksFromHash(hash common.Hash, n int) []*types.WorkObject {
	return c.sl.hc.GetBlocksFromHash(hash, n)
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (c *Core) GetUnclesInChain(block *types.WorkObject, length int) []*types.WorkObjectHeader {
	return c.sl.hc.GetUnclesInChain(block, length)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (c *Core) GetGasUsedInChain(block *types.WorkObject, length int) int64 {
	return c.sl.hc.GetGasUsedInChain(block, length)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (c *Core) CalculateBaseFee(header *types.WorkObject) *big.Int {
	return c.sl.hc.CalculateBaseFee(header)
}

// CurrentBlock returns the block for the current header.
func (c *Core) CurrentBlock() *types.WorkObject {
	return c.sl.hc.CurrentBlock()
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (c *Core) CurrentHeader() *types.WorkObject {
	return c.sl.hc.CurrentHeader()
}

// CurrentLogEntropy returns the logarithm of the total entropy reduction since genesis for our current head block
func (c *Core) CurrentLogEntropy() *big.Int {
	return c.engine.TotalLogS(c, c.sl.hc.CurrentHeader())
}

// TotalLogS returns the total entropy reduction if the chain since genesis to the given header
func (c *Core) TotalLogS(header *types.WorkObject) *big.Int {
	return c.engine.TotalLogS(c, header)
}

// CalcOrder returns the order of the block within the hierarchy of chains
func (c *Core) CalcOrder(header *types.WorkObject) (*big.Int, int, error) {
	return c.engine.CalcOrder(c, header)
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (c *Core) GetHeaderByHash(hash common.Hash) *types.WorkObject {
	return c.sl.hc.GetHeaderByHash(hash)
}

// GetHeaderOrCandidateByHash retrieves a block header from the database by hash, caching it if
// found.
func (c *Core) GetHeaderOrCandidateByHash(hash common.Hash) *types.WorkObject {
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
func (c *Core) Genesis() *types.WorkObject {
	return c.GetBlockByHash(c.sl.hc.genesisHeader.Hash())
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (c *Core) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return c.sl.hc.SubscribeChainHeadEvent(ch)
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (c *Core) GetBody(hash common.Hash) *types.WorkObject {
	return c.sl.hc.GetBody(hash)
}

// GetTerminiByHash retrieves the termini stored for a given header hash
func (c *Core) GetTerminiByHash(hash common.Hash) *types.Termini {
	return c.sl.hc.GetTerminiByHash(hash)
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (c *Core) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return c.sl.hc.SubscribeChainSideEvent(ch)
}

// ComputeEfficiencyScore computes the efficiency score for the given prime
// block This data is is only valid if called from Prime context, otherwise
// there is no guarantee for this data to be accurate
func (c *Core) ComputeEfficiencyScore(header *types.WorkObject) uint16 {
	return c.sl.hc.ComputeEfficiencyScore(header)
}

// IsGenesisHash checks if a hash is the genesis block hash.
func (c *Core) IsGenesisHash(hash common.Hash) bool {
	return c.sl.hc.IsGenesisHash(hash)
}

func (c *Core) GetExpansionNumber() uint8 {
	return c.sl.hc.GetExpansionNumber()
}

func (c *Core) UpdateEtxEligibleSlices(header *types.WorkObject, location common.Location) common.Hash {
	return c.sl.hc.UpdateEtxEligibleSlices(header, location)
}

func (c *Core) GetPrimeTerminus(header *types.WorkObject) *types.WorkObject {
	return c.sl.hc.GetPrimeTerminus(header)
}

func (c *Core) CheckIfEtxIsEligible(etxEligibleSlices common.Hash, location common.Location) bool {
	return c.sl.hc.CheckIfEtxIsEligible(etxEligibleSlices, location)
}

func (c *Core) WriteAddressOutpoints(outpoints map[string]map[string]*types.OutpointAndDenomination) error {
	return c.sl.hc.WriteAddressOutpoints(outpoints)
}

func (c *Core) GetMaxTxInWorkShare() uint64 {
	return c.sl.hc.GetMaxTxInWorkShare()
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

func (c *Core) SetExtra(extra []byte) error {
	return c.sl.miner.SetExtra(extra)
}

//---------------//
// Miner methods //
//---------------//

func (c *Core) Miner() *Miner {
	return c.sl.Miner()
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
func (c *Core) Pending() *types.WorkObject {
	return c.sl.miner.Pending()
}

// PendingBlock returns the currently pending block.
//
// Note, to access both the pending block and the pending state
// simultaneously, please use Pending(), as the pending state can
// change between multiple method calls
func (c *Core) PendingBlock() *types.WorkObject {
	return c.sl.miner.PendingBlock()
}

// PendingBlockAndReceipts returns the currently pending block and corresponding receipts.
func (c *Core) PendingBlockAndReceipts() (*types.WorkObject, types.Receipts) {
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
func (c *Core) SubscribePendingHeader(ch chan<- *types.WorkObject) event.Subscription {
	return c.sl.miner.SubscribePendingHeader(ch)
}

func (c *Core) IsMining() bool { return c.sl.miner.Mining() }

func (c *Core) SendWorkShare(workShare *types.WorkObjectHeader) error {
	return c.sl.miner.worker.AddWorkShare(workShare)
}

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
func (c *Core) StateAt(root, utxoRoot, etxRoot common.Hash, quaiStateSize *big.Int) (*state.StateDB, error) {
	return c.sl.hc.bc.processor.StateAt(root, utxoRoot, etxRoot, quaiStateSize)
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
func (c *Core) StateAtBlock(block *types.WorkObject, reexec uint64, base *state.StateDB, checkLive bool) (statedb *state.StateDB, err error) {
	return c.sl.hc.bc.processor.StateAtBlock(block, reexec, base, checkLive)
}

func (c *Core) StateAtTransaction(block *types.WorkObject, txIndex int, reexec uint64) (Message, vm.BlockContext, *state.StateDB, error) {
	return c.sl.hc.bc.processor.StateAtTransaction(block, txIndex, reexec)
}

func (c *Core) TrieNode(hash common.Hash) ([]byte, error) {
	return c.sl.hc.bc.processor.TrieNode(hash)
}

func (c *Core) GetOutpointsByAddress(address common.Address) map[string]*types.OutpointAndDenomination {
	return rawdb.ReadOutpointsForAddress(c.sl.sliceDb, address.Hex())
}

func (c *Core) GetUTXOsByAddressAtState(state *state.StateDB, address common.Address) ([]*types.UtxoEntry, error) {
	outpointsForAddress := c.GetOutpointsByAddress(address)
	utxos := make([]*types.UtxoEntry, 0, len(outpointsForAddress))

	for _, outpoint := range outpointsForAddress {
		entry := state.GetUTXO(outpoint.TxHash, outpoint.Index)
		if entry == nil {
			return nil, errors.New("failed to get UTXO for address")
		}
		utxos = append(utxos, entry)
	}

	return utxos, nil
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

func (c *Core) AddRemote(tx *types.Transaction) {
	c.remoteTxQueue.Add(tx.Hash(), *tx)
}

func (c *Core) AddRemotes(txs types.Transactions) {
	for _, tx := range txs {
		c.remoteTxQueue.Add(tx.Hash(), *tx)
	}
}

func (c *Core) TxPoolPending(enforceTips bool) (map[common.AddressBytes]types.Transactions, error) {
	return c.sl.txPool.TxPoolPending(enforceTips)
}

func (c *Core) Get(hash common.Hash) *types.Transaction {
	return c.sl.txPool.Get(hash)
}

func (c *Core) Nonce(addr common.Address) uint64 {
	internal, err := addr.InternalAndQuaiAddress()
	if err != nil {
		return 0
	}
	return c.sl.txPool.Nonce(internal)
}

func (c *Core) Stats() (int, int, int) {
	return c.sl.txPool.Stats()
}

func (c *Core) Content() (map[common.InternalAddress]types.Transactions, map[common.InternalAddress]types.Transactions) {
	return c.sl.txPool.Content()
}

func (c *Core) ContentFrom(addr common.Address) (types.Transactions, types.Transactions) {
	internal, err := addr.InternalAndQuaiAddress()
	if err != nil {
		return nil, nil
	}
	return c.sl.txPool.ContentFrom(internal)
}
