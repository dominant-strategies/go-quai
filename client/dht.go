package client

import (
	"github.com/dominant-strategies/go-quai/log"

	kadht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/peer"
)

// initDHT initializes the DHT using client's libp2p node
func (c *P2PClient) InitDHT() error {
	dht, err := kadht.New(c.ctx, c.node)
	if err != nil {
		log.Fatalf("error creating DHT: %s", err)
	}
	c.dht = dht
	return nil
}

// BootstrapDHT bootstraps the DHT with the given list of bootstrap peers
func (c *P2PClient) BootstrapDHT(bootstrapPeers ...string) error {
	var bootStrapPeersAddrInfo []peer.AddrInfo
	if len(bootstrapPeers) == 0 {
		// if no bootstrap peers are given, bootstrap with the default bootstrap peers
		log.Warnf("no bootstrap peers given, using default public bootstrap peers")
		bootStrapPeersAddrInfo = kadht.GetDefaultBootstrapPeerAddrInfos()
	} else {
		for _, peerAddr := range bootstrapPeers {
			peerInfo, err := peer.AddrInfoFromString(peerAddr)
			if err != nil {
				log.Errorf("error creating peer info from address: %s", err)
				continue
			}
			bootStrapPeersAddrInfo = append(bootStrapPeersAddrInfo, *peerInfo)
		}
	}

	for _, peerInfo := range bootStrapPeersAddrInfo {
		log.Debugf("adding bootstraping node: %s", peerInfo.ID.Pretty())
		err := c.node.Connect(c.ctx, peerInfo)
		if err != nil {
			log.Errorf("error connecting to bootstrap node: %s", err)
			continue
		}
		log.Debugf("connected to bootstrap node: %s", peerInfo.ID.Pretty())
	}

	// Bootstrap the DHT
	return c.dht.Bootstrap(c.ctx)
}
