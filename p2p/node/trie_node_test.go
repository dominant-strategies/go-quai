package node

import (
	"context"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	"github.com/dominant-strategies/go-quai/trie"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
)

type stubNetwork struct {
	network.Network
	closed []peer.ID
}

func (n *stubNetwork) ClosePeer(id peer.ID) error {
	n.closed = append(n.closed, id)
	return nil
}

type stubHost struct {
	host.Host
	net *stubNetwork
}

func (h *stubHost) Network() network.Network { return h.net }

type trieTestPeerManager struct {
	missingResponsePeerManager
	banned []peer.ID
	host   *stubHost
}

func newTrieTestPeerManager() *trieTestPeerManager {
	return &trieTestPeerManager{
		host: &stubHost{net: &stubNetwork{}},
	}
}

func (m *trieTestPeerManager) IsProtectedPeer(peer.ID) bool { return false }
func (m *trieTestPeerManager) BanPeer(id peer.ID)           { m.banned = append(m.banned, id) }
func (m *trieTestPeerManager) GetHost() host.Host           { return m.host }

func TestTrieNodeTopicMapping(t *testing.T) {
	genesis := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	loc := common.Location{0, 0}

	singleTopic, err := pubsubManager.NewTopic(genesis, loc, &trie.TrieNodeResponse{})
	require.NoError(t, err)
	require.Equal(t, genesis.String()+"/0,0/blocks", singleTopic.String())

	batchTopic, err := pubsubManager.NewTopic(genesis, loc, &trie.TrieNodesResponse{})
	require.NoError(t, err)
	require.Equal(t, genesis.String()+"/0,0/blocks", batchTopic.String())
}

func TestRequestFromPeerTrieNodeValidation(t *testing.T) {
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, &trie.TrieNodeResponse{})
	require.NoError(t, err)

	blob := []byte("valid trie node payload")
	validHash := crypto.Keccak256Hash(blob)

	t.Run("accepts matching keccak256", func(t *testing.T) {
		pm := newTrieTestPeerManager()
		responses := make(chan interface{}, 1)
		responses <- &trie.TrieNodeResponse{NodeData: blob}
		n := &P2PNode{
			ctx:            context.Background(),
			peerManager:    pm,
			requestManager: &missingResponseRequestManager{result: responses},
			requestSlots:   make(chan struct{}, 1),
		}
		res, err := n.requestFromPeer(peer.ID("trie-valid-peer"), topic, validHash, &trie.TrieNodeResponse{})
		require.NoError(t, err)
		require.Equal(t, &trie.TrieNodeResponse{NodeData: blob}, res)
		require.Empty(t, pm.banned)
	})

	t.Run("bans peer on hash mismatch", func(t *testing.T) {
		pm := newTrieTestPeerManager()
		responses := make(chan interface{}, 1)
		responses <- &trie.TrieNodeResponse{NodeData: []byte("corrupted payload")}
		n := &P2PNode{
			ctx:            context.Background(),
			peerManager:    pm,
			requestManager: &missingResponseRequestManager{result: responses},
			requestSlots:   make(chan struct{}, 1),
		}
		peerID := peer.ID("trie-bad-peer")
		res, err := n.requestFromPeer(peerID, topic, validHash, &trie.TrieNodeResponse{})
		require.Error(t, err)
		require.Nil(t, res)
		require.Equal(t, []peer.ID{peerID}, pm.banned)
	})

	t.Run("missing node returns nil without ban", func(t *testing.T) {
		pm := newTrieTestPeerManager()
		responses := make(chan interface{}, 1)
		responses <- nil
		n := &P2PNode{
			ctx:            context.Background(),
			peerManager:    pm,
			requestManager: &missingResponseRequestManager{result: responses},
			requestSlots:   make(chan struct{}, 1),
		}
		res, err := n.requestFromPeer(peer.ID("trie-missing-peer"), topic, validHash, &trie.TrieNodeResponse{})
		require.NoError(t, err)
		require.Nil(t, res)
		require.Empty(t, pm.banned)
	})
}

func TestRequestFromPeerTrieNodesBatchValidation(t *testing.T) {
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, &trie.TrieNodesResponse{})
	require.NoError(t, err)

	blob1 := []byte("node-1")
	blob2 := []byte("node-2")
	hash1 := crypto.Keccak256Hash(blob1)
	hash2 := crypto.Keccak256Hash(blob2)
	reqHashes := []common.Hash{hash1, hash2}

	t.Run("accepts valid subset", func(t *testing.T) {
		pm := newTrieTestPeerManager()
		responses := make(chan interface{}, 1)
		responses <- &trie.TrieNodesResponse{Nodes: [][]byte{blob2}}
		n := &P2PNode{
			ctx:            context.Background(),
			peerManager:    pm,
			requestManager: &missingResponseRequestManager{result: responses},
			requestSlots:   make(chan struct{}, 1),
		}
		res, err := n.requestFromPeer(peer.ID("batch-valid-peer"), topic, reqHashes, &trie.TrieNodesResponse{})
		require.NoError(t, err)
		require.Equal(t, &trie.TrieNodesResponse{Nodes: [][]byte{blob2}}, res)
		require.Empty(t, pm.banned)
	})

	t.Run("bans peer on unsolicited blob", func(t *testing.T) {
		pm := newTrieTestPeerManager()
		responses := make(chan interface{}, 1)
		responses <- &trie.TrieNodesResponse{Nodes: [][]byte{blob1, []byte("unsolicited")}}
		n := &P2PNode{
			ctx:            context.Background(),
			peerManager:    pm,
			requestManager: &missingResponseRequestManager{result: responses},
			requestSlots:   make(chan struct{}, 1),
		}
		peerID := peer.ID("batch-bad-peer")
		res, err := n.requestFromPeer(peerID, topic, reqHashes, &trie.TrieNodesResponse{})
		require.Error(t, err)
		require.Nil(t, res)
		require.Equal(t, []peer.ID{peerID}, pm.banned)
	})

	t.Run("bans peer on duplicate blob", func(t *testing.T) {
		pm := newTrieTestPeerManager()
		responses := make(chan interface{}, 1)
		responses <- &trie.TrieNodesResponse{Nodes: [][]byte{blob1, blob1}}
		n := &P2PNode{
			ctx:            context.Background(),
			peerManager:    pm,
			requestManager: &missingResponseRequestManager{result: responses},
			requestSlots:   make(chan struct{}, 1),
		}
		peerID := peer.ID("batch-dup-peer")
		res, err := n.requestFromPeer(peerID, topic, reqHashes, &trie.TrieNodesResponse{})
		require.Error(t, err)
		require.Nil(t, res)
		require.Equal(t, []peer.ID{peerID}, pm.banned)
	})
}
