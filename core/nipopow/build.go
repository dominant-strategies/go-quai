package nipopow

import (
	"context"
	"errors"
	"fmt"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

const (
	DefaultMaxProofChainLength uint64 = 4096
	DefaultMaxProofHeaders     uint64 = 1024
	DefaultMaxProofM           uint64 = 1024
)

var (
	ErrNilProofSource       = errors.New("nil nipopow proof source")
	ErrInvalidProofEndpoint = errors.New("invalid nipopow proof endpoint")
	ErrProofHeaderNotFound  = errors.New("nipopow proof header not found")
	ErrAnchorNotFound       = errors.New("nipopow proof anchor is not an ancestor of tip")
	ErrHeaderHashMismatch   = errors.New("nipopow proof source returned mismatched header")
	ErrCycleDetected        = errors.New("nipopow proof walk detected a cycle")
	ErrProofTooLarge        = errors.New("nipopow proof exceeds configured limits")
)

// BuildLimits bounds read-only proof construction for public RPC callers.
type BuildLimits struct {
	MaxChainLength  uint64
	MaxProofHeaders uint64
	MaxM            uint64
}

var DefaultBuildLimits = BuildLimits{
	MaxChainLength:  DefaultMaxProofChainLength,
	MaxProofHeaders: DefaultMaxProofHeaders,
	MaxM:            DefaultMaxProofM,
}

func (limits BuildLimits) withDefaults() BuildLimits {
	if limits.MaxChainLength == 0 {
		limits.MaxChainLength = DefaultMaxProofChainLength
	}
	if limits.MaxProofHeaders == 0 {
		limits.MaxProofHeaders = DefaultMaxProofHeaders
	}
	if limits.MaxM == 0 {
		limits.MaxM = DefaultMaxProofM
	}
	return limits
}

// PrimeProofSource supplies hydrated Prime proof headers. Returned WorkObjects
// must include the InterlinkHashes committed to by their InterlinkRootHash.
type PrimeProofSource interface {
	ProofHeader(hash common.Hash) (*types.WorkObject, error)
}

// BuildPrimeProof builds a Prime-chain proof from anchor to tip using default
// public-RPC safety limits.
func BuildPrimeProof(source PrimeProofSource, anchor common.Hash, tip common.Hash, m uint64) (*Proof, error) {
	return BuildPrimeProofWithContext(context.Background(), source, anchor, tip, m, DefaultBuildLimits)
}

// BuildPrimeProofWithContext builds a Prime-chain proof from anchor to tip.
//
// The builder is read-only. It walks the canonical parent chain supplied by the
// source, keeps the last M headers as a linear suffix, and greedily compresses
// the prefix with committed interlink jumps when available. If no interlink jump
// is available it falls back to a linear proof, which remains valid though less
// compact. Context and limits bound public RPC use.
func BuildPrimeProofWithContext(ctx context.Context, source PrimeProofSource, anchor common.Hash, tip common.Hash, m uint64, limits BuildLimits) (*Proof, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	limits = limits.withDefaults()

	if source == nil {
		return nil, ErrNilProofSource
	}
	if anchor == (common.Hash{}) || tip == (common.Hash{}) || m == 0 {
		return nil, ErrInvalidProofEndpoint
	}
	if m > limits.MaxM {
		return nil, fmt.Errorf("%w: m %d exceeds max %d", ErrProofTooLarge, m, limits.MaxM)
	}

	chain, err := collectPrimeChain(ctx, source, anchor, tip, limits.MaxChainLength)
	if err != nil {
		return nil, err
	}
	if uint64(len(chain)) < m {
		return nil, ErrInsufficientSuffix
	}

	proof := &Proof{
		Anchor:  anchor,
		M:       m,
		Headers: compressPrimeChain(chain, int(m)),
	}
	if uint64(len(proof.Headers)) > limits.MaxProofHeaders {
		return nil, fmt.Errorf("%w: proof headers %d exceeds max %d", ErrProofTooLarge, len(proof.Headers), limits.MaxProofHeaders)
	}
	if err := VerifyPrimeProof(proof); err != nil {
		return nil, err
	}
	return proof, nil
}

func collectPrimeChain(ctx context.Context, source PrimeProofSource, anchor common.Hash, tip common.Hash, maxChainLength uint64) ([]*types.WorkObject, error) {
	visited := make(map[common.Hash]struct{})
	reverse := make([]*types.WorkObject, 0)

	for hash := tip; ; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if maxChainLength > 0 && uint64(len(reverse)) >= maxChainLength {
			return nil, fmt.Errorf("%w: chain length exceeds max %d", ErrProofTooLarge, maxChainLength)
		}
		if _, exists := visited[hash]; exists {
			return nil, fmt.Errorf("%w: %s", ErrCycleDetected, hash.Hex())
		}
		visited[hash] = struct{}{}

		header, err := source.ProofHeader(hash)
		if err != nil || header == nil {
			return nil, fmt.Errorf("%w: %s", ErrProofHeaderNotFound, hash.Hex())
		}
		if err := verifyUsableHeader(header); err != nil {
			return nil, err
		}
		if header.Hash() != hash {
			return nil, fmt.Errorf("%w: expected %s got %s", ErrHeaderHashMismatch, hash.Hex(), header.Hash().Hex())
		}
		reverse = append(reverse, header)

		if hash == anchor {
			break
		}
		if header.Number(common.PRIME_CTX).Sign() == 0 || header.ParentHash(common.PRIME_CTX) == (common.Hash{}) {
			return nil, fmt.Errorf("%w: %s", ErrAnchorNotFound, anchor.Hex())
		}
		hash = header.ParentHash(common.PRIME_CTX)
	}

	chain := make([]*types.WorkObject, len(reverse))
	for i := range reverse {
		chain[len(reverse)-1-i] = reverse[i]
	}
	return chain, nil
}

func compressPrimeChain(chain []*types.WorkObject, m int) []*types.WorkObject {
	if len(chain) <= m || m <= 0 {
		return append([]*types.WorkObject(nil), chain...)
	}

	suffixStart := len(chain) - m
	proof := make([]*types.WorkObject, 0, len(chain))
	proof = append(proof, chain[0])

	current := 0
	for current < suffixStart {
		farthest := current + 1
		for candidate := suffixStart; candidate > current+1; candidate-- {
			if interlinksContain(chain[candidate].InterlinkHashes(), chain[current].Hash()) {
				farthest = candidate
				break
			}
		}
		proof = append(proof, chain[farthest])
		current = farthest
	}

	for i := current + 1; i < len(chain); i++ {
		proof = append(proof, chain[i])
	}
	return proof
}
