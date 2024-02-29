package quai

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"math/big"
	"sync"
	"time"
)

const (
	// c_missingBlockChanSize is the size of channel listening to the MissingBlockEvent
	c_missingBlockChanSize = 60
	// c_checkNextPrimeBlockInterval is the interval for checking the next Block in Prime
	c_checkNextPrimeBlockInterval = 10 * time.Second
	// c_txsChanSize is the size of channel listening to the new txs event
	c_newTxsChanSize = 100
)

// handler manages the fetch requests from the core and tx pool also takes care of the tx broadcast
type handler struct {
	nodeLocation    common.Location
	p2pBackend      NetworkingAPI
	core            *core.Core
	missingBlockCh  chan types.BlockRequest
	missingBlockSub event.Subscription
	txsCh           chan core.NewTxsEvent
	txsSub          event.Subscription
	wg              sync.WaitGroup
	quitCh          chan struct{}
}

func newHandler(p2pBackend NetworkingAPI, core *core.Core, nodeLocation common.Location) *handler {
	handler := &handler{
		nodeLocation: nodeLocation,
		p2pBackend:   p2pBackend,
		core:         core,
		quitCh:       make(chan struct{}),
	}
	return handler
}

func (h *handler) Start() {
	h.wg.Add(1)
	h.missingBlockCh = make(chan types.BlockRequest, c_missingBlockChanSize)
	h.missingBlockSub = h.core.SubscribeMissingBlockEvent(h.missingBlockCh)
	go h.missingBlockLoop()

	nodeCtx := h.nodeLocation.Context()
	if nodeCtx == common.ZONE_CTX && h.core.ProcessingState() {
		h.wg.Add(1)
		h.txsCh = make(chan core.NewTxsEvent, c_newTxsChanSize)
		h.txsSub = h.core.SubscribeNewTxsEvent(h.txsCh)
		go h.txBroadcastLoop()
	}

	if nodeCtx == common.PRIME_CTX {
		h.wg.Add(1)
		go h.checkNextPrimeBlock()
	}
}

func (h *handler) Stop() {
	h.missingBlockSub.Unsubscribe() // quits missingBlockLoop
	nodeCtx := h.nodeLocation.Context()
	if nodeCtx == common.ZONE_CTX && h.core.ProcessingState() {
		h.txsSub.Unsubscribe() // quits the txBroadcastLoop
	}
	close(h.quitCh)
	h.wg.Wait()
}

// missingBlockLoop announces new pendingEtxs to connected peers.
func (h *handler) missingBlockLoop() {
	defer h.wg.Done()
	for {
		select {
		case blockRequest := <-h.missingBlockCh:
			go func() {
				resultCh := h.p2pBackend.Request(h.nodeLocation, blockRequest.Hash, &types.Block{})
				block := <-resultCh
				if block != nil {
					h.core.WriteBlock(block.(*types.Block))
				}
			}()
		case <-h.missingBlockSub.Err():
			return
		}
	}
}

// txBroadcastLoop announces new transactions to connected peers.
func (h *handler) txBroadcastLoop() {
	defer h.wg.Done()
	for {
		select {
		case event := <-h.txsCh:
			for _, tx := range event.Txs {
				err := h.p2pBackend.Broadcast(h.nodeLocation, tx)
				if err != nil {
					log.Global.Error("Error broadcasting transaction hash", tx.Hash(), err)
				}
			}
		case <-h.txsSub.Err():
			return
		}
	}
}

// checkNextPrimeBlock runs every c_checkNextPrimeBlockInterval and ask the peer for the next Block
func (h *handler) checkNextPrimeBlock() {
	defer h.wg.Done()
	checkNextPrimeBlockTimer := time.NewTicker(c_checkNextPrimeBlockInterval)
	defer checkNextPrimeBlockTimer.Stop()
	for {
		select {
		case <-checkNextPrimeBlockTimer.C:
			currentHeight := h.core.CurrentHeader().Number(h.nodeLocation.Context())
			go func() {
				resultCh := h.p2pBackend.Request(h.nodeLocation, new(big.Int).Add(currentHeight, big.NewInt(1)), common.Hash{})
				data := <-resultCh
				// If we find a new hash for the requested block number we can check
				// first if we already have the block in the database otherwise ask the
				// peers for it
				if data != nil {
					blockHash, ok := data.(common.Hash)
					if ok {
						block := h.core.GetBlockByHash(blockHash)
						// If the blockHash for the asked number is not present in the
						// appended database we ask the peer for the block with this hash
						if block == nil {
							go func() {
								resultCh := h.p2pBackend.Request(h.nodeLocation, blockHash, &types.Block{})
								block := <-resultCh
								if block != nil {
									h.core.WriteBlock(block.(*types.Block))
								}
							}()
						}
					}
				}
			}()
		case <-h.quitCh:
			return
		}
	}
}
