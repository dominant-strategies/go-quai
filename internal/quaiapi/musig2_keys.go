package quaiapi

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/params"
)

// MuSig2KeyManager manages the keys for 2-of-3 MuSig2 signatures
type MuSig2KeyManager struct {
	// Our private key
	PrivateKey *btcec.PrivateKey
	// Our public key
	PublicKey *btcec.PublicKey
	// Index of this participant (0, 1, or 2)
	ParticipantIndex int
	// All 3 public keys for the 2-of-3 multisig
	AllPublicKeys [3]*btcec.PublicKey
}

var musig2Keys *MuSig2KeyManager

// InitMuSig2Keys initializes the MuSig2 key manager from environment variables
func InitMuSig2Keys() error {
	// Read private key from environment
	privKeyHex := os.Getenv("MUSIG2_PRIVKEY")
	if privKeyHex == "" {
		return fmt.Errorf("MUSIG2_PRIVKEY environment variable not set")
	}

	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode MUSIG2_PRIVKEY: %w", err)
	}

	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)
	if privKey == nil {
		return fmt.Errorf("failed to parse private key")
	}

	// Load the 3 public keys from protocol params
	var allPubKeys [3]*btcec.PublicKey
	if len(params.MuSig2PublicKeys) != 3 {
		return fmt.Errorf("expected 3 MuSig2 public keys in protocol params, got %d", len(params.MuSig2PublicKeys))
	}

	for i := 0; i < 3; i++ {
		pubKeyBytes, err := hex.DecodeString(params.MuSig2PublicKeys[i])
		if err != nil {
			return fmt.Errorf("failed to decode MuSig2PublicKeys[%d]: %w", i, err)
		}

		pubKey, err := btcec.ParsePubKey(pubKeyBytes)
		if err != nil {
			return fmt.Errorf("failed to parse MuSig2PublicKeys[%d]: %w", i, err)
		}

		allPubKeys[i] = pubKey
	}

	// Determine which index we are by matching our public key
	ourPubKey := privKey.PubKey()
	participantIndex := -1
	for i, pk := range allPubKeys {
		if pk.IsEqual(ourPubKey) {
			participantIndex = i
			break
		}
	}

	if participantIndex == -1 {
		return fmt.Errorf("our public key doesn't match any of the 3 configured public keys")
	}

	musig2Keys = &MuSig2KeyManager{
		PrivateKey:       privKey,
		PublicKey:        ourPubKey,
		ParticipantIndex: participantIndex,
		AllPublicKeys:    allPubKeys,
	}

	return nil
}

// GetMuSig2Keys returns the initialized MuSig2 key manager
func GetMuSig2Keys() (*MuSig2KeyManager, error) {
	if musig2Keys == nil {
		// Try to initialize if not already done
		if err := InitMuSig2Keys(); err != nil {
			// Return nil if keys are not configured (optional feature)
			return nil, nil
		}
	}
	return musig2Keys, nil
}
