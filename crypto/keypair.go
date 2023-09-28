// Copyright 2021 ChainSafe Systems (ON)
// SPDX-License-Identifier: LGPL-3.0-only

package crypto

import (
	bip39 "github.com/cosmos/go-bip39"
	"github.com/dominant-strategies/go-quai/common"
	"golang.org/x/crypto/blake2b"
)

// KeyType str
type KeyType = string

// Ed25519Type ed25519
const Ed25519Type KeyType = "ed25519"

// Sr25519Type sr25519
const Sr25519Type KeyType = "sr25519"

// Secp256k1Type secp256k1
const Secp256k1Type KeyType = "secp256k1"

// UnknownType is used by the GenericKeystore
const UnknownType KeyType = "unknown"

// PublicKey interface
type PublicKey interface {
	Verify(msg, sig []byte) (bool, error)
	Encode() []byte
	Decode([]byte) error
	Address() common.Address
	Hex() string
}

// PrivateKey interface
type PrivateKey interface {
	Sign(msg []byte) ([]byte, error)
	Public() (PublicKey, error)
	Encode() []byte
	Decode([]byte) error
	Hex() string
}

var ss58Prefix = []byte("SS58PRE")

const (
	// PublicKeyLength is the expected public key length for sr25519.
	PublicKeyLength = 32
	// SeedLength is the expected seed length for sr25519.
	SeedLength = 32
	// PrivateKeyLength is the expected private key length for sr25519.
	PrivateKeyLength = 32
	// SignatureLength is the expected signature length for sr25519.
	SignatureLengthEd25519 = 64
	// VRFOutputLength is the expected VFR output length for sr25519.
	VRFOutputLength = 32
	// VRFProofLength is the expected VFR proof length for sr25519.
	VRFProofLength = 64
)

// PublicKeyToAddress returns an ss58 address given a PublicKey
// see: https://github.com/paritytech/substrate/wiki/External-Address-Format-(SS58)
// also see: https://github.com/paritytech/substrate/blob/master/primitives/core/src/crypto.rs#L275
func PublicKeyToAddress(pub PublicKey) common.Address {
	enc := append([]byte{42}, pub.Encode()...)
	return publicKeyBytesToAddress(enc)
}

func publicKeyBytesToAddress(b []byte) common.Address {
	hasher, err := blake2b.New(64, nil)
	if err != nil {
		return common.Address{}
	}
	_, err = hasher.Write(append(ss58Prefix, b...))
	if err != nil {
		return common.Address{}
	}
	checksum := hasher.Sum(nil)
	return common.BytesToAddress(append(b, checksum[:2]...))
}

// PublicAddressToByteArray returns []byte address for given PublicKey Address
func PublicAddressToByteArray(add common.Address) []byte {
	if (add == common.Address{}) {
		return nil
	}
	// k := base58.Decode(add.String())
	// return k[1:33]
	return add.Bytes()
}

// NewBIP39Mnemonic returns a new BIP39-compatible mnemonic
func NewBIP39Mnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return "", err
	}

	return bip39.NewMnemonic(entropy)
}
