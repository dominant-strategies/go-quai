package nipopow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dominant-strategies/go-quai/common"
)

var (
	ErrNilValidationSource = errors.New("nil nipopow validation source")
	ErrNoValidationRanges  = errors.New("no nipopow validation ranges configured")
)

// PrimeValidationSource supplies canonical Prime hashes and hydrated proof
// headers for real-chain validation runs.
type PrimeValidationSource interface {
	PrimeProofSource
	GetCanonicalHash(number uint64) common.Hash
}

// ValidationRange identifies one canonical Prime range to build and verify.
type ValidationRange struct {
	Name         string `json:"name"`
	AnchorNumber uint64 `json:"anchorNumber"`
	TipNumber    uint64 `json:"tipNumber"`
}

// ValidationOptions configures a batch of Prime proof validation ranges.
type ValidationOptions struct {
	M      uint64            `json:"m"`
	Ranges []ValidationRange `json:"ranges"`
	Limits BuildLimits       `json:"limits"`
}

// ValidationReport is the structured output of a real-chain validation run.
type ValidationReport struct {
	M       uint64             `json:"m"`
	Limits  BuildLimits        `json:"limits"`
	Results []ValidationResult `json:"results"`
}

// Failed reports whether any validation range failed.
func (r *ValidationReport) Failed() bool {
	if r == nil {
		return true
	}
	for _, result := range r.Results {
		if !result.OK {
			return true
		}
	}
	return false
}

// ValidationResult contains the proof metrics and status for one range.
type ValidationResult struct {
	Name         string        `json:"name"`
	AnchorNumber uint64        `json:"anchorNumber"`
	TipNumber    uint64        `json:"tipNumber"`
	ChainLength  uint64        `json:"chainLength"`
	Anchor       common.Hash   `json:"anchor"`
	Tip          common.Hash   `json:"tip"`
	ProofHeaders uint64        `json:"proofHeaders"`
	ProofBytes   uint64        `json:"proofBytes"`
	Elapsed      time.Duration `json:"elapsed"`
	OK           bool          `json:"ok"`
	Error        string        `json:"error,omitempty"`
}

// ValidatePrimeProofRanges builds and verifies Prime NiPoPoW proofs for the
// configured canonical ranges. Range-level failures are recorded in the report
// so a real-chain validation run can continue across many samples; only invalid
// harness configuration is returned as a top-level error.
func ValidatePrimeProofRanges(ctx context.Context, source PrimeValidationSource, opts ValidationOptions) (*ValidationReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if source == nil {
		return nil, ErrNilValidationSource
	}
	if opts.M == 0 {
		return nil, ErrInvalidM
	}
	if len(opts.Ranges) == 0 {
		return nil, ErrNoValidationRanges
	}

	limits := opts.Limits.withDefaults()
	report := &ValidationReport{
		M:       opts.M,
		Limits:  limits,
		Results: make([]ValidationResult, 0, len(opts.Ranges)),
	}
	for _, validationRange := range opts.Ranges {
		report.Results = append(report.Results, validatePrimeProofRange(ctx, source, opts.M, limits, validationRange))
	}
	return report, nil
}

func validatePrimeProofRange(ctx context.Context, source PrimeValidationSource, m uint64, limits BuildLimits, validationRange ValidationRange) ValidationResult {
	result := ValidationResult{
		Name:         validationRange.name(),
		AnchorNumber: validationRange.AnchorNumber,
		TipNumber:    validationRange.TipNumber,
	}
	if validationRange.TipNumber < validationRange.AnchorNumber {
		result.Error = fmt.Sprintf("tip number %d is before anchor number %d", validationRange.TipNumber, validationRange.AnchorNumber)
		return result
	}
	result.ChainLength = validationRange.TipNumber - validationRange.AnchorNumber + 1

	anchor := source.GetCanonicalHash(validationRange.AnchorNumber)
	if anchor == (common.Hash{}) {
		result.Error = fmt.Sprintf("canonical anchor hash not found for number %d", validationRange.AnchorNumber)
		return result
	}
	tip := source.GetCanonicalHash(validationRange.TipNumber)
	if tip == (common.Hash{}) {
		result.Error = fmt.Sprintf("canonical tip hash not found for number %d", validationRange.TipNumber)
		return result
	}
	result.Anchor = anchor
	result.Tip = tip

	started := time.Now()
	proof, err := BuildPrimeProofWithContext(ctx, source, anchor, tip, m, limits)
	result.Elapsed = time.Since(started)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := VerifyPrimeProof(proof); err != nil {
		result.Error = err.Error()
		return result
	}
	result.ProofHeaders = uint64(len(proof.Headers))
	result.ProofBytes = estimateProofBytes(proof)
	result.OK = true
	return result
}

func estimateProofBytes(proof *Proof) uint64 {
	if proof == nil {
		return 0
	}
	size := uint64(common.HashLength + 8) // anchor + m
	for _, header := range proof.Headers {
		if header == nil {
			continue
		}
		headerSize := uint64(header.Size())
		if headerSize == 0 {
			headerSize = uint64(2*common.HashLength + 8 + common.HashLength*len(header.InterlinkHashes()))
		}
		size += headerSize
	}
	return size
}

func (r ValidationRange) name() string {
	if r.Name != "" {
		return r.Name
	}
	return fmt.Sprintf("%d-%d", r.AnchorNumber, r.TipNumber)
}
