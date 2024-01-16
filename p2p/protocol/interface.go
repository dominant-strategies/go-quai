package protocol

import (
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// interface required to join the quai protocol network
type QuaiP2PNode interface {
	GetBootPeers() []peer.AddrInfo
	Connect(pi peer.AddrInfo) error
	NewStream(peerID peer.ID, protocolID protocol.ID) (network.Stream, error)
	Network() network.Network
	// Search for a block in the node's cache, or query the consensus backend if it's not found in cache.
	// Returns nil if the block is not found.
	GetBlock(hash common.Hash, location common.Location) *types.Block
}
