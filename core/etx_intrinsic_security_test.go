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

func TestUnderfundedETXBecomesFailedReceiptAfterFork(t *testing.T) {
	location := common.Location{0, 0}
	db := rawdb.NewMemoryDatabase(log.Global)
	statedb, err := state.New(common.Hash{}, common.Hash{}, new(big.Int), state.NewDatabase(db), state.NewDatabase(db), nil, location, log.Global)
	if err != nil {
		t.Fatal(err)
	}
	to := common.ZeroAddress(location)
	msg := types.NewMessage(to, &to, 0, big.NewInt(0), params.TxGas, big.NewInt(0), []byte{1}, nil, true)
	chainConfig := *params.TestChainConfig
	chainConfig.Location = location
	for _, tc := range []struct {
		name   string
		height uint64
		soft   bool
	}{
		{"before_fork", params.SecurityHardeningForkBlock - 1, false},
		{"after_fork", params.SecurityHardeningForkBlock, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evm := vm.NewEVM(vm.BlockContext{
				BlockNumber:         new(big.Int).SetUint64(tc.height),
				PrimeTerminusNumber: tc.height,
				GasLimit:            1_000_000,
			}, vm.TxContext{}, statedb, &chainConfig, vm.Config{}, nil)
			pool := new(types.GasPool).AddGas(1_000_000)
			result, err := NewStateTransition(evm, msg, pool).TransitionDb()
			if tc.soft {
				if err != nil || result == nil || !errors.Is(result.Err, ErrIntrinsicGas) || result.UsedGas != params.TxGas {
					t.Fatalf("underfunded ETX was not a failed execution: result %+v, error %v", result, err)
				}
				if result.QuaiFees == nil || result.QuaiFees.Sign() != 0 {
					t.Fatalf("failed ETX fees must be zero, got %v", result.QuaiFees)
				}
				if got := new(big.Int).Add(big.NewInt(7), result.QuaiFees); got.Cmp(big.NewInt(7)) != 0 {
					t.Fatalf("failed ETX changed aggregated fees: %v", got)
				}
				if pool.Gas() != 1_000_000-params.TxGas {
					t.Fatalf("ETX gas pool debit: %d", pool.Gas())
				}
			} else if !errors.Is(err, ErrIntrinsicGas) || result != nil {
				t.Fatalf("historical intrinsic-gas result changed: result %+v, error %v", result, err)
			}
		})
	}
}

func TestOverLimitETXAcrossSecurityFork(t *testing.T) {
	location := common.Location{0, 0}
	db := rawdb.NewMemoryDatabase(log.Global)
	statedb, err := state.New(common.Hash{}, common.Hash{}, new(big.Int), state.NewDatabase(db), state.NewDatabase(db), nil, location, log.Global)
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x0022000000000000000000000000000000000000", location)
	msg := types.NewMessage(to, &to, 0, big.NewInt(0), 200_001, big.NewInt(0), nil, nil, true)
	chainConfig := *params.TestChainConfig
	chainConfig.Location = location
	for _, tc := range []struct {
		name   string
		height uint64
		soft   bool
	}{
		{"before_fork", params.SecurityHardeningForkBlock - 1, false},
		{"after_fork", params.SecurityHardeningForkBlock, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evm := vm.NewEVM(vm.BlockContext{
				BlockNumber:         big.NewInt(1),
				PrimeTerminusNumber: tc.height,
				GasLimit:            1_000_000,
			}, vm.TxContext{}, statedb, &chainConfig, vm.Config{}, nil)
			pool := new(types.GasPool).AddGas(1_000_000)
			result, err := NewStateTransition(evm, msg, pool).TransitionDb()
			if !tc.soft {
				if !errors.Is(err, ErrEtxGasLimitReached) || result != nil || pool.Gas() != 1_000_000 {
					t.Fatalf("pre-fork ETX must reject without charging gas: result %+v, error %v, pool %d", result, err, pool.Gas())
				}
				return
			}
			if err != nil || result == nil || !errors.Is(result.Err, ErrEtxGasLimitReached) {
				t.Fatalf("post-fork ETX was not a failed execution: result %+v, error %v", result, err)
			}
			if result.QuaiFees == nil || result.QuaiFees.Sign() != 0 {
				t.Fatalf("failed ETX fees must be zero, got %v", result.QuaiFees)
			}
			if got := new(big.Int).Add(big.NewInt(7), result.QuaiFees); got.Cmp(big.NewInt(7)) != 0 {
				t.Fatalf("failed ETX changed aggregated fees: %v", got)
			}
			if result.UsedGas != params.TxGas || pool.Gas() != 1_000_000-params.TxGas {
				t.Fatalf("failed ETX gas accounting: result %d pool %d", result.UsedGas, pool.Gas())
			}
		})
	}
}
