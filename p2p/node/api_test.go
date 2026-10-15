package node

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
)

type recordingRequestPeerSource struct {
	gotTopic     *pubsubManager.Topic
	getPeersCall int
	topicPeers   map[peer.ID]struct{}
	streamPeers  []peer.ID
}

func (s *recordingRequestPeerSource) GetPeers(topic *pubsubManager.Topic) map[peer.ID]struct{} {
	s.gotTopic = topic
	s.getPeersCall++
	return s.topicPeers
}

func (s *recordingRequestPeerSource) GetStreamPeers() []peer.ID {
	return s.streamPeers
}

func noShuffle(_ int, _ func(int, int)) {}

func TestRequestPeersForTopicRotatesLivePeersWithoutDatabaseQuery(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingRequestPeerSource{
		topicPeers: map[peer.ID]struct{}{peer.ID("stored-peer"): {}},
		streamPeers: []peer.ID{
			peer.ID("live-a"),
			peer.ID("live-b"),
			peer.ID("live-c"),
		},
	}
	reverse := func(n int, swap func(int, int)) {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			swap(i, j)
		}
	}

	got := requestPeersForTopic(source, topic, 2, reverse)

	if source.getPeersCall != 0 {
		t.Fatalf("GetPeers called %d times with live streams available", source.getPeersCall)
	}
	if len(got) != 2 || got[0] != peer.ID("live-c") || got[1] != peer.ID("live-b") {
		t.Fatalf("selected peers = %q, want shuffled live peers", got)
	}
}

func TestRequestPeersForTopicDoesNotMutateStreamCache(t *testing.T) {
	topic := &pubsubManager.Topic{}
	original := []peer.ID{peer.ID("peer-a"), peer.ID("peer-b"), peer.ID("peer-c")}
	source := &recordingRequestPeerSource{streamPeers: append([]peer.ID(nil), original...)}
	reverse := func(n int, swap func(int, int)) {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			swap(i, j)
		}
	}

	got := requestPeersForTopic(source, topic, 2, reverse)

	if got[0] != peer.ID("peer-c") || got[1] != peer.ID("peer-b") {
		t.Fatalf("selected peers = %q, want shuffled live peers", got)
	}
	for i := range original {
		if source.streamPeers[i] != original[i] {
			t.Fatalf("stream cache mutated: got %q want %q", source.streamPeers, original)
		}
	}
}

func TestRequestPeersForTopicUsesStoredPeersOnlyWithoutLiveStreams(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingRequestPeerSource{
		topicPeers: map[peer.ID]struct{}{
			peer.ID("stored-a"): {},
			peer.ID("stored-b"): {},
			peer.ID("stored-c"): {},
		},
	}

	got := requestPeersForTopic(source, topic, 2, noShuffle)

	if source.getPeersCall != 1 || source.gotTopic != topic {
		t.Fatalf("GetPeers calls/topic = %d/%p, want 1/%p", source.getPeersCall, source.gotTopic, topic)
	}
	if len(got) != 2 {
		t.Fatalf("selected %d peers, want 2", len(got))
	}
	for _, peerID := range got {
		if _, ok := source.topicPeers[peerID]; !ok {
			t.Fatalf("selected peer %q is not from topic fallback", peerID)
		}
	}
}
