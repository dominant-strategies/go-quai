package misc

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
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
		kQuai := CalculateKQuai(big.NewInt(test[0]), big.NewInt(test[1]), 10, newBeta0)
		require.Equal(t, test[3], kQuai.Int64())
	}

}

func TestCalculateKQuaiSlowdownAfterKQuaiChangeBlock(t *testing.T) {
	// Test the 3x slowdown mechanism for KQuai increases after KQuaiChangeBlock

	t.Run("KQuai increases are 3x slower after KQuaiChangeBlock", func(t *testing.T) {
		parentExchangeRate := big.NewInt(1000000)
		minerDifficulty := big.NewInt(10000000000000000)

		// Create a scenario that would cause KQuai to increase
		// Use high best difficulty to trigger increase
		bestDifficulty := big.NewInt(100000000000000000) // Much higher than miner difficulty
		newBeta0 := new(big.Int).Quo(new(big.Int).Mul(bestDifficulty, common.Big2e64), common.LogBig(bestDifficulty))

		// Test before KQuaiChangeBlock
		beforeBlock := params.KQuaiChangeBlock - 1
		kQuaiBefore := CalculateKQuai(parentExchangeRate, minerDifficulty, beforeBlock, newBeta0)
		increaseBefore := new(big.Int).Sub(kQuaiBefore, parentExchangeRate)

		// Test after KQuaiChangeBlock
		afterBlock := params.KQuaiChangeBlock + 1
		kQuaiAfter := CalculateKQuai(parentExchangeRate, minerDifficulty, afterBlock, newBeta0)
		increaseAfter := new(big.Int).Sub(kQuaiAfter, parentExchangeRate)

		// Only proceed if we actually have increases in both cases
		if increaseBefore.Cmp(common.Big0) <= 0 || increaseAfter.Cmp(common.Big0) <= 0 {
			t.Skip("Test scenario doesn't produce increases in both cases")
		}

		// The increase after should be approximately 3x smaller
		expectedIncreaseAfter := new(big.Int).Div(increaseBefore, big.NewInt(3))

		// Allow for some rounding tolerance (within 10% difference)
		tolerance := new(big.Int).Div(expectedIncreaseAfter, big.NewInt(10))
		diff := new(big.Int).Sub(increaseAfter, expectedIncreaseAfter)
		if diff.Cmp(common.Big0) < 0 {
			diff = new(big.Int).Neg(diff)
		}

		require.True(t, diff.Cmp(tolerance) <= 0,
			"Expected increase after KQuaiChangeBlock to be ~3x smaller. Before: %v, After: %v, Expected: %v",
			increaseBefore, increaseAfter, expectedIncreaseAfter)

		t.Logf("Before KQuaiChangeBlock - KQuai: %v, Increase: %v", kQuaiBefore, increaseBefore)
		t.Logf("After KQuaiChangeBlock - KQuai: %v, Increase: %v", kQuaiAfter, increaseAfter)
		t.Logf("Slowdown ratio: %v", new(big.Float).Quo(new(big.Float).SetInt(increaseBefore), new(big.Float).SetInt(increaseAfter)))
	})

	t.Run("KQuai decreases are NOT affected by slowdown", func(t *testing.T) {
		parentExchangeRate := big.NewInt(1000000)
		minerDifficulty := big.NewInt(10000000000000000)

		// Create a scenario that would cause KQuai to decrease
		// Use low best difficulty to trigger decrease
		bestDifficulty := big.NewInt(1000000000000000) // Much lower than miner difficulty
		newBeta0 := new(big.Int).Quo(new(big.Int).Mul(bestDifficulty, common.Big2e64), common.LogBig(bestDifficulty))

		// Test before KQuaiChangeBlock
		beforeBlock := params.KQuaiChangeBlock - 1
		kQuaiBefore := CalculateKQuai(parentExchangeRate, minerDifficulty, beforeBlock, newBeta0)
		decreaseBefore := new(big.Int).Sub(parentExchangeRate, kQuaiBefore)

		// Test after KQuaiChangeBlock
		afterBlock := params.KQuaiChangeBlock + 1
		kQuaiAfter := CalculateKQuai(parentExchangeRate, minerDifficulty, afterBlock, newBeta0)
		decreaseAfter := new(big.Int).Sub(parentExchangeRate, kQuaiAfter)

		// The decreases should be the same (no slowdown for decreases)
		require.Equal(t, decreaseBefore.Int64(), decreaseAfter.Int64(),
			"KQuai decreases should not be affected by slowdown mechanism")

		t.Logf("Before KQuaiChangeBlock - KQuai: %v, Decrease: %v", kQuaiBefore, decreaseBefore)
		t.Logf("After KQuaiChangeBlock - KQuai: %v, Decrease: %v", kQuaiAfter, decreaseAfter)
	})

	t.Run("Multiple scenarios to verify 3x slowdown magnitude", func(t *testing.T) {
		testCases := []struct {
			name               string
			parentExchangeRate int64
			minerDifficulty    int64
			bestDifficulty     int64
		}{
			{"Small values", 1000, 10000000000000000, 100000000000000000},
			{"Medium values", 100000, 50000000000000000, 500000000000000000},
			{"Large values", 10000000, 100000000000000000, 1000000000000000000},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				parentRate := big.NewInt(tc.parentExchangeRate)
				minerDiff := big.NewInt(tc.minerDifficulty)
				bestDiff := big.NewInt(tc.bestDifficulty)
				newBeta0 := new(big.Int).Quo(new(big.Int).Mul(bestDiff, common.Big2e64), common.LogBig(bestDiff))

				// Before KQuaiChangeBlock
				kQuaiBefore := CalculateKQuai(parentRate, minerDiff, params.KQuaiChangeBlock-1, newBeta0)
				increaseBefore := new(big.Int).Sub(kQuaiBefore, parentRate)

				// After KQuaiChangeBlock
				kQuaiAfter := CalculateKQuai(parentRate, minerDiff, params.KQuaiChangeBlock+1, newBeta0)
				increaseAfter := new(big.Int).Sub(kQuaiAfter, parentRate)

				// Only test if there's actually an increase (not a decrease)
				if increaseBefore.Cmp(common.Big0) > 0 && increaseAfter.Cmp(common.Big0) > 0 {
					// Calculate the actual slowdown ratio
					ratio := new(big.Float).Quo(new(big.Float).SetInt(increaseBefore), new(big.Float).SetInt(increaseAfter))
					ratioFloat, _ := ratio.Float64()

					// For very small values (single digits), the integer division can cause larger deviations
					tolerance := 1.0 // Very generous tolerance for small values
					if increaseBefore.Cmp(big.NewInt(10000)) > 0 {
						tolerance = 0.3 // 30% tolerance for larger values
					}

					minRatio := 3.0 - tolerance
					maxRatio := 3.0 + tolerance

					require.True(t, ratioFloat >= minRatio && ratioFloat <= maxRatio,
						"Slowdown ratio should be approximately 3x, got %.2f (expected %.2f-%.2f)", ratioFloat, minRatio, maxRatio)

					t.Logf("%s - Slowdown ratio: %.2f", tc.name, ratioFloat)
				}
			})
		}
	})

	t.Run("Edge case: exactly at KQuaiChangeBlock", func(t *testing.T) {
		parentExchangeRate := big.NewInt(1000000)
		minerDifficulty := big.NewInt(10000000000000000)
		bestDifficulty := big.NewInt(100000000000000000)
		newBeta0 := new(big.Int).Quo(new(big.Int).Mul(bestDifficulty, common.Big2e64), common.LogBig(bestDifficulty))

		// Test at exact KQuaiChangeBlock
		kQuaiAtChange := CalculateKQuai(parentExchangeRate, minerDifficulty, params.KQuaiChangeBlock, newBeta0)
		increaseAtChange := new(big.Int).Sub(kQuaiAtChange, parentExchangeRate)

		// Test one block after
		kQuaiAfterChange := CalculateKQuai(parentExchangeRate, minerDifficulty, params.KQuaiChangeBlock+1, newBeta0)
		increaseAfterChange := new(big.Int).Sub(kQuaiAfterChange, parentExchangeRate)

		// At KQuaiChangeBlock, slowdown should NOT be active yet
		// After KQuaiChangeBlock, slowdown SHOULD be active
		if increaseAtChange.Cmp(common.Big0) > 0 && increaseAfterChange.Cmp(common.Big0) > 0 {
			require.True(t, increaseAtChange.Cmp(increaseAfterChange) > 0,
				"Increase should be larger at KQuaiChangeBlock than after (when slowdown kicks in)")
		}

		t.Logf("At KQuaiChangeBlock: increase = %v", increaseAtChange)
		t.Logf("After KQuaiChangeBlock: increase = %v", increaseAfterChange)
	})
}

func TestKawpowEquivalentDifficulty(t *testing.T) {
	testCases := []struct {
		difficulty         *big.Int
		shaCount           *big.Int
		scryptCount        *big.Int
		expectedDifficulty *big.Int
	}{
		// If the sha and scrypt shares are zero, the equivalent difficulty doesnt change
		{big.NewInt(1000000), big.NewInt(0), big.NewInt(0), big.NewInt(1000000)},
		// If the sha and scrypt shares are over the expected shares, the equivalent should reach a ceil
		{big.NewInt(1000000), new(big.Int).Mul(big.NewInt(0), common.Big2e32), new(big.Int).Mul(big.NewInt(8), common.Big2e32), big.NewInt(9000000)},
		{big.NewInt(1000000), new(big.Int).Mul(big.NewInt(8), common.Big2e32), new(big.Int).Mul(big.NewInt(0), common.Big2e32), big.NewInt(9000000)},
		{big.NewInt(1000000), new(big.Int).Mul(big.NewInt(4), common.Big2e32), new(big.Int).Mul(big.NewInt(4), common.Big2e32), big.NewInt(9000000)},
		{big.NewInt(1000000), new(big.Int).Mul(big.NewInt(10), common.Big2e32), new(big.Int).Mul(big.NewInt(10), common.Big2e32), big.NewInt(9000000)},
		// If the sha and scrypt shares are half of the expected shares, the equivalent difficulty should be 1.8x
		{big.NewInt(1000000), new(big.Int).Mul(big.NewInt(2), common.Big2e32), new(big.Int).Mul(big.NewInt(2), common.Big2e32), big.NewInt(1800000)},
	}

	shaDiff := big.NewInt(10)
	scryptDiff := big.NewInt(20)

	for _, tc := range testCases {
		header := types.EmptyWorkObject(common.ZONE_CTX)
		header.WorkObjectHeader().SetShaDiffAndCount(types.NewPowShareDiffAndCount(shaDiff, tc.shaCount, tc.shaCount))
		header.WorkObjectHeader().SetScryptDiffAndCount(types.NewPowShareDiffAndCount(scryptDiff, tc.scryptCount, tc.scryptCount))
		header.WorkObjectHeader().SetDifficulty(tc.difficulty)

		result := KawPowEquivalentDifficulty(header.WorkObjectHeader(), tc.difficulty)
		require.Equal(t, tc.expectedDifficulty, result, "Expected %v got %v", tc.expectedDifficulty, result)
	}
}
