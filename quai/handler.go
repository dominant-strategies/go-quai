package quai

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"sync"
)

const (
	// missingBlockChanSize is the size of channel listening to the MissingBlockEvent
	missingBlockChanSize = 60
)

// handler manages the fetch requests from the core and tx pool also takes care of the tx broadcast
type handler struct {
	nodeLocation    common.Location
	p2pBackend      NetworkingAPI
	core            *core.Core
	missingBlockCh  chan types.BlockRequest
	missingBlockSub event.Subscription
	wg              sync.WaitGroup
}

func newHandler(p2pBackend NetworkingAPI, core *core.Core, nodeLocation common.Location) *handler {
	handler := &handler{
		nodeLocation: nodeLocation,
		p2pBackend:   p2pBackend,
		core:         core,
	}
	return handler
}

func (h *handler) Start() {
	h.wg.Add(1)
	h.missingBlockCh = make(chan types.BlockRequest, missingBlockChanSize)
	h.missingBlockSub = h.core.SubscribeMissingBlockEvent(h.missingBlockCh)
	go h.missingBlockLoop()
}

func (h *handler) Stop() {
	h.missingBlockSub.Unsubscribe() // quits missingBlockLoop
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
