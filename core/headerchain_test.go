package core

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/stretchr/testify/require"
)

func TestComputeKQuaiDiscount(t *testing.T) {

	// First value is the current block(5004000) exchange rate
	// Second value is the block 5000000 exchange rate
	// Third value is the expected kQuai discount value given the starting
	// kQuaiDiscount of 50
	testcases := [][3]int64{{10000, 200000, 74}, {10000, 9000, 52}, {10000, 10000, 49}}
	startingKQuaiDiscount := big.NewInt(50)

	for _, test := range testcases {
		block := types.EmptyWorkObject(common.PRIME_CTX)
		blockNumber := big.NewInt(5004000)
		block.Header().SetKQuaiDiscount(startingKQuaiDiscount)
		block.Header().SetNumber(blockNumber, common.PRIME_CTX)

		hc := NewTestHeaderChain()
		// Create a new header db
		hc.headerDb = rawdb.NewMemoryDatabase(log.Global)
		hc.bc = NewTestBodyDb(hc.headerDb)

		blockOne := types.EmptyWorkObject(common.PRIME_CTX)
		blockOne.Header().SetNumber(big.NewInt(5000000), common.PRIME_CTX)
		blockOne.Header().SetExchangeRate(big.NewInt(test[0]))

		rawdb.WriteTermini(hc.headerDb, blockOne.Hash(), types.EmptyTermini())
		rawdb.WriteCanonicalHash(hc.headerDb, blockOne.Hash(), 5000000)
		rawdb.WriteWorkObject(hc.headerDb, blockOne.Hash(), blockOne, types.BlockObject, common.PRIME_CTX)

		computedKQuaiDiscount := hc.ComputeKQuaiDiscount(block, big.NewInt(test[1]))

		require.Equal(t, test[2], computedKQuaiDiscount.Int64())
	}
}

func TestApplyCubicDiscount(t *testing.T) {

	testCases := [][3]int64{
		{100, 10000, 0}, // If the value is more than 10 times the average, the realized amount should be zero
		{100, 90, 89},   // If the value is less than average the realized amount is 99% of the value
		{100, 100, 99},  // If the value is exactly the average, there is 1% slip
		{100, 150, 149},
		{100, 1000, 0},
		{100000000, 100000000, 99800000},
		{100, 1001, 0},
		{100, 999, 1},
		{0, 0, 0},
		{100000, 100011, 99810},
	}

	for _, test := range testCases {
		value := big.NewInt(test[1])
		mean := big.NewInt(test[0])
		discountedValue := misc.ApplyCubicDiscount(value, mean)
		discountedValueInt, _ := discountedValue.Int(nil)
		require.Equal(t, test[2], discountedValueInt.Int64())
	}

}
