package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/core/types"
)

func CalculateReward(header *types.Header) *big.Int {
	if header.Coinbase().IsInQiLedgerScope() {
		return calculateQiReward(header)
	} else {
		return calculateQuaiReward(header)
	}
}

// Calculate the amount of Quai that Qi can be converted to. Expect the current Header and the Qi amount in "qits", returns the quai amount in "its"
func QiToQuai(currentHeader *types.Header, qiAmt *big.Int) *big.Int {
	quaiPerQi := new(big.Int).Div(calculateQuaiReward(currentHeader), calculateQiReward(currentHeader))
	return new(big.Int).Mul(qiAmt, quaiPerQi)
}

// Calculate the amount of Qi that Quai can be converted to. Expect the current Header and the Quai amount in "its", returns the Qi amount in "qits"
func QuaiToQi(currentHeader *types.Header, quaiAmt *big.Int) *big.Int {
	qiPerQuai := new(big.Int).Div(calculateQiReward(currentHeader), calculateQuaiReward(currentHeader))
	return new(big.Int).Mul(quaiAmt, qiPerQuai)
}

// CalculateQuaiReward calculates the quai that can be recieved for mining a block and returns value in its
func calculateQuaiReward(header *types.Header) *big.Int {
	return big.NewInt(1000000000000000000)
}

// CalculateQiReward caculates the qi that can be received for mining a block and returns value in qits
func calculateQiReward(header *types.Header) *big.Int {
	return big.NewInt(1000)
}

// FindMinDenominations finds the minimum number of denominations to make up the reward
func FindMinDenominations(reward *big.Int) map[uint8]uint8 {
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
