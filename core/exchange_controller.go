package core

import (
	"errors"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
)

// Function to convert the map to a slice of TokenChoice structs
func ConvertMapToSlice(tokenChoicesMap map[string]struct{ Quai, Qi int }) []types.TokenChoices {
	var result []types.TokenChoices

	for diffStr, entry := range tokenChoicesMap {
		// Convert the diff string back to *big.Int
		diff := new(big.Int)
		diff.SetString(diffStr, 10) // Assumes the string was created using big.Int.String()

		// Append to the result slice
		result = append(result, types.TokenChoices{
			Quai: uint(entry.Quai),
			Qi:   uint(entry.Qi),
			Diff: diff,
		})
	}

	return result
}

func CalculateTokenChoicesSet(hc *HeaderChain, block *types.WorkObject) (types.TokenChoiceSet, error) {

	// Look up prior tokenChoiceSet and update
	blockTokenChoicesSet := rawdb.ReadTokenChoicesSet(hc.headerDb, block.Hash())
	if blockTokenChoicesSet == nil {
		// If the block is genesis return an empty set
		if block.Hash() == hc.config.DefaultGenesisHash {
			return types.TokenChoiceSet{}, nil
		}

		var subRollup types.Transactions
		rollup, exists := hc.subRollupCache.Peek(block.Hash())
		if exists && rollup != nil {
			subRollup = rollup
			hc.logger.WithFields(log.Fields{
				"Hash": block.Hash(),
				"len":  len(subRollup),
			}).Debug("Found the rollup in cache")
		} else {
			subRollup, err := hc.CollectSubRollup(block)
			if err != nil {
				return types.TokenChoiceSet{}, err
			}
			hc.subRollupCache.Add(block.Hash(), subRollup)
		}
		tokenChoices := make(map[string]struct{ Quai, Qi int })

		for _, tx := range subRollup {
			if types.IsCoinBaseTx(tx) {
				_, _, diff, err := tx.DecodeEtxData()
				if err != nil {
					return types.TokenChoiceSet{}, err
				}
				// Convert diff (big.Int) to a string key
				diffKey := diff.String()

				if entry, exists := tokenChoices[diffKey]; exists {
					if tx.ETXSender().IsInQiLedgerScope() {
						entry.Qi++
					} else if tx.ETXSender().IsInQuaiLedgerScope() {
						entry.Quai++
					}
					tokenChoices[diffKey] = entry
				} else {
					var quai, qi int
					if tx.ETXSender().IsInQiLedgerScope() {
						qi = 1
					} else if tx.ETXSender().IsInQuaiLedgerScope() {
						quai = 1
					}

					tokenChoices[diffKey] = struct{ Quai, Qi int }{
						Quai: quai,
						Qi:   qi,
					}
				}

			}
		}

		tokenChoicesSlice := ConvertMapToSlice(tokenChoices)

		newTokenChoiceSet := types.NewTokenChoiceSet()

		var parentTokenChoiceSet *types.TokenChoiceSet
		// read the parents token choice set
		if block.ParentHash(common.PRIME_CTX) != hc.config.DefaultGenesisHash {
			parentTokenChoiceSet = rawdb.ReadTokenChoicesSet(hc.headerDb, block.ParentHash(common.PRIME_CTX))
		}

		// Until block number 100 is reached, we need to just accumulate to the
		// set and then after block 100 we trim and add the new element
		if block.NumberU64(common.PRIME_CTX) <= types.C_tokenChoiceSetSize {
			if parentTokenChoiceSet == nil { // parent is genesis
				newTokenChoiceSet[0] = tokenChoicesSlice
			} else {
				// go through the parent token choice set and copy it to the new
				// token choice set
				for i, tokenChoices := range *parentTokenChoiceSet {
					newTokenChoiceSet[i] = tokenChoices
				}
				// add the elements from the current block at the end
				newTokenChoiceSet[block.NumberU64(common.PRIME_CTX)-1] = tokenChoicesSlice
			}
		} else {
			// Once block 100 is reached, the first element in the token set has
			// to be discarded and the current block elements have to appended
			// at the end
			for i, tokenChoices := range *parentTokenChoiceSet {
				if i > 0 {
					newTokenChoiceSet[i-1] = tokenChoices
				}
			}
			// Last element is set to the current block choices
			newTokenChoiceSet[types.C_tokenChoiceSetSize-1] = tokenChoicesSlice
		}
		rawdb.WriteTokenChoicesSet(hc.headerDb, block.Hash(), &newTokenChoiceSet)

	} else {
		return *blockTokenChoicesSet, nil
	}

	return types.TokenChoiceSet{}, errors.New("Failed to calculate token choices set")
}

// serialize tokenChoiceSet
func SerializeTokenChoiceSet(tokenChoiceSet types.TokenChoiceSet) ([]*big.Int, []*big.Int) {
	var diff []*big.Int
	var token []*big.Int

	for _, tokenChoices := range tokenChoiceSet {
		for _, choice := range tokenChoices {
			for i := 0; i < int(choice.Quai); i++ {
				diff = append(diff, choice.Diff)
				token = append(token, big.NewInt(0))
			}
			for i := 0; i < int(choice.Qi); i++ {
				diff = append(diff, choice.Diff)
				token = append(token, big.NewInt(1))
			}
		}
	}

	return diff, token
}
