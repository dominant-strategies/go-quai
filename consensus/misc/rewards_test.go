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
		{1000, 10000000000000000, 5000, 994},
		{1000, 10000000000000000, 110000000000000000, 1046},
		{1000, 10000000000000000, 5000000000000000, 992},
		{10000000, 10000000000000000, 20000000000000000, 10048153},
		{1000, 10000000000000000, 1000000000000000000, 1439},
		{100000000, 100000, 1000000000000000000, 1388888888988388864},
		{10000000000000, 10000000000000000, 10, 9949999999999},
	}

	for _, test := range testCases {
		newBeta0 := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(test[2]), common.Big2e64), common.LogBig(big.NewInt(test[2])))
		// Only if the best difficulty greater than the miner difficulty, multiply the beta by -1
		if big.NewInt(test[2]).Cmp(big.NewInt(test[1])) > 0 {
			newBeta0 = new(big.Int).Mul(newBeta0, big.NewInt(-1))
		}
		// computation
		kQuai := CalculateKQuai(big.NewInt(test[0]), big.NewInt(test[1]), newBeta0)
		require.Equal(t, test[3], kQuai.Int64())
	}

}
