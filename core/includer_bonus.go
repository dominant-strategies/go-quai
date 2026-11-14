package core

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

// includerBonus is the flat bonus targetBlock's miner earns for the workshares
// targetBlock included: IncluderBonusNumerator / IncluderBonusDenominator of
// targetBlock's own per-slot reward for each one, whatever the share's height.
// The payout loop adds it to targetBlock's own reward when targetBlock is paid
// out; a block only includes shares from its own height and the three below
// it, so every one of them has been paid by then. Riding on the existing reward
// ETX, the bonus adds no ETX and no token-choice vote. It is zero before
// IncluderBonusForkBlock, which is checked against targetBlock.
func includerBonus(targetBlock *types.WorkObject, quaiRewardPerShare, qiRewardPerShare *big.Int) *big.Int {
	if targetBlock.PrimeTerminusNumber().Uint64() < params.IncluderBonusForkBlock {
		return new(big.Int)
	}

	// Same filter the payout loop applies to the shares it pays.
	count := int64(0)
	for _, share := range targetBlock.Uncles() {
		if _, err := share.PrimaryCoinbase().InternalAddress(); err == nil {
			count++
		}
	}

	// Same base the payout loop starts from for this coinbase, so its Qi
	// conversion before the conversion-lock fork applies to the bonus too.
	base := quaiRewardPerShare
	if targetBlock.PrimaryCoinbase().IsInQiLedgerScope() && targetBlock.PrimeTerminusNumber().Uint64() >= params.ConversionLockChangeForkBlock {
		base = qiRewardPerShare
	}
	perShare := new(big.Int).Mul(base, new(big.Int).SetUint64(params.IncluderBonusNumerator))
	perShare.Div(perShare, new(big.Int).SetUint64(params.IncluderBonusDenominator))
	return perShare.Mul(perShare, big.NewInt(count))
}
