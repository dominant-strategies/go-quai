package node

import (
	"context"
	"fmt"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/discovery"
	quaips "github.com/dominant-strategies/go-quai/p2p/pubsub"
	routedhost "github.com/libp2p/go-libp2p/p2p/host/routed"

	"github.com/libp2p/go-libp2p"
	kadht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"

	multiaddr "github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"
)

// Name of the file that contains the Docker's host IP
const hostIPFile = "hostip"

// P2PNode represents a libp2p node
type P2PNode struct {
	host.Host
	dht  discovery.DHTDiscovery
	mDNS discovery.MDNSDiscovery
	ctx  context.Context
	ps   quaips.PSManager
}

// Returns a new libp2p node.
// The node is created with the given context and options passed as arguments.
func NewNode(ctx context.Context) (*P2PNode, error) {

	// get parameters from config, flags or environment variables
	ipAddr := viper.GetString("ipaddr")
	port := viper.GetString("port")
	log.Debugf("Creating node with IP address: %s and port: %s", ipAddr, port)
	p2pNode := &P2PNode{
		ctx: ctx,
	}

	privKeyFile := viper.GetString("privkey")
	log.Debugf("Loading private key from file: %s", privKeyFile)
	privateKey, err := getPrivKey(privKeyFile)
	if err != nil {
		log.Fatalf("error getting private key: %s", err)
	}

	// list of options to instantiate the libp2p node
	nodeOptions := []libp2p.Option{}
	// use a private key for persistent identity
	nodeOptions = append(nodeOptions, libp2p.Identity(privateKey))
	// pass the ip address and port to listen on
	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%s", ipAddr, port))
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

	// get NAT related options
	nodeOptions = append(nodeOptions, getNATOptions()...)

	// create the libp2p node
	node, err := libp2p.New(nodeOptions...)
	if err != nil {
		log.Errorf("error creating node: %s", err)
		return nil, err
	}
	p2pNode.Host = node

	// Initialize the DHT
	if err := p2pNode.initializeDHT(); err != nil {
		log.Errorf("error initializing DHT: %s", err)
		return nil, err
	}

	// wrap the node with the routed host to improve network performance
	rnode := routedhost.Wrap(node, p2pNode.dht)
	p2pNode.Host = rnode
	log.Debugf("Routed node created")

	// initialize mDNS discovery
	p2pNode.initializeMDNS()

	// initialize PubSub
	if err := p2pNode.initializePubSub(); err != nil {
		log.Errorf("error initializing PubSub: %s", err)
		return nil, err
	}

	err = deleteNodeInfoFile()
	if err != nil {
		log.Errorf("error deleting node info file: %s", err)
		return nil, err
	}

	// log the p2p node's ID
	log.Infof("node created: %s", p2pNode.ID().Pretty())
	saveNodeInfo("Node ID: " + p2pNode.ID().Pretty())

	// log the p2p node's listening addresses
	for _, addr := range p2pNode.Addrs() {
		log.Infof("listening on: %s", addr.String())
	}

	return p2pNode, nil
}

// Initializes the DHT for the libp2p node in server mode.
func (p *P2PNode) initializeDHT(opts ...kadht.Option) error {
	serverModeOpt := kadht.Mode(kadht.ModeServer)
	opts = append(opts, serverModeOpt)
	p.dht = &discovery.KadDHT{}
	return p.dht.Initialize(p.ctx, p.Host, opts...)
}

// Bootstrap bootstraps the DHT for the libp2p node
func (p *P2PNode) bootstrapDHT(bootstrapPeers ...string) error {
	return p.dht.Bootstrap(p.ctx, p.Host, bootstrapPeers...)
}

// Inialize mDNS discovery service
func (p *P2PNode) initializeMDNS() {
	p.mDNS = discovery.NewmDNSDiscovery(p.ctx, p.Host)
}

// Initialize the PubSub manager with default options
func (p *P2PNode) initializePubSub() error {
	psMgr, err := quaips.NewPubSubManager(p.ctx, p.Host, nil)
	if err != nil {
		return err
	}
	p.ps = psMgr
	return nil
}
