package protocol

import (
	"math/big"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/p2p/node/requestManager"
	"github.com/dominant-strategies/go-quai/trie"
)

// interface required to join the quai protocol network
type QuaiP2PNode interface {
	// Search for a block in the node's cache, or query the consensus backend if it's not found in cache.
	// Returns nil if the block is not found.
	GetWorkObject(hash common.Hash, location common.Location) *types.WorkObject
	GetBlockHashByNumber(number *big.Int, location common.Location) *common.Hash
	GetTrieNode(hash common.Hash, location common.Location) *trie.TrieNodeResponse
	GetRequestManager() requestManager.RequestManager

	Connect(peer.AddrInfo) error
	GetStream(peer.ID) (network.Stream, error)
}
