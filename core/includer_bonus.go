package core

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
)

var includerBonusTagPrefix = []byte("quai-includer-bonus")

// appendIncluderBonusEtxs appends one coinbase ETX per block in includers that
// included shares at targetBlock's height, paying a flat IncluderBonusNumerator /
// IncluderBonusDenominator of the per-slot reward for each included share. It
// appends nothing before IncluderBonusForkBlock. includers is the caller's
// targetBlocks slice (heights n-1 down to n-depth); targetBlock is
// includers[len(includers)-1].
func appendIncluderBonusEtxs(etxs []*types.Transaction, targetBlock *types.WorkObject, includers []*types.WorkObject,
	quaiRewardPerShare, qiRewardPerShare *big.Int, parentHash common.Hash, location common.Location) []*types.Transaction {
	if targetBlock.PrimeTerminusNumber().Uint64() < params.IncluderBonusForkBlock {
		return etxs
	}

	targetNumber := targetBlock.NumberU64(common.ZONE_CTX)
	targetHash := targetBlock.Hash()
	num := new(big.Int).SetUint64(params.IncluderBonusNumerator)
	den := new(big.Int).SetUint64(params.IncluderBonusDenominator)
	quaiPerShare := new(big.Int).Div(new(big.Int).Mul(quaiRewardPerShare, num), den)
	qiPerShare := new(big.Int).Div(new(big.Int).Mul(qiRewardPerShare, num), den)

	for _, includer := range includers {
		count := int64(0)
		for _, share := range includer.Uncles() {
			if share.NumberU64() != targetNumber {
				continue
			}
			if _, err := share.PrimaryCoinbase().InternalAddress(); err != nil {
				continue
			}
			count++
		}

		// Header validation already rejects blocks whose coinbase is not internal.
		coinbase := includer.PrimaryCoinbase()
		perShare, originHash := quaiPerShare, common.SetBlockHashForQuai(parentHash, location)
		if coinbase.IsInQiLedgerScope() {
			perShare, originHash = qiPerShare, common.SetBlockHashForQi(parentHash, location)
		}
		total := new(big.Int).Mul(perShare, big.NewInt(count))
		if total.Sign() == 0 {
			continue
		}

		// Trailing 32 bytes must not equal any share/block hash, so the
		// workshare-hash payout index and quai_getCoinbaseTxForWorkShareHash
		// keep resolving shares to their own payout.
		tag := crypto.Keccak256Hash(includerBonusTagPrefix, includer.Hash().Bytes(), targetHash.Bytes())
		data := make([]byte, 0, len(includer.Data())+common.HashLength)
		data = append(data, includer.Data()...)
		data = append(data, tag.Bytes()...)

		etxs = append(etxs, types.NewTx(&types.ExternalTx{
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
	return etxs
}
