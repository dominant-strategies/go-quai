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

type (
	executionFunc func(pc *uint64, interpreter *EVMInterpreter, callContext *ScopeContext) ([]byte, error)
	// memorySizeFunc returns the required size, and whether the operation overflowed a uint64
	memorySizeFunc func(*Stack) (size uint64, overflow bool)
)

type operation struct {
	// execute is the operation function
	execute executionFunc
	// minStack tells how many stack items are required
	minStack int
	// maxStack specifies the max length the stack can have for this operation
	// to not overflow the stack.
	maxStack int

	// memorySize returns the memory size required for the operation
	memorySize memorySizeFunc

	halts   bool // indicates whether the operation should halt further execution
	jumps   bool // indicates whether the program counter should not increment
	writes  bool // determines whether this a state modifying operation
	reverts bool // determines whether the operation reverts state (implicitly halts)
	returns bool // determines whether the operations sets the return data content
}

var (
	instructionSet = NewInstructionSet()
)

// JumpTable contains the EVM opcodes supported at a given fork.
type JumpTable [256]*operation

// NewInstructionSet returns all instructions.
func NewInstructionSet() JumpTable {
	instructionSet := newInstructionSet()
	instructionSet[DELEGATECALL] = &operation{
		execute:    opDelegateCall,
		minStack:   minStack(6, 1),
		maxStack:   maxStack(6, 1),
		memorySize: memoryDelegateCall,
		returns:    true,
	}
	instructionSet[STATICCALL] = &operation{
		execute:    opStaticCall,
		minStack:   minStack(6, 1),
		maxStack:   maxStack(6, 1),
		memorySize: memoryStaticCall,
		returns:    true,
	}
	instructionSet[RETURNDATASIZE] = &operation{
		execute:  opReturnDataSize,
		minStack: minStack(0, 1),
		maxStack: maxStack(0, 1),
	}
	instructionSet[RETURNDATACOPY] = &operation{
		execute:    opReturnDataCopy,
		minStack:   minStack(3, 0),
		maxStack:   maxStack(3, 0),
		memorySize: memoryReturnDataCopy,
	}
	instructionSet[REVERT] = &operation{
		execute:    opRevert,
		minStack:   minStack(2, 0),
		maxStack:   maxStack(2, 0),
		memorySize: memoryRevert,
		reverts:    true,
		returns:    true,
	}
	instructionSet[SHL] = &operation{
		execute:  opSHL,
		minStack: minStack(2, 1),
		maxStack: maxStack(2, 1),
	}
	instructionSet[SHR] = &operation{
		execute:  opSHR,
		minStack: minStack(2, 1),
		maxStack: maxStack(2, 1),
	}
	instructionSet[SAR] = &operation{
		execute:  opSAR,
		minStack: minStack(2, 1),
		maxStack: maxStack(2, 1),
	}
	instructionSet[EXTCODEHASH] = &operation{
		execute:  opExtCodeHash,
		minStack: minStack(1, 1),
		maxStack: maxStack(1, 1),
	}
	instructionSet[CREATE2] = &operation{
		execute:    opCreate2,
		minStack:   minStack(4, 1),
		maxStack:   maxStack(4, 1),
		memorySize: memoryCreate2,
		writes:     true,
		returns:    true,
	}
	instructionSet[CHAINID] = &operation{
		execute:  opChainID,
		minStack: minStack(0, 1),
		maxStack: maxStack(0, 1),
	}
	instructionSet[SELFBALANCE] = &operation{
		execute:  opSelfBalance,
		minStack: minStack(0, 1),
		maxStack: maxStack(0, 1),
	}
	return instructionSet
}

func newInstructionSet() JumpTable {
	return JumpTable{
		STOP: {
			execute:  opStop,
			minStack: minStack(0, 0),
			maxStack: maxStack(0, 0),
			halts:    true,
		},
		ADD: {
			execute:  opAdd,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		MUL: {
			execute:  opMul,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		SUB: {
			execute:  opSub,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		DIV: {
			execute:  opDiv,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		SDIV: {
			execute:  opSdiv,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		MOD: {
			execute:  opMod,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		SMOD: {
			execute:  opSmod,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		ADDMOD: {
			execute:  opAddmod,
			minStack: minStack(3, 1),
			maxStack: maxStack(3, 1),
		},
		MULMOD: {
			execute:  opMulmod,
			minStack: minStack(3, 1),
			maxStack: maxStack(3, 1),
		},
		EXP: {
			execute:  opExp,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		SIGNEXTEND: {
			execute:  opSignExtend,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		LT: {
			execute:  opLt,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		GT: {
			execute:  opGt,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		SLT: {
			execute:  opSlt,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		SGT: {
			execute:  opSgt,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		EQ: {
			execute:  opEq,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		ISZERO: {
			execute:  opIszero,
			minStack: minStack(1, 1),
			maxStack: maxStack(1, 1),
		},
		AND: {
			execute:  opAnd,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		XOR: {
			execute:  opXor,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		OR: {
			execute:  opOr,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		NOT: {
			execute:  opNot,
			minStack: minStack(1, 1),
			maxStack: maxStack(1, 1),
		},
		BYTE: {
			execute:  opByte,
			minStack: minStack(2, 1),
			maxStack: maxStack(2, 1),
		},
		SHA3: {
			execute:    opSha3,
			minStack:   minStack(2, 1),
			maxStack:   maxStack(2, 1),
			memorySize: memorySha3,
		},
		ADDRESS: {
			execute:  opAddress,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		BALANCE: {
			execute:  opBalance,
			minStack: minStack(1, 1),
			maxStack: maxStack(1, 1),
		},
		ORIGIN: {
			execute:  opOrigin,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		CALLER: {
			execute:  opCaller,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		CALLVALUE: {
			execute:  opCallValue,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		CALLDATALOAD: {
			execute:  opCallDataLoad,
			minStack: minStack(1, 1),
			maxStack: maxStack(1, 1),
		},
		CALLDATASIZE: {
			execute:  opCallDataSize,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		CALLDATACOPY: {
			execute:    opCallDataCopy,
			minStack:   minStack(3, 0),
			maxStack:   maxStack(3, 0),
			memorySize: memoryCallDataCopy,
		},
		CODESIZE: {
			execute:  opCodeSize,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		CODECOPY: {
			execute:    opCodeCopy,
			minStack:   minStack(3, 0),
			maxStack:   maxStack(3, 0),
			memorySize: memoryCodeCopy,
		},
		EXTCODESIZE: {
			execute:  opExtCodeSize,
			minStack: minStack(1, 1),
			maxStack: maxStack(1, 1),
		},
		EXTCODECOPY: {
			execute:    opExtCodeCopy,
			minStack:   minStack(4, 0),
			maxStack:   maxStack(4, 0),
			memorySize: memoryExtCodeCopy,
		},
		BLOCKHASH: {
			execute:  opBlockhash,
			minStack: minStack(1, 1),
			maxStack: maxStack(1, 1),
		},
		COINBASE: {
			execute:  opCoinbase,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		TIMESTAMP: {
			execute:  opTimestamp,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		NUMBER: {
			execute:  opNumber,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		DIFFICULTY: {
			execute:  opDifficulty,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		GASLIMIT: {
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		POP: {
			execute:  opPop,
			minStack: minStack(1, 0),
			maxStack: maxStack(1, 0),
		},
		MLOAD: {
			execute:    opMload,
			minStack:   minStack(1, 1),
			maxStack:   maxStack(1, 1),
			memorySize: memoryMLoad,
		},
		MSTORE: {
			execute:    opMstore,
			minStack:   minStack(2, 0),
			maxStack:   maxStack(2, 0),
			memorySize: memoryMStore,
		},
		MSTORE8: {
			execute:    opMstore8,
			memorySize: memoryMStore8,
			minStack:   minStack(2, 0),
			maxStack:   maxStack(2, 0),
		},
		SLOAD: {
			execute:  opSload,
			minStack: minStack(1, 1),
			maxStack: maxStack(1, 1),
		},
		SSTORE: {
			execute:  opSstore,
			minStack: minStack(2, 0),
			maxStack: maxStack(2, 0),
			writes:   true,
		},
		JUMP: {
			execute:  opJump,
			minStack: minStack(1, 0),
			maxStack: maxStack(1, 0),
			jumps:    true,
		},
		JUMPI: {
			execute:  opJumpi,
			minStack: minStack(2, 0),
			maxStack: maxStack(2, 0),
			jumps:    true,
		},
		PC: {
			execute:  opPc,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		MSIZE: {
			execute:  opMsize,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		GAS: {
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		JUMPDEST: {
			execute:  opJumpdest,
			minStack: minStack(0, 0),
			maxStack: maxStack(0, 0),
		},
		PUSH1: {
			execute:  opPush1,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH2: {
			execute:  makePush(2, 2),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH3: {
			execute:  makePush(3, 3),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH4: {
			execute:  makePush(4, 4),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH5: {
			execute:  makePush(5, 5),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH6: {
			execute:  makePush(6, 6),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH7: {
			execute:  makePush(7, 7),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH8: {
			execute:  makePush(8, 8),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH9: {
			execute:  makePush(9, 9),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH10: {
			execute:  makePush(10, 10),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH11: {
			execute:  makePush(11, 11),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH12: {
			execute:  makePush(12, 12),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH13: {
			execute:  makePush(13, 13),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH14: {
			execute:  makePush(14, 14),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH15: {
			execute:  makePush(15, 15),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH16: {
			execute:  makePush(16, 16),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH17: {
			execute:  makePush(17, 17),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH18: {
			execute:  makePush(18, 18),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH19: {
			execute:  makePush(19, 19),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH20: {
			execute:  makePush(20, 20),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH21: {
			execute:  makePush(21, 21),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH22: {
			execute:  makePush(22, 22),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH23: {
			execute:  makePush(23, 23),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH24: {
			execute:  makePush(24, 24),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH25: {
			execute:  makePush(25, 25),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH26: {
			execute:  makePush(26, 26),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH27: {
			execute:  makePush(27, 27),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH28: {
			execute:  makePush(28, 28),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH29: {
			execute:  makePush(29, 29),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH30: {
			execute:  makePush(30, 30),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH31: {
			execute:  makePush(31, 31),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		PUSH32: {
			execute:  makePush(32, 32),
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		DUP1: {
			execute:  makeDup(1),
			minStack: minDupStack(1),
			maxStack: maxDupStack(1),
		},
		DUP2: {
			execute:  makeDup(2),
			minStack: minDupStack(2),
			maxStack: maxDupStack(2),
		},
		DUP3: {
			execute:  makeDup(3),
			minStack: minDupStack(3),
			maxStack: maxDupStack(3),
		},
		DUP4: {
			execute:  makeDup(4),
			minStack: minDupStack(4),
			maxStack: maxDupStack(4),
		},
		DUP5: {
			execute:  makeDup(5),
			minStack: minDupStack(5),
			maxStack: maxDupStack(5),
		},
		DUP6: {
			execute:  makeDup(6),
			minStack: minDupStack(6),
			maxStack: maxDupStack(6),
		},
		DUP7: {
			execute:  makeDup(7),
			minStack: minDupStack(7),
			maxStack: maxDupStack(7),
		},
		DUP8: {
			execute:  makeDup(8),
			minStack: minDupStack(8),
			maxStack: maxDupStack(8),
		},
		DUP9: {
			execute:  makeDup(9),
			minStack: minDupStack(9),
			maxStack: maxDupStack(9),
		},
		DUP10: {
			execute:  makeDup(10),
			minStack: minDupStack(10),
			maxStack: maxDupStack(10),
		},
		DUP11: {
			execute:  makeDup(11),
			minStack: minDupStack(11),
			maxStack: maxDupStack(11),
		},
		DUP12: {
			execute:  makeDup(12),
			minStack: minDupStack(12),
			maxStack: maxDupStack(12),
		},
		DUP13: {
			execute:  makeDup(13),
			minStack: minDupStack(13),
			maxStack: maxDupStack(13),
		},
		DUP14: {
			execute:  makeDup(14),
			minStack: minDupStack(14),
			maxStack: maxDupStack(14),
		},
		DUP15: {
			execute:  makeDup(15),
			minStack: minDupStack(15),
			maxStack: maxDupStack(15),
		},
		DUP16: {
			execute:  makeDup(16),
			minStack: minDupStack(16),
			maxStack: maxDupStack(16),
		},
		SWAP1: {
			execute:  makeSwap(1),
			minStack: minSwapStack(2),
			maxStack: maxSwapStack(2),
		},
		SWAP2: {
			execute:  makeSwap(2),
			minStack: minSwapStack(3),
			maxStack: maxSwapStack(3),
		},
		SWAP3: {
			execute:  makeSwap(3),
			minStack: minSwapStack(4),
			maxStack: maxSwapStack(4),
		},
		SWAP4: {
			execute:  makeSwap(4),
			minStack: minSwapStack(5),
			maxStack: maxSwapStack(5),
		},
		SWAP5: {
			execute:  makeSwap(5),
			minStack: minSwapStack(6),
			maxStack: maxSwapStack(6),
		},
		SWAP6: {
			execute:  makeSwap(6),
			minStack: minSwapStack(7),
			maxStack: maxSwapStack(7),
		},
		SWAP7: {
			execute:  makeSwap(7),
			minStack: minSwapStack(8),
			maxStack: maxSwapStack(8),
		},
		SWAP8: {
			execute:  makeSwap(8),
			minStack: minSwapStack(9),
			maxStack: maxSwapStack(9),
		},
		SWAP9: {
			execute:  makeSwap(9),
			minStack: minSwapStack(10),
			maxStack: maxSwapStack(10),
		},
		SWAP10: {
			execute:  makeSwap(10),
			minStack: minSwapStack(11),
			maxStack: maxSwapStack(11),
		},
		SWAP11: {
			execute:  makeSwap(11),
			minStack: minSwapStack(12),
			maxStack: maxSwapStack(12),
		},
		SWAP12: {
			execute:  makeSwap(12),
			minStack: minSwapStack(13),
			maxStack: maxSwapStack(13),
		},
		SWAP13: {
			execute:  makeSwap(13),
			minStack: minSwapStack(14),
			maxStack: maxSwapStack(14),
		},
		SWAP14: {
			execute:  makeSwap(14),
			minStack: minSwapStack(15),
			maxStack: maxSwapStack(15),
		},
		SWAP15: {
			execute:  makeSwap(15),
			minStack: minSwapStack(16),
			maxStack: maxSwapStack(16),
		},
		SWAP16: {
			execute:  makeSwap(16),
			minStack: minSwapStack(17),
			maxStack: maxSwapStack(17),
		},
		LOG0: {
			execute:    makeLog(0),
			minStack:   minStack(2, 0),
			maxStack:   maxStack(2, 0),
			memorySize: memoryLog,
			writes:     true,
		},
		LOG1: {
			execute:    makeLog(1),
			minStack:   minStack(3, 0),
			maxStack:   maxStack(3, 0),
			memorySize: memoryLog,
			writes:     true,
		},
		LOG2: {
			execute:    makeLog(2),
			minStack:   minStack(4, 0),
			maxStack:   maxStack(4, 0),
			memorySize: memoryLog,
			writes:     true,
		},
		LOG3: {
			execute:    makeLog(3),
			minStack:   minStack(5, 0),
			maxStack:   maxStack(5, 0),
			memorySize: memoryLog,
			writes:     true,
		},
		LOG4: {
			execute:    makeLog(4),
			minStack:   minStack(6, 0),
			maxStack:   maxStack(6, 0),
			memorySize: memoryLog,
			writes:     true,
		},
		CREATE: {
			execute:    opCreate,
			minStack:   minStack(3, 1),
			maxStack:   maxStack(3, 1),
			memorySize: memoryCreate,
			writes:     true,
			returns:    true,
		},
		CALL: {
			execute:    opCall,
			minStack:   minStack(7, 1),
			maxStack:   maxStack(7, 1),
			memorySize: memoryCall,
			returns:    true,
		},
		CALLCODE: {
			execute:    opCallCode,
			minStack:   minStack(7, 1),
			maxStack:   maxStack(7, 1),
			memorySize: memoryCall,
			returns:    true,
		},
		RETURN: {
			execute:    opReturn,
			minStack:   minStack(2, 0),
			maxStack:   maxStack(2, 0),
			memorySize: memoryReturn,
			halts:      true,
		},
		SELFDESTRUCT: {
			execute:  opSuicide,
			minStack: minStack(1, 0),
			maxStack: maxStack(1, 0),
			halts:    true,
			writes:   true,
		},
		BASEFEE: {
			execute:  opBaseFee,
			minStack: minStack(0, 1),
			maxStack: maxStack(0, 1),
		},
		ETX: {
			execute:    opETX,
			minStack:   minStack(10, 1),
			maxStack:   maxStack(10, 1),
			memorySize: memoryETX,
			writes:     true,
		},
		ISADDRINTERNAL: {
			execute:  opIsAddressInternal,
			minStack: minStack(1, 1),
			maxStack: maxStack(1, 1),
		},
	}
}
