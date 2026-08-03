package core

import (
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
)

func TestChainIndexerIndexesForkAwareUnwrapLock(t *testing.T) {
	tests := []struct {
		name        string
		primeHeight uint64
		lockPeriod  uint64
	}{
		{
			name:        "before fork",
			primeHeight: params.ConversionLockChangeForkBlock - 1,
			lockPeriod:  params.ConversionLockPeriod,
		},
		{
			name:        "at fork",
			primeHeight: params.ConversionLockChangeForkBlock,
			lockPeriod:  params.UnwrapQiLockPeriod,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := rawdb.NewMemoryDatabase(log.Global)
			defer db.Close()
			indexer := &ChainIndexer{chainDb: db, logger: log.Global}

			blockHeight := uint64(100)
			block := types.EmptyWorkObject(common.ZONE_CTX)
			block.SetNumber(new(big.Int).SetUint64(blockHeight), common.ZONE_CTX)
			block.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(tc.primeHeight))

			to := common.HexToAddress("0x0080000000000000000000000000000000000000", common.Location{0, 0})
			require.True(t, to.IsInQiLedgerScope())
			unwrap := types.NewTx(&types.ExternalTx{
				OriginatingTxHash: common.Hash{1},
				ETXIndex:          1,
				Gas:               params.CallValueTransferGas,
				To:                &to,
				Value:             new(big.Int).Set(types.Denominations[types.MaxDenomination]),
				Sender:            common.ZeroAddress(common.Location{0, 0}),
				EtxType:           types.UnwrapQiType,
			})
			block.Body().SetTransactions([]*types.Transaction{unwrap})

			indexer.addOutpointsToIndexer(common.ZONE_CTX, params.ChainConfig{Location: common.Location{0, 0}}, block)

			addressAtHeight := to.Bytes20()
			binary.BigEndian.PutUint32(addressAtHeight[16:], uint32(blockHeight))
			outpoints, err := rawdb.ReadOutpointsForAddressAtBlock(db, addressAtHeight)
			require.NoError(t, err)
			require.Len(t, outpoints, 1)
			require.Equal(t, uint8(types.MaxDenomination), outpoints[0].Denomination)
			require.Equal(t, new(big.Int).SetUint64(blockHeight+tc.lockPeriod), outpoints[0].Lock)
		})
	}
}
