package protocol

import (
	"math/big"

	libp2pmetrics "github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/p2p/node/requestManager"
)

// interface required to join the quai protocol network
type QuaiP2PNode interface {
	// Search for a block in the node's cache, or query the consensus backend if it's not found in cache.
	// Returns nil if the block is not found.
	GetWorkObject(hash common.Hash, location common.Location) *types.WorkObject
	GetWorkObjectsFrom(hash common.Hash, location common.Location, count int) []*types.WorkObjectBlockView
	GetBlockHashByNumber(number *big.Int, location common.Location) *common.Hash
	GetRequestManager() requestManager.RequestManager
	GetBandwidthCounter() libp2pmetrics.Reporter

	Connect(peer.AddrInfo) error
	GetStream(peer.ID) (network.Stream, error)
}
