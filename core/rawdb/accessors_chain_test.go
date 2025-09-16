package rawdb

import (
	"bytes"
	"math/big"
	"os"
	reflect "reflect"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
)

// testAuxPow creates a test AuxPow with default values
func testAuxPow() *types.AuxPow {
	ravencoinHeader := &types.RavencoinBlockHeader{
		Version:        4,
		HashPrevBlock:  types.EmptyRootHash,
		HashMerkleRoot: types.EmptyRootHash,
		Time:           34545,
		Bits:           0x1d00ffff,
		Nonce64:        367899,
		Height:         298899,
		MixHash:        types.EmptyRootHash,
	}
	header := types.NewAuxPowHeader(ravencoinHeader)
	coinbaseOut := []byte{0x76, 0xa9, 0x14, 0x89, 0xab, 0xcd, 0xef, 0x88, 0xac}
	coinbaseTx := types.NewAuxPowCoinbaseTx(types.Kawpow, 100, coinbaseOut, types.EmptyRootHash, 123)

	return types.NewAuxPow(
		types.Kawpow,
		header,
		[]byte{},
		[]byte{0x03, 0x04},
		[][]byte{},
		coinbaseTx,
	)
}

func testScryptPowShareDiffAndCount() *types.PowShareDiffAndCount {
	return types.NewPowShareDiffAndCount(big.NewInt(1000), big.NewInt(20), big.NewInt(21))
}

func testShaPowShareDiffAndCount() *types.PowShareDiffAndCount {
	return types.NewPowShareDiffAndCount(big.NewInt(2000), big.NewInt(29), big.NewInt(30))
}

func testWorkObjectHeader(headerHash common.Hash) *types.WorkObjectHeader {
	workObjectHeader := types.NewWorkObjectHeader(headerHash,
		common.Hash{1},
		big.NewInt(1),
		big.NewInt(30000),
		big.NewInt(42),
		types.EmptyRootHash,
		types.BlockNonce{23},
		1,
		1,
		common.LocationFromAddressBytes([]byte{0x01, 0x01}),
		common.BytesToAddress([]byte{1},
			common.Location{0, 0}),
		[]byte{0x01, 0x02},
		testAuxPow(),
		testScryptPowShareDiffAndCount(),
		testShaPowShareDiffAndCount(),
		big.NewInt(100),
		big.NewInt(2300),
		big.NewInt(1000))
	return workObjectHeader
}

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

	woHeader := testWorkObjectHeader(header.Hash())

	wo := types.NewWorkObject(woHeader, woBody, nil)

	if HasHeader(db, wo.Hash(), common.REGION_CTX) {
		t.Fatal("Non existent header returned")
	}
	if entry := ReadHeader(db, wo.NumberU64(common.REGION_CTX), wo.Hash()); entry != nil {
		t.Fatalf("Non existent header returned: %v", entry)
	}

	WriteWorkObject(db, wo.Hash(), wo, types.BlockObject, common.REGION_CTX)
	t.Log("Header Hash stored", header.Hash())

	if !HasHeader(db, wo.Hash(), header.NumberU64(common.REGION_CTX)) {
		t.Fatal("HasHeader returned false")
	}

	if entry := ReadHeader(db, wo.NumberU64(common.REGION_CTX), wo.Hash()); entry == nil {
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
	if entry := ReadHeader(db, header.NumberU64(common.REGION_CTX), header.Hash()); entry != nil {
		t.Fatalf("Deleted header returned: %v", entry)
	}
}

func TestProcessedStateStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadProcessedState(db, common.Hash{1}); entry != false {
		t.Fatal("False positive on ReadProcessedState")
	}

	WriteProcessedState(db, common.Hash{1})

	if entry := ReadProcessedState(db, common.Hash{1}); entry != true {
		t.Fatal("False negative on ReadProcessedState")
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

func TestHeadBlockHashStorage(t *testing.T) {
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

func TestHeadBlockStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadHeadBlock(db); entry != nil {
		t.Fatalf("Non existent head block returned: %v", entry)
	}

	hash := common.Hash{1}
	WriteHeadBlockHash(db, hash)

	if entry := ReadHeadBlock(db); entry != nil {
		t.Fatalf("Non existent head block returned: %v", entry)
	}

	blockNumber := uint64(1)
	WriteHeaderNumber(db, hash, blockNumber)

	wo := types.EmptyWorkObject(common.ZONE_CTX)
	wo.WorkObjectHeader().SetPrimaryCoinbase(common.BytesToAddress([]byte{1}, common.Location{0, 0}))

	WriteWorkObject(db, hash, wo, types.BlockObject, common.ZONE_CTX)

	if entry := ReadHeadBlock(db); entry == nil || entry.Hash() != wo.Hash() {
		t.Fatalf("Failed to read head block: expeted: %v \n found: %v", wo, entry)
	}

}

func TestPbCacheStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadPbCacheBody(db, common.Hash{1}); entry != nil {
		t.Fatalf("Non existent pb cache body returned: %v", entry)
	}

	// Create a test header to move around the database and make sure it's really new
	woBody := types.EmptyWorkObjectBody()
	woBody.SetHeader(types.EmptyHeader())

	woHeader := testWorkObjectHeader(types.EmptyHeader().Hash())
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

func TestBestPendingHeaderStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadBestPendingHeader(db); entry != nil {
		t.Fatalf("Non existent pending header returned: %v", entry)
	}

	emptyHeader := types.EmptyWorkObject(common.ZONE_CTX)

	emptyHeader.SetTx(nil)

	WriteBestPendingHeader(db, emptyHeader)

	if entry := ReadBestPendingHeader(db); entry == nil {
		t.Fatalf("Stored pb  bodyKeys not found: %v", entry)
	}

	DeleteBestPendingHeader(db)

	if entry := ReadBestPendingHeader(db); entry != nil {
		t.Fatalf("Deleted pb  bodyKeys returned: %v", entry)
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
			blockHeader := types.EmptyHeader()
			blockBody := types.EmptyWorkObjectBody()
			blockBody.SetHeader(blockHeader)
			blockWoHeader := testWorkObjectHeader(blockHeader.Hash())
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
	regionWoHeader := testWorkObjectHeader(regionHeader.Hash())
	regionWoHeader.SetNumber(big.NewInt(1))
	regionBlock := types.NewWorkObject(regionWoHeader, regionBody, nil)
	WriteWorkObject(db, regionBlock.Hash(), regionBlock, types.BlockObject, common.REGION_CTX)

	//Write one block on zone 0 context
	zone0Body := types.EmptyWorkObjectBody()
	zone0Body.SetHeader(regionHeader)
	zone0WoHeader := testWorkObjectHeader(regionHeader.Hash())
	zone0WoHeader.SetParentHash(regionBlock.Hash())
	zone0WoHeader.SetNumber(big.NewInt(2))
	zone0Block := types.NewWorkObject(zone0WoHeader, zone0Body, nil)
	WriteWorkObject(db, zone0Block.Hash(), zone0Block, types.BlockObject, common.ZONE_CTX)

	//Write two blocks on zone 1 context
	zone1Header1 := types.EmptyHeader()
	zone1Body1 := types.EmptyWorkObjectBody()
	zone1Body1.SetHeader(zone1Header1)
	zone1WoHeader1 := testWorkObjectHeader(zone1Header1.Hash())
	zone1WoHeader1.SetParentHash(regionBlock.Hash())
	zone1WoHeader1.SetNumber(big.NewInt(2))
	zone1Block1 := types.NewWorkObject(zone1WoHeader1, zone1Body1, nil)
	WriteWorkObject(db, zone1Block1.Hash(), zone1Block1, types.BlockObject, common.ZONE_CTX)

	zone1Header2 := types.EmptyHeader()
	zone1Body2 := types.EmptyWorkObjectBody()
	zone1Body2.SetHeader(zone1Header2)
	zone1WoHeader2 := testWorkObjectHeader(zone1Header2.Hash())
	zone1WoHeader2.SetParentHash(zone1Block1.Hash())
	zone1WoHeader2.SetNumber(big.NewInt(3))
	zone1WoHeader2.SetAuxPow(nil)
	zone1Block2 := types.NewWorkObject(zone1WoHeader2, zone1Body2, nil)
	WriteWorkObject(db, zone1Block2.Hash(), zone1Block2, types.BlockObject, common.ZONE_CTX)

	ancestor, err := FindCommonAncestor(db, zone0Block, zone1Block2, common.ZONE_CTX)
	if err != nil {
		t.Fatalf("Error finding common ancestor: %v", err)
	}
	if ancestor == nil {
		t.Fatalf("Common ancestor not found: %v", ancestor)
	}
	if ancestor.Hash() != regionBlock.Hash() {
		t.Fatalf("Common ancestor hash mismatch: got %v, want %v", ancestor.Hash(), regionBlock.Hash())
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
		Header:       wo,
		OutboundEtxs: types.Transactions{transaction},
	}

	WritePendingEtxs(db, etx)

	if entry := ReadPendingEtxs(db, wo.Hash()); entry.OutboundEtxs[0].Hash() != transaction.Hash() {
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
		GasPrice:   new(big.Int).SetUint64(0),
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

// TestEmptyInboundEtxsStorage writes a nil and an empty transactions and makes
// sure that the code doesnt panic and returns an empty list of transactions in
// both cases
func TestEmptyInboundEtxsStorage(t *testing.T) {
	var inboundEtxs types.Transactions = nil

	db := NewMemoryDatabase(log.Global)
	hash := common.Hash{1}

	WriteInboundEtxs(db, hash, inboundEtxs)

	readInboundEtxs := ReadInboundEtxs(db, hash)
	require.Equal(t, types.Transactions{}, readInboundEtxs)

	inboundEtxs = types.Transactions{}

	db = NewMemoryDatabase(log.Global)
	hash = common.Hash{1}

	WriteInboundEtxs(db, hash, inboundEtxs)

	readInboundEtxs = ReadInboundEtxs(db, hash)
	require.Equal(t, types.Transactions{}, readInboundEtxs)

	// In case nothing is written for the hash, nil is returned
	db = NewMemoryDatabase(log.Global)
	hash = common.Hash{1}

	readInboundEtxs = ReadInboundEtxs(db, hash)
	require.Equal(t, types.Transactions(nil), readInboundEtxs)
}

func TestHasBody(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	wo := createTestWorkObject()

	if HasBody(db, wo.Hash(), wo.NumberU64(common.ZONE_CTX)) {
		t.Fatalf("HasBody returned true on unexistent block")
	}

	WriteWorkObject(db, wo.Hash(), wo, types.BlockObject, common.ZONE_CTX)

	if !HasBody(db, wo.Hash(), wo.NumberU64(common.ZONE_CTX)) {
		t.Fatalf("HasBody returned false after writing block")
	}

	DeleteWorkObject(db, wo.Hash(), wo.Number(common.ZONE_CTX).Uint64(), types.BlockObject)

	if HasBody(db, wo.Hash(), wo.NumberU64(common.ZONE_CTX)) {
		t.Fatalf("HasBody returned true on deleted block")
	}
}

func TestWorkObjectStorage(t *testing.T) {
	t.Run("BlockObjectTest", func(t *testing.T) {
		db := NewMemoryDatabase(log.Global)
		testWorkObject(t, db, createTestWorkObject(), types.BlockObject)
	})
}

func createTestWorkObject() *types.WorkObject {
	woBody := types.EmptyWorkObjectBody()
	woBody.SetHeader(types.EmptyHeader())

	woHeader := testWorkObjectHeader(types.EmptyHeader().Hash())
	return types.NewWorkObject(woHeader, woBody, nil)
}

func testWorkObject(t *testing.T, db ethdb.Database, wo *types.WorkObject, woType types.WorkObjectView) {
	if entry := ReadWorkObject(db, wo.NumberU64(common.ZONE_CTX), wo.Hash(), woType); entry != nil {
		t.Fatalf("Non existent header returned: %v", entry)
	}

	// Write and verify the header in the database
	WriteWorkObject(db, wo.Hash(), wo, woType, common.ZONE_CTX)
	t.Log("Wo Hash stored", wo.Hash())
	entry := ReadWorkObject(db, wo.NumberU64(common.ZONE_CTX), wo.Hash(), woType)
	if entry == nil {
		t.Fatalf("Stored header not found with hash")
	} else if entry.Hash() != wo.Hash() {
		t.Fatalf("Retrieved header mismatch: have %v, want %v", entry, wo)
	}
	t.Log("Successfuly read WorkObject")
	// Delete the header and verify the execution
	DeleteWorkObject(db, wo.Hash(), wo.Number(common.ZONE_CTX).Uint64(), woType)
	if entry := ReadWorkObject(db, wo.NumberU64(common.ZONE_CTX), wo.Hash(), woType); entry != nil {
		t.Fatalf("Deleted header returned: %v", entry)

	}
	t.Log("Deleted WorkObject")
	//Write work object again to test delete without number
	WriteWorkObject(db, wo.Hash(), wo, woType, common.ZONE_CTX)
	t.Log("Wo Hash stored", wo.Hash())
	DeleteBlockWithoutNumber(db, wo.Hash(), wo.NumberU64(common.ZONE_CTX), woType)
	if entry := ReadHeaderNumber(db, wo.Hash()); *entry != wo.NumberU64(common.ZONE_CTX) {
		t.Fatalf("Wrong header number returned: have %v, want %v", *entry, wo.NumberU64(common.ZONE_CTX))
	}
	t.Log("Deleted Block without number")
	if entry := ReadWorkObject(db, wo.NumberU64(common.ZONE_CTX), wo.Hash(), woType); entry != nil {
		t.Fatalf("Deleted header returned: %v", entry)
	}
}

func createTransaction(nonce uint64) *types.Transaction {
	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	inner := &types.QuaiTx{
		ChainID:    new(big.Int).SetUint64(1),
		Nonce:      nonce,
		GasPrice:   new(big.Int).SetUint64(0),
		Gas:        uint64(0),
		To:         &to,
		Value:      new(big.Int).SetUint64(0),
		Data:       []byte{},
		AccessList: types.AccessList{},
		V:          new(big.Int).SetUint64(0),
		R:          new(big.Int).SetUint64(0),
		S:          new(big.Int).SetUint64(0),
	}
	return types.NewTx(inner)
}

func TestReceiptsStorage(t *testing.T) {
	// Create a buffer to test Log calls
	logBuf := bytes.Buffer{}
	log.Global.SetOutput(&logBuf)

	db := NewMemoryDatabase(log.Global)

	tx1 := createTransaction(1)
	tx2 := createTransaction(2)
	receipts := createReceipts(types.Transactions{tx1, tx2})

	hash := receipts[0].BlockHash

	blockNumber := uint64(0)

	if entry := ReadReceipts(db, hash, blockNumber, &params.ChainConfig{}); entry != nil {
		t.Fatalf("Non existent receipts returned: %v", entry)
	}

	WriteReceipts(db, hash, blockNumber, receipts)

	// Test Read receipts with missing body
	if entry := ReadReceipts(db, hash, blockNumber, &params.ChainConfig{}); entry != nil {
		t.Fatalf("Receipt without body returned: %v", entry)
	}
	if !strings.Contains(logBuf.String(), "Missing body but have receipt") {
		t.Errorf("Expected log to contain 'Missing body but have receipt', got %s", logBuf.String())
	}

	// Write Block
	writeBlockForReceipts(db, hash, types.Transactions{tx1, tx2})

	if entry := ReadReceipts(db, hash, blockNumber, &params.ChainConfig{}); len(entry) != 2 {
		t.Fatal("Stored receipts not found")
	}

	DeleteReceipts(db, hash, blockNumber)

	if entry := ReadReceipts(db, hash, blockNumber, &params.ChainConfig{}); entry != nil {
		t.Fatalf("Deleted receipts returned: %v", entry)
	}
}

func createReceipts(txs types.Transactions) types.Receipts {

	receipt1 := createReceipt(txs[0].Hash(), types.EmptyHash)

	receipt2 := createReceipt(txs[1].Hash(), types.EmptyHash)

	return types.Receipts{receipt1, receipt2}
}

func createReceipt(txHash common.Hash, blockHash common.Hash) *types.Receipt {
	receipt := types.NewReceipt([]byte{}, false, 55000)
	receipt.TxHash = txHash
	receipt.GasUsed = 55000
	receipt.BlockHash = blockHash
	receipt.BlockNumber = big.NewInt(11)
	return receipt
}

func TestAncientReceiptsStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	dataDir := os.TempDir() + "/testFreezer"
	freezerDb, err := NewDatabaseWithFreezer(db, dataDir, os.TempDir(), false, common.ZONE_CTX, log.Global, common.Location{0, 0})

	require.NoError(t, err)
	defer func() {
		err := freezerDb.Close()
		require.NoError(t, err)

		err = os.RemoveAll(dataDir)
		require.NoError(t, err)
	}()

	tx1 := createTransaction(1)
	tx2 := createTransaction(2)
	receipts := createReceipts(types.Transactions{tx1, tx2})
	hash := receipts[0].BlockHash

	freezerDb.AppendAncient(0, hash.Bytes(), receipts.Bytes(freezerDb.Logger()))

	txs := types.Transactions{tx1, tx2}
	writeBlockForReceipts(db, hash, txs)

	if entry := ReadReceipts(freezerDb, hash, 0, &params.ChainConfig{}); len(entry) != 2 {
		t.Fatal("Stored receipts not found")
	}

}

func createBlockWithTransactions(txs types.Transactions) *types.WorkObject {
	woBody := types.EmptyWorkObjectBody()
	woBody.SetHeader(types.EmptyHeader())
	woBody.SetTransactions(txs)
	return types.NewWorkObject(testWorkObjectHeader(types.EmptyHeader().Hash()), woBody, nil)
}

func writeBlockForReceipts(db ethdb.Database, hash common.Hash, txs types.Transactions) {
	wo := createBlockWithTransactions(txs)
	wo.SetNumber(big.NewInt(0), common.ZONE_CTX)
	WriteWorkObject(db, hash, wo, types.BlockObject, common.ZONE_CTX)
}

func TestInterlinkHashesStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	hash := common.Hash{1}
	if entry := ReadInterlinkHashes(db, hash); entry != nil {
		t.Fatalf("Non existent interlink hashes returned: %v", entry)
	}

	interlinkHashes := common.Hashes{{2}, {3}}

	WriteInterlinkHashes(db, hash, interlinkHashes)

	if entry := ReadInterlinkHashes(db, common.Hash{1}); entry[0] != interlinkHashes[0] || entry[1] != interlinkHashes[1] {
		t.Fatalf("Stored interlink hashes not found: %v", entry)
	}

	DeleteInterlinkHashes(db, common.Hash{1})

	if entry := ReadInterlinkHashes(db, common.Hash{1}); entry != nil {
		t.Fatalf("Deleted interlink hashes returned: %v", entry)
	}
}

func TestAddressOutpointsStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	address := "0x008aeeda4d805471df9b2a5b0f38a0c3bcba786b"
	address2 := "0x008b2a5b0f38a0c3bcba786ba3df9b2a5b0f38a0"

	if entry, err := ReadOutpointsForAddress(db, common.HexToAddress(address, db.Location())); len(entry) != 0 || err != nil {
		t.Fatalf("Non existent address outpoints returned: %v", entry)
	}

	outpoint := types.OutpointAndDenomination{
		TxHash:       common.Hash{1},
		Index:        uint16(1),
		Denomination: uint8(1),
		Lock:         big.NewInt(0),
	}

	outpoint2 := types.OutpointAndDenomination{
		TxHash:       common.Hash{2},
		Index:        uint16(1),
		Denomination: uint8(1),
		Lock:         big.NewInt(0),
	}

	addressOutpointMap := map[[20]byte][]*types.OutpointAndDenomination{
		common.HexToAddressBytes(address):  {&outpoint},
		common.HexToAddressBytes(address2): {&outpoint2},
	}

	WriteAddressOutpoints(db, addressOutpointMap)

	if entry, err := ReadOutpointsForAddress(db, common.HexToAddress(address, db.Location())); len(entry) == 0 || *entry[0] != outpoint || err != nil {
		require.Equal(t, outpoint, *entry[0])
	}

	if entry, err := ReadOutpointsForAddress(db, common.HexToAddress(address2, db.Location())); len(entry) == 0 || *entry[0] != outpoint2 || err != nil {
		require.Equal(t, outpoint2, *entry[0])
	}
}

func TestGenesisHashesStorage(t *testing.T) {
	db := NewMemoryDatabase(log.Global)

	if entry := ReadGenesisHashes(db); len(entry) != 0 {
		t.Fatalf("Non existent genesis hashes returned: %v", entry)
	}

	hashes := common.Hashes{{1}, {2}}
	WriteGenesisHashes(db, hashes)

	if entry := ReadGenesisHashes(db); entry[0] != hashes[0] || entry[1] != hashes[1] {
		t.Fatalf("Stored genesis hashes not found: %v", entry)
	}

	DeleteGenesisHashes(db)

	if entry := ReadGenesisHashes(db); len(entry) != 0 {
		t.Fatalf("Deleted genesis hashes returned: %v", entry)
	}
}
