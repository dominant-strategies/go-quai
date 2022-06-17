// Copyright 2021 The go-ethereum Authors
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
	crand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	mrand "math/rand"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/common/math"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/params"
)

// ChainReader defines a small collection of methods needed to access the local
// blockchain during header verification. It's implemented by both blockchain
// and lightchain.
type ChainReader interface {
	// Config retrieves the header chain's chain configuration.
	Config() *params.ChainConfig

	// GetTd returns the total difficulty of a local block.
	GetTd(common.Hash, uint64) *big.Int

	// GetBlockByHash retrieves a block from the database by hash, caching it if found.
	GetBlockByHash(hash common.Hash) *types.Block

	// GetHierarchicalTD gets the localTD and externTD for the local header and the incoming header
	GetHierarchicalTD(currentBlock *types.Header, block *types.Header) (*big.Int, *big.Int, error)

	// PCRC
	PCRC(header *types.Header) ([]common.Hash, []*big.Int, error)

	GetTdByHash(hash common.Hash) *big.Int

	CalcHLCRNetDifficulty(terminalHashes []common.Hash, netDifficulties []*big.Int) (*big.Int, error)
}

// ForkChoice is the fork chooser based on the highest total difficulty of the
// chain(the fork choice used in the eth1) and the external fork choice (the fork
// choice used in the eth2). This main goal of this ForkChoice is not only for
// offering fork choice during the eth1/2 merge phase, but also keep the compatibility
// for all other proof-of-work networks.
type ForkChoice struct {
	chain ChainReader
	rand  *mrand.Rand

	// preserve is a helper function used in td fork choice.
	// Miners will prefer to choose the local mined block if the
	// local td is equal to the extern one. It can be nil for light
	// client
	preserve func(header *types.Header) bool
}

func NewForkChoice(chainReader ChainReader, preserve func(header *types.Header) bool) *ForkChoice {
	// Seed a fast but crypto originating random generator
	seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		log.Crit("Failed to initialize random seed", "err", err)
	}
	return &ForkChoice{
		chain:    chainReader,
		rand:     mrand.New(mrand.NewSource(seed.Int64())),
		preserve: preserve,
	}
}

// ReorgNeeded returns whether the reorg should be applied
// based on the given external header and local canonical chain.
// In the td mode, the new head is chosen if the corresponding
// total difficulty is higher. In the extern mode, the trusted
// header is always selected as the head.
func (f *ForkChoice) ReorgNeeded(current *types.Header, header *types.Header) (bool, error) {

	localTd := f.chain.GetTdByHash(current.Hash())

	fmt.Println("Running PCRC")
	// Check PCRC for the external block and return the terminal hash and net difficulties
	externTerminalHashes, externNetDifficulties, err := f.chain.PCRC(header)
	if err != nil {
		return false, err
	}

	fmt.Println("Running CalcHLCRNetDifficulty")
	// Use HLCR to compute net total difficulty
	externNetTd, err := f.chain.CalcHLCRNetDifficulty(externTerminalHashes, externNetDifficulties)
	if err != nil {
		return false, err
	}

	externTd := big.NewInt(0)
	fmt.Println("In forkchoice")
	fmt.Println("externNetTD", externNetTd)
	externTd = externNetTd.Add(externNetTd, f.chain.GetTdByHash(externTerminalHashes[0]))
	fmt.Println("externTerminalHashes[0]", f.chain.GetTdByHash(externTerminalHashes[0]))

	fmt.Println("localTD", localTd)
	fmt.Println("externTD", externTd)
	if localTd == nil || externNetTd == nil {
		return false, errors.New("missing td")
	}

	currentBlock := f.chain.GetBlockByHash(current.Hash())
	externBlock := f.chain.GetBlockByHash(header.Hash())

	// If the total difficulty is higher than our known, add it to the canonical chain
	// Second clause in the if statement reduces the vulnerability to selfish mining.
	// Please refer to http://www.cs.cornell.edu/~ie53/publications/btcProcFC.pdf
	reorg := externTd.Cmp(localTd) > 0
	if !reorg && externTd.Cmp(localTd) == 0 {
		number, headNumber := externBlock.NumberU64(), currentBlock.NumberU64()
		if number < headNumber {
			reorg = true
		} else if number == headNumber {
			var currentPreserve, externPreserve bool
			if f.preserve != nil {
				currentPreserve, externPreserve = f.preserve(current), f.preserve(header)
			}
			reorg = !currentPreserve && (externPreserve || f.rand.Float64() < 0.5)
		}
	}
	return reorg, nil
}
