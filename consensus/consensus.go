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
	"github.com/dominant-strategies/go-quai/core/types"
)

const (
	// staleThreshold is the maximum depth of the acceptable stale but valid solution.
	StaleThreshold = 7
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

// Engine is an algorithm agnostic consensus engine.
type Engine interface {
	// Seal generates a new sealing request for the given input block and pushes
	// the result into the given channel.
	//
	// Note, the method returns immediately and will send the result async. More
	// than one result may also be returned depending on the consensus algorithm.
	Seal(header *types.WorkObject, results chan<- *types.WorkObject, stop <-chan struct{}) error

	// ComputePowHash returns the pow hash of the workobject header
	ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error)

	ComputePowLight(header *types.WorkObjectHeader) (common.Hash, common.Hash)

	SetThreads(threads int)
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
	diffTarget := new(big.Int).Div(common.Big2e256, diff)
	workShareTarget := new(big.Int).Exp(common.Big2, big.NewInt(int64(workShareThresholdDiff)), nil)

	return workShareTarget.Mul(diffTarget, workShareTarget), nil
}
