package params

import (
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCalculateLockupByteRewardMultiple(t *testing.T) {

	type rewardMultiple struct {
		lockupByte       uint8
		blockNumber      uint64
		expectedMultiple *big.Int
		expectedError    string
	}

	year1BlockNumber := BlocksPerYear
	year2BlockNumber := 2 * BlocksPerYear
	year3BlockNumber := 3 * BlocksPerYear
	year4BlockNumber := 4 * BlocksPerYear
	year5BlockNumber := 5 * BlocksPerYear

	testCases := []rewardMultiple{
		{
			lockupByte:       0,
			blockNumber:      1000,
			expectedMultiple: nil,
			expectedError:    errors.New("invalid lockup byte used").Error(),
		},
		{
			lockupByte:       4,
			blockNumber:      1000,
			expectedMultiple: nil,
			expectedError:    errors.New("invalid lockup byte used").Error(),
		},
		{
			lockupByte:       6,
			blockNumber:      1000,
			expectedMultiple: nil,
			expectedError:    errors.New("invalid lockup byte used").Error(),
		},
		{
			lockupByte:       1,
			blockNumber:      1000,
			expectedMultiple: big.NewInt(103500),
			expectedError:    "",
		},
		{
			lockupByte:       1,
			blockNumber:      10000000000,
			expectedMultiple: big.NewInt(100218),
			expectedError:    "",
		},
		{
			lockupByte:       1,
			blockNumber:      year1BlockNumber,
			expectedMultiple: big.NewInt(103500),
			expectedError:    "",
		},
		{
			lockupByte:       1,
			blockNumber:      year2BlockNumber,
			expectedMultiple: big.NewInt(102679),
			expectedError:    "",
		},
		{
			lockupByte:       1,
			blockNumber:      year3BlockNumber,
			expectedMultiple: big.NewInt(101859),
			expectedError:    "",
		},
		{
			lockupByte:       1,
			blockNumber:      year4BlockNumber,
			expectedMultiple: big.NewInt(101038),
			expectedError:    "",
		},
		{
			lockupByte:       2,
			blockNumber:      1000,
			expectedMultiple: big.NewInt(110000),
			expectedError:    "",
		},
		{
			lockupByte:       2,
			blockNumber:      10000000000,
			expectedMultiple: big.NewInt(100625),
			expectedError:    "",
		},
		{
			lockupByte:       2,
			blockNumber:      year1BlockNumber,
			expectedMultiple: big.NewInt(110000),
			expectedError:    "",
		},
		{
			lockupByte:       2,
			blockNumber:      year2BlockNumber,
			expectedMultiple: big.NewInt(107656),
			expectedError:    "",
		},
		{
			lockupByte:       2,
			blockNumber:      year3BlockNumber,
			expectedMultiple: big.NewInt(105312),
			expectedError:    "",
		},
		{
			lockupByte:       2,
			blockNumber:      year4BlockNumber,
			expectedMultiple: big.NewInt(102968),
			expectedError:    "",
		},
		{
			lockupByte:       3,
			blockNumber:      1000,
			expectedMultiple: big.NewInt(125000),
			expectedError:    "",
		},
		{
			lockupByte:       3,
			blockNumber:      10000000000,
			expectedMultiple: big.NewInt(101562),
			expectedError:    "",
		},
		{
			lockupByte:       3,
			blockNumber:      year1BlockNumber,
			expectedMultiple: big.NewInt(125000),
			expectedError:    "",
		},
		{
			lockupByte:       3,
			blockNumber:      year2BlockNumber,
			expectedMultiple: big.NewInt(119140),
			expectedError:    "",
		},
		{
			lockupByte:       3,
			blockNumber:      year3BlockNumber,
			expectedMultiple: big.NewInt(113281),
			expectedError:    "",
		},
		{
			lockupByte:       3,
			blockNumber:      year4BlockNumber,
			expectedMultiple: big.NewInt(107421),
			expectedError:    "",
		},
		{
			lockupByte:       3,
			blockNumber:      year5BlockNumber,
			expectedMultiple: big.NewInt(101562),
			expectedError:    "",
		},
	}

	for _, test := range testCases {
		multiple, err := CalculateLockupByteRewardsMultiple(test.lockupByte, test.blockNumber)
		require.Equal(t, test.expectedMultiple, multiple)
		if err != nil {
			require.Equal(t, test.expectedError, err.Error())
		} else {
			require.Equal(t, nil, err)
		}
	}
}

func TestCalculateCoinbaseValueWithLockup(t *testing.T) {
	// value should not change for the first two months
	twoMonthBlock := 2 * BlocksPerMonth
	value := big.NewInt(1000)
	lockedValue := CalculateCoinbaseValueWithLockup(value, 1, twoMonthBlock-1)
	require.Equal(t, lockedValue.Uint64(), value.Uint64())

	// block after the two months should have value greater than than the input
	// value
	lockedValue = CalculateCoinbaseValueWithLockup(value, 1, twoMonthBlock+1)
	require.Greater(t, lockedValue.Uint64(), value.Uint64())
}

func TestOneOverKQi(t *testing.T) {

	doublingBlocks := (365 * BlocksPerDay * 269) / 100

	blockNumbers := []uint64{0, 10, 1000, 10000000, 100000000000, 100000000}
	expectedOneOverKQi := []uint64{26000000, 26000015, 26001532, 41324434, 104000000, 104000000}

	// at the doubling block, it should be double the base rate
	blockNumbers = append(blockNumbers, doublingBlocks)
	expectedOneOverKQi = append(expectedOneOverKQi, 52000000)

	// at the second doubling, it should be 4x the base rate
	blockNumbers = append(blockNumbers, 2*doublingBlocks)
	expectedOneOverKQi = append(expectedOneOverKQi, 104000000)

	// at 1.5 first doubling, it should be 3x the base rate
	blockNumbers = append(blockNumbers, 3*doublingBlocks/2)
	expectedOneOverKQi = append(expectedOneOverKQi, 78000000)

	for i, number := range blockNumbers {
		oneOverKQi := OneOverKqi(number)
		require.Equal(t, expectedOneOverKQi[i], oneOverKQi.Uint64())
	}
}

type GasTestCase struct {
	stateSize          *big.Int
	contractSize       *big.Int
	baseRate           uint64
	expectedScaledRate uint64
}

func TestCalculateGasWithStateScaling(t *testing.T) {
	tests := []GasTestCase{{
		stateSize:          big.NewInt(100),
		contractSize:       big.NewInt(0),
		baseRate:           42000,
		expectedScaledRate: 13952,
	}, {
		stateSize:          big.NewInt(0),
		contractSize:       big.NewInt(0),
		baseRate:           42000,
		expectedScaledRate: 0,
	}, {
		stateSize:          nil,
		contractSize:       big.NewInt(0),
		baseRate:           42000,
		expectedScaledRate: 0,
	}, {
		stateSize:          nil,
		contractSize:       nil,
		baseRate:           42000,
		expectedScaledRate: 0,
	}, {
		stateSize:          big.NewInt(1000000000),
		contractSize:       nil,
		baseRate:           42000,
		expectedScaledRate: 62784,
	}, {
		stateSize:          nil,
		contractSize:       big.NewInt(1000000000),
		baseRate:           42000,
		expectedScaledRate: 62784,
	}, {
		stateSize:          big.NewInt(100000),
		contractSize:       big.NewInt(1000),
		baseRate:           42000,
		expectedScaledRate: 55808,
	},
	}

	for _, test := range tests {
		scaledGas := CalculateGasWithStateScaling(test.stateSize, test.contractSize, test.baseRate)
		require.Equal(t, test.expectedScaledRate, scaledGas)
	}
}
