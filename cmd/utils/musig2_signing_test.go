package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
)

func TestMuSig2Signing(t *testing.T) {
	t.Log("=== MuSig2 2-of-3 Signing Test ===")
	t.Log()

	// Test message (simulating an AuxTemplate)
	message := []byte("test aux template data")
	msgHash := sha256.Sum256(message)

	// Load private keys from environment or use test keys
	privKey0Hex := os.Getenv("TEST_PRIVKEY_0")
	privKey1Hex := os.Getenv("TEST_PRIVKEY_1")

	if privKey0Hex == "" {
		// Use default test keys
		privKey0Hex = "6c8adb7ffbcb3a819bd54ef44734f73977c1e63ef930f8598993917d980e214a"
		privKey1Hex = "23ff6833b48e876dad7a4669907013a0cc89a0dc793d5728a605754da067e4f6"
	}

	// Parse private keys
	privKey0Bytes, _ := hex.DecodeString(privKey0Hex)
	privKey1Bytes, _ := hex.DecodeString(privKey1Hex)

	privKey0, _ := btcec.PrivKeyFromBytes(privKey0Bytes)
	privKey1, _ := btcec.PrivKeyFromBytes(privKey1Bytes)

	pubKey0 := privKey0.PubKey()
	pubKey1 := privKey1.PubKey()

	t.Logf("Participant 0 public key: %x\n", pubKey0.SerializeCompressed())
	t.Logf("Participant 1 public key: %x\n", pubKey1.SerializeCompressed())
	t.Log()

	// Create signing set (2 of 3)
	signSet := []*btcec.PublicKey{pubKey0, pubKey1}

	// Create MuSig2 contexts
	ctx0, err := musig2.NewContext(privKey0, false, musig2.WithKnownSigners(signSet))
	if err != nil {
		t.Fatalf("Failed to create context 0: %v", err)
	}

	ctx1, err := musig2.NewContext(privKey1, false, musig2.WithKnownSigners(signSet))
	if err != nil {
		t.Fatalf("Failed to create context 1: %v", err)
	}

	// Create sessions
	session0, err := ctx0.NewSession()
	if err != nil {
		t.Fatalf("Failed to create session 0: %v", err)
	}

	session1, err := ctx1.NewSession()
	if err != nil {
		t.Fatalf("Failed to create session 1: %v", err)
	}

	// Exchange nonces
	nonce0 := session0.PublicNonce()
	nonce1 := session1.PublicNonce()

	t.Logf("Nonce 0: %x\n", nonce0[:])
	t.Logf("Nonce 1: %x\n", nonce1[:])
	t.Log()

	// Register nonces
	_, err = session0.RegisterPubNonce(nonce1)
	if err != nil {
		t.Fatalf("Failed to register nonce 1 in session 0: %v", err)
	}

	_, err = session1.RegisterPubNonce(nonce0)
	if err != nil {
		t.Fatalf("Failed to register nonce 0 in session 1: %v", err)
	}

	// Create partial signatures
	partialSig0, err := session0.Sign([32]byte(msgHash))
	if err != nil {
		t.Fatalf("Failed to create partial signature 0: %v", err)
	}

	partialSig1, err := session1.Sign([32]byte(msgHash))
	if err != nil {
		t.Fatalf("Failed to create partial signature 1: %v", err)
	}

	t.Logf("Partial signature 0: %x\n", partialSig0.S.Bytes())
	t.Logf("Partial signature 1: %x\n", partialSig1.S.Bytes())
	t.Log()

	// Combine signatures
	_, err = session0.CombineSig(partialSig1)
	if err != nil {
		t.Fatalf("Failed to combine signatures: %v", err)
	}

	finalSig := session0.FinalSig()
	t.Logf("Final Schnorr signature: %x\n", finalSig.Serialize())
	t.Log()

	// Verify the signature
	aggKey, _, _, err := musig2.AggregateKeys(signSet, false)
	if err != nil {
		t.Fatalf("Failed to aggregate keys: %v", err)
	}

	// Parse and verify
	sig, err := schnorr.ParseSignature(finalSig.Serialize())
	if err != nil {
		t.Fatalf("Failed to parse signature: %v", err)
	}

	valid := sig.Verify(msgHash[:], aggKey.FinalKey)
	if valid {
		t.Log("✓ Signature verification PASSED!")
	} else {
		t.Log("✗ Signature verification FAILED!")
	}

	t.Log()
	t.Log("=== Test Complete ===")
}