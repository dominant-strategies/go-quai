package vm

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/holiman/uint256"
)

func TestOpETXCreationIntrinsicBeforeDebit(t *testing.T) {
	creation := common.ZeroAddress(common.Location{0, 1})
	ordinary := common.HexToAddress("0x0122000000000000000000000000000000000000", common.Location{0, 0})
	for _, tc := range []struct {
		name   string
		to     common.Address
		gas    uint64
		data   []byte
		height uint64
		accept bool
	}{
		{"creation_below_intrinsic", creation, params.TxGasContractCreation - 1, nil, params.SecurityHardeningForkBlock, false},
		{"creation_at_intrinsic", creation, params.TxGasContractCreation, nil, params.SecurityHardeningForkBlock, true},
		{"creation_payload_below_intrinsic", creation, params.TxGasContractCreation, []byte{1}, params.SecurityHardeningForkBlock, false},
		{"creation_payload_at_intrinsic", creation, params.TxGasContractCreation + params.TxDataNonZeroGas, []byte{1}, params.SecurityHardeningForkBlock, true},
		{"ordinary_transfer", ordinary, params.TxGas, nil, params.SecurityHardeningForkBlock, true},
		{"historical_creation", creation, params.TxGas, nil, params.SecurityHardeningForkBlock - 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evm, statedb, contract, sender := newValueOverflowTestEnvAt(t, tc.height)
			before := new(big.Int).Set(statedb.GetBalance(sender))
			stack := newstack()
			defer returnStack(stack)
			stack.push(uint256.NewInt(0)) // access-list size
			stack.push(uint256.NewInt(0)) // access-list offset
			stack.push(uint256.NewInt(uint64(len(tc.data))))
			stack.push(uint256.NewInt(0)) // data offset
			stack.push(uint256.NewInt(0)) // fee cap
			stack.push(uint256.NewInt(0)) // tip cap
			stack.push(uint256.NewInt(tc.gas))
			stack.push(uint256.NewInt(1))
			stack.push(new(uint256.Int).SetBytes(tc.to.Bytes()))
			stack.push(uint256.NewInt(0))
			memory := NewMemory()
			if len(tc.data) > 0 {
				memory.Resize(32)
				memory.Set(0, uint64(len(tc.data)), tc.data)
			}
			pc := uint64(0)
			if _, err := opETX(&pc, evm.interpreter, &ScopeContext{Memory: memory, Stack: stack, Contract: contract}); err != nil {
				t.Fatal(err)
			}
			if tc.accept {
				if stack.len() != 1 || stack.peek().IsZero() || len(evm.ETXCache) != 1 {
					t.Fatalf("valid ETX rejected: status %v count %d", stack.peek(), len(evm.ETXCache))
				}
				if got := statedb.GetBalance(sender); got.Cmp(new(big.Int).Sub(before, big.NewInt(1))) != 0 {
					t.Fatalf("sender balance: got %v", got)
				}
			} else {
				assertRejectedWithoutSideEffects(t, "opETX", stack, evm, statedb, sender, before)
			}
		})
	}
	if etxGasCoversIntrinsicForRecipient(params.TxGasContractCreation, creation, []byte{1}, nil) {
		t.Fatal("creation ETX omitted payload cost")
	}
	if !etxGasCoversIntrinsicForRecipient(params.TxGasContractCreation+params.TxDataNonZeroGas, creation, []byte{1}, nil) {
		t.Fatal("creation ETX rejected exact payload cost")
	}
}

func TestCreateETXCreationIntrinsicBeforeDebit(t *testing.T) {
	creation := common.ZeroAddress(common.Location{0, 1})
	ordinary := common.HexToAddress("0x0122000000000000000000000000000000000000", common.Location{0, 0})
	for _, tc := range []struct {
		name   string
		to     common.Address
		gas    uint64
		data   []byte
		access types.AccessList
		height uint64
		accept bool
	}{
		{"creation_below_intrinsic", creation, params.TxGasContractCreation - 1, nil, nil, params.SecurityHardeningForkBlock, false},
		{"creation_at_intrinsic", creation, params.TxGasContractCreation, nil, nil, params.SecurityHardeningForkBlock, true},
		{"creation_payload_below_intrinsic", creation, params.TxGasContractCreation, []byte{1}, nil, params.SecurityHardeningForkBlock, false},
		{"creation_payload_at_intrinsic", creation, params.TxGasContractCreation + params.TxDataNonZeroGas, []byte{1}, nil, params.SecurityHardeningForkBlock, true},
		{"creation_access_list_below_intrinsic", creation, params.TxGasContractCreation, nil, types.AccessList{{}}, params.SecurityHardeningForkBlock, false},
		{"creation_access_list_at_intrinsic", creation, params.TxGasContractCreation + params.TxAccessListAddressGas, nil, types.AccessList{{}}, params.SecurityHardeningForkBlock, true},
		{"ordinary_transfer", ordinary, params.TxGas, nil, nil, params.SecurityHardeningForkBlock, true},
		{"historical_creation", creation, params.TxGas, nil, nil, params.SecurityHardeningForkBlock - 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evm, statedb, contract, sender := newValueOverflowTestEnvAt(t, tc.height)
			evm.AccessList = tc.access
			before := new(big.Int).Set(statedb.GetBalance(sender))
			_, _, _, err := evm.CreateETX(tc.to, contract.Address(), params.ETXGas+tc.gas, big.NewInt(1), tc.data)
			if tc.accept {
				if err != nil || len(evm.ETXCache) != 1 || evm.ETXCache[0].Gas() != tc.gas {
					t.Fatalf("valid ETX rejected: err %v count %d", err, len(evm.ETXCache))
				}
				if got := statedb.GetBalance(sender); got.Cmp(new(big.Int).Sub(before, big.NewInt(1))) != 0 {
					t.Fatalf("sender balance: got %v", got)
				}
			} else {
				if err == nil || len(evm.ETXCache) != 0 || statedb.GetBalance(sender).Cmp(before) != 0 {
					t.Fatalf("underfunded ETX changed state: err %v count %d", err, len(evm.ETXCache))
				}
			}
		})
	}
}
