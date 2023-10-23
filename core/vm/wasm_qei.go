package vm

import (
	"fmt"
	"log"

	"github.com/bytecodealliance/wasmtime-go"
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
	// config.SetConsumeFuel(true)

	vm := &WasmVM{engine: wasmtime.NewEngineWithConfig(config)}
	// prevent WasmVMBase from starting timeout interrupting,
	// instead we simply let WasmTime run out of fuel
	vm.timeoutStarted = true // DisableWasmTimeout

	vm.LinkHost(in)

	return vm
}

func (vm *WasmVM) LinkHost(in *WASMInterpreter) (err error) {
	vm.store = wasmtime.NewStore(vm.engine)
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
		log.Fatal(err)
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

func (in *WASMInterpreter) getAddress(resultOffset int32) {
	//in.gasAccounting(10)
	fmt.Println("🤖: getAddress, addr", in.Contract.CodeAddr)
	fmt.Println("🤖: getAddress, offset", resultOffset)
	addr := []byte(in.Contract.CodeAddr.String())

	// Assume vm is a field in your WASMInterpreter struct referring to your WasmVM instance
	memoryData := in.vm.memory.UnsafeData(in.vm.store)
	copy(memoryData[resultOffset:], addr)
}
