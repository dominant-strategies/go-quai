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

package common

import (
	"math/big"

	"modernc.org/mathutil"
)

const (
	MantBits = 64
)

// Common big integers often used
var (
	Big0     = big.NewInt(0)
	Big1     = big.NewInt(1)
	Big2     = big.NewInt(2)
	Big3     = big.NewInt(3)
	Big8     = big.NewInt(8)
	Big32    = big.NewInt(32)
	Big256   = big.NewInt(256)
	Big257   = big.NewInt(257)
	Big2e256 = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0))
	Big2e64  = new(big.Int).Exp(big.NewInt(2), big.NewInt(64), big.NewInt(0))
)

func BigBitsToBits(original *big.Int) *big.Int {
	return big.NewInt(0).Div(original, Big2e64)
}

func BigBitsToBitsFloat(original *big.Int) *big.Float {
	return new(big.Float).Quo(new(big.Float).SetInt(original), new(big.Float).SetInt(Big2e64))
}

func BitsToBigBits(original *big.Int) *big.Int {
	c, m := mathutil.BinaryLog(original, 64)
	bigBits := new(big.Int).Mul(big.NewInt(int64(c)), new(big.Int).Exp(big.NewInt(2), big.NewInt(64), nil))
	bigBits = new(big.Int).Add(bigBits, m)
	return bigBits
}

func BigBitsArrayToBitsArray(original []*big.Int) []*big.Int {
	bitsArray := make([]*big.Int, len(original))
	for i, bits := range original {
		bitsArray[i] = big.NewInt(0).Div(bits, Big2e64)
	}

	return bitsArray
}

func EntropyBigBitsToDifficultyBits(bigBits *big.Int) *big.Int {
	twopowerBits := new(big.Int).Exp(big.NewInt(2), new(big.Int).Div(bigBits, Big2e64), nil)
	return new(big.Int).Div(Big2e256, twopowerBits)
}

// IntrinsicLogEntropy returns the logarithm of the intrinsic entropy reduction of a PoW hash
func LogBig(diff *big.Int) *big.Int {
	diffCopy := new(big.Int).Set(diff)
	c, m := mathutil.BinaryLog(diffCopy, MantBits)
	bigBits := new(big.Int).Mul(big.NewInt(int64(c)), new(big.Int).Exp(big.NewInt(2), big.NewInt(MantBits), nil))
	bigBits = new(big.Int).Add(bigBits, m)
	return bigBits
}
