package nipopow

import (
	"errors"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

func TestValidatePrimeProofAdversarialCasesRejectsExpectedMutations(t *testing.T) {
	proof := validAdversarialPrimeProof(t)

	report, err := ValidatePrimeProofAdversarialCases(proof, []AdversarialCase{
		{
			Name:          "anchor-mismatch",
			ExpectedError: ErrAnchorMismatch,
			Mutate: func(proof *Proof) error {
				proof.Anchor = common.HexToHash("0x01")
				return nil
			},
		},
		{
			Name:          "nil-header",
			ExpectedError: ErrNilHeader,
			Mutate: func(proof *Proof) error {
				proof.Headers[1] = nil
				return nil
			},
		},
		{
			Name:          "swapped-body-header",
			ExpectedError: ErrHeaderHashMismatch,
			Mutate: func(proof *Proof) error {
				proof.Headers[1].SetBody(types.CopyWorkObject(proof.Headers[0]).Body())
				return nil
			},
		},
		{
			Name:          "bad-interlink-root",
			ExpectedError: ErrInterlinkRootMismatch,
			Mutate: func(proof *Proof) error {
				proof.Headers[1].Body().SetInterlinkHashes(common.Hashes{})
				return nil
			},
		},
		{
			Name:          "disconnected-prefix",
			ExpectedError: ErrDisconnectedHeaders,
			Mutate: func(proof *Proof) error {
				proof.Headers[1] = testPrimeBlock(t, 9, nil, nil)
				return nil
			},
		},
		{
			Name:          "nonlinear-suffix",
			ExpectedError: ErrNonLinearSuffix,
			Mutate: func(proof *Proof) error {
				proof.Headers = []*types.WorkObject{proof.Headers[0], proof.Headers[1], proof.Headers[3]}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("expected adversarial report, got error: %v", err)
	}
	if report.Failed() {
		t.Fatalf("expected all adversarial cases to be rejected as expected: %+v", report.Results)
	}
	if !report.BaselineOK || report.BaselineError != "" {
		t.Fatalf("expected valid baseline: %+v", report)
	}
	if len(report.Results) != 6 {
		t.Fatalf("unexpected result count: %+v", report.Results)
	}
	for _, result := range report.Results {
		if !result.OK || result.Error == "" || result.ExpectedError == "" {
			t.Fatalf("unexpected adversarial result: %+v", result)
		}
	}
}

func TestValidatePrimeProofAdversarialCasesFailsWhenMutationStillVerifies(t *testing.T) {
	proof := validAdversarialPrimeProof(t)

	report, err := ValidatePrimeProofAdversarialCases(proof, []AdversarialCase{
		{
			Name:          "no-op",
			ExpectedError: ErrDisconnectedHeaders,
			Mutate: func(proof *Proof) error {
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("expected adversarial report, got error: %v", err)
	}
	if !report.Failed() || len(report.Results) != 1 {
		t.Fatalf("expected failed adversarial report: %+v", report)
	}
	result := report.Results[0]
	if result.OK || !errors.Is(result.Err, ErrAdversarialCaseUnexpectedPass) {
		t.Fatalf("expected unexpected-pass result, got %+v", result)
	}
}

func TestValidatePrimeProofAdversarialCasesRejectsInvalidHarnessConfig(t *testing.T) {
	_, err := ValidatePrimeProofAdversarialCases(validAdversarialPrimeProof(t), nil)
	if !errors.Is(err, ErrNoAdversarialCases) {
		t.Fatalf("expected no-cases error, got %v", err)
	}

	_, err = ValidatePrimeProofAdversarialCases(validAdversarialPrimeProof(t), []AdversarialCase{{Name: "nil-mutator"}})
	if !errors.Is(err, ErrNilAdversarialMutator) {
		t.Fatalf("expected nil-mutator error, got %v", err)
	}
}

func FuzzVerifyPrimeProofRejectsUnsafeMutations(f *testing.F) {
	for seed := byte(0); seed < 8; seed++ {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, mutation byte) {
		proof := validAdversarialPrimeProof(t)
		switch mutation % 8 {
		case 0:
			proof.M = 0
		case 1:
			proof.Headers = nil
		case 2:
			proof.Anchor = common.HexToHash("0x02")
		case 3:
			proof.Headers[1] = nil
		case 4:
			proof.Headers[1].Header().SetExtra([]byte{mutation})
		case 5:
			proof.Headers[1].Body().SetInterlinkHashes(common.Hashes{})
		case 6:
			proof.Headers = []*types.WorkObject{proof.Headers[0], proof.Headers[1], proof.Headers[3]}
		case 7:
			proof.Headers[1] = testPrimeBlock(t, 9, nil, nil)
		}
		if err := VerifyPrimeProof(proof); err == nil {
			t.Fatalf("mutation %d unexpectedly verified", mutation%8)
		}
	})
}

func validAdversarialPrimeProof(t *testing.T) *Proof {
	t.Helper()
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
		t.Fatalf("invalid adversarial baseline fixture: %v", err)
	}
	return proof
}
