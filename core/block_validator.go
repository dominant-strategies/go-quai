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
	"math/big"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto/multiset"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// BlockValidator is responsible for validating block headers, uncles and
// processed state.
//
// BlockValidator implements Validator.
type BlockValidator struct {
	config  *params.ChainConfig // Chain configuration options
	hc      *HeaderChain        // HeaderChain
	engines []consensus.Engine  // Consensus engines used for validating
}

// NewBlockValidator returns a new block validator which is safe for re-use
func NewBlockValidator(config *params.ChainConfig, headerChain *HeaderChain, engines []consensus.Engine) *BlockValidator {
	validator := &BlockValidator{
		config:  config,
		engines: engines,
		hc:      headerChain,
	}
	return validator
}

// getEngineForHeader returns the appropriate consensus engine for the given header
func (v *BlockValidator) getEngineForHeader(header *types.WorkObjectHeader) consensus.Engine {
	// Use HeaderChain's engine selection logic
	return v.hc.GetEngineForHeader(header)
}

// ValidateBody validates the given block's uncles and verifies the block
// header's transaction and uncle roots. The headers are assumed to be already
// validated at this point.
func (v *BlockValidator) ValidateBody(block *types.WorkObject) error {
	nodeCtx := v.config.Location.Context()
	header := block.Header()
	// Subordinate manifest must match ManifestHash in subordinate context, _iff_
	// we have a subordinate (i.e. if we are not a zone)
	if nodeCtx != common.ZONE_CTX {
		// Region nodes should have body with zero length txs and etxs
		if len(block.Transactions()) != 0 {
			return fmt.Errorf("region body has non zero transactions")
		}
		if len(block.OutboundEtxs()) != 0 {
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
		if err := v.hc.VerifyUncles(block); err != nil {
			return err
		}
		if hash := types.CalcUncleHash(block.Uncles()); hash != header.UncleHash() {
			return fmt.Errorf("uncle root hash mismatch: have %x, want %x", hash, header.UncleHash())
		}
		if hash := types.DeriveSha(block.Transactions(), trie.NewStackTrie(nil)); hash != header.TxHash() {
			return fmt.Errorf("transaction root hash mismatch: have %x, want %x", hash, header.TxHash())
		}
		activeLocations := common.NewChainsAdded(v.hc.currentExpansionNumber)
		for _, tx := range block.Transactions() {
			if types.QiTxType == tx.Type() {
				for _, txo := range tx.TxOut() {
					found := false
					for _, activeLoc := range activeLocations {
						if common.IsInChainScope(txo.Address, activeLoc) {
							found = true
						}
					}
					if !found {
						return fmt.Errorf("Qi TXO emitted to an inactive chain")
					}
				}
			}
		}
		// The header view should have the etxs populated
		if hash := types.DeriveSha(block.OutboundEtxs(), trie.NewStackTrie(nil)); hash != header.OutboundEtxHash() {
			return fmt.Errorf("outbound etx hash mismatch: have %x, want %x", hash, header.OutboundEtxHash())
		}
	}
	return nil
}

// SanityCheckWorkObjectBlockViewBody is used in the case of gossipsub validation, it quickly checks if any of the fields
// that are supposed to be empty are not for the work object block view
func (v *BlockValidator) SanityCheckWorkObjectBlockViewBody(wo *types.WorkObject) error {
	if wo == nil {
		return fmt.Errorf("wo is nil")
	}
	if wo.Header() == nil {
		return fmt.Errorf("wo header is nil")
	}
	if wo.WorkObjectHeader() == nil {
		return fmt.Errorf("wo work object header is nil")
	}
	if err := v.hc.CheckPowIdValidity(wo.WorkObjectHeader()); err != nil {
		return err
	}
	if wo.WorkObjectHeader().Lock() > uint8(len(params.LockupByteToBlockDepth)-1) {
		return fmt.Errorf("work object header has invalid lockup byte")
	}

	nodeCtx := v.config.Location.Context()
	header := wo.Header()
	// Subordinate manifest must match ManifestHash in subordinate context, _iff_
	// we have a subordinate (i.e. if we are not a zone)
	if nodeCtx != common.ZONE_CTX {
		// Region nodes should have body with zero length txs and etxs
		if len(wo.Transactions()) != 0 {
			return fmt.Errorf("region body has non zero transactions")
		}
		if len(wo.OutboundEtxs()) != 0 {
			return fmt.Errorf("region body has non zero etx transactions")
		}
		if len(wo.Uncles()) != 0 {
			return fmt.Errorf("region body has non zero uncles")
		}
		subManifestHash := types.DeriveSha(wo.Manifest(), trie.NewStackTrie(nil))
		if subManifestHash == types.EmptyRootHash || subManifestHash != header.ManifestHash(nodeCtx+1) {
			// If we have a subordinate chain, it is impossible for the subordinate manifest to be empty
			return ErrBadSubManifest
		}
		if nodeCtx == common.PRIME_CTX {
			interlinkRootHash := types.DeriveSha(wo.InterlinkHashes(), trie.NewStackTrie(nil))
			if interlinkRootHash != header.InterlinkRootHash() {
				return ErrBadInterlink
			}
		}
	} else {
		if len(wo.Manifest()) != 0 {
			return fmt.Errorf("zone body has non zero manifests")
		}
		if len(wo.InterlinkHashes()) != 0 {
			return fmt.Errorf("zone body has non zero interlink hashes")
		}
		if hash := types.CalcUncleHash(wo.Uncles()); hash != header.UncleHash() {
			return fmt.Errorf("uncle root hash mismatch: have %x, want %x", hash, header.UncleHash())
		}
		if hash := types.DeriveSha(wo.Transactions(), trie.NewStackTrie(nil)); hash != header.TxHash() {
			return fmt.Errorf("transaction root hash mismatch: have %x, want %x", hash, header.TxHash())
		}
		// The header view should have the etxs populated
		if hash := types.DeriveSha(wo.OutboundEtxs(), trie.NewStackTrie(nil)); hash != header.OutboundEtxHash() {
			return fmt.Errorf("outbound transaction hash mismatch: have %x, want %x", hash, header.OutboundEtxHash())
		}
		if wo.PrimaryCoinbase().IsInQiLedgerScope() && wo.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
			return fmt.Errorf("work object primary coinbase is in qi ledger scope before controller kick in block")
		}
	}
	return nil
}

func (v *BlockValidator) ApplyPoWFilter(wo *types.WorkObject) pubsub.ValidationResult {
	var err error
	powhash, exists := v.hc.powHashCache.Peek(wo.Hash())
	if !exists {
		powhash, err = v.hc.VerifySeal(wo.WorkObjectHeader())
		if err != nil {
			return pubsub.ValidationReject
		}
		v.hc.powHashCache.Add(wo.Hash(), powhash)
	}
	newBlockIntrinsic := common.IntrinsicLogEntropy(powhash)

	currentHeader := v.hc.CurrentHeader()
	currentHeaderHash := currentHeader.Hash()
	// cannot have a pow filter when the current header is genesis
	if v.hc.IsGenesisHash(currentHeaderHash) {
		return pubsub.ValidationAccept
	}

	currentHeaderPowHash, exists := v.hc.powHashCache.Peek(currentHeaderHash)
	if !exists {
		currentHeaderPowHash, err = v.hc.VerifySeal(currentHeader.WorkObjectHeader())
		if err != nil {
			return pubsub.ValidationReject
		}
		v.hc.powHashCache.Add(currentHeaderHash, currentHeaderPowHash)
	}
	currentHeaderIntrinsic := common.IntrinsicLogEntropy(currentHeaderPowHash)

	// Check if the Block is atleast half the current difficulty in Zone Context,
	// this makes sure that the nodes don't listen to the forks with the PowHash
	//	with less than 50% of current difficulty
	if v.hc.NodeCtx() == common.ZONE_CTX && newBlockIntrinsic.Cmp(new(big.Int).Div(currentHeaderIntrinsic, big.NewInt(2))) < 0 {
		return pubsub.ValidationIgnore
	}

	currentS := currentHeader.ParentEntropy(v.hc.NodeCtx())
	MaxAllowableEntropyDist := new(big.Int).Mul(currentHeaderIntrinsic, new(big.Int).SetUint64(params.MaxAllowableEntropyDist))

	broadCastEntropy := wo.ParentEntropy(common.ZONE_CTX)

	// If someone is mining not within MaxAllowableEntropyDist*currentIntrinsicS dont broadcast
	if currentS.Cmp(new(big.Int).Add(broadCastEntropy, MaxAllowableEntropyDist)) > 0 {
		return pubsub.ValidationIgnore
	}

	// Quickly validate the header and propagate the block if it passes
	err = v.hc.VerifyHeader(wo)

	// Including the ErrUnknownAncestor as well because a filter has already
	// been applied for all the blocks that come until here. Since there
	// exists a timedCache where the blocks expire, it is okay to let this
	// block through and broadcast the block.
	if err == nil || err.Error() == consensus.ErrUnknownAncestor.Error() {
		return pubsub.ValidationAccept
	} else if err.Error() == consensus.ErrFutureBlock.Error() {
		v.hc.logger.WithField("hash", wo.Hash()).WithError(err).Debug("Future block, ignoring")
		// Weird future block, don't fail, but neither propagate
		return pubsub.ValidationIgnore
	} else {
		v.hc.logger.WithField("hash", wo.Hash()).WithError(err).Debug("Invalid block, rejecting")
		return pubsub.ValidationReject
	}
}

// SanityCheckWorkObjectHeaderViewBody is used in the case of gossipsub validation, it quickly checks if any of the fields
// that are supposed to be empty are not for the work object header view
func (v *BlockValidator) SanityCheckWorkObjectHeaderViewBody(wo *types.WorkObject) error {
	if wo == nil {
		return fmt.Errorf("wo is nil")
	}
	if wo.Header() == nil {
		return fmt.Errorf("wo header is nil")
	}
	if wo.WorkObjectHeader() == nil {
		return fmt.Errorf("wo work object header is nil")
	}
	if err := v.hc.CheckPowIdValidity(wo.WorkObjectHeader()); err != nil {
		return err
	}
	if wo.WorkObjectHeader().Lock() > uint8(len(params.LockupByteToBlockDepth)-1) {
		return fmt.Errorf("work object header has invalid lockup byte")
	}
	header := wo.Header()
	nodeCtx := v.config.Location.Context()
	// Subordinate manifest must match ManifestHash in subordinate context, _iff_
	// we have a subordinate (i.e. if we are not a zone)
	if nodeCtx != common.ZONE_CTX {
		// Region nodes should have body with zero length txs and etxs
		if len(wo.Transactions()) != 0 {
			return fmt.Errorf("region body has non zero transactions")
		}
		if len(wo.OutboundEtxs()) != 0 {
			return fmt.Errorf("region body has non zero etx transactions")
		}
		if len(wo.Uncles()) != 0 {
			return fmt.Errorf("region body has non zero uncles")
		}
		subManifestHash := types.DeriveSha(wo.Manifest(), trie.NewStackTrie(nil))
		if subManifestHash == types.EmptyRootHash || subManifestHash != header.ManifestHash(nodeCtx+1) {
			// If we have a subordinate chain, it is impossible for the subordinate manifest to be empty
			return ErrBadSubManifest
		}
		if nodeCtx == common.PRIME_CTX {
			interlinkRootHash := types.DeriveSha(wo.InterlinkHashes(), trie.NewStackTrie(nil))
			if interlinkRootHash != header.InterlinkRootHash() {
				return ErrBadInterlink
			}
		}
	} else {
		// Transactions, SubManifestHash, InterlinkHashes should be nil in the workshare in Zone context
		if len(wo.Transactions()) != 0 {
			return fmt.Errorf("zone body has non zero transactions")
		}
		if len(wo.Manifest()) != 0 {
			return fmt.Errorf("zone body has non zero manifests")
		}
		if len(wo.InterlinkHashes()) != 0 {
			return fmt.Errorf("zone body has non zero interlink hashes")
		}
		// The header view should have the etxs populated
		if hash := types.DeriveSha(wo.OutboundEtxs(), trie.NewStackTrie(nil)); hash != header.OutboundEtxHash() {
			return fmt.Errorf("outbound transaction hash mismatch: have %x, want %x", hash, header.OutboundEtxHash())
		}
		if wo.PrimaryCoinbase().IsInQiLedgerScope() && wo.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
			return fmt.Errorf("work object primary coinbase is in qi ledger scope before controller kick in block")
		}
	}
	return nil
}

// SanityCheckWorkObjectShareViewBody is used in the case of gossipsub validation, it quickly checks if any of the fields
// that are supposed to be empty are not for the work object share view
func (v *BlockValidator) SanityCheckWorkObjectShareViewBody(wo *types.WorkObject) error {
	nodeCtx := v.config.Location.Context()
	if nodeCtx != common.ZONE_CTX {
		return fmt.Errorf("work object shares dont exist in non zone chains")
	}
	if wo == nil {
		return fmt.Errorf("wo is nil")
	}
	if wo.WorkObjectHeader() == nil {
		return fmt.Errorf("work object header is nil")
	}
	if err := v.hc.CheckPowIdValidityForWorkshare(wo.WorkObjectHeader()); err != nil {
		return err
	}
	// Lockup byte for the first two months has to be zero
	if wo.WorkObjectHeader().NumberU64() < 2*params.BlocksPerMonth && wo.WorkObjectHeader().Lock() != 0 {
		return fmt.Errorf("work object header has invalid lockup byte")
	}
	if wo.WorkObjectHeader().Lock() > uint8(len(params.LockupByteToBlockDepth)-1) {
		return fmt.Errorf("work object header has invalid lockup byte")
	}
	// Transactions, SubManifestHash, InterlinkHashes should be nil in the workshare in Zone context
	if len(wo.OutboundEtxs()) != 0 {
		return fmt.Errorf("zone body has non zero transactions")
	}
	if len(wo.Manifest()) != 0 {
		return fmt.Errorf("zone body has non zero manifests")
	}
	if len(wo.InterlinkHashes()) != 0 {
		return fmt.Errorf("zone body has non zero interlink hashes")
	}
	if len(wo.Uncles()) != 0 {
		return fmt.Errorf("zone body has non zero uncles")
	}
	// check if the txs in the workObject hash to the tx hash in the body header
	if hash := types.DeriveSha(wo.Transactions(), trie.NewStackTrie(nil)); hash != wo.TxHash() {
		return fmt.Errorf("transaction root hash mismatch: have %x, want %x", hash, wo.TxHash())
	}
	if wo.PrimaryCoinbase().IsInQiLedgerScope() && wo.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
		return fmt.Errorf("work object primary coinbase is in qi ledger scope before controller kick in block")
	}

	return nil
}

// ValidateState validates the various changes that happen after a state
// transition, such as amount of used gas, the receipt roots and the state root
// itself. ValidateState returns a database batch if the validation was a success
// otherwise nil and an error is returned.
func (v *BlockValidator) ValidateState(block *types.WorkObject, statedb *state.StateDB, receipts types.Receipts, etxs types.Transactions, multiSet *multiset.MultiSet, usedGas uint64, usedState uint64) error {
	start := time.Now()
	header := types.CopyHeader(block.Header())
	time1 := common.PrettyDuration(time.Since(start))
	if block.GasUsed() != usedGas {
		return fmt.Errorf("invalid gas used (remote: %d local: %d)", block.GasUsed(), usedGas)
	}
	if block.StateUsed() != usedState {
		return fmt.Errorf("invalid state used (remote: %d local: %d)", block.StateUsed(), usedState)
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
	if stateSize := statedb.GetQuaiTrieSize(); header.QuaiStateSize().Cmp(stateSize) != 0 {
		return fmt.Errorf("invalid quai trie size (remote: %x local: %x)", header.QuaiStateSize(), stateSize)
	}
	if root := multiSet.Hash(); header.UTXORoot() != root {
		return fmt.Errorf("invalid utxo root (remote: %x local: %x)", header.UTXORoot(), root)
	}
	if root := statedb.ETXRoot(); header.EtxSetRoot() != root {
		return fmt.Errorf("invalid etx root (remote: %x local: %x)", header.EtxSetRoot(), root)
	}
	time5 := common.PrettyDuration(time.Since(start))
	time6 := common.PrettyDuration(time.Since(start))

	// Confirm the ETXs emitted by the transactions in this block exactly match the
	// ETXs given in the block body
	if etxHash := types.DeriveSha(etxs, trie.NewStackTrie(nil)); etxHash != header.OutboundEtxHash() {
		return fmt.Errorf("invalid outbound etx hash (remote: %x local: %x)", header.OutboundEtxHash(), etxHash)
	}

	// Check that the UncledEntropy in the header matches the S from the block
	expectedUncledEntropy := v.hc.UncledLogEntropy(block)
	if expectedUncledEntropy.Cmp(header.UncledEntropy()) != 0 {
		return fmt.Errorf("invalid uncledEntropy (remote: %x local: %x)", header.UncledEntropy(), expectedUncledEntropy)
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

	minGasLimit := params.MinGasLimit(parent.NumberU64(common.ZONE_CTX))
	// If parent gas is zero and we have passed the 5 day threshold, we can set the first block gas limit to min gas limit
	if parent.GasLimit() == 0 {
		return minGasLimit
	}

	//  For the first two months increment the gas limit slowly, then just
	//  return the max gas limit
	if parent.NumberU64(common.ZONE_CTX) < 2*params.BlocksPerMonth {
		gasLimit := (parent.NumberU64(common.ZONE_CTX) * gasCeil) / (2 * params.BlocksPerMonth)
		if gasLimit < minGasLimit {
			return minGasLimit
		}
		return gasLimit
	} else {
		return gasCeil
	}
}
