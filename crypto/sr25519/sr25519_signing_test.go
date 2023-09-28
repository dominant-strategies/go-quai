package sr25519

import (
	"crypto/rand"
	"errors"
	"fmt"
	"testing"

	bip39 "github.com/cosmos/go-bip39"
	"github.com/gtank/merlin"
	"github.com/stretchr/testify/require"
)

func TestNewKeypairFromSeed(t *testing.T) {
	seed := make([]byte, 32)
	_, err := rand.Read(seed)
	require.NoError(t, err)

	kp, err := NewKeypairFromSeed(seed)
	require.NoError(t, err)
	require.NotNil(t, kp.public)
	require.NotNil(t, kp.private)

	seed = make([]byte, 20)
	_, err = rand.Read(seed)
	require.NoError(t, err)
	kp, err = NewKeypairFromSeed(seed)
	require.Nil(t, kp)
	require.Error(t, err, "cannot generate key from seed: seed is not 32 bytes long")
}

func TestSignAndVerify(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)

	msg := []byte("helloworld")
	sig, err := kp.Sign(msg)
	require.NoError(t, err)

	pub := kp.Public().(*PublicKey)
	ok, err := pub.Verify(msg, sig)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestPublicKeys(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)

	priv := kp.Private().(*PrivateKey)
	kp2, err := NewKeypair(priv.key)
	require.NoError(t, err)
	require.Equal(t, kp.Public(), kp2.Public())
}

func TestEncodeAndDecodePrivateKey(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)

	enc := kp.Private().Encode()
	res := new(PrivateKey)
	err = res.Decode(enc)
	require.NoError(t, err)

	exp := kp.Private().(*PrivateKey).key.Encode()
	require.Equal(t, exp, res.key.Encode())
}

func TestEncodeAndDecodePublicKey(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)

	enc := kp.Public().Encode()
	res := new(PublicKey)
	err = res.Decode(enc)
	require.NoError(t, err)

	exp := kp.Public().(*PublicKey).key.Encode()
	require.Equal(t, exp, res.key.Encode())
}

func TestVrfSignAndVerify(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)

	transcript := merlin.NewTranscript("helloworld")
	out, proof, err := kp.VrfSign(transcript)
	require.NoError(t, err)

	pub := kp.Public().(*PublicKey)
	transcript2 := merlin.NewTranscript("helloworld")
	ok, err := pub.VrfVerify(transcript2, out, proof)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestSignAndVerify_Deprecated(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)

	msg := []byte("helloworld")
	sig, err := kp.Sign(msg)
	require.NoError(t, err)

	pub := kp.Public().(*PublicKey)
	address := pub.Address()
	fmt.Println("address", address)
	ok, err := pub.VerifyDeprecated(msg, sig)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestNewKeypairFromMnenomic(t *testing.T) {
	entropy, err := bip39.NewEntropy(128)
	require.NoError(t, err)

	mnemonic, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)

	_, err = NewKeypairFromMnenomic(mnemonic, "")
	require.NoError(t, err)
}

func TestVerifySignature(t *testing.T) {
	t.Parallel()

	keypair, err := GenerateKeypair()
	require.NoError(t, err)

	publicKey := keypair.public.Encode()

	message := []byte("Hello world!")

	signature, err := keypair.Sign(message)
	require.NoError(t, err)

	testCase := map[string]struct {
		publicKey, signature, message []byte
		err                           error
	}{
		"success": {
			publicKey: publicKey,
			signature: signature,
			message:   message,
		},
		"bad_public_key_input": {
			publicKey: []byte{},
			signature: signature,
			message:   message,
			err:       errors.New("sr25519: cannot create public key: input is not 32 bytes"),
		},
		"invalid_signature_length": {
			publicKey: publicKey,
			signature: []byte{},
			message:   message,
			err:       fmt.Errorf("sr25519: invalid signature length"),
		},
		"verification_failed": {
			publicKey: publicKey,
			signature: signature,
			message:   []byte("a225e8c75da7da319af6335e7642d473"),
			err: fmt.Errorf("sr25519: %w: for message 0x%x, signature 0x%x and public key 0x%x",
				errors.New("failed to verify signature"), []byte("a225e8c75da7da319af6335e7642d473"), signature, publicKey),
		},
	}

	for name, value := range testCase {
		testCase := value
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := VerifySignature(testCase.publicKey, testCase.signature, testCase.message)

			if testCase.err != nil {
				require.EqualError(t, err, testCase.err.Error())
				return
			}
			require.NoError(t, err)
		})
	}

}
