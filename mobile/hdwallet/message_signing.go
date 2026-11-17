package hdwallet

import (
	"crypto/ecdsa"
	"fmt"

	quaiCrypto "github.com/dominant-strategies/go-quai/crypto"
)

// PersonalMessageHash computes the EIP-191 prefixed message hash used by personal_sign/signMessage.
func PersonalMessageHash(message []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	return quaiCrypto.Keccak256([]byte(prefix), message)
}

// SignPersonalMessage signs a message using the EIP-191 personal_sign prefix.
func SignPersonalMessage(privKey *ecdsa.PrivateKey, message []byte) ([]byte, error) {
	if privKey == nil {
		return nil, fmt.Errorf("private key is nil")
	}
	sig, err := quaiCrypto.Sign(PersonalMessageHash(message), privKey)
	if err != nil {
		return nil, err
	}
	return normalizeSignatureV(sig), nil
}

// SignRawMessage signs keccak256(message) without an EIP-191 prefix (eth_sign semantics).
func SignRawMessage(privKey *ecdsa.PrivateKey, message []byte) ([]byte, error) {
	if privKey == nil {
		return nil, fmt.Errorf("private key is nil")
	}
	sig, err := quaiCrypto.Sign(quaiCrypto.Keccak256(message), privKey)
	if err != nil {
		return nil, err
	}
	return normalizeSignatureV(sig), nil
}

// SignPersonalMessage signs a message with the private key for the given address using EIP-191.
func (w *HDWallet) SignPersonalMessage(address string, message []byte) ([]byte, error) {
	privKey, err := w.GetPrivateKeyForAddress(address)
	if err != nil {
		return nil, err
	}
	return SignPersonalMessage(privKey, message)
}

// SignRawMessage signs keccak256(message) with the private key for the given address.
func (w *HDWallet) SignRawMessage(address string, message []byte) ([]byte, error) {
	privKey, err := w.GetPrivateKeyForAddress(address)
	if err != nil {
		return nil, err
	}
	return SignRawMessage(privKey, message)
}

func normalizeSignatureV(sig []byte) []byte {
	if len(sig) != 65 {
		return sig
	}
	out := make([]byte, len(sig))
	copy(out, sig)
	if out[64] < 27 {
		out[64] += 27
	}
	return out
}
