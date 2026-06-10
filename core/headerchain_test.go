package core

import (
	"math/big"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
	"modernc.org/mathutil"
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

func TestCalculateKawpowShareDiffWithZeroKawpowDifficulty(t *testing.T) {
	header := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
	header.SetPrimeTerminusNumber(big.NewInt(int64(params.KawPowForkBlock + 1)))
	header.SetDifficulty(big.NewInt(5000))
	header.SetKawpowDifficulty(common.Big0)
	header.SetShaDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), common.Big0, common.Big0))
	header.SetScryptDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), common.Big0, common.Big0))
	header.SetShaShareTarget(new(big.Int).Mul(big.NewInt(100), common.Big2e32))
	header.SetScryptShareTarget(new(big.Int).Mul(big.NewInt(100), common.Big2e32))

	diff := CalculateKawpowShareDiff(header)
	require.Equal(t, header.Difficulty(), diff)
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
		{
			name:               "At halfway point to the update",
			primeTerminusNum:   params.InclusionDepthChangeBlock + params.InclusionDepthUpdatePeriod/2,
			difficulty:         big.NewInt(100),
			kawpowDifficulty:   big.NewInt(0), // Zero kawpow diff -> max difference -> increase
			currentShareTarget: maxShaShares,  // Start at max
			expectedTarget:     new(big.Int).Add(targetShaShares, new(big.Int).Div(new(big.Int).Sub(maxShaShares, targetShaShares), big.NewInt(2))),
		},
		{
			name:               "After the update period",
			primeTerminusNum:   params.InclusionDepthChangeBlock + params.InclusionDepthUpdatePeriod + 1,
			difficulty:         big.NewInt(100),
			kawpowDifficulty:   big.NewInt(0), // Zero kawpow diff -> max difference -> increase
			currentShareTarget: maxShaShares,  // Start at max
			expectedTarget:     maxShaShares,
		},
		{
			name:               "Long time after the update period",
			primeTerminusNum:   params.InclusionDepthChangeBlock + params.InclusionDepthUpdatePeriod + 1000000,
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

func TestCalculatePowDiffAndCountConversionStabilityForkParameters(t *testing.T) {
	hc := NewTestHeaderChain()

	testCases := []struct {
		name                    string
		primeTerminusNumber     uint64
		workShareEmaBlocks      *big.Int
		powDiffAdjustmentFactor *big.Int
	}{
		{
			name:                    "before conversion stability fork uses legacy parameters",
			primeTerminusNumber:     params.ConversionStabilityForkBlock - 1,
			workShareEmaBlocks:      params.WorkShareEmaBlocks,
			powDiffAdjustmentFactor: params.PowDiffAdjustmentFactor,
		},
		{
			name:                    "at conversion stability fork uses new parameters",
			primeTerminusNumber:     params.ConversionStabilityForkBlock,
			workShareEmaBlocks:      params.NewWorkShareEmaBlocks,
			powDiffAdjustmentFactor: params.NewPowDiffAdjustmentFactor,
		},
		{
			name:                    "after conversion stability fork uses new parameters",
			primeTerminusNumber:     params.ConversionStabilityForkBlock + 1,
			workShareEmaBlocks:      params.NewWorkShareEmaBlocks,
			powDiffAdjustmentFactor: params.NewPowDiffAdjustmentFactor,
		},
	}

	algoCases := []struct {
		name           string
		powID          types.PowID
		shares         *types.PowShareDiffAndCount
		shareTarget    *big.Int
		observedShares int
		observedUncled int
		diffLowerBound *big.Int
	}{
		{
			name:           "sha",
			powID:          types.SHA_BTC,
			shares:         types.NewPowShareDiffAndCount(big.NewInt(1_000_000_000_000_000_000), sharesAsFixedPoint(8), sharesAsFixedPoint(2)),
			shareTarget:    sharesAsFixedPoint(4),
			observedShares: 2,
			observedUncled: 1,
			diffLowerBound: params.ShaDiffLowerBound,
		},
		{
			name:           "scrypt",
			powID:          types.Scrypt,
			shares:         types.NewPowShareDiffAndCount(big.NewInt(1_000_000_000_000_000), sharesAsFixedPoint(7), sharesAsFixedPoint(3)),
			shareTarget:    sharesAsFixedPoint(5),
			observedShares: 3,
			observedUncled: 1,
			diffLowerBound: params.ScryptDiffLowerBound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			header := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
			header.SetPrimeTerminusNumber(new(big.Int).SetUint64(tc.primeTerminusNumber))

			for _, ac := range algoCases {
				t.Run(ac.name, func(t *testing.T) {
					parent := conversionStabilityPowDiffParent()

					newDiff, newAverageShares, newUncledShares := hc.CalculatePowDiffAndCount(parent, header, ac.powID)
					expectedDiff, expectedAverageShares, expectedUncledShares := expectedPowDiffAndCount(
						ac.shares,
						ac.shareTarget,
						ac.observedShares,
						ac.observedUncled,
						tc.workShareEmaBlocks,
						tc.powDiffAdjustmentFactor,
						ac.diffLowerBound,
					)

					require.Equal(t, expectedDiff, newDiff)
					require.Equal(t, expectedAverageShares, newAverageShares)
					require.Equal(t, expectedUncledShares, newUncledShares)
				})
			}
		})
	}
}

func TestCalculatePowDiffAndCountConversionForkDoesNotUseParentPrimeTerminus(t *testing.T) {
	hc := NewTestHeaderChain()
	parent := conversionStabilityPowDiffParent()
	parent.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(params.ConversionStabilityForkBlock - 1))

	header := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
	header.SetPrimeTerminusNumber(new(big.Int).SetUint64(params.ConversionStabilityForkBlock))

	newDiff, newAverageShares, newUncledShares := hc.CalculatePowDiffAndCount(parent, header, types.SHA_BTC)
	expectedDiff, expectedAverageShares, expectedUncledShares := expectedPowDiffAndCount(
		parent.ShaDiffAndCount(),
		parent.ShaShareTarget(),
		2,
		1,
		params.NewWorkShareEmaBlocks,
		params.NewPowDiffAdjustmentFactor,
		params.ShaDiffLowerBound,
	)

	require.Equal(t, expectedDiff, newDiff)
	require.Equal(t, expectedAverageShares, newAverageShares)
	require.Equal(t, expectedUncledShares, newUncledShares)
}

func TestCalculatePowDiffAndCountConversionForkLowerBound(t *testing.T) {
	hc := NewTestHeaderChain()
	parent := conversionStabilityPowDiffParent()
	parent.Body().SetUncles(nil)
	parent.WorkObjectHeader().SetShaDiffAndCount(types.NewPowShareDiffAndCount(
		new(big.Int).Add(params.ShaDiffLowerBound, big.NewInt(1)),
		sharesAsFixedPoint(8),
		sharesAsFixedPoint(2),
	))

	header := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
	header.SetPrimeTerminusNumber(new(big.Int).SetUint64(params.ConversionStabilityForkBlock))

	newDiff, newAverageShares, newUncledShares := hc.CalculatePowDiffAndCount(parent, header, types.SHA_BTC)
	expectedDiff, expectedAverageShares, expectedUncledShares := expectedPowDiffAndCount(
		parent.ShaDiffAndCount(),
		parent.ShaShareTarget(),
		0,
		0,
		params.NewWorkShareEmaBlocks,
		params.NewPowDiffAdjustmentFactor,
		params.ShaDiffLowerBound,
	)

	require.Equal(t, params.ShaDiffLowerBound, newDiff)
	require.Equal(t, expectedDiff, newDiff)
	require.Equal(t, expectedAverageShares, newAverageShares)
	require.Equal(t, expectedUncledShares, newUncledShares)
}

func TestCalculateTimeDiscountedShareReward(t *testing.T) {
	hc := NewTestHeaderChain()

	// Constants from params:
	// NoPenaltyTimeThreshold = 3 (seconds)
	// ShareLivenessTime = 18 (seconds for Kawpow/Scrypt)
	// NewShareLivenessTimeForSha = 30 (seconds for SHA_BTC/SHA_BCH)
	// UnlivelySharePenalty = 70 (70% reward at max penalty)
	// ShareRewardPenaltyDivisor = 100

	testCases := []struct {
		name           string
		powID          types.PowID
		shareTimestamp uint32 // timestamp in the share's AuxPow header
		signatureTime  uint32 // signature time passed to the function
		shareReward    int64
		expectedReward int64
	}{
		{
			name:           "No penalty - within 3 seconds (Kawpow)",
			powID:          types.Kawpow,
			shareTimestamp: 103,
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 1000, // Full reward when timeSinceSignature < NoPenaltyTimeThreshold
		},
		{
			name:           "No penalty - exactly at 3 seconds boundary (Kawpow)",
			powID:          types.Kawpow,
			shareTimestamp: 103,
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 1000, // timeSinceSignature=3, clamped to 3, distance=15, full reward
		},
		{
			name:           "Maximum penalty - at liveness time (Kawpow)",
			powID:          types.Kawpow,
			shareTimestamp: 118, // 18 seconds after signature
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 700, // 70% of reward at max penalty
		},
		{
			name:           "Maximum penalty - beyond liveness time (Kawpow)",
			powID:          types.Kawpow,
			shareTimestamp: 200, // well beyond 18 seconds
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 700, // Clamped to livenessTime, so 70%
		},
		{
			name:           "Midpoint penalty (Kawpow)",
			powID:          types.Kawpow,
			shareTimestamp: 110, // 10 seconds after signature (midpoint of 3-18 range)
			signatureTime:  100,
			shareReward:    1000,
			// timeDeltaRange = 18 - 3 = 15
			// distance = 18 - 10 = 8
			// numerator = 70 * 15 + (100-70) * 8 = 1050 + 240 = 1290
			// denominator = 100 * 15 = 1500
			// reward = 1000 * 1290 / 1500 = 860
			expectedReward: 860,
		},
		{
			name:           "No penalty - within 3 seconds (SHA_BTC)",
			powID:          types.SHA_BTC,
			shareTimestamp: 102,
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 1000, // Full reward
		},
		{
			name:           "Maximum penalty - at liveness time (SHA_BTC)",
			powID:          types.SHA_BTC,
			shareTimestamp: 130, // 30 seconds after signature (SHA liveness time)
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 700, // 70% of reward
		},
		{
			name:           "Midpoint penalty (SHA_BTC)",
			powID:          types.SHA_BTC,
			shareTimestamp: 116, // 16 seconds after signature (roughly midpoint of 3-30)
			signatureTime:  100,
			shareReward:    1000,
			// timeDeltaRange = 30 - 3 = 27
			// distance = 30 - 16 = 14
			// numerator = 70 * 27 + 30 * 14 = 1890 + 420 = 2310
			// denominator = 100 * 27 = 2700
			// reward = 1000 * 2310 / 2700 = 855
			expectedReward: 855,
		},
		{
			name:           "SHA_BCH uses SHA liveness time",
			powID:          types.SHA_BCH,
			shareTimestamp: 130, // 30 seconds after signature
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 700, // Max penalty at 30s for SHA
		},
		{
			name:           "Scrypt uses default liveness time",
			powID:          types.Scrypt,
			shareTimestamp: 118, // 18 seconds after signature
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 700, // Max penalty at 18s for Scrypt
		},
		{
			name:           "Maximum penalty - beyond liveness time (SHA_BTC)",
			powID:          types.SHA_BTC,
			shareTimestamp: 200, // well beyond 30 seconds
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 700, // Clamped to livenessTime, so 70%
		},
		{
			name:           "Maximum penalty - beyond liveness time (SHA_BCH)",
			powID:          types.SHA_BCH,
			shareTimestamp: 200, // well beyond 30 seconds
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 700, // Clamped to livenessTime, so 70%
		},
		{
			name:           "Maximum penalty - beyond liveness time (Scrypt)",
			powID:          types.Scrypt,
			shareTimestamp: 200, // well beyond 18 seconds
			signatureTime:  100,
			shareReward:    1000,
			expectedReward: 700, // Clamped to livenessTime, so 70%
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a share with the specified AuxPow
			share := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()

			// Create AuxPow with the appropriate PowID and timestamp
			auxPow := createTestAuxPow(tc.powID, tc.shareTimestamp)
			share.SetAuxPow(auxPow)

			shareReward := big.NewInt(tc.shareReward)
			result := hc.CalculateTimeDiscountedShareReward(share, shareReward, tc.signatureTime)

			require.Equal(t, tc.expectedReward, result.Int64(),
				"Expected reward %d but got %d for test case: %s",
				tc.expectedReward, result.Int64(), tc.name)
		})
	}
}

func conversionStabilityPowDiffParent() *types.WorkObject {
	parent := types.EmptyWorkObject(common.ZONE_CTX)
	parent.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(params.ConversionStabilityForkBlock - 1))
	parent.WorkObjectHeader().SetShaDiffAndCount(types.NewPowShareDiffAndCount(
		big.NewInt(1_000_000_000_000_000_000),
		sharesAsFixedPoint(8),
		sharesAsFixedPoint(2),
	))
	parent.WorkObjectHeader().SetScryptDiffAndCount(types.NewPowShareDiffAndCount(
		big.NewInt(1_000_000_000_000_000),
		sharesAsFixedPoint(7),
		sharesAsFixedPoint(3),
	))
	parent.WorkObjectHeader().SetShaShareTarget(sharesAsFixedPoint(4))
	parent.WorkObjectHeader().SetScryptShareTarget(sharesAsFixedPoint(5))
	parent.Body().SetUncles([]*types.WorkObjectHeader{
		conversionStabilityWorkShare(types.SHA_BTC, false),
		conversionStabilityWorkShare(types.SHA_BCH, true),
		conversionStabilityWorkShare(types.Scrypt, false),
		conversionStabilityWorkShare(types.Scrypt, false),
		conversionStabilityWorkShare(types.Scrypt, true),
		conversionStabilityWorkShare(types.Kawpow, false),
	})
	return parent
}

func conversionStabilityWorkShare(powID types.PowID, uncled bool) *types.WorkObjectHeader {
	share := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
	share.SetAuxPow(createTestAuxPow(powID, 1))
	if uncled {
		share.SetPrimaryCoinbase(common.Address{})
	} else {
		share.SetPrimaryCoinbase(common.ZeroAddress(common.Location{0, 0}))
	}
	return share
}

func sharesAsFixedPoint(shares int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(shares), common.Big2e32)
}

func expectedPowDiffAndCount(shares *types.PowShareDiffAndCount, shareTarget *big.Int, observedShares, observedUncled int, workShareEmaBlocks, powDiffAdjustmentFactor, diffLowerBound *big.Int) (*big.Int, *big.Int, *big.Int) {
	numShares := sharesAsFixedPoint(int64(observedShares))
	uncledShares := sharesAsFixedPoint(int64(observedUncled))

	error := new(big.Int).Sub(numShares, shareTarget)
	newDiff := new(big.Int).Mul(error, shares.Difficulty())
	k, _ := mathutil.BinaryLog(new(big.Int).Set(shares.Difficulty()), common.MantBits)
	newDiff = new(big.Int).Mul(newDiff, big.NewInt(int64(k)))
	newDiff = new(big.Int).Div(newDiff, powDiffAdjustmentFactor)
	newDiff = new(big.Int).Div(newDiff, common.Big2e32)
	newDiff = new(big.Int).Add(shares.Difficulty(), newDiff)
	if newDiff.Cmp(diffLowerBound) < 0 {
		newDiff = new(big.Int).Set(diffLowerBound)
	}

	emaBlocksMinusOne := new(big.Int).Sub(workShareEmaBlocks, common.Big1)
	newAverageShares := new(big.Int).Mul(shares.Count(), emaBlocksMinusOne)
	newAverageShares = new(big.Int).Add(newAverageShares, numShares)
	newAverageShares = new(big.Int).Div(newAverageShares, workShareEmaBlocks)

	newUncledShares := new(big.Int).Mul(shares.Uncled(), emaBlocksMinusOne)
	newUncledShares = new(big.Int).Add(newUncledShares, uncledShares)
	newUncledShares = new(big.Int).Div(newUncledShares, workShareEmaBlocks)

	return newDiff, newAverageShares, newUncledShares
}

// createTestAuxPow creates an AuxPow with the specified PowID and timestamp for testing
func createTestAuxPow(powID types.PowID, timestamp uint32) *types.AuxPow {
	auxPow := &types.AuxPow{}
	auxPow.SetPowID(powID)
	auxPow.SetSignature([]byte{})
	auxPow.SetMerkleBranch([][]byte{})

	coinbaseOut := []byte{0x76, 0xa9, 0x14, 0x89, 0xab, 0xcd, 0xef, 0x88, 0xac}

	switch powID {
	case types.Kawpow:
		header := &types.RavencoinBlockHeader{
			Version:        10,
			HashPrevBlock:  types.EmptyRootHash,
			HashMerkleRoot: types.EmptyRootHash,
			Time:           timestamp,
			Bits:           0x1d00ffff,
			Nonce64:        367899,
			Height:         298899,
			MixHash:        types.EmptyRootHash,
		}
		auxPow.SetHeader(types.NewAuxPowHeader(header))
		auxPow.SetTransaction(types.NewAuxPowCoinbaseTx(powID, 100, coinbaseOut, types.EmptyRootHash, 0))
	case types.SHA_BTC:
		header := types.NewBitcoinBlockHeader(10, types.EmptyRootHash, types.EmptyRootHash, 0, 0x1d00ffff, 0)
		header.BlockHeader.Timestamp = time.Unix(int64(timestamp), 0)
		auxPow.SetHeader(types.NewAuxPowHeader(header))
		auxPow.SetTransaction(types.NewAuxPowCoinbaseTx(powID, 100, coinbaseOut, types.EmptyRootHash, 0))
	case types.SHA_BCH:
		header := types.NewBitcoinCashBlockHeader(10, types.EmptyRootHash, types.EmptyRootHash, 0, 0x1d00ffff, 0)
		header.BlockHeader.Timestamp = time.Unix(int64(timestamp), 0)
		auxPow.SetHeader(types.NewAuxPowHeader(header))
		auxPow.SetTransaction(types.NewAuxPowCoinbaseTx(powID, 100, coinbaseOut, types.EmptyRootHash, 0))
	case types.Scrypt:
		header := types.NewLitecoinBlockHeader(10, types.EmptyRootHash, types.EmptyRootHash, 0, 0x1d00ffff, 0)
		header.BlockHeader.Timestamp = time.Unix(int64(timestamp), 0)
		auxPow.SetHeader(types.NewAuxPowHeader(header))
		auxPow.SetTransaction(types.NewAuxPowCoinbaseTx(powID, 100, coinbaseOut, types.EmptyRootHash, 0))
	}

	return auxPow
}
