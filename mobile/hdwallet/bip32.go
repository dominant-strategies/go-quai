package hdwallet

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/dominant-strategies/go-quai/crypto"
)

// HardenedBit is the BIP32 hardened child flag.
const HardenedBit = hdkeychain.HardenedKeyStart

// HDNode represents a BIP32 hierarchical deterministic key node.
// It wraps btcutil/hdkeychain.ExtendedKey for all key derivation.
type HDNode struct {
	key  *hdkeychain.ExtendedKey
	Path string
}

// NewMasterNode derives the BIP32 master key from a 64-byte seed.
func NewMasterNode(seed []byte) (*HDNode, error) {
	// chaincfg.MainNetParams is only used for xprv/xpub version bytes
	// (serialization prefix). It does not affect key derivation.
	key, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, fmt.Errorf("failed to create master key: %w", err)
	}
	return &HDNode{key: key, Path: "m"}, nil
}

// DeriveChild derives a child node at the given index.
// If index >= HardenedBit, hardened derivation is used.
func (n *HDNode) DeriveChild(index uint32) (*HDNode, error) {
	child, err := n.key.Derive(index)
	if err != nil {
		return nil, fmt.Errorf("failed to derive child %d: %w", index, err)
	}

	childPath := n.Path
	if index >= HardenedBit {
		childPath += fmt.Sprintf("/%d'", index-HardenedBit)
	} else {
		childPath += fmt.Sprintf("/%d", index)
	}

	return &HDNode{key: child, Path: childPath}, nil
}

// DerivePath derives a node following a BIP32 path like "m/44'/994'/0'/0/5".
func (n *HDNode) DerivePath(path string) (*HDNode, error) {
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return nil, errors.New("empty path")
	}

	current := n
	start := 0
	if parts[0] == "m" {
		start = 1
	}

	for _, part := range parts[start:] {
		if part == "" {
			continue
		}

		hardened := false
		if strings.HasSuffix(part, "'") {
			hardened = true
			part = part[:len(part)-1]
		}

		idx, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return nil, fmt.Errorf("invalid path component %q: %w", part, err)
		}

		index := uint32(idx)
		if hardened {
			index += HardenedBit
		}

		current, err = current.DeriveChild(index)
		if err != nil {
			return nil, fmt.Errorf("error deriving child at %s: %w", part, err)
		}
	}

	return current, nil
}

// PrivateKey returns the ECDSA private key for this node.
func (n *HDNode) PrivateKey() (*ecdsa.PrivateKey, error) {
	btcKey, err := n.key.ECPrivKey()
	if err != nil {
		return nil, err
	}
	return btcKey.ToECDSA(), nil
}

// PublicKeyBytes returns the 33-byte compressed public key.
func (n *HDNode) PublicKeyBytes() ([]byte, error) {
	pub, err := n.key.ECPubKey()
	if err != nil {
		return nil, err
	}
	return pub.SerializeCompressed(), nil
}

// Neuter returns a copy of the node without the private key.
func (n *HDNode) Neuter() (*HDNode, error) {
	neutered, err := n.key.Neuter()
	if err != nil {
		return nil, err
	}
	return &HDNode{key: neutered, Path: n.Path}, nil
}

// AddressBytes computes the raw 20-byte Quai address: keccak256(uncompressedPub[1:])[12:]
func (n *HDNode) AddressBytes() ([]byte, error) {
	priv, err := n.key.ECPrivKey()
	if err == nil {
		pubBytes := crypto.FromECDSAPub(priv.PubKey().ToECDSA())
		if pubBytes == nil {
			return nil, errors.New("failed to serialize public key")
		}
		return crypto.Keccak256(pubBytes[1:])[12:], nil
	}
	// Fallback for neutered nodes: decompress the public key
	pub, err := n.key.ECPubKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}
	pubBytes := pub.SerializeUncompressed()
	return crypto.Keccak256(pubBytes[1:])[12:], nil
}

// Depth returns the depth of this node in the hierarchy.
func (n *HDNode) Depth() uint8 {
	return n.key.Depth()
}

// ChildIndex returns the child index of this node.
func (n *HDNode) ChildIndex() uint32 {
	return n.key.ChildIndex()
}

// ChainCode returns the 32-byte chain code.
func (n *HDNode) ChainCode() []byte {
	return n.key.ChainCode()
}

// IsPrivate returns true if this node holds a private key.
func (n *HDNode) IsPrivate() bool {
	return n.key.IsPrivate()
}
