package core

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
)

var includerBonusTagPrefix = []byte("quai-includer-bonus")

// appendIncluderBonusEtx appends one coinbase ETX paying targetBlock's miner a
// flat IncluderBonusNumerator / IncluderBonusDenominator of the per-slot reward
// for each workshare targetBlock included. It runs when targetBlock itself is
// paid out; a block only includes shares from its own height and the three
// below it, so every one of them has been paid by then. It appends nothing
// before IncluderBonusForkBlock.
func appendIncluderBonusEtx(etxs []*types.Transaction, targetBlock *types.WorkObject,
	quaiRewardPerShare, qiRewardPerShare *big.Int, parentHash common.Hash, location common.Location) []*types.Transaction {
	if targetBlock.PrimeTerminusNumber().Uint64() < params.IncluderBonusForkBlock {
		return etxs
	}

	// Same filter the payout loop applies to the shares it pays.
	count := int64(0)
	for _, share := range targetBlock.Uncles() {
		if _, err := share.PrimaryCoinbase().InternalAddress(); err == nil {
			count++
		}
	}

	// Header validation already rejects blocks whose coinbase is not internal.
	coinbase := targetBlock.PrimaryCoinbase()
	base, originHash := quaiRewardPerShare, common.SetBlockHashForQuai(parentHash, location)
	if coinbase.IsInQiLedgerScope() {
		base, originHash = qiRewardPerShare, common.SetBlockHashForQi(parentHash, location)
	}
	perShare := new(big.Int).Mul(base, new(big.Int).SetUint64(params.IncluderBonusNumerator))
	perShare.Div(perShare, new(big.Int).SetUint64(params.IncluderBonusDenominator))
	total := new(big.Int).Mul(perShare, big.NewInt(count))
	if total.Sign() == 0 {
		return etxs
	}

	// Trailing 32 bytes must not equal any share/block hash, so the
	// workshare-hash payout index and quai_getCoinbaseTxForWorkShareHash
	// keep resolving shares and blocks to their own payout.
	tag := crypto.Keccak256Hash(includerBonusTagPrefix, targetBlock.Hash().Bytes())
	data := make([]byte, 0, len(targetBlock.Data())+common.HashLength)
	data = append(data, targetBlock.Data()...)
	data = append(data, tag.Bytes()...)

	return append(etxs, types.NewTx(&types.ExternalTx{
		To:                &coinbase,
		Gas:               params.TxGas,
		Value:             total,
		EtxType:           types.CoinbaseType,
		OriginatingTxHash: originHash,
		ETXIndex:          uint16(len(etxs)),
		Sender:            coinbase,
		Data:              data,
	}))
}
