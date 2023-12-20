package node

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p"
	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	dual "github.com/libp2p/go-libp2p-kad-dht/dual"
	pubsub "github.com/libp2p/go-libp2p-pubsub"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/p2p/pubsubManager"
	lru "github.com/hashicorp/golang-lru/v2"
)

// P2PNode represents a libp2p node
type P2PNode struct {
	// Host interface
	host.Host

	// Backend for handling consensus data
	consensus common.ConsensusAPI

	// List of peers to introduce us to the network
	bootpeers []peer.AddrInfo

	// TODO: Consolidate into network interface, and consensus interface
	// DHT instance
	dht *dual.DHT

	// Gossipsub instance
	pubsub *pubsubManager.PubsubManager

	// Gossipsub subscriptions
	// Each sliceID will have a topic for each data type
	// - C_blockTopicName
	// - C_transactionTopicName
	topics map[types.SliceID]map[string]*pubsub.Topic

	// cache of received blocks
	blockCache *lru.Cache[types.Hash, *types.Block]
	// cache of received transactions
	txCache *lru.Cache[types.Hash, *types.Transaction]

	// runtime context
	ctx context.Context
}

// Returns a new libp2p node.
// The node is created with the given context and options passed as arguments.
func NewNode(ctx context.Context) (*P2PNode, error) {
	ipAddr := viper.GetString(utils.IPAddrFlag.Name)
	port := viper.GetString(utils.P2PPortFlag.Name)

	// Load bootpeers
	bootpeers, err := loadBootPeers()
	if err != nil {
		log.Errorf("error loading bootpeers: %s", err)
		return nil, err
	}

	// Define a connection manager
	connectionManager, err := connmgr.NewConnManager(
		viper.GetInt(utils.MaxPeersFlag.Name),   // LowWater
		2*viper.GetInt(utils.MaxPeersFlag.Name), // HighWater
	)
	if err != nil {
		log.Fatalf("error creating libp2p connection manager: %s", err)
		return nil, err
	}

	// Create the libp2p host
	var dht *dual.DHT
	host, err := libp2p.New(
		// use a private key for persistent identity
		libp2p.Identity(getNodeKey()),

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

		// Optionally attempt to configure network port mapping with UPnP
		func() libp2p.Option {
			if viper.GetBool(utils.PortMapFlag.Name) {
				return libp2p.NATPortMap()
			} else {
				return nil
			}
		}(),

		// Enable NAT detection service
		libp2p.EnableNATService(),

		// If publicly reachable, provide a relay service for other peers
		libp2p.EnableRelayService(),

		// If behind NAT, automatically advertise relay address through relay peers
		// TODO: today the bootnodes act as static relays. In the future we should dynamically select relays from publicly reachable peers.
		libp2p.EnableAutoRelayWithStaticRelays(bootpeers),

		// Attempt to open a direct connection with relayed peers, using relay
		// nodes to coordinate the holepunch.
		libp2p.EnableHolePunching(),

		// Let this host use the DHT to find other hosts
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			dht, err = dual.New(ctx, h,
				dual.WanDHTOption(
					kaddht.Mode(kaddht.ModeServer),
					kaddht.BootstrapPeersFunc(func() []peer.AddrInfo {
						log.Debugf("Bootstrapping to the following peers: %v", bootpeers)
						return bootpeers
					}),
					kaddht.ProtocolPrefix("/quai"),
					kaddht.RoutingTableRefreshPeriod(1*time.Minute),
				),
			)
			return dht, err
		}),
	)
	if err != nil {
		log.Fatalf("error creating libp2p host: %s", err)
		return nil, err
	}

	idOpts := []identify.Option{
		// TODO: Add version number + commit hash
		identify.UserAgent("go-quai"),
		identify.ProtocolVersion(string(protocol.ProtocolVersion)),
	}

	// Create the identity service
	idServ, err := identify.NewIDService(host, idOpts...)
	if err != nil {
		log.Fatalf("error creating libp2p identity service: %s", err)
		return nil, err
	}
	// Register the identity service with the host
	idServ.Start()

	// log the p2p node's ID
	nodeID := host.ID()
	log.Infof("node created: %s", nodeID)

	// Create a gossipsub instance with helper functions
	ps, err := pubsubManager.NewGossipSubManager(ctx, host)

	if err != nil {
		return nil, err
	}

	// Create a new LRU cache for blocks and transactions
	const cacheSize = 10
	blockCache, err := lru.New[types.Hash, *types.Block](cacheSize)
	if err != nil {
		return nil, err
	}

	txCache, err := lru.New[types.Hash, *types.Transaction](cacheSize)
	if err != nil {
		return nil, err
	}

	return &P2PNode{
		ctx:        ctx,
		Host:       host,
		bootpeers:  bootpeers,
		dht:        dht,
		pubsub:     ps,
		topics:     make(map[types.SliceID]map[string]*pubsub.Topic),
		blockCache: blockCache,
		txCache:    txCache,
	}, nil
}

// Get the full multi-address to reach our node
func (p *P2PNode) p2pAddress() (multiaddr.Multiaddr, error) {
	return multiaddr.NewMultiaddr(fmt.Sprintf("/p2p/%s", p.ID()))
}
