package nipopow

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

type fakePrimeProofSource map[common.Hash]*types.WorkObject

func (s fakePrimeProofSource) ProofHeader(hash common.Hash) (*types.WorkObject, error) {
	if header := s[hash]; header != nil {
		return header, nil
	}
	return nil, errors.New("missing header")
}

func sourceFromHeaders(headers ...*types.WorkObject) fakePrimeProofSource {
	source := make(fakePrimeProofSource)
	for _, header := range headers {
		source[header.Hash()] = header
	}
	return source
}

func proofHashes(proof *Proof) []common.Hash {
	hashes := make([]common.Hash, len(proof.Headers))
	for i, header := range proof.Headers {
		hashes[i] = header.Hash()
	}
	return hashes
}

func TestBuildPrimeProofUsesInterlinkJumpsBeforeLinearSuffix(t *testing.T) {
	b1 := testPrimeBlock(t, 1, nil, nil)
	b2 := testPrimeBlock(t, 2, b1, nil)
	b3 := testPrimeBlock(t, 3, b2, nil)
	b4 := testPrimeBlock(t, 4, b3, common.Hashes{b1.Hash()})
	b5 := testPrimeBlock(t, 5, b4, nil)
	b6 := testPrimeBlock(t, 6, b5, nil)
	source := sourceFromHeaders(b1, b2, b3, b4, b5, b6)

	proof, err := BuildPrimeProof(source, b1.Hash(), b6.Hash(), 2)
	if err != nil {
		t.Fatalf("expected proof to build: %v", err)
	}
	if err := VerifyPrimeProof(proof); err != nil {
		t.Fatalf("expected built proof to verify: %v", err)
	}

	want := []common.Hash{b1.Hash(), b4.Hash(), b5.Hash(), b6.Hash()}
	if got := proofHashes(proof); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected proof headers:\nwant %v\n got %v", want, got)
	}
}

func TestBuildPrimeProofFallsBackToLinearChainWithoutInterlinkJump(t *testing.T) {
	b1 := testPrimeBlock(t, 1, nil, nil)
	b2 := testPrimeBlock(t, 2, b1, nil)
	b3 := testPrimeBlock(t, 3, b2, nil)
	source := sourceFromHeaders(b1, b2, b3)

	proof, err := BuildPrimeProof(source, b1.Hash(), b3.Hash(), 2)
	if err != nil {
		t.Fatalf("expected proof to build: %v", err)
	}

	want := []common.Hash{b1.Hash(), b2.Hash(), b3.Hash()}
	if got := proofHashes(proof); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected fallback proof headers:\nwant %v\n got %v", want, got)
	}
}

func TestBuildPrimeProofErrorsWhenAnchorIsNotAncestor(t *testing.T) {
	anchor := testPrimeBlock(t, 1, nil, nil)
	other := testPrimeBlock(t, 9, nil, nil)
	tip := testPrimeBlock(t, 10, other, nil)
	source := sourceFromHeaders(anchor, other, tip)

	if _, err := BuildPrimeProof(source, anchor.Hash(), tip.Hash(), 1); err == nil {
		t.Fatal("expected missing-anchor error")
	}
}

func TestBuildPrimeProofWithContextRejectsCanceledContext(t *testing.T) {
	anchor := testPrimeBlock(t, 1, nil, nil)
	tip := testPrimeBlock(t, 2, anchor, nil)
	source := sourceFromHeaders(anchor, tip)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := BuildPrimeProofWithContext(ctx, source, anchor.Hash(), tip.Hash(), 1, DefaultBuildLimits)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

func TestBuildPrimeProofWithLimitsRejectsLongWalk(t *testing.T) {
	anchor := testPrimeBlock(t, 1, nil, nil)
	mid := testPrimeBlock(t, 2, anchor, nil)
	tip := testPrimeBlock(t, 3, mid, nil)
	source := sourceFromHeaders(anchor, mid, tip)

	_, err := BuildPrimeProofWithContext(context.Background(), source, anchor.Hash(), tip.Hash(), 1, BuildLimits{
		MaxChainLength:  2,
		MaxProofHeaders: 2,
		MaxM:            2,
	})
	if !errors.Is(err, ErrProofTooLarge) {
		t.Fatalf("expected proof-too-large error, got %v", err)
	}
}
