package hdwallet

import (
	"encoding/hex"
	"testing"
)

func TestGenerateMnemonic(t *testing.T) {
	m, err := GenerateMnemonic("")
	if err != nil {
		t.Fatal(err)
	}
	if m.Phrase == "" {
		t.Fatal("phrase should not be empty")
	}
	if len(m.Entropy) != 16 {
		t.Fatalf("expected 16 bytes entropy, got %d", len(m.Entropy))
	}
	if !IsValidMnemonic(m.Phrase) {
		t.Fatal("generated mnemonic should be valid")
	}
}

func TestNewMnemonicFromPhrase(t *testing.T) {
	phrase := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	m, err := NewMnemonicFromPhrase(phrase, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Phrase != phrase {
		t.Fatalf("phrase mismatch: got %q", m.Phrase)
	}
}

func TestNewMnemonicFromPhraseInvalid(t *testing.T) {
	_, err := NewMnemonicFromPhrase("not a valid mnemonic phrase at all", "")
	if err == nil {
		t.Fatal("expected error for invalid mnemonic")
	}
}

func TestNewMnemonicFromEntropy(t *testing.T) {
	// 128-bit entropy (all zeros) should produce "abandon" x11 + "about"
	entropy := make([]byte, 16)
	m, err := NewMnemonicFromEntropy(entropy, "")
	if err != nil {
		t.Fatal(err)
	}
	expected := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	if m.Phrase != expected {
		t.Fatalf("phrase mismatch:\n  got:  %q\n  want: %q", m.Phrase, expected)
	}
}

func TestComputeSeed_Deterministic(t *testing.T) {
	phrase := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	m, err := NewMnemonicFromPhrase(phrase, "testpass")
	if err != nil {
		t.Fatal(err)
	}
	seed1, err := m.ComputeSeed()
	if err != nil {
		t.Fatal(err)
	}
	seed2, err := m.ComputeSeed()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(seed1) != hex.EncodeToString(seed2) {
		t.Fatal("repeated seed derivation should be deterministic")
	}
	if len(seed1) != 64 {
		t.Fatalf("expected 64-byte seed, got %d", len(seed1))
	}
	// Different passphrase should produce different seed
	m2, _ := NewMnemonicFromPhrase(phrase, "otherpass")
	seed3, _ := m2.ComputeSeed()
	if hex.EncodeToString(seed1) == hex.EncodeToString(seed3) {
		t.Fatal("different passwords should produce different seeds")
	}
}

func TestComputeSeed_NoPassphrase(t *testing.T) {
	phrase := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	m, err := NewMnemonicFromPhrase(phrase, "")
	if err != nil {
		t.Fatal(err)
	}
	seed, err := m.ComputeSeed()
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) != 64 {
		t.Fatalf("expected 64-byte seed, got %d", len(seed))
	}
	// BIP39 vector for this phrase with empty passphrase
	expectedHex := "5eb00bbddcf069084889a8ab9155568165f5c453ccb85e70811aaed6f6da5fc19a5ac40b389cd370d086206dec8aa6c43daea6690f20ad3d8d48b2d2ce9e38e4"
	if hex.EncodeToString(seed) != expectedHex {
		t.Fatalf("seed mismatch:\n  got:  %s\n  want: %s", hex.EncodeToString(seed), expectedHex)
	}
}

func TestIsValidMnemonic(t *testing.T) {
	if !IsValidMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about") {
		t.Fatal("expected valid mnemonic")
	}
	if IsValidMnemonic("invalid mnemonic") {
		t.Fatal("expected invalid mnemonic")
	}
	if IsValidMnemonic("") {
		t.Fatal("expected empty string to be invalid")
	}
}

func TestRoundTrip(t *testing.T) {
	m1, err := GenerateMnemonic("test")
	if err != nil {
		t.Fatal(err)
	}
	m2, err := NewMnemonicFromPhrase(m1.Phrase, "test")
	if err != nil {
		t.Fatal(err)
	}
	seed1, err := m1.ComputeSeed()
	if err != nil {
		t.Fatal(err)
	}
	seed2, err := m2.ComputeSeed()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(seed1) != hex.EncodeToString(seed2) {
		t.Fatal("seeds should match after round-trip")
	}
}
