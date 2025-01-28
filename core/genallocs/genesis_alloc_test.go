package genallocs

import (
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
)

const (
	genAllocsStr = `
	[
		{
			"Vest Schedule": 0,
			"Address": "0x0000000000000000000000000000000000000001",
			"Amount": 1029384756000000000000000000
		},
		{
			"Vest Schedule": 1,
			"Address": "0x0000000000000000000000000000000000000001",
			"Amount": 500000000000000000000000
		},
		{
			"Vest Schedule": 2,
			"Address": "0x0000000000000000000000000000000000000002",
			"Amount": 7000000000000000000000000
		},
		{
			"Vest Schedule": 3,
			"Address": "0x0000000000000000000000000000000000000003",
			"Amount": 1234567000000000000000000
		}
	]`
)

var (
	expectedAllocs = [4]*GenesisAccount{
		{
			VestSchedule:    0,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000001", common.Location{0, 0}),
			TotalBalance:    new(big.Int).Mul(big.NewInt(1029384756), common.Big10e18),
			BalanceSchedule: map[uint64]*big.Int{},
		},

		{
			VestSchedule:    1,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000001", common.Location{0, 0}),
			TotalBalance:    new(big.Int).Mul(big.NewInt(500000), common.Big10e18),
			BalanceSchedule: map[uint64]*big.Int{},
		},

		{
			VestSchedule:    2,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000002", common.Location{0, 0}),
			TotalBalance:    new(big.Int).Mul(big.NewInt(7000000), common.Big10e18),
			BalanceSchedule: map[uint64]*big.Int{},
		},

		{
			VestSchedule:    3,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000003", common.Location{0, 0}),
			TotalBalance:    new(big.Int).Mul(big.NewInt(1234567), common.Big10e18),
			BalanceSchedule: map[uint64]*big.Int{},
		},
	}
)

func TestReadingGenallocs(t *testing.T) {

	allocs, err := decodeGenesisAllocs(strings.NewReader(genAllocsStr))
	require.NoError(t, err, "Unable to parse genesis file")

	for num, actualAlloc := range allocs {
		expectedAlloc := expectedAllocs[num]

		require.Equal(t, expectedAlloc.VestSchedule, actualAlloc.VestSchedule)
		require.Equal(t, expectedAlloc.Address, actualAlloc.Address)
		require.Equal(t, expectedAlloc.TotalBalance, actualAlloc.TotalBalance)
	}
}

type expectedAllocValues struct {
	account     *GenesisAccount
	cliffAmount *big.Int
	cliffBlock  uint64
	numUnlocks  int
}

func calcExpectedValues(account *GenesisAccount) expectedAllocValues {
	vestingSchedule := vestingSchedules[account.VestSchedule]

	cliffPercentage := new(big.Int).SetUint64(vestingSchedule.lumpSumPercentage)
	cliffAmount := cliffPercentage.Mul(cliffPercentage, account.TotalBalance)
	cliffAmount.Div(cliffAmount, common.Big100)

	cliffIndex := vestingSchedule.lumpSumMonth * params.BlocksPerMonth

	numVestingUnlocks := vestingSchedule.vestDuration * 12
	numUnlocks := numVestingUnlocks + 1 // Include cliff index.

	return expectedAllocValues{
		account,
		cliffAmount,
		cliffIndex,
		numUnlocks,
	}
}

func TestCalculatingGenallocs(t *testing.T) {
	allocs, err := decodeGenesisAllocs(strings.NewReader(genAllocsStr))
	require.NoError(t, err, "Unable to parse genesis file")

	for allocNum, actualAlloc := range allocs {
		expectedAlloc := expectedAllocs[allocNum]
		expectedAllocValues := calcExpectedValues(expectedAlloc)
		actualAlloc.calculateLockedBalances()

		// Retrieve VestSchedule information.
		vestingSchedule := vestingSchedules[expectedAlloc.VestSchedule]

		// Verify the correct number of unlocks are allocated.
		require.Equal(t, expectedAllocValues.numUnlocks, len(actualAlloc.BalanceSchedule))

		totalAdded := new(big.Int)
		for blockNum, actualUnlock := range actualAlloc.BalanceSchedule {
			// There should never be unlocks after month 0, before month 12.
			if vestingSchedule.lumpSumMonth == 0 {
				// If lumpSum is TGE, there should not be an unlock at month 12.
				require.NotEqual(t, 12*params.BlocksPerMonth, blockNum)
			} else {
				// If lumpSum is not TGE, there should not be an unlock at TGE.
				require.NotEqual(t, 0, blockNum)
			}

			// In any case, there should never be an unlock between 0 and 12.
			require.False(t, blockNum > 0 && blockNum < 12*params.BlocksPerMonth)

			// Verify cliff amount and timing is as expected.
			if blockNum == expectedAllocValues.cliffBlock {
				require.Zero(t, expectedAllocValues.cliffAmount.Cmp(actualUnlock))
			}

			totalAdded.Add(totalAdded, actualUnlock)
		}

		// Verify the total added is equal to the total allocated.
		require.Zero(t, expectedAllocValues.account.TotalBalance.Cmp(totalAdded))
		require.Zero(t, actualAlloc.TotalBalance.Cmp(totalAdded))
	}
}
