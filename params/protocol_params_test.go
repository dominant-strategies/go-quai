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
