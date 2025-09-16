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

// Package math provides integer math utilities.
package math

import (
	"fmt"
	"math/big"
)

// Various big integer limit values.
var (
	tt255     = BigPow(2, 255)
	tt256     = BigPow(2, 256)
	tt256m1   = new(big.Int).Sub(tt256, big.NewInt(1))
	tt63      = BigPow(2, 63)
	MaxBig256 = new(big.Int).Set(tt256m1)
	MaxBig63  = new(big.Int).Sub(tt63, big.NewInt(1))
	ln2       = big.NewFloat(0.6931)
	ln2e2d2   = big.NewFloat(0.24022)
	ln2e3d6   = big.NewFloat(0.055)
)

const (
	// number of bits in a big.Word
	wordBits = 32 << (uint64(^big.Word(0)) >> 63)
	// number of bytes in a big.Word
	wordBytes = wordBits / 8
)

// HexOrDecimal256 marshals big.Int as hex or decimal.
type HexOrDecimal256 big.Int

// NewHexOrDecimal256 creates a new HexOrDecimal256
func NewHexOrDecimal256(x int64) *HexOrDecimal256 {
	b := big.NewInt(x)
	h := HexOrDecimal256(*b)
	return &h
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (i *HexOrDecimal256) UnmarshalText(input []byte) error {
	bigint, ok := ParseBig256(string(input))
	if !ok {
		return fmt.Errorf("invalid hex or decimal integer %q", input)
	}
	*i = HexOrDecimal256(*bigint)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (i *HexOrDecimal256) MarshalText() ([]byte, error) {
	if i == nil {
		return []byte("0x0"), nil
	}
	return []byte(fmt.Sprintf("%#x", (*big.Int)(i))), nil
}

// Decimal256 unmarshals big.Int as a decimal string. When unmarshalling,
// it however accepts either "0x"-prefixed (hex encoded) or non-prefixed (decimal)
type Decimal256 big.Int

// NewHexOrDecimal256 creates a new Decimal256
func NewDecimal256(x int64) *Decimal256 {
	b := big.NewInt(x)
	d := Decimal256(*b)
	return &d
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (i *Decimal256) UnmarshalText(input []byte) error {
	bigint, ok := ParseBig256(string(input))
	if !ok {
		return fmt.Errorf("invalid hex or decimal integer %q", input)
	}
	*i = Decimal256(*bigint)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (i *Decimal256) MarshalText() ([]byte, error) {
	return []byte(i.String()), nil
}

// String implements Stringer.
func (i *Decimal256) String() string {
	if i == nil {
		return "0"
	}
	return fmt.Sprintf("%#d", (*big.Int)(i))
}

// ParseBig256 parses s as a 256 bit integer in decimal or hexadecimal syntax.
// Leading zeros are accepted. The empty string parses as zero.
func ParseBig256(s string) (*big.Int, bool) {
	if s == "" {
		return new(big.Int), true
	}
	var bigint *big.Int
	var ok bool
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		bigint, ok = new(big.Int).SetString(s[2:], 16)
	} else {
		bigint, ok = new(big.Int).SetString(s, 10)
	}
	if ok && bigint.BitLen() > 256 {
		bigint, ok = nil, false
	}
	return bigint, ok
}

// MustParseBig256 parses s as a 256 bit big integer and panics if the string is invalid.
func MustParseBig256(s string) *big.Int {
	v, ok := ParseBig256(s)
	if !ok {
		panic("invalid 256 bit integer: " + s)
	}
	return v
}

// BigPow returns a ** b as a big integer.
func BigPow(a, b int64) *big.Int {
	r := big.NewInt(a)
	return r.Exp(r, big.NewInt(b), nil)
}

// BigMax returns the larger of x or y.
// It returns a reference to one of the inputs (no allocation).
// If one input is nil, the other is returned. If both are nil, nil is returned.
// Callers must not mutate the returned value unless they own it.
func BigMax(x, y *big.Int) *big.Int {
	if x.Cmp(y) < 0 {
		return y
	}
	return new(big.Int).Set(x)
}

// BigMin returns the smaller of x or y.
// It returns a reference to one of the inputs (no allocation).
// If one input is nil, the other is returned. If both are nil, nil is returned.
// Callers must not mutate the returned value unless they own it.
func BigMin(x, y *big.Int) *big.Int {
	if x.Cmp(y) > 0 {
		return new(big.Int).Set(y)
	}
	return new(big.Int).Set(x)
}

// FirstBitSet returns the index of the first 1 bit in v, counting from LSB.
func FirstBitSet(v *big.Int) int {
	for i := 0; i < v.BitLen(); i++ {
		if v.Bit(i) > 0 {
			return i
		}
	}
	return v.BitLen()
}

// PaddedBigBytes encodes a big integer as a big-endian byte slice. The length
// of the slice is at least n bytes.
func PaddedBigBytes(bigint *big.Int, n int) []byte {
	if bigint.BitLen()/8 >= n {
		return bigint.Bytes()
	}
	ret := make([]byte, n)
	ReadBits(bigint, ret)
	return ret
}

// bigEndianByteAt returns the byte at position n,
// in Big-Endian encoding
// So n==0 returns the least significant byte
func bigEndianByteAt(bigint *big.Int, n int) byte {
	words := bigint.Bits()
	// Check word-bucket the byte will reside in
	i := n / wordBytes
	if i >= len(words) {
		return byte(0)
	}
	word := words[i]
	// Offset of the byte
	shift := 8 * uint(n%wordBytes)

	return byte(word >> shift)
}

// Byte returns the byte at position n,
// with the supplied padlength in Little-Endian encoding.
// n==0 returns the MSB
// Example: bigint '5', padlength 32, n=31 => 5
func Byte(bigint *big.Int, padlength, n int) byte {
	if n >= padlength {
		return byte(0)
	}
	return bigEndianByteAt(bigint, padlength-1-n)
}

// ReadBits encodes the absolute value of bigint as big-endian bytes. Callers must ensure
// that buf has enough space. If buf is too short the result will be incomplete.
func ReadBits(bigint *big.Int, buf []byte) {
	i := len(buf)
	for _, d := range bigint.Bits() {
		for j := 0; j < wordBytes && i > 0; j++ {
			i--
			buf[i] = byte(d)
			d >>= 8
		}
	}
}

// U256 encodes as a 256 bit two's complement number. This operation is destructive.
func U256(x *big.Int) *big.Int {
	return x.And(x, tt256m1)
}

// U256Bytes converts a big Int into a 256bit EVM number.
// This operation is destructive.
func U256Bytes(n *big.Int) []byte {
	return PaddedBigBytes(U256(n), 32)
}

// S256 interprets x as a two's complement number.
// x must not exceed 256 bits (the result is undefined if it does) and is not modified.
//
//	S256(0)        = 0
//	S256(1)        = 1
//	S256(2**255)   = -2**255
//	S256(2**256-1) = -1
func S256(x *big.Int) *big.Int {
	if x.Cmp(tt255) < 0 {
		return x
	}
	return new(big.Int).Sub(x, tt256)
}

// Exp implements exponentiation by squaring.
// Exp returns a newly-allocated big integer and does not change
// base or exponent. The result is truncated to 256 bits.
func Exp(base, exponent *big.Int) *big.Int {
	result := big.NewInt(1)

	for _, word := range exponent.Bits() {
		for i := 0; i < wordBits; i++ {
			if word&1 == 1 {
				U256(result.Mul(result, base))
			}
			U256(base.Mul(base, base))
			word >>= 1
		}
	}
	return result
}

func SquaredCubed(x *big.Float) (xSquared, xCubed *big.Float) {
	xSquared = new(big.Float).Mul(x, x)      // x^2
	xCubed = new(big.Float).Mul(xSquared, x) // x^3
	return xSquared, xCubed
}

// TwoToTheX computes the expression 1 + 0.6931*x + 0.24022*x^2 + 0.055*x^3
func TwoToTheX(x *big.Float) *big.Float {
	// Compute integer, decimal, and 2^floor(x) componenets
	intX64, _ := x.Int64()
	intX := new(big.Float).SetInt64(intX64)
	if intX64 < 0 || intX64 > 1023 {
		return new(big.Float).SetInt64(0)
	}
	intExp := new(big.Float).SetMantExp(new(big.Float).SetInt64(1), int(intX64))
	decX := new(big.Float).Sub(x, intX)
	xSquared, xCubed := SquaredCubed(decX)

	// Compute each term
	term1 := new(big.Float).Mul(ln2, decX)
	term2 := new(big.Float).Mul(ln2e2d2, xSquared)
	term3 := new(big.Float).Mul(ln2e3d6, xCubed)

	// Sum all terms: 1 + term1 + term2 + term3
	result := new(big.Float).Add(term1, term2)
	result = new(big.Float).Add(result, term3)
	result = new(big.Float).Add(result, big.NewFloat(1.0))

	// Multiply 2^decX * 2^floor(x)
	result = new(big.Float).Mul(result, intExp)

	return result
}

// EToTheX computes the expression 1 + x + (1/2)*x^2 + (1/6)*x^3 + (1/24)*x^4 + (1/120)*x^5 + (1/720)*x^6 + (1/5040)*x^7
func EToTheX(x *big.Float) *big.Float {
	// Set the desired precision
	prec := uint(16) // You can adjust the precision as needed

	// Initialize constants with the specified precision
	one := new(big.Float).SetPrec(prec).SetInt64(1)
	half := new(big.Float).SetPrec(prec).Quo(
		new(big.Float).SetPrec(prec).SetInt64(1),
		new(big.Float).SetPrec(prec).SetInt64(6),
	)
	oneSixth := new(big.Float).SetPrec(prec).Quo(
		new(big.Float).SetPrec(prec).SetInt64(1),
		new(big.Float).SetPrec(prec).SetInt64(6),
	)
	oneTwentyFourth := new(big.Float).SetPrec(prec).Quo(
		new(big.Float).SetPrec(prec).SetInt64(1),
		new(big.Float).SetPrec(prec).SetInt64(24),
	)
	oneOneTwentieth := new(big.Float).SetPrec(prec).Quo(
		new(big.Float).SetPrec(prec).SetInt64(1),
		new(big.Float).SetPrec(prec).SetInt64(120),
	)
	oneOneSevenTwentieth := new(big.Float).SetPrec(prec).Quo(
		new(big.Float).SetPrec(prec).SetInt64(1),
		new(big.Float).SetPrec(prec).SetInt64(720),
	)
	oneFiveThousandFourtieth := new(big.Float).SetPrec(prec).Quo(
		new(big.Float).SetPrec(prec).SetInt64(1),
		new(big.Float).SetPrec(prec).SetInt64(5040),
	)

	// Compute x^2
	x2 := new(big.Float).SetPrec(prec).Mul(x, x)

	// Compute x^3
	x3 := new(big.Float).SetPrec(prec).Mul(x2, x)

	// Compute x^4
	x4 := new(big.Float).SetPrec(prec).Mul(x3, x)

	// Compute x^5
	x5 := new(big.Float).SetPrec(prec).Mul(x4, x)

	// Compute x^6
	x6 := new(big.Float).SetPrec(prec).Mul(x5, x)

	// Compute x^7
	x7 := new(big.Float).SetPrec(prec).Mul(x6, x)

	// Compute terms
	term2 := new(big.Float).SetPrec(prec).Mul(half, x2)                     // 0.5 * x^2
	term3 := new(big.Float).SetPrec(prec).Mul(oneSixth, x3)                 // (1/6) * x^3
	term4 := new(big.Float).SetPrec(prec).Mul(oneTwentyFourth, x4)          // (1/24) * x^4
	term5 := new(big.Float).SetPrec(prec).Mul(oneOneTwentieth, x5)          // (1/120) * x^5
	term6 := new(big.Float).SetPrec(prec).Mul(oneOneSevenTwentieth, x6)     // (1/720) * x^6
	term7 := new(big.Float).SetPrec(prec).Mul(oneFiveThousandFourtieth, x7) // (1/5040) * x^7

	// Sum up the terms: result = 1 + x + term2 + term3 + term4
	result := new(big.Float).SetPrec(prec).Add(one, x) // result = 1 + x
	result.Add(result, term2)                          // result += term2
	result.Add(result, term3)                          // result += term3
	result.Add(result, term4)                          // result += term4
	result.Add(result, term5)                          // result += term5
	result.Add(result, term6)                          // result += term6
	result.Add(result, term7)                          // result += term7

	return result
}
