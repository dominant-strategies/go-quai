package qichannel

import (
	"errors"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
)

// Curve constants shared with the Quai-side channel contract.
var (
	// secp256k1GX is the x coordinate of the generator. The generator's y
	// coordinate is even, which is why recovery against it uses v = 27.
	secp256k1GX, _ = new(big.Int).SetString("79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798", 16)
	// secp256k1N is the group order.
	secp256k1N, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
)

// ErrBadScalar is returned for a scalar outside [1, n-1].
var ErrBadScalar = errors.New("qichannel: scalar is outside the valid range")

// PointAddress returns the 20-byte address commitment to a payment point:
// the last 20 bytes of keccak256(T.x || T.y).
//
// This is the form a payment point takes on the Quai ledger. The channel
// contract cannot afford to store or manipulate curve points directly, but it
// can obtain exactly this value from the ecrecover precompile for about 3000
// gas, so a Qi payment point and its Quai counterpart are the same condition
// expressed two ways. That is what allows a single routed payment to cross
// between the two ledgers.
func PointAddress(point *btcec.PublicKey) common.AddressBytes {
	var addr common.AddressBytes
	if point == nil {
		return addr
	}
	// Skip the 0x04 prefix of the uncompressed encoding, matching how an
	// address is derived from a public key.
	hashed := crypto.Keccak256(point.SerializeUncompressed()[1:])
	copy(addr[:], hashed[12:])
	return addr
}

// SecretPointAddress returns the point address a secret unlocks, which is
// what a payee publishes and the contract stores.
func SecretPointAddress(secret *PaymentSecret) (common.AddressBytes, error) {
	if secret == nil || secret.IsZero() {
		return common.AddressBytes{}, ErrBadScalar
	}
	var point btcec.JacobianPoint
	btcec.ScalarBaseMultNonConst(secret, &point)
	point.ToAffine()
	return PointAddress(btcec.NewPublicKey(&point.X, &point.Y)), nil
}

// ContractRecoveryArgs returns the (hash, v, r, s) arguments the Quai-side
// contract passes to ecrecover in order to compute the point address of a
// scalar, i.e. what `pointAddress(secret)` evaluates in Solidity.
//
// ecrecover(h, v, r, s) recovers r^-1 * (s*R - h*G), where R is the curve
// point with x coordinate r and parity from v. Taking h = 0, r = Gx and
// s = secret*Gx mod n makes R the generator and collapses the expression to
// secret*G, so the precompile performs a scalar multiplication.
//
// This function exists so the Go side can verify, against the same curve
// implementation the EVM precompile uses, that the contract's constants and
// derivation are correct.
func ContractRecoveryArgs(secret *PaymentSecret) (hash [32]byte, v byte, r, s *big.Int, err error) {
	if secret == nil || secret.IsZero() {
		return hash, 0, nil, nil, ErrBadScalar
	}
	scalarBytes := secret.Bytes()
	scalar := new(big.Int).SetBytes(scalarBytes[:])
	if scalar.Sign() == 0 || scalar.Cmp(secp256k1N) >= 0 {
		return hash, 0, nil, nil, ErrBadScalar
	}
	r = new(big.Int).Set(secp256k1GX)
	s = new(big.Int).Mod(new(big.Int).Mul(scalar, secp256k1GX), secp256k1N)
	if s.Sign() == 0 {
		return hash, 0, nil, nil, ErrBadScalar
	}
	// v = 27 in Solidity terms; the Go recovery API uses 0-based recovery ids.
	return hash, 0, r, s, nil
}
