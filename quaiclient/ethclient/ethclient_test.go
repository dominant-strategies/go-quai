// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package ethclient

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	quai "github.com/dominant-strategies/go-quai"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/progpow"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/eth"
	"github.com/dominant-strategies/go-quai/eth/ethconfig"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/node"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quaiclient"
	"github.com/dominant-strategies/go-quai/rpc"
)

// Verify that Client implements the quai interfaces.
var (
	_ = quai.ChainReader(&Client{})
	_ = quai.TransactionReader(&Client{})
	_ = quai.ChainStateReader(&Client{})
	_ = quai.ChainSyncReader(&Client{})
	_ = quai.ContractCaller(&Client{})
	_ = quai.GasEstimator(&Client{})
	_ = quai.GasPricer(&Client{})
	//_ = quai.LogFilterer(&Client{})
	_ = quai.PendingStateReader(&Client{})
	// _ = quai.PendingStateEventer(&Client{})
	_ = quai.PendingContractCaller(&Client{})
)

var (
	testKey, _  = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	testAddr    = crypto.PubkeyToAddress(testKey.PublicKey)
	testBalance = big.NewInt(2e15)
)

func newTestBackend(t *testing.T) (*node.Node, []*types.Block, *rpc.Client) {
	// Set location to ZONE_CTX
	common.NodeLocation = common.Location{0, 0}
	// Generate test chain.
	db := rawdb.NewMemoryDatabase()
	genesis, blocks := generateTestChain(db)
	// Create node
	n, err := node.New(&node.Config{})
	if err != nil {
		t.Fatalf("can't create new node: %v", err)
	}
	// Create quai Service
	config := &ethconfig.Config{Genesis: genesis}
	config.Zone = 0
	config.Miner.ExtraData = []byte("test miner")
	config.Progpow.PowMode = progpow.ModeFake
	config.DomUrl = "http://localhost:8080"

	client, _ := n.Attach()

	ethservice, err := eth.NewFake(n, config, db)
	if err != nil {
		t.Fatalf("can't create new quai service: %v", err)
	}
	// Import the test chain.
	if err := n.Start(); err != nil {
		t.Fatalf("can't start test node: %v", err)
	}

	if _, err := ethservice.Core().InsertChain(blocks[1:]); err != nil {
		t.Fatalf("can't import test blocks: %v", err)
	}
	return n, blocks, client
}

func generateTestChain(db ethdb.Database) (*core.Genesis, []*types.Block) {
	config := params.TestChainConfig
	config.Location = common.Location{0, 0}
	config.ChainID = big.NewInt(1)

	genesis := &core.Genesis{
		Config:     config,
		Nonce:      0,
		ExtraData: []byte("test genesis"),
		GasLimit:   5000000,
		Difficulty: big.NewInt(300000000),
	}
	generate := func(i int, g *core.BlockGen) {
		g.OffsetTime(5)
		g.SetExtra([]byte("test"))
	}
	gblock := genesis.ToBlock(db)

	config.GenesisHash = gblock.Hash()

	engine := progpow.NewFaker()
	blocks, _ := core.GenerateChain(config, gblock, engine, db, 1, generate)
	blocks = append([]*types.Block{gblock}, blocks...)
	return genesis, blocks
}

func TestEthClient(t *testing.T) {
	backend, chain, client := newTestBackend(t)
	defer backend.Close()
	defer client.Close()

	tests := map[string]struct {
		test func(t *testing.T)
	}{
		"TestHeader": {
			func(t *testing.T) { testHeader(t, chain, client) },
		},
		"TestBalanceAt": {
			func(t *testing.T) { testBalanceAt(t, client) },
		},
		"TestTxInBlockInterrupted": {
			func(t *testing.T) { testTransactionInBlockInterrupted(t, client) },
		},
		"TestChainID": {
			func(t *testing.T) { testChainID(t, client) },
		},
		"TestGetBlock": {
			func(t *testing.T) { testGetBlock(t, client) },
		},
		"TestStatusFunctions": {
			func(t *testing.T) { testStatusFunctions(t, client) },
		},
		"TestCallContract": {
			func(t *testing.T) { testCallContract(t, client) },
		},
		"TestAtFunctions": {
			func(t *testing.T) { testAtFunctions(t, client) },
		},
	}

	t.Parallel()
	for name, tt := range tests {
		t.Run(name, tt.test)
	}
}

func testHeader(t *testing.T, chain []*types.Block, client *rpc.Client) {
	tests := map[string]struct {
		block   string
		want    *types.Header
		wantErr error
	}{
		"genesis": {
			block: "0x0",
			want:  chain[0].Header(),
		},
		"first_block": {
			block: "0x1",
			want:  chain[1].Header(),
		},
		"future_block": {
			block:   "0xffffff",
			want:    nil,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ec := quaiclient.NewClient(&quaiclient.TestRpcClient{Chain: chain})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			got := ec.HeaderByNumber(ctx, tt.block)

			err := got.Compare(tt.want)
			if err != nil {
				t.Fatalf("deepEqual failed %v", err)
			}
		})
	}
}

func testBalanceAt(t *testing.T, client *rpc.Client) {
	tests := map[string]struct {
		account common.Address
		block   *big.Int
		want    *big.Int
		wantErr error
	}{
		"valid_account": {
			account: testAddr,
			block:   big.NewInt(1),
			want:    testBalance,
		},
		"non_existent_account": {
			account: common.Address{},
			block:   big.NewInt(1),
			want:    big.NewInt(0),
		},
		"future_block": {
			account: testAddr,
			block:   big.NewInt(1000000000),
			want:    big.NewInt(0),
			wantErr: errors.New("header not found"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ec := NewClient(client)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			got, err := ec.BalanceAt(ctx, tt.account, tt.block)
			if tt.wantErr != nil && (err == nil || err.Error() != tt.wantErr.Error()) {
				t.Fatalf("BalanceAt(%x, %v) error = %q, want %q", tt.account, tt.block, err, tt.wantErr)
			}
			if got.Cmp(tt.want) != 0 {
				t.Fatalf("BalanceAt(%x, %v) = %v, want %v", tt.account, tt.block, got, tt.want)
			}
		})
	}
}

func testTransactionInBlockInterrupted(t *testing.T, client *rpc.Client) {
	ec := NewClient(client)

	// Get current block by number
	block, err := ec.BlockByNumber(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Test tx in block interupted
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx, err := ec.TransactionInBlock(ctx, block.Hash(), 1)
	if tx != nil {
		t.Fatal("transaction should be nil")
	}
	if err == nil || err == quai.NotFound {
		t.Fatal("error should not be nil/notfound")
	}
	// Test tx in block not found
	if _, err := ec.TransactionInBlock(context.Background(), block.Hash(), 1); err != quai.NotFound {
		t.Fatal("error should be quai.NotFound")
	}
}

func testChainID(t *testing.T, client *rpc.Client) {
	ec := NewClient(client)
	id, err := ec.ChainID(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == nil || id.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("ChainID returned wrong number: %+v", id)
	}
}

func testGetBlock(t *testing.T, client *rpc.Client) {
	ec := NewClient(client)
	// Get current block number
	blockNumber, err := ec.BlockNumber(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if blockNumber != 1 {
		t.Fatalf("BlockNumber returned wrong number: %d", blockNumber)
	}
	// Get current block by number
	block, err := ec.BlockByNumber(context.Background(), new(big.Int).SetUint64(blockNumber))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if block.NumberU64() != blockNumber {
		t.Fatalf("BlockByNumber returned wrong block: want %d got %d", blockNumber, block.NumberU64())
	}
	// Get current block by hash
	blockH, err := ec.BlockByHash(context.Background(), block.Hash())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if block.Hash() != blockH.Hash() {
		t.Fatalf("BlockByHash returned wrong block: want %v got %v", block.Hash().Hex(), blockH.Hash().Hex())
	}
	// Get header by number
	header, err := ec.HeaderByNumber(context.Background(), new(big.Int).SetUint64(blockNumber))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if block.Header().Hash() != header.Hash() {
		t.Fatalf("HeaderByNumber returned wrong header: want %v got %v", block.Header().Hash().Hex(), header.Hash().Hex())
	}
	// Get header by hash
	headerH, err := ec.HeaderByHash(context.Background(), block.Hash())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if block.Header().Hash() != headerH.Hash() {
		t.Fatalf("HeaderByHash returned wrong header: want %v got %v", block.Header().Hash().Hex(), headerH.Hash().Hex())
	}
}

func testStatusFunctions(t *testing.T, client *rpc.Client) {
	ec := NewClient(client)

	// Sync progress
	progress, err := ec.SyncProgress(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if progress != nil {
		t.Fatalf("unexpected progress: %v", progress)
	}
	// NetworkID
	networkID, err := ec.NetworkID(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if networkID.Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("unexpected networkID: %v", networkID)
	}
	// SuggestGasPrice (should suggest 1 Gwei)
	gasPrice, err := ec.SuggestGasPrice(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gasPrice.Cmp(big.NewInt(1875000000)) != 0 { // 1 gwei tip + 0.875 basefee after a 1 gwei fee empty block
		t.Fatalf("unexpected gas price: %v", gasPrice)
	}
	// SuggestGasTipCap (should suggest 1 Gwei)
	gasTipCap, err := ec.SuggestGasTipCap(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gasTipCap.Cmp(big.NewInt(1000000000)) != 0 {
		t.Fatalf("unexpected gas tip cap: %v", gasTipCap)
	}
}

func testCallContract(t *testing.T, client *rpc.Client) {
	ec := NewClient(client)

	// EstimateGas
	msg := quai.CallMsg{
		From:  testAddr,
		To:    &common.Address{},
		Gas:   21000,
		Value: big.NewInt(1),
	}
	gas, err := ec.EstimateGas(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gas != 21000 {
		t.Fatalf("unexpected gas price: %v", gas)
	}
	// CallContract
	if _, err := ec.CallContract(context.Background(), msg, big.NewInt(1)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// PendingCallCOntract
	if _, err := ec.PendingCallContract(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testAtFunctions(t *testing.T, client *rpc.Client) {
	ec := NewClient(client)
	// send a transaction for some interesting pending status
	sendTransaction(ec)
	time.Sleep(100 * time.Millisecond)
	// Check pending transaction count
	pending, err := ec.PendingTransactionCount(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pending != 1 {
		t.Fatalf("unexpected pending, wanted 1 got: %v", pending)
	}
	// Query balance
	balance, err := ec.BalanceAt(context.Background(), testAddr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	penBalance, err := ec.PendingBalanceAt(context.Background(), testAddr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance.Cmp(penBalance) == 0 {
		t.Fatalf("unexpected balance: %v %v", balance, penBalance)
	}
	// NonceAt
	nonce, err := ec.NonceAt(context.Background(), testAddr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	penNonce, err := ec.PendingNonceAt(context.Background(), testAddr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if penNonce != nonce+1 {
		t.Fatalf("unexpected nonce: %v %v", nonce, penNonce)
	}
	// StorageAt
	storage, err := ec.StorageAt(context.Background(), testAddr, common.Hash{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	penStorage, err := ec.PendingStorageAt(context.Background(), testAddr, common.Hash{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(storage, penStorage) {
		t.Fatalf("unexpected storage: %v %v", storage, penStorage)
	}
	// CodeAt
	code, err := ec.CodeAt(context.Background(), testAddr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	penCode, err := ec.PendingCodeAt(context.Background(), testAddr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(code, penCode) {
		t.Fatalf("unexpected code: %v %v", code, penCode)
	}
}

func sendTransaction(ec *Client) error {
	// Retrieve chainID
	chainID, err := ec.ChainID(context.Background())
	if err != nil {
		return err
	}
	// Create transaction
	tx := types.NewTx(&types.ExternalTx{})
	signer := types.LatestSignerForChainID(chainID)
	signature, err := crypto.Sign(signer.Hash(tx).Bytes(), testKey)
	if err != nil {
		return err
	}
	signedTx, err := tx.WithSignature(signer, signature)
	if err != nil {
		return err
	}
	// Send transaction
	return ec.SendTransaction(context.Background(), signedTx)
}
