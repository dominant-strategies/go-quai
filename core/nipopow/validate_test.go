package nipopow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

type validationTestSource struct {
	fakePrimeProofSource
	canonical map[uint64]common.Hash
}

func newValidationTestSource(headers ...*types.WorkObject) *validationTestSource {
	source := &validationTestSource{
		fakePrimeProofSource: sourceFromHeaders(headers...),
		canonical:            make(map[uint64]common.Hash),
	}
	for _, header := range headers {
		number := header.NumberU64(common.PRIME_CTX)
		source.canonical[number] = header.Hash()
	}
	return source
}

func (s *validationTestSource) GetCanonicalHash(number uint64) common.Hash {
	return s.canonical[number]
}

func TestValidatePrimeProofRangesReportsSuccessfulRange(t *testing.T) {
	b1 := testPrimeBlock(t, 1, nil, nil)
	b2 := testPrimeBlock(t, 2, b1, nil)
	b3 := testPrimeBlock(t, 3, b2, nil)
	b4 := testPrimeBlock(t, 4, b3, common.Hashes{b1.Hash()})
	b5 := testPrimeBlock(t, 5, b4, nil)
	b6 := testPrimeBlock(t, 6, b5, nil)
	source := newValidationTestSource(b1, b2, b3, b4, b5, b6)

	report, err := ValidatePrimeProofRanges(context.Background(), source, ValidationOptions{
		M: 2,
		Ranges: []ValidationRange{{
			Name:         "interlink-jump",
			AnchorNumber: 1,
			TipNumber:    6,
		}},
		Limits: BuildLimits{MaxChainLength: 16, MaxProofHeaders: 16, MaxM: 16},
	})
	if err != nil {
		t.Fatalf("expected validation report: %v", err)
	}
	if report.Failed() {
		t.Fatalf("expected report to pass: %+v", report.Results)
	}
	if report.M != 2 || len(report.Results) != 1 {
		t.Fatalf("unexpected report shape: %+v", report)
	}

	result := report.Results[0]
	if !result.OK || result.Error != "" {
		t.Fatalf("expected successful result: %+v", result)
	}
	if result.Name != "interlink-jump" {
		t.Fatalf("unexpected result name: %q", result.Name)
	}
	if result.Anchor != b1.Hash() || result.Tip != b6.Hash() {
		t.Fatalf("unexpected endpoints: anchor=%s tip=%s", result.Anchor, result.Tip)
	}
	if result.AnchorNumber != 1 || result.TipNumber != 6 || result.ChainLength != 6 {
		t.Fatalf("unexpected range metrics: %+v", result)
	}
	if result.ProofHeaders != 4 {
		t.Fatalf("expected compressed proof to contain 4 headers, got %d", result.ProofHeaders)
	}
	if result.ProofBytes == 0 {
		t.Fatal("expected non-zero encoded proof size")
	}
}

func TestValidatePrimeProofRangesContinuesAfterRangeFailure(t *testing.T) {
	b1 := testPrimeBlock(t, 1, nil, nil)
	b2 := testPrimeBlock(t, 2, b1, nil)
	source := newValidationTestSource(b1, b2)
	delete(source.canonical, 1)

	report, err := ValidatePrimeProofRanges(context.Background(), source, ValidationOptions{
		M:      1,
		Ranges: []ValidationRange{{Name: "missing-anchor", AnchorNumber: 1, TipNumber: 2}},
		Limits: BuildLimits{MaxChainLength: 4, MaxProofHeaders: 4, MaxM: 4},
	})
	if err != nil {
		t.Fatalf("expected per-range failure report, got top-level error: %v", err)
	}
	if !report.Failed() || len(report.Results) != 1 {
		t.Fatalf("expected failed report: %+v", report)
	}
	result := report.Results[0]
	if result.OK {
		t.Fatalf("expected result failure: %+v", result)
	}
	if !strings.Contains(result.Error, "canonical anchor hash not found") {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}

func TestValidatePrimeProofRangesReportsReversedRangeFailure(t *testing.T) {
	b1 := testPrimeBlock(t, 1, nil, nil)
	b2 := testPrimeBlock(t, 2, b1, nil)
	source := newValidationTestSource(b1, b2)

	report, err := ValidatePrimeProofRanges(context.Background(), source, ValidationOptions{
		M:      1,
		Ranges: []ValidationRange{{Name: "reversed", AnchorNumber: 2, TipNumber: 1}},
		Limits: BuildLimits{MaxChainLength: 4, MaxProofHeaders: 4, MaxM: 4},
	})
	if err != nil {
		t.Fatalf("expected per-range failure report, got top-level error: %v", err)
	}
	if !report.Failed() || len(report.Results) != 1 {
		t.Fatalf("expected failed report: %+v", report)
	}
	if got := report.Results[0].Error; !strings.Contains(got, "before anchor") {
		t.Fatalf("unexpected reversed-range error: %q", got)
	}
}

func TestValidatePrimeProofRangesReportsLimitFailureAndDefaultName(t *testing.T) {
	b1 := testPrimeBlock(t, 1, nil, nil)
	b2 := testPrimeBlock(t, 2, b1, nil)
	source := newValidationTestSource(b1, b2)

	report, err := ValidatePrimeProofRanges(context.Background(), source, ValidationOptions{
		M:      2,
		Ranges: []ValidationRange{{AnchorNumber: 1, TipNumber: 2}},
		Limits: BuildLimits{MaxChainLength: 4, MaxProofHeaders: 4, MaxM: 1},
	})
	if err != nil {
		t.Fatalf("expected per-range failure report, got top-level error: %v", err)
	}
	if !report.Failed() || len(report.Results) != 1 {
		t.Fatalf("expected failed report: %+v", report)
	}
	result := report.Results[0]
	if result.Name != "1-2" {
		t.Fatalf("unexpected default result name: %q", result.Name)
	}
	if !strings.Contains(result.Error, "exceeds max") {
		t.Fatalf("unexpected limit error: %q", result.Error)
	}
}

func TestValidatePrimeProofRangesRejectsInvalidConfig(t *testing.T) {
	source := newValidationTestSource(testPrimeBlock(t, 1, nil, nil))
	_, err := ValidatePrimeProofRanges(context.Background(), source, ValidationOptions{M: 0})
	if err == nil {
		t.Fatal("expected invalid m to return a top-level error")
	}

	_, err = ValidatePrimeProofRanges(context.Background(), nil, ValidationOptions{M: 1})
	if err == nil {
		t.Fatal("expected nil source to return a top-level error")
	}

	_, err = ValidatePrimeProofRanges(context.Background(), source, ValidationOptions{M: 1})
	if !errors.Is(err, ErrNoValidationRanges) {
		t.Fatalf("expected missing ranges error, got %v", err)
	}
}
