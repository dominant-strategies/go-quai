package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"modernc.org/mathutil"
)

func CalculateReward(header *types.WorkObjectHeader, difficulty *big.Int, exchangeRate *big.Int) *big.Int {
	var reward *big.Int
	if header.PrimaryCoinbase().IsInQiLedgerScope() {
		reward = new(big.Int).Set(CalculateQiReward(header, difficulty))
	} else {
		reward = new(big.Int).Set(CalculateQuaiReward(difficulty, exchangeRate))
	}

	return reward
}

// Calculate the amount of Quai that Qi can be converted to. Expect the current Header and the Qi amount in "qits", returns the quai amount in "its"
func QiToQuai(block *types.WorkObject, exchangeRate *big.Int, difficulty *big.Int, qiAmt *big.Int) *big.Int {
	quaiByQi := new(big.Int).Mul(CalculateQuaiReward(difficulty, exchangeRate), qiAmt)
	return new(big.Int).Quo(quaiByQi, CalculateQiReward(block.WorkObjectHeader(), difficulty))
}

// Calculate the amount of Qi that Quai can be converted to. Expect the current Header and the Quai amount in "its", returns the Qi amount in "qits"
func QuaiToQi(block *types.WorkObject, exchangeRate *big.Int, difficulty *big.Int, quaiAmt *big.Int) *big.Int {
	qiByQuai := new(big.Int).Mul(CalculateQiReward(block.WorkObjectHeader(), difficulty), quaiAmt)
	return new(big.Int).Quo(qiByQuai, CalculateQuaiReward(difficulty, exchangeRate))
}

// ComputeConversionAmountInQuai computes the amount of conversion volume in
// quai for the given set of newInboundEtxs using the values from the header
func ComputeConversionAmountInQuai(header *types.WorkObject, newInboundEtxs types.Transactions) *big.Int {
	conversionAmountInQuai := big.NewInt(0)
	for _, etx := range newInboundEtxs {
		// If the etx is conversion
		if types.IsConversionTx(etx) {
			value := etx.Value()
			// If to is in Qi, convert the value into Qi
			if etx.To().IsInQiLedgerScope() {
				conversionAmountInQuai = new(big.Int).Add(conversionAmountInQuai, value)
			}
			// If To is in Quai, convert the value into Quai
			if etx.To().IsInQuaiLedgerScope() {
				value = QiToQuai(header, header.ExchangeRate(), header.MinerDifficulty(), value)
				conversionAmountInQuai = new(big.Int).Add(conversionAmountInQuai, value)
			}
		}
	}
	return conversionAmountInQuai
}

// ApplyCubicDiscount applies the slippage based on the historical
// converted amounts and apply the
func ApplyCubicDiscount(valueInt, meanInt *big.Int) *big.Float {
	value := new(big.Float).SetInt(valueInt)
	mean := new(big.Float).SetInt(meanInt)
	tenTimesAverage := new(big.Float).Mul(mean, new(big.Float).SetInt64(10))
	if value.Cmp(mean) <= 0 {
		value = new(big.Float).Mul(value, new(big.Float).SetInt64(99))
		value = new(big.Float).Quo(value, new(big.Float).SetInt64(100))
		return value
	} else if value.Cmp(tenTimesAverage) > 0 {
		return new(big.Float).SetInt64(0)
	} else {
		normalizedValue := new(big.Float).Quo(value, tenTimesAverage)
		normalizedValueSquare := new(big.Float).Mul(normalizedValue, normalizedValue)
		normalizedValueCube := new(big.Float).Mul(normalizedValueSquare, normalizedValue)
		discountedValue := new(big.Float).Sub(new(big.Float).SetInt64(1), normalizedValueCube)
		// Make sure that discounted value is greater than zero as a sanity check
		if discountedValue.Cmp(new(big.Float).SetInt64(0)) <= 0 {
			return new(big.Float).SetInt64(0)
		} else {
			// Return the actual discounted amount by multiplying the value
			return new(big.Float).Mul(discountedValue, new(big.Float).SetInt(valueInt))
		}
	}
}

// CalculateQuaiReward calculates the quai that can be recieved for mining a block and returns value in its
// k_quai = state["K Quai"]
// alpha = params["Controller Alpha Parameter"]
// D = spaces[0]["Block Difficulty"]
// D = sum(D) / len(D)
// d1 = D
// d2 = log(D, params["Quai Reward Base Parameter"])
// x_d = d1 / d2
// x_b_star = -spaces[1]["Beta"][0] / spaces[1]["Beta"][1]
// k_quai += alpha * (x_b_star / x_d - 1) * k_quai
// spaces = [{"K Qi": state["K Qi"], "K Quai": k_quai}, spaces[1]]
// return spaces
func CalculateKQuai(parentExchangeRate *big.Int, minerDifficulty *big.Int, xbStar *big.Int) *big.Int {
	// Set kQuai to the exchange rate from the header
	kQuai := new(big.Int).Set(parentExchangeRate) // in Its

	// Calculate log of the difficulty
	d1 := new(big.Int).Mul(common.Big2e64, minerDifficulty)
	d2 := LogBig(minerDifficulty)

	// k_quai += alpha * (x_b_star / x_d - 1) * k_quai
	// k_quai = k_quai + alpha * (x_b_star / x_d - 1) * k_quai
	// k_quai = (k_quai * d1 + k_quai * alpha * (x_b_star * log(d1) - d1))/d1

	// To keep the maximum amount of precision,
	// denum = d1 * 1/alpha
	// adder = k_quai * denum
	// num = (adder + k_quai * (xbStar * log(d1) - d1))
	// There is a 2^64 element here on all the terms to keep the decimals from
	// the log

	// Multiply beta1 and the difficulty
	// Multiply params.OneOverAlpha by 2^64
	denum := new(big.Int).Mul(d1, params.OneOverAlpha)
	adder := new(big.Int).Mul(kQuai, denum)

	// Multiply beta0 and d2
	num := new(big.Int).Mul(xbStar, d2)
	// Subtract the d1
	num = new(big.Int).Sub(num, d1)
	// Multiply by kQuai
	num = new(big.Int).Mul(num, kQuai)
	// Add the previous kQuai with the denum multiplied(adder)
	num = new(big.Int).Add(num, adder)

	// Divide bykQuai by divisor to get the final result
	final := new(big.Int).Quo(num, denum)

	return final
}

func CalculateQuaiReward(difficulty *big.Int, exchangeRate *big.Int) *big.Int {
	numerator := new(big.Int).Mul(exchangeRate, LogBig(difficulty))
	reward := new(big.Int).Quo(numerator, common.Big2e64)
	if reward.Cmp(common.Big0) == 0 {
		reward = big.NewInt(1)
	}
	return reward
}

// CalculateQiReward caculates the qi that can be received for mining a block and returns value in qits
func CalculateQiReward(header *types.WorkObjectHeader, difficulty *big.Int) *big.Int {
	qiReward := new(big.Int).Quo(difficulty, params.OneOverKqi(header.NumberU64()))
	if qiReward.Cmp(common.Big0) == 0 {
		qiReward = big.NewInt(1)
	}
	return qiReward
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

// IntrinsicLogEntropy returns the logarithm of the intrinsic entropy reduction of a PoW hash
func LogBig(diff *big.Int) *big.Int {
	diffCopy := new(big.Int).Set(diff)
	c, m := mathutil.BinaryLog(diffCopy, consensus.MantBits)
	bigBits := new(big.Int).Mul(big.NewInt(int64(c)), new(big.Int).Exp(big.NewInt(2), big.NewInt(consensus.MantBits), nil))
	bigBits = new(big.Int).Add(bigBits, m)
	return bigBits
}
