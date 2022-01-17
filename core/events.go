// Copyright 2014 The go-ethereum Authors
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
	"math/big"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/core/types"
)

var (
	ContextDepth = 3
)

// NewTxsEvent is posted when a batch of transactions enter the transaction pool.
type NewTxsEvent struct{ Txs []*types.Transaction }

// NewMinedBlockEvent is posted when a block has been imported.
type NewMinedBlockEvent struct{ Block *types.Block }

// RemovedLogsEvent is posted when a reorg happens
type RemovedLogsEvent struct{ Logs []*types.Log }

type ChainEvent struct {
	Block *types.Block
	Hash  common.Hash
	Logs  []*types.Log
}

type ReOrgRollup struct {
	ReOrgHeader     *types.Header
	OldChainHeaders []*types.Header
	NewChainHeaders []*types.Header
}
type ChainSideEvent struct {
	Block *types.Block
}

type ChainHeadEvent struct{ Block *types.Block }

// CopyHeader creates a deep copy of a block header to prevent side effects from
// modifying a header variable.
func CopyHeader(h *types.Header) *types.Header {
	cpy := *h
	for i := 0; i < ContextDepth; i++ {
		if len(h.Difficulty) > i && h.Difficulty[i] != nil {
			cpy.Difficulty[i].Set(h.Difficulty[i])
		}
		if len(h.NetworkDifficulty) > i && h.NetworkDifficulty[i] != nil {
			cpy.NetworkDifficulty[i].Set(h.NetworkDifficulty[i])
		}
		if len(h.Number) > i && h.Number[i] != nil {
			cpy.Number[i].Set(h.Number[i])
		}
		if len(h.BaseFee) > i && h.BaseFee != nil && h.BaseFee[i] != nil {
			cpy.BaseFee[i] = new(big.Int).Set(h.BaseFee[i])
		}
		if len(h.Extra) > i {
			if len(h.Extra[i]) > 0 {
				cpy.Extra[i] = make([]byte, len(h.Extra[i]))
				copy(cpy.Extra[i], h.Extra[i])
			}
		}
	}
	return &cpy
}
