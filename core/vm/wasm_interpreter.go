package vm

import (
	"context"
	"fmt"
	"log"

	"github.com/tetratelabs/wazero"
)

type terminationType int

// List of termination reasons
const (
	TerminateFinish = iota
	TerminateRevert
	TerminateSuicide
	TerminateInvalid
)

type WASMInterpreter struct {
	evm *EVM
	cfg Config

	vm wazero.Runtime
	contract *Contract

	returnData []byte // Last CALL's return data for subsequent reuse
	
	TxContext
	// StateDB gives access to the underlying state
	StateDB StateDB
	// Depth is the current call stack
	depth int

	Config Config

	gasTable GasTable

	terminationType terminationType
	staticMode bool

	metering bool
}

// GasTable organizes gas prices for different ethereum phases.
type GasTable struct {
	ExtcodeSize uint64
	ExtcodeCopy uint64
	ExtcodeHash uint64
	Balance     uint64
	SLoad       uint64
	Calls       uint64
	Suicide     uint64

	ExpByte uint64

	// CreateBySuicide occurs when the
	// refunded account is one that does
	// not exist. This logic is similar
	// to call. May be left nil. Nil means
	// not charged.
	CreateBySuicide uint64
}



func NewWASMInterpreter(evm *EVM, cfg Config) *WASMInterpreter {

	gasTable := GasTable{
		ExtcodeSize: 700,
		ExtcodeCopy: 700,
		ExtcodeHash: 400,
		Balance:     400,
		SLoad:       200,
		Calls:       700,
		Suicide:     5000,
		ExpByte:     50,

		CreateBySuicide: 25000,
	}

	inter := WASMInterpreter{
		StateDB:  evm.StateDB,
		evm: 	evm,
		gasTable: gasTable,
	}
	

	return &inter
}



func (in *WASMInterpreter) Run(contract *Contract, input []byte, readOnly bool) (ret []byte, err error) {
	// Increment the call depth which is restricted to 1024
	in.evm.depth++
	defer func() { in.evm.depth-- }()

	in.contract = contract
	in.contract.Input = input

	// Choose the context to use for function calls.
	ctx := context.Background()
	runtime := InstantiateWASMRuntime(ctx, in)
	defer runtime.Close(ctx)

	in.vm = runtime


	// config := wazero.NewModuleConfig().WithStartFunctions("run")


	// compiledModule, err := runtime.CompileModule(ctx, contract.Code)
	// if err != nil {
	// 	fmt.Println("failed to compile module", err)
	// 	return nil, fmt.Errorf("failed to compile module: %w", err)
	// }

	// Compile the WebAssembly module.
	// module, err := runtime.InstantiateModule(ctx, compiledModule, config)
	// if err != nil {
	// 	fmt.Println("failed to instantiate module", err)
	// 	return nil, fmt.Errorf("failed to instantiate module: %w", err)
	// }

	
	module, err := runtime.Instantiate(ctx, contract.Code)
	if err != nil {
		fmt.Println("failed to instantiate module", err)
		return nil, fmt.Errorf("failed to instantiate module: %w", err)
	}

	// Get references to WebAssembly function: "run"
	runModuleFunction := module.ExportedFunction("run")

	// Now, we can call "add", which reads the string we wrote to memory!
	// result []uint64
	result, errCallFunction := runModuleFunction.Call(ctx)
	if errCallFunction != nil {
		log.Panicln("🔴 Error while calling the function ", errCallFunction)
	}

	fmt.Println("result:", result)
	// exports := module.ExportedFunctionDefinitions()
	
	// if _, ok := exports["main"]; !ok {
	// 	return nil, fmt.Errorf("no main function exported")
	// }
	
	// if _, ok := exports["memory"]; !ok {
	// 	return nil, fmt.Errorf("no memory exported")
	// }

	// if len(input) > 2 {
	// 	return nil, fmt.Errorf("input too long")
	// }

	// TODO validate input from spec

	// main := module.ExportedFunction("main")

	// if _, ok := main.Call(ctx); ok != nil {
	// 	return nil, fmt.Errorf("failed to call main: %w", err)
	// }

	return in.returnData, nil
}

// // WriteWASMInterpreter writes the WASMInterpreter to the WASM module's memory.
// func WriteWASMInterpreter(module api.Module, in *WASMInterpreter) {
// 	interpreterBytes, err := json.Marshal(in)
// 	if err != nil {
// 		log.Panicln("🔴 Error while marshalling interpreter: ", err)
// 	}
	
// 	// The pointer is a linear memory offset, which is where we write the name.
// 	fmt.Println("interpreterBytes:", len(interpreterBytes))
	

// 	var lenBytes [4]byte
// 	binary.LittleEndian.PutUint32(lenBytes[:], uint32(len(interpreterBytes)))

// 	fmt.Println("lenBytes:", lenBytes)
// 	if !module.Memory().Write(0, lenBytes[:]) {
// 		log.Panicf("🟥 Memory.Write(%d, %d) out of range of memory size %d",
// 			0, len(interpreterBytes), module.Memory().Size())
// 	}

// 	if !module.Memory().Write(4, interpreterBytes) {
// 		log.Panicf("🟥 Memory.Write(%d, %d) out of range of memory size %d",
// 			0, interpreterBytes, module.Memory().Size())
// 	}
// }