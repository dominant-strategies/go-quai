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

const bonusT = 100 // target height t

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

// runBonus turns the bonus on for the test and calls appendIncluderBonusEtxs
// the way the payout loops do: includers are heights t+3 down to t, and the
// target block is the last one.
func runBonus(t *testing.T, etxs []*types.Transaction, includers []*types.WorkObject, quaiR, qiR int64) []*types.Transaction {
	fork := params.IncluderBonusForkBlock
	params.IncluderBonusForkBlock = 0
	t.Cleanup(func() { params.IncluderBonusForkBlock = fork })
	return appendIncluderBonusEtxs(etxs, includers[len(includers)-1], includers, big.NewInt(quaiR), big.NewInt(qiR),
		bonusParent, bonusLoc)
}

func TestIncluderBonusOffBeforeFork(t *testing.T) {
	includers := []*types.WorkObject{
		bonusBlock(bonusT+3, quaiA, bonusShare(bonusT, quaiB)),
		bonusBlock(bonusT+2, quaiA),
		bonusBlock(bonusT+1, quaiA),
		bonusBlock(bonusT, quaiA),
	}
	// The target's prime terminus (0) is below IncluderBonusForkBlock.
	etxs := appendIncluderBonusEtxs(nil, includers[3], includers, big.NewInt(1_000_000), big.NewInt(1_000_000),
		bonusParent, bonusLoc)
	require.Empty(t, etxs)
}

func TestIncluderBonusFlatAcrossHeights(t *testing.T) {
	etxs := runBonus(t, nil, []*types.WorkObject{
		bonusBlock(bonusT+3, quaiA, bonusShare(bonusT, quaiB)),
		bonusBlock(bonusT+2, quaiA, bonusShare(bonusT, quaiB)),
		bonusBlock(bonusT+1, quaiA, bonusShare(bonusT, quaiB)),
		bonusBlock(bonusT, quaiA, bonusShare(bonusT, quaiB)),
	}, 1_000_000, 0)

	require.Len(t, etxs, 4)
	for _, etx := range etxs { // R × 500/10000, whichever chance the share was included on
		require.Equal(t, int64(50_000), etx.Value().Int64())
		require.Equal(t, uint64(types.CoinbaseType), etx.EtxType())
		require.Equal(t, params.TxGas, etx.Gas())
	}
}

func TestIncluderBonusAggregatesShares(t *testing.T) {
	etxs := runBonus(t, nil, []*types.WorkObject{
		bonusBlock(bonusT+3, quaiA),
		bonusBlock(bonusT+2, quaiA),
		bonusBlock(bonusT+1, quaiA, bonusShare(bonusT, quaiB), bonusShare(bonusT, qiA), bonusShare(bonusT, quaiA)),
		bonusBlock(bonusT, quaiA),
	}, 10_013, 0)

	require.Len(t, etxs, 1)
	// The per-share amount is floored before multiplying by the count:
	// 3 × ⌊10013×500/10000⌋ = 1500, whereas ⌊3×10013×500/10000⌋ = 1501.
	require.Equal(t, int64(1500), etxs[0].Value().Int64())
}

func TestIncluderBonusFiltering(t *testing.T) {
	etxs := runBonus(t, nil, []*types.WorkObject{
		// One counted share; one at another height and one non-internal are skipped.
		bonusBlock(bonusT+3, quaiA, bonusShare(bonusT, quaiB), bonusShare(bonusT+1, quaiB), bonusShare(bonusT, extA)),
		// No counted entries.
		bonusBlock(bonusT+2, quaiA, bonusShare(bonusT+1, quaiB), bonusShare(bonusT, extA)),
		bonusBlock(bonusT+1, quaiA),
		bonusBlock(bonusT, quaiA),
	}, 1_000_000, 0)

	require.Len(t, etxs, 1)
	require.True(t, etxs[0].To().Equal(quaiA))
	require.Equal(t, int64(50_000), etxs[0].Value().Int64())

	// A bonus that rounds down to zero emits nothing: ⌊19×500/10000⌋ = 0.
	require.Empty(t, runBonus(t, nil, []*types.WorkObject{
		bonusBlock(bonusT+3, quaiA),
		bonusBlock(bonusT+2, quaiA),
		bonusBlock(bonusT+1, quaiA),
		bonusBlock(bonusT, quaiA, bonusShare(bonusT, quaiB)),
	}, 19, 19))
}

func TestIncluderBonusLedger(t *testing.T) {
	etxs := runBonus(t, nil, []*types.WorkObject{
		bonusBlock(bonusT+3, qiA, bonusShare(bonusT, quaiB)),
		bonusBlock(bonusT+2, quaiA, bonusShare(bonusT, quaiB)),
		bonusBlock(bonusT+1, quaiA),
		bonusBlock(bonusT, quaiA),
	}, 1_000_000, 2_000_000)

	require.Len(t, etxs, 2)
	require.Equal(t, int64(100_000), etxs[0].Value().Int64()) // Qi R × 500/10000
	require.Equal(t, common.SetBlockHashForQi(bonusParent, bonusLoc), etxs[0].OriginatingTxHash())
	require.Equal(t, int64(50_000), etxs[1].Value().Int64()) // Quai R × 500/10000
	require.Equal(t, common.SetBlockHashForQuai(bonusParent, bonusLoc), etxs[1].OriginatingTxHash())
}

func TestIncluderBonusOrderAndIndex(t *testing.T) {
	prior := make([]*types.Transaction, 7) // share payouts already emitted by the loop
	etxs := runBonus(t, prior, []*types.WorkObject{
		bonusBlock(bonusT+3, quaiA, bonusShare(bonusT, quaiB)),
		bonusBlock(bonusT+2, quaiB), // pays nothing and leaves no index gap
		bonusBlock(bonusT+1, qiA, bonusShare(bonusT, quaiB)),
		bonusBlock(bonusT, quaiB, bonusShare(bonusT, quaiA)),
	}, 1_000_000, 1_000_000)

	require.Len(t, etxs, 10)
	for i, want := range []common.Address{quaiA, qiA, quaiB} {
		etx := etxs[len(prior)+i]
		require.True(t, etx.To().Equal(want))
		require.True(t, etx.ETXSender().Equal(want))
		require.Equal(t, uint16(len(prior)+i), etx.ETXIndex())
	}
}

func TestIncluderBonusData(t *testing.T) {
	shares := []*types.WorkObjectHeader{bonusShare(bonusT, quaiA), bonusShare(bonusT, quaiB)}
	includer := bonusBlock(bonusT+3, quaiA, shares...)
	includer.WorkObjectHeader().SetData(append([]byte{0x01}, quaiB.Bytes()...)) // lockup byte + lockup contract
	target := bonusBlock(bonusT, quaiB)
	etxs := runBonus(t, nil, []*types.WorkObject{includer, bonusBlock(bonusT+2, quaiA), bonusBlock(bonusT+1, quaiA), target}, 1_000_000, 0)

	require.Len(t, etxs, 1)
	data := etxs[0].Data()
	require.Len(t, data, len(includer.Data())+common.HashLength)
	require.Equal(t, includer.Data(), data[:len(includer.Data())])
	tag := common.BytesToHash(data[len(includer.Data()):])
	for _, h := range []common.Hash{includer.Hash(), target.Hash(), shares[0].Hash(), shares[1].Hash()} {
		require.NotEqual(t, h, tag)
	}
}

func TestIncluderBonusParams(t *testing.T) {
	require.Less(t, params.IncluderBonusNumerator, params.IncluderBonusDenominator)
	for _, fork := range []uint64{params.ConversionLockChangeForkBlock, params.InclusionDepthChangeBlock, params.KawPowForkBlock} {
		require.GreaterOrEqual(t, params.IncluderBonusForkBlock, fork)
	}
}
