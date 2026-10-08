package nipopow

import (
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

type fakePrimePoWVerifier map[common.Hash]common.Hash

func (v fakePrimePoWVerifier) ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error) {
	if powHash, ok := v[header.Hash()]; ok {
		return powHash, nil
	}
	return common.Hash{}, errors.New("missing pow hash")
}

func hashFromBig(t *testing.T, n *big.Int) common.Hash {
	t.Helper()
	if n.Sign() < 0 || n.BitLen() > 256 {
		t.Fatalf("cannot encode %s as 256-bit hash", n.String())
	}
	return common.BytesToHash(n.Bytes())
}

func powHashForRank(t *testing.T, difficulty *big.Int, rank uint) common.Hash {
	t.Helper()
	target := new(big.Int).Div(common.Big2e256, difficulty)
	powHash := new(big.Int).Rsh(target, rank)
	return hashFromBig(t, powHash)
}

func powHashAboveTarget(t *testing.T, difficulty *big.Int) common.Hash {
	t.Helper()
	target := new(big.Int).Div(common.Big2e256, difficulty)
	return hashFromBig(t, new(big.Int).Add(target, common.Big1))
}

func TestVerifyPrimeProofWorkComputesRankFromPowHash(t *testing.T) {
	difficulty := big.NewInt(2)
	anchor := testPrimeBlockWithDifficulty(t, 1, nil, nil, difficulty)
	tip := testPrimeBlockWithDifficulty(t, 2, anchor, nil, difficulty)

	anchorPow := powHashForRank(t, difficulty, 0)
	tipPow := powHashForRank(t, difficulty, 2)
	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       1,
		Headers: []*types.WorkObject{anchor, tip},
	}

	work, err := VerifyPrimeProofWork(proof, fakePrimePoWVerifier{
		anchor.Hash(): anchorPow,
		tip.Hash():    tipPow,
	})
	if err != nil {
		t.Fatalf("expected proof work to verify: %v", err)
	}
	if len(work) != 2 {
		t.Fatalf("expected work for 2 headers, got %d", len(work))
	}
	if work[0].Hash != anchor.Hash() || work[0].PowHash != anchorPow || work[0].Rank != 0 {
		t.Fatalf("unexpected anchor work: %+v", work[0])
	}
	if work[1].Hash != tip.Hash() || work[1].PowHash != tipPow || work[1].Rank != 2 {
		t.Fatalf("unexpected tip work: %+v", work[1])
	}
}

func TestVerifyPrimeProofWithPoWRejectsPowHashAboveTarget(t *testing.T) {
	difficulty := big.NewInt(2)
	anchor := testPrimeBlockWithDifficulty(t, 1, nil, nil, difficulty)
	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       1,
		Headers: []*types.WorkObject{anchor},
	}

	err := VerifyPrimeProofWithPoW(proof, fakePrimePoWVerifier{
		anchor.Hash(): powHashAboveTarget(t, difficulty),
	})
	if !errors.Is(err, ErrInvalidPoW) {
		t.Fatalf("expected invalid PoW error, got %v", err)
	}
}

func TestVerifyPrimeProofWithPoWRequiresVerifier(t *testing.T) {
	difficulty := big.NewInt(2)
	anchor := testPrimeBlockWithDifficulty(t, 1, nil, nil, difficulty)
	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       1,
		Headers: []*types.WorkObject{anchor},
	}

	err := VerifyPrimeProofWithPoW(proof, nil)
	if !errors.Is(err, ErrNilPoWVerifier) {
		t.Fatalf("expected nil verifier error, got %v", err)
	}
}
