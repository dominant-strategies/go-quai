package state

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

// Will go through the balance schedule and add each one to the account's balance in state.
func (state *StateDB) AddLockedBalances(header *types.WorkObject, genesisAccounts []params.GenesisAccount, forfeitureAddresses map[common.AddressBytes]bool, log *log.Logger) error {
	blockNum := header.Number(common.ZONE_CTX)
	uintBlockNum := blockNum.Uint64()
	// Check if this block is a monthly unlock.
	if uintBlockNum%params.BlocksPerMonth == 0 || uintBlockNum == 1 {
		if uintBlockNum == 1 {
			uintBlockNum = 0
		}
		// Rotate through the accounts and apply the unlocks valid for this month.
		accountsAdded := 0
		for _, account := range genesisAccounts {
			// Skip forfeiture addresses
			if header.PrimeTerminusNumber().Uint64() >= params.SingularityForkBlock &&
				forfeitureAddresses[account.Address.Bytes20()] {
				continue
			}
			accountAddr, err := account.Address.InternalAddress()
			if err != nil {
				return err
			}
			if balance, ok := account.BalanceSchedule.Get(uintBlockNum); ok && balance != nil {
				state.AddBalance(accountAddr, balance)
				accountsAdded += 1
			}
		}
		log.WithField("accountsAdded", accountsAdded).Debug("Allocated genesis accounts")
	}
	return nil
}
