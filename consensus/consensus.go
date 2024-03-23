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
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
)

// ChainHeaderReader defines a small collection of methods needed to access the local
// blockchain during header verification.
type ChainHeaderReader interface {
	// Config retrieves the blockchain's chain configuration.
	Config() *params.ChainConfig

	// CurrentHeader retrieves the current header from the local chain.
	CurrentHeader() *types.Header

	// GetHeader retrieves a block header from the database by hash and number.
	GetHeader(hash common.Hash, number uint64) *types.Header

	// GetHeaderByNumber retrieves a block header from the database by number.
	GetHeaderByNumber(number uint64) *types.Header

	// GetHeaderByHash retrieves a block header from the database by its hash.
	GetHeaderByHash(hash common.Hash) *types.Header

	// GetTerminiByHash retrieves the termini for a given header hash
	GetTerminiByHash(hash common.Hash) *types.Termini

	// ProcessingState returns true for slices that are running
	ProcessingState() bool

	// ComputeEfficiencyScore returns the efficiency score computed at each prime block
	ComputeEfficiencyScore(header *types.Header) uint16

	// IsGenesisHash returns true if the given hash is the genesis block hash.
	IsGenesisHash(hash common.Hash) bool

	// UpdateEtxEligibleSlices updates the etx eligible slice for the given zone location
	UpdateEtxEligibleSlices(header *types.Header, location common.Location) common.Hash
}

// ChainReader defines a small collection of methods needed to access the local
// blockchain during header and/or uncle verification.
type ChainReader interface {
	ChainHeaderReader

	// GetBlock retrieves a block from the database by hash and number.
	GetBlock(hash common.Hash, number uint64) *types.Block
}

type GenesisReader interface {
	// IsGenesisHash returns true if the given hash is the genesis block hash.
	IsGenesisHash(hash common.Hash) bool
}

// Engine is an algorithm agnostic consensus engine.
type Engine interface {
	// Author retrieves the Quai address of the account that minted the given
	// block, which may be different from the header's coinbase if a consensus
	// engine is based on signatures.
	Author(header *types.Header) (common.Address, error)

	// IntrinsicLogS returns the logarithm of the intrinsic entropy reduction of a PoW hash
	IntrinsicLogS(powHash common.Hash) *big.Int

	// CalcOrder returns the order of the block within the hierarchy of chains
	CalcOrder(header *types.Header) (*big.Int, int, error)

	// TotalLogS returns the log of the total entropy reduction if the chain since genesis to the given header
	TotalLogS(chain GenesisReader, header *types.Header) *big.Int

	// TotalLogPhS returns the log of the total entropy reduction if the chain since genesis for a pending header
	TotalLogPhS(header *types.Header) *big.Int

	// DeltaLogS returns the log of the entropy delta for a chain since its prior coincidence
	DeltaLogS(chain GenesisReader, header *types.Header) *big.Int

	// UncledLogS returns the log of the entropy reduction by uncles referenced in the block
	UncledLogS(block *types.Block) *big.Int

	// UncledUncledSubDeltaLogS returns the log of the uncled entropy reduction  since the past coincident
	UncledSubDeltaLogS(chain GenesisReader, header *types.Header) *big.Int

	// CalcRank calculates the rank of the prime block
	CalcRank(chain GenesisReader, header *types.Header) (int, error)

	ComputePowLight(header *types.Header) (mixHash, powHash common.Hash)

	// VerifyHeader checks whether a header conforms to the consensus rules of a
	// given engine. Verifying the seal may be done optionally here, or explicitly
	// via the VerifySeal method.
	VerifyHeader(chain ChainHeaderReader, header *types.Header) error

	// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
	// concurrently. The method returns a quit channel to abort the operations and
	// a results channel to retrieve the async verifications (the order is that of
	// the input slice).
	VerifyHeaders(chain ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error)

	// VerifyUncles verifies that the given block's uncles conform to the consensus
	// rules of a given engine.
	VerifyUncles(chain ChainReader, block *types.Block) error

	// Prepare initializes the consensus fields of a block header according to the
	// rules of a particular engine. The changes are executed inline.
	Prepare(chain ChainHeaderReader, header *types.Header, parent *types.Header) error

	// Finalize runs any post-transaction state modifications (e.g. block rewards)
	// but does not assemble the block.
	//
	// Note: The block header and state database might be updated to reflect any
	// consensus rules that happen at finalization (e.g. block rewards).
	Finalize(chain ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction,
		uncles []*types.Header)

	// FinalizeAndAssemble runs any post-transaction state modifications (e.g. block
	// rewards) and assembles the final block.
	//
	// Note: The block header and state database might be updated to reflect any
	// consensus rules that happen at finalization (e.g. block rewards).
	FinalizeAndAssemble(chain ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, etxs []*types.Transaction, manifest types.BlockManifest, receipts []*types.Receipt) (*types.Block, error)

	// Seal generates a new sealing request for the given input block and pushes
	// the result into the given channel.
	//
	// Note, the method returns immediately and will send the result async. More
	// than one result may also be returned depending on the consensus algorithm.
	Seal(header *types.Header, results chan<- *types.Header, stop <-chan struct{}) error

	// CalcDifficulty is the difficulty adjustment algorithm. It returns the difficulty
	// that a new block should have.
	CalcDifficulty(chain ChainHeaderReader, parent *types.Header) *big.Int

	// IsDomCoincident returns true if this block satisfies the difficulty order
	// of a dominant chain. If this node does not have a dominant chain (i.e.
	// if this is a prime node), then the function will always return false.
	//
	// Importantly, this check does NOT mean the block is canonical in the
	// dominant chain, or even that the claimed dominant difficulty is valid.
	IsDomCoincident(chain ChainHeaderReader, header *types.Header) bool

	// VerifySeal computes the PowHash and checks if work meets the difficulty
	// requirement specified in header
	VerifySeal(header *types.Header) (common.Hash, error)

	// APIs returns the RPC APIs this consensus engine provides.
	APIs(chain ChainHeaderReader) []rpc.API

	// Close terminates any background threads maintained by the consensus engine.
	Close() error
}

func TargetToDifficulty(target *big.Int) *big.Int {
	big2e256 := new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0)) // 2^256
	return new(big.Int).Div(big2e256, target)
}

func DifficultyToTarget(difficulty *big.Int) *big.Int {
	return TargetToDifficulty(difficulty)
}

// PoW is a consensus engine based on proof-of-work.
type PoW interface {
	Engine
}
