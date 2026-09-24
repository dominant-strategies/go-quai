package vm

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/holiman/uint256"
)

func TestOpETXRejectsCrossZoneQiDestinationWithoutDebit(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnvAt(t, params.SecurityHardeningForkBlock)
	toAddr := common.HexToAddress("0x0180000000000000000000000000000000000000", common.Location{0, 0})
	if !toAddr.IsInQiLedgerScope() || common.IsInChainScope(toAddr.Bytes(), common.Location{0, 0}) {
		t.Fatal("test requires a cross-zone Qi destination")
	}
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))
	stack := newstack()
	defer returnStack(stack)
	for i := 0; i < 4; i++ {
		stack.push(uint256.NewInt(0))
	}
	stack.push(uint256.NewInt(1))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(uint256.NewInt(14))
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))
	pc := uint64(0)
	if _, err := opETX(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatal(err)
	}
	assertRejectedWithoutSideEffects(t, "opETX", stack, evm, statedb, sender, initialBalance)
}

func TestOpETXRejectsUnderfundedPayloadWithoutDebit(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnvAt(t, params.SecurityHardeningForkBlock)
	toAddr := common.HexToAddress("0x0100000000000000000000000000000000000000", common.Location{0, 0})
	if !toAddr.IsInQuaiLedgerScope() || common.IsInChainScope(toAddr.Bytes(), common.Location{0, 0}) {
		t.Fatal("test requires a cross-zone Quai destination")
	}
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))
	stack := newstack()
	defer returnStack(stack)
	stack.push(uint256.NewInt(0)) // access list size
	stack.push(uint256.NewInt(0)) // access list offset
	stack.push(uint256.NewInt(1)) // data size
	stack.push(uint256.NewInt(0)) // data offset
	stack.push(uint256.NewInt(0)) // fee cap
	stack.push(uint256.NewInt(0)) // tip cap
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(uint256.NewInt(1))
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))
	memory := NewMemory()
	memory.Resize(32)
	memory.Set(0, 1, []byte{1})
	pc := uint64(0)
	if _, err := opETX(&pc, evm.interpreter, &ScopeContext{Memory: memory, Stack: stack, Contract: contract}); err != nil {
		t.Fatal(err)
	}
	assertRejectedWithoutSideEffects(t, "opETX", stack, evm, statedb, sender, initialBalance)

	if !etxGasCoversIntrinsic(params.TxGas+params.TxDataNonZeroGas, []byte{1}, nil) {
		t.Fatal("adequately funded ETX payload rejected")
	}
}
