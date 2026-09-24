package core

import (
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

func TestFailedQiCandidateRestoresWorkerGas(t *testing.T) {
	config := params.TestChainConfig
	w := &worker{hc: &HeaderChain{bc: &BodyDb{chainConfig: config}}, chainConfig: config, workerDb: rawdb.NewMemoryDatabase(log.Global)}
	header := &types.WorkObjectHeader{}
	header.SetLocation(config.Location)
	env := &environment{
		wo:                 types.NewWorkObject(header, types.EmptyWorkObjectBody(), nil),
		gasPool:            new(types.GasPool).AddGas(500000),
		qiGasScalingFactor: 1,
	}
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTx(&types.QiTx{
		ChainID: new(big.Int).Set(config.ChainID),
		TxIn: types.TxIns{{PreviousOutPoint: types.OutPoint{
			TxHash: common.Hash{1}, Index: 1,
		}, PubKey: crypto.FromECDSAPub(&private.PublicKey)}},
		Signature: new(schnorr.Signature),
	})
	before := env.gasPool.Gas()
	err = w.processQiTx(tx, env, nil, nil, true)
	if err == nil || !strings.Contains(err.Error(), "non-existent UTXO") {
		t.Fatalf("unexpected result: %v", err)
	}
	if env.gasPool.Gas() != before {
		t.Fatalf("failed Qi candidate consumed gas: before %d, after %d", before, env.gasPool.Gas())
	}
}

func TestFailedQiCandidateReleasesItsInputReservation(t *testing.T) {
	config := params.TestChainConfig
	db := rawdb.NewMemoryDatabase(log.Global)
	w := &worker{hc: &HeaderChain{bc: &BodyDb{chainConfig: config}}, chainConfig: config, workerDb: db}
	header := &types.WorkObjectHeader{}
	header.SetLocation(config.Location)
	env := &environment{
		wo:                 types.NewWorkObject(header, types.EmptyWorkObjectBody(), nil),
		gasPool:            new(types.GasPool).AddGas(500000),
		qiGasScalingFactor: 1,
		deletedUtxos:       make(map[common.Hash]struct{}),
	}
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	inputHash := common.Hash{1}
	qiAddress := common.HexToAddress("0x0080000000000000000000000000000000000000", config.Location)
	if err := rawdb.CreateUTXO(db, inputHash, 1, types.NewUtxoEntry(types.NewTxOut(1, qiAddress.Bytes(), nil))); err != nil {
		t.Fatal(err)
	}
	tx := types.NewTx(&types.QiTx{
		ChainID: new(big.Int).Set(config.ChainID),
		TxIn: types.TxIns{{PreviousOutPoint: types.OutPoint{
			TxHash: inputHash, Index: 1,
		}, PubKey: crypto.FromECDSAPub(&private.PublicKey)}},
		TxOut:     types.TxOuts{*types.NewTxOut(types.MaxDenomination+1, qiAddress.Bytes(), nil)},
		Signature: new(schnorr.Signature),
	})
	before := env.gasPool.Gas()
	if err := w.processQiTx(tx, env, nil, nil, true); err == nil || !strings.Contains(err.Error(), "max allowed") {
		t.Fatalf("unexpected result: %v", err)
	}
	if len(env.deletedUtxos) != 0 || env.gasPool.Gas() != before {
		t.Fatalf("failed candidate retained input reservation or gas: %d inputs, %d gas", len(env.deletedUtxos), env.gasPool.Gas())
	}
}
