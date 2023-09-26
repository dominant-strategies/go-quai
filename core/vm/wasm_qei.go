package vm

import (
	"context"
	"fmt"
	"log"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const (
	// QEICallSuccess is the return value in case of a successful contract execution
	QEICallSuccess = 0
	// ErrQEICallFailure is the return value in case of a contract execution failture
	ErrQEICallFailure = 1
	// ErrQEICallRevert is the return value in case a contract calls `revert`
	ErrQEICallRevert = 2

	// Max recursion depth for contracts
	maxCallDepth = 1024

	// Size (in bytes) of a u256
	u256Len = 32

	// Size (in bytes) of a u128
	u128Len = 16

	// Address of the sentinel (metering) contract
	sentinelContractAddress = "0x000000000000000000000000000000000000000a"
)

// List of gas costs
const (
	GasCostZero           = 0
	GasCostBase           = 2
	GasCostVeryLow        = 3
	GasCostLow            = 5
	GasCostMid            = 8
	GasCostHigh           = 10
	GasCostExtCode        = 700
	GasCostBalance        = 400
	GasCostSLoad          = 200
	GasCostJumpDest       = 1
	GasCostSSet           = 20000
	GasCostSReset         = 5000
	GasRefundSClear       = 15000
	GasRefundSelfDestruct = 24000
	GasCostCreate         = 32000
	GasCostCall           = 700
	GasCostCallValue      = 9000
	GasCostCallStipend    = 2300
	GasCostNewAccount     = 25000
	GasCostLog            = 375
	GasCostLogData        = 8
	GasCostLogTopic       = 375
	GasCostCopy           = 3
	GasCostBlockHash      = 800
)

var eeiFunctionList = []string{
	"useGas",
	"getAddress",
	"getExternalBalance",
	"getBlockHash",
	"call",
	"callDataCopy",
	"getCallDataSize",
	"callCode",
	"callDelegate",
	"callStatic",
	"storageStore",
	"storageLoad",
	"getCaller",
	"getCallValue",
	"codeCopy",
	"getCodeSize",
	"getBlockCoinbase",
	"create",
	"getBlockDifficulty",
	"externalCodeCopy",
	"getExternalCodeSize",
	"getGasLeft",
	"getBlockGasLimit",
	"getTxGasPrice",
	"log",
	"getBlockNumber",
	"getTxOrigin",
	"finish",
	"revert",
	"getReturnDataSize",
	"returnDataCopy",
	"selfDestruct",
	"getBlockTimestamp",
}

func InstantiateWASMRuntime(ctx context.Context, in *WASMInterpreter) wazero.Runtime {

	// Create a new WebAssembly Runtime.
	r := wazero.NewRuntime(ctx)

	_, errEnv := r.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(logUint32).Export("hostLogUint32").
		NewFunctionBuilder().WithFunc(logString).Export("hostLogString").
		NewFunctionBuilder().WithFunc(in.useGas).Export("useGas").
		NewFunctionBuilder().WithFunc(in.getAddress).Export("getAddress").
		NewFunctionBuilder().WithFunc(in.getExternalBalance).Export("getExternalBalance").
		NewFunctionBuilder().WithFunc(in.getCaller).Export("getCaller").
		NewFunctionBuilder().WithFunc(in.getBlockHash).Export("getBlockHash").
		NewFunctionBuilder().WithFunc(in.getBlockNumber).Export("getBlockNumber").
		NewFunctionBuilder().WithFunc(in.getBlockTimestamp).Export("getBlockTimestamp").
		Instantiate(ctx)
	if errEnv != nil {
		fmt.Println("🔴 Error with env:", errEnv)
	}

	_, errInstantiate := wasi_snapshot_preview1.Instantiate(ctx, r)
	if errInstantiate != nil {
		fmt.Println("🔴 Error with Instantiate:", errInstantiate)
	}

	fmt.Println("🤖: Instantiated WASM Runtime")

	return r
}

func logUint32(value uint32) {
	fmt.Println("🤖:", value)
}

func logString(ctx context.Context, module api.Module, offset, byteCount uint32) {
	buf, ok := module.Memory().Read(offset, byteCount)
	if !ok {
		log.Panicf("🟥 Memory.Read(%d, %d) out of range", offset, byteCount)
	}
	fmt.Println("👽:", string(buf))
}

func (in WASMInterpreter) gasAccounting(cost uint64) {
	if in.contract == nil {
		panic("nil contract")
	}
	if cost > in.contract.Gas {
		panic(fmt.Sprintf("out of gas %d > %d", cost, in.contract.Gas))
	}
	in.contract.Gas -= cost
}

func (in *WASMInterpreter) useGas(ctx context.Context, module api.Module, amount int64) {
	fmt.Println("🤖: useGas", amount)
	fmt.Println("🤖: useGas", in.contract.Gas)
	in.gasAccounting(uint64(amount))
}

func readSize(ctx context.Context, module api.Module, offset uint32, size uint32) []byte {
	// TODO modify the process interface to find out how much memory is
	// available on the system.
	bytes, ok := module.Memory().Read(offset, size)
	if !ok {
		log.Panicf("🟥 Memory.ReadInto(%d, %d) out of range", offset, size)
	}

	return bytes
}

func writeBytes(ctx context.Context, module api.Module, bytes []byte, offset uint32) {
	if !module.Memory().Write(offset, bytes) {
		log.Panicf("🟥 Memory.Write(%d, %d) out of range", offset, len(bytes))
	}
}

func swapEndian(src []byte) []byte {
	ret := make([]byte, len(src))
	for i, v := range src {
		ret[len(src)-i-1] = v
	}
	return ret
}

func (in *WASMInterpreter) getAddress(ctx context.Context, module api.Module, resultOffset uint32) {
	in.gasAccounting(GasCostBase)
	fmt.Println("🤖: getAddress, addr", in.contract.CodeAddr)
	fmt.Println("🤖: getAddress, offset", resultOffset)
	addr := []byte(in.contract.CodeAddr.String())

	fmt.Println("🤖: getAddress, addr", len(addr), in.contract.CodeAddr.String())
	fmt.Println("🤖: getAddress, size", module.Memory().Size())
	// 	if !module.Memory().Write(resultOffset, addr) {
	// 		log.Panicf("🟥 Memory.Write(%d, %d) out of range", resultOffset, len(addr))
	// 	}
	writeBytes(ctx, module, addr, resultOffset)
}

func (in *WASMInterpreter) getExternalBalance(ctx context.Context, module api.Module, addressOffset uint32, resultOffset int32) {
	in.gasAccounting(in.gasTable.Balance)
	addr := common.BytesToAddress(readSize(ctx, module, addressOffset, common.AddressLength))

	internal, err := addr.InternalAddress()
	if err != nil {
		log.Panicf("🟥 Memory.Write(%d, %d) out of range", resultOffset, len(internal))
	}
	balance := swapEndian(in.StateDB.GetBalance(internal).Bytes())
	writeBytes(ctx, module, balance, uint32(resultOffset))
}

func (in *WASMInterpreter) getBlockHash(ctx context.Context, module api.Module, number int64, resultOffset int32) int32 {
	in.gasAccounting(GasCostBlockHash)
	n := big.NewInt(number)
	fmt.Println(n)
	n.Sub(in.evm.Context.BlockNumber, n)
	fmt.Println(n, n.Cmp(big.NewInt(256)), n.Cmp(big.NewInt(0)))
	if n.Cmp(big.NewInt(256)) > 0 || n.Cmp(big.NewInt(0)) <= 0 {
		return 1
	}
	h := in.evm.Context.GetHash(uint64(number))
	writeBytes(ctx, module, h.Bytes(), uint32(resultOffset))
	return 0
}

func (in *WASMInterpreter) callCommon(ctx context.Context, contract, targetContract *Contract, input []byte, value *big.Int, snapshot int, gas int64, ro bool) int32 {
	if in.evm.depth > maxCallDepth {
		return ErrQEICallFailure
	}

	savedVM := in.vm

	in.Run(targetContract, input, ro)

	in.vm = savedVM
	in.contract = contract

	if value.Cmp(big.NewInt(0)) != 0 {
		in.gasAccounting(uint64(gas) - targetContract.Gas - GasCostCallStipend)
	} else {
		in.gasAccounting(uint64(gas) - targetContract.Gas)
	}

	switch in.terminationType {
	case TerminateFinish:
		return QEICallSuccess
	case TerminateRevert:
		in.StateDB.RevertToSnapshot(snapshot)
		return ErrQEICallRevert
	default:
		in.StateDB.RevertToSnapshot(snapshot)
		contract.UseGas(targetContract.Gas)
		return ErrQEICallFailure
	}
}

func (in *WASMInterpreter) call(ctx context.Context, module api.Module, gas int64, addressOffset uint32, valueOffset uint32, dataOffset uint32, dataLength uint32) int32 {
	contract := in.contract

	// Get the address of the contract to call
	addrInterface := common.BytesToAddress(readSize(ctx, module, addressOffset, common.AddressLength))
	addr, err := addrInterface.InternalAddress()
	if err != nil {
		return ErrQEICallFailure
	}

	// Get the value. The [spec](https://github.com/ewasm/design/blob/master/eth_interface.md#call)
	// requires this operation to be U128, which is incompatible with the EVM version that expects
	// a u256.
	// To be compatible with hera, one must read a u256 value, then check that this is a u128.
	value := big.NewInt(0).SetBytes(swapEndian(readSize(ctx, module, valueOffset, u256Len)))
	check128bits := big.NewInt(1)
	check128bits.Lsh(check128bits, 128)
	if value.Cmp(check128bits) > 0 {
		return ErrQEICallFailure
	}

	internal, err := contract.Address().InternalAddress()
	if err != nil {
		return ErrQEICallFailure
	}

	// Fail if the account's balance is greater than 128bits as discussed
	// in https://github.com/ewasm/hera/issues/456
	if in.StateDB.GetBalance(internal).Cmp(check128bits) > 0 {
		in.gasAccounting(contract.Gas)
		return ErrQEICallRevert
	}

	if in.staticMode == true && value.Cmp(big.NewInt(0)) != 0 {
		in.gasAccounting(in.contract.Gas)
		return ErrQEICallFailure
	}

	in.gasAccounting(GasCostCall)

	if in.evm.depth > maxCallDepth {
		return ErrQEICallFailure
	}

	if value.Cmp(big.NewInt(0)) != 0 {
		in.gasAccounting(GasCostCallValue)
	}

	// Get the arguments.
	// TODO check the need for callvalue (seems not, a lot of that stuff is
	// already accounted for in the functions that I already called - need to
	// refactor all that)
	input := readSize(ctx, module, dataOffset, dataLength)

	snapshot := in.StateDB.Snapshot()

	// Check that there is enough balance to transfer the value
	if in.StateDB.GetBalance(internal).Cmp(value) < 0 {
		return ErrQEICallFailure
	}

	// Check that the contract exists
	if !in.StateDB.Exist(addr) {
		in.gasAccounting(GasCostNewAccount)
		in.StateDB.CreateAccount(addr)
	}

	var calleeGas uint64
	if uint64(gas) > ((63 * contract.Gas) / 64) {
		calleeGas = contract.Gas - (contract.Gas / 64)
	} else {
		calleeGas = uint64(gas)
	}
	in.gasAccounting(calleeGas)

	if value.Cmp(big.NewInt(0)) != 0 {
		calleeGas += GasCostCallStipend
	}

	// TODO tracing

	// Add amount to recipient
	in.evm.Context.Transfer(in.StateDB, contract.Address(), addrInterface, value)

	// Load the contract code in a new VM structure
	targetContract := NewContract(contract, AccountRef(addrInterface), value, calleeGas)
	code := in.StateDB.GetCode(addr)
	if len(code) == 0 {
		in.contract.Gas += calleeGas
		return QEICallSuccess
	}
	targetContract.SetCallCode(&addrInterface, in.StateDB.GetCodeHash(addr), code)

	savedVM := in.vm

	in.Run(targetContract, input, false)

	in.vm = savedVM
	in.contract = contract

	// Add leftover gas
	in.contract.Gas += targetContract.Gas
	defer func() { in.terminationType = TerminateFinish }()

	switch in.terminationType {
	case TerminateFinish:
		return QEICallSuccess
	case TerminateRevert:
		in.StateDB.RevertToSnapshot(snapshot)
		return ErrQEICallRevert
	default:
		in.StateDB.RevertToSnapshot(snapshot)
		contract.UseGas(targetContract.Gas)
		return ErrQEICallFailure
	}
}

func (in *WASMInterpreter) callDataCopy(ctx context.Context, module api.Module, resultOffset int32, dataOffset int32, length int32) {
	in.gasAccounting(GasCostVeryLow + GasCostCopy*(uint64(length+31)>>5))
	writeBytes(ctx, module, in.contract.Input[dataOffset:dataOffset+length], uint32(resultOffset))
}

func (in *WASMInterpreter) getCallDataSize(ctx context.Context, module api.Module) int32 {
	in.gasAccounting(GasCostBase)
	return int32(len(in.contract.Input))
}

func (in *WASMInterpreter) callCode(ctx context.Context, module api.Module, gas int64, addressOffset uint32, valueOffset uint32, dataOffset uint32, dataLength uint32) int32 {
	in.gasAccounting(GasCostCall)

	contract := in.contract

	// Get the address of the contract to call
	addrInterface := common.BytesToAddress(readSize(ctx, module, addressOffset, common.AddressLength))
	addr, err := addrInterface.InternalAddress()
	if err != nil {
		return ErrQEICallFailure
	}

	// Get the value. The [spec](https://github.com/ewasm/design/blob/master/eth_interface.md#call)
	// requires this operation to be U128, which is incompatible with the EVM version that expects
	// a u256.
	value := big.NewInt(0).SetBytes(readSize(ctx, module, valueOffset, u128Len))

	if value.Cmp(big.NewInt(0)) != 0 {
		in.gasAccounting(GasCostCallValue)
		gas += GasCostCallStipend
	}

	// Get the arguments.
	// TODO check the need for callvalue (seems not, a lot of that stuff is
	// already accounted for in the functions that I already called - need to
	// refactor all that)
	input := readSize(ctx, module, dataOffset, dataLength)

	snapshot := in.StateDB.Snapshot()

	// Check that there is enough balance to transfer the value
	if in.StateDB.GetBalance(addr).Cmp(value) < 0 {
		fmt.Printf("Not enough balance: wanted to use %v, got %v\n", value, in.StateDB.GetBalance(addr))
		return ErrQEICallFailure
	}

	// TODO tracing
	// TODO check that EIP-150 is respected

	// Load the contract code in a new VM structure
	targetContract := NewContract(contract.caller, AccountRef(contract.Address()), value, uint64(gas))
	code := in.StateDB.GetCode(addr)
	targetContract.SetCallCode(&addrInterface, in.StateDB.GetCodeHash(addr), code)

	return in.callCommon(ctx, contract, targetContract, input, value, snapshot, gas, false)
}

func (in *WASMInterpreter) callDelegate(ctx context.Context, module api.Module, gas int64, addressOffset uint32, dataOffset uint32, dataLength uint32) int32 {
	in.gasAccounting(GasCostCall)

	contract := in.contract

	// Get the address of the contract to call
	addrInterface := common.BytesToAddress(readSize(ctx, module, addressOffset, common.AddressLength))
	addr, err := addrInterface.InternalAddress()
	if err != nil {
		return ErrQEICallFailure
	}

	// Get the value. The [spec](https://github.com/ewasm/design/blob/master/eth_interface.md#call)
	// requires this operation to be U128, which is incompatible with the EVM version that expects
	// a u256.
	value := contract.value

	if value.Cmp(big.NewInt(0)) != 0 {
		in.gasAccounting(GasCostCallValue)
		gas += GasCostCallStipend
	}

	// Get the arguments.
	// TODO check the need for callvalue (seems not, a lot of that stuff is
	// already accounted for in the functions that I already called - need to
	// refactor all that)
	input := readSize(ctx, module, dataOffset, dataLength)

	snapshot := in.StateDB.Snapshot()

	// Check that there is enough balance to transfer the value
	if in.StateDB.GetBalance(addr).Cmp(value) < 0 {
		fmt.Printf("Not enough balance: wanted to use %v, got %v\n", value, in.StateDB.GetBalance(addr))
		return ErrQEICallFailure
	}

	// TODO tracing
	// TODO check that EIP-150 is respected

	// Load the contract code in a new VM structure
	targetContract := NewContract(AccountRef(contract.Address()), AccountRef(contract.Address()), value, uint64(gas))
	code := in.StateDB.GetCode(addr)
	caddr := contract.Address()
	targetContract.SetCallCode(&caddr, in.StateDB.GetCodeHash(addr), code)

	return in.callCommon(ctx, contract, targetContract, input, value, snapshot, gas, false)
}

func (in *WASMInterpreter) callStatic(ctx context.Context, module api.Module, gas int64, addressOffset uint32, dataOffset uint32, dataLength uint32) int32 {
	contract := in.contract

	// Get the address of the contract to call
	addrInterface := common.BytesToAddress(readSize(ctx, module, addressOffset, common.AddressLength))
	addr, err := addrInterface.InternalAddress()
	if err != nil {
		return ErrQEICallFailure
	}

	value := big.NewInt(0)

	// Get the arguments.
	// TODO check the need for callvalue (seems not, a lot of that stuff is
	// already accounted for in the functions that I already called - need to
	// refactor all that)
	input := readSize(ctx, module, dataOffset, dataLength)

	snapshot := in.StateDB.Snapshot()

	in.gasAccounting(GasCostCall)

	if in.evm.depth > maxCallDepth {
		return ErrQEICallFailure
	}

	// Check that the contract exists
	if !in.StateDB.Exist(addr) {
		in.gasAccounting(GasCostNewAccount)
		in.StateDB.CreateAccount(addr)
	}

	calleeGas := uint64(gas)
	if calleeGas > ((63 * contract.Gas) / 64) {
		calleeGas -= ((63 * contract.Gas) / 64)
	}
	in.gasAccounting(calleeGas)

	// TODO tracing

	// Add amount to recipient
	in.evm.Context.Transfer(in.StateDB, contract.Address(), addrInterface, value)

	// Load the contract code in a new VM structure
	targetContract := NewContract(contract, AccountRef(addrInterface), value, calleeGas)
	code := in.StateDB.GetCode(addr)
	if len(code) == 0 {
		in.contract.Gas += calleeGas
		return QEICallSuccess
	}
	targetContract.SetCallCode(&addrInterface, in.StateDB.GetCodeHash(addr), code)

	savedVM := in.vm
	saveStatic := in.staticMode
	in.staticMode = true
	defer func() { in.staticMode = saveStatic }()

	in.Run(targetContract, input, false)

	in.vm = savedVM
	in.contract = contract

	// Add leftover gas
	in.contract.Gas += targetContract.Gas

	switch in.terminationType {
	case TerminateFinish:
		return QEICallSuccess
	case TerminateRevert:
		in.StateDB.RevertToSnapshot(snapshot)
		return ErrQEICallRevert
	default:
		in.StateDB.RevertToSnapshot(snapshot)
		contract.UseGas(targetContract.Gas)
		return ErrQEICallFailure
	}
}

func (in *WASMInterpreter) storageStore(ctx context.Context, module api.Module, pathOffset uint32, valueOffset uint32) {
	if in.staticMode == true {
		panic("Static mode violation in storageStore")
	}

	loc := common.BytesToHash(readSize(ctx, module, pathOffset, u256Len))
	val := common.BytesToHash(readSize(ctx, module, valueOffset, u256Len))

	fmt.Println(val, loc)
	nonZeroBytes := 0
	for _, b := range val.Bytes() {
		if b != 0 {
			nonZeroBytes++
		}
	}

	addr, err := in.contract.Address().InternalAddress()
	if err != nil {
		panic(err)
	}

	oldValue := in.StateDB.GetState(addr, loc)
	oldNonZeroBytes := 0
	for _, b := range oldValue.Bytes() {
		if b != 0 {
			oldNonZeroBytes++
		}
	}

	if (nonZeroBytes > 0 && oldNonZeroBytes != nonZeroBytes) || (oldNonZeroBytes != 0 && nonZeroBytes == 0) {
		in.gasAccounting(GasCostSSet)
	} else {
		// Refund for setting one value to 0 or if the "zeroness" remains
		// unchanged.
		in.gasAccounting(GasCostSReset)
	}

	in.StateDB.SetState(addr, loc, val)
}

func (in *WASMInterpreter) storageLoad(ctx context.Context, module api.Module, pathOffset uint32, resultOffset int32) {
	in.gasAccounting(in.gasTable.SLoad)
	loc := common.BytesToHash(readSize(ctx, module, pathOffset, u256Len))
	addr, err := in.contract.Address().InternalAddress()
	if err != nil {
		panic(err)
	}
	valBytes := in.StateDB.GetState(addr, loc).Bytes()
	writeBytes(ctx, module, valBytes, uint32(resultOffset))
}

func (in *WASMInterpreter) getCaller(ctx context.Context, module api.Module, resultOffset int32) {
	callerAddress := in.contract.CallerAddress
	fmt.Println("caller address", callerAddress)
	in.gasAccounting(GasCostBase)
	fmt.Println("caller address", callerAddress.Bytes())
	fmt.Println("caller address", uint32(resultOffset))
	writeBytes(ctx, module, callerAddress.Bytes(), uint32(resultOffset))
}

func (in *WASMInterpreter) getCallValue(ctx context.Context, module api.Module, resultOffset int32) {
	in.gasAccounting(GasCostBase)
	writeBytes(ctx, module, swapEndian(in.contract.Value().Bytes()), uint32(resultOffset))
}

func (in *WASMInterpreter) codeCopy(ctx context.Context, module api.Module, resultOffset int32, codeOffset int32, length int32) {
	in.gasAccounting(GasCostVeryLow + GasCostCopy*(uint64(length+31)>>5))
	code := in.contract.Code
	writeBytes(ctx, module, code[codeOffset:codeOffset+length], uint32(resultOffset))
}

func (in *WASMInterpreter) getCodeSize(ctx context.Context, module api.Module) int32 {
	in.gasAccounting(GasCostBase)
	addr, err := in.contract.CodeAddr.InternalAddress()
	if err != nil {
		panic(err)
	}

	code := in.StateDB.GetCode(addr)
	return int32(len(code))
}

func (in *WASMInterpreter) getBlockCoinbase(ctx context.Context, module api.Module, resultOffset int32) {
	in.gasAccounting(GasCostBase)
	writeBytes(ctx, module, in.evm.Context.Coinbase.Bytes(), uint32(resultOffset))
}

// func (in *WASMInterpreter) sentinel(input []byte) ([]byte, uint64, error) {
// 	savedContract := in.contract
// 	savedVM := in.vm
// 	defer func() {
// 		in.contract = savedContract
// 		in.vm = savedVM
// 	}()
// 	meteringContractAddress := common.HexToAddress(sentinelContractAddress)
// 	addr, err := in.contract.Address().Bytes()
// 	meteringCode := in.StateDB.GetCode(meteringContractAddress)
// 	in.contract = NewContract(in.contract, AccountRef(meteringContractAddress), &big.Int{}, in.contract.Gas)
// 	in.contract.SetCallCode(&meteringContractAddress, crypto.Keccak256Hash(meteringCode), meteringCode)
// 	vm, err := exec.NewVM(in.meteringModule)
// 	vm.RecoverPanic = true
// 	in.vm = vm
// 	if err != nil {
// 		panic(fmt.Sprintf("Error allocating metering VM: %v", err))
// 	}
// 	in.contract.Input = input
// 	meteredCode, err := in.vm.ExecCode(in.meteringStartIndex)
// 	if meteredCode == nil {
// 		meteredCode = in.returnData
// 	}

// 	var asBytes []byte
// 	if err == nil {
// 		asBytes = meteredCode.([]byte)
// 	}

// 	return asBytes, savedContract.Gas - in.contract.Gas, err
// }

func (in *WASMInterpreter) create(ctx context.Context, module api.Module, valueOffset uint32, codeOffset uint32, length uint32, resultOffset uint32) int32 {
	in.gasAccounting(GasCostCreate)
	savedVM := in.vm
	savedContract := in.contract
	defer func() {
		in.vm = savedVM
		in.contract = savedContract
	}()
	in.terminationType = TerminateInvalid

	memorySize := module.Memory().Size()
	if codeOffset+length > memorySize {
		return ErrQEICallFailure
	}
	input := readSize(ctx, module, codeOffset, length)

	if (valueOffset + u128Len) > memorySize {
		return ErrQEICallFailure
	}
	value := swapEndian(readSize(ctx, module, valueOffset, u128Len))

	in.terminationType = TerminateFinish

	// EIP150 says that the calling contract should keep 1/64th of the
	// leftover gas.
	gas := in.contract.Gas - in.contract.Gas/64
	in.gasAccounting(gas)

	/* Meter the contract code if metering is enabled */
	// if in.metering {
	// 	input, _, _ = sentinel(in, input)
	// 	if len(input) < 5 {
	// 		return ErrQEICallFailure
	// 	}
	// }

	_, addr, gasLeft, _ := in.evm.Create(in.contract, input, gas, big.NewInt(0).SetBytes(value))

	switch in.terminationType {
	case TerminateFinish:
		savedContract.Gas += gasLeft
		writeBytes(ctx, module, addr.Bytes(), uint32(resultOffset))
		return QEICallSuccess
	case TerminateRevert:
		savedContract.Gas += gas
		return ErrQEICallRevert
	default:
		savedContract.Gas += gasLeft
		return ErrQEICallFailure
	}
}

func (in *WASMInterpreter) getBlockDifficulty(ctx context.Context, module api.Module, resultOffset int32) {
	in.gasAccounting(GasCostBase)
	writeBytes(ctx, module, swapEndian(in.evm.Context.Difficulty.Bytes()), uint32(resultOffset))
}

func (in *WASMInterpreter) externalCodeCopy(ctx context.Context, module api.Module, addressOffset uint32, resultOffset int32, codeOffset int32, length int32) {
	in.gasAccounting(in.gasTable.ExtcodeCopy + GasCostCopy*(uint64(length+31)>>5))
	addrInterface := common.BytesToAddress(readSize(ctx, module, addressOffset, common.AddressLength))
	addr, err := addrInterface.InternalAddress()
	if err != nil {
		panic(err)
	}
	code := in.StateDB.GetCode(addr)
	writeBytes(ctx, module, code[codeOffset:codeOffset+length], uint32(resultOffset))
}

func (in *WASMInterpreter) getExternalCodeSize(ctx context.Context, module api.Module, addressOffset uint32) int32 {
	in.gasAccounting(in.gasTable.ExtcodeSize)
	addrInterface := common.BytesToAddress(readSize(ctx, module, addressOffset, common.AddressLength))
	addr, err := addrInterface.InternalAddress()
	if err != nil {
		panic(err)
	}
	code := in.StateDB.GetCode(addr)
	return int32(len(code))
}

func (in *WASMInterpreter) getGasLeft(ctx context.Context, module api.Module) int64 {
	in.gasAccounting(GasCostBase)
	return int64(in.contract.Gas)
}

func (in *WASMInterpreter) getBlockGasLimit(ctx context.Context, module api.Module) int64 {
	in.gasAccounting(GasCostBase)
	return int64(in.evm.Context.GasLimit)
}

func (in *WASMInterpreter) getTxGasPrice(ctx context.Context, module api.Module, valueOffset uint32) {
	in.gasAccounting(GasCostBase)
	writeBytes(ctx, module, in.evm.GasPrice.Bytes(), valueOffset)
}

// // It would be nice to be able to use variadic functions to pass the number of topics,
// // however this imposes a change in wagon because the number of arguments is being
// // checked when calling a function.
// func (in *WASMInterpreter) log(ctx context.Context, module api.Module, dataOffset int32, length int32, numberOfTopics int32, topic1 int32, topic2 int32, topic3 int32, topic4 int32) {
// 	in.gasAccounting(GasCostLog + GasCostLogData*uint64(length) + uint64(numberOfTopics)*GasCostLogTopic)

// 	// TODO need to add some info about the memory boundary on wagon
// 	if uint64(len(in.vm.Memory())) <= uint64(length)+uint64(dataOffset) {
// 		panic("out of memory")
// 	}
// 	data := readSize(p, dataOffset, int(uint32(length)))
// 	topics := make([]common.Hash, numberOfTopics)

// 	if numberOfTopics > 4 || numberOfTopics < 0 {
// 		in.terminationType = TerminateInvalid
// 		// p.Terminate()
// 	}

// 	// Variadic functions FTW
// 	if numberOfTopics > 0 {
// 		if uint64(len(in.vm.Memory())) <= uint64(topic1) {
// 			panic("out of memory")
// 		}
// 		topics[0] = common.BigToHash(big.NewInt(0).SetBytes(readSize(p, topic1, u256Len)))
// 	}
// 	if numberOfTopics > 1 {
// 		if uint64(len(in.vm.Memory())) <= uint64(topic2) {
// 			panic("out of memory")
// 		}
// 		topics[1] = common.BigToHash(big.NewInt(0).SetBytes(readSize(p, topic2, u256Len)))
// 	}
// 	if numberOfTopics > 2 {
// 		if uint64(len(in.vm.Memory())) <= uint64(topic3) {
// 			panic("out of memory")
// 		}
// 		topics[2] = common.BigToHash(big.NewInt(0).SetBytes(readSize(p, topic3, u256Len)))
// 	}
// 	if numberOfTopics > 3 {
// 		if uint64(len(in.vm.Memory())) <= uint64(topic3) {
// 			panic("out of memory")
// 		}
// 		topics[3] = common.BigToHash(big.NewInt(0).SetBytes(readSize(p, topic4, u256Len)))
// 	}

// 	in.StateDB.AddLog(&types.Log{
// 		Address:     in.contract.Address(),
// 		Topics:      topics,
// 		Data:        data,
// 		BlockNumber: in.evm.Context.BlockNumber.Uint64(),
// 	})
// }

func (in *WASMInterpreter) getBlockNumber(ctx context.Context, module api.Module) int64 {
	in.gasAccounting(GasCostBase)
	return in.evm.Context.BlockNumber.Int64()
}

func (in *WASMInterpreter) getTxOrigin(ctx context.Context, module api.Module, resultOffset int32) {
	in.gasAccounting(GasCostBase)
	writeBytes(ctx, module, in.evm.TxContext.Origin.Bytes(), uint32(resultOffset))
}

func (in *WASMInterpreter) unWindContract(ctx context.Context, module api.Module, dataOffset int32, length uint32) {
	buf, ok := module.Memory().Read(uint32(dataOffset), length)
	if !ok {
		log.Panicf("🟥 Memory.Read(%d, %d) out of range", dataOffset, length)
	}
	in.returnData = buf
}

func (in *WASMInterpreter) finish(ctx context.Context, module api.Module, dataOffset int32, length uint32) {
	in.unWindContract(ctx, module, dataOffset, length)

	in.terminationType = TerminateFinish
	// p.Terminate()
}

func (in *WASMInterpreter) revert(ctx context.Context, module api.Module, dataOffset int32, length uint32) {
	in.unWindContract(ctx, module, dataOffset, length)

	in.terminationType = TerminateRevert
	ctx.Done()
}

func (in *WASMInterpreter) getReturnDataSize(ctx context.Context, module api.Module) int32 {
	in.gasAccounting(GasCostBase)
	return int32(len(in.returnData))
}

func (in *WASMInterpreter) returnDataCopy(ctx context.Context, module api.Module, resultOffset int32, dataOffset int32, length int32) {
	in.gasAccounting(GasCostVeryLow + GasCostCopy*(uint64(length+31)>>5))
	writeBytes(ctx, module, in.returnData[dataOffset:dataOffset+length], uint32(resultOffset))
}

// func (in *WASMInterpreter) selfDestruct(ctx context.Context, module api.Module, addressOffset int32) {
// 	contract := in.contract
// 	mem := module.Memory().

// 	addrInterface := contract.Address()
// 	caAddr, err := addrInterface.InternalAddress()
// 	if err != nil {
// 		panic(err)
// 	}

// 	balance := in.StateDB.GetBalance(*caAddr)

// 	addr := common.BytesToAddress(mem[addressOffset : addressOffset+common.AddressLength])

// 	totalGas := in.gasTable.Suicide
// 	// If the destination address doesn't exist, add the account creation costs
// 	if in.StateDB.Empty(addr) && balance.Sign() != 0 {
// 		totalGas += in.gasTable.CreateBySuicide
// 	}
// 	in.gasAccounting(totalGas)

// 	in.StateDB.AddBalance(addr, balance)
// 	in.StateDB.Suicide(*caAddr)

// 	// Same as for `revert` and `return`, I need to forcefully terminate
// 	// the execution of the contract.
// 	in.terminationType = TerminateSuicide
// 	p.Terminate()
// }

func (in *WASMInterpreter) getBlockTimestamp(ctx context.Context, module api.Module) int64 {
	in.gasAccounting(GasCostBase)
	return in.evm.Context.Time.Int64()
}
