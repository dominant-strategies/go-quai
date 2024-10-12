package core

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/logistic"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
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

// CalculateExchangeRate takes in the parent block and the etxs generated in the
// current block and calculates the new exchange rate
func CalculateExchangeRate(hc *HeaderChain, block *types.WorkObject, newTokenChoiceSet types.TokenChoiceSet) (*big.Int, *big.Float, *big.Float, error) {

	// Do the regression to calculate new betas
	diff, tokenChoice := SerializeTokenChoiceSet(newTokenChoiceSet)
	var exchangeRate *big.Int

	// Read the parents beta values
	betas := rawdb.ReadBetas(hc.headerDb, block.Hash())
	if betas == nil {
		return nil, nil, nil, errors.New("could not find the betas stored for parent hash")
	}
	if len(tokenChoice) != 0 {
		r := logistic.NewLogisticRegression(betas.Beta0(), betas.Beta1())
		// If parent is genesis, there is nothing to train
		exchangeRate = misc.CalculateKQuai(block, r.BigBeta0(), r.BigBeta1())

		r.Train(diff, tokenChoice)

		// Plotting for testing purpose
		// xFloat := make([]float64, len(diff))
		// for i, x := range diff {
		// 	xFloat[i] = float64(x.Uint64())
		// }
		// yFloat := make([]float64, len(tokenChoice))
		// for i, y := range tokenChoice {
		// 	yFloat[i] = float64(y.Uint64())
		// }
		// r.PlotSigmoid(xFloat, yFloat, block.NumberU64(common.ZONE_CTX))

		return exchangeRate, r.Beta0(), r.Beta1(), nil
	}

	return nil, nil, nil, errors.New("length of token choice cannot be zero")
}

// CalculateTokenChoicesSet reads the block token choices set and adds in the
// choices generated in the current block
func CalculateTokenChoicesSet(hc *HeaderChain, block *types.WorkObject, etxs types.Transactions) (types.TokenChoiceSet, error) {
	// If the parent is genesis return an empty set
	if block.Hash() == hc.config.DefaultGenesisHash {
		return types.TokenChoiceSet{}, nil
	}

	// Look up prior tokenChoiceSet and update
	parentTokenChoicesSet := rawdb.ReadTokenChoicesSet(hc.headerDb, block.ParentHash(common.ZONE_CTX))
	if parentTokenChoicesSet == nil {
		return types.TokenChoiceSet{}, errors.New("cannot find the token choice set for the parent hash")
	}

	tokenChoices := make(map[string]struct{ Quai, Qi int })

	diff := block.Difficulty()
	for _, tx := range etxs {
		if types.IsCoinBaseTx(tx) {

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
		} else if types.IsConversionTx(tx) {
			// Convert diff (big.Int) to a string key
			diffKey := diff.String()

			if entry, exists := tokenChoices[diffKey]; exists {
				if tx.ETXSender().IsInQiLedgerScope() {
					entry.Quai += NormalizeConversionValueToBlock(block, tx.Value(), false)
				} else if tx.ETXSender().IsInQuaiLedgerScope() {
					entry.Qi += NormalizeConversionValueToBlock(block, tx.Value(), true)
				}
				tokenChoices[diffKey] = entry
			} else {
				var quai, qi int
				if tx.ETXSender().IsInQiLedgerScope() {
					quai += NormalizeConversionValueToBlock(block, tx.Value(), false)
				} else if tx.ETXSender().IsInQuaiLedgerScope() {
					qi += NormalizeConversionValueToBlock(block, tx.Value(), true)
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

	// Until block number 100 is reached, we need to just accumulate to the
	// set and then after block 100 we trim and add the new element
	if block.NumberU64(common.ZONE_CTX) <= types.C_tokenChoiceSetSize {
		if hc.IsGenesisHash(block.ParentHash(common.ZONE_CTX)) { // parent is genesis
			newTokenChoiceSet[0] = tokenChoicesSlice
		} else {
			// go through the parent token choice set and copy it to the new
			// token choice set
			for i, tokenChoices := range *parentTokenChoicesSet {
				// TODO: can cut this short using parent Number
				newTokenChoiceSet[i] = tokenChoices
			}
			// add the elements from the current block at the end
			newTokenChoiceSet[block.NumberU64(common.ZONE_CTX)-1] = tokenChoicesSlice
		}
	} else {
		// Once block 100 is reached, the first element in the token set has
		// to be discarded and the current block elements have to appended
		// at the end
		for i, tokenChoices := range *parentTokenChoicesSet {
			if i > 0 {
				newTokenChoiceSet[i-1] = tokenChoices
			}
		}
		// Last element is set to the current block choices
		newTokenChoiceSet[types.C_tokenChoiceSetSize-1] = tokenChoicesSlice
	}

	return newTokenChoiceSet, nil
}

func NormalizeConversionValueToBlock(block *types.WorkObject, value *big.Int, chooseQi bool) int {
	var reward *big.Int
	if chooseQi {
		reward = misc.CalculateQiReward(block.WorkObjectHeader())
	} else {
		reward = misc.CalculateQuaiReward(block)
	}

	numBlocks := int(new(big.Int).Quo(value, reward).Int64())
	fmt.Printf("numBlocks: %v\n", numBlocks)
	return numBlocks
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
