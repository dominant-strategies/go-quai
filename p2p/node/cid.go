package node

import (
	"crypto/rand"
	"os"

	"github.com/dominant-strategies/go-quai/log"

	"github.com/libp2p/go-libp2p/core/crypto"
)

// Returns the private key stored in the file to be used to generate
// the node's identity (CID)
func getPrivKey(privKeyFile string) (crypto.PrivKey, error) {
	// check if there is a file with the private key. It not, create one
	_, err := os.Stat(privKeyFile)
	if os.IsNotExist(err) {
		log.Warnf("private key file not found. Generating new private key...")
		// generate private key
		privateKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			log.Errorf("error generating private key: %s", err)
			return nil, err

		}

		// save private key
		privateKeyBytes, err := crypto.MarshalPrivateKey(privateKey)
		if err != nil {
			log.Errorf("error marshalling private key: %s", err)
			return nil, err
		}
		err = os.WriteFile(privKeyFile, privateKeyBytes, 0600)
		if err != nil {
			log.Errorf("error saving private key: %s", err)
			return nil, err
		}
		log.Warnf(" new private key saved to %s", privKeyFile)
	}

	// load private key
	privateKeyBytes, err := os.ReadFile(privKeyFile)
	if err != nil {
		log.Errorf("error reading private key: %s", err)
		return nil, err
	}
	privateKey, err := crypto.UnmarshalPrivateKey(privateKeyBytes)
	if err != nil {
		log.Errorf("error unmarshalling private key: %s", err)
		return nil, err
	}
	return privateKey, nil

}
