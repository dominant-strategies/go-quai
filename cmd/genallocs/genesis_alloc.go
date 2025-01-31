package genallocs

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

// GenesisAccount is an account in the state of the genesis block.
type GenesisAccount struct {
	UnlockSchedule  int                                      `json:"Unlock Schedule"`
	Address         common.Address                           `json:"Address"`
	TotalBalance    *big.Int                                 `json:"Amount"`
	VestedBalance   *big.Int                                 `json:"Vested"`
	BalanceSchedule *orderedmap.OrderedMap[uint64, *big.Int] `json:"BalanceSchedule"` // Map of blockNumber->balanceUnlocked (at that block).
}

// Defines the unlockSchedule parameters.
// Some unlockSchedules have a lumpSum payment, some at TGE, some at 1 year.
// All unlockSchedules begin regular unlocks after 1 year.
type unlockSchedule struct {
	unlockDuration    int    // Total unlocking duration in years. The first year cliff is not part of unlocking.
	lumpSumPercentage uint64 // One-time percentage unlocked.
	lumpSumMonth      uint64 // Number of months before lump sum payment.
	unlockMonthStart  uint64 // Month of first regular unlock.
}

var unlockSchedules = [4]unlockSchedule{
	{
		// schedule0
		// Immediate unlock: 100% @ Month 0
		unlockDuration:    0,
		lumpSumPercentage: 100,
		lumpSumMonth:      0,
	},

	{
		// schedule1
		// Unlock duration: 5 years
		// Lump Sum: 2 % @ Month 0
		// Unlock Start: Month 6
		unlockDuration:    5,
		lumpSumPercentage: 2,
		lumpSumMonth:      0,
		unlockMonthStart:  6,
	},

	{
		// schedule2
		// Unlock duration: 3 years
		// Lump Sum: 25% @ Month 0
		// Unlock Start: Month 13
		unlockDuration:    3,
		lumpSumPercentage: 25,
		lumpSumMonth:      0,
		unlockMonthStart:  13,
	},

	{
		// schedule3
		// Unlock duration: 3 years
		// Lump Sum: 25% @ Month 12
		// Unlock Start: Month 13
		unlockDuration:    3,
		lumpSumPercentage: 25,
		lumpSumMonth:      12,
		unlockMonthStart:  13,
	},
}

// Will return all the GenesisAccounts with their calculated unlock schedules
func AllocateGenesisAccounts(filename string) ([]GenesisAccount, error) {
	// Read from allocs file.
	allocs, err := readGenesisAllocs(filename)
	if err != nil {
		return nil, err
	}

	// Calculate unlocking schedules for each account.
	for i := range allocs {
		allocs[i].calculateLockedBalances()
	}
	return allocs, nil
}

// Parses the allocs JSON and populates the basic unlocking info.
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

// Will calculate the unlock heights according to the pre-defined unlocking schedule.
func (account *GenesisAccount) calculateLockedBalances() {
	account.BalanceSchedule = orderedmap.New[uint64, *big.Int]()

	unlockSchedule := unlockSchedules[account.UnlockSchedule]

	// Calculate total lump sum payment.
	lumpSumPercentage := new(big.Int).SetUint64(unlockSchedule.lumpSumPercentage)
	lumpSumAmount := new(big.Int).Mul(account.TotalBalance, lumpSumPercentage)
	lumpSumAmount.Div(lumpSumAmount, common.Big100) // Divide back by 100 to undo percentage.

	// Verify that lumpSum payment is not more than the total vested allocation.
	if lumpSumAmount.Cmp(account.VestedBalance) > 0 {
		lumpSumAmount = account.VestedBalance
	}

	lumpSumIndex := unlockSchedule.lumpSumMonth
	account.BalanceSchedule.Set(lumpSumIndex*params.BlocksPerMonth, lumpSumAmount)

	// Accumulate total rewards.
	var totalDistributed = new(big.Int)
	totalDistributed.Add(totalDistributed, lumpSumAmount)

	if unlockSchedule.unlockDuration != 0 {
		// Calculate number of unlocks.
		numUnlocks := uint64((unlockSchedule.unlockDuration) * 12) // Total months that unlock.

		// Calculate amount per unlock.
		quaiPerUnlock := new(big.Int).Sub(account.VestedBalance, lumpSumAmount)
		quaiPerUnlock.Div(quaiPerUnlock, new(big.Int).SetUint64(numUnlocks))

		// Calculate start and end indices (inclusive).
		var firstUnlockIndex uint64 = unlockSchedule.unlockMonthStart
		var lastUnlockIndex uint64 = firstUnlockIndex + numUnlocks - 1

		// Calculate the unlock at each block height.
		for unlockIndex := firstUnlockIndex; unlockIndex <= lastUnlockIndex; unlockIndex++ {
			account.BalanceSchedule.Set(unlockIndex*params.BlocksPerMonth, quaiPerUnlock)
			totalDistributed.Add(totalDistributed, quaiPerUnlock)
		}

		// Calculate total added vs expected total. Add rounding balance to final unlock.
		roundingDifference := new(big.Int).Sub(account.TotalBalance, totalDistributed)
		lastUnlockBlock := lastUnlockIndex * params.BlocksPerMonth
		account.BalanceSchedule.Set(lastUnlockBlock, new(big.Int).Add(quaiPerUnlock, roundingDifference))
	}

}
