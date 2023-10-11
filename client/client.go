package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/dominant-strategies/go-quai/log"
	p2pnode "github.com/dominant-strategies/go-quai/p2p/node"

	"github.com/libp2p/go-libp2p/core/event"
)

type P2PClient struct {
	node       *p2pnode.P2PNode
	httpServer *http.Server
	ctx        context.Context
}

func NewClient(ctx context.Context, node *p2pnode.P2PNode) *P2PClient {
	client := &P2PClient{
		node: node,
		ctx:  ctx,
	}

	client.node.SetStreamHandler(myProtocol, client.handleStream)
	return client
}

// subscribes to the event bus to listen for specific events
func (c *P2PClient) ListenForEvents() {
	subAddrUpdated, err := c.node.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		log.Fatalf("Failed to subscribe to address change events: %s", err)
	}
	defer subAddrUpdated.Close()

	subPeerConnected, err := c.node.EventBus().Subscribe(new(event.EvtPeerConnectednessChanged))
	if err != nil {
		log.Fatalf("Failed to subscribe to peer connectedness events: %s", err)
	}
	defer subPeerConnected.Close()

	for {
		select {
		case evt := <-subAddrUpdated.Out():
			if e, ok := evt.(event.EvtLocalAddressesUpdated); ok {
				for _, addr := range e.Current {
					fullAddr := fmt.Sprintf("%+v/p2p/%s", addr, c.node.ID().Pretty())
					log.Debugf("Advertised Address changed: %s", fullAddr)
				}
			}
		case evt := <-subPeerConnected.Out():
			if e, ok := evt.(event.EvtPeerConnectednessChanged); ok {
				log.Tracef("Peer %s is now %s", e.Peer.String(), e.Connectedness)
			}
		case <-c.ctx.Done():
			log.Warnf("Context cancel received. Stopping event listener")
			return
		}
	}
}
