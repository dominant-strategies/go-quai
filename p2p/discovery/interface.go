package discovery

import (
	"context"

	kadht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

type DHT interface {
	// Initialize initializes the DHT with the given host and options
	Initialize(ctx context.Context, node host.Host, opts ...kadht.Option) error
	// Bootstrap bootstraps the DHT with the given host and bootstrap peers
	Bootstrap(ctx context.Context, node host.Host, bootstrapPeers ...string) error

	// Methods used for testing
	FindPeer(ctx context.Context, peerID peer.ID) (peer.AddrInfo, error)
	GetPeers() []peer.ID
}
