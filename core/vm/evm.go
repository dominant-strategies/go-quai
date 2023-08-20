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
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
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
)

func (evm *EVM) precompile(addr common.Address) (PrecompiledContract, bool, common.Address) {
	if index, ok := TranslatedAddresses[addr.Bytes20()]; ok {
		addr = PrecompiledAddresses[common.NodeLocation.Name()][index]
	}
	p, ok := PrecompiledContracts[addr.Bytes20()]
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

	// Block information
	Coinbase    common.Address // Provides information for COINBASE
	BlockNumber *big.Int       // Provides information for NUMBER
	Time        *big.Int       // Provides information for TIME
	Difficulty  *big.Int       // Provides information for DIFFICULTY
	BaseFee     *big.Int       // Provides information for BASEFEE
}

// TxContext provides the EVM with information about a transaction.
// All fields can change between transactions.
type TxContext struct {
	// Message information
	Origin        common.Address // Provides information for ORIGIN
	ETXSender     common.Address // Original sender of the ETX
	TxType        byte
	ETXData       []byte
	ETXAccessList types.AccessList
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
	ETXCache     []*types.Transaction
	ETXCacheLock sync.RWMutex
}

// NewEVM returns a new EVM. The returned EVM is not thread safe and should
// only ever be used *once*.
func NewEVM(blockCtx BlockContext, txCtx TxContext, statedb StateDB, chainConfig *params.ChainConfig, config Config) *EVM {
	evm := &EVM{
		Context:     blockCtx,
		TxContext:   txCtx,
		StateDB:     statedb,
		Config:      config,
		chainConfig: chainConfig,
		chainRules:  chainConfig.Rules(blockCtx.BlockNumber),
		ETXCache:    make([]*types.Transaction, 0),
	}
	evm.interpreter = NewEVMInterpreter(evm, config)
	return evm
}

// Reset resets the EVM with a new transaction context.Reset
// This is not threadsafe and should only be done very cautiously.
func (evm *EVM) Reset(txCtx TxContext, statedb StateDB) {
	evm.TxContext = txCtx
	evm.StateDB = statedb
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
func (evm *EVM) Call(caller ContractRef, addr common.Address, input []byte, value *big.Int) (ret []byte, err error) {
	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, nil
	}
	// Fail if we're trying to execute above the call depth limit
	if evm.depth > int(params.CallCreateDepth) {
		return nil, ErrDepth
	}
	// Fail if we're trying to transfer more than the available balance
	if value.Sign() != 0 && !evm.Context.CanTransfer(evm.StateDB, caller.Address(), value) {
		return nil, ErrInsufficientBalance
	}
	snapshot := evm.StateDB.Snapshot()
	p, isPrecompile, addr := evm.precompile(addr)
	if evm.TxType == types.InternalToExternalTxType {
		return evm.CreateETX(addr, caller.Address(), evm.ETXData, evm.ETXAccessList, value)
	}
	internalAddr, err := addr.InternalAddress()
	if err != nil {
		// We might want to return zero leftOverGas here, but we're being nice
		return nil, err
	}
	if !evm.StateDB.Exist(internalAddr) {
		if !isPrecompile && value.Sign() == 0 {
			// Calling a non existing account, don't do anything, but ping the tracer
			if evm.Config.Debug && evm.depth == 0 {
				evm.Config.Tracer.CaptureStart(evm, caller.Address(), addr, false, input, value)
				evm.Config.Tracer.CaptureEnd(ret, 0, nil)
			}
			return nil, nil
		}
		evm.StateDB.CreateAccount(internalAddr)
	}
	if err := evm.Context.Transfer(evm.StateDB, caller.Address(), addr, value); err != nil {
		return nil, err
	}

	// Capture the tracer start/end events in debug mode
	if evm.Config.Debug && evm.depth == 0 {
		evm.Config.Tracer.CaptureStart(evm, caller.Address(), addr, false, input, value)
		defer func(startTime time.Time) { // Lazy evaluation of the parameters
			evm.Config.Tracer.CaptureEnd(ret, time.Since(startTime), err)
		}(time.Now())
	}

	if isPrecompile {
		ret, err = RunPrecompiledContract(p, input)
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
			contract := NewContract(caller, AccountRef(addrCopy), value)
			contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), code)
			ret, err = evm.interpreter.Run(contract, input, false)
		}
	}
	// When an error was returned by the EVM or when setting the creation code
	// above we revert to the snapshot and consume any gas remaining. Additionally
	// when we're in this also counts for code storage gas errors.
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
		}
		// TODO: consider clearing up unused snapshots:
		//} else {
		//	evm.StateDB.DiscardSnapshot(snapshot)
	}
	return ret, err
}

// CallCode executes the contract associated with the addr with the given input
// as parameters. It also handles any necessary value transfer required and takes
// the necessary steps to create accounts and reverses the state in case of an
// execution error or failed value transfer.
//
// CallCode differs from Call in the sense that it executes the given address'
// code with the caller as context.
func (evm *EVM) CallCode(caller ContractRef, addr common.Address, input []byte, value *big.Int) (ret []byte, err error) {
	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, nil
	}
	// Fail if we're trying to execute above the call depth limit
	if evm.depth > int(params.CallCreateDepth) {
		return nil, ErrDepth
	}
	// Fail if we're trying to transfer more than the available balance
	// Note although it's noop to transfer X ether to caller itself. But
	// if caller doesn't have enough balance, it would be an error to allow
	// over-charging itself. So the check here is necessary.
	if !evm.Context.CanTransfer(evm.StateDB, caller.Address(), value) {
		return nil, ErrInsufficientBalance
	}
	var snapshot = evm.StateDB.Snapshot()

	// It is allowed to call precompiles, even via delegatecall
	if p, isPrecompile, addr := evm.precompile(addr); isPrecompile {
		ret, err = RunPrecompiledContract(p, input)
	} else {
		addrCopy := addr
		internalAddr, err := addrCopy.InternalAddress()
		if err != nil {
			return nil, err
		}
		// Initialise a new contract and set the code that is to be used by the EVM.
		// The contract is a scoped environment for this execution context only.
		contract := NewContract(caller, AccountRef(caller.Address()), value)
		contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
		ret, err = evm.interpreter.Run(contract, input, false)
	}
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
		}
	}
	return ret, err
}

// DelegateCall executes the contract associated with the addr with the given input
// as parameters. It reverses the state in case of an execution error.
//
// DelegateCall differs from CallCode in the sense that it executes the given address'
// code with the caller as context and the caller is set to the caller of the caller.
func (evm *EVM) DelegateCall(caller ContractRef, addr common.Address, input []byte) (ret []byte, err error) {
	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, nil
	}
	// Fail if we're trying to execute above the call depth limit
	if evm.depth > int(params.CallCreateDepth) {
		return nil, ErrDepth
	}
	var snapshot = evm.StateDB.Snapshot()

	// It is allowed to call precompiles, even via delegatecall
	if p, isPrecompile, addr := evm.precompile(addr); isPrecompile {
		ret, err = RunPrecompiledContract(p, input)
	} else {
		addrCopy := addr
		internalAddr, err := addrCopy.InternalAddress()
		if err != nil {
			return nil, err
		}
		// Initialise a new contract and make initialise the delegate values
		contract := NewContract(caller, AccountRef(caller.Address()), nil).AsDelegate()
		contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
		ret, err = evm.interpreter.Run(contract, input, false)
	}
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
		}
	}
	return ret, err
}

// StaticCall executes the contract associated with the addr with the given input
// as parameters while disallowing any modifications to the state during the call.
// Opcodes that attempt to perform such modifications will result in exceptions
// instead of performing the modifications.
func (evm *EVM) StaticCall(caller ContractRef, addr common.Address, input []byte) (ret []byte, err error) {
	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, nil
	}
	// Fail if we're trying to execute above the call depth limit
	if evm.depth > int(params.CallCreateDepth) {
		return nil, ErrDepth
	}

	// We take a snapshot here. This is a bit counter-intuitive, and could probably be skipped.
	// However, even a staticcall is considered a 'touch'. On mainnet, static calls were introduced
	// after all empty accounts were deleted, so this is not required. However, if we omit this,
	// then certain tests start failing; stRevertTest/RevertPrecompiledTouchExactOOG.json.
	// We could change this, but for now it's left for legacy reasons
	var snapshot = evm.StateDB.Snapshot()

	if p, isPrecompile, addr := evm.precompile(addr); isPrecompile {
		ret, err = RunPrecompiledContract(p, input)
	} else {
		internalAddr, err := addr.InternalAddress()
		if err != nil {
			return nil, err
		}
		// At this point, we use a copy of address. If we don't, the go compiler will
		// leak the 'contract' to the outer scope, and make allocation for 'contract'
		// even if the actual execution ends on RunPrecompiled above.
		addrCopy := addr
		// Initialise a new contract and set the code that is to be used by the EVM.
		// The contract is a scoped environment for this execution context only.
		contract := NewContract(caller, AccountRef(addrCopy), new(big.Int))
		contract.SetCallCode(&addrCopy, evm.StateDB.GetCodeHash(internalAddr), evm.StateDB.GetCode(internalAddr))
		// When an error was returned by the EVM or when setting the creation code
		// above we revert to the snapshot and consume any gas remaining. Additionally
		// when we're in this also counts for code storage gas errors.
		ret, err = evm.interpreter.Run(contract, input, true)
	}
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
		}
	}
	return ret, err
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
func (evm *EVM) create(caller ContractRef, codeAndHash *codeAndHash, value *big.Int, address common.Address) ([]byte, common.Address, error) {
	internalCallerAddr, err := caller.Address().InternalAddress()
	if err != nil {
		return nil, common.ZeroAddr, err
	}
	nonce := evm.StateDB.GetNonce(internalCallerAddr)
	evm.StateDB.SetNonce(internalCallerAddr, nonce+1)

	// Depth check execution. Fail if we're trying to execute above the
	// limit.
	if evm.depth > int(params.CallCreateDepth) {
		return nil, common.ZeroAddr, ErrDepth
	}
	if !evm.Context.CanTransfer(evm.StateDB, caller.Address(), value) {
		return nil, common.ZeroAddr, ErrInsufficientBalance
	}

	internalContractAddr, err := address.InternalAddress()
	if err != nil {
		return nil, common.ZeroAddr, err
	}

	// We add this to the access list _before_ taking a snapshot. Even if the creation fails,
	// the access-list change should not be rolled back
	evm.StateDB.AddAddressToAccessList(address)

	// Ensure there's no existing contract already at the designated address
	contractHash := evm.StateDB.GetCodeHash(internalContractAddr)
	if evm.StateDB.GetNonce(internalContractAddr) != 0 || (contractHash != (common.Hash{}) && contractHash != emptyCodeHash) {
		return nil, common.ZeroAddr, ErrContractAddressCollision
	}
	// Create a new account on the state
	snapshot := evm.StateDB.Snapshot()
	evm.StateDB.CreateAccount(internalContractAddr)

	evm.StateDB.SetNonce(internalContractAddr, 1)

	if err := evm.Context.Transfer(evm.StateDB, caller.Address(), address, value); err != nil {
		return nil, common.ZeroAddr, err
	}

	// Initialise a new contract and set the code that is to be used by the EVM.
	// The contract is a scoped environment for this execution context only.
	contract := NewContract(caller, AccountRef(address), value)
	contract.SetCodeOptionalHash(&address, codeAndHash)

	if evm.Config.NoRecursion && evm.depth > 0 {
		return nil, address, nil
	}

	if evm.Config.Debug && evm.depth == 0 {
		evm.Config.Tracer.CaptureStart(evm, caller.Address(), address, true, codeAndHash.code, value)
	}
	start := time.Now()

	ret, err := evm.interpreter.Run(contract, nil, false)

	// Check whether the max code size has been exceeded, assign err if the case.
	if err == nil && len(ret) > params.MaxCodeSize {
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
		evm.StateDB.SetCode(internalContractAddr, ret)
	}

	// When an error was returned by the EVM or when setting the creation code
	// above we revert to the snapshot and consume any gas remaining. Additionally
	// when we're in this also counts for code storage gas errors.
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
		}
	}

	if evm.Config.Debug && evm.depth == 0 {
		evm.Config.Tracer.CaptureEnd(ret, time.Since(start), err)
	}
	return ret, address, err
}

// Create creates a new contract using code as deployment code.
func (evm *EVM) Create(caller ContractRef, code []byte, value *big.Int) (ret []byte, contractAddr common.Address, err error) {
	internalAddr, err := caller.Address().InternalAddress()
	if err != nil {
		return nil, common.ZeroAddr, err
	}
	contractAddr = crypto.CreateAddress(caller.Address(), evm.StateDB.GetNonce(internalAddr), code)
	return evm.create(caller, &codeAndHash{code: code}, value, contractAddr)
}

// Create2 creates a new contract using code as deployment code.
//
// The different between Create2 with Create is Create2 uses sha3(0xff ++ msg.sender ++ salt ++ sha3(init_code))[12:]
// instead of the usual sender-and-nonce-hash as the address where the contract is initialized at.
func (evm *EVM) Create2(caller ContractRef, code []byte, endowment *big.Int, salt *uint256.Int) (ret []byte, contractAddr common.Address, err error) {
	codeAndHash := &codeAndHash{code: code}
	contractAddr = crypto.CreateAddress2(caller.Address(), salt.Bytes32(), codeAndHash.Hash().Bytes())
	return evm.create(caller, codeAndHash, endowment, contractAddr)
}

func (evm *EVM) CreateETX(toAddr common.Address, fromAddr common.Address, etxData []byte, etxAccessList types.AccessList, value *big.Int) (ret []byte, err error) {

	// Verify address is not in context
	if common.IsInChainScope(toAddr.Bytes()) {
		return []byte{}, fmt.Errorf("%x is in chain scope, but CreateETX was called", toAddr)
	}
	fromInternal, err := fromAddr.InternalAddress()
	if err != nil {
		return []byte{}, fmt.Errorf("CreateETX error: %s", err.Error())
	}

	fee := big.NewInt(0)
	total := big.NewInt(0)
	total.Add(value, fee)
	// Fail if we're trying to transfer more than the available balance
	if total.Sign() == 0 || !evm.Context.CanTransfer(evm.StateDB, fromAddr, total) {
		return []byte{}, fmt.Errorf("CreateETX: %x cannot transfer %d", fromAddr, total.Uint64())
	}

	evm.StateDB.SubBalance(fromInternal, total)

	nonce := evm.StateDB.GetNonce(fromInternal)

	// create external transaction
	etxInner := types.ExternalTx{Value: value, To: &toAddr, Sender: fromAddr, Data: etxData, AccessList: etxAccessList, Nonce: nonce, ChainID: evm.chainConfig.ChainID}
	etx := types.NewTx(&etxInner)

	evm.ETXCacheLock.Lock()
	evm.ETXCache = append(evm.ETXCache, etx)
	evm.ETXCacheLock.Unlock()

	return []byte{}, nil
}

// Emitted ETXs must include some multiple of BaseFee as miner tip, to
// encourage processing at the destination.
func calcEtxFeeMultiplier(fromAddr, toAddr common.Address) *big.Int {
	confirmationCtx := fromAddr.Location().CommonDom(*toAddr.Location()).Context()
	multiplier := big.NewInt(common.NumZonesInRegion)
	if confirmationCtx == common.PRIME_CTX {
		multiplier = big.NewInt(0).Mul(multiplier, big.NewInt(common.NumRegionsInPrime))
	}
	return multiplier
}

// ChainConfig returns the environment's chain configuration
func (evm *EVM) ChainConfig() *params.ChainConfig { return evm.chainConfig }
