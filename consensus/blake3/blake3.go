package blake3

import (
	crand "crypto/rand"
	"math/big"
	"math/rand"
	"sync"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/common/hexutil"
	"github.com/spruce-solutions/go-quai/common/math"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/metrics"
	"github.com/spruce-solutions/go-quai/rpc"
)

// Config are the configuration parameters of Blake3 PoW
type Config struct {
	// Number of threads to use when mining.
	// -1 => mining disabled
	// 0 => max no. of threads, limited by max CPUs
	// >0 => exact no. of threads, up to max CPUs
	MiningThreads int

	// When set, notifications sent by the remote sealer will
	// be block header JSON objects instead of work package arrays.
	NotifyFull bool

	// Logger object
	Log log.Logger `toml:"-"`

	// Fake proof of work for testing
	Fakepow bool
}

// Blake3 a consensus engine based on the Blake3 hash function
type Blake3 struct {
	config Config

	// Runtime state
	lock      sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
	closeOnce sync.Once  // Ensures exit channel will not be closed twice.

	// Mining related fields
	rand     *rand.Rand    // Properly seeded random source for nonces
	update   chan struct{} // Notification channel to update mining parameters
	hashrate metrics.Meter // Meter tracking the average hashrate
	remote   *remoteSealer
}

// Creates a new Blake3 engine
func New(config Config, notify []string, noverify bool) (*Blake3, error) {
	// Do not allow Fakepow for a real consensus engine
	config.Fakepow = false

	if config.Log == nil {
		config.Log = log.Root()
	}
	blake3 := &Blake3{
		config:   config,
		hashrate: metrics.NewMeterForced(),
	}
	rng_seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	blake3.rand = rand.New(rand.NewSource(rng_seed.Int64()))
	if nil != err {
		return nil, err
	}
	return blake3, nil
}

// NewFaker creates a blake3 consensus engine with a fake PoW scheme that accepts
// all blocks' seal as valid, though they still have to conform to the Ethereum
// consensus rules.
func NewFaker() *Blake3 {
	return &Blake3{
		config: Config{
			Log:     log.Root(),
			Fakepow: true,
		},
	}
}

// NewTester creates a small sized ethash PoW scheme useful only for testing
// purposes. Params have yet to be implemented.
func NewTester(notify []string, noverify bool) *Blake3 {
	return NewFaker()
}

// Close closes the exit channel to notify all backend threads exiting.
func (blake3 *Blake3) Close() error {
	blake3.closeOnce.Do(func() {
		// Short circuit if the exit channel is not allocated.
		if blake3.remote == nil {
			return
		}
		close(blake3.remote.requestExit)
		<-blake3.remote.exitCh
	})
	return nil
}

// Hashrate implements PoW, returning the measured rate of the search invocations
// per second over the last minute.
// Note the returned hashrate includes local hashrate, but also includes the total
// hashrate of all remote miner.
func (blake3 *Blake3) Hashrate() float64 {
	return blake3.hashrate.Rate1()
}

// SubmitHashrate can be used for remote miners to submit their hash rate.
// This enables the node to report the combined hash rate of all miners
// which submit work through this node.
//
// It accepts the miner hash rate and an identifier which must be unique
// between nodes.
func (blake3 *Blake3) SubmitHashrate(rate hexutil.Uint64, id common.Hash) bool {
	if blake3.remote == nil {
		return false
	}

	var done = make(chan struct{}, 1)
	select {
	case blake3.remote.submitRateCh <- &hashrate{done: done, rate: uint64(rate), id: id}:
	case <-blake3.remote.exitCh:
		return false
	}

	// Block until hash rate submitted successfully.
	<-done
	return true
}

// APIs implements consensus.Engine, returning the user facing RPC APIs.
func (blake3 *Blake3) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	// In order to ensure backward compatibility, we exposes ethash RPC APIs
	// to both eth and ethash namespaces.
	return []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   &API{blake3},
			Public:    true,
		},
		{
			Namespace: "ethash",
			Version:   "1.0",
			Service:   &API{blake3},
			Public:    true,
		},
	}
}
