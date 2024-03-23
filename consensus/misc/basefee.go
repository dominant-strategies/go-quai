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

package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

// CalcBaseFee calculates the basefee of the header taking into account the basefee ceiling
func CalcBaseFee(config *params.ChainConfig, parent *types.WorkObject) *big.Int {
	calculatedBaseFee := calcBaseFee(config, parent)
	ceiling := big.NewInt(params.MaxBaseFee)
	if calculatedBaseFee.Cmp(ceiling) > 0 {
		return ceiling
	}
	return calculatedBaseFee
}

// calcBaseFee calculates the basefee of the header.
func calcBaseFee(config *params.ChainConfig, parent *types.WorkObject) *big.Int {
	var (
		parentGasTarget          = parent.GasLimit() / params.ElasticityMultiplier
		parentGasTargetBig       = new(big.Int).SetUint64(parentGasTarget)
		baseFeeChangeDenominator = new(big.Int).SetUint64(params.BaseFeeChangeDenominator)
	)
	// If the parent gasUsed is the same as the target, the baseFee remains unchanged.
	if parent.GasUsed() == parentGasTarget {
		return new(big.Int).Set(parent.BaseFee())
	}
	if parent.GasUsed() > parentGasTarget {
		// If the parent block used more gas than its target, the baseFee should increase.
		gasUsedDelta := new(big.Int).SetUint64(parent.GasUsed() - parentGasTarget)
		x := new(big.Int).Mul(parent.BaseFee(), gasUsedDelta)
		y := x.Div(x, parentGasTargetBig)
		baseFeeDelta := math.BigMax(x.Div(y, baseFeeChangeDenominator), big.NewInt(params.GWei))

		return x.Add(parent.BaseFee(), baseFeeDelta)
	} else {
		// Otherwise if the parent block used less gas than its target, the baseFee should decrease.
		gasUsedDelta := new(big.Int).SetUint64(parentGasTarget - parent.GasUsed())
		x := new(big.Int).Mul(parent.BaseFee(), gasUsedDelta)
		y := x.Div(x, parentGasTargetBig)
		baseFeeDelta := x.Div(y, baseFeeChangeDenominator)

		return math.BigMax(x.Sub(parent.BaseFee(), baseFeeDelta), big.NewInt(params.GWei))
	}
}
