package vm

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

func TestUndoCoinbasesDeletedPreservesEarlierTransactionClaim(t *testing.T) {
	loc := common.Location{0, 0}
	db := rawdb.NewMemoryDatabase(log.Global)
	statedb, err := state.New(common.Hash{}, common.Hash{}, new(big.Int), state.NewDatabase(db), state.NewDatabase(db), nil, loc, log.Global)
	if err != nil {
		t.Fatal(err)
	}
	owner := common.HexToAddress("0x0011000000000000000000000000000000000000", loc)
	beneficiary := common.HexToAddress("0x0022000000000000000000000000000000000000", loc)
	amountA, amountB := big.NewInt(123), big.NewInt(456)
	keyA, err := rawdb.WriteCoinbaseLockup(db, owner, beneficiary, 0, 1, amountA, 1, 1, beneficiary)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := rawdb.WriteCoinbaseLockup(db, owner, beneficiary, 0, 2, amountB, 1, 1, beneficiary)
	if err != nil {
		t.Fatal(err)
	}
	originalB, err := db.Get(keyB)
	if err != nil {
		t.Fatal(err)
	}
	batch := db.NewBatch()
	batch.SetPending(true)
	evm := NewEVM(BlockContext{}, TxContext{Hash: common.Hash{1}}, statedb, params.TestChainConfig, Config{}, batch)

	// Transaction A succeeds. Its lockup delete stays in the shared batch.
	deletedA := rawdb.DeleteCoinbaseLockup(batch, owner, beneficiary, 0, 1)
	if err := rawdb.WriteCoinbaseLockupToMap(evm.CoinbasesDeleted, deletedA, amountA, 1, 1, beneficiary); err != nil {
		t.Fatal(err)
	}
	evm.Reset(TxContext{Hash: common.Hash{2}}, statedb)
	if len(evm.CoinbasesDeleted) != 0 {
		t.Fatal("transaction B inherited transaction A's undo entries")
	}

	// Transaction B deletes a different lockup, then fails.
	deletedB := rawdb.DeleteCoinbaseLockup(batch, owner, beneficiary, 0, 2)
	if err := rawdb.WriteCoinbaseLockupToMap(evm.CoinbasesDeleted, deletedB, amountB, 1, 1, beneficiary); err != nil {
		t.Fatal(err)
	}
	evm.UndoCoinbasesDeleted()
	if len(evm.CoinbasesDeleted) != 0 {
		t.Fatal("transaction B's undo entries were not cleared")
	}
	if got, _, _, _ := rawdb.ReadCoinbaseLockup(db, batch, owner, beneficiary, 0, 1); got.Sign() != 0 {
		t.Fatalf("successful transaction A's claim was undone: %v", got)
	}
	if got, _, _, _ := rawdb.ReadCoinbaseLockup(db, batch, owner, beneficiary, 0, 2); got.Cmp(amountB) != 0 {
		t.Fatalf("failed transaction B's claim was not restored: %v", got)
	}
	if err := batch.Write(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Get(keyA); err == nil {
		t.Fatal("successful transaction A's claim did not remain deleted")
	}
	if got, err := db.Get(keyB); err != nil || !bytes.Equal(got, originalB) {
		t.Fatalf("failed transaction B's original lockup was not preserved: %x, %v", got, err)
	}
}
