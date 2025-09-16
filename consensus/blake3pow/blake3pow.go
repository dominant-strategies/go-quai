package blake3pow

import (
	"math/rand"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

var (
	// sharedBlake3pow is a full instance that can be shared between multiple users.
	sharedBlake3pow *Blake3pow
)

// Mode defines the type and amount of PoW verification a blake3pow engine makes.
type Mode uint

const (
	ModeNormal Mode = iota
	ModeShared
	ModeTest
	ModeFake
	ModeFullFake
)

// Blake3pow is a proof-of-work consensus engine using the blake3 hash algorithm
type Blake3pow struct {
	config params.PowConfig

	// Mining related fields
	rand    *rand.Rand    // Properly seeded random source for nonces
	threads int           // Number of threads to mine on if mining
	update  chan struct{} // Notification channel to update mining parameters

	// The fields below are hooks for testing
	shared    *Blake3pow    // Shared PoW verifier to avoid cache regeneration
	fakeFail  uint64        // Block number which fails PoW check even in fake mode
	fakeDelay time.Duration // Time delay to sleep for before returning from verify

	lock      sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
	closeOnce sync.Once  // Ensures exit channel will not be closed twice.

	logger *log.Logger
}

// New creates a full sized blake3pow PoW scheme and starts a background thread for
// remote mining, also optionally notifying a batch of remote services of new work
// packages.
func New(config params.PowConfig, notify []string, noverify bool, logger *log.Logger) *Blake3pow {
	blake3pow := &Blake3pow{
		config:  config,
		update:  make(chan struct{}),
		logger:  logger,
		rand:    rand.New(rand.NewSource(time.Now().UnixNano())),
		threads: config.NumThreads,
	}
	if config.PowMode == params.ModeShared {
		blake3pow.shared = sharedBlake3pow
	}
	return blake3pow
}

// Threads returns the number of mining threads currently enabled. This doesn't
// necessarily mean that mining is running!
func (blake3pow *Blake3pow) Threads() int {
	blake3pow.lock.Lock()
	defer blake3pow.lock.Unlock()

	return blake3pow.threads
}

// SetThreads updates the number of mining threads currently enabled. Calling
// this method does not start mining, only sets the thread count. If zero is
// specified, the miner will use all cores of the machine. Setting a thread
// count below zero is allowed and will cause the miner to idle, without any
// work being done.
func (blake3pow *Blake3pow) SetThreads(threads int) {
	blake3pow.lock.Lock()
	defer blake3pow.lock.Unlock()

	if blake3pow.shared != nil {
		// If we're running a shared PoW, set the thread count on that instead
		blake3pow.shared.SetThreads(threads)
	} else {
		// Update the threads and ping any running seal to pull in any changes
		blake3pow.threads = threads
		select {
		case blake3pow.update <- struct{}{}:
		default:
		}
	}
}

func (blake3pow *Blake3pow) ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error) {
	return header.Hash(), nil
}

func (blake3pow *Blake3pow) ComputePowLight(header *types.WorkObjectHeader) (common.Hash, common.Hash) {
	return common.Hash{}, common.Hash{}
}
