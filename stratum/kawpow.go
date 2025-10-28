package stratum

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"

	"github.com/dominant-strategies/go-quai/consensus/kawpow"
)

// KawpowDiff1 is the reference target for stratum difficulty 1.
// From kawpow-stratum-pool: 0x00000000ff000000000000000000000000000000000000000000000000000000
// This means stratum difficulty 1 corresponds to ~2^32 hashes (similar to Bitcoin).
// Used only when miner specifies custom difficulty via password field.
var KawpowDiff1 *big.Int

func init() {
	KawpowDiff1 = new(big.Int)
	KawpowDiff1.SetString("00000000ff000000000000000000000000000000000000000000000000000000", 16)
}

// kawpowJob holds kawpow-specific job data
type kawpowJob struct {
	id         string
	headerHash string // 32-byte header hash (without nonce/mixhash)
	seedHash   string // 32-byte seed hash for DAG epoch
	target     string // 32-byte target (256-bit)
	height     uint64 // block height - critical for DAG calculation
	bits       uint32 // nBits compact difficulty
	// Store pending work object for submission
	pending interface{}
}

// calculateSeedHash computes the seed hash for a given block height
// Uses go-quai's kawpow.SeedHash which uses the correct epoch length (7500)
func calculateSeedHash(epoch uint64) string {
	// kawpow.SeedHash expects block number, not epoch
	// epoch * epochLength gives us a block in that epoch
	blockNum := epoch * kawpow.C_epochLength
	seed := kawpow.SeedHash(blockNum)
	return hex.EncodeToString(seed)
}

// calculateEpoch returns the epoch number for a given block height
// Uses go-quai's kawpow epoch length (7500 blocks)
func calculateEpoch(height uint64) uint64 {
	return height / kawpow.C_epochLength
}

// difficultyToTarget converts a big.Int difficulty to a 256-bit target hex string
// target = 2^256 / difficulty
func difficultyToTarget(difficulty *big.Int) string {
	if difficulty == nil || difficulty.Sign() <= 0 {
		// Return max target if difficulty is invalid
		return "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	}

	// target = 2^256 / difficulty
	maxTarget := new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil)
	target := new(big.Int).Div(maxTarget, difficulty)

	// Convert to 32-byte hex string (64 chars), zero-padded
	targetBytes := target.Bytes()
	result := make([]byte, 32)
	copy(result[32-len(targetBytes):], targetBytes)

	return hex.EncodeToString(result)
}

// targetToDifficulty converts a 256-bit target back to difficulty
func targetToDifficulty(targetHex string) *big.Int {
	targetBytes, err := hex.DecodeString(targetHex)
	if err != nil || len(targetBytes) == 0 {
		return big.NewInt(1)
	}

	target := new(big.Int).SetBytes(targetBytes)
	if target.Sign() <= 0 {
		return big.NewInt(1)
	}

	maxTarget := new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil)
	return new(big.Int).Div(maxTarget, target)
}

// reverseBytes reverses a byte slice
func reverseBytes(b []byte) []byte {
	result := make([]byte, len(b))
	for i := range b {
		result[i] = b[len(b)-1-i]
	}
	return result
}

// uint64ToLEBytes converts uint64 to little-endian bytes
func uint64ToLEBytes(n uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, n)
	return b
}

// uint32ToLEBytes converts uint32 to little-endian bytes
func uint32ToLEBytes(n uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, n)
	return b
}

// meetsTarget checks if a hash meets the target (hash <= target)
func meetsTarget(hashHex, targetHex string) bool {
	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	targetBytes, err := hex.DecodeString(targetHex)
	if err != nil {
		return false
	}

	hashInt := new(big.Int).SetBytes(hashBytes)
	targetInt := new(big.Int).SetBytes(targetBytes)

	return hashInt.Cmp(targetInt) <= 0
}
