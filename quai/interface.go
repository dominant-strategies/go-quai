package quai

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"

	"github.com/libp2p/go-libp2p/core"
)

// The consensus backend will implement the following interface to provide information to the networking backend.
type ConsensusAPI interface {
	// Returns the current block height for the given location
	GetHeight(common.Location) uint64

	// Handle new data propagated from the gossip network. Should return quickly.
	// Return true if this data should be relayed to peers. False if it should be ignored.
	OnNewBlock(sourcePeer core.PeerID, block types.Block) bool
	OnNewTransaction(sourcePeer core.PeerID, tx types.Transaction) bool

	// Asks the consensus backend to lookup a block by hash and location.
	// If the block is found, it should be returned. Otherwise, nil should be returned.
	LookupBlock(hash common.Hash, location common.Location) *types.Block
}

// The networking backend will implement the following interface to enable consensus to communicate with other nodes.
type NetworkingAPI interface {
	// Start the p2p node
	Start() error

	// Stop the p2p node
	Stop() error

	// Method to subscribe to data from a given location. If the data-type is not supported, an error will be returned.
	// Specify location and data type to subscribe to
	Subscribe(common.Location, interface{}) error

	// Method to broadcast data to the network
	// Specify location and the data to send
	Broadcast(common.Location, interface{}) error

	// Method to request data from the network
	// Specify location, data hash, and data type to request
	Request(common.Location, common.Hash, interface{}) chan interface{}

	// Method to report a peer to the P2PClient as behaving maliciously
	ReportBadPeer(core.PeerID)
}
