package hdwallet

import (
	"errors"

	"github.com/tyler-smith/go-bip39"
)

// Mnemonic represents a BIP39 mnemonic phrase with its derived seed.
type Mnemonic struct {
	Phrase   string
	Password string // optional passphrase (default "")
	Entropy  []byte // 16-32 bytes of underlying entropy
}

// GenerateMnemonic creates a new random 12-word (128-bit) mnemonic.
func GenerateMnemonic(password string) (*Mnemonic, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return nil, err
	}
	phrase, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, err
	}
	return &Mnemonic{
		Phrase:   phrase,
		Password: password,
		Entropy:  entropy,
	}, nil
}

// NewMnemonicFromPhrase validates a BIP39 phrase and creates a Mnemonic.
func NewMnemonicFromPhrase(phrase string, password string) (*Mnemonic, error) {
	if !bip39.IsMnemonicValid(phrase) {
		return nil, errors.New("invalid mnemonic phrase")
	}
	entropy, err := bip39.EntropyFromMnemonic(phrase)
	if err != nil {
		return nil, err
	}
	return &Mnemonic{
		Phrase:   phrase,
		Password: password,
		Entropy:  entropy,
	}, nil
}

// NewMnemonicFromEntropy generates a mnemonic from the given entropy bytes.
// Entropy must be 16, 20, 24, 28, or 32 bytes.
func NewMnemonicFromEntropy(entropy []byte, password string) (*Mnemonic, error) {
	phrase, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, err
	}
	return &Mnemonic{
		Phrase:   phrase,
		Password: password,
		Entropy:  entropy,
	}, nil
}

// ComputeSeed derives the 64-byte BIP39 seed using
// PBKDF2(phrase, "mnemonic"+password, 2048, 64, SHA-512).
func (m *Mnemonic) ComputeSeed() ([]byte, error) {
	if m.Phrase == "" {
		return nil, errors.New("empty mnemonic phrase")
	}
	return bip39.NewSeedWithErrorChecking(m.Phrase, m.Password)
}

// IsValidMnemonic checks if a phrase is a valid BIP39 mnemonic.
func IsValidMnemonic(phrase string) bool {
	return bip39.IsMnemonicValid(phrase)
}
