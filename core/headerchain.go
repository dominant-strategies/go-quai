package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	headerCacheLimit      = 25
	numberCacheLimit      = 2048
	c_subRollupCacheSize  = 50
	primeHorizonThreshold = 20
	c_powCacheLimit       = 1000
	c_calcOrderCacheLimit = 10000
)

var (
	errInvalidEfficiencyScore = errors.New("unable to compute efficiency score")
)

type calcOrderResponse struct {
	intrinsicEntropy *big.Int
	order            int
}

// getPendingEtxsRollup gets the pendingEtxsRollup rollup from appropriate Region
type getPendingEtxsRollup func(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxsRollup, error)

// getPendingEtxs gets the pendingEtxs from the appropriate Zone
type getPendingEtxs func(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxs, error)

type getPrimeBlock func(blockHash common.Hash) *types.WorkObject

type HeaderChain struct {
	config *params.ChainConfig

	bc     *BodyDb
	engine consensus.Engine
	pool   *TxPool

	currentExpansionNumber uint8

	chainHeadFeed event.Feed
	unlocksFeed   event.Feed
	chainSideFeed event.Feed
	scope         event.SubscriptionScope

	headerDb      ethdb.Database
	genesisHeader *types.WorkObject

	currentHeader atomic.Value                              // Current head of the header chain (may be above the block chain!)
	headerCache   *lru.Cache[common.Hash, types.WorkObject] // Cache for the most recent block headers
	numberCache   *lru.Cache[common.Hash, uint64]           // Cache for the most recent block numbers

	fetchPEtxRollup getPendingEtxsRollup
	fetchPEtx       getPendingEtxs

	fetchPrimeBlock getPrimeBlock

	pendingEtxsRollup *lru.Cache[common.Hash, types.PendingEtxsRollup]
	pendingEtxs       *lru.Cache[common.Hash, types.PendingEtxs]
	blooms            *lru.Cache[common.Hash, types.Bloom]
	subRollupCache    *lru.Cache[common.Hash, types.Transactions]

	wg            sync.WaitGroup // chain processing wait group for shutting down
	running       int32          // 0 if chain is running, 1 when stopped
	procInterrupt int32          // interrupt signaler for block processing

	headermu        sync.RWMutex
	heads           []*types.WorkObject
	slicesRunning   []common.Location
	processingState bool

	powHashCache *lru.Cache[common.Hash, common.Hash]

	calcOrderCache *lru.Cache[common.Hash, calcOrderResponse]

	logger *log.Logger
}

// NewHeaderChain creates a new HeaderChain structure. ProcInterrupt points
// to the parent's interrupt semaphore.
func NewHeaderChain(db ethdb.Database, engine consensus.Engine, pEtxsRollupFetcher getPendingEtxsRollup, pEtxsFetcher getPendingEtxs, primeBlockFetcher getPrimeBlock, chainConfig *params.ChainConfig, cacheConfig *CacheConfig, txLookupLimit *uint64, vmConfig vm.Config, slicesRunning []common.Location, currentExpansionNumber uint8, logger *log.Logger) (*HeaderChain, error) {

	nodeCtx := chainConfig.Location.Context()

	hc := &HeaderChain{
		config:                 chainConfig,
		headerDb:               db,
		engine:                 engine,
		slicesRunning:          slicesRunning,
		fetchPEtxRollup:        pEtxsRollupFetcher,
		fetchPEtx:              pEtxsFetcher,
		fetchPrimeBlock:        primeBlockFetcher,
		logger:                 logger,
		currentExpansionNumber: currentExpansionNumber,
	}

	headerCache, _ := lru.New[common.Hash, types.WorkObject](headerCacheLimit)
	hc.headerCache = headerCache

	numberCache, _ := lru.New[common.Hash, uint64](numberCacheLimit)
	hc.numberCache = numberCache

	powHashCache, _ := lru.New[common.Hash, common.Hash](c_powCacheLimit)
	hc.powHashCache = powHashCache

	calcOrderCache, _ := lru.New[common.Hash, calcOrderResponse](c_calcOrderCacheLimit)
	hc.calcOrderCache = calcOrderCache

	genesisHash := hc.GetGenesisHashes()[0]
	genesisNumber := rawdb.ReadHeaderNumber(db, genesisHash)
	if genesisNumber == nil {
		return nil, ErrNoGenesis
	}
	hc.genesisHeader = rawdb.ReadWorkObject(db, *genesisNumber, genesisHash, types.BlockObject)
	if bytes.Equal(chainConfig.Location, common.Location{0, 0}) {
		if hc.genesisHeader == nil {
			return nil, ErrNoGenesis
		}
		if hc.genesisHeader.Hash() != hc.config.DefaultGenesisHash {
			return nil, fmt.Errorf("genesis hash mismatch: have %x, want %x", hc.genesisHeader.Hash(), chainConfig.DefaultGenesisHash)
		}
	}
	hc.logger.WithField("Hash", hc.genesisHeader.Hash()).Info("Genesis")
	//Load any state that is in our db
	if err := hc.loadLastState(); err != nil {
		return nil, err
	}

	var err error
	hc.bc, err = NewBodyDb(db, engine, hc, chainConfig, cacheConfig, txLookupLimit, vmConfig, slicesRunning)
	if err != nil {
		return nil, err
	}

	// Record if the chain is processing state
	hc.processingState = hc.setStateProcessing()

	pendingEtxsRollup, _ := lru.New[common.Hash, types.PendingEtxsRollup](c_maxPendingEtxsRollup)
	hc.pendingEtxsRollup = pendingEtxsRollup

	var pendingEtxs *lru.Cache[common.Hash, types.PendingEtxs]
	if nodeCtx == common.PRIME_CTX {
		pendingEtxs, _ = lru.New[common.Hash, types.PendingEtxs](c_maxPendingEtxBatchesPrime)
		hc.pendingEtxs = pendingEtxs
	} else {
		pendingEtxs, _ = lru.New[common.Hash, types.PendingEtxs](c_maxPendingEtxBatchesRegion)
		hc.pendingEtxs = pendingEtxs
	}

	blooms, _ := lru.New[common.Hash, types.Bloom](c_maxBloomFilters)
	hc.blooms = blooms

	subRollupCache, _ := lru.New[common.Hash, types.Transactions](c_subRollupCacheSize)
	hc.subRollupCache = subRollupCache

	// Initialize the heads slice
	heads := make([]*types.WorkObject, 0)
	hc.heads = heads

	return hc, nil
}

// CollectSubRollup collects the rollup of ETXs emitted from the subordinate
// chain in the slice which emitted the given block.
func (hc *HeaderChain) CollectSubRollup(b *types.WorkObject) (types.Transactions, error) {
	nodeCtx := hc.NodeCtx()
	subRollup := types.Transactions{}
	if nodeCtx < common.ZONE_CTX {
		// Since in prime the pending etxs are stored in 2 parts, pendingEtxsRollup
		// consists of region header and subrollups
		for _, hash := range b.Manifest() {
			if nodeCtx == common.PRIME_CTX {
				pEtxRollup, err := hc.GetPendingEtxsRollup(hash, b.Location())
				if err == nil {
					subRollup = append(subRollup, pEtxRollup.EtxsRollup...)
				} else {
					// Try to get the pending etx from the Regions
					hc.fetchPEtxRollup(b.Hash(), hash, b.Location())
					return nil, ErrPendingEtxNotFound
				}
				// Region works normally as before collecting pendingEtxs for each hash in the manifest
			} else if nodeCtx == common.REGION_CTX {
				pendingEtxs, err := hc.GetPendingEtxs(hash)
				if err != nil {
					// Get the pendingEtx from the appropriate zone
					hc.fetchPEtx(b.Hash(), hash, b.Location())
					return nil, ErrPendingEtxNotFound
				}
				subRollup = append(subRollup, pendingEtxs.OutboundEtxs...)
			}
		}
	}
	return subRollup, nil
}

// GetPendingEtxs gets the pendingEtxs form the
func (hc *HeaderChain) GetPendingEtxs(hash common.Hash) (*types.PendingEtxs, error) {
	var pendingEtxs types.PendingEtxs
	// Look for pending ETXs first in pending ETX cache, then in database
	if res, ok := hc.pendingEtxs.Get(hash); ok {
		pendingEtxs = res
	} else if res := rawdb.ReadPendingEtxs(hc.headerDb, hash); res != nil {
		pendingEtxs = *res
	} else {
		hc.logger.WithField("hash", hash.String()).Trace("Unable to find pending etxs for hash in manifest")
		return nil, ErrPendingEtxNotFound
	}
	return &pendingEtxs, nil
}

func (hc *HeaderChain) GetPendingEtxsRollup(hash common.Hash, location common.Location) (*types.PendingEtxsRollup, error) {
	var rollups types.PendingEtxsRollup
	// Look for pending ETXs first in pending ETX cache, then in database
	if res, ok := hc.pendingEtxsRollup.Get(hash); ok {
		rollups = res
	} else if res := rawdb.ReadPendingEtxsRollup(hc.headerDb, hash); res != nil {
		rollups = *res
	} else {
		hc.logger.WithField("hash", hash.String()).Trace("Unable to find pending etx rollups for hash in manifest")
		return nil, ErrPendingEtxRollupNotFound
	}
	return &rollups, nil
}

// GetBloom gets the bloom from the cache or database
func (hc *HeaderChain) GetBloom(hash common.Hash) (*types.Bloom, error) {
	var bloom types.Bloom
	// Look for bloom first in bloom cache, then in database
	if res, ok := hc.blooms.Get(hash); ok {
		bloom = res
	} else if res := rawdb.ReadBloom(hc.headerDb, hash); res != nil {
		bloom = *res
	} else {
		hc.logger.WithField("hash", hash.String()).Trace("Unable to find bloom for hash in manifest")
		return nil, ErrBloomNotFound
	}
	return &bloom, nil
}

// Collect all emmitted ETXs since the last coincident block, but excluding
// those emitted in this block
func (hc *HeaderChain) CollectEtxRollup(b *types.WorkObject) (types.Transactions, error) {
	if hc.IsGenesisHash(b.Hash()) {
		return b.OutboundEtxs(), nil
	}
	parent := hc.GetBlock(b.ParentHash(hc.NodeCtx()), b.NumberU64(hc.NodeCtx())-1)
	if parent == nil {
		return nil, errors.New("parent not found")
	}
	return hc.collectInclusiveEtxRollup(parent)
}

func (hc *HeaderChain) collectInclusiveEtxRollup(b *types.WorkObject) (types.Transactions, error) {
	// Initialize the rollup with ETXs emitted by this block
	newEtxs := b.OutboundEtxs()
	// Terminate the search if we reached genesis
	if hc.IsGenesisHash(b.Hash()) {
		return newEtxs, nil
	}
	// Terminate the search on coincidence with dom chain
	if hc.engine.IsDomCoincident(hc, b) {
		return newEtxs, nil
	}
	// Recursively get the ancestor rollup, until a coincident ancestor is found
	ancestor := hc.GetBlock(b.ParentHash(hc.NodeCtx()), b.NumberU64(hc.NodeCtx())-1)
	if ancestor == nil {
		return nil, errors.New("ancestor not found")
	}
	etxRollup, err := hc.collectInclusiveEtxRollup(ancestor)
	if err != nil {
		return nil, err
	}
	etxRollup = append(etxRollup, newEtxs...)
	return etxRollup, nil
}

// Append
func (hc *HeaderChain) AppendHeader(header *types.WorkObject) error {
	nodeCtx := hc.NodeCtx()
	hc.logger.WithFields(log.Fields{
		"Hash":     header.Hash(),
		"Number":   header.NumberArray(),
		"Location": header.Location,
		"Parent":   header.ParentHash(nodeCtx),
	}).Debug("Headerchain Append")

	err := hc.engine.VerifyHeader(hc, header)
	if err != nil {
		return err
	}

	// Verify the manifest matches expected
	// Load the manifest of headers preceding this header
	// note: prime manifest is non-existent, because a prime header cannot be
	// coincident with a higher order chain. So, this check is skipped for prime
	// nodes.
	if nodeCtx > common.PRIME_CTX {
		manifest := rawdb.ReadManifest(hc.headerDb, header.ParentHash(nodeCtx))
		if manifest == nil {
			return errors.New("manifest not found for parent")
		}
		if header.ManifestHash(nodeCtx) != types.DeriveSha(manifest, trie.NewStackTrie(nil)) {
			return errors.New("manifest does not match hash")
		}
	}

	// Verify the Interlink root hash matches the interlink
	if nodeCtx == common.PRIME_CTX {
		interlinkHashes := rawdb.ReadInterlinkHashes(hc.headerDb, header.ParentHash(nodeCtx))
		if interlinkHashes == nil {
			return errors.New("interlink hashes not found")
		}
		if header.InterlinkRootHash() != types.DeriveSha(interlinkHashes, trie.NewStackTrie(nil)) {
			return errors.New("interlink root hash does not match interlink")
		}
	}

	return nil
}

func (hc *HeaderChain) CalculateInterlink(block *types.WorkObject) (common.Hashes, error) {
	header := types.CopyWorkObject(block)
	var interlinkHashes common.Hashes
	if hc.IsGenesisHash(header.Hash()) {
		// On genesis, the interlink hashes are all the same and should start with genesis hash
		interlinkHashes = common.Hashes{header.Hash(), header.Hash(), header.Hash(), header.Hash()}
	} else {
		// check if parent belongs to any interlink level
		rank, err := hc.engine.CalcRank(hc, header)
		if err != nil {
			return nil, err
		}
		parentInterlinkHashes := rawdb.ReadInterlinkHashes(hc.headerDb, header.ParentHash(common.PRIME_CTX))
		if parentInterlinkHashes == nil {
			return nil, ErrSubNotSyncedToDom
		}
		if rank == 0 { // No change in the interlink hashes, so carry
			interlinkHashes = parentInterlinkHashes
		} else if rank > 0 && rank <= common.InterlinkDepth {
			interlinkHashes = parentInterlinkHashes
			// update the interlink hashes for each level below the rank
			for i := 0; i < rank; i++ {
				interlinkHashes[i] = header.Hash()
			}
		} else {
			hc.logger.Error("Not possible to find rank greater than the max interlink levels")
			return nil, errors.New("not possible to find rank greater than the max interlink levels")
		}
	}
	// Store the interlink hashes in the database
	rawdb.WriteInterlinkHashes(hc.headerDb, header.Hash(), interlinkHashes)
	return interlinkHashes, nil
}

func (hc *HeaderChain) CalculateManifest(header *types.WorkObject) types.BlockManifest {
	manifest := rawdb.ReadManifest(hc.headerDb, header.Hash())
	if manifest == nil {
		nodeCtx := hc.NodeCtx()
		// Compute and set manifest hash
		var manifest types.BlockManifest
		if nodeCtx == common.PRIME_CTX {
			// Nothing to do for prime chain
			manifest = types.BlockManifest{}
		} else if hc.engine.IsDomCoincident(hc, header) {
			manifest = types.BlockManifest{header.Hash()}
		} else {
			parentManifest := rawdb.ReadManifest(hc.headerDb, header.ParentHash(nodeCtx))
			manifest = append(parentManifest, header.Hash())
		}
		// write the manifest into the disk
		rawdb.WriteManifest(hc.headerDb, header.Hash(), manifest)
	}
	return manifest
}

func (hc *HeaderChain) ProcessingState() bool {
	return hc.processingState
}

func (hc *HeaderChain) setStateProcessing() bool {
	nodeCtx := hc.NodeCtx()
	for _, slice := range hc.slicesRunning {
		switch nodeCtx {
		case common.PRIME_CTX:
			return true
		case common.REGION_CTX:
			if slice.Region() == hc.NodeLocation().Region() {
				return true
			}
		case common.ZONE_CTX:
			if slice.Equal(hc.NodeLocation()) {
				return true
			}
		}
	}
	return false
}

// Append
func (hc *HeaderChain) AppendBlock(block *types.WorkObject) error {
	blockappend := time.Now()
	// Append block else revert header append
	logs, unlocks, err := hc.bc.Append(block)
	if err != nil {
		return err
	}
	if unlocks != nil && len(unlocks) > 0 {
		hc.unlocksFeed.Send(UnlocksEvent{
			Hash:    block.Hash(),
			Unlocks: unlocks,
		})
	}
	hc.logger.WithField("append block", common.PrettyDuration(time.Since(blockappend))).Debug("Time taken to")

	if len(logs) > 0 {
		hc.bc.logsFeed.Send(logs)
	}

	return nil
}

// SetCurrentHeader sets the current header based on the POEM choice
func (hc *HeaderChain) SetCurrentHeader(head *types.WorkObject) error {
	nodeCtx := hc.NodeCtx()

	prevHeader := hc.CurrentHeader()
	// if trying to set the same header, escape
	if prevHeader.Hash() == head.Hash() {
		return nil
	}

	// If head is the normal extension of canonical head, we can return by just wiring the canonical hash.
	if prevHeader.Hash() == head.ParentHash(hc.NodeCtx()) {
		rawdb.WriteCanonicalHash(hc.headerDb, head.Hash(), head.NumberU64(hc.NodeCtx()))
		if nodeCtx == common.ZONE_CTX {
			err := hc.AppendBlock(head)
			if err != nil {
				rawdb.DeleteCanonicalHash(hc.headerDb, head.NumberU64(hc.NodeCtx()))
				return err
			}
		}
		// write the head block hash to the db
		rawdb.WriteHeadBlockHash(hc.headerDb, head.Hash())
		hc.logger.WithFields(log.Fields{
			"Hash":   head.Hash(),
			"Number": head.NumberArray(),
		}).Info("Setting the current header")
		hc.currentHeader.Store(head)
		return nil
	}

	//Find a common header between the current header and the new head
	commonHeader, err := rawdb.FindCommonAncestor(hc.headerDb, prevHeader, head, nodeCtx)
	if err != nil {
		return err
	}
	newHeader := types.CopyWorkObject(head)

	// Delete each header and rollback state processor until common header
	// Accumulate the hash slice stack
	var hashStack []*types.WorkObject
	for {
		if newHeader.Hash() == commonHeader.Hash() {
			break
		}
		hashStack = append(hashStack, newHeader)
		newHeader = hc.GetHeaderByHash(newHeader.ParentHash(hc.NodeCtx()))
		if newHeader == nil {
			return ErrSubNotSyncedToDom
		}
		// genesis check to not delete the genesis block
		if hc.IsGenesisHash(newHeader.Hash()) {
			break
		}
	}
	var prevHashStack []*types.WorkObject
	for {
		if prevHeader.Hash() == commonHeader.Hash() {
			break
		}
		prevHashStack = append(prevHashStack, prevHeader)
		rawdb.DeleteCanonicalHash(hc.headerDb, prevHeader.NumberU64(hc.NodeCtx()))
		// UTXO Rollback logic: Recreate deleted UTXOs and delete created UTXOs
		if nodeCtx == common.ZONE_CTX && hc.ProcessingState() {
			sutxos, err := rawdb.ReadSpentUTXOs(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			trimmedUtxos, err := rawdb.ReadTrimmedUTXOs(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			sutxos = append(sutxos, trimmedUtxos...)
			for _, sutxo := range sutxos {
				rawdb.CreateUTXO(hc.headerDb, sutxo.TxHash, sutxo.Index, sutxo.UtxoEntry)
			}
			utxoKeys, err := rawdb.ReadCreatedUTXOKeys(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			for _, key := range utxoKeys {
				if len(key) == rawdb.UtxoKeyWithDenominationLength {
					key = key[:rawdb.UtxoKeyLength] // The last byte of the key is the denomination (but only in CreatedUTXOKeys)
				}
				hc.headerDb.Delete(key)
			}
			createdCoinbaseKeys, err := rawdb.ReadCreatedCoinbaseLockupKeys(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			for _, key := range createdCoinbaseKeys {
				if len(key) != rawdb.CoinbaseLockupKeyLength {
					return fmt.Errorf("invalid created coinbase key length: %d", len(key))
				}
				hc.headerDb.Delete(key)
			}
			deletedCoinbases, err := rawdb.ReadDeletedCoinbaseLockups(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			for key, coinbase := range deletedCoinbases {
				if len(key) != rawdb.CoinbaseLockupKeyLength {
					return fmt.Errorf("invalid deleted coinbase key length: %d", len(key))
				}
				hc.headerDb.Put(key[:], coinbase)
			}
		}
		prevHeader = hc.GetHeaderByHash(prevHeader.ParentHash(hc.NodeCtx()))
		if prevHeader == nil {
			return errors.New("Could not find previously canonical header during reorg")
		}
		// genesis check to not delete the genesis block
		if hc.IsGenesisHash(prevHeader.Hash()) {
			break
		}
	}

	hc.logger.WithFields(log.Fields{
		"number":    newHeader.NumberArray(),
		"newHeader": newHeader.Hash(),
	}).Info("New Header")
	hc.logger.WithFields(log.Fields{
		"number":     prevHeader.NumberArray(),
		"prevHeader": prevHeader.Hash(),
	}).Info("Prev Header")
	hc.logger.WithFields(log.Fields{
		"number": commonHeader.NumberArray(),
		"hash":   commonHeader.Hash(),
	}).Info("Common Header")

	// Run through the hash stack to update canonicalHash and forward state processor
	for i := len(hashStack) - 1; i >= 0; i-- {
		hc.logger.Info("Reverting header: ", " Number Array: ", hashStack[i].NumberArray(), " Hash: ", hashStack[i].Hash())
		rawdb.WriteCanonicalHash(hc.headerDb, hashStack[i].Hash(), hashStack[i].NumberU64(hc.NodeCtx()))
		if nodeCtx == common.ZONE_CTX {
			block := hc.GetBlockOrCandidate(hashStack[i].Hash(), hashStack[i].NumberU64(nodeCtx))
			if block == nil {
				return errors.New("could not find block during SetCurrentState: " + hashStack[i].Hash().String())
			}
			err := hc.AppendBlock(block)
			if err != nil {
				hc.logger.WithFields(log.Fields{
					"error": err,
					"block": block.Hash(),
				}).Error("Error appending block during reorg")
				rawdb.DeleteCanonicalHash(hc.headerDb, hashStack[i].NumberU64(hc.NodeCtx()))
				// Append failed, rollback the UTXO set to the common header
				for j := i + 1; j < len(hashStack); j++ {
					hc.logger.Info("Append failed reverting header: ", " Number Array: ", hashStack[j].NumberArray(), " Hash: ", hashStack[j].Hash())
					rawdb.DeleteCanonicalHash(hc.headerDb, hashStack[j].NumberU64(hc.NodeCtx()))
					if nodeCtx == common.ZONE_CTX && hc.ProcessingState() {
						sutxos, err := rawdb.ReadSpentUTXOs(hc.headerDb, hashStack[j].Hash())
						if err != nil {
							return err
						}
						trimmedUtxos, err := rawdb.ReadTrimmedUTXOs(hc.headerDb, prevHeader.Hash())
						if err != nil {
							return err
						}
						sutxos = append(sutxos, trimmedUtxos...)
						for _, sutxo := range sutxos {
							rawdb.CreateUTXO(hc.headerDb, sutxo.TxHash, sutxo.Index, sutxo.UtxoEntry)
						}
						utxoKeys, err := rawdb.ReadCreatedUTXOKeys(hc.headerDb, hashStack[j].Hash())
						if err != nil {
							return err
						}
						for _, key := range utxoKeys {
							hc.headerDb.Delete(key)
						}
						createdCoinbaseKeys, err := rawdb.ReadCreatedCoinbaseLockupKeys(hc.headerDb, prevHeader.Hash())
						if err != nil {
							return err
						}
						for _, key := range createdCoinbaseKeys {
							if len(key) != rawdb.CoinbaseLockupKeyLength {
								return fmt.Errorf("invalid created coinbase key length: %d", len(key))
							}
							hc.headerDb.Delete(key)
						}
						deletedCoinbases, err := rawdb.ReadDeletedCoinbaseLockups(hc.headerDb, prevHeader.Hash())
						if err != nil {
							return err
						}
						for key, coinbase := range deletedCoinbases {
							if len(key) != rawdb.CoinbaseLockupKeyLength {
								return fmt.Errorf("invalid deleted coinbase key length: %d", len(key))
							}
							hc.headerDb.Put(key[:], coinbase)
						}
					}
				}
				for k := len(prevHashStack) - 1; k >= 0; k-- {
					hc.logger.Info("Append failed reapplying header: ", " Number Array: ", prevHashStack[k].NumberArray(), " Hash: ", prevHashStack[k].Hash())
					rawdb.WriteCanonicalHash(hc.headerDb, prevHashStack[k].Hash(), prevHashStack[k].NumberU64(hc.NodeCtx()))
					if nodeCtx == common.ZONE_CTX {
						block := hc.GetBlockOrCandidate(prevHashStack[k].Hash(), prevHashStack[k].NumberU64(nodeCtx))
						if block == nil {
							return errors.New("could not find block during SetCurrentState: " + prevHashStack[k].Hash().String())
						}
						err := hc.AppendBlock(block)
						if err != nil {
							hc.logger.WithFields(log.Fields{
								"error": err,
								"block": block.Hash(),
							}).Error("Error appending block during reorg")
							return err
						}
					}
				}
				return err
			}
		}
	}
	// write the head block hash to the db
	rawdb.WriteHeadBlockHash(hc.headerDb, head.Hash())
	hc.logger.WithFields(log.Fields{
		"Hash":   head.Hash(),
		"Number": head.NumberArray(),
	}).Info("Setting the current header")
	hc.currentHeader.Store(head)

	if hc.NodeCtx() == common.ZONE_CTX && hc.ProcessingState() {
		// Every Block that got removed from the canonical hash db is sent in the side feed to be
		// recorded as uncles
		go func() {
			defer func() {
				if r := recover(); r != nil {
					hc.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Fatal("Go-Quai Panicked")
				}
			}()
			var blocks []*types.WorkObject
			for i := len(prevHashStack) - 1; i >= 0; i-- {
				block := hc.bc.GetBlock(prevHashStack[i].Hash(), prevHashStack[i].NumberU64(hc.NodeCtx()))
				if block != nil {
					blocks = append(blocks, block)
				}
			}
			hc.chainSideFeed.Send(ChainSideEvent{Blocks: blocks})
		}()
	}

	return nil
}

// findCommonAncestor
func (hc *HeaderChain) findCommonAncestor(header *types.WorkObject) *types.WorkObject {
	current := types.CopyWorkObject(header)
	for {
		if current == nil {
			return nil
		}
		if hc.IsGenesisHash(current.Hash()) {
			return current
		}
		canonicalHash := rawdb.ReadCanonicalHash(hc.headerDb, current.NumberU64(hc.NodeCtx()))
		if canonicalHash == current.Hash() {
			return hc.GetHeaderByHash(canonicalHash)
		}
		current = hc.GetHeaderByHash(current.ParentHash(hc.NodeCtx()))
	}

}
func (hc *HeaderChain) WorkShareDistance(wo *types.WorkObject, ws *types.WorkObjectHeader) (*big.Int, error) {
	current := wo
	// Create a list of ancestor blocks to the work object
	ancestors := make(map[common.Hash]struct{})
	for i := 0; i < params.WorkSharesInclusionDepth; i++ {
		parent := hc.GetBlockByHash(current.ParentHash(common.ZONE_CTX))
		if parent == nil {
			return big.NewInt(0), errors.New("error finding the parent")
		}
		ancestors[parent.Hash()] = struct{}{}
		current = parent
	}

	var distance int64 = 0
	// trace back from the workshare and check if any of the parents exist in
	// the ancestors list
	parentHash := ws.ParentHash()
	for {
		parent := hc.GetBlockByHash(parentHash)
		if parent == nil {
			return big.NewInt(0), errors.New("error finding the parent")
		}
		if _, exists := ancestors[parent.Hash()]; exists {
			distance += int64(wo.NumberU64(common.ZONE_CTX) - parent.NumberU64(common.ZONE_CTX) - 1)
			break
		}
		distance++
		// If distance is greater than the WorkSharesInclusionDepth, exit the for loop
		if distance > int64(params.WorkSharesInclusionDepth) {
			break
		}
		parentHash = parent.ParentHash(common.ZONE_CTX)
	}

	// If distance is greater than the WorkSharesInclusionDepth, reject the workshare
	if distance > int64(params.WorkSharesInclusionDepth) {
		return big.NewInt(0), errors.New("workshare is at distance more than WorkSharesInclusionDepth")
	}

	return big.NewInt(distance), nil
}

func (hc *HeaderChain) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	if !pEtxs.IsValid(trie.NewStackTrie(nil)) && !hc.IsGenesisHash(pEtxs.Header.Hash()) {
		hc.logger.Info("PendingEtx is not valid")
		return ErrPendingEtxNotValid
	}
	hc.logger.WithField("block", pEtxs.Header.Hash()).Debug("Received pending ETXs")
	// Only write the pending ETXs if we have not seen them before
	if !hc.pendingEtxs.Contains(pEtxs.Header.Hash()) {
		// Write to pending ETX database
		rawdb.WritePendingEtxs(hc.headerDb, pEtxs)
		// Also write to cache for faster access
		hc.pendingEtxs.Add(pEtxs.Header.Hash(), pEtxs)
	} else {
		return ErrPendingEtxAlreadyKnown
	}
	return nil
}

func (hc *HeaderChain) AddBloom(bloom types.Bloom, hash common.Hash) error {
	// Only write the bloom if we have not seen it before
	if !hc.blooms.Contains(hash) {
		// Write to bloom database
		rawdb.WriteBloom(hc.headerDb, hash, bloom)
		// Also write to cache for faster access
		hc.blooms.Add(hash, bloom)
	} else {
		return ErrBloomAlreadyKnown
	}
	return nil
}

// loadLastState loads the last known chain state from the database. This method
// assumes that the chain manager mutex is held.
func (hc *HeaderChain) loadLastState() error {
	// TODO: create function to find highest block number and fill Head FIFO
	headsHashes := rawdb.ReadHeadsHashes(hc.headerDb)

	if head := rawdb.ReadHeadBlockHash(hc.headerDb); head != (common.Hash{}) {
		if chead := hc.GetHeaderByHash(head); chead != nil {
			hc.currentHeader.Store(chead)
		} else {
			// This is only done if during the stop, currenthead hash was not stored
			// properly and it doesn't crash the nodes
			hc.currentHeader.Store(hc.genesisHeader)
		}
	} else {
		// Recover the current header
		hc.logger.Warn("Recovering Current Header")
		recoveredHeader := hc.RecoverCurrentHeader()
		rawdb.WriteHeadBlockHash(hc.headerDb, recoveredHeader.Hash())
		hc.currentHeader.Store(recoveredHeader)
	}

	heads := make([]*types.WorkObject, 0)
	for _, hash := range headsHashes {
		heads = append(heads, hc.GetHeaderByHash(hash))
	}
	hc.heads = heads

	return nil
}

// Stop stops the blockchain service. If any imports are currently in progress
// it will abort them using the procInterrupt.
func (hc *HeaderChain) Stop() {
	if !atomic.CompareAndSwapInt32(&hc.running, 0, 1) {
		return
	}

	hashes := make(common.Hashes, 0)
	for i := 0; i < len(hc.heads); i++ {
		hashes = append(hashes, hc.heads[i].Hash())
	}
	// Save the heads
	rawdb.WriteHeadsHashes(hc.headerDb, hashes)

	// Unsubscribe all subscriptions registered from blockchain
	hc.scope.Close()
	hc.bc.scope.Close()
	hc.wg.Wait()
	if hc.NodeCtx() == common.ZONE_CTX && hc.ProcessingState() {
		hc.bc.processor.Stop()
	}
	hc.logger.Info("headerchain stopped")
}

// CheckInCalcOrderCache checks if the hash exists in the calc order cache
// and if if the hash exists it returns the order value
func (hc *HeaderChain) CheckInCalcOrderCache(hash common.Hash) (*big.Int, int, bool) {
	calcOrderResponse, exists := hc.calcOrderCache.Peek(hash)
	if !exists || calcOrderResponse.intrinsicEntropy.Cmp(common.Big0) == 0 {
		return nil, -1, false
	} else {
		return calcOrderResponse.intrinsicEntropy, calcOrderResponse.order, true
	}
}

// AddToCalcOrderCache adds the hash and the order pair to the calcorder cache
func (hc *HeaderChain) AddToCalcOrderCache(hash common.Hash, order int, intrinsicS *big.Int) {
	if intrinsicS.Cmp(common.Big0) == 0 {
		hc.logger.Error("Tried to write intrinsicS of zero to CalcOrderCache")
		return
	}
	hc.calcOrderCache.Add(hash, calcOrderResponse{intrinsicEntropy: intrinsicS, order: order})
}

// Empty checks if the headerchain is empty.
func (hc *HeaderChain) Empty() bool {
	for _, hash := range []common.Hash{rawdb.ReadHeadBlockHash(hc.headerDb)} {
		if !hc.IsGenesisHash(hash) {
			return false
		}
	}
	return true
}

// GetBlockNumber retrieves the block number belonging to the given hash
// from the cache or database
func (hc *HeaderChain) GetBlockNumber(hash common.Hash) *uint64 {
	if cached, ok := hc.numberCache.Get(hash); ok {
		number := cached
		return &number
	}
	number := rawdb.ReadHeaderNumber(hc.headerDb, hash)
	if number != nil {
		hc.numberCache.Add(hash, *number)
	}
	return number
}

func (hc *HeaderChain) GetTerminiByHash(hash common.Hash) *types.Termini {
	termini := rawdb.ReadTermini(hc.headerDb, hash)
	return termini
}

// GetBlockHashesFromHash retrieves a number of block hashes starting at a given
// hash, fetching towards the genesis block.
func (hc *HeaderChain) GetBlockHashesFromHash(hash common.Hash, max uint64) []common.Hash {
	// Get the origin header from which to fetch
	header := hc.GetHeaderByHash(hash)
	if header == nil {
		return nil
	}
	// Iterate the headers until enough is collected or the genesis reached
	chain := make([]common.Hash, 0, max)
	for i := uint64(0); i < max; i++ {
		next := header.ParentHash(hc.NodeCtx())
		if header = hc.GetHeaderByHash(next); header == nil {
			break
		}
		chain = append(chain, next)
		if header.Number(hc.NodeCtx()).Sign() == 0 {
			break
		}
	}
	return chain
}

// GetAncestor retrieves the Nth ancestor of a given block. It assumes that either the given block or
// a close ancestor of it is canonical. maxNonCanonical points to a downwards counter limiting the
// number of blocks to be individually checked before we reach the canonical chain.
//
// Note: ancestor == 0 returns the same block, 1 returns its parent and so on.
func (hc *HeaderChain) GetAncestor(hash common.Hash, number, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	if ancestor > number {
		return common.Hash{}, 0
	}
	if ancestor == 1 {
		// in this case it is cheaper to just read the header
		if header := hc.GetHeaderByHash(hash); header != nil {
			return header.ParentHash(hc.NodeCtx()), number - 1
		}
		return common.Hash{}, 0
	}
	for ancestor != 0 {
		if rawdb.ReadCanonicalHash(hc.headerDb, number) == hash {
			ancestorHash := rawdb.ReadCanonicalHash(hc.headerDb, number-ancestor)
			if rawdb.ReadCanonicalHash(hc.headerDb, number) == hash {
				number -= ancestor
				return ancestorHash, number
			}
		}
		if *maxNonCanonical == 0 {
			return common.Hash{}, 0
		}
		*maxNonCanonical--
		ancestor--
		header := hc.GetHeaderByHash(hash)
		if header == nil {
			return common.Hash{}, 0
		}
		hash = header.ParentHash(hc.NodeCtx())
		number--
	}
	return hash, number
}

func (hc *HeaderChain) WriteBlock(block *types.WorkObject) {
	hc.bc.WriteBlock(block, hc.NodeCtx())
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (hc *HeaderChain) GetHeaderByHash(hash common.Hash) *types.WorkObject {
	termini := hc.GetTerminiByHash(hash)
	if termini == nil {
		return nil
	}

	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return &header
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	header := rawdb.ReadHeader(hc.headerDb, *number, hash)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, *header)
	return header
}

// GetHeaderOrCandidateByHash retrieves a block header from the database by hash,
// caching it if found.
func (hc *HeaderChain) GetHeaderOrCandidateByHash(hash common.Hash) *types.WorkObject {
	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return &header
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	header := rawdb.ReadHeader(hc.headerDb, *number, hash)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, *header)
	return header
}

// RecoverCurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache
func (hc *HeaderChain) RecoverCurrentHeader() *types.WorkObject {
	// Start logarithmic ascent to find the upper bound
	high := uint64(1)
	for hc.GetHeaderByNumber(high) != nil {
		high *= 2
	}
	// Run binary search to find the max header
	low := high / 2
	for low <= high {
		mid := (low + high) / 2
		if hc.GetHeaderByNumber(mid) != nil {
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	header := hc.GetHeaderByNumber(high)
	hc.logger.WithField("Hash", header.Hash().String()).Info("Header Recovered")

	return header
}

// HasHeader checks if a block header is present in the database or not.
// In theory, if header is present in the database, all relative components
// like td and hash->number should be present too.
func (hc *HeaderChain) HasHeader(hash common.Hash, number uint64) bool {
	return rawdb.HasHeader(hc.headerDb, hash, number)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (hc *HeaderChain) GetHeaderByNumber(number uint64) *types.WorkObject {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetHeaderByHash(hash)
}

func (hc *HeaderChain) GetCanonicalHash(number uint64) common.Hash {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	return hash
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (hc *HeaderChain) CurrentHeader() *types.WorkObject {
	return hc.currentHeader.Load().(*types.WorkObject)
}

// CurrentBlock returns the block for the current header.
func (hc *HeaderChain) CurrentBlock() *types.WorkObject {
	return hc.GetBlockOrCandidateByHash(hc.CurrentHeader().Hash())
}

// SetGenesis sets a new genesis block header for the chain
func (hc *HeaderChain) SetGenesis(head *types.WorkObject) {
	hc.genesisHeader = head
}

// Config retrieves the header chain's chain configuration.
func (hc *HeaderChain) Config() *params.ChainConfig { return hc.config }

// GetBlock implements consensus.ChainReader, and returns nil for every input as
// a header chain does not have blocks available for retrieval.
func (hc *HeaderChain) GetBlock(hash common.Hash, number uint64) *types.WorkObject {
	return hc.bc.GetBlock(hash, number)
}

func (hc *HeaderChain) GetWorkObject(hash common.Hash) *types.WorkObject {
	return hc.bc.GetWorkObject(hash)
}

func (hc *HeaderChain) GetWorkObjectWithWorkShares(hash common.Hash) *types.WorkObject {
	return hc.bc.GetWorkObjectWithWorkShares(hash)
}

// CheckContext checks to make sure the range of a context or order is valid
func (hc *HeaderChain) CheckContext(context int) error {
	if context < 0 || context > common.HierarchyDepth {
		return errors.New("the provided path is outside the allowable range")
	}
	return nil
}

// GasLimit returns the gas limit of the current HEAD block.
func (hc *HeaderChain) GasLimit() uint64 {
	return hc.CurrentHeader().GasLimit()
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetUnclesInChain(block *types.WorkObject, length int) []*types.WorkObjectHeader {
	uncles := []*types.WorkObjectHeader{}
	for i := 0; block != nil && i < length; i++ {
		uncles = append(uncles, block.Uncles()...)
		block = hc.GetBlock(block.ParentHash(hc.NodeCtx()), block.NumberU64(hc.NodeCtx())-1)
	}
	return uncles
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetGasUsedInChain(block *types.WorkObject, length int) int64 {
	gasUsed := 0
	for i := 0; block != nil && i < length; i++ {
		gasUsed += int(block.GasUsed())
		block = hc.GetBlock(block.ParentHash(hc.NodeCtx()), block.NumberU64(hc.NodeCtx())-1)
	}
	return int64(gasUsed)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) CalculateBaseFee(header *types.WorkObject) *big.Int {
	return header.BaseFee()
}

// Export writes the active chain to the given writer.
func (hc *HeaderChain) Export(w io.Writer) error {
	return hc.ExportN(w, uint64(0), hc.CurrentHeader().NumberU64(hc.NodeCtx()))
}

// ExportN writes a subset of the active chain to the given writer.
func (hc *HeaderChain) ExportN(w io.Writer, first uint64, last uint64) error {
	return nil
}

// GetBlockFromCacheOrDb looks up the body cache first and then checks the db
func (hc *HeaderChain) GetBlockFromCacheOrDb(hash common.Hash, number uint64) *types.WorkObject {
	// Short circuit if the block's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.blockCache.Get(hash); ok {
		block := cached
		return &block
	}
	return hc.GetBlock(hash, number)
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (hc *HeaderChain) GetBlockByHash(hash common.Hash) *types.WorkObject {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.GetBlock(hash, *number)
}

func (hc *HeaderChain) GetBlockOrCandidate(hash common.Hash, number uint64) *types.WorkObject {
	return hc.bc.GetBlockOrCandidate(hash, number)
}

// GetBlockOrCandidateByHash retrieves any block from the database by hash, caching it if found.
func (hc *HeaderChain) GetBlockOrCandidateByHash(hash common.Hash) *types.WorkObject {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.bc.GetBlockOrCandidate(hash, *number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (hc *HeaderChain) GetBlockByNumber(number uint64) *types.WorkObject {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetBlock(hash, number)
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (hc *HeaderChain) GetBody(hash common.Hash) *types.WorkObject {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.bodyCache.Get(hash); ok {
		body := cached
		return &body
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadWorkObject(hc.headerDb, *number, hash, types.BlockObject)
	if body == nil {
		return nil
	}
	// Cache the found body for next time and return
	hc.bc.bodyCache.Add(hash, *body)
	return body
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (hc *HeaderChain) GetBlocksFromHash(hash common.Hash, n int) (blocks types.WorkObjects) {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := hc.GetBlock(hash, *number)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash(hc.NodeCtx())
		*number--
	}
	return
}

func (hc *HeaderChain) NodeLocation() common.Location {
	return hc.bc.NodeLocation()
}

func (hc *HeaderChain) NodeCtx() int {
	return hc.bc.NodeCtx()
}

// Engine reterives the consensus engine.
func (hc *HeaderChain) Engine() consensus.Engine {
	return hc.engine
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (hc *HeaderChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return hc.scope.Track(hc.chainHeadFeed.Subscribe(ch))
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (hc *HeaderChain) SubscribeUnlocksEvent(ch chan<- UnlocksEvent) event.Subscription {
	return hc.scope.Track(hc.unlocksFeed.Subscribe(ch))
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (hc *HeaderChain) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return hc.scope.Track(hc.chainSideFeed.Subscribe(ch))
}

func (hc *HeaderChain) StateAt(root, etxRoot common.Hash, quaiStateSize *big.Int) (*state.StateDB, error) {
	return hc.bc.processor.StateAt(root, etxRoot, quaiStateSize)
}

func (hc *HeaderChain) SlicesRunning() []common.Location {
	return hc.slicesRunning
}

func (hc *HeaderChain) ComputeExpansionNumber(parent *types.WorkObject) (uint8, error) {
	// If the parent is a prime block, prime terminus is the parent hash
	_, order, err := hc.engine.CalcOrder(hc, parent)
	if err != nil {
		return 0, err
	}
	var primeTerminusHash common.Hash
	if order == common.PRIME_CTX {
		primeTerminusHash = parent.Hash()
	} else {
		primeTerminusHash = parent.PrimeTerminusHash()
	}

	primeTerminus := hc.GetBlockByHash(primeTerminusHash)

	if primeTerminus == nil {
		return 0, errors.New("prime terminus is nil in compute expansion number")
	}

	// If the Prime Terminus is genesis the expansion number is the genesis expansion number
	if hc.IsGenesisHash(primeTerminusHash) && hc.NodeLocation().Equal(common.Location{0, 0}) {
		return primeTerminus.ExpansionNumber(), nil
	} else {
		// check if the prime terminus is the block where the threshold count
		// exceeds the trigger and expansion wait window Expansion happens when
		// the threshold count is greater than the expansion threshold and we
		// cross the tree expansion trigger window
		if primeTerminus.ThresholdCount() == params.TREE_EXPANSION_TRIGGER_WINDOW+params.TREE_EXPANSION_WAIT_COUNT {
			return primeTerminus.ExpansionNumber() + 1, nil
		}
		// get the parent of the prime terminus
		parentOfPrimeTerminus := hc.fetchPrimeBlock(primeTerminus.ParentHash(common.PRIME_CTX))
		if parentOfPrimeTerminus == nil {
			return 0, fmt.Errorf("parent of prime terminus is nil %v", primeTerminus.ParentHash(common.PRIME_CTX))
		}
		return parentOfPrimeTerminus.ExpansionNumber(), nil
	}
}

// ComputeEfficiencyScore calculates the efficiency score for the given header
func (hc *HeaderChain) ComputeEfficiencyScore(parent *types.WorkObject) (uint16, error) {
	deltaEntropy := new(big.Int).Add(parent.ParentDeltaEntropy(common.REGION_CTX), parent.ParentDeltaEntropy(common.ZONE_CTX))
	uncledDeltaEntropy := new(big.Int).Add(parent.ParentUncledDeltaEntropy(common.REGION_CTX), parent.ParentUncledDeltaEntropy(common.ZONE_CTX))

	// Take the ratio of deltaEntropy to the uncledDeltaEntropy in percentage
	efficiencyScore := uncledDeltaEntropy.Mul(uncledDeltaEntropy, big.NewInt(100))
	if deltaEntropy.Cmp(common.Big0) == 0 {
		hc.logger.Error(errInvalidEfficiencyScore)
		return 0, errInvalidEfficiencyScore
	}
	efficiencyScore.Div(efficiencyScore, deltaEntropy)

	// Calculate the exponential moving average
	ewma := (uint16(efficiencyScore.Uint64()) + parent.EfficiencyScore()*params.TREE_EXPANSION_FILTER_ALPHA) / 10
	return ewma, nil
}

// CalcMaxBaseFee takes an average of the base fee over past 100 blocks
func (hc *HeaderChain) CalcMaxBaseFee(block *types.WorkObject) (*big.Int, error) {
	// get the parent block
	parent := hc.GetBlockByHash(block.ParentHash(hc.NodeCtx()))
	if parent == nil {
		return nil, errors.New("parent cannot be found in the CalcMaxBaseFee")
	}
	parentBaseFee := parent.BaseFee()
	// Adjust the max base fee calculation for next block using this formula,
	// nextBlockBaseFee = blockBaseFee/OneOverBaseFeeControllerAlpha + parentBaseFee
	baseFee := new(big.Int).Div(block.BaseFee(), params.OneOverBaseFeeControllerAlpha)
	baseFee = new(big.Int).Add(baseFee, parentBaseFee)

	// Make sure max base fee is less than 50x of the average base fee
	baseFee = new(big.Int).Mul(baseFee, params.BaseFeeMultiplier)

	minBaseFee := hc.CalcMinBaseFee(block)
	if minBaseFee.Cmp(baseFee) >= 0 {
		baseFee = minBaseFee
	}

	return baseFee, nil
}

// CalcMinBaseFee calculates the mininum base fee supplied by the transaction
// to get inclusion in the next block
func (hc *HeaderChain) CalcMinBaseFee(block *types.WorkObject) *big.Int {
	// If the base fee is calculated is less than the min base fee, then set
	// this to min base fee
	minBaseFee := misc.QiToQuai(block, params.MinBaseFeeInQits)
	minBaseFee = new(big.Int).Div(minBaseFee, big.NewInt(int64(params.TxGas)))
	return minBaseFee
}

// UpdateEtxEligibleSlices returns the updated etx eligible slices field
func (hc *HeaderChain) UpdateEtxEligibleSlices(header *types.WorkObject, location common.Location) common.Hash {
	// After 5 days of the start of a new chain, the chain becomes eligible to receive etxs
	position := location[0]*16 + location[1]
	byteIndex := position / 8      // Find the byte index within the array
	bitIndex := uint(position % 8) // Find the specific bit within the byte, cast to uint for bit operations
	newHash := header.EtxEligibleSlices()
	if header.NumberU64(common.ZONE_CTX) > params.TimeToStartTx {
		// Set the position bit to 1
		newHash[byteIndex] |= 1 << bitIndex
	} else {
		// Set the position bit to 0
		newHash[byteIndex] &^= 1 << bitIndex
	}
	return newHash
}

// CheckIfETXIsEligible checks if the given zone location is eligible to receive
// etx based on the etxEligibleSlices hash
func (hc *HeaderChain) CheckIfEtxIsEligible(etxEligibleSlices common.Hash, to common.Location) bool {
	position := to.Region()*16 + to.Zone()
	// Calculate the index of the byte and the specific bit within that byte
	byteIndex := position / 8      // Find the byte index within the array
	bitIndex := uint(position % 8) // Find the specific bit within the byte, cast to uint for bit operations

	// Check if the bit is set to 1
	return etxEligibleSlices[byteIndex]&(1<<bitIndex) != 0
}

// IsGenesisHash checks if a hash is a genesis hash
func (hc *HeaderChain) IsGenesisHash(hash common.Hash) bool {
	return rawdb.IsGenesisHash(hc.headerDb, hash)
}

// AddGenesisHash appends the given hash to the genesis hash list
func (hc *HeaderChain) AddGenesisHash(hash common.Hash) {
	genesisHashes := rawdb.ReadGenesisHashes(hc.headerDb)
	genesisHashes = append(genesisHashes, hash)

	// write the genesis hash to the database
	rawdb.WriteGenesisHashes(hc.headerDb, genesisHashes)
}

// GetGenesisHashes returns the genesis hashes stored
func (hc *HeaderChain) GetGenesisHashes() []common.Hash {
	return rawdb.ReadGenesisHashes(hc.headerDb)
}

func (hc *HeaderChain) SetCurrentExpansionNumber(expansionNumber uint8) {
	hc.currentExpansionNumber = expansionNumber
}

func (hc *HeaderChain) GetExpansionNumber() uint8 {
	return hc.currentExpansionNumber
}

func (hc *HeaderChain) GetPrimeTerminus(header *types.WorkObject) *types.WorkObject {
	return hc.GetHeaderByHash(header.PrimeTerminusHash())
}

func (hc *HeaderChain) WriteAddressOutpoints(outpoints map[[20]byte][]*types.OutpointAndDenomination) error {
	return rawdb.WriteAddressOutpoints(hc.bc.db, outpoints)
}

func (hc *HeaderChain) GetMaxTxInWorkShare() uint64 {
	currentGasLimit := hc.CurrentHeader().GasLimit()
	maxEoaInBlock := currentGasLimit / params.TxGas
	// (maxEoaInBlock*2)/(2^bits)
	currentHeader := hc.CurrentHeader()
	if currentHeader != nil && currentHeader.NumberU64(common.ZONE_CTX) < params.GoldenAgeForkNumberV2 {
		return (maxEoaInBlock * 2) / uint64(math.Pow(2, float64(params.OldWorkSharesThresholdDiff)))
	} else {
		return (maxEoaInBlock * 2) / uint64(math.Pow(2, float64(params.NewWorkSharesThresholdDiff)))
	}
}

func (hc *HeaderChain) Database() ethdb.Database {
	return hc.headerDb
}
