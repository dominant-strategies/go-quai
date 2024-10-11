package core

import (
	"errors"
	"math/big"

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

func CalculateTokenChoicesSet(hc *HeaderChain, parent *types.WorkObject) (*types.TokenChoiceSet, error) {

	// Look up prior tokenChoiceSet and update
	parentTokenChoiceSet := rawdb.ReadTokenChoicesSet(hc.headerDb, parent.Hash())
	if parentTokenChoiceSet == nil {
		var subRollup types.Transactions
		rollup, exists := hc.subRollupCache.Peek(parent.Hash())
		if exists && rollup != nil {
			subRollup = rollup
			hc.logger.WithFields(log.Fields{
				"Hash": parent.Hash(),
				"len":  len(subRollup),
			}).Debug("Found the rollup in cache")
		} else {
			subRollup, err := hc.CollectSubRollup(parent)
			if err != nil {
				return nil, err
			}
			hc.subRollupCache.Add(parent.Hash(), subRollup)
		}
		tokenChoices := make(map[string]struct{ Quai, Qi int })

		for _, tx := range subRollup {
			if types.IsCoinBaseTx(tx) {
				_, _, diff, err := tx.DecodeEtxData()
				if err != nil {
					return nil, err
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
		for i, tokenChoices := range parentTokenChoiceSet {
			if i > 0 {
				newTokenChoiceSet[i-1] = tokenChoices
			}
		}
		newTokenChoiceSet[len(newTokenChoiceSet)-1] = tokenChoicesSlice
		rawdb.WriteTokenChoicesSet(hc.headerDb, parent.Hash(), &newTokenChoiceSet)

	} else {
		return parentTokenChoiceSet, nil
	}

	return nil, errors.New("Failed to calculate token choices set")
}

// serialize tokenChoiceSet
