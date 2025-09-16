package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

func CalculateReward(header *types.WorkObjectHeader, difficulty *big.Int, exchangeRate *big.Int) *big.Int {
	var reward *big.Int
	if header.PrimaryCoinbase().IsInQiLedgerScope() {
		reward = new(big.Int).Set(CalculateQiReward(header, difficulty))
	} else {
		reward = new(big.Int).Set(CalculateQuaiReward(header, difficulty, exchangeRate))
	}

	return reward
}

// Calculate the amount of Quai that Qi can be converted to. Expect the current Header and the Qi amount in "qits", returns the quai amount in "its"
func QiToQuai(block *types.WorkObject, exchangeRate *big.Int, difficulty *big.Int, qiAmt *big.Int) *big.Int {
	quaiByQi := new(big.Int).Mul(CalculateQuaiReward(block.WorkObjectHeader(), difficulty, exchangeRate), qiAmt)
	return new(big.Int).Quo(quaiByQi, CalculateQiReward(block.WorkObjectHeader(), difficulty))
}

// Calculate the amount of Qi that Quai can be converted to. Expect the current Header and the Quai amount in "its", returns the Qi amount in "qits"
func QuaiToQi(block *types.WorkObject, exchangeRate *big.Int, difficulty *big.Int, quaiAmt *big.Int) *big.Int {
	qiByQuai := new(big.Int).Mul(CalculateQiReward(block.WorkObjectHeader(), difficulty), quaiAmt)
	return new(big.Int).Quo(qiByQuai, CalculateQuaiReward(block.WorkObjectHeader(), difficulty, exchangeRate))
}

// ComputeConversionAmountInQuai computes the amount of conversion volume in
// quai for the given set of newInboundEtxs using the values from the header
func ComputeConversionAmountInQuai(header *types.WorkObject, newInboundEtxs types.Transactions) *big.Int {
	conversionAmountInQuai := big.NewInt(0)
	for _, etx := range newInboundEtxs {
		// If the etx is conversion
		if types.IsConversionTx(etx) {
			value := etx.Value()
			if value.Cmp(common.Big0) == 0 {
				continue
			}
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
// average converted amounts and apply a cubic discount
// discountedValue := (1- (value/10*average)^3) * value
// Mininum of 20 basis point is applied to every transaction as well
func ApplyCubicDiscount(valueInt, meanInt *big.Int) *big.Float {
	value := new(big.Float).SetInt(valueInt)
	mean := new(big.Float).SetInt(meanInt)
	tenTimesAverage := new(big.Float).Mul(mean, new(big.Float).SetInt64(10))

	// Every transaction takes a 20 basis point slip
	minimumDiscount := new(big.Float).Mul(value, new(big.Float).Sub(new(big.Float).SetUint64(params.MinCubicDiscountDivisor), new(big.Float).SetUint64(params.MinCubicDiscountBasisPoint)))
	minimumDiscount = new(big.Float).Quo(minimumDiscount, new(big.Float).SetUint64(params.MinCubicDiscountDivisor))

	if value.Cmp(mean) <= 0 {
		return new(big.Float).Set(minimumDiscount)
	} else if value.Cmp(tenTimesAverage) > 0 {
		return new(big.Float).SetInt64(0)
	} else {
		normalizedValue := new(big.Float).Quo(value, tenTimesAverage)
		normalizedValueSquare := new(big.Float).Mul(normalizedValue, normalizedValue)
		normalizedValueCube := new(big.Float).Mul(normalizedValueSquare, normalizedValue)
		// always add 10 basis point discount to make the function continous
		tenBasisDiscount := new(big.Float).Quo(new(big.Float).SetInt64(1), new(big.Float).SetInt64(1000))
		normalizedValueCube = new(big.Float).Add(normalizedValueCube, tenBasisDiscount)
		discountedValue := new(big.Float).Sub(new(big.Float).SetInt64(1), normalizedValueCube)
		// Return the actual discounted amount by multiplying the value
		discountedValue = new(big.Float).Mul(discountedValue, new(big.Float).SetInt(valueInt))
		// Make sure that discounted value is greater than zero as a sanity check
		if discountedValue.Cmp(new(big.Float).SetInt(common.Big0)) < 0 {
			return new(big.Float).SetInt(common.Big0)
		} else {
			return discountedValue
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
func CalculateKQuai(parentExchangeRate *big.Int, minerDifficulty *big.Int, blockNumber uint64, xbStar *big.Int) *big.Int {
	// Set kQuai to the exchange rate from the header
	kQuai := new(big.Int).Set(parentExchangeRate) // in Its

	// Calculate log of the difficulty
	d1 := new(big.Int).Mul(common.Big2e64, minerDifficulty)
	d2 := common.LogBig(minerDifficulty)

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

	var kQuaiIncrease bool
	// Multiply beta0 and d2
	num := new(big.Int).Mul(xbStar, d2)
	// Subtract the d1
	num = new(big.Int).Sub(num, d1)
	// If the num is greater than zero, then kQuai will be increased
	kQuaiIncrease = num.Cmp(common.Big0) > 0

	// If kQuaiIncrease is true, slow down the increase in kQuai by reducing the adjustment
	if blockNumber > params.KQuaiChangeBlock && kQuaiIncrease {
		// Remove 1/3 of the increase to slow down the increase in kQuai after
		// the kawpow fork
		if blockNumber < params.KawPowForkBlock {
			num = new(big.Int).Div(num, common.Big3)
		}
	}

	// Multiply by kQuai
	num = new(big.Int).Mul(num, kQuai)
	// Add the previous kQuai with the denum multiplied(adder)
	num = new(big.Int).Add(num, adder)

	// Divide bykQuai by divisor to get the final result
	final := new(big.Int).Quo(num, denum)

	return final
}

func CalculateQuaiReward(header *types.WorkObjectHeader, difficulty *big.Int, exchangeRate *big.Int) *big.Int {
	if header.PrimeTerminusNumber().Uint64() >= params.KawPowForkBlock {
		difficulty = KawPowEquivalentDifficulty(header, difficulty)
	}
	numerator := new(big.Int).Mul(exchangeRate, common.LogBig(difficulty))
	reward := new(big.Int).Quo(numerator, common.Big2e64)
	if reward.Cmp(common.Big0) == 0 {
		reward = big.NewInt(1)
	}
	return reward
}

// CalculateQiReward caculates the qi that can be received for mining a block and returns value in qits
func CalculateQiReward(header *types.WorkObjectHeader, difficulty *big.Int) *big.Int {
	var qiReward *big.Int
	if header.PrimeTerminusNumber().Uint64() >= params.KawPowForkBlock {
		difficulty = KawPowEquivalentDifficulty(header, difficulty)
	}
	qiReward = new(big.Int).Quo(difficulty, params.OneOverKqi(header.NumberU64()))
	if qiReward.Cmp(common.Big0) == 0 {
		qiReward = big.NewInt(1)
	}
	return qiReward
}

// Find the KawPow equivalent difficulty by adjusting for various algos in the hash shares
func KawPowEquivalentDifficulty(header *types.WorkObjectHeader, difficulty *big.Int) *big.Int {
	// Need to adjust the difficulty based on the current average reported sha and scrypt shares
	scryptShares := header.ScryptDiffAndCount().Count() //This is in 2^32 units
	shaShares := header.ShaDiffAndCount().Count()

	// Difficulty Adjustment formula base 10:
	// Where:
	// expectedShares = 2^WorkSharesThresholdDiff + 1
	// Diff_adjusted = (Diff_original * expectedShares / (expectedShares - Min(scryptShares + shaShares, expectedShares - 1))
	//
	// Difficulty adjustment formula: Diff_adjusted = (Diff_original * (2^32 * 2^WorkSharesThresholdDiff)) / (2^32 * 2^WorkShareThresholdDiff - Min(scryptShares + shaShares, 2^32*2^WorkSharesThresholdDiff - 2^32))
	expectedTotalShares := new(big.Int).Mul(big.NewInt(int64(params.ExpectedWorksharesPerBlock+1)), common.Big2e32)                       // Expected total shares in 2^32 units
	totalMultiAlgoShares := math.BigMin(new(big.Int).Add(scryptShares, shaShares), new(big.Int).Sub(expectedTotalShares, common.Big2e32)) // Make sure we don't exceed expected shares
	numerator := new(big.Int).Mul(difficulty, expectedTotalShares)
	denominator := new(big.Int).Sub(expectedTotalShares, totalMultiAlgoShares)
	adjustedDifficulty := new(big.Int).Div(numerator, denominator)
	return adjustedDifficulty
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
