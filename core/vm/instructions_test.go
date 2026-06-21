package vm

import (
	"bytes"
	"crypto/sha256"
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/crypto"
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

// --- BLOCKHASH and POWHASH tests and benchmarks ---

// mockGetHash returns a simple deterministic hash for block numbers in range
func mockGetHash(n uint64) common.Hash {
	return common.BytesToHash(crypto.Keccak256(new(big.Int).SetUint64(n).Bytes()))
}

// mockGetPowHashCached simulates a cache-hit PoW hash lookup (just returns a hash)
func mockGetPowHashCached(n uint64) common.Hash {
	return common.BytesToHash(crypto.Keccak256(append([]byte("pow"), new(big.Int).SetUint64(n).Bytes()...)))
}

// mockGetPowHashCold simulates the cost of a kawpow light verification cache miss.
// KawPow light does ~64 rounds of memory-hard hashing over a 16MB cache.
// We approximate this with repeated SHA256 rounds to measure relative cost.
func mockGetPowHashCold(rounds int) func(uint64) common.Hash {
	return func(n uint64) common.Hash {
		h := crypto.Keccak256(new(big.Int).SetUint64(n).Bytes())
		for i := 0; i < rounds; i++ {
			s := sha256.Sum256(h)
			h = s[:]
		}
		return common.BytesToHash(h)
	}
}

func setupBlockhashTest(blockNumber uint64, getHash GetHashFunc, getPowHash GetHashFunc) (*EVMInterpreter, *ScopeContext) {
	env := NewEVM(BlockContext{
		BlockNumber: new(big.Int).SetUint64(blockNumber),
		GetHash:     getHash,
		GetPowHash:  getPowHash,
	}, TxContext{}, nil, params.TestChainConfig, Config{}, nil)

	stack := newstack()
	mem := NewMemory()
	evmInterpreter := NewEVMInterpreter(env, env.Config)
	env.interpreter = evmInterpreter
	contract := NewContract(
		AccountRef(common.ZeroAddress(common.Location{0, 0})),
		AccountRef(common.ZeroAddress(common.Location{0, 0})),
		new(big.Int), 100000,
	)
	scopeContext := &ScopeContext{mem, stack, contract}
	return evmInterpreter, scopeContext
}

func TestOpBlockhash(t *testing.T) {
	currentBlock := uint64(1000)
	interp, scope := setupBlockhashTest(currentBlock, mockGetHash, mockGetPowHashCached)
	pc := uint64(0)

	tests := []struct {
		name      string
		queryBlock uint64
		expectZero bool
	}{
		{"valid recent block", 999, false},
		{"valid oldest block", 744, false},   // 1000 - 256 = 744
		{"too old", 743, true},               // below lower bound
		{"current block", 1000, true},        // can't get current block hash
		{"future block", 1001, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope.Stack.push(new(uint256.Int).SetUint64(tc.queryBlock))
			opBlockhash(&pc, interp, scope)

			result := scope.Stack.pop()
			if tc.expectZero {
				if !result.IsZero() {
					t.Errorf("expected zero, got %x", result.Bytes())
				}
			} else {
				expected := mockGetHash(tc.queryBlock)
				if !bytes.Equal(result.Bytes(), expected.Bytes()) {
					t.Errorf("expected %x, got %x", expected.Bytes(), result.Bytes())
				}
			}
		})
	}
}

func TestOpPowhash(t *testing.T) {
	currentBlock := uint64(1000)
	interp, scope := setupBlockhashTest(currentBlock, mockGetHash, mockGetPowHashCached)
	pc := uint64(0)

	tests := []struct {
		name       string
		queryBlock uint64
		expectZero bool
	}{
		{"valid recent block", 999, false},
		{"valid oldest block", 744, false},
		{"too old", 743, true},
		{"current block", 1000, true},
		{"future block", 1001, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope.Stack.push(new(uint256.Int).SetUint64(tc.queryBlock))
			opPowhash(&pc, interp, scope)

			result := scope.Stack.pop()
			if tc.expectZero {
				if !result.IsZero() {
					t.Errorf("expected zero, got %x", result.Bytes())
				}
			} else {
				expected := mockGetPowHashCached(tc.queryBlock)
				if !bytes.Equal(result.Bytes(), expected.Bytes()) {
					t.Errorf("expected %x, got %x", expected.Bytes(), result.Bytes())
				}
			}
		})
	}

	// Test overflow: push a value > uint64 max
	t.Run("overflow", func(t *testing.T) {
		overflow := new(uint256.Int).SetBytes(common.Hex2Bytes("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"))
		scope.Stack.push(overflow)
		opPowhash(&pc, interp, scope)
		result := scope.Stack.pop()
		if !result.IsZero() {
			t.Errorf("expected zero for overflow, got %x", result.Bytes())
		}
	})
}

func TestOpPowhashDiffersFromBlockhash(t *testing.T) {
	currentBlock := uint64(1000)
	interp, scope := setupBlockhashTest(currentBlock, mockGetHash, mockGetPowHashCached)
	pc := uint64(0)

	// Get blockhash for block 999
	scope.Stack.push(new(uint256.Int).SetUint64(999))
	opBlockhash(&pc, interp, scope)
	blockhashResult := scope.Stack.pop()

	// Get powhash for block 999
	scope.Stack.push(new(uint256.Int).SetUint64(999))
	opPowhash(&pc, interp, scope)
	powhashResult := scope.Stack.pop()

	// They should be different (since mock functions produce different hashes)
	if bytes.Equal(blockhashResult.Bytes(), powhashResult.Bytes()) {
		t.Error("BLOCKHASH and POWHASH should return different values")
	}

	// Neither should be zero
	if blockhashResult.IsZero() {
		t.Error("BLOCKHASH should not be zero for valid block")
	}
	if powhashResult.IsZero() {
		t.Error("POWHASH should not be zero for valid block")
	}
}

// BenchmarkOpBlockhash measures the cost of the BLOCKHASH opcode with a trivial hash lookup
func BenchmarkOpBlockhash(b *testing.B) {
	currentBlock := uint64(1000)
	interp, scope := setupBlockhashTest(currentBlock, mockGetHash, mockGetPowHashCached)
	pc := uint64(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope.Stack.push(new(uint256.Int).SetUint64(999))
		opBlockhash(&pc, interp, scope)
		scope.Stack.pop()
	}
}

// BenchmarkOpPowhashCached measures POWHASH with the same mock as BenchmarkOpBlockhash
// for an apples-to-apples comparison of opcode execution overhead.
func BenchmarkOpPowhashCached(b *testing.B) {
	currentBlock := uint64(1000)
	interp, scope := setupBlockhashTest(currentBlock, mockGetHash, mockGetHash)
	pc := uint64(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope.Stack.push(new(uint256.Int).SetUint64(999))
		opPowhash(&pc, interp, scope)
		scope.Stack.pop()
	}
}

// BenchmarkOpPowhashCold_100 simulates a cache miss with 100 SHA256 rounds (~progpow light cost).
func BenchmarkOpPowhashCold_100(b *testing.B) {
	currentBlock := uint64(1000)
	interp, scope := setupBlockhashTest(currentBlock, mockGetHash, mockGetPowHashCold(100))
	pc := uint64(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope.Stack.push(new(uint256.Int).SetUint64(999))
		opPowhash(&pc, interp, scope)
		scope.Stack.pop()
	}
}

// BenchmarkOpPowhashCold_1000 simulates a heavier cache miss with 1000 SHA256 rounds.
func BenchmarkOpPowhashCold_1000(b *testing.B) {
	currentBlock := uint64(1000)
	interp, scope := setupBlockhashTest(currentBlock, mockGetHash, mockGetPowHashCold(1000))
	pc := uint64(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope.Stack.push(new(uint256.Int).SetUint64(999))
		opPowhash(&pc, interp, scope)
		scope.Stack.pop()
	}
}

// BenchmarkOpPowhashCold_10000 simulates a very heavy cache miss with 10000 SHA256 rounds.
// KawPow light verification is roughly in this ballpark.
func BenchmarkOpPowhashCold_10000(b *testing.B) {
	currentBlock := uint64(1000)
	interp, scope := setupBlockhashTest(currentBlock, mockGetHash, mockGetPowHashCold(10000))
	pc := uint64(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope.Stack.push(new(uint256.Int).SetUint64(999))
		opPowhash(&pc, interp, scope)
		scope.Stack.pop()
	}
}

// TestPowhashForkActivation verifies that POWHASH is rejected before PowHashForkHeight
// and accepted at/after PowHashForkHeight.
func TestPowhashForkActivation(t *testing.T) {
	tests := []struct {
		name        string
		blockNumber uint64
		expectError bool
	}{
		{"rejected before fork", uint64(params.PowHashForkHeight) - 1, true},
		{"accepted at fork", uint64(params.PowHashForkHeight), false},
		{"accepted after fork", uint64(params.PowHashForkHeight) + 1000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build bytecode: PUSH3 <queryBlock> POWHASH STOP
			queryBlock := tt.blockNumber - 1
			b0 := byte((queryBlock >> 16) & 0xFF)
			b1 := byte((queryBlock >> 8) & 0xFF)
			b2 := byte(queryBlock & 0xFF)
			code := []byte{
				byte(PUSH3), b0, b1, b2, // push query block number
				byte(POWHASH), // 0xf9
				byte(STOP),
			}

			env := NewEVM(BlockContext{
				BlockNumber: new(big.Int).SetUint64(tt.blockNumber),
				GetPowHash:  mockGetPowHashCached,
			}, TxContext{}, nil, params.TestChainConfig, Config{}, nil)

			evmInterpreter := NewEVMInterpreter(env, env.Config)
			env.interpreter = evmInterpreter

			contract := NewContract(
				AccountRef(common.ZeroAddress(common.Location{0, 0})),
				AccountRef(common.ZeroAddress(common.Location{0, 0})),
				new(big.Int), 10000,
			)
			contract.Code = code

			_, err := evmInterpreter.Run(contract, nil, false)

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected ErrInvalidOpCode before fork height, got nil")
				}
				if _, ok := err.(*ErrInvalidOpCode); !ok {
					t.Fatalf("expected *ErrInvalidOpCode, got %T: %v", err, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error at/after fork height, got: %v", err)
				}
			}
		})
	}
}
