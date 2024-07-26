package rawdb

import (
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
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
