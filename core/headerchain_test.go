package core

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
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

func TestCalculateKawpowShareDiff(t *testing.T) {
	// Setup parameters
	// params.ExpectedWorksharesPerBlock = 8
	// common.Big2e32 = 4294967296

	testCases := []struct {
		name             string
		difficulty       int64
		kawpowDifficulty int64
		shaCount         int64 // in units of 2^32
		scryptCount      int64 // in units of 2^32
		expectedDiff     int64
	}{
		{
			name:             "No discount, no other shares",
			difficulty:       5000,
			kawpowDifficulty: 10000,
			shaCount:         0,
			scryptCount:      0,
			expectedDiff:     555, // 5000 * 2^32 / (9 * 2^32) = 5000 / 9 = 555
		},
		{
			name:             "Linear discount (80%), no other shares",
			difficulty:       8000,
			kawpowDifficulty: 10000,
			shaCount:         0,
			scryptCount:      0,
			expectedDiff:     1333, // 8000 * 2^32 / (6 * 2^32) = 8000 / 6 = 1333
		},
		{
			name:             "Max discount (95%), no other shares",
			difficulty:       9500,
			kawpowDifficulty: 10000,
			shaCount:         0,
			scryptCount:      0,
			expectedDiff:     9500, // 9500 * 2^32 / (1 * 2^32) = 9500
		},
		{
			name:             "No discount, with other shares",
			difficulty:       5000,
			kawpowDifficulty: 10000,
			shaCount:         2,
			scryptCount:      2,
			expectedDiff:     1000, // 5000 * 2^32 / (5 * 2^32) = 1000
		},
		{
			name:             "linear discount at 90%, only kawpow blocks",
			difficulty:       9000,
			kawpowDifficulty: 10000,
			shaCount:         1,
			scryptCount:      1,
			expectedDiff:     9000,
		},
		{
			name:             "linear discount greater than 90%, only kawpow blocks",
			difficulty:       9500,
			kawpowDifficulty: 10000,
			shaCount:         1,
			scryptCount:      1,
			expectedDiff:     9500,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			header := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
			header.SetPrimeTerminusNumber(big.NewInt(int64(params.KawPowForkBlock + 1)))
			header.SetDifficulty(big.NewInt(tc.difficulty))
			header.SetKawpowDifficulty(big.NewInt(tc.kawpowDifficulty))

			shaCount := new(big.Int).Mul(big.NewInt(tc.shaCount), common.Big2e32)
			scryptCount := new(big.Int).Mul(big.NewInt(tc.scryptCount), common.Big2e32)

			// Set counts and targets (targets high enough so counts are used)
			header.SetShaDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), shaCount, big.NewInt(0)))
			header.SetScryptDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), scryptCount, big.NewInt(0)))
			header.SetShaShareTarget(new(big.Int).Mul(big.NewInt(100), common.Big2e32))
			header.SetScryptShareTarget(new(big.Int).Mul(big.NewInt(100), common.Big2e32))

			diff := CalculateKawpowShareDiff(header)
			require.Equal(t, tc.expectedDiff, diff.Int64())
		})
	}
}

func TestCalculateShareTarget(t *testing.T) {
	hc := NewTestHeaderChain()

	targetShaShares := params.TargetShaShares
	maxShaShares := params.MaxShaShares
	// params.BlocksPerDay is 17280

	testCases := []struct {
		name               string
		primeTerminusNum   uint64
		difficulty         *big.Int
		kawpowDifficulty   *big.Int
		currentShareTarget *big.Int
		expectedTarget     *big.Int
	}{
		{
			name:               "Transition Block",
			primeTerminusNum:   params.KawPowForkBlock,
			difficulty:         big.NewInt(1000),
			kawpowDifficulty:   big.NewInt(1000),
			currentShareTarget: big.NewInt(1000),
			expectedTarget:     targetShaShares,
		},
		{
			name:               "Normal Increase",
			primeTerminusNum:   params.KawPowForkBlock + 1,
			difficulty:         big.NewInt(1000000),
			kawpowDifficulty:   big.NewInt(100000),                                     // Small kawpow diff -> large difference -> increase
			currentShareTarget: new(big.Int).Add(targetShaShares, big.NewInt(1000000)), // Start somewhere in middle
			// Expected calculation:
			// maxSubsidy = 100000 * 3 / 4 = 75000
			// diff = 1000000 - 75000 = 925000
			// delta = (925000 * 12885901888) / 1000000 / 17280 = 689783
			// expected = 12885901888 + 689783 = 12886591671
			expectedTarget: big.NewInt(12886591671),
		},
		{
			name:               "Min Bound",
			primeTerminusNum:   params.KawPowForkBlock + 1,
			difficulty:         big.NewInt(100),
			kawpowDifficulty:   big.NewInt(200), // Large kawpow diff -> negative difference -> decrease
			currentShareTarget: targetShaShares, // Start at min
			expectedTarget:     targetShaShares,
		},
		{
			name:               "Max Bound",
			primeTerminusNum:   params.KawPowForkBlock + 1,
			difficulty:         big.NewInt(100),
			kawpowDifficulty:   big.NewInt(0), // Zero kawpow diff -> max difference -> increase
			currentShareTarget: maxShaShares,  // Start at max
			expectedTarget:     maxShaShares,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			header := types.EmptyWorkObject(common.ZONE_CTX)
			header.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(tc.primeTerminusNum))

			parent := types.EmptyWorkObject(common.ZONE_CTX)
			parent.WorkObjectHeader().SetDifficulty(tc.difficulty)
			parent.WorkObjectHeader().SetKawpowDifficulty(tc.kawpowDifficulty)
			parent.WorkObjectHeader().SetShaShareTarget(tc.currentShareTarget)

			result := hc.CalculateShareTarget(parent, header)

			require.Equal(t, tc.expectedTarget, result)
		})
	}
}
