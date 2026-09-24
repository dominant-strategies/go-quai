package consensus

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

func TestCalcWorkShareThresholdRejectsInvalidDifficulty(t *testing.T) {
	for _, difficulty := range []int64{0, -1} {
		header := new(types.WorkObjectHeader)
		header.SetDifficulty(big.NewInt(difficulty))
		if _, err := CalcWorkShareThreshold(header, 1); err != ErrInvalidDifficulty {
			t.Fatalf("difficulty %d: got %v, want %v", difficulty, err, ErrInvalidDifficulty)
		}
	}
	if _, err := CalcWorkShareThreshold(nil, 1); err != ErrInvalidDifficulty {
		t.Fatalf("nil header: got %v", err)
	}

	header := new(types.WorkObjectHeader)
	header.SetDifficulty(big.NewInt(2))
	target, err := CalcWorkShareThreshold(header, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := new(big.Int).Set(common.Big2e256); target.Cmp(want) != 0 {
		t.Fatalf("valid workshare target: got %s, want %s", target, want)
	}
}
