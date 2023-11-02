package node

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	routedhost "github.com/libp2p/go-libp2p/p2p/host/routed"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/options"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
)

// P2PNode represents a libp2p node
type P2PNode struct {
	// Host interface
	host.Host

	// libp2p subprotocol services
	dht *p2p.DHT

	// Backend for handling consensus data
	consensus consensus.ConsensusBackend

	// List of peers to introduce us to the network
	bootpeers []peer.AddrInfo

	// runtime context
	ctx context.Context
}

// Returns a new libp2p node.
// The node is created with the given context and options passed as arguments.
func NewNode(ctx context.Context) (*P2PNode, error) {
	ipAddr := viper.GetString(options.IP_ADDR)
	port := viper.GetString(options.PORT)
	node := &P2PNode{}

	// Load bootpeers
	for _, p := range viper.GetStringSlice(options.BOOTPEERS) {
		addr, err := multiaddr.NewMultiaddr(p)
		if err != nil {
			log.Fatalf("error loading multiaddress for %s in bootpeers: %s", p, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			log.Fatalf("error getting address info for %s in bootpeers: %s", addr, err)
		}
		node.bootpeers = append(node.bootpeers, *info)
	}

	// Define a connection manager
	connectionManager, err := connmgr.NewConnManager(
		100, // Lowwater
		400, // HighWater,
	)
	if err != nil {
		log.Fatalf("error creating libp2p connection manager: %s", err)
		return nil, err
	}

	// Create the libp2p host
	host, err := libp2p.New(
		// use a private key for persistent identity
		libp2p.Identity(GetNodeKey()),

		// pass the ip address and port to listen on
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/%s/tcp/%s", ipAddr, port),
		),

		// support all transports
		libp2p.DefaultTransports,

		// support Noise connections
		libp2p.Security(noise.ID, noise.New),

		// Let's prevent our peer from having too many
		// connections by attaching a connection manager.
		libp2p.ConnectionManager(connectionManager),

		// Attempt to open ports using uPNP for NATed hosts.
		libp2p.NATPortMap(),
	)
	if err != nil {
		log.Fatalf("error creating libp2p host: %s", err)
		return nil, err
	}

	// Create the DHT service
	if node.dht, err = p2p.NewDHT(ctx, host); err != nil {
		log.Fatalf("error creating libp2p dht: %s", err)
		return nil, err
	}
	log.Debugf("dht service created")

	// Wrap the host with the routed host to improve network performance
	node.Host = routedhost.Wrap(host, node.dht.Kad)
	log.Debugf("routed host created")

	// Set the node context
	node.ctx = ctx

	// log the p2p node's ID
	nodeID := node.ID().Pretty()
	log.Infof("node created: %s", nodeID)

	return node, nil
}

func (p *P2PNode) p2pAddress() (multiaddr.Multiaddr, error) {
	return multiaddr.NewMultiaddr(fmt.Sprintf("/p2p/%s", p.ID()))
}

// subscribes to the event bus and handles libp2p events as they're received
func (p *P2PNode) eventLoop() {
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
		log.Fatalf("failed to subscribe to peer connectedness events: %s", err)
	}
	defer sub.Close()

	// Wait for events
	for {
		select {
		case evt := <-sub.Out():
			if e, ok := evt.(event.EvtLocalAddressesUpdated); ok {
				p2pAddr, err := p.p2pAddress()
				if err != nil {
					log.Errorf("error computing p2p address: %s", err)
				} else {
					for _, addr := range e.Current {
						addr := addr.Address.Encapsulate(p2pAddr)
						log.Infof("listening at address: %s", addr)
					}
				}
			} else if e, ok := evt.(event.EvtPeerConnectednessChanged); ok {
				log.Debugf("peer %s is now %s", e.Peer.String(), e.Connectedness)
			} else {
				log.Tracef("unhandled host event: %s", evt)
			}

		case <-p.ctx.Done():
			log.Warnf("context cancel received. Stopping event listener")
			return
		}
	}
}
