package stratum

import (
	"encoding/hex"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
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
	// Header-only pending (body is nil) - body is retrieved from pendingBodies cache
	pending interface{}
	// Key to look up the full block body in Server.pendingBodies cache
	// This enables many-to-one mapping: many miners share one cached body
	bodyCacheKey common.Hash
}

// calculateSeedHash computes the seed hash for a given block height
// Uses go-quai's kawpow.SeedHash which uses the correct epoch length (7500)
func calculateSeedHash(epoch uint64) string {
	// kawpow.SeedHash expects block number, not epoch
	// Use epoch * epochLength + 1 to match consensus/kawpow/kawpow.go:364
	// (mathematically equivalent due to integer division, but consistent)
	blockNum := epoch*kawpow.C_epochLength + 1
	seed := kawpow.SeedHash(blockNum)
	return hex.EncodeToString(seed)
}

// calculateEpoch returns the epoch number for a given block height
// Uses go-quai's kawpow epoch length (7500 blocks)
func calculateEpoch(height uint64) uint64 {
	return height / kawpow.C_epochLength
}
