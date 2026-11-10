package quai

import (
	"context"
	"errors"
	"fmt"
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
	// c_primeSyncMaxDifficultyDrop is how many times lower than the current
	// header difficulty a downloaded prime block difficulty can be. A peer can
	// otherwise send prime blocks with a trivial difficulty that are cheap to
	// mine and are written to the database before full validation.
	c_primeSyncMaxDifficultyDrop = 16
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
			}).Error("Go-Quai Panicked")
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

			if currentHeader != nil && !h.core.IsGenesisHash(currentHeader.Hash()) && currentHeader.NumberU64(common.ZONE_CTX) > params.MaxCodeSizeForkHeight {
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
						}).Error("Go-Quai Panicked")
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
			}).Error("Go-Quai Panicked")
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
						}).Error("Go-Quai Panicked")
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
							nextBatch := new(big.Int).Add(syncHeight, big.NewInt(protocol.C_NumPrimeBlocksToDownload))
							h.GetNextPrimeBlock(nextBatch)
							secondBatch := new(big.Int).Add(syncHeight, big.NewInt(2*protocol.C_NumPrimeBlocksToDownload))
							h.GetNextPrimeBlock(secondBatch)
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

type primeBlockStore interface {
	GetBlockByHash(common.Hash) *types.WorkObject
	GetCanonicalHash(uint64) common.Hash
	ReemitChainEventForHC(*types.WorkObject) error
	WriteBlock(*types.WorkObject)
}

// applyPrimeBlock only stops at a canonical block whose full body is stored.
// The canonical header index alone does not establish that the block can be
// replayed or used as the local synchronization boundary.
func applyPrimeBlock(store primeBlockStore, block *types.WorkObject) (alreadyCanonical, replayed bool, err error) {
	blockHash := block.WorkObjectHeader().Hash()
	storedBlock := store.GetBlockByHash(blockHash)
	if storedBlock != nil {
		if store.GetCanonicalHash(block.NumberU64(common.PRIME_CTX)) == blockHash {
			return true, false, nil
		}
		return false, true, store.ReemitChainEventForHC(storedBlock)
	}
	store.WriteBlock(block)
	return false, false, nil
}

func (h *handler) GetNextPrimeBlock(number *big.Int) error {
	// Ask peers for the batch that starts after number.
	firstNumber := new(big.Int).Add(number, big.NewInt(1))
	resultCh := requestPrimeSyncBlocks(h.p2pBackend, h.core, h.nodeLocation, firstNumber)
	blocks := <-resultCh
	if blocks != nil {
		// The peer returned a validated slice of blocks from the requested number.
		workObjects := blocks.([]*types.WorkObjectBlockView)

		// Write validated blocks to the database and append queue.
		for _, wo := range workObjects {
			block := wo.WorkObject
			blockHash := block.WorkObjectHeader().Hash()
			blockNumber := block.NumberU64(common.PRIME_CTX)

			// A block can have been fully appended to the graph without being
			// selected as the canonical head. This commonly survives a restart,
			// while the coordinator's in-memory event history does not. Treat only
			// the canonical hash as synchronized. Replay stored non-canonical
			// blocks so the new coordinator can reconsider them.
			alreadyCanonical, replayed, err := applyPrimeBlock(h.core, block)
			if alreadyCanonical {
				return ErrBlockAlreadyAppended
			}
			if replayed {
				h.logger.WithFields(log.Fields{
					"hash":   blockHash,
					"number": blockNumber,
				}).Info("Replaying stored non-canonical prime block")
				if err != nil {
					h.logger.WithFields(log.Fields{
						"err":  err,
						"hash": blockHash,
					}).Warn("Failed to replay stored non-canonical prime block")
				}
			}
		}
	}
	return nil
}

func requestPrimeSyncBlocks(backend NetworkingAPI, chain primeSyncChain, location common.Location, firstNumber *big.Int) chan interface{} {
	return backend.RequestValidated(location, firstNumber, []*types.WorkObjectBlockView{}, func(response interface{}) error {
		workObjects, ok := response.([]*types.WorkObjectBlockView)
		if !ok {
			return fmt.Errorf("prime sync response has type %T", response)
		}
		return validatePrimeSyncBlocks(chain, firstNumber, workObjects)
	})
}

// primeSyncChain is the part of the core that validatePrimeSyncBlocks uses.
type primeSyncChain interface {
	CurrentHeader() *types.WorkObject
	SanityCheckWorkObjectBlockViewBody(wo *types.WorkObject) error
	CalcOrder(wo *types.WorkObject) (*big.Int, int, error)
}

// validatePrimeSyncBlocks checks the prime blocks that a peer returned for a
// request by number, before they are written to the database. The checks run
// from the cheapest to the most expensive.
func validatePrimeSyncBlocks(chain primeSyncChain, firstNumber *big.Int, workObjects []*types.WorkObjectBlockView) error {
	if len(workObjects) != protocol.C_NumPrimeBlocksToDownload {
		return fmt.Errorf("got %d prime blocks, want %d", len(workObjects), protocol.C_NumPrimeBlocksToDownload)
	}
	currentHeader := chain.CurrentHeader()
	if currentHeader == nil || currentHeader.Difficulty() == nil {
		return errors.New("no current header to compare the difficulty with")
	}
	minDifficulty := new(big.Int).Div(currentHeader.Difficulty(), big.NewInt(c_primeSyncMaxDifficultyDrop))

	var parent *types.WorkObject
	for i, wo := range workObjects {
		if wo == nil || wo.WorkObject == nil || wo.WorkObjectHeader() == nil || wo.Header() == nil {
			return fmt.Errorf("prime block %d is incomplete", i)
		}
		workObject := wo.WorkObject
		// The blocks must be the requested numbers, in order
		wantNumber := new(big.Int).Add(firstNumber, big.NewInt(int64(i)))
		if workObject.Number(common.PRIME_CTX).Cmp(wantNumber) != 0 {
			return fmt.Errorf("prime block %d has number %v, want %v", i, workObject.Number(common.PRIME_CTX), wantNumber)
		}
		// Check that all the prime blocks form a continous chain of blocks
		if parent != nil && workObject.ParentHash(common.PRIME_CTX) != parent.Hash() {
			return errors.New("downloaded non continous chain of prime blocks")
		}
		if workObject.Difficulty() == nil || workObject.Difficulty().Cmp(minDifficulty) < 0 {
			return fmt.Errorf("prime block %d difficulty %v is below the minimum %v", i, workObject.Difficulty(), minDifficulty)
		}
		if err := chain.SanityCheckWorkObjectBlockViewBody(workObject); err != nil {
			return fmt.Errorf("prime block %d failed the sanity check: %w", i, err)
		}
		// CalcOrder verifies the seal, and the block must be a prime block
		_, order, err := chain.CalcOrder(workObject)
		if err != nil {
			return fmt.Errorf("prime block %d order: %w", i, err)
		}
		if order != common.PRIME_CTX {
			return fmt.Errorf("prime block %d has order %d, want prime", i, order)
		}
		parent = workObject
	}
	return nil
}
