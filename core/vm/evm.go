// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package vm

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/ethdb/memorydb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/holiman/uint256"
)

// emptyCodeHash is used by create to ensure deployment is disallowed to already
// deployed contract addresses (relevant after the account abstraction).
var emptyCodeHash = crypto.Keccak256Hash(nil)

type (
	// CanTransferFunc is the signature of a transfer guard function
	CanTransferFunc func(StateDB, common.Address, *big.Int) bool
	// TransferFunc is the signature of a transfer function
	TransferFunc func(StateDB, common.Address, common.Address, *big.Int) error
	// GetHashFunc returns the n'th block hash in the blockchain
	// and is used by the BLOCKHASH EVM op code.
	GetHashFunc func(uint64) common.Hash
	// CheckIfEtXEligibleFunc checks if the given slice is eligible to accept
	// the etx given the destination location and etx eligible slices hash
	CheckIfEtxEligibleFunc func(common.Hash, common.Location) bool
)

func (evm *EVM) precompile(addr common.Address) (PrecompiledContract, bool, common.Address) {
	p, ok := PrecompiledContracts[addr.Bytes20()]
	if !ok {
		// to translate the address, we set the location-specific byte prefix to the first byte and check if it's a precompile
		translatedAddress := addr.Bytes20()
		translatedAddress[0] = evm.chainConfig.Location.BytePrefix()
		p, ok = PrecompiledContracts[translatedAddress]
		if ok {
			// if it's a precompile, set the address to the precompile address
			addr = common.Bytes20ToAddress(translatedAddress, evm.chainConfig.Location)
		}
	}
	return p, ok, addr
}

// BlockContext provides the EVM with auxiliary information. Once provided
// it shouldn't be modified.
type BlockContext struct {
	// CanTransfer returns whether the account contains
	// sufficient ether to transfer the value
	CanTransfer CanTransferFunc
	// Transfer transfers ether from one account to the other
	Transfer TransferFunc
	// GetHash returns the hash corresponding to n
	GetHash GetHashFunc
	// CheckIfEtxEligible returns true if etx is eligible for the input location
	CheckIfEtxEligible CheckIfEtxEligibleFunc

	// Block information
	PrimaryCoinbase common.Address // Provides information for COINBASE
	GasLimit        uint64         // Provides information for GASLIMIT
	BlockNumber     *big.Int       // Provides information for NUMBER
	Time            *big.Int       // Provides information for TIME
	Difficulty      *big.Int       // Provides information for DIFFICULTY
	BaseFee         *big.Int       // Provides information for BASEFEE
	QuaiStateSize   *big.Int       // Provides information for QUAISTATESIZE

	// Prime Terminus information for the given block
	EtxEligibleSlices   common.Hash
	PrimeTerminusNumber uint64
}

// TxContext provides the EVM with information about a transaction.
// All fields can change between transactions.
type TxContext struct {
	// Message information
	Origin     common.Address // Provides information for ORIGIN
	GasPrice   *big.Int       // Provides information for GASPRICE
	TxType     byte
	Hash       common.Hash
	AccessList types.AccessList
	ETXSender  common.Address // Original sender of the ETX
}

// EVM is the Quai Virtual Machine base object and provides
// the necessary tools to run a contract on the given state with
// the provided context. It should be noted that any error
// generated through any of the calls should be considered a
// revert-state-and-consume-all-gas operation, no checks on
// specific errors should ever be performed. The interpreter makes
// sure that any errors generated are to be considered faulty code.
//
// The EVM should never be reused and is not thread safe.
type EVM struct {
	// Context provides auxiliary blockchain related information
	Context BlockContext
	TxContext
	// StateDB gives access to the underlying state
	StateDB StateDB
	// Depth is the current call stack
	depth int

	// chainConfig contains information about the current chain
	chainConfig *params.ChainConfig
	// chain rules contains the chain rules for the current epoch
	chainRules params.Rules
	// virtual machine configuration options used to initialise the
	// evm.
	Config Config
	// global (to this context) quai virtual machine
	// used throughout the execution of the tx.
	interpreter *EVMInterpreter
	// abort is used to abort the EVM calling operations
	// NOTE: must be set atomically
	abort int32
	// callGasTemp holds the gas available for the current call. This is needed because the
	// available gas is calculated in gasCall* according to the 63/64 rule and later
	// applied in opCall*.
	callGasTemp uint64

	ETXCache              []*types.Transaction
	CoinbaseDeletedHashes []*common.Hash
	CoinbasesDeleted      map[[47]byte][]byte
	ETXCacheLock          sync.RWMutex
	Batch                 ethdb.Batch
}

// NewEVM returns a new EVM. The returned EVM is not thread safe and should
// only ever be used *once*.
func NewEVM(blockCtx BlockContext, txCtx TxContext, statedb StateDB, chainConfig *params.ChainConfig, config Config, batch ethdb.Batch) *EVM {
	evm := &EVM{
		Context:               blockCtx,
		TxContext:             txCtx,
		StateDB:               statedb,
		Config:                config,
		chainConfig:           chainConfig,
		chainRules:            chainConfig.Rules(blockCtx.BlockNumber),
		ETXCache:              make([]*types.Transaction, 0),
		CoinbaseDeletedHashes: make([]*common.Hash, 0),
		CoinbasesDeleted:      make(map[[47]byte][]byte),
	}
	if batch != nil {
		evm.Batch = batch
	} else {
		evm.Batch = memorydb.New(log.Global).NewBatch() // Just used as a cache for simulating calls
	}
	evm.interpreter = NewEVMInterpreter(evm, config)
	return evm
}

// Reset resets the EVM with a new transaction context.Reset
// This is not threadsafe and should only be done very cautiously.
func (evm *EVM) Reset(txCtx TxContext, statedb StateDB) {
	evm.TxContext = txCtx
	evm.StateDB = statedb
	evm.ResetCoinbasesDeleted()
}

// Cancel cancels any running EVM operation. This may be called concurrently and
// it's safe to be called multiple times.
func (evm *EVM) Cancel() {
	atomic.StoreInt32(&evm.abort, 1)
}

// Cancelled returns true if Cancel has been called
func (evm *EVM) Cancelled() bool {
	return atomic.LoadInt32(&evm.abort) == 1
}

// Interpreter returns the current interpreter
func (evm *EVM) Interpreter() *EVMInterpreter {
	return evm.interpreter
}

// Call executes the contract associated with the addr with the given input as
// parameters. It also handles any necessary value transfer required and takes
// the necessary steps to create accounts and reverses the state in case of an
// execution error or failed value transfer.
func (evm *EVM) Call(caller ContractRef, addr common.Address, input []byte, gas uint64, value *big.Int) (ret []byte, leftOverGas uint64, stateGas uint64, err error) {
	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, gas, 0, nil
	}
	// Fail if we're trying to execute above the call depth limit
	if evm.depth > int(params.CallCreateDepth) {
		return nil, gas, 0, ErrDepth
	}
	// Fail if we're trying to transfer more than the available balance
	if value.Sign() != 0 && !evm.Context.CanTransfer(evm.StateDB, caller.Address(), value) {
		return nil, gas, 0, ErrInsufficientBalance
	}
	if addr.Equal(LockupContractAddresses[[2]byte(evm.chainConfig.Location)]) {
		if evm.Context.BlockNumber.Uint64() < params.OrchardEvmReturnFixBlock {
			ret, err := RunLockupContract(evm, caller.Address(), &gas, input)
			return ret, gas, 0, err
		} else {
			ret, err = RunLockupContract(evm, caller.Address(), &gas, input)
			return ret, gas, 0, err
		}
	}
	snapshot := evm.StateDB.Snapshot()
	p, isPrecompile, addr := evm.precompile(addr)
	var internalAddr common.InternalAddress
	internalAddr, err = addr.InternalAndQuaiAddress()
	if err != nil {
		if evm.Context.BlockNumber.Uint64() < params.OrchardEvmReturnFixBlock {
			return evm.CreateETX(addr, caller.Address(), gas, value, input)
		} else {
			ret, leftOverGas, stateGas, err = evm.CreateETX(addr, caller.Address(), gas, value, input)
			if err != nil {
				evm.StateDB.RevertToSnapshot(snapshot)
			}
			return ret, leftOverGas, stateGas, err
		}
	}
	if !evm.StateDB.Exist(internalAddr) {
		if !isPrecompile && value.Sign() == 0 {
			// Calling a non existing account, don't do anything, but ping the tracer
			if evm.Config.Debug && evm.depth == 0 {
				evm.Config.Tracer.CaptureStart(evm, caller.Address(), addr, false, input, gas, value)
				evm.Config.Tracer.CaptureEnd(ret, 0, 0, nil)
			}
			return nil, gas, stateGas, nil
		}
		newAccountCreationGas := params.CallNewAccountGas(evm.Context.QuaiStateSize)
		if gas > newAccountCreationGas {
			gas = gas - newAccountCreationGas
		} else {
			return nil, gas, stateGas, ErrInsufficientBalance
		}
		stateGas += params.CallNewAccountGas(evm.Context.QuaiStateSize)
		evm.StateDB.CreateAccount(internalAddr)
	}
	if err := evm.Context.Transfer(evm.StateDB, caller.Address(), addr, value); err != nil {
		return nil, gas, stateGas, err
	}

	// Capture the tracer start/end events in debug mode
	if evm.Config.Debug && evm.depth == 0 {
		evm.Config.Tracer.CaptureStart(evm, caller.Address(), addr, false, input, gas, value)
		defer func(startGas uint64, startTime time.Time) { // Lazy evaluation of the parameters
			evm.Config.Tracer.CaptureEnd(ret, startGas-gas, time.Since(startTime), err)
		}(gas, time.Now())
	}

	if isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		// Initialise a new contract and set the code that is to be used by the EVM.
		// The contract is a scoped environment for this execution context only.
		code := evm.StateDB.GetCode(internalAddr)
		if len(code) == 0 {
			ret, err = nil, nil // gas is unchanged
		} else {
			addrCopy := addr
			// If the account has no code, we can abort here
			// The depth-check is already done, and precompiles handled above
			contract := NewContract(caller, AccountRef(addrCopy), value, gas)
			contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), code)
			ret, err = evm.interpreter.Run(contract, input, false)
			gas = contract.Gas
			stateGas += contract.StateGas
		}
	}
	// When an error was returned by the EVM or when setting the creation code
	// above we revert to the snapshot and consume any gas remaining. Additionally
	// when we're in this also counts for code storage gas errors.
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			gas = 0
		}
		// TODO: consider clearing up unused snapshots:
		//} else {
		//	evm.StateDB.DiscardSnapshot(snapshot)
	}
	return ret, gas, stateGas, err
}

// CallCode executes the contract associated with the addr with the given input
// as parameters. It also handles any necessary value transfer required and takes
// the necessary steps to create accounts and reverses the state in case of an
// execution error or failed value transfer.
//
// CallCode differs from Call in the sense that it executes the given address'
// code with the caller as context.
func (evm *EVM) CallCode(caller ContractRef, addr common.Address, input []byte, gas uint64, value *big.Int) (ret []byte, leftOverGas uint64, err error) {
	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, gas, nil
	}
	// Fail if we're trying to execute above the call depth limit
	if evm.depth > int(params.CallCreateDepth) {
		return nil, gas, ErrDepth
	}
	// Fail if we're trying to transfer more than the available balance
	// Note although it's noop to transfer X ether to caller itself. But
	// if caller doesn't have enough balance, it would be an error to allow
	// over-charging itself. So the check here is necessary.
	if !evm.Context.CanTransfer(evm.StateDB, caller.Address(), value) {
		return nil, gas, ErrInsufficientBalance
	}
	var snapshot = evm.StateDB.Snapshot()

	// It is allowed to call precompiles, even via delegatecall
	if p, isPrecompile, addr := evm.precompile(addr); isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		addrCopy := addr
		if evm.Context.BlockNumber.Uint64() < params.OrchardEvmReturnFixBlock {
			internalAddr, err := addrCopy.InternalAndQuaiAddress()
			if err != nil {
				return nil, gas, err
			}
			// Initialise a new contract and set the code that is to be used by the EVM.
			// The contract is a scoped environment for this execution context only.
			contract := NewContract(caller, AccountRef(caller.Address()), value, gas)
			contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
			ret, err = evm.interpreter.Run(contract, input, false)
			gas = contract.Gas
		} else {
			var internalAddr common.InternalAddress
			internalAddr, err = addrCopy.InternalAndQuaiAddress()
			if err != nil {
				gas = 0
				return nil, gas, err
			}
			// Initialise a new contract and set the code that is to be used by the EVM.
			// The contract is a scoped environment for this execution context only.
			contract := NewContract(caller, AccountRef(caller.Address()), value, gas)
			contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
			ret, err = evm.interpreter.Run(contract, input, false)
			gas = contract.Gas
		}
	}
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

// DelegateCall executes the contract associated with the addr with the given input
// as parameters. It reverses the state in case of an execution error.
//
// DelegateCall differs from CallCode in the sense that it executes the given address'
// code with the caller as context and the caller is set to the caller of the caller.
func (evm *EVM) DelegateCall(caller ContractRef, addr common.Address, input []byte, gas uint64) (ret []byte, leftOverGas uint64, err error) {
	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, gas, nil
	}
	// Fail if we're trying to execute above the call depth limit
	if evm.depth > int(params.CallCreateDepth) {
		return nil, gas, ErrDepth
	}
	var snapshot = evm.StateDB.Snapshot()

	// It is allowed to call precompiles, even via delegatecall
	if p, isPrecompile, addr := evm.precompile(addr); isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		addrCopy := addr
		if evm.Context.BlockNumber.Uint64() < params.OrchardEvmReturnFixBlock {
			internalAddr, err := addrCopy.InternalAndQuaiAddress()
			if err != nil {
				return nil, gas, err
			}
			// Initialise a new contract and make initialise the delegate values
			contract := NewContract(caller, AccountRef(caller.Address()), nil, gas).AsDelegate()
			contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
			ret, err = evm.interpreter.Run(contract, input, false)
			gas = contract.Gas
		} else {
			var internalAddr common.InternalAddress
			internalAddr, err = addrCopy.InternalAndQuaiAddress()
			if err != nil {
				gas = 0
				return nil, gas, err
			}
			// Initialise a new contract and make initialise the delegate values
			contract := NewContract(caller, AccountRef(caller.Address()), nil, gas).AsDelegate()
			contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
			ret, err = evm.interpreter.Run(contract, input, false)
			gas = contract.Gas
		}
	}
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

// StaticCall executes the contract associated with the addr with the given input
// as parameters while disallowing any modifications to the state during the call.
// Opcodes that attempt to perform such modifications will result in exceptions
// instead of performing the modifications.
func (evm *EVM) StaticCall(caller ContractRef, addr common.Address, input []byte, gas uint64) (ret []byte, leftOverGas uint64, err error) {
	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, gas, nil
	}
	// Fail if we're trying to execute above the call depth limit
	if evm.depth > int(params.CallCreateDepth) {
		return nil, gas, ErrDepth
	}

	// We take a snapshot here. This is a bit counter-intuitive, and could probably be skipped.
	// However, even a staticcall is considered a 'touch'. On mainnet, static calls were introduced
	// after all empty accounts were deleted, so this is not required. However, if we omit this,
	// then certain tests start failing; stRevertTest/RevertPrecompiledTouchExactOOG.json.
	// We could change this, but for now it's left for legacy reasons
	var snapshot = evm.StateDB.Snapshot()

	if p, isPrecompile, addr := evm.precompile(addr); isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		if evm.Context.BlockNumber.Uint64() < params.OrchardEvmReturnFixBlock {
			internalAddr, err := addr.InternalAndQuaiAddress()
			if err != nil {
				return nil, gas, err
			}
			// At this point, we use a copy of address. If we don't, the go compiler will
			// leak the 'contract' to the outer scope, and make allocation for 'contract'
			// even if the actual execution ends on RunPrecompiled above.
			addrCopy := addr
			// Initialise a new contract and set the code that is to be used by the EVM.
			// The contract is a scoped environment for this execution context only.
			contract := NewContract(caller, AccountRef(addrCopy), new(big.Int), gas)
			contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
			// When an error was returned by the EVM or when setting the creation code
			// above we revert to the snapshot and consume any gas remaining. Additionally
			// when we're in this also counts for code storage gas errors.
			ret, err = evm.interpreter.Run(contract, input, true)
			gas = contract.Gas
		} else {
			var internalAddr common.InternalAddress
			internalAddr, err = addr.InternalAndQuaiAddress()
			if err != nil {
				gas = 0
				return nil, gas, err
			}
			// At this point, we use a copy of address. If we don't, the go compiler will
			// leak the 'contract' to the outer scope, and make allocation for 'contract'
			// even if the actual execution ends on RunPrecompiled above.
			addrCopy := addr
			// Initialise a new contract and set the code that is to be used by the EVM.
			// The contract is a scoped environment for this execution context only.
			contract := NewContract(caller, AccountRef(addrCopy), new(big.Int), gas)
			contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
			// When an error was returned by the EVM or when setting the creation code
			// above we revert to the snapshot and consume any gas remaining. Additionally
			// when we're in this also counts for code storage gas errors.
			ret, err = evm.interpreter.Run(contract, input, true)
			gas = contract.Gas
		}
	}
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

type codeAndHash struct {
	code []byte
	hash common.Hash
}

func (c *codeAndHash) Hash() common.Hash {
	if c.hash == (common.Hash{}) {
		c.hash = crypto.Keccak256Hash(c.code)
	}
	return c.hash
}

// create creates a new contract using code as deployment code.
func (evm *EVM) create(caller ContractRef, codeAndHash *codeAndHash, gas uint64, value *big.Int, address common.Address) ([]byte, common.Address, uint64, uint64, error) {
	internalCallerAddr, err := caller.Address().InternalAndQuaiAddress()
	if err != nil {
		return nil, common.Zero, 0, 0, err
	}
	nonce := evm.StateDB.GetNonce(internalCallerAddr)
	evm.StateDB.SetNonce(internalCallerAddr, nonce+1)

	// Depth check execution. Fail if we're trying to execute above the
	// limit.
	if evm.depth > int(params.CallCreateDepth) {
		return nil, common.Zero, gas, 0, ErrDepth
	}
	newAccountCreationGas := params.CallNewAccountGas(evm.Context.QuaiStateSize)
	if gas > newAccountCreationGas {
		gas = gas - newAccountCreationGas
	} else {
		return nil, common.Zero, gas, 0, ErrInsufficientBalance
	}
	stateUsed := newAccountCreationGas
	if !evm.Context.CanTransfer(evm.StateDB, caller.Address(), value) {
		return nil, common.Zero, gas, 0, ErrInsufficientBalance
	}

	internalContractAddr, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, common.Zero, 0, 0, err
	}
	if addressOk := evm.StateDB.AddressInAccessList(internalContractAddr.Bytes20()); !addressOk {
		return nil, common.Zero, 0, 0, ErrInvalidAccessList
	}

	// Ensure there's no existing contract already at the designated address
	contractHash := evm.StateDB.GetCodeHash(internalContractAddr)
	if evm.StateDB.GetNonce(internalContractAddr) != 0 || (contractHash != (common.Hash{}) && contractHash != emptyCodeHash) {
		return nil, common.Zero, 0, 0, ErrContractAddressCollision
	}
	// Create a new account on the state
	snapshot := evm.StateDB.Snapshot()
	evm.StateDB.CreateAccount(internalContractAddr)

	evm.StateDB.SetNonce(internalContractAddr, 1)

	if err := evm.Context.Transfer(evm.StateDB, caller.Address(), address, value); err != nil {
		return nil, common.Zero, 0, stateUsed, err
	}

	// Initialise a new contract and set the code that is to be used by the EVM.
	// The contract is a scoped environment for this execution context only.
	contract := NewContract(caller, AccountRef(address), value, gas)
	contract.SetCodeOptionalHash(&address, codeAndHash)

	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, address, gas, stateUsed, nil
	}

	if evm.Config.Debug && evm.depth == 0 {
		evm.Config.Tracer.CaptureStart(evm, caller.Address(), address, true, codeAndHash.code, gas, value)
	}
	if evm.Config.Debug {
		if tracer, ok := evm.Config.Tracer.(*AccessListTracer); ok {
			tracer.list.addAddress(address)
		}
	}
	start := time.Now()

	ret, err := evm.interpreter.Run(contract, nil, false)

	// Check whether the max code size has been exceeded, assign err if the case.
	maxCodeSize := params.GetMaxCodeSize(evm.Context.BlockNumber.Uint64())
	if err == nil && len(ret) > maxCodeSize {
		err = ErrMaxCodeSizeExceeded
	}

	// Reject code starting with 0xEF
	if err == nil && len(ret) >= 1 && ret[0] == 0xEF {
		err = ErrInvalidCode
	}

	// if the contract creation ran successfully and no errors were returned
	// calculate the gas required to store the code. If the code could not
	// be stored due to not enough gas set an error and let it be handled
	// by the error checking condition below.
	if err == nil {
		createDataGas := uint64(len(ret)) * params.CreateDataGas
		if contract.UseGas(createDataGas) {
			evm.StateDB.SetCode(internalContractAddr, ret)
		} else {
			err = ErrCodeStoreOutOfGas
		}
	}

	// When an error was returned by the EVM or when setting the creation code
	// above we revert to the snapshot and consume any gas remaining. Additionally
	// when we're in this also counts for code storage gas errors.
	if err != nil && err != ErrCodeStoreOutOfGas {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			contract.UseGas(contract.Gas)
		}
	}

	if evm.Config.Debug && evm.depth == 0 {
		evm.Config.Tracer.CaptureEnd(ret, gas-contract.Gas, time.Since(start), err)
	}
	return ret, address, contract.Gas, stateUsed, err
}

// Create creates a new contract using code as deployment code.
// Difference between Ethereum EVM and Quai EVM is that Quai EVM will grind the contract address if the contract address is not valid for the current shard.
func (evm *EVM) Create(caller ContractRef, code []byte, gas uint64, value *big.Int) (ret []byte, contractAddr common.Address, leftOverGas uint64, stateUsed uint64, err error) {
	internalAddr, err := caller.Address().InternalAndQuaiAddress()
	if err != nil {
		return nil, common.Zero, 0, 0, err
	}

	nonce := evm.StateDB.GetNonce(internalAddr)

	contractAddr = crypto.CreateAddress(caller.Address(), nonce, code, evm.chainConfig.Location)
	if _, err := contractAddr.InternalAndQuaiAddress(); err == nil {
		return evm.create(caller, &codeAndHash{code: code}, gas, value, contractAddr)
	}

	// Calculate the gas required for the keccak256 computation of the input data.
	gasCost, err := calculateKeccakGas(code)
	if err != nil {
		return nil, common.Zero, 0, 0, err
	}

	// attempt to grind the address
	contractAddr, remainingGas, err := evm.attemptGrindContractCreation(caller, nonce, gas, gasCost, code)
	if err != nil {
		return nil, common.Zero, 0, 0, err
	}

	gas = remainingGas

	return evm.create(caller, &codeAndHash{code: code}, gas, value, contractAddr)
}

// calculateKeccakGas calculates the gas required for performing a keccak256 hash on the given data.
// It returns the total gas cost and any error that may occur during the calculation.
func calculateKeccakGas(data []byte) (int64, error) {
	// Base gas for keccak256 computation.
	keccakBaseGas := int64(params.Sha3Gas)
	// Calculate the number of words (rounded up) in the data for gas calculation.
	wordCount := (len(data) + 31) / 32 // Round up to the nearest word
	return keccakBaseGas + int64(wordCount)*int64(params.Sha3WordGas), nil
}

// attemptContractCreation tries to create a contract address by iterating through possible nonce values.
// It returns the modified data for contract creation and any error encountered.
func (evm *EVM) attemptGrindContractCreation(caller ContractRef, nonce uint64, gas uint64, gasCost int64, code []byte) (common.Address, uint64, error) {
	codeAndHash := &codeAndHash{code: code}
	return GrindContract(caller.Address(), nonce, gas, gasCost, codeAndHash.Hash(), evm.Context.BlockNumber, evm.chainConfig.Location)
}

func GrindContract(senderAddress common.Address, nonce uint64, gas uint64, gasCost int64, codeHash common.Hash, blockNumber *big.Int, nodeLocation common.Location) (common.Address, uint64, error) {
	var salt [32]byte
	binary.BigEndian.PutUint64(salt[24:], nonce)

	MaxAddressGrindAttemmpts := params.MaxAddressGrindAttempts
	if blockNumber.Cmp(params.NewOpcodesForkBlock) < 0 {
		MaxAddressGrindAttemmpts = params.PreviousMaxAddressGrindAttempts
	}

	// Iterate through possible nonce values to find a suitable contract address.
	for i := 0; i < MaxAddressGrindAttemmpts; i++ {

		// Check if there is enough gas left to continue.
		if gas < uint64(gasCost) {
			return common.Zero, 0, fmt.Errorf("out of gas grinding contract address for %v", senderAddress.String())
		}

		// Subtract the gas cost for each attempt.
		gas -= uint64(gasCost)

		// Place i in the [32]byte array.
		binary.BigEndian.PutUint64(salt[16:24], uint64(i))

		// Generate a potential contract address.
		contractAddr := crypto.CreateAddress2(senderAddress, salt, codeHash.Bytes(), nodeLocation)

		// Check if the generated address is valid.
		if _, err := contractAddr.InternalAndQuaiAddress(); err == nil {
			return contractAddr, gas, nil
		}
	}
	// Return an error if a valid address could not be found after the maximum number of attempts.
	return common.Zero, 0, fmt.Errorf("exceeded number of attempts grinding address %v", senderAddress.String())
}

// Create2 creates a new contract using code as deployment code.
//
// The different between Create2 with Create is Create2 uses sha3(0xff ++ msg.sender ++ salt ++ sha3(init_code))[12:]
// instead of the usual sender-and-nonce-hash as the address where the contract is initialized at.
// NOTE: In Quai EVM, Create2 does not grind the contract address if the contract address is not valid for the current shard. This means that the caller must provide a salt that creates an address that is valid for the current shard.
func (evm *EVM) Create2(caller ContractRef, code []byte, gas uint64, endowment *big.Int, salt *uint256.Int) (ret []byte, contractAddr common.Address, leftOverGas uint64, stateUsed uint64, err error) {
	codeAndHash := &codeAndHash{code: code}
	contractAddr = crypto.CreateAddress2(caller.Address(), salt.Bytes32(), codeAndHash.Hash().Bytes(), evm.chainConfig.Location)
	return evm.create(caller, codeAndHash, gas, endowment, contractAddr)
}

// CreateETX creates an external transaction that calls a function on a contract or sends a value to an address on a different shard. It is also used to create a conversion transaction.
func (evm *EVM) CreateETX(toAddr common.Address, fromAddr common.Address, gas uint64, value *big.Int, data []byte) (ret []byte, leftOverGas uint64, stateGas uint64, err error) {

	// Verify address is not in context
	if toAddr.IsInQuaiLedgerScope() && common.IsInChainScope(toAddr.Bytes(), evm.chainConfig.Location) {
		return []byte{}, 0, 0, fmt.Errorf("%x is in chain scope, but CreateETX was called", toAddr)
	}
	conversion := false
	if toAddr.IsInQiLedgerScope() && common.IsInChainScope(toAddr.Bytes(), evm.chainConfig.Location) { // Quai->Qi Conversion
		if evm.Context.PrimeTerminusNumber < params.ControllerKickInBlock {
			return []byte{}, 0, 0, fmt.Errorf("CreateETX conversion error: Quai->Qi conversion is not allowed before block %d", params.ControllerKickInBlock)
		}
		conversion = true
	}
	// If conversion, then disallow if the block number is within the hold
	// interval after the KawPow fork
	if conversion {
		if evm.Context.PrimeTerminusNumber >= params.KawPowForkBlock &&
			evm.Context.PrimeTerminusNumber < params.KawPowForkBlock+params.KQuaiChangeHoldInterval {
			return []byte{}, 0, 0, fmt.Errorf("CreateETX error: ETX is not eligible to be sent to %x until block %d", toAddr, params.KawPowForkBlock+params.KQuaiChangeHoldInterval)
		}
	}
	if toAddr.IsInQiLedgerScope() && !common.IsInChainScope(toAddr.Bytes(), evm.chainConfig.Location) {
		return []byte{}, 0, 0, fmt.Errorf("%x is in qi scope and is not in the same location, but CreateETX was called", toAddr)
	} else if conversion && value.Cmp(params.MinQuaiConversionAmount) < 0 {
		return []byte{}, 0, 0, fmt.Errorf("CreateETX conversion error: %d is not sufficient value, required amount: %d", value, params.MinQuaiConversionAmount)
	}
	if gas < params.ETXGas {
		return []byte{}, 0, 0, fmt.Errorf("CreateETX error: %d is not sufficient gas, required amount: %d", gas, params.ETXGas)
	}
	fromInternal, err := fromAddr.InternalAndQuaiAddress()
	if err != nil {
		return []byte{}, 0, 0, fmt.Errorf("CreateETX error: %s", err.Error())
	}

	gas = gas - params.ETXGas

	if gas < params.TxGas { // ETX must have enough gas to create a transaction
		return []byte{}, 0, 0, fmt.Errorf("CreateETX error: %d is not sufficient gas for ETX, required amount: %d", gas, params.TxGas)
	}

	// Fail if we're trying to transfer more than the available balance
	if !evm.Context.CanTransfer(evm.StateDB, fromAddr, value) {
		return []byte{}, 0, 0, fmt.Errorf("CreateETX: %x cannot transfer %d", fromAddr, value.Uint64())
	}

	evm.StateDB.SubBalance(fromInternal, value)

	evm.ETXCacheLock.RLock()
	index := len(evm.ETXCache) // this is virtually guaranteed to be zero, but the logic is the same as opETX
	evm.ETXCacheLock.RUnlock()
	if index > math.MaxUint16 {
		return []byte{}, 0, 0, fmt.Errorf("CreateETX overflow error: too many ETXs in cache")
	}

	etxType := types.DefaultType
	if conversion {
		etxType = types.ConversionType
	}
	// create external transaction
	etxInner := types.ExternalTx{Value: value, To: &toAddr, Sender: fromAddr, EtxType: uint64(etxType), OriginatingTxHash: evm.Hash, ETXIndex: uint16(index), Gas: gas, Data: data, AccessList: evm.AccessList}
	etx := types.NewTx(&etxInner)

	// check if the etx is eligible to be sent to the to location
	if !conversion && !evm.Context.CheckIfEtxEligible(evm.Context.EtxEligibleSlices, *etx.To().Location()) {
		return []byte{}, 0, 0, fmt.Errorf("CreateETX error: ETX is not eligible to be sent to %x", etx.To())
	}

	evm.ETXCacheLock.Lock()
	evm.ETXCache = append(evm.ETXCache, etx)
	evm.ETXCacheLock.Unlock()

	return []byte{}, 0, 0, nil // all leftover gas goes to the ETX
}

// ChainConfig returns the environment's chain configuration
func (evm *EVM) ChainConfig() *params.ChainConfig { return evm.chainConfig }

func (evm *EVM) ResetCoinbasesDeleted() {
	evm.CoinbasesDeleted = make(map[[47]byte][]byte)
	evm.CoinbaseDeletedHashes = make([]*common.Hash, 0)
}

func (evm *EVM) UndoCoinbasesDeleted() {
	for key, value := range evm.CoinbasesDeleted {
		evm.Batch.Put(key[:], value)
	}
	evm.ResetCoinbasesDeleted()
}
