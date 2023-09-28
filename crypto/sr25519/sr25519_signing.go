// Copyright 2021 ChainSafe Systems (ON)
// SPDX-License-Identifier: LGPL-3.0-only

package sr25519

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	sr25519 "github.com/ChainSafe/go-schnorrkel"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/gtank/merlin"
)

// SigningContext is the context for signatures used or created with substrate
var SigningContext = []byte("substrate")

// Keypair is a sr25519 public-private keypair
type Keypair struct {
	public  *PublicKey
	private *PrivateKey
}

// PublicKey holds reference to a sr25519.PublicKey
type PublicKey struct {
	key *sr25519.PublicKey
}

// PrivateKey holds reference to a sr25519.SecretKey
type PrivateKey struct {
	key *sr25519.SecretKey
}

var ErrSignatureVerificationFailed = errors.New("failed to verify signature")

// VerifySignature verifies a signature given a public key and a message
func VerifySignature(publicKey, signature, message []byte) error {
	pubKey, err := NewPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("sr25519: %w", err)
	}

	ok, err := pubKey.Verify(message, signature)
	if err != nil {
		return fmt.Errorf("sr25519: %w", err)
	} else if !ok {
		return fmt.Errorf("sr25519: %w: for message 0x%x, signature 0x%x and public key 0x%x",
			ErrSignatureVerificationFailed, message, signature, publicKey)
	}

	return nil
}

// NewKeypair returns a sr25519 Keypair given a schnorrkel secret key
func NewKeypair(priv *sr25519.SecretKey) (*Keypair, error) {
	pub, err := priv.Public()
	if err != nil {
		return nil, err
	}

	return &Keypair{
		public:  &PublicKey{key: pub},
		private: &PrivateKey{key: priv},
	}, nil
}

// NewKeypairFromPrivate returns a sr25519 Keypair given a *sr25519.PrivateKey
func NewKeypairFromPrivate(priv *PrivateKey) (*Keypair, error) {
	pub, err := priv.Public()
	if err != nil {
		return nil, err
	}

	return &Keypair{
		public:  pub.(*PublicKey),
		private: priv,
	}, nil
}

// NewKeypairFromSeed returns a new sr25519 Keypair given a seed
func NewKeypairFromSeed(keystr []byte) (*Keypair, error) {
	if len(keystr) != crypto.SeedLength {
		return nil, errors.New("cannot generate key from seed: seed is not 32 bytes long")
	}

	buf := [crypto.SeedLength]byte{}
	copy(buf[:], keystr)
	msc, err := sr25519.NewMiniSecretKeyFromRaw(buf)
	if err != nil {
		return nil, err
	}

	priv := msc.ExpandEd25519()
	pub := msc.Public()

	return &Keypair{
		public:  &PublicKey{key: pub},
		private: &PrivateKey{key: priv},
	}, nil
}

// HexToBytes turns a 0x prefixed hex string into a byte slice
func HexToBytes(in string) (b []byte, err error) {
	if !strings.HasPrefix(in, "0x") {
		return nil, fmt.Errorf("%w: %s", errors.New("rr no prefix"), in)
	}

	b, err = hex.DecodeString(in[2:])
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, in)
	}

	return b, nil
}

// NewKeypairFromPrivateKeyString returns a Keypair given a 0x prefixed private key string
func NewKeypairFromPrivateKeyString(in string) (*Keypair, error) {
	privBytes, err := HexToBytes(in)
	if err != nil {
		return nil, err
	}

	return NewKeypairFromPrivateKeyBytes(privBytes)
}

// NewKeypairFromPrivateKeyBytes returns a Keypair given a private key byte slice
func NewKeypairFromPrivateKeyBytes(in []byte) (*Keypair, error) {
	priv, err := NewPrivateKey(in)
	if err != nil {
		return nil, err
	}

	pub, err := priv.Public()
	if err != nil {
		return nil, err
	}

	return &Keypair{
		private: priv,
		public:  pub.(*PublicKey),
	}, nil
}

// NewKeypairFromMnenomic returns a new Keypair using the given mnemonic and password.
func NewKeypairFromMnenomic(mnemonic, password string) (*Keypair, error) {
	msc, err := sr25519.MiniSecretKeyFromMnemonic(mnemonic, password)
	if err != nil {
		return nil, err
	}

	priv := msc.ExpandEd25519()
	pub := msc.Public()

	return &Keypair{
		public:  &PublicKey{key: pub},
		private: &PrivateKey{key: priv},
	}, nil
}

// NewPrivateKey creates a new private key using the input bytes
func NewPrivateKey(in []byte) (*PrivateKey, error) {
	if len(in) != crypto.PrivateKeyLength {
		return nil, errors.New("input to create sr25519 private key is not 32 bytes")
	}
	priv := new(PrivateKey)
	err := priv.Decode(in)
	return priv, err
}

// GenerateKeypair returns a new sr25519 keypair
func GenerateKeypair() (*Keypair, error) {
	priv, pub, err := sr25519.GenerateKeypair()
	if err != nil {
		return nil, err
	}

	return &Keypair{
		public:  &PublicKey{key: pub},
		private: &PrivateKey{key: priv},
	}, nil
}

// NewPublicKey returns a sr25519 public key from 32 byte input
func NewPublicKey(in []byte) (*PublicKey, error) {
	if len(in) != crypto.PublicKeyLength {
		return nil, errors.New("cannot create public key: input is not 32 bytes")
	}

	buf := [crypto.PublicKeyLength]byte{}
	copy(buf[:], in)

	sr25519Key, err := sr25519.NewPublicKey(buf)
	if err != nil {
		return nil, fmt.Errorf("creating sr25519 public key: %w", err)
	}

	return &PublicKey{key: sr25519Key}, nil
}

// Type returns Sr25519Type
func (*Keypair) Type() crypto.KeyType {
	return crypto.Sr25519Type
}

// Sign uses the keypair to sign the message using the sr25519 signature algorithm
func (kp *Keypair) Sign(msg []byte) ([]byte, error) {
	return kp.private.Sign(msg)
}

// Public returns the public key corresponding to this keypair
func (kp *Keypair) Public() crypto.PublicKey {
	return kp.public
}

// Private returns the private key corresponding to this keypair
func (kp *Keypair) Private() crypto.PrivateKey {
	return kp.private
}

// VrfSign creates a VRF output and proof from a message and private key
func (kp *Keypair) VrfSign(t *merlin.Transcript) ([crypto.VRFOutputLength]byte, [crypto.VRFProofLength]byte, error) {
	return kp.private.VrfSign(t)
}

// Sign uses the private key to sign the message using the sr25519 signature algorithm
func (k *PrivateKey) Sign(msg []byte) ([]byte, error) {
	if k.key == nil {
		return nil, errors.New("key is nil")
	}
	t := sr25519.NewSigningContext(SigningContext, msg)
	sig, err := k.key.Sign(t)
	if err != nil {
		return nil, err
	}
	enc := sig.Encode()
	return enc[:], nil
}

// VrfSign creates a VRF output and proof from a message and private key
func (k *PrivateKey) VrfSign(t *merlin.Transcript) ([crypto.VRFOutputLength]byte, [crypto.VRFProofLength]byte, error) {
	inout, proof, err := k.key.VrfSign(t)
	if err != nil {
		return [32]byte{}, [64]byte{}, err
	}
	out := inout.Output().Encode()
	proofb := proof.Encode()
	return out, proofb, nil
}

// Public returns the public key corresponding to this private key
func (k *PrivateKey) Public() (crypto.PublicKey, error) {
	if k.key == nil {
		return nil, errors.New("key is nil")
	}
	pub, err := k.key.Public()
	if err != nil {
		return nil, err
	}
	return &PublicKey{key: pub}, nil
}

// Encode returns the 32-byte encoding of the private key
func (k *PrivateKey) Encode() []byte {
	if k.key == nil {
		return nil
	}
	enc := k.key.Encode()
	return enc[:]
}

// Decode decodes the input bytes into a private key and sets the receiver the decoded key
// Input must be 32 bytes, or else this function will error
func (k *PrivateKey) Decode(in []byte) error {
	if len(in) != crypto.PrivateKeyLength {
		return errors.New("input to sr25519 private key decode is not 32 bytes")
	}
	b := [crypto.PrivateKeyLength]byte{}
	copy(b[:], in)
	k.key = &sr25519.SecretKey{}
	return k.key.Decode(b)
}

// Hex returns the private key as a '0x' prefixed hex string
func (k *PrivateKey) Hex() string {
	enc := k.Encode()
	h := hex.EncodeToString(enc)
	return "0x" + h
}

// Verify uses the sr25519 signature algorithm to verify that the message was signed by
// this public key; it returns true if this key created the signature for the message,
// false otherwise
func (k *PublicKey) Verify(msg, sig []byte) (bool, error) {
	if k.key == nil {
		return false, errors.New("nil public key")
	}

	if len(sig) != crypto.SignatureLengthEd25519 {
		return false, errors.New("invalid signature length")
	}

	b := [crypto.SignatureLengthEd25519]byte{}
	copy(b[:], sig)

	s := &sr25519.Signature{}
	err := s.Decode(b)
	if err != nil {
		return false, err
	}

	t := sr25519.NewSigningContext(SigningContext, msg)
	return k.key.Verify(s, t)
}

// VerifyDeprecated verifies that the public key signed the given message.
// Deprecated: this is used by ext_crypto_sr25519_verify_version_1 only and should not be used anywhere else.
// This method does not check that the signature is in fact a schnorrkel signature, and does not
// distinguish between sr25519 and ed25519 signatures.
func (k *PublicKey) VerifyDeprecated(msg, sig []byte) (bool, error) {
	if k.key == nil {
		return false, errors.New("nil public key")
	}

	if len(sig) != crypto.SignatureLengthEd25519 {
		return false, errors.New("invalid signature length")
	}

	b := [crypto.SignatureLengthEd25519]byte{}
	copy(b[:], sig)

	s := &sr25519.Signature{}
	err := s.DecodeNotDistinguishedFromEd25519(b)
	if err != nil {
		return false, err
	}

	t := sr25519.NewSigningContext(SigningContext, msg)
	ok, err := k.key.Verify(s, t)
	if err != nil {
		return false, fmt.Errorf("verifying signature for sr25519 signing context: %w", err)
	} else if ok {
		return true, nil
	}

	t = merlin.NewTranscript(string(SigningContext))
	t.AppendMessage([]byte("sign-bytes"), msg)
	ok, err = k.key.Verify(s, t)
	if err != nil {
		return false, fmt.Errorf("verifying signature for merlin transcript: %w", err)
	}

	return ok, nil
}

// VrfVerify confirms that the output and proof are valid given a message and public key
func (k *PublicKey) VrfVerify(t *merlin.Transcript, out [crypto.VRFOutputLength]byte,
	proof [crypto.VRFProofLength]byte) (bool, error) {
	o := new(sr25519.VrfOutput)
	err := o.Decode(out)
	if err != nil {
		return false, err
	}

	p := new(sr25519.VrfProof)
	err = p.Decode(proof)
	if err != nil {
		return false, err
	}

	sr25519Key, err := sr25519.NewOutput(out)
	if err != nil {
		return false, fmt.Errorf("creating sr25519 key: %w", err)
	}

	return k.key.VrfVerify(t, sr25519Key, p)
}

// Encode returns the 32-byte encoding of the public key
func (k *PublicKey) Encode() []byte {
	if k.key == nil {
		return nil
	}

	enc := k.key.Encode()
	return enc[:]
}

// Decode decodes the input bytes into a public key and sets the receiver the decoded key
// Input must be 32 bytes, or else this function will error
func (k *PublicKey) Decode(in []byte) error {
	if len(in) != crypto.PublicKeyLength {
		return errors.New("input to sr25519 public key decode is not 32 bytes")
	}
	b := [crypto.PublicKeyLength]byte{}
	copy(b[:], in)
	k.key = &sr25519.PublicKey{}
	return k.key.Decode(b)
}

// Address returns the ss58 address for this public key
func (k *PublicKey) Address() common.Address {
	return crypto.PublicKeyToAddress(k)
}

// Hex returns the public key as a '0x' prefixed hex string
func (k *PublicKey) Hex() string {
	enc := k.Encode()
	h := hex.EncodeToString(enc)
	return "0x" + h
}

// AsBytes returns the key as a [crypto.PublicKeyLength]byte
func (k *PublicKey) AsBytes() [crypto.PublicKeyLength]byte {
	return k.key.Encode()
}

// AttachInput wraps schnorrkel *VrfOutput.AttachInput
func AttachInput(output [crypto.VRFOutputLength]byte, pub *PublicKey, t *merlin.Transcript) (
	vrfInOut *sr25519.VrfInOut, err error) {
	out, err := sr25519.NewOutput(output)
	if err != nil {
		return nil, fmt.Errorf("creating sr25519 output: %w", err)
	}

	vrfInOut, err = out.AttachInput(pub.key, t)
	if err != nil {
		return nil, fmt.Errorf("attaching input: %w", err)
	}

	return vrfInOut, nil
}
