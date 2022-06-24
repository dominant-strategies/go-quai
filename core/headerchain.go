// Copyright 2015 The go-ethereum Authors
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

package core

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	mrand "math/rand"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/params"
)

const (
	headerCacheLimit = 512
	tdCacheLimit     = 1024
	numberCacheLimit = 2048
)

// HeaderChain implements the basic block header chain logic that is shared by
// core.BlockChain and light.LightChain. It is not usable in itself, only as
// a part of either structure.
//
// HeaderChain is responsible for maintaining the header chain including the
// header query and updating.
//
// The components maintained by headerchain includes: (1) total difficult
// (2) header (3) block hash -> number mapping (4) canonical number -> hash mapping
// and (5) head header flag.
//
// It is not thread safe either, the encapsulating chain structures should do
// the necessary mutex locking/unlocking.
type HeaderChain struct {
	config *params.ChainConfig

	chainDb       ethdb.Database
	genesisHeader *types.Header

	currentHeader     atomic.Value // Current head of the header chain (may be above the block chain!)
	currentHeaderHash common.Hash  // Hash of the current head of the header chain (prevent recomputing all the time)

	headerCache *lru.Cache // Cache for the most recent block headers
	tdCache     *lru.Cache // Cache for the most recent block total difficulties
	numberCache *lru.Cache // Cache for the most recent block numbers

	procInterrupt func() bool

	rand   *mrand.Rand
	engine consensus.Engine
}

// NewHeaderChain creates a new HeaderChain structure. ProcInterrupt points
// to the parent's interrupt semaphore.
func NewHeaderChain(chainDb ethdb.Database, config *params.ChainConfig, engine consensus.Engine, procInterrupt func() bool) (*HeaderChain, error) {
	headerCache, _ := lru.New(headerCacheLimit)
	tdCache, _ := lru.New(tdCacheLimit)
	numberCache, _ := lru.New(numberCacheLimit)

	// Seed a fast but crypto originating random generator
	seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, err
	}

	hc := &HeaderChain{
		config:        config,
		chainDb:       chainDb,
		headerCache:   headerCache,
		tdCache:       tdCache,
		numberCache:   numberCache,
		procInterrupt: procInterrupt,
		rand:          mrand.New(mrand.NewSource(seed.Int64())),
		engine:        engine,
	}

	hc.genesisHeader = hc.GetHeaderByNumber(0)
	if hc.genesisHeader == nil {
		return nil, ErrNoGenesis
	}

	hc.currentHeader.Store(hc.genesisHeader)
	if head := rawdb.ReadHeadBlockHash(chainDb); head != (common.Hash{}) {
		if chead := hc.GetHeaderByHash(head); chead != nil {
			hc.currentHeader.Store(chead)
		}
	}
	hc.currentHeaderHash = hc.CurrentHeader().Hash()
	headHeaderGauge.Update(hc.CurrentHeader().Number[types.QuaiNetworkContext].Int64())

	return hc, nil
}

// GetBlockNumber retrieves the block number belonging to the given hash
// from the cache or database
func (hc *HeaderChain) GetBlockNumber(hash common.Hash) *uint64 {
	if cached, ok := hc.numberCache.Get(hash); ok {
		number := cached.(uint64)
		return &number
	}
	number := rawdb.ReadHeaderNumber(hc.chainDb, hash)
	if number != nil {
		hc.numberCache.Add(hash, *number)
	}
	return number
}

type headerWriteResult struct {
	status     WriteStatus
	ignored    int
	imported   int
	lastHash   common.Hash
	lastHeader *types.Header
}

// WriteHeaders writes a chain of headers into the local chain, given that the parents
// are already known. If the total difficulty of the newly inserted chain becomes
// greater than the current known TD, the canonical chain is reorged.
//
// Note: This method is not concurrent-safe with inserting blocks simultaneously
// into the chain, as side effects caused by reorganisations cannot be emulated
// without the real blocks. Hence, writing headers directly should only be done
// in two scenarios: pure-header mode of operation (light clients), or properly
// separated header/block phases (non-archive clients).
func (hc *HeaderChain) writeHeaders(headers []*types.Header) (result *headerWriteResult, err error) {
	if len(headers) == 0 {
		return &headerWriteResult{}, nil
	}
	ptd := hc.GetTd(headers[0].ParentHash[types.QuaiNetworkContext], headers[0].Number[types.QuaiNetworkContext].Uint64()-1)
	if ptd == nil {
		return &headerWriteResult{}, consensus.ErrUnknownAncestor
	}
	var (
		lastNumber = headers[0].Number[types.QuaiNetworkContext].Uint64() - 1 // Last successfully imported number
		lastHash   = headers[0].ParentHash[types.QuaiNetworkContext]          // Last imported header hash

		newTd = ptd // Total difficulty of inserted chain

		lastHeader    *types.Header
		inserted      []numberHash // Ephemeral lookup of number/hash for the chain
		firstInserted = -1         // Index of the first non-ignored header
	)

	batch := hc.chainDb.NewBatch()
	parentKnown := true // Set to true to force hc.HasHeader check the first iteration
	for i, header := range headers {
		var hash common.Hash
		// The headers have already been validated at this point, so we already
		// know that it's a contiguous chain, where
		// headers[i].Hash() == headers[i+1].ParentHash
		if i < len(headers)-1 {
			hash = headers[i+1].ParentHash[types.QuaiNetworkContext]
		} else {
			hash = header.Hash()
		}

		newTd, err = hc.CalcTd(header)
		if err != nil {
			return &headerWriteResult{}, errors.New("error calculating the total td for the header")
		}

		number := header.Number[types.QuaiNetworkContext].Uint64()

		// If the parent was not present, store it
		// If the header is already known, skip it, otherwise store
		alreadyKnown := parentKnown && hc.HasHeader(hash, number)
		if !alreadyKnown {
			// Irrelevant of the canonical status, write the TD and header to the database.
			rawdb.WriteTd(batch, hash, number, newTd)
			hc.tdCache.Add(hash, newTd)

			rawdb.WriteHeader(batch, header)
			inserted = append(inserted, numberHash{number, hash})
			hc.headerCache.Add(hash, header)
			hc.numberCache.Add(hash, number)
			if firstInserted < 0 {
				firstInserted = i
			}
		}
		parentKnown = alreadyKnown
		lastHeader, lastHash, lastNumber = header, hash, number
	}

	// Skip the slow disk write of all headers if interrupted.
	if hc.procInterrupt() {
		log.Debug("Premature abort during headers import")
		return &headerWriteResult{}, errors.New("aborted")
	}
	// Commit to disk!
	if err := batch.Write(); err != nil {
		log.Crit("Failed to write headers", "error", err)
	}
	batch.Reset()

	var (
		head    = hc.CurrentHeader().Number[types.QuaiNetworkContext].Uint64()
		localTd = hc.GetTd(hc.currentHeaderHash, head)
		status  = SideStatTy
	)
	// If the total difficulty is higher than our known, add it to the canonical chain
	// Second clause in the if statement reduces the vulnerability to selfish mining.
	// Please refer to http://www.cs.cornell.edu/~ie53/publications/btcProcFC.pdf
	// TODO: need to change the HLCR logic to return something else if the difficulties are equal (replace the first if statement)
	reorg := hc.HLCR(localTd, newTd)
	equalTd := newTd[0].Cmp(localTd[0]) == 0 && newTd[1].Cmp(localTd[1]) == 0 && newTd[2].Cmp(localTd[2]) == 0
	if !reorg && equalTd {
		if lastNumber < head {
			reorg = true
		} else if lastNumber == head {
			reorg = mrand.Float64() < 0.5
		}
	}
	// If the parent of the (first) block is already the canon header,
	// we don't have to go backwards to delete canon blocks, but
	// simply pile them onto the existing chain
	chainAlreadyCanon := headers[0].ParentHash[types.QuaiNetworkContext] == hc.currentHeaderHash
	if reorg {
		// If the header can be added into canonical chain, adjust the
		// header chain markers(canonical indexes and head header flag).
		//
		// Note all markers should be written atomically.
		markerBatch := batch // we can reuse the batch to keep allocs down
		if !chainAlreadyCanon {
			// Delete any canonical number assignments above the new head
			for i := lastNumber + 1; ; i++ {
				hash := rawdb.ReadCanonicalHash(hc.chainDb, i)
				if hash == (common.Hash{}) {
					break
				}
				rawdb.DeleteCanonicalHash(markerBatch, i)
			}
			// Overwrite any stale canonical number assignments, going
			// backwards from the first header in this import
			var (
				headHash   = headers[0].ParentHash[types.QuaiNetworkContext]          // inserted[0].parent?
				headNumber = headers[0].Number[types.QuaiNetworkContext].Uint64() - 1 // inserted[0].num-1 ?
				headHeader = hc.GetHeader(headHash, headNumber)
			)
			for rawdb.ReadCanonicalHash(hc.chainDb, headNumber) != headHash {
				rawdb.WriteCanonicalHash(markerBatch, headHash, headNumber)
				headHash = headHeader.ParentHash[types.QuaiNetworkContext]
				headNumber = headHeader.Number[types.QuaiNetworkContext].Uint64() - 1
				headHeader = hc.GetHeader(headHash, headNumber)
			}
			// If some of the older headers were already known, but obtained canon-status
			// during this import batch, then we need to write that now
			// Further down, we continue writing the staus for the ones that
			// were not already known
			for i := 0; i < firstInserted; i++ {
				hash := headers[i].Hash()
				num := headers[i].Number[types.QuaiNetworkContext].Uint64()
				rawdb.WriteCanonicalHash(markerBatch, hash, num)
				rawdb.WriteHeadHeaderHash(markerBatch, hash)
			}
		}
		// Extend the canonical chain with the new headers
		for _, hn := range inserted {
			rawdb.WriteCanonicalHash(markerBatch, hn.hash, hn.number)
			rawdb.WriteHeadHeaderHash(markerBatch, hn.hash)
		}
		if err := markerBatch.Write(); err != nil {
			log.Crit("Failed to write header markers into disk", "err", err)
		}
		markerBatch.Reset()
		// Last step update all in-memory head header markers
		hc.currentHeaderHash = lastHash
		hc.currentHeader.Store(types.CopyHeader(lastHeader))
		headHeaderGauge.Update(lastHeader.Number[types.QuaiNetworkContext].Int64())

		// Chain status is canonical since this insert was a reorg.
		// Note that all inserts which have higher TD than existing are 'reorg'.
		status = CanonStatTy
	}

	if len(inserted) == 0 {
		status = NonStatTy
	}
	return &headerWriteResult{
		status:     status,
		ignored:    len(headers) - len(inserted),
		imported:   len(inserted),
		lastHash:   lastHash,
		lastHeader: lastHeader,
	}, nil
}

func (hc *HeaderChain) ValidateHeaderChain(chain []*types.Header, checkFreq int) (int, error) {
	// Do a sanity check that the provided chain is actually ordered and linked
	for i := 1; i < len(chain); i++ {
		if chain[i].Number[types.QuaiNetworkContext].Uint64() != chain[i-1].Number[types.QuaiNetworkContext].Uint64()+1 {
			hash := chain[i].Hash()
			parentHash := chain[i-1].Hash()
			// Chain broke ancestry, log a message (programming error) and skip insertion
			log.Error("Non contiguous header insert", "number", chain[i].Number, "hash", hash,
				"parent", chain[i].ParentHash, "prevnumber", chain[i-1].Number, "prevhash", parentHash)

			return 0, fmt.Errorf("non contiguous insert: item %d is #%d [%x..], item %d is #%d [%x..] (parent [%x..])", i-1, chain[i-1].Number,
				parentHash.Bytes()[:4], i, chain[i].Number, hash.Bytes()[:4], chain[i].ParentHash[:4])
		}
		// If the header is a banned one, straight out abort
		if BadHashes[chain[i].ParentHash[types.QuaiNetworkContext]] {
			return i - 1, ErrBannedHash
		}
		// If it's the last header in the cunk, we need to check it too
		if i == len(chain)-1 && BadHashes[chain[i].Hash()] {
			return i, ErrBannedHash
		}
	}

	// Generate the list of seal verification requests, and start the parallel verifier
	seals := make([]bool, len(chain))
	if checkFreq != 0 {
		// In case of checkFreq == 0 all seals are left false.
		for i := 0; i <= len(seals)/checkFreq; i++ {
			index := i*checkFreq + hc.rand.Intn(checkFreq)
			if index >= len(seals) {
				index = len(seals) - 1
			}
			seals[index] = true
		}
		// Last should always be verified to avoid junk.
		seals[len(seals)-1] = true
	}

	abort, results := hc.engine.VerifyHeaders(hc, chain, seals)
	defer close(abort)

	// Iterate over the headers and ensure they all check out
	for i := range chain {
		// If the chain is terminating, stop processing blocks
		if hc.procInterrupt() {
			log.Debug("Premature abort during headers verification")
			return 0, errors.New("aborted")
		}
		// Otherwise wait for headers checks and ensure they pass
		if err := <-results; err != nil {
			return i, err
		}
	}

	return 0, nil
}

// InsertHeaderChain inserts the given headers.
//
// The validity of the headers is NOT CHECKED by this method, i.e. they need to be
// validated by ValidateHeaderChain before calling InsertHeaderChain.
//
// This insert is all-or-nothing. If this returns an error, no headers were written,
// otherwise they were all processed successfully.
//
// The returned 'write status' says if the inserted headers are part of the canonical chain
// or a side chain.
func (hc *HeaderChain) InsertHeaderChain(chain []*types.Header, start time.Time) (WriteStatus, error) {
	if hc.procInterrupt() {
		return 0, errors.New("aborted")
	}
	res, err := hc.writeHeaders(chain)

	// Report some public statistics so the user has a clue what's going on
	context := []interface{}{
		"count", res.imported,
		"elapsed", common.PrettyDuration(time.Since(start)),
	}
	if err != nil {
		context = append(context, "err", err)
	}
	if last := res.lastHeader; last != nil {
		context = append(context, "number", last.Number, "hash", res.lastHash)
		if timestamp := time.Unix(int64(last.Time), 0); time.Since(timestamp) > time.Minute {
			context = append(context, []interface{}{"age", common.PrettyAge(timestamp)}...)
		}
	}
	if res.ignored > 0 {
		context = append(context, []interface{}{"ignored", res.ignored}...)
	}
	log.Info("Imported new block headers", context...)
	return res.status, err
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
		next := header.ParentHash[types.QuaiNetworkContext]
		if header = hc.GetHeader(next, header.Number[types.QuaiNetworkContext].Uint64()-1); header == nil {
			break
		}
		chain = append(chain, next)
		if header.Number[types.QuaiNetworkContext].Sign() == 0 {
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
			return header.ParentHash[types.QuaiNetworkContext], number - 1
		}
		return common.Hash{}, 0
	}
	for ancestor != 0 {
		if rawdb.ReadCanonicalHash(hc.chainDb, number) == hash {
			ancestorHash := rawdb.ReadCanonicalHash(hc.chainDb, number-ancestor)
			if rawdb.ReadCanonicalHash(hc.chainDb, number) == hash {
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
		hash = header.ParentHash[types.QuaiNetworkContext]
		number--
	}
	return hash, number
}

// GetTd retrieves a block's total difficulty in the canonical chain from the
// database by hash and number, caching it if found.
func (hc *HeaderChain) GetTd(hash common.Hash, number uint64) []*big.Int {
	// Short circuit if the td's already in the cache, retrieve otherwise
	if cached, ok := hc.tdCache.Get(hash); ok {
		return cached.([]*big.Int)
	}
	td := rawdb.ReadTd(hc.chainDb, hash, number)
	if td == nil {
		return make([]*big.Int, 3)
	}
	// Cache the found body for next time and return
	hc.tdCache.Add(hash, td)
	return td
}

// GetTdByHash retrieves a block's total difficulty in the canonical chain from the
// database by hash, caching it if found.
func (hc *HeaderChain) GetTdByHash(hash common.Hash) []*big.Int {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return make([]*big.Int, 3)
	}
	return hc.GetTd(hash, *number)
}

// GetHeader retrieves a block header from the database by hash and number,
// caching it if found.
func (hc *HeaderChain) GetHeader(hash common.Hash, number uint64) *types.Header {
	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return header.(*types.Header)
	}
	header := rawdb.ReadHeader(hc.chainDb, hash, number)
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
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.GetHeader(hash, *number)
}

// HasHeader checks if a block header is present in the database or not.
// In theory, if header is present in the database, all relative components
// like td and hash->number should be present too.
func (hc *HeaderChain) HasHeader(hash common.Hash, number uint64) bool {
	if hc.numberCache.Contains(hash) || hc.headerCache.Contains(hash) {
		return true
	}
	return rawdb.HasHeader(hc.chainDb, hash, number)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (hc *HeaderChain) GetHeaderByNumber(number uint64) *types.Header {
	hash := rawdb.ReadCanonicalHash(hc.chainDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetHeader(hash, number)
}

// GetExternalBlocks is not applicable in the header chain since the BlockChain contains
// the external blocks cache.
func (hc *HeaderChain) GetExternalBlocks(header *types.Header) ([]*types.ExternalBlock, error) {
	return nil, nil
}

// GetExternalBlocks is not applicable in the header chain since the BlockChain contains
// the external blocks cache.
func (hc *HeaderChain) GetLinkExternalBlocks(header *types.Header) ([]*types.ExternalBlock, error) {
	return nil, nil
}

// GetExternalBlock is not applicable in the header chain since the BlockChain contains
// the external blocks cache.
func (hc *HeaderChain) GetExternalBlock(hash common.Hash, number uint64, location []byte, context uint64) (*types.ExternalBlock, error) {
	return nil, nil
}

// QueueAndRetrieveExtBlocks is not applicable in the header chain since the BlockChain contains
// the external blocks cache.
func (hc *HeaderChain) QueueAndRetrieveExtBlocks(blocks []*types.ExternalBlock, header *types.Header) []*types.ExternalBlock {
	return nil
}

func (hc *HeaderChain) GetCanonicalHash(number uint64) common.Hash {
	return rawdb.ReadCanonicalHash(hc.chainDb, number)
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (hc *HeaderChain) CurrentHeader() *types.Header {
	return hc.currentHeader.Load().(*types.Header)
}

// SetCurrentHeader sets the in-memory head header marker of the canonical chan
// as the given header.
func (hc *HeaderChain) SetCurrentHeader(head *types.Header) {
	hc.currentHeader.Store(head)
	hc.currentHeaderHash = head.Hash()
	headHeaderGauge.Update(head.Number[types.QuaiNetworkContext].Int64())
}

type (
	// UpdateHeadBlocksCallback is a callback function that is called by SetHead
	// before head header is updated. The method will return the actual block it
	// updated the head to (missing state) and a flag if setHead should continue
	// rewinding till that forcefully (exceeded ancient limits)
	UpdateHeadBlocksCallback func(ethdb.KeyValueWriter, *types.Header) (uint64, bool)

	// DeleteBlockContentCallback is a callback function that is called by SetHead
	// before each header is deleted.
	DeleteBlockContentCallback func(ethdb.KeyValueWriter, common.Hash, uint64)
)

// SetHead rewinds the local chain to a new head. Everything above the new head
// will be deleted and the new one set.
func (hc *HeaderChain) SetHead(head uint64, updateFn UpdateHeadBlocksCallback, delFn DeleteBlockContentCallback) {
	var (
		parentHash common.Hash
		batch      = hc.chainDb.NewBatch()
		origin     = true
	)
	for hdr := hc.CurrentHeader(); hdr != nil && hdr.Number[types.QuaiNetworkContext].Uint64() > head; hdr = hc.CurrentHeader() {
		num := hdr.Number[types.QuaiNetworkContext].Uint64()

		// Rewind block chain to new head.
		parent := hc.GetHeader(hdr.ParentHash[types.QuaiNetworkContext], num-1)
		if parent == nil {
			parent = hc.genesisHeader
		}
		parentHash = parent.Hash()

		// Notably, since geth has the possibility for setting the head to a low
		// height which is even lower than ancient head.
		// In order to ensure that the head is always no higher than the data in
		// the database (ancient store or active store), we need to update head
		// first then remove the relative data from the database.
		//
		// Update head first(head fast block, head full block) before deleting the data.
		markerBatch := hc.chainDb.NewBatch()
		if updateFn != nil {
			newHead, force := updateFn(markerBatch, parent)
			if force && newHead < head {
				log.Warn("Force rewinding till ancient limit", "head", newHead)
				head = newHead
			}
		}
		// Update head header then.
		rawdb.WriteHeadHeaderHash(markerBatch, parentHash)
		if err := markerBatch.Write(); err != nil {
			log.Crit("Failed to update chain markers", "error", err)
		}
		hc.currentHeader.Store(parent)
		hc.currentHeaderHash = parentHash
		headHeaderGauge.Update(parent.Number[types.QuaiNetworkContext].Int64())

		// If this is the first iteration, wipe any leftover data upwards too so
		// we don't end up with dangling daps in the database
		var nums []uint64
		if origin {
			for n := num + 1; len(rawdb.ReadAllHashes(hc.chainDb, n)) > 0; n++ {
				nums = append([]uint64{n}, nums...) // suboptimal, but we don't really expect this path
			}
			origin = false
		}
		nums = append(nums, num)

		// Remove the related data from the database on all sidechains
		for _, num := range nums {
			// Gather all the side fork hashes
			hashes := rawdb.ReadAllHashes(hc.chainDb, num)
			if len(hashes) == 0 {
				// No hashes in the database whatsoever, probably frozen already
				hashes = append(hashes, hdr.Hash())
			}
			for _, hash := range hashes {
				if delFn != nil {
					delFn(batch, hash, num)
				}
				rawdb.DeleteHeader(batch, hash, num)
				rawdb.DeleteTd(batch, hash, num)
			}
			rawdb.DeleteCanonicalHash(batch, num)
		}
	}
	// Flush all accumulated deletions.
	if err := batch.Write(); err != nil {
		log.Crit("Failed to rewind block", "error", err)
	}
	// Clear out any stale content from the caches
	hc.headerCache.Purge()
	hc.tdCache.Purge()
	hc.numberCache.Purge()
}

// HLCR does hierarchical comparison of two difficulty tuples and returns true if second tuple is greater than the first
func (hc *HeaderChain) HLCR(localDifficulties []*big.Int, externDifficulties []*big.Int) bool {
	if localDifficulties[0].Cmp(externDifficulties[0]) < 0 {
		return true
	} else if localDifficulties[1].Cmp(externDifficulties[1]) < 0 {
		return true
	} else if localDifficulties[2].Cmp(externDifficulties[2]) < 0 {
		return true
	} else {
		return false
	}
}

// CalcTd calculates the TD of the given header using PCRC and CalcHLCRNetDifficulty.
func (hc *HeaderChain) CalcTd(header *types.Header) ([]*big.Int, error) {
	// Check PCRC for the external block and return the terminal hash and net difficulties
	externTerminalHash, err := hc.PCRC(header)
	if err != nil {
		return nil, err
	}

	// Use HLCR to compute net total difficulty
	externNd, err := hc.CalcHLCRNetDifficulty(externTerminalHash, header)
	if err != nil {
		return nil, err
	}

	externTerminalHeader := hc.GetHeaderByHash(externTerminalHash)
	externTd, err := hc.NdToTd(externTerminalHeader, externNd)
	if err != nil {
		return nil, err
	}

	return externTd, nil
}

// NdToTd returns the total difficulty for a header given the net difficulty
func (hc *HeaderChain) NdToTd(header *types.Header, nD []*big.Int) ([]*big.Int, error) {
	if header == nil {
		return nil, errors.New("header provided to ndtotd is nil")
	}
	startingHeader := header
	k := big.NewInt(0)
	k.Sub(k, header.Difficulty[0])

	var prevExternTerminus *types.Header
	var err error
	for {
		if hc.GetBlockNumber(header.Hash()) != nil {
			break
		}
		prevExternTerminus, err = hc.Engine().PreviousCoincidentOnPath(hc, header, header.Location, params.PRIME, params.PRIME)
		if err != nil {
			return nil, err
		}
		header = prevExternTerminus
	}
	header = startingHeader
	for prevExternTerminus.Hash() != header.Hash() {
		k.Add(k, header.Difficulty[0])
		if header.Hash() == hc.Config().GenesisHashes[0] {
			k.Add(k, header.Difficulty[0])
			break
		}
		// Get previous header on local chain by hash
		prevHeader := hc.GetHeaderByHash(header.ParentHash[params.PRIME])
		if prevHeader == nil {
			// Get previous header on external chain by hash
			prevExtBlock, err := hc.GetExternalBlock(header.ParentHash[params.PRIME], header.Number[params.PRIME].Uint64()-1, header.Location, uint64(params.PRIME))
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

	k.Add(k, hc.GetTdByHash(header.Hash())[params.PRIME])

	// adding the common total difficulty to the net
	nD[0].Add(nD[0], k)
	nD[1].Add(nD[1], k)
	nD[2].Add(nD[2], k)

	return nD, nil
}

// The purpose of the Previous Coincident Reference Check (PCRC) is to establish
// that we have linked untwisted chains prior to checking HLCR & applying external state transfers.
// NOTE: note that it only guarantees linked & untwisted back to the prime terminus, assuming the
// prime termini match. To check deeper than that, you need to iteratively apply PCRC to get that guarantee.
func (hc *HeaderChain) PCRC(header *types.Header) (common.Hash, error) {

	if header.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
		return hc.config.GenesisHashes[0], nil
	}

	slice := header.Location

	// Region twist check
	// RTZ -- Region coincident along zone path
	// RTR -- Region coincident along region path
	RTZ, err := hc.Engine().PreviousCoincidentOnPath(hc, header, slice, params.REGION, params.ZONE)
	if err != nil {
		return common.Hash{}, err
	}

	RTR, err := hc.Engine().PreviousCoincidentOnPath(hc, header, slice, params.REGION, params.REGION)
	if err != nil {
		return common.Hash{}, err
	}

	if RTZ.Hash() != RTR.Hash() {
		return common.Hash{}, errors.New("there exists a region twist")
	}

	// Prime twist check
	// PTZ -- Prime coincident along zone path
	// PTR -- Prime coincident along region path
	// PTP -- Prime coincident along prime path
	PTZ, err := hc.Engine().PreviousCoincidentOnPath(hc, header, slice, params.PRIME, params.ZONE)
	if err != nil {
		return common.Hash{}, err
	}

	PTR, err := hc.Engine().PreviousCoincidentOnPath(hc, header, slice, params.PRIME, params.REGION)
	if err != nil {
		return common.Hash{}, err
	}

	PTP, err := hc.Engine().PreviousCoincidentOnPath(hc, header, slice, params.PRIME, params.PRIME)
	if err != nil {
		return common.Hash{}, err
	}

	if PTZ.Hash() != PTR.Hash() || PTR.Hash() != PTP.Hash() || PTP.Hash() != PTZ.Hash() {
		return common.Hash{}, errors.New("there exists a prime twist")
	}

	return PTP.Hash(), nil
}

// calcHLCRNetDifficulty calculates the net difficulty from previous prime.
// The netDifficulties parameter inputs the nets of instantaneous difficulties from the terminus block.
// By correctly summing the net difficulties we have obtained the proper array to be compared in HLCR.
func (hc *HeaderChain) CalcHLCRNetDifficulty(terminalHash common.Hash, header *types.Header) ([]*big.Int, error) {

	if (terminalHash == common.Hash{}) {
		return nil, errors.New("one or many of the  terminal hashes were nil")
	}

	primeNd := big.NewInt(0)
	regionNd := big.NewInt(0)
	zoneNd := big.NewInt(0)

	for {
		order, err := hc.engine.GetDifficultyOrder(header)
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

		if header.Hash() == terminalHash {
			break
		}

		// Get previous header on local chain by hash
		prevHeader := hc.GetHeaderByHash(header.ParentHash[order])
		if prevHeader == nil {
			// Get previous header on external chain by hash
			prevExtBlock, err := hc.GetExternalBlock(header.ParentHash[order], header.Number[order].Uint64()-1, header.Location, uint64(order))
			if err != nil {
				return nil, err
			}
			// Increment previous header
			prevHeader = prevExtBlock.Header()
		}
		header = prevHeader
	}

	return []*big.Int{primeNd, regionNd, zoneNd}, nil
}

// SetGenesis sets a new genesis block header for the chain
func (hc *HeaderChain) SetGenesis(head *types.Header) {
	hc.genesisHeader = head
}

// Config retrieves the header chain's chain configuration.
func (hc *HeaderChain) Config() *params.ChainConfig { return hc.config }

// Engine retrieves the header chain's consensus engine.
func (hc *HeaderChain) Engine() consensus.Engine { return hc.engine }

// GetBlock implements consensus.ChainReader, and returns nil for every input as
// a header chain does not have blocks available for retrieval.
func (hc *HeaderChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	return nil
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetGasUsedInChain(block *types.Block, length int) int64 {
	return int64(0)
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	return nil
}

// CheckContext checks to make sure the range of a context or order is valid
func (hc *HeaderChain) CheckContext(context int) error {
	if context < 0 || context > len(params.FullerOntology) {
		return errors.New("the provided path is outside the allowable range")
	}
	return nil
}

// CheckLocationRange checks to make sure the range of r and z are valid
func (hc *HeaderChain) CheckLocationRange(location []byte) error {
	if int(location[0]) < 1 || int(location[0]) > params.FullerOntology[0] {
		return errors.New("the provided location is outside the allowable region range")
	}
	if int(location[1]) < 1 || int(location[1]) > params.FullerOntology[1] {
		return errors.New("the provided location is outside the allowable zone range")
	}
	return nil
}
