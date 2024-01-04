package quai

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/types"
	"github.com/dominant-strategies/go-quai/p2p"
)

// QuaiBackend implements the quai consensus protocol
type QuaiBackend struct {
	p2p common.NetworkingAPI

	runningSlices map[types.SliceID]*types.Slice
}

// Create a new instance of the QuaiBackend consensus service
func NewQuaiBackend() (*QuaiBackend, error) {
	return &QuaiBackend{}, nil
}

// Assign the p2p client interface to use for interacting with the p2p network
func (qbe *QuaiBackend) SetP2PNode(api common.NetworkingAPI) {
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

// Returns the current block height for the given sliceID
func (qbe *QuaiBackend) GetHeight(slice types.SliceID) uint64 {
	// Example/mock implementation
	panic("todo")
}

func (qbe *QuaiBackend) GetSlice(slice types.SliceID) *types.Slice {
	return qbe.runningSlices[slice]
}

func (qbe *QuaiBackend) GetRunningSlices() map[types.SliceID]*types.Slice {
	return qbe.runningSlices
}

func (qbe *QuaiBackend) SetRunningSlices(slices []types.Slice) {
	qbe.runningSlices = make(map[types.SliceID]*types.Slice)
	for _, slice := range slices {
		qbe.runningSlices[slice.SliceID] = &slice
	}
}

func (qbe *QuaiBackend) LookupBlock(hash types.Hash, slice types.SliceID) *types.Block {
	panic("todo")
}