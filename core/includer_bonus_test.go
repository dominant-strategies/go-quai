package core

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
)

const bonusT = 100 // height of the block being paid out

var (
	bonusLoc    = common.Location{0, 0}
	bonusParent = common.Hash{1}

	quaiA = bonusAddr(0x00, 0x01, 0xa1)
	quaiB = bonusAddr(0x00, 0x01, 0xa2)
	qiA   = bonusAddr(0x00, 0x80, 0xb1)
	extA  = bonusAddr(0x10, 0x01, 0xc1) // zone 1-0, not internal to zone 0-0
)

// bonusAddr builds an address whose first byte is the zone prefix and whose
// second byte selects the ledger (> 127 is Qi).
func bonusAddr(prefix, ledger, fill byte) common.Address {
	b := bytes.Repeat([]byte{fill}, common.AddressLength)
	b[0], b[1] = prefix, ledger
	return common.BytesToAddress(b, bonusLoc)
}

func bonusShare(number uint64, coinbase common.Address) *types.WorkObjectHeader {
	share := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
	share.SetNumber(new(big.Int).SetUint64(number))
	share.SetPrimaryCoinbase(coinbase)
	return share
}

func bonusBlock(number uint64, coinbase common.Address, shares ...*types.WorkObjectHeader) *types.WorkObject {
	wo := types.EmptyWorkObject(common.ZONE_CTX)
	wo.WorkObjectHeader().SetNumber(new(big.Int).SetUint64(number))
	wo.WorkObjectHeader().SetPrimaryCoinbase(coinbase)
	wo.WorkObjectHeader().SetData([]byte{0x01})
	wo.Body().SetUncles(shares)
	return wo
}

// runBonus turns the bonus on for the test and calls appendIncluderBonusEtx
// the way the payout loops do.
func runBonus(t *testing.T, etxs []*types.Transaction, target *types.WorkObject, quaiR, qiR int64) []*types.Transaction {
	fork := params.IncluderBonusForkBlock
	params.IncluderBonusForkBlock = 0
	t.Cleanup(func() { params.IncluderBonusForkBlock = fork })
	return appendIncluderBonusEtx(etxs, target, big.NewInt(quaiR), big.NewInt(qiR), bonusParent, bonusLoc)
}

func TestIncluderBonusOffBeforeFork(t *testing.T) {
	target := bonusBlock(bonusT, quaiA, bonusShare(bonusT, quaiB))
	// The target's prime terminus (0) is below IncluderBonusForkBlock.
	etxs := appendIncluderBonusEtx(nil, target, big.NewInt(1_000_000), big.NewInt(1_000_000), bonusParent, bonusLoc)
	require.Empty(t, etxs)
}

func TestIncluderBonusOneEtxForAllHeights(t *testing.T) {
	// Shares from every height a block can include, paid as a single ETX.
	etxs := runBonus(t, nil, bonusBlock(bonusT, quaiA,
		bonusShare(bonusT-3, quaiB), bonusShare(bonusT-2, quaiB), bonusShare(bonusT-1, quaiB), bonusShare(bonusT, quaiB),
	), 1_000_000, 0)

	require.Len(t, etxs, 1)
	require.Equal(t, int64(200_000), etxs[0].Value().Int64()) // 4 × R × 500/10000
	require.Equal(t, uint64(types.CoinbaseType), etxs[0].EtxType())
	require.Equal(t, params.TxGas, etxs[0].Gas())
}

func TestIncluderBonusRounding(t *testing.T) {
	etxs := runBonus(t, nil, bonusBlock(bonusT, quaiA,
		bonusShare(bonusT, quaiB), bonusShare(bonusT, qiA), bonusShare(bonusT-1, quaiA),
	), 10_013, 0)

	require.Len(t, etxs, 1)
	// The per-share amount is floored before multiplying by the count:
	// 3 × ⌊10013×500/10000⌋ = 1500, whereas ⌊3×10013×500/10000⌋ = 1501.
	require.Equal(t, int64(1500), etxs[0].Value().Int64())
}

func TestIncluderBonusFiltering(t *testing.T) {
	// Shares with a non-internal coinbase are not paid, so they earn no bonus.
	etxs := runBonus(t, nil, bonusBlock(bonusT, quaiA,
		bonusShare(bonusT, quaiB), bonusShare(bonusT-1, quaiB), bonusShare(bonusT, extA),
	), 1_000_000, 0)
	require.Len(t, etxs, 1)
	require.Equal(t, int64(100_000), etxs[0].Value().Int64())

	// No counted shares, or a bonus that rounds down to zero (⌊19×500/10000⌋ = 0), emits nothing.
	require.Empty(t, runBonus(t, nil, bonusBlock(bonusT, quaiA, bonusShare(bonusT, extA)), 1_000_000, 0))
	require.Empty(t, runBonus(t, nil, bonusBlock(bonusT, quaiA), 1_000_000, 0))
	require.Empty(t, runBonus(t, nil, bonusBlock(bonusT, quaiA, bonusShare(bonusT, quaiB)), 19, 19))
}

func TestIncluderBonusLedger(t *testing.T) {
	qi := runBonus(t, nil, bonusBlock(bonusT, qiA, bonusShare(bonusT, quaiB)), 1_000_000, 2_000_000)
	require.Len(t, qi, 1)
	require.Equal(t, int64(100_000), qi[0].Value().Int64()) // Qi R × 500/10000
	require.Equal(t, common.SetBlockHashForQi(bonusParent, bonusLoc), qi[0].OriginatingTxHash())

	quai := runBonus(t, nil, bonusBlock(bonusT, quaiA, bonusShare(bonusT, quaiB)), 1_000_000, 2_000_000)
	require.Len(t, quai, 1)
	require.Equal(t, int64(50_000), quai[0].Value().Int64()) // Quai R × 500/10000
	require.Equal(t, common.SetBlockHashForQuai(bonusParent, bonusLoc), quai[0].OriginatingTxHash())
}

func TestIncluderBonusAppendsAfterPayouts(t *testing.T) {
	prior := make([]*types.Transaction, 7) // share payouts already emitted by the loop
	etxs := runBonus(t, prior, bonusBlock(bonusT, quaiA, bonusShare(bonusT, quaiB)), 1_000_000, 0)

	require.Len(t, etxs, 8)
	bonus := etxs[7]
	require.True(t, bonus.To().Equal(quaiA))
	require.True(t, bonus.ETXSender().Equal(quaiA))
	require.Equal(t, uint16(7), bonus.ETXIndex())
}

func TestIncluderBonusData(t *testing.T) {
	shares := []*types.WorkObjectHeader{bonusShare(bonusT, quaiA), bonusShare(bonusT-1, quaiB)}
	target := bonusBlock(bonusT, quaiA, shares...)
	target.WorkObjectHeader().SetData(append([]byte{0x01}, quaiB.Bytes()...)) // lockup byte + lockup contract
	etxs := runBonus(t, nil, target, 1_000_000, 0)

	require.Len(t, etxs, 1)
	data := etxs[0].Data()
	require.Len(t, data, len(target.Data())+common.HashLength)
	require.Equal(t, target.Data(), data[:len(target.Data())])
	tag := common.BytesToHash(data[len(target.Data()):])
	for _, h := range []common.Hash{target.Hash(), shares[0].Hash(), shares[1].Hash()} {
		require.NotEqual(t, h, tag)
	}
}

func TestIncluderBonusParams(t *testing.T) {
	require.Less(t, params.IncluderBonusNumerator, params.IncluderBonusDenominator)
	for _, fork := range []uint64{params.ConversionLockChangeForkBlock, params.InclusionDepthChangeBlock, params.KawPowForkBlock} {
		require.GreaterOrEqual(t, params.IncluderBonusForkBlock, fork)
	}
}
