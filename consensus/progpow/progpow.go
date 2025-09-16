package progpow

import (
	"bytes"
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
	// sharedProgpow is a full instance that can be shared between multiple users.
	sharedProgpow *Progpow
	// algorithmRevision is the data structure version used for file naming.
	algorithmRevision = 1
	// dumpMagic is a dataset dump header to sanity check a data dump.
	dumpMagic = []uint32{0xbaddcafe, 0xfee1dead}
)

var ErrInvalidDumpMagic = errors.New("invalid dump magic")

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

// Mode defines the type and amount of PoW verification a progpow engine makes.
type Mode uint

const (
	ModeNormal Mode = iota
	ModeShared
	ModeTest
	ModeFake
	ModeFullFake
)

type mixHashWorkHash struct {
	mixHash  []byte
	workHash []byte
}

// Progpow is a proof-of-work consensus engine using the blake3 hash algorithm
type Progpow struct {
	config params.PowConfig

	caches    *lru // In memory caches to avoid regenerating too often
	hashCache *goLRU.Cache[common.Hash, mixHashWorkHash]

	// Mining related fields
	rand    *rand.Rand    // Properly seeded random source for nonces
	threads int           // Number of threads to mine on if mining
	update  chan struct{} // Notification channel to update mining parameters

	// The fields below are hooks for testing
	shared    *Progpow      // Shared PoW verifier to avoid cache regeneration
	fakeFail  uint64        // Block number which fails PoW check even in fake mode
	fakeDelay time.Duration // Time delay to sleep for before returning from verify

	lock      sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
	closeOnce sync.Once  // Ensures exit channel will not be closed twice.

	logger *log.Logger
}

// New creates a full sized progpow PoW scheme and starts a background thread for
// remote mining, also optionally notifying a batch of remote services of new work
// packages.
func New(config params.PowConfig, notify []string, noverify bool, logger *log.Logger) *Progpow {
	if config.CachesInMem <= 0 {
		logger.WithField("requested", config.CachesInMem).Warn("Invalid ethash caches in memory, defaulting to 1")
		config.CachesInMem = 3
	}
	if config.CacheDir != "" && config.CachesOnDisk > 0 {
		logger.WithFields(log.Fields{
			"dir":   config.CacheDir,
			"count": config.CachesOnDisk,
		}).Info("Disk storage enabled for ethash caches")
	}

	hashCache, err := goLRU.New[common.Hash, mixHashWorkHash](c_dagItemsInCache)
	if err != nil {
		logger.WithField("err", err).Fatal("Failed to create ethash hash cache")
	}

	progpow := &Progpow{
		config:    config,
		caches:    newlru("cache", config.CachesInMem, newCache, logger),
		hashCache: hashCache,
		update:    make(chan struct{}),
		logger:    logger,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
		threads:   config.NumThreads,
	}
	if config.PowMode == params.ModeShared {
		progpow.shared = sharedProgpow
	}
	return progpow
}

// NewTester creates a small sized progpow PoW scheme useful only for testing
// purposes.
func NewTester(notify []string, noverify bool) *Progpow {
	return New(params.PowConfig{PowMode: params.ModeTest}, notify, noverify, log.NewLogger("test-progpow.log", "info", 500))
}

// NewFaker creates a progpow consensus engine with a fake PoW scheme that accepts
// all blocks' seal as valid, though they still have to conform to the Quai
// consensus rules.
func NewFaker() *Progpow {
	return &Progpow{
		config: params.PowConfig{
			PowMode: params.ModeFake,
		},
	}
}

// NewFakeFailer creates a progpow consensus engine with a fake PoW scheme that
// accepts all blocks as valid apart from the single one specified, though they
// still have to conform to the Quai consensus rules.
func NewFakeFailer(fail uint64) *Progpow {
	return &Progpow{
		config: params.PowConfig{
			PowMode: params.ModeFake,
		},
		fakeFail: fail,
	}
}

// NewFakeDelayer creates a progpow consensus engine with a fake PoW scheme that
// accepts all blocks as valid, but delays verifications by some time, though
// they still have to conform to the Quai consensus rules.
func NewFakeDelayer(delay time.Duration) *Progpow {
	return &Progpow{
		config: params.PowConfig{
			PowMode: params.ModeFake,
		},
		fakeDelay: delay,
	}
}

// NewFullFaker creates an progpow consensus engine with a full fake scheme that
// accepts all blocks as valid, without checking any consensus rules whatsoever.
func NewFullFaker() *Progpow {
	return &Progpow{
		config: params.PowConfig{
			PowMode: params.ModeFullFake,
		},
	}
}

// NewShared creates a full sized progpow PoW shared between all requesters running
// in the same process.
func NewShared() *Progpow {
	return &Progpow{shared: sharedProgpow}
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
		logger.WithField("epoch", key).Trace("Evicted ethash " + what)
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
			lru.logger.WithField("epoch", epoch).Trace("Requiring new ethash " + lru.what)
			item = lru.new(epoch)
		}
		lru.cache.Add(epoch, item)
	}
	// Update the 'future item' if epoch is larger than previously seen.
	if epoch < maxCachedEpoch-1 && lru.future < epoch+1 {
		lru.logger.WithField("epoch", epoch+1).Trace("Requiring new future ethash " + lru.what)
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
	cDag  []uint32  // The cDag used by progpow. May be nil
	once  sync.Once // Ensures the cache is generated only once
}

// newCache creates a new ethash verification cache and returns it as a plain Go
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
			c.cDag = make([]uint32, progpowCacheWords)
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
			logger.Debug("Loaded old ethash cache from disk")
			c.cDag = make([]uint32, progpowCacheWords)
			generateCDag(c.cDag, c.cache, c.epoch, logger)
			return
		}
		logger.WithField("err", err).Debug("Failed to load old ethash cache from disk")

		// No previous cache available, create a new cache file to fill
		c.dump, c.mmap, c.cache, err = memoryMapAndGenerate(path, size, lock, func(buffer []uint32) { generateCache(buffer, c.epoch, seed, logger) })
		if err != nil {
			logger.WithField("err", err).Error("Failed to generate mapped ethash cache")

			c.cache = make([]uint32, size/4)
			generateCache(c.cache, c.epoch, seed, logger)
		}
		c.cDag = make([]uint32, progpowCacheWords)
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
func (progpow *Progpow) cache(block uint64) *cache {
	epoch := block / C_epochLength
	currentI, futureI := progpow.caches.get(epoch)
	current := currentI.(*cache)

	// Wait for generation finish.
	current.generate(progpow.config.CacheDir, progpow.config.CachesOnDisk, progpow.config.CachesLockMmap, progpow.config.PowMode == params.ModeTest, progpow.logger)

	// If we need a new future cache, now's a good time to regenerate it.
	if futureI != nil {
		future := futureI.(*cache)
		go future.generate(progpow.config.CacheDir, progpow.config.CachesOnDisk, progpow.config.CachesLockMmap, progpow.config.PowMode == params.ModeTest, progpow.logger)
	}
	return current
}

// Threads returns the number of mining threads currently enabled. This doesn't
// necessarily mean that mining is running!
func (progpow *Progpow) Threads() int {
	progpow.lock.Lock()
	defer progpow.lock.Unlock()

	return progpow.threads
}

// SetThreads updates the number of mining threads currently enabled. Calling
// this method does not start mining, only sets the thread count. If zero is
// specified, the miner will use all cores of the machine. Setting a thread
// count below zero is allowed and will cause the miner to idle, without any
// work being done.
func (progpow *Progpow) SetThreads(threads int) {
	progpow.lock.Lock()
	defer progpow.lock.Unlock()

	if progpow.shared != nil {
		// If we're running a shared PoW, set the thread count on that instead
		progpow.shared.SetThreads(threads)
	} else {
		// Update the threads and ping any running seal to pull in any changes
		progpow.threads = threads
		select {
		case progpow.update <- struct{}{}:
		default:
		}
	}
}

func (progpow *Progpow) ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error) {
	// Check progpow
	mixHash := header.PowDigest.Load()
	powHash := header.PowHash.Load()
	if powHash == nil || mixHash == nil {
		mixHash, powHash = progpow.ComputePowLight(header)
	}
	// Verify the calculated values against the ones provided in the header
	if !bytes.Equal(header.MixHash().Bytes(), mixHash.(common.Hash).Bytes()) {
		return common.Hash{}, consensus.ErrInvalidMixHash
	}
	return powHash.(common.Hash), nil
}

func (progpow *Progpow) ComputePowLight(header *types.WorkObjectHeader) (mixHash, powHash common.Hash) {
	hashes, ok := progpow.hashCache.Peek(header.Hash())
	if ok {
		return common.Hash(hashes.mixHash), common.Hash(hashes.workHash)
	}
	powLight := func(size uint64, cache []uint32, hash common.Hash, nonce uint64, blockNumber uint64) ([]byte, []byte) {
		ethashCache := progpow.cache(blockNumber)
		if ethashCache.cDag == nil {
			cDag := make([]uint32, progpowCacheWords)
			generateCDag(cDag, ethashCache.cache, blockNumber/C_epochLength, progpow.logger)
			ethashCache.cDag = cDag
		}
		return progpowLight(size, cache, hash.Bytes(), nonce, blockNumber, ethashCache.cDag)
	}
	cache := progpow.cache(header.PrimeTerminusNumber().Uint64())
	size := datasetSize(header.PrimeTerminusNumber().Uint64())
	digest, result := powLight(size, cache.cache, header.SealHash(), header.NonceU64(), header.PrimeTerminusNumber().Uint64())
	mixHash = common.BytesToHash(digest)
	powHash = common.BytesToHash(result)
	header.PowDigest.Store(mixHash)
	header.PowHash.Store(powHash)

	// Cache the hash
	progpow.hashCache.Add(header.Hash(), mixHashWorkHash{mixHash: mixHash.Bytes(), workHash: powHash.Bytes()})
	// Caches are unmapped in a finalizer. Ensure that the cache stays alive
	// until after the call to hashimotoLight so it's not unmapped while being used.
	runtime.KeepAlive(cache)

	return mixHash, powHash
}
