package quai

import (
	"github.com/dominant-strategies/go-quai/consensus/types"
	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/p2p/node"
)

// QuaiBackend implements the quai consensus protocol
type QuaiBackend struct {
	p2p node.Api

	runningSlices	[]types.SliceID
}

// Create a new instance of the QuaiBackend consensus service
func NewQuaiBackend() (*QuaiBackend, error) {
	return &QuaiBackend{}, nil
}

// Assign the p2p client interface to use for interacting with the p2p network
func (qbe *QuaiBackend) SetP2PNode(api node.Api) {
	qbe.p2p = api
}

// Start the QuaiBackend consensus service
func (qbe *QuaiBackend) Start() error {
	return nil
}

// Handle blocks received from the P2P client
func (qbe *QuaiBackend) OnNewBlock(sourcePeer p2p.PeerID, block types.Block) bool {
	panic("todo")
}

// Handle transactions received from the P2P client
func (qbe *QuaiBackend) OnNewTransaction(sourcePeer p2p.PeerID, tx types.Transaction) bool {
	panic("todo")
}

func (qbe *QuaiBackend) GetRunningSlices() []types.SliceID {
	// Example/mock implementation
	return []types.SliceID{
		{
			Region: 0,
			Zone: 0,
		},
	}
}

func (qbe *QuaiBackend) SetRunningSlices(slices []types.SliceID) {
	// Example/mock implementation
	qbe.runningSlices = slices
}