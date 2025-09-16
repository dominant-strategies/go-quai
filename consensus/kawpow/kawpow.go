// ethash: C/C++ implementation of Ethash, the Ethereum Proof of Work algorithm.
// Copyright 2018-2019 Pawel Bylica.
// Licensed under the Apache License, Version 2.0.

package kawpow

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	mmap "github.com/edsrzf/mmap-go"

	"github.com/hashicorp/golang-lru/simplelru"
	goLRU "github.com/hashicorp/golang-lru/v2"
)

const (
	c_dagItemsInCache = 10000
)

var (
	// sharedKawpow is a full instance that can be shared between multiple users.
	sharedKawpow *Kawpow
	// algorithmRevision is the data structure version used for file naming.
	algorithmRevision = 1
	// dumpMagic is a dataset dump header to sanity check a data dump.
	dumpMagic = []uint32{0xbaddcafe, 0xfee1dead}
)

var ErrInvalidDumpMagic = errors.New("invalid dump magic")

// Kawpow-specific constants
var ravencoinKawpow [15]uint32 = [15]uint32{
	0x00000072, //R
	0x00000041, //A
	0x00000056, //V
	0x00000045, //E
	0x0000004E, //N
	0x00000043, //C
	0x0000004F, //O
	0x00000049, //I
	0x0000004E, //N
	0x0000004B, //K
	0x00000041, //A
	0x00000057, //W
	0x00000050, //P
	0x0000004F, //O
	0x00000057, //W
}

// isLittleEndian returns whether the local system is running in little or big
// endian byte order.
func isLittleEndian() bool {
	n := uint32(0x01020304)
	return *(*byte)(unsafe.Pointer(&n)) == 0x04
}

// memoryMap tries to memory map a file of uint32s for read only access.
func memoryMap(path string, lock bool) (*os.File, mmap.MMap, []uint32, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, 0644)
	if err != nil {
		return nil, nil, nil, err
	}
	mem, buffer, err := memoryMapFile(file, false)
	if err != nil {
		file.Close()
		return nil, nil, nil, err
	}
	for i, magic := range dumpMagic {
		if buffer[i] != magic {
			mem.Unmap()
			file.Close()
			return nil, nil, nil, ErrInvalidDumpMagic
		}
	}
	if lock {
		if err := mem.Lock(); err != nil {
			mem.Unmap()
			file.Close()
			return nil, nil, nil, err
		}
	}
	return file, mem, buffer[len(dumpMagic):], err
}

// memoryMapFile tries to memory map an already opened file descriptor.
func memoryMapFile(file *os.File, write bool) (mmap.MMap, []uint32, error) {
	// Try to memory map the file
	flag := mmap.RDONLY
	if write {
		flag = mmap.RDWR
	}
	mem, err := mmap.Map(file, flag, 0)
	if err != nil {
		return nil, nil, err
	}
	// Yay, we managed to memory map the file, here be dragons
	header := *(*reflect.SliceHeader)(unsafe.Pointer(&mem))
	header.Len /= 4
	header.Cap /= 4

	return mem, *(*[]uint32)(unsafe.Pointer(&header)), nil
}

// memoryMapAndGenerate tries to memory map a temporary file of uint32s for write
// access, fill it with the data from a generator and then move it into the final
// path requested.
func memoryMapAndGenerate(path string, size uint64, lock bool, generator func(buffer []uint32)) (*os.File, mmap.MMap, []uint32, error) {
	// Ensure the data folder exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, nil, nil, err
	}
	// Create a huge temporary empty file to fill with data
	temp := path + "." + strconv.Itoa(rand.Int())

	dump, err := os.Create(temp)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = dump.Truncate(int64(len(dumpMagic))*4 + int64(size)); err != nil {
		return nil, nil, nil, err
	}
	// Memory map the file for writing and fill it with the generator
	mem, buffer, err := memoryMapFile(dump, true)
	if err != nil {
		dump.Close()
		return nil, nil, nil, err
	}
	copy(buffer, dumpMagic)

	data := buffer[len(dumpMagic):]
	generator(data)

	if err := mem.Unmap(); err != nil {
		return nil, nil, nil, err
	}
	if err := dump.Close(); err != nil {
		return nil, nil, nil, err
	}
	if err := os.Rename(temp, path); err != nil {
		return nil, nil, nil, err
	}
	return memoryMap(path, lock)
}

type mixHashWorkHash struct {
	mixHash  []byte
	workHash []byte
}

// Kawpow is a proof-of-work consensus engine using the kawpow hash algorithm
type Kawpow struct {
	config params.PowConfig

	caches    *lru // In memory caches to avoid regenerating too often
	hashCache *goLRU.Cache[common.Hash, mixHashWorkHash]

	// Mining related fields
	rand    *rand.Rand    // Properly seeded random source for nonces
	threads int           // Number of threads to mine on if mining
	update  chan struct{} // Notification channel to update mining parameters

	// The fields below are hooks for testing
	shared    *Kawpow       // Shared PoW verifier to avoid cache regeneration
	fakeFail  uint64        // Block number which fails PoW check even in fake mode
	fakeDelay time.Duration // Time delay to sleep for before returning from verify

	lock      sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
	closeOnce sync.Once  // Ensures exit channel will not be closed twice.

	logger *log.Logger
}

// New creates a full sized kawpow PoW scheme and starts a background thread for
// remote mining, also optionally notifying a batch of remote services of new work
// packages.
func New(config params.PowConfig, notify []string, noverify bool, logger *log.Logger) *Kawpow {
	logger.WithField("requested", config.CachesInMem).Warn("Invalid kawpow caches in memory, defaulting to 3")
	config.CachesInMem = 3
	if config.CacheDir != "" && config.CachesOnDisk > 0 {
		logger.WithFields(log.Fields{
			"dir":   config.CacheDir,
			"count": config.CachesOnDisk,
		}).Info("Disk storage enabled for kawpow caches")
	}

	hashCache, err := goLRU.New[common.Hash, mixHashWorkHash](c_dagItemsInCache)
	if err != nil {
		logger.WithField("err", err).Fatal("Failed to create kawpow hash cache")
	}

	kawpow := &Kawpow{
		config:    config,
		caches:    newlru("cache", config.CachesInMem, newCache, logger),
		hashCache: hashCache,
		update:    make(chan struct{}),
		logger:    logger,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
		threads:   config.NumThreads,
	}
	if config.PowMode == params.ModeShared {
		kawpow.shared = sharedKawpow
	}
	return kawpow
}

// NewTester creates a small sized kawpow PoW scheme useful only for testing
// purposes.
func NewTester(notify []string, noverify bool) *Kawpow {
	return New(params.PowConfig{PowMode: params.ModeTest}, notify, noverify, log.NewLogger("test-kawpow.log", "info", 500))
}

// NewFaker creates a kawpow consensus engine with a fake PoW scheme that accepts
// all blocks' seal as valid, though they still have to conform to the Quai
// consensus rules.
func NewFaker() *Kawpow {
	return &Kawpow{
		config: params.PowConfig{
			PowMode: params.ModeFake,
		},
	}
}

// NewFakeFailer creates a kawpow consensus engine with a fake PoW scheme that
// accepts all blocks as valid apart from the single one specified, though they
// still have to conform to the Quai consensus rules.
func NewFakeFailer(fail uint64) *Kawpow {
	return &Kawpow{
		config: params.PowConfig{
			PowMode: params.ModeFake,
		},
		fakeFail: fail,
	}
}

// NewFakeDelayer creates a kawpow consensus engine with a fake PoW scheme that
// accepts all blocks as valid, but delays verifications by some time, though
// they still have to conform to the Quai consensus rules.
func NewFakeDelayer(delay time.Duration) *Kawpow {
	return &Kawpow{
		config: params.PowConfig{
			PowMode: params.ModeFake,
		},
		fakeDelay: delay,
	}
}

// NewFullFaker creates an kawpow consensus engine with a full fake scheme that
// accepts all blocks as valid, without checking any consensus rules whatsoever.
func NewFullFaker() *Kawpow {
	return &Kawpow{
		config: params.PowConfig{
			PowMode: params.ModeFullFake,
		},
	}
}

// NewShared creates a full sized kawpow PoW shared between all requesters running
// in the same process.
func NewShared() *Kawpow {
	return &Kawpow{shared: sharedKawpow}
}

// lru tracks caches or datasets by their last use time, keeping at most N of them.
type lru struct {
	what string
	new  func(epoch uint64) interface{}
	mu   sync.Mutex
	// Items are kept in a LRU cache, but there is a special case:
	// We always keep an item for (highest seen epoch) + 1 as the 'future item'.
	cache      *simplelru.LRU
	future     uint64
	futureItem interface{}

	logger *log.Logger
}

// newlru create a new least-recently-used cache for either the verification caches
// or the mining datasets.
func newlru(what string, maxItems int, new func(epoch uint64) interface{}, logger *log.Logger) *lru {
	if maxItems <= 0 {
		maxItems = 1
	}
	cache, _ := simplelru.NewLRU(maxItems, func(key, value interface{}) {
		logger.WithField("epoch", key).Trace("Evicted kawpow " + what)
	})
	return &lru{what: what, new: new, cache: cache, logger: logger}
}

// get retrieves or creates an item for the given epoch. The first return value is always
// non-nil. The second return value is non-nil if lru thinks that an item will be useful in
// the near future.
func (lru *lru) get(epoch uint64) (item, future interface{}) {
	lru.mu.Lock()
	defer lru.mu.Unlock()

	// Get or create the item for the requested epoch.
	item, ok := lru.cache.Get(epoch)
	if !ok {
		if lru.future > 0 && lru.future == epoch {
			item = lru.futureItem
		} else {
			lru.logger.WithField("epoch", epoch).Trace("Requiring new kawpow " + lru.what)
			item = lru.new(epoch)
		}
		lru.cache.Add(epoch, item)
	}
	// Update the 'future item' if epoch is larger than previously seen.
	if epoch < maxCachedEpoch-1 && lru.future < epoch+1 {
		lru.logger.WithField("epoch", epoch+1).Trace("Requiring new future kawpow " + lru.what)
		future = lru.new(epoch + 1)
		lru.future = epoch + 1
		lru.futureItem = future
	}
	return item, future
}

// cache wraps an ethash cache with some metadata to allow easier concurrent use.
type cache struct {
	epoch uint64    // Epoch for which this cache is relevant
	dump  *os.File  // File descriptor of the memory mapped cache
	mmap  mmap.MMap // Memory map itself to unmap before releasing
	cache []uint32  // The actual cache data content (may be memory mapped)
	cDag  []uint32  // The cDag used by kawpow. May be nil
	once  sync.Once // Ensures the cache is generated only once
}

// newCache creates a new kawpow verification cache and returns it as a plain Go
// interface to be usable in an LRU cache.
func newCache(epoch uint64) interface{} {
	return &cache{epoch: epoch}
}

// generate ensures that the cache content is generated before use.
func (c *cache) generate(dir string, limit int, lock bool, test bool, logger *log.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	c.once.Do(func() {
		size := cacheSize(c.epoch*C_epochLength + 1)
		seed := seedHash(c.epoch*C_epochLength + 1)
		if test {
			size = 1024
		}
		// If we don't store anything on disk, generate and return.
		if dir == "" {
			c.cache = make([]uint32, size/4)
			generateCache(c.cache, c.epoch, seed, logger)
			c.cDag = make([]uint32, kawpowCacheWords)
			generateCDag(c.cDag, c.cache, c.epoch, logger)
			return
		}
		// Disk storage is needed, this will get fancy
		var endian string
		if !isLittleEndian() {
			endian = ".be"
		}
		path := filepath.Join(dir, fmt.Sprintf("cache-R%d-%x%s", algorithmRevision, seed[:8], endian))

		// We're about to mmap the file, ensure that the mapping is cleaned up when the
		// cache becomes unused.
		runtime.SetFinalizer(c, (*cache).finalizer)

		// Try to load the file from disk and memory map it
		var err error
		c.dump, c.mmap, c.cache, err = memoryMap(path, lock)
		if err == nil {
			logger.Debug("Loaded old kawpow cache from disk")
			c.cDag = make([]uint32, kawpowCacheWords)
			generateCDag(c.cDag, c.cache, c.epoch, logger)
			return
		}
		logger.WithField("err", err).Debug("Failed to load old kawpow cache from disk")

		// No previous cache available, create a new cache file to fill
		c.dump, c.mmap, c.cache, err = memoryMapAndGenerate(path, size, lock, func(buffer []uint32) { generateCache(buffer, c.epoch, seed, logger) })
		if err != nil {
			logger.WithField("err", err).Error("Failed to generate mapped kawpow cache")

			c.cache = make([]uint32, size/4)
			generateCache(c.cache, c.epoch, seed, logger)
		}
		c.cDag = make([]uint32, kawpowCacheWords)
		generateCDag(c.cDag, c.cache, c.epoch, logger)
		// Iterate over all previous instances and delete old ones
		for ep := int(c.epoch) - limit; ep >= 0; ep-- {
			seed := seedHash(uint64(ep)*C_epochLength + 1)
			path := filepath.Join(dir, fmt.Sprintf("cache-R%d-%x%s", algorithmRevision, seed[:8], endian))
			os.Remove(path)
		}
	})
}

// finalizer unmaps the memory and closes the file.
func (c *cache) finalizer() {
	if c.mmap != nil {
		c.mmap.Unmap()
		c.dump.Close()
		c.mmap, c.dump = nil, nil
	}
}

// cache tries to retrieve a verification cache for the specified block number
// by first checking against a list of in-memory caches, then against caches
// stored on disk, and finally generating one if none can be found.
func (kawpow *Kawpow) cache(block uint64) *cache {
	epoch := block / C_epochLength
	currentI, futureI := kawpow.caches.get(epoch)
	current := currentI.(*cache)

	// Wait for generation finish.
	current.generate(kawpow.config.CacheDir, kawpow.config.CachesOnDisk, kawpow.config.CachesLockMmap, kawpow.config.PowMode == params.ModeTest, kawpow.logger)

	// If we need a new future cache, now's a good time to regenerate it.
	if futureI != nil {
		future := futureI.(*cache)
		go future.generate(kawpow.config.CacheDir, kawpow.config.CachesOnDisk, kawpow.config.CachesLockMmap, kawpow.config.PowMode == params.ModeTest, kawpow.logger)
	}
	return current
}

// Threads returns the number of mining threads currently enabled. This doesn't
// necessarily mean that mining is running!
func (kawpow *Kawpow) Threads() int {
	kawpow.lock.Lock()
	defer kawpow.lock.Unlock()

	return kawpow.threads
}

// SetThreads updates the number of mining threads currently enabled. Calling
// this method does not start mining, only sets the thread count. If zero is
// specified, the miner will use all cores of the machine. Setting a thread
// count below zero is allowed and will cause the miner to idle, without any
// work being done.
func (kawpow *Kawpow) SetThreads(threads int) {
	kawpow.lock.Lock()
	defer kawpow.lock.Unlock()

	if kawpow.shared != nil {
		// If we're running a shared PoW, set the thread count on that instead
		kawpow.shared.SetThreads(threads)
	} else {
		// Update the threads and ping any running seal to pull in any changes
		kawpow.threads = threads
		select {
		case kawpow.update <- struct{}{}:
		default:
		}
	}
}

func (kawpow *Kawpow) ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error) {
	mixHash, powHash := kawpow.ComputePowLight(header)
	// For KAWPOW, get the mix hash from the Ravencoin header in AuxPow
	auxPow := header.AuxPow()
	if auxPow == nil {
		return common.Hash{}, fmt.Errorf("AuxPow is nil for KAWPOW")
	}

	ravencoinHeader := auxPow.Header()

	// Verify the calculated mix against the one provided in the Ravencoin header.
	// The kawpowLight function returns mixHash in little-endian format (KAWPOW algorithm spec).
	// The stratum proxy reverses the mixHash bytes before sending (stratum-converter.py:394).
	// DecodeRavencoinHeader() reverses the bytes back when reading (ravencoin.go:241-244),
	// so both the calculated and header mixHash are now in the same format (little-endian).
	headerMix := ravencoinHeader.MixHash().Bytes()
	if !bytes.Equal(headerMix, mixHash.Bytes()) {
		kawpow.logger.WithFields(log.Fields{
			"receivedMixHash":   fmt.Sprintf("%x", headerMix),
			"calculatedMixHash": fmt.Sprintf("%x", mixHash.Bytes()),
		}).Error("MixHash mismatch in ComputePowHash")
		return common.Hash{}, consensus.ErrInvalidMixHash
	}
	return powHash, nil
}

// VerifyKawpowShare validates a Kawpow share for pool mining.
// This function is used by mining pools to validate shares without requiring a full WorkObject.
//
// Parameters:
// - headerHash: The Kawpow header hash (seal hash) from RavencoinBlockHeader.GetKAWPOWHeaderHash()
// - nonce: The 64-bit nonce value
// - mixHash: The mix hash submitted by the miner
// - blockNumber: The block height for epoch calculation
//
// Returns:
// - calculatedMixHash: The mix hash calculated by the Kawpow algorithm
// - powHash: The proof-of-work hash to compare against the target
// - error: Any error that occurred during calculation
func (kawpow *Kawpow) VerifyKawpowShare(headerHash common.Hash, nonce uint64, blockNumber uint64) (common.Hash, common.Hash, error) {
	// Get the cache for this block number
	ethashCache := kawpow.cache(blockNumber)

	// Generate cDag if not already present
	if ethashCache.cDag == nil {
		cDag := make([]uint32, kawpowCacheWords)
		generateCDag(cDag, ethashCache.cache, blockNumber/C_epochLength, kawpow.logger)
		ethashCache.cDag = cDag
	}

	// Get dataset size for this block
	size := datasetSize(blockNumber)

	// Compute Kawpow hash using kawpowLight
	// The headerHash should be reversed to little-endian for the algorithm
	digest, result := kawpowLight(size, ethashCache.cache, headerHash.Bytes(), nonce, blockNumber, ethashCache.cDag)

	// Convert results to common.Hash
	// MixHash should be reversed back to little-endian
	calculatedMixHash := common.Hash(digest).Reverse()
	powHash := common.BytesToHash(result)

	return calculatedMixHash, powHash, nil
}

// ComputePowLight computes the kawpow hash and returns mixHash and powHash
func (kawpow *Kawpow) ComputePowLight(header *types.WorkObjectHeader) (mixHash, powHash common.Hash) {
	// For quai blocks to rely on pow done on the raven coin donor header
	if header.AuxPow() == nil {
		kawpow.logger.Error("AuxPow is nil in ComputePowLight")
		return common.Hash{}, common.Hash{}
	}

	ravencoinHeader := header.AuxPow().Header()

	// For KAWPOW, the nonce is stored directly in the Ravencoin header
	nonce64 := ravencoinHeader.Nonce64()

	// Get the KAWPOW header hash (the input to the KAWPOW algorithm)
	// NOTE: SealHash() returns the hash in BIG-ENDIAN format (SHA256 natural output)
	// This will be reversed to little-endian before passing to kawpowLight
	kawpowHeaderHash := ravencoinHeader.SealHash()
	blockNumber := uint64(ravencoinHeader.Height())

	// Create a unique cache key using the RVN-compatible header hash + nonce so
	// results are cached consistently with the actual kernel input.
	nonceBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(nonceBytes, nonce64)
	cacheKey := crypto.Keccak256Hash(kawpowHeaderHash.Bytes(), nonceBytes)

	// Check cache with the unique key
	hashes, ok := kawpow.hashCache.Peek(cacheKey)
	if ok {
		return common.Hash(hashes.mixHash), common.Hash(hashes.workHash)
	}

	// Get cache for this block (same as sealer)
	ethashCache := kawpow.cache(blockNumber)
	if ethashCache.cDag == nil {
		cDag := make([]uint32, kawpowCacheWords)
		generateCDag(cDag, ethashCache.cache, blockNumber/C_epochLength, kawpow.logger)
		ethashCache.cDag = cDag
	}

	// Use the same kawpowLight function as the sealer, but feed the
	// RVN-compatible (byte-reversed) header hash bytes, since the
	// implementation expects the hash as little endian, it needs to be
	size := datasetSize(blockNumber)
	digest, result := kawpowLight(size, ethashCache.cache, kawpowHeaderHash.Reverse().Bytes(), nonce64, blockNumber, ethashCache.cDag)
	// MixHash stored in the header should be little endian, so reverse it back to little endian
	mixHash = common.Hash(digest).Reverse() // reverse to little-endian
	powHash = common.BytesToHash(result)

	// Cache the result with the unique key
	kawpow.hashCache.Add(cacheKey, mixHashWorkHash{mixHash: mixHash.Bytes(), workHash: powHash.Bytes()})

	return mixHash, powHash
}
