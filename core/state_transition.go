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

package core

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
)

var emptyCodeHash = crypto.Keccak256Hash(nil)
var suicide = []byte("Suicide")

// conversion related variables
var (
	kQuaiSettingAddress = common.HexToAddress("0x00201De0D8854d63121c0cfF96Ae01cD3ef62414", common.Location{0, 0})
	updateKQuai         = []byte("update")
	updateKQuaiLen      = len(updateKQuai)
	freezeKQuai         = []byte("freeze")
	freezeKQuaiLen      = len(freezeKQuai)
	unfreezeKQuai       = []byte("unfreeze")
	unfreezeKQuaiLen    = len(unfreezeKQuai)
)

/*
The State Transitioning Model

A state transition is a change made when a transaction is applied to the current world state
The state transitioning model does all the necessary work to work out a valid new state root.

1) Nonce handling
2) Pre pay gas
3) Create a new state object if the recipient is \0*32
4) Value transfer
== If contract creation ==

	4a) Attempt to run transaction data
	4b) If valid, use result as code for the new state object

== end ==
5) Run Script section
6) Derive new state root
*/
type StateTransition struct {
	gp         *types.GasPool
	msg        Message
	gas        uint64
	gasPrice   *big.Int
	initialGas uint64
	value      *big.Int
	data       []byte
	state      vm.StateDB
	evm        *vm.EVM
}

func (st *StateTransition) fee() *big.Int {
	return st.gasPrice
}

// Message represents a message sent to a contract.
type Message interface {
	From() common.Address
	To() *common.Address

	GasPrice() *big.Int
	Gas() uint64
	Value() *big.Int

	Nonce() uint64
	IsETX() bool
	Data() []byte
	AccessList() types.AccessList
	ETXSender() common.Address
	Type() byte
	Hash() common.Hash
}

// ExecutionResult includes all output after executing given evm
// message no matter the execution itself is successful or not.
type ExecutionResult struct {
	UsedGas      uint64               // Total used gas but include the refunded gas
	UsedState    uint64               // Total used state
	Err          error                // Any error encountered during the execution(listed in core/vm/errors.go)
	ReturnData   []byte               // Returned data from evm(function result or data supplied with revert opcode)
	Etxs         []*types.Transaction // External transactions generated from opETX
	QuaiFees     *big.Int             // Fees that needs to be credited to the miner from the transaction
	ContractAddr *common.Address      // Address of the contract created by the message
}

// Unwrap returns the internal evm error which allows us for further
// analysis outside.
func (result *ExecutionResult) Unwrap() error {
	return result.Err
}

// Failed returns the indicator whether the execution is successful or not
func (result *ExecutionResult) Failed() bool { return result.Err != nil }

// Return is a helper function to help caller distinguish between revert reason
// and function return. Return returns the data after execution if no error occurs.
func (result *ExecutionResult) Return() []byte {
	if result.Err != nil {
		return nil
	}
	return common.CopyBytes(result.ReturnData)
}

// Revert returns the concrete revert reason if the execution is aborted by `REVERT`
// opcode. Note the reason can be nil if no data supplied with revert opcode.
func (result *ExecutionResult) Revert() []byte {
	if result.Err != vm.ErrExecutionReverted {
		return nil
	}
	return common.CopyBytes(result.ReturnData)
}

// IntrinsicGas computes the 'intrinsic gas' for a message with the given data.
func IntrinsicGas(data []byte, accessList types.AccessList, isContractCreation bool) (uint64, error) {
	// Set the starting gas for the raw transaction
	var gas uint64
	if isContractCreation {
		gas = params.TxGasContractCreation
	} else {
		gas = params.TxGas
	}
	// Bump the required gas by the amount of transactional data
	if len(data) > 0 {
		// Zero and non-zero bytes are priced differently
		var nz uint64
		for _, byt := range data {
			if byt != 0 {
				nz++
			}
		}
		// Make sure we don't exceed uint64 for all data combinations
		nonZeroGas := params.TxDataNonZeroGas
		if (math.MaxUint64-gas)/nonZeroGas < nz {
			return 0, ErrGasUintOverflow
		}
		gas += nz * nonZeroGas

		z := uint64(len(data)) - nz
		if (math.MaxUint64-gas)/params.TxDataZeroGas < z {
			return 0, ErrGasUintOverflow
		}
		gas += z * params.TxDataZeroGas
	}
	if accessList != nil {
		gas += uint64(len(accessList)) * params.TxAccessListAddressGas
		gas += uint64(accessList.StorageKeys()) * params.TxAccessListStorageKeyGas
	}
	return gas, nil
}

// NewStateTransition initialises and returns a new state transition object.
func NewStateTransition(evm *vm.EVM, msg Message, gp *types.GasPool) *StateTransition {
	return &StateTransition{
		gp:       gp,
		evm:      evm,
		msg:      msg,
		gasPrice: msg.GasPrice(),
		value:    msg.Value(),
		data:     msg.Data(),
		state:    evm.StateDB,
	}
}

// ApplyMessage computes the new state by applying the given message
// against the old state within the environment.
//
// ApplyMessage returns the bytes returned by any EVM execution (if it took place),
// the gas used (which includes gas refunds) and an error if it failed. An error always
// indicates a core error meaning that the message would always fail for that particular
// state and would never be accepted within a block.
func ApplyMessage(evm *vm.EVM, msg Message, gp *types.GasPool) (*ExecutionResult, error) {
	return NewStateTransition(evm, msg, gp).TransitionDb()
}

// to returns the recipient of the message.
func (st *StateTransition) to() common.Address {
	if st.msg == nil || st.msg.To() == nil /* contract creation */ {
		return common.ZeroAddress(st.evm.ChainConfig().Location)
	}
	return *st.msg.To()
}

func (st *StateTransition) buyGas() error {
	mgval := new(big.Int).SetUint64(st.msg.Gas())
	mgval = mgval.Mul(mgval, st.gasPrice)
	balanceCheck := mgval
	balanceCheck = new(big.Int).SetUint64(st.msg.Gas())
	balanceCheck = balanceCheck.Mul(balanceCheck, st.gasPrice)
	balanceCheck.Add(balanceCheck, st.value)
	from, err := st.msg.From().InternalAndQuaiAddress()
	if err != nil {
		return err
	}
	if have, want := st.state.GetBalance(from), balanceCheck; have.Cmp(want) < 0 {
		return fmt.Errorf("%w: address %v have %v want %v", ErrInsufficientFunds, st.msg.From().Hex(), have, want)
	}
	if err := st.gp.SubGas(st.msg.Gas()); err != nil {
		return err
	}
	st.gas += st.msg.Gas()

	st.initialGas = st.msg.Gas()
	st.state.SubBalance(from, mgval)
	return nil
}

// subGasETX subtracts the gas for an ETX from the gas pool and adds it to the total gas used.
// The ETX does not pay for the gas.
func (st *StateTransition) subGasETX() error {
	maxEtxGasLimit := st.evm.Context.GasLimit / params.MinimumEtxGasDivisor
	if st.msg.Gas() > maxEtxGasLimit {
		if err := st.gp.SubGas(params.TxGas); err != nil {
			return err
		}
		return fmt.Errorf("%w: have %d, want %d", ErrEtxGasLimitReached, st.msg.Gas(), maxEtxGasLimit)
	}
	if err := st.gp.SubGas(st.msg.Gas()); err != nil {
		return err
	}
	st.gas += st.msg.Gas()
	st.initialGas = st.msg.Gas()
	return nil
}

func (st *StateTransition) preCheck() error {
	from, err := st.msg.From().InternalAndQuaiAddress()
	if err != nil {
		return err
	}
	// Make sure this transaction's nonce is correct.
	stNonce := st.state.GetNonce(from)
	if msgNonce := st.msg.Nonce(); stNonce < msgNonce {
		return fmt.Errorf("%w: address %v, tx: %d state: %d", ErrNonceTooHigh,
			st.msg.From().Hex(), msgNonce, stNonce)
	} else if stNonce > msgNonce {
		return fmt.Errorf("%w: address %v, tx: %d state: %d", ErrNonceTooLow,
			st.msg.From().Hex(), msgNonce, stNonce)
	}
	// Make sure the sender is an EOA
	if codeHash := st.state.GetCodeHash(from); codeHash != emptyCodeHash && codeHash != (common.Hash{}) {
		return fmt.Errorf("%w: address %v, codehash: %s", ErrSenderNoEOA,
			st.msg.From().Hex(), codeHash)
	}
	// Make sure that transaction gasPrice is greater than the baseFee
	// Skip the checks if gas fields are zero and baseFee was explicitly disabled (eth_call)
	if !st.evm.Config.NoBaseFee || st.gasPrice.BitLen() > 0 {
		if l := st.gasPrice.BitLen(); l > 256 {
			return fmt.Errorf("%w: address %v, gasPrice bit length: %d", ErrFeeCapVeryHigh,
				st.msg.From().Hex(), l)
		}
		// This will panic if baseFee is nil, but basefee presence is verified
		// as part of header validation.
		if st.gasPrice.Cmp(st.evm.Context.BaseFee) < 0 {
			return fmt.Errorf("%w: address %v, gasPrice: %s baseFee: %s", ErrFeeCapTooLow,
				st.msg.From().Hex(), st.gasPrice, st.evm.Context.BaseFee)
		}
	}
	return st.buyGas()
}

// TransitionDb will transition the state by applying the current message and
// returning the evm execution result with following fields.
//
//   - used gas:
//     total gas used (including gas being refunded)
//   - returndata:
//     the returned data from evm
//   - concrete execution error:
//     various **EVM** error which aborts the execution,
//     e.g. ErrOutOfGas, ErrExecutionReverted
//
// However if any consensus issue encountered, return the error directly with
// nil evm execution result.
func (st *StateTransition) TransitionDb() (*ExecutionResult, error) {
	// First check this message satisfies all consensus rules before
	// applying the message. The rules include these clauses
	//
	// 1. the nonce of the message caller is correct
	// 2. caller has enough balance to cover transaction fee(gaslimit * gasprice)
	// 3. the amount of gas required is available in the block
	// 4. the purchased gas is enough to cover intrinsic usage
	// 5. there is no overflow when calculating intrinsic gas
	// 6. caller has enough balance to cover asset transfer for **topmost** call

	// If ETX, bypass accessList enforcement
	if st.msg.IsETX() {
		orig := st.state.ConfigureAccessListChecks(false)
		defer st.state.ConfigureAccessListChecks(orig)
	}

	// Check clauses 1-3, buy gas if everything is correct
	if !st.msg.IsETX() {
		if err := st.preCheck(); err != nil {
			return nil, err
		}
	} else if err := st.subGasETX(); err != nil {
		if strings.Contains(err.Error(), ErrEtxGasLimitReached.Error()) {
			return &ExecutionResult{
				UsedGas:      params.TxGas,
				UsedState:    params.EtxStateUsed,
				Err:          err,
				ReturnData:   []byte{},
				Etxs:         nil,
				ContractAddr: nil,
			}, nil
		} else {
			return nil, err
		}
	}

	msg := st.msg
	sender := vm.AccountRef(msg.From())
	contractCreation := msg.To() == nil
	if st.msg.IsETX() {
		// for ETX contract creation, we compare the "to" to the contextual zero-address (only in the ETX case)
		// the "from" is also set to the contextual zero-address, so it will look like a transaction from the zero-address to itself
		contractCreation = msg.To().Equal(common.ZeroAddress(st.evm.ChainConfig().Location))
	}
	// Check clauses 4-5, subtract intrinsic gas if everything is correct
	gas, err := IntrinsicGas(st.data, st.msg.AccessList(), contractCreation)
	if err != nil {
		return nil, err
	}
	if st.gas < gas {
		return nil, fmt.Errorf("%w: have %d, want %d", ErrIntrinsicGas, st.gas, gas)
	}
	st.gas -= gas

	// Check clause 6
	if msg.Value().Sign() > 0 && !st.evm.Context.CanTransfer(st.state, msg.From(), msg.Value()) {
		return nil, fmt.Errorf("%w: address %v", ErrInsufficientFundsForTransfer, msg.From().Hex())
	}

	// Set up the initial access list.
	rules := st.evm.ChainConfig().Rules(st.evm.Context.BlockNumber)
	activePrecompiles := vm.ActivePrecompiles(rules, st.evm.ChainConfig().Location)
	st.state.PrepareAccessList(msg.From(), msg.To(), activePrecompiles, msg.AccessList(), st.evm.Config.Debug)

	// If the transaction is coming from kQuaiSettingAddress, it can encode information in the data and
	// perform three things
	// 1) Set the KQuai exchange rate so that the protocol can use it going forward
	// from the next block
	// 2) Freeze the KQuai exchange rate computation, once this is done, the
	// KQuai controller stops computing until its unforzen
	// 3) UnFreeze the KQuai exchange rate computation, once this is done, the
	// KQuai controller, starts computing the KQuai value
	// This address cannot do anything else
	if msg.From().Equal(kQuaiSettingAddress) && st.evm.Context.BlockNumber.Uint64() < params.BlocksPerYear {
		if !st.msg.IsETX() && !contractCreation && len(st.data) > updateKQuaiLen && bytes.Equal(st.data[:updateKQuaiLen], updateKQuai) {
			kQuaiValue := new(big.Int).SetBytes(st.data[updateKQuaiLen:])
			err := st.state.UpdateKQuai(kQuaiValue)
			if err != nil {
				return nil, fmt.Errorf("unable to update k quai: %v", err)
			}
		} else if !st.msg.IsETX() && !contractCreation && len(st.data) == freezeKQuaiLen && bytes.Equal(st.data[:freezeKQuaiLen], freezeKQuai) {
			err := st.state.FreezeKQuai()
			if err != nil {
				return nil, fmt.Errorf("unable to freeze k quai: %v", err)
			}
		} else if !st.msg.IsETX() && !contractCreation && len(st.data) == unfreezeKQuaiLen && bytes.Equal(st.data[:unfreezeKQuaiLen], unfreezeKQuai) {
			err := st.state.UnFreezeKQuai()
			if err != nil {
				return nil, fmt.Errorf("unable to un freeze k quai: %v", err)
			}
		} else {
			return nil, fmt.Errorf("invalid data used: %v", st.data)
		}

		from, err := msg.From().InternalAddress()
		if err != nil {
			return nil, err
		}
		st.state.SetNonce(from, st.state.GetNonce(from)+1)

		effectiveTip := st.fee()
		return &ExecutionResult{
			UsedGas:      st.gasUsed(),
			UsedState:    0,
			Err:          nil,
			ReturnData:   []byte{},
			Etxs:         nil,
			QuaiFees:     new(big.Int).Mul(new(big.Int).SetUint64(st.gasUsed()), effectiveTip),
			ContractAddr: nil,
		}, nil

	} else if msg.From().Equal(kQuaiSettingAddress) {
		return nil, fmt.Errorf("transaction is not allowed from the kQuaiSettingAddress after the first year")
	}

	if !st.msg.IsETX() && !contractCreation && len(st.data) == 27 && bytes.Equal(st.data[:7], suicide) && st.to().Equal(st.msg.From()) {
		// Caller requests self-destruct
		beneficiary, err := common.BytesToAddress(st.data[7:27], st.evm.ChainConfig().Location).InternalAndQuaiAddress()
		if err != nil {
			return nil, fmt.Errorf("Unable to self-destruct: %v", err)
		}
		fromInternal, err := msg.From().InternalAndQuaiAddress()
		if err != nil {
			return nil, fmt.Errorf("Unable to self-destruct: %v", err)
		}
		balance := st.evm.StateDB.GetBalance(fromInternal)
		st.evm.StateDB.Suicide(fromInternal)
		refund := new(big.Int).Mul(st.evm.Context.BaseFee, new(big.Int).SetUint64(params.CallNewAccountGas(st.evm.Context.QuaiStateSize)))
		balance.Add(balance, refund)
		st.evm.StateDB.AddBalance(beneficiary, balance)

		effectiveTip := st.fee()
		return &ExecutionResult{
			UsedGas:      st.gasUsed(),
			UsedState:    0,
			Err:          nil,
			ReturnData:   []byte{},
			Etxs:         nil,
			QuaiFees:     new(big.Int).Mul(new(big.Int).SetUint64(st.gasUsed()), effectiveTip),
			ContractAddr: nil,
		}, nil
	}
	var (
		ret          []byte
		vmerr        error // vm errors do not effect consensus and are therefore not assigned to err
		contractAddr *common.Address
	)
	var stateUsed uint64
	if contractCreation {
		var contract common.Address
		ret, contract, st.gas, stateUsed, vmerr = st.evm.Create(sender, st.data, st.gas, st.value)
		contractAddr = &contract
	} else {
		// Increment the nonce for the next transaction
		addr, err := sender.Address().InternalAndQuaiAddress()
		if err != nil {
			return nil, err
		}
		from, err := msg.From().InternalAndQuaiAddress()
		if err != nil {
			return nil, err
		}
		st.state.SetNonce(from, st.state.GetNonce(addr)+1)
		ret, st.gas, stateUsed, vmerr = st.evm.Call(sender, st.to(), st.data, st.gas, st.value)
	}

	// At this point, the execution completed, so the ETX cache can be dumped and reset
	st.evm.ETXCacheLock.Lock()
	etxs := make([]*types.Transaction, len(st.evm.ETXCache))
	copy(etxs, st.evm.ETXCache)
	st.evm.ETXCache = make([]*types.Transaction, 0)
	st.evm.ETXCacheLock.Unlock()

	// refunds are capped to gasUsed / 5
	st.refundGas(params.RefundQuotient)

	effectiveTip := st.fee()

	_, err = st.evm.Context.PrimaryCoinbase.InternalAddress()
	if err != nil {
		return nil, err
	}
	fees := big.NewInt(0)
	if !st.msg.IsETX() {
		fees = new(big.Int).Mul(new(big.Int).SetUint64(st.gasUsed()), effectiveTip)
	}

	return &ExecutionResult{
		UsedGas:      st.gasUsed(),
		UsedState:    stateUsed,
		Err:          vmerr,
		ReturnData:   ret,
		Etxs:         etxs,
		QuaiFees:     fees,
		ContractAddr: contractAddr,
	}, nil
}

func (st *StateTransition) refundGas(refundQuotient uint64) {
	// Apply refund counter, capped to a refund quotient
	refund := st.gasUsed() / refundQuotient
	if refund > st.state.GetRefund() {
		refund = st.state.GetRefund()
	}
	st.gas += refund

	// Return ETH for remaining gas, exchanged at the original rate.
	remaining := new(big.Int).Mul(new(big.Int).SetUint64(st.gas), st.gasPrice)
	from, err := st.msg.From().InternalAndQuaiAddress()
	if err != nil {
		return
	}
	st.state.AddBalance(from, remaining)

	// Also return remaining gas to the block gas counter so it is
	// available for the next transaction.
	st.gp.AddGas(st.gas)
}

// gasUsed returns the amount of gas used up by the state transition.
func (st *StateTransition) gasUsed() uint64 {
	return st.initialGas - st.gas
}
