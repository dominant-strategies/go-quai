package vm

import (
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/holiman/uint256"
)

func TestETXChargesForMemoryBeforeExpansion(t *testing.T) {
	location := common.Location{0, 0}
	db := rawdb.NewMemoryDatabase(log.Global)
	st, err := state.New(common.Hash{}, common.Hash{}, new(big.Int), state.NewDatabase(db), state.NewDatabase(db), nil, location, log.Global)
	if err != nil {
		t.Fatal(err)
	}
	evm := NewEVM(BlockContext{BlockNumber: new(big.Int).SetUint64(params.SecurityHardeningForkBlock), PrimeTerminusNumber: params.SecurityHardeningForkBlock}, TxContext{}, st, params.TestChainConfig, Config{}, nil)
	addr := common.ZeroAddress(location)
	// access size, access offset, 1 MiB input size, input offset, fee cap,
	// tip cap, ETX gas, value, destination, call gas.
	code := []byte{0x60, 0, 0x60, 0, 0x62, 0x10, 0, 0, 0x60, 0, 0x60, 0, 0x60, 0, 0x61, 0x52, 0x08, 0x60, 0, 0x60, 0, 0x60, 0, byte(ETX), byte(STOP)}
	lowGas := NewContract(AccountRef(addr), AccountRef(addr), big.NewInt(0), 50000)
	lowGas.SetCallCode(&addr, common.Hash{}, code)
	if _, err := evm.Interpreter().Run(lowGas, nil, false); !errors.Is(err, ErrOutOfGas) {
		t.Fatalf("1 MiB ETX with 50,000 gas: got %v, want out of gas", err)
	}
	legacyHeight := params.SecurityHardeningForkBlock - 1
	legacyEVM := NewEVM(BlockContext{BlockNumber: new(big.Int).SetUint64(legacyHeight), PrimeTerminusNumber: legacyHeight}, TxContext{}, st, params.TestChainConfig, Config{}, nil)
	legacyGas := NewContract(AccountRef(addr), AccountRef(addr), big.NewInt(0), 50000)
	legacyGas.SetCallCode(&addr, common.Hash{}, code)
	if _, err := legacyEVM.Interpreter().Run(legacyGas, nil, false); err != nil {
		t.Fatalf("historical ETX memory gas changed: %v", err)
	}
	highGas := NewContract(AccountRef(addr), AccountRef(addr), big.NewInt(0), 3000000)
	highGas.SetCallCode(&addr, common.Hash{}, code)
	if _, err := evm.Interpreter().Run(highGas, nil, false); err != nil {
		t.Fatalf("adequately funded ETX memory expansion: %v", err)
	}
	if highGas.Gas >= 3000000-50000 {
		t.Fatalf("memory expansion was not charged: %d gas remaining", highGas.Gas)
	}
}

func TestETXMemorySizingPreservesPreForkRule(t *testing.T) {
	stack := newstack()
	defer returnStack(stack)
	for i := 0; i < 10; i++ {
		stack.push(uint256.NewInt(0))
	}
	// The legacy rule sums the ends of both memory ranges. The new rule uses
	// their maximum; they must remain distinct across the fork.
	stack.Data()[2] = *uint256.NewInt(64)
	stack.Data()[0] = *uint256.NewInt(96)
	legacy, overflow := memoryETX(stack)
	if overflow || legacy != 160 {
		t.Fatalf("legacy memory size: %d, overflow %v", legacy, overflow)
	}
	fixed, overflow := memoryETXFixed(stack)
	if overflow || fixed != 96 {
		t.Fatalf("fixed memory size: %d, overflow %v", fixed, overflow)
	}
}
