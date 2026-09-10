package nipopow

import (
	"errors"
	"math/big"
	"reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/core/types"
)

func testForkPrimeBlock(t *testing.T, number uint64, parent *types.WorkObject, tag string) *types.WorkObject {
	t.Helper()
	wo := testPrimeBlock(t, number, parent, nil)
	wo.Header().SetExtra([]byte(tag))
	setTestInterlinks(t, wo, nil)
	return wo
}

func TestScorePrimeProofBuildsSuperchainCounts(t *testing.T) {
	difficulty := big.NewInt(2)
	anchor := testPrimeBlockWithDifficulty(t, 1, nil, nil, difficulty)
	mid := testPrimeBlockWithDifficulty(t, 2, anchor, nil, difficulty)
	tip := testPrimeBlockWithDifficulty(t, 3, mid, nil, difficulty)
	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       1,
		Headers: []*types.WorkObject{anchor, mid, tip},
	}

	score, err := ScorePrimeProof(proof, fakePrimePoWVerifier{
		anchor.Hash(): powHashForRank(t, difficulty, 0),
		mid.Hash():    powHashForRank(t, difficulty, 2),
		tip.Hash():    powHashForRank(t, difficulty, 1),
	})
	if err != nil {
		t.Fatalf("expected proof to score: %v", err)
	}
	if score.Tip != tip.Hash() || score.TipNumber != 3 || score.MaxRank != 2 {
		t.Fatalf("unexpected score metadata: %+v", score)
	}
	wantCounts := []uint64{3, 2, 1}
	if !reflect.DeepEqual(score.SuperchainCounts, wantCounts) {
		t.Fatalf("unexpected superchain counts: want %v got %v", wantCounts, score.SuperchainCounts)
	}
}

func TestComparePrimeProofsPrefersHigherSuperchainCount(t *testing.T) {
	difficulty := big.NewInt(2)
	anchor := testPrimeBlockWithDifficulty(t, 1, nil, nil, difficulty)

	a2 := testForkPrimeBlock(t, 2, anchor, "a2")
	a3 := testForkPrimeBlock(t, 3, a2, "a3")
	proofA := &Proof{Anchor: anchor.Hash(), M: 1, Headers: []*types.WorkObject{anchor, a2, a3}}

	b2 := testForkPrimeBlock(t, 2, anchor, "b2")
	b3 := testForkPrimeBlock(t, 3, b2, "b3")
	proofB := &Proof{Anchor: anchor.Hash(), M: 1, Headers: []*types.WorkObject{anchor, b2, b3}}

	cmp, scoreA, scoreB, err := ComparePrimeProofs(proofA, proofB, fakePrimePoWVerifier{
		anchor.Hash(): powHashForRank(t, difficulty, 0),
		a2.Hash():     powHashForRank(t, difficulty, 2),
		a3.Hash():     powHashForRank(t, difficulty, 0),
		b2.Hash():     powHashForRank(t, difficulty, 1),
		b3.Hash():     powHashForRank(t, difficulty, 1),
	})
	if err != nil {
		t.Fatalf("expected proofs to compare: %v", err)
	}
	if cmp <= 0 {
		t.Fatalf("expected proof A to win, cmp=%d scoreA=%+v scoreB=%+v", cmp, scoreA, scoreB)
	}
}

func TestComparePrimeProofsRejectsDifferentAnchors(t *testing.T) {
	difficulty := big.NewInt(2)
	anchorA := testPrimeBlockWithDifficulty(t, 1, nil, nil, difficulty)
	anchorB := testForkPrimeBlock(t, 1, nil, "anchorB")
	proofA := &Proof{Anchor: anchorA.Hash(), M: 1, Headers: []*types.WorkObject{anchorA}}
	proofB := &Proof{Anchor: anchorB.Hash(), M: 1, Headers: []*types.WorkObject{anchorB}}

	_, _, _, err := ComparePrimeProofs(proofA, proofB, fakePrimePoWVerifier{
		anchorA.Hash(): powHashForRank(t, difficulty, 0),
		anchorB.Hash(): powHashForRank(t, difficulty, 0),
	})
	if !errors.Is(err, ErrIncomparableProofs) {
		t.Fatalf("expected incomparable proof error, got %v", err)
	}
}
