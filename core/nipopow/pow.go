package nipopow

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

var (
	ErrNilPoWVerifier    = errors.New("nil nipopow proof pow verifier")
	ErrInvalidDifficulty = errors.New("nipopow proof header has invalid difficulty")
	ErrInvalidPoW        = errors.New("nipopow proof header has invalid proof-of-work")
)

// PrimePoWVerifier supplies a consensus-engine PoW hash for a proof header.
// It intentionally matches consensus.Engine.ComputePowHash and the public API
// backend method so proof validation can be layered onto existing engine access.
type PrimePoWVerifier interface {
	ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error)
}

// HeaderWork is the verified PoW metadata for one proof header.
type HeaderWork struct {
	Hash    common.Hash `json:"hash"`
	PowHash common.Hash `json:"powHash"`
	Rank    uint64      `json:"rank"`
}

// VerifyPrimeProofWithPoW validates the Prime proof structure and each header's
// proof-of-work against its declared difficulty.
func VerifyPrimeProofWithPoW(proof *Proof, verifier PrimePoWVerifier) error {
	_, err := VerifyPrimeProofWork(proof, verifier)
	return err
}

// VerifyPrimeProofWork validates a Prime proof and returns per-header PoW rank.
// Rank is floor(log2(target / powHash)), where target = 2^256 / difficulty.
// Valid block proofs have rank >= 0; larger ranks indicate better-than-required
// work and are the primitive needed for later proof scoring.
func VerifyPrimeProofWork(proof *Proof, verifier PrimePoWVerifier) ([]HeaderWork, error) {
	if verifier == nil {
		return nil, ErrNilPoWVerifier
	}
	if err := VerifyPrimeProof(proof); err != nil {
		return nil, err
	}

	work := make([]HeaderWork, len(proof.Headers))
	for i, header := range proof.Headers {
		powHash, err := verifier.ComputePowHash(header.WorkObjectHeader())
		if err != nil {
			return nil, fmt.Errorf("header %d: compute pow hash: %w", i, err)
		}
		rank, err := CalcRank(powHash, header.Difficulty())
		if err != nil {
			return nil, fmt.Errorf("header %d: %w", i, err)
		}
		work[i] = HeaderWork{
			Hash:    header.Hash(),
			PowHash: powHash,
			Rank:    rank,
		}
	}
	return work, nil
}

// CalcRank verifies that powHash satisfies difficulty and returns its NiPoPoW
// superchain rank above the header's base target.
func CalcRank(powHash common.Hash, difficulty *big.Int) (uint64, error) {
	if difficulty == nil || difficulty.Sign() <= 0 {
		return 0, ErrInvalidDifficulty
	}
	target := new(big.Int).Div(common.Big2e256, difficulty)
	if target.Sign() <= 0 {
		return 0, ErrInvalidDifficulty
	}

	pow := new(big.Int).SetBytes(powHash.Bytes())
	if pow.Sign() == 0 {
		return 256, nil
	}
	if pow.Cmp(target) > 0 {
		return 0, ErrInvalidPoW
	}

	ratio := new(big.Int).Div(target, pow)
	return uint64(ratio.BitLen() - 1), nil
}
