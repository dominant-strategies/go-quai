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

	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/state"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/params"
	"github.com/spruce-solutions/go-quai/trie"
)

// BlockValidator is responsible for validating block headers, uncles and
// processed state.
//
// BlockValidator implements Validator.
type BlockValidator struct {
	config *params.ChainConfig // Chain configuration options
	hc     *HeaderChain        // Canonical header chain
	engine consensus.Engine    // Consensus engine used for validating
}

// NewBlockValidator returns a new block validator which is safe for re-use
func NewBlockValidator(config *params.ChainConfig, headerchain *HeaderChain, engine consensus.Engine) *BlockValidator {
	validator := &BlockValidator{
		config: config,
		engine: engine,
		hc:     headerchain,
	}
	return validator
}

// ValidateBody validates the given block's uncles and verifies the block
// header's transaction and uncle roots. The headers are assumed to be already
// validated at this point.
func (v *BlockValidator) ValidateBody(block *types.Block) error {
	// Check whether the block's known, and if not, that it's linkable
	if v.hc.bc.HasBlockAndState(block.Hash(), block.NumberU64()) {
		return ErrKnownBlock
	}
	// Header validity is known at this point, check the uncles and transactions
	header := block.Header()
	if err := v.engine.VerifyUncles(v.hc, block); err != nil {
		return err
	}
	if hash := types.CalcUncleHash(block.Uncles()); hash != header.UncleHash[types.QuaiNetworkContext] {
		return fmt.Errorf("uncle root hash mismatch: have %x, want %x", hash, header.UncleHash[types.QuaiNetworkContext])
	}
	if hash := types.DeriveSha(block.Transactions(), trie.NewStackTrie(nil)); hash != header.TxHash[types.QuaiNetworkContext] {
		return fmt.Errorf("transaction root hash mismatch: have %x, want %x", hash, header.TxHash)
	}
	if !v.hc.bc.HasBlockAndState(block.ParentHash(), block.NumberU64()-1) {
		if !v.hc.bc.HasBlock(block.ParentHash(), block.NumberU64()-1) {
			return consensus.ErrUnknownAncestor
		}
		return consensus.ErrPrunedAncestor
	}
	return nil
}

// ValidateState validates the various changes that happen after a state
// transition, such as amount of used gas, the receipt roots and the state root
// itself. ValidateState returns a database batch if the validation was a success
// otherwise nil and an error is returned.
func (v *BlockValidator) ValidateState(block *types.Block, statedb *state.StateDB, receipts types.Receipts, usedGas uint64) error {
	header := block.Header()
	if header.GasUsed[types.QuaiNetworkContext] != usedGas {
		return fmt.Errorf("invalid gas used (remote: %d local: %d)", block.GasUsed(), usedGas)
	}
	// Validate the received block's bloom with the one derived from the generated receipts.
	// For valid blocks this should always validate to true.
	rbloom := types.CreateBloom(receipts)
	if rbloom != header.Bloom[types.QuaiNetworkContext] {
		return fmt.Errorf("invalid bloom (remote: %x  local: %x)", header.Bloom, rbloom)
	}
	// Tre receipt Trie's root (R = (Tr [[H1, R1], ... [Hn, Rn]]))
	receiptSha := types.DeriveSha(receipts, trie.NewStackTrie(nil))
	if receiptSha != header.ReceiptHash[types.QuaiNetworkContext] {
		return fmt.Errorf("invalid receipt root hash (remote: %x local: %x)", header.ReceiptHash[types.QuaiNetworkContext], receiptSha)
	}
	// Validate the state root against the received state root and throw
	// an error if they don't match.
	if root := statedb.IntermediateRoot(v.config.IsEIP158(header.Number[types.QuaiNetworkContext])); header.Root[types.QuaiNetworkContext] != root {
		return fmt.Errorf("invalid merkle root (remote: %x local: %x)", header.Root, root)
	}
	return nil
}

// CalcGasLimit computes the gas limit of the next block after parent. It aims
// to keep blocks 95% full.  If we have achieved our max gas limit, we will expand
// our gas limit to reach our uncle rate.
func CalcGasLimit(parentGasLimit, gasUsed uint64, uncleCount int) uint64 {
	delta := parentGasLimit/params.GasLimitBoundDivisor - 1
	// Add 1000 check for uint64 division comparison to 95%
	percent := (gasUsed * 1000) / (parentGasLimit * 1000)
	limit := parentGasLimit
	aboveRate := uncleCount > params.TargetUncles[types.QuaiNetworkContext]
	// If we're receiving full blocks, we try to increase the block size
	if percent > uint64(800) {
		limit = parentGasLimit + delta
		if aboveRate {
			limit = limit - delta
		}
		return limit
	} else {
		limit = parentGasLimit - delta
		if limit < params.MinGasLimit {
			limit = params.MinGasLimit
		}
		if limit < params.EmptyGasLimit {
			limit = parentGasLimit + delta
		}
		return limit
	}
}
