package quai

import (
	"context"
	"errors"
	"math/big"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/params"
	expireLru "github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	// c_missingBlockChanSize is the size of channel listening to the MissingBlockEvent
	c_missingBlockChanSize = 60
	// c_checkNextPrimeBlockInterval is the interval for checking the next Block in Prime
	c_checkNextPrimeBlockInterval = 10 * time.Second
	// c_recentBlockReqCache is the size of the cache for the recent block requests
	c_recentBlockReqCache = 1000
	// c_recentBlockReqTimeout is the timeout for the recent block requests cache
	c_recentBlockReqTimeout = 1 * time.Minute
	// c_primeBlockSyncDepth is how far back the prime block downloading will start
	c_primeBlockSyncDepth = 2000
)

var (
	ErrBlockAlreadyAppended = errors.New("block has already been appended")
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
					block := <-resultCh
					if block != nil {
						h.core.WriteBlock(block.(*types.WorkObjectHeaderView).WorkObject)
					}
				} else {
					resultCh := h.p2pBackend.Request(h.nodeLocation, blockRequest.Hash, &types.WorkObjectBlockView{})
					block := <-resultCh
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
	blocks := <-resultCh
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
