package core

import (
	"errors"
	"fmt"
	"io"
	"math/big"
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
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru"
)

const (
	headerCacheLimit      = 512
	numberCacheLimit      = 2048
	primeHorizonThreshold = 20
)

type HeaderChain struct {
	config *params.ChainConfig

	bc     *BodyDb
	engine consensus.Engine

	chainHeadFeed event.Feed
	chainSideFeed event.Feed
	scope         event.SubscriptionScope

	headerDb      ethdb.Database
	genesisHeader *types.Header

	currentHeader atomic.Value // Current head of the header chain (may be above the block chain!)

	headerCache *lru.Cache // Cache for the most recent block headers
	numberCache *lru.Cache // Cache for the most recent block numbers

	pendingEtxsRollup            *lru.Cache
	pendingEtxs                  *lru.Cache
	blooms                       *lru.Cache
	missingPendingEtxsFeed       event.Feed
	missingPendingEtxsRollupFeed event.Feed

	wg            sync.WaitGroup // chain processing wait group for shutting down
	running       int32          // 0 if chain is running, 1 when stopped
	procInterrupt int32          // interrupt signaler for block processing

	headermu      sync.RWMutex
	heads         []*types.Header
	slicesRunning []common.Location
}

// NewHeaderChain creates a new HeaderChain structure. ProcInterrupt points
// to the parent's interrupt semaphore.
func NewHeaderChain(db ethdb.Database, engine consensus.Engine, chainConfig *params.ChainConfig, cacheConfig *CacheConfig, txLookupLimit *uint64, vmConfig vm.Config, slicesRunning []common.Location) (*HeaderChain, error) {
	headerCache, _ := lru.New(headerCacheLimit)
	numberCache, _ := lru.New(numberCacheLimit)

	hc := &HeaderChain{
		config:        chainConfig,
		headerDb:      db,
		headerCache:   headerCache,
		numberCache:   numberCache,
		engine:        engine,
		slicesRunning: slicesRunning,
	}

	pendingEtxsRollup, _ := lru.New(c_maxPendingEtxsRollup)
	hc.pendingEtxsRollup = pendingEtxsRollup

	pendingEtxs, _ := lru.New(c_maxPendingEtxBatches)
	hc.pendingEtxs = pendingEtxs

	blooms, _ := lru.New(c_maxBloomFilters)
	hc.blooms = blooms

	hc.genesisHeader = hc.GetHeaderByNumber(0)
	if hc.genesisHeader.Hash() != chainConfig.GenesisHash {
		return nil, fmt.Errorf("genesis block mismatch: have %x, want %x", hc.genesisHeader.Hash(), chainConfig.GenesisHash)
	}
	log.Info("Genesis", "Hash:", hc.genesisHeader.Hash())
	if hc.genesisHeader == nil {
		return nil, ErrNoGenesis
	}
	//Load any state that is in our db
	if err := hc.loadLastState(); err != nil {
		return nil, err
	}

	var err error
	hc.bc, err = NewBodyDb(db, engine, hc, chainConfig, cacheConfig, txLookupLimit, vmConfig, slicesRunning)
	if err != nil {
		return nil, err
	}

	// Initialize the heads slice
	heads := make([]*types.Header, 0)
	hc.heads = heads

	return hc, nil
}

// CollectSubRollup collects the rollup of ETXs emitted from the subordinate
// chain in the slice which emitted the given block.
func (hc *HeaderChain) CollectSubRollup(b *types.Block) (types.Transactions, error) {
	nodeCtx := common.NodeLocation.Context()
	subRollup := types.Transactions{}
	if nodeCtx < common.ZONE_CTX {
		// Since in prime the pending etxs are stored in 2 parts, pendingEtxsRollup
		// consists of region header and its sub manifests
		// Prime independently stores the pending etxs for each of the hashes in
		// the sub manifests, so it needs the pendingEtxsRollup to do so.
		for _, hash := range b.SubManifest() {
			if nodeCtx == common.PRIME_CTX {
				pEtxRollup, err := hc.GetPendingEtxsRollup(hash)
				if err == nil {
					for _, pEtxHash := range pEtxRollup.Manifest {
						pendingEtxs, err := hc.GetPendingEtxs(pEtxHash)
						if err != nil {
							// Start backfilling the missing pending ETXs needed to process this block
							go hc.backfillPETXs(b.Header(), b.SubManifest())
							return nil, ErrPendingEtxNotFound
						}
						subRollup = append(subRollup, pendingEtxs.Etxs...)
					}
				} else {
					// Start backfilling the missing pending ETXs needed to process this block
					go hc.backfillPETXs(b.Header(), b.SubManifest())
					return nil, ErrPendingEtxNotFound
				}
				// Region works normally as before collecting pendingEtxs for each hash in the manifest
			} else if nodeCtx == common.REGION_CTX {
				pendingEtxs, err := hc.GetPendingEtxs(hash)
				if err != nil {
					// Start backfilling the missing pending ETXs needed to process this block
					go hc.backfillPETXs(b.Header(), b.SubManifest())
					return nil, ErrPendingEtxNotFound
				}
				subRollup = append(subRollup, pendingEtxs.Etxs...)
			}
		}
		// Rolluphash is specifically for zone rollup, which can only be validated by region
		if nodeCtx == common.REGION_CTX {
			if subRollupHash := types.DeriveSha(subRollup, trie.NewStackTrie(nil)); subRollupHash != b.EtxRollupHash() {
				return nil, errors.New("sub rollup does not match sub rollup hash")
			}
		}
	}
	return subRollup, nil
}

// GetPendingEtxs gets the pendingEtxs form the
func (hc *HeaderChain) GetPendingEtxs(hash common.Hash) (*types.PendingEtxs, error) {
	var pendingEtxs types.PendingEtxs
	// Look for pending ETXs first in pending ETX cache, then in database
	if res, ok := hc.pendingEtxs.Get(hash); ok && res != nil {
		pendingEtxs = res.(types.PendingEtxs)
	} else if res := rawdb.ReadPendingEtxs(hc.headerDb, hash); res != nil {
		pendingEtxs = *res
	} else {
		log.Trace("unable to find pending etxs for hash in manifest", "hash:", hash.String())
		return nil, ErrPendingEtxNotFound
	}
	return &pendingEtxs, nil
}

func (hc *HeaderChain) GetPendingEtxsRollup(hash common.Hash) (*types.PendingEtxsRollup, error) {
	var rollups types.PendingEtxsRollup
	// Look for pending ETXs first in pending ETX cache, then in database
	if res, ok := hc.pendingEtxsRollup.Get(hash); ok && res != nil {
		rollups = res.(types.PendingEtxsRollup)
	} else if res := rawdb.ReadPendingEtxsRollup(hc.headerDb, hash); res != nil {
		rollups = *res
	} else {
		log.Trace("unable to find pending etxs rollups for hash in manifest", "hash:", hash.String())
		return nil, ErrPendingEtxNotFound
	}
	return &rollups, nil
}

// GetBloom gets the bloom from the cache or database
func (hc *HeaderChain) GetBloom(hash common.Hash) (*types.Bloom, error) {
	var bloom types.Bloom
	// Look for bloom first in bloom cache, then in database
	if res, ok := hc.blooms.Get(hash); ok && res != nil {
		bloom = res.(types.Bloom)
	} else if res := rawdb.ReadBloom(hc.headerDb, hash); res != nil {
		bloom = *res
	} else {
		log.Debug("unable to find bloom for hash in database", "hash:", hash.String())
		return nil, ErrBloomNotFound
	}
	return &bloom, nil
}

// backfillPETXs collects any missing PendingETX objects needed to process the
// given header. This is done by informing the fetcher of any pending ETXs we do
// not have, so that they can be fetched from our peers.
func (hc *HeaderChain) backfillPETXs(header *types.Header, subManifest types.BlockManifest) {
	nodeCtx := common.NodeLocation.Context()
	for _, hash := range subManifest {
		if nodeCtx == common.PRIME_CTX {
			// In the case of prime, get the pendingEtxsRollup for each region block
			// and then fetch the pending etx for each of the rollup hashes
			if pEtxRollup, err := hc.GetPendingEtxsRollup(hash); err == nil {
				for _, pEtxHash := range pEtxRollup.Manifest {
					if _, err := hc.GetPendingEtxs(pEtxHash); err != nil {
						// Send the pendingEtxs to the feed for broadcast
						hc.missingPendingEtxsFeed.Send(types.HashAndLocation{Hash: pEtxHash, Location: pEtxRollup.Header.Location()})
					}
				}
			} else {
				hc.missingPendingEtxsRollupFeed.Send(hash)
			}
		} else if nodeCtx == common.REGION_CTX {
			if _, err := hc.GetPendingEtxs(hash); err != nil {
				// Send the pendingEtxs to the feed for broadcast
				hc.missingPendingEtxsFeed.Send(types.HashAndLocation{Hash: hash, Location: header.Location()})
			}
		}
	}
}

// Collect all emmitted ETXs since the last coincident block, but excluding
// those emitted in this block
func (hc *HeaderChain) CollectEtxRollup(b *types.Block) (types.Transactions, error) {
	if b.NumberU64() == 0 && b.Hash() == hc.config.GenesisHash {
		return b.ExtTransactions(), nil
	}
	parent := hc.GetBlock(b.ParentHash(), b.NumberU64()-1)
	if parent == nil {
		return nil, errors.New("parent not found")
	}
	return hc.collectInclusiveEtxRollup(parent)
}

func (hc *HeaderChain) collectInclusiveEtxRollup(b *types.Block) (types.Transactions, error) {
	// Initialize the rollup with ETXs emitted by this block
	newEtxs := b.ExtTransactions()
	// Terminate the search if we reached genesis
	if b.NumberU64() == 0 {
		if b.Hash() != hc.config.GenesisHash {
			return nil, fmt.Errorf("manifest builds on incorrect genesis, block0 hash: %s", b.Hash().String())
		} else {
			return newEtxs, nil
		}
	}
	// Terminate the search on coincidence with dom chain
	if hc.engine.IsDomCoincident(hc, b.Header()) {
		return newEtxs, nil
	}
	// Recursively get the ancestor rollup, until a coincident ancestor is found
	ancestor := hc.GetBlock(b.ParentHash(), b.NumberU64()-1)
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
func (hc *HeaderChain) AppendHeader(header *types.Header) error {
	nodeCtx := common.NodeLocation.Context()
	log.Debug("HeaderChain Append:", "Header information: Hash:", header.Hash(), "header header hash:", header.Hash(), "Number:", header.NumberU64(), "Location:", header.Location, "Parent:", header.ParentHash())

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
		manifest := rawdb.ReadManifest(hc.headerDb, header.ParentHash())
		if manifest == nil {
			return errors.New("manifest not found for parent")
		}
		if header.ManifestHash(nodeCtx) != types.DeriveSha(manifest, trie.NewStackTrie(nil)) {
			return errors.New("manifest does not match hash")
		}
	}

	return nil
}
func (hc *HeaderChain) ProcessingState() bool {
	return hc.bc.ProcessingState()
}

// Append
func (hc *HeaderChain) AppendBlock(block *types.Block, newInboundEtxs types.Transactions) error {
	blockappend := time.Now()
	// Append block else revert header append
	logs, err := hc.bc.Append(block, newInboundEtxs)
	if err != nil {
		return err
	}
	log.Info("Time taken to", "Append in bc", common.PrettyDuration(time.Since(blockappend)))

	hc.bc.chainFeed.Send(ChainEvent{Block: block, Hash: block.Hash(), Logs: logs})
	if len(logs) > 0 {
		hc.bc.logsFeed.Send(logs)
	}

	return nil
}

// SetCurrentHeader sets the in-memory head header marker of the canonical chan
// as the given header.
func (hc *HeaderChain) SetCurrentHeader(head *types.Header) error {
	hc.headermu.Lock()
	defer hc.headermu.Unlock()

	prevHeader := hc.CurrentHeader()
	// if trying to set the same header, escape
	if prevHeader.Hash() == head.Hash() {
		return nil
	}
	//Find a common header
	commonHeader := hc.findCommonAncestor(head)
	newHeader := head

	// write the head block hash to the db
	rawdb.WriteHeadBlockHash(hc.headerDb, head.Hash())
	hc.currentHeader.Store(head)

	// If head is the normal extension of canonical head, we can return by just wiring the canonical hash.
	if prevHeader.Hash() == head.ParentHash() {
		rawdb.WriteCanonicalHash(hc.headerDb, head.Hash(), head.NumberU64())
		return nil
	}

	// Delete each header and rollback state processor until common header
	// Accumulate the hash slice stack
	var hashStack []*types.Header
	for {
		if newHeader.Hash() == commonHeader.Hash() {
			break
		}
		hashStack = append(hashStack, newHeader)
		newHeader = hc.GetHeader(newHeader.ParentHash(), newHeader.NumberU64()-1)

		// genesis check to not delete the genesis block
		if newHeader.Hash() == hc.config.GenesisHash {
			break
		}
	}

	for {
		if prevHeader.Hash() == commonHeader.Hash() {
			break
		}
		rawdb.DeleteCanonicalHash(hc.headerDb, prevHeader.NumberU64())
		prevHeader = hc.GetHeader(prevHeader.ParentHash(), prevHeader.NumberU64()-1)

		// genesis check to not delete the genesis block
		if prevHeader.Hash() == hc.config.GenesisHash {
			break
		}
	}

	// Run through the hash stack to update canonicalHash and forward state processor
	for i := len(hashStack) - 1; i >= 0; i-- {
		block := hc.GetBlockByHash(hashStack[i].Hash())
		if block == nil {
			return errors.New("Could not find block during reorg")
		}
		_, order, err := hc.engine.CalcOrder(block.Header())
		if err != nil {
			return err
		}
		nodeCtx := common.NodeLocation.Context()
		var inboundEtxs types.Transactions
		if order < nodeCtx {
			inboundEtxs = rawdb.ReadInboundEtxs(hc.headerDb, hashStack[i].Hash())
		}
		hc.AppendBlock(block, inboundEtxs)
		rawdb.WriteCanonicalHash(hc.headerDb, hashStack[i].Hash(), hashStack[i].NumberU64())
	}

	return nil
}

// findCommonAncestor
func (hc *HeaderChain) findCommonAncestor(header *types.Header) *types.Header {
	for {
		if header == nil {
			return nil
		}
		canonicalHash := rawdb.ReadCanonicalHash(hc.headerDb, header.NumberU64())
		if canonicalHash == header.Hash() {
			return hc.GetHeaderByHash(canonicalHash)
		}
		header = hc.GetHeader(header.ParentHash(), header.NumberU64()-1)
	}

}

func (hc *HeaderChain) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	if !pEtxs.IsValid(trie.NewStackTrie(nil)) {
		return ErrPendingEtxNotValid
	}
	log.Debug("Received pending ETXs", "block: ", pEtxs.Header.Hash())
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
		}
	}

	heads := make([]*types.Header, 0)
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

	hashes := make([]common.Hash, 0)
	for i := 0; i < len(hc.heads); i++ {
		hashes = append(hashes, hc.heads[i].Hash())
	}
	// Save the heads
	rawdb.WriteHeadsHashes(hc.headerDb, hashes)
	rawdb.WriteHeadBlockHash(hc.headerDb, hc.CurrentHeader().Hash())

	// Unsubscribe all subscriptions registered from blockchain
	hc.scope.Close()
	hc.bc.scope.Close()
	hc.wg.Wait()
	if common.NodeLocation.Context() == common.ZONE_CTX {
		hc.bc.processor.Stop()
	}
	log.Info("headerchain stopped")
}

// Empty checks if the headerchain is empty.
func (hc *HeaderChain) Empty() bool {
	genesis := hc.config.GenesisHash
	for _, hash := range []common.Hash{rawdb.ReadHeadBlockHash(hc.headerDb)} {
		if hash != genesis {
			return false
		}
	}
	return true
}

// GetBlockNumber retrieves the block number belonging to the given hash
// from the cache or database
func (hc *HeaderChain) GetBlockNumber(hash common.Hash) *uint64 {
	if cached, ok := hc.numberCache.Get(hash); ok {
		number := cached.(uint64)
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
		next := header.ParentHash()
		if header = hc.GetHeader(next, header.NumberU64()-1); header == nil {
			break
		}
		chain = append(chain, next)
		if header.Number().Sign() == 0 {
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
		if header := hc.GetHeader(hash, number); header != nil {
			return header.ParentHash(), number - 1
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
		header := hc.GetHeader(hash, number)
		if header == nil {
			return common.Hash{}, 0
		}
		hash = header.ParentHash()
		number--
	}
	return hash, number
}

func (hc *HeaderChain) WriteBlock(block *types.Block) {
	hc.bc.WriteBlock(block)
}

// GetHeader retrieves a block header from the database by hash and number,
// caching it if found.
func (hc *HeaderChain) GetHeader(hash common.Hash, number uint64) *types.Header {
	termini := hc.GetTerminiByHash(hash)
	if termini == nil {
		return nil
	}
	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return header.(*types.Header)
	}
	header := rawdb.ReadHeader(hc.headerDb, hash, number)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, header)
	return header
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (hc *HeaderChain) GetHeaderByHash(hash common.Hash) *types.Header {
	termini := hc.GetTerminiByHash(hash)
	if termini == nil {
		return nil
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}

	return hc.GetHeader(hash, *number)
}

// GetHeaderOrCandidate retrieves a block header from the database by hash and number,
// caching it if found.
func (hc *HeaderChain) GetHeaderOrCandidate(hash common.Hash, number uint64) *types.Header {
	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return header.(*types.Header)
	}
	header := rawdb.ReadHeader(hc.headerDb, hash, number)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, header)
	return header
}

// GetHeaderOrCandidateByHash retrieves a block header from the database by hash, caching it if
// found.
func (hc *HeaderChain) GetHeaderOrCandidateByHash(hash common.Hash) *types.Header {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}

	return hc.GetHeaderOrCandidate(hash, *number)
}

// HasHeader checks if a block header is present in the database or not.
// In theory, if header is present in the database, all relative components
// like td and hash->number should be present too.
func (hc *HeaderChain) HasHeader(hash common.Hash, number uint64) bool {
	if hc.numberCache.Contains(hash) || hc.headerCache.Contains(hash) {
		return true
	}
	return rawdb.HasHeader(hc.headerDb, hash, number)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (hc *HeaderChain) GetHeaderByNumber(number uint64) *types.Header {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetHeader(hash, number)
}

func (hc *HeaderChain) GetCanonicalHash(number uint64) common.Hash {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	return hash
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (hc *HeaderChain) CurrentHeader() *types.Header {
	return hc.currentHeader.Load().(*types.Header)
}

// CurrentBlock returns the block for the current header.
func (hc *HeaderChain) CurrentBlock() *types.Block {
	return hc.GetBlockByHash(hc.CurrentHeader().Hash())
}

// SetGenesis sets a new genesis block header for the chain
func (hc *HeaderChain) SetGenesis(head *types.Header) {
	hc.genesisHeader = head
}

// Config retrieves the header chain's chain configuration.
func (hc *HeaderChain) Config() *params.ChainConfig { return hc.config }

// GetBlock implements consensus.ChainReader, and returns nil for every input as
// a header chain does not have blocks available for retrieval.
func (hc *HeaderChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	return hc.bc.GetBlock(hash, number)
}

// CheckContext checks to make sure the range of a context or order is valid
func (hc *HeaderChain) CheckContext(context int) error {
	if context < 0 || context > common.HierarchyDepth {
		return errors.New("the provided path is outside the allowable range")
	}
	return nil
}

// CheckLocationRange checks to make sure the range of r and z are valid
func (hc *HeaderChain) CheckLocationRange(location []byte) error {
	if int(location[0]) < 1 || int(location[0]) > common.NumRegionsInPrime {
		return errors.New("the provided location is outside the allowable region range")
	}
	if int(location[1]) < 1 || int(location[1]) > common.NumZonesInRegion {
		return errors.New("the provided location is outside the allowable zone range")
	}
	return nil
}

// GasLimit returns the gas limit of the current HEAD block.
func (hc *HeaderChain) GasLimit() uint64 {
	return hc.CurrentHeader().GasLimit()
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	uncles := []*types.Header{}
	for i := 0; block != nil && i < length; i++ {
		uncles = append(uncles, block.Uncles()...)
		block = hc.GetBlock(block.ParentHash(), block.NumberU64()-1)
	}
	return uncles
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetGasUsedInChain(block *types.Block, length int) int64 {
	gasUsed := 0
	for i := 0; block != nil && i < length; i++ {
		gasUsed += int(block.GasUsed())
		block = hc.GetBlock(block.ParentHash(), block.NumberU64()-1)
	}
	return int64(gasUsed)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) CalculateBaseFee(header *types.Header) *big.Int {
	return misc.CalcBaseFee(hc.Config(), header)
}

// Export writes the active chain to the given writer.
func (hc *HeaderChain) Export(w io.Writer) error {
	return hc.ExportN(w, uint64(0), hc.CurrentHeader().NumberU64())
}

// ExportN writes a subset of the active chain to the given writer.
func (hc *HeaderChain) ExportN(w io.Writer, first uint64, last uint64) error {
	hc.headermu.RLock()
	defer hc.headermu.RUnlock()

	if first > last {
		return fmt.Errorf("export failed: first (%d) is greater than last (%d)", first, last)
	}
	log.Info("Exporting batch of blocks", "count", last-first+1)

	start, reported := time.Now(), time.Now()
	for nr := first; nr <= last; nr++ {
		block := hc.GetBlockByNumber(nr)
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

// GetBlockFromCacheOrDb looks up the body cache first and then checks the db
func (hc *HeaderChain) GetBlockFromCacheOrDb(hash common.Hash, number uint64) *types.Block {
	// Short circuit if the block's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.blockCache.Get(hash); ok {
		block := cached.(*types.Block)
		return block
	}
	return hc.GetBlock(hash, number)
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (hc *HeaderChain) GetBlockByHash(hash common.Hash) *types.Block {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.GetBlock(hash, *number)
}

// GetBlockOrCandidateByHash retrieves any block from the database by hash, caching it if found.
func (hc *HeaderChain) GetBlockOrCandidateByHash(hash common.Hash) *types.Block {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.bc.GetBlockOrCandidate(hash, *number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (hc *HeaderChain) GetBlockByNumber(number uint64) *types.Block {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetBlock(hash, number)
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (hc *HeaderChain) GetBody(hash common.Hash) *types.Body {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.bodyCache.Get(hash); ok {
		body := cached.(*types.Body)
		return body
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBody(hc.headerDb, hash, *number)
	if body == nil {
		return nil
	}
	// Cache the found body for next time and return
	hc.bc.bodyCache.Add(hash, body)
	return body
}

// GetBodyRLP retrieves a block body in RLP encoding from the database by hash,
// caching it if found.
func (hc *HeaderChain) GetBodyRLP(hash common.Hash) rlp.RawValue {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.bodyRLPCache.Get(hash); ok {
		return cached.(rlp.RawValue)
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBodyRLP(hc.headerDb, hash, *number)
	if len(body) == 0 {
		return nil
	}
	// Cache the found body for next time and return
	hc.bc.bodyRLPCache.Add(hash, body)
	return body
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (hc *HeaderChain) GetBlocksFromHash(hash common.Hash, n int) (blocks []*types.Block) {
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
		hash = block.ParentHash()
		*number--
	}
	return
}

// Engine reterives the consensus engine.
func (hc *HeaderChain) Engine() consensus.Engine {
	return hc.engine
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (hc *HeaderChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return hc.scope.Track(hc.chainHeadFeed.Subscribe(ch))
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (hc *HeaderChain) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return hc.scope.Track(hc.chainSideFeed.Subscribe(ch))
}

func (hc *HeaderChain) SubscribeMissingPendingEtxsEvent(ch chan<- types.HashAndLocation) event.Subscription {
	return hc.scope.Track(hc.missingPendingEtxsFeed.Subscribe(ch))
}

func (hc *HeaderChain) SubscribeMissingPendingEtxsRollupEvent(ch chan<- common.Hash) event.Subscription {
	return hc.scope.Track(hc.missingPendingEtxsRollupFeed.Subscribe(ch))
}

func (hc *HeaderChain) StateAt(root common.Hash) (*state.StateDB, error) {
	return hc.bc.processor.StateAt(root)
}
