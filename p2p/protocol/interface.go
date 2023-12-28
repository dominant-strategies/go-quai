package protocol

import (
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/consensus/types"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// interface required to join the quai protocol network
type QuaiP2PNode interface {
	GetBootPeers() []peer.AddrInfo
	Connect(pi peer.AddrInfo) error
	NewStream(peerID peer.ID, protocolID protocol.ID) (network.Stream, error)
	Network() network.Network
	// Checks if the cache has a block with the given hash. If the block is not found, returns nil.
	GetBlock(hash types.Hash) *types.Block
}
