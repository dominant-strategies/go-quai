package misc

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/stretchr/testify/assert"
)

func TestCalculateReward(t *testing.T) {
	// Create a mock primeTerminus and header
	primeTerminus := &types.WorkObject{}
	woheader := &types.WorkObjectHeader{}
	wb := &types.WorkObjectBody{}
	header := &types.Header{}
	wb.SetHeader(header)
	primeTerminus.SetBody(wb)

	// Test the case where header is in Qi Ledger Scope
	woheader.SetPrimaryCoinbase(common.BytesToAddress(hexutil.MustDecode("0x0080002942281f0731b46A4a2d6AD80cd0DC10A4"), common.Location([]byte{0, 0})))
	woheader.SetDifficulty(big.NewInt(500000))

	// Add logic to mock IsInQiLedgerScope for PrimaryCoinbase to return true
	expectedReward := big.NewInt(1000) // Example expected reward value
	actualReward := CalculateReward(primeTerminus, woheader)
	assert.Equal(t, expectedReward, actualReward, "CalculateReward should return the correct Qi reward")

	// Mock expected reward for Quai Ledger
	woheader.SetPrimaryCoinbase(common.BytesToAddress(hexutil.MustDecode("0x00002234700cAE768Cf4e6F04D9ED8996D0E4e1f"), common.Location([]byte{0, 0})))
	primeTerminus.Header().SetExchangeRate(big.NewInt(52821824897296313)) //10^18/log2(500000) numerator is the target reward in its and 5e5 is starting difficulty
	expectedQuaiReward := big.NewInt(999999999999999999)                  // Example expected reward value
	actualQuaiReward := CalculateReward(primeTerminus, woheader)
	assert.Equal(t, expectedQuaiReward, actualQuaiReward, "CalculateReward should return the correct Quai reward")
}

func TestQiToQuai(t *testing.T) {
	primeTerminus := &types.WorkObject{}
	wb := &types.WorkObjectBody{}
	header := &types.Header{}
	wb.SetHeader(header)
	primeTerminus.SetBody(wb)
	primeTerminus.Header().SetExchangeRate(big.NewInt(52821824897296313)) //10^18/log2(500000) numerator is the target reward in its and 5e5 is starting difficulty

	currentWo := &types.WorkObject{}
	currentWoHeader := &types.WorkObjectHeader{}
	currentWoHeader.SetDifficulty(big.NewInt(500000))
	currentWo.SetWorkObjectHeader(currentWoHeader)

	qiAmt := big.NewInt(1000) // 1 Qi or 1000 Qits

	// Expected result for conversion
	expectedQuaiAmt := big.NewInt(999999999999999999) // Replace with actual expected value

	// Call the function
	actualQuaiAmt := QiToQuai(primeTerminus, currentWo, qiAmt)

	// Assert the result
	assert.Equal(t, expectedQuaiAmt, actualQuaiAmt, "QiToQuai should return the correct Quai amount")
}
func TestQuaiToQi(t *testing.T) {
	primeTerminus := &types.WorkObject{}
	wb := &types.WorkObjectBody{}
	header := &types.Header{}
	wb.SetHeader(header)
	primeTerminus.SetBody(wb)
	primeTerminus.Header().SetExchangeRate(big.NewInt(52821824897296313)) // 10^18/log2(500000), numerator is the target reward in Qits, 5e5 is starting difficulty

	currentWo := &types.WorkObject{}
	currentWoHeader := &types.WorkObjectHeader{}
	currentWoHeader.SetDifficulty(big.NewInt(500000))
	currentWo.SetWorkObjectHeader(currentWoHeader)

	quaiAmt := big.NewInt(999999999999999999) // 1 Quai or 10^18 Qits

	// Expected result for conversion
	expectedQiAmt := big.NewInt(1000) // Replace with actual expected value

	// Call the function
	actualQiAmt := QuaiToQi(primeTerminus, currentWo, quaiAmt)

	// Assert the result
	assert.Equal(t, expectedQiAmt, actualQiAmt, "QuaiToQi should return the correct Qi amount")
}

func TestCalculateKQuai(t *testing.T) {
	prime := &types.WorkObject{}
	wb := &types.WorkObjectBody{}
	header := &types.Header{}
	wb.SetHeader(header)
	prime.SetBody(wb)
	prime.Header().SetExchangeRate(big.NewInt(52821824897296313)) // 10^18/log2(500000), numerator is the target reward in Qits, 5e5 is starting difficulty
	primeWoHeader := &types.WorkObjectHeader{}
	primeWoHeader.SetDifficulty(big.NewInt(500000))
	prime.ExchangeRate().Set(big.NewInt(5000000000000000000)) // Example value for KQuai
	prime.SetWorkObjectHeader(primeWoHeader)

	// Set beta0 and beta1 values for testing
	beta0 := big.NewFloat(-0.5)        // Example value for beta0
	beta1 := big.NewFloat(0.000189316) // Example value for beta1

	bigBeta0 := BigBeta(beta0)
	bigBeta1 := BigBeta(beta1)

	// Expected result for the calculation (replace with actual expected value)
	expectedKQuai := big.NewInt(5000000000000000000) // Placeholder expected value

	// Call the CalculateKQuai function
	actualKQuai := CalculateKQuai(prime, bigBeta0, bigBeta1)

	// Assert the result
	assert.Equal(t, expectedKQuai, actualKQuai, "CalculateKQuai should return the correct KQuai amount")
}

func BigBeta(beta *big.Float) *big.Int {
	bigBeta := new(big.Float).Mul(beta, new(big.Float).SetInt(common.Big2e64))
	bigBetaInt, _ := bigBeta.Int(nil)
	return bigBetaInt
}

func TestLogBig(t *testing.T) {
	// Test with a difficulty value
	difficulty := big.NewInt(500000)

	// Expected result (replace with actual expected logarithm result)
	expectedLog, pass := new(big.Int).SetString("349225800312206723030", 10)
	if !pass {
		t.Errorf("Failed to parse expected logarithmic result")
	}
	// Call the function
	actualLog := LogBig(difficulty)

	// Assert the result
	assert.Equal(t, expectedLog, actualLog, "LogBig should return the correct logarithmic result")
}
