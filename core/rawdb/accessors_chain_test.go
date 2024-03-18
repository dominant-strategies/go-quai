package rawdb

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

func TestCanonicalHashStorage(t *testing.T) {
	db := NewMemoryDatabase()

	emptyHash := common.Hash{}
	hash := common.Hash{1}
	number := uint64(1)

	if entry := ReadCanonicalHash(db, number); entry != emptyHash {
		t.Fatalf("Non existent canonical hash returned: %v", entry)
	}

	t.Log("Canonical Hash stored", hash)
	WriteCanonicalHash(db, hash, number)

	if entry := ReadCanonicalHash(db, number); entry == emptyHash {
		t.Fatalf("Stored canonical hash not found with number %d", number)
	} else if entry != hash {
		t.Fatalf("Retrieved canonical hash mismatch: have %v, want %v", entry, hash)
	}

	DeleteCanonicalHash(db, number)

	if entry := ReadCanonicalHash(db, number); entry != emptyHash {
		t.Fatalf("Deleted canonical hash returned: %v", entry)
	}

}

// Tests block header storage and retrieval operations.
func TestHeaderStorage(t *testing.T) {
	db := NewMemoryDatabase()

	// Create a test header to move around the database and make sure it's really new
	header := types.EmptyHeader()
	header.SetParentHash(common.Hash{1}, common.ZONE_CTX)
	header.SetBaseFee(big.NewInt(1))
	header.SetLocation(common.Location{0, 0})
	header.SetNumber(big.NewInt(1), common.ZONE_CTX)
	header.SetCoinbase(common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{0, 0}))

	if HasHeader(db, header.Hash(), common.ZONE_CTX) {
		t.Fatal("Non existent header returned")
	}
	if entry := ReadHeader(db, header.Hash(), common.ZONE_CTX); entry != nil {
		t.Fatalf("Non existent header returned: %v", entry)
	}

	WriteHeader(db, header, common.ZONE_CTX)
	t.Log("Header Hash stored", header.Hash())

	if HasHeader(db, header.Hash(), common.ZONE_CTX) {
		t.Fatal("HasHeader returned false")
	}

	if entry := ReadHeader(db, header.Hash(), header.Number(common.ZONE_CTX).Uint64()); entry == nil {
		t.Fatalf("Stored header not found with hash %s", entry.Hash())
	} else if entry.Hash() != header.Hash() {
		t.Fatalf("Retrieved header mismatch: have %v, want %v", entry, header)
	}

	// Verify header number
	if number := ReadHeaderNumber(db, header.Hash()); *number != uint64(1) {
		t.Fatalf("Retrieved header number mismatch: have %v, want %v", number, big.NewInt(1))
	}
	// Modify the header number and check if it was updated
	WriteHeaderNumber(db, header.Hash(), uint64(2))
	if number := ReadHeaderNumber(db, header.Hash()); *number != uint64(2) {
		t.Fatalf("Retrieved header number mismatch: have %v, want %v", number, big.NewInt(1))
	}

	DeleteHeaderNumber(db, header.Hash())
	if number := ReadHeaderNumber(db, header.Hash()); number != nil {
		t.Fatalf("Deleted header number returned: %v", number)
	}

	// Delete the header and verify the execution
	DeleteHeader(db, header.Hash(), header.Number(common.ZONE_CTX).Uint64())
	if entry := ReadHeader(db, header.Hash(), header.Number(common.ZONE_CTX).Uint64()); entry != nil {
		t.Fatalf("Deleted header returned: %v", entry)
	}
}

func TestHeadHeaderStorage(t *testing.T) {
	db := NewMemoryDatabase()

	emptyHash := common.Hash{}
	if entry := ReadHeadHeaderHash(db); entry != emptyHash {
		t.Fatalf("Non existent head header hash returned: %v", entry)
	}

	hash := common.Hash{1}
	WriteHeadHeaderHash(db, hash)

	if entry := ReadHeadHeaderHash(db); entry != hash {
		t.Fatalf("Stored head header hash not found: %v", entry)
	}
}

func TestHeadBlockStorage(t *testing.T) {
	db := NewMemoryDatabase()

	emptyHash := common.Hash{}
	if entry := ReadHeadBlockHash(db); entry != emptyHash {
		t.Fatalf("Non existent head block hash returned: %v", entry)
	}

	hash := common.Hash{1}
	WriteHeadBlockHash(db, hash)

	if entry := ReadHeadBlockHash(db); entry != hash {
		t.Fatalf("Stored head block hash not found: %v", entry)
	}
}

func TestLastPivotStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadLastPivotNumber(db); entry != nil {
		t.Fatalf("Non existent last pivot number returned: %v", entry)
	}

	WriteLastPivotNumber(db, uint64(1))

	if entry := ReadLastPivotNumber(db); *entry != uint64(1) {
		t.Fatalf("Stored last pivot not found: %v", entry)
	}
}

func TestFastTrieStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadFastTrieProgress(db); entry != 0 {
		t.Fatalf("Non existent fast trie returned: %v", entry)
	}

	WriteFastTrieProgress(db, uint64(1))

	if entry := ReadFastTrieProgress(db); entry != uint64(1) {
		t.Fatalf("Stored fast trie not found: %v", entry)
	}
}

func TestTxIndexTailStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadTxIndexTail(db); entry != nil {
		t.Fatalf("Non existent tx index returned: %v", entry)
	}

	WriteTxIndexTail(db, uint64(1))

	if entry := ReadTxIndexTail(db); *entry != uint64(1) {
		t.Fatalf("Stored tx index not found: %v", entry)
	}
}

func TestFastTxLookupStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadFastTxLookupLimit(db); entry != nil {
		t.Fatalf("Non existent fast tx lookup returned: %v", entry)
	}

	WriteFastTxLookupLimit(db, uint64(1))

	if entry := ReadFastTxLookupLimit(db); *entry != uint64(1) {
		t.Fatalf("Stored fast tx lookup not found: %v", entry)
	}
}

func TestPbCacheStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadPbCacheBody(db, common.Hash{1}, common.Location{0, 0}); entry != nil {
		t.Fatalf("Non existent pb cache body returned: %v", entry)
	}

	WritePbCacheBody(db, common.Hash{1}, &types.Body{})

	if entry := ReadPbCacheBody(db, common.Hash{1}, common.Location{0, 0}); entry == nil {
		t.Fatalf("Stored pb cache body not found: %v", entry)
	}

	DeletePbCacheBody(db, common.Hash{1})

	if entry := ReadPbCacheBody(db, common.Hash{1}, common.Location{0, 0}); entry != nil {
		t.Fatalf("Deleted pb cache body returned: %v", entry)
	}
}

func TestPbBodyKeysStorage(t *testing.T) {
	db := NewMemoryDatabase()

	hashes := common.Hashes{{1}, {2}}

	if entry := ReadPbBodyKeys(db); entry != nil {
		t.Fatalf("Non existent pb  bodyKeys returned: %v", entry)
	}

	WritePbBodyKeys(db, hashes)

	if entry := ReadPbBodyKeys(db); hashes[0] != entry[0] || hashes[1] != entry[1] {
		t.Fatalf("Stored pb  bodyKeys not found: %v", entry)
	}

	DeleteAllPbBodyKeys(db)

	if entry := ReadPbBodyKeys(db); entry != nil {
		t.Fatalf("Deleted pb  bodyKeys returned: %v", entry)
	}
}

// Tests termini storage and retrieval operations.
func TestTerminiStorage(t *testing.T) {
	db := NewMemoryDatabase()

	// Create a test termini to move around the database and make sure it's really new
	termini := types.EmptyTermini()
	termini.SetDomTermini(common.Hashes{{1}, {2}})
	hash := types.EmptyRootHash
	if entry := ReadTermini(db, hash); entry != nil {
		t.Fatalf("Non existent termini returned: %v", entry)
	}
	t.Log("Termini Hash stored", hash)
	// Write and verify the termini in the database
	WriteTermini(db, hash, termini)
	if entry := ReadTermini(db, hash); entry == nil {
		t.Fatalf("Stored termini not found with hash %s", hash)
	}
	// Delete the termini and verify the execution
	DeleteTermini(db, hash)
	if entry := ReadTermini(db, hash); entry != nil {
		t.Fatalf("Deleted termini returned: %v", entry)
	}
}

func TestPendingHeaderStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadPendingHeader(db, common.Hash{1}); entry != nil {
		t.Fatalf("Non existent pending header returned: %v", entry)
	}

	emptyHeader := types.EmptyHeader()

	emptyPendingHeader := types.EmptyPendingHeader()
	emptyPendingHeader.SetHeader(emptyHeader)

	WritePendingHeader(db, common.Hash{1}, emptyPendingHeader)

	if entry := ReadPendingHeader(db, common.Hash{1}); entry == nil {
		t.Fatalf("Stored pb  bodyKeys not found: %v", entry)
	}

	DeletePendingHeader(db, common.Hash{1})

	if entry := ReadPendingHeader(db, common.Hash{1}); entry != nil {
		t.Fatalf("Deleted pb  bodyKeys returned: %v", entry)
	}
}

func TestBestPhKeyStorage(t *testing.T) {
	db := NewMemoryDatabase()

	emptyHash := common.Hash{}

	if entry := ReadBestPhKey(db); entry != emptyHash {
		t.Fatalf("Non existent best phKey returned: %v", entry)
	}

	hash := common.Hash{1}

	WriteBestPhKey(db, hash)

	if entry := ReadBestPhKey(db); entry != hash {
		t.Fatalf("Stored best phKey not found: %v", entry)
	}

	DeleteBestPhKey(db)

	if entry := ReadBestPhKey(db); entry != emptyHash {
		t.Fatalf("Failed to delete key: %v", entry)
	}
}

func TestEtxSetStorage(t *testing.T) {
	db := NewMemoryDatabase()

	// Create a test etxSet to move around the database and make sure it's really new
	etxSet := types.NewEtxSet()
	hash := common.Hash{1}
	var number uint64 = 0
	location := common.Location{0, 0}
	if entry := ReadEtxSet(db, hash, number, location); entry != nil {
		t.Fatalf("Non existent etxSet returned: %v", entry)
	}
	t.Log("EtxSet Hash stored", hash)
	// Write and verify the etxSet in the database
	WriteEtxSet(db, hash, 0, etxSet)
	if entry := ReadEtxSet(db, hash, number, location); entry == nil {
		t.Fatalf("Stored etxSet not found with hash %s", hash)
	}
	// Delete the etxSet and verify the execution
	DeleteEtxSet(db, hash, number)
	if entry := ReadEtxSet(db, hash, number, location); entry != nil {
		t.Fatalf("Deleted etxSet returned: %v", entry)
	}
}
func TestHeadsHashesStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadHeadsHashes(db); len(entry) != 0 {
		t.Fatalf("Non existent heads hashes returned: %v", entry)
	}

	hashes := common.Hashes{{1}, {2}}
	WriteHeadsHashes(db, hashes)

	if entry := ReadHeadsHashes(db); entry[0] != hashes[0] || entry[1] != hashes[1] {
		t.Fatalf("Stored heads hashes not found: %v", entry)
	}

	DeleteAllHeadsHashes(db)

	if entry := ReadHeadsHashes(db); len(entry) != 0 {
		t.Fatalf("Deleted heads hashes returned: %v", entry)
	}
}

func TestBodyAndReceiptsStorage(t *testing.T) {
	db := NewMemoryDatabase()

	location := common.Location{0, 0}
	hash := common.Hash{1}
	number := uint64(1)

	//Body
	if HasBody(db, common.Hash{1}, uint64(1)) {
		t.Fatal("Non existent body returned")
	}

	if entry := ReadBody(db, hash, number, location); entry != nil {
		t.Fatalf("Non existent body returned: %v", entry)
	}

	inner := &types.InternalTx{
		V: new(big.Int).SetUint64(1),
		R: new(big.Int).SetUint64(1),
		S: new(big.Int).SetUint64(1),
	}
	transaction := types.NewTx(inner)
	emptyBody := &types.Body{
		Transactions:    types.Transactions{transaction},
		Uncles:          []*types.Header{},
		ExtTransactions: types.Transactions{},
		SubManifest:     types.BlockManifest{},
	}

	WriteBody(db, hash, number, emptyBody)

	if entry := ReadBody(db, hash, number, location); entry == nil {
		t.Fatalf("Stored body not found: %v", entry)
	}

	//Receipts

	if HasReceipts(db, hash, number) {
		t.Fatal("Non existent header returned")
	}

	config := &params.ChainConfig{Location: location}

	if entry := ReadReceipts(db, hash, number, config); entry != nil {
		t.Fatalf("Non existent receipts returned: %v", entry)
	}

	receipts := types.Receipts{types.NewReceipt([]byte{1}, false, uint64(1))}
	WriteReceipts(db, hash, number, receipts)

	if entry := ReadReceipts(db, hash, number, config); entry == nil {
		t.Fatal("Stored Receipts not found")
	}

	// TestDelete

	// Receipts
	DeleteReceipts(db, hash, number)

	if entry := ReadReceipts(db, hash, number, config); entry != nil {
		t.Fatalf("Deleted receipts returned: %v", entry)
	}

	// Body
	DeleteBody(db, hash, number)

	if entry := ReadBody(db, hash, number, location); entry != nil {
		t.Fatalf("Deleted body returned: %v", entry)
	}
}

// Tests inbound etx storage and retrieval operations.
func TestInboundEtxsStorage(t *testing.T) {
	db := NewMemoryDatabase()
	hash := common.Hash{1}
	location := common.Location{0, 0}

	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	inner := &types.InternalTx{
		ChainID:    new(big.Int).SetUint64(1),
		Nonce:      uint64(0),
		GasTipCap:  new(big.Int).SetUint64(0),
		GasFeeCap:  new(big.Int).SetUint64(0),
		Gas:        uint64(0),
		To:         &to,
		Value:      new(big.Int).SetUint64(0),
		Data:       []byte{0x04},
		AccessList: types.AccessList{},
		V:          new(big.Int).SetUint64(0),
		R:          new(big.Int).SetUint64(0),
		S:          new(big.Int).SetUint64(0),
	}
	tx := types.NewTx(inner)
	inboundEtxs := types.Transactions{tx}

	if entry := ReadInboundEtxs(db, hash, location); entry != nil {
		t.Fatalf("Non existent inbound etxs returned: %v", entry)
	}
	t.Log("Inbound InboundEtxs stored", inboundEtxs)
	// Write and verify the inboundEtxs in the database
	WriteInboundEtxs(db, hash, inboundEtxs)
	if entry := ReadInboundEtxs(db, hash, location); entry == nil {
		t.Fatalf("Stored InboundEtxs not found with hash %s", hash)
	} else {
		t.Log("InboundEtxs", entry)
		reflect.DeepEqual(inboundEtxs, entry)
	}
	// Delete the inboundEtxs and verify the execution
	DeleteInboundEtxs(db, hash)
	if entry := ReadInboundEtxs(db, hash, location); entry != nil {
		t.Fatalf("Deleted InboundEtxs returned: %v", entry)
	}
}
