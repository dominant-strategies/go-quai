package qiwallet

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
)

// maxGrindAttempts bounds address grinding. Landing in a specific zone and
// in Qi scope constrains 9 bits, so ~512 attempts are needed on average;
// 65536 makes failure astronomically unlikely.
const maxGrindAttempts = 65536

// QiAddressFromKey returns the Qi address a public key spends with: the
// Keccak hash of the uncompressed public key, as consensus derives it from
// TxIn public keys.
func QiAddressFromKey(pubKey *btcec.PublicKey, location common.Location) common.Address {
	return crypto.PubkeyBytesToAddress(pubKey.SerializeUncompressed(), location)
}

// InQiScope reports whether an address is spendable in the given zone's Qi
// ledger: its first byte must encode the zone and the high bit of its
// second byte must be set.
func InQiScope(addr common.Address, location common.Location) bool {
	return common.IsInChainScope(addr.Bytes(), location) && addr.IsInQiLedgerScope()
}

// GrindQiAddress derives fresh key pairs until one lands in the given
// zone's Qi address scope, returning the key, its address and the number of
// attempts used. newKey may be nil, in which case fresh random keys are
// generated; pass a custom derivation (e.g. sequential HD child keys) to
// grind deterministically from a seed.
func GrindQiAddress(location common.Location, newKey func() (*btcec.PrivateKey, error)) (*btcec.PrivateKey, common.Address, int, error) {
	if newKey == nil {
		newKey = btcec.NewPrivateKey
	}
	for attempts := 1; attempts <= maxGrindAttempts; attempts++ {
		key, err := newKey()
		if err != nil {
			return nil, common.Address{}, attempts, err
		}
		if key == nil {
			return nil, common.Address{}, attempts, errors.New("key derivation returned nil")
		}
		addr := QiAddressFromKey(key.PubKey(), location)
		if InQiScope(addr, location) {
			return key, addr, attempts, nil
		}
	}
	return nil, common.Address{}, maxGrindAttempts, fmt.Errorf("no Qi address found in %d attempts", maxGrindAttempts)
}
