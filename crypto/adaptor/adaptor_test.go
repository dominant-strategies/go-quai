package adaptor_test

import (
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/crypto/adaptor"
)

func testMsg(seed byte) [32]byte {
	var msg [32]byte
	for i := range msg {
		msg[i] = seed + byte(i)
	}
	return msg
}

func newKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// TestSingleKeyRoundTrip covers the full PTLC lifecycle for a single key:
// pre-sign, verify without the secret, complete, verify as a standard BIP-340
// signature, and recover the secret from the completed signature.
func TestSingleKeyRoundTrip(t *testing.T) {
	// Run repeatedly so both nonce parities are exercised.
	for i := 0; i < 32; i++ {
		key := newKey(t)
		msg := testMsg(byte(i))
		secret, point, err := adaptor.NewSecret()
		if err != nil {
			t.Fatal(err)
		}

		sig, err := adaptor.PreSign(key, msg, point)
		if err != nil {
			t.Fatal(err)
		}
		if !sig.Verify(key.PubKey(), msg, point) {
			t.Fatal("adaptor signature failed verification")
		}

		final, err := sig.Adapt(secret)
		if err != nil {
			t.Fatal(err)
		}
		if !final.Verify(msg[:], key.PubKey()) {
			t.Fatal("completed signature is not a valid BIP-340 signature")
		}

		recovered, err := sig.Extract(final)
		if err != nil {
			t.Fatal(err)
		}
		if !recovered.Equals(secret) {
			t.Fatal("extracted secret does not match the original")
		}
		if !adaptor.VerifySecret(recovered, point) {
			t.Fatal("extracted secret is not the discrete log of the adaptor point")
		}
	}
}

func TestSingleKeyRejectsBadInputs(t *testing.T) {
	key := newKey(t)
	msg := testMsg(1)
	secret, point, err := adaptor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := adaptor.PreSign(key, msg, point)
	if err != nil {
		t.Fatal(err)
	}

	// Wrong adaptor point.
	_, otherPoint, err := adaptor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if sig.Verify(key.PubKey(), msg, otherPoint) {
		t.Fatal("verified against the wrong adaptor point")
	}
	// Wrong message.
	if sig.Verify(key.PubKey(), testMsg(2), point) {
		t.Fatal("verified against the wrong message")
	}
	// Wrong public key.
	if sig.Verify(newKey(t).PubKey(), msg, point) {
		t.Fatal("verified against the wrong public key")
	}
	// Completing with the wrong secret must not yield a valid signature.
	otherSecret, _, err := adaptor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	bad, err := sig.Adapt(otherSecret)
	if err != nil {
		t.Fatal(err)
	}
	if bad.Verify(msg[:], key.PubKey()) {
		t.Fatal("completing with the wrong secret produced a valid signature")
	}
	// Tampering with the adaptor scalar must break verification.
	tampered := &adaptor.Signature{R: sig.R, S: new(btcec.ModNScalar).SetInt(7), Negated: sig.Negated}
	if tampered.Verify(key.PubKey(), msg, point) {
		t.Fatal("verified a tampered adaptor signature")
	}
	_ = secret
}

// musigSession runs a full two-round MuSig2 adaptor signing session across
// the given keys and returns the combined adaptor signature.
func musigSession(t *testing.T, keys []*btcec.PrivateKey, msg [32]byte, point *btcec.PublicKey) (*adaptor.Signature, *btcec.PublicKey) {
	t.Helper()
	pubKeys := make([]*btcec.PublicKey, len(keys))
	nonces := make([]*musig2.Nonces, len(keys))
	pubNonces := make([][musig2.PubNonceSize]byte, len(keys))
	for i, key := range keys {
		pubKeys[i] = key.PubKey()
		nonce, err := musig2.GenNonces(musig2.WithPublicKey(key.PubKey()))
		if err != nil {
			t.Fatal(err)
		}
		nonces[i] = nonce
		pubNonces[i] = nonce.PubNonce
	}
	combinedNonce, err := musig2.AggregateNonces(pubNonces)
	if err != nil {
		t.Fatal(err)
	}
	partials := make([]*musig2.PartialSignature, len(keys))
	for i, key := range keys {
		partial, err := adaptor.PreSignPartial(nonces[i].SecNonce, key, combinedNonce, pubKeys, msg, point)
		if err != nil {
			t.Fatal(err)
		}
		partials[i] = partial
	}
	sig, err := adaptor.CombinePartials(partials, pubKeys, msg, point)
	if err != nil {
		t.Fatal(err)
	}
	aggKey, err := adaptor.AggregateKeys(pubKeys)
	if err != nil {
		t.Fatal(err)
	}
	return sig, aggKey
}

// TestMuSig2RoundTrip is the construction channels actually use: a 2-of-2
// adaptor signature that completes into a signature valid under the
// aggregate key, exactly as Qi consensus verifies it.
func TestMuSig2RoundTrip(t *testing.T) {
	for i := 0; i < 16; i++ {
		keys := []*btcec.PrivateKey{newKey(t), newKey(t)}
		msg := testMsg(byte(i))
		secret, point, err := adaptor.NewSecret()
		if err != nil {
			t.Fatal(err)
		}

		sig, aggKey := musigSession(t, keys, msg, point)
		if !sig.Verify(aggKey, msg, point) {
			t.Fatal("combined adaptor signature failed verification")
		}

		final, err := sig.Adapt(secret)
		if err != nil {
			t.Fatal(err)
		}
		if !final.Verify(msg[:], aggKey) {
			t.Fatal("completed MuSig2 signature is not valid under the aggregate key")
		}

		recovered, err := sig.Extract(final)
		if err != nil {
			t.Fatal(err)
		}
		if !recovered.Equals(secret) {
			t.Fatal("extracted secret does not match")
		}
	}
}

// TestMuSig2AggregateMatchesConsensus pins the aggregate key to what the
// consensus code computes, since a Qi UTXO is addressed to its hash.
func TestMuSig2AggregateMatchesConsensus(t *testing.T) {
	keys := []*btcec.PrivateKey{newKey(t), newKey(t), newKey(t)}
	pubKeys := make([]*btcec.PublicKey, len(keys))
	for i, key := range keys {
		pubKeys[i] = key.PubKey()
	}
	got, err := adaptor.AggregateKeys(pubKeys)
	if err != nil {
		t.Fatal(err)
	}
	// Mirrors ValidateQiTxOutputsAndSignature.
	want, _, _, err := musig2.AggregateKeys(pubKeys, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsEqual(want.FinalKey) {
		t.Fatal("aggregate key does not match consensus key aggregation")
	}
}

// TestMuSig2ThreeParty covers aggregate sizes beyond the 2-of-2 channel case.
func TestMuSig2ThreeParty(t *testing.T) {
	keys := []*btcec.PrivateKey{newKey(t), newKey(t), newKey(t)}
	msg := testMsg(9)
	secret, point, err := adaptor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	sig, aggKey := musigSession(t, keys, msg, point)
	final, err := sig.Adapt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Verify(msg[:], aggKey) {
		t.Fatal("three-party completed signature invalid")
	}
}

// TestAtomicityAcrossHops is the property routing depends on: the same
// secret completes adaptor signatures held by different, unrelated signers,
// so settling downstream hands the upstream hop what it needs to settle too.
func TestAtomicityAcrossHops(t *testing.T) {
	secret, point, err := adaptor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	// Hop 1: A -> B, a 2-of-2 channel. Hop 2: B -> C, a different channel.
	hop1Keys := []*btcec.PrivateKey{newKey(t), newKey(t)}
	hop2Keys := []*btcec.PrivateKey{newKey(t), newKey(t)}
	msg1, msg2 := testMsg(11), testMsg(22)

	sig1, agg1 := musigSession(t, hop1Keys, msg1, point)
	sig2, agg2 := musigSession(t, hop2Keys, msg2, point)

	// The downstream hop settles first, publishing a completed signature.
	final2, err := sig2.Adapt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !final2.Verify(msg2[:], agg2) {
		t.Fatal("downstream settlement invalid")
	}

	// The upstream node observes it and recovers the secret...
	recovered, err := sig2.Extract(final2)
	if err != nil {
		t.Fatal(err)
	}
	// ...then uses it to settle its own, entirely separate, adaptor signature.
	final1, err := sig1.Adapt(recovered)
	if err != nil {
		t.Fatal(err)
	}
	if !final1.Verify(msg1[:], agg1) {
		t.Fatal("upstream settlement with the recovered secret failed: routing is not atomic")
	}
}

// TestBlindedPointsStayConsistent checks the decorrelation property: hops use
// different points, but the offsets compose so settlement still cascades.
func TestBlindedPointsStayConsistent(t *testing.T) {
	secret, point, err := adaptor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	// Upstream hop is locked to a blinded point T' = T + r*G.
	blind, blindPoint, err := adaptor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	blinded, err := adaptor.AddPoints(point, blindPoint)
	if err != nil {
		t.Fatal(err)
	}
	keys := []*btcec.PrivateKey{newKey(t), newKey(t)}
	msg := testMsg(33)
	sig, aggKey := musigSession(t, keys, msg, blinded)

	// Knowing t and r, the blinded secret is t + r.
	blindedSecret := new(btcec.ModNScalar)
	blindedSecret.Set(secret).Add(blind)
	final, err := sig.Adapt(blindedSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Verify(msg[:], aggKey) {
		t.Fatal("blinded adaptor signature did not complete correctly")
	}
}

func TestExtractRejectsGarbage(t *testing.T) {
	key := newKey(t)
	msg := testMsg(5)
	_, point, err := adaptor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := adaptor.PreSign(key, msg, point)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sig.Extract(nil); err == nil {
		t.Fatal("expected error extracting from a nil signature")
	}
	// Extracting from an unrelated signature yields a scalar that is not the
	// adaptor secret; callers must check with VerifySecret.
	other, err := schnorr.Sign(key, msg[:])
	if err != nil {
		t.Fatal(err)
	}
	bogus, err := sig.Extract(other)
	if err == nil && adaptor.VerifySecret(bogus, point) {
		t.Fatal("extracted a valid secret from an unrelated signature")
	}
}
