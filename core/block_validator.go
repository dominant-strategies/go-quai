// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"fmt"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
)

// BlockValidator is responsible for validating block headers, uncles and
// processed state.
//
// BlockValidator implements Validator.
type BlockValidator struct {
	config *params.ChainConfig // Chain configuration options
	hc     *HeaderChain        // HeaderChain
	engine consensus.Engine    // Consensus engine used for validating
}

// NewBlockValidator returns a new block validator which is safe for re-use
func NewBlockValidator(config *params.ChainConfig, headerChain *HeaderChain, engine consensus.Engine) *BlockValidator {
	validator := &BlockValidator{
		config: config,
		engine: engine,
		hc:     headerChain,
	}
	return validator
}

// ValidateBody validates the given block's uncles and verifies the block
// header's transaction and uncle roots. The headers are assumed to be already
// validated at this point.
func (v *BlockValidator) ValidateBody(block *types.WorkObject) error {
	nodeCtx := v.config.Location.Context()
	// Check whether the block's known, and if not, that it's linkable
	if nodeCtx == common.ZONE_CTX && v.hc.ProcessingState() {
		if v.hc.bc.processor.HasBlockAndState(block.Hash(), block.NumberU64(nodeCtx)) {
			return ErrKnownBlock
		}
	}
	header := block.Header()
	// Subordinate manifest must match ManifestHash in subordinate context, _iff_
	// we have a subordinate (i.e. if we are not a zone)
	if nodeCtx != common.ZONE_CTX {
		// Region nodes should have body with zero length txs and etxs
		if len(block.Transactions()) != 0 {
			return fmt.Errorf("region body has non zero transactions")
		}
		if len(block.ExtTransactions()) != 0 {
			return fmt.Errorf("region body has non zero etx transactions")
		}
		if len(block.Uncles()) != 0 {
			return fmt.Errorf("region body has non zero uncles")
		}
		subManifestHash := types.DeriveSha(block.Manifest(), trie.NewStackTrie(nil))
		if subManifestHash == types.EmptyRootHash || subManifestHash != header.ManifestHash(nodeCtx+1) {
			// If we have a subordinate chain, it is impossible for the subordinate manifest to be empty
			return ErrBadSubManifest
		}
		if nodeCtx == common.PRIME_CTX {
			interlinkRootHash := types.DeriveSha(block.InterlinkHashes(), trie.NewStackTrie(nil))
			if interlinkRootHash != header.InterlinkRootHash() {
				return ErrBadInterlink
			}
		}
	} else {
		// Header validity is known at this point, check the uncles and transactions
		if err := v.engine.VerifyUncles(v.hc, block); err != nil {
			return err
		}
		if hash := types.CalcUncleHash(block.Uncles()); hash != header.UncleHash() {
			return fmt.Errorf("uncle root hash mismatch: have %x, want %x", hash, header.UncleHash())
		}
		if v.hc.ProcessingState() {
			if hash := types.DeriveSha(block.Transactions(), trie.NewStackTrie(nil)); hash != header.TxHash() {
				return fmt.Errorf("transaction root hash mismatch: have %x, want %x", hash, header.TxHash())
			}
			if hash := types.DeriveSha(block.ExtTransactions(), trie.NewStackTrie(nil)); hash != header.EtxHash() {
				return fmt.Errorf("external transaction root hash mismatch: have %x, want %x", hash, header.EtxHash())
			}
		}
	}
	return nil
}

// ValidateState validates the various changes that happen after a state
// transition, such as amount of used gas, the receipt roots and the state root
// itself. ValidateState returns a database batch if the validation was a success
// otherwise nil and an error is returned.
func (v *BlockValidator) ValidateState(block *types.WorkObject, statedb *state.StateDB, receipts types.Receipts, utxoEtxs []*types.Transaction, usedGas uint64) error {
	start := time.Now()
	header := types.CopyHeader(block.Header())
	time1 := common.PrettyDuration(time.Since(start))
	if block.GasUsed() != usedGas {
		return fmt.Errorf("invalid gas used (remote: %d local: %d)", block.GasUsed(), usedGas)
	}
	time2 := common.PrettyDuration(time.Since(start))
	time3 := common.PrettyDuration(time.Since(start))
	// Tre receipt Trie's root (R = (Tr [[H1, R1], ... [Hn, Rn]]))
	receiptSha := types.DeriveSha(receipts, trie.NewStackTrie(nil))
	if receiptSha != header.ReceiptHash() {
		return fmt.Errorf("invalid receipt root hash (remote: %x local: %x)", header.ReceiptHash(), receiptSha)
	}
	time4 := common.PrettyDuration(time.Since(start))
	// Validate the state root against the received state root and throw
	// an error if they don't match.
	if root := statedb.IntermediateRoot(true); header.EVMRoot() != root {
		return fmt.Errorf("invalid merkle root (remote: %x local: %x)", header.EVMRoot(), root)
	}
	if root := statedb.UTXORoot(); header.UTXORoot() != root {
		return fmt.Errorf("invalid utxo root (remote: %x local: %x)", header.UTXORoot(), root)
	}
	if root := statedb.ETXRoot(); header.EtxSetRoot() != root {
		return fmt.Errorf("invalid etx root (remote: %x local: %x)", header.EtxSetRoot(), root)
	}
	time5 := common.PrettyDuration(time.Since(start))
	// Collect ETXs emitted from each successful transaction
	var emittedEtxs types.Transactions
	for _, receipt := range receipts {
		if receipt.Status == types.ReceiptStatusSuccessful {
			emittedEtxs = append(emittedEtxs, receipt.Etxs...)
		}
	}
	emittedEtxs = append(emittedEtxs, utxoEtxs...)
	time6 := common.PrettyDuration(time.Since(start))

	// Confirm the ETXs emitted by the transactions in this block exactly match the
	// ETXs given in the block body
	if etxHash := types.DeriveSha(emittedEtxs, trie.NewStackTrie(nil)); etxHash != header.EtxHash() {
		return fmt.Errorf("invalid etx hash (remote: %x local: %x)", header.EtxHash(), etxHash)
	}

	// Check that the UncledS in the header matches the S from the block
	expectedUncledS := v.engine.UncledLogS(block)
	if expectedUncledS.Cmp(header.UncledS()) != 0 {
		return fmt.Errorf("invalid uncledS (remote: %x local: %x)", header.UncledS(), expectedUncledS)
	}
	v.hc.logger.WithFields(log.Fields{
		"t1": time1,
		"t2": time2,
		"t3": time3,
		"t4": time4,
		"t5": time5,
		"t6": time6,
	}).Debug("times during validate state")
	return nil
}

// CalcGasLimit computes the gas limit of the next block after parent. It aims
// to keep the baseline gas close to the provided target, and increase it towards
// the target if the baseline gas is lower.
func CalcGasLimit(parent *types.WorkObject, gasCeil uint64) uint64 {
	// No Gas for TimeToStartTx days worth of zone blocks, this gives enough time to
	// onboard new miners into the slice
	if parent.NumberU64(common.ZONE_CTX) < params.TimeToStartTx {
		return 0
	}

	// If parent gas is zero and we have passed the 5 day threshold, we can set the first block gas limit to min gas limit
	if parent.GasLimit() == 0 {
		return params.MinGasLimit
	}

	parentGasLimit := parent.GasLimit()

	delta := parentGasLimit/params.GasLimitBoundDivisor - 1
	limit := parentGasLimit

	var desiredLimit uint64
	percentGasUsed := parent.GasUsed() * 100 / parent.GasLimit()
	if percentGasUsed > params.PercentGasUsedThreshold {
		desiredLimit = CalcGasCeil(parent.NumberU64(common.ZONE_CTX), gasCeil)
		if desiredLimit > gasCeil {
			desiredLimit = gasCeil
		}
		if limit+delta > desiredLimit {
			return desiredLimit
		} else {
			return limit + delta
		}
	} else {
		desiredLimit = params.MinGasLimit
		if limit-delta/2 < desiredLimit {
			return desiredLimit
		} else {
			return limit - delta/2
		}
	}
}

func CalcGasCeil(blockNumber uint64, gasCeil uint64) uint64 {
	if blockNumber < params.GasLimitStepOneBlockThreshold {
		return gasCeil / 4
	} else if blockNumber < params.GasLimitStepTwoBlockThreshold {
		return gasCeil / 2
	} else if blockNumber < params.GasLimitStepThreeBlockThreshold {
		return gasCeil * 3 / 4
	}
	return gasCeil
}
