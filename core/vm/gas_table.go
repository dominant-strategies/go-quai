// Copyright 2017 The go-ethereum Authors
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
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/params"
)

// memoryGasCost calculates the quadratic gas for memory expansion. It does so
// only for the memory region that is expanded, not the total memory.
func memoryGasCost(mem *Memory, newMemSize uint64) (uint64, uint64, error) {
	if newMemSize == 0 {
		return 0, 0, nil
	}
	// The maximum that will fit in a uint64 is max_word_count - 1. Anything above
	// that will result in an overflow. Additionally, a newMemSize which results in
	// a newMemSizeWords larger than 0xFFFFFFFF will cause the square operation to
	// overflow. The constant 0x1FFFFFFFE0 is the highest number that can be used
	// without overflowing the gas calculation.
	if newMemSize > 0x1FFFFFFFE0 {
		return 0, 0, ErrGasUintOverflow
	}
	newMemSizeWords := toWordSize(newMemSize)
	newMemSize = newMemSizeWords * 32

	if newMemSize > uint64(mem.Len()) {
		square := newMemSizeWords * newMemSizeWords
		linCoef := newMemSizeWords * params.MemoryGas
		quadCoef := square / params.QuadCoeffDiv
		newTotalFee := linCoef + quadCoef

		fee := newTotalFee - mem.lastGasCost
		mem.lastGasCost = newTotalFee

		return fee, 0, nil
	}
	return 0, 0, nil
}

// memoryCopierGas creates the gas functions for the following opcodes, and takes
// the stack position of the operand which determines the size of the data to copy
// as argument:
// CALLDATACOPY (stack position 2)
// CODECOPY (stack position 2)
// EXTCODECOPY (stack poition 3)
// RETURNDATACOPY (stack position 2)
func memoryCopierGas(stackpos int) gasFunc {
	return func(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
		// Gas for expanding the memory
		gas, _, err := memoryGasCost(mem, memorySize)
		if err != nil {
			return 0, 0, err
		}
		// And gas for copying data, charged per word at param.CopyGas
		words, overflow := stack.Back(stackpos).Uint64WithOverflow()
		if overflow {
			return 0, 0, ErrGasUintOverflow
		}

		if words, overflow = math.SafeMul(toWordSize(words), params.CopyGas); overflow {
			return 0, 0, ErrGasUintOverflow
		}

		if gas, overflow = math.SafeAdd(gas, words); overflow {
			return 0, 0, ErrGasUintOverflow
		}
		return gas, 0, nil
	}
}

var (
	gasCallDataCopy   = memoryCopierGas(2)
	gasCodeCopy       = memoryCopierGas(2)
	gasReturnDataCopy = memoryCopierGas(2)
)

func makeGasLog(n uint64) gasFunc {
	return func(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
		requestedSize, overflow := stack.Back(1).Uint64WithOverflow()
		if overflow {
			return 0, 0, ErrGasUintOverflow
		}

		gas, _, err := memoryGasCost(mem, memorySize)
		if err != nil {
			return 0, 0, err
		}

		if gas, overflow = math.SafeAdd(gas, params.LogGas); overflow {
			return 0, 0, ErrGasUintOverflow
		}
		if gas, overflow = math.SafeAdd(gas, n*params.LogTopicGas); overflow {
			return 0, 0, ErrGasUintOverflow
		}

		var memorySizeGas uint64
		if memorySizeGas, overflow = math.SafeMul(requestedSize, params.LogDataGas); overflow {
			return 0, 0, ErrGasUintOverflow
		}
		if gas, overflow = math.SafeAdd(gas, memorySizeGas); overflow {
			return 0, 0, ErrGasUintOverflow
		}
		// make gas log doesnt use any state operation, so return 0 for stateGas used
		return gas, 0, nil
	}
}

func gasSha3(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
	gas, _, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, 0, err
	}
	wordGas, overflow := stack.Back(1).Uint64WithOverflow()
	if overflow {
		return 0, 0, ErrGasUintOverflow
	}
	if wordGas, overflow = math.SafeMul(toWordSize(wordGas), params.Sha3WordGas); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	if gas, overflow = math.SafeAdd(gas, wordGas); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	return gas, 0, nil
}

// pureMemoryGascost is used by several operations, which aside from their
// static cost have a dynamic cost which is solely based on the memory
// expansion
func pureMemoryGascost(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
	return memoryGasCost(mem, memorySize)
}

var (
	gasReturn  = pureMemoryGascost
	gasRevert  = pureMemoryGascost
	gasMLoad   = pureMemoryGascost
	gasMStore8 = pureMemoryGascost
	gasMStore  = pureMemoryGascost
	gasCreate  = pureMemoryGascost
)

func gasCreate2(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
	gas, _, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, 0, err
	}
	wordGas, overflow := stack.Back(2).Uint64WithOverflow()
	if overflow {
		return 0, 0, ErrGasUintOverflow
	}
	if wordGas, overflow = math.SafeMul(toWordSize(wordGas), params.Sha3WordGas); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	if gas, overflow = math.SafeAdd(gas, wordGas); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	return gas, 0, nil
}

func gasExp(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
	expByteLen := uint64((stack.data[stack.len()-2].BitLen() + 7) / 8)

	var (
		gas      = expByteLen * params.ExpByte // no overflow check required. Max is 256 * ExpByte gas
		overflow bool
	)
	if gas, overflow = math.SafeAdd(gas, params.ExpGas); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	return gas, 0, nil
}

func gasCall(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
	var (
		gas            uint64
		stateGas       uint64
		transfersValue = !stack.Back(2).IsZero()
		address, err   = common.Bytes20ToAddress(stack.Back(1).Bytes20(), evm.chainConfig.Location).InternalAndQuaiAddress()
	)
	if err != nil {
		return 0, 0, err
	}
	if transfersValue && evm.StateDB.Empty(address) {
		gas += params.CallNewAccountGas
	}
	if transfersValue {
		gas += params.CallValueTransferGas
	}
	memoryGas, _, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, 0, err
	}
	var overflow bool
	if gas, overflow = math.SafeAdd(gas, memoryGas); overflow {
		return 0, 0, ErrGasUintOverflow
	}

	evm.callGasTemp, err = callGas(contract.Gas, gas, stack.Back(0))
	if err != nil {
		return 0, 0, err
	}
	if gas, overflow = math.SafeAdd(gas, evm.callGasTemp); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	return gas, stateGas, nil
}

func gasCallCode(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
	memoryGas, _, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, 0, err
	}
	var (
		gas      uint64
		stateGas uint64
		overflow bool
	)
	if stack.Back(2).Sign() != 0 {
		gas += params.CallValueTransferGas
	}
	if gas, overflow = math.SafeAdd(gas, memoryGas); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	evm.callGasTemp, err = callGas(contract.Gas, gas, stack.Back(0))
	if err != nil {
		return 0, 0, err
	}
	if gas, overflow = math.SafeAdd(gas, evm.callGasTemp); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	return gas, stateGas, nil
}

func gasDelegateCall(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
	gas, _, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, 0, err
	}
	evm.callGasTemp, err = callGas(contract.Gas, gas, stack.Back(0))
	if err != nil {
		return 0, 0, err
	}
	var overflow bool
	if gas, overflow = math.SafeAdd(gas, evm.callGasTemp); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	return gas, 0, nil // TODO: Need to check if this method acceses state
}

func gasStaticCall(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, uint64, error) {
	gas, _, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, 0, err
	}
	evm.callGasTemp, err = callGas(contract.Gas, gas, stack.Back(0))
	if err != nil {
		return 0, 0, err
	}
	var overflow bool
	if gas, overflow = math.SafeAdd(gas, evm.callGasTemp); overflow {
		return 0, 0, ErrGasUintOverflow
	}
	return gas, 0, nil // TODO: check of this method needs state gas
}

func gasSelfdestruct(evm *EVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	var gas uint64
	contractAddr, err := contract.Address().InternalAndQuaiAddress()
	if err != nil {
		return 0, err
	}
	gas = params.SelfdestructGas
	address, err := common.Bytes20ToAddress(stack.Back(0).Bytes20(), evm.chainConfig.Location).InternalAndQuaiAddress()
	if err != nil {
		return 0, err
	}
	// if empty and transfers value
	if evm.StateDB.Empty(address) && evm.StateDB.GetBalance(contractAddr).Sign() != 0 {
		gas += params.CreateBySelfdestructGas
	}

	if !evm.StateDB.HasSuicided(contractAddr) {
		evm.StateDB.AddRefund(params.SelfdestructRefundGas)
	}
	return gas, nil
}
