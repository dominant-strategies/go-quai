package blake3pow

import (
	"math/big"
	"math/rand"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
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

// Config are the configuration parameters of the blake3pow.
type Config struct {
	PowMode Mode

	DurationLimit *big.Int

	GasCeil uint64

	NodeLocation common.Location

	MinDifficulty *big.Int

	// When set, notifications sent by the remote sealer will
	// be block header JSON objects instead of work package arrays.
	NotifyFull bool

	Log *log.Logger `toml:"-"`
	// Number of threads to mine on if mining
	NumThreads int
}

// Blake3pow is a proof-of-work consensus engine using the blake3 hash algorithm
type Blake3pow struct {
	config Config

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
func New(config Config, notify []string, noverify bool, logger *log.Logger) *Blake3pow {
	blake3pow := &Blake3pow{
		config:  config,
		update:  make(chan struct{}),
		logger:  logger,
		threads: config.NumThreads,
	}
	if config.PowMode == ModeShared {
		blake3pow.shared = sharedBlake3pow
	}
	return blake3pow
}

// NewTester creates a small sized blake3pow PoW scheme useful only for testing
// purposes.
func NewTester(notify []string, noverify bool) *Blake3pow {
	return New(Config{PowMode: ModeTest}, notify, noverify, log.NewLogger("test-blake3pow.log", "info"))
}

// NewFaker creates a blake3pow consensus engine with a fake PoW scheme that accepts
// all blocks' seal as valid, though they still have to conform to the Quai
// consensus rules.
func NewFaker() *Blake3pow {
	return &Blake3pow{
		config: Config{
			PowMode: ModeFake,
		},
	}
}

// NewFakeFailer creates a blake3pow consensus engine with a fake PoW scheme that
// accepts all blocks as valid apart from the single one specified, though they
// still have to conform to the Quai consensus rules.
func NewFakeFailer(fail uint64) *Blake3pow {
	return &Blake3pow{
		config: Config{
			PowMode: ModeFake,
		},
		fakeFail: fail,
	}
}

// NewFakeDelayer creates a blake3pow consensus engine with a fake PoW scheme that
// accepts all blocks as valid, but delays verifications by some time, though
// they still have to conform to the Quai consensus rules.
func NewFakeDelayer(delay time.Duration) *Blake3pow {
	return &Blake3pow{
		config: Config{
			PowMode: ModeFake,
		},
		fakeDelay: delay,
	}
}

// NewFullFaker creates an blake3pow consensus engine with a full fake scheme that
// accepts all blocks as valid, without checking any consensus rules whatsoever.
func NewFullFaker() *Blake3pow {
	return &Blake3pow{
		config: Config{
			PowMode: ModeFullFake,
		},
	}
}

// NewShared creates a full sized blake3pow PoW shared between all requesters running
// in the same process.
func NewShared() *Blake3pow {
	return &Blake3pow{shared: sharedBlake3pow}
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
