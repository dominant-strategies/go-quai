package types

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
)

const (
	EtxExpirationAge = 8640 // With 10s blocks, ETX expire after ~24hrs
)

// The EtxSet maps an ETX hash to the block number in which it became available.
// If no entry exists for a given ETX hash, then that ETX is not available.
type EtxSet map[common.Hash]uint64

func NewEtxSet() EtxSet {
	return make(EtxSet)
}

// updateInboundEtxs updates the set of inbound ETXs available to be mined into
// a block in this location. This method adds any new ETXs to the set and
// removes expired ETXs.
func (set EtxSet) Update(newInboundEtxs Transactions, currentHeight uint64) {
	// Add new ETX entries to the inbound set
	for _, etx := range newInboundEtxs {
		if etx.ToChain().Equal(common.NodeLocation) {
			set[etx.Hash()] = currentHeight
		} else {
			log.Error("skipping ETX belonging to other destination", "etxHash: ", etx.Hash(), "etxToChain: ", etx.ToChain())
		}
	}

	// Remove expired ETXs
	for txHash, availableAtBlock := range set {
		etxExpirationHeight := availableAtBlock + EtxExpirationAge
		if currentHeight > etxExpirationHeight {
			delete(set, txHash)
		}
	}
}
