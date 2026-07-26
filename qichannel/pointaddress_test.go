package qichannel_test

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/qichannel"
)

// recoverPointAddress performs the recovery the Quai-side contract performs
// via the ecrecover precompile, using the same secp256k1 implementation the
// precompile is backed by.
func recoverPointAddress(t *testing.T, secret *btcec.ModNScalar) []byte {
	t.Helper()
	hash, v, r, s, err := qichannel.ContractRecoveryArgs(secret)
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 65)
	r.FillBytes(sig[0:32])
	s.FillBytes(sig[32:64])
	sig[64] = v

	pubKey, err := crypto.Ecrecover(hash[:], sig)
	if err != nil {
		t.Fatalf("ecrecover failed: %v", err)
	}
	// Strip the 0x04 prefix and hash, matching how the precompile derives an
	// address from the recovered point.
	return crypto.Keccak256(pubKey[1:])[12:]
}

// TestContractPointAddressMatchesScalarMult is the load-bearing check for
// cross-ledger routing: the contract verifies a payment secret by asking
// ecrecover for the address of secret*G, and that must equal the address of
// the payment point the payee committed to. If the contract's curve
// constants or derivation were wrong, this would diverge.
func TestContractPointAddressMatchesScalarMult(t *testing.T) {
	for i := 0; i < 64; i++ {
		secret, point, err := qichannel.NewPayment()
		if err != nil {
			t.Fatal(err)
		}

		// What the payee publishes and the contract stores.
		want := qichannel.PointAddress(point)
		// What the contract computes from the revealed secret.
		got := recoverPointAddress(t, secret)

		if !bytes.Equal(want[:], got) {
			t.Fatalf("iteration %d: contract recovery gives %x, want %x", i, got, want[:])
		}

		// SecretPointAddress must agree with both.
		derived, err := qichannel.SecretPointAddress(secret)
		if err != nil {
			t.Fatal(err)
		}
		if derived != want {
			t.Fatalf("iteration %d: SecretPointAddress diverges from PointAddress", i)
		}
	}
}

// TestWrongSecretGivesDifferentAddress checks the contract's claim path
// actually discriminates: a secret that is not the discrete log of the stored
// point must not recover to it.
func TestWrongSecretGivesDifferentAddress(t *testing.T) {
	_, point, err := qichannel.NewPayment()
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := qichannel.NewPayment()
	if err != nil {
		t.Fatal(err)
	}
	want := qichannel.PointAddress(point)
	got := recoverPointAddress(t, other)
	if bytes.Equal(want[:], got) {
		t.Fatal("an unrelated secret recovered to the stored point address")
	}
}

// TestBlindedPointsWorkOnBothLedgers checks that the per-hop blinding used
// for route decorrelation survives the translation to the contract's
// representation, so a hop may be a Qi channel on one side and this contract
// on the other.
func TestBlindedPointsWorkOnBothLedgers(t *testing.T) {
	secret, point, err := qichannel.NewPayment()
	if err != nil {
		t.Fatal(err)
	}
	blinded, blind, err := qichannel.BlindPoint(point)
	if err != nil {
		t.Fatal(err)
	}
	blindedSecret := new(btcec.ModNScalar)
	blindedSecret.Set(secret).Add(blind)

	// The blinded point's on-chain commitment must be unlockable by the
	// blinded secret, exactly as the unblinded pair is.
	want := qichannel.PointAddress(blinded)
	got := recoverPointAddress(t, blindedSecret)
	if !bytes.Equal(want[:], got) {
		t.Fatal("blinded point does not match the secret that should unlock it on the contract side")
	}

	// And the underlying secret is still recoverable from the blinded one,
	// which is what lets the upstream hop settle.
	if recovered := qichannel.UnblindSecret(blindedSecret, blind); !recovered.Equals(secret) {
		t.Fatal("unblinding failed")
	}
}

func TestContractRecoveryArgsRejectsBadScalars(t *testing.T) {
	if _, _, _, _, err := qichannel.ContractRecoveryArgs(nil); err == nil {
		t.Fatal("expected nil scalar to be rejected")
	}
	zero := new(btcec.ModNScalar)
	if _, _, _, _, err := qichannel.ContractRecoveryArgs(zero); err == nil {
		t.Fatal("expected zero scalar to be rejected")
	}
	if _, err := qichannel.SecretPointAddress(zero); err == nil {
		t.Fatal("expected zero scalar to be rejected")
	}
}

// TestRecoveryArgsUseGeneratorConstants pins the r value the contract passes
// to ecrecover to the generator's x coordinate, since the trick only reduces
// to a scalar multiplication for that specific point.
func TestRecoveryArgsUseGeneratorConstants(t *testing.T) {
	secret, _, err := qichannel.NewPayment()
	if err != nil {
		t.Fatal(err)
	}
	_, _, r, s, err := qichannel.ContractRecoveryArgs(secret)
	if err != nil {
		t.Fatal(err)
	}
	gx, _ := new(big.Int).SetString("79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798", 16)
	n, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	if r.Cmp(gx) != 0 {
		t.Fatal("r is not the generator x coordinate")
	}
	if s.Sign() == 0 || s.Cmp(n) >= 0 {
		t.Fatal("s is outside the valid range")
	}
}
