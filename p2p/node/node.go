package node

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/discovery"
	quaips "github.com/dominant-strategies/go-quai/p2p/pubsub"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	routedhost "github.com/libp2p/go-libp2p/p2p/host/routed"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	multiaddr "github.com/multiformats/go-multiaddr"
)

// name of the file that contains the Docker's host IP
const hostIPFile = "hostip"

// p2pNode represents a libp2p node
type P2PNode struct {
	host.Host
	dht  discovery.DHTDiscovery
	mDNS discovery.MDNSDiscovery
	ctx  context.Context
	ps   quaips.PSManager
}

// returns a new libp2p node setup with the given IP address, address and private key
func NewNode(ctx context.Context, ipaddr string, port string, privKeyFile string) (*P2PNode, error) {

	p2pNode := &P2PNode{
		ctx: ctx,
	}

	// list of options to instantiate the libp2p node
	nodeOptions := []libp2p.Option{}

	privateKey, err := getPrivKey(privKeyFile)
	if err != nil {
		log.Fatalf("error getting private key: %s", err)
	}
	nodeOptions = append(nodeOptions, libp2p.Identity(privateKey))

	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%s", ipaddr, port))
	nodeOptions = append(nodeOptions, libp2p.ListenAddrs(sourceMultiAddr))

	// check if there is a host IP we can use to replace Docker's internal IP
	hostIP, err := readHostIPFromFile(hostIPFile)
	if err != nil || hostIP == "" {
		log.Warnf("error reading host IP from file: %s. Skipping...", err)
	} else {
		log.Infof("found host IP: %s", hostIP)
		addrsFactory := makeAddrsFactory(hostIP)
		nodeOptions = append(nodeOptions, libp2p.AddrsFactory(addrsFactory))
	}

	// enable NAT traversal
	nodeOptions = append(nodeOptions, libp2p.EnableNATService())

	// enable NAT port mapping
	nodeOptions = append(nodeOptions, libp2p.NATPortMap())

	// create the libp2p node
	node, err := libp2p.New(nodeOptions...)
	if err != nil {
		log.Errorf("error creating node: %s", err)
		return nil, err
	}
	p2pNode.Host = node

	// Initialize the DHT
	if err := p2pNode.InitializeDHT(); err != nil {
		log.Errorf("error initializing DHT: %s", err)
		return nil, err
	}

	// wrap the node with the routed host
	rnode := routedhost.Wrap(node, p2pNode.dht)
	p2pNode.Host = rnode
	log.Debugf("Routed node created")

	// Initialize mDNS discovery
	if err := p2pNode.InitializeMDNS(); err != nil {
		log.Errorf("error initializing mDNS discovery: %s", err)
		return nil, err
	}

	// Initialize the PubSub manager with default options
	psMgr, err := quaips.NewPubSubManager(ctx, rnode, nil)
	if err != nil {
		log.Errorf("error initializing PubSub manager: %s", err)
		return nil, err
	}
	p2pNode.ps = psMgr

	return p2pNode, nil
}

// ******* DHT methods ******* //

// Initialize initializes the DHT for the libp2p node
func (p *P2PNode) InitializeDHT(opts ...dht.Option) error {
	p.dht = &discovery.KadDHT{}
	return p.dht.Initialize(p.ctx, p.Host, opts...)
}

// Bootstrap bootstraps the DHT for the libp2p node
func (p *P2PNode) BootstrapDHT(bootstrapPeers ...string) error {
	return p.dht.Bootstrap(p.ctx, p.Host, bootstrapPeers...)
}

// FindPeer finds a peer within the DHT using the given peer ID
func (p *P2PNode) FindPeer(ctx context.Context, peerID peer.ID) (peer.AddrInfo, error) {
	return p.dht.FindPeer(ctx, peerID)
}

// RoutingTable returns the routing table of the DHT
func (p *P2PNode) GetPeers() []peer.ID {
	return p.dht.GetPeers()
}

// ******* mDNS methods ******* //

// Inialize mDNS discovery service
func (p *P2PNode) InitializeMDNS() error {
	p.mDNS = discovery.NewmDNSDiscovery(p.ctx, p.Host)
	return p.mDNS.Start()
}

// ******* PubSub methods ******* //

// Join a PubSub topic
func (p *P2PNode) Join(topic string) (*pubsub.Topic, error) {
	return p.ps.Join(topic)
}

// Subscribe to a PubSub topic
func (p *P2PNode) Subscribe(topic string) (*pubsub.Subscription, error) {
	return p.ps.Subscribe(topic)
}

// Publish a message to a PubSub topic
func (p *P2PNode) Publish(topic string, data []byte) error {
	return p.ps.Publish(topic, data)
}

// ListPeers lists the peers we are connected to for a given topic
func (p *P2PNode) ListPeers(topic string) []peer.ID {
	return p.ps.ListPeers(topic)
}

// ******* Shutdown method ******* //

type stopFunc func() error

// Function to gracefully shtudown all running services
func (p *P2PNode) Shutdown() error {
	// define a list of functions to stop the services the node is running
	stopFuncs := []stopFunc{
		p.mDNS.Stop,
		p.dht.Stop,
		p.ps.Stop,
		p.Host.Close,
	}
	// create a channel to collect errors
	errs := make(chan error, len(stopFuncs))
	// run each stop function in a goroutine
	for _, fn := range stopFuncs {
		go func(fn stopFunc) {
			errs <- fn()
		}(fn)
	}

	var allErrors []error
	for i := 0; i < len(stopFuncs); i++ {
		select {
		case err := <-errs:
			if err != nil {
				log.Errorf("error during shutdown: %s", err)
				// You can either collect all errors or just return the first one.
				// Here, we're setting finalErr to the last error we received.
				allErrors = append(allErrors, err)
			}
		case <-time.After(5 * time.Second):
			err := errors.New("timeout during shutdown")
			log.Warnf("error: %s", err)
			allErrors = append(allErrors, err)
		}
	}
	close(errs)
	if len(allErrors) > 0 {
		return errors.Errorf("errors during shutdown: %v", allErrors)
	} else {
		return nil
	}
}
