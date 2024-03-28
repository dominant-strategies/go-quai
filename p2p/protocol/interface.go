package protocol

import (
	"math/big"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/p2p/requestManager"
)

// interface required to join the quai protocol network
type QuaiP2PNode interface {
	GetBootPeers() []peer.AddrInfo
	// Search for a block in the node's cache, or query the consensus backend if it's not found in cache.
	// Returns nil if the block is not found.
	GetBlock(hash common.Hash, location common.Location) *types.Block
	GetHeader(hash common.Hash, location common.Location) *types.Header
	GetBlockHashByNumber(number *big.Int, location common.Location) *common.Hash
	GetTrieNode(hash common.Hash, location common.Location) ([]byte, error)
	GetRequestManager() requestManager.RequestManager
	GetHostBackend() host.Host

	Connect(peer.AddrInfo) error
	NewStream(peer.ID) (network.Stream, error)
	Network() network.Network
}
