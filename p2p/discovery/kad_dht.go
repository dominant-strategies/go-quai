package discovery

import (
	"context"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/pkg/errors"

	kadht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// represents the DHT for the libp2p node
type KadDHT struct {
	dht *kadht.IpfsDHT
}

func (k *KadDHT) Initialize(ctx context.Context, node host.Host, opts ...kadht.Option) error {
	// create a new DHT with the given options
	serverModeOpt := kadht.Mode(kadht.ModeServer)
	opts = append(opts, serverModeOpt)
	dht, err := kadht.New(ctx, node, opts...)
	if err != nil {
		return errors.Wrap(err, "error creating DHT")
	}
	k.dht = dht
	return nil
}

func (k *KadDHT) Bootstrap(ctx context.Context, node host.Host, bootstrapPeers ...string) error {
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

	if len(bootStrapPeersAddrInfo) == 0 {
		return errors.Errorf("no valid bootstrap peers given: %v", bootstrapPeers)
	}

	for _, peerInfo := range bootStrapPeersAddrInfo {
		log.Debugf("adding bootstraping node: %s", peerInfo.ID.Pretty())
		err := node.Connect(ctx, peerInfo)
		if err != nil {
			log.Errorf("error connecting to bootstrap node: %s", err)
			continue
		}
		log.Debugf("connected to bootstrap node: %s", peerInfo.ID.Pretty())
	}
	// Bootstrap the DHT
	return k.dht.Bootstrap(ctx)

}

func (k *KadDHT) FindPeer(ctx context.Context, peerID peer.ID) (peer.AddrInfo, error) {
	return k.dht.FindPeer(ctx, peerID)
}

func (k *KadDHT) GetPeers() []peer.ID {
	return k.dht.RoutingTable().ListPeers()
}

func (k *KadDHT) Start() error {
	// TODO: implement
	return nil
}

func (k *KadDHT) Stop() error {
	return k.dht.Close()
}
