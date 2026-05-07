package vm

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/holiman/uint256"
)

func TestGasExtCodeCopyUsesBaseCopyGas(t *testing.T) {
	location := common.Location{0, 0}
	chainConfig := *params.TestChainConfig
	chainConfig.Location = location
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
		t.Fatalf("state.New: %v", err)
	}

	env := NewEVM(
		BlockContext{
			BlockNumber:   new(big.Int),
			QuaiStateSize: new(big.Int),
		},
		TxContext{},
		statedb,
		&chainConfig,
		Config{},
		nil,
	)
	stack := newstack()
	contractAddr := common.BytesToAddress(common.OneInternal(location).Bytes(), location)
	targetAddr := common.BytesToAddress(common.ZeroInternal(location).Bytes(), location)
	contract := NewContract(AccountRef(contractAddr), AccountRef(contractAddr), new(big.Int), 0)

	// EXTCODECOPY stack order: address, memOffset, codeOffset, length.
	stack.push(uint256.NewInt(32))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(new(uint256.Int).SetBytes(targetAddr.Bytes()))

	memSize, overflow := memoryExtCodeCopy(stack)
	if overflow {
		t.Fatal("memoryExtCodeCopy overflow")
	}
	memorySize, overflow := math.SafeMul(toWordSize(memSize), 32)
	if overflow {
		t.Fatal("memory size overflow")
	}

	baseGas, baseStateGas, err := gasExtCodeCopyBase(env, contract, stack, NewMemory(), memorySize)
	if err != nil {
		t.Fatalf("gasExtCodeCopyBase: %v", err)
	}
	if baseStateGas != 0 {
		t.Fatalf("unexpected base state gas: %d", baseStateGas)
	}

	t.Run("warm access", func(t *testing.T) {
		statedb.AddAddressToAccessList(targetAddr.Bytes20())

		gas, stateGas, err := gasExtCodeCopy(env, contract, stack, NewMemory(), memorySize)
		if err != nil {
			t.Fatalf("gasExtCodeCopy: %v", err)
		}
		if gas != baseGas {
			t.Fatalf("warm gas mismatch: want %d have %d", baseGas, gas)
		}
		if stateGas != baseStateGas {
			t.Fatalf("warm state gas mismatch: want %d have %d", baseStateGas, stateGas)
		}
	})

	t.Run("cold access", func(t *testing.T) {
		statedb, err = state.New(
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
			t.Fatalf("state.New: %v", err)
		}
		env.StateDB = statedb

		contractInternal, err := contract.Address().InternalAddress()
		if err != nil {
			t.Fatalf("contract internal address: %v", err)
		}
		contractSize := statedb.GetSize(contractInternal)
		coldAccountAccessCost := params.ColdAccountAccessCost(env.Context.QuaiStateSize, contractSize)
		warmStorageReadCost := params.WarmStorageReadCost(env.Context.QuaiStateSize, contractSize)

		wantGas, overflow := math.SafeAdd(baseGas, coldAccountAccessCost-warmStorageReadCost)
		if overflow {
			t.Fatal("expected gas overflow")
		}

		gas, stateGas, err := gasExtCodeCopy(env, contract, stack, NewMemory(), memorySize)
		if err != nil {
			t.Fatalf("gasExtCodeCopy: %v", err)
		}
		if gas != wantGas {
			t.Fatalf("cold gas mismatch: want %d have %d", wantGas, gas)
		}
		if stateGas != coldAccountAccessCost+warmStorageReadCost {
			t.Fatalf("cold state gas mismatch: want %d have %d", coldAccountAccessCost+warmStorageReadCost, stateGas)
		}
	})
}
