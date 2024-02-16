package quai

import (
	"math/big"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/quai/downloader"
)

const (
	// c_missingBlockChanSize is the size of channel listening to the MissingBlockEvent
	c_missingBlockChanSize = 60
	// c_checkNextPrimeBlockInterval is the interval for checking the next Block in Prime
	c_checkNextPrimeBlockInterval = 60 * time.Second
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

	wg     sync.WaitGroup
	quitCh chan struct{}

	snapSyncCh  chan core.SnapSyncStartEvent
	snapSyncSub event.Subscription
	d           *downloader.Downloader
}

func newHandler(p2pBackend NetworkingAPI, core *core.Core, nodeLocation common.Location, chainDb ethdb.Database) *handler {
	quitCh := make(chan struct{})
	d := downloader.NewDownloader(p2pBackend, chainDb, quitCh)
	handler := &handler{
		nodeLocation: nodeLocation,
		p2pBackend:   p2pBackend,
		core:         core,
		quitCh:       quitCh,
		d:            d,
	}
	return handler
}

func (h *handler) Start() {
	h.wg.Add(1)
	h.missingBlockCh = make(chan types.BlockRequest, c_missingBlockChanSize)
	h.missingBlockSub = h.core.SubscribeMissingBlockEvent(h.missingBlockCh)
	go h.missingBlockLoop()

	h.snapSyncCh = make(chan core.SnapSyncStartEvent)
	h.snapSyncSub = h.core.SubscribeSnapSyncStartEvent(h.snapSyncCh)
	h.wg.Add(1)
	go h.snapSyncLoop()

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

// snapSyncLoop starts a snapSync process every time a SnapSyncStartEvent is received
func (h *handler) snapSyncLoop() {
	defer h.wg.Done()

	for {
		select {
		case event := <-h.snapSyncCh:
			h.startSnapSync(big.NewInt(int64(event.BlockNumber)))
		case <-h.snapSyncSub.Err():
			log.Global.Error("snapSyncSub error")
			return
		case <-h.quitCh:
			return
		}
	}
}

func (h *handler) startSnapSync(blockNumber *big.Int) {
	log.Global.Infof("starting snapshot sync for location %s and block %d", h.nodeLocation.Name(), blockNumber)
	err := h.d.StartSnapSync(h.nodeLocation, blockNumber)
	if err != nil {
		panic(err)
	}
	log.Global.Infof("snapshot sync for location %s and block %d finished succesfully", h.nodeLocation.Name(), blockNumber)
}
