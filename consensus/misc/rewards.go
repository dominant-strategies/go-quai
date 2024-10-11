package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"modernc.org/mathutil"
)

var (
	big2e64        = new(big.Int).Exp(big.NewInt(2), big.NewInt(64), nil)
	denominatorIts = new(big.Int).Mul(big.NewInt(1000000000000000000), big2e64)
)

func CalculateReward(primeTerminus *types.WorkObject, header *types.WorkObjectHeader) *big.Int {
	if header.PrimaryCoinbase().IsInQiLedgerScope() {
		return CalculateQiReward(header)
	} else {
		return CalculateQuaiReward(primeTerminus, header)
	}
}

// Calculate the amount of Quai that Qi can be converted to. Expect the current Header and the Qi amount in "qits", returns the quai amount in "its"
func QiToQuai(primeTerminus *types.WorkObject, header *types.WorkObject, qiAmt *big.Int) *big.Int {
	quaiByQi := new(big.Int).Mul(CalculateQuaiReward(primeTerminus, header.WorkObjectHeader()), qiAmt)
	return new(big.Int).Div(quaiByQi, CalculateQiReward(header.WorkObjectHeader()))
}

// Calculate the amount of Qi that Quai can be converted to. Expect the current Header and the Quai amount in "its", returns the Qi amount in "qits"
func QuaiToQi(primeTerminus *types.WorkObject, header *types.WorkObject, quaiAmt *big.Int) *big.Int {
	qiByQuai := new(big.Int).Mul(CalculateQiReward(header.WorkObjectHeader()), quaiAmt)
	return new(big.Int).Div(qiByQuai, CalculateQuaiReward(primeTerminus, header.WorkObjectHeader()))
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
func CalculateKQuai(primeTerminus *types.WorkObject, header *types.WorkObjectHeader, beta0 *big.Int, beta1 *big.Int) *big.Int {
	kQuai := new(big.Int).Set(primeTerminus.ExchangeRate()) // in Its
	d2 := LogBig(header.Difficulty())
	num := new(big.Int).Mul(beta0, d2)
	negnum := new(big.Int).Neg(num)
	denom := new(big.Int).Mul(beta1, header.Difficulty())
	frac := new(big.Int).Quo(negnum, denom)
	sub := new(big.Int).Sub(frac, common.Big2e64)
	bykQuai := new(big.Int).Mul(sub, kQuai)
	divisor := new(big.Int).Mul(params.OneOverAlpha, common.Big2e64)
	final := new(big.Int).Quo(bykQuai, divisor)

	return final
}

func CalculateQuaiReward(primeTerminus *types.WorkObject, header *types.WorkObjectHeader) *big.Int {
	kQuaiIts := new(big.Int).Set(primeTerminus.ExchangeRate())
	numerator := new(big.Int).Mul(kQuaiIts, LogBig(header.Difficulty()))
	return new(big.Int).Div(numerator, denominatorIts)
}

// CalculateQiReward caculates the qi that can be received for mining a block and returns value in qits
func CalculateQiReward(header *types.WorkObjectHeader) *big.Int {
	return new(big.Int).Mul(header.Difficulty(), params.OneOverKqi)
}

// CalculateExchangeRate based on the quai to qi and qi to quai exchange rates
func CalculateExchangeRate(quaiToQi *big.Int, qiToQuai *big.Int) *big.Int {
	return new(big.Int).Div(quaiToQi, qiToQuai)
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
	d := new(big.Int).Div(common.Big2e256, diff)
	c, m := mathutil.BinaryLog(d, consensus.MantBits)
	bigBits := new(big.Int).Mul(big.NewInt(int64(c)), new(big.Int).Exp(big.NewInt(2), big.NewInt(consensus.MantBits), nil))
	bigBits = new(big.Int).Add(bigBits, m)
	return bigBits
}
