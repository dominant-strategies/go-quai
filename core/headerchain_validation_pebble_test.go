//go:build (arm64 || amd64) && !openbsd

package core

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/crypto/multiset"
	pebbledb "github.com/dominant-strategies/go-quai/ethdb/pebble"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

// TestFinalizeDeduplicatesSpendAtTrimHeight reproduces the exact ordering used
// while processing a block:
//
//  1. transaction processing stages a UTXO deletion in a pending batch;
//  2. Finalize examines UTXOs created at the denomination's expiry height;
//  3. both paths encounter the same UTXO before the batch is committed.
//
// The height is constructed directly, so this regression does not depend on a
// miner, mempool ordering, wall-clock timing, or the local shortened trim depth.
func TestFinalizeDeduplicatesSpendAtTrimHeight(t *testing.T) {
	location := common.Location{1, 1}
	kvdb, err := pebbledb.New(t.TempDir(), 16, 16, "trim-spend-test", false, log.Global, location)
	if err != nil {
		t.Fatal(err)
	}
	db := rawdb.NewDatabase(kvdb)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close Pebble database: %v", err)
		}
	})

	config := &params.ChainConfig{Location: location}
	bodyDb := NewTestBodyDb(db)
	bodyDb.chainConfig = config
	bodyDb.logger = log.Global
	headerCache, err := lru.New[common.Hash, types.WorkObject](headerCacheLimit)
	if err != nil {
		t.Fatal(err)
	}
	hc := &HeaderChain{
		config:      config,
		bc:          bodyDb,
		headerDb:    db,
		headerCache: headerCache,
		logger:      log.Global,
	}

	const (
		createdAt    = uint64(1)
		denomination = uint8(0)
		outputIndex  = uint16(0)
	)
	createdBlockHash := common.HexToHash("0x1001")
	parentHash := common.HexToHash("0x2001")
	txHash := common.HexToHash("0x3001")
	key, err := crypto.HexToECDSA("03b51b275bfc4c731b0712c3abc97f6c87a242621d6cdf853528bd4ab2f7fdf1")
	if err != nil {
		t.Fatal(err)
	}
	inputAddress := crypto.PubkeyToAddress(key.PublicKey, location)
	if !inputAddress.IsInQiLedgerScope() {
		t.Fatalf("fixed test key produced non-Qi address %s", inputAddress)
	}
	utxo := &types.UtxoEntry{
		Denomination: denomination,
		Address:      inputAddress.Bytes(),
		Lock:         new(big.Int),
	}
	if err := rawdb.CreateUTXO(db, txHash, outputIndex, utxo); err != nil {
		t.Fatal(err)
	}
	createdKey := rawdb.UtxoKeyWithDenomination(txHash, outputIndex, denomination)
	if err := rawdb.WriteCreatedUTXOKeys(db, createdBlockHash, [][]byte{createdKey}); err != nil {
		t.Fatal(err)
	}
	rawdb.WriteCanonicalHash(db, createdBlockHash, createdAt)

	utxoHash := types.UTXOHash(txHash, outputIndex, utxo)
	parentSet := multiset.New()
	parentSet.Add(utxoHash.Bytes())
	rawdb.WriteMultiSet(db, parentHash, parentSet)

	header := types.EmptyWorkObject(common.ZONE_CTX)
	header.WorkObjectHeader().SetLocation(location)
	header.SetParentHash(parentHash, common.ZONE_CTX)
	header.SetNumber(new(big.Int).SetUint64(createdAt+types.TrimDepths[denomination]), common.ZONE_CTX)
	header.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(params.ConversionLockChangeForkBlock))
	header.WorkObjectHeader().SetDifficulty(big.NewInt(1_000_000_000_000))
	header.Header().SetBaseFee(new(big.Int))
	header.Header().SetGasLimit(10_000_000)

	primeTerminusHash := common.HexToHash("0x4001")
	primeTerminus := types.EmptyWorkObject(common.PRIME_CTX)
	primeTerminus.Header().SetExchangeRate(big.NewInt(1))
	rawdb.WriteTermini(db, primeTerminusHash, types.EmptyTermini())
	headerCache.Add(primeTerminusHash, *primeTerminus)
	header.Header().SetPrimeTerminusHash(primeTerminusHash)

	batch := db.NewBatch()
	batch.SetPending(true)
	chainID := big.NewInt(1)
	spend := types.NewTx(&types.QiTx{
		ChainID: new(big.Int).Set(chainID),
		TxIn: types.TxIns{{
			PreviousOutPoint: types.OutPoint{TxHash: txHash, Index: outputIndex},
			PubKey:           crypto.FromECDSAPub(&key.PublicKey),
		}},
	})
	gasPool := new(types.GasPool).AddGas(header.GasLimit())
	var usedGas, etxRLimit, etxPLimit uint64
	etxRLimit = ^uint64(0)
	etxPLimit = ^uint64(0)
	changes := &UtxosCreatedDeleted{}
	supplyAdded := new(big.Int)
	supplyRemoved := new(big.Int)
	_, _, receipt, err, _ := ProcessQiTx(
		spend,
		hc,
		false,
		true,
		header,
		batch,
		db,
		gasPool,
		&usedGas,
		types.NewSigner(chainID, location),
		location,
		*chainID,
		1,
		&etxRLimit,
		&etxPLimit,
		changes,
		supplyAdded,
		supplyRemoved,
		false,
	)
	if err != nil {
		t.Fatalf("process deterministic Qi spend: %v", err)
	}
	if receipt == nil || receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatal("deterministic Qi spend did not produce a successful receipt")
	}
	if len(changes.UtxosDeletedHashes) != 1 || changes.UtxosDeletedHashes[0] != utxoHash {
		t.Fatalf("Qi spend recorded unexpected deletions: %v", changes.UtxosDeletedHashes)
	}
	// Pebble still exposes the committed UTXO to trimBlock, while batch-aware
	// transaction reads already see it as spent.
	if got := rawdb.GetUTXOWithBatch(db, batch, txHash, outputIndex); got != nil {
		t.Fatal("pending transaction deletion is not visible through the batch")
	}
	if got := rawdb.GetUTXO(db, txHash, outputIndex); got == nil {
		t.Fatal("test setup must leave the UTXO visible in the committed database until batch commit")
	}

	resultSet, resultSize, trimmed, err := hc.Finalize(
		batch,
		header,
		nil,
		false,
		1,
		nil,
		changes.UtxosDeletedHashes,
		supplyRemoved,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resultSize != 0 {
		t.Fatalf("UTXO set size counted the spend more than once: got %d, want 0", resultSize)
	}
	if len(trimmed) != 0 {
		t.Fatalf("transaction-spent UTXO was also recorded as trimmed: got %d entries", len(trimmed))
	}
	if supplyRemoved.Cmp(types.Denominations[denomination]) != 0 {
		t.Fatalf("removed supply counted the spend more than once: got %s, want %s", supplyRemoved, types.Denominations[denomination])
	}
	emptySet := multiset.New()
	if resultSet.Hash() != emptySet.Hash() {
		t.Fatalf("UTXO multiset removed the same hash more than once: got %s, want %s", resultSet.Hash(), emptySet.Hash())
	}

	if err := batch.Write(); err != nil {
		t.Fatal(err)
	}
	if got := rawdb.GetUTXO(db, txHash, outputIndex); got != nil {
		t.Fatal("transaction-spent UTXO remains after batch commit")
	}
	trimmedJournal, err := rawdb.ReadTrimmedUTXOs(db, header.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if len(trimmedJournal) != 0 {
		t.Fatalf("trim journal contains transaction-spent UTXO: got %d entries", len(trimmedJournal))
	}
}
