package node

import (
	"runtime/debug"

	"github.com/libp2p/go-libp2p/core/event"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/network"
)

// subscribes to the event bus and handles libp2p events as they're received
func (p *P2PNode) eventLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()

	// Subscribe to any events of interest
	sub, err := p.EventBus().Subscribe([]interface{}{
		new(event.EvtLocalProtocolsUpdated),
		new(event.EvtLocalAddressesUpdated),
		new(event.EvtLocalReachabilityChanged),
		new(event.EvtNATDeviceTypeChanged),
		new(event.EvtPeerProtocolsUpdated),
		new(event.EvtPeerIdentificationCompleted),
		new(event.EvtPeerIdentificationFailed),
		new(event.EvtPeerConnectednessChanged),
	})
	if err != nil {
		log.Global.Fatalf("failed to subscribe to peer connectedness events: %s", err)
	}
	defer sub.Close()

	log.Global.Debugf("Event listener started")

	for {
		select {
		case evt := <-sub.Out():
			switch e := evt.(type) {
			case event.EvtLocalProtocolsUpdated:
				log.Global.Debugf("Event: 'Local protocols updated' - added: %+v, removed: %+v", e.Added, e.Removed)
			case event.EvtLocalAddressesUpdated:
				p2pAddr, err := p.p2pAddress()
				if err != nil {
					log.Global.Errorf("error computing p2p address: %s", err)
				} else {
					for _, addr := range e.Current {
						addr := addr.Address.Encapsulate(p2pAddr)
						log.Global.Infof("Event: 'Local address updated': %s", addr)
					}
					// log removed addresses
					for _, addr := range e.Removed {
						addr := addr.Address.Encapsulate(p2pAddr)
						log.Global.Infof("Event: 'Local address removed': %s", addr)
					}
				}
			case event.EvtLocalReachabilityChanged:
				log.Global.Debugf("Event: 'Local reachability changed': %+v", e.Reachability)
			case event.EvtNATDeviceTypeChanged:
				log.Global.Debugf("Event: 'NAT device type changed' - DeviceType %v, transport: %v", e.NatDeviceType.String(), e.TransportProtocol.String())
			case event.EvtPeerProtocolsUpdated:
				log.Global.Debugf("Event: 'Peer protocols updated' - added: %+v, removed: %+v, peer: %+v", e.Added, e.Removed, e.Peer)
			case event.EvtPeerIdentificationCompleted:
				log.Global.Debugf("Event: 'Peer identification completed' - %v", e.Peer)
			case event.EvtPeerIdentificationFailed:
				log.Global.Debugf("Event 'Peer identification failed' - peer: %v, reason: %v", e.Peer, e.Reason.Error())
			case event.EvtPeerConnectednessChanged:
				// get the peer info
				peerInfo := p.Peerstore().PeerInfo(e.Peer)
				// get the peer ID
				peerID := peerInfo.ID
				// get the peer protocols
				peerProtocols, err := p.Peerstore().GetProtocols(peerID)
				if err != nil {
					log.Global.Errorf("error getting peer protocols: %s", err)
				}
				// get the peer addresses
				peerAddresses := p.Peerstore().Addrs(peerID)
				log.Global.Debugf("Event: 'Peer connectedness change' - Peer %s (peerInfo: %+v) is now %s, protocols: %v, addresses: %v", peerID.String(), peerInfo, e.Connectedness, peerProtocols, peerAddresses)

				if e.Connectedness == network.NotConnected {
					p.peerManager.RemovePeer(peerID)
				}
			case *event.EvtNATDeviceTypeChanged:
				log.Global.Debugf("Event `NAT device type changed` - DeviceType %v, transport: %v", e.NatDeviceType.String(), e.TransportProtocol.String())
			default:
				log.Global.Debugf("Received unknown event (type: %T): %+v", e, e)
			}
		case <-p.ctx.Done():
			log.Global.Warnf("Context cancel received. Stopping event listener")
			return
		}
	}
}
