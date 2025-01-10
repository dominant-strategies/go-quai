package state

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/core/genallocs"
	"github.com/dominant-strategies/go-quai/params"
)

// Will go through the balance schedule and add each one to the account's balance in state.
func (state *StateDB) AddLockedBalances(blockNum *big.Int, genesisAccounts []genallocs.GenesisAccount) error {
	uintBlockNum := blockNum.Uint64() - 1 // Off by 1 to include in correct block.
	// Check if this block is a monthly unlock.
	if uintBlockNum%params.BlocksPerMonth == 0 {
		// Rotate through the accounts and apply the unlocks valid for this month.
		for _, account := range genesisAccounts {
			accountAddr, err := account.Address.InternalAddress()
			if err != nil {
				return err
			}
			state.AddBalance(accountAddr, account.BalanceSchedule[uintBlockNum])
		}
	}
	return nil
}
