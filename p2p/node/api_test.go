package node

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
)

type recordingRequestPeerSource struct {
	gotTopic    *pubsubManager.Topic
	topicPeers  map[peer.ID]struct{}
	streamPeers []peer.ID
}

func (s *recordingRequestPeerSource) GetPeers(topic *pubsubManager.Topic) map[peer.ID]struct{} {
	s.gotTopic = topic
	return s.topicPeers
}

func (s *recordingRequestPeerSource) GetStreamPeers() []peer.ID {
	return s.streamPeers
}

func noShuffle(_ int, _ func(int, int)) {}

func TestRequestPeersForTopicPrefersLiveTopicPeers(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingRequestPeerSource{
		topicPeers: map[peer.ID]struct{}{
			peer.ID("live-topic-peer"):  {},
			peer.ID("stale-topic-peer"): {},
		},
		streamPeers: []peer.ID{
			peer.ID("live-fallback-peer"),
			peer.ID("live-topic-peer"),
			peer.ID("second-live-fallback-peer"),
		},
	}

	got := requestPeersForTopic(source, topic, 2, noShuffle)

	if source.gotTopic != topic {
		t.Fatalf("GetPeers called with wrong topic: got %p want %p", source.gotTopic, topic)
	}
	if len(got) != 2 {
		t.Fatalf("got %d peers, want 2", len(got))
	}
	if got[0] != peer.ID("live-topic-peer") {
		t.Fatalf("first peer = %q, want live topic peer", got[0])
	}
	if got[1] != peer.ID("live-fallback-peer") {
		t.Fatalf("second peer = %q, want live fallback peer", got[1])
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

func TestRequestPeersForTopicDialsStoredPeersOnlyWhenNeeded(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingRequestPeerSource{
		topicPeers:  map[peer.ID]struct{}{peer.ID("stored-topic-peer"): {}},
		streamPeers: []peer.ID{peer.ID("live-peer")},
	}

	got := requestPeersForTopic(source, topic, 2, noShuffle)

	if len(got) != 2 || got[0] != peer.ID("live-peer") || got[1] != peer.ID("stored-topic-peer") {
		t.Fatalf("selected peers = %q, want live peer followed by stored topic peer", got)
	}
}
