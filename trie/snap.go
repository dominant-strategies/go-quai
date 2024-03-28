package trie

import (
	"github.com/dominant-strategies/go-quai/common"
)

// Used to send a trie node request to a peer
type TrieNodeRequest struct{}

// Used to get a trie node response from a peer
type TrieNodeResponse struct {
	NodeData []byte
	NodeHash common.Hash
}

// GetTrieNode returns the trie node from a TrieNodeResponse
func (t *TrieNodeResponse) GetTrieNode() *TrieNode {
	node, err := decodeNode(t.NodeHash[:], t.NodeData)
	if err != nil {
		panic(err)
	}
	return &TrieNode{n: node}
}

// TrieNode is a public wrapper around a trie node that exposes
// methods for handling trie nodes.
type TrieNode struct {
	n node
}

// ChildHashes returns the hashes of the children of a fullNode trie node
func (t *TrieNode) ChildHashes() []common.Hash {
	switch n := t.n.(type) {
	case *fullNode:
		hashes := make([]common.Hash, 0, 17)
		for _, child := range n.Children {
			if child != nil {
				// child MUST be a hashNode
				hn, _ := child.(hashNode)
				hashes = append(hashes, common.BytesToHash(hn))
			}
		}
		return hashes
	default:
		return nil
	}
}

// IsFullNode returns true if the trie node is a fullNode
func (t *TrieNode) IsFullNode() bool {
	_, ok := t.n.(*fullNode)
	return ok
}
