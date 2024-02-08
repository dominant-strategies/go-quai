package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/core/types"
)

// CalculateReward calculates the coinbase rewards depending on the type of the block
func CalculateRewardForQuai(header *types.Header) *big.Int {
	//// This Reward Schedule is only for Iron Age Testnet and has nothing to do
	//// with the Mainnet Schedule
	return new(big.Int).Mul(header.Difficulty(), big.NewInt(10e8))
}

func CalculateRewardForQi(header *types.Header) map[uint8]uint8 {
	rewardFromDifficulty := new(big.Int).Add(types.Denominations[types.MaxDenomination], types.Denominations[10])
	return findMinDenominations(rewardFromDifficulty)
}

func CalculateRewardForQiWithFees(header *types.Header, fees *big.Int) map[uint8]uint8 {
	rewardFromDifficulty := new(big.Int).Add(types.Denominations[types.MaxDenomination], types.Denominations[10])
	reward := new(big.Int).Add(rewardFromDifficulty, fees)
	return findMinDenominations(reward)
}

func CalculateRewardForQiWithFeesBigInt(header *types.Header, fees *big.Int) *big.Int {
	rewardFromDifficulty := new(big.Int).Add(types.Denominations[types.MaxDenomination], types.Denominations[10])
	reward := new(big.Int).Add(rewardFromDifficulty, fees)
	return reward
}

// findMinDenominations finds the minimum number of denominations to make up the reward
func findMinDenominations(reward *big.Int) map[uint8]uint8 {
	// Store the count of each denomination used (map denomination to count)
	denominationCount := make(map[uint8]uint8)
	amount := new(big.Int).Set(reward)

	// Iterate over the denominations in descending order (by key)
	for i := 15; i >= 0; i-- {
		denom := types.Denominations[uint8(i)]

		// Calculate the number of times the denomination fits into the remaining amount
		count := new(big.Int).Div(amount, denom)

		// Ensure that the amount never goes below zero
		totalValue := new(big.Int).Mul(count, denom)
		newAmount := new(big.Int).Sub(amount, totalValue)
		if newAmount.Cmp(big.NewInt(0)) >= 0 {
			if count.Cmp(big.NewInt(0)) > 0 {
				denominationCount[uint8(i)] = uint8(count.Uint64())
				amount = newAmount
			}
		}
	}

	return denominationCount
}
