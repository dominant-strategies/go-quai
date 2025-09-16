package core

import (
	"errors"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

func CalculateBetaFromMiningChoiceAndConversions(hc *HeaderChain, block *types.WorkObject, parentExchangeRate *big.Int, newTokenChoiceSet types.TokenChoiceSet) (*big.Int, error) {

	// Until there are tokenChoicesSetSize of miner token choices, the exchange rate is unchanged
	if block.NumberU64(common.PRIME_CTX) < params.ControllerKickInBlock+params.TokenChoiceSetSize {
		return params.ExchangeRate, nil
	}

	// Apply KQuai changes based on the table
	currentBlock := block.NumberU64(common.PRIME_CTX)
	// Before the KawPow fork, the exchange rate is reduced based on the
	// KQuaiChangeTable and held for the KQuaiChangeHoldInterval
	if currentBlock < params.KawPowForkBlock {
		for _, entry := range params.KQuaiChangeTable {
			blockNumber := entry[0]
			reductionPercent := entry[1]

			// Apply the reduction at the exact block
			if currentBlock == blockNumber {
				// If the block number is the kquai reset the exchange rate back to
				// the starting exchange rate
				if currentBlock == params.KQuaiChangeBlock {
					return params.ExchangeRate, nil
				}
				exchangeRate := new(big.Int).Mul(parentExchangeRate, big.NewInt(int64(reductionPercent)))
				exchangeRate = new(big.Int).Div(exchangeRate, big.NewInt(100))
				return exchangeRate, nil
			}

			// Hold the value during the hold interval after each reduction
			if currentBlock > blockNumber && currentBlock < blockNumber+params.KQuaiChangeHoldInterval {
				exchangeRate := new(big.Int).Set(parentExchangeRate)
				return exchangeRate, nil
			}
		}
	} else {
		// Resetting the exchange rate on the fork block and hold it for the kquai change hold interval
		if currentBlock == params.KawPowForkBlock {
			return params.ExchangeRate, nil
		} else if currentBlock < params.KawPowForkBlock+params.KQuaiChangeHoldInterval {
			exchangeRate := new(big.Int).Set(parentExchangeRate)
			return exchangeRate, nil
		}
	}

	// xbStar point of diff indicating the miners/speculators choice is done by
	// taking an average of tokenChoices Difficulty which is already shifted
	// based on the current preference
	totalDiff := big.NewInt(0)
	for _, tokenChoices := range newTokenChoiceSet {
		totalDiff.Add(totalDiff, tokenChoices.Diff)
	}

	bestDiff := new(big.Int).Div(totalDiff, big.NewInt(int64(params.TokenChoiceSetSize)))

	// Firstly calculated the new beta from the best diff calculated from the previous step
	// Since, -B0/B1 = diff/log(diff), B1 is set to 1
	newBeta0OverBeta1 := new(big.Int).Div(new(big.Int).Mul(bestDiff, common.Big2e64), common.LogBig(bestDiff))

	exchangeRate := misc.CalculateKQuai(parentExchangeRate, block.MinerDifficulty(), block.NumberU64(common.PRIME_CTX), newBeta0OverBeta1)

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
	if block.NumberU64(common.PRIME_CTX) <= params.ControllerKickInBlock+1 {
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
		// Difficulty is adjusted based on relative preference of
		// miners/speculators between Quai and Qi. In the case of if Quai is
		// chosen more than Qi, the difficulty (miner difficulty) is shifted
		// downwards and in the case of Qi being greater than Quai the
		// difficulty is is shifted upwards
		// delta to shift by is calculated as
		// diffDelta = (1-alpha) * (actualAmount - realizedAmount)/actualAmount
		diffErr := new(big.Int).Sub(actualConversionAmountInHash, realizedConversionAmountInHash)
		diffErr = new(big.Int).Mul(diffErr, tokenChoices.Diff)
		diffErr = new(big.Int).Div(diffErr, actualConversionAmountInHash)
		diffErr = new(big.Int).Div(diffErr, params.TokenDiffAlpha)
		if tokenChoices.Qi > tokenChoices.Quai {
			tokenChoices.Diff = new(big.Int).Add(tokenChoices.Diff, diffErr)
		} else { // If Quai choices are more than the Qi choices, then shift the difficulty to the left
			tokenChoices.Diff = new(big.Int).Sub(tokenChoices.Diff, diffErr)
		}
	}

	// If the tokenChoices diff is zero, which should not happen, set it to 1
	if tokenChoices.Diff.Cmp(common.Big0) == 0 {
		hc.logger.Warn("Token choices diff cannot be zero as min 10% of the value is kept")
		tokenChoices.Diff = new(big.Int).SetInt64(1)
	}

	newTokenChoiceSet := types.NewTokenChoiceSet()

	// Until block number 100 is reached, we need to just accumulate to the
	// set and then after block 100 we trim and add the new element
	if block.NumberU64(common.PRIME_CTX) <= params.ControllerKickInBlock+params.TokenChoiceSetSize {
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
		newTokenChoiceSet[params.TokenChoiceSetSize-1] = tokenChoices
	}

	return newTokenChoiceSet, nil
}

func NormalizeConversionValueToBlock(block *types.WorkObject, exchangeRate *big.Int, value *big.Int, chooseQi bool) uint64 {
	var reward *big.Int
	if chooseQi {
		reward = misc.CalculateQiReward(block.WorkObjectHeader(), block.MinerDifficulty())
	} else {
		reward = misc.CalculateQuaiReward(block.WorkObjectHeader(), block.MinerDifficulty(), exchangeRate)
	}

	numBlocks := int(new(big.Int).Quo(value, reward).Uint64())
	return uint64(numBlocks)
}
