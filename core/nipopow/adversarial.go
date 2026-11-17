package nipopow

import (
	"errors"
	"fmt"

	"github.com/dominant-strategies/go-quai/core/types"
)

var (
	ErrNoAdversarialCases            = errors.New("no nipopow adversarial cases configured")
	ErrNilAdversarialMutator         = errors.New("nil nipopow adversarial mutator")
	ErrAdversarialCaseUnexpectedPass = errors.New("adversarial nipopow case unexpectedly verified")
	ErrAdversarialCaseUnexpectedErr  = errors.New("adversarial nipopow case returned unexpected error")
)

// ProofMutation mutates a copied proof for adversarial validation. Mutators must
// not mutate the baseline proof passed to ValidatePrimeProofAdversarialCases.
type ProofMutation func(*Proof) error

// AdversarialCase describes one malformed proof sample that must be rejected by
// VerifyPrimeProof. If ExpectedError is nil, any verification error is accepted.
type AdversarialCase struct {
	Name          string
	Mutate        ProofMutation
	ExpectedError error
}

// AdversarialReport captures whether the baseline proof verifies and whether all
// adversarial mutations were rejected with the expected verifier errors.
type AdversarialReport struct {
	BaselineOK    bool                `json:"baselineOk"`
	BaselineError string              `json:"baselineError,omitempty"`
	Results       []AdversarialResult `json:"results"`
}

// Failed reports whether the baseline failed or any adversarial case was not
// rejected as expected.
func (r *AdversarialReport) Failed() bool {
	if r == nil || !r.BaselineOK {
		return true
	}
	for _, result := range r.Results {
		if !result.OK {
			return true
		}
	}
	return false
}

// AdversarialResult records one adversarial mutation outcome. OK means the
// mutated proof was rejected, and when ExpectedError was configured the rejection
// matched it via errors.Is.
type AdversarialResult struct {
	Name          string `json:"name"`
	OK            bool   `json:"ok"`
	ExpectedError string `json:"expectedError,omitempty"`
	Error         string `json:"error,omitempty"`
	Err           error  `json:"-"`
}

// ValidatePrimeProofAdversarialCases verifies a known-good baseline proof, then
// clones and mutates it for each adversarial case. The function treats a mutated
// proof that still verifies as a failed gate result.
func ValidatePrimeProofAdversarialCases(baseline *Proof, cases []AdversarialCase) (*AdversarialReport, error) {
	if len(cases) == 0 {
		return nil, ErrNoAdversarialCases
	}
	for i, validationCase := range cases {
		if validationCase.Mutate == nil {
			return nil, fmt.Errorf("%w: case %d %q", ErrNilAdversarialMutator, i, validationCase.name(i))
		}
	}

	report := &AdversarialReport{
		Results: make([]AdversarialResult, 0, len(cases)),
	}
	if err := VerifyPrimeProof(baseline); err != nil {
		report.BaselineError = err.Error()
		return report, nil
	}
	report.BaselineOK = true

	for i, validationCase := range cases {
		report.Results = append(report.Results, runAdversarialCase(baseline, validationCase, i))
	}
	return report, nil
}

func runAdversarialCase(baseline *Proof, validationCase AdversarialCase, index int) AdversarialResult {
	result := AdversarialResult{
		Name: validationCase.name(index),
	}
	if validationCase.ExpectedError != nil {
		result.ExpectedError = validationCase.ExpectedError.Error()
	}

	mutated := cloneProof(baseline)
	if err := validationCase.Mutate(mutated); err != nil {
		result.Err = err
		result.Error = err.Error()
		return result
	}

	err := VerifyPrimeProof(mutated)
	if err == nil {
		result.Err = fmt.Errorf("%w: %s", ErrAdversarialCaseUnexpectedPass, result.Name)
		result.Error = result.Err.Error()
		return result
	}
	result.Err = err
	result.Error = err.Error()
	if validationCase.ExpectedError != nil && !errors.Is(err, validationCase.ExpectedError) {
		result.OK = false
		result.Err = fmt.Errorf("%w: case %s expected %q got %q", ErrAdversarialCaseUnexpectedErr, result.Name, validationCase.ExpectedError.Error(), err.Error())
		result.Error = result.Err.Error()
		return result
	}
	result.OK = true
	return result
}

func (c AdversarialCase) name(index int) string {
	if c.Name != "" {
		return c.Name
	}
	return fmt.Sprintf("case-%d", index)
}

func cloneProof(proof *Proof) *Proof {
	if proof == nil {
		return nil
	}
	clone := &Proof{
		Anchor:  proof.Anchor,
		M:       proof.M,
		Headers: make([]*types.WorkObject, len(proof.Headers)),
	}
	for i, header := range proof.Headers {
		if header != nil {
			clone.Headers[i] = types.CopyWorkObject(header)
		}
	}
	return clone
}
