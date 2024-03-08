package rawdb

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
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

	if entry := ReadHeader(db, header.Hash(), common.ZONE_CTX); entry != nil {
		t.Fatalf("Non existent header returned: %v", entry)
	}
	t.Log("Header Hash stored", header.Hash())
	// Write and verify the header in the database
	WriteHeader(db, header, common.ZONE_CTX)
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

// Tests termini storage and retrieval operations.
func TestTerminiStorage(t *testing.T) {
	db := NewMemoryDatabase()

	// Create a test termini to move around the database and make sure it's really new
	termini := types.EmptyTermini()
	termini.SetDomTermini([]common.Hash{common.Hash{1}, common.Hash{2}})
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
