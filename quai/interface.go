package quai

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"

	"github.com/libp2p/go-libp2p/core"
)

// The consensus backend will implement the following interface to provide information to the networking backend.
type ConsensusAPI interface {
	// Returns the current block height for the given sliceID
	GetHeight(types.SliceID) uint64

	// Returns the slices this node is processing
	GetRunningSlices() map[types.SliceID]*types.Slice
	// Sets the slices this node is processing
	SetRunningSlices(slices []types.Slice)

	// Handle new data propagated from the gossip network. Should return quickly.
	// Return true if this data should be relayed to peers. False if it should be ignored.
	OnNewBlock(sourcePeer core.PeerID, block types.Block) bool
	OnNewTransaction(sourcePeer core.PeerID, tx types.Transaction) bool

	// Asks the consensus backend to lookup a block by hash and sliceID.
	// If the block is found, it should be returned. Otherwise, nil should be returned.
	LookupBlock(hash common.Hash, loc types.SliceID) *types.Block
}

// The networking backend will implement the following interface to enable consensus to communicate with other nodes.
type NetworkingAPI interface {
	// Start the p2p node
	Start() error

	// Stop the p2p node
	Stop() error

	// Method to subscribe to data from a given location. If the data-type is not supported, an error will be returned.
	Subscribe(types.SliceID, interface{}) error

	// Method to broadcast data to the network
	Broadcast(types.SliceID, interface{}) error

	// Methods to lookup specific data from the network. Each request method
	// returns a result channel. If the result is found, it will be put into the
	// channel. If the result is not found, the channel will be closed.
	RequestBlock(hash common.Hash, loc types.SliceID) chan *types.Block
	RequestTransaction(hash common.Hash, loc types.SliceID) chan *types.Transaction

	// Method to report a peer to the P2PClient as behaving maliciously
	ReportBadPeer(peer core.PeerID)
}
