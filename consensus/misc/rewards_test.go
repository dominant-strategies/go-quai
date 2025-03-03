package misc

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/stretchr/testify/require"
)

func TestCalculateKQuai(t *testing.T) {

	// Term1: ParentExchangeRate
	// Term2: MinerDifficulty
	// Term3: Best Difficulty

	// Expectation is that if the best difficulty is higher, the kquai adjust higher
	// and if its lower, it adjust lower
	// Adjustment upwards doesnt seem to have a limit, but adjustment lower does
	testCases := [][4]int64{
		{1000, 10000000000000000, 5000, 999},
		{1000, 10000000000000000, 110000000000000000, 1009},
		{1000, 10000000000000000, 5000000000000000, 999},
		{10000000, 10000000000000000, 20000000000000000, 10009630},
		{1000, 10000000000000000, 1000000000000000000, 1087},
		{100000000, 100000, 1000000000000000000, 277777777877677772},
		{10000000000000, 10000000000000000, 10, 9990000000000},
	}

	for _, test := range testCases {
		newBeta0 := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(test[2]), common.Big2e64), common.LogBig(big.NewInt(test[2])))
		// computation
		kQuai := CalculateKQuai(big.NewInt(test[0]), big.NewInt(test[1]), newBeta0)
		require.Equal(t, test[3], kQuai.Int64())
	}

}
