package nipopow

import (
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/trie"
)

func testPrimeBlock(t *testing.T, number uint64, parent *types.WorkObject, interlinks common.Hashes) *types.WorkObject {
	return testPrimeBlockWithDifficulty(t, number, parent, interlinks, big.NewInt(1))
}

func testPrimeBlockWithDifficulty(t *testing.T, number uint64, parent *types.WorkObject, interlinks common.Hashes, difficulty *big.Int) *types.WorkObject {
	t.Helper()

	wo := types.EmptyWorkObject(common.PRIME_CTX)
	wo.WorkObjectHeader().SetNumber(new(big.Int).SetUint64(number))
	wo.WorkObjectHeader().SetDifficulty(new(big.Int).Set(difficulty))
	wo.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(number))
	wo.WorkObjectHeader().SetTime(number)

	header := wo.Header()
	header.SetNumber(new(big.Int).SetUint64(number), common.PRIME_CTX)
	header.SetParentDeltaEntropy(big.NewInt(1), common.PRIME_CTX)
	header.SetExpansionNumber(0)
	if parent != nil {
		header.SetParentHash(parent.Hash(), common.PRIME_CTX)
		wo.WorkObjectHeader().SetParentHash(parent.Hash())
	}
	setTestInterlinks(t, wo, interlinks)
	return wo
}

func setTestInterlinks(t *testing.T, wo *types.WorkObject, interlinks common.Hashes) {
	t.Helper()
	wo.Body().SetInterlinkHashes(interlinks)
	wo.Header().SetInterlinkRootHash(types.DeriveSha(interlinks, trie.NewStackTrie(nil)))
	wo.WorkObjectHeader().SetHeaderHash(wo.Header().Hash())
}

func TestVerifyPrimeProofAcceptsLinearSuffixAndInterlinkJump(t *testing.T) {
	anchor := testPrimeBlock(t, 1, nil, nil)
	jump := testPrimeBlock(t, 4, anchor, common.Hashes{anchor.Hash()})
	suffix1 := testPrimeBlock(t, 5, jump, common.Hashes{anchor.Hash(), jump.Hash()})
	suffix2 := testPrimeBlock(t, 6, suffix1, common.Hashes{jump.Hash()})

	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       2,
		Headers: []*types.WorkObject{anchor, jump, suffix1, suffix2},
	}

	if err := VerifyPrimeProof(proof); err != nil {
		t.Fatalf("expected proof to verify: %v", err)
	}
}

func TestVerifyPrimeProofRejectsMismatchedInterlinkRoot(t *testing.T) {
	anchor := testPrimeBlock(t, 1, nil, nil)
	jump := testPrimeBlock(t, 4, anchor, common.Hashes{anchor.Hash()})
	jump.Header().SetInterlinkRootHash(common.HexToHash("0x01"))
	jump.WorkObjectHeader().SetHeaderHash(jump.Header().Hash())

	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       1,
		Headers: []*types.WorkObject{anchor, jump},
	}

	if err := VerifyPrimeProof(proof); err == nil {
		t.Fatal("expected proof with mismatched interlink root to fail")
	}
}

func TestVerifyPrimeProofRejectsHeaderHashMismatch(t *testing.T) {
	anchor := testPrimeBlock(t, 1, nil, nil)
	jump := testPrimeBlock(t, 4, anchor, common.Hashes{anchor.Hash()})
	jump.Header().SetExtra([]byte("tampered without updating work object header hash"))

	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       1,
		Headers: []*types.WorkObject{anchor, jump},
	}

	if err := VerifyPrimeProof(proof); err == nil {
		t.Fatal("expected proof with mismatched body header hash to fail")
	}
}

func TestVerifyPrimeProofRejectsDisconnectedHeaders(t *testing.T) {
	anchor := testPrimeBlock(t, 1, nil, nil)
	unrelatedParent := testPrimeBlock(t, 2, nil, nil)
	disconnected := testPrimeBlock(t, 5, unrelatedParent, nil)

	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       1,
		Headers: []*types.WorkObject{anchor, disconnected},
	}

	if err := VerifyPrimeProof(proof); err == nil {
		t.Fatal("expected disconnected proof to fail")
	}
}

func TestVerifyPrimeProofRequiresLinearSuffix(t *testing.T) {
	anchor := testPrimeBlock(t, 1, nil, nil)
	jump := testPrimeBlock(t, 4, anchor, common.Hashes{anchor.Hash()})
	nonlinearSuffix := testPrimeBlock(t, 8, jump, common.Hashes{jump.Hash()})

	proof := &Proof{
		Anchor:  anchor.Hash(),
		M:       2,
		Headers: []*types.WorkObject{anchor, jump, nonlinearSuffix},
	}

	if err := VerifyPrimeProof(proof); err == nil {
		t.Fatal("expected non-linear suffix to fail")
	}
}

func TestVerifyPrimeProofRejectsBackwardInterlinkJump(t *testing.T) {
	newer := testPrimeBlock(t, 5, nil, nil)
	older := testPrimeBlock(t, 4, nil, common.Hashes{newer.Hash()})

	proof := &Proof{
		Anchor:  newer.Hash(),
		M:       1,
		Headers: []*types.WorkObject{newer, older},
	}

	err := VerifyPrimeProof(proof)
	if !errors.Is(err, ErrDisconnectedHeaders) {
		t.Fatalf("expected backward jump to be disconnected, got %v", err)
	}
}
