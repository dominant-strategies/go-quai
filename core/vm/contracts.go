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

package vm

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/crypto/blake2b"
	"github.com/dominant-strategies/go-quai/crypto/bn256"
	"github.com/dominant-strategies/go-quai/params"

	//lint:ignore SA1019 Needed for precompile
	"golang.org/x/crypto/ripemd160"
)

// PrecompiledContract is the basic interface for native Go contracts. The implementation
// requires a deterministic gas count based on the input size of the Run method of the
// contract.
type PrecompiledContract interface {
	Run(input []byte) ([]byte, error) // Run runs the precompiled contract
}

var TranslatedAddresses = map[common.AddressBytes]int{
	common.AddressBytes([20]byte{1}): 0,
	common.AddressBytes([20]byte{2}): 1,
	common.AddressBytes([20]byte{3}): 2,
	common.AddressBytes([20]byte{4}): 3,
	common.AddressBytes([20]byte{5}): 4,
	common.AddressBytes([20]byte{6}): 5,
	common.AddressBytes([20]byte{7}): 6,
	common.AddressBytes([20]byte{8}): 7,
	common.AddressBytes([20]byte{9}): 8,
}

var (
	PrecompiledContracts map[common.AddressBytes]PrecompiledContract = make(map[common.AddressBytes]PrecompiledContract)
	PrecompiledAddresses map[string][]common.Address                 = make(map[string][]common.Address)
)

func InitializePrecompiles() {
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][0].Bytes20()] = &ecrecover{}
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][1].Bytes20()] = &sha256hash{}
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][2].Bytes20()] = &ripemd160hash{}
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][3].Bytes20()] = &dataCopy{}
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][4].Bytes20()] = &bigModExp{}
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][5].Bytes20()] = &bn256Add{}
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][6].Bytes20()] = &bn256ScalarMul{}
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][7].Bytes20()] = &bn256Pairing{}
	PrecompiledContracts[PrecompiledAddresses[common.NodeLocation.Name()][8].Bytes20()] = &blake2F{}
}

func init() {

	PrecompiledAddresses["cyprus1"] = []common.Address{
		common.HexToAddress("0x1400000000000000000000000000000000000001"),
		common.HexToAddress("0x1400000000000000000000000000000000000002"),
		common.HexToAddress("0x1400000000000000000000000000000000000003"),
		common.HexToAddress("0x1400000000000000000000000000000000000004"),
		common.HexToAddress("0x1400000000000000000000000000000000000005"),
		common.HexToAddress("0x1400000000000000000000000000000000000006"),
		common.HexToAddress("0x1400000000000000000000000000000000000007"),
		common.HexToAddress("0x1400000000000000000000000000000000000008"),
		common.HexToAddress("0x1400000000000000000000000000000000000009"),
	}
	PrecompiledAddresses["cyprus2"] = []common.Address{
		common.HexToAddress("0x2000000000000000000000000000000000000001"),
		common.HexToAddress("0x2000000000000000000000000000000000000002"),
		common.HexToAddress("0x2000000000000000000000000000000000000003"),
		common.HexToAddress("0x2000000000000000000000000000000000000004"),
		common.HexToAddress("0x2000000000000000000000000000000000000005"),
		common.HexToAddress("0x2000000000000000000000000000000000000006"),
		common.HexToAddress("0x2000000000000000000000000000000000000007"),
		common.HexToAddress("0x2000000000000000000000000000000000000008"),
		common.HexToAddress("0x2000000000000000000000000000000000000009"),
	}
	PrecompiledAddresses["cyprus3"] = []common.Address{
		common.HexToAddress("0x3E00000000000000000000000000000000000001"),
		common.HexToAddress("0x3E00000000000000000000000000000000000002"),
		common.HexToAddress("0x3E00000000000000000000000000000000000003"),
		common.HexToAddress("0x3E00000000000000000000000000000000000004"),
		common.HexToAddress("0x3E00000000000000000000000000000000000005"),
		common.HexToAddress("0x3E00000000000000000000000000000000000006"),
		common.HexToAddress("0x3E00000000000000000000000000000000000007"),
		common.HexToAddress("0x3E00000000000000000000000000000000000008"),
		common.HexToAddress("0x3E00000000000000000000000000000000000009"),
	}
	PrecompiledAddresses["paxos1"] = []common.Address{
		common.HexToAddress("0x5A00000000000000000000000000000000000001"),
		common.HexToAddress("0x5A00000000000000000000000000000000000002"),
		common.HexToAddress("0x5A00000000000000000000000000000000000003"),
		common.HexToAddress("0x5A00000000000000000000000000000000000004"),
		common.HexToAddress("0x5A00000000000000000000000000000000000005"),
		common.HexToAddress("0x5A00000000000000000000000000000000000006"),
		common.HexToAddress("0x5A00000000000000000000000000000000000007"),
		common.HexToAddress("0x5A00000000000000000000000000000000000008"),
		common.HexToAddress("0x5A00000000000000000000000000000000000009"),
	}
	PrecompiledAddresses["paxos2"] = []common.Address{
		common.HexToAddress("0x7800000000000000000000000000000000000001"),
		common.HexToAddress("0x7800000000000000000000000000000000000002"),
		common.HexToAddress("0x7800000000000000000000000000000000000003"),
		common.HexToAddress("0x7800000000000000000000000000000000000004"),
		common.HexToAddress("0x7800000000000000000000000000000000000005"),
		common.HexToAddress("0x7800000000000000000000000000000000000006"),
		common.HexToAddress("0x7800000000000000000000000000000000000007"),
		common.HexToAddress("0x7800000000000000000000000000000000000008"),
		common.HexToAddress("0x7800000000000000000000000000000000000009"),
	}
	PrecompiledAddresses["paxos3"] = []common.Address{
		common.HexToAddress("0x9600000000000000000000000000000000000001"),
		common.HexToAddress("0x9600000000000000000000000000000000000002"),
		common.HexToAddress("0x9600000000000000000000000000000000000003"),
		common.HexToAddress("0x9600000000000000000000000000000000000004"),
		common.HexToAddress("0x9600000000000000000000000000000000000005"),
		common.HexToAddress("0x9600000000000000000000000000000000000006"),
		common.HexToAddress("0x9600000000000000000000000000000000000007"),
		common.HexToAddress("0x9600000000000000000000000000000000000008"),
		common.HexToAddress("0x9600000000000000000000000000000000000009"),
	}
	PrecompiledAddresses["hydra1"] = []common.Address{
		common.HexToAddress("0xB400000000000000000000000000000000000001"),
		common.HexToAddress("0xB400000000000000000000000000000000000002"),
		common.HexToAddress("0xB400000000000000000000000000000000000003"),
		common.HexToAddress("0xB400000000000000000000000000000000000004"),
		common.HexToAddress("0xB400000000000000000000000000000000000005"),
		common.HexToAddress("0xB400000000000000000000000000000000000006"),
		common.HexToAddress("0xB400000000000000000000000000000000000007"),
		common.HexToAddress("0xB400000000000000000000000000000000000008"),
		common.HexToAddress("0xB400000000000000000000000000000000000009"),
	}
	PrecompiledAddresses["hydra2"] = []common.Address{
		common.HexToAddress("0xD200000000000000000000000000000000000001"),
		common.HexToAddress("0xD200000000000000000000000000000000000002"),
		common.HexToAddress("0xD200000000000000000000000000000000000003"),
		common.HexToAddress("0xD200000000000000000000000000000000000004"),
		common.HexToAddress("0xD200000000000000000000000000000000000005"),
		common.HexToAddress("0xD200000000000000000000000000000000000006"),
		common.HexToAddress("0xD200000000000000000000000000000000000007"),
		common.HexToAddress("0xD200000000000000000000000000000000000008"),
		common.HexToAddress("0xD200000000000000000000000000000000000009"),
	}
	PrecompiledAddresses["hydra3"] = []common.Address{
		common.HexToAddress("0xF000000000000000000000000000000000000001"),
		common.HexToAddress("0xF000000000000000000000000000000000000002"),
		common.HexToAddress("0xF000000000000000000000000000000000000003"),
		common.HexToAddress("0xF000000000000000000000000000000000000004"),
		common.HexToAddress("0xF000000000000000000000000000000000000005"),
		common.HexToAddress("0xF000000000000000000000000000000000000006"),
		common.HexToAddress("0xF000000000000000000000000000000000000007"),
		common.HexToAddress("0xF000000000000000000000000000000000000008"),
		common.HexToAddress("0xF000000000000000000000000000000000000009"),
	}
}

// ActivePrecompiles returns the precompiles enabled with the current configuration.
func ActivePrecompiles(rules params.Rules) []common.Address {
	return PrecompiledAddresses[common.NodeLocation.Name()]
}

// RunPrecompiledContract runs and evaluates the output of a precompiled contract.
// It returns
// - the returned bytes,
// - the _remaining_ gas,
// - any error that occurred
func RunPrecompiledContract(p PrecompiledContract, input []byte) (ret []byte, err error) {
	output, err := p.Run(input)
	return output, err
}

// ECRECOVER implemented as a native contract.
type ecrecover struct{}

func (c *ecrecover) Run(input []byte) ([]byte, error) {
	const ecRecoverInputLength = 128

	input = common.RightPadBytes(input, ecRecoverInputLength)
	// "input" is (hash, v, r, s), each 32 bytes
	// but for ecrecover we want (r, s, v)

	r := new(big.Int).SetBytes(input[64:96])
	s := new(big.Int).SetBytes(input[96:128])
	v := input[63] - 27

	// tighter sig s values input only apply to tx sigs
	if !allZero(input[32:63]) || !crypto.ValidateSignatureValues(v, r, s) {
		return nil, nil
	}
	// We must make sure not to modify the 'input', so placing the 'v' along with
	// the signature needs to be done on a new allocation
	sig := make([]byte, 65)
	copy(sig, input[64:128])
	sig[64] = v
	// v needs to be at the end for libsecp256k1
	pubKey, err := crypto.Ecrecover(input[:32], sig)
	// make sure the public key is a valid one
	if err != nil {
		return nil, nil
	}

	// the first byte of pubkey is bitcoin heritage
	return common.LeftPadBytes(crypto.Keccak256(pubKey[1:])[12:], 32), nil
}

// SHA256 implemented as a native contract.
type sha256hash struct{}

func (c *sha256hash) Run(input []byte) ([]byte, error) {
	h := sha256.Sum256(input)
	return h[:], nil
}

// RIPEMD160 implemented as a native contract.
type ripemd160hash struct{}

func (c *ripemd160hash) Run(input []byte) ([]byte, error) {
	ripemd := ripemd160.New()
	ripemd.Write(input)
	return common.LeftPadBytes(ripemd.Sum(nil), 32), nil
}

// data copy implemented as a native contract.
type dataCopy struct{}

func (c *dataCopy) Run(in []byte) ([]byte, error) {
	return in, nil
}

// bigModExp implements a native big integer exponential modular operation.
type bigModExp struct {
}

var (
	big0      = big.NewInt(0)
	big1      = big.NewInt(1)
	big2      = big.NewInt(2)
	big3      = big.NewInt(3)
	big4      = big.NewInt(4)
	big7      = big.NewInt(7)
	big8      = big.NewInt(8)
	big16     = big.NewInt(16)
	big20     = big.NewInt(20)
	big32     = big.NewInt(32)
	big64     = big.NewInt(64)
	big96     = big.NewInt(96)
	big480    = big.NewInt(480)
	big1024   = big.NewInt(1024)
	big3072   = big.NewInt(3072)
	big199680 = big.NewInt(199680)
)

// modexpMultComplexity implements bigModexp multComplexity formula
//
// def mult_complexity(x):
//
//	if x <= 64: return x ** 2
//	elif x <= 1024: return x ** 2 // 4 + 96 * x - 3072
//	else: return x ** 2 // 16 + 480 * x - 199680
//
// where is x is max(length_of_MODULUS, length_of_BASE)
func modexpMultComplexity(x *big.Int) *big.Int {
	switch {
	case x.Cmp(big64) <= 0:
		x.Mul(x, x) // x ** 2
	case x.Cmp(big1024) <= 0:
		// (x ** 2 // 4 ) + ( 96 * x - 3072)
		x = new(big.Int).Add(
			new(big.Int).Div(new(big.Int).Mul(x, x), big4),
			new(big.Int).Sub(new(big.Int).Mul(big96, x), big3072),
		)
	default:
		// (x ** 2 // 16) + (480 * x - 199680)
		x = new(big.Int).Add(
			new(big.Int).Div(new(big.Int).Mul(x, x), big16),
			new(big.Int).Sub(new(big.Int).Mul(big480, x), big199680),
		)
	}
	return x
}

func (c *bigModExp) Run(input []byte) ([]byte, error) {
	var (
		baseLen = new(big.Int).SetBytes(getData(input, 0, 32)).Uint64()
		expLen  = new(big.Int).SetBytes(getData(input, 32, 32)).Uint64()
		modLen  = new(big.Int).SetBytes(getData(input, 64, 32)).Uint64()
	)
	if len(input) > 96 {
		input = input[96:]
	} else {
		input = input[:0]
	}
	// Handle a special case when both the base and mod length is zero
	if baseLen == 0 && modLen == 0 {
		return []byte{}, nil
	}
	// Retrieve the operands and execute the exponentiation
	var (
		base = new(big.Int).SetBytes(getData(input, 0, baseLen))
		exp  = new(big.Int).SetBytes(getData(input, baseLen, expLen))
		mod  = new(big.Int).SetBytes(getData(input, baseLen+expLen, modLen))
	)
	if mod.BitLen() == 0 {
		// Modulo 0 is undefined, return zero
		return common.LeftPadBytes([]byte{}, int(modLen)), nil
	}
	return common.LeftPadBytes(base.Exp(base, exp, mod).Bytes(), int(modLen)), nil
}

// newCurvePoint unmarshals a binary blob into a bn256 elliptic curve point,
// returning it, or an error if the point is invalid.
func newCurvePoint(blob []byte) (*bn256.G1, error) {
	p := new(bn256.G1)
	if _, err := p.Unmarshal(blob); err != nil {
		return nil, err
	}
	return p, nil
}

// newTwistPoint unmarshals a binary blob into a bn256 elliptic curve point,
// returning it, or an error if the point is invalid.
func newTwistPoint(blob []byte) (*bn256.G2, error) {
	p := new(bn256.G2)
	if _, err := p.Unmarshal(blob); err != nil {
		return nil, err
	}
	return p, nil
}

// runBn256Add implements the Bn256Add precompile
func runBn256Add(input []byte) ([]byte, error) {
	x, err := newCurvePoint(getData(input, 0, 64))
	if err != nil {
		return nil, err
	}
	y, err := newCurvePoint(getData(input, 64, 64))
	if err != nil {
		return nil, err
	}
	res := new(bn256.G1)
	res.Add(x, y)
	return res.Marshal(), nil
}

// bn256Add implements a native elliptic curve point addition conforming to consensus rules.
type bn256Add struct{}

func (c *bn256Add) Run(input []byte) ([]byte, error) {
	return runBn256Add(input)
}

// runBn256ScalarMul implements the Bn256ScalarMul precompile
func runBn256ScalarMul(input []byte) ([]byte, error) {
	p, err := newCurvePoint(getData(input, 0, 64))
	if err != nil {
		return nil, err
	}
	res := new(bn256.G1)
	res.ScalarMult(p, new(big.Int).SetBytes(getData(input, 64, 32)))
	return res.Marshal(), nil
}

// bn256ScalarMul implements a native elliptic curve scalar
// multiplication conforming to  consensus rules.
type bn256ScalarMul struct{}

func (c *bn256ScalarMul) Run(input []byte) ([]byte, error) {
	return runBn256ScalarMul(input)
}

var (
	// true32Byte is returned if the bn256 pairing check succeeds.
	true32Byte = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

	// false32Byte is returned if the bn256 pairing check fails.
	false32Byte = make([]byte, 32)

	// errBadPairingInput is returned if the bn256 pairing input is invalid.
	errBadPairingInput = errors.New("bad elliptic curve pairing size")
)

// runBn256Pairing implements the Bn256Pairing precompile
func runBn256Pairing(input []byte) ([]byte, error) {
	// Handle some corner cases cheaply
	if len(input)%192 > 0 {
		return nil, errBadPairingInput
	}
	// Convert the input into a set of coordinates
	var (
		cs []*bn256.G1
		ts []*bn256.G2
	)
	for i := 0; i < len(input); i += 192 {
		c, err := newCurvePoint(input[i : i+64])
		if err != nil {
			return nil, err
		}
		t, err := newTwistPoint(input[i+64 : i+192])
		if err != nil {
			return nil, err
		}
		cs = append(cs, c)
		ts = append(ts, t)
	}
	// Execute the pairing checks and return the results
	if bn256.PairingCheck(cs, ts) {
		return true32Byte, nil
	}
	return false32Byte, nil
}

// bn256Pairing implements a pairing pre-compile for the bn256 curve
// conforming to  consensus rules.
type bn256Pairing struct{}

func (c *bn256Pairing) Run(input []byte) ([]byte, error) {
	return runBn256Pairing(input)
}

type blake2F struct{}

const (
	blake2FInputLength        = 213
	blake2FFinalBlockBytes    = byte(1)
	blake2FNonFinalBlockBytes = byte(0)
)

var (
	errBlake2FInvalidInputLength = errors.New("invalid input length")
	errBlake2FInvalidFinalFlag   = errors.New("invalid final flag")
)

func (c *blake2F) Run(input []byte) ([]byte, error) {
	// Make sure the input is valid (correct length and final flag)
	if len(input) != blake2FInputLength {
		return nil, errBlake2FInvalidInputLength
	}
	if input[212] != blake2FNonFinalBlockBytes && input[212] != blake2FFinalBlockBytes {
		return nil, errBlake2FInvalidFinalFlag
	}
	// Parse the input into the Blake2b call parameters
	var (
		rounds = binary.BigEndian.Uint32(input[0:4])
		final  = (input[212] == blake2FFinalBlockBytes)

		h [8]uint64
		m [16]uint64
		t [2]uint64
	)
	for i := 0; i < 8; i++ {
		offset := 4 + i*8
		h[i] = binary.LittleEndian.Uint64(input[offset : offset+8])
	}
	for i := 0; i < 16; i++ {
		offset := 68 + i*8
		m[i] = binary.LittleEndian.Uint64(input[offset : offset+8])
	}
	t[0] = binary.LittleEndian.Uint64(input[196:204])
	t[1] = binary.LittleEndian.Uint64(input[204:212])

	// Execute the compression function, extract and return the result
	blake2b.F(&h, m, t, final, rounds)

	output := make([]byte, 64)
	for i := 0; i < 8; i++ {
		offset := i * 8
		binary.LittleEndian.PutUint64(output[offset:offset+8], h[i])
	}
	return output, nil
}
