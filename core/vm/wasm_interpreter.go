package vm

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

	vm       *WasmVM
	Contract *Contract

	returnData []byte // Last CALL's return data for subsequent reuse

	TxContext
	// StateDB gives access to the underlying state
	StateDB StateDB
	// Depth is the current call stack
	depth int

	Config Config
}

func NewWASMInterpreter(evm *EVM, cfg Config) *WASMInterpreter {

	inter := WASMInterpreter{
		StateDB: evm.StateDB,
		evm:     evm,
	}

	return &inter
}

func (in *WASMInterpreter) Run(contract *Contract, input []byte, readOnly bool) (ret []byte, err error) {
	// Increment the call depth which is restricted to 1024
	in.evm.depth++
	defer func() { in.evm.depth-- }()

	in.Contract = contract
	in.Contract.Input = input

	// Create VM with the configure.
	vm := InstantiateWASMVM(in)

	in.vm = vm

	err = vm.LoadWasm(contract.Code)
	if err != nil {
		return nil, err
	}

	return in.returnData, nil
}
