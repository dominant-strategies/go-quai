package vm

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/holiman/uint256"
)

func newValueOverflowTestEnv(t *testing.T) (*EVM, *state.StateDB, *Contract, common.InternalAddress) {
	return newValueOverflowTestEnvAt(t, params.SelfDestructRefundForkBlock)
}

func newValueOverflowTestEnvAt(t *testing.T, primeTerminusNumber uint64) (*EVM, *state.StateDB, *Contract, common.InternalAddress) {
	t.Helper()

	location := common.Location{0, 0}
	chainConfig := *params.TestChainConfig
	chainConfig.Location = location

	db := rawdb.NewMemoryDatabase(log.Global)
	statedb, err := state.New(
		common.Hash{},
		common.Hash{},
		new(big.Int),
		state.NewDatabase(db),
		state.NewDatabase(db),
		nil,
		location,
		log.Global,
	)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	canTransfer := func(db StateDB, addr common.Address, amount *big.Int) bool {
		internal, err := addr.InternalAndQuaiAddress()
		if err != nil {
			return false
		}
		return db.GetBalance(internal).Cmp(amount) >= 0
	}
	checkEtxEligible := func(common.Hash, common.Location) bool {
		return true
	}

	evm := NewEVM(
		BlockContext{
			CanTransfer:         canTransfer,
			CheckIfEtxEligible:  checkEtxEligible,
			BlockNumber:         big.NewInt(1),
			QuaiStateSize:       new(big.Int),
			PrimeTerminusNumber: primeTerminusNumber,
		},
		TxContext{
			Hash:     common.HexToHash("0x1"),
			GasPrice: big.NewInt(1),
		},
		statedb,
		&chainConfig,
		Config{},
		nil,
	)

	contractAddr := common.BytesToAddress(common.OneInternal(location).Bytes(), location)
	internalAddr, err := contractAddr.InternalAndQuaiAddress()
	if err != nil {
		t.Fatalf("InternalAndQuaiAddress: %v", err)
	}
	statedb.CreateAccount(internalAddr)
	statedb.AddBalance(internalAddr, big.NewInt(1))

	contract := NewContract(AccountRef(contractAddr), AccountRef(contractAddr), new(big.Int), 0)
	return evm, statedb, contract, internalAddr
}

func wrappingValueForFee(fee uint64, wrappedDebit uint64) (*big.Int, *uint256.Int) {
	twoTo256 := new(big.Int).Lsh(big.NewInt(1), 256)
	value := new(big.Int).Sub(twoTo256, new(big.Int).SetUint64(fee))
	value.Add(value, new(big.Int).SetUint64(wrappedDebit))
	value256, _ := uint256.FromBig(value)
	return value, value256
}

func uint256FromBigForTest(t *testing.T, value *big.Int) *uint256.Int {
	t.Helper()

	value256, overflow := uint256.FromBig(value)
	if overflow {
		t.Fatalf("test value overflows uint256: %v", value)
	}
	return value256
}

func maxUint256ForTest(t *testing.T) *uint256.Int {
	t.Helper()

	twoTo256 := new(big.Int).Lsh(big.NewInt(1), 256)
	return uint256FromBigForTest(t, new(big.Int).Sub(twoTo256, big.NewInt(1)))
}

func overflowingUint64ForTest(t *testing.T) *uint256.Int {
	t.Helper()

	return uint256FromBigForTest(t, new(big.Int).Lsh(big.NewInt(1), 64))
}

func minConversionValueForTest(t *testing.T) *uint256.Int {
	t.Helper()

	return uint256FromBigForTest(t, params.MinQuaiConversionAmount)
}

func assertRejectedWithoutSideEffects(t *testing.T, opName string, stack *Stack, evm *EVM, statedb *state.StateDB, sender common.InternalAddress, initialBalance *big.Int) {
	t.Helper()

	if stack.len() != 1 || !stack.peek().IsZero() {
		t.Fatalf("%s should reject with zero status", opName)
	}
	if balance := statedb.GetBalance(sender); balance.Cmp(initialBalance) != 0 {
		t.Fatalf("%s changed sender balance on rejected overflow: have %v want %v", opName, balance, initialBalance)
	}
	if len(evm.ETXCache) != 0 {
		t.Fatalf("%s overflow should not emit ETX, have %d", opName, len(evm.ETXCache))
	}
}

func assertAcceptedWithWrappedDebit(t *testing.T, opName string, stack *Stack, evm *EVM, statedb *state.StateDB, sender common.InternalAddress, wantBalance *big.Int) {
	t.Helper()

	if stack.len() != 1 || stack.peek().IsZero() {
		t.Fatalf("%s should accept with non-zero status", opName)
	}
	if balance := statedb.GetBalance(sender); balance.Cmp(wantBalance) != 0 {
		t.Fatalf("%s sender balance mismatch: have %v want %v", opName, balance, wantBalance)
	}
	if len(evm.ETXCache) != 1 {
		t.Fatalf("%s should emit one ETX, have %d", opName, len(evm.ETXCache))
	}
}

func TestOpConvertAllowsLegacyValueDebitOverflowBeforeFork(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnvAt(t, params.SelfDestructRefundForkBlock-1)

	_, stackValue := wrappingValueForFee(params.TxGas, 1)
	toAddr := common.HexToAddress("0x0083e45Aa16163f2663015B6695894D918866D19", common.Location{0, 0})

	stack := newstack()
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(stackValue)
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opConvert(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opConvert returned error: %v", err)
	}
	assertAcceptedWithWrappedDebit(t, "opConvert", stack, evm, statedb, sender, new(big.Int))
}

func TestOpETXAllowsLegacyValueDebitOverflowBeforeFork(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnvAt(t, params.SelfDestructRefundForkBlock-1)

	_, stackValue := wrappingValueForFee(params.TxGas, 1)
	toAddr := crypto.CreateAddress(common.HexToAddress("0x0100000000000000000000000000000000000000", common.Location{0, 0}), 1, nil, common.Location{0, 1})

	stack := newstack()
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(1))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(stackValue)
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opETX(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opETX returned error: %v", err)
	}
	assertAcceptedWithWrappedDebit(t, "opETX", stack, evm, statedb, sender, new(big.Int))
}

func TestOpConvertRejectsValueDebitOverflow(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnv(t)

	_, stackValue := wrappingValueForFee(params.TxGas, 1)
	toAddr := common.HexToAddress("0x0083e45Aa16163f2663015B6695894D918866D19", common.Location{0, 0})
	if _, err := toAddr.InternalAndQiAddress(); err != nil {
		t.Fatalf("expected internal Qi address: %v", err)
	}
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))

	stack := newstack()
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(stackValue)
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opConvert(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opConvert returned error: %v", err)
	}
	assertRejectedWithoutSideEffects(t, "opConvert", stack, evm, statedb, sender, initialBalance)
}

func TestOpETXRejectsValueDebitOverflow(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnv(t)

	_, stackValue := wrappingValueForFee(params.TxGas, 1)
	toAddr := crypto.CreateAddress(common.HexToAddress("0x0100000000000000000000000000000000000000", common.Location{0, 0}), 1, nil, common.Location{0, 1})
	if common.IsInChainScope(toAddr.Bytes(), common.Location{0, 0}) {
		t.Fatalf("expected external destination address")
	}
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))

	stack := newstack()
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(1))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(stackValue)
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opETX(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opETX returned error: %v", err)
	}
	assertRejectedWithoutSideEffects(t, "opETX", stack, evm, statedb, sender, initialBalance)
}

func TestOpETXRejectsFeeAdditionOverflow(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnv(t)

	toAddr := crypto.CreateAddress(common.HexToAddress("0x0100000000000000000000000000000000000000", common.Location{0, 0}), 1, nil, common.Location{0, 1})
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))

	stack := newstack()
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(1))
	stack.push(maxUint256ForTest(t))
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(uint256.NewInt(1))
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opETX(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opETX returned error: %v", err)
	}
	assertRejectedWithoutSideEffects(t, "opETX", stack, evm, statedb, sender, initialBalance)
}

func TestOpETXRejectsFeeMultiplicationOverflow(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnv(t)

	toAddr := crypto.CreateAddress(common.HexToAddress("0x0100000000000000000000000000000000000000", common.Location{0, 0}), 1, nil, common.Location{0, 1})
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))
	largeTip := uint256FromBigForTest(t, new(big.Int).Lsh(big.NewInt(1), 255))

	stack := newstack()
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(uint256.NewInt(0))
	stack.push(largeTip)
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(uint256.NewInt(1))
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opETX(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opETX returned error: %v", err)
	}
	assertRejectedWithoutSideEffects(t, "opETX", stack, evm, statedb, sender, initialBalance)
}

func TestOpConvertRejectsGasLimitUint64Overflow(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnv(t)

	toAddr := common.HexToAddress("0x0083e45Aa16163f2663015B6695894D918866D19", common.Location{0, 0})
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))

	stack := newstack()
	stack.push(overflowingUint64ForTest(t))
	stack.push(minConversionValueForTest(t))
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opConvert(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opConvert returned error: %v", err)
	}
	assertRejectedWithoutSideEffects(t, "opConvert", stack, evm, statedb, sender, initialBalance)
}

func TestOpConvertRejectsGasPriceUint256Overflow(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnv(t)

	evm.GasPrice = new(big.Int).Lsh(big.NewInt(1), 256)
	toAddr := common.HexToAddress("0x0083e45Aa16163f2663015B6695894D918866D19", common.Location{0, 0})
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))

	stack := newstack()
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(minConversionValueForTest(t))
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opConvert(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opConvert returned error: %v", err)
	}
	assertRejectedWithoutSideEffects(t, "opConvert", stack, evm, statedb, sender, initialBalance)
}

func TestOpConvertRejectsFeeMultiplicationOverflow(t *testing.T) {
	evm, statedb, contract, sender := newValueOverflowTestEnv(t)

	evm.GasPrice = new(big.Int).Lsh(big.NewInt(1), 255)
	toAddr := common.HexToAddress("0x0083e45Aa16163f2663015B6695894D918866D19", common.Location{0, 0})
	initialBalance := new(big.Int).Set(statedb.GetBalance(sender))

	stack := newstack()
	stack.push(uint256.NewInt(params.TxGas))
	stack.push(minConversionValueForTest(t))
	stack.push(new(uint256.Int).SetBytes(toAddr.Bytes()))
	stack.push(uint256.NewInt(0))

	pc := uint64(0)
	if _, err := opConvert(&pc, evm.interpreter, &ScopeContext{Memory: NewMemory(), Stack: stack, Contract: contract}); err != nil {
		t.Fatalf("opConvert returned error: %v", err)
	}
	assertRejectedWithoutSideEffects(t, "opConvert", stack, evm, statedb, sender, initialBalance)
}
