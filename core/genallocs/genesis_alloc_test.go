package genallocs

import (
	"fmt"
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
			"Amount": 500000
		},
		{
			"Vest Schedule": 1,
			"Address": "0x0000000000000000000000000000000000000002",
			"Amount": 7000000
		},
		{
			"Vest Schedule": 2,
			"Address": "0x0000000000000000000000000000000000000003",
			"Amount": 1234567
		}
	]`
)

var (
	expectedAllocs = [5]GenesisAccount{
		{
			VestSchedule: 0,
			Address:      common.HexToAddress("0x0000000000000000000000000000000000000001", common.Location{0, 0}),
			TotalBalance: 500000,
			BalanceSchedule: map[uint64]*big.Int{
				0:                                 big.NewInt(500000 * 30 / 100),
				(12)*params.BlocksPerMonth - 1:    big.NewInt(5833),
				(12+1)*params.BlocksPerMonth - 1:  big.NewInt(5833),
				(12+2)*params.BlocksPerMonth - 1:  big.NewInt(5833),
				(12+3)*params.BlocksPerMonth - 1:  big.NewInt(5833),
				(12+4)*params.BlocksPerMonth - 1:  big.NewInt(5833),
				(12+58)*params.BlocksPerMonth - 1: big.NewInt(5833),
				(12+59)*params.BlocksPerMonth - 1: big.NewInt(5833),
				(12+60)*params.BlocksPerMonth - 1: big.NewInt(5833 + 20), // rounding
			},
		},
		{
			VestSchedule: 1,
			Address:      common.HexToAddress("0x0000000000000000000000000000000000000002", common.Location{0, 0}),
			TotalBalance: 7000000,
			BalanceSchedule: map[uint64]*big.Int{
				0:                                 big.NewInt(7000000 * 25 / 100),
				(12)*params.BlocksPerMonth - 1:    big.NewInt(145833),
				(12+1)*params.BlocksPerMonth - 1:  big.NewInt(145833),
				(12+2)*params.BlocksPerMonth - 1:  big.NewInt(145833),
				(12+3)*params.BlocksPerMonth - 1:  big.NewInt(145833),
				(12+4)*params.BlocksPerMonth - 1:  big.NewInt(145833),
				(12+34)*params.BlocksPerMonth - 1: big.NewInt(145833),
				(12+35)*params.BlocksPerMonth - 1: big.NewInt(145833),
				(12+36)*params.BlocksPerMonth - 1: big.NewInt(145833 + 12), // rounding
			},
		},
		{
			VestSchedule: 2,
			Address:      common.HexToAddress("0x0000000000000000000000000000000000000003", common.Location{0, 0}),
			TotalBalance: 1234567,
			BalanceSchedule: map[uint64]*big.Int{
				0:                                 big.NewInt(0),
				(12)*params.BlocksPerMonth - 1:    big.NewInt(34293),
				(12+1)*params.BlocksPerMonth - 1:  big.NewInt(34293),
				(12+2)*params.BlocksPerMonth - 1:  big.NewInt(34293),
				(12+3)*params.BlocksPerMonth - 1:  big.NewInt(34293),
				(12+4)*params.BlocksPerMonth - 1:  big.NewInt(34293),
				(12+34)*params.BlocksPerMonth - 1: big.NewInt(34293),
				(12+35)*params.BlocksPerMonth - 1: big.NewInt(34293),
				(12+36)*params.BlocksPerMonth - 1: big.NewInt(34293 + 18), // rounding
			},
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

func TestCalculatingGenallocs(t *testing.T) {
	allocs, err := decodeGenesisAllocs(strings.NewReader(genAllocsStr))
	require.NoError(t, err, "Unable to parse genesis file")

	for allocNum, actualAlloc := range allocs[:2] {
		actualAlloc.calculateLockedBalances()
		for blockNum, expectedUnlock := range expectedAllocs[allocNum].BalanceSchedule {
			require.Zero(t,
				expectedUnlock.Cmp(actualAlloc.BalanceSchedule[blockNum]),
				fmt.Sprintf("incorrect balance unlock on block %d", blockNum),
			)
		}
	}
}
