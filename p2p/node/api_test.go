package node

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
)

type recordingTopicPeerSource struct {
	gotTopic *pubsubManager.Topic
	peers    map[peer.ID]struct{}
}

func (s *recordingTopicPeerSource) GetPeers(topic *pubsubManager.Topic) map[peer.ID]struct{} {
	s.gotTopic = topic
	return s.peers
}

func TestRequestPeersForTopicUsesOnlyTopicPeers(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingTopicPeerSource{peers: map[peer.ID]struct{}{
		peer.ID("topic-peer-a"): {},
		peer.ID("topic-peer-b"): {},
	}}

	got := requestPeersForTopic(source, topic)

	if source.gotTopic != topic {
		t.Fatalf("GetPeers called with wrong topic: got %p want %p", source.gotTopic, topic)
	}
	gotSet := make(map[peer.ID]struct{}, len(got))
	for _, id := range got {
		gotSet[id] = struct{}{}
	}
	if len(gotSet) != len(source.peers) {
		t.Fatalf("got %d unique peers, want %d", len(gotSet), len(source.peers))
	}
	for id := range source.peers {
		if _, ok := gotSet[id]; !ok {
			t.Fatalf("topic peer %q missing from selection", id)
		}
	}
}
