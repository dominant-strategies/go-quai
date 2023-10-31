package node

import (
	"fmt"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/event"
)

// subscribes to the event bus and handles libp2p events as they're received
func (p *P2PNode) eventLoop() {
	subAddrUpdated, err := p.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		log.Fatalf("Failed to subscribe to address change events: %s", err)
	}
	defer subAddrUpdated.Close()

	subPeerConnected, err := p.EventBus().Subscribe(new(event.EvtPeerConnectednessChanged))
	if err != nil {
		log.Fatalf("Failed to subscribe to peer connectedness events: %s", err)
	}
	defer subPeerConnected.Close()
	log.Debugf("Event listener started")

	for {
		select {
		case evt := <-subAddrUpdated.Out():
			if e, ok := evt.(event.EvtLocalAddressesUpdated); ok {
				for _, addr := range e.Current {
					fullAddr := fmt.Sprintf("%+v/p2p/%s", addr.Address.String(), p.ID().Pretty())
					log.Debugf("Advertised Address changed: %s", fullAddr)
					saveNodeInfo("Advertised address: " + fullAddr)
				}
			}
		case evt := <-subPeerConnected.Out():
			if e, ok := evt.(event.EvtPeerConnectednessChanged); ok {
				log.Tracef("Peer %s is now %s", e.Peer.String(), e.Connectedness)
			}
		case <-p.ctx.Done():
			log.Warnf("Context cancel received. Stopping event listener")
			return
		}
	}
}
