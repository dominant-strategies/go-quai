package core

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/stretchr/testify/require"
)

// setCurrentHeaderHarness intentionally uses the real raw database accessors and
// HeaderChain caches. The BodyDb does not process state while rolling a branch
// forward; state rollback is enabled independently on HeaderChain when a test
// needs to exercise the UTXO undo journal.
type setCurrentHeaderHarness struct {
	t  *testing.T
	db ethdb.Database
	hc *HeaderChain
}

func newSetCurrentHeaderHarness(t *testing.T, processRollbackState, indexAddressUTXOs bool) *setCurrentHeaderHarness {
	t.Helper()
	db := rawdb.NewMemoryDatabase(log.Global)
	config := &params.ChainConfig{
		ChainID:           big.NewInt(1),
		Location:          common.Location{0, 0},
		IndexAddressUtxos: indexAddressUTXOs,
	}
	bc := NewTestBodyDb(db)
	bc.chainConfig = config
	bc.logger = log.Global
	headerCache, err := lru.New[common.Hash, types.WorkObject](headerCacheLimit)
	require.NoError(t, err)
	numberCache, err := lru.New[common.Hash, uint64](headerCacheLimit)
	require.NoError(t, err)
	hc := &HeaderChain{
		config:          config,
		bc:              bc,
		headerDb:        db,
		headerCache:     headerCache,
		numberCache:     numberCache,
		processingState: processRollbackState,
		logger:          log.Global,
	}
	return &setCurrentHeaderHarness{t: t, db: db, hc: hc}
}

func (h *setCurrentHeaderHarness) block(parent *types.WorkObject, number, nonce uint64) *types.WorkObject {
	h.t.Helper()
	block := types.EmptyWorkObject(common.ZONE_CTX)
	block.SetNumber(new(big.Int).SetUint64(number), common.ZONE_CTX)
	block.WorkObjectHeader().SetNonce(types.EncodeNonce(nonce))
	block.WorkObjectHeader().SetLocation(common.Location{0, 0})
	block.WorkObjectHeader().SetPrimaryCoinbase(common.BytesToAddress(bytes.Repeat([]byte{byte(nonce)}, common.AddressLength), common.Location{0, 0}))
	if parent != nil {
		block.SetParentHash(parent.Hash(), common.ZONE_CTX)
	}
	rawdb.WriteTermini(h.db, block.Hash(), types.EmptyTermini())
	rawdb.WriteWorkObject(h.db, block.Hash(), block, types.BlockObject, common.ZONE_CTX)
	stored := rawdb.ReadHeader(h.db, number, block.Hash())
	require.NotNil(h.t, stored)
	require.Equal(h.t, block.Hash(), stored.Hash(), "test block must survive database encoding")
	h.hc.bc.blockCache.Add(block.Hash(), *block)
	return block
}

func (h *setCurrentHeaderHarness) setCanonical(chain ...*types.WorkObject) {
	h.t.Helper()
	for _, block := range chain {
		rawdb.WriteCanonicalHash(h.db, block.Hash(), block.NumberU64(common.ZONE_CTX))
	}
	head := chain[len(chain)-1]
	rawdb.WriteHeadBlockHash(h.db, head.Hash())
	h.hc.currentHeader.Store(head)
	h.hc.genesisHeader = chain[0]
	rawdb.WriteGenesisHashes(h.db, common.Hashes{chain[0].Hash()})
}

func TestSetCurrentHeaderTopology(t *testing.T) {
	t.Run("same head is a no-op", func(t *testing.T) {
		h := newSetCurrentHeaderHarness(t, false, false)
		genesis := h.block(nil, 0, 1)
		head := h.block(genesis, 1, 2)
		h.setCanonical(genesis, head)

		require.NoError(t, h.hc.SetCurrentHeader(head))
		require.Equal(t, head.Hash(), h.hc.CurrentHeader().Hash())
		require.Equal(t, head.Hash(), rawdb.ReadHeadBlockHash(h.db))
		require.Equal(t, head.Hash(), rawdb.ReadCanonicalHash(h.db, 1))
	})

	t.Run("direct extension advances canonical head", func(t *testing.T) {
		h := newSetCurrentHeaderHarness(t, false, false)
		genesis := h.block(nil, 0, 11)
		head := h.block(genesis, 1, 12)
		next := h.block(head, 2, 13)
		h.setCanonical(genesis, head)

		require.NoError(t, h.hc.SetCurrentHeader(next))
		require.Equal(t, next.Hash(), h.hc.CurrentHeader().Hash())
		require.Equal(t, next.Hash(), rawdb.ReadHeadBlockHash(h.db))
		require.Equal(t, next.Hash(), rawdb.ReadCanonicalHash(h.db, 2))
	})

	t.Run("rollback to ancestor removes stale canonical heights", func(t *testing.T) {
		h := newSetCurrentHeaderHarness(t, false, false)
		genesis := h.block(nil, 0, 21)
		one := h.block(genesis, 1, 22)
		two := h.block(one, 2, 23)
		three := h.block(two, 3, 24)
		h.setCanonical(genesis, one, two, three)

		require.NoError(t, h.hc.SetCurrentHeader(one))
		require.Equal(t, one.Hash(), h.hc.CurrentHeader().Hash())
		require.Equal(t, one.Hash(), rawdb.ReadHeadBlockHash(h.db))
		require.Equal(t, common.Hash{}, rawdb.ReadCanonicalHash(h.db, 2))
		require.Equal(t, common.Hash{}, rawdb.ReadCanonicalHash(h.db, 3))
	})

	t.Run("competing longer branch replaces every canonical mapping", func(t *testing.T) {
		h := newSetCurrentHeaderHarness(t, false, false)
		genesis := h.block(nil, 0, 31)
		commonBlock := h.block(genesis, 1, 32)
		oldTwo := h.block(commonBlock, 2, 33)
		oldThree := h.block(oldTwo, 3, 34)
		newTwo := h.block(commonBlock, 2, 35)
		newThree := h.block(newTwo, 3, 36)
		newFour := h.block(newThree, 4, 37)
		h.setCanonical(genesis, commonBlock, oldTwo, oldThree)

		require.NoError(t, h.hc.SetCurrentHeader(newFour))
		require.Equal(t, commonBlock.Hash(), rawdb.ReadCanonicalHash(h.db, 1))
		require.Equal(t, newTwo.Hash(), rawdb.ReadCanonicalHash(h.db, 2))
		require.Equal(t, newThree.Hash(), rawdb.ReadCanonicalHash(h.db, 3))
		require.Equal(t, newFour.Hash(), rawdb.ReadCanonicalHash(h.db, 4))
		require.Equal(t, newFour.Hash(), rawdb.ReadHeadBlockHash(h.db))
		require.Equal(t, newFour.Hash(), h.hc.CurrentHeader().Hash())
	})

	t.Run("missing roll-forward body leaves the chain at the common ancestor", func(t *testing.T) {
		h := newSetCurrentHeaderHarness(t, false, false)
		genesis := h.block(nil, 0, 41)
		commonBlock := h.block(genesis, 1, 42)
		oldHead := h.block(commonBlock, 2, 43)
		newHead := h.block(commonBlock, 2, 44)
		h.setCanonical(genesis, commonBlock, oldHead)
		rawdb.DeleteWorkObjectBody(h.db, newHead.Hash())
		h.hc.bc.blockCache.Remove(newHead.Hash())

		err := h.hc.SetCurrentHeader(newHead)
		require.ErrorContains(t, err, "could not find block during SetCurrentState")
		require.Equal(t, commonBlock.Hash(), h.hc.CurrentHeader().Hash())
		require.Equal(t, commonBlock.Hash(), rawdb.ReadHeadBlockHash(h.db))
		require.Equal(t, common.Hash{}, rawdb.ReadCanonicalHash(h.db, 2))
	})

}

func TestSetCurrentHeaderReorgHorizon(t *testing.T) {
	buildFork := func(t *testing.T, oldDepth uint64) (*setCurrentHeaderHarness, *types.WorkObject, *types.WorkObject) {
		t.Helper()
		h := newSetCurrentHeaderHarness(t, false, false)
		baseHeight := uint64(params.MaxCodeSizeForkHeight + 1)
		commonBlock := h.block(nil, baseHeight, 500)
		oldChain := []*types.WorkObject{commonBlock}
		parent := commonBlock
		for i := uint64(1); i <= oldDepth; i++ {
			parent = h.block(parent, baseHeight+i, 500+i)
			oldChain = append(oldChain, parent)
		}
		newHead := h.block(commonBlock, baseHeight+1, 900)
		h.setCanonical(oldChain...)
		return h, parent, newHead
	}

	t.Run("rejects a common ancestor beyond the horizon", func(t *testing.T) {
		h, oldHead, newHead := buildFork(t, c_zoneHorizonThreshold+1)

		err := h.hc.SetCurrentHeader(newHead)
		require.ErrorContains(t, err, "common header too old")
		require.Equal(t, oldHead.Hash(), h.hc.CurrentHeader().Hash())
		require.Equal(t, oldHead.Hash(), rawdb.ReadHeadBlockHash(h.db))
		require.Equal(t, oldHead.Hash(), rawdb.ReadCanonicalHash(h.db, oldHead.NumberU64(common.ZONE_CTX)))
	})

	t.Run("accepts a common ancestor exactly at the horizon", func(t *testing.T) {
		h, oldHead, newHead := buildFork(t, c_zoneHorizonThreshold)

		require.NoError(t, h.hc.SetCurrentHeader(newHead))
		require.Equal(t, newHead.Hash(), h.hc.CurrentHeader().Hash())
		require.Equal(t, newHead.Hash(), rawdb.ReadHeadBlockHash(h.db))
		require.Equal(t, common.Hash{}, rawdb.ReadCanonicalHash(h.db, oldHead.NumberU64(common.ZONE_CTX)))
	})
}

func TestSetCurrentHeaderRollsBackUTXOJournal(t *testing.T) {
	h := newSetCurrentHeaderHarness(t, true, true)
	genesis := h.block(nil, 0, 101)
	head := h.block(genesis, 1, 102)
	h.setCanonical(genesis, head)

	spent := testSpentUTXO(0x11, 0, 0x21)
	trimmed := testSpentUTXO(0x12, 1, 0x22)
	created := testSpentUTXO(0x13, 2, 0x23)
	require.NoError(t, rawdb.WriteSpentUTXOs(h.db, head.Hash(), []*types.SpentUtxoEntry{spent}))
	require.NoError(t, rawdb.WriteTrimmedUTXOs(h.db, head.Hash(), []*types.SpentUtxoEntry{trimmed}))
	require.NoError(t, rawdb.CreateUTXO(h.db, created.TxHash, created.Index, created.UtxoEntry))
	require.NoError(t, rawdb.WriteCreatedUTXOKeys(h.db, head.Hash(), [][]byte{
		rawdb.UtxoKeyWithDenomination(created.TxHash, created.Index, created.Denomination),
	}))
	require.NoError(t, rawdb.WriteAddressUTXOs(h.db, h.db, map[[20]byte][]*types.OutpointAndDenomination{
		common.AddressBytes(created.Address): {{TxHash: created.TxHash, Index: created.Index, Denomination: created.Denomination, Lock: created.Lock}},
	}))

	require.NoError(t, h.hc.SetCurrentHeader(genesis))
	require.NotNil(t, rawdb.GetUTXO(h.db, spent.TxHash, spent.Index))
	require.NotNil(t, rawdb.GetUTXO(h.db, trimmed.TxHash, trimmed.Index))
	require.Nil(t, rawdb.GetUTXO(h.db, created.TxHash, created.Index))
	requireAddressOutpointCount(t, h.db, spent, 1)
	requireAddressOutpointCount(t, h.db, trimmed, 1)
	requireAddressOutpointCount(t, h.db, created, 0)
}

func TestSetCurrentHeaderRollbackOrderAcrossBlocks(t *testing.T) {
	h := newSetCurrentHeaderHarness(t, true, false)
	genesis := h.block(nil, 0, 201)
	createdInOne := h.block(genesis, 1, 202)
	spentInTwo := h.block(createdInOne, 2, 203)
	h.setCanonical(genesis, createdInOne, spentInTwo)

	utxo := testSpentUTXO(0x31, 0, 0x41)
	require.NoError(t, rawdb.WriteCreatedUTXOKeys(h.db, createdInOne.Hash(), [][]byte{
		rawdb.UtxoKeyWithDenomination(utxo.TxHash, utxo.Index, utxo.Denomination),
	}))
	require.NoError(t, rawdb.WriteSpentUTXOs(h.db, spentInTwo.Hash(), []*types.SpentUtxoEntry{utxo}))

	require.NoError(t, h.hc.SetCurrentHeader(genesis))
	// Rolling back block two recreates the output, then rolling back the block
	// which created it must delete it again.
	require.Nil(t, rawdb.GetUTXO(h.db, utxo.TxHash, utxo.Index))
	require.Equal(t, common.Hash{}, rawdb.ReadCanonicalHash(h.db, 1))
	require.Equal(t, common.Hash{}, rawdb.ReadCanonicalHash(h.db, 2))
}

func TestSetCurrentHeaderRollbackCreatedAndSpentInSameBlock(t *testing.T) {
	h := newSetCurrentHeaderHarness(t, true, true)
	genesis := h.block(nil, 0, 251)
	head := h.block(genesis, 1, 252)
	h.setCanonical(genesis, head)

	utxo := testSpentUTXO(0x35, 4, 0x45)
	require.NoError(t, rawdb.WriteSpentUTXOs(h.db, head.Hash(), []*types.SpentUtxoEntry{utxo}))
	require.NoError(t, rawdb.WriteCreatedUTXOKeys(h.db, head.Hash(), [][]byte{
		rawdb.UtxoKeyWithDenomination(utxo.TxHash, utxo.Index, utxo.Denomination),
	}))

	require.NoError(t, h.hc.SetCurrentHeader(genesis))
	// The restore is intentionally ordered before deletion of outputs created by
	// the same block, leaving the pre-block state (where the outpoint did not exist).
	require.Nil(t, rawdb.GetUTXO(h.db, utxo.TxHash, utxo.Index))
	requireAddressOutpointCount(t, h.db, utxo, 0)
}

func TestSetCurrentHeaderDeduplicatesHistoricalF4RollbackRecords(t *testing.T) {
	h := newSetCurrentHeaderHarness(t, true, true)
	genesis := h.block(nil, 0, 301)
	head := h.block(genesis, 1, 302)
	h.setCanonical(genesis, head)

	utxo := testSpentUTXO(0x51, 0, 0x61)
	// Pre-fix F4 blocks journaled the same outpoint as both transaction-spent
	// and trimmed. The key/value UTXO database is idempotent, but the optional
	// address index is an append-only list and must not receive two entries.
	require.NoError(t, rawdb.WriteSpentUTXOs(h.db, head.Hash(), []*types.SpentUtxoEntry{utxo}))
	require.NoError(t, rawdb.WriteTrimmedUTXOs(h.db, head.Hash(), []*types.SpentUtxoEntry{utxo}))

	require.NoError(t, h.hc.SetCurrentHeader(genesis))
	require.NotNil(t, rawdb.GetUTXO(h.db, utxo.TxHash, utxo.Index))
	requireAddressOutpointCount(t, h.db, utxo, 1)
}

func TestSetCurrentHeaderRollsBackCoinbaseLockups(t *testing.T) {
	h := newSetCurrentHeaderHarness(t, true, false)
	genesis := h.block(nil, 0, 401)
	head := h.block(genesis, 1, 402)
	h.setCanonical(genesis, head)

	owner := common.BytesToAddress(bytes.Repeat([]byte{0x71}, common.AddressLength), common.Location{0, 0})
	beneficiary := common.BytesToAddress(bytes.Repeat([]byte{0x72}, common.AddressLength), common.Location{0, 0})
	createdKey, err := rawdb.WriteCoinbaseLockup(h.db, owner, beneficiary, 1, 10, big.NewInt(50), 1, 1, common.Zero)
	require.NoError(t, err)
	require.NoError(t, rawdb.WriteCreatedCoinbaseLockupKeys(h.db, head.Hash(), [][]byte{createdKey}))

	deletedKey := rawdb.CoinbaseLockupKey(owner, beneficiary, 2, 11)
	deletedValue, err := rawdb.WriteCoinbaseLockupToSlice(big.NewInt(75), 2, 3, common.Zero)
	require.NoError(t, err)
	intermediateValue, err := rawdb.WriteCoinbaseLockupToSlice(big.NewInt(80), 3, 4, common.Zero)
	require.NoError(t, err)
	require.NoError(t, rawdb.WriteDeletedCoinbaseLockups(h.db, head.Hash(), []rawdb.DeletedCoinbaseLockup{
		{Key: deletedKey, Value: deletedValue},
		{Key: deletedKey, Value: intermediateValue},
	}))

	require.NoError(t, h.hc.SetCurrentHeader(genesis))
	createdData, _ := h.db.Get(createdKey)
	require.Empty(t, createdData)
	restoredData, err := h.db.Get(deletedKey)
	require.NoError(t, err)
	require.Equal(t, deletedValue, restoredData)
}

func TestSetCurrentHeaderUndoesAddressLockupDeltas(t *testing.T) {
	h := newSetCurrentHeaderHarness(t, true, true)
	genesis := h.block(nil, 0, 451)
	head := h.block(genesis, 1, 452)
	h.setCanonical(genesis, head)

	var address common.InternalAddress
	copy(address[:], bytes.Repeat([]byte{0x81}, common.AddressLength))
	rawdb.WriteNewLockups(h.db, h.db, head.Hash(), map[common.InternalAddress]*big.Int{address: big.NewInt(125)}, nil)
	require.Equal(t, int64(125), rawdb.ReadLockedBalance(h.db, address).Int64())

	require.NoError(t, h.hc.SetCurrentHeader(genesis))
	require.Zero(t, rawdb.ReadLockedBalance(h.db, address).Sign())
}

func testSpentUTXO(txByte byte, index uint16, addressByte byte) *types.SpentUtxoEntry {
	return &types.SpentUtxoEntry{
		OutPoint: types.OutPoint{TxHash: common.BytesToHash([]byte{txByte}), Index: index},
		UtxoEntry: &types.UtxoEntry{
			Denomination: 0,
			Address:      bytes.Repeat([]byte{addressByte}, common.AddressLength),
			Lock:         new(big.Int),
		},
	}
}

func requireAddressOutpointCount(t *testing.T, db ethdb.Reader, utxo *types.SpentUtxoEntry, expected int) {
	t.Helper()
	outpoints, err := rawdb.ReadAddressUTXOs(db, common.AddressBytes(utxo.Address))
	require.NoError(t, err)
	count := 0
	for _, outpoint := range outpoints {
		if outpoint.TxHash == utxo.TxHash && outpoint.Index == utxo.Index {
			count++
		}
	}
	require.Equal(t, expected, count)
}
