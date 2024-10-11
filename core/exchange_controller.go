package core

import (
	"errors"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/logistic"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

// CalculateExchangeRate takes in the parent block and the etxs generated in the
// current block and calculates the new exchange rate
func CalculateExchangeRate(hc *HeaderChain, block *types.WorkObject, newTokenChoiceSet types.TokenChoiceSet) (*big.Int, *big.Float, *big.Float, error) {

	// Read the parents beta values
	betas := rawdb.ReadBetas(hc.headerDb, block.Hash())
	if betas == nil {
		return nil, nil, nil, errors.New("could not find the betas stored for parent hash")
	}

	if block.NumberU64(common.ZONE_CTX) < types.C_tokenChoiceSetSize {
		return params.ExchangeRate, betas.Beta0(), betas.Beta1(), nil
	}

	// Do the regression to calculate new betas
	diff, tokenChoice := SerializeTokenChoiceSet(newTokenChoiceSet)
	var exchangeRate *big.Int

	if len(tokenChoice) != 0 {
		r := logistic.NewLogisticRegression(betas.Beta0(), betas.Beta1())
		// If parent is genesis, there is nothing to train
		exchangeRate = misc.CalculateKQuai(block, r.BigBeta0(), r.BigBeta1())

		r.Train(diff, tokenChoice)

		return exchangeRate, r.Beta0(), r.Beta1(), nil
	} else {
		return block.ExchangeRate(), betas.Beta0(), betas.Beta1(), nil
	}
}

// CalculateTokenChoicesSet reads the block token choices set and adds in the
// choices generated in the current block
func CalculateTokenChoicesSet(hc *HeaderChain, block *types.WorkObject, etxs types.Transactions) (types.TokenChoiceSet, error) {
	// If the parent is genesis return an empty set
	if block.Hash() == hc.config.DefaultGenesisHash {
		return types.NewTokenChoiceSet(), nil
	}

	// Look up prior tokenChoiceSet and update
	parentTokenChoicesSet := rawdb.ReadTokenChoicesSet(hc.headerDb, block.ParentHash(common.ZONE_CTX))
	if parentTokenChoicesSet == nil {
		return types.TokenChoiceSet{}, errors.New("cannot find the token choice set for the parent hash")
	}

	tokenChoices := types.TokenChoices{Quai: 0, Qi: 0, Diff: block.Difficulty()}

	for _, tx := range etxs {
		if types.IsCoinBaseTx(tx) {
			if tx.To().IsInQiLedgerScope() {
				tokenChoices.Qi++
			} else if tx.To().IsInQuaiLedgerScope() {
				tokenChoices.Quai++
			}
		} else if types.IsConversionTx(tx) {
			if tx.To().IsInQiLedgerScope() {
				tokenChoices.Qi += NormalizeConversionValueToBlock(block, tx.Value(), true)
			} else if tx.To().IsInQuaiLedgerScope() {
				tokenChoices.Quai += NormalizeConversionValueToBlock(block, tx.Value(), false)
			}
		}
	}

	newTokenChoiceSet := types.NewTokenChoiceSet()

	// Until block number 100 is reached, we need to just accumulate to the
	// set and then after block 100 we trim and add the new element
	if block.NumberU64(common.ZONE_CTX) <= types.C_tokenChoiceSetSize {
		if hc.IsGenesisHash(block.ParentHash(common.ZONE_CTX)) { // parent is genesis
			newTokenChoiceSet[0] = tokenChoices
		} else {
			// go through the parent token choice set and copy it to the new
			// token choice set
			for i, prevTokenChoices := range *parentTokenChoicesSet {
				// TODO: can cut this short using parent Number
				newTokenChoiceSet[i] = prevTokenChoices
			}
			// add the elements from the current block at the end
			newTokenChoiceSet[block.NumberU64(common.ZONE_CTX)-1] = tokenChoices
		}
	} else {
		// Once block 100 is reached, the first element in the token set has
		// to be discarded and the current block elements have to appended
		// at the end
		for i, prevTokenChoices := range *parentTokenChoicesSet {
			if i > 0 {
				newTokenChoiceSet[i-1] = prevTokenChoices
			}
		}
		// Last element is set to the current block choices
		newTokenChoiceSet[types.C_tokenChoiceSetSize-1] = tokenChoices
	}

	return newTokenChoiceSet, nil
}

func NormalizeConversionValueToBlock(block *types.WorkObject, value *big.Int, chooseQi bool) uint64 {
	var reward *big.Int
	if chooseQi {
		reward = misc.CalculateQiReward(block.WorkObjectHeader())
	} else {
		reward = misc.CalculateQuaiReward(block)
	}

	numBlocks := int(new(big.Int).Quo(value, reward).Uint64())
	return uint64(numBlocks)
}

// serialize tokenChoiceSet
func SerializeTokenChoiceSet(tokenChoiceSet types.TokenChoiceSet) ([]*big.Int, []*big.Int) {
	var diff []*big.Int
	var token []*big.Int

	for _, tokenChoices := range tokenChoiceSet {
		for i := 0; i < int(tokenChoices.Quai); i++ {
			diff = append(diff, tokenChoices.Diff)
			token = append(token, big.NewInt(0))
		}
		for i := 0; i < int(tokenChoices.Qi); i++ {
			diff = append(diff, tokenChoices.Diff)
			token = append(token, big.NewInt(1))
		}
	}

	return diff, token
}
