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
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	lru "github.com/hashicorp/golang-lru"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/common/mclock"
	"github.com/spruce-solutions/go-quai/common/prque"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/consensus/misc"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/state"
	"github.com/spruce-solutions/go-quai/core/state/snapshot"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/event"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/metrics"
	"github.com/spruce-solutions/go-quai/params"
	"github.com/spruce-solutions/go-quai/rlp"
	"github.com/spruce-solutions/go-quai/trie"
)

var (
	headBlockGauge     = metrics.NewRegisteredGauge("chain/head/block", nil)
	headHeaderGauge    = metrics.NewRegisteredGauge("chain/head/header", nil)
	headFastBlockGauge = metrics.NewRegisteredGauge("chain/head/receipt", nil)

	accountReadTimer   = metrics.NewRegisteredTimer("chain/account/reads", nil)
	accountHashTimer   = metrics.NewRegisteredTimer("chain/account/hashes", nil)
	accountUpdateTimer = metrics.NewRegisteredTimer("chain/account/updates", nil)
	accountCommitTimer = metrics.NewRegisteredTimer("chain/account/commits", nil)

	storageReadTimer   = metrics.NewRegisteredTimer("chain/storage/reads", nil)
	storageHashTimer   = metrics.NewRegisteredTimer("chain/storage/hashes", nil)
	storageUpdateTimer = metrics.NewRegisteredTimer("chain/storage/updates", nil)
	storageCommitTimer = metrics.NewRegisteredTimer("chain/storage/commits", nil)

	snapshotAccountReadTimer = metrics.NewRegisteredTimer("chain/snapshot/account/reads", nil)
	snapshotStorageReadTimer = metrics.NewRegisteredTimer("chain/snapshot/storage/reads", nil)
	snapshotCommitTimer      = metrics.NewRegisteredTimer("chain/snapshot/commits", nil)

	blockInsertTimer     = metrics.NewRegisteredTimer("chain/inserts", nil)
	blockValidationTimer = metrics.NewRegisteredTimer("chain/validation", nil)
	blockExecutionTimer  = metrics.NewRegisteredTimer("chain/execution", nil)
	blockWriteTimer      = metrics.NewRegisteredTimer("chain/write", nil)

	blockReorgMeter         = metrics.NewRegisteredMeter("chain/reorg/executes", nil)
	blockReorgAddMeter      = metrics.NewRegisteredMeter("chain/reorg/add", nil)
	blockReorgDropMeter     = metrics.NewRegisteredMeter("chain/reorg/drop", nil)
	blockReorgInvalidatedTx = metrics.NewRegisteredMeter("chain/reorg/invalidTx", nil)

	blockPrefetchExecuteTimer   = metrics.NewRegisteredTimer("chain/prefetch/executes", nil)
	blockPrefetchInterruptMeter = metrics.NewRegisteredMeter("chain/prefetch/interrupts", nil)

	errInsertionInterrupted = errors.New("insertion is interrupted")
	errChainStopped         = errors.New("blockchain is stopped")
	errExtBlockNotFound     = errors.New("error finding external block by context and hash")
)

const (
	bodyCacheLimit      = 256
	blockCacheLimit     = 256
	receiptsCacheLimit  = 32
	txLookupCacheLimit  = 1024
	maxFutureBlocks     = 256
	maxTimeFutureBlocks = 30
	TriesInMemory       = 128
	extBlockQueueLimit  = 1024

	// BlockChainVersion ensures that an incompatible database forces a resync from scratch.
	//
	// Changelog:
	//
	// - Version 4
	//   The following incompatible database changes were added:
	//   * the `BlockNumber`, `TxHash`, `TxIndex`, `BlockHash` and `Index` fields of log are deleted
	//   * the `Bloom` field of receipt is deleted
	//   * the `BlockIndex` and `TxIndex` fields of txlookup are deleted
	// - Version 5
	//  The following incompatible database changes were added:
	//    * the `TxHash`, `GasCost`, and `ContractAddress` fields are no longer stored for a receipt
	//    * the `TxHash`, `GasCost`, and `ContractAddress` fields are computed by looking up the
	//      receipts' corresponding block
	// - Version 6
	//  The following incompatible database changes were added:
	//    * Transaction lookup information stores the corresponding block number instead of block hash
	// - Version 7
	//  The following incompatible database changes were added:
	//    * Use freezer as the ancient database to maintain all ancient data
	// - Version 8
	//  The following incompatible database changes were added:
	//    * New scheme for contract code in order to separate the codes and trie nodes
	BlockChainVersion uint64 = 8
)

// CacheConfig contains the configuration values for the trie caching/pruning
// that's resident in a blockchain.
type CacheConfig struct {
	TrieCleanLimit      int           // Memory allowance (MB) to use for caching trie nodes in memory
	TrieCleanJournal    string        // Disk journal for saving clean cache entries.
	TrieCleanRejournal  time.Duration // Time interval to dump clean cache to disk periodically
	TrieCleanNoPrefetch bool          // Whether to disable heuristic state prefetching for followup blocks
	TrieDirtyLimit      int           // Memory limit (MB) at which to start flushing dirty trie nodes to disk
	TrieDirtyDisabled   bool          // Whether to disable trie write caching and GC altogether (archive node)
	TrieTimeLimit       time.Duration // Time limit after which to flush the current in-memory trie to disk
	SnapshotLimit       int           // Memory allowance (MB) to use for caching snapshot entries in memory
	Preimages           bool          // Whether to store preimage of trie key to the disk

	SnapshotWait bool // Wait for snapshot construction on startup. TODO(karalabe): This is a dirty hack for testing, nuke it

	ExternalBlockLimit   int    // Memory allowance (MB) to use for caching trie nodes in memory
	ExternalBlockJournal string // Disk journal for saving clean cache entries.
}

// defaultCacheConfig are the default caching values if none are specified by the
// user (also used during testing).
var defaultCacheConfig = &CacheConfig{
	TrieCleanLimit:     256,
	TrieDirtyLimit:     256,
	TrieTimeLimit:      5 * time.Minute,
	SnapshotLimit:      256,
	SnapshotWait:       true,
	ExternalBlockLimit: 256,
}

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

	db     ethdb.Database // Low level persistent database to store final content in
	snaps  *snapshot.Tree // Snapshot tree for fast trie leaf access
	triegc *prque.Prque   // Priority queue mapping block numbers to tries to gc
	gcproc time.Duration  // Accumulates canonical block processing for trie dumping

	// txLookupLimit is the maximum number of blocks from head whose tx indices
	// are reserved:
	//  * 0:   means no limit and regenerate any missing indexes
	//  * N:   means N block limit [HEAD-N+1, HEAD] and delete extra indexes
	//  * nil: disable tx reindexer/deleter, but still index new blocks
	txLookupLimit uint64

	hc                       *HeaderChain
	rmLogsFeed               event.Feed
	chainFeed                event.Feed
	reOrgFeed                event.Feed
	chainSideFeed            event.Feed
	chainHeadFeed            event.Feed
	chainUncleFeed           event.Feed
	missingExternalBlockFeed event.Feed
	logsFeed                 event.Feed
	blockProcFeed            event.Feed
	scope                    event.SubscriptionScope
	genesisBlock             *types.Block

	chainmu sync.RWMutex // blockchain insertion lock
	reorgmu sync.RWMutex // reorg call lock

	currentBlock     atomic.Value // Current head of the block chain
	currentFastBlock atomic.Value // Current head of the fast-sync chain (may be above the block chain!)

	stateCache         state.Database   // State database to reuse between imports (contains state cache)
	bodyCache          *lru.Cache       // Cache for the most recent block bodies
	bodyRLPCache       *lru.Cache       // Cache for the most recent block bodies in RLP encoded format
	receiptsCache      *lru.Cache       // Cache for the most recent receipts per block
	blockCache         *lru.Cache       // Cache for the most recent entire blocks
	txLookupCache      *lru.Cache       // Cache for the most recent transaction lookup data.
	futureBlocks       *lru.Cache       // future blocks are blocks added for later processing
	externalBlockQueue *lru.Cache       // Queue for external blocks
	externalBlocks     *fastcache.Cache // blocks that need to be applied externally

	quit          chan struct{}  // blockchain quit channel
	wg            sync.WaitGroup // chain processing wait group for shutting down
	running       int32          // 0 if chain is running, 1 when stopped
	procInterrupt int32          // interrupt signaler for block processing

	engine     consensus.Engine
	validator  Validator // Block and state validator interface
	prefetcher Prefetcher
	processor  Processor // Block transaction processor interface
	vmConfig   vm.Config
	forker     *ForkChoice

	shouldPreserve func(*types.Block) bool // Function used to determine whether should preserve the given block.
}

// NewBlockChain returns a fully initialised block chain using information
// available in the database. It initialises the default Ethereum Validator and
// Processor.
func NewBlockChain(db ethdb.Database, cacheConfig *CacheConfig, chainConfig *params.ChainConfig, engine consensus.Engine, vmConfig vm.Config, shouldPreserve func(header *types.Header) bool, txLookupLimit *uint64) (*BlockChain, error) {
	if cacheConfig == nil {
		cacheConfig = defaultCacheConfig
	}
	bodyCache, _ := lru.New(bodyCacheLimit)
	bodyRLPCache, _ := lru.New(bodyCacheLimit)
	receiptsCache, _ := lru.New(receiptsCacheLimit)
	blockCache, _ := lru.New(blockCacheLimit)
	txLookupCache, _ := lru.New(txLookupCacheLimit)
	futureBlocks, _ := lru.New(maxFutureBlocks)
	externalBlockQueue, _ := lru.New(extBlockQueueLimit)

	var externalBlocks *fastcache.Cache
	if cacheConfig.ExternalBlockJournal == "" {
		externalBlocks = fastcache.New(cacheConfig.ExternalBlockLimit * 1024 * 1024)
	} else {
		externalBlocks = fastcache.LoadFromFileOrNew(cacheConfig.ExternalBlockJournal, cacheConfig.ExternalBlockLimit*1024*1024)
	}

	bc := &BlockChain{
		chainConfig: chainConfig,
		cacheConfig: cacheConfig,
		db:          db,
		triegc:      prque.New(nil),
		stateCache: state.NewDatabaseWithConfig(db, &trie.Config{
			Cache:     cacheConfig.TrieCleanLimit,
			Journal:   cacheConfig.TrieCleanJournal,
			Preimages: cacheConfig.Preimages,
		}),
		quit:               make(chan struct{}),
		bodyCache:          bodyCache,
		bodyRLPCache:       bodyRLPCache,
		receiptsCache:      receiptsCache,
		blockCache:         blockCache,
		txLookupCache:      txLookupCache,
		futureBlocks:       futureBlocks,
		externalBlocks:     externalBlocks,
		externalBlockQueue: externalBlockQueue,
		engine:             engine,
		vmConfig:           vmConfig,
	}

	bc.forker = NewForkChoice(bc, shouldPreserve)
	bc.validator = NewBlockValidator(chainConfig, bc, engine)
	bc.prefetcher = newStatePrefetcher(chainConfig, bc, engine)
	bc.processor = NewStateProcessor(chainConfig, bc, engine)

	var err error
	bc.hc, err = NewHeaderChain(db, chainConfig, engine, bc.insertStopped)
	if err != nil {
		return nil, err
	}
	bc.genesisBlock = bc.GetBlockByNumber(0)
	if bc.genesisBlock == nil {
		return nil, ErrNoGenesis
	}

	var nilBlock *types.Block
	bc.currentBlock.Store(nilBlock)
	bc.currentFastBlock.Store(nilBlock)

	// Initialize the chain with ancient data if it isn't empty.
	var txIndexBlock uint64

	if bc.empty() {
		rawdb.InitDatabaseFromFreezer(bc.db)
		// If ancient database is not empty, reconstruct all missing
		// indices in the background.
		frozen, _ := bc.db.Ancients()
		if frozen > 0 {
			txIndexBlock = frozen
		}
	}
	if err := bc.loadLastState(); err != nil {
		return nil, err
	}
	// Make sure the state associated with the block is available
	head := bc.CurrentBlock()
	if _, err := state.New(head.Root(), bc.stateCache, bc.snaps); err != nil {
		// Head state is missing, before the state recovery, find out the
		// disk layer point of snapshot(if it's enabled). Make sure the
		// rewound point is lower than disk layer.
		var diskRoot common.Hash
		if bc.cacheConfig.SnapshotLimit > 0 {
			diskRoot = rawdb.ReadSnapshotRoot(bc.db)
		}
		if diskRoot != (common.Hash{}) {
			log.Warn("Head state missing, repairing", "number", head.Number(), "hash", head.Hash(), "snaproot", diskRoot)

			snapDisk, err := bc.SetHeadBeyondRoot(head.NumberU64(), diskRoot)
			if err != nil {
				return nil, err
			}
			// Chain rewound, persist old snapshot number to indicate recovery procedure
			if snapDisk != 0 {
				rawdb.WriteSnapshotRecoveryNumber(bc.db, snapDisk)
			}
		} else {
			log.Warn("Head state missing, repairing", "number", head.Number(), "hash", head.Hash())
			if err := bc.SetHead(head.NumberU64()); err != nil {
				return nil, err
			}
		}
	}
	// Ensure that a previous crash in SetHead doesn't leave extra ancients
	if frozen, err := bc.db.Ancients(); err == nil && frozen > 0 {
		var (
			needRewind bool
			low        uint64
		)
		// The head full block may be rolled back to a very low height due to
		// blockchain repair. If the head full block is even lower than the ancient
		// chain, truncate the ancient store.
		fullBlock := bc.CurrentBlock()
		if fullBlock != nil && fullBlock.Hash() != bc.genesisBlock.Hash() && fullBlock.NumberU64() < frozen-1 {
			needRewind = true
			low = fullBlock.NumberU64()
		}
		// In fast sync, it may happen that ancient data has been written to the
		// ancient store, but the LastFastBlock has not been updated, truncate the
		// extra data here.
		fastBlock := bc.CurrentFastBlock()
		if fastBlock != nil && fastBlock.NumberU64() < frozen-1 {
			needRewind = true
			if fastBlock.NumberU64() < low || low == 0 {
				low = fastBlock.NumberU64()
			}
		}
		if needRewind {
			log.Error("Truncating ancient chain", "from", bc.CurrentHeader().Number[types.QuaiNetworkContext].Uint64(), "to", low)
			if err := bc.SetHead(low); err != nil {
				return nil, err
			}
		}
	}
	// The first thing the node will do is reconstruct the verification data for
	// the head block (ethash cache or clique voting snapshot). Might as well do
	// it in advance.
	bc.engine.VerifyHeader(bc, bc.CurrentHeader(), true)

	// Check the current state of the block hashes and make sure that we do not have any of the bad blocks in our chain
	for hash := range BadHashes {
		if header := bc.GetHeaderByHash(hash); header != nil {
			// get the canonical block corresponding to the offending header's number
			headerByNumber := bc.GetHeaderByNumber(header.Number[types.QuaiNetworkContext].Uint64())
			// make sure the headerByNumber (if present) is in our current canonical chain
			if headerByNumber != nil && headerByNumber.Hash() == header.Hash() {
				log.Error("Found bad hash, rewinding chain", "number", header.Number[types.QuaiNetworkContext], "hash", header.ParentHash[types.QuaiNetworkContext])
				if err := bc.SetHead(header.Number[types.QuaiNetworkContext].Uint64() - 1); err != nil {
					return nil, err
				}
				log.Error("Chain rewind was successful, resuming normal operation")
			}
		}
	}
	// Load any existing snapshot, regenerating it if loading failed
	if bc.cacheConfig.SnapshotLimit > 0 {
		// If the chain was rewound past the snapshot persistent layer (causing
		// a recovery block number to be persisted to disk), check if we're still
		// in recovery mode and in that case, don't invalidate the snapshot on a
		// head mismatch.
		var recover bool

		head := bc.CurrentBlock()
		if layer := rawdb.ReadSnapshotRecoveryNumber(bc.db); layer != nil && *layer > head.NumberU64() {
			log.Warn("Enabling snapshot recovery", "chainhead", head.NumberU64(), "diskbase", *layer)
			recover = true
		}
		bc.snaps, _ = snapshot.New(bc.db, bc.stateCache.TrieDB(), bc.cacheConfig.SnapshotLimit, head.Root(), !bc.cacheConfig.SnapshotWait, true, recover)
	}
	// Take ownership of this particular state
	bc.wg.Add(1)
	go bc.update()

	if txLookupLimit != nil {
		bc.txLookupLimit = *txLookupLimit

		bc.wg.Add(1)
		go bc.maintainTxIndex(txIndexBlock)
	}
	// If periodic cache journal is required, spin it up.
	if bc.cacheConfig.TrieCleanRejournal > 0 {
		if bc.cacheConfig.TrieCleanRejournal < time.Minute {
			log.Warn("Sanitizing invalid trie cache journal time", "provided", bc.cacheConfig.TrieCleanRejournal, "updated", time.Minute)
			bc.cacheConfig.TrieCleanRejournal = time.Minute
		}
		triedb := bc.stateCache.TrieDB()
		bc.wg.Add(1)
		go func() {
			defer bc.wg.Done()
			triedb.SaveCachePeriodically(bc.cacheConfig.TrieCleanJournal, bc.cacheConfig.TrieCleanRejournal, bc.quit)
		}()
	}

	return bc, nil
}

// GetVMConfig returns the block chain VM config.
func (bc *BlockChain) GetVMConfig() *vm.Config {
	return &bc.vmConfig
}

// empty returns an indicator whether the blockchain is empty.
// Note, it's a special case that we connect a non-empty ancient
// database with an empty node, so that we can plugin the ancient
// into node seamlessly.
func (bc *BlockChain) empty() bool {
	genesis := bc.genesisBlock.Hash()
	for _, hash := range []common.Hash{rawdb.ReadHeadBlockHash(bc.db), rawdb.ReadHeadHeaderHash(bc.db), rawdb.ReadHeadFastBlockHash(bc.db)} {
		if hash != genesis {
			return false
		}
	}
	return true
}

// loadLastState loads the last known chain state from the database. This method
// assumes that the chain manager mutex is held.
func (bc *BlockChain) loadLastState() error {
	// Restore the last known head block
	head := rawdb.ReadHeadBlockHash(bc.db)
	if head == (common.Hash{}) {
		// Corrupt or empty database, init from scratch
		log.Warn("Empty database, resetting chain")
		return bc.Reset()
	}
	// Make sure the entire head block is available
	currentBlock := bc.GetBlockByHash(head)
	if currentBlock == nil {
		// Corrupt or empty database, init from scratch
		log.Warn("Head block missing, resetting chain", "hash", head)
		return bc.Reset()
	}
	// Everything seems to be fine, set as the head block
	bc.currentBlock.Store(currentBlock)
	headBlockGauge.Update(int64(currentBlock.NumberU64()))

	// Restore the last known head header
	currentHeader := currentBlock.Header()
	if head := rawdb.ReadHeadHeaderHash(bc.db); head != (common.Hash{}) {
		if header := bc.GetHeaderByHash(head); header != nil {
			currentHeader = header
		}
	}
	bc.hc.SetCurrentHeader(currentHeader)

	// Restore the last known head fast block
	bc.currentFastBlock.Store(currentBlock)
	headFastBlockGauge.Update(int64(currentBlock.NumberU64()))

	if head := rawdb.ReadHeadFastBlockHash(bc.db); head != (common.Hash{}) {
		if block := bc.GetBlockByHash(head); block != nil {
			bc.currentFastBlock.Store(block)
			headFastBlockGauge.Update(int64(block.NumberU64()))
		}
	}
	// Issue a status log for the user
	currentFastBlock := bc.CurrentFastBlock()

	headerTd := bc.GetTd(currentHeader.Hash(), currentHeader.Number[types.QuaiNetworkContext].Uint64())
	blockTd := bc.GetTd(currentBlock.Hash(), currentBlock.NumberU64())
	fastTd := bc.GetTd(currentFastBlock.Hash(), currentFastBlock.NumberU64())

	log.Info("Loaded most recent local header", "number", currentHeader.Number, "hash", currentHeader.Hash(), "td", headerTd, "age", common.PrettyAge(time.Unix(int64(currentHeader.Time), 0)))
	log.Info("Loaded most recent local full block", "number", currentBlock.Number(), "hash", currentBlock.Hash(), "td", blockTd, "age", common.PrettyAge(time.Unix(int64(currentBlock.Time()), 0)))
	log.Info("Loaded most recent local fast block", "number", currentFastBlock.Number(), "hash", currentFastBlock.Hash(), "td", fastTd, "age", common.PrettyAge(time.Unix(int64(currentFastBlock.Time()), 0)))
	if pivot := rawdb.ReadLastPivotNumber(bc.db); pivot != nil {
		log.Info("Loaded last fast-sync pivot marker", "number", *pivot)
	}
	return nil
}

// SetHead rewinds the local chain to a new head. Depending on whether the node
// was fast synced or full synced and in which state, the method will try to
// delete minimal data from disk whilst retaining chain consistency.
func (bc *BlockChain) SetHead(head uint64) error {
	_, err := bc.SetHeadBeyondRoot(head, common.Hash{})
	return err
}

// SetHeadBeyondRoot rewinds the local chain to a new head with the extra condition
// that the rewind must pass the specified state root. This method is meant to be
// used when rewinding with snapshots enabled to ensure that we go back further than
// persistent disk layer. Depending on whether the node was fast synced or full, and
// in which state, the method will try to delete minimal data from disk whilst
// retaining chain consistency.
//
// The method returns the block number where the requested root cap was found.
func (bc *BlockChain) SetHeadBeyondRoot(head uint64, root common.Hash) (uint64, error) {
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	// Track the block number of the requested root hash
	var rootNumber uint64 // (no root == always 0)

	// Retrieve the last pivot block to short circuit rollbacks beyond it and the
	// current freezer limit to start nuking id underflown
	pivot := rawdb.ReadLastPivotNumber(bc.db)
	frozen, _ := bc.db.Ancients()

	updateFn := func(db ethdb.KeyValueWriter, header *types.Header) (uint64, bool) {
		// Rewind the block chain, ensuring we don't end up with a stateless head
		// block. Note, depth equality is permitted to allow using SetHead as a
		// chain reparation mechanism without deleting any data!
		if currentBlock := bc.CurrentBlock(); currentBlock != nil && header.Number[types.QuaiNetworkContext].Uint64() <= currentBlock.NumberU64() {
			newHeadBlock := bc.GetBlock(header.Hash(), header.Number[types.QuaiNetworkContext].Uint64())
			if newHeadBlock == nil {
				log.Error("Gap in the chain, rewinding to genesis", "number", header.Number, "hash", header.Hash())
				newHeadBlock = bc.genesisBlock
			} else {
				// Block exists, keep rewinding until we find one with state,
				// keeping rewinding until we exceed the optional threshold
				// root hash
				beyondRoot := (root == common.Hash{}) // Flag whether we're beyond the requested root (no root, always true)

				for {
					// If a root threshold was requested but not yet crossed, check
					if root != (common.Hash{}) && !beyondRoot && newHeadBlock.Root() == root {
						beyondRoot, rootNumber = true, newHeadBlock.NumberU64()
					}
					if _, err := state.New(newHeadBlock.Root(), bc.stateCache, bc.snaps); err != nil {
						log.Trace("Block state missing, rewinding further", "number", newHeadBlock.NumberU64(), "hash", newHeadBlock.Hash())
						if pivot == nil || newHeadBlock.NumberU64() > *pivot {
							parent := bc.GetBlock(newHeadBlock.ParentHash(), newHeadBlock.NumberU64()-1)
							if parent != nil {
								newHeadBlock = parent
								continue
							}
							log.Error("Missing block in the middle, aiming genesis", "number", newHeadBlock.NumberU64()-1, "hash", newHeadBlock.ParentHash())
							newHeadBlock = bc.genesisBlock
						} else {
							log.Trace("Rewind passed pivot, aiming genesis", "number", newHeadBlock.NumberU64(), "hash", newHeadBlock.Hash(), "pivot", *pivot)
							newHeadBlock = bc.genesisBlock
						}
					}
					if beyondRoot || newHeadBlock.NumberU64() == 0 {
						log.Debug("Rewound to block with state", "number", newHeadBlock.NumberU64(), "hash", newHeadBlock.Hash())
						break
					}
					log.Debug("Skipping block with threshold state", "number", newHeadBlock.NumberU64(), "hash", newHeadBlock.Hash(), "root", newHeadBlock.Root())
					newHeadBlock = bc.GetBlock(newHeadBlock.ParentHash(), newHeadBlock.NumberU64()-1) // Keep rewinding
				}
			}
			rawdb.WriteHeadBlockHash(db, newHeadBlock.Hash())

			// Degrade the chain markers if they are explicitly reverted.
			// In theory we should update all in-memory markers in the
			// last step, however the direction of SetHead is from high
			// to low, so it's safe the update in-memory markers directly.
			bc.currentBlock.Store(newHeadBlock)
			headBlockGauge.Update(int64(newHeadBlock.NumberU64()))
		}
		// Rewind the fast block in a simpleton way to the target head
		if currentFastBlock := bc.CurrentFastBlock(); currentFastBlock != nil && header.Number[types.QuaiNetworkContext].Uint64() < currentFastBlock.NumberU64() {
			newHeadFastBlock := bc.GetBlock(header.Hash(), header.Number[types.QuaiNetworkContext].Uint64())
			// If either blocks reached nil, reset to the genesis state
			if newHeadFastBlock == nil {
				newHeadFastBlock = bc.genesisBlock
			}
			rawdb.WriteHeadFastBlockHash(db, newHeadFastBlock.Hash())

			// Degrade the chain markers if they are explicitly reverted.
			// In theory we should update all in-memory markers in the
			// last step, however the direction of SetHead is from high
			// to low, so it's safe the update in-memory markers directly.
			bc.currentFastBlock.Store(newHeadFastBlock)
			headFastBlockGauge.Update(int64(newHeadFastBlock.NumberU64()))
		}
		head := bc.CurrentBlock().NumberU64()

		// If setHead underflown the freezer threshold and the block processing
		// intent afterwards is full block importing, delete the chain segment
		// between the stateful-block and the sethead target.
		var wipe bool
		if head+1 < frozen {
			wipe = pivot == nil || head >= *pivot
		}
		return head, wipe // Only force wipe if full synced
	}
	// Rewind the header chain, deleting all block bodies until then
	delFn := func(db ethdb.KeyValueWriter, hash common.Hash, num uint64) {
		// Ignore the error here since light client won't hit this path
		frozen, _ := bc.db.Ancients()
		if num+1 <= frozen {
			// Truncate all relative data(header, total difficulty, body, receipt
			// and canonical hash) from ancient store.
			if err := bc.db.TruncateAncients(num); err != nil {
				log.Crit("Failed to truncate ancient data", "number", num, "err", err)
			}
			// Remove the hash <-> number mapping from the active store.
			rawdb.DeleteHeaderNumber(db, hash)
		} else {
			// Remove relative body and receipts from the active store.
			// The header, total difficulty and canonical hash will be
			// removed in the hc.SetHead function.
			rawdb.DeleteBody(db, hash, num)
			rawdb.DeleteReceipts(db, hash, num)
		}
		// Todo(rjl493456442) txlookup, bloombits, etc
	}
	// If SetHead was only called as a chain reparation method, try to skip
	// touching the header chain altogether, unless the freezer is broken
	if block := bc.CurrentBlock(); block.NumberU64() == head {
		if target, force := updateFn(bc.db, block.Header()); force {
			bc.hc.SetHead(target, updateFn, delFn)
		}
	} else {
		// Rewind the chain to the requested head and keep going backwards until a
		// block with a state is found or fast sync pivot is passed
		log.Warn("Rewinding blockchain", "target", head)
		bc.hc.SetHead(head, updateFn, delFn)
	}
	// Clear out any stale content from the caches
	bc.bodyCache.Purge()
	bc.bodyRLPCache.Purge()
	bc.receiptsCache.Purge()
	bc.blockCache.Purge()
	bc.txLookupCache.Purge()
	bc.futureBlocks.Purge()
	bc.externalBlockQueue.Purge()

	return rootNumber, bc.loadLastState()
}

// FastSyncCommitHead sets the current head block to the one defined by the hash
// irrelevant what the chain contents were prior.
func (bc *BlockChain) FastSyncCommitHead(hash common.Hash) error {
	// Make sure that both the block as well at its state trie exists
	block := bc.GetBlockByHash(hash)
	if block == nil {
		return fmt.Errorf("non existent block [%x..]", hash[:4])
	}
	if _, err := trie.NewSecure(block.Root(), bc.stateCache.TrieDB()); err != nil {
		return err
	}
	// If all checks out, manually set the head block
	bc.chainmu.Lock()
	bc.currentBlock.Store(block)
	headBlockGauge.Update(int64(block.NumberU64()))
	bc.chainmu.Unlock()

	// Destroy any existing state snapshot and regenerate it in the background,
	// also resuming the normal maintenance of any previously paused snapshot.
	if bc.snaps != nil {
		bc.snaps.Rebuild(block.Root())
	}
	log.Info("Committed new head block", "number", block.Number(), "hash", hash)
	return nil
}

// GasLimit returns the gas limit of the current HEAD block.
func (bc *BlockChain) GasLimit() uint64 {
	return bc.CurrentBlock().GasLimit()
}

// CurrentBlock retrieves the current head block of the canonical chain. The
// block is retrieved from the blockchain's internal cache.
func (bc *BlockChain) CurrentBlock() *types.Block {
	return bc.currentBlock.Load().(*types.Block)
}

// Snapshots returns the blockchain snapshot tree.
func (bc *BlockChain) Snapshots() *snapshot.Tree {
	return bc.snaps
}

// CurrentFastBlock retrieves the current fast-sync head block of the canonical
// chain. The block is retrieved from the blockchain's internal cache.
func (bc *BlockChain) CurrentFastBlock() *types.Block {
	return bc.currentFastBlock.Load().(*types.Block)
}

// Validator returns the current validator.
func (bc *BlockChain) Validator() Validator {
	return bc.validator
}

// Processor returns the current processor.
func (bc *BlockChain) Processor() Processor {
	return bc.processor
}

// State returns a new mutable state based on the current HEAD block.
func (bc *BlockChain) State() (*state.StateDB, error) {
	return bc.StateAt(bc.CurrentBlock().Root())
}

// StateAt returns a new mutable state based on a particular point in time.
func (bc *BlockChain) StateAt(root common.Hash) (*state.StateDB, error) {
	return state.New(root, bc.stateCache, bc.snaps)
}

// StateCache returns the caching database underpinning the blockchain instance.
func (bc *BlockChain) StateCache() state.Database {
	return bc.stateCache
}

// Reset purges the entire blockchain, restoring it to its genesis state.
func (bc *BlockChain) Reset() error {
	return bc.ResetWithGenesisBlock(bc.genesisBlock)
}

// ResetWithGenesisBlock purges the entire blockchain, restoring it to the
// specified genesis state.
func (bc *BlockChain) ResetWithGenesisBlock(genesis *types.Block) error {
	// Dump the entire block chain and purge the caches
	if err := bc.SetHead(0); err != nil {
		return err
	}
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	// Prepare the genesis block and reinitialise the chain
	batch := bc.db.NewBatch()
	rawdb.WriteTd(batch, genesis.Hash(), genesis.NumberU64(), genesis.Header().Difficulty)
	rawdb.WriteBlock(batch, genesis)
	if err := batch.Write(); err != nil {
		log.Crit("Failed to write genesis block", "err", err)
	}
	bc.writeHeadBlock(genesis)

	// Last update all in-memory chain markers
	bc.genesisBlock = genesis
	bc.currentBlock.Store(bc.genesisBlock)
	headBlockGauge.Update(int64(bc.genesisBlock.NumberU64()))
	bc.hc.SetGenesis(bc.genesisBlock.Header())
	bc.hc.SetCurrentHeader(bc.genesisBlock.Header())
	bc.currentFastBlock.Store(bc.genesisBlock)
	headFastBlockGauge.Update(int64(bc.genesisBlock.NumberU64()))
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

// writeHeadBlock injects a new head block into the current block chain. This method
// assumes that the block is indeed a true head. It will also reset the head
// header and the head fast sync block to this very same block if they are older
// or if they are on a different side chain.
//
// Note, this function assumes that the `mu` mutex is held!
func (bc *BlockChain) writeHeadBlock(block *types.Block) {
	// If the block is on a side chain or an unknown one, force other heads onto it too
	updateHeads := rawdb.ReadCanonicalHash(bc.db, block.NumberU64()) != block.Hash()

	// Add the block to the canonical chain number scheme and mark as the head
	batch := bc.db.NewBatch()
	rawdb.WriteCanonicalHash(batch, block.Hash(), block.NumberU64())
	rawdb.WriteTxLookupEntriesByBlock(batch, block)
	rawdb.WriteHeadBlockHash(batch, block.Hash())

	// If the block is better than our head or is on a different chain, force update heads
	if updateHeads {
		rawdb.WriteHeadHeaderHash(batch, block.Hash())
		rawdb.WriteHeadFastBlockHash(batch, block.Hash())
	}
	// Flush the whole batch into the disk, exit the node if failed
	if err := batch.Write(); err != nil {
		log.Crit("Failed to update chain indexes and markers", "err", err)
	}
	// Update all in-memory chain markers in the last step
	if updateHeads {
		bc.hc.SetCurrentHeader(block.Header())
		bc.currentFastBlock.Store(block)
		headFastBlockGauge.Update(int64(block.NumberU64()))
	}
	bc.currentBlock.Store(block)
	headBlockGauge.Update(int64(block.NumberU64()))
}

// Genesis retrieves the chain's genesis block.
func (bc *BlockChain) Genesis() *types.Block {
	return bc.genesisBlock
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (bc *BlockChain) GetBody(hash common.Hash) *types.Body {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := bc.bodyCache.Get(hash); ok {
		body := cached.(*types.Body)
		return body
	}
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBody(bc.db, hash, *number)
	if body == nil {
		return nil
	}
	// Cache the found body for next time and return
	bc.bodyCache.Add(hash, body)
	return body
}

// GetBodyRLP retrieves a block body in RLP encoding from the database by hash,
// caching it if found.
func (bc *BlockChain) GetBodyRLP(hash common.Hash) rlp.RawValue {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := bc.bodyRLPCache.Get(hash); ok {
		return cached.(rlp.RawValue)
	}
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBodyRLP(bc.db, hash, *number)
	if len(body) == 0 {
		return nil
	}
	// Cache the found body for next time and return
	bc.bodyRLPCache.Add(hash, body)
	return body
}

// HasBlock checks if a block is fully present in the database or not.
func (bc *BlockChain) HasBlock(hash common.Hash, number uint64) bool {
	if bc.blockCache.Contains(hash) {
		return true
	}
	return rawdb.HasBody(bc.db, hash, number)
}

// HasFastBlock checks if a fast block is fully present in the database or not.
func (bc *BlockChain) HasFastBlock(hash common.Hash, number uint64) bool {
	if !bc.HasBlock(hash, number) {
		return false
	}
	if bc.receiptsCache.Contains(hash) {
		return true
	}
	return rawdb.HasReceipts(bc.db, hash, number)
}

// HasState checks if state trie is fully present in the database or not.
func (bc *BlockChain) HasState(hash common.Hash) bool {
	_, err := bc.stateCache.OpenTrie(hash)
	return err == nil
}

// HasBlockAndState checks if a block and associated state trie is fully present
// in the database or not, caching it if present.
func (bc *BlockChain) HasBlockAndState(hash common.Hash, number uint64) bool {
	// Check first that the block itself is known
	block := bc.GetBlock(hash, number)
	if block == nil {
		return false
	}
	return bc.HasState(block.Root())
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

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (bc *BlockChain) GetReceiptsByHash(hash common.Hash) types.Receipts {
	if receipts, ok := bc.receiptsCache.Get(hash); ok {
		return receipts.(types.Receipts)
	}
	number := rawdb.ReadHeaderNumber(bc.db, hash)
	if number == nil {
		return nil
	}
	receipts := rawdb.ReadReceipts(bc.db, hash, *number, bc.chainConfig)
	if receipts == nil {
		return nil
	}
	bc.receiptsCache.Add(hash, receipts)
	return receipts
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

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (bc *BlockChain) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	uncles := []*types.Header{}
	for i := 0; block != nil && i < length; i++ {
		uncles = append(uncles, block.Uncles()...)
		block = bc.GetBlock(block.ParentHash(), block.NumberU64()-1)
	}
	return uncles
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (bc *BlockChain) GetGasUsedInChain(block *types.Block, length int) int64 {
	gasUsed := 0
	for i := 0; block != nil && i < length; i++ {
		gasUsed += int(block.GasUsed())
		block = bc.GetBlock(block.ParentHash(), block.NumberU64()-1)
	}
	return int64(gasUsed)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (bc *BlockChain) CalculateBaseFee(header *types.Header) *big.Int {
	return misc.CalcBaseFee(bc.Config(), header, bc.GetHeaderByNumber, bc.GetUnclesInChain, bc.GetGasUsedInChain)
}

// TrieNode retrieves a blob of data associated with a trie node
// either from ephemeral in-memory cache, or from persistent storage.
func (bc *BlockChain) TrieNode(hash common.Hash) ([]byte, error) {
	return bc.stateCache.TrieDB().Node(hash)
}

// ContractCode retrieves a blob of data associated with a contract hash
// either from ephemeral in-memory cache, or from persistent storage.
func (bc *BlockChain) ContractCode(hash common.Hash) ([]byte, error) {
	return bc.stateCache.ContractCode(common.Hash{}, hash)
}

// ContractCodeWithPrefix retrieves a blob of data associated with a contract
// hash either from ephemeral in-memory cache, or from persistent storage.
//
// If the code doesn't exist in the in-memory cache, check the storage with
// new code scheme.
func (bc *BlockChain) ContractCodeWithPrefix(hash common.Hash) ([]byte, error) {
	type codeReader interface {
		ContractCodeWithPrefix(addrHash, codeHash common.Hash) ([]byte, error)
	}
	return bc.stateCache.(codeReader).ContractCodeWithPrefix(common.Hash{}, hash)
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

	// Ensure that the entirety of the state snapshot is journalled to disk.
	var snapBase common.Hash
	if bc.snaps != nil {
		var err error
		if snapBase, err = bc.snaps.Journal(bc.CurrentBlock().Root()); err != nil {
			log.Error("Failed to journal state snapshot", "err", err)
		}
	}

	// Save the state of the external block cache to disk
	log.Info("Writing external blocks state to disk", "dir", bc.cacheConfig.ExternalBlockJournal)
	bc.externalBlocks.SaveToFileConcurrent(bc.cacheConfig.ExternalBlockJournal, runtime.GOMAXPROCS(0))

	// Ensure the state of a recent block is also stored to disk before exiting.
	// We're writing three different states to catch different restart scenarios:
	//  - HEAD:     So we don't need to reprocess any blocks in the general case
	//  - HEAD-1:   So we don't do large reorgs if our HEAD becomes an uncle
	//  - HEAD-127: So we have a hard limit on the number of blocks reexecuted
	if !bc.cacheConfig.TrieDirtyDisabled {
		triedb := bc.stateCache.TrieDB()

		for _, offset := range []uint64{0, 1, TriesInMemory - 1} {
			if number := bc.CurrentBlock().NumberU64(); number > offset {
				recent := bc.GetBlockByNumber(number - offset)
				if recent != nil {
					log.Info("Writing cached state to disk", "block", recent.Number(), "hash", recent.Hash(), "root", recent.Root())
					if err := triedb.Commit(recent.Root(), true, nil); err != nil {
						log.Error("Failed to commit recent state trie", "err", err)
					}
				}
			}
		}
		if snapBase != (common.Hash{}) {
			log.Info("Writing snapshot state to disk", "root", snapBase)
			if err := triedb.Commit(snapBase, true, nil); err != nil {
				log.Error("Failed to commit recent state trie", "err", err)
			}
		}
		for !bc.triegc.Empty() {
			triedb.Dereference(bc.triegc.PopItem().(common.Hash))
		}
		if size, _ := triedb.Size(); size != 0 {
			log.Error("Dangling trie nodes after full cleanup")
		}
	}
	// Ensure all live cached entries be saved into disk, so that we can skip
	// cache warmup when node restarts.
	if bc.cacheConfig.TrieCleanJournal != "" {
		triedb := bc.stateCache.TrieDB()
		triedb.SaveCache(bc.cacheConfig.TrieCleanJournal)
	}
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

func (bc *BlockChain) procFutureBlocks() {
	blocks := make([]*types.Block, 0, bc.futureBlocks.Len())
	for _, hash := range bc.futureBlocks.Keys() {
		if block, exist := bc.futureBlocks.Peek(hash); exist {
			blocks = append(blocks, block.(*types.Block))
		}
	}
	if len(blocks) > 0 {
		sort.Slice(blocks, func(i, j int) bool {
			return blocks[i].NumberU64() < blocks[j].NumberU64()
		})
		// Insert one by one as chain insertion needs contiguous ancestry between blocks
		for i := range blocks {
			bc.InsertChain(blocks[i : i+1])
		}
	}
}

// WriteStatus status of write
type WriteStatus byte

const (
	NonStatTy WriteStatus = iota
	CanonStatTy
	SideStatTy
)

// numberHash is just a container for a number and a hash, to represent a block
type numberHash struct {
	number uint64
	hash   common.Hash
}

// InsertReceiptChain attempts to complete an already existing header chain with
// transaction and receipt data.
func (bc *BlockChain) InsertReceiptChain(blockChain types.Blocks, receiptChain []types.Receipts, ancientLimit uint64) (int, error) {
	// We don't require the chainMu here since we want to maximize the
	// concurrency of header insertion and receipt insertion.
	bc.wg.Add(1)
	defer bc.wg.Done()

	var (
		ancientBlocks, liveBlocks     types.Blocks
		ancientReceipts, liveReceipts []types.Receipts
	)
	// Do a sanity check that the provided chain is actually ordered and linked
	for i := 0; i < len(blockChain); i++ {
		if i != 0 {
			if blockChain[i].NumberU64() != blockChain[i-1].NumberU64()+1 || blockChain[i].ParentHash() != blockChain[i-1].Hash() {
				log.Error("Non contiguous receipt insert", "number", blockChain[i].Number(), "hash", blockChain[i].Hash(), "parent", blockChain[i].ParentHash(),
					"prevnumber", blockChain[i-1].Number(), "prevhash", blockChain[i-1].Hash())
				return 0, fmt.Errorf("non contiguous insert: item %d is #%d [%x..], item %d is #%d [%x..] (parent [%x..])", i-1, blockChain[i-1].NumberU64(),
					blockChain[i-1].Hash().Bytes()[:4], i, blockChain[i].NumberU64(), blockChain[i].Hash().Bytes()[:4], blockChain[i].ParentHash().Bytes()[:4])
			}
		}
		if blockChain[i].NumberU64() <= ancientLimit {
			ancientBlocks, ancientReceipts = append(ancientBlocks, blockChain[i]), append(ancientReceipts, receiptChain[i])
		} else {
			liveBlocks, liveReceipts = append(liveBlocks, blockChain[i]), append(liveReceipts, receiptChain[i])
		}
	}

	var (
		stats = struct{ processed, ignored int32 }{}
		start = time.Now()
		size  = int64(0)
	)

	// updateHead updates the head fast sync block if the inserted blocks are better
	// and returns an indicator whether the inserted blocks are canonical.
	updateHead := func(head *types.Block) bool {
		bc.chainmu.Lock()
		defer bc.chainmu.Unlock()

		// Rewind may have occurred, skip in that case.
		if bc.CurrentHeader().Number[types.QuaiNetworkContext].Cmp(head.Number()) >= 0 {
			reorg, err := bc.forker.ReorgNeeded(bc.CurrentFastBlock().Header(), head.Header())
			if err != nil {
				log.Warn("Reorg failed", "err", err)
				return false
			} else if !reorg {
				return false
			}
			rawdb.WriteHeadFastBlockHash(bc.db, head.Hash())
			bc.currentFastBlock.Store(head)
			headFastBlockGauge.Update(int64(head.NumberU64()))
			return true
		}
		return false
	}

	// writeAncient writes blockchain and corresponding receipt chain into ancient store.
	//
	// this function only accepts canonical chain data. All side chain will be reverted
	// eventually.
	writeAncient := func(blockChain types.Blocks, receiptChain []types.Receipts) (int, error) {
		first := blockChain[0]
		last := blockChain[len(blockChain)-1]

		// Ensure genesis is in ancients.
		if first.NumberU64() == 1 {
			if frozen, _ := bc.db.Ancients(); frozen == 0 {
				b := bc.genesisBlock
				td := bc.genesisBlock.Difficulty()
				writeSize, err := rawdb.WriteAncientBlocks(bc.db, []*types.Block{b}, []types.Receipts{nil}, td)
				size += writeSize
				if err != nil {
					log.Error("Error writing genesis to ancients", "err", err)
					return 0, err
				}
				log.Info("Wrote genesis to ancients")
			}
		}
		// Before writing the blocks to the ancients, we need to ensure that
		// they correspond to the what the headerchain 'expects'.
		// We only check the last block/header, since it's a contiguous chain.
		if !bc.HasHeader(last.Hash(), last.NumberU64()) {
			return 0, fmt.Errorf("containing header #%d [%x..] unknown", last.Number(), last.Hash().Bytes()[:4])
		}

		// Write all chain data to ancients.
		td := bc.GetTd(first.Hash(), first.NumberU64())
		writeSize, err := rawdb.WriteAncientBlocks(bc.db, blockChain, receiptChain, td[types.QuaiNetworkContext])
		size += writeSize
		if err != nil {
			log.Error("Error importing chain data to ancients", "err", err)
			return 0, err
		}

		// Write tx indices if any condition is satisfied:
		// * If user requires to reserve all tx indices(txlookuplimit=0)
		// * If all ancient tx indices are required to be reserved(txlookuplimit is even higher than ancientlimit)
		// * If block number is large enough to be regarded as a recent block
		// It means blocks below the ancientLimit-txlookupLimit won't be indexed.
		//
		// But if the `TxIndexTail` is not nil, e.g. Geth is initialized with
		// an external ancient database, during the setup, blockchain will start
		// a background routine to re-indexed all indices in [ancients - txlookupLimit, ancients)
		// range. In this case, all tx indices of newly imported blocks should be
		// generated.
		var batch = bc.db.NewBatch()
		for _, block := range blockChain {
			if bc.txLookupLimit == 0 || ancientLimit <= bc.txLookupLimit || block.NumberU64() >= ancientLimit-bc.txLookupLimit {
				rawdb.WriteTxLookupEntriesByBlock(batch, block)
			} else if rawdb.ReadTxIndexTail(bc.db) != nil {
				rawdb.WriteTxLookupEntriesByBlock(batch, block)
			}
			stats.processed++
		}

		// Flush all tx-lookup index data.
		size += int64(batch.ValueSize())
		if err := batch.Write(); err != nil {
			// The tx index data could not be written.
			// Roll back the ancient store update.
			fastBlock := bc.CurrentFastBlock().NumberU64()
			if err := bc.db.TruncateAncients(fastBlock + 1); err != nil {
				log.Error("Can't truncate ancient store after failed insert", "err", err)
			}
			return 0, err
		}

		// Sync the ancient store explicitly to ensure all data has been flushed to disk.
		if err := bc.db.Sync(); err != nil {
			return 0, err
		}

		// Update the current fast block because all block data is now present in DB.
		previousFastBlock := bc.CurrentFastBlock().NumberU64()
		if !updateHead(blockChain[len(blockChain)-1]) {
			// We end up here if the header chain has reorg'ed, and the blocks/receipts
			// don't match the canonical chain.
			if err := bc.db.TruncateAncients(previousFastBlock + 1); err != nil {
				log.Error("Can't truncate ancient store after failed insert", "err", err)
			}
			return 0, errSideChainReceipts
		}

		// Delete block data from the main database.
		batch.Reset()
		canonHashes := make(map[common.Hash]struct{})
		for _, block := range blockChain {
			canonHashes[block.Hash()] = struct{}{}
			if block.NumberU64() == 0 {
				continue
			}
			rawdb.DeleteCanonicalHash(batch, block.NumberU64())
			rawdb.DeleteBlockWithoutNumber(batch, block.Hash(), block.NumberU64())
		}
		// Delete side chain hash-to-number mappings.
		for _, nh := range rawdb.ReadAllHashesInRange(bc.db, first.NumberU64(), last.NumberU64()) {
			if _, canon := canonHashes[nh.Hash]; !canon {
				rawdb.DeleteHeader(batch, nh.Hash, nh.Number)
			}
		}
		if err := batch.Write(); err != nil {
			return 0, err
		}
		return 0, nil
	}

	// writeLive writes blockchain and corresponding receipt chain into active store.
	writeLive := func(blockChain types.Blocks, receiptChain []types.Receipts) (int, error) {
		skipPresenceCheck := false
		batch := bc.db.NewBatch()
		for i, block := range blockChain {
			// Short circuit insertion if shutting down or processing failed
			if bc.insertStopped() {
				return 0, errInsertionInterrupted
			}
			// Short circuit if the owner header is unknown
			if !bc.HasHeader(block.Hash(), block.NumberU64()) {
				return i, fmt.Errorf("containing header #%d [%x..] unknown", block.Number(), block.Hash().Bytes()[:4])
			}
			if !skipPresenceCheck {
				// Ignore if the entire data is already known
				if bc.HasBlock(block.Hash(), block.NumberU64()) {
					stats.ignored++
					continue
				} else {
					// If block N is not present, neither are the later blocks.
					// This should be true, but if we are mistaken, the shortcut
					// here will only cause overwriting of some existing data
					skipPresenceCheck = true
				}
			}
			// Write all the data out into the database
			rawdb.WriteBody(batch, block.Hash(), block.NumberU64(), block.Body())
			rawdb.WriteReceipts(batch, block.Hash(), block.NumberU64(), receiptChain[i])
			rawdb.WriteTxLookupEntriesByBlock(batch, block) // Always write tx indices for live blocks, we assume they are needed

			// Write everything belongs to the blocks into the database. So that
			// we can ensure all components of body is completed(body, receipts,
			// tx indexes)
			if batch.ValueSize() >= ethdb.IdealBatchSize {
				if err := batch.Write(); err != nil {
					return 0, err
				}
				size += int64(batch.ValueSize())
				batch.Reset()
			}
			stats.processed++
		}
		// Write everything belongs to the blocks into the database. So that
		// we can ensure all components of body is completed(body, receipts,
		// tx indexes)
		if batch.ValueSize() > 0 {
			size += int64(batch.ValueSize())
			if err := batch.Write(); err != nil {
				return 0, err
			}
		}
		updateHead(blockChain[len(blockChain)-1])
		return 0, nil
	}

	// Write downloaded chain data and corresponding receipt chain data
	if len(ancientBlocks) > 0 {
		if n, err := writeAncient(ancientBlocks, ancientReceipts); err != nil {
			if err == errInsertionInterrupted {
				return 0, nil
			}
			return n, err
		}
	}
	// Write the tx index tail (block number from where we index) before write any live blocks
	if len(liveBlocks) > 0 && liveBlocks[0].NumberU64() == ancientLimit+1 {
		// The tx index tail can only be one of the following two options:
		// * 0: all ancient blocks have been indexed
		// * ancient-limit: the indices of blocks before ancient-limit are ignored
		if tail := rawdb.ReadTxIndexTail(bc.db); tail == nil {
			if bc.txLookupLimit == 0 || ancientLimit <= bc.txLookupLimit {
				rawdb.WriteTxIndexTail(bc.db, 0)
			} else {
				rawdb.WriteTxIndexTail(bc.db, ancientLimit-bc.txLookupLimit)
			}
		}
	}
	if len(liveBlocks) > 0 {
		if n, err := writeLive(liveBlocks, liveReceipts); err != nil {
			if err == errInsertionInterrupted {
				return 0, nil
			}
			return n, err
		}
	}

	head := blockChain[len(blockChain)-1]
	context := []interface{}{
		"count", stats.processed, "elapsed", common.PrettyDuration(time.Since(start)),
		"number", head.Number(), "hash", head.Hash(), "age", common.PrettyAge(time.Unix(int64(head.Time()), 0)),
		"size", common.StorageSize(size),
	}
	if stats.ignored > 0 {
		context = append(context, []interface{}{"ignored", stats.ignored}...)
	}
	log.Info("Imported new block receipts", context...)

	return 0, nil
}

// SetTxLookupLimit is responsible for updating the txlookup limit to the
// original one stored in db if the new mismatches with the old one.
func (bc *BlockChain) SetTxLookupLimit(limit uint64) {
	bc.txLookupLimit = limit
}

// TxLookupLimit retrieves the txlookup limit used by blockchain to prune
// stale transaction indices.
func (bc *BlockChain) TxLookupLimit() uint64 {
	return bc.txLookupLimit
}

var lastWrite uint64

// writeBlockWithoutState writes only the block and its metadata to the database,
// but does not write any state. This is used to construct competing side forks
// up to the point where they exceed the canonical total difficulty.
func (bc *BlockChain) writeBlockWithoutState(block *types.Block, td []*big.Int) (err error) {
	bc.wg.Add(1)
	defer bc.wg.Done()

	batch := bc.db.NewBatch()
	rawdb.WriteTd(batch, block.Hash(), block.NumberU64(), td)
	rawdb.WriteBlock(batch, block)
	if err := batch.Write(); err != nil {
		log.Crit("Failed to write block into disk", "err", err)
	}
	return nil
}

// writeKnownBlock updates the head block flag with a known block
// and introduces chain reorg if necessary.
func (bc *BlockChain) writeKnownBlock(block *types.Block) error {
	bc.wg.Add(1)
	defer bc.wg.Done()

	current := bc.CurrentBlock()
	if block.ParentHash() != current.Hash() {
		if err := bc.reorg(current, block); err != nil {
			return err
		}
	}
	bc.writeHeadBlock(block)
	return nil
}

// NdToTd returns the total difficulty for a header given the net difficulty
func (bc *BlockChain) NdToTd(header *types.Header, nD []*big.Int) ([]*big.Int, error) {
	if header == nil {
		return nil, errors.New("header provided to ndtotd is nil")
	}
	startingHeader := header
	k := big.NewInt(0)

	var prevExternTerminus *types.Header
	var err error
	for {
		if bc.hc.GetBlockNumber(header.Hash()) != nil {
			break
		}
		prevExternTerminus, err = bc.Engine().PreviousCoincidentOnPath(bc, header, header.Location, params.PRIME, params.PRIME)
		if err != nil {
			return nil, err
		}
		header = prevExternTerminus
	}
	header = startingHeader
	for prevExternTerminus.Hash() != header.Hash() {
		k.Add(k, header.Difficulty[0])
		if header.Hash() == bc.Config().GenesisHashes[0] {
			break
		}

		// Get previous header on local chain by hash
		prevHeader := bc.GetHeaderByHash(header.ParentHash[params.PRIME])
		if prevHeader == nil {
			// Get previous header on external chain by hash
			prevExtBlock, err := bc.GetExternalBlock(header.ParentHash[params.PRIME], header.Number[params.PRIME].Uint64()-1, header.Location, uint64(params.PRIME))
			if err != nil {
				return nil, err
			}
			// Increment previous header
			prevHeader = prevExtBlock.Header()
		}
		header = prevHeader
	}
	// subtract the terminal block difficulty
	k.Sub(k, header.Difficulty[0])

	k.Add(k, bc.GetTdByHash(header.Hash())[params.PRIME])

	// adding the common total difficulty to the net
	nD[0].Add(nD[0], k)
	nD[1].Add(nD[1], k)
	nD[2].Add(nD[2], k)

	return nD, nil
}

// CalcTd calculates the TD of the given header using PCRC and CalcHLCRNetDifficulty.
func (bc *BlockChain) CalcTd(header *types.Header) ([]*big.Int, error) {
	// Check PCRC for the external block and return the terminal hash and net difficulties
	externTerminalHash, err := bc.PCRC(header)
	if err != nil {
		return nil, err
	}

	// Use HLCR to compute net total difficulty
	externNd, err := bc.CalcHLCRNetDifficulty(externTerminalHash, header)
	if err != nil {
		return nil, err
	}

	externTerminalHeader := bc.GetHeaderByHash(externTerminalHash)
	externTd, err := bc.NdToTd(externTerminalHeader, externNd)
	if err != nil {
		return nil, err
	}

	return externTd, nil
}

// writeBlockWithState writes the block and all associated state to the database,
// but is expects the chain mutex to be held.
func (bc *BlockChain) writeBlockWithState(block *types.Block, receipts []*types.Receipt, logs []*types.Log, state *state.StateDB, linkExtBlocks []*types.ExternalBlock) error {

	externTd, err := bc.CalcTd(block.Header())
	if err != nil {
		return err
	}
	// Irrelevant of the canonical status, write the block itself to the database.
	//
	// Note all the components of block(td, hash->number map, header, body, receipts)
	// should be written atomically. BlockBatch is used for containing all components.
	blockBatch := bc.db.NewBatch()
	fmt.Println("Writing TD", externTd, block.Hash())
	rawdb.WriteTd(blockBatch, block.Hash(), block.NumberU64(), externTd)
	rawdb.WriteBlock(blockBatch, block)
	rawdb.WriteReceipts(blockBatch, block.Hash(), block.NumberU64(), receipts)
	rawdb.WritePreimages(blockBatch, state.Preimages())
	if err := blockBatch.Write(); err != nil {
		log.Crit("Failed to write block into disk", "err", err)
	}
	// Commit all cached state changes into underlying memory database.
	root, err := state.Commit(bc.chainConfig.IsEIP158(block.Number()))
	if err != nil {
		return err
	}
	triedb := bc.stateCache.TrieDB()

	// If we're running an archive node, always flush
	if bc.cacheConfig.TrieDirtyDisabled {
		if err := triedb.Commit(root, false, nil); err != nil {
			return err
		}
	} else {
		// Full but not archive node, do proper garbage collection
		triedb.Reference(root, common.Hash{}) // metadata reference to keep trie alive
		bc.triegc.Push(root, -int64(block.NumberU64()))

		if current := block.NumberU64(); current > TriesInMemory {
			// If we exceeded our memory allowance, flush matured singleton nodes to disk
			var (
				nodes, imgs = triedb.Size()
				limit       = common.StorageSize(bc.cacheConfig.TrieDirtyLimit) * 1024 * 1024
			)
			if nodes > limit || imgs > 4*1024*1024 {
				triedb.Cap(limit - ethdb.IdealBatchSize)
			}
			// Find the next state trie we need to commit
			chosen := current - TriesInMemory

			// If we exceeded out time allowance, flush an entire trie to disk
			if bc.gcproc > bc.cacheConfig.TrieTimeLimit {
				// If the header is missing (canonical chain behind), we're reorging a low
				// diff sidechain. Suspend committing until this operation is completed.
				header := bc.GetHeaderByNumber(chosen)
				if header == nil {
					log.Warn("Reorg in progress, trie commit postponed", "number", chosen)
				} else {
					// If we're exceeding limits but haven't reached a large enough memory gap,
					// warn the user that the system is becoming unstable.
					if chosen < lastWrite+TriesInMemory && bc.gcproc >= 2*bc.cacheConfig.TrieTimeLimit {
						log.Info("State in memory for too long, committing", "time", bc.gcproc, "allowance", bc.cacheConfig.TrieTimeLimit, "optimum", float64(chosen-lastWrite)/TriesInMemory)
					}
					// Flush an entire trie and restart the counters
					triedb.Commit(header.Root[types.QuaiNetworkContext], true, nil)
					lastWrite = chosen
					bc.gcproc = 0
				}
			}
			// Garbage collect anything below our required write retention
			for !bc.triegc.Empty() {
				root, number := bc.triegc.Pop()
				if uint64(-number) > chosen {
					bc.triegc.Push(root, number)
					break
				}
				triedb.Dereference(root.(common.Hash))
			}
		}
	}
	return nil
}

// WriteBlockWithState writes the block and all associated state to the database.
func (bc *BlockChain) WriteBlockAndSetHead(block *types.Block, receipts []*types.Receipt, logs []*types.Log, state *state.StateDB, linkExtBlocks []*types.ExternalBlock, emitHeadEvent bool) (status WriteStatus, err error) {
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	return bc.writeBlockAndSetHead(block, receipts, logs, state, linkExtBlocks, emitHeadEvent)
}

// writeBlockAndSetHead writes the block and all associated state to the database,
// and also it applies the given block as the new chain head. This function expects
// the chain mutex to be held.
func (bc *BlockChain) writeBlockAndSetHead(block *types.Block, receipts []*types.Receipt, logs []*types.Log, state *state.StateDB, linkExtBlocks []*types.ExternalBlock, emitHeadEvent bool) (status WriteStatus, err error) {
	if err := bc.writeBlockWithState(block, receipts, logs, state, linkExtBlocks); err != nil {
		return NonStatTy, err
	}
	currentBlock := bc.CurrentBlock()
	reorg, err := bc.forker.ReorgNeeded(currentBlock.Header(), block.Header())
	if err != nil {
		return NonStatTy, err
	}

	// If the total difficulty is higher than our known, add it to the canonical chain
	// Second clause in the if statement reduces the vulnerability to selfish mining.
	// Please refer to http://www.cs.cornell.edu/~ie53/publications/btcProcFC.pdf
	if reorg {
		// Reorganise the chain if the parent is not the head block
		if block.ParentHash() != currentBlock.Hash() {
			if err := bc.reorg(currentBlock, block); err != nil {
				return NonStatTy, err
			}
		}
		status = CanonStatTy
		if err := bc.CheckLinkExtBlocks(block, linkExtBlocks); err != nil {
			return NonStatTy, err
		}
	} else {
		status = SideStatTy
	}
	// Set new head.
	if status == CanonStatTy {
		bc.writeHeadBlock(block)
	}
	bc.futureBlocks.Remove(block.Hash())

	if status == CanonStatTy {
		bc.chainFeed.Send(ChainEvent{Block: block, Hash: block.Hash(), Logs: logs})
		if len(logs) > 0 {
			bc.logsFeed.Send(logs)
		}
		// In theory we should fire a ChainHeadEvent when we inject
		// a canonical block, but sometimes we can insert a batch of
		// canonicial blocks. Avoid firing too many ChainHeadEvents,
		// we will fire an accumulated ChainHeadEvent and disable fire
		// event here.
		if emitHeadEvent {
			bc.chainHeadFeed.Send(ChainHeadEvent{Block: block})
		}
	} else {
		bc.chainUncleFeed.Send(block.Header())
		bc.chainSideFeed.Send(ChainSideEvent{Block: block})
	}
	return status, nil
}

// addFutureBlock checks if the block is within the max allowed window to get
// accepted for future processing, and returns an error if the block is too far
// ahead and was not added.
func (bc *BlockChain) addFutureBlock(block *types.Block) error {
	max := uint64(time.Now().Unix() + maxTimeFutureBlocks)
	if block.Time() > max {
		return fmt.Errorf("future block timestamp %v > allowed %v", block.Time(), max)
	}
	bc.futureBlocks.Add(block.Hash(), block)
	return nil
}

// AddExternalBlocks adds a group of external blocks to the cache
func (bc *BlockChain) AddExternalBlocks(blocks []*types.ExternalBlock) error {
	for _, extBlock := range blocks {
		err := bc.AddExternalBlock(extBlock)
		if err != nil {
			return err
		}
	}
	return nil
}

// addExternalBlock adds the received block to the external block cache.
func (bc *BlockChain) AddExternalBlock(block *types.ExternalBlock) error {
	context := []interface{}{
		"context", block.Context(), "numbers", block.Header().Number, "hash", block.Hash(), "location", block.Header().Location,
		"txs", len(block.Transactions()), "receipts", len(block.Receipts()),
	}
	log.Info("Adding external block", context...)
	data, err := rlp.EncodeToBytes(block)
	if err != nil {
		log.Crit("Failed to RLP encode external block", "err", err)
	}
	bc.externalBlocks.Set(block.CacheKey(), data)
	return nil
}

// ReOrgRollBack compares the difficulty of the newchain and oldchain. Rolls back
// the current header to the position where the reorg took place in a higher context
func (bc *BlockChain) ReOrgRollBack(header *types.Header, validHeaders []*types.Header, invalidHeaders []*types.Header) error {
	log.Info("Rolling back header beyond", "hash", header.Hash(), "from", bc.CurrentBlock().Header().Hash())
	bc.reorgmu.Lock()
	defer bc.reorgmu.Unlock()
	var (
		deletedTxs  types.Transactions
		deletedLogs [][]*types.Log

		// collectLogs collects the logs that were generated or removed during
		// the processing of the block that corresponds with the given hash.
		// These logs are later announced as deleted or reborn
		collectLogs = func(hash common.Hash) {
			number := bc.hc.GetBlockNumber(hash)
			if number == nil {
				return
			}
			receipts := rawdb.ReadReceipts(bc.db, hash, *number, bc.chainConfig)

			var logs []*types.Log
			for _, receipt := range receipts {
				for _, log := range receipt.Logs {
					l := *log
					l.Removed = true
					logs = append(logs, &l)
				}
			}
			if len(logs) > 0 {
				deletedLogs = append(deletedLogs, logs)
			}
		}
		// mergeLogs returns a merged log slice with specified sort order.
		mergeLogs = func(logs [][]*types.Log, reverse bool) []*types.Log {
			var ret []*types.Log
			if reverse {
				for i := len(logs) - 1; i >= 0; i-- {
					ret = append(ret, logs[i]...)
				}
			} else {
				for i := 0; i < len(logs); i++ {
					ret = append(ret, logs[i]...)
				}
			}
			return ret
		}
	)

	if header != nil {
		// get the commonBlock
		commonBlock := bc.GetBlockByHash(header.Hash())

		// if commonBlock isn't canoncial in our chain, do not reorg
		// because commonBlock parentHash could potentially be in our chain.

		// if a block with this hash does not exist then we dont have to roll back
		if commonBlock == nil {
			return nil
		}
		// get the current head in this chain
		currentBlock := bc.CurrentBlock()
		for {
			deletedTxs = append(deletedTxs, currentBlock.Transactions()...)
			collectLogs(currentBlock.Hash())
			if currentBlock.Hash() == commonBlock.Hash() {
				break
			}

			currentBlock = bc.GetBlock(currentBlock.ParentHash(), currentBlock.NumberU64()-1)
			if currentBlock == nil {
				return fmt.Errorf("invalid current chain")
			}
		}

		// Additional step is needed since we want to rollback 1 past the commonBlock.
		deletedTxs = append(deletedTxs, currentBlock.Transactions()...)
		collectLogs(currentBlock.Hash())
		currentBlock = bc.GetBlock(currentBlock.ParentHash(), currentBlock.NumberU64()-1)
		if currentBlock == nil {
			return fmt.Errorf("invalid current chain")
		}

		// set the head back to the block before the rollback point
		if err := bc.SetHead(commonBlock.NumberU64() - 1); err != nil {
			return err
		}
		// writing the head to the blockchain state
		bc.writeHeadBlock(currentBlock)
		bc.futureBlocks.Remove(currentBlock.Hash())

		// get all the receipts and extract the logs from it
		receipts := bc.GetReceiptsByHash(currentBlock.Hash())
		var logs []*types.Log

		for _, receipt := range receipts {
			logs = append(logs, receipt.Logs...)
		}
		// send a chain event so that it updates the pending header
		bc.chainFeed.Send(ChainEvent{Block: currentBlock, Hash: currentBlock.Hash(), Logs: logs})
		bc.chainHeadFeed.Send(ChainHeadEvent{Block: currentBlock})

		log.Info("Header is now rolled back and the current head is at block with ", "Hash ", bc.CurrentBlock().Hash(), " Number ", bc.CurrentBlock().NumberU64())

		// Delete useless indexes right now which includes the non-canonical
		// transaction indexes, canonical chain indexes which above the head.
		indexesBatch := bc.db.NewBatch()
		for _, tx := range deletedTxs {
			rawdb.DeleteTxLookupEntry(indexesBatch, tx.Hash())
		}

		// Delete any canonical number assignments above the new head
		number := bc.CurrentBlock().NumberU64()
		for i := number + 1; ; i++ {
			hash := rawdb.ReadCanonicalHash(bc.db, i)
			if hash == (common.Hash{}) {
				break
			}
			rawdb.DeleteCanonicalHash(indexesBatch, i)
		}
		if err := indexesBatch.Write(); err != nil {
			log.Crit("Failed to delete useless indexes", "err", err)
		}

		// send the deleted logs to the removed logs feed
		if len(deletedLogs) > 0 {
			bc.rmLogsFeed.Send(RemovedLogsEvent{mergeLogs(deletedLogs, true)})
		}
	} else {
		return fmt.Errorf("reorg header was null")
	}

	return nil
}

// InsertChain attempts to insert the given batch of blocks in to the canonical
// chain or, otherwise, create a fork. If an error is returned it will return
// the index number of the failing block as well an error describing what went
// wrong. After insertion is done, all accumulated events will be fired.
func (bc *BlockChain) InsertChain(chain types.Blocks) (int, error) {
	// Sanity check that we have something meaningful to import
	if len(chain) == 0 {
		return 0, nil
	}

	bc.blockProcFeed.Send(true)
	defer bc.blockProcFeed.Send(false)

	// Remove already known canon-blocks
	var (
		block, prev *types.Block
	)
	// Do a sanity check that the provided chain is actually ordered and linked
	for i := 1; i < len(chain); i++ {
		block = chain[i]
		prev = chain[i-1]
		if block.NumberU64() != prev.NumberU64()+1 || block.ParentHash() != prev.Hash() {
			// Chain broke ancestry, log a message (programming error) and skip insertion
			log.Error("Non contiguous block insert", "number", block.Number(), "hash", block.Hash(),
				"parent", block.ParentHash(), "prevnumber", prev.Number(), "prevhash", prev.Hash())

			return 0, fmt.Errorf("non contiguous insert: item %d is #%d [%x..], item %d is #%d [%x..] (parent [%x..])", i-1, prev.NumberU64(),
				prev.Hash().Bytes()[:4], i, block.NumberU64(), block.Hash().Bytes()[:4], block.ParentHash().Bytes()[:4])
		}
	}
	// Pre-checks passed, start the full block imports
	bc.reorgmu.Lock()
	bc.chainmu.Lock()
	n, err := bc.insertChain(chain, true, true)
	bc.chainmu.Unlock()
	bc.reorgmu.Unlock()

	return n, err
}

// InsertChainWithoutSealVerification works exactly the same
// except for seal verification, seal verification is omitted
func (bc *BlockChain) InsertChainWithoutSealVerification(block *types.Block) (int, error) {
	bc.blockProcFeed.Send(true)
	defer bc.blockProcFeed.Send(false)

	// Pre-checks passed, start the full block imports
	bc.wg.Add(1)
	bc.chainmu.Lock()
	n, err := bc.insertChain(types.Blocks([]*types.Block{block}), false, true)
	bc.chainmu.Unlock()
	bc.wg.Done()

	return n, err
}

// insertChain is the internal implementation of InsertChain, which assumes that
// 1) chains are contiguous, and 2) The chain mutex is held.
//
// This method is split out so that import batches that require re-injecting
// historical blocks can do so without releasing the lock, which could lead to
// racey behaviour. If a sidechain import is in progress, and the historic state
// is imported, but then new canon-head is added before the actual sidechain
// completes, then the historic state could be pruned again
func (bc *BlockChain) insertChain(chain types.Blocks, verifySeals bool, setHead bool) (int, error) {
	// If the chain is terminating, don't even bother starting up
	if atomic.LoadInt32(&bc.procInterrupt) == 1 {
		return 0, nil
	}
	// Start a parallel signature recovery (signer will fluke on fork transition, minimal perf loss)
	senderCacher.recoverFromBlocks(types.MakeSigner(bc.chainConfig, chain[0].Number()), chain)

	var (
		stats     = insertStats{startTime: mclock.Now()}
		lastCanon *types.Block
	)
	// Fire a single chain head event if we've progressed the chain
	defer func() {
		if lastCanon != nil && bc.CurrentBlock().Hash() == lastCanon.Hash() {
			bc.chainHeadFeed.Send(ChainHeadEvent{lastCanon})
		}
	}()
	// Start the parallel header verifier
	headers := make([]*types.Header, len(chain))
	seals := make([]bool, len(chain))

	for i, block := range chain {
		headers[i] = block.Header()
		seals[i] = verifySeals
	}
	abort, results := bc.engine.VerifyHeaders(bc, headers, seals)
	defer close(abort)

	// Peek the error for the first block to decide the directing import logic
	it := newInsertIterator(chain, results, bc.validator)
	block, err := it.next()

	// Left-trim all the known blocks
	if err == ErrKnownBlock {
		// First block (and state) is known
		//   1. We did a roll-back, and should now do a re-import
		//   2. The block is stored as a sidechain, and is lying about it's stateroot, and passes a stateroot
		// 	    from the canonical chain, which has not been verified.
		// Skip all known blocks that are behind us
		var (
			reorg   bool
			current = bc.CurrentBlock()
		)
		for block != nil && err == ErrKnownBlock {
			reorg, err = bc.forker.ReorgNeeded(current.Header(), block.Header())
			if err != nil {
				return it.index, err
			}
			if reorg {
				break
			}
			log.Info("Ignoring already known block", "number", block.Number(), "hash", block.Hash())
			stats.ignored++

			block, err = it.next()
		}
		// The remaining blocks are still known blocks, the only scenario here is:
		// During the fast sync, the pivot point is already submitted but rollback
		// happens. Then node resets the head full block to a lower height via `rollback`
		// and leaves a few known blocks in the database.
		//
		// When node runs a fast sync again, it can re-import a batch of known blocks via
		// `insertChain` while a part of them have higher total difficulty than current
		// head full block(new pivot point).
		for block != nil && err == ErrKnownBlock {
			log.Info("Writing previously known block", "number", block.Number(), "hash", block.Hash())
			if err := bc.writeKnownBlock(block); err != nil {
				return it.index, err
			}
			lastCanon = block

			block, err = it.next()
		}
		// Falls through to the block import
	}
	switch {
	// First block is pruned, insert as sidechain and reorg only if TD grows enough
	case errors.Is(err, consensus.ErrPrunedAncestor):
		log.Debug("Pruned ancestor, inserting as sidechain", "number", block.Number(), "hash", block.Hash())
		return bc.insertSideChain(block, it)

	// First block is future, shove it (and all children) to the future queue (unknown ancestor)
	case errors.Is(err, consensus.ErrFutureBlock) || (errors.Is(err, consensus.ErrUnknownAncestor) && bc.futureBlocks.Contains(it.first().ParentHash())):
		for block != nil && (it.index == 0 || errors.Is(err, consensus.ErrUnknownAncestor)) {
			log.Debug("Future block, postponing import", "number", block.Number(), "hash", block.Hash())
			if err := bc.addFutureBlock(block); err != nil {
				return it.index, err
			}
			block, err = it.next()
		}
		stats.queued += it.processed()
		stats.ignored += it.remaining()

		// If there are any still remaining, mark as ignored
		return it.index, err

		// Some other error occurred, abort
	case err != nil:
		bc.futureBlocks.Remove(block.Hash())
		stats.ignored += len(it.chain)
		bc.reportBlock(block, nil, err)
		return it.index, err
	}
	// No validation errors for the first block (or chain prefix skipped)
	var activeState *state.StateDB
	defer func() {
		// The chain importer is starting and stopping trie prefetchers. If a bad
		// block or other error is hit however, an early return may not properly
		// terminate the background threads. This defer ensures that we clean up
		// and dangling prefetcher, without defering each and holding on live refs.
		if activeState != nil {
			activeState.StopPrefetcher()
		}
	}()

	for ; block != nil && err == nil || err == ErrKnownBlock; block, err = it.next() {
		// If the chain is terminating, stop processing blocks
		if bc.insertStopped() {
			log.Debug("Abort during block processing")
			break
		}
		// If the header is a banned one, straight out abort
		if BadHashes[block.Hash()] {
			bc.reportBlock(block, nil, ErrBannedHash)
			return it.index, ErrBannedHash
		}
		// If the block is known (in the middle of the chain), it's a special case for
		// Clique blocks where they can share state among each other, so importing an
		// older block might complete the state of the subsequent one. In this case,
		// just skip the block (we already validated it once fully (and crashed), since
		// its header and body was already in the database).
		if err == ErrKnownBlock {
			logger := log.Debug
			if bc.chainConfig.Clique == nil {
				logger = log.Warn
			}
			logger("Inserted known block", "number", block.Number(), "hash", block.Hash(),
				"uncles", len(block.Uncles()), "txs", len(block.Transactions()), "gas", block.GasUsed(),
				"root", block.Root())

			// Special case. Commit the empty receipt slice if we meet the known
			// block in the middle. It can only happen in the clique chain. Whenever
			// we insert blocks via `insertSideChain`, we only commit `td`, `header`
			// and `body` if it's non-existent. Since we don't have receipts without
			// reexecution, so nothing to commit. But if the sidechain will be adpoted
			// as the canonical chain eventually, it needs to be reexecuted for missing
			// state, but if it's this special case here(skip reexecution) we will lose
			// the empty receipt entry.
			if len(block.Transactions()) == 0 {
				rawdb.WriteReceipts(bc.db, block.Hash(), block.NumberU64(), nil)
			} else {
				log.Error("Please file an issue, skip known block execution without receipt",
					"hash", block.Hash(), "number", block.NumberU64())
			}
			if err := bc.writeKnownBlock(block); err != nil {
				return it.index, err
			}
			stats.processed++

			// We can assume that logs are empty here, since the only way for consecutive
			// Clique blocks to have the same state is if there are no transactions.
			lastCanon = block
			continue
		}
		// Retrieve the parent block and it's state to execute on top
		start := time.Now()

		parent := it.previous()
		if parent == nil {
			parent = bc.GetHeader(block.ParentHash(), block.NumberU64()-1)
		}
		statedb, err := state.New(parent.Root[types.QuaiNetworkContext], bc.stateCache, bc.snaps)
		if err != nil {
			return it.index, err
		}
		// Enable prefetching to pull in trie node paths while processing transactions
		statedb.StartPrefetcher("chain")
		activeState = statedb

		// If we have a followup block, run that against the current state to pre-cache
		// transactions and probabilistically some of the account/storage trie nodes.
		var followupInterrupt uint32
		if !bc.cacheConfig.TrieCleanNoPrefetch {
			if followup, err := it.peek(); followup != nil && err == nil {
				throwaway, _ := state.New(parent.Root[types.QuaiNetworkContext], bc.stateCache, bc.snaps)

				go func(start time.Time, followup *types.Block, throwaway *state.StateDB, interrupt *uint32) {
					bc.prefetcher.Prefetch(followup, throwaway, bc.vmConfig, &followupInterrupt)

					blockPrefetchExecuteTimer.Update(time.Since(start))
					if atomic.LoadUint32(interrupt) == 1 {
						blockPrefetchInterruptMeter.Mark(1)
					}
				}(time.Now(), followup, throwaway, &followupInterrupt)
			}
		}
		// Process block using the parent state as reference point
		substart := time.Now()

		// If in Prime or Region, take to see if we have already included the hash in the lower level.
		// TODO: #179 Extend CheckHashInclusion to if the hash was ever included in the chain, not just the parent.
		err = bc.CheckHashInclusion(block.Header(), parent)
		if err != nil {
			bc.reportBlock(block, make(types.Receipts, 0), err)
			bc.chainUncleFeed.Send(block.Header())
			bc.futureBlocks.Remove(block.Hash())
			return it.index, err
		}

		// Process our block and retrieve external blocks.
		receipts, logs, usedGas, externalBlocks, err := bc.processor.Process(block, statedb, bc.vmConfig)
		if err != nil {
			bc.reportBlock(block, receipts, err)
			atomic.StoreUint32(&followupInterrupt, 1)
			bc.futureBlocks.Remove(block.Hash())
			return it.index, err
		}
		// Update the metrics touched during block processing
		accountReadTimer.Update(statedb.AccountReads)                 // Account reads are complete, we can mark them
		storageReadTimer.Update(statedb.StorageReads)                 // Storage reads are complete, we can mark them
		accountUpdateTimer.Update(statedb.AccountUpdates)             // Account updates are complete, we can mark them
		storageUpdateTimer.Update(statedb.StorageUpdates)             // Storage updates are complete, we can mark them
		snapshotAccountReadTimer.Update(statedb.SnapshotAccountReads) // Account reads are complete, we can mark them
		snapshotStorageReadTimer.Update(statedb.SnapshotStorageReads) // Storage reads are complete, we can mark them
		triehash := statedb.AccountHashes + statedb.StorageHashes     // Save to not double count in validation
		trieproc := statedb.SnapshotAccountReads + statedb.AccountReads + statedb.AccountUpdates
		trieproc += statedb.SnapshotStorageReads + statedb.StorageReads + statedb.StorageUpdates

		blockExecutionTimer.Update(time.Since(substart) - trieproc - triehash)

		// Validate the state using the default validator
		substart = time.Now()
		if err := bc.validator.ValidateState(block, statedb, receipts, usedGas); err != nil {
			bc.reportBlock(block, receipts, err)
			atomic.StoreUint32(&followupInterrupt, 1)
			return it.index, err
		}
		proctime := time.Since(start)

		// Update the metrics touched during block validation
		accountHashTimer.Update(statedb.AccountHashes) // Account hashes are complete, we can mark them
		storageHashTimer.Update(statedb.StorageHashes) // Storage hashes are complete, we can mark them

		blockValidationTimer.Update(time.Since(substart) - (statedb.AccountHashes + statedb.StorageHashes - triehash))

		linkExtBlocks, err := bc.engine.GetLinkExternalBlocks(bc, block.Header(), true)
		if err != nil {
			bc.reportBlock(block, receipts, err)
			bc.chainUncleFeed.Send(block.Header())
			return it.index, err
		}

		// Write the block to the chain and get the status.
		substart = time.Now()
		var status WriteStatus
		if !setHead {
			// Don't set the head, only insert the block
			err = bc.writeBlockWithState(block, receipts, logs, statedb, linkExtBlocks)
		} else {
			status, err = bc.writeBlockAndSetHead(block, receipts, logs, statedb, linkExtBlocks, false)
		}
		atomic.StoreUint32(&followupInterrupt, 1)
		if err != nil {
			bc.reportBlock(block, receipts, err)
			bc.chainUncleFeed.Send(block.Header())
			return it.index, err
		}

		// Update the metrics touched during block commit
		accountCommitTimer.Update(statedb.AccountCommits)   // Account commits are complete, we can mark them
		storageCommitTimer.Update(statedb.StorageCommits)   // Storage commits are complete, we can mark them
		snapshotCommitTimer.Update(statedb.SnapshotCommits) // Snapshot commits are complete, we can mark them

		blockWriteTimer.Update(time.Since(substart) - statedb.AccountCommits - statedb.StorageCommits - statedb.SnapshotCommits)
		blockInsertTimer.UpdateSince(start)

		if !setHead {
			// We did not setHead, so we don't have any stats to update
			log.Info("Inserted block", "number", block.Number(), "hash", block.Hash(), "txs", len(block.Transactions()), "elapsed", common.PrettyDuration(time.Since(start)))
			return it.index, nil
		}

		switch status {
		case CanonStatTy:
			bc.StoreExternalBlocks(linkExtBlocks)
			log.Info("Inserted new block", "number", block.Header().Number, "hash", block.Hash(), "extBlocks", len(externalBlocks),
				"uncles", len(block.Uncles()), "txs", len(block.Transactions()), "gas", block.GasUsed(),
				"elapsed", common.PrettyDuration(time.Since(start)),
				"root", block.Root())

			lastCanon = block

			// Only count canonical blocks for GC processing time
			bc.gcproc += proctime

		case SideStatTy:
			log.Info("Inserted forked block", "number", block.Number(), "hash", block.Hash(),
				"diff", block.Difficulty(), "elapsed", common.PrettyDuration(time.Since(start)),
				"txs", len(block.Transactions()), "gas", block.GasUsed(), "uncles", len(block.Uncles()),
				"root", block.Root())

		default:
			// This in theory is impossible, but lets be nice to our future selves and leave
			// a log, instead of trying to track down blocks imports that don't emit logs.
			log.Info("Inserted block with unknown status", "number", block.Number(), "hash", block.Hash(),
				"diff", block.Difficulty(), "elapsed", common.PrettyDuration(time.Since(start)),
				"txs", len(block.Transactions()), "gas", block.GasUsed(), "uncles", len(block.Uncles()),
				"root", block.Root())
		}
		stats.processed++
		stats.usedGas += usedGas

		dirty, _ := bc.stateCache.TrieDB().Size()
		stats.report(chain, it.index, dirty)
	}
	// Any blocks remaining here? The only ones we care about are the future ones
	if block != nil && errors.Is(err, consensus.ErrFutureBlock) {
		if err := bc.addFutureBlock(block); err != nil {
			return it.index, err
		}
		block, err = it.next()

		for ; block != nil && errors.Is(err, consensus.ErrUnknownAncestor); block, err = it.next() {
			if err := bc.addFutureBlock(block); err != nil {
				return it.index, err
			}
			stats.queued++
		}
	}
	stats.ignored += it.remaining()

	return it.index, err
}

// insertSideChain is called when an import batch hits upon a pruned ancestor
// error, which happens when a sidechain with a sufficiently old fork-block is
// found.
//
// The method writes all (header-and-body-valid) blocks to disk, then tries to
// switch over to the new chain if the TD exceeded the current chain.
func (bc *BlockChain) insertSideChain(block *types.Block, it *insertIterator) (int, error) {
	var (
		externTd  []*big.Int
		lastBlock = block
		current   = bc.CurrentBlock()
	)
	// The first sidechain block error is already verified to be ErrPrunedAncestor.
	// Since we don't import them here, we expect ErrUnknownAncestor for the remaining
	// ones. Any other errors means that the block is invalid, and should not be written
	// to disk.
	err := consensus.ErrPrunedAncestor
	for ; block != nil && errors.Is(err, consensus.ErrPrunedAncestor); block, err = it.next() {
		// Check the canonical state root for that number
		if number := block.NumberU64(); current.NumberU64() >= number {
			canonical := bc.GetBlockByNumber(number)
			if canonical != nil && canonical.Hash() == block.Hash() {
				// Not a sidechain block, this is a re-import of a canon block which has it's state pruned

				// Collect the TD of the block. Since we know it's a canon one,
				// we can get it directly, and not (like further below) use
				// the parent and then add the block on top
				externTd = bc.GetTd(block.Hash(), block.NumberU64())
				continue
			}
			if canonical != nil && canonical.Root() == block.Root() {
				// This is most likely a shadow-state attack. When a fork is imported into the
				// database, and it eventually reaches a block height which is not pruned, we
				// just found that the state already exist! This means that the sidechain block
				// refers to a state which already exists in our canon chain.
				//
				// If left unchecked, we would now proceed importing the blocks, without actually
				// having verified the state of the previous blocks.
				log.Warn("Sidechain ghost-state attack detected", "number", block.NumberU64(), "sideroot", block.Root(), "canonroot", canonical.Root())

				// If someone legitimately side-mines blocks, they would still be imported as usual. However,
				// we cannot risk writing unverified blocks to disk when they obviously target the pruning
				// mechanism.
				return it.index, errors.New("sidechain ghost-state attack")
			}
		}
		if externTd == nil {
			externTd, err = bc.CalcTd(block.Header())
			if err != nil {
				return it.index, err
			}
		}

		if !bc.HasBlock(block.Hash(), block.NumberU64()) {
			start := time.Now()
			if err := bc.writeBlockWithoutState(block, externTd); err != nil {
				return it.index, err
			}
			log.Debug("Injected sidechain block", "number", block.Number(), "hash", block.Hash(),
				"diff", block.Difficulty(), "elapsed", common.PrettyDuration(time.Since(start)),
				"txs", len(block.Transactions()), "gas", block.GasUsed(), "uncles", len(block.Uncles()),
				"root", block.Root())
		}
		lastBlock = block
	}
	// At this point, we've written all sidechain blocks to database. Loop ended
	// either on some other error or all were processed. If there was some other
	// error, we can ignore the rest of those blocks.
	//
	// If the externTd was larger than our local TD, we now need to reimport the previous
	// blocks to regenerate the required state
	reorg, err := bc.forker.ReorgNeeded(current.Header(), lastBlock.Header())
	if err != nil {
		return it.index, err
	}
	if !reorg {
		localTd := bc.GetTd(current.Hash(), current.NumberU64())
		log.Info("Sidechain written to disk", "start", it.first().NumberU64(), "end", it.previous().Number, "sidetd", externTd, "localtd", localTd)
		return it.index, err
	}
	// Gather all the sidechain hashes (full blocks may be memory heavy)
	var (
		hashes  []common.Hash
		numbers []uint64
	)
	parent := it.previous()
	for parent != nil && !bc.HasState(parent.Root[types.QuaiNetworkContext]) {
		hashes = append(hashes, parent.Hash())
		numbers = append(numbers, parent.Number[types.QuaiNetworkContext].Uint64())

		parent = bc.GetHeader(parent.ParentHash[types.QuaiNetworkContext], parent.Number[types.QuaiNetworkContext].Uint64()-1)
	}
	if parent == nil {
		return it.index, errors.New("missing parent")
	}
	// Import all the pruned blocks to make the state available
	var (
		blocks []*types.Block
		memory common.StorageSize
	)
	for i := len(hashes) - 1; i >= 0; i-- {
		// Append the next block to our batch
		block := bc.GetBlock(hashes[i], numbers[i])

		blocks = append(blocks, block)
		memory += block.Size()

		// If memory use grew too large, import and continue. Sadly we need to discard
		// all raised events and logs from notifications since we're too heavy on the
		// memory here.
		if len(blocks) >= 2048 || memory > 64*1024*1024 {
			log.Info("Importing heavy sidechain segment", "blocks", len(blocks), "start", blocks[0].NumberU64(), "end", block.NumberU64())
			if _, err := bc.insertChain(blocks, false, true); err != nil {
				return 0, err
			}
			blocks, memory = blocks[:0], 0

			// If the chain is terminating, stop processing blocks
			if bc.insertStopped() {
				log.Debug("Abort during blocks processing")
				return 0, nil
			}
		}
	}
	if len(blocks) > 0 {
		log.Info("Importing sidechain segment", "start", blocks[0].NumberU64(), "end", blocks[len(blocks)-1].NumberU64())
		return bc.insertChain(blocks, false, true)
	}
	return 0, nil
}

func (bc *BlockChain) getAllHeaders(blocks []*types.Block) []*types.Header {
	// Initialize the headers array
	var headers []*types.Header

	// Find all the headers since genesis
	for i := 0; i < len(blocks); i++ {
		headers = append(headers, blocks[i].Header())
	}
	return headers
}

// collectLogs collects the logs that were generated or removed during
// the processing of the block that corresponds with the given hash.
// These logs are later announced as deleted or reborn.
func (bc *BlockChain) collectLogs(hash common.Hash, removed bool) []*types.Log {
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	receipts := rawdb.ReadReceipts(bc.db, hash, *number, bc.chainConfig)

	var logs []*types.Log
	for _, receipt := range receipts {
		for _, log := range receipt.Logs {
			l := *log
			if removed {
				l.Removed = true
			}
			logs = append(logs, &l)
		}
	}
	return logs
}

// mergeLogs returns a merged log slice with specified sort order.
func mergeLogs(logs [][]*types.Log, reverse bool) []*types.Log {
	var ret []*types.Log
	if reverse {
		for i := len(logs) - 1; i >= 0; i-- {
			ret = append(ret, logs[i]...)
		}
	} else {
		for i := 0; i < len(logs); i++ {
			ret = append(ret, logs[i]...)
		}
	}
	return ret
}

// reorg takes two blocks, an old chain and a new chain and will reconstruct the
// blocks and inserts them to be part of the new canonical chain and accumulates
// potential missing transactions and post an event about them.
func (bc *BlockChain) reorg(oldBlock, newBlock *types.Block) error {
	var (
		newChain    types.Blocks
		oldChain    types.Blocks
		commonBlock *types.Block

		deletedTxs types.Transactions
		addedTxs   types.Transactions

		deletedLogs [][]*types.Log
		rebirthLogs [][]*types.Log
	)

	// Reduce the longer chain to the same number as the shorter one
	if oldBlock.NumberU64() > newBlock.NumberU64() {
		// Old chain is longer, gather all transactions and logs as deleted ones
		for ; oldBlock != nil && oldBlock.NumberU64() != newBlock.NumberU64(); oldBlock = bc.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1) {
			oldChain = append(oldChain, oldBlock)
			deletedTxs = append(deletedTxs, oldBlock.Transactions()...)

			// Collect deleted logs for notification
			logs := bc.collectLogs(oldBlock.Hash(), true)
			if len(logs) > 0 {
				deletedLogs = append(deletedLogs, logs)
			}
		}
	} else {
		// New chain is longer, stash all blocks away for subsequent insertion
		for ; newBlock != nil && newBlock.NumberU64() != oldBlock.NumberU64(); newBlock = bc.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1) {
			newChain = append(newChain, newBlock)
		}
	}
	if oldBlock == nil {
		return fmt.Errorf("invalid old chain")
	}
	if newBlock == nil {
		return fmt.Errorf("invalid new chain")
	}
	// Both sides of the reorg are at the same number, reduce both until the common
	// ancestor is found
	for {
		// If the common ancestor was found, bail out
		if oldBlock.Hash() == newBlock.Hash() {
			commonBlock = oldBlock

			// Once the common block is found, the reorg data is sent to the reOrg feed
			bc.reOrgFeed.Send(ReOrgRollup{ReOrgHeader: commonBlock.Header(), OldChainHeaders: bc.getAllHeaders(oldChain), NewChainHeaders: bc.getAllHeaders(newChain)})
			break
		}
		// Remove an old block as well as stash away a new block
		oldChain = append(oldChain, oldBlock)
		deletedTxs = append(deletedTxs, oldBlock.Transactions()...)

		// Collect deleted logs for notification
		logs := bc.collectLogs(oldBlock.Hash(), true)
		if len(logs) > 0 {
			deletedLogs = append(deletedLogs, logs)
		}

		newChain = append(newChain, newBlock)

		// Step back with both chains
		oldBlock = bc.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1)
		if oldBlock == nil {
			return fmt.Errorf("invalid old chain")
		}
		newBlock = bc.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1)
		if newBlock == nil {
			return fmt.Errorf("invalid new chain")
		}
	}
	// Ensure the user sees large reorgs
	if len(oldChain) > 0 && len(newChain) > 0 {
		logFn := log.Info
		msg := "Chain reorg detected"
		if len(oldChain) > 63 {
			msg = "Large chain reorg detected"
			logFn = log.Warn
		}
		logFn(msg, "number", commonBlock.Number(), "hash", commonBlock.Hash(),
			"drop", len(oldChain), "dropfrom", oldChain[0].Hash(), "add", len(newChain), "addfrom", newChain[0].Hash())
		blockReorgAddMeter.Mark(int64(len(newChain)))
		blockReorgDropMeter.Mark(int64(len(oldChain)))
		blockReorgMeter.Mark(1)
	} else {
		log.Error("Impossible reorg, please file an issue", "oldnum", oldBlock.Number(), "oldhash", oldBlock.Hash(), "newnum", newBlock.Number(), "newhash", newBlock.Hash())
	}
	// Insert the new chain(except the head block(reverse order)),
	// taking care of the proper incremental order.
	for i := len(newChain) - 1; i >= 1; i-- {
		// Insert the block in the canonical way, re-writing history
		bc.writeHeadBlock(newChain[i])

		// Collect reborn logs due to chain reorg
		logs := bc.collectLogs(newChain[i].Hash(), false)
		if len(logs) > 0 {
			rebirthLogs = append(rebirthLogs, logs)
		}

		// Collect the new added transactions.
		addedTxs = append(addedTxs, newChain[i].Transactions()...)
	}
	// Delete useless indexes right now which includes the non-canonical
	// transaction indexes, canonical chain indexes which above the head.
	indexesBatch := bc.db.NewBatch()
	for _, tx := range types.TxDifference(deletedTxs, addedTxs) {
		rawdb.DeleteTxLookupEntry(indexesBatch, tx.Hash())
	}
	// Delete any canonical number assignments above the new head
	number := bc.CurrentBlock().NumberU64()
	for i := number + 1; ; i++ {
		hash := rawdb.ReadCanonicalHash(bc.db, i)
		if hash == (common.Hash{}) {
			break
		}
		rawdb.DeleteCanonicalHash(indexesBatch, i)
	}
	if err := indexesBatch.Write(); err != nil {
		log.Crit("Failed to delete useless indexes", "err", err)
	}
	// If any logs need to be fired, do it now. In theory we could avoid creating
	// this goroutine if there are no events to fire, but realistcally that only
	// ever happens if we're reorging empty blocks, which will only happen on idle
	// networks where performance is not an issue either way.
	if len(deletedLogs) > 0 {
		bc.rmLogsFeed.Send(RemovedLogsEvent{mergeLogs(deletedLogs, true)})
	}
	if len(rebirthLogs) > 0 {
		bc.logsFeed.Send(mergeLogs(rebirthLogs, false))
	}
	if len(oldChain) > 0 {
		for i := len(oldChain) - 1; i >= 0; i-- {
			bc.chainSideFeed.Send(ChainSideEvent{Block: oldChain[i]})
		}
	}

	return nil
}

func (bc *BlockChain) update() {
	futureTimer := time.NewTicker(5 * time.Second)
	defer futureTimer.Stop()
	defer bc.wg.Done()
	for {
		select {
		case <-futureTimer.C:
			bc.procFutureBlocks()
		case <-bc.quit:
			return
		}
	}
}

// maintainTxIndex is responsible for the construction and deletion of the
// transaction index.
//
// User can use flag `txlookuplimit` to specify a "recentness" block, below
// which ancient tx indices get deleted. If `txlookuplimit` is 0, it means
// all tx indices will be reserved.
//
// The user can adjust the txlookuplimit value for each launch after fast
// sync, Geth will automatically construct the missing indices and delete
// the extra indices.
func (bc *BlockChain) maintainTxIndex(ancients uint64) {
	defer bc.wg.Done()

	// Before starting the actual maintenance, we need to handle a special case,
	// where user might init Geth with an external ancient database. If so, we
	// need to reindex all necessary transactions before starting to process any
	// pruning requests.
	if ancients > 0 {
		var from = uint64(0)
		if bc.txLookupLimit != 0 && ancients > bc.txLookupLimit {
			from = ancients - bc.txLookupLimit
		}
		rawdb.IndexTransactions(bc.db, from, ancients, bc.quit)
	}
	// indexBlocks reindexes or unindexes transactions depending on user configuration
	indexBlocks := func(tail *uint64, head uint64, done chan struct{}) {
		defer func() { done <- struct{}{} }()

		// If the user just upgraded Geth to a new version which supports transaction
		// index pruning, write the new tail and remove anything older.
		if tail == nil {
			if bc.txLookupLimit == 0 || head < bc.txLookupLimit {
				// Nothing to delete, write the tail and return
				rawdb.WriteTxIndexTail(bc.db, 0)
			} else {
				// Prune all stale tx indices and record the tx index tail
				rawdb.UnindexTransactions(bc.db, 0, head-bc.txLookupLimit+1, bc.quit)
			}
			return
		}
		// If a previous indexing existed, make sure that we fill in any missing entries
		if bc.txLookupLimit == 0 || head < bc.txLookupLimit {
			if *tail > 0 {
				rawdb.IndexTransactions(bc.db, 0, *tail, bc.quit)
			}
			return
		}
		// Update the transaction index to the new chain state
		if head-bc.txLookupLimit+1 < *tail {
			// Reindex a part of missing indices and rewind index tail to HEAD-limit
			rawdb.IndexTransactions(bc.db, head-bc.txLookupLimit+1, *tail, bc.quit)
		} else {
			// Unindex a part of stale indices and forward index tail to HEAD-limit
			rawdb.UnindexTransactions(bc.db, *tail, head-bc.txLookupLimit+1, bc.quit)
		}
	}
	// Any reindexing done, start listening to chain events and moving the index window
	var (
		done   chan struct{}                  // Non-nil if background unindexing or reindexing routine is active.
		headCh = make(chan ChainHeadEvent, 1) // Buffered to avoid locking up the event feed
	)
	sub := bc.SubscribeChainHeadEvent(headCh)
	if sub == nil {
		return
	}
	defer sub.Unsubscribe()

	for {
		select {
		case head := <-headCh:
			if done == nil {
				done = make(chan struct{})
				go indexBlocks(rawdb.ReadTxIndexTail(bc.db), head.Block.NumberU64(), done)
			}
		case <-done:
			done = nil
		case <-bc.quit:
			if done != nil {
				log.Info("Waiting background transaction indexer to exit")
				<-done
			}
			return
		}
	}
}

// reportBlock logs a bad block error.
func (bc *BlockChain) reportBlock(block *types.Block, receipts types.Receipts, err error) {
	rawdb.WriteBadBlock(bc.db, block)

	var receiptString string
	for i, receipt := range receipts {
		receiptString += fmt.Sprintf("\t %d: cumulative: %v gas: %v contract: %v status: %v tx: %v logs: %v bloom: %x state: %x\n",
			i, receipt.CumulativeGasUsed, receipt.GasUsed, receipt.ContractAddress.Hex(),
			receipt.Status, receipt.TxHash.Hex(), receipt.Logs, receipt.Bloom, receipt.PostState)
	}
	log.Error(fmt.Sprintf(`
########## BAD BLOCK #########
Chain config: %v

Number: %v
Hash: 0x%x
%v

Error: %v
##############################
`, bc.chainConfig, block.Number(), block.Hash(), receiptString, err))
}

// InsertHeaderChain attempts to insert the given header chain in to the local
// chain, possibly creating a reorg. If an error is returned, it will return the
// index number of the failing header as well an error describing what went wrong.
//
// The verify parameter can be used to fine tune whether nonce verification
// should be done or not. The reason behind the optional check is because some
// of the header retrieval mechanisms already need to verify nonces, as well as
// because nonces can be verified sparsely, not needing to check each.
func (bc *BlockChain) InsertHeaderChain(chain []*types.Header, checkFreq int) (int, error) {
	start := time.Now()
	if i, err := bc.hc.ValidateHeaderChain(chain, checkFreq); err != nil {
		return i, err
	}

	// Make sure only one thread manipulates the chain at once
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	bc.wg.Add(1)
	defer bc.wg.Done()
	_, err := bc.hc.InsertHeaderChain(chain, start)
	return 0, err
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (bc *BlockChain) CurrentHeader() *types.Header {
	return bc.hc.CurrentHeader()
}

// GetTd retrieves a block's total difficulty in the canonical chain from the
// database by hash and number, caching it if found.
func (bc *BlockChain) GetTd(hash common.Hash, number uint64) []*big.Int {
	return bc.hc.GetTd(hash, number)
}

// GetTdByHash retrieves a block's total difficulty in the canonical chain from the
// database by hash, caching it if found.
func (bc *BlockChain) GetTdByHash(hash common.Hash) []*big.Int {
	return bc.hc.GetTdByHash(hash)
}

// GetHeader retrieves a block header from the database by hash and number,
// caching it if found.
func (bc *BlockChain) GetHeader(hash common.Hash, number uint64) *types.Header {
	// Blockchain might have cached the whole block, only if not go to headerchain
	if block, ok := bc.blockCache.Get(hash); ok {
		return block.(*types.Block).Header()
	}

	return bc.hc.GetHeader(hash, number)
}

// CheckHashInclusion checks to see if a hash is already included in a previous block.
func (bc *BlockChain) CheckHashInclusion(header *types.Header, parent *types.Header) error {

	// If we are in Prime node, check to see if the subordinate Region hash included in the parent block
	// is the same as the hash we are trying to include in the current block.
	// Need to run when number is greater than 1 for the edge case of new Regions / Zones being mined in sequentially.
	if types.QuaiNetworkContext < 1 {
		if header.ParentHash[1] == parent.ParentHash[1] && header.Number[1].Cmp(big.NewInt(1)) > 0 {
			return fmt.Errorf("error subordinate hash already included in parent")
		}
	}

	// If we are in a Prime or Region node, check to see if the subordinate Zone hash included in the parent block
	// is the same as the hash we are trying to include in the current block.
	if types.QuaiNetworkContext < 2 {
		if header.ParentHash[2] == parent.ParentHash[2] && header.Number[2].Cmp(big.NewInt(1)) > 0 {
			return fmt.Errorf("error subordinate hash already included in parent")
		}
	}

	if types.QuaiNetworkContext == 2 {
		// Upper level check
		currentBlock := bc.CurrentBlock()

		if currentBlock.NumberU64() == 0 {
			return nil
		}

		currOrder, err := bc.Engine().GetDifficultyOrder(currentBlock.Header())
		if err != nil {
			return err
		}

		newOrder, err := bc.Engine().GetDifficultyOrder(header)
		if err != nil {
			return err
		}

		// Attempting to stop blocks from being included / triggering reorg that are waiting on Zone updates.
		// We see this in the following scenario when the Zone block that already was included in a Region reorgs
		// the region block.
		// REGION: [6 8 60] 0x657dc29c4b2ac624638707b660e2bef4897a82efe4cc6e91471ae37d50bd665d
		// ZONE:   [6 9 60] 0xedd4cdc59db07818f9b5c38a71d5f68c2d661168e1b0b57570614b05b6264211
		if currOrder == newOrder && header.ParentHash[newOrder] != currentBlock.Header().ParentHash[currOrder] {
			if header.ParentHash[2] == currentBlock.Header().ParentHash[2] {
				return fmt.Errorf("error subordinate hash already included in parent")
			}
		}
	}

	return nil
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (bc *BlockChain) GetHeaderByHash(hash common.Hash) *types.Header {
	// Blockchain might have cached the whole block, only if not go to headerchain
	if block, ok := bc.blockCache.Get(hash); ok {
		return block.(*types.Block).Header()
	}

	return bc.hc.GetHeaderByHash(hash)
}

// GetExternalBlock retrieves an external block from either the ext block cache or rawdb.
func (bc *BlockChain) GetExternalBlock(hash common.Hash, number uint64, location []byte, context uint64) (*types.ExternalBlock, error) {
	// Lookup block in externalBlocks cache
	key := types.ExtBlockCacheKey(number, context, hash)

	if block, ok := bc.externalBlocks.HasGet(nil, key); ok {
		var blockDecoded *types.ExternalBlock
		rlp.DecodeBytes(block, &blockDecoded)
		return blockDecoded, nil
	}
	block := rawdb.ReadExternalBlock(bc.db, hash, number, context)

	if block == nil {
		block = bc.requestExternalBlock(hash, number, location, context)
		if block == nil {
			return &types.ExternalBlock{}, errExtBlockNotFound
		}
	}
	return block, nil
}

// requestExternalBlock sends an external block event to the missingExternalBlockFeed in order to be fulfilled by a manager or client.
func (bc *BlockChain) requestExternalBlock(hash common.Hash, number uint64, location []byte, context uint64) *types.ExternalBlock {
	bc.missingExternalBlockFeed.Send(MissingExternalBlock{Hash: hash, Location: location, Context: int(context)})
	for i := 0; i < params.ExternalBlockLookupLimit; i++ {
		time.Sleep(time.Duration(params.ExternalBlockLookupDelay) * time.Millisecond)
		// Lookup block in externalBlocks cache
		key := types.ExtBlockCacheKey(number, context, hash)
		if block, ok := bc.externalBlocks.HasGet(nil, key); ok {
			var blockDecoded *types.ExternalBlock
			rlp.DecodeBytes(block, &blockDecoded)
			return blockDecoded
		}
		block := rawdb.ReadExternalBlock(bc.db, hash, number, context)
		if block != nil {
			return block
		}
	}
	return nil
}

// StoreExternalBlocks removes the external block from the cached blocks and writes it into the database
func (bc *BlockChain) StoreExternalBlocks(blocks []*types.ExternalBlock) error {

	for i := 0; i < len(blocks); i++ {
		context := blocks[i].Context().Uint64()
		number := blocks[i].Header().Number[context].Uint64()
		hash := blocks[i].Hash()

		// Lookup block in externalBlocks cache
		key := types.ExtBlockCacheKey(number, context, hash)
		bc.externalBlocks.Del(key)

		rawdb.WriteExternalBlock(bc.db, blocks[i])
	}
	return nil
}

// GetExternalBlocks retrieves the external blocks for a given header. Will call the necessary
// TraceBranch functionality.
func (bc *BlockChain) GetExternalBlocks(header *types.Header) ([]*types.ExternalBlock, error) {
	// Lookup block in externalBlocks cache
	context := bc.Config().Context // Index that node is currently at
	externalBlocks := make([]*types.ExternalBlock, 0)

	// Check if header is nil
	if header == nil {
		return nil, fmt.Errorf("error getting external blocks for nil header")
	}

	// Check header number
	if header.Number == nil {
		return nil, fmt.Errorf("error getting external blocks for header with nil number")
	}

	// Do not run on block 1
	if header.Number[context].Cmp(big.NewInt(1)) > 0 {
		prevHeader := bc.GetHeaderByHash(header.ParentHash[context])
		difficultyContext, err := bc.engine.CheckPrevHeaderCoincident(bc, context, prevHeader)
		if err != nil {
			return nil, err
		}

		// Check if in Zone and PrevHeader is not a coincident header, no external blocks to trace.
		if context == 2 && difficultyContext == 2 {
			return externalBlocks, nil
		}

		externalBlocks, err = bc.engine.TraceBranches(bc, prevHeader, difficultyContext, context, header.Location)
		if err != nil {
			return nil, err
		}
	}

	return externalBlocks, nil
}

// GetLinkExternalBlocks retrieves the external link blocks for a given header. Will call the necessary
// TraceBranch functionality.
func (bc *BlockChain) GetLinkExternalBlocks(header *types.Header) ([]*types.ExternalBlock, error) {
	// Lookup block in externalBlocks cache
	context := bc.Config().Context // Index that node is currently at
	externalBlocks := make([]*types.ExternalBlock, 0)

	// Check if header is nil
	if header == nil || header.Number == nil {
		return nil, fmt.Errorf("error getting external blocks for nil header")
	}

	difficultyContext, err := bc.engine.GetDifficultyOrder(header)
	if err != nil {
		return nil, err
	}

	// Check if in Zone and PrevHeader is not a coincident header, no external blocks to trace.
	if context == 2 && difficultyContext == 2 {
		return externalBlocks, nil
	}

	externalBlocks, err = bc.engine.TraceBranches(bc, header, difficultyContext, context, header.Location)
	if err != nil {
		return nil, err
	}

	return externalBlocks, nil
}

// QueueExternalBlocks takes a set of external blocks and adds them to the queue
func (bc *BlockChain) QueueAndRetrieveExtBlocks(externalBlocks []*types.ExternalBlock, header *types.Header) []*types.ExternalBlock {
	for _, block := range externalBlocks {
		bc.externalBlockQueue.Add(block.Hash(), block)
	}
	resultBlocks := make([]*types.ExternalBlock, 0)
	gasUsed := 0
	for {
		key, result, ok := bc.externalBlockQueue.GetOldest()
		if !ok || result == nil {
			break
		}

		externalBlock := result.(*types.ExternalBlock)

		// Must append before gas check since it will never undo queue if 1 block exceeds limit
		resultBlocks = append(resultBlocks, externalBlock)
		bc.externalBlockQueue.Remove(key)

		for _, tx := range externalBlock.Transactions() {
			receipt := externalBlock.ReceiptForTransaction(tx)
			gasUsed += int(receipt.GasUsed)
		}
		// If the total gasUsed for external transactions exceeds this blocks gasLimit by 50% break
		if gasUsed > int(header.GasLimit[types.QuaiNetworkContext]/2) {
			break
		}
	}
	log.Info("QueueAndRetrieveExtBlocks: Returning result blocks", "len", len(resultBlocks))
	return resultBlocks
}

// CheckLinkExtBlocks will check if the passed in extBlocks are valid against a block.
func (bc *BlockChain) CheckLinkExtBlocks(block *types.Block, linkExtBlocks []*types.ExternalBlock) error {
	if len(linkExtBlocks) > 0 {
		duplicateErr := bc.CheckExtBlockDuplicates(linkExtBlocks)
		if duplicateErr != nil {
			return duplicateErr
		}

		collisionErr := bc.checkExtBlockCollision(block.Header(), linkExtBlocks)
		if collisionErr != nil {
			return collisionErr
		}
	}
	return nil
}

// CheckExtBlockDuplicates iterates external blocks to ensure that no duplicates are included.
func (bc *BlockChain) CheckExtBlockDuplicates(externalBlocks []*types.ExternalBlock) error {
	m := make(map[string]bool)
	for _, extBlock := range externalBlocks {
		if _, ok := m[string(extBlock.CacheKey())]; !ok {
			m[string(extBlock.CacheKey())] = true
		} else {
			return fmt.Errorf("duplicate external blocks contained in link trace")
		}
	}
	return nil
}

// CheckExtBlockDuplicates iterates external blocks to ensure that no duplicates are included.
func (bc *BlockChain) checkExtBlockCollision(header *types.Header, externalBlocks []*types.ExternalBlock) error {
	for _, extBlock := range externalBlocks {
		equalLocation := bytes.Compare(extBlock.Header().Location, header.Location) == 0
		greaterContext := int(extBlock.Context().Int64()) < types.QuaiNetworkContext

		subExtBlockNum := extBlock.Header().Number[types.QuaiNetworkContext]
		subHeaderNum := header.Number[types.QuaiNetworkContext]

		domExtBlockNum := extBlock.Header().Number[extBlock.Context().Int64()]
		domHeaderNum := header.Number[extBlock.Context().Int64()]

		if equalLocation && greaterContext && subExtBlockNum.Cmp(subHeaderNum) >= 0 && domExtBlockNum.Cmp(domHeaderNum) != 0 {
			return fmt.Errorf("external block collision detected")
		}
	}
	return nil
}

// HLCR does hierarchical comparison of two difficulty tuples and returns true if second tuple is greater than the first
func (bc *BlockChain) HLCR(localDifficulties []*big.Int, externDifficulties []*big.Int) bool {
	log.Info("HLCR", "localDiff", localDifficulties, "externDiff", externDifficulties)
	if localDifficulties[0].Cmp(externDifficulties[0]) < 0 {
		return true
	} else if localDifficulties[0].Cmp(externDifficulties[0]) > 0 {
		return false
	}
	if localDifficulties[1].Cmp(externDifficulties[1]) < 0 {
		return true
	} else if localDifficulties[1].Cmp(externDifficulties[1]) > 0 {
		return false
	}
	if localDifficulties[2].Cmp(externDifficulties[2]) < 0 {
		return true
	} else if localDifficulties[2].Cmp(externDifficulties[2]) > 0 {
		return false
	}
	return false
}

// The purpose of the Previous Coincident Reference Check (PCRC) is to establish
// that we have linked untwisted chains prior to checking HLCR & applying external state transfers.
// NOTE: note that it only guarantees linked & untwisted back to the prime terminus, assuming the
// prime termini match. To check deeper than that, you need to iteratively apply PCRC to get that guarantee.
func (bc *BlockChain) PCRC(header *types.Header) (common.Hash, error) {

	if header.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
		return bc.chainConfig.GenesisHashes[0], nil
	}

	slice := header.Location
	headerOrder, err := bc.Engine().GetDifficultyOrder(header)
	if err != nil {
		return common.Hash{}, err
	}

	// Only check for region twist if block is of region order
	if headerOrder <= params.REGION {
		// Region twist check
		// RTZ -- Region coincident along zone path
		// RTR -- Region coincident along region path
		RTZ, err := bc.Engine().PreviousCoincidentOnPath(bc, header, slice, params.REGION, params.ZONE)
		if err != nil {
			return common.Hash{}, err
		}

		RTR, err := bc.Engine().PreviousCoincidentOnPath(bc, header, slice, params.REGION, params.REGION)
		if err != nil {
			return common.Hash{}, err
		}

		// PCRC has failed. Rollback through the prior untwisted region.
		if RTZ.Hash() != RTR.Hash() {
			log.Info("Error in PCRC", "RTZ:", RTZ.Hash(), "RTR:", RTR.Hash())
			// If we are running in Prime or Region and have failed PCRC
			// 1. Check to see if the Zone terminus is on our chain.
			// 2. If Zone terminus is in our chain, do nothing.
			// 3. If Zone terminus is not in our chain, uncle the RTZ in the subordinate context.
			if types.QuaiNetworkContext < params.ZONE {
				rtz := bc.hc.GetBlockNumber(RTZ.Hash())
				// rtz is not in our Region chain, remove it from subordinate chains.
				if rtz == nil {
					bc.chainUncleFeed.Send(RTZ)
				}
			}
			return common.Hash{}, errors.New("there exists a region twist")
		}
	}

	// Prime twist check
	// PTZ -- Prime coincident along zone path
	// PTR -- Prime coincident along region path
	// PTP -- Prime coincident along prime path

	// Always calculate PTZ because it is always valid and we need terminus for calcHLCRDifficulty
	PTZ, err := bc.Engine().PreviousCoincidentOnPath(bc, header, slice, params.PRIME, params.ZONE)
	if err != nil {
		return common.Hash{}, err
	}

	// Only check for prime twist if block is of prime order
	if headerOrder == params.PRIME {
		PTR, err := bc.Engine().PreviousCoincidentOnPath(bc, header, slice, params.PRIME, params.REGION)
		if err != nil {
			return common.Hash{}, err
		}
		PTP, err := bc.Engine().PreviousCoincidentOnPath(bc, header, slice, params.PRIME, params.PRIME)
		if err != nil {
			return common.Hash{}, err
		}

		// PCRC has failed. Rollback through the prior untwisted prime.
		if PTR.Hash() != PTP.Hash() {
			log.Info("Error in PCRC", "PTR:", PTR.Hash(), "RTR:", PTP.Hash())
			if types.QuaiNetworkContext < params.REGION {
				ptr := bc.hc.GetBlockNumber(PTR.Hash())
				// ptr is not in our Prime chain, remove it from subordinate chains.
				if ptr == nil {
					bc.chainUncleFeed.Send(PTR)
				}
			}
			return common.Hash{}, errors.New("there exists a prime twist")
		}

		if PTZ.Hash() != PTR.Hash() {
			log.Info("Error in PCRC", "PTZ:", PTZ.Hash(), "PTR:", PTR.Hash())
			if types.QuaiNetworkContext < params.REGION {
				ptz := bc.hc.GetBlockNumber(PTZ.Hash())
				// ptz is not in our Prime chain, remove it from subordinate chains.
				if ptz == nil {
					bc.chainUncleFeed.Send(PTZ)
				}
			}
			return common.Hash{}, errors.New("there exists a prime twist")
		}
	}

	return PTZ.Hash(), nil
}

// calcHLCRNetDifficulty calculates the net difficulty from previous prime.
// The netDifficulties parameter inputs the nets of instantaneous difficulties from the terminus block.
// By correctly summing the net difficulties we have obtained the proper array to be compared in HLCR.
func (bc *BlockChain) CalcHLCRNetDifficulty(terminalHash common.Hash, header *types.Header) ([]*big.Int, error) {

	if (terminalHash == common.Hash{}) {
		return nil, errors.New("one or many of the  terminal hashes were nil")
	}

	primeNd := big.NewInt(0)
	regionNd := big.NewInt(0)
	zoneNd := big.NewInt(0)

	for {
		if header.Hash() == terminalHash {
			break
		}

		order, err := bc.engine.GetDifficultyOrder(header)
		if err != nil {
			return nil, err
		}
		nD := header.Difficulty[order]
		if order <= params.PRIME {
			primeNd.Add(primeNd, nD)
		}
		if order <= params.REGION {
			regionNd.Add(regionNd, nD)
		}
		if order <= params.ZONE {
			zoneNd.Add(zoneNd, nD)
		}

		// Get previous header on local chain by hash
		prevHeader := bc.GetHeaderByHash(header.ParentHash[order])
		if prevHeader == nil {
			// Get previous header on external chain by hash
			prevExtBlock, err := bc.GetExternalBlock(header.ParentHash[order], header.Number[order].Uint64()-1, header.Location, uint64(order))
			if err != nil {
				return nil, err
			}
			// Increment previous header
			prevHeader = prevExtBlock.Header()
		}
		header = prevHeader

		if header.Hash() == bc.Config().GenesisHashes[0] {
			break
		}
	}

	return []*big.Int{primeNd, regionNd, zoneNd}, nil
}

// HasHeader checks if a block header is present in the database or not, caching
// it if present.
func (bc *BlockChain) HasHeader(hash common.Hash, number uint64) bool {
	return bc.hc.HasHeader(hash, number)
}

// GetCanonicalHash returns the canonical hash for a given block number
func (bc *BlockChain) GetCanonicalHash(number uint64) common.Hash {
	return bc.hc.GetCanonicalHash(number)
}

// GetBlockHashesFromHash retrieves a number of block hashes starting at a given
// hash, fetching towards the genesis block.
func (bc *BlockChain) GetBlockHashesFromHash(hash common.Hash, max uint64) []common.Hash {
	return bc.hc.GetBlockHashesFromHash(hash, max)
}

// GetAncestor retrieves the Nth ancestor of a given block. It assumes that either the given block or
// a close ancestor of it is canonical. maxNonCanonical points to a downwards counter limiting the
// number of blocks to be individually checked before we reach the canonical chain.
//
// Note: ancestor == 0 returns the same block, 1 returns its parent and so on.
func (bc *BlockChain) GetAncestor(hash common.Hash, number, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	return bc.hc.GetAncestor(hash, number, ancestor, maxNonCanonical)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (bc *BlockChain) GetHeaderByNumber(number uint64) *types.Header {
	return bc.hc.GetHeaderByNumber(number)
}

// GetTransactionLookup retrieves the lookup associate with the given transaction
// hash from the cache or database.
func (bc *BlockChain) GetTransactionLookup(hash common.Hash) *rawdb.LegacyTxLookupEntry {
	// Short circuit if the txlookup already in the cache, retrieve otherwise
	if lookup, exist := bc.txLookupCache.Get(hash); exist {
		return lookup.(*rawdb.LegacyTxLookupEntry)
	}
	tx, blockHash, blockNumber, txIndex := rawdb.ReadTransaction(bc.db, hash)
	if tx == nil {
		return nil
	}
	lookup := &rawdb.LegacyTxLookupEntry{BlockHash: blockHash, BlockIndex: blockNumber, Index: txIndex}
	bc.txLookupCache.Add(hash, lookup)
	return lookup
}

// Config retrieves the chain's fork configuration.
func (bc *BlockChain) Config() *params.ChainConfig { return bc.chainConfig }

// Engine retrieves the blockchain's consensus engine.
func (bc *BlockChain) Engine() consensus.Engine { return bc.engine }

// SubscribeRemovedLogsEvent registers a subscription of RemovedLogsEvent.
func (bc *BlockChain) SubscribeRemovedLogsEvent(ch chan<- RemovedLogsEvent) event.Subscription {
	return bc.scope.Track(bc.rmLogsFeed.Subscribe(ch))
}

// SubscribeChainEvent registers a subscription of ChainEvent.
func (bc *BlockChain) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return bc.scope.Track(bc.chainFeed.Subscribe(ch))
}

// SubscribeReOrgEvent registers a subscription of ReOrgEvent.
func (bc *BlockChain) SubscribeReOrgEvent(ch chan<- ReOrgRollup) event.Subscription {
	return bc.scope.Track(bc.reOrgFeed.Subscribe(ch))
}

func (bc *BlockChain) SubscribeMissingExternalBlockEvent(ch chan<- MissingExternalBlock) event.Subscription {
	return bc.scope.Track(bc.missingExternalBlockFeed.Subscribe(ch))
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (bc *BlockChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return bc.scope.Track(bc.chainHeadFeed.Subscribe(ch))
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (bc *BlockChain) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return bc.scope.Track(bc.chainSideFeed.Subscribe(ch))
}

// SubscribeChainUncleEvent registers a subscription of an uncled header.
func (bc *BlockChain) SubscribeChainUncleEvent(ch chan<- *types.Header) event.Subscription {
	return bc.scope.Track(bc.chainUncleFeed.Subscribe(ch))
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

// CheckContext checks to make sure the range of a context or order is valid
func (bc *BlockChain) CheckContext(number int) error {
	if number < 0 || number > len(params.FullerOntology) {
		return errors.New("the provided path is outside the allowable range")
	}
	return nil
}

// CheckLocationRange checks to make sure the range of location is valid
func (bc *BlockChain) CheckLocationRange(location []byte) error {
	if int(location[0]) < 1 || int(location[0]) > params.FullerOntology[0] {
		return errors.New("the provided location is outside the allowable region range")
	}
	if int(location[1]) < 1 || int(location[1]) > params.FullerOntology[1] {
		return errors.New("the provided location is outside the allowable zone range")
	}
	return nil
}
