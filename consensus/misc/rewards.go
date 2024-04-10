package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

func CalculateReward(header *types.WorkObject, bigBitsDiff *big.Int) *big.Int {
	if header.Coinbase().IsInQiLedgerScope() {
		return calculateQiReward(header)
	} else {
		return calculateQuaiReward(header, bigBitsDiff)
	}
}

// Calculate the amount of Quai that Qi can be converted to. Expect the current Header and the Qi amount in "qits", returns the quai amount in "its"
func QiToQuai(currentHeader *types.WorkObject, qiAmt *big.Int, bigBitsDiff *big.Int) *big.Int {
	quaiPerQi := new(big.Int).Div(calculateQuaiReward(currentHeader, bigBitsDiff), calculateQiReward(currentHeader))
	result := new(big.Int).Mul(qiAmt, quaiPerQi)
	if result.Cmp(big.NewInt(0)) == 0 {
		return big.NewInt(1)
	}
	return result
}

// Calculate the amount of Qi that Quai can be converted to. Expect the current Header and the Quai amount in "its", returns the Qi amount in "qits"
func QuaiToQi(currentHeader *types.WorkObject, quaiAmt *big.Int, bigBitsDiff *big.Int) *big.Int {
	qiPerQuai := new(big.Int).Div(calculateQiReward(currentHeader), calculateQuaiReward(currentHeader, bigBitsDiff))
	result := new(big.Int).Mul(quaiAmt, qiPerQuai)
	if result.Cmp(types.Denominations[0]) < 0 {
		return types.Denominations[0]
	}
	return result
}

// CalculateQuaiReward calculates the quai that can be recieved for mining a block and returns value in its
func calculateQuaiReward(header *types.WorkObject, bigBitsDiff *big.Int) *big.Int {
	divisor := new(big.Int).Exp(big.NewInt(2), header.Header().ExchangeRate(), nil)
	return new(big.Int).Div(bigBitsDiff, divisor)
}

// CalculateQiReward caculates the qi that can be received for mining a block and returns value in qits
func calculateQiReward(header *types.WorkObject) *big.Int {
	return new(big.Int).Quo(header.Difficulty(), params.Kqi)
}

// FindMinDenominations finds the minimum number of denominations to make up the reward
func FindMinDenominations(reward *big.Int) map[uint8]uint8 {
	// Store the count of each denomination used (map denomination to count)
	denominationCount := make(map[uint8]uint8)
	amount := new(big.Int).Set(reward)

	// Iterate over the denominations in descending order (by key)
	for i := types.MaxDenomination; i >= 0; i-- {
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

// calculateExchangeRate calculates the exchange rate for the block
func CalculateExchangeRate(parent *types.WorkObject) (*big.Int, *big.Int, *big.Int, error) {
	qiToQuai := parent.Header().QiToQuai() //Units of Qi
	quaiToQi := parent.Header().QuaiToQi() //Units of Quai

	//Calculate the new amounts of qi and quai being exchanged
	for _, etx := range parent.ExtTransactions() {
		if etx.Conversion() {
			if etx.To().IsInQuaiLedgerScope() {
				//Add the value of the transaction to the total amount of Qi being converted to Quai in Quai
				qiToQuai = new(big.Int).Add(etx.Value(), qiToQuai)
			} else if etx.To().IsInQiLedgerScope() {
				//Add the value of the transaction to the total amount of Quai being converted to Qi in Qi
				quaiToQi = new(big.Int).Add(etx.Value(), quaiToQi)
			}
		}
	}
	//Add in the mined amounts
	qiToQuai = new(big.Int).Add(qiToQuai, parent.Header().MinedQi())
	quaiToQi = new(big.Int).Add(quaiToQi, parent.Header().MinedQuai())

	return qiToQuai, quaiToQi, parent.Header().ExchangeRate(), nil
}

// convert qiToHash
func QiToHash(qiAmount *big.Int, kqi *big.Int) *big.Int {
	if qiAmount.Cmp(big.NewInt(0)) <= 0 {
		return big.NewInt(0)
	}
	return new(big.Int).Mul(qiAmount, kqi)
}
