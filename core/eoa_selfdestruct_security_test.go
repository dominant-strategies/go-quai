package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

func newEOASelfDestructTestEnv(t *testing.T, height uint64) (*vm.EVM, *state.StateDB, common.Address, common.Address) {
	t.Helper()
	location := common.Location{0, 0}
	db := rawdb.NewMemoryDatabase(log.Global)
	statedb, err := state.New(common.Hash{}, common.Hash{}, new(big.Int), state.NewDatabase(db), state.NewDatabase(db), nil, location, log.Global)
	if err != nil {
		t.Fatal(err)
	}
	from := common.HexToAddress("0x0000000000000000000000000000000000000001", location)
	beneficiary := common.HexToAddress("0x0000000000000000000000000000000000000002", location)
	fromInternal, _ := from.InternalAndQuaiAddress()
	statedb.AddBalance(fromInternal, big.NewInt(1_000_000))
	chainConfig := *params.TestChainConfig
	chainConfig.Location = location
	evm := vm.NewEVM(vm.BlockContext{
		BlockNumber:         new(big.Int).SetUint64(height),
		PrimeTerminusNumber: height,
		BaseFee:             big.NewInt(1),
		QuaiStateSize:       big.NewInt(1_000_000),
		GasLimit:            1_000_000,
	}, vm.TxContext{}, statedb, &chainConfig, vm.Config{}, nil)
	return evm, statedb, from, beneficiary
}

func eoaSelfDestructMessage(from, beneficiary common.Address, nonce uint64) types.Message {
	data := append([]byte("Suicide"), beneficiary.Bytes()...)
	return types.NewMessage(from, &from, nonce, big.NewInt(0), 25_000, big.NewInt(1), data, nil, false)
}

func TestEOASelfDestructPreservesNonceAfterSharedFork(t *testing.T) {
	height := params.SecurityHardeningForkBlock + 10
	setForkForTest(t, &params.PolarisForkBlock, height)
	evm, statedb, from, beneficiary := newEOASelfDestructTestEnv(t, height)
	fromInternal, _ := from.InternalAndQuaiAddress()
	beneficiaryInternal, _ := beneficiary.InternalAndQuaiAddress()
	msg := eoaSelfDestructMessage(from, beneficiary, 0)
	if _, err := ApplyMessage(evm, msg, new(types.GasPool).AddGas(1_000_000)); err != nil {
		t.Fatal(err)
	}
	if statedb.HasSuicided(fromInternal) {
		t.Fatal("post-fork EOA self-destruct deleted the nonce")
	}
	if got := statedb.GetBalance(beneficiaryInternal); got.Cmp(big.NewInt(975_000)) != 0 {
		t.Fatalf("beneficiary received a state-rent mint: %v", got)
	}
	statedb.Finalize(true)
	if got := statedb.GetNonce(fromInternal); got != 1 {
		t.Fatalf("nonce after finalization: got %d, want 1", got)
	}
	root, err := statedb.Commit(true)
	if err != nil {
		t.Fatalf("commit nonce tombstone: %v", err)
	}
	reloaded, err := state.New(root, common.Hash{}, new(big.Int), statedb.Database(), statedb.ETXDatabase(), nil, common.Location{0, 0}, log.Global)
	if err != nil {
		t.Fatalf("reload nonce tombstone: %v", err)
	}
	if got := reloaded.GetNonce(fromInternal); got != 1 {
		t.Fatalf("nonce after trie reload: got %d, want 1", got)
	}
	statedb.AddBalance(fromInternal, big.NewInt(1_000_000))
	if _, err := ApplyMessage(evm, msg, new(types.GasPool).AddGas(1_000_000)); !errors.Is(err, ErrNonceTooLow) {
		t.Fatalf("old signed transaction replayed: %v", err)
	}
	if _, err := ApplyMessage(evm, eoaSelfDestructMessage(from, beneficiary, 1), new(types.GasPool).AddGas(1_000_000)); err != nil {
		t.Fatalf("next nonce rejected: %v", err)
	}
	statedb.Finalize(true)
	if got := statedb.GetNonce(fromInternal); got != 2 {
		t.Fatalf("nonce after second self-destruct: got %d, want 2", got)
	}
	if got := statedb.GetBalance(beneficiaryInternal); got.Cmp(big.NewInt(1_950_000)) != 0 {
		t.Fatalf("second transaction minted a refund: %v", got)
	}
}

func TestEOASelfDestructHistoricalRuleRemains(t *testing.T) {
	height := params.SecurityHardeningForkBlock + 10
	setForkForTest(t, &params.PolarisForkBlock, height)
	evm, statedb, from, beneficiary := newEOASelfDestructTestEnv(t, height-1)
	fromInternal, _ := from.InternalAndQuaiAddress()
	beneficiaryInternal, _ := beneficiary.InternalAndQuaiAddress()
	msg := eoaSelfDestructMessage(from, beneficiary, 0)
	if _, err := ApplyMessage(evm, msg, new(types.GasPool).AddGas(1_000_000)); err != nil {
		t.Fatal(err)
	}
	if !statedb.HasSuicided(fromInternal) || statedb.GetBalance(beneficiaryInternal).Cmp(big.NewInt(975_000)) <= 0 {
		t.Fatal("historical suicide or refund behavior changed")
	}
	statedb.Finalize(true)
	if got := statedb.GetNonce(fromInternal); got != 0 {
		t.Fatalf("historical nonce reset changed: %d", got)
	}
	statedb.AddBalance(fromInternal, big.NewInt(1_000_000))
	if _, err := ApplyMessage(evm, msg, new(types.GasPool).AddGas(1_000_000)); err != nil {
		t.Fatalf("historical signed transaction no longer replays: %v", err)
	}
}

func TestEOASelfDestructToSelfBurnsBalanceAfterSharedFork(t *testing.T) {
	height := params.SecurityHardeningForkBlock + 10
	setForkForTest(t, &params.PolarisForkBlock, height)
	evm, statedb, from, _ := newEOASelfDestructTestEnv(t, height)
	fromInternal, _ := from.InternalAndQuaiAddress()
	if _, err := ApplyMessage(evm, eoaSelfDestructMessage(from, from, 0), new(types.GasPool).AddGas(1_000_000)); err != nil {
		t.Fatal(err)
	}
	statedb.Finalize(true)
	if statedb.GetBalance(fromInternal).Sign() != 0 || statedb.GetNonce(fromInternal) != 1 {
		t.Fatalf("self-beneficiary result: balance %v, nonce %d", statedb.GetBalance(fromInternal), statedb.GetNonce(fromInternal))
	}
}
