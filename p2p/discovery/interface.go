package discovery

import (
	"context"

	kadht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Discovery represents the a generic discovery service
// with basic Start() and Stop() methods.
type Discovery interface {
	Start() error
	Stop() error
}

// DHT represents the Kademlia DHT discovery service.
// It extends the Discovery interface with methods specific to the DHT.
type DHTDiscovery interface {
	Discovery
	// Initialize initializes the DHT with the given host and options
	Initialize(ctx context.Context, node host.Host, opts ...kadht.Option) error
	// Bootstrap bootstraps the DHT with the given host and bootstrap peers
	Bootstrap(ctx context.Context, node host.Host, bootstrapPeers ...string) error

	// Methods used for testing
	FindPeer(ctx context.Context, peerID peer.ID) (peer.AddrInfo, error)
	GetPeers() []peer.ID
}

// mDNSDiscovery represents the mDNS discovery service.
// It contains basic methods for starting and stopping the service.
type MDNSDiscovery interface {
	Discovery
}
