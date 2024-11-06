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

package core

import (
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto/multiset"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// Validator is an interface which defines the standard for block validation. It
// is only responsible for validating block contents, as the header validation is
// done by the specific consensus engines.
type Validator interface {
	// ValidateBody validates the given block's content.
	ValidateBody(block *types.WorkObject) error

	SanityCheckWorkObjectBlockViewBody(wo *types.WorkObject) error

	SanityCheckWorkObjectHeaderViewBody(wo *types.WorkObject) error

	SanityCheckWorkObjectShareViewBody(wo *types.WorkObject) error

	ApplyPoWFilter(wo *types.WorkObject) pubsub.ValidationResult

	// ValidateState validates the given statedb and optionally the receipts and
	// gas used.
	ValidateState(block *types.WorkObject, state *state.StateDB, receipts types.Receipts, etxs types.Transactions, multiSet *multiset.MultiSet, usedGas uint64, usedState uint64) error
}

// Prefetcher is an interface for pre-caching transaction signatures and state.
type Prefetcher interface {
	// Prefetch processes the state changes according to the Quai rules by running
	// the transaction messages using the statedb, but any changes are discarded. The
	// only goal is to pre-cache transaction signatures and state trie nodes.
	Prefetch(block *types.WorkObject, statedb *state.StateDB, cfg vm.Config, interrupt *uint32)
}

// Processor is an interface for processing blocks using a given initial state.
type Processor interface {
	// Process processes the state changes according to the Quai rules by running
	// the transaction messages using the statedb and applying any rewards to both
	// the processor (coinbase) and any included uncles.
	Process(block *types.WorkObject, statedb *state.StateDB, cfg vm.Config) (types.Receipts, []*types.Log, uint64, error)
	Apply(block *types.WorkObject) error
}
