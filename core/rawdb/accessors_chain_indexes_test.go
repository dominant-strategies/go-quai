package rawdb

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/stretchr/testify/require"
)

func TestTxLookupStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry, err := ReadTxLookupEntry(db, common.Hash{1}); err == nil {
		t.Fatalf("Non existent tx lookup returned: %v", entry)
	}

	if entry, err := ReadTxLookupEntry(db, common.Hash{2}); err == nil {
		t.Fatalf("Non existent tx lookup returned: %v", entry)
	}

	hashes := []common.Hash{{1}, {2}}

	WriteTxLookupEntries(db, 1, hashes)

	tx1 := createTransaction(1)
	tx2 := createTransaction(2)
	block := createBlockWithTransactions(0, types.Transactions{tx1, tx2})
	block.SetNumber(big.NewInt(1), common.ZONE_CTX)

	WriteTxLookupEntriesByBlock(db, block, common.ZONE_CTX)

	if entry, _ := ReadTxLookupEntry(db, common.Hash{1}); entry != 1 {
		t.Fatal("Wrong tx lookup returned for hash 1")
	}

	if entry, _ := ReadTxLookupEntry(db, common.Hash{2}); entry != 1 {
		t.Fatal("Wrong tx lookup returned for hash 2")
	}

	DeleteTxLookupEntries(db, hashes)

	// check deleted tx lookups
	if _, err := ReadTxLookupEntry(db, common.Hash{1}); err == nil {
		t.Fatal("Deleted lookup returned for hash 1")
	}

	if _, err := ReadTxLookupEntry(db, common.Hash{2}); err == nil {
		t.Fatal("Deleted lookup returned for hash 2")
	}

	// txs writen by block
	if entry, _ := ReadTxLookupEntry(db, tx1.Hash()); entry != block.NumberU64(common.ZONE_CTX) {
		t.Fatal("Wrong tx lookup returned for tx1 hash")
	}

	if entry, _ := ReadTxLookupEntry(db, tx2.Hash()); entry != block.NumberU64(common.ZONE_CTX) {
		t.Fatal("Wrong tx lookup returned for tx2 hash")
	}

}

func TestReadTransaction(t *testing.T) {
	db := NewMemoryDatabase(log.Global)
	tx := createTransaction(1)
	block := createBlockWithTransactions(0, types.Transactions{tx})
	block.SetNumber(big.NewInt(1), common.ZONE_CTX)

	testNilReadTransaction(t, db, tx.Hash())

	WriteTxLookupEntriesByBlock(db, block, common.ZONE_CTX)

	testNilReadTransaction(t, db, tx.Hash())

	WriteCanonicalHash(db, block.Hash(), block.NumberU64(common.ZONE_CTX))

	testNilReadTransaction(t, db, tx.Hash())

	WriteWorkObject(db, block.Hash(), block, types.BlockObject, common.ZONE_CTX)

	rtx, rhash, rnumber, rindex := ReadTransaction(db, tx.Hash())
	require.Equalf(t, tx.Hash(), rtx.Hash(), "Wrong transaction hash. have %v, want %v", rtx.Hash(), tx.Hash())
	require.Equalf(t, block.Hash(), rhash, "Wrong block hash. have %v, want %v", rhash, block.Hash())
	require.Equalf(t, block.NumberU64(common.ZONE_CTX), rnumber, "Wrong block number")
	require.Equalf(t, uint64(0), rindex, "Wrong transaction index. have %d, want %d", rindex, 0)
}

func testNilReadTransaction(t *testing.T, db ethdb.Database, hash common.Hash) {
	rtx, rhash, rnumber, rindex := ReadTransaction(db, hash)
	require.Nil(t, rtx, "Non-nil transaction returned")
	require.Equal(t, types.EmptyHash, rhash, "Non-nil block hash returned")
	require.Equal(t, uint64(0), rnumber, "Non-zero block number returned")
	require.Equal(t, uint64(0), rindex, "Non-negative transaction index returned")
}
