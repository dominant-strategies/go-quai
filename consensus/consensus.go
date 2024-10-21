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

// Package consensus implements different Quai consensus engines.
package consensus

import (
	"errors"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/multiset"
	"github.com/dominant-strategies/go-quai/params"
)

const (
	// staleThreshold is the maximum depth of the acceptable stale but valid solution.
	StaleThreshold = 7
	MantBits       = 64
)

// Some useful constants to avoid constant memory allocs for them.
var (
	ExpDiffPeriod = big.NewInt(100000)
	Big0          = big.NewInt(0)
	Big1          = big.NewInt(1)
	Big2          = big.NewInt(2)
	Big3          = big.NewInt(3)
	Big8          = big.NewInt(8)
	Big9          = big.NewInt(9)
	Big10         = big.NewInt(10)
	Big32         = big.NewInt(32)
	BigMinus99    = big.NewInt(-99)
	Big2e256      = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0)) // 2^256
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	ErrOlderBlockTime       = errors.New("timestamp older than parent")
	ErrTooManyUncles        = errors.New("too many uncles")
	ErrDuplicateUncle       = errors.New("duplicate uncle")
	ErrUncleIsAncestor      = errors.New("uncle is ancestor")
	ErrDanglingUncle        = errors.New("uncle's parent is not ancestor")
	ErrInvalidDifficulty    = errors.New("difficulty too low")
	ErrInvalidThresholdDiff = errors.New("threshold provided is below base difficulty")
	ErrDifficultyCrossover  = errors.New("sub's difficulty exceeds dom's")
	ErrInvalidMixHash       = errors.New("invalid mixHash")
	ErrInvalidPoW           = errors.New("invalid proof-of-work")
	ErrInvalidOrder         = errors.New("invalid order")
)

// ChainHeaderReader defines a small collection of methods needed to access the local
// blockchain during header verification.
type ChainHeaderReader interface {
	// Config retrieves the blockchain's chain configuration.
	Config() *params.ChainConfig

	// CurrentHeader retrieves the current header from the local chain.
	CurrentHeader() *types.WorkObject

	// GetHeaderByNumber retrieves a block header from the database by number.
	GetHeaderByNumber(number uint64) *types.WorkObject

	// GetHeaderByHash retrieves a block header from the database by its hash.
	GetHeaderByHash(hash common.Hash) *types.WorkObject

	// GetBlockByhash retrieves a block from the database by hash.
	GetBlockByHash(hash common.Hash) *types.WorkObject

	// GetTerminiByHash retrieves the termini for a given header hash
	GetTerminiByHash(hash common.Hash) *types.Termini

	// ProcessingState returns true for slices that are running
	ProcessingState() bool

	// ComputeEfficiencyScore returns the efficiency score computed at each prime block
	ComputeEfficiencyScore(header *types.WorkObject) (uint16, error)

	// ComputeExpansionNumber returns the expansion number of the block
	ComputeExpansionNumber(parent *types.WorkObject) (uint8, error)

	// IsGenesisHash returns true if the given hash is the genesis block hash.
	IsGenesisHash(hash common.Hash) bool

	// UpdateEtxEligibleSlices updates the etx eligible slice for the given zone location
	UpdateEtxEligibleSlices(header *types.WorkObject, location common.Location) common.Hash

	// WriteAddressOutpoints writes the address outpoints to the database
	WriteAddressOutpoints(outpointsMap map[[20]byte][]*types.OutpointAndDenomination) error

	// WorkShareDistance calculates the geodesic distance between the
	// workshare and the workobject in which that workshare is included.
	WorkShareDistance(wo *types.WorkObject, ws *types.WorkObjectHeader) (*big.Int, error)
	Database() ethdb.Database

	BlockReader
}

// ChainReader defines a small collection of methods needed to access the local
// blockchain during header and/or uncle verification.
type ChainReader interface {
	ChainHeaderReader

	// GetBlock retrieves a block from the database by hash and number.
	GetWorkObject(hash common.Hash) *types.WorkObject

	// GetWorkObjectWithWorkShares retrieves a block from the database by hash
	// but only has header and workshares populated in the body
	GetWorkObjectWithWorkShares(hash common.Hash) *types.WorkObject
}

// Engine is an algorithm agnostic consensus engine.
type Engine interface {
	// Author retrieves the Quai address of the account that minted the given
	// block, which may be different from the header's coinbase if a consensus
	// engine is based on signatures.
	Author(header *types.WorkObject) (common.Address, error)

	// IntrinsicLogEntropy returns the logarithm of the intrinsic entropy reduction of a PoW hash
	IntrinsicLogEntropy(powHash common.Hash) *big.Int

	// CalcOrder returns the order of the block within the hierarchy of chains
	CalcOrder(chain BlockReader, header *types.WorkObject) (*big.Int, int, error)

	// TotalLogEntropy returns the log of the total entropy reduction if the chain since genesis to the given header
	TotalLogEntropy(chain ChainHeaderReader, header *types.WorkObject) *big.Int

	// DeltaLogEntropy returns the log of the entropy delta for a chain since its prior coincidence
	DeltaLogEntropy(chain ChainHeaderReader, header *types.WorkObject) *big.Int

	// UncledLogEntropy returns the log of the entropy reduction by uncles referenced in the block
	UncledLogEntropy(block *types.WorkObject) *big.Int

	// WorkShareLogEntropy returns the log of the entropy reduction by the workshare referenced in the block
	WorkShareLogEntropy(chain ChainHeaderReader, block *types.WorkObject) (*big.Int, error)

	// CheckIfValidWorkShare checks if the workshare meets the work share
	// requirements defined by the protocol
	CheckIfValidWorkShare(workShare *types.WorkObjectHeader) types.WorkShareValidity

	// UncledDeltaLogEntropy returns the log of the uncled entropy reduction  since the past coincident
	UncledDeltaLogEntropy(chain ChainHeaderReader, header *types.WorkObject) *big.Int

	// CalcRank calculates the rank of the prime block
	CalcRank(chain ChainHeaderReader, header *types.WorkObject) (int, error)

	ComputePowLight(header *types.WorkObjectHeader) (mixHash, powHash common.Hash)

	// VerifyHeader checks whether a header conforms to the consensus rules of a
	// given engine. Verifying the seal may be done optionally here, or explicitly
	// via the VerifySeal method.
	VerifyHeader(chain ChainHeaderReader, header *types.WorkObject) error

	// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
	// concurrently. The method returns a quit channel to abort the operations and
	// a results channel to retrieve the async verifications (the order is that of
	// the input slice).
	VerifyHeaders(chain ChainHeaderReader, headers []*types.WorkObject) (chan<- struct{}, <-chan error)

	// VerifyUncles verifies that the given block's uncles conform to the consensus
	// rules of a given engine.
	VerifyUncles(chain ChainReader, wo *types.WorkObject) error

	// Prepare initializes the consensus fields of a block header according to the
	// rules of a particular engine. The changes are executed inline.
	Prepare(chain ChainHeaderReader, header *types.WorkObject, parent *types.WorkObject) error

	// Finalize runs any post-transaction state modifications (e.g. block rewards)
	// but does not assemble the block.
	//
	// Note: The block header and state database might be updated to reflect any
	// consensus rules that happen at finalization (e.g. block rewards).
	Finalize(chain ChainHeaderReader, batch ethdb.Batch, header *types.WorkObject, state *state.StateDB, setRoots bool, parentUtxoSetSize uint64, utxosCreate, utxosDelete []common.Hash) (*multiset.MultiSet, uint64, error)

	// FinalizeAndAssemble runs any post-transaction state modifications (e.g. block
	// rewards) and assembles the final block.
	//
	// Note: The block header and state database might be updated to reflect any
	// consensus rules that happen at finalization (e.g. block rewards).
	FinalizeAndAssemble(chain ChainHeaderReader, woHeader *types.WorkObject, state *state.StateDB, txs []*types.Transaction, uncles []*types.WorkObjectHeader, etxs []*types.Transaction, subManifest types.BlockManifest, receipts []*types.Receipt, parentUtxoSetSize uint64, utxosCreate, utxosDelete []common.Hash) (*types.WorkObject, error)

	// Seal generates a new sealing request for the given input block and pushes
	// the result into the given channel.
	//
	// Note, the method returns immediately and will send the result async. More
	// than one result may also be returned depending on the consensus algorithm.
	Seal(header *types.WorkObject, results chan<- *types.WorkObject, stop <-chan struct{}) error

	// CalcDifficulty is the difficulty adjustment algorithm. It returns the difficulty
	// that a new block should have.
	CalcDifficulty(chain ChainHeaderReader, parent *types.WorkObjectHeader, expansionNum uint8) *big.Int

	// ComputePowHash returns the pow hash of the workobject header
	ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error)

	// IsDomCoincident returns true if this block satisfies the difficulty order
	// of a dominant chain. If this node does not have a dominant chain (i.e.
	// if this is a prime node), then the function will always return false.
	//
	// Importantly, this check does NOT mean the block is canonical in the
	// dominant chain, or even that the claimed dominant difficulty is valid.
	IsDomCoincident(chain ChainHeaderReader, header *types.WorkObject) bool

	// VerifySeal computes the PowHash and checks if work meets the difficulty
	// requirement specified in header
	VerifySeal(header *types.WorkObjectHeader) (common.Hash, error)

	// VerifyWorkThreshold checks if the work meets the difficulty requirement
	CheckWorkThreshold(workObjectHeader *types.WorkObjectHeader, workShareThreshold int) bool

	SetThreads(threads int)

	// Mine is the actual proof-of-work miner that searches for a nonce starting from
	// seed that results in correct final block difficulty.
	Mine(workObject *types.WorkObject, abort <-chan struct{}, found chan *types.WorkObject)

	// MineToThreshold allows for customization of the difficulty threshold.
	MineToThreshold(workObject *types.WorkObject, threshold int, abort <-chan struct{}, found chan *types.WorkObject)
}

type BlockReader interface {
	GetBlockByHash(hash common.Hash) *types.WorkObject
	CheckInCalcOrderCache(common.Hash) (*big.Int, int, bool)
	AddToCalcOrderCache(common.Hash, int, *big.Int)
}

func TargetToDifficulty(target *big.Int) *big.Int {
	return new(big.Int).Div(common.Big2e256, target)
}

func DifficultyToTarget(difficulty *big.Int) *big.Int {
	return TargetToDifficulty(difficulty)
}

// CalcWorkShareThreshold lowers the difficulty of the workShare header by thresholdDiff bits.
// workShareTarget := 2^256 / workShare.Difficulty() * 2^workShareThresholdDiff
func CalcWorkShareThreshold(workShare *types.WorkObjectHeader, workShareThresholdDiff int) (*big.Int, error) {
	if workShareThresholdDiff <= 0 {
		// If workShareThresholdDiff = 0, you should use the difficulty directly from the header.
		return nil, ErrInvalidThresholdDiff
	}
	diff := workShare.Difficulty()
	diffTarget := new(big.Int).Div(Big2e256, diff)
	workShareTarget := new(big.Int).Exp(Big2, big.NewInt(int64(workShareThresholdDiff)), nil)

	return workShareTarget.Mul(diffTarget, workShareTarget), nil
}

// PoW is a consensus engine based on proof-of-work.
type PoW interface {
	Engine
}
