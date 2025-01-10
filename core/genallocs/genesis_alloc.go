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
	TotalBalance    uint64              `json:"Amount" gencodec:"required"`
	BalanceSchedule map[uint64]*big.Int // Map of blockNumber->balanceUnlocked (at that block).
	privateKey      []byte              `json:"secretKey,omitempty"`
}

type vestingSchedule struct {
	vestDuration  uint64  // Total vesting duration in years. The first year cliff is not part of vesting.
	tgePercentage float32 // One-time percentage unlocked at TGE.
	unlockStart   int     // First month that regular unlocks start.
}

var vestingSchedules = [3]vestingSchedule{
	{
		// schedule1
		// 	Vesting duration: 5 years
		// 	TGE: 30%
		vestDuration:  5,
		tgePercentage: 0.3,
	},

	{
		// schedule2
		// 	Vesting duration: 3 years
		// 	TGE: 25%
		vestDuration:  3,
		tgePercentage: 0.25,
	},

	{
		// schedule3
		// 	Vesting duration: 3 years
		// 	TGE: 0%
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
	balanceInt := new(big.Int).SetUint64(account.TotalBalance)

	// Calculate total unlock at tge.
	tgePercentage := big.NewInt(int64(vestingSchedule.tgePercentage * 100)) // Multiply by 100 to remove need for floats.
	tgeAmount := new(big.Int).Mul(balanceInt, tgePercentage)
	tgeAmount.Div(tgeAmount, common.Big100) // Divide back by 100.
	account.BalanceSchedule[0] = tgeAmount

	// Calculate number of unlocks.
	numUnlocks := uint64((vestingSchedule.vestDuration) * 12) // Total months that unlock.

	// Calculate amount per unlock.
	quaiPerUnlock := new(big.Int).Sub(balanceInt, tgeAmount)
	quaiPerUnlock.Div(quaiPerUnlock, new(big.Int).SetUint64(numUnlocks))

	// Calculate the unlock at each block height.
	for i := uint64(0); i <= numUnlocks; i++ {
		account.BalanceSchedule[(12+i)*params.BlocksPerMonth-1] = quaiPerUnlock // Off by 1 to include in the following block.
		// account.BalanceSchedule[i] = quaiPerUnlock
	}

	// Verify the total and add back any rounding.
	totalQuai := new(big.Int).Add(tgeAmount, new(big.Int).Mul(quaiPerUnlock, new(big.Int).SetUint64(numUnlocks)))
	roundingDiff := totalQuai.Sub(balanceInt, totalQuai)

	finalUnlockHeight := (12+numUnlocks)*params.BlocksPerMonth - 1 // Off by 1 to include in the following block.
	account.BalanceSchedule[finalUnlockHeight] = new(big.Int).Add(
		account.BalanceSchedule[finalUnlockHeight],
		roundingDiff,
	)
}
