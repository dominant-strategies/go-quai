package qiwallet

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/core/types"
)

// ExpiryHeight returns the chain height at which a UTXO of the given
// denomination created at creationHeight will be trimmed (burned), or 0 if
// it is not subject to trimming. Denominations above MaxTrimDenomination
// never expire, locked outputs are skipped by the trimmer, and a zero
// creationHeight means the creation height is unknown.
func ExpiryHeight(denomination uint8, creationHeight uint64, lock *big.Int) uint64 {
	if denomination > types.MaxTrimDenomination || creationHeight == 0 {
		return 0
	}
	if lock != nil && lock.Sign() != 0 {
		return 0
	}
	return creationHeight + types.TrimDepths[denomination]
}

// RefreshCandidates returns the subset of utxos that will be trimmed within
// window blocks of currentHeight and are therefore candidates for an urgent
// refresh spend. Whether refreshing is economical depends on the fee; see
// SelectUTXOs, which weighs each expiring note against its marginal fee.
func RefreshCandidates(utxos []OwnedUTXO, currentHeight, window uint64) []OwnedUTXO {
	candidates := make([]OwnedUTXO, 0)
	for _, utxo := range utxos {
		expiry := ExpiryHeight(utxo.Denomination, utxo.CreationHeight, utxo.Lock)
		if expiry == 0 || expiry <= currentHeight {
			continue
		}
		if expiry-currentHeight <= window {
			candidates = append(candidates, utxo)
		}
	}
	return candidates
}
