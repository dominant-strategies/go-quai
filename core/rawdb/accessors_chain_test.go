package rawdb

import (
	"math/big"
	reflect "reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
)

func TestCanonicalHashStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

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
	db := NewMemoryDatabase(log.Global)

	header := types.EmptyHeader()
	header.SetParentHash(common.Hash{1}, common.REGION_CTX)
	header.SetNumber(big.NewInt(1), common.REGION_CTX)

	woBody := types.EmptyWorkObjectBody()
	woBody.SetHeader(header)

	woHeader := types.NewWorkObjectHeader(header.Hash(), common.Hash{1}, big.NewInt(1), big.NewInt(30000), types.EmptyRootHash, types.BlockNonce{23}, 1, common.LocationFromAddressBytes([]byte{0x01, 0x01}))

	wo := types.NewWorkObject(woHeader, woBody, nil)

	if HasHeader(db, header.Hash(), common.REGION_CTX) {
		t.Fatal("Non existent header returned")
	}
	if entry := ReadHeader(db, wo.Hash()); entry != nil {
		t.Fatalf("Non existent header returned: %v", entry)
	}

	WriteWorkObject(db, wo.Hash(), wo, types.BlockObject, common.REGION_CTX)
	t.Log("Header Hash stored", header.Hash())

	if HasHeader(db, header.Hash(), header.NumberU64(common.REGION_CTX)) {
		t.Fatal("HasHeader returned false")
	}

	if entry := ReadHeader(db, wo.Hash()); entry == nil {
		t.Fatalf("Stored header not found")
	} else if entry.Hash() != wo.Hash() {
		t.Fatalf("Retrieved header mismatch: have %v, want %v", entry, header)
	}

	// Verify header number
	if number := ReadHeaderNumber(db, wo.Hash()); *number != uint64(1) {
		t.Fatalf("Retrieved header number mismatch: have %v, want %v", number, big.NewInt(1))
	}
	// Modify the header number and check if it was updated
	WriteHeaderNumber(db, wo.Hash(), uint64(2))
	if number := ReadHeaderNumber(db, wo.Hash()); *number != uint64(2) {
		t.Fatalf("Retrieved header number mismatch: have %v, want %v", number, big.NewInt(1))
	}

	DeleteHeaderNumber(db, header.Hash())
	if number := ReadHeaderNumber(db, header.Hash()); number != nil {
		t.Fatalf("Deleted header number returned: %v", number)
	}

	// Delete the header and verify the execution
	DeleteHeader(db, header.Hash(), header.Number(common.REGION_CTX).Uint64())
	if entry := ReadHeader(db, header.Hash()); entry != nil {
		t.Fatalf("Deleted header returned: %v", entry)
	}
}

func TestHeadHeaderStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

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
	db := NewMemoryDatabase(log.Global)

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
	db := NewMemoryDatabase(log.Global)

	if entry := ReadLastPivotNumber(db); entry != nil {
		t.Fatalf("Non existent last pivot number returned: %v", entry)
	}

	WriteLastPivotNumber(db, uint64(1))

	if entry := ReadLastPivotNumber(db); *entry != uint64(1) {
		t.Fatalf("Stored last pivot not found: %v", entry)
	}
}

func TestFastTrieStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadFastTrieProgress(db); entry != 0 {
		t.Fatalf("Non existent fast trie returned: %v", entry)
	}

	WriteFastTrieProgress(db, uint64(1))

	if entry := ReadFastTrieProgress(db); entry != uint64(1) {
		t.Fatalf("Stored fast trie not found: %v", entry)
	}
}

func TestTxIndexTailStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadTxIndexTail(db); entry != nil {
		t.Fatalf("Non existent tx index returned: %v", entry)
	}

	WriteTxIndexTail(db, uint64(1))

	if entry := ReadTxIndexTail(db); *entry != uint64(1) {
		t.Fatalf("Stored tx index not found: %v", entry)
	}
}

func TestFastTxLookupStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadFastTxLookupLimit(db); entry != nil {
		t.Fatalf("Non existent fast tx lookup returned: %v", entry)
	}

	WriteFastTxLookupLimit(db, uint64(1))

	if entry := ReadFastTxLookupLimit(db); *entry != uint64(1) {
		t.Fatalf("Stored fast tx lookup not found: %v", entry)
	}
}

func TestPbCacheStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadPbCacheBody(db, common.Hash{1}); entry != nil {
		t.Fatalf("Non existent pb cache body returned: %v", entry)
	}

	// Create a test header to move around the database and make sure it's really new
	woBody := &types.WorkObjectBody{}
	woBody.SetTransactions([]*types.Transaction{})
	woBody.SetExtTransactions([]*types.Transaction{})
	woBody.SetHeader(types.EmptyHeader())

	woHeader := types.NewWorkObjectHeader(types.EmptyRootHash, types.EmptyRootHash, big.NewInt(11), big.NewInt(30000), types.EmptyRootHash, types.BlockNonce{23}, 1, common.LocationFromAddressBytes([]byte{0x01, 0x01}))
	wo := types.NewWorkObject(woHeader, woBody, nil)
	WritePbCacheBody(db, common.Hash{1}, wo)

	if entry := ReadPbCacheBody(db, common.Hash{1}); entry.Hash() != wo.Hash() {
		t.Fatalf("Stored pb cache body not found: %v", entry)
	}

	DeletePbCacheBody(db, common.Hash{1})

	if entry := ReadPbCacheBody(db, common.Hash{1}); entry != nil {
		t.Fatalf("Deleted pb cache body returned: %v", entry)
	}
}

func TestPbBodyKeysStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

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

func TestTerminiStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

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
	db := NewMemoryDatabase(log.Global)

	if entry := ReadPendingHeader(db, common.Hash{1}); entry != nil {
		t.Fatalf("Non existent pending header returned: %v", entry)
	}

	emptyHeader := types.EmptyWorkObject(common.ZONE_CTX)

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
	db := NewMemoryDatabase(log.Global)

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
	db := NewMemoryDatabase(log.Global)

	// Create a test etxSet to move around the database and make sure it's really new
	etxSet := types.NewEtxSet()
	hash := common.Hash{1}
	var number uint64 = 0
	if entry := ReadEtxSet(db, hash, number); entry != nil {
		t.Fatalf("Non existent etxSet returned: %v", entry)
	}
	t.Log("EtxSet Hash stored", hash)
	// Write and verify the etxSet in the database
	WriteEtxSet(db, hash, 0, etxSet)
	if entry := ReadEtxSet(db, hash, number); entry == nil {
		t.Fatalf("Stored etxSet not found with hash %s", hash)
	}
	// Delete the etxSet and verify the execution
	DeleteEtxSet(db, hash, number)
	if entry := ReadEtxSet(db, hash, number); entry != nil {
		t.Fatalf("Deleted etxSet returned: %v", entry)
	}
}

func TestHeadsHashesStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

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

func TestBlockHashesIterator(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

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
			//TODO: FILL UP DATABASE
			blockHeader := types.EmptyHeader()
			blockBody := types.EmptyWorkObjectBody()
			blockBody.SetHeader(blockHeader)
			blockWoHeader := types.NewWorkObjectHeader(blockHeader.Hash(), types.EmptyHash, big.NewInt(int64(i+1)), big.NewInt(3000), types.EmptyRootHash, types.BlockNonce{23}, 2, common.Location{0, byte(j)})
			block := types.NewWorkObject(blockWoHeader, blockBody, nil)
			WriteWorkObject(db, block.Hash(), block, types.BlockObject, common.ZONE_CTX)
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
	db := NewMemoryDatabase(log.Global)

	// Write region block
	regionHeader := types.EmptyHeader()
	regionHeader.SetNumber(big.NewInt(1), common.REGION_CTX)
	regionBody := types.EmptyWorkObjectBody()
	regionBody.SetHeader(regionHeader)
	regionWoHeader := types.NewWorkObjectHeader(regionHeader.Hash(), types.EmptyRootHash, big.NewInt(1), big.NewInt(3000), types.EmptyRootHash, types.BlockNonce{23}, 1, common.Location{0})
	regionBlock := types.NewWorkObject(regionWoHeader, regionBody, nil)
	WriteWorkObject(db, regionBlock.Hash(), regionBlock, types.BlockObject, common.REGION_CTX)

	//Write one block on zone 0 context
	zone0Header := types.EmptyHeader()
	zone0Body := types.EmptyWorkObjectBody()
	zone0Body.SetHeader(zone0Header)
	zone0WoHeader := types.NewWorkObjectHeader(zone0Header.Hash(), regionBlock.Hash(), big.NewInt(2), big.NewInt(3000), types.EmptyRootHash, types.BlockNonce{23}, 2, common.Location{0, 0})
	zone0Block := types.NewWorkObject(zone0WoHeader, zone0Body, nil)
	WriteWorkObject(db, zone0Block.Hash(), zone0Block, types.BlockObject, common.ZONE_CTX)

	//Write two blocks on zone 1 context
	zone1Header1 := types.EmptyHeader()
	zone1Body1 := types.EmptyWorkObjectBody()
	zone1Body1.SetHeader(zone1Header1)
	zone1WoHeader1 := types.NewWorkObjectHeader(zone1Header1.Hash(), regionBlock.Hash(), big.NewInt(2), big.NewInt(3000), types.EmptyRootHash, types.BlockNonce{23}, 2, common.Location{0, 1})
	zone1Block1 := types.NewWorkObject(zone1WoHeader1, zone1Body1, nil)
	WriteWorkObject(db, zone1Block1.Hash(), zone1Block1, types.BlockObject, common.ZONE_CTX)

	zone1Header2 := types.EmptyHeader()
	zone1Body2 := types.EmptyWorkObjectBody()
	zone1Body2.SetHeader(zone1Header2)
	zone1WoHeader2 := types.NewWorkObjectHeader(zone1Header2.Hash(), zone1Block1.Hash(), big.NewInt(3), big.NewInt(3000), types.EmptyRootHash, types.BlockNonce{23}, 3, common.Location{0, 1})
	zone1Block2 := types.NewWorkObject(zone1WoHeader2, zone1Body2, nil)
	WriteWorkObject(db, zone1Block2.Hash(), zone1Block2, types.BlockObject, common.ZONE_CTX)

	ancestor := FindCommonAncestor(db, zone0Block, zone1Block2, common.ZONE_CTX)
	if ancestor == nil || ancestor.Hash() != regionBlock.Hash() {
		t.Fatalf("Common ancestor not found: %v", ancestor)
	}
}

func TestPendingEtxStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	wo := types.EmptyWorkObject(common.ZONE_CTX)

	address := common.HexToAddress("0x0000000000000000000000000000000000000000", common.Location{1, 1})

	transaction := types.NewTx(&types.ExternalTx{
		OriginatingTxHash: common.Hash{1},
		ETXIndex:          uint16(1),
		Gas:               uint64(1),
		To:                &address,
		Value:             new(big.Int).SetUint64(1),
		Data:              []byte{},
		AccessList:        nil,
		Sender:            address,
	})

	if entry := ReadPendingEtxs(db, wo.Hash()); entry != nil {
		t.Fatalf("Non existent pending etx returned: %v", entry)
	}

	etx := types.PendingEtxs{
		Header: wo,
		Etxs:   types.Transactions{transaction},
	}

	WritePendingEtxs(db, etx)

	if entry := ReadPendingEtxs(db, wo.Hash()); entry.Etxs[0].Hash() != transaction.Hash() {
		t.Fatalf("Stored pending etx not found: %v", entry)
	}

	DeletePendingEtxs(db, wo.Hash())

	if entry := ReadPendingEtxs(db, wo.Hash()); entry != nil {
		t.Fatalf("Deleted pending etx returned: %v", entry)
	}
}

func TestPendingEtxsRollupStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	header := types.EmptyWorkObject(common.ZONE_CTX)

	etxRollup := types.PendingEtxsRollup{
		Header:     header,
		EtxsRollup: types.Transactions{},
	}

	if entry := ReadPendingEtxsRollup(db, header.Hash()); entry != nil {
		t.Fatalf("Non existent pending etx rollup returned: %v", entry)
	}

	WritePendingEtxsRollup(db, etxRollup)

	if entry := ReadPendingEtxsRollup(db, header.Hash()); entry == nil {
		t.Fatalf("Stored pending etx rollup not found: %v", entry)
	}

	DeletePendingEtxsRollup(db, header.Hash())

	if entry := ReadPendingEtxsRollup(db, header.Hash()); entry != nil {
		t.Fatalf("Deleted pending etx rollup returned: %v", entry)
	}
}

func TestManifestStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

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
	db := NewMemoryDatabase(log.Global)

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
	db := NewMemoryDatabase(log.Global)

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
	db := NewMemoryDatabase(log.Global)
	hash := common.Hash{1}

	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	inner := &types.QuaiTx{
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

	if entry := ReadInboundEtxs(db, hash); entry != nil {
		t.Fatalf("Non existent inbound etxs returned: %v", entry)
	}
	t.Log("Inbound InboundEtxs stored", inboundEtxs)
	// Write and verify the inboundEtxs in the database
	WriteInboundEtxs(db, hash, inboundEtxs)
	if entry := ReadInboundEtxs(db, hash); entry == nil {
		t.Fatalf("Stored InboundEtxs not found with hash %s", hash)
	} else {
		t.Log("InboundEtxs", entry)
		reflect.DeepEqual(inboundEtxs, entry)
	}
	// Delete the inboundEtxs and verify the execution
	DeleteInboundEtxs(db, hash)
	if entry := ReadInboundEtxs(db, hash); entry != nil {
		t.Fatalf("Deleted InboundEtxs returned: %v", entry)
	}
}

// Tests block header storage and retrieval operations.
func TestWorkObjectStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	// Create a test header to move around the database and make sure it's really new
	woBody := &types.WorkObjectBody{}
	woBody.SetTransactions([]*types.Transaction{})
	woBody.SetExtTransactions([]*types.Transaction{})
	woBody.SetHeader(types.EmptyHeader())
	header := types.NewWorkObject(types.NewWorkObjectHeader(types.EmptyRootHash, types.EmptyRootHash, big.NewInt(11), big.NewInt(30000), types.EmptyRootHash, types.BlockNonce{23}, 1, common.LocationFromAddressBytes([]byte{0x01, 0x01})), woBody, nil)

	if entry := ReadWorkObject(db, header.Hash(), types.BlockObject); entry != nil {
		t.Fatalf("Non existent header returned: %v", entry)
	}
	t.Log("Header Hash stored", header.Hash())
	// Write and verify the header in the database
	WriteWorkObject(db, header.Hash(), header, types.BlockObject, common.ZONE_CTX)
	entry := ReadWorkObject(db, header.Hash(), types.BlockObject)
	if entry == nil {
		t.Fatalf("Stored header not found with hash %s", entry.Hash())
	} else if entry.Hash() != header.Hash() {
		t.Fatalf("Retrieved header mismatch: have %v, want %v", entry, header)
	}
	// Delete the header and verify the execution
	DeleteWorkObject(db, header.Hash(), header.Number(common.ZONE_CTX).Uint64(), types.BlockObject)
	if entry := ReadWorkObject(db, header.Hash(), types.BlockObject); entry != nil {
		t.Fatalf("Deleted header returned: %v", entry)

	}
}
