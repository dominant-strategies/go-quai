package blake3pow

import (
	"math/big"
	"math/rand"
	sync "github.com/sasha-s/go-deadlock"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics"
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

	// When set, notifications sent by the remote sealer will
	// be block header JSON objects instead of work package arrays.
	NotifyFull bool

	Log log.Logger `toml:"-"`
}

// Blake3pow is a proof-of-work consensus engine using the blake3 hash algorithm
type Blake3pow struct {
	config Config

	// Mining related fields
	rand     *rand.Rand    // Properly seeded random source for nonces
	threads  int           // Number of threads to mine on if mining
	update   chan struct{} // Notification channel to update mining parameters
	hashrate metrics.Meter // Meter tracking the average hashrate
	remote   *remoteSealer

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
		config.Log = log.Log
	}
	blake3pow := &Blake3pow{
		config:   config,
		update:   make(chan struct{}),
		hashrate: metrics.NewMeterForced(),
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
// all blocks' seal as valid, though they still have to conform to the Ethereum
// consensus rules.
func NewFaker() *Blake3pow {
	return &Blake3pow{
		config: Config{
			PowMode: ModeFake,
			Log:     log.Log,
		},
	}
}

// NewFakeFailer creates a blake3pow consensus engine with a fake PoW scheme that
// accepts all blocks as valid apart from the single one specified, though they
// still have to conform to the Ethereum consensus rules.
func NewFakeFailer(fail uint64) *Blake3pow {
	return &Blake3pow{
		config: Config{
			PowMode: ModeFake,
			Log:     log.Log,
		},
		fakeFail: fail,
	}
}

// NewFakeDelayer creates a blake3pow consensus engine with a fake PoW scheme that
// accepts all blocks as valid, but delays verifications by some time, though
// they still have to conform to the Ethereum consensus rules.
func NewFakeDelayer(delay time.Duration) *Blake3pow {
	return &Blake3pow{
		config: Config{
			PowMode: ModeFake,
			Log:     log.Log,
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
			Log:     log.Log,
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

// Hashrate implements PoW, returning the measured rate of the search invocations
// per second over the last minute.
// Note the returned hashrate includes local hashrate, but also includes the total
// hashrate of all remote miner.
func (blake3pow *Blake3pow) Hashrate() float64 {
	// Short circuit if we are run the blake3pow in normal/test mode.
	if blake3pow.config.PowMode != ModeNormal && blake3pow.config.PowMode != ModeTest {
		return blake3pow.hashrate.Rate1()
	}
	var res = make(chan uint64, 1)

	select {
	case blake3pow.remote.fetchRateCh <- res:
	case <-blake3pow.remote.exitCh:
		// Return local hashrate only if blake3pow is stopped.
		return blake3pow.hashrate.Rate1()
	}

	// Gather total submitted hash rate of remote sealers.
	return blake3pow.hashrate.Rate1() + float64(<-res)
}

// SubmitHashrate can be used for remote miners to submit their hash rate.
// This enables the node to report the combined hash rate of all miners
// which submit work through this node.
//
// It accepts the miner hash rate and an identifier which must be unique
// between nodes.
func (blake3pow *Blake3pow) SubmitHashrate(rate hexutil.Uint64, id common.Hash) bool {
	if blake3pow.remote == nil {
		return false
	}

	var done = make(chan struct{}, 1)
	select {
	case blake3pow.remote.submitRateCh <- &hashrate{done: done, rate: uint64(rate), id: id}:
	case <-blake3pow.remote.exitCh:
		return false
	}

	// Block until hash rate submitted successfully.
	<-done
	return true
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
