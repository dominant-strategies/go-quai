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
	header := &types.WorkObject{}
	qiAmt := big.NewInt(1000)

	// Expected result for conversion
	expectedQuaiAmt := big.NewInt(500) // Replace with actual expected value

	// Call the function
	actualQuaiAmt := QiToQuai(primeTerminus, header, qiAmt)

	// Assert the result
	assert.Equal(t, expectedQuaiAmt, actualQuaiAmt, "QiToQuai should return the correct Quai amount")
}

func TestCalculateQuaiReward(t *testing.T) {
	primeTerminus := &types.WorkObject{}
	header := &types.WorkObjectHeader{}

	// Mock ExchangeRate and Difficulty
	// primeTerminus.ExchangeRate = func() *big.Int {
	// 	return big.NewInt(100000)
	// }
	// header.Difficulty = func() *big.Int {
	// 	return big.NewInt(100)
	// }

	// Expected result
	expectedReward := big.NewInt(50) // Replace with actual expected value

	// Call the function
	actualReward := CalculateQuaiReward(primeTerminus, header)

	// Assert the result
	assert.Equal(t, expectedReward, actualReward, "CalculateQuaiReward should return the correct reward")
}

func TestLogBig(t *testing.T) {
	// Test with a difficulty value
	difficulty := big.NewInt(1000000000)

	// Expected result (replace with actual expected logarithm result)
	expectedLog := big.NewInt(123456) // Replace with actual expected value

	// Call the function
	actualLog := LogBig(difficulty)

	// Assert the result
	assert.Equal(t, expectedLog, actualLog, "LogBig should return the correct logarithmic result")
}
