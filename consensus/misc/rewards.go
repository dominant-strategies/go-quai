package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/core/types"
)

func CalculateReward(header *types.WorkObjectHeader) *big.Int {
	if header.PrimaryCoinbase().IsInQiLedgerScope() {
		return CalculateQiReward(header)
	} else {
		return CalculateQuaiReward(header)
	}
}

// Calculate the amount of Quai that Qi can be converted to. Expect the current Header and the Qi amount in "qits", returns the quai amount in "its"
func QiToQuai(currentHeader *types.WorkObjectHeader, qiAmt *big.Int) *big.Int {
	quaiPerQi := new(big.Int).Div(CalculateQuaiReward(currentHeader), CalculateQiReward(currentHeader))
	return new(big.Int).Mul(qiAmt, quaiPerQi)
}

// Calculate the amount of Qi that Quai can be converted to. Expect the current Header and the Quai amount in "its", returns the Qi amount in "qits"
func QuaiToQi(currentHeader *types.WorkObjectHeader, quaiAmt *big.Int) *big.Int {
	qiPerQuai := new(big.Int).Div(CalculateQiReward(currentHeader), CalculateQuaiReward(currentHeader))
	return new(big.Int).Mul(quaiAmt, qiPerQuai)
}

// CalculateQuaiReward calculates the quai that can be recieved for mining a block and returns value in its
func CalculateQuaiReward(header *types.WorkObjectHeader) *big.Int {
	return big.NewInt(1000000000000000000)
}

// CalculateQiReward caculates the qi that can be received for mining a block and returns value in qits
func CalculateQiReward(header *types.WorkObjectHeader) *big.Int {
	return big.NewInt(1000)
}

// FindMinDenominations finds the minimum number of denominations to make up the reward
func FindMinDenominations(reward *big.Int) map[uint8]uint64 {
	// Store the count of each denomination used (map denomination to count)
	denominationCount := make(map[uint8]uint64)
	amount := new(big.Int).Set(reward)

	// Iterate over the denominations in descending order (by key)
	for i := types.MaxDenomination; i >= 0; i-- {
		denom := types.Denominations[uint8(i)]

		// Calculate the number of times the denomination fits into the remaining amount
		count := new(big.Int).Div(amount, denom)
		if count.Cmp(big.NewInt(0)) == 0 {
			continue
		}
		// Ensure that the amount never goes below zero
		totalValue := new(big.Int).Mul(count, denom)
		newAmount := new(big.Int).Sub(amount, totalValue)
		if newAmount.Cmp(big.NewInt(0)) > 0 { // If the amount is still greater than zero, check the next denomination
			denominationCount[uint8(i)] = count.Uint64()
			amount = newAmount
		} else if newAmount.Cmp(big.NewInt(0)) == 0 { // If the amount is zero, add the denom and we are done
			denominationCount[uint8(i)] = count.Uint64()
			break
		} else { // If the amount is negative, ignore the denom and we are done
			break
		}
	}
	return denominationCount
}
