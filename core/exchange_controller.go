package core

import (
	"errors"
	"math/big"
	"sort"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

func CalculateBetaFromMiningChoiceAndConversions(hc *HeaderChain, block *types.WorkObject, parentExchangeRate *big.Int, newTokenChoiceSet types.TokenChoiceSet) (*big.Int, error) {

	// Until there are tokenChoicesSetSize of miner token choices, the exchange rate is unchanged
	if block.NumberU64(common.PRIME_CTX) < params.ControllerKickInBlock+types.C_tokenChoiceSetSize {
		return params.ExchangeRate, nil
	}

	var totalQiChoices uint64 = 0
	var totalQuaiChoices uint64 = 0

	tokenChoicesSet := make([]types.TokenChoices, 0)
	for _, tokenChoices := range newTokenChoiceSet {
		totalQuaiChoices += tokenChoices.Quai
		totalQiChoices += tokenChoices.Qi
		tokenChoicesSet = append(tokenChoicesSet, tokenChoices)
	}

	sort.Slice(tokenChoicesSet, func(i, j int) bool {
		return tokenChoicesSet[i].Diff.Cmp(tokenChoicesSet[j].Diff) < 0
	})

	bestScore := 0
	bestDiff := big.NewInt(0)

	var left_zeros, right_zeros, left_ones, right_ones int = 0, 0, 0, 0
	// once the tokenchoices set is sorted the difficulty values are looked from
	// smallest to the largest. The goal of this algorithm is to find the difficulty point
	// at which the number of choices of Qi and Quai are equal
	for i, choice := range tokenChoicesSet {
		left_zeros = left_zeros + int(choice.Quai)
		right_zeros = int(totalQuaiChoices) - left_zeros

		left_ones = left_ones + int(choice.Qi)
		right_ones = int(totalQiChoices) - left_ones

		score := left_zeros - right_zeros + right_ones - left_ones
		if i == 0 {
			bestDiff = new(big.Int).Set(choice.Diff)
			bestScore = score
		} else {
			if score > bestScore {
				bestScore = score
				bestDiff = new(big.Int).Set(choice.Diff)
			}
		}
	}

	// Firstly calculated the new beta from the best diff calculated from the previous step
	// Since, -B0/B1 = diff/log(diff), B1 is set to 1
	newBeta0 := new(big.Float).Quo(new(big.Float).SetInt(bestDiff), new(big.Float).SetInt(common.LogBig(bestDiff)))
	newBeta0 = new(big.Float).Mul(newBeta0, big.NewFloat(-1))

	// convert the beta values into the big numbers so that in the exchange rate
	// computation
	bigBeta0 := new(big.Float).Mul(newBeta0, new(big.Float).SetInt(common.Big2e64))
	bigBeta0Int, _ := bigBeta0.Int(nil)

	parent := hc.GetBlockByHash(block.ParentHash(common.PRIME_CTX))
	if parent == nil {
		return nil, errors.New("parent cannot be found")
	}

	minerDifficulty := hc.ComputeMinerDifficulty(parent)
	// If parent is genesis, there is nothing to train
	exchangeRate := misc.CalculateKQuai(block, parentExchangeRate, minerDifficulty, bigBeta0Int)

	return exchangeRate, nil
}

// CalculateTokenChoicesSet reads the block token choices set and adds in the
// choices generated in the current block
func CalculateTokenChoicesSet(hc *HeaderChain, block, parent *types.WorkObject, exchangeRate *big.Int, etxs types.Transactions, actualConversionAmountInHash, realizedConversionAmountInHash *big.Int, minerDifficulty *big.Int) (types.TokenChoiceSet, error) {
	// If the parent is genesis return an empty set
	if block.Hash() == hc.config.DefaultGenesisHash {
		return types.NewTokenChoiceSet(), nil
	}

	var parentTokenChoicesSet *types.TokenChoiceSet
	// Look up prior tokenChoiceSet and update
	if block.NumberU64(common.PRIME_CTX) == params.ControllerKickInBlock {
		emptyTokenChoicesSet := types.NewTokenChoiceSet()
		parentTokenChoicesSet = &emptyTokenChoicesSet
	} else {
		parentTokenChoicesSet = rawdb.ReadTokenChoicesSet(hc.headerDb, block.ParentHash(common.PRIME_CTX))
		if parentTokenChoicesSet == nil {
			return types.TokenChoiceSet{}, errors.New("cannot find the token choice set for the parent hash")
		}
	}

	tokenChoices := types.TokenChoices{Quai: 0, Qi: 0, Diff: minerDifficulty}

	for _, tx := range etxs {
		if types.IsCoinBaseTx(tx) {
			if tx.To().IsInQiLedgerScope() {
				tokenChoices.Qi++
			} else if tx.To().IsInQuaiLedgerScope() {
				tokenChoices.Quai++
			}
		} else if types.IsConversionTx(tx) {
			// Here the parents exchange rate is used to calculate the
			// conversion value because the blocks exchange rate is yet to be
			// calculated
			if tx.To().IsInQiLedgerScope() {
				tokenChoices.Qi += NormalizeConversionValueToBlock(parent, exchangeRate, tx.Value(), true)
			} else if tx.To().IsInQuaiLedgerScope() {
				tokenChoices.Quai += NormalizeConversionValueToBlock(parent, exchangeRate, tx.Value(), false)
			}
		}
	}

	// Depending on the number of the Quai/Qi choices the diff value can be moved
	// to the right or left
	if realizedConversionAmountInHash.Cmp(common.Big0) != 0 && actualConversionAmountInHash.Cmp(common.Big0) != 0 {
		if tokenChoices.Qi > tokenChoices.Quai {
			tokenChoices.Diff = new(big.Int).Mul(tokenChoices.Diff, realizedConversionAmountInHash)
			tokenChoices.Diff = new(big.Int).Div(tokenChoices.Diff, actualConversionAmountInHash)
		} else {
			tokenChoices.Diff = new(big.Int).Mul(tokenChoices.Diff, actualConversionAmountInHash)
			tokenChoices.Diff = new(big.Int).Div(tokenChoices.Diff, realizedConversionAmountInHash)
		}
	}

	// If the tokenChoices diff is zero, which should not happen, set it to 1
	if tokenChoices.Diff.Cmp(common.Big0) == 0 {
		tokenChoices.Diff = new(big.Int).SetInt64(1)
	}

	newTokenChoiceSet := types.NewTokenChoiceSet()

	// Until block number 100 is reached, we need to just accumulate to the
	// set and then after block 100 we trim and add the new element
	if block.NumberU64(common.PRIME_CTX) <= params.ControllerKickInBlock+types.C_tokenChoiceSetSize {
		if hc.IsGenesisHash(block.ParentHash(common.PRIME_CTX)) || block.NumberU64(common.PRIME_CTX) == params.ControllerKickInBlock { // parent is genesis
			newTokenChoiceSet[0] = tokenChoices
		} else {
			// go through the parent token choice set and copy it to the new
			// token choice set
			for i, prevTokenChoices := range *parentTokenChoicesSet {
				// TODO: can cut this short using parent Number
				newTokenChoiceSet[i] = prevTokenChoices
			}
			// add the elements from the current block at the end
			newTokenChoiceSet[block.NumberU64(common.PRIME_CTX)-params.ControllerKickInBlock-1] = tokenChoices
		}
	} else {
		// Once block 100 is reached, the first element in the token set has
		// to be discarded and the current block elements have to appended
		// at the end
		for i, prevTokenChoices := range *parentTokenChoicesSet {
			if i > 0 {
				newTokenChoiceSet[i-1] = types.TokenChoices{Quai: prevTokenChoices.Quai, Qi: prevTokenChoices.Qi, Diff: new(big.Int).Set(prevTokenChoices.Diff)}
			}
		}
		// Last element is set to the current block choices
		newTokenChoiceSet[types.C_tokenChoiceSetSize-1] = tokenChoices
	}

	return newTokenChoiceSet, nil
}

func NormalizeConversionValueToBlock(block *types.WorkObject, exchangeRate *big.Int, value *big.Int, chooseQi bool) uint64 {
	var reward *big.Int
	if chooseQi {
		reward = misc.CalculateQiReward(block.WorkObjectHeader(), block.MinerDifficulty())
	} else {
		reward = misc.CalculateQuaiReward(block.MinerDifficulty(), exchangeRate)
	}

	numBlocks := int(new(big.Int).Quo(value, reward).Uint64())
	return uint64(numBlocks)
}
