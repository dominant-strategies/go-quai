package quai

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/params"
	expireLru "github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	// c_missingBlockChanSize is the size of channel listening to the MissingBlockEvent
	c_missingBlockChanSize = 1024
	// c_checkNextPrimeBlockInterval is the interval for checking the next Block in Prime
	c_checkNextPrimeBlockInterval = 10 * time.Second
	// c_checkNextBlockInterval is the interval for checking the next Block in Region/Zone
	c_checkNextBlockInterval = 2 * time.Second
	// c_checkNextBlockIdleInterval is used when no catch-up work is needed.
	c_checkNextBlockIdleInterval = 10 * time.Second
	// c_checkNextBlockMaxInterval caps remote recovery backoff.
	c_checkNextBlockMaxInterval = 30 * time.Second
	// c_checkNextBlockStallThreshold is how long the local head may sit unchanged before catch-up requests are allowed.
	c_checkNextBlockStallThreshold = 15 * time.Second
	// c_catchupNearTipDepth is the observed-head gap below which recovery uses conservative peer fanout.
	c_catchupNearTipDepth = 32
	// c_catchupRequestDegree is used only while the node has evidence it is materially behind.
	c_catchupRequestDegree = 8
	// c_nearTipRequestDegree keeps near-tip recovery network-friendly.
	c_nearTipRequestDegree = 3
	// c_recentBlockReqCache is the size of the cache for the recent block requests
	c_recentBlockReqCache = 1000
	// c_recentBlockReqTimeout is the timeout for the recent block requests cache
	c_recentBlockReqTimeout = 1 * time.Minute
	// c_primeBlockSyncDepth is how far back the prime block downloading will start
	c_primeBlockSyncDepth = 2000
	// c_localNextBlockPromotionLimit caps local recovery promotions per check tick.
	c_localNextBlockPromotionLimit = 250
	// c_remoteRecoveryMaxBatches bounds remote catch-up work per pass.
	c_remoteRecoveryMaxBatches = 3
)

var (
	ErrBlockAlreadyAppended = errors.New("block has already been appended")
	catchupGauges           = metrics_config.NewGaugeVec("CatchupGauges", "Catch-up recovery gauges")
)

// handler manages the fetch requests from the core and tx pool also takes care of the tx broadcast
type handler struct {
	nodeLocation    common.Location
	p2pBackend      NetworkingAPI
	core            *core.Core
	missingBlockCh  chan types.BlockRequest
	missingBlockSub event.Subscription
	wg              sync.WaitGroup
	quitCh          chan struct{}
	logger          *log.Logger

	txs types.Transactions

	recentBlockReqCache *expireLru.LRU[common.Hash, interface{}] // cache the latest requests on a 1 min timer

	lastHeadNumber         uint64
	lastHeadChange         time.Time
	remoteRecoveryFailures int

	ctx        context.Context
	cancelFunc context.CancelFunc
}

func newHandler(p2pBackend NetworkingAPI, core *core.Core, nodeLocation common.Location, logger *log.Logger) *handler {
	ctx, cancel := context.WithCancel(context.Background())
	handler := &handler{
		nodeLocation: nodeLocation,
		p2pBackend:   p2pBackend,
		core:         core,
		quitCh:       make(chan struct{}),
		logger:       logger,
		txs:          make(types.Transactions, 0),
		ctx:          ctx,
		cancelFunc:   cancel,
	}
	handler.recentBlockReqCache = expireLru.NewLRU[common.Hash, interface{}](c_recentBlockReqCache, nil, c_recentBlockReqTimeout)
	return handler
}

func (h *handler) Start() {
	h.wg.Add(1)
	h.missingBlockCh = make(chan types.BlockRequest, c_missingBlockChanSize)
	h.missingBlockSub = h.core.SubscribeMissingBlockEvent(h.missingBlockCh)
	go h.missingBlockLoop()

	nodeCtx := h.nodeLocation.Context()
	if nodeCtx == common.PRIME_CTX {
		h.wg.Add(1)
		go h.checkNextPrimeBlock()
	} else {
		h.wg.Add(1)
		go h.checkNextBlock()
	}
}

func (h *handler) Stop() {
	h.cancelFunc()
	h.missingBlockSub.Unsubscribe() // quits missingBlockLoop
	close(h.quitCh)
	h.wg.Wait()
	h.logger.Info("quai handler stopped")
}

// missingBlockLoop announces new pendingEtxs to connected peers.
func (h *handler) missingBlockLoop() {
	defer func() {
		if r := recover(); r != nil {
			h.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	defer h.wg.Done()

	for {
		select {
		case blockRequest := <-h.missingBlockCh:

			// If the blockRequest Hash is a bad block hash, node should not ask
			// any peer for the hash
			if h.core.IsBlockHashABadHash(blockRequest.Hash) {
				continue
			}

			// get the current header and compare the entropy of the block that
			// is getting fetched with the current header entropy
			currentHeader := h.core.CurrentHeader()

			if !h.core.IsGenesisHash(currentHeader.Hash()) && currentHeader != nil && currentHeader.NumberU64(common.ZONE_CTX) > params.MaxCodeSizeForkHeight {
				currentHeaderPowHash, err := h.core.VerifySeal(currentHeader.WorkObjectHeader())
				if err != nil {
					continue
				}
				currentHeaderIntrinsic := common.IntrinsicLogEntropy(currentHeaderPowHash)
				currentS := h.core.CurrentHeader().ParentEntropy(h.core.NodeCtx())
				MaxAllowableEntropyDist := new(big.Int).Mul(currentHeaderIntrinsic, new(big.Int).SetUint64(params.MaxAllowableEntropyDist))

				// If someone is mining not within MaxAllowableEntropyDist*currentIntrinsicS dont broadcast
				if currentS.Cmp(new(big.Int).Add(blockRequest.Entropy, MaxAllowableEntropyDist)) > 0 {
					continue
				}
			}

			_, exists := h.recentBlockReqCache.Get(blockRequest.Hash)
			if !exists {
				// Add the block request to the cache to avoid requesting the same block multiple times
				h.recentBlockReqCache.Add(blockRequest.Hash, true)
			} else {
				// Don't ask for the same block multiple times within a min window
				continue
			}

			go func() {
				defer func() {
					if r := recover(); r != nil {
						h.logger.WithFields(log.Fields{
							"error":      r,
							"stacktrace": string(debug.Stack()),
						}).Fatal("Go-Quai Panicked")
					}
				}()
				if !h.core.ProcessingState() && h.nodeLocation.Context() == common.ZONE_CTX {
					resultCh := h.p2pBackend.Request(h.nodeLocation, blockRequest.Hash, &types.WorkObjectHeaderView{})
					block := firstNonNilResult(resultCh)
					if block != nil {
						h.core.WriteBlock(block.(*types.WorkObjectHeaderView).WorkObject)
					}
				} else {
					resultCh := h.p2pBackend.Request(h.nodeLocation, blockRequest.Hash, &types.WorkObjectBlockView{})
					block := firstNonNilResult(resultCh)
					if block != nil {
						h.core.WriteBlock(block.(*types.WorkObjectBlockView).WorkObject)
					}
				}
				h.recentBlockReqCache.Remove(blockRequest.Hash)
			}()
		case <-h.missingBlockSub.Err():
			return
		case <-h.quitCh:
			return
		}
	}
}

func firstNonNilResult(resultCh <-chan interface{}) interface{} {
	for result := range resultCh {
		if result != nil {
			return result
		}
	}
	return nil
}

// checkNextBlock runs every c_checkNextBlockInterval and asks peers for the next local block by number.
func (h *handler) checkNextBlock() {
	defer func() {
		if r := recover(); r != nil {
			h.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	defer h.wg.Done()

	checkNextBlockTimer := time.NewTimer(jitterDuration(c_checkNextBlockInterval))
	defer checkNextBlockTimer.Stop()
	for {
		select {
		case <-checkNextBlockTimer.C:
			attempted, progressed := h.requestNextBlock()
			h.recordRecoveryAttempt(attempted, progressed)
			checkNextBlockTimer.Reset(h.nextBlockCheckDelay(attempted, progressed))
		case <-h.quitCh:
			return
		}
	}
}

func (h *handler) requestNextBlock() (bool, bool) {
	if h.ctx.Err() != nil {
		return false, false
	}

	nodeCtx := h.nodeLocation.Context()
	currentHeader := h.core.CurrentHeader()
	if currentHeader == nil {
		return false, false
	}
	if h.promoteLocalNextBlocks(currentHeader, nodeCtx) {
		return false, true
	}
	if !h.shouldRunCatchupRecovery(currentHeader) {
		return false, false
	}

	return true, h.requestRemoteNextBlocks(currentHeader, nodeCtx)
}

func (h *handler) shouldRunCatchupRecovery(currentHeader *types.WorkObject) bool {
	now := time.Now()
	nodeCtx := h.nodeLocation.Context()
	currentNumber := currentHeader.NumberU64(nodeCtx)
	if h.lastHeadChange.IsZero() {
		h.lastHeadChange = now
		h.lastHeadNumber = currentNumber
	}
	if currentNumber != h.lastHeadNumber {
		h.lastHeadNumber = currentNumber
		h.lastHeadChange = now
		h.remoteRecoveryFailures = 0
	}

	highestSeen := h.core.HighestSeenBlockNumber()
	if highestSeen < currentNumber {
		highestSeen = currentNumber
	}
	observedGap := highestSeen - currentNumber
	appendQueueLen := h.core.AppendQueueLen()
	stalled := now.Sub(h.lastHeadChange) >= c_checkNextBlockStallThreshold
	behind := observedGap > c_catchupNearTipDepth
	active := behind || stalled || (appendQueueLen > 0 && stalled)

	activeValue := 0.0
	if active {
		activeValue = 1
	}
	catchupGauges.WithLabelValues("active").Set(activeValue)
	catchupGauges.WithLabelValues("observed_head_gap").Set(float64(observedGap))
	catchupGauges.WithLabelValues("append_queue").Set(float64(appendQueueLen))
	catchupGauges.WithLabelValues("remote_failures").Set(float64(h.remoteRecoveryFailures))

	return active
}

func (h *handler) requestRemoteNextBlocks(currentHeader *types.WorkObject, nodeCtx int) bool {
	expectedNumber := new(big.Int).Add(currentHeader.Number(nodeCtx), big.NewInt(1))
	parentHash := currentHeader.Hash()
	requestDegree := h.catchupRequestDegree(currentHeader)
	catchupGauges.WithLabelValues("request_degree").Set(float64(requestDegree))

	recovered := make([]*types.WorkObject, 0, c_remoteRecoveryMaxBatches*protocol.C_NumPrimeBlocksToDownload)
	for batch := 0; batch < c_remoteRecoveryMaxBatches; batch++ {
		resultCh := h.p2pBackend.RequestWithDegree(h.nodeLocation, new(big.Int).Set(expectedNumber), []*types.WorkObjectBlockView{}, requestDegree)
		batchBlocks, ok := h.firstValidNextBlockBatch(resultCh, expectedNumber, parentHash, nodeCtx)
		if !ok {
			break
		}
		for _, block := range batchBlocks {
			recovered = append(recovered, block)
			parentHash = block.Hash()
			expectedNumber.Add(expectedNumber, big.NewInt(1))
		}
		if len(batchBlocks) < protocol.C_NumPrimeBlocksToDownload {
			break
		}
	}

	if len(recovered) == 0 {
		if block := h.requestSingleNextBlock(currentHeader, nodeCtx, requestDegree); block != nil {
			recovered = append(recovered, block)
		}
	}
	if len(recovered) == 0 {
		h.logger.WithField("number", new(big.Int).Add(currentHeader.Number(nodeCtx), big.NewInt(1))).Debug("no peer returned a usable next block")
		return false
	}

	for _, block := range recovered {
		h.core.WriteBlock(block)
	}
	h.logger.WithFields(log.Fields{
		"count":       len(recovered),
		"firstNumber": recovered[0].Number(nodeCtx),
		"lastNumber":  recovered[len(recovered)-1].Number(nodeCtx),
		"degree":      requestDegree,
	}).Info("wrote recovered next blocks")
	return true
}

func (h *handler) catchupRequestDegree(currentHeader *types.WorkObject) int {
	currentNumber := currentHeader.NumberU64(h.nodeLocation.Context())
	highestSeen := h.core.HighestSeenBlockNumber()
	if highestSeen > currentNumber+c_catchupNearTipDepth {
		return c_catchupRequestDegree
	}
	return c_nearTipRequestDegree
}

func (h *handler) firstValidNextBlockBatch(resultCh <-chan interface{}, expectedNumber *big.Int, parentHash common.Hash, nodeCtx int) ([]*types.WorkObject, bool) {
	for result := range resultCh {
		if result == nil {
			continue
		}
		workObjects, ok := result.([]*types.WorkObjectBlockView)
		if !ok {
			h.logger.WithFields(log.Fields{
				"number": expectedNumber,
				"type":   fmt.Sprintf("%T", result),
			}).Warn("unexpected next block batch response type")
			continue
		}
		blocks, ok := h.validateNextBlockBatch(workObjects, expectedNumber, parentHash, nodeCtx)
		if ok {
			return blocks, true
		}
	}
	return nil, false
}

func (h *handler) validateNextBlockBatch(workObjects []*types.WorkObjectBlockView, expectedNumber *big.Int, parentHash common.Hash, nodeCtx int) ([]*types.WorkObject, bool) {
	if len(workObjects) == 0 {
		return nil, false
	}
	number := new(big.Int).Set(expectedNumber)
	parent := parentHash
	blocks := make([]*types.WorkObject, 0, len(workObjects))
	for _, wo := range workObjects {
		if wo == nil || wo.WorkObject == nil {
			h.logger.WithField("number", number).Warn("received nil work object in next block batch")
			return nil, false
		}
		workObject := wo.WorkObject
		if workObject.Number(nodeCtx).Cmp(number) != 0 || workObject.ParentHash(nodeCtx) != parent {
			h.logger.WithFields(log.Fields{
				"expectedNumber": number,
				"actualNumber":   workObject.Number(nodeCtx),
				"expectedParent": parent,
				"actualParent":   workObject.ParentHash(nodeCtx),
				"hash":           workObject.Hash(),
			}).Debug("next block batch failed continuity check")
			return nil, false
		}
		blocks = append(blocks, workObject)
		parent = workObject.Hash()
		number.Add(number, big.NewInt(1))
	}
	return blocks, true
}

func (h *handler) requestSingleNextBlock(currentHeader *types.WorkObject, nodeCtx int, requestDegree int) *types.WorkObject {
	nextNumber := new(big.Int).Add(currentHeader.Number(nodeCtx), big.NewInt(1))
	resultCh := h.p2pBackend.RequestWithDegree(h.nodeLocation, nextNumber, &types.WorkObjectBlockView{}, requestDegree)
	for result := range resultCh {
		if result == nil {
			continue
		}
		blockView, ok := result.(*types.WorkObjectBlockView)
		if !ok {
			h.logger.WithFields(log.Fields{
				"number": nextNumber,
				"type":   fmt.Sprintf("%T", result),
			}).Warn("unexpected next block response type")
			continue
		}
		workObject := blockView.WorkObject
		if workObject == nil || workObject.Number(nodeCtx).Cmp(nextNumber) != 0 || workObject.ParentHash(nodeCtx) != currentHeader.Hash() {
			h.logger.WithFields(log.Fields{
				"number":         nextNumber,
				"expectedParent": currentHeader.Hash(),
			}).Debug("next block failed continuity check")
			continue
		}
		return workObject
	}
	return nil
}

func (h *handler) recordRecoveryAttempt(attempted bool, progressed bool) {
	if progressed {
		h.remoteRecoveryFailures = 0
		return
	}
	if attempted {
		h.remoteRecoveryFailures++
	}
}

func (h *handler) nextBlockCheckDelay(attempted bool, progressed bool) time.Duration {
	if !attempted && !progressed {
		return jitterDuration(c_checkNextBlockIdleInterval)
	}
	delay := c_checkNextBlockInterval
	for i := 0; i < h.remoteRecoveryFailures && delay < c_checkNextBlockMaxInterval; i++ {
		delay *= 2
	}
	if delay > c_checkNextBlockMaxInterval {
		delay = c_checkNextBlockMaxInterval
	}
	return jitterDuration(delay)
}

func jitterDuration(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	jitter := int64(base / 5)
	if jitter == 0 {
		return base
	}
	return base - time.Duration(jitter/2) + time.Duration(rand.Int63n(jitter+1))
}

func (h *handler) promoteLocalNextBlocks(currentHeader *types.WorkObject, nodeCtx int) bool {
	parentHash := currentHeader.Hash()
	nextNumber := currentHeader.NumberU64(nodeCtx) + 1
	promoted := 0
	firstNumber := uint64(0)
	lastNumber := uint64(0)

	for promoted < c_localNextBlockPromotionLimit {
		var nextBlock *types.WorkObject
		for _, block := range h.core.GetBlocksOrCandidatesByNumber(nextNumber) {
			if block != nil && block.ParentHash(nodeCtx) == parentHash {
				nextBlock = block
				break
			}
		}
		if nextBlock == nil {
			break
		}

		if _, err := h.core.InsertChain([]*types.WorkObject{nextBlock}); err != nil && err.Error() != core.ErrKnownBlock.Error() {
			h.logger.WithFields(log.Fields{
				"number": nextBlock.Number(nodeCtx),
				"hash":   nextBlock.Hash(),
				"err":    err,
			}).Warn("local next block promotion append failed")
			return promoted > 0
		}
		if _, err := h.core.GeneratePendingHeader(nextBlock, false); err != nil {
			h.logger.WithFields(log.Fields{
				"number": nextBlock.Number(nodeCtx),
				"hash":   nextBlock.Hash(),
				"err":    err,
			}).Warn("local next block promotion head update failed")
			return promoted > 0
		}

		if promoted == 0 {
			firstNumber = nextBlock.NumberU64(nodeCtx)
		}
		lastNumber = nextBlock.NumberU64(nodeCtx)
		promoted++
		parentHash = nextBlock.Hash()
		nextNumber++
	}

	if promoted > 0 {
		h.logger.WithFields(log.Fields{
			"count":       promoted,
			"firstNumber": firstNumber,
			"lastNumber":  lastNumber,
		}).Info("promoted local next blocks")
	}

	return promoted > 0
}

// checkNextPrimeBlock runs every c_checkNextPrimeBlockInterval and ask the peer for the next Block
func (h *handler) checkNextPrimeBlock() {
	defer func() {
		if r := recover(); r != nil {
			h.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	defer h.wg.Done()

	checkNextPrimeBlockTimer := time.NewTicker(c_checkNextPrimeBlockInterval)
	defer checkNextPrimeBlockTimer.Stop()
	for {
		select {
		case <-checkNextPrimeBlockTimer.C:

			h.wg.Add(1)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						h.logger.WithFields(log.Fields{
							"error":      r,
							"stacktrace": string(debug.Stack()),
						}).Fatal("Go-Quai Panicked")
					}
				}()
				defer h.wg.Done()

				if h.ctx.Err() != nil {
					return
				}

				// Start of the downloading process happens from the tip of the
				// prime chain, Going back 10 blocks at a time and checking
				// until we reach a point where we already have appended the
				// block, then ask the next 20 prime blocks
				currentHeight := h.core.CurrentHeader().Number(h.nodeLocation.Context())
				syncHeight := new(big.Int).Set(currentHeight)
				for i := 0; i < c_primeBlockSyncDepth; i += protocol.C_NumPrimeBlocksToDownload {
					h.logger.Info("Downloading prime blocks from syncHeight ", syncHeight)

					if h.ctx.Err() != nil {
						return
					}
					// the prime block on this try already existed in the database
					if err := h.GetNextPrimeBlock(syncHeight); err != nil {
						// If i > 2 * protocol.C_NumPrimeBlocksToDownload that
						// means the blocks that the node wanted has alreay been
						// downloaded otherwise, download next 2 *
						// protocol.C_NumPrimeBlocksToDownload
						if i < 2*protocol.C_NumPrimeBlocksToDownload {
							h.GetNextPrimeBlock(syncHeight.Add(syncHeight, big.NewInt(protocol.C_NumPrimeBlocksToDownload)))
							h.GetNextPrimeBlock(syncHeight.Add(new(big.Int).Add(syncHeight, big.NewInt(protocol.C_NumPrimeBlocksToDownload)), big.NewInt(protocol.C_NumPrimeBlocksToDownload)))
						}
						break
					}
					syncHeight.Sub(syncHeight, big.NewInt(protocol.C_NumPrimeBlocksToDownload))
					if syncHeight.Sign() == -1 {
						break
					}
				}

			}()
		case <-h.quitCh:
			return
		}
	}
}

func (h *handler) GetNextPrimeBlock(number *big.Int) error {
	// If the blockHash for the asked number is not present in the
	// appended database we ask the peer for the block with this hash
	resultCh := h.p2pBackend.Request(h.nodeLocation, new(big.Int).Add(number, big.NewInt(1)), []*types.WorkObjectBlockView{})
	blocks := firstNonNilResult(resultCh)
	if blocks != nil {
		// peer returns a slice of blocks from the requested number
		workObjects := blocks.([]*types.WorkObjectBlockView)
		if len(workObjects) != protocol.C_NumPrimeBlocksToDownload {
			h.logger.Error("did not get expected number of workobjects in prime")
			return nil
		}
		var parent *types.WorkObject
		for i, wo := range workObjects {
			if wo == nil {
				h.logger.Error("one of the work objects is nil")
				return nil
			}
			workObject := wo.WorkObject
			// Check that all the prime blocks form a continous chain of blocks
			if i != 0 {
				if workObject.ParentHash(common.PRIME_CTX) != parent.Hash() {
					h.logger.Error("downloaded non continous chain of prime blocks")
					return nil
				}
			}
			parent = workObject
		}

		// Write all the blocks are the sanity check into the database and
		// add it to the append queue
		for _, wo := range workObjects {
			// If the work object is already on chain, return a error back and start the sync from that point
			block := h.core.GetBlockByHash(wo.WorkObjectHeader().Hash())
			if block != nil {
				return ErrBlockAlreadyAppended
			} else {
				h.core.WriteBlock(wo.WorkObject)
			}
		}
	}
	return nil
}
