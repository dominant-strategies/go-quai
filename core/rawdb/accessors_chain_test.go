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

	// Fill database with testing data.
	for i := uint64(1); i <= 5; i++ {
		WriteCanonicalHash(db, common.Hash{1}, i)
	}

	if entry := ReadCanonicalHash(db, number); entry == emptyHash {
		t.Fatalf("Stored canonical hash not found with number %d", number)
	} else if entry != hash {
		t.Fatalf("Retrieved canonical hash mismatch: have %v, want %v", entry, hash)
	}

	var cases = []struct {
		from, to uint64
		limit    int
		expect   []uint64
	}{
		{1, 2, 0, nil},
		{1, 3, 2, []uint64{1, 2}},
		{2, 6, 6, []uint64{2, 3, 4, 5}},
		{1, 6, 6, []uint64{1, 2, 3, 4, 5}},
		{6, 7, 6, nil},
	}

	for i, c := range cases {
		numbers, _ := ReadAllCanonicalHashes(db, c.from, c.to, c.limit)
		if !reflect.DeepEqual(numbers, c.expect) {
			t.Fatalf("Case %d failed, want %v, got %v", i, c.expect, numbers)
		}
	}

	// Delete all data from database.
	for i := uint64(1); i <= 5; i++ {
		DeleteCanonicalHash(db, uint64(i))
	}

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

func TestBlockHashesIterator(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadAllHashes(db, 1); len(entry) != 0 {
		t.Fatalf("Non existent block hashes returned: %v", entry)
	}

	testCases := []struct {
		blockHeight int64
		blockAmount int64
	}{
		{1, 1},
		{2, 2},
		{3, 3},
	}

	hashes := make([]map[common.Hash]bool, len(testCases))

	// Seed the database with test data
	for i, tc := range testCases {
		hashes[i] = make(map[common.Hash]bool)
		for j := int64(1); j <= tc.blockAmount; j++ {
			block := types.EmptyBlock()
			block.Header().SetNumber(big.NewInt(int64(i+1)), common.ZONE_CTX)
			// Change location from blocks on same height
			block.Header().SetLocation(common.Location{0, byte(j)})
			WriteBlock(db, block, common.ZONE_CTX)
			// Store block hashes to verify later
			hashes[i][block.Hash()] = true
		}
	}

	for i := range testCases {
		entry := ReadAllHashes(db, uint64(i+1))
		for _, hash := range entry {
			if !hashes[i][hash] {
				t.Fatalf("Case %d failed, hash %v not found in entry", i+1, hash)
			}
		}
	}
}

func TestCommonAncestor(t *testing.T) {
	db := NewMemoryDatabase()

	// Write region block
	regionBlock := types.EmptyBlock()
	regionBlock.Header().SetNumber(big.NewInt(1), common.REGION_CTX)
	regionBlock.Header().SetLocation(common.Location{0})
	regionBlock.Header().SetCoinbase(common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{0}))
	WriteBlock(db, regionBlock, common.REGION_CTX)

	//Write one block on zone 0 context
	zone0Block := types.EmptyBlock()
	zone0Block.Header().SetNumber(big.NewInt(2), common.ZONE_CTX)
	zone0Block.Header().SetParentHash(regionBlock.Hash(), common.ZONE_CTX)
	zone0Block.Header().SetLocation(common.Location{0, 0})
	zone0Block.Header().SetCoinbase(common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{0, 0}))
	WriteBlock(db, zone0Block, common.ZONE_CTX)

	//Write two blocks on zone 1 context
	zone1Block1 := types.EmptyBlock()
	zone1Block1.Header().SetNumber(big.NewInt(2), common.ZONE_CTX)
	zone1Block1.Header().SetParentHash(regionBlock.Hash(), common.ZONE_CTX)
	zone1Block1.Header().SetCoinbase(common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{0, 1}))
	zone1Block1.Header().SetLocation(common.Location{0, 1})
	WriteBlock(db, zone1Block1, common.ZONE_CTX)

	zone1Block2 := types.EmptyBlock()
	zone1Block2.Header().SetNumber(big.NewInt(3), common.ZONE_CTX)
	zone1Block2.Header().SetParentHash(zone1Block1.Hash(), common.ZONE_CTX)
	zone1Block2.Header().SetCoinbase(common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{0, 1}))
	zone1Block2.Header().SetLocation(common.Location{0, 1})
	WriteBlock(db, zone1Block2, common.ZONE_CTX)

	ancestor := FindCommonAncestor(db, zone0Block.Header(), zone1Block2.Header(), common.ZONE_CTX)
	if ancestor.Hash() != regionBlock.Hash() {
		t.Fatalf("Common ancestor not found: %v", ancestor)
	}
}

func TestBlockStorage(t *testing.T) {
	db := NewMemoryDatabase()

	location := common.Location{0, 0}
	block := types.EmptyBlock()
	block.Header().SetNumber(big.NewInt(1), common.ZONE_CTX)
	hash := block.Hash()
	number := block.NumberU64(common.ZONE_CTX)

	if entry := ReadBlock(db, hash, number, location); entry != nil {
		t.Fatalf("Non existent block returned: %v", entry)
	}

	WriteBlock(db, block, common.ZONE_CTX)

	if entry := ReadBlock(db, hash, number, location); entry == nil {
		t.Fatalf("Stored block not found: %v", entry)
	}

	DeleteBlock(db, hash, number)

	if entry := ReadBlock(db, hash, number, common.Location{0, 0}); entry != nil {
		t.Fatalf("Deleted block returned: %v", entry)
	}

	WriteBlock(db, block, common.ZONE_CTX)

	DeleteBlockWithoutNumber(db, hash, number)

	if entry := ReadHeaderNumber(db, hash); *entry != number {
		t.Fatalf("Failed to return block number: %v", entry)

	}
}

func TestBadBlockStorage(t *testing.T) {
	db := NewMemoryDatabase()

	location := common.Location{0, 0}
	block := types.EmptyBlock()
	block.Header().SetNumber(big.NewInt(1), common.ZONE_CTX)
	block.Header().SetLocation(location)
	block.Header().SetCoinbase(common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{0, 0}))
	hash := block.Hash()

	if entry := ReadBadBlock(db, hash, location); entry != nil {
		t.Fatalf("Non existent bad block returned: %v", entry)
	}

	WriteBadBlock(db, block, location)

	if entry := ReadBadBlock(db, hash, location); entry.Hash() != block.Hash() {
		t.Fatalf("Stored bad block not found: %v", entry)
	}

	block2 := types.EmptyBlock()
	block2.Header().SetNumber(big.NewInt(2), common.ZONE_CTX)
	block2.Header().SetLocation(location)
	block2.Header().SetCoinbase(common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{0, 0}))

	WriteBadBlock(db, block2, location)

	if entry := ReadAllBadBlocks(db, location); entry[1].Hash() != block.Hash() || entry[0].Hash() != block2.Hash() {
		t.Fatalf("Stored bad block not found: %v", entry)
	}

	DeleteBadBlocks(db)

	if entry := ReadAllBadBlocks(db, location); len(entry) != 0 {
		t.Fatalf("Failed to delete bad blocks: %v", entry)
	}
}

func TestPendingEtxStorage(t *testing.T) {
	db := NewMemoryDatabase()

	header := types.EmptyHeader()
	header.SetLocation(common.Location{0, 0})

	address := common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{1, 1})

	transaction := types.NewTx(&types.ExternalTx{
		ChainID:           new(big.Int).SetUint64(1),
		OriginatingTxHash: common.Hash{1},
		ETXIndex:          uint16(1),
		Gas:               uint64(1),
		To:                &address,
		Value:             new(big.Int).SetUint64(1),
		Data:              []byte{},
		AccessList:        nil,
		Sender:            address,
	})

	if entry := ReadPendingEtxs(db, header.Hash()); entry != nil {
		t.Fatalf("Non existent pending etx returned: %v", entry)
	}

	etx := types.PendingEtxs{
		Header: header,
		Etxs:   types.Transactions{transaction},
	}

	WritePendingEtxs(db, etx)

	test := ReadPendingEtxs(db, header.Hash())
	t.Log(test)

	if entry := ReadPendingEtxs(db, header.Hash()); entry.Etxs[0].Hash() != transaction.Hash() {
		t.Fatalf("Stored pending etx not found: %v", entry)
	}

	DeletePendingEtxs(db, header.Hash())

	if entry := ReadPendingEtxs(db, header.Hash()); entry != nil {
		t.Fatalf("Deleted pending etx returned: %v", entry)
	}
}

func TestPendingEtxsRollupStorage(t *testing.T) {
	db := NewMemoryDatabase()

	location := common.Location{0, 0}
	header := types.EmptyHeader()
	header.SetLocation(location)

	etxRollup := types.PendingEtxsRollup{
		Header:     header,
		EtxsRollup: types.Transactions{},
	}

	if entry := ReadPendingEtxsRollup(db, header.Hash(), location); entry != nil {
		t.Fatalf("Non existent pending etx rollup returned: %v", entry)
	}

	WritePendingEtxsRollup(db, etxRollup)

	if entry := ReadPendingEtxsRollup(db, header.Hash(), location); entry == nil {
		t.Fatalf("Stored pending etx rollup not found: %v", entry)
	}

	DeletePendingEtxsRollup(db, header.Hash())

	if entry := ReadPendingEtxsRollup(db, header.Hash(), location); entry != nil {
		t.Fatalf("Deleted pending etx rollup returned: %v", entry)
	}
}

func TestManifestStorage(t *testing.T) {
	db := NewMemoryDatabase()

	hash := common.Hash{1}
	manifest := types.BlockManifest{common.Hash{2}}

	if entry := ReadManifest(db, hash); entry != nil {
		t.Fatalf("Non existent manifest returned: %v", entry)
	}

	WriteManifest(db, hash, manifest)

	if entry := ReadManifest(db, hash); entry[0] != manifest[0] {
		t.Fatalf("Stored manifest not found: %v", entry)
	}

	DeleteManifest(db, hash)

	if entry := ReadManifest(db, hash); entry != nil {
		t.Fatalf("Deleted manifest returned: %v", entry)
	}
}

func TestBloomStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadBloom(db, common.Hash{1}); entry != nil {
		t.Fatalf("Non existent bloom returned: %v", entry)
	}

	bloom := types.BytesToBloom([]byte{1})
	WriteBloom(db, common.Hash{1}, bloom)

	if entry := ReadBloom(db, common.Hash{1}); *entry != bloom {
		t.Fatalf("Stored bloom not found: %v", entry)
	}

	DeleteBloom(db, common.Hash{1}, 1)

	if entry := ReadBloom(db, common.Hash{1}); entry != nil {
		t.Fatalf("Deleted bloom returned: %v", entry)
	}
}

func TestBadHashesListStorage(t *testing.T) {
	db := NewMemoryDatabase()

	if entry := ReadBadHashesList(db); entry != nil {
		t.Fatalf("Non existent bad hashes list returned: %v", entry)
	}

	hashes := common.Hashes{{1}, {2}}
	WriteBadHashesList(db, hashes)

	if entry := ReadBadHashesList(db); entry[0] != hashes[0] || entry[1] != hashes[1] {
		t.Fatalf("Stored bad hashes list not found: %v", entry)
	}

	DeleteBadHashesList(db)

	if entry := ReadBadHashesList(db); entry != nil {
		t.Fatalf("Deleted bad hashes list returned: %v", entry)
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

func TestAddressUtxosStorage(t *testing.T) {
	db := NewMemoryDatabase()
	address := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})

	if entry := ReadAddressUtxos(db, address); len(entry) != 0 {
		t.Fatalf("Non existent address utxos returned: %v", entry)
	}

	utxos := []*types.UtxoEntry{{Denomination: 1, Address: address.Bytes()}}

	WriteAddressUtxos(db, address, utxos)

	if entry := ReadAddressUtxos(db, address); entry[0].Denomination != utxos[0].Denomination {
		t.Fatalf("Stored address utxo not found: %v", entry)
	}

	DeleteAddressUtxos(db, address)

	if entry := ReadAddressUtxos(db, address); len(entry) != 0 {
		t.Fatalf("Deleted address utxos returned: %v", entry)
	}
}
