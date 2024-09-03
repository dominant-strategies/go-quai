package rawdb

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestTxLookupStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadTxLookupEntry(db, common.Hash{1}); entry != nil {
		t.Fatalf("Non existent tx lookup returned: %v", entry)
	}

	if entry := ReadTxLookupEntry(db, common.Hash{2}); entry != nil {
		t.Fatalf("Non existent tx lookup returned: %v", entry)
	}

	hashes := []common.Hash{{1}, {2}}

	WriteTxLookupEntries(db, 1, hashes)

	tx1 := createTransaction(1)
	tx2 := createTransaction(2)
	block := createBlockWithTransactions(types.Transactions{tx1, tx2})
	block.SetNumber(big.NewInt(1), common.ZONE_CTX)

	WriteTxLookupEntriesByBlock(db, block, common.ZONE_CTX)

	if entry := ReadTxLookupEntry(db, common.Hash{1}); *entry != 1 {
		t.Fatal("Wrong tx lookup returned for hash 1")
	}

	if entry := ReadTxLookupEntry(db, common.Hash{2}); *entry != 1 {
		t.Fatal("Wrong tx lookup returned for hash 2")
	}

	DeleteTxLookupEntries(db, hashes)

	// check deleted tx lookups
	if entry := ReadTxLookupEntry(db, common.Hash{1}); entry != nil {
		t.Fatal("Deleted lookup returned for hash 1")
	}

	if entry := ReadTxLookupEntry(db, common.Hash{2}); entry != nil {
		t.Fatal("Deleted lookup returned for hash 2")
	}

	// txs writen by block
	if entry := ReadTxLookupEntry(db, tx1.Hash()); *entry != block.NumberU64(common.ZONE_CTX) {
		t.Fatal("Wrong tx lookup returned for tx1 hash")
	}

	if entry := ReadTxLookupEntry(db, tx2.Hash()); *entry != block.NumberU64(common.ZONE_CTX) {
		t.Fatal("Wrong tx lookup returned for tx2 hash")
	}

	//v4-v5 tx lookup
	v4Hash := common.Hash{4}
	v4Number := uint64(3)
	WriteHeaderNumber(db, v4Hash, v4Number)
	writeTxLookupEntry(db, v4Hash, v4Hash.Bytes())

	if entry := ReadTxLookupEntry(db, v4Hash); *entry != v4Number {
		t.Fatal("Wrong tx lookup returned for v4 hash")
	}

	//v3 tx lookup
	v3Hash := common.Hash{5}
	v3ProtoHash := &common.ProtoHash{Value: v3Hash.Bytes()}
	v3Number := uint64(4)
	v3entry, err := proto.Marshal(&ProtoLegacyTxLookupEntry{BlockIndex: v3Number, Hash: v3ProtoHash})
	if err != nil {
		t.Fatal("Failed to marshal ProtoLegacyTxLookupEntry")
	}
	writeTxLookupEntry(db, v3Hash, v3entry)

	if entry := ReadTxLookupEntry(db, v3Hash); *entry != v3Number {
		t.Fatal("Wrong tx lookup returned for v4 hash")
	}

}

func TestReadTransaction(t *testing.T) {
	db := NewMemoryDatabase(log.Global)
	tx := createTransaction(1)
	block := createBlockWithTransactions(types.Transactions{tx})
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
