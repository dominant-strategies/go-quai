package genallocs

import (
	"log"
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
	expectedAllocs = [5]*GenesisAccount{
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
	account    *GenesisAccount
	tgeAmount  *big.Int
	numUnlocks uint64
}

func calcExpectedValues(account *GenesisAccount) expectedAllocValues {
	vestingSchedule := vestingSchedules[account.VestSchedule]

	tgeAmount := new(big.Int).Mul(account.TotalBalance, new(big.Int).SetUint64(vestingSchedule.tgePercentage))
	tgeAmount = new(big.Int).Div(tgeAmount, common.Big100) // Divide by 100 to undo percentage.
	// Fill in TGE unlock.
	var unlockHeight uint64 = 1
	account.BalanceSchedule[unlockHeight] = tgeAmount

	numUnlocks := new(big.Int).SetUint64(vestingSchedule.vestDuration * 12)
	unlockableBalance := new(big.Int).Sub(account.TotalBalance, tgeAmount)

	var totalAllocated *big.Int = new(big.Int).Set(tgeAmount)
	if unlockableBalance.Cmp(common.Big0) != 0 {
		unlockAmount := new(big.Int).Div(unlockableBalance, numUnlocks)

		var blockNum uint64
		for blockNum = uint64(12); blockNum < numUnlocks.Uint64()+12; blockNum++ {
			account.BalanceSchedule[blockNum*params.BlocksPerMonth] = unlockAmount
		}

		// Undo the final blockNum from the loop.
		unlockHeight = (blockNum - 1) * params.BlocksPerMonth

		// Calculate allocated vs expected.
		totalAllocated = new(big.Int).Add(totalAllocated, new(big.Int).Mul(unlockAmount, numUnlocks))
	}

	roundingDifference := new(big.Int).Sub(account.TotalBalance, totalAllocated)
	account.BalanceSchedule[unlockHeight] = new(big.Int).Add(account.BalanceSchedule[unlockHeight], roundingDifference)

	return expectedAllocValues{
		account,
		tgeAmount,
		numUnlocks.Uint64(),
	}
}

func TestCalculatingGenallocs(t *testing.T) {
	allocs, err := decodeGenesisAllocs(strings.NewReader(genAllocsStr))
	require.NoError(t, err, "Unable to parse genesis file")

	for allocNum, actualAlloc := range allocs {
		expectedAlloc := expectedAllocs[allocNum]
		expectedAllocValues := calcExpectedValues(expectedAlloc)
		actualAlloc.calculateLockedBalances()

		// Check lengths are equal, so we can do a one way check.
		require.Equal(t, len(expectedAllocs[allocNum].BalanceSchedule), len(actualAlloc.BalanceSchedule))

		totalAdded := new(big.Int)
		for blockNum, actualUnlock := range actualAlloc.BalanceSchedule {
			log.Print(blockNum)
			if blockNum == 1 {
				require.Zero(t, actualUnlock.Cmp(expectedAllocValues.tgeAmount))
			}

			require.Zero(t, actualUnlock.Cmp(expectedAlloc.BalanceSchedule[blockNum]))

			totalAdded.Add(totalAdded, actualUnlock)
		}

		require.Zero(t, expectedAllocValues.account.TotalBalance.Cmp(totalAdded))
		log.Print(new(big.Int).Sub(expectedAllocValues.account.TotalBalance, totalAdded))
	}
}
