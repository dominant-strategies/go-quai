package node

import (
	"crypto/rand"
	"os"

	"github.com/dominant-strategies/go-quai/config"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/spf13/viper"
)

// Returns the private key stored in the file to be used to generate
// the node's identity (ID)
func getPrivKey() (crypto.PrivKey, error) {
	// check if private key file exists
	privateKeyFilePath := viper.GetString(config.CONFIG_DIR) + config.PRIVATE_KEY_FILENAME
	log.Debugf("looking for private key file at %s", privateKeyFilePath)
	_, err := os.Stat(privateKeyFilePath)
	if err != nil {
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
		err = os.WriteFile(privateKeyFilePath, privateKeyBytes, 0600)
		if err != nil {
			log.Errorf("error saving private key: %s", err)
			return nil, err
		}
		log.Infof("new private key saved to %s", privateKeyFilePath)
	} else {
		log.Debugf("private key file found at %s", privateKeyFilePath)
	}

	// load private key
	privateKeyBytes, err := os.ReadFile(privateKeyFilePath)
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
