package nipopow

import (
	"errors"
	"fmt"

	"github.com/dominant-strategies/go-quai/common"
)

var ErrIncomparableProofs = errors.New("nipopow proofs are not comparable")

// ProofScore is the verified superchain score for a Prime NiPoPoW proof.
//
// SuperchainCounts[level] is the number of proof headers whose verified PoW rank
// is at least level. Competing proofs are compared from the highest populated
// level down to level zero; ties are left unresolved instead of using arbitrary
// tip-hash tie breakers.
type ProofScore struct {
	Anchor           common.Hash  `json:"anchor"`
	Tip              common.Hash  `json:"tip"`
	M                uint64       `json:"m"`
	TipNumber        uint64       `json:"tipNumber"`
	HeaderCount      uint64       `json:"headerCount"`
	MaxRank          uint64       `json:"maxRank"`
	SuperchainCounts []uint64     `json:"superchainCounts"`
	Work             []HeaderWork `json:"work"`
}

// ScorePrimeProof verifies a Prime NiPoPoW proof and derives its superchain
// counts from each header's verified PoW rank.
func ScorePrimeProof(proof *Proof, verifier PrimePoWVerifier) (*ProofScore, error) {
	work, err := VerifyPrimeProofWork(proof, verifier)
	if err != nil {
		return nil, err
	}

	maxRank := uint64(0)
	for _, headerWork := range work {
		if headerWork.Rank > maxRank {
			maxRank = headerWork.Rank
		}
	}

	superchainCounts := make([]uint64, maxRank+1)
	for _, headerWork := range work {
		for level := range superchainCounts {
			if uint64(level) <= headerWork.Rank {
				superchainCounts[level]++
			}
		}
	}

	tip := proof.Headers[len(proof.Headers)-1]
	return &ProofScore{
		Anchor:           proof.Anchor,
		Tip:              tip.Hash(),
		M:                proof.M,
		TipNumber:        tip.Number(common.PRIME_CTX).Uint64(),
		HeaderCount:      uint64(len(proof.Headers)),
		MaxRank:          maxRank,
		SuperchainCounts: superchainCounts,
		Work:             work,
	}, nil
}

// ComparePrimeProofs verifies and compares two Prime NiPoPoW proofs. It returns
// +1 when left is better, -1 when right is better, and 0 when their score vectors
// tie. Proofs are comparable only when they use the same anchor and suffix
// parameter M.
func ComparePrimeProofs(left, right *Proof, verifier PrimePoWVerifier) (int, *ProofScore, *ProofScore, error) {
	leftScore, err := ScorePrimeProof(left, verifier)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("left proof: %w", err)
	}
	rightScore, err := ScorePrimeProof(right, verifier)
	if err != nil {
		return 0, leftScore, nil, fmt.Errorf("right proof: %w", err)
	}
	if leftScore.Anchor != rightScore.Anchor || leftScore.M != rightScore.M {
		return 0, leftScore, rightScore, fmt.Errorf("%w: anchor/m mismatch", ErrIncomparableProofs)
	}
	return CompareProofScores(leftScore, rightScore), leftScore, rightScore, nil
}

// CompareProofScores compares verified proof score vectors. It returns +1 when
// left is better, -1 when right is better, and 0 for an unresolved tie.
func CompareProofScores(left, right *ProofScore) int {
	maxLen := len(left.SuperchainCounts)
	if len(right.SuperchainCounts) > maxLen {
		maxLen = len(right.SuperchainCounts)
	}
	for level := maxLen - 1; level >= 0; level-- {
		leftCount := countAtLevel(left.SuperchainCounts, level)
		rightCount := countAtLevel(right.SuperchainCounts, level)
		if leftCount > rightCount {
			return 1
		}
		if leftCount < rightCount {
			return -1
		}
	}
	return 0
}

func countAtLevel(counts []uint64, level int) uint64 {
	if level < 0 || level >= len(counts) {
		return 0
	}
	return counts[level]
}
