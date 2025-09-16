package kawpow

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
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

var (
	errNoMiningWork      = errors.New("no mining work available yet")
	errInvalidSealResult = errors.New("invalid or stale proof-of-work solution")
)

// Seal implements consensus.Engine, attempting to find a nonce that satisfies
// the header's difficulty requirements.
func (kawpow *Kawpow) Seal(header *types.WorkObject, results chan<- *types.WorkObject, stop <-chan struct{}) error {
	// If we're running a fake PoW, simply return a 0 nonce immediately
	if kawpow.config.PowMode == params.ModeFake || kawpow.config.PowMode == params.ModeFullFake {
		header.WorkObjectHeader().SetNonce(types.BlockNonce{})
		select {
		case results <- header:
		default:
			kawpow.logger.WithFields(log.Fields{
				"mode":     "fake",
				"sealhash": header.SealHash(),
			}).Warn("Sealing result is not read by miner")
		}
		return nil
	}
	// If we're running a shared PoW, delegate sealing to it
	if kawpow.shared != nil {
		return kawpow.shared.Seal(header, results, stop)
	}
	// Create a runner and the multiple search threads it directs
	abort := make(chan struct{})

	kawpow.lock.Lock()
	threads := kawpow.threads
	if kawpow.rand == nil {
		seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
		if err != nil {
			kawpow.lock.Unlock()
			return err
		}
		kawpow.rand = rand.New(rand.NewSource(seed.Int64()))
	}
	kawpow.lock.Unlock()
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
		go func() {
			defer func() {
				if r := recover(); r != nil {
					kawpow.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Error("Go-Quai Panicked")
				}
			}()
			defer pend.Done()
			kawpow.Mine(header, abort, locals)
		}()
	}
	// Wait until sealing is terminated or a nonce is found
	go func() {
		defer func() {
			if r := recover(); r != nil {
				kawpow.logger.WithFields(log.Fields{
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
				kawpow.logger.WithFields(log.Fields{
					"mode":     "local",
					"sealhash": header.SealHash(),
				}).Warn("Sealing result is not read by miner")
			}
			close(abort)
		case <-kawpow.update:
			// Thread count was changed on user request, restart
			close(abort)
			if err := kawpow.Seal(header, results, stop); err != nil {
				kawpow.logger.WithField("err", err).Error("Failed to restart sealing after update")
			}
		}
		// Wait for all miners to terminate and return the block
		pend.Wait()
	}()
	return nil
}

func (kawpow *Kawpow) Mine(workObject *types.WorkObject, abort <-chan struct{}, found chan *types.WorkObject) {
	kawpow.MineToThreshold(workObject, params.WorkSharesThresholdDiff, abort, found)
}

func (kawpow *Kawpow) MineToThreshold(workObject *types.WorkObject, workShareThreshold int, abort <-chan struct{}, found chan *types.WorkObject) {
	if workShareThreshold <= 0 {
		log.Global.WithField("WorkshareThreshold", workShareThreshold).Error("WorkshareThreshold must be positive")
		return
	}

	target, err := consensus.CalcWorkShareThreshold(workObject.WorkObjectHeader(), workShareThreshold)
	if err != nil {
		log.Global.WithField("err", err).Error("Issue mining")
		return
	}

	// Extract the height and setup once before mining loop (expensive operations)
	auxPow := workObject.WorkObjectHeader().AuxPow()
	if auxPow == nil {
		kawpow.logger.Error("AuxPow is nil for KAWPOW mining")
		return
	}

	header := auxPow.Header()

	// Get the block height directly from the header (now properly encoded)
	blockHeight := uint64(header.Height())

	// Get the KAWPOW header hash (this is the input to the KAWPOW algorithm)
	kawpowHeaderHash := header.SealHash()

	// Pre-initialize cache and dataset (expensive operations)
	cache := kawpow.cache(blockHeight)
	size := datasetSize(blockHeight)

	// Pre-initialize cDag if needed
	if cache.cDag == nil {
		cDag := make([]uint32, kawpowCacheWords)
		generateCDag(cDag, cache.cache, blockHeight/C_epochLength, kawpow.logger)
		cache.cDag = cDag
	}

	// Start generating random nonces until we abort or find a good one
	kawpow.lock.Lock()
	seed := kawpow.rand.Uint64()
	kawpow.lock.Unlock()
	var (
		attempts = int64(0)
		nonce    = seed
	)

search:
	for {
		select {
		case <-abort:
			// Mining terminated, update stats and abort
			kawpow.logger.WithField("attempts", attempts).Debug("KAWPOW mining aborted")
			break search

		default:
			// We don't have to update hash rate on every nonce, so update after after 2^X nonces
			attempts++
			if (attempts % (1 << 10)) == 0 { // Changed to 1024 for more frequent logging
				kawpow.logger.WithFields(log.Fields{
					"attempts": attempts,
					"nonce":    nonce,
				}).Info("KAWPOW mining progress")
			}

			// Compute the PoW value of this nonce using the RVN-compatible
			// (byte-reversed) KAWPOW header hash bytes
			digest, result := kawpowLight(size, cache.cache, kawpowHeaderHash.Reverse().Bytes(), nonce, blockHeight, cache.cDag)
			resultBig := new(big.Int).SetBytes(result)

			if resultBig.Cmp(target) <= 0 {
				// Correct nonce found, update the AuxPow with the new nonce and mix hash
				kawpow.logger.WithFields(log.Fields{
					"nonce":  nonce,
					"result": resultBig.Text(16),
					"target": target.Text(16),
				}).Debug("KAWPOW solution found!")
				workObject = types.CopyWorkObject(workObject)

				// Update the Ravencoin header with the found nonce and mix hash
				workObject.WorkObjectHeader().AuxPow().Header().SetNonce64(nonce)
				// reverse the digest to little endian before populating the mix hash into the kawpow
				workObject.WorkObjectHeader().AuxPow().Header().SetMixHash(common.Hash(digest).Reverse())

				// Create a new AuxPow with the updated header and coinbase
				updatedAuxPow := types.NewAuxPow(
					auxPow.PowID(),
					workObject.WorkObjectHeader().AuxPow().Header(),
					auxPow.AuxPow2(),
					auxPow.Signature(),
					auxPow.MerkleBranch(),
					auxPow.Transaction(),
				)

				// Set the updated AuxPow (don't touch other WorkObject header fields)
				workObject.WorkObjectHeader().SetAuxPow(updatedAuxPow)

				found <- workObject
				break search
			}
			nonce++
		}
	}
}
