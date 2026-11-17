package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/nipopow"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/trie"
)

var (
	ErrNiPoPoWPrimeOnly       = errors.New("nipopow proof generation is only available in prime context")
	ErrNiPoPoWHeaderNotFound  = errors.New("nipopow header not found")
	ErrNiPoPoWInterlinkAbsent = errors.New("nipopow interlink hashes not found")
)

// ProofHeader returns a hydrated Prime proof header for nipopow.BuildPrimeProof.
func (hc *HeaderChain) ProofHeader(hash common.Hash) (*types.WorkObject, error) {
	return hc.GetPrimeNiPoPoWProofHeader(hash)
}

// GetPrimeNiPoPoWProofHeader returns a copy of a Prime header with the
// interlink hashes committed to by its InterlinkRootHash populated in the body.
// It is read-only and never calls CalculateInterlink, because CalculateInterlink
// writes interlink state.
func (hc *HeaderChain) GetPrimeNiPoPoWProofHeader(hash common.Hash) (*types.WorkObject, error) {
	if hc.NodeCtx() != common.PRIME_CTX {
		return nil, ErrNiPoPoWPrimeOnly
	}
	header := hc.GetHeaderByHash(hash)
	if header == nil {
		return nil, fmt.Errorf("%w: %s", ErrNiPoPoWHeaderNotFound, hash.Hex())
	}

	proofHeader := types.CopyWorkObject(header)
	interlinkSource := proofHeader.ParentHash(common.PRIME_CTX)
	if hc.IsGenesisHash(hash) {
		interlinkSource = hash
	}
	interlinks := rawdb.ReadInterlinkHashes(hc.headerDb, interlinkSource)
	if interlinks == nil {
		if !hc.IsGenesisHash(hash) {
			return nil, fmt.Errorf("%w for source %s", ErrNiPoPoWInterlinkAbsent, interlinkSource.Hex())
		}
		interlinks = common.Hashes{}
	}
	proofHeader.Body().SetInterlinkHashes(interlinks)

	expected := types.DeriveSha(interlinks, trie.NewStackTrie(nil))
	if proofHeader.InterlinkRootHash() != expected {
		return nil, fmt.Errorf("%w: expected %s got %s", nipopow.ErrInterlinkRootMismatch, expected.Hex(), proofHeader.InterlinkRootHash().Hex())
	}
	return proofHeader, nil
}

// BuildPrimeNiPoPoWProof builds a read-only Prime NiPoPoW proof from anchor to
// tip using headers and interlinks already stored by this node.
func (hc *HeaderChain) BuildPrimeNiPoPoWProof(ctx context.Context, anchor common.Hash, tip common.Hash, m uint64) (*nipopow.Proof, error) {
	if hc.NodeCtx() != common.PRIME_CTX {
		return nil, ErrNiPoPoWPrimeOnly
	}
	return nipopow.BuildPrimeProofWithContext(ctx, hc, anchor, tip, m, nipopow.DefaultBuildLimits)
}
