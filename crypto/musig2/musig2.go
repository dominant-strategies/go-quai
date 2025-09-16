package musig2

import (
	// removed: "crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/params"
)

// Constants for MuSig2 operations
const (
	// NonceSize is the expected size of a public nonce in bytes
	NonceSize = 66
	// SignatureSize is the expected size of a Schnorr signature in bytes
	SignatureSize = 64
	// RequiredSigners is the number of signers required for 2-of-3 MuSig2
	RequiredSigners = 2
	// TotalSigners is the total number of potential signers in 2-of-3 MuSig2
	TotalSigners = 3
	// MaxMessageSize is the maximum allowed message size in bytes
	MaxMessageSize = 1024 * 1024 // 1MB
	// MessageDigestSize is the expected size of a BIP340 message digest.
	MessageDigestSize = 32
)

// Predefined errors for MuSig2 operations
var (
	ErrInvalidPrivateKey       = errors.New("invalid private key")
	ErrInvalidPublicKey        = errors.New("invalid public key")
	ErrInvalidNonceSize        = errors.New("invalid nonce size")
	ErrInvalidSignatureSize    = errors.New("invalid signature size")
	ErrInvalidMessageSize      = errors.New("invalid message size")
	ErrInvalidParticipantIndex = errors.New("invalid participant index")
	ErrSelfSigning             = errors.New("cannot sign with ourselves")
	ErrInsufficientSigners     = errors.New("insufficient signers for 2-of-3 MuSig2")
	ErrSignatureVerification   = errors.New("signature verification failed")
	ErrMissingNonces           = errors.New("don't have all nonces yet")
	ErrKeyMismatch             = errors.New("private key does not match any of the configured public keys")
)

// MuSig2Manager defines the interface for MuSig2 operations
type MuSig2Manager interface {
	// GetParticipantIndex returns the participant index
	GetParticipantIndex() int
	// GetPublicKey returns the public key for this participant
	GetPublicKey() *btcec.PublicKey
	// GetAllPublicKeys returns all configured public keys
	GetAllPublicKeys() []*btcec.PublicKey
	// NewSigningSession creates a new signing session for a message with another participant
	NewSigningSession(message []byte, otherParticipantIndex int) (MuSig2SigningSession, error)
}

// MuSig2SigningSession defines the interface for MuSig2 signing sessions
type MuSig2SigningSession interface {
	// GetPublicNonce returns the public nonce for this session
	GetPublicNonce() []byte
	// RegisterOtherNonce registers the other party's nonce in this session
	RegisterOtherNonce(otherNonce []byte) error
	// CreatePartialSignature creates a partial signature (requires other nonce to be registered first)
	CreatePartialSignature() ([]byte, error)
	// GetManager returns the manager for this session
	GetManager() MuSig2Manager
	// GetOtherIndex returns the index of the other participant
	GetOtherIndex() int
}

// Manager handles MuSig2 operations
type Manager struct {
	privateKey       *btcec.PrivateKey
	publicKeys       []*btcec.PublicKey
	participantIndex int
}

// Ensure Manager implements MuSig2Manager interface
var _ MuSig2Manager = (*Manager)(nil)

// NewManager creates a new MuSig2 manager with the provided private key
func NewManager(privateKey *btcec.PrivateKey) (*Manager, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("private key cannot be nil: %w", ErrInvalidPrivateKey)
	}

	// Validate private key
	if privateKey == nil || privateKey.Key.IsZero() {
		return nil, fmt.Errorf("invalid private key: %w", ErrInvalidPrivateKey)
	}

	// Load public keys from protocol params
	if len(params.MuSig2PublicKeys) != TotalSigners {
		return nil, fmt.Errorf("expected %d MuSig2 public keys, got %d: %w", TotalSigners, len(params.MuSig2PublicKeys), ErrInsufficientSigners)
	}

	publicKeys := make([]*btcec.PublicKey, len(params.MuSig2PublicKeys))
	participantIndex := -1

	for i, pubKeyHex := range params.MuSig2PublicKeys {
		if pubKeyHex == "" {
			return nil, fmt.Errorf("empty public key at index %d: %w", i, ErrInvalidPublicKey)
		}

		pubKeyBytes, err := hex.DecodeString(pubKeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid public key hex at index %d: %w", i, err)
		}

		pubKey, err := btcec.ParsePubKey(pubKeyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key at index %d: %w", i, err)
		}

		publicKeys[i] = pubKey

		// Check if this matches our private key
		if pubKey.IsEqual(privateKey.PubKey()) {
			participantIndex = i
		}
	}

	if participantIndex == -1 {
		return nil, fmt.Errorf("private key does not match any of the configured public keys: %w", ErrKeyMismatch)
	}

	return &Manager{
		privateKey:       privateKey,
		publicKeys:       publicKeys,
		participantIndex: participantIndex,
	}, nil
}

// SigningSession represents an active signing session
type SigningSession struct {
	manager    *Manager
	context    *musig2.Context
	session    *musig2.Session
	msgHash    [32]byte
	otherIndex int
	partialSig *musig2.PartialSignature
}

// Ensure SigningSession implements MuSig2SigningSession interface
var _ MuSig2SigningSession = (*SigningSession)(nil)

// sortKeys sorts public keys lexicographically by their compressed serialization
func sortKeys(keys []*btcec.PublicKey) []*btcec.PublicKey {
	sorted := make([]*btcec.PublicKey, len(keys))
	copy(sorted, keys)

	sort.Slice(sorted, func(i, j int) bool {
		return strings.Compare(
			hex.EncodeToString(sorted[i].SerializeCompressed()),
			hex.EncodeToString(sorted[j].SerializeCompressed()),
		) < 0
	})

	return sorted
}

// NewSigningSession creates a new signing session for a message with another participant
func (m *Manager) NewSigningSession(message []byte, otherParticipantIndex int) (MuSig2SigningSession, error) {
	// Validate message digest length (expect 32-byte BIP340 digest)
	if len(message) != MessageDigestSize {
		return nil, fmt.Errorf("message digest must be %d bytes, got %d: %w", MessageDigestSize, len(message), ErrInvalidMessageSize)
	}

	// Validate participant index
	if otherParticipantIndex < 0 || otherParticipantIndex >= len(m.publicKeys) {
		return nil, fmt.Errorf("invalid other participant index %d: %w", otherParticipantIndex, ErrInvalidParticipantIndex)
	}

	if otherParticipantIndex == m.participantIndex {
		return nil, fmt.Errorf("cannot sign with ourselves: %w", ErrSelfSigning)
	}

	// Get the two signing keys in a consistent order
	// Always put the lower index first to ensure both parties use the same ordering
	var signers []*btcec.PublicKey
	if m.participantIndex < otherParticipantIndex {
		signers = []*btcec.PublicKey{
			m.publicKeys[m.participantIndex],
			m.publicKeys[otherParticipantIndex],
		}
	} else {
		signers = []*btcec.PublicKey{
			m.publicKeys[otherParticipantIndex],
			m.publicKeys[m.participantIndex],
		}
	}

	// Don't sort keys - maintain deterministic ordering based on participant indices
	// This ensures both parties use the same key order

	// Create MuSig2 context without sorting
	musigCtx, err := musig2.NewContext(
		m.privateKey,
		false, // don't sort keys
		musig2.WithKnownSigners(signers),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create MuSig2 context: %w", err)
	}

	// Create session
	session, err := musigCtx.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Treat message as already-digested (32 bytes)
	var msgHash [32]byte
	copy(msgHash[:], message)

	return &SigningSession{
		manager:    m,
		context:    musigCtx,
		session:    session,
		msgHash:    msgHash,
		otherIndex: otherParticipantIndex,
	}, nil
}

// GetPublicNonce returns the public nonce for this session
func (s *SigningSession) GetPublicNonce() []byte {
	nonce := s.session.PublicNonce()
	return nonce[:]
}

// RegisterOtherNonce registers the other party's nonce in this session
func (s *SigningSession) RegisterOtherNonce(otherNonce []byte) error {
	if len(otherNonce) != NonceSize {
		return fmt.Errorf("invalid nonce size: expected %d, got %d: %w", NonceSize, len(otherNonce), ErrInvalidNonceSize)
	}

	// Parse other nonce
	var otherPubNonce [66]byte
	copy(otherPubNonce[:], otherNonce)

	// Register the other party's nonce
	haveAllNonces, err := s.session.RegisterPubNonce(otherPubNonce)
	if err != nil {
		return fmt.Errorf("failed to register other nonce: %w", err)
	}

	if !haveAllNonces {
		return fmt.Errorf("don't have all nonces yet: %w", ErrMissingNonces)
	}

	return nil
}

// CreatePartialSignature creates a partial signature (requires other nonce to be registered first)
func (s *SigningSession) CreatePartialSignature() ([]byte, error) {
	// Sign the message
	partialSig, err := s.session.Sign(s.msgHash)
	if err != nil {
		return nil, fmt.Errorf("failed to create partial signature: %w", err)
	}

	// Store the partial signature for later use
	s.partialSig = partialSig

	// Return the S value bytes (32 bytes)
	sBytes := partialSig.S.Bytes()
	return sBytes[:], nil
}

// CombinePartialSignatures combines partial signatures into a final Schnorr signature
// Note: The session must have already called CreatePartialSignature() which stores our signature internally.
// The ourPartialSig parameter is included for API consistency but is not used since the session already has it.
func CombinePartialSignatures(session *SigningSession, ourPartialSig, theirPartialSig []byte) ([]byte, error) {
	// Validate input signatures
	if len(ourPartialSig) == 0 {
		return nil, fmt.Errorf("our partial signature cannot be empty: %w", ErrInvalidSignatureSize)
	}
	if len(theirPartialSig) == 0 {
		return nil, fmt.Errorf("their partial signature cannot be empty: %w", ErrInvalidSignatureSize)
	}

	// The session must have already created its own partial signature
	if session.partialSig == nil {
		return nil, fmt.Errorf("session has not created its own partial signature yet")
	}

	// Create PartialSignature object from the other party's signature
	// The R value should be nil for partial signatures when combining
	theirSig := &musig2.PartialSignature{
		S: new(btcec.ModNScalar),
	}
	theirSig.S.SetByteSlice(theirPartialSig)

	// The session already has our signature internally from CreatePartialSignature(), so we just need to add theirs
	haveAllSigs, err := session.session.CombineSig(theirSig)
	if err != nil {
		return nil, fmt.Errorf("failed to combine signatures: %w", err)
	}

	if !haveAllSigs {
		return nil, fmt.Errorf("don't have all signatures yet: %w", ErrMissingNonces)
	}

	// Get final signature
	finalSig := session.session.FinalSig()
	// Schnorr signatures are 64 bytes (R || S)
	return finalSig.Serialize(), nil
}

// VerifyCompositeSignature verifies a composite Schnorr signature
func VerifyCompositeSignature(message []byte, signature []byte, signerIndices []int) error {
	// Validate message digest length
	if len(message) != MessageDigestSize {
		return fmt.Errorf("message digest must be %d bytes, got %d: %w", MessageDigestSize, len(message), ErrInvalidMessageSize)
	}

	// Validate signature
	if len(signature) != SignatureSize {
		return fmt.Errorf("invalid signature length: expected %d, got %d: %w", SignatureSize, len(signature), ErrInvalidSignatureSize)
	}

	// Validate signer indices
	if len(signerIndices) != RequiredSigners {
		return fmt.Errorf("exactly %d signers required for 2-of-3 MuSig2, got %d: %w", RequiredSigners, len(signerIndices), ErrInsufficientSigners)
	}

	// Load public keys from protocol params
	if len(params.MuSig2PublicKeys) != TotalSigners {
		return fmt.Errorf("expected %d MuSig2 public keys, got %d: %w", TotalSigners, len(params.MuSig2PublicKeys), ErrInsufficientSigners)
	}

	publicKeys := make([]*btcec.PublicKey, len(params.MuSig2PublicKeys))
	for i, pubKeyHex := range params.MuSig2PublicKeys {
		if pubKeyHex == "" {
			return fmt.Errorf("empty public key at index %d: %w", i, ErrInvalidPublicKey)
		}

		pubKeyBytes, err := hex.DecodeString(pubKeyHex)
		if err != nil {
			return fmt.Errorf("invalid public key hex at index %d: %w", i, err)
		}

		pubKey, err := btcec.ParsePubKey(pubKeyBytes)
		if err != nil {
			return fmt.Errorf("failed to parse public key at index %d: %w", i, err)
		}
		publicKeys[i] = pubKey
	}

	// Get the signing public keys
	signingKeys := make([]*btcec.PublicKey, len(signerIndices))
	for i, idx := range signerIndices {
		if idx < 0 || idx >= len(publicKeys) {
			return fmt.Errorf("invalid signer index %d: %w", idx, ErrInvalidParticipantIndex)
		}
		signingKeys[i] = publicKeys[idx]
	}

	// Don't sort keys - use the same ordering as in NewSigningSession
	// This ensures consistency between signing and verification

	// Aggregate the signing public keys
	aggregatedKey, _, _, err := musig2.AggregateKeys(signingKeys, false)
	if err != nil {
		return fmt.Errorf("failed to aggregate signing keys: %w", err)
	}

	// Parse the signature
	sig, err := schnorr.ParseSignature(signature)
	if err != nil {
		return fmt.Errorf("failed to parse signature: %w", err)
	}

	// Verify: message is already a 32-byte digest
	if !sig.Verify(message, aggregatedKey.FinalKey) {
		return fmt.Errorf("signature verification failed: %w", ErrSignatureVerification)
	}

	return nil
}

// GetParticipantIndex returns the participant index
func (m *Manager) GetParticipantIndex() int {
	return m.participantIndex
}

// GetPublicKey returns the public key for this participant
func (m *Manager) GetPublicKey() *btcec.PublicKey {
	return m.privateKey.PubKey()
}

// GetAllPublicKeys returns all configured public keys
func (m *Manager) GetAllPublicKeys() []*btcec.PublicKey {
	return m.publicKeys
}

// GetManager returns the manager for this session
func (s *SigningSession) GetManager() MuSig2Manager {
	return s.manager
}

// GetOtherIndex returns the index of the other participant
func (s *SigningSession) GetOtherIndex() int {
	return s.otherIndex
}
