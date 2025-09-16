package params

import (
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/stretchr/testify/require"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const (
	genAllocsStr = `
	[
		{
			"unlockSchedule": 1,
			"address": "0x0000000000000000000000000000000000000001",
			"award": 1029384756000000000000000000,
			"vested": 1029384756000000000000000000
		},
		{
			"unlockSchedule": 2,
			"address": "0x0000000000000000000000000000000000000002",
			"award": 500000000000000000000000,
			"vested": 500000000000000000000000
		},
		{
			"unlockSchedule": 3,
			"address": "0x0000000000000000000000000000000000000003",
			"award": 7000000000000000000000000,
			"vested": 7000000000000000000000000
		},
		{
			"unlockSchedule": 3,
			"address": "0x0000000000000000000000000000000000000003",
			"award": 4000000000000000000000000,
			"vested": 1000000000000000000000000
		},
		{
			"unlockSchedule": 3,
			"address": "0x0000000000000000000000000000000000000003",
			"award": 1000000000000000000000000,
			"vested": 700000000000000000000000
		}
	]`
)

var (
	expectedAllocs = [5]*GenesisAccount{
		{
			UnlockSchedule:  1,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000001", common.Location{0, 0}),
			Award:           new(big.Int).Mul(big.NewInt(1029384756), common.Big10e18),
			Vested:          new(big.Int).Mul(big.NewInt(1029384756), common.Big10e18),
			BalanceSchedule: &orderedmap.OrderedMap[uint64, *big.Int]{},
		},

		{
			UnlockSchedule:  2,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000002", common.Location{0, 0}),
			Award:           new(big.Int).Mul(big.NewInt(500000), common.Big10e18),
			Vested:          new(big.Int).Mul(big.NewInt(500000), common.Big10e18),
			BalanceSchedule: &orderedmap.OrderedMap[uint64, *big.Int]{},
		},

		{
			UnlockSchedule:  3,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000003", common.Location{0, 0}),
			Award:           new(big.Int).Mul(big.NewInt(7000000), common.Big10e18),
			Vested:          new(big.Int).Mul(big.NewInt(7000000), common.Big10e18),
			BalanceSchedule: &orderedmap.OrderedMap[uint64, *big.Int]{},
		},

		{
			UnlockSchedule:  3,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000003", common.Location{0, 0}),
			Award:           new(big.Int).Mul(big.NewInt(4000000), common.Big10e18),
			Vested:          new(big.Int).Mul(big.NewInt(1000000), common.Big10e18),
			BalanceSchedule: &orderedmap.OrderedMap[uint64, *big.Int]{},
		},

		{
			UnlockSchedule:  3,
			Address:         common.HexToAddress("0x0000000000000000000000000000000000000003", common.Location{0, 0}),
			Award:           new(big.Int).Mul(big.NewInt(1000000), common.Big10e18),
			Vested:          new(big.Int).Mul(big.NewInt(700000), common.Big10e18),
			BalanceSchedule: &orderedmap.OrderedMap[uint64, *big.Int]{},
		},
	}
)

func TestReadingGenallocs(t *testing.T) {

	allocs, err := decodeGenesisAllocs(strings.NewReader(genAllocsStr))
	require.NoError(t, err, "Unable to parse genesis file")

	for num, actualAlloc := range allocs {
		expectedAlloc := expectedAllocs[num]

		require.Equal(t, expectedAlloc.UnlockSchedule, actualAlloc.UnlockSchedule)
		require.Equal(t, expectedAlloc.Address, actualAlloc.Address)
		require.Equal(t, expectedAlloc.Award, actualAlloc.Award)
		require.Equal(t, expectedAlloc.Vested, actualAlloc.Vested)
	}
}

type expectedAllocValues struct {
	account       *GenesisAccount
	cliffAmount   *big.Int
	cliffBlock    uint64
	numUnlocks    uint64
	quaiPerUnlock *big.Int
}

func calcExpectedValues(account *GenesisAccount) expectedAllocValues {
	unlockSchedule := unlockSchedules[account.UnlockSchedule]

	cliffPercentage := new(big.Int).SetUint64(unlockSchedule.lumpSumPercentage)
	cliffAmount := cliffPercentage.Mul(cliffPercentage, account.Award)
	cliffAmount.Div(cliffAmount, common.Big100)

	if cliffAmount.Cmp(account.Vested) > 0 {
		cliffAmount = account.Vested
	}

	cliffIndex := unlockSchedule.lumpSumMonth * BlocksPerMonth

	var numUnlocks uint64 = 1 // Cliff index
	var quaiPerUnlock *big.Int
	if unlockSchedule.unlockDuration > 0 {
		// Number of unlocks is calculated by dividing the total allocated over duration then
		// calculating how many unlocks are required to reach (vested balance-cliff).

		unlockableBalance := new(big.Int).Sub(account.Award, cliffAmount)
		quaiPerUnlock = new(big.Int).Div(unlockableBalance, new(big.Int).SetUint64(unlockSchedule.unlockDuration))

		remainingBalance := new(big.Int).Sub(account.Vested, cliffAmount)
		numUnlocks += new(big.Int).Div(remainingBalance, quaiPerUnlock).Uint64()
	}

	return expectedAllocValues{
		account,
		cliffAmount,
		cliffIndex,
		numUnlocks,
		quaiPerUnlock,
	}
}

func TestCalculatingGenallocs(t *testing.T) {
	allocs, err := decodeGenesisAllocs(strings.NewReader(genAllocsStr))
	require.NoError(t, err, "Unable to parse genesis file")

	for allocNum, actualAlloc := range allocs {
		expectedAlloc := expectedAllocs[allocNum]
		expectedAllocValues := calcExpectedValues(expectedAlloc)
		actualAlloc.calculateLockedBalances()

		// Retrieve UnlockSchedule information.
		unlockSchedule := unlockSchedules[expectedAlloc.UnlockSchedule]

		// Verify the correct number of unlocks are allocated.
		require.Equal(t, expectedAllocValues.numUnlocks, uint64(actualAlloc.BalanceSchedule.Len()))

		totalAdded := new(big.Int)
		for actualUnlock := actualAlloc.BalanceSchedule.Oldest(); actualUnlock != nil; actualUnlock = actualUnlock.Next() {
			blockNum := actualUnlock.Key
			// There should never be unlocks after month 0, before month 12.
			if unlockSchedule.lumpSumMonth != 0 {
				// If lumpSum is not TGE, there should not be an unlock at TGE.
				require.NotEqual(t, 0, blockNum)
			}

			// Verify cliff amount and timing is as expected.
			if blockNum == expectedAllocValues.cliffBlock {
				require.Zero(t, expectedAllocValues.cliffAmount.Cmp(actualUnlock.Value))
			} else if blockNum != actualAlloc.BalanceSchedule.Newest().Key {
				// Verify that all unlocks except for the last one (due to rounding) are correct.
				require.Zero(t, expectedAllocValues.quaiPerUnlock.Cmp(actualUnlock.Value))
			}

			totalAdded.Add(totalAdded, actualUnlock.Value)
		}

		// Verify the total added is equal to the total allocated.
		require.Zero(t, expectedAllocValues.account.Vested.Cmp(totalAdded))
		require.Zero(t, actualAlloc.Vested.Cmp(totalAdded))
	}
}
