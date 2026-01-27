package node

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/libp2p/go-libp2p"
	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/libp2p/go-libp2p/core/host"
	libp2pmetrics "github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/node/peerManager"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	"github.com/dominant-strategies/go-quai/p2p/node/requestManager"
	"github.com/dominant-strategies/go-quai/p2p/node/streamManager"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai"
)

const (
	// c_defaultCacheSize is the default size for the p2p cache
	c_defaultCacheSize    = 32
	c_streamPeerThreshold = 25
)

// P2PNode represents a libp2p node
type P2PNode struct {
	// Backend for handling consensus data
	consensus quai.ConsensusAPI

	// Gossipsub instance
	pubsub *pubsubManager.PubsubManager

	// Peer management interface instance
	peerManager peerManager.PeerManager

	// Request management interface instance
	requestManager requestManager.RequestManager

	// Caches for each type of data we may receive
	cache map[string]map[reflect.Type]*lru.Cache[common.Hash, interface{}]

	// Channel to signal when to quit and shutdown
	quitCh chan struct{}

	// runtime context
	ctx context.Context

	// used to control all the different sub processes of the P2PNode
	cancel context.CancelFunc

	// host management interface
	host host.Host

	// dht interface
	dht *kaddht.IpfsDHT

	// libp2p bandwidth counter
	bandwidthCounter *libp2pmetrics.BandwidthCounter
}

// buildAddrsFactory creates an AddrsFactory that replaces announced addresses
// with the specified external address. Used for Kubernetes deployments where
// the pod's internal IP is not externally routable.
func buildAddrsFactory(externalAddr string) (libp2p.Option, error) {
	if externalAddr == "" {
		return nil, nil
	}

	addr, err := multiaddr.NewMultiaddr(externalAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid external-addr %q: %w", externalAddr, err)
	}

	return libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
		return []multiaddr.Multiaddr{addr}
	}), nil
}

// Returns a new libp2p node.
// The node is created with the given context and options passed as arguments.
func NewNode(ctx context.Context, quitCh chan struct{}) (*P2PNode, error) {
	ipAddr := viper.GetString(utils.IPAddrFlag.Name)
	port := viper.GetString(utils.P2PPortFlag.Name)
	externalAddr := viper.GetString(utils.ExternalAddrFlag.Name)
	forcePublic := viper.GetBool(utils.ForcePublicFlag.Name)

	// Peer manager handles both connection management and connection gating
	peerMgr, err := peerManager.NewManager(
		ctx,
		viper.GetInt(utils.MaxPeersFlag.Name), // LowWater
		2*viper.GetInt(utils.MaxPeersFlag.Name), // HighWater
		nil,
	)
	if err != nil {
		log.Global.Fatalf("error creating libp2p connection manager: %s", err)
		return nil, err
	}

	// Set up libp2p resource manager and stats reporter
	rcmgr.MustRegisterWith(prometheus.DefaultRegisterer)

	str, err := rcmgr.NewStatsTraceReporter()
	if err != nil {
		log.Global.Fatal(err)
	}

	rmgr, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(rcmgr.DefaultLimits.AutoScale()), rcmgr.WithTraceReporter(str))
	if err != nil {
		log.Global.Fatal(err)
	}
	bwctr := libp2pmetrics.NewBandwidthCounter()

	log.Global.Info("listen addr tcp ", fmt.Sprintf("/ip4/%s/udp/%s/tcp", ipAddr, port))
	log.Global.Info("listen addrs quic ", fmt.Sprintf("/ip4/%s/udp/%s/quic", ipAddr, port))
	// Create the libp2p host

	// Build AddrsFactory for external address announcement (K8s support)
	addrsFactoryOpt, err := buildAddrsFactory(externalAddr)
	if err != nil {
		log.Global.Fatalf("error building address factory: %s", err)
		return nil, err
	}

	peerKey := getNodeKey()
	var dht *kaddht.IpfsDHT
	host, err := libp2p.New(
		// Pass in the resource manager
		libp2p.ResourceManager(rmgr),

		// Set up libp2p bandwitdh reporting
		libp2p.BandwidthReporter(bwctr),

		// use a private key for persistent identity
		libp2p.Identity(peerKey),

		// pass the ip address and port to listen on
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/%s/tcp/%s", ipAddr, port),
		),

		// Custom address factory for K8s external addresses
		func() libp2p.Option {
			if addrsFactoryOpt != nil {
				return addrsFactoryOpt
			}
			return nil
		}(),

		// Force public reachability (skip NAT detection) for K8s
		func() libp2p.Option {
			if forcePublic {
				return libp2p.ForceReachabilityPublic()
			}
			return nil
		}(),

		// support all transports
		libp2p.DefaultTransports,

		// support Noise connections
		libp2p.Security(noise.ID, noise.New),

		// Optionally attempt to configure network port mapping with UPnP
		// Skip when force-public is set (UPnP not needed in K8s)
		func() libp2p.Option {
			if forcePublic {
				return nil
			}
			if viper.GetBool(utils.PortMapFlag.Name) {
				return libp2p.NATPortMap()
			}
			return nil
		}(),

		// Enable NAT detection service
		libp2p.EnableNATService(),

		// If publicly reachable, provide a relay service for other peers
		libp2p.EnableRelayService(),

		// If behind NAT, automatically advertise relay address through relay peers
		// TODO: today the bootnodes act as static relays. In the future we should dynamically select relays from publicly reachable peers.
		libp2p.EnableAutoRelayWithStaticRelays(peerMgr.RefreshBootpeers()),

		// Attempt to open a direct connection with relayed peers, using relay
		// nodes to coordinate the holepunch.
		libp2p.EnableHolePunching(),

		// Connection manager will tag and prioritize peers
		libp2p.ConnectionManager(peerMgr),

		// Connection gater will prevent connections to blacklisted peers
		libp2p.ConnectionGater(peerMgr),

		// Let this host use the DHT to find other hosts
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			dht, err = kaddht.New(ctx, h,
				kaddht.Mode(kaddht.ModeServer),
				kaddht.BootstrapPeersFunc(func() []peer.AddrInfo {
					return peerMgr.RefreshBootpeers()
				}),
				kaddht.ProtocolPrefix("/quai"),
				kaddht.RoutingTableRefreshPeriod(1*time.Minute),
			)
			return dht, err
		}),
	)
	if err != nil {
		log.Global.Fatalf("error creating libp2p host: %s", err)
		return nil, err
	}

	idOpts := []identify.Option{
		identify.UserAgent("go-quai " + params.VersionWithCommit("", "")),
		identify.ProtocolVersion(string(protocol.ProtocolVersion)),
	}

	// Create the identity service
	idServ, err := identify.NewIDService(host, idOpts...)
	if err != nil {
		log.Global.Fatalf("error creating libp2p identity service: %s", err)
		return nil, err
	}
	// Register the identity service with the host
	idServ.Start()

	// log the p2p node's ID
	nodeID := host.ID()
	log.Global.Infof("node created: %s", nodeID)

	// Log address configuration for debugging K8s deployments
	log.Global.WithFields(log.Fields{
		"listenAddrs":  host.Addrs(),
		"externalAddr": externalAddr,
		"forcePublic":  forcePublic,
	}).Info("P2P node address configuration")

	// Set peer manager's self ID
	peerMgr.SetSelfID(nodeID)

	// Set the DHT for the peer manager
	peerMgr.SetDHT(dht)

	// Bootstrapping the DHT (this step is essential for peer discovery)
	if err := dht.Bootstrap(ctx); err != nil {
		log.Global.Info("Failed to bootstrap DHT:", err)
		return nil, err
	}

	// Create a gossipsub instance with helper functions
	ps, err := pubsubManager.NewGossipSubManager(ctx, host)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	p2p := &P2PNode{
		ctx:              ctx,
		pubsub:           ps,
		peerManager:      peerMgr,
		requestManager:   requestManager.NewManager(),
		cache:            initializeCaches(common.GenerateLocations(common.MaxRegions, common.MaxZones)),
		quitCh:           quitCh,
		cancel:           cancel,
		host:             host,
		dht:              dht,
		bandwidthCounter: bwctr,
	}

	sm, err := streamManager.NewStreamManager(p2p, host)
	if err != nil {
		return nil, err
	}
	sm.Start()

	p2p.peerManager.SetStreamManager(sm)

	return p2p, nil
}

// Close performs cleanup of resources used by P2PNode
func (p *P2PNode) Close() error {
	// Close PubSub manager
	if err := p.pubsub.Stop(); err != nil {
		log.Global.Errorf("error closing pubsub manager: %s", err)
	}

	// Close the stream manager
	if err := p.peerManager.Stop(); err != nil {
		log.Global.Errorf("error closing peer manager: %s", err)
	}

	// Close DHT
	if err := p.dht.Close(); err != nil {
		log.Global.Errorf("error closing DHT: %s", err)
	}

	// Close the libp2p host
	if err := p.host.Close(); err != nil {
		log.Global.Errorf("error closing libp2p host: %s", err)
	}

	close(p.quitCh)
	return nil
}

// acceptableTypes is used to filter out unsupported broadcast types
var acceptableTypes = map[reflect.Type]struct{}{
	reflect.TypeOf(types.WorkObjectShareView{}):  {},
	reflect.TypeOf(types.WorkObjectBlockView{}):  {},
	reflect.TypeOf(types.WorkObjectHeaderView{}): {},
	reflect.TypeOf(&types.AuxTemplate{}):         {},
}

func initializeCaches(locations []common.Location) map[string]map[reflect.Type]*lru.Cache[common.Hash, interface{}] {
	caches := make(map[string]map[reflect.Type]*lru.Cache[common.Hash, interface{}])
	for _, location := range locations {
		locCache := map[reflect.Type]*lru.Cache[common.Hash, interface{}]{}
		for typ := range acceptableTypes {
			locCache[reflect.PointerTo(typ)] = createCache(c_defaultCacheSize)
		}
		caches[location.Name()] = locCache
	}
	return caches
}

func createCache(size int) *lru.Cache[common.Hash, interface{}] {
	cache, err := lru.New[common.Hash, interface{}](size)
	if err != nil {
		log.Global.Fatal("error initializing cache;", err)
	}
	return cache
}

// Get the full multi-address to reach our node
func (p *P2PNode) p2pAddress() (multiaddr.Multiaddr, error) {
	return multiaddr.NewMultiaddr(fmt.Sprintf("/p2p/%s", p.peerManager.GetSelfID()))
}

// Helper to access the corresponding data cache
func (p *P2PNode) pickCache(datatype interface{}, location common.Location) *lru.Cache[common.Hash, interface{}] {
	return p.cache[location.Name()][reflect.TypeOf(datatype)]
}

// Add a datagram into the corresponding cache
func (p *P2PNode) cacheAdd(hash common.Hash, data interface{}, location common.Location) {
	cache := p.pickCache(data, location)
	cache.Add(hash, data)
}

// Get a datagram from the corresponding cache
func (p *P2PNode) cacheGet(hash common.Hash, datatype interface{}, location common.Location) (interface{}, bool) {
	cache := p.pickCache(datatype, location)
	return cache.Get(hash)
}
