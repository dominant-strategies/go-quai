package musig2

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants
const (
	testMessage  = "test message for musig2 signing"
	testPrivKey0 = "6c8adb7ffbcb3a819bd54ef44734f73977c1e63ef930f8598993917d980e214a"
	testPrivKey1 = "23ff6833b48e876dad7a4669907013a0cc89a0dc793d5728a605754da067e4f6"
	testPrivKey2 = "a1b2c3d4e5f6789012345678901234567890123456789012345678901234567890"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, 66, NonceSize)
	assert.Equal(t, 64, SignatureSize)
	assert.Equal(t, 2, RequiredSigners)
	assert.Equal(t, 3, TotalSigners)
	assert.Equal(t, 1024*1024, MaxMessageSize)
}

func TestErrors(t *testing.T) {
	// Test that all predefined errors are not nil
	assert.NotNil(t, ErrInvalidPrivateKey)
	assert.NotNil(t, ErrInvalidPublicKey)
	assert.NotNil(t, ErrInvalidNonceSize)
	assert.NotNil(t, ErrInvalidSignatureSize)
	assert.NotNil(t, ErrInvalidMessageSize)
	assert.NotNil(t, ErrInvalidParticipantIndex)
	assert.NotNil(t, ErrSelfSigning)
	assert.NotNil(t, ErrInsufficientSigners)
	assert.NotNil(t, ErrSignatureVerification)
	assert.NotNil(t, ErrMissingNonces)
	assert.NotNil(t, ErrKeyMismatch)
}

func TestNewManager(t *testing.T) {
	t.Run("ValidPrivateKey", func(t *testing.T) {
		// Create a test private key
		privKeyBytes, err := hex.DecodeString(testPrivKey0)
		require.NoError(t, err)
		privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

		// Mock the protocol params for testing
		originalKeys := params.MuSig2PublicKeys
		params.MuSig2PublicKeys = []string{
			"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
			"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
			"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
		}
		defer func() { params.MuSig2PublicKeys = originalKeys }()

		manager, err := NewManager(privKey)
		require.NoError(t, err)
		require.NotNil(t, manager)
		assert.Equal(t, 0, manager.GetParticipantIndex())
		assert.Equal(t, privKey.PubKey(), manager.GetPublicKey())
		assert.Len(t, manager.GetAllPublicKeys(), 3)
	})

	t.Run("NilPrivateKey", func(t *testing.T) {
		manager, err := NewManager(nil)
		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "private key cannot be nil")
	})

	t.Run("InvalidPrivateKey", func(t *testing.T) {
		// Create an invalid private key
		invalidPrivKey := &btcec.PrivateKey{}
		manager, err := NewManager(invalidPrivKey)
		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "invalid private key")
	})

	t.Run("KeyMismatch", func(t *testing.T) {
		// Create a private key that doesn't match any of the protocol keys
		privKeyBytes, err := hex.DecodeString(testPrivKey2)
		require.NoError(t, err)
		privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

		// Mock the protocol params for testing
		originalKeys := params.MuSig2PublicKeys
		params.MuSig2PublicKeys = []string{
			"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
			"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
			"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
		}
		defer func() { params.MuSig2PublicKeys = originalKeys }()

		manager, err := NewManager(privKey)
		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "private key does not match")
	})

	t.Run("InsufficientKeys", func(t *testing.T) {
		privKeyBytes, err := hex.DecodeString(testPrivKey0)
		require.NoError(t, err)
		privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

		// Mock insufficient keys
		originalKeys := params.MuSig2PublicKeys
		params.MuSig2PublicKeys = []string{
			"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
		}
		defer func() { params.MuSig2PublicKeys = originalKeys }()

		manager, err := NewManager(privKey)
		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "expected 3 MuSig2 public keys")
	})
}

func TestNewSigningSession(t *testing.T) {
	// Setup test manager
	privKeyBytes, err := hex.DecodeString(testPrivKey0)
	require.NoError(t, err)
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

	// Mock the protocol params for testing
	originalKeys := params.MuSig2PublicKeys
	params.MuSig2PublicKeys = []string{
		"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
		"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
		"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
	}
	defer func() { params.MuSig2PublicKeys = originalKeys }()

	manager, err := NewManager(privKey)
	require.NoError(t, err)

	message := sha256.Sum256([]byte(testMessage))

	t.Run("ValidSession", func(t *testing.T) {
		session, err := manager.NewSigningSession(message[:], 1)
		require.NoError(t, err)
		require.NotNil(t, session)
		assert.Equal(t, manager, session.GetManager())
		assert.Equal(t, 1, session.GetOtherIndex())
		assert.Len(t, session.GetPublicNonce(), NonceSize)
	})

	t.Run("EmptyMessage", func(t *testing.T) {
		session, err := manager.NewSigningSession([]byte{}, 1)
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "message digest must be 32 bytes")
	})

	t.Run("MessageTooLarge", func(t *testing.T) {
		largeMessage := make([]byte, MaxMessageSize+1)
		session, err := manager.NewSigningSession(largeMessage, 1)
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "message size")
	})

	t.Run("InvalidParticipantIndex", func(t *testing.T) {
		session, err := manager.NewSigningSession(message[:], -1)
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "invalid other participant index")

		session, err = manager.NewSigningSession(message[:], 3)
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "invalid other participant index")
	})

	t.Run("SelfSigning", func(t *testing.T) {
		session, err := manager.NewSigningSession(message[:], 0)
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "cannot sign with ourselves")
	})
}

func TestCreatePartialSignature(t *testing.T) {
	// Setup two managers for testing
	privKey0Bytes, err := hex.DecodeString(testPrivKey0)
	require.NoError(t, err)
	privKey0, _ := btcec.PrivKeyFromBytes(privKey0Bytes)

	privKey1Bytes, err := hex.DecodeString(testPrivKey1)
	require.NoError(t, err)
	privKey1, _ := btcec.PrivKeyFromBytes(privKey1Bytes)

	// Mock the protocol params for testing
	originalKeys := params.MuSig2PublicKeys
	params.MuSig2PublicKeys = []string{
		"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
		"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
		"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
	}
	defer func() { params.MuSig2PublicKeys = originalKeys }()

	manager0, err := NewManager(privKey0)
	require.NoError(t, err)

	manager1, err := NewManager(privKey1)
	require.NoError(t, err)

	message := sha256.Sum256([]byte(testMessage))

	// Create sessions
	session0, err := manager0.NewSigningSession(message[:], 1)
	require.NoError(t, err)

	session1, err := manager1.NewSigningSession(message[:], 0)
	require.NoError(t, err)

	t.Run("ValidPartialSignature", func(t *testing.T) {
		// Get the real nonce from the other session
		validNonce := session1.GetPublicNonce()

		// First register the other party's nonce
		err := session0.RegisterOtherNonce(validNonce)
		require.NoError(t, err)

		// Then create the partial signature
		partialSig, err := session0.CreatePartialSignature()
		require.NoError(t, err)
		require.NotNil(t, partialSig)
		assert.Len(t, partialSig, 32) // S value is 32 bytes
	})

	t.Run("InvalidNonceSize", func(t *testing.T) {
		invalidNonce := make([]byte, 32) // Wrong size
		err := session0.RegisterOtherNonce(invalidNonce)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid nonce size")
	})

	t.Run("EmptyNonce", func(t *testing.T) {
		err := session0.RegisterOtherNonce([]byte{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid nonce size")
	})
}

func TestCombinePartialSignatures(t *testing.T) {
	// Setup two managers for testing
	privKey0Bytes, err := hex.DecodeString(testPrivKey0)
	require.NoError(t, err)
	privKey0, _ := btcec.PrivKeyFromBytes(privKey0Bytes)

	privKey1Bytes, err := hex.DecodeString(testPrivKey1)
	require.NoError(t, err)
	privKey1, _ := btcec.PrivKeyFromBytes(privKey1Bytes)

	// Mock the protocol params for testing
	originalKeys := params.MuSig2PublicKeys
	params.MuSig2PublicKeys = []string{
		"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
		"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
		"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
	}
	defer func() { params.MuSig2PublicKeys = originalKeys }()

	manager0, err := NewManager(privKey0)
	require.NoError(t, err)

	manager1, err := NewManager(privKey1)
	require.NoError(t, err)

	message := sha256.Sum256([]byte(testMessage))

	// Create sessions
	session0, err := manager0.NewSigningSession(message[:], 1)
	require.NoError(t, err)

	session1, err := manager1.NewSigningSession(message[:], 0)
	require.NoError(t, err)

	// Exchange nonces
	nonce0 := session0.GetPublicNonce()
	nonce1 := session1.GetPublicNonce()

	// Register nonces in both sessions
	err = session0.RegisterOtherNonce(nonce1)
	require.NoError(t, err)

	err = session1.RegisterOtherNonce(nonce0)
	require.NoError(t, err)

	// Create partial signatures
	partialSig0, err := session0.CreatePartialSignature()
	require.NoError(t, err)

	partialSig1, err := session1.CreatePartialSignature()
	require.NoError(t, err)

	t.Run("ValidCombination", func(t *testing.T) {
		// Test that we can create partial signatures successfully
		// The actual combination will be tested in the full flow test
		require.NotNil(t, partialSig0)
		require.NotNil(t, partialSig1)
		assert.Len(t, partialSig0, 32)
		assert.Len(t, partialSig1, 32)
	})

	t.Run("EmptyOurSignature", func(t *testing.T) {
		session0Concrete := session0.(*SigningSession)
		finalSig, err := CombinePartialSignatures(session0Concrete, []byte{}, partialSig1)
		assert.Error(t, err)
		assert.Nil(t, finalSig)
		assert.Contains(t, err.Error(), "our partial signature cannot be empty")
	})

	t.Run("EmptyTheirSignature", func(t *testing.T) {
		session0Concrete := session0.(*SigningSession)
		finalSig, err := CombinePartialSignatures(session0Concrete, partialSig0, []byte{})
		assert.Error(t, err)
		assert.Nil(t, finalSig)
		assert.Contains(t, err.Error(), "their partial signature cannot be empty")
	})
}

func TestVerifyCompositeSignature(t *testing.T) {
	// Setup test data
	message := []byte(testMessage)
	msgHash := sha256.Sum256(message)

	// Create test keys
	privKey0Bytes, err := hex.DecodeString(testPrivKey0)
	require.NoError(t, err)
	privKey0, _ := btcec.PrivKeyFromBytes(privKey0Bytes)

	privKey1Bytes, err := hex.DecodeString(testPrivKey1)
	require.NoError(t, err)
	privKey1, _ := btcec.PrivKeyFromBytes(privKey1Bytes)

	// Create a valid signature using the underlying musig2 library
	signers := []*btcec.PublicKey{privKey0.PubKey(), privKey1.PubKey()}
	ctx0, err := musig2.NewContext(privKey0, false, musig2.WithKnownSigners(signers))
	require.NoError(t, err)

	ctx1, err := musig2.NewContext(privKey1, false, musig2.WithKnownSigners(signers))
	require.NoError(t, err)

	session0, err := ctx0.NewSession()
	require.NoError(t, err)

	session1, err := ctx1.NewSession()
	require.NoError(t, err)

	nonce0 := session0.PublicNonce()
	nonce1 := session1.PublicNonce()

	_, err = session0.RegisterPubNonce(nonce1)
	require.NoError(t, err)

	_, err = session1.RegisterPubNonce(nonce0)
	require.NoError(t, err)

	_, err = session0.Sign(msgHash)
	require.NoError(t, err)

	partialSig1, err := session1.Sign(msgHash)
	require.NoError(t, err)

	// Combine the other party's signature (our own is already in the session)
	_, err = session0.CombineSig(partialSig1)
	require.NoError(t, err)

	validSignature := session0.FinalSig().Serialize()

	// Mock the protocol params for testing
	originalKeys := params.MuSig2PublicKeys
	params.MuSig2PublicKeys = []string{
		"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
		"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
		"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
	}
	defer func() { params.MuSig2PublicKeys = originalKeys }()

	t.Run("ValidSignature", func(t *testing.T) {
		err := VerifyCompositeSignature(msgHash[:], validSignature, []int{0, 1})
		assert.NoError(t, err)
	})

	t.Run("EmptyMessage", func(t *testing.T) {
		err := VerifyCompositeSignature([]byte{}, validSignature, []int{0, 1})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "message digest must be 32 bytes")
	})

	t.Run("MessageTooLarge", func(t *testing.T) {
		largeMessage := make([]byte, MaxMessageSize+1)
		err := VerifyCompositeSignature(largeMessage, validSignature, []int{0, 1})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "message size")
	})

	t.Run("InvalidSignatureSize", func(t *testing.T) {
		invalidSig := make([]byte, 32) // Wrong size
		err := VerifyCompositeSignature(msgHash[:], invalidSig, []int{0, 1})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid signature length")
	})

	t.Run("InvalidSignerCount", func(t *testing.T) {
		err := VerifyCompositeSignature(msgHash[:], validSignature, []int{0})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exactly 2 signers required")
	})

	t.Run("InvalidSignerIndex", func(t *testing.T) {
		err := VerifyCompositeSignature(msgHash[:], validSignature, []int{0, 3})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid signer index")
	})

	t.Run("InvalidSignature", func(t *testing.T) {
		invalidSig := make([]byte, SignatureSize)
		for i := range invalidSig {
			invalidSig[i] = 0xFF
		}
		err := VerifyCompositeSignature(msgHash[:], invalidSig, []int{0, 1})
		assert.Error(t, err)
		// The error could be either parsing failure or verification failure
		assert.True(t, strings.Contains(err.Error(), "failed to parse signature") ||
			strings.Contains(err.Error(), "signature verification failed"),
			"Error should mention either parsing or verification failure")
	})
}

func TestInterfaceCompliance(t *testing.T) {
	// Test that Manager implements MuSig2Manager interface
	var _ MuSig2Manager = (*Manager)(nil)

	// Test that SigningSession implements MuSig2SigningSession interface
	var _ MuSig2SigningSession = (*SigningSession)(nil)
}

func TestFullSigningFlow(t *testing.T) {
	// This test demonstrates the complete MuSig2 signing flow
	message := sha256.Sum256([]byte("complete musig2 signing test"))

	// Setup two participants
	privKey0Bytes, err := hex.DecodeString(testPrivKey0)
	require.NoError(t, err)
	privKey0, _ := btcec.PrivKeyFromBytes(privKey0Bytes)

	privKey1Bytes, err := hex.DecodeString(testPrivKey1)
	require.NoError(t, err)
	privKey1, _ := btcec.PrivKeyFromBytes(privKey1Bytes)

	// Mock the protocol params for testing
	originalKeys := params.MuSig2PublicKeys
	params.MuSig2PublicKeys = []string{
		"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
		"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
		"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
	}
	defer func() { params.MuSig2PublicKeys = originalKeys }()

	// Create managers
	manager0, err := NewManager(privKey0)
	require.NoError(t, err)

	manager1, err := NewManager(privKey1)
	require.NoError(t, err)

	// Create signing sessions
	session0, err := manager0.NewSigningSession(message[:], 1)
	require.NoError(t, err)

	session1, err := manager1.NewSigningSession(message[:], 0)
	require.NoError(t, err)

	// Exchange nonces
	nonce0 := session0.GetPublicNonce()
	nonce1 := session1.GetPublicNonce()

	// Register nonces in both sessions
	err = session0.RegisterOtherNonce(nonce1)
	require.NoError(t, err)

	err = session1.RegisterOtherNonce(nonce0)
	require.NoError(t, err)

	// Create partial signatures using the API methods
	partialSig0, err := session0.CreatePartialSignature()
	require.NoError(t, err)
	require.NotNil(t, partialSig0)
	require.Len(t, partialSig0, 32)

	partialSig1, err := session1.CreatePartialSignature()
	require.NoError(t, err)
	require.NotNil(t, partialSig1)
	require.Len(t, partialSig1, 32)

	// Combine signatures properly
	session0Concrete := session0.(*SigningSession)
	finalSig, err := CombinePartialSignatures(session0Concrete, partialSig0, partialSig1)
	require.NoError(t, err)
	require.NotNil(t, finalSig)
	require.Len(t, finalSig, SignatureSize)

	// Verify the signature
	err = VerifyCompositeSignature(message[:], finalSig, []int{0, 1})
	require.NoError(t, err)

	// Additional verification using the underlying library
	// Use same key ordering as in signing (participant index order, not sorted)
	aggKey, _, _, err := musig2.AggregateKeys([]*btcec.PublicKey{privKey0.PubKey(), privKey1.PubKey()}, false)
	require.NoError(t, err)

	sig, err := schnorr.ParseSignature(finalSig)
	require.NoError(t, err)

	valid := sig.Verify(message[:], aggKey.FinalKey)
	assert.True(t, valid, "Signature should be valid")
}

func TestSortKeys(t *testing.T) {
	// Test the sortKeys function
	key1Bytes, _ := hex.DecodeString("0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91")
	key2Bytes, _ := hex.DecodeString("02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068")
	key3Bytes, _ := hex.DecodeString("02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a")

	key1, _ := btcec.ParsePubKey(key1Bytes)
	key2, _ := btcec.ParsePubKey(key2Bytes)
	key3, _ := btcec.ParsePubKey(key3Bytes)

	// Test with different orderings
	keys := []*btcec.PublicKey{key3, key1, key2}
	sorted := sortKeys(keys)

	// Should be sorted lexicographically
	assert.Equal(t, key3, sorted[0]) // 02afc... comes first
	assert.Equal(t, key2, sorted[1]) // 02f68... comes second
	assert.Equal(t, key1, sorted[2]) // 0325f... comes third
}

// Benchmark tests
func BenchmarkNewManager(b *testing.B) {
	privKeyBytes, _ := hex.DecodeString(testPrivKey0)
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

	originalKeys := params.MuSig2PublicKeys
	params.MuSig2PublicKeys = []string{
		"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
		"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
		"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
	}
	defer func() { params.MuSig2PublicKeys = originalKeys }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NewManager(privKey)
	}
}

func BenchmarkNewSigningSession(b *testing.B) {
	privKeyBytes, _ := hex.DecodeString(testPrivKey0)
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

	originalKeys := params.MuSig2PublicKeys
	params.MuSig2PublicKeys = []string{
		"0325f78ada1d0ef80bc17dbf53665b2645a3422881147897b16fb1d4f6f44e3b91",
		"02f6884b696e4eeb46a9ed2c259204a98d083eff72312e2ce145f055b52b55f068",
		"02afc5977973d21ab4d0f682f7d872c35d347699be69910444d61a0bfe42b31c7a",
	}
	defer func() { params.MuSig2PublicKeys = originalKeys }()

	manager, _ := NewManager(privKey)
	message := []byte("benchmark message")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.NewSigningSession(message, 1)
	}
}
