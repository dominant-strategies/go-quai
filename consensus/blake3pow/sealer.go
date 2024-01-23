package blake3pow

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"math/rand"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
)

const (
	// staleThreshold is the maximum depth of the acceptable stale but valid blake3pow solution.
	staleThreshold = 7
	mantBits       = 64
)

var (
	errNoMiningWork      = errors.New("no mining work available yet")
	errInvalidSealResult = errors.New("invalid or stale proof-of-work solution")
)

// Seal implements consensus.Engine, attempting to find a nonce that satisfies
// the header's difficulty requirements.
func (blake3pow *Blake3pow) Seal(header *types.Header, results chan<- *types.Header, stop <-chan struct{}) error {
	// If we're running a fake PoW, simply return a 0 nonce immediately
	if blake3pow.config.PowMode == ModeFake || blake3pow.config.PowMode == ModeFullFake {
		header.SetNonce(types.BlockNonce{})
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
		locals = make(chan *types.Header)
	)
	for i := 0; i < threads; i++ {
		pend.Add(1)
		go func(id int, nonce uint64) {
			defer pend.Done()
			blake3pow.mine(header, id, nonce, abort, locals)
		}(i, uint64(blake3pow.rand.Int63()))
	}
	// Wait until sealing is terminated or a nonce is found
	go func() {
		var result *types.Header
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

// mine is the actual proof-of-work miner that searches for a nonce starting from
// seed that results in correct final header difficulty.
func (blake3pow *Blake3pow) mine(header *types.Header, id int, seed uint64, abort chan struct{}, found chan *types.Header) {
	// Extract some data from the header
	var (
		target = new(big.Int).Div(big2e256, header.Difficulty())
	)
	// Start generating random nonces until we abort or find a good one
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
			header = types.CopyHeader(header)
			header.SetNonce(types.EncodeNonce(nonce))
			hash := header.Hash().Bytes()
			if powBuffer.SetBytes(hash).Cmp(target) <= 0 {
				// Correct nonce found, create a new header with it

				// Seal and return a block (if still needed)
				select {
				case found <- header:
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

// This is the timeout for HTTP requests to notify external miners.
const remoteSealerTimeout = 1 * time.Second

type remoteSealer struct {
	works         map[common.Hash]*types.Header
	rates         map[common.Hash]hashrate
	currentHeader *types.Header
	currentWork   [4]string
	notifyCtx     context.Context
	cancelNotify  context.CancelFunc // cancels all notification requests
	reqWG         sync.WaitGroup     // tracks notification request goroutines

	blake3pow    *Blake3pow
	noverify     bool
	notifyURLs   []string
	results      chan<- *types.Header
	workCh       chan *sealTask   // Notification channel to push new work and relative result channel to remote sealer
	fetchWorkCh  chan *sealWork   // Channel used for remote sealer to fetch mining work
	submitWorkCh chan *mineResult // Channel used for remote sealer to submit their mining result
	fetchRateCh  chan chan uint64 // Channel used to gather submitted hash rate for local or remote sealer.
	submitRateCh chan *hashrate   // Channel used for remote sealer to submit their mining hashrate
	requestExit  chan struct{}
	exitCh       chan struct{}
}

// sealTask wraps a seal header with relative result channel for remote sealer thread.
type sealTask struct {
	header  *types.Header
	results chan<- *types.Header
}

// mineResult wraps the pow solution parameters for the specified block.
type mineResult struct {
	nonce types.BlockNonce
	hash  common.Hash

	errc chan error
}

// hashrate wraps the hash rate submitted by the remote sealer.
type hashrate struct {
	id   common.Hash
	ping time.Time
	rate uint64

	done chan struct{}
}

// sealWork wraps a seal work package for remote sealer.
type sealWork struct {
	errc chan error
	res  chan [4]string
}

func startRemoteSealer(blake3pow *Blake3pow, urls []string, noverify bool) *remoteSealer {
	ctx, cancel := context.WithCancel(context.Background())
	s := &remoteSealer{
		blake3pow:    blake3pow,
		noverify:     noverify,
		notifyURLs:   urls,
		notifyCtx:    ctx,
		cancelNotify: cancel,
		works:        make(map[common.Hash]*types.Header),
		rates:        make(map[common.Hash]hashrate),
		workCh:       make(chan *sealTask),
		fetchWorkCh:  make(chan *sealWork),
		submitWorkCh: make(chan *mineResult),
		fetchRateCh:  make(chan chan uint64),
		submitRateCh: make(chan *hashrate),
		requestExit:  make(chan struct{}),
		exitCh:       make(chan struct{}),
	}
	go s.loop()
	return s
}

func (s *remoteSealer) loop() {
	defer func() {
		s.blake3pow.logger.Trace("Blake3pow remote sealer is exiting")
		s.cancelNotify()
		s.reqWG.Wait()
		close(s.exitCh)
	}()

	nodeCtx := s.blake3pow.config.NodeLocation.Context()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case work := <-s.workCh:
			// Update current work with new received header.
			// Note same work can be past twice, happens when changing CPU threads.
			s.results = work.results
			s.makeWork(work.header)
			s.notifyWork()

		case work := <-s.fetchWorkCh:
			// Return current mining work to remote miner.
			if s.currentHeader == nil {
				work.errc <- errNoMiningWork
			} else {
				work.res <- s.currentWork
			}

		case result := <-s.submitWorkCh:
			// Verify submitted PoW solution based on maintained mining blocks.
			if s.submitWork(result.nonce, result.hash) {
				result.errc <- nil
			} else {
				result.errc <- errInvalidSealResult
			}

		case result := <-s.submitRateCh:
			// Trace remote sealer's hash rate by submitted value.
			s.rates[result.id] = hashrate{rate: result.rate, ping: time.Now()}
			close(result.done)

		case req := <-s.fetchRateCh:
			// Gather all hash rate submitted by remote sealer.
			var total uint64
			for _, rate := range s.rates {
				// this could overflow
				total += rate.rate
			}
			req <- total

		case <-ticker.C:
			// Clear stale submitted hash rate.
			for id, rate := range s.rates {
				if time.Since(rate.ping) > 10*time.Second {
					delete(s.rates, id)
				}
			}
			// Clear stale pending blocks
			if s.currentHeader != nil {
				for hash, header := range s.works {
					if header.NumberU64(nodeCtx)+staleThreshold <= s.currentHeader.NumberU64(nodeCtx) {
						delete(s.works, hash)
					}
				}
			}

		case <-s.requestExit:
			return
		}
	}
}

// makeWork creates a work package for external miner.
//
// The work package consists of 3 strings:
//
//	result[0], 32 bytes hex encoded current header pow-hash
//	result[1], 32 bytes hex encoded seed hash used for DAG
//	result[2], 32 bytes hex encoded boundary condition ("target"), 2^256/difficulty
//	result[3], hex encoded header number
func (s *remoteSealer) makeWork(header *types.Header) {
	nodeCtx := s.blake3pow.config.NodeLocation.Context()
	hash := header.SealHash()
	s.currentWork[0] = hash.Hex()
	s.currentWork[1] = hexutil.EncodeBig(header.Number(nodeCtx))
	s.currentWork[2] = common.BytesToHash(new(big.Int).Div(big2e256, header.Difficulty()).Bytes()).Hex()

	// Trace the seal work fetched by remote sealer.
	s.currentHeader = header
	s.works[hash] = header
}

// notifyWork notifies all the specified mining endpoints of the availability of
// new work to be processed.
func (s *remoteSealer) notifyWork() {
	work := s.currentWork

	// Encode the JSON payload of the notification. When NotifyFull is set,
	// this is the complete block header, otherwise it is a JSON array.
	var blob []byte
	if s.blake3pow.config.NotifyFull {
		blob, _ = json.Marshal(s.currentHeader)
	} else {
		blob, _ = json.Marshal(work)
	}

	s.reqWG.Add(len(s.notifyURLs))
	for _, url := range s.notifyURLs {
		go s.sendNotification(s.notifyCtx, url, blob, work)
	}
}

func (s *remoteSealer) sendNotification(ctx context.Context, url string, json []byte, work [4]string) {
	defer s.reqWG.Done()

	req, err := http.NewRequest("POST", url, bytes.NewReader(json))
	if err != nil {
		s.blake3pow.logger.WithField("err", err).Warn("Failed to create remote miner notification")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, remoteSealerTimeout)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.blake3pow.logger.WithField("err", err).Warn("Failed to notify remote miner")
	} else {
		s.blake3pow.logger.WithFields(log.Fields{
			"miner":  url,
			"hash":   work[0],
			"target": work[2],
		}).Trace("Notified remote miner")
		resp.Body.Close()
	}
}

// submitWork verifies the submitted pow solution, returning
// whether the solution was accepted or not (not can be both a bad pow as well as
// any other error, like no pending work or stale mining result).
func (s *remoteSealer) submitWork(nonce types.BlockNonce, sealhash common.Hash) bool {
	if s.currentHeader == nil {
		s.blake3pow.logger.WithField("sealhash", sealhash).Warn("Pending work without block")
		return false
	}
	nodeCtx := s.blake3pow.config.NodeLocation.Context()
	// Make sure the work submitted is present
	header := s.works[sealhash]
	if header == nil {
		s.blake3pow.logger.WithFields(log.Fields{
			"sealhash":  sealhash,
			"curnumber": s.currentHeader.NumberU64(nodeCtx),
		}).Warn("Work submitted but none pending")
		return false
	}
	// Verify the correctness of submitted result.
	header.SetNonce(nonce)

	start := time.Now()
	// Make sure the result channel is assigned.
	if s.results == nil {
		s.blake3pow.logger.Warn("Blake3pow result channel is empty, submitted mining result is rejected")
		return false
	}
	s.blake3pow.logger.WithFields(log.Fields{
		"sealhash": sealhash,
		"elapsed":  common.PrettyDuration(time.Since(start)),
	}).Trace("Verified correct proof-of-work")

	// Solutions seems to be valid, return to the miner and notify acceptance.
	solution := header

	// The submitted solution is within the scope of acceptance.
	if solution.NumberU64(nodeCtx)+staleThreshold > s.currentHeader.NumberU64(nodeCtx) {
		select {
		case s.results <- solution:
			s.blake3pow.logger.WithFields(log.Fields{
				"number":   solution.NumberU64(nodeCtx),
				"sealhash": sealhash,
				"hash":     solution.Hash(),
			}).Trace("Work submitted is acceptable")
			return true
		default:
			s.blake3pow.logger.WithField("sealhash", sealhash).Warn("Sealing result is not read by miner")
			return false
		}
	}
	// The submitted block is too old to accept, drop it.
	s.blake3pow.logger.WithFields(log.Fields{
		"number":   solution.NumberU64(nodeCtx),
		"sealhash": sealhash,
		"hash":     solution.Hash(),
	}).Warn("Work submitted is too old")
	return false
}
