package vm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"math/big"

	"github.com/bytecodealliance/wasmtime-go"
	"github.com/dominant-strategies/go-quai/common"
)

var qeiFunctionList = []string{
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

type WasmVM struct {
	evm *EVM

	engine   *wasmtime.Engine
	instance *wasmtime.Instance
	linker   *wasmtime.Linker
	memory   *wasmtime.Memory
	module   *wasmtime.Module
	store    *wasmtime.Store

	Contract *Contract

	cachedResult   []byte
	panicErr       error
	timeoutStarted bool
}

func InstantiateWASMVM(in *WASMInterpreter) *WasmVM {
	config := wasmtime.NewConfig()
	// no need to be interruptable by WasmVMBase
	// config.SetInterruptable(true)
	config.SetConsumeFuel(true)

	vm := &WasmVM{engine: wasmtime.NewEngineWithConfig(config)}
	// prevent WasmVMBase from starting timeout interrupting,
	// instead we simply let WasmTime run out of fuel
	vm.timeoutStarted = true // DisableWasmTimeout

	vm.LinkHost(in)

	return vm
}

func (vm *WasmVM) LinkHost(in *WASMInterpreter) (err error) {
	vm.store = wasmtime.NewStore(vm.engine)
	vm.store.AddFuel(in.Contract.Gas)
	vm.linker = wasmtime.NewLinker(vm.engine)

	// Create a new memory instance.
	memoryType := wasmtime.NewMemoryType(1, true, 300)
	vm.memory, err = wasmtime.NewMemory(vm.store, memoryType)
	if err != nil {
		return err
	}

	err = vm.linker.DefineFunc(vm.store, "", "logHelloWorld", logHelloWorld)
	if err != nil {
		return err
	}

	err = vm.linker.DefineFunc(vm.store, "", "useGas", in.useGas)
	if err != nil {
		return err
	}

	err = vm.linker.DefineFunc(vm.store, "", "getAddress", in.getAddress)
	if err != nil {
		return err
	}

	err = vm.linker.DefineFunc(vm.store, "", "getExternalBalance", in.getExternalBalance)
	if err != nil {
		return err
	}

	err = vm.linker.DefineFunc(vm.store, "", "getBlockNumber", in.getBlockNumber)
	if err != nil {
		return err
	}

	err = vm.linker.DefineFunc(vm.store, "", "getBlockHash", in.getBlockHash)
	if err != nil {
		return err
	}

	return nil
}

func (vm *WasmVM) LoadWasm(wasm []byte) (err error) {
	module, err := wasmtime.NewModule(vm.engine, wasm)
	if err != nil {
		return err
	}
	bytes, err := module.Serialize()
	if err != nil {
		return err
	}

	// Deserialize the compiled module.
	module, err = wasmtime.NewModuleDeserialize(vm.store.Engine, bytes)
	if err != nil {
		return err
	}

	vm.instance, err = vm.linker.Instantiate(vm.store, module)
	if err != nil {
		return err
	}

	// After we've instantiated we can lookup our `run` function and call
	// it.
	run := vm.instance.GetFunc(vm.store, "run")
	if run == nil {
		panic("not a function")
	}

	_, err = run.Call(vm.store)
	if err != nil {
		return err
	}

	return nil
}

func (vm *WasmVM) UnsafeMemory() []byte {
	return vm.memory.UnsafeData(vm.store)
}

func logHelloWorld() {
	fmt.Println("🤖: Hello World")
}

func (in *WASMInterpreter) gasAccounting(cost uint64) {
	if in.Contract == nil {
		panic("nil contract")
	}
	if cost > in.Contract.Gas {
		panic(fmt.Sprintf("out of gas %d > %d", cost, in.Contract.Gas))
	}
	in.Contract.Gas -= cost
}

func (in *WASMInterpreter) useGas(amount int64) {
	in.gasAccounting(uint64(amount))
}

func (in *WASMInterpreter) getAddress(resultOffset int32) (error, error) {
	_, err := in.vm.store.ConsumeFuel(100)
	if err != nil {
		in.vm.panicErr = err
		return wasmtime.NewTrap("out of gas"), err
	}

	addr := []byte(in.Contract.CodeAddr.String())

	// Assume vm is a field in your WASMInterpreter struct referring to your WasmVM instance
	memoryData := in.vm.memory.UnsafeData(in.vm.store)
	copy(memoryData[resultOffset:], addr)

	return nil, nil
}

func swapEndian(src []byte) []byte {
	ret := make([]byte, len(src))
	for i, v := range src {
		ret[len(src)-i-1] = v
	}
	return ret
}

func (in *WASMInterpreter) getExternalBalance(addressOffset uint32, resultOffset int32) (error, error) {
	_, err := in.vm.store.ConsumeFuel(100)
	if err != nil {
		in.vm.panicErr = err
		return wasmtime.NewTrap("out of gas"), err
	}
	memoryData := in.vm.memory.UnsafeData(in.vm.store)
	addr := common.BytesToAddress(memoryData[addressOffset : addressOffset+common.AddressLength])
	internal, err := addr.InternalAddress()
	if err != nil {
		log.Panicf("🟥 Memory.Write(%d, %d) out of range", resultOffset, len(internal))
	}
	balance := swapEndian(in.StateDB.GetBalance(internal).Bytes())
	copy(memoryData[resultOffset:], balance)
	return nil, nil

}

func (in *WASMInterpreter) getBlockNumber(resultOffset int32) (error, error) {
	_, err := in.vm.store.ConsumeFuel(100)
	if err != nil {
		in.vm.panicErr = err
		return wasmtime.NewTrap("out of gas"), err
	}
	blockNumber := in.evm.Context.BlockNumber.Int64()
	memoryData := in.vm.memory.UnsafeData(in.vm.store)
	copy(memoryData[resultOffset:], int64ToBytes(blockNumber))
	return nil, nil
}

// Helper function to convert int64 to []byte
func int64ToBytes(i int64) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, i)
	return buf.Bytes()
}

func (in *WASMInterpreter) getBlockHash(number int64, resultOffset int32) (error, error) {
	_, err := in.vm.store.ConsumeFuel(100)
	if err != nil {
		in.vm.panicErr = err
		return wasmtime.NewTrap("out of gas"), err
	}
	n := big.NewInt(number)
	fmt.Println(n)
	n.Sub(in.evm.Context.BlockNumber, n)
	fmt.Println(n, n.Cmp(big.NewInt(256)), n.Cmp(big.NewInt(0)))
	// TODO Fix this error return
	if n.Cmp(big.NewInt(256)) > 0 || n.Cmp(big.NewInt(0)) <= 0 {
		return nil, nil
	}
	h := in.evm.Context.GetHash(uint64(number))
	memoryData := in.vm.memory.UnsafeData(in.vm.store)
	copy(memoryData[resultOffset:], h.Bytes())
	return nil, nil
}
