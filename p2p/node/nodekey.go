package node

import (
	"crypto/rand"
	"os"

	"github.com/dominant-strategies/go-quai/cmd/options"
	"github.com/dominant-strategies/go-quai/log"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/spf13/viper"
)

// Returns the private key of the node. If the key does not exist, it is created and saved to disk.
// TODO: consider using a key manager to store the key
func getNodeKey() crypto.PrivKey {
	file := viper.GetString(options.KEYFILE)
	log.Debugf("loading node key from file: %s", file)

	// If key file does not exist, create one with a random key
	_, err := os.Stat(file)
	if os.IsNotExist(err) {
		log.Infof("node key not found.")
		privateKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			log.Fatalf("error generating private key: %s", err)
		}
		privateKeyBytes, err := crypto.MarshalPrivateKey(privateKey)
		if err != nil {
			log.Fatalf("error marshalling private key: %s", err)
		}
		err = os.WriteFile(file, privateKeyBytes, 0600)
		if err != nil {
			log.Fatalf("error saving private key: %s", err)
		}
		log.Infof("saved new node key at %s", file)
	}

	// load private key
	privateKeyBytes, err := os.ReadFile(file)
	if err != nil {
		log.Fatalf("error reading private key: %s", err)
	}
	privateKey, err := crypto.UnmarshalPrivateKey(privateKeyBytes)
	if err != nil {
		log.Fatalf("error unmarshalling private key: %s", err)
	}
	return privateKey
}
