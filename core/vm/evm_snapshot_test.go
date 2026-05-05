package vm

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

func TestEVMSnapshotRevertsLocalSideEffects(t *testing.T) {
	testEVMSnapshotLocalSideEffects(t, params.ShaEquivalentDifficultyForkBlock, true)
}

func testEVMSnapshotLocalSideEffects(t *testing.T, primeTerminusNumber uint64, revertsLocalSideEffects bool) {
	t.Helper()

	location := common.Location{0, 0}
	account := common.ZeroInternal(location)

	statedb, err := state.New(
		common.Hash{},
		common.Hash{},
		new(big.Int),
		state.NewDatabase(rawdb.NewMemoryDatabase(log.Global)),
		state.NewDatabase(rawdb.NewMemoryDatabase(log.Global)),
		nil,
		location,
		log.Global,
	)
	if err != nil {
		t.Fatalf("failed to create state db: %v", err)
	}
	statedb.CreateAccount(account)
	statedb.AddBalance(account, big.NewInt(1))

	evm := NewEVM(
		BlockContext{BlockNumber: new(big.Int).SetUint64(primeTerminusNumber), PrimeTerminusNumber: primeTerminusNumber},
		TxContext{Hash: common.BytesToHash([]byte{0x1})},
		statedb,
		params.TestChainConfig,
		Config{},
		nil,
	)

	baseKey := [47]byte{0x1}
	baseHash := common.BytesToHash([]byte{0x1})
	baseETX := types.NewTx(&types.ExternalTx{
		To:                addressPtr(common.HexToAddress("0x0000000000000000000000000000000000000002", location)),
		Sender:            common.HexToAddress("0x0000000000000000000000000000000000000001", location),
		OriginatingTxHash: common.BytesToHash([]byte{0x1}),
		ETXIndex:          0,
		Gas:               params.TxGas,
		Value:             big.NewInt(1),
	})

	evm.ETXCache = append(evm.ETXCache, baseETX)
	evm.CoinbaseDeletedHashes = append(evm.CoinbaseDeletedHashes, &baseHash)
	evm.CoinbasesDeleted[baseKey] = []byte{0xaa}

	snapshot := evm.snapshot()

	statedb.AddBalance(account, big.NewInt(2))
	revertedHash := common.BytesToHash([]byte{0x2})
	revertedKey := [47]byte{0x2}
	revertedETX := types.NewTx(&types.ExternalTx{
		To:                addressPtr(common.HexToAddress("0x0000000000000000000000000000000000000004", location)),
		Sender:            common.HexToAddress("0x0000000000000000000000000000000000000003", location),
		OriginatingTxHash: common.BytesToHash([]byte{0x2}),
		ETXIndex:          1,
		Gas:               params.TxGas,
		Value:             big.NewInt(2),
	})

	evm.ETXCache = append(evm.ETXCache, revertedETX)
	evm.CoinbaseDeletedHashes = append(evm.CoinbaseDeletedHashes, &revertedHash)
	evm.CoinbasesDeleted[baseKey] = []byte{0xcc}
	evm.CoinbasesDeleted[revertedKey] = []byte{0xbb}

	evm.revertToSnapshot(snapshot)

	if balance := statedb.GetBalance(account); balance.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("unexpected reverted balance: want 1, have %v", balance)
	}
	if !revertsLocalSideEffects {
		if len(evm.ETXCache) != 2 {
			t.Fatalf("unexpected pre-fork etx cache length: want 2, have %d", len(evm.ETXCache))
		}
		if len(evm.CoinbaseDeletedHashes) != 2 {
			t.Fatalf("unexpected pre-fork deleted hash length: want 2, have %d", len(evm.CoinbaseDeletedHashes))
		}
		if len(evm.CoinbasesDeleted) != 2 {
			t.Fatalf("unexpected pre-fork deleted map length: want 2, have %d", len(evm.CoinbasesDeleted))
		}
		if !bytes.Equal(evm.CoinbasesDeleted[baseKey], []byte{0xcc}) {
			t.Fatalf("unexpected pre-fork deleted map value: %x", evm.CoinbasesDeleted[baseKey])
		}
		return
	}
	if len(evm.ETXCache) != 1 {
		t.Fatalf("unexpected etx cache length: want 1, have %d", len(evm.ETXCache))
	}
	if evm.ETXCache[0].Hash() != baseETX.Hash() {
		t.Fatalf("unexpected etx retained after revert")
	}
	if len(evm.CoinbaseDeletedHashes) != 1 {
		t.Fatalf("unexpected deleted hash length: want 1, have %d", len(evm.CoinbaseDeletedHashes))
	}
	if *evm.CoinbaseDeletedHashes[0] != baseHash {
		t.Fatalf("unexpected deleted hash retained after revert")
	}
	if len(evm.CoinbasesDeleted) != 1 {
		t.Fatalf("unexpected deleted map length: want 1, have %d", len(evm.CoinbasesDeleted))
	}
	if !bytes.Equal(evm.CoinbasesDeleted[baseKey], []byte{0xaa}) {
		t.Fatalf("unexpected deleted map value after revert: %x", evm.CoinbasesDeleted[baseKey])
	}
	if _, exists := evm.CoinbasesDeleted[revertedKey]; exists {
		t.Fatalf("reverted deleted map entry still present")
	}
}

func addressPtr(addr common.Address) *common.Address {
	return &addr
}
