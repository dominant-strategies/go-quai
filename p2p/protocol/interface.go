package protocol

import (
	"math/big"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/p2p/requestManager"
	"github.com/dominant-strategies/go-quai/trie"
)

type StreamManager interface {
	// Set the host for the stream manager
	SetP2PBackend(QuaiP2PNode)

	// Set the host for the stream manager
	SetHost(host.Host)

	// GetStream returns a valid stream, either creating a new one or returning an existing one
	GetStream(peer.ID) (network.Stream, error)

	// RemoveStream goes through all the steps to properly close and remove a stream's resources
	CloseStream(peer.ID) error
}

// interface required to join the quai protocol network
type QuaiP2PNode interface {
	GetBootPeers() []peer.AddrInfo
	// Search for a block in the node's cache, or query the consensus backend if it's not found in cache.
	// Returns nil if the block is not found.
	GetWorkObject(hash common.Hash, location common.Location) *types.WorkObject
	GetHeader(hash common.Hash, location common.Location) *types.WorkObject
	GetBlockHashByNumber(number *big.Int, location common.Location) *common.Hash
	GetTrieNode(hash common.Hash, location common.Location) *trie.TrieNodeResponse
	GetRequestManager() requestManager.RequestManager

	Connect(peer.AddrInfo) error
	NewStream(peer.ID) (network.Stream, error)
	Network() network.Network
}
