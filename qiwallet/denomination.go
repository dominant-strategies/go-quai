package qiwallet

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	mrand "math/rand"

	"github.com/dominant-strategies/go-quai/core/types"
)

// NewRand returns a math/rand generator seeded from crypto/rand. Wallets
// should use this (or their own CSPRNG-backed source) for decomposition
// shaping and output shuffling; a predictable seed makes shapes linkable.
func NewRand() *mrand.Rand {
	var seed [8]byte
	if _, err := crand.Read(seed[:]); err != nil {
		// Extremely unlikely; fall back to a fixed seed rather than failing.
		return mrand.New(mrand.NewSource(1))
	}
	return mrand.New(mrand.NewSource(int64(binary.LittleEndian.Uint64(seed[:]))))
}

// NotesValue returns the total value in qits of a multiset of denominations.
func NotesValue(notes []uint8) *big.Int {
	total := big.NewInt(0)
	for _, note := range notes {
		total.Add(total, types.Denominations[note])
	}
	return total
}

// denominationRatio returns how many notes of denomination d-1 one note of
// denomination d is worth. All adjacent ratios in the table are integral.
func denominationRatio(d uint8) uint64 {
	return new(big.Int).Div(types.Denominations[d], types.Denominations[d-1]).Uint64()
}

// DecomposeGreedy splits a positive value (in qits) into the minimal
// multiset of denominations, largest first. This is the canonical shape;
// wallets should prefer DecomposeRandom for outputs they put on chain.
func DecomposeGreedy(value *big.Int) ([]uint8, error) {
	if value == nil || value.Sign() <= 0 {
		return nil, errors.New("value must be positive")
	}
	remaining := new(big.Int).Set(value)
	notes := make([]uint8, 0, 8)
	for d := types.MaxDenomination; d >= 0; d-- {
		denomination := types.Denominations[uint8(d)]
		for remaining.Cmp(denomination) >= 0 {
			remaining.Sub(remaining, denomination)
			notes = append(notes, uint8(d))
		}
	}
	if remaining.Sign() != 0 {
		return nil, errors.New("value is not a whole number of qits")
	}
	return notes, nil
}

// SplitNotes randomly splits notes into smaller denominations, preserving
// total value, until maxNotes is reached or no split is chosen. Randomized
// shapes prevent observers from recognizing the canonical greedy pattern
// and separating change from payment. The result is shuffled.
func SplitNotes(notes []uint8, maxNotes int, rng *mrand.Rand) []uint8 {
	out := append([]uint8(nil), notes...)
	for attempts := 0; attempts < 4*maxNotes && len(out) < maxNotes; attempts++ {
		i := rng.Intn(len(out))
		d := out[i]
		if d == 0 {
			continue
		}
		ratio := int(denominationRatio(d))
		if len(out)-1+ratio > maxNotes {
			continue
		}
		if rng.Intn(2) == 0 {
			continue
		}
		out = append(out[:i], out[i+1:]...)
		for j := 0; j < ratio; j++ {
			out = append(out, d-1)
		}
	}
	rng.Shuffle(len(out), func(a, b int) { out[a], out[b] = out[b], out[a] })
	return out
}

// DecomposeRandom decomposes a value (in qits) into a randomized multiset of
// at most maxNotes denominations.
func DecomposeRandom(value *big.Int, maxNotes int, rng *mrand.Rand) ([]uint8, error) {
	notes, err := DecomposeGreedy(value)
	if err != nil {
		return nil, err
	}
	if len(notes) > maxNotes {
		return nil, fmt.Errorf("value requires %d notes, more than the maximum of %d", len(notes), maxNotes)
	}
	return SplitNotes(notes, maxNotes, rng), nil
}
