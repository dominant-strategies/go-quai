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

// Defines the vestingSchedule parameters.
// Some vestingSchedules have a lumpSum payment, some at TGE, some at 1 year.
// All vestingSchedules begin regular unlocks after 1 year.
type vestingSchedule struct {
	vestDuration      int    // Total vesting duration in years. The first year cliff is not part of vesting.
	lumpSumPercentage uint64 // One-time percentage unlocked.
	lumpSumMonth      uint64 // Number of months before lump sum payment.
}

var vestingSchedules = [4]vestingSchedule{
	{
		// schedule0
		// Immediate unlock: 100% @ Month 0
		vestDuration:      0,
		lumpSumPercentage: 100,
		lumpSumMonth:      0,
	},

	{
		// schedule1
		// Vesting duration: 5 years
		// Lump Sum: 30% @ Month 0
		vestDuration:      5,
		lumpSumPercentage: 30,
		lumpSumMonth:      0,
	},

	{
		// schedule2
		// Vesting duration: 3 years
		// Lump Sum: 25% @ Month 0
		vestDuration:      3,
		lumpSumPercentage: 25,
		lumpSumMonth:      0,
	},

	{
		// schedule3
		// Vesting duration: 3 years
		// Lump Sum: 25% @ Month 12
		vestDuration:      3,
		lumpSumPercentage: 25,
		lumpSumMonth:      12,
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

	// Calculate total lump sum payment.
	lumpSumPercentage := new(big.Int).SetUint64(vestingSchedule.lumpSumPercentage)
	lumpSumAmount := new(big.Int).Mul(account.TotalBalance, lumpSumPercentage)
	lumpSumAmount.Div(lumpSumAmount, common.Big100) // Divide back by 100 to undo percentage.

	lumpSumIndex := vestingSchedule.lumpSumMonth
	account.BalanceSchedule[lumpSumIndex*params.BlocksPerMonth] = lumpSumAmount

	// Accumulate total rewards.
	var totalDistributed = new(big.Int)
	totalDistributed.Add(totalDistributed, lumpSumAmount)

	if vestingSchedule.vestDuration != 0 {
		// Calculate number of unlocks.
		numUnlocks := uint64((vestingSchedule.vestDuration) * 12) // Total months that unlock.

		// Calculate amount per unlock.
		quaiPerUnlock := new(big.Int).Sub(account.TotalBalance, lumpSumAmount)
		quaiPerUnlock.Div(quaiPerUnlock, new(big.Int).SetUint64(numUnlocks))

		// Calculate start and end indices (inclusive).
		var firstUnlockIndex uint64 = 13 // All vesting schedules start regular unlocks at month 13.
		var lastUnlockIndex uint64 = firstUnlockIndex + numUnlocks - 1

		// Calculate the unlock at each block height.
		for unlockIndex := firstUnlockIndex; unlockIndex <= lastUnlockIndex; unlockIndex++ {
			account.BalanceSchedule[unlockIndex*params.BlocksPerMonth] = quaiPerUnlock
			totalDistributed.Add(totalDistributed, quaiPerUnlock)
		}

		// Calculate total added vs expected total. Add rounding balance to final unlock.
		roundingDifference := new(big.Int).Sub(account.TotalBalance, totalDistributed)
		lastUnlockBlock := lastUnlockIndex * params.BlocksPerMonth
		account.BalanceSchedule[lastUnlockBlock] = new(big.Int).Add(account.BalanceSchedule[lastUnlockBlock], roundingDifference)
	}

}
