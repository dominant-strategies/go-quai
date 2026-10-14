package core

import (
	"math/big"
	"testing"
)

func TestRegularQiETXDenominationRejectsTruncationAndOutOfRange(t *testing.T) {
	for _, value := range []*big.Int{nil, big.NewInt(-1), big.NewInt(15), big.NewInt(270), new(big.Int).Lsh(big.NewInt(1), 64)} {
		if _, err := regularQiETXDenomination(value); err == nil {
			t.Fatalf("accepted invalid denomination %v", value)
		}
	}
	for _, value := range []int64{0, 14} {
		got, err := regularQiETXDenomination(big.NewInt(value))
		if err != nil || got != uint8(value) {
			t.Fatalf("valid denomination %d: got %d, %v", value, got, err)
		}
	}
}
