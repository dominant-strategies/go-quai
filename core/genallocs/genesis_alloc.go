package genallocs

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
)

// GenesisAccount is an account in the state of the genesis block.
type GenesisAccount struct {
	VestSchedule    int                 `json:"Vest Schedule" gencodec:"required"`
	Address         common.Address      `json:"Address" gencodec:"required"`
	TotalBalance    *big.Int            `json:"Amount" gencodec:"required"`
	BalanceSchedule map[uint64]*big.Int // Map of blockNumber->balanceUnlocked (at that block).
}

type vestingSchedule struct {
	vestDuration  uint64 // Total vesting duration in years. The first year cliff is not part of vesting.
	tgePercentage uint64 // One-time percentage unlocked at TGE.
}

var vestingSchedules = [4]vestingSchedule{
	{
		// schedule0
		// Immediate unlock
		vestDuration:  0,
		tgePercentage: 100,
	},

	{
		// schedule1
		// Vesting duration: 5 years
		// TGE: 30%
		vestDuration:  5,
		tgePercentage: 15,
	},

	{
		// schedule2
		// Vesting duration: 3 years
		// TGE: 25%
		vestDuration:  3,
		tgePercentage: 25,
	},

	{
		// schedule3
		// Vesting duration: 3 years
		// TGE: 0%
		vestDuration:  3,
		tgePercentage: 0,
	},
}

// Will return all the GenesisAccounts with their calculated vesting schedules
func AllocateGenesisAccounts(filename string) ([]GenesisAccount, error) {
	// Read from allocs file.
	allocs, err := readGenesisAllocs(filename)
	if err != nil {
		return nil, err
	}

	// Calculate vesting schedules for each account.
	for i := range allocs {
		allocs[i].calculateLockedBalances()
	}
	return allocs, nil
}

// Parses the allocs JSON and populates the basic vesting info.
func readGenesisAllocs(filename string) ([]GenesisAccount, error) {
	// Open the JSON file
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return decodeGenesisAllocs(file)
}

func decodeGenesisAllocs(r io.Reader) ([]GenesisAccount, error) {
	// Decode into slice
	var accounts []GenesisAccount
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&accounts); err != nil {
		return nil, fmt.Errorf("failed to decode JSON: %w", err)
	}

	return accounts, nil
}

// Will calculate the unlock heights according to the pre-defined vesting schedule.
func (account *GenesisAccount) calculateLockedBalances() {
	account.BalanceSchedule = make(map[uint64]*big.Int)

	vestingSchedule := vestingSchedules[account.VestSchedule]

	// Calculate total unlock at tge.
	tgePercentage := new(big.Int).SetUint64(vestingSchedule.tgePercentage)
	tgeAmount := new(big.Int).Mul(account.TotalBalance, tgePercentage)
	tgeAmount.Div(tgeAmount, common.Big100) // Divide back by 100 to undo percentage.

	// Accumulate total rewards.
	totalDistributed := new(big.Int)
	var unlockIndex uint64 = 1
	account.BalanceSchedule[unlockIndex] = tgeAmount
	totalDistributed.Add(totalDistributed, tgeAmount)

	if vestingSchedule.vestDuration != 0 {
		// Calculate number of unlocks.
		numUnlocks := uint64((vestingSchedule.vestDuration) * 12) // Total months that unlock.

		// Calculate amount per unlock.
		quaiPerUnlock := new(big.Int).Sub(account.TotalBalance, tgeAmount)
		quaiPerUnlock.Div(quaiPerUnlock, new(big.Int).SetUint64(numUnlocks))

		// Calculate the unlock at each block height.
		for unlockIndex = 0; unlockIndex < numUnlocks; unlockIndex++ {
			account.BalanceSchedule[(12+unlockIndex)*params.BlocksPerMonth] = quaiPerUnlock
			totalDistributed.Add(totalDistributed, quaiPerUnlock)
		}
		// Get the last index out of the for loop.
		unlockIndex = (unlockIndex - 1 + 12) * params.BlocksPerMonth
	}

	// Calculate total added vs expected total. Add rounding balance to final unlock.
	roundingDifference := new(big.Int).Sub(account.TotalBalance, totalDistributed)
	account.BalanceSchedule[unlockIndex] = new(big.Int).Add(account.BalanceSchedule[unlockIndex], roundingDifference)
}
