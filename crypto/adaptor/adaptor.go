// Package adaptor implements Schnorr adaptor signatures over secp256k1, for
// both single keys and MuSig2 aggregate keys.
//
// An adaptor signature is a signature that is "missing" a secret scalar. Given
// an adaptor point T = t*G, a signer can produce a value that is not yet a
// valid signature, but which anyone can:
//
//   - verify is well formed and will become valid once t is supplied,
//   - complete into a valid signature given t (Adapt), and
//   - use, together with the completed signature, to recover t (Extract).
//
// That last property is what makes payments atomic: publishing the completed
// signature on chain necessarily reveals t to the counterparty, who can then
// complete their own adaptor signature one hop upstream. This is the basis of
// point timelocked contracts (PTLCs), the scriptless replacement for HTLCs.
//
// The signatures produced here are ordinary BIP-340 Schnorr signatures, and
// the MuSig2 variant aggregates keys exactly as consensus does
// (musig2.AggregateKeys(keys, false)), so completed signatures verify against
// the same aggregate key a Qi UTXO is addressed to.
//
// # Construction
//
// The signer commits to the shifted nonce point R' = R + T, where R is the
// nonce point it actually knows the discrete log of. The challenge is
// computed over R' as usual, so the resulting scalar is short by exactly t.
// BIP-340 signatures carry only x(R'), so verification lifts it to the
// even-Y representative; when R' has an odd Y coordinate the signer negates
// its nonce and completion subtracts t rather than adding it. The Negated
// field records which case applies.
//
// # Security
//
// Nonces must be uniformly random and never reused across signing sessions;
// reuse leaks the private key. The MuSig2 API takes nonces from the caller
// (as MuSig2 requires an interactive nonce exchange) and callers must
// generate them with musig2.GenNonces and use each secret nonce exactly once.
package adaptor

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
)

// Signature is an adaptor signature: a scalar S that becomes a valid BIP-340
// signature over the nonce point R once the discrete log of the adaptor point
// is added (or subtracted, when Negated is set).
type Signature struct {
	// R is the shifted nonce point R' = R + T that the completed signature
	// will commit to.
	R *btcec.PublicKey

	// S is the adaptor scalar: the signature scalar less the adaptor secret.
	S *btcec.ModNScalar

	// Negated reports whether R had an odd Y coordinate, in which case
	// completion subtracts the adaptor secret instead of adding it.
	Negated bool
}

var (
	ErrNilArgument     = errors.New("adaptor: nil argument")
	ErrInvalidAdaptor  = errors.New("adaptor: adaptor signature is invalid")
	ErrSecretMismatch  = errors.New("adaptor: secret does not match the adaptor point")
	ErrNoPartialSigs   = errors.New("adaptor: no partial signatures provided")
	ErrPointAtInfinity = errors.New("adaptor: computed point is at infinity")
)

// NewSecret generates a random adaptor secret t and its point T = t*G.
func NewSecret() (*btcec.ModNScalar, *btcec.PublicKey, error) {
	key, err := btcec.NewPrivateKey()
	if err != nil {
		return nil, nil, err
	}
	secret := new(btcec.ModNScalar)
	secret.Set(&key.Key)
	return secret, key.PubKey(), nil
}

// PointFromSecret returns T = t*G for an adaptor secret t.
func PointFromSecret(secret *btcec.ModNScalar) (*btcec.PublicKey, error) {
	if secret == nil {
		return nil, ErrNilArgument
	}
	if secret.IsZero() {
		return nil, ErrPointAtInfinity
	}
	var point btcec.JacobianPoint
	btcec.ScalarBaseMultNonConst(secret, &point)
	point.ToAffine()
	return btcec.NewPublicKey(&point.X, &point.Y), nil
}

// challenge computes the BIP-340 challenge e = H(x(R) || x(Q) || m).
func challenge(nonce, pubKey *btcec.PublicKey, msg [32]byte) *btcec.ModNScalar {
	var buf bytes.Buffer
	buf.Write(schnorr.SerializePubKey(nonce))
	buf.Write(schnorr.SerializePubKey(pubKey))
	buf.Write(msg[:])
	hash := chainhash.TaggedHash(musig2.ChallengeHashTag, buf.Bytes())
	e := new(btcec.ModNScalar)
	e.SetByteSlice(hash[:])
	return e
}

// isOdd reports whether a public key has an odd Y coordinate.
func isOdd(key *btcec.PublicKey) bool {
	return key.SerializeCompressed()[0] == secp256k1PubKeyFormatCompressedOdd
}

// secp256k1PubKeyFormatCompressedOdd is the compressed-point prefix for an
// odd Y coordinate.
const secp256k1PubKeyFormatCompressedOdd = 0x03

// AddPoints returns a + b. It is exported because PTLC route construction
// blinds adaptor points per hop by adding an offset point.
func AddPoints(a, b *btcec.PublicKey) (*btcec.PublicKey, error) {
	return addPoints(a, b)
}

// addPoints returns a + b.
func addPoints(a, b *btcec.PublicKey) (*btcec.PublicKey, error) {
	var aJ, bJ, sum btcec.JacobianPoint
	a.AsJacobian(&aJ)
	b.AsJacobian(&bJ)
	btcec.AddNonConst(&aJ, &bJ, &sum)
	if (sum.X.IsZero() && sum.Y.IsZero()) || sum.Z.IsZero() {
		return nil, ErrPointAtInfinity
	}
	sum.ToAffine()
	return btcec.NewPublicKey(&sum.X, &sum.Y), nil
}

// negatePoint returns -p.
func negatePoint(p *btcec.PublicKey) *btcec.PublicKey {
	var pJ btcec.JacobianPoint
	p.AsJacobian(&pJ)
	pJ.ToAffine()
	pJ.Y.Negate(1)
	pJ.Y.Normalize()
	return btcec.NewPublicKey(&pJ.X, &pJ.Y)
}

// evenY returns the even-Y representative of a point, which is what BIP-340
// verification lifts an x-only coordinate to.
func evenY(p *btcec.PublicKey) *btcec.PublicKey {
	if isOdd(p) {
		return negatePoint(p)
	}
	return p
}

// PreSign produces an adaptor signature over msg with a single private key,
// locked to the adaptor point T.
func PreSign(privKey *btcec.PrivateKey, msg [32]byte, T *btcec.PublicKey) (*Signature, error) {
	if privKey == nil || T == nil {
		return nil, ErrNilArgument
	}
	// A fresh random nonce per call. Never make this deterministic in a
	// multi-party setting; see the package security note.
	nonceKey, err := btcec.NewPrivateKey()
	if err != nil {
		return nil, err
	}
	k := new(btcec.ModNScalar)
	k.Set(&nonceKey.Key)

	shiftedNonce, err := addPoints(nonceKey.PubKey(), T)
	if err != nil {
		return nil, err
	}

	// BIP-340 verification lifts x(R') to even Y, so when R' is odd the
	// signer negates its nonce and completion subtracts the secret.
	negated := isOdd(shiftedNonce)
	if negated {
		k.Negate()
	}

	pubKey := privKey.PubKey()
	d := new(btcec.ModNScalar)
	d.Set(&privKey.Key)
	if isOdd(pubKey) {
		d.Negate()
	}

	e := challenge(shiftedNonce, pubKey, msg)
	s := new(btcec.ModNScalar)
	s.Add(k).Add(e.Mul(d))

	sig := &Signature{R: shiftedNonce, S: s, Negated: negated}
	if !sig.Verify(pubKey, msg, T) {
		return nil, ErrInvalidAdaptor
	}
	return sig, nil
}

// Verify checks that the adaptor signature is well formed for the given
// public key, message and adaptor point: that completing it with the discrete
// log of T would yield a valid BIP-340 signature. It does not require, and
// tells the verifier nothing about, the adaptor secret.
//
// For a MuSig2 adaptor signature, pubKey is the aggregate key.
func (sig *Signature) Verify(pubKey *btcec.PublicKey, msg [32]byte, T *btcec.PublicKey) bool {
	if sig == nil || sig.R == nil || sig.S == nil || pubKey == nil || T == nil {
		return false
	}
	// The completed signature satisfies s*G = R_even + e*Q_even, with
	// s = S + t (or S - t when negated), so the adaptor scalar satisfies
	//   S*G + T = R_even + e*Q_even   (not negated)
	//   S*G - T = R_even + e*Q_even   (negated)
	var sG btcec.JacobianPoint
	btcec.ScalarBaseMultNonConst(sig.S, &sG)
	sG.ToAffine()
	if sG.X.IsZero() && sG.Y.IsZero() {
		return false
	}
	left, err := addPoints(btcec.NewPublicKey(&sG.X, &sG.Y), adaptorTerm(T, sig.Negated))
	if err != nil {
		return false
	}

	e := challenge(sig.R, pubKey, msg)
	var qJ, eQ btcec.JacobianPoint
	evenY(pubKey).AsJacobian(&qJ)
	btcec.ScalarMultNonConst(e, &qJ, &eQ)
	eQ.ToAffine()
	if eQ.Z.IsZero() {
		return false
	}
	right, err := addPoints(evenY(sig.R), btcec.NewPublicKey(&eQ.X, &eQ.Y))
	if err != nil {
		return false
	}
	return left.IsEqual(right)
}

// adaptorTerm returns T or -T depending on the nonce parity.
func adaptorTerm(T *btcec.PublicKey, negated bool) *btcec.PublicKey {
	if negated {
		return negatePoint(T)
	}
	return T
}

// Adapt completes the adaptor signature with the adaptor secret, producing a
// standard BIP-340 Schnorr signature.
func (sig *Signature) Adapt(secret *btcec.ModNScalar) (*schnorr.Signature, error) {
	if sig == nil || sig.S == nil || sig.R == nil || secret == nil {
		return nil, ErrNilArgument
	}
	s := new(btcec.ModNScalar)
	s.Set(sig.S)
	if sig.Negated {
		var negated btcec.ModNScalar
		negated.Set(secret)
		negated.Negate()
		s.Add(&negated)
	} else {
		s.Add(secret)
	}
	var rX btcec.FieldVal
	rX.SetByteSlice(schnorr.SerializePubKey(sig.R))
	return schnorr.NewSignature(&rX, s), nil
}

// Extract recovers the adaptor secret from a completed signature, given the
// adaptor signature it was completed from. This is what lets a routing node
// learn the payment secret from an on-chain (or off-chain) settlement and
// claim the corresponding payment one hop upstream.
func (sig *Signature) Extract(final *schnorr.Signature) (*btcec.ModNScalar, error) {
	if sig == nil || sig.S == nil || final == nil {
		return nil, ErrNilArgument
	}
	serialized := final.Serialize()
	if len(serialized) != 64 {
		return nil, fmt.Errorf("adaptor: unexpected signature length %d", len(serialized))
	}
	finalS := new(btcec.ModNScalar)
	if overflow := finalS.SetByteSlice(serialized[32:]); overflow {
		return nil, errors.New("adaptor: signature scalar overflows the group order")
	}

	// final = S + t  =>  t = final - S       (not negated)
	// final = S - t  =>  t = S - final       (negated)
	secret := new(btcec.ModNScalar)
	if sig.Negated {
		secret.Set(sig.S)
		var negated btcec.ModNScalar
		negated.Set(finalS)
		negated.Negate()
		secret.Add(&negated)
	} else {
		secret.Set(finalS)
		var negated btcec.ModNScalar
		negated.Set(sig.S)
		negated.Negate()
		secret.Add(&negated)
	}
	if secret.IsZero() {
		return nil, ErrSecretMismatch
	}
	return secret, nil
}

// VerifySecret checks that a secret is the discrete log of an adaptor point.
func VerifySecret(secret *btcec.ModNScalar, T *btcec.PublicKey) bool {
	point, err := PointFromSecret(secret)
	if err != nil {
		return false
	}
	return point.IsEqual(T)
}

// TweakCombinedNonce shifts an aggregated MuSig2 public nonce by the adaptor
// point, by adding T to the first of its two nonce points. Every signer in
// the session must sign against this tweaked nonce; the resulting combined
// signature is then short by exactly the adaptor secret.
func TweakCombinedNonce(combinedNonce [musig2.PubNonceSize]byte, T *btcec.PublicKey) ([musig2.PubNonceSize]byte, error) {
	var out [musig2.PubNonceSize]byte
	if T == nil {
		return out, ErrNilArgument
	}
	const pointSize = musig2.PubNonceSize / 2
	first, err := btcec.ParsePubKey(combinedNonce[:pointSize])
	if err != nil {
		return out, err
	}
	shifted, err := addPoints(first, T)
	if err != nil {
		return out, err
	}
	copy(out[:pointSize], shifted.SerializeCompressed())
	copy(out[pointSize:], combinedNonce[pointSize:])
	return out, nil
}

// PreSignPartial produces one signer's partial signature in a MuSig2 adaptor
// signing session. combinedNonce is the untweaked aggregate of all signers'
// public nonces; this function applies the adaptor tweak itself so that every
// signer derives the same shifted nonce.
//
// Keys are aggregated unsorted, matching Qi consensus verification.
func PreSignPartial(secNonce [musig2.SecNonceSize]byte, privKey *btcec.PrivateKey,
	combinedNonce [musig2.PubNonceSize]byte, pubKeys []*btcec.PublicKey,
	msg [32]byte, T *btcec.PublicKey) (*musig2.PartialSignature, error) {

	if privKey == nil || T == nil {
		return nil, ErrNilArgument
	}
	tweaked, err := TweakCombinedNonce(combinedNonce, T)
	if err != nil {
		return nil, err
	}
	return musig2.Sign(secNonce, privKey, tweaked, pubKeys, msg)
}

// CombinePartials aggregates partial adaptor signatures into a single adaptor
// signature over the MuSig2 aggregate key. It verifies the result before
// returning, so a malformed or malicious partial signature is caught here
// rather than at settlement time.
func CombinePartials(partialSigs []*musig2.PartialSignature, pubKeys []*btcec.PublicKey,
	msg [32]byte, T *btcec.PublicKey) (*Signature, error) {

	if len(partialSigs) == 0 {
		return nil, ErrNoPartialSigs
	}
	if T == nil {
		return nil, ErrNilArgument
	}
	shiftedNonce := partialSigs[0].R
	if shiftedNonce == nil {
		return nil, ErrNilArgument
	}
	s := new(btcec.ModNScalar)
	for _, partial := range partialSigs {
		if partial == nil || partial.S == nil {
			return nil, ErrNilArgument
		}
		if partial.R == nil || !partial.R.IsEqual(shiftedNonce) {
			return nil, errors.New("adaptor: partial signatures disagree on the nonce point")
		}
		s.Add(partial.S)
	}

	aggKey, err := AggregateKeys(pubKeys)
	if err != nil {
		return nil, err
	}
	sig := &Signature{R: shiftedNonce, S: s, Negated: isOdd(shiftedNonce)}
	if !sig.Verify(aggKey, msg, T) {
		return nil, ErrInvalidAdaptor
	}
	return sig, nil
}

// AggregateKeys returns the MuSig2 aggregate public key for a set of signers,
// aggregated exactly as Qi consensus does (unsorted, untweaked). A Qi address
// derived from this key is spendable by a signature from the full signer set
// and is indistinguishable on chain from an ordinary single-key address.
func AggregateKeys(pubKeys []*btcec.PublicKey) (*btcec.PublicKey, error) {
	if len(pubKeys) == 0 {
		return nil, ErrNilArgument
	}
	if len(pubKeys) == 1 {
		return pubKeys[0], nil
	}
	aggKey, _, _, err := musig2.AggregateKeys(pubKeys, false)
	if err != nil {
		return nil, err
	}
	return aggKey.FinalKey, nil
}
