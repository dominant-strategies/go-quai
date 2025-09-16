package params

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
	orderedmap "github.com/wk8/go-ordered-map/v2"
	"lukechampine.com/blake3"
)

// GenesisAccount is an account in the state of the genesis block.
type GenesisAccount struct {
	UnlockSchedule  int                                      `json:"unlockSchedule"`
	Address         common.Address                           `json:"address"`
	Award           *big.Int                                 `json:"award"`
	Vested          *big.Int                                 `json:"vested"`
	LumpSumMonth    uint64                                   `json:"lumpSumMonth"`
	BalanceSchedule *orderedmap.OrderedMap[uint64, *big.Int] `json:"balanceSchedule"` // Map of blockNumber->balanceUnlocked (at that block).
}

// Defines the unlockSchedule parameters.
// Some unlockSchedules have a lumpSum payment, some at TGE, some at 1 year.
// All unlockSchedules begin regular unlocks after 1 year.
type unlockSchedule struct {
	unlockDuration    uint64 // Total unlocking duration in months. The first year cliff is not part of unlocking.
	lumpSumPercentage uint64 // One-time percentage unlocked.
	lumpSumMonth      uint64 // Number of months before lump sum payment.
	unlockMonthStart  uint64 // Month of first regular unlock.
}

var unlockSchedules = [4]unlockSchedule{
	{},

	{
		// schedule1
		// Lump Sum: 100% @ Variable Month
		lumpSumPercentage: 100,
	},

	{
		// schedule2
		// Unlock duration: 36 months
		// Lump Sum: 25% @ TGE
		// Unlock Start: Month 13
		unlockDuration:    36,
		lumpSumPercentage: 25,
		lumpSumMonth:      0,
		unlockMonthStart:  13,
	},

	{
		// schedule3
		// Unlock duration: 36 months
		// Lump Sum: 25% @ 1 year
		// Unlock Start: Month 7
		unlockDuration:    36,
		lumpSumPercentage: 25,
		lumpSumMonth:      12,
		unlockMonthStart:  13,
	},
}

// Will return all the GenesisAccounts with their calculated unlock schedules.
// Ignores any existing values in BalanceSchedule and recalculates them.
func GenerateGenesisUnlocks(filename string) ([]GenesisAccount, error) {
	// Read from allocs file.
	// Open the JSON file
	path := filepath.Clean(filename)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	allocs, err := decodeGenesisAllocs(file)
	if err != nil {
		return nil, err
	}

	// Calculate unlocking schedules for each account.
	for i := range allocs {
		allocs[i].calculateLockedBalances()
	}
	return allocs, nil
}

// Performs verification tasks on provided unlock info.
func VerifyGenesisAllocs(filename string, expectedHash common.Hash) ([]GenesisAccount, error) {
	// Open the JSON file
	path := filepath.Clean(filename)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := blake3.New(32, nil)
	// Stream the file to the hasher to avoid holding it all in memory.
	if _, err := io.Copy(hasher, file); err != nil {
		fmt.Println("Error hashing file:", err)
		return nil, err
	}
	hash := hasher.Sum(nil)
	if !bytes.Equal(hash, expectedHash.Bytes()) {
		return nil, errors.New("invalid genesis unlocks")
	}

	// Reset file pointer to the beginning before decoding JSON.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek file: %w", err)
	}

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
	lumpSumAmount := lumpSumPercentage.Mul(account.Award, lumpSumPercentage)
	lumpSumAmount.Div(lumpSumAmount, common.Big100) // Divide back by 100 to undo percentage.

	// Verify that lumpSum payment is not more than the total vested allocation.
	if lumpSumAmount.Cmp(account.Vested) > 0 {
		lumpSumAmount = account.Vested
	}

	var lumpSumIndex uint64
	if account.UnlockSchedule == 1 {
		// If the unlockSchedule is 1, then take the lumpSumMonth from the account definition.
		lumpSumIndex = account.LumpSumMonth
	} else {
		// Otherwise, take it from the schedule definition.
		lumpSumIndex = unlockSchedule.lumpSumMonth
	}
	account.BalanceSchedule.Set(lumpSumIndex*BlocksPerMonth, lumpSumAmount)

	// Accumulate total rewards.
	var totalDistributed = new(big.Int)
	totalDistributed.Add(totalDistributed, lumpSumAmount)

	if unlockSchedule.unlockDuration != 0 {
		// 1. Divide the total allocation by the unlock period.
		unlockableBalance := new(big.Int).Sub(account.Award, lumpSumAmount)
		quaiPerUnlock := unlockableBalance.Div(unlockableBalance, new(big.Int).SetUint64(unlockSchedule.unlockDuration))

		// Calculate the number of unlocks based on the remaining vested Quai not allocated at the cliff.
		remainingBalance := new(big.Int).Sub(account.Vested, lumpSumAmount)

		// 2. Continue unlocking this amount each month until the vested amount is reached.
		// Divide the vested balance amount, by amount per unlock, to figure out how many unlocks must be unlocked.
		numUnlocks := new(big.Int).Div(remainingBalance, quaiPerUnlock)
		// Calculate start and end indices (inclusive).
		var firstUnlockIndex uint64 = unlockSchedule.unlockMonthStart
		var lastUnlockIndex uint64 = firstUnlockIndex + numUnlocks.Uint64() - 1

		// Calculate the unlock at each block height.
		for unlockIndex := firstUnlockIndex; unlockIndex <= lastUnlockIndex; unlockIndex++ {
			account.BalanceSchedule.Set(unlockIndex*BlocksPerMonth, quaiPerUnlock)
			totalDistributed.Add(totalDistributed, quaiPerUnlock)
		}

		// Calculate total added vs expected total. Add rounding balance to final unlock.
		roundingDifference := new(big.Int).Sub(account.Vested, totalDistributed)
		lastUnlockBlock := lastUnlockIndex * BlocksPerMonth
		finalUnlockAmount, ok := account.BalanceSchedule.Get(lastUnlockBlock)
		if !ok {
			log.Global.WithField("lastUnlockBlock", lastUnlockBlock).Fatal("Issue generating balance schedule")
		}
		account.BalanceSchedule.Set(lastUnlockBlock, new(big.Int).Add(finalUnlockAmount, roundingDifference))
	}
}
