package blake3pow

import (
	"math/big"
	"math/rand"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
)

var (
	// sharedBlake3pow is a full instance that can be shared between multiple users.
	sharedBlake3pow *Blake3pow
)

func init() {
	sharedConfig := Config{
		PowMode: ModeNormal,
	}
	sharedBlake3pow = New(sharedConfig, nil, false)
}

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

	MinDifficulty *big.Int

	// When set, notifications sent by the remote sealer will
	// be block header JSON objects instead of work package arrays.
	NotifyFull bool

	Log *log.Logger `toml:"-"`
}

// Blake3pow is a proof-of-work consensus engine using the blake3 hash algorithm
type Blake3pow struct {
	config Config

	// Mining related fields
	rand    *rand.Rand    // Properly seeded random source for nonces
	threads int           // Number of threads to mine on if mining
	update  chan struct{} // Notification channel to update mining parameters
	remote  *remoteSealer

	// The fields below are hooks for testing
	shared    *Blake3pow    // Shared PoW verifier to avoid cache regeneration
	fakeFail  uint64        // Block number which fails PoW check even in fake mode
	fakeDelay time.Duration // Time delay to sleep for before returning from verify

	lock      sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
	closeOnce sync.Once  // Ensures exit channel will not be closed twice.
}

// New creates a full sized blake3pow PoW scheme and starts a background thread for
// remote mining, also optionally notifying a batch of remote services of new work
// packages.
func New(config Config, notify []string, noverify bool) *Blake3pow {
	if config.Log == nil {
		config.Log = &log.Log
	}
	blake3pow := &Blake3pow{
		config: config,
		update: make(chan struct{}),
	}
	if config.PowMode == ModeShared {
		blake3pow.shared = sharedBlake3pow
	}
	blake3pow.remote = startRemoteSealer(blake3pow, notify, noverify)
	return blake3pow
}

// NewTester creates a small sized blake3pow PoW scheme useful only for testing
// purposes.
func NewTester(notify []string, noverify bool) *Blake3pow {
	return New(Config{PowMode: ModeTest}, notify, noverify)
}

// NewFaker creates a blake3pow consensus engine with a fake PoW scheme that accepts
// all blocks' seal as valid, though they still have to conform to the Quai
// consensus rules.
func NewFaker() *Blake3pow {
	return &Blake3pow{
		config: Config{
			PowMode: ModeFake,
			Log:     &log.Log,
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
			Log:     &log.Log,
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
			Log:     &log.Log,
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
			Log:     &log.Log,
		},
	}
}

// NewShared creates a full sized blake3pow PoW shared between all requesters running
// in the same process.
func NewShared() *Blake3pow {
	return &Blake3pow{shared: sharedBlake3pow}
}

// Close closes the exit channel to notify all backend threads exiting.
func (blake3pow *Blake3pow) Close() error {
	blake3pow.closeOnce.Do(func() {
		// Short circuit if the exit channel is not allocated.
		if blake3pow.remote == nil {
			return
		}
		close(blake3pow.remote.requestExit)
		<-blake3pow.remote.exitCh
	})
	return nil
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

// APIs implements consensus.Engine, returning the user facing RPC APIs.
func (blake3pow *Blake3pow) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	// In order to ensure backward compatibility, we exposes blake3pow RPC APIs
	// to both eth and blake3pow namespaces.
	return []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   &API{blake3pow},
			Public:    true,
		},
		{
			Namespace: "blake3pow",
			Version:   "1.0",
			Service:   &API{blake3pow},
			Public:    true,
		},
	}
}
