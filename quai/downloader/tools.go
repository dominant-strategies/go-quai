package downloader

import (
	"bytes"

	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/trie"
)

// VerifyNodeHash verifies a expected hash against the RLP encoding of the received trie node
func verifyNodeHash(recvd *trie.TrieNodeResponse, expectedHash []byte) bool {
	// compare received hash with expected hash
	receivedHash := recvd.NodeHash.Bytes()
	if !bytes.Equal(receivedHash, expectedHash) {
		return false
	}
	calculatedHash := crypto.Keccak256(recvd.NodeData)
	return bytes.Equal(calculatedHash, expectedHash)
}
