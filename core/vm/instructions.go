// Copyright 2015 The go-ethereum Authors
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
	"math"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/holiman/uint256"
	"golang.org/x/crypto/sha3"
)

func opAdd(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.Add(&x, y)
	return nil, nil
}

func opSub(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.Sub(&x, y)
	return nil, nil
}

func opMul(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.Mul(&x, y)
	return nil, nil
}

func opDiv(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.Div(&x, y)
	return nil, nil
}

func opSdiv(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.SDiv(&x, y)
	return nil, nil
}

func opMod(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.Mod(&x, y)
	return nil, nil
}

func opSmod(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.SMod(&x, y)
	return nil, nil
}

func opExp(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	base, exponent := scope.Stack.pop(), scope.Stack.peek()
	exponent.Exp(&base, exponent)
	return nil, nil
}

func opSignExtend(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	back, num := scope.Stack.pop(), scope.Stack.peek()
	num.ExtendSign(num, &back)
	return nil, nil
}

func opNot(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x := scope.Stack.peek()
	x.Not(x)
	return nil, nil
}

func opLt(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	if x.Lt(y) {
		y.SetOne()
	} else {
		y.Clear()
	}
	return nil, nil
}

func opGt(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	if x.Gt(y) {
		y.SetOne()
	} else {
		y.Clear()
	}
	return nil, nil
}

func opSlt(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	if x.Slt(y) {
		y.SetOne()
	} else {
		y.Clear()
	}
	return nil, nil
}

func opSgt(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	if x.Sgt(y) {
		y.SetOne()
	} else {
		y.Clear()
	}
	return nil, nil
}

func opEq(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	if x.Eq(y) {
		y.SetOne()
	} else {
		y.Clear()
	}
	return nil, nil
}

func opIszero(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x := scope.Stack.peek()
	if x.IsZero() {
		x.SetOne()
	} else {
		x.Clear()
	}
	return nil, nil
}

func opAnd(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.And(&x, y)
	return nil, nil
}

func opOr(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.Or(&x, y)
	return nil, nil
}

func opXor(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y := scope.Stack.pop(), scope.Stack.peek()
	y.Xor(&x, y)
	return nil, nil
}

func opByte(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	th, val := scope.Stack.pop(), scope.Stack.peek()
	val.Byte(&th)
	return nil, nil
}

func opAddmod(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y, z := scope.Stack.pop(), scope.Stack.pop(), scope.Stack.peek()
	if z.IsZero() {
		z.Clear()
	} else {
		z.AddMod(&x, &y, z)
	}
	return nil, nil
}

func opMulmod(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x, y, z := scope.Stack.pop(), scope.Stack.pop(), scope.Stack.peek()
	z.MulMod(&x, &y, z)
	return nil, nil
}

// opSHL implements Shift Left
// The SHL instruction (shift left) pops 2 values from the stack, first arg1 and then arg2,
// and pushes on the stack arg2 shifted to the left by arg1 number of bits.
func opSHL(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	// Note, second operand is left in the stack; accumulate result into it, and no need to push it afterwards
	shift, value := scope.Stack.pop(), scope.Stack.peek()
	if shift.LtUint64(256) {
		value.Lsh(value, uint(shift.Uint64()))
	} else {
		value.Clear()
	}
	return nil, nil
}

// opSHR implements Logical Shift Right
// The SHR instruction (logical shift right) pops 2 values from the stack, first arg1 and then arg2,
// and pushes on the stack arg2 shifted to the right by arg1 number of bits with zero fill.
func opSHR(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	// Note, second operand is left in the stack; accumulate result into it, and no need to push it afterwards
	shift, value := scope.Stack.pop(), scope.Stack.peek()
	if shift.LtUint64(256) {
		value.Rsh(value, uint(shift.Uint64()))
	} else {
		value.Clear()
	}
	return nil, nil
}

// opSAR implements Arithmetic Shift Right
// The SAR instruction (arithmetic shift right) pops 2 values from the stack, first arg1 and then arg2,
// and pushes on the stack arg2 shifted to the right by arg1 number of bits with sign extension.
func opSAR(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	shift, value := scope.Stack.pop(), scope.Stack.peek()
	if shift.GtUint64(256) {
		if value.Sign() >= 0 {
			value.Clear()
		} else {
			// Max negative shift: all bits set
			value.SetAllOne()
		}
		return nil, nil
	}
	n := uint(shift.Uint64())
	value.SRsh(value, n)
	return nil, nil
}

func opSha3(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	offset, size := scope.Stack.pop(), scope.Stack.peek()
	data := scope.Memory.GetPtr(int64(offset.Uint64()), int64(size.Uint64()))

	if interpreter.hasher == nil {
		interpreter.hasher = sha3.NewLegacyKeccak256().(keccakState)
	} else {
		interpreter.hasher.Reset()
	}
	interpreter.hasher.Write(data)
	interpreter.hasher.Read(interpreter.hasherBuf[:])

	evm := interpreter.evm
	if evm.Config.EnablePreimageRecording {
		evm.StateDB.AddPreimage(interpreter.hasherBuf, data)
	}

	size.SetBytes(interpreter.hasherBuf[:])
	return nil, nil
}
func opAddress(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetBytes(scope.Contract.Address().Bytes()))
	return nil, nil
}

func opBalance(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	slot := scope.Stack.peek()
	address, err := common.Bytes20ToAddress(slot.Bytes20(), interpreter.evm.chainConfig.Location).InternalAndQuaiAddress()
	if err != nil { // if an ErrInvalidScope error is returned, the caller (usually interpreter.go/Run) will return the error to Call which will eventually set ReceiptStatusFailed in the tx receipt (state_processor.go/applyTransaction)
		return nil, err
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(address.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	slot.SetFromBig(interpreter.evm.StateDB.GetBalance(address))
	return nil, nil
}

func opOrigin(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetBytes(interpreter.evm.Origin.Bytes()))
	return nil, nil
}
func opCaller(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	if interpreter.evm.TxType == types.ExternalTxType {
		scope.Stack.push(new(uint256.Int).SetBytes(interpreter.evm.ETXSender.Bytes()))
	} else {
		scope.Stack.push(new(uint256.Int).SetBytes(scope.Contract.Caller().Bytes()))
	}

	return nil, nil
}

func opCallValue(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	v, _ := uint256.FromBig(scope.Contract.value)
	scope.Stack.push(v)
	return nil, nil
}

func opCallDataLoad(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	x := scope.Stack.peek()
	if offset, overflow := x.Uint64WithOverflow(); !overflow {
		data := getData(scope.Contract.Input, offset, 32)
		x.SetBytes(data)
	} else {
		x.Clear()
	}
	return nil, nil
}

func opCallDataSize(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetUint64(uint64(len(scope.Contract.Input))))
	return nil, nil
}

func opCallDataCopy(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	var (
		memOffset  = scope.Stack.pop()
		dataOffset = scope.Stack.pop()
		length     = scope.Stack.pop()
	)
	dataOffset64, overflow := dataOffset.Uint64WithOverflow()
	if overflow {
		dataOffset64 = 0xffffffffffffffff
	}
	// These values are checked for overflow during gas cost calculation
	memOffset64 := memOffset.Uint64()
	length64 := length.Uint64()
	scope.Memory.Set(memOffset64, length64, getData(scope.Contract.Input, dataOffset64, length64))

	return nil, nil
}

func opReturnDataSize(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetUint64(uint64(len(interpreter.returnData))))
	return nil, nil
}

func opReturnDataCopy(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	var (
		memOffset  = scope.Stack.pop()
		dataOffset = scope.Stack.pop()
		length     = scope.Stack.pop()
	)

	offset64, overflow := dataOffset.Uint64WithOverflow()
	if overflow {
		return nil, ErrReturnDataOutOfBounds
	}
	// we can reuse dataOffset now (aliasing it for clarity)
	var end = dataOffset
	end.Add(&dataOffset, &length)
	end64, overflow := end.Uint64WithOverflow()
	if overflow || uint64(len(interpreter.returnData)) < end64 {
		return nil, ErrReturnDataOutOfBounds
	}
	scope.Memory.Set(memOffset.Uint64(), length.Uint64(), interpreter.returnData[offset64:end64])
	return nil, nil
}

func opExtCodeSize(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	slot := scope.Stack.peek()
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(slot.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	slot.SetUint64(uint64(interpreter.evm.StateDB.GetCodeSize(slot.Bytes20())))
	return nil, nil
}

func opCodeSize(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	l := new(uint256.Int)
	l.SetUint64(uint64(len(scope.Contract.Code)))
	scope.Stack.push(l)
	return nil, nil
}

func opCodeCopy(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	var (
		memOffset  = scope.Stack.pop()
		codeOffset = scope.Stack.pop()
		length     = scope.Stack.pop()
	)
	uint64CodeOffset, overflow := codeOffset.Uint64WithOverflow()
	if overflow {
		uint64CodeOffset = 0xffffffffffffffff
	}
	codeCopy := getData(scope.Contract.Code, uint64CodeOffset, length.Uint64())
	scope.Memory.Set(memOffset.Uint64(), length.Uint64(), codeCopy)

	return nil, nil
}

func opExtCodeCopy(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	var (
		stack      = scope.Stack
		a          = stack.pop()
		memOffset  = stack.pop()
		codeOffset = stack.pop()
		length     = stack.pop()
	)
	uint64CodeOffset, overflow := codeOffset.Uint64WithOverflow()
	if overflow {
		uint64CodeOffset = 0xffffffffffffffff
	}
	addr, err := common.Bytes20ToAddress(a.Bytes20(), interpreter.evm.chainConfig.Location).InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(addr.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	codeCopy := getData(interpreter.evm.StateDB.GetCode(addr), uint64CodeOffset, length.Uint64())
	scope.Memory.Set(memOffset.Uint64(), length.Uint64(), codeCopy)

	return nil, nil
}

// opExtCodeHash returns the code hash of a specified account.
// There are several cases when the function is called, while we can relay everything
// to `state.GetCodeHash` function to ensure the correctness.
//
//	(1) Caller tries to get the code hash of a normal contract account, state
//
// should return the relative code hash and set it as the result.
//
//	(2) Caller tries to get the code hash of a non-existent account, state should
//
// return common.Hash{} and zero will be set as the result.
//
//	(3) Caller tries to get the code hash for an account without contract code,
//
// state should return emptyCodeHash(0xc5d246...) as the result.
//
//	(4) Caller tries to get the code hash of a precompiled account, the result
//
// should be zero or emptyCodeHash.
//
// It is worth noting that in order to avoid unnecessary create and clean,
// all precompile accounts on mainnet have been transferred 1 wei, so the return
// here should be emptyCodeHash.
// If the precompile account is not transferred any amount on a private or
// customized chain, the return value will be zero.
//
//	(5) Caller tries to get the code hash for an account which is marked as suicided
//
// in the current transaction, the code hash of this account should be returned.
//
//	(6) Caller tries to get the code hash for an account which is marked as deleted,
//
// this account should be regarded as a non-existent account and zero should be returned.
func opExtCodeHash(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	slot := scope.Stack.peek()
	address, err := common.Bytes20ToAddress(slot.Bytes20(), interpreter.evm.chainConfig.Location).InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(address.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	if interpreter.evm.StateDB.Empty(address) {
		slot.Clear()
	} else {
		slot.SetBytes(interpreter.evm.StateDB.GetCodeHash(address).Bytes())
	}
	return nil, nil
}

func opGasprice(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	v, _ := uint256.FromBig(interpreter.evm.GasPrice)
	scope.Stack.push(v)
	return nil, nil
}

func opBlockhash(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	num := scope.Stack.peek()
	num64, overflow := num.Uint64WithOverflow()
	if overflow {
		num.Clear()
		return nil, nil
	}
	var upper, lower uint64
	upper = interpreter.evm.Context.BlockNumber.Uint64()
	if upper < 257 {
		lower = 0
	} else {
		lower = upper - 256
	}
	if num64 >= lower && num64 < upper {
		num.SetBytes(interpreter.evm.Context.GetHash(num64).Bytes())
	} else {
		num.Clear()
	}
	return nil, nil
}

func opCoinbase(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetBytes(interpreter.evm.Context.PrimaryCoinbase.Bytes()))
	return nil, nil
}

func opTimestamp(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	v, _ := uint256.FromBig(interpreter.evm.Context.Time)
	scope.Stack.push(v)
	return nil, nil
}

func opNumber(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	v, _ := uint256.FromBig(interpreter.evm.Context.BlockNumber)
	scope.Stack.push(v)
	return nil, nil
}

func opDifficulty(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	v, _ := uint256.FromBig(interpreter.evm.Context.Difficulty)
	scope.Stack.push(v)
	return nil, nil
}

func opGasLimit(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetUint64(interpreter.evm.Context.GasLimit))
	return nil, nil
}

func opPop(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.pop()
	return nil, nil
}

func opMload(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	v := scope.Stack.peek()
	offset := int64(v.Uint64())
	v.SetBytes(scope.Memory.GetPtr(offset, 32))
	return nil, nil
}

func opMstore(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	// pop value of the stack
	mStart, val := scope.Stack.pop(), scope.Stack.pop()
	scope.Memory.Set32(mStart.Uint64(), &val)
	return nil, nil
}

func opMstore8(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	off, val := scope.Stack.pop(), scope.Stack.pop()
	scope.Memory.store[off.Uint64()] = byte(val.Uint64())
	return nil, nil
}

func opSload(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	loc := scope.Stack.peek()
	hash := common.Hash(loc.Bytes32())
	addr, err := scope.Contract.Address().InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(addr.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	val := interpreter.evm.StateDB.GetState(addr, hash)
	loc.SetBytes(val.Bytes())
	return nil, nil
}

func opSstore(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	loc := scope.Stack.pop()
	val := scope.Stack.pop()
	addr, err := scope.Contract.Address().InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(addr.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	interpreter.evm.StateDB.SetState(addr,
		loc.Bytes32(), val.Bytes32())
	return nil, nil
}

func opJump(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	pos := scope.Stack.pop()
	if !scope.Contract.validJumpdest(&pos) {
		return nil, ErrInvalidJump
	}
	*pc = pos.Uint64()
	return nil, nil
}

func opJumpi(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	pos, cond := scope.Stack.pop(), scope.Stack.pop()
	if !cond.IsZero() {
		if !scope.Contract.validJumpdest(&pos) {
			return nil, ErrInvalidJump
		}
		*pc = pos.Uint64()
	} else {
		*pc++
	}
	return nil, nil
}

func opJumpdest(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	return nil, nil
}

func opPc(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetUint64(*pc))
	return nil, nil
}

func opMsize(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetUint64(uint64(scope.Memory.Len())))
	return nil, nil
}

func opGas(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int).SetUint64(scope.Contract.Gas))
	return nil, nil
}

func opCreate(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	var (
		value        = scope.Stack.pop()
		offset, size = scope.Stack.pop(), scope.Stack.pop()
		input        = scope.Memory.GetCopy(int64(offset.Uint64()), int64(size.Uint64()))
		gas          = scope.Contract.Gas
	)
	gas -= gas / 64
	// reuse size int for stackvalue
	stackvalue := size

	scope.Contract.UseGas(gas)
	//TODO: use uint256.Int instead of converting with toBig()
	var bigVal = common.Big0
	if !value.IsZero() {
		bigVal = value.ToBig()
	}

	res, addr, returnGas, stateUsed, suberr := interpreter.evm.Create(scope.Contract, input, gas, bigVal)
	// Push item on the stack based on the returned error.
	if suberr != nil {
		stackvalue.Clear()
	} else {
		stackvalue.SetBytes(addr.Bytes())
	}
	scope.Stack.push(&stackvalue)
	scope.Contract.Gas += returnGas
	scope.Contract.StateGas += stateUsed

	if suberr == ErrExecutionReverted {
		return res, nil
	}
	return nil, nil
}

func opCreate2(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	var (
		endowment    = scope.Stack.pop()
		offset, size = scope.Stack.pop(), scope.Stack.pop()
		salt         = scope.Stack.pop()
		input        = scope.Memory.GetCopy(int64(offset.Uint64()), int64(size.Uint64()))
		gas          = scope.Contract.Gas
	)
	gas -= gas / 64
	scope.Contract.UseGas(gas)
	// reuse size int for stackvalue
	stackvalue := size
	//TODO: use uint256.Int instead of converting with toBig()
	bigEndowment := common.Big0
	if !endowment.IsZero() {
		bigEndowment = endowment.ToBig()
	}
	res, addr, returnGas, stateUsed, suberr := interpreter.evm.Create2(scope.Contract, input, gas,
		bigEndowment, &salt)
	// Push item on the stack based on the returned error.
	if suberr != nil {
		stackvalue.Clear()
	} else {
		stackvalue.SetBytes(addr.Bytes())
	}
	scope.Stack.push(&stackvalue)
	scope.Contract.Gas += returnGas
	scope.Contract.StateGas += stateUsed

	if suberr == ErrExecutionReverted {
		return res, nil
	}
	return nil, nil
}

func opCall(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	stack := scope.Stack
	// Pop gas. The actual gas in interpreter.evm.callGasTemp.
	// We can use this as a temporary value
	temp := stack.pop()
	gas := interpreter.evm.callGasTemp
	// Pop other call parameters.
	addr, value, inOffset, inSize, retOffset, retSize := stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop()
	toAddr := common.Bytes20ToAddress(addr.Bytes20(), interpreter.evm.chainConfig.Location)
	// Get the arguments from the memory.
	args := scope.Memory.GetPtr(int64(inOffset.Uint64()), int64(inSize.Uint64()))

	var bigVal = common.Big0
	//TODO: use uint256.Int instead of converting with toBig()
	// By using big0 here, we save an alloc for the most common case (non-ether-transferring contract calls),
	// but it would make more sense to extend the usage of uint256.Int
	if !value.IsZero() {
		gas += params.CallStipend
		bigVal = value.ToBig()
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(toAddr.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}

	ret, returnGas, stateGas, err := interpreter.evm.Call(scope.Contract, toAddr, args, gas, bigVal)

	if err != nil {
		temp.Clear()
	} else {
		temp.SetOne()
	}
	stack.push(&temp)
	if err == nil || err == ErrExecutionReverted {
		ret = common.CopyBytes(ret)
		scope.Memory.Set(retOffset.Uint64(), retSize.Uint64(), ret)
	} else if err == common.ErrExternalAddress {
		return nil, err
	}
	scope.Contract.Gas += returnGas
	scope.Contract.StateGas += stateGas

	return ret, nil
}

func opCallCode(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	// Pop gas. The actual gas is in interpreter.evm.callGasTemp.
	stack := scope.Stack
	// We use it as a temporary value
	temp := stack.pop()
	gas := interpreter.evm.callGasTemp
	// Pop other call parameters.
	addr, value, inOffset, inSize, retOffset, retSize := stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop()
	toAddr := common.Bytes20ToAddress(addr.Bytes20(), interpreter.evm.chainConfig.Location)
	// Get arguments from the memory.
	args := scope.Memory.GetPtr(int64(inOffset.Uint64()), int64(inSize.Uint64()))

	//TODO: use uint256.Int instead of converting with toBig()
	var bigVal = common.Big0
	if !value.IsZero() {
		gas += params.CallStipend
		bigVal = value.ToBig()
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(addr.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	ret, returnGas, err := interpreter.evm.CallCode(scope.Contract, toAddr, args, gas, bigVal)
	if err != nil {
		temp.Clear()
	} else {
		temp.SetOne()
	}
	stack.push(&temp)
	if err == nil || err == ErrExecutionReverted {
		ret = common.CopyBytes(ret)
		scope.Memory.Set(retOffset.Uint64(), retSize.Uint64(), ret)
	} else if err == common.ErrExternalAddress {
		return nil, err
	}
	scope.Contract.Gas += returnGas

	return ret, nil
}

func opDelegateCall(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	stack := scope.Stack
	// Pop gas. The actual gas is in interpreter.evm.callGasTemp.
	// We use it as a temporary value
	temp := stack.pop()
	gas := interpreter.evm.callGasTemp
	// Pop other call parameters.
	addr, inOffset, inSize, retOffset, retSize := stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop()
	toAddr := common.Bytes20ToAddress(addr.Bytes20(), interpreter.evm.chainConfig.Location)
	// Get arguments from the memory.
	args := scope.Memory.GetPtr(int64(inOffset.Uint64()), int64(inSize.Uint64()))
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(toAddr.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	ret, returnGas, err := interpreter.evm.DelegateCall(scope.Contract, toAddr, args, gas)
	if err != nil {
		temp.Clear()
	} else {
		temp.SetOne()
	}
	stack.push(&temp)
	if err == nil || err == ErrExecutionReverted {
		ret = common.CopyBytes(ret)
		scope.Memory.Set(retOffset.Uint64(), retSize.Uint64(), ret)
	} else if err == common.ErrExternalAddress {
		return nil, err
	}
	scope.Contract.Gas += returnGas

	return ret, nil
}

func opStaticCall(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	// Pop gas. The actual gas is in interpreter.evm.callGasTemp.
	stack := scope.Stack
	// We use it as a temporary value
	temp := stack.pop()
	gas := interpreter.evm.callGasTemp
	// Pop other call parameters.
	addr, inOffset, inSize, retOffset, retSize := stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop()
	toAddr := common.Bytes20ToAddress(addr.Bytes20(), interpreter.evm.chainConfig.Location)
	// Get arguments from the memory.
	args := scope.Memory.GetPtr(int64(inOffset.Uint64()), int64(inSize.Uint64()))
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(toAddr.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	ret, returnGas, err := interpreter.evm.StaticCall(scope.Contract, toAddr, args, gas)
	if err != nil {
		temp.Clear()
	} else {
		temp.SetOne()
	}
	stack.push(&temp)
	if err == nil || err == ErrExecutionReverted {
		ret = common.CopyBytes(ret)
		scope.Memory.Set(retOffset.Uint64(), retSize.Uint64(), ret)
	} else if err == common.ErrExternalAddress {
		return nil, err
	}
	scope.Contract.Gas += returnGas

	return ret, nil
}

func opReturn(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	offset, size := scope.Stack.pop(), scope.Stack.pop()
	ret := scope.Memory.GetPtr(int64(offset.Uint64()), int64(size.Uint64()))

	return ret, nil
}

func opRevert(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	offset, size := scope.Stack.pop(), scope.Stack.pop()
	ret := scope.Memory.GetPtr(int64(offset.Uint64()), int64(size.Uint64()))

	return ret, nil
}

func opStop(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	return nil, nil
}

func opSuicide(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	beneficiary := scope.Stack.pop()
	addr, err := scope.Contract.Address().InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	beneficiaryAddr, err := common.Bytes20ToAddress(beneficiary.Bytes20(), interpreter.evm.chainConfig.Location).InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(addr.Bytes20()); !addressOk { // this may be unnecessary since the contract address ('to') is always in the access list implicitly
		return nil, ErrInvalidAccessList
	}
	if addressOk := interpreter.evm.StateDB.AddressInAccessList(beneficiaryAddr.Bytes20()); !addressOk {
		return nil, ErrInvalidAccessList
	}
	balance := interpreter.evm.StateDB.GetBalance(addr)
	interpreter.evm.StateDB.AddBalance(beneficiaryAddr, balance)
	refund := new(big.Int).Mul(interpreter.evm.Context.BaseFee, new(big.Int).SetUint64(params.CallNewAccountGas(interpreter.evm.Context.QuaiStateSize)))
	interpreter.evm.StateDB.AddBalance(beneficiaryAddr, refund)
	interpreter.evm.StateDB.Suicide(addr)
	return nil, nil
}

// opETX creates an external transaction that calls a function on a contract or sends a value to an address on a different chain
// External transactions are added to the current context's cache
// opETX is intended to be called in a contract.
func opETX(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	// Pop gas. The actual gas is in interpreter.evm.callGasTemp.
	stack := scope.Stack
	// We use it as a temporary value
	temp := stack.pop() // following opCall protocol
	// Pop other call parameters.
	addr, value, etxGasLimit, gasTipCap, gasFeeCap, inOffset, inSize, accessListOffset, accessListSize := stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop(), stack.pop()
	toAddr := common.Bytes20ToAddress(addr.Bytes20(), interpreter.evm.chainConfig.Location)
	// Verify address is not in context
	// This means that a conversion cannot happen with opETX
	if common.IsInChainScope(toAddr.Bytes(), interpreter.evm.chainConfig.Location) {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x is in chain scope, but opETX was called\n", toAddr)
		return nil, nil // following opCall protocol
	}
	sender := scope.Contract.self.Address()
	internalSender, err := sender.InternalAndQuaiAddress()
	if err != nil {
		log.Global.Errorf("%x opETX error: %s\n", scope.Contract.self.Address(), err.Error())
		return nil, nil
	}

	fee := uint256.NewInt(0)
	fee.Add(&gasTipCap, &gasFeeCap)
	fee.Mul(fee, &etxGasLimit)
	total := uint256.NewInt(0)
	total.Add(&value, fee)
	// Fail if we're trying to transfer more than the available balance
	if total.Sign() == 0 || !interpreter.evm.Context.CanTransfer(interpreter.evm.StateDB, scope.Contract.self.Address(), total.ToBig()) {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x cannot transfer %d\n", scope.Contract.self.Address(), total.Uint64())
		return nil, nil
	}

	if etxGasLimit.CmpUint64(math.MaxUint64) == 1 {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x opETX error: gas limit %d is greater than maximum %d\n", scope.Contract.self.Address(), etxGasLimit, math.MaxInt64)
		return nil, nil
	}

	// Overflow not a problem here as overflow guarantees a number larger than txgas
	if etxGasLimit.Uint64() < params.TxGas {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x opETX error: gas limit %d is less than minimum %d\n", scope.Contract.self.Address(), etxGasLimit, params.TxGas)
		return nil, nil
	}

	interpreter.evm.StateDB.SubBalance(internalSender, total.ToBig())

	// Get the arguments from the memory.
	data := scope.Memory.GetPtr(int64(inOffset.Uint64()), int64(inSize.Uint64()))
	accessList := types.AccessList{}
	// Get access list from memory
	accessListBytes := scope.Memory.GetPtr(int64(accessListOffset.Uint64()), int64(accessListSize.Uint64()))
	err = rlp.DecodeBytes(accessListBytes, &accessList)
	if err != nil && accessListSize.Sign() != 0 {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x opETX error: %s\n", scope.Contract.self.Address(), err.Error())
		return nil, nil // following opCall protocol
	}

	interpreter.evm.ETXCacheLock.RLock()
	index := len(interpreter.evm.ETXCache)
	interpreter.evm.ETXCacheLock.RUnlock()
	if index > math.MaxUint16 {
		temp.Clear()
		stack.push(&temp)
		log.Global.Error("opETX overflow error: too many ETXs in cache")
		return nil, nil
	}

	// create external transaction
	etxInner := types.ExternalTx{Value: value.ToBig(), To: &toAddr, Sender: sender, OriginatingTxHash: interpreter.evm.Hash, EtxType: types.DefaultType, ETXIndex: uint16(index), Gas: etxGasLimit.Uint64(), Data: data, AccessList: accessList}
	etx := types.NewTx(&etxInner)

	// check if the etx is eligible to be sent to the to location
	if !interpreter.evm.Context.CheckIfEtxEligible(interpreter.evm.Context.EtxEligibleSlices, *etx.To().Location()) {
		log.Global.Error("opETX error: ETX is not eligible to be sent to ", etx.To())
		return nil, nil
	}
	interpreter.evm.ETXCacheLock.Lock()
	interpreter.evm.ETXCache = append(interpreter.evm.ETXCache, etx)
	interpreter.evm.ETXCacheLock.Unlock()

	temp.SetOne() // following opCall protocol
	stack.push(&temp)

	return nil, nil
}

// opConvert creates an external transaction that converts Quai to Qi
// the ETX is added to the current context's cache and must go through Prime
// opConvert is intended to be called in a contract
func opConvert(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	// Pop gas. The actual gas is in interpreter.evm.callGasTemp.
	stack := scope.Stack
	// We use it as a temporary value
	temp := stack.pop() // following opCall protocol
	// Pop other call parameters.
	addr, uint256Value, etxGasLimit := stack.pop(), stack.pop(), stack.pop()
	bigValue := uint256Value.ToBig()
	toAddr := common.Bytes20ToAddress(addr.Bytes20(), interpreter.evm.chainConfig.Location)
	// Verify address is in shard
	if !common.IsInChainScope(toAddr.Bytes(), interpreter.evm.chainConfig.Location) {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x is not in chain scope, but opConvert was called\n", toAddr)
		return nil, nil // following opCall protocol
	} else if !toAddr.IsInQiLedgerScope() {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x is not in Qi ledger scope, but opConvert was called\n", toAddr)
		return nil, nil // following opCall protocol
	} else if bigValue.Cmp(params.MinQuaiConversionAmount) < 0 {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x cannot convert less than %d\n", scope.Contract.self.Address(), params.MinQuaiConversionAmount.Uint64())
		return nil, nil // following opCall protocol
	}
	if interpreter.evm.Context.PrimeTerminusNumber < params.ControllerKickInBlock {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x opConvert error: not after controller kick in block\n", scope.Contract.self.Address())
		return nil, nil
	}
	if interpreter.evm.Context.PrimeTerminusNumber >= params.KawPowForkBlock &&
		interpreter.evm.Context.PrimeTerminusNumber < params.KawPowForkBlock+params.KQuaiChangeHoldInterval {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x opConvert error: ETX is not eligible to be sent to %x until block %d\n", scope.Contract.self.Address(), toAddr, params.KawPowForkBlock+params.KQuaiChangeHoldInterval)
		return nil, nil
	}
	sender := scope.Contract.self.Address()
	internalSender, err := sender.InternalAndQuaiAddress()
	if err != nil {
		log.Global.Errorf("%x opConvert error: %s\n", scope.Contract.self.Address(), err.Error())
		return nil, nil
	}

	fee := uint256.NewInt(0)
	gasPrice, _ := uint256.FromBig(interpreter.evm.GasPrice)
	fee.Mul(gasPrice, &etxGasLimit) // optional: add gasPrice (base fee) and gasTipCap
	total := uint256.NewInt(0)
	total.Add(&uint256Value, fee)
	// Fail if we're trying to transfer more than the available balance
	if total.Sign() == 0 || !interpreter.evm.Context.CanTransfer(interpreter.evm.StateDB, scope.Contract.self.Address(), total.ToBig()) {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x cannot transfer %d\n", scope.Contract.self.Address(), total.Uint64())
		return nil, nil
	}

	// Overflow not a problem here as overflow guarantees a number larger than txgas
	if etxGasLimit.Uint64() < params.TxGas {
		temp.Clear()
		stack.push(&temp)
		log.Global.Errorf("%x opConvert error: gas limit %d is less than minimum %d\n", scope.Contract.self.Address(), etxGasLimit, params.TxGas)
		return nil, nil
	}

	interpreter.evm.StateDB.SubBalance(internalSender, total.ToBig())

	interpreter.evm.ETXCacheLock.RLock()
	index := len(interpreter.evm.ETXCache)
	interpreter.evm.ETXCacheLock.RUnlock()
	if index > math.MaxUint16 {
		temp.Clear()
		stack.push(&temp)
		log.Global.Error("opConvert overflow error: too many ETXs in cache")
		return nil, nil
	}

	// create external transaction
	etxInner := types.ExternalTx{Value: bigValue, To: &toAddr, Sender: sender, EtxType: types.ConversionType, OriginatingTxHash: interpreter.evm.Hash, ETXIndex: uint16(index), Gas: etxGasLimit.Uint64()}
	etx := types.NewTx(&etxInner)

	interpreter.evm.ETXCacheLock.Lock()
	interpreter.evm.ETXCache = append(interpreter.evm.ETXCache, etx)
	interpreter.evm.ETXCacheLock.Unlock()

	temp.SetOne() // following opCall protocol
	stack.push(&temp)

	return nil, nil
}

// opIsAddressInternal is used to determine if an address is internal or external based on the current chain context
func opIsAddressInternal(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	addr := scope.Stack.peek()
	commonAddr := common.Bytes20ToAddress(addr.Bytes20(), interpreter.evm.chainConfig.Location)
	if common.IsInChainScope(commonAddr.Bytes(), interpreter.evm.chainConfig.Location) {
		addr.SetOne()
	} else {
		addr.Clear()
	}
	return nil, nil
}

// following functions are used by the instruction jump  table

// make log instruction function
func makeLog(size int) executionFunc {
	return func(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
		topics := make([]common.Hash, size)
		stack := scope.Stack
		mStart, mSize := stack.pop(), stack.pop()
		for i := 0; i < size; i++ {
			addr := stack.pop()
			topics[i] = addr.Bytes32()
		}

		d := scope.Memory.GetCopy(int64(mStart.Uint64()), int64(mSize.Uint64()))
		interpreter.evm.StateDB.AddLog(&types.Log{
			Address: scope.Contract.Address(),
			Topics:  topics,
			Data:    d,
			// This is a non-consensus field, but assigned here because
			// core/state doesn't know the current block number.
			BlockNumber: interpreter.evm.Context.BlockNumber.Uint64(),
		})

		return nil, nil
	}
}

// opPush1 is a specialized version of pushN
func opPush1(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	var (
		codeLen = uint64(len(scope.Contract.Code))
		integer = new(uint256.Int)
	)
	*pc += 1
	if *pc < codeLen {
		scope.Stack.push(integer.SetUint64(uint64(scope.Contract.Code[*pc])))
	} else {
		scope.Stack.push(integer.Clear())
	}
	return nil, nil
}

// opPush0 implements the PUSH0 opcode
func opPush0(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	scope.Stack.push(new(uint256.Int))
	return nil, nil
}

// opTload implements TLOAD opcode
func opTload(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	addr, err := scope.Contract.Address().InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	loc := scope.Stack.peek()
	hash := common.Hash(loc.Bytes32())
	val := interpreter.evm.StateDB.GetTransientState(addr, hash)
	loc.SetBytes(val.Bytes())
	return nil, nil
}

// opTstore implements TSTORE opcode
func opTstore(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	if interpreter.readOnly {
		return nil, ErrWriteProtection
	}
	addr, err := scope.Contract.Address().InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	loc := scope.Stack.pop()
	val := scope.Stack.pop()
	interpreter.evm.StateDB.SetTransientState(addr, loc.Bytes32(), val.Bytes32())
	return nil, nil
}

// opMcopy implements the MCOPY opcode (https://eips.ethereum.org/EIPS/eip-5656)
func opMcopy(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
	var (
		dst    = scope.Stack.pop()
		src    = scope.Stack.pop()
		length = scope.Stack.pop()
	)
	// These values are checked for overflow during memory expansion calculation
	// (the memorySize function on the opcode).
	scope.Memory.Copy(dst.Uint64(), src.Uint64(), length.Uint64())
	return nil, nil
}

// make push instruction function
func makePush(size uint64, pushByteSize int) executionFunc {
	return func(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
		codeLen := len(scope.Contract.Code)

		startMin := codeLen
		if int(*pc+1) < startMin {
			startMin = int(*pc + 1)
		}

		endMin := codeLen
		if startMin+pushByteSize < endMin {
			endMin = startMin + pushByteSize
		}

		integer := new(uint256.Int)
		scope.Stack.push(integer.SetBytes(common.RightPadBytes(
			scope.Contract.Code[startMin:endMin], pushByteSize)))

		*pc += size
		return nil, nil
	}
}

// make dup instruction function
func makeDup(size int64) executionFunc {
	return func(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
		scope.Stack.dup(int(size))
		return nil, nil
	}
}

// make swap instruction function
func makeSwap(size int64) executionFunc {
	// switch n + 1 otherwise n would be swapped with n
	size++
	return func(pc *uint64, interpreter *EVMInterpreter, scope *ScopeContext) ([]byte, error) {
		scope.Stack.swap(int(size))
		return nil, nil
	}
}
