package vm

import (
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
)


func TestHelloContract(t *testing.T) {
	var (
		env            = NewEVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		wasmInterpreter = NewWASMInterpreter(env, env.Config)
	)

	wasmBytes, err := ioutil.ReadFile("wasm_contracts/hello/hello.wasm")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading wasm file:", err)
		os.Exit(1)
	}
	
	// Track time taken and memory usage
	// defer trackTime(time.Now(), "wasm")
	

	// Initialise a new contract and set the code that is to be used by the EVM.
	// The contract is a scoped environment for this execution context only.
	contract := NewContract(&dummyContractRef{}, &dummyContractRef{}, new(big.Int), 2000)
	contract.SetCodeOptionalHash(nil, &codeAndHash{
		code: wasmBytes,
	})

	_, err2 := wasmInterpreter.Run(contract, nil, false)
	if err2 != nil {
		t.Errorf("error: %v", err)
	}
}

func TestUseGasContract(t *testing.T) {
	var (
		env            = NewEVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		wasmInterpreter = NewWASMInterpreter(env, env.Config)
	)

	wasmBytes, err := ioutil.ReadFile("wasm_contracts/use_gas/use_gas.wasm")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading wasm file:", err)
		os.Exit(1)
	}
	
	// Track time taken and memory usage
	// defer trackTime(time.Now(), "wasm")
	

	// Initialise a new contract and set the code that is to be used by the EVM.
	// The contract is a scoped environment for this execution context only.
	contract := NewContract(&dummyContractRef{}, &dummyContractRef{}, new(big.Int), 2000)
	contract.SetCodeOptionalHash(nil, &codeAndHash{
		code: wasmBytes,
	})

	_, err2 := wasmInterpreter.Run(contract, nil, false)
	if err2 != nil {
		t.Errorf("error: %v", err)
	}
}

func TestGetAddressContract(t *testing.T) {
	var (
		env            = NewEVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		wasmInterpreter = NewWASMInterpreter(env, env.Config)
	)

	wasmBytes, err := ioutil.ReadFile("wasm_contracts/get_address/get_address.wasm")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading wasm file:", err)
		os.Exit(1)
	}
	
	// Track time taken and memory usage
	// defer trackTime(time.Now(), "wasm")
	

	// Initialise a new contract and set the code that is to be used by the EVM.
	// The contract is a scoped environment for this execution context only.
	contract := NewContract(&dummyContractRef{}, &dummyContractRef{}, new(big.Int), 2000)
	contract.SetCodeOptionalHash(&common.ZeroAddr, &codeAndHash{
		code: wasmBytes,
		hash: crypto.Keccak256Hash(wasmBytes),
	})

	_, err2 := wasmInterpreter.Run(contract, nil, false)
	if err2 != nil {
		t.Errorf("error: %v", err)
	}
}

func TestGetBlockNumber(t *testing.T) {
	context := BlockContext{
		BlockNumber: big.NewInt(100),
	}

	var (
		env            = NewEVM(context, TxContext{}, nil, params.TestChainConfig, Config{})
		wasmInterpreter = NewWASMInterpreter(env, env.Config)
	)

	wasmBytes, err := ioutil.ReadFile("wasm_contracts/get_block_number/get_block_number.wasm")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading wasm file:", err)
		os.Exit(1)
	}
	
	// Track time taken and memory usage
	// defer trackTime(time.Now(), "wasm")
	

	// Initialise a new contract and set the code that is to be used by the EVM.
	// The contract is a scoped environment for this execution context only.
	contract := NewContract(&dummyContractRef{}, &dummyContractRef{}, new(big.Int), 2000)
	contract.SetCodeOptionalHash(&common.ZeroAddr, &codeAndHash{
		code: wasmBytes,
		hash: crypto.Keccak256Hash(wasmBytes),
	})

	_, err2 := wasmInterpreter.Run(contract, nil, false)
	if err2 != nil {
		t.Errorf("error: %v", err)
	}
}

func TestGetCallerContract(t *testing.T) {
	var (
		env            = NewEVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		wasmInterpreter = NewWASMInterpreter(env, env.Config)
	)

	wasmBytes, err := ioutil.ReadFile("wasm_contracts/get_caller/get_caller.wasm")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading wasm file:", err)
		os.Exit(1)
	}
	
	// Track time taken and memory usage
	// defer trackTime(time.Now(), "wasm")
	

	// Initialise a new contract and set the code that is to be used by the EVM.
	// The contract is a scoped environment for this execution context only.
	contract := NewContract(&dummyContractRef{}, &dummyContractRef{}, new(big.Int), 2000)
	contract.SetCodeOptionalHash(&common.ZeroAddr, &codeAndHash{
		code: wasmBytes,
		hash: crypto.Keccak256Hash(wasmBytes),
	})

	hexString := "0x1234567898765432123412345678987654321234"
	contract.CallerAddress = common.HexToAddress(hexString)

	_, err2 := wasmInterpreter.Run(contract, nil, false)
	if err2 != nil {
		t.Errorf("error: %v", err)
	}
}
