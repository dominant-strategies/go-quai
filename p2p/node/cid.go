package node

import (
	"bufio"
	"crypto/rand"
	"os"

	"github.com/dominant-strategies/go-quai/log"

	"github.com/libp2p/go-libp2p/core/crypto"
)

const (
	// file to store node's CID and listening address
	nodeInfoFile = "node.info"
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

// Utility function that synchronously writes the provided "info" string to the node.info file.
// If the file doesn't exist, it creates it. Otherwise, it appends the new "info" as a new line.
func saveNodeInfo(info string) {
	go func() {
		// Open file with O_APPEND flag to append data to the file or create the file if it doesn't exist.
		f, err := os.OpenFile(nodeInfoFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Errorf("error opening node info file: %s", err)
			return
		}
		defer f.Close()

		// Use bufio for efficient writing
		writer := bufio.NewWriter(f)
		defer writer.Flush()

		// Append new line and write to file
		writer.WriteString(info + "\n")
	}()
}

// utility function used to delete any existing node info file
func deleteNodeInfoFile() error {
	if _, err := os.Stat(nodeInfoFile); !os.IsNotExist(err) {
		err := os.Remove(nodeInfoFile)
		if err != nil {
			return err
		}
	}
	return nil
}
