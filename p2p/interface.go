package p2p

import (
	"github.com/dominant-strategies/go-quai/consensus/types"
)

// P2PClient defines an interface which can be used to send/receive data
// through the P2P client
type P2PClient interface {
	// Methods to broadcast data to the network
	BroadcastBlock(block types.Block) error
	BroadcastTransaction(tx types.Transaction) error

	// Methods to lookup specific data from the network. Each request method
	// returns a result channel. If the result is found, it will be put into the
	// channel. If the result is not found, the channel will be closed.
	RequestBlock(hash types.Hash, loc types.Location) chan types.Block
	RequestTransaction(hash types.Hash, loc types.Location) chan types.Transaction

	// Method to report a peer to the P2PClient as behaving maliciously
	ReportBadPeer(peer PeerID)
}
