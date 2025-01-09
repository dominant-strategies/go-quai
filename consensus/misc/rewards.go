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
func CalculateKQuai(block *types.WorkObject, parentExchangeRate *big.Int, minerDifficulty *big.Int, beta0 *big.Int) *big.Int {
	// Set kQuai to the exchange rate from the header
	kQuai := new(big.Int).Set(parentExchangeRate) // in Its

	// Calculate log of the difficulty
	d2 := LogBig(minerDifficulty)

	// Multiply beta0 and d2
	num := new(big.Int).Mul(beta0, d2)

	// Negate num
	negnum := new(big.Int).Neg(num)

	// Multiply beta1 and the difficulty
	denom := new(big.Int).Mul(common.Big2e64, minerDifficulty)

	// Divide negnum by denom
	frac := new(big.Int).Quo(negnum, denom)

	// Subtract 2^64 from frac
	sub := new(big.Int).Sub(frac, common.Big2e64)

	// Multiply sub by kQuai
	bykQuai := new(big.Int).Mul(sub, kQuai)

	// Multiply params.OneOverAlpha by 2^64
	divisor := new(big.Int).Mul(params.OneOverAlpha, common.Big2e64)

	// Divide bykQuai by divisor to get the final result
	delta := new(big.Int).Quo(bykQuai, divisor)

	final := new(big.Int).Add(kQuai, delta)

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
