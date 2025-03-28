package vm

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/holiman/uint256"
)

// This tests opMcopy
func TestOpMCopy(t *testing.T) {
	// Test cases from https://eips.ethereum.org/EIPS/eip-5656#test-cases
	for i, tc := range []struct {
		dst, src, len string
		pre           string
		want          string
		wantGas       uint64
	}{
		{ // MCOPY 0 32 32 - copy 32 bytes from offset 32 to offset 0.
			dst: "0x0", src: "0x20", len: "0x20",
			pre:     "0000000000000000000000000000000000000000000000000000000000000000 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			want:    "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			wantGas: 6,
		},

		{ // MCOPY 0 0 32 - copy 32 bytes from offset 0 to offset 0.
			dst: "0x0", src: "0x0", len: "0x20",
			pre:     "0101010101010101010101010101010101010101010101010101010101010101",
			want:    "0101010101010101010101010101010101010101010101010101010101010101",
			wantGas: 6,
		},
		{ // MCOPY 0 1 8 - copy 8 bytes from offset 1 to offset 0 (overlapping).
			dst: "0x0", src: "0x1", len: "0x8",
			pre:     "000102030405060708 000000000000000000000000000000000000000000000000",
			want:    "010203040506070808 000000000000000000000000000000000000000000000000",
			wantGas: 6,
		},
		{ // MCOPY 1 0 8 - copy 8 bytes from offset 0 to offset 1 (overlapping).
			dst: "0x1", src: "0x0", len: "0x8",
			pre:     "000102030405060708 000000000000000000000000000000000000000000000000",
			want:    "000001020304050607 000000000000000000000000000000000000000000000000",
			wantGas: 6,
		},
		// Tests below are not in the EIP, but maybe should be added
		{ // MCOPY 0xFFFFFFFFFFFF 0xFFFFFFFFFFFF 0 - copy zero bytes from out-of-bounds index(overlapping).
			dst: "0xFFFFFFFFFFFF", src: "0xFFFFFFFFFFFF", len: "0x0",
			pre:     "11",
			want:    "11",
			wantGas: 3,
		},
		{ // MCOPY 0xFFFFFFFFFFFF 0 0 - copy zero bytes from start of mem to out-of-bounds.
			dst: "0xFFFFFFFFFFFF", src: "0x0", len: "0x0",
			pre:     "11",
			want:    "11",
			wantGas: 3,
		},
		{ // MCOPY 0 0xFFFFFFFFFFFF 0 - copy zero bytes from out-of-bounds to start of mem
			dst: "0x0", src: "0xFFFFFFFFFFFF", len: "0x0",
			pre:     "11",
			want:    "11",
			wantGas: 3,
		},
		{ // MCOPY - copy 1 from space outside of uint64  space
			dst: "0x0", src: "0x10000000000000000", len: "0x1",
			pre: "0",
		},
		{ // MCOPY - copy 1 from 0 to space outside of uint64
			dst: "0x10000000000000000", src: "0x0", len: "0x1",
			pre: "0",
		},
		{ // MCOPY - copy nothing from 0 to space outside of uint64
			dst: "0x10000000000000000", src: "0x0", len: "0x0",
			pre:     "",
			want:    "",
			wantGas: 3,
		},
		{ // MCOPY - copy 1 from 0x20 to 0x10, with no prior allocated mem
			dst: "0x10", src: "0x20", len: "0x1",
			pre: "",
			// 64 bytes
			want:    "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
			wantGas: 12,
		},
		{ // MCOPY - copy 1 from 0x19 to 0x10, with no prior allocated mem
			dst: "0x10", src: "0x19", len: "0x1",
			pre: "",
			// 32 bytes
			want:    "0x0000000000000000000000000000000000000000000000000000000000000000",
			wantGas: 9,
		},
	} {
		var (
			env            = NewEVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{}, nil)
			stack          = newstack()
			pc             = uint64(0)
			evmInterpreter = env.interpreter
		)
		data := common.FromHex(strings.ReplaceAll(tc.pre, " ", ""))
		// Set pre
		mem := NewMemory()
		mem.Resize(uint64(len(data)))
		mem.Set(0, uint64(len(data)), data)
		// Push stack args
		len, _ := uint256.FromHex(tc.len)
		src, _ := uint256.FromHex(tc.src)
		dst, _ := uint256.FromHex(tc.dst)

		stack.push(len)
		stack.push(src)
		stack.push(dst)
		wantErr := (tc.wantGas == 0)
		// Calc mem expansion
		var memorySize uint64
		if memSize, overflow := memoryMcopy(stack); overflow {
			if wantErr {
				continue
			}
			t.Errorf("overflow")
		} else {
			var overflow bool
			if memorySize, overflow = math.SafeMul(toWordSize(memSize), 32); overflow {
				t.Error(ErrGasUintOverflow)
			}
		}
		// and the dynamic cost
		var haveGas uint64
		if dynamicCost, _, err := gasMcopy(env, nil, stack, mem, memorySize); err != nil {
			t.Error(err)
		} else {
			haveGas = GasFastestStep + dynamicCost
		}
		// Expand mem
		if memorySize > 0 {
			mem.Resize(memorySize)
		}
		// Do the copy
		opMcopy(&pc, evmInterpreter, &ScopeContext{mem, stack, nil})
		want := common.FromHex(strings.ReplaceAll(tc.want, " ", ""))
		if have := mem.store; !bytes.Equal(want, have) {
			t.Errorf("case %d: \nwant: %#x\nhave: %#x\n", i, want, have)
		}
		wantGas := tc.wantGas
		if haveGas != wantGas {
			t.Errorf("case %d: gas wrong, want %d have %d\n", i, wantGas, haveGas)
		}
	}
}

// This tests memory.Copy
func TestMemoryCopy(t *testing.T) {
	// Test cases from https://eips.ethereum.org/EIPS/eip-5656#test-cases
	for i, tc := range []struct {
		dst, src, len uint64
		pre           string
		want          string
	}{
		{ // MCOPY 0 32 32 - copy 32 bytes from offset 32 to offset 0.
			0, 32, 32,
			"0000000000000000000000000000000000000000000000000000000000000000 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		},

		{ // MCOPY 0 0 32 - copy 32 bytes from offset 0 to offset 0.
			0, 0, 32,
			"0101010101010101010101010101010101010101010101010101010101010101",
			"0101010101010101010101010101010101010101010101010101010101010101",
		},
		{ // MCOPY 0 1 8 - copy 8 bytes from offset 1 to offset 0 (overlapping).
			0, 1, 8,
			"000102030405060708 000000000000000000000000000000000000000000000000",
			"010203040506070808 000000000000000000000000000000000000000000000000",
		},
		{ // MCOPY 1 0 8 - copy 8 bytes from offset 0 to offset 1 (overlapping).
			1, 0, 8,
			"000102030405060708 000000000000000000000000000000000000000000000000",
			"000001020304050607 000000000000000000000000000000000000000000000000",
		},
		// Tests below are not in the EIP, but maybe should be added
		{ // MCOPY 0xFFFFFFFFFFFF 0xFFFFFFFFFFFF 0 - copy zero bytes from out-of-bounds index(overlapping).
			0xFFFFFFFFFFFF, 0xFFFFFFFFFFFF, 0,
			"11",
			"11",
		},
		{ // MCOPY 0xFFFFFFFFFFFF 0 0 - copy zero bytes from start of mem to out-of-bounds.
			0xFFFFFFFFFFFF, 0, 0,
			"11",
			"11",
		},
		{ // MCOPY 0 0xFFFFFFFFFFFF 0 - copy zero bytes from out-of-bounds to start of mem
			0, 0xFFFFFFFFFFFF, 0,
			"11",
			"11",
		},
	} {
		m := NewMemory()
		// Clean spaces
		data := common.FromHex(strings.ReplaceAll(tc.pre, " ", ""))
		// Set pre
		m.Resize(uint64(len(data)))
		m.Set(0, uint64(len(data)), data)
		// Do the copy
		m.Copy(tc.dst, tc.src, tc.len)
		want := common.FromHex(strings.ReplaceAll(tc.want, " ", ""))
		if have := m.store; !bytes.Equal(want, have) {
			t.Errorf("case %d: want: %#x\nhave: %#x\n", i, want, have)
		}
	}
}

func TestOpTstore(t *testing.T) {
	var (
		statedb, _     = state.New(common.Hash{}, common.Hash{}, new(big.Int), state.NewDatabase(rawdb.NewMemoryDatabase(log.Global)), state.NewDatabase(rawdb.NewMemoryDatabase(log.Global)), nil, common.Location{0, 0}, log.Global)
		env            = NewEVM(BlockContext{}, TxContext{}, statedb, params.TestChainConfig, Config{}, nil)
		stack          = newstack()
		mem            = NewMemory()
		evmInterpreter = NewEVMInterpreter(env, env.Config)
		caller         = common.ZeroInternal(common.Location{0, 0})
		to             = common.OneInternal(common.Location{0, 0})
		contract       = NewContract(AccountRef(common.ZeroAddress(common.Location{0, 0})), AccountRef(common.BytesToAddress(to.Bytes(), common.Location{0, 0})), new(big.Int), 0)
		scopeContext   = ScopeContext{mem, stack, contract}
		value          = common.Hex2Bytes("abcdef00000000000000abba000000000deaf000000c0de00100000000133700")
	)

	// Add a stateObject for the caller and the contract being called
	statedb.CreateAccount(caller)
	statedb.CreateAccount(to)

	env.interpreter = evmInterpreter
	pc := uint64(0)
	// push the value to the stack
	stack.push(new(uint256.Int).SetBytes(value))
	// push the location to the stack
	stack.push(new(uint256.Int))
	opTstore(&pc, evmInterpreter, &scopeContext)
	// there should be no elements on the stack after TSTORE
	if stack.len() != 0 {
		t.Fatal("stack wrong size")
	}
	// push the location to the stack
	stack.push(new(uint256.Int))
	opTload(&pc, evmInterpreter, &scopeContext)
	// there should be one element on the stack after TLOAD
	if stack.len() != 1 {
		t.Fatal("stack wrong size")
	}
	val := stack.peek()
	if !bytes.Equal(val.Bytes(), value) {
		t.Fatal("incorrect element read from transient storage")
	}
}
