package core

import (
	"math/big"
	"sync"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

func TestTrimDeduplicationActivatesWithQiUnlockFork(t *testing.T) {
	tests := []struct {
		name             string
		primeHeight      uint64
		initialSetSize   uint64
		wantDeletions    int
		wantTrimmed      int
		wantSupplyChange bool
	}{
		{
			name:             "before fork preserves legacy accounting",
			primeHeight:      params.ConversionLockChangeForkBlock - 1,
			initialSetSize:   1,
			wantDeletions:    2,
			wantTrimmed:      1,
			wantSupplyChange: true,
		},
		{
			name:           "at fork deduplicates transaction spend",
			primeHeight:    params.ConversionLockChangeForkBlock,
			initialSetSize: 0,
			wantDeletions:  1,
			wantTrimmed:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := rawdb.NewMemoryDatabase(log.Global)
			defer db.Close()
			hc := &HeaderChain{headerDb: db}
			txHash := common.HexToHash("0x01")
			blockHash := common.HexToHash("0x02")
			const index uint16 = 0
			const denomination uint8 = 0
			utxo := &types.UtxoEntry{
				Denomination: denomination,
				Address:      make([]byte, common.AddressLength),
				Lock:         new(big.Int),
			}
			if err := rawdb.CreateUTXO(db, txHash, index, utxo); err != nil {
				t.Fatal(err)
			}
			createdKey := rawdb.UtxoKeyWithDenomination(txHash, index, denomination)
			if err := rawdb.WriteCreatedUTXOKeys(db, blockHash, [][]byte{createdKey}); err != nil {
				t.Fatal(err)
			}

			batch := db.NewBatch()
			batch.SetPending(true)
			rawdb.DeleteUTXO(batch, txHash, index) // Transaction spend pending in this block.
			utxoHash := types.UTXOHash(txHash, index, utxo)
			utxosDelete := []common.Hash{utxoHash}
			trimmed := []*types.SpentUtxoEntry{}
			supplyRemoved := new(big.Int)
			utxoSetSize := tc.initialSetSize
			var lock sync.Mutex

			hc.trimBlock(batch, denomination, 1, blockHash, &utxosDelete, newTrimDeleteSet(tc.primeHeight, utxosDelete), &trimmed, supplyRemoved, &utxoSetSize, true, &lock, log.Global)

			if len(utxosDelete) != tc.wantDeletions || len(trimmed) != tc.wantTrimmed || utxoSetSize != 0 {
				t.Fatalf("unexpected trim accounting: deletions=%d trimmed=%d size=%d", len(utxosDelete), len(trimmed), utxoSetSize)
			}
			if got := supplyRemoved.Sign() != 0; got != tc.wantSupplyChange {
				t.Fatalf("unexpected supply change: got %s", supplyRemoved)
			}
		})
	}
}
