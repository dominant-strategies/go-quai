package node

import (
	"context"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
)

type recordingRequestPeerSource struct {
	gotTopic     *pubsubManager.Topic
	getPeersCall int
	gotExcluded  map[peer.ID]struct{}
	topicPeers   map[peer.ID]struct{}
	streamPeers  []peer.ID
}

func (s *recordingRequestPeerSource) GetPeersExcluding(topic *pubsubManager.Topic, excluded map[peer.ID]struct{}) map[peer.ID]struct{} {
	s.gotTopic = topic
	s.getPeersCall++
	s.gotExcluded = excluded
	selected := make(map[peer.ID]struct{}, len(s.topicPeers))
	for peerID := range s.topicPeers {
		if _, tried := excluded[peerID]; !tried {
			selected[peerID] = struct{}{}
		}
	}
	return selected
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

	var got []peer.ID
	requestPeerRounds(context.Background(), source, topic, 2, reverse, func(peers []peer.ID) bool {
		got = peers
		return true
	})

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

	var got []peer.ID
	requestPeerRounds(context.Background(), source, topic, 2, reverse, func(peers []peer.ID) bool {
		got = peers
		return true
	})

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

	var got []peer.ID
	requestPeerRounds(context.Background(), source, topic, 2, noShuffle, func(peers []peer.ID) bool {
		got = peers
		return true
	})

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

func TestRequestPeerRoundsFallsBackAfterLivePeersFail(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingRequestPeerSource{
		streamPeers: []peer.ID{peer.ID("live-a"), peer.ID("live-b")},
		topicPeers: map[peer.ID]struct{}{
			peer.ID("live-a"):   {},
			peer.ID("stored-c"): {},
		},
	}
	var rounds [][]peer.ID
	requestPeerRounds(context.Background(), source, topic, 2, noShuffle, func(peers []peer.ID) bool {
		rounds = append(rounds, peers)
		return len(rounds) == 2
	})
	if source.getPeersCall != 1 || source.gotTopic != topic {
		t.Fatalf("topic fallback calls/topic = %d/%p, want 1/%p", source.getPeersCall, source.gotTopic, topic)
	}
	if _, tried := source.gotExcluded[peer.ID("live-a")]; !tried {
		t.Fatal("failed live peer was not excluded from topic fallback")
	}
	if len(rounds) != 2 || len(rounds[0]) != 2 || rounds[0][0] != peer.ID("live-a") || rounds[0][1] != peer.ID("live-b") || len(rounds[1]) != 1 || rounds[1][0] != peer.ID("stored-c") {
		t.Fatalf("request rounds = %q, want live peers then untried topic peer", rounds)
	}
}

func TestRequestPeerRoundsSkipsFallbackAfterCancellation(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingRequestPeerSource{
		streamPeers: []peer.ID{peer.ID("live-a")},
		topicPeers:  map[peer.ID]struct{}{peer.ID("stored-b"): {}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts int
	requestPeerRounds(ctx, source, topic, 1, noShuffle, func([]peer.ID) bool {
		attempts++
		cancel()
		return false
	})
	if attempts != 1 || source.getPeersCall != 0 {
		t.Fatalf("attempts/fallback queries = %d/%d, want 1/0", attempts, source.getPeersCall)
	}
}
