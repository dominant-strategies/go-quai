package node

import (
	"fmt"

	"github.com/dominant-strategies/go-quai/log"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"

	multiaddr "github.com/multiformats/go-multiaddr"
)

// name of the file that contains the Docker's host IP
const hostIPFile = "hostip"

func NewNode(ipaddr string, port string, privKeyFile string) (host.Host, error) {

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
	return node, nil
}
