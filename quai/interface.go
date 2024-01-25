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
	// Specify the peer which propagated the data to us, as well as the data itself.
	// Return true if this data should be relayed to peers. False if it should be ignored.
	OnNewBroadcast(core.PeerID, interface{}) bool

	// Asks the consensus backend to lookup a block by hash and location.
	// If the block is found, it should be returned. Otherwise, nil should be returned.
	LookupBlock(common.Hash, common.Location) *types.Block
}

// The networking backend will implement the following interface to enable consensus to communicate with other nodes.
type NetworkingAPI interface {
	// Start the p2p node
	Start() error

	// Stop the p2p node
	Stop() error

	// Specify location and data type to subscribe to
	Subscribe(common.Location, interface{}) error

	// Method to broadcast data to the network
	// Specify location and the data to send
	Broadcast(common.Location, interface{}) error

	// Method to request data from the network
	// Specify location, data hash, and data type to request
	Request(common.Location, common.Hash, interface{}) chan interface{}

	// Methods to report a peer to the P2PClient as behaving maliciously
	// Promote and Demote record the peer's behavior in the peer manager
	PromotePeer(core.PeerID)
	DemotePeer(core.PeerID)

	// Protects the peer's connection from being pruned
	ProtectPeer(core.PeerID)
	// Ban will close the connection and prevent future connections with this peer
	BanPeer(core.PeerID)
}
