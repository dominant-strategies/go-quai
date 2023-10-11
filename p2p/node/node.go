package node

import (
	"context"
	"fmt"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/discovery"

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
	dht discovery.DHT
	ctx context.Context
}

// returns a new libp2p node setup with the given IP address, address and private key
func NewNode(ctx context.Context, ipaddr string, port string, privKeyFile string) (*P2PNode, error) {

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
		log.Warnf("error reading host IP from file: %s. Skiping...", err)
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

	p2pNode := &P2PNode{
		Host: node,
		ctx:  ctx,
	}
	return p2pNode, nil
}

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
