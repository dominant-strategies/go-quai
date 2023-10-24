package consensus

import (
	"github.com/dominant-strategies/go-quai/consensus/types"
	"github.com/dominant-strategies/go-quai/p2p"
)

// The consensus backend will implement the following interface, to inform P2P handling of data
type ConsensusBackend interface {
	// Handle new data propagated from the gossip network. Should return quickly.
	// Return true if this data should be relayed to peers. False if it should be ignored.
	OnNewBlock(sourcePeer p2p.PeerID, block types.Block) bool
	OnNewTransaction(sourcePeer p2p.PeerID, tx types.Transaction) bool
}
