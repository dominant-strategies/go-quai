package blake3pow

import (
	crand "crypto/rand"
	"errors"
	"math"
	"math/big"
	"math/rand"
	"runtime"
	"runtime/debug"
	"sync"

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
func (blake3pow *Blake3pow) Seal(header *types.WorkObject, results chan<- *types.WorkObject, stop <-chan struct{}) error {
	// If we're running a fake PoW, simply return a 0 nonce immediately
	if blake3pow.config.PowMode == params.ModeFake || blake3pow.config.PowMode == params.ModeFullFake {
		header.WorkObjectHeader().SetNonce(types.BlockNonce{})
		select {
		case results <- header:
		default:
			blake3pow.logger.WithFields(log.Fields{
				"mode":     "fake",
				"sealhash": header.SealHash(),
			}).Warn("Sealing result is not read by miner")
		}
		return nil
	}
	// If we're running a shared PoW, delegate sealing to it
	if blake3pow.shared != nil {
		return blake3pow.shared.Seal(header, results, stop)
	}
	// Create a runner and the multiple search threads it directs
	abort := make(chan struct{})

	blake3pow.lock.Lock()
	threads := blake3pow.threads
	if blake3pow.rand == nil {
		seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
		if err != nil {
			blake3pow.lock.Unlock()
			return err
		}
		blake3pow.rand = rand.New(rand.NewSource(seed.Int64()))
	}
	blake3pow.lock.Unlock()
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
					blake3pow.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Error("Go-Quai Panicked")
				}
			}()
			defer pend.Done()
			blake3pow.Mine(header, abort, locals)
		}()
	}
	// Wait until sealing is terminated or a nonce is found
	go func() {
		defer func() {
			if r := recover(); r != nil {
				blake3pow.logger.WithFields(log.Fields{
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
				blake3pow.logger.WithFields(log.Fields{
					"mode":     "local",
					"sealhash": header.SealHash(),
				}).Warn("Sealing result is not read by miner")
			}
			close(abort)
		case <-blake3pow.update:
			// Thread count was changed on user request, restart
			close(abort)
			if err := blake3pow.Seal(header, results, stop); err != nil {
				blake3pow.logger.WithField("err", err).Error("Failed to restart sealing after update")
			}
		}
		// Wait for all miners to terminate and return the block
		pend.Wait()
	}()
	return nil
}

func (blake3pow *Blake3pow) Mine(header *types.WorkObject, abort <-chan struct{}, found chan *types.WorkObject) {
	blake3pow.MineToThreshold(header, params.WorkSharesThresholdDiff, abort, found)
}

func (blake3pow *Blake3pow) MineToThreshold(workObject *types.WorkObject, workShareThreshold int, abort <-chan struct{}, found chan *types.WorkObject) {
	if workShareThreshold <= 0 {
		log.Global.WithField("WorkshareThreshold", workShareThreshold).Error("WorkshareThreshold must be positive")
		return
	}

	target, err := consensus.CalcWorkShareThreshold(workObject.WorkObjectHeader(), workShareThreshold)
	if err != nil {
		log.Global.WithField("err", err).Error("Issue mining")
		return
	}

	// Start generating random nonces until we abort or find a good one
	blake3pow.lock.Lock()
	seed := blake3pow.rand.Uint64()
	blake3pow.lock.Unlock()
	var (
		attempts  = int64(0)
		nonce     = seed
		powBuffer = new(big.Int)
	)
	blake3pow.logger.WithField("seed", seed).Trace("Started blake3pow search for new nonces")
search:
	for {
		select {
		case <-abort:
			// Mining terminated, update stats and abort
			blake3pow.logger.WithField("attempts", nonce-seed).Trace("Blake3pow nonce search aborted")
			break search

		default:
			// We don't have to update hash rate on every nonce, so update after after 2^X nonces
			attempts++
			if (attempts % (1 << 15)) == 0 {
				attempts = 0
			}
			// Compute the PoW value of this nonce
			workObject = types.CopyWorkObject(workObject)
			workObject.WorkObjectHeader().SetNonce(types.EncodeNonce(nonce))
			hash := workObject.Hash().Bytes()
			if powBuffer.SetBytes(hash).Cmp(target) <= 0 {
				// Seal and return a block (if still needed)
				select {
				case found <- workObject:
					blake3pow.logger.WithFields(log.Fields{
						"attempts": nonce - seed,
						"nonce":    nonce,
					}).Trace("Blake3pow nonce found and reported")
				case <-abort:
					blake3pow.logger.WithFields(log.Fields{
						"attempts": nonce - seed,
						"nonce":    nonce,
					}).Trace("Blake3pow nonce found but discarded")
				}
				break search
			}
			nonce++
		}
	}
}
