package randomx

//#cgo CFLAGS: -I./randomx
//#cgo LDFLAGS: -L${SRCDIR}/lib -lrandomx -lstdc++ -lm -lpthread
//#include <stdlib.h>
//#include "lib/randomx.h"
import "C"
import (
	"bytes"
	crand "crypto/rand"
	"errors"
	"math/big"
	"math/rand"
	"runtime"
	"sync"
	"unsafe"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/common/hexutil"
	"github.com/spruce-solutions/go-quai/common/math"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/metrics"
	"github.com/spruce-solutions/go-quai/rpc"
)

const RxTestKey = "STATIC TEST KEY! Real key should rotate periodically."

const RxHashSize = C.RANDOMX_HASH_SIZE

// All flags
const (
	FlagDefault     C.randomx_flags = 0 // for all default
	FlagLargePages  C.randomx_flags = 1 // for dataset & rxCache & vm
	FlagHardAES     C.randomx_flags = 2 // for vm
	FlagFullMEM     C.randomx_flags = 4 // for vm
	FlagJIT         C.randomx_flags = 8 // for vm & cache
	FlagSecure      C.randomx_flags = 16
	FlagArgon2SSSE3 C.randomx_flags = 32 // for cache
	FlagArgon2AVX2  C.randomx_flags = 64 // for cache
	FlagArgon2      C.randomx_flags = 96 // = avx2 + sse3
)

type Cache *C.randomx_cache

type Dataset *C.randomx_dataset

type VM *C.randomx_vm

// Config are the configuration parameters of randomx
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

	// RandomX  VM flags
	LargePages  bool
	HardAES     bool
	FullMem     bool
	JIT         bool
	Secure      bool
	Argon2SSSE3 bool
	Argon2AVX2  bool
	Argon2      bool
}

func (c *Config) compileFlags() C.randomx_flags {
	flags := FlagDefault
	if c.LargePages {
		flags |= FlagLargePages
	}
	if c.HardAES {
		flags |= FlagHardAES
	}
	if c.FullMem {
		flags |= FlagFullMEM
	}
	if c.JIT {
		flags |= FlagJIT
	}
	if c.Secure {
		flags |= FlagSecure
	}
	if c.Argon2SSSE3 {
		flags |= FlagArgon2SSSE3
	}
	if c.Argon2AVX2 {
		flags |= FlagArgon2AVX2
	}
	if c.Argon2 {
		flags |= FlagArgon2
	}
	return flags
}

// Randomx is a consensus engine based on proof-of-work, implementing the RandomX algorithm.
type Randomx struct {
	config Config

	// Runtime state
	vm        VM         // RandomX virtual machine context
	lock      sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
	closeOnce sync.Once  // Ensures exit channel will not be closed twice.
	seed      []byte     // Seed string used to key the RandomX hash

	// Mining related fields
	cache    Cache         // Cache memory allocation
	dataset  Dataset       // Dataset memory allocation used for FullMem mode
	rand     *rand.Rand    // Properly seeded random source for nonces
	update   chan struct{} // Notification channel to update mining parameters
	hashrate metrics.Meter // Meter tracking the average hashrate
	remote   *remoteSealer
}

// Creates a new Randomx
func New(config Config, seed []byte, notify []string, noverify bool) (*Randomx, error) {
	if len(seed) == 0 {
		return nil, errors.New("Seed may not be be empty")
	}
	if config.Log == nil {
		config.Log = log.Root()
	}

	rx := &Randomx{
		config:   config,
		cache:    nil,
		dataset:  nil,
		vm:       nil,
		seed:     seed,
		hashrate: metrics.NewMeterForced(),
	}
	rng_seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	rx.rand = rand.New(rand.NewSource(rng_seed.Int64()))
	if nil != err {
		return nil, err
	}
	// Compile the binary flags
	flags := rx.config.compileFlags()
	// Allocate and initialize cache
	rx.cache = C.randomx_alloc_cache(flags)
	if rx.cache == nil {
		return nil, errors.New("Failed to alloc RandomX cache")
	}
	C.randomx_init_cache(rx.cache, unsafe.Pointer(&seed[0]), C.size_t(len(seed)))

	// Only allocate a dataset if in full-memory mode
	var dataset Dataset = nil
	if rx.config.FullMem {
		dataset = C.randomx_alloc_dataset(flags)
		if dataset == nil {
			return nil, errors.New("Failed to alloc RandomX dataset")
		}

		// Parallel initialization of dataset
		count := uint32(C.randomx_dataset_item_count())
		var wg sync.WaitGroup
		var workerNum = uint32(runtime.NumCPU())
		for i := uint32(0); i < workerNum; i++ {
			wg.Add(1)
			a := (count * i) / workerNum
			b := (count * (i + 1)) / workerNum
			go func() {
				defer wg.Done()
				C.randomx_init_dataset(dataset, rx.cache, C.ulong(a), C.ulong(b-a))
			}()
		}
		wg.Wait()
	}

	// Create the virtual machine
	vm := C.randomx_create_vm(flags, rx.cache, dataset)
	if vm == nil {
		return nil, errors.New("failed to create vm")
	} else {
		rx.vm = vm
		rx.remote = startRemoteSealer(rx, notify, noverify)
		return rx, nil
	}
}

func (rx *Randomx) ReKey(seed []byte) error {
	// If the seed matches, nothing to do
	if bytes.Compare(rx.seed, seed) == 0 {
		return nil
	} else {
		rx.lock.Lock()
		// Compile the binary flags
		vm, err := New(rx.config, seed, rx.remote.notifyURLs, rx.remote.noverify)
		if err != nil {
			return err
		}
		C.randomx_destroy_vm(rx.vm)
		rx = vm
		rx.lock.Unlock()
		return nil
	}
}

func (rx *Randomx) Destroy() {
	rx.lock.Lock()
	C.randomx_destroy_vm(rx.vm)
}

// Close closes the exit channel to notify all backend threads exiting.
func (rx *Randomx) Close() error {
	rx.closeOnce.Do(func() {
		// Short circuit if the exit channel is not allocated.
		if rx.remote == nil {
			return
		}
		close(rx.remote.requestExit)
		<-rx.remote.exitCh
	})
	return nil
}

func (rx *Randomx) Hash(in []byte) []byte {
	if rx.vm == nil {
		panic("failed hashing: using empty vm")
	}
	rx.lock.Lock()
	input := C.CBytes(in)
	output := C.CBytes(make([]byte, RxHashSize))
	C.randomx_calculate_hash(rx.vm, input, C.size_t(len(in)), output)
	hash := C.GoBytes(output, RxHashSize)
	C.free(unsafe.Pointer(input))
	C.free(unsafe.Pointer(output))
	rx.lock.Unlock()

	return hash
}

func (rx *Randomx) HashWithNonce(nonce uint64, in []byte) []byte {
	// Prefix the nonce to the incoming bytes to be hashed
	encoded_nonce := types.EncodeNonce(nonce)
	preimage := make([]byte, len(encoded_nonce)+len(in))
	copy(preimage[:len(encoded_nonce)], encoded_nonce[:])
	copy(preimage[len(encoded_nonce):], in[:])
	return rx.Hash(preimage)
}

func (rx *Randomx) HashFirst(in []byte) {
	if rx.vm == nil {
		panic("failed hashing: using empty vm")
	}

	rx.lock.Lock()
	input := C.CBytes(in)
	C.randomx_calculate_hash_first(rx.vm, input, C.size_t(len(in)))
	C.free(unsafe.Pointer(input))
	rx.lock.Unlock()
}

func (rx *Randomx) HashNext(in []byte) []byte {
	if rx.vm == nil {
		panic("failed hashing: using empty vm")
	}

	rx.lock.Lock()
	input := C.CBytes(in)
	output := C.CBytes(make([]byte, RxHashSize))
	C.randomx_calculate_hash_next(rx.vm, input, C.size_t(len(in)), output)
	hash := C.GoBytes(output, RxHashSize)
	C.free(unsafe.Pointer(input))
	C.free(unsafe.Pointer(output))
	rx.lock.Unlock()

	return hash
}

func (rx *Randomx) HashLast() []byte {
	if rx.vm == nil {
		panic("failed hashing: using empty vm")
	}

	rx.lock.Lock()
	output := C.CBytes(make([]byte, RxHashSize))
	C.randomx_calculate_hash_last(rx.vm, output)
	hash := C.GoBytes(output, RxHashSize)
	C.free(unsafe.Pointer(output))
	rx.lock.Unlock()

	return hash
}

// Hashrate implements PoW, returning the measured rate of the search invocations
// per second over the last minute.
// Note the returned hashrate includes local hashrate, but also includes the total
// hashrate of all remote miner.
func (rx *Randomx) Hashrate() float64 {
	return rx.hashrate.Rate1()
}

// SubmitHashrate can be used for remote miners to submit their hash rate.
// This enables the node to report the combined hash rate of all miners
// which submit work through this node.
//
// It accepts the miner hash rate and an identifier which must be unique
// between nodes.
func (rx *Randomx) SubmitHashrate(rate hexutil.Uint64, id common.Hash) bool {
	if rx.remote == nil {
		return false
	}

	var done = make(chan struct{}, 1)
	select {
	case rx.remote.submitRateCh <- &hashrate{done: done, rate: uint64(rate), id: id}:
	case <-rx.remote.exitCh:
		return false
	}

	// Block until hash rate submitted successfully.
	<-done
	return true
}

// APIs implements consensus.Engine, returning the user facing RPC APIs.
func (rx *Randomx) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	// In order to ensure backward compatibility, we exposes ethash RPC APIs
	// to both eth and ethash namespaces.
	return []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   &API{rx},
			Public:    true,
		},
		{
			Namespace: "ethash",
			Version:   "1.0",
			Service:   &API{rx},
			Public:    true,
		},
	}
}
