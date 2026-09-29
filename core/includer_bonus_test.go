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
	return common.BytesToAddress(b, common.Location{0, 0})
}

func bonusShare(number uint64, coinbase common.Address) *types.WorkObjectHeader {
	share := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
	share.SetNumber(new(big.Int).SetUint64(number))
	share.SetPrimaryCoinbase(coinbase)
	return share
}

func bonusBlock(coinbase common.Address, shares ...*types.WorkObjectHeader) *types.WorkObject {
	wo := types.EmptyWorkObject(common.ZONE_CTX)
	wo.WorkObjectHeader().SetNumber(big.NewInt(bonusT))
	wo.WorkObjectHeader().SetPrimaryCoinbase(coinbase)
	wo.Body().SetUncles(shares)
	return wo
}

// bonusFor turns the bonus on for the test and returns targetBlock's bonus.
func bonusFor(t *testing.T, target *types.WorkObject, quaiR, qiR int64) int64 {
	fork := params.IncluderBonusForkBlock
	params.IncluderBonusForkBlock = 0
	t.Cleanup(func() { params.IncluderBonusForkBlock = fork })
	return includerBonus(target, big.NewInt(quaiR), big.NewInt(qiR)).Int64()
}

func TestIncluderBonusOffBeforeFork(t *testing.T) {
	// The target's prime terminus (0) is below IncluderBonusForkBlock.
	target := bonusBlock(quaiA, bonusShare(bonusT, quaiB))
	require.Zero(t, includerBonus(target, big.NewInt(1_000_000), big.NewInt(1_000_000)).Sign())
}

func TestIncluderBonusAmount(t *testing.T) {
	// Shares from every height a block can include are all paid on the block's
	// own R; the share with a non-internal coinbase is not paid, so earns nothing.
	target := bonusBlock(quaiA,
		bonusShare(bonusT-3, quaiB), bonusShare(bonusT-2, quaiB), bonusShare(bonusT-1, quaiB), bonusShare(bonusT, quaiB),
		bonusShare(bonusT, extA))
	require.Equal(t, int64(200_000), bonusFor(t, target, 1_000_000, 0)) // 4 × R × 500/10000

	// Floored per share: 3 × ⌊10013×500/10000⌋ = 1500, not ⌊3×10013×500/10000⌋ = 1501.
	target = bonusBlock(quaiA, bonusShare(bonusT, quaiB), bonusShare(bonusT, qiA), bonusShare(bonusT-1, quaiA))
	require.Equal(t, int64(1500), bonusFor(t, target, 10_013, 0))

	require.Zero(t, bonusFor(t, bonusBlock(quaiA), 1_000_000, 0))
}

func TestIncluderBonusBase(t *testing.T) {
	// Before the conversion-lock fork the payout loop starts a Qi coinbase from
	// the Quai base and converts it afterwards, so the bonus uses the Quai base.
	target := bonusBlock(qiA, bonusShare(bonusT, quaiB))
	require.Equal(t, int64(50_000), bonusFor(t, target, 1_000_000, 2_000_000))

	target.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(params.ConversionLockChangeForkBlock))
	require.Equal(t, int64(100_000), bonusFor(t, target, 1_000_000, 2_000_000))
}
