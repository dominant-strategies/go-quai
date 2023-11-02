package p2p

import (
	"context"

	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// type alias around IpfsDHT
type DHT struct {
	// IpfsDHT instance
	Kad *kaddht.IpfsDHT
}

// Create a new DHT object
func NewDHT(ctx context.Context, node host.Host) (*DHT, error) {
	// Create DHT with default options
	kad, err := kaddht.New(ctx, node)
	if err != nil {
		return nil, err
	}
	return &DHT{
		Kad: kad,
	}, nil
}

// Stop the DHT
func (d *DHT) Close() error {
	return d.Kad.Close()
}

// Bootstraps the DHT with the given bootstrap peers.
// If no bootstrap peers are given, the default bootstrap peers are used.
func (d *DHT) Bootstrap(ctx context.Context) error {
	// Bootstrap the DHT
	return d.Kad.Bootstrap(ctx)
}

func (d *DHT) FindPeer(ctx context.Context, peerID peer.ID) (peer.AddrInfo, error) {
	return d.Kad.FindPeer(ctx, peerID)
}

func (d *DHT) GetPeers() []peer.ID {
	return d.Kad.RoutingTable().ListPeers()
}

func (d *DHT) Stop() error {
	return d.Kad.Close()
}
