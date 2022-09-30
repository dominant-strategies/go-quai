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

import "math/big"

// Common big integers often used
var (
	BigMinus99 = big.NewInt(-99)
	Big0       = big.NewInt(0)
	Big1       = big.NewInt(1)
	Big2       = big.NewInt(2)
	Big3       = big.NewInt(3)
	Big4       = big.NewInt(4)
	Big7       = big.NewInt(7)
	Big8       = big.NewInt(8)
	Big10      = big.NewInt(10)
	Big16      = big.NewInt(16)
	Big20      = big.NewInt(20)
	Big32      = big.NewInt(32)
	Big64      = big.NewInt(64)
	Big96      = big.NewInt(96)
	Big480     = big.NewInt(480)
	Big1024    = big.NewInt(1024)
	Big3072    = big.NewInt(3072)
	Big199680  = big.NewInt(199680)
	Big2e256   = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0)) // 2^256
)
