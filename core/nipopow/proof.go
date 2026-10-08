// Package nipopow contains read-only NiPoPoW proof primitives.
//
// The initial implementation is intentionally non-consensus: it verifies the
// compact proof structure against WorkObject headers/bodies without changing
// block validation rules.
package nipopow

import (
	"errors"
	"fmt"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/trie"
)

var (
	ErrNilProof              = errors.New("nil nipopow proof")
	ErrNoHeaders             = errors.New("nipopow proof has no headers")
	ErrInvalidM              = errors.New("nipopow proof has invalid m")
	ErrInsufficientSuffix    = errors.New("nipopow proof has fewer headers than m")
	ErrAnchorMismatch        = errors.New("nipopow proof anchor mismatch")
	ErrNilHeader             = errors.New("nipopow proof contains nil header")
	ErrInterlinkRootMismatch = errors.New("nipopow proof interlink root mismatch")
	ErrDisconnectedHeaders   = errors.New("nipopow proof headers are disconnected")
	ErrNonLinearSuffix       = errors.New("nipopow proof suffix is not linear")
)

// Proof is a compact Prime-chain NiPoPoW proof.
//
// Headers are ordered oldest-to-newest. Adjacent headers may either be connected
// linearly by parent hash and height, or compactly by an interlink committed to
// by the newer header's InterlinkRootHash. The final M headers form the suffix
// and must be linearly connected.
type Proof struct {
	Anchor  common.Hash         `json:"anchor"`
	M       uint64              `json:"m"`
	Headers []*types.WorkObject `json:"headers"`
}

// Tip returns the newest header hash in the proof, or the zero hash if the
// proof is nil or empty.
func (p *Proof) Tip() common.Hash {
	if p == nil || len(p.Headers) == 0 || p.Headers[len(p.Headers)-1] == nil {
		return common.Hash{}
	}
	return p.Headers[len(p.Headers)-1].Hash()
}

// VerifyPrimeProof validates the read-only structural invariants of a Prime
// NiPoPoW proof.
//
// This structural primitive verifies header/body/interlink connectivity without
// consensus-engine access. Use VerifyPrimeProofWithPoW when the caller can
// supply ComputePowHash and needs seal/rank validation too.
func VerifyPrimeProof(proof *Proof) error {
	if proof == nil {
		return ErrNilProof
	}
	if proof.M == 0 {
		return ErrInvalidM
	}
	if len(proof.Headers) == 0 {
		return ErrNoHeaders
	}
	if uint64(len(proof.Headers)) < proof.M {
		return ErrInsufficientSuffix
	}

	for i, header := range proof.Headers {
		if err := verifyUsableHeader(header); err != nil {
			return fmt.Errorf("header %d: %w", i, err)
		}
		if err := verifyInterlinkRoot(header); err != nil {
			return fmt.Errorf("header %d: %w", i, err)
		}
		if i == 0 {
			if proof.Anchor != (common.Hash{}) && proof.Anchor != header.Hash() {
				return fmt.Errorf("%w: expected %s got %s", ErrAnchorMismatch, proof.Anchor.Hex(), header.Hash().Hex())
			}
			continue
		}
		if !connectedByParentOrInterlink(proof.Headers[i-1], header) {
			return fmt.Errorf("%w: %s -> %s", ErrDisconnectedHeaders, proof.Headers[i-1].Hash().Hex(), header.Hash().Hex())
		}
	}

	if err := verifyLinearSuffix(proof.Headers, int(proof.M)); err != nil {
		return err
	}
	return nil
}

func verifyUsableHeader(header *types.WorkObject) error {
	if header == nil || header.WorkObjectHeader() == nil || header.Body() == nil || header.Header() == nil {
		return ErrNilHeader
	}
	if header.WorkObjectHeader().HeaderHash() != header.Header().Hash() {
		return fmt.Errorf("%w: expected %s got %s", ErrHeaderHashMismatch, header.Header().Hash().Hex(), header.WorkObjectHeader().HeaderHash().Hex())
	}
	if header.Number(common.PRIME_CTX) == nil {
		return fmt.Errorf("%w: missing prime number", ErrNilHeader)
	}
	return nil
}

func verifyInterlinkRoot(header *types.WorkObject) error {
	expected := types.DeriveSha(header.InterlinkHashes(), trie.NewStackTrie(nil))
	if header.InterlinkRootHash() != expected {
		return fmt.Errorf("%w: expected %s got %s", ErrInterlinkRootMismatch, expected.Hex(), header.InterlinkRootHash().Hex())
	}
	return nil
}

func connectedByParentOrInterlink(prev, current *types.WorkObject) bool {
	return isLinearChild(prev, current) || isForwardInterlinkJump(prev, current)
}

func isForwardInterlinkJump(prev, current *types.WorkObject) bool {
	if prev == nil || current == nil || prev.Number(common.PRIME_CTX) == nil || current.Number(common.PRIME_CTX) == nil {
		return false
	}
	if current.Number(common.PRIME_CTX).Uint64() <= prev.Number(common.PRIME_CTX).Uint64() {
		return false
	}
	return interlinksContain(current.InterlinkHashes(), prev.Hash())
}

func verifyLinearSuffix(headers []*types.WorkObject, m int) error {
	if m <= 1 {
		return nil
	}
	start := len(headers) - m
	for i := start + 1; i < len(headers); i++ {
		if !isLinearChild(headers[i-1], headers[i]) {
			return fmt.Errorf("%w: %s -> %s", ErrNonLinearSuffix, headers[i-1].Hash().Hex(), headers[i].Hash().Hex())
		}
	}
	return nil
}

func isLinearChild(parent, child *types.WorkObject) bool {
	if parent == nil || child == nil || parent.Number(common.PRIME_CTX) == nil || child.Number(common.PRIME_CTX) == nil {
		return false
	}
	if child.ParentHash(common.PRIME_CTX) != parent.Hash() {
		return false
	}
	parentNumber := parent.Number(common.PRIME_CTX)
	childNumber := child.Number(common.PRIME_CTX)
	return childNumber.Uint64() == parentNumber.Uint64()+1
}

func interlinksContain(interlinks common.Hashes, hash common.Hash) bool {
	for _, interlink := range interlinks {
		if interlink == hash {
			return true
		}
	}
	return false
}
