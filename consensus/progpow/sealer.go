package progpow

import (
	crand "crypto/rand"
	"errors"
	"math"
	"math/big"
	"math/rand"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"modernc.org/mathutil"
)

const (
	// staleThreshold is the maximum depth of the acceptable stale but valid progpow solution.
	staleThreshold = 7
	mantBits       = 64
)

var (
	errNoMiningWork      = errors.New("no mining work available yet")
	errInvalidSealResult = errors.New("invalid or stale proof-of-work solution")
)

// Seal implements consensus.Engine, attempting to find a nonce that satisfies
// the header's difficulty requirements.
func (progpow *Progpow) Seal(header *types.WorkObject, results chan<- *types.WorkObject, stop <-chan struct{}) error {
	// If we're running a fake PoW, simply return a 0 nonce immediately
	if progpow.config.PowMode == ModeFake || progpow.config.PowMode == ModeFullFake {
		header.WorkObjectHeader().SetNonce(types.BlockNonce{})
		select {
		case results <- header:
		default:
			progpow.logger.WithFields(log.Fields{
				"mode":     "fake",
				"sealhash": header.SealHash(),
			}).Warn("Sealing result is not read by miner")
		}
		return nil
	}
	// If we're running a shared PoW, delegate sealing to it
	if progpow.shared != nil {
		return progpow.shared.Seal(header, results, stop)
	}
	// Create a runner and the multiple search threads it directs
	abort := make(chan struct{})

	progpow.lock.Lock()
	threads := progpow.threads
	if progpow.rand == nil {
		seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
		if err != nil {
			progpow.lock.Unlock()
			return err
		}
		progpow.rand = rand.New(rand.NewSource(seed.Int64()))
	}
	progpow.lock.Unlock()
	if threads == 0 {
		threads = runtime.NumCPU()
	}
	if threads < 0 {
		threads = 0 // Allows disabling local mining without extra logic around local/remote
	}
	var (
		pend   sync.WaitGroup
		locals = make(chan *types.WorkObject)
	)
	for i := 0; i < threads; i++ {
		pend.Add(1)
		go func(id int, nonce uint64) {
			defer func() {
				if r := recover(); r != nil {
					progpow.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Error("Go-Quai Panicked")
				}
			}()
			defer pend.Done()
			progpow.mine(header, id, nonce, abort, locals)
		}(i, uint64(progpow.rand.Int63()))
	}
	// Wait until sealing is terminated or a nonce is found
	go func() {
		defer func() {
			if r := recover(); r != nil {
				progpow.logger.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		var result *types.WorkObject
		select {
		case <-stop:
			// Outside abort, stop all miner threads
			close(abort)
		case result = <-locals:
			// One of the threads found a block, abort all others
			select {
			case results <- result:
			default:
				progpow.logger.WithFields(log.Fields{
					"mode":     "local",
					"sealhash": header.SealHash(),
				}).Warn("Sealing result is not read by miner")
			}
			close(abort)
		case <-progpow.update:
			// Thread count was changed on user request, restart
			close(abort)
			if err := progpow.Seal(header, results, stop); err != nil {
				progpow.logger.WithField("err", err).Error("Failed to restart sealing after update")
			}
		}
		// Wait for all miners to terminate and return the block
		pend.Wait()
	}()
	return nil
}

// mine is the actual proof-of-work miner that searches for a nonce starting from
// seed that results in correct final block difficulty.
func (progpow *Progpow) mine(workObject *types.WorkObject, id int, seed uint64, abort chan struct{}, found chan *types.WorkObject) {
	// Extract some data from the header
	diff := new(big.Int).Set(workObject.Difficulty())
	c, _ := mathutil.BinaryLog(diff, mantBits)
	if c <= params.WorkSharesThresholdDiff {
		return
	}
	workShareThreshold := c - params.WorkSharesThresholdDiff
	workShareDiff := new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(workShareThreshold)), nil)
	var (
		target  = new(big.Int).Div(big2e256, workShareDiff)
		nodeCtx = progpow.config.NodeLocation.Context()
	)
	// Start generating random nonces until we abort or find a good one
	var (
		attempts = int64(0)
		nonce    = seed
	)
search:
	for {
		select {
		case <-abort:
			// Mining terminated, update stats and abort
			break search

		default:
			// We don't have to update hash rate on every nonce, so update after after 2^X nonces
			attempts++
			if (attempts % (1 << 15)) == 0 {
				attempts = 0
			}
			powLight := func(size uint64, cache []uint32, hash []byte, nonce uint64, blockNumber uint64) ([]byte, []byte) {
				ethashCache := progpow.cache(blockNumber)
				if ethashCache.cDag == nil {
					cDag := make([]uint32, progpowCacheWords)
					generateCDag(cDag, ethashCache.cache, blockNumber/epochLength, progpow.logger)
					ethashCache.cDag = cDag
				}
				return progpowLight(size, cache, hash, nonce, blockNumber, ethashCache.cDag, progpow.lookupCache)
			}
			cache := progpow.cache(workObject.NumberU64(nodeCtx))
			size := datasetSize(workObject.NumberU64(nodeCtx))
			// Compute the PoW value of this nonce
			digest, result := powLight(size, cache.cache, workObject.SealHash().Bytes(), nonce, workObject.PrimeTerminusNumber().Uint64())
			if new(big.Int).SetBytes(result).Cmp(target) <= 0 {
				// Correct nonce found, create a new header with it
				workObject = types.CopyWorkObject(workObject)
				workObject.WorkObjectHeader().SetNonce(types.EncodeNonce(nonce))
				hashBytes := common.BytesToHash(digest)
				workObject.SetMixHash(hashBytes)
				found <- workObject
				break search
			}
			nonce++
		}
	}
}
