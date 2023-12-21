package common

import (
	"github.com/dominant-strategies/go-quai/consensus/types"

	"github.com/libp2p/go-libp2p/core"
)

// The consensus backend will implement the following interface to provide information to the networking backend.
type ConsensusAPI interface {
	// Returns the current block height for the given sliceID
	GetHeight(types.SliceID) uint64

	// Returns the slices this node is processing
	GetRunningSlices() []types.SliceID
	// Sets the slices this node is processing
	SetRunningSlices([]types.SliceID)

	// Handle new data propagated from the gossip network. Should return quickly.
	// Return true if this data should be relayed to peers. False if it should be ignored.
	OnNewBlock(sourcePeer core.PeerID, block types.Block) bool
	OnNewTransaction(sourcePeer core.PeerID, tx types.Transaction) bool
}

// The networking backend will implement the following interface to enable consensus to communicate with other nodes.
type NetworkingAPI interface {
	// Start the p2p node
	Start() error

	// Stop the p2p node
	Stop() error

	// Methods to broadcast data to the network
	BroadcastBlock(types.SliceID, types.Block) error
	BroadcastTransaction(tx types.Transaction) error

	// Methods to lookup specific data from the network. Each request method
	// returns a result channel. If the result is found, it will be put into the
	// channel. If the result is not found, the channel will be closed.
	RequestBlock(hash types.Hash, loc types.SliceID) chan *types.Block
	RequestTransaction(hash types.Hash, loc types.SliceID) chan *types.Transaction

	// Method to report a peer to the P2PClient as behaving maliciously
	ReportBadPeer(peer core.PeerID)
}
