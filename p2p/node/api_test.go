package node

import (
	"context"
	"slices"
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
	qualities    map[peer.ID]int
}

func (s *recordingRequestPeerSource) GetPeersExcluding(topic *pubsubManager.Topic, excluded map[peer.ID]struct{}) []peer.ID {
	s.gotTopic = topic
	s.getPeersCall++
	s.gotExcluded = excluded
	selected := make([]peer.ID, 0, len(s.topicPeers))
	for peerID := range s.topicPeers {
		if _, tried := excluded[peerID]; !tried {
			selected = append(selected, peerID)
		}
	}
	return selected
}

func (s *recordingRequestPeerSource) GetStreamPeers() []peer.ID {
	return s.streamPeers
}

func (s *recordingRequestPeerSource) GetPeerQuality(id peer.ID) int {
	return s.qualities[id]
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
	requestPeerRounds(context.Background(), source, topic, nil, 2, reverse, func(peers []peer.ID) bool {
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
	requestPeerRounds(context.Background(), source, topic, nil, 2, reverse, func(peers []peer.ID) bool {
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
	requestPeerRounds(context.Background(), source, topic, nil, 2, noShuffle, func(peers []peer.ID) bool {
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
	requestPeerRounds(context.Background(), source, topic, nil, 2, noShuffle, func(peers []peer.ID) bool {
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
	requestPeerRounds(ctx, source, topic, nil, 1, noShuffle, func([]peer.ID) bool {
		attempts++
		cancel()
		return false
	})
	if attempts != 1 || source.getPeersCall != 0 {
		t.Fatalf("attempts/fallback queries = %d/%d, want 1/0", attempts, source.getPeersCall)
	}
}

func TestRequestPeerRoundsPrefersTopicStreamsAndRetriesOtherLiveStreams(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingRequestPeerSource{
		streamPeers: []peer.ID{
			peer.ID("other-a"), peer.ID("topic-a"), peer.ID("other-b"),
			peer.ID("topic-b"), peer.ID("other-c"),
		},
		topicPeers: map[peer.ID]struct{}{peer.ID("stored-peer"): {}},
	}
	original := append([]peer.ID(nil), source.streamPeers...)
	var rounds [][]peer.ID
	requestPeerRounds(context.Background(), source, topic, []peer.ID{peer.ID("topic-b"), peer.ID("topic-a")}, 2, noShuffle, func(peers []peer.ID) bool {
		rounds = append(rounds, append([]peer.ID(nil), peers...))
		return len(rounds) == 2
	})
	if len(rounds) != 2 || len(rounds[0]) != 2 || rounds[0][0] != peer.ID("topic-a") || rounds[0][1] != peer.ID("topic-b") ||
		len(rounds[1]) != 2 || rounds[1][0] != peer.ID("other-a") || rounds[1][1] != peer.ID("other-b") {
		t.Fatalf("request rounds = %q, want topic streams before other live streams", rounds)
	}
	if source.getPeersCall != 0 {
		t.Fatalf("database queried after live peer responded: %d calls", source.getPeersCall)
	}
	for i, peerID := range original {
		if source.streamPeers[i] != peerID {
			t.Fatalf("stream cache changed at %d: got %q want %q", i, source.streamPeers[i], peerID)
		}
	}
}

func TestLiveRequestPeersShuffleWithinQualityBuckets(t *testing.T) {
	topicBestA := peer.ID("topic-best-a")
	topicBestB := peer.ID("topic-best-b")
	topicResponsive := peer.ID("topic-responsive")
	topicLast := peer.ID("topic-last")
	otherBest := peer.ID("other-best")
	otherResponsive := peer.ID("other-responsive")
	source := &recordingRequestPeerSource{
		streamPeers: []peer.ID{
			topicLast, otherResponsive, topicBestA, topicResponsive,
			otherBest, topicBestB, topicBestA,
		},
		qualities: map[peer.ID]int{
			topicBestA: 900, topicBestB: 850, topicResponsive: 500,
			topicLast: 0, otherBest: 1000, otherResponsive: 300,
		},
	}
	reverse := func(n int, swap func(int, int)) {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			swap(i, j)
		}
	}
	original := append([]peer.ID(nil), source.streamPeers...)
	got := orderLiveRequestPeers(source, []peer.ID{topicBestA, topicBestB, topicResponsive, topicLast}, reverse)
	want := []peer.ID{topicBestB, topicBestA, topicResponsive, topicLast, otherBest, otherResponsive}
	if !slices.Equal(got, want) {
		t.Fatalf("live peers = %q, want topic peers and quality buckets in order %q", got, want)
	}
	if !slices.Equal(source.streamPeers, original) {
		t.Fatal("stream cache order changed")
	}
}

func TestLiveRequestPeersUseCurrentQualityOnEachRequest(t *testing.T) {
	low, high := peer.ID("low"), peer.ID("high")
	source := &recordingRequestPeerSource{
		streamPeers: []peer.ID{low, high},
		qualities:   map[peer.ID]int{low: 0, high: 100},
	}
	first := orderLiveRequestPeers(source, nil, noShuffle)
	if first[0] != high {
		t.Fatalf("first request chose %q, want high-quality peer %q", first[0], high)
	}
	source.qualities[low] = 200
	second := orderLiveRequestPeers(source, nil, noShuffle)
	if second[0] != low {
		t.Fatalf("next request chose %q, want newly higher-quality peer %q", second[0], low)
	}
}

func TestRequestPeerRoundsExcludesOnlyPeersTriedInCurrentRequest(t *testing.T) {
	topic := &pubsubManager.Topic{}
	source := &recordingRequestPeerSource{
		streamPeers: []peer.ID{peer.ID("live-a"), peer.ID("live-b"), peer.ID("live-c"), peer.ID("live-d"), peer.ID("live-e")},
		topicPeers: map[peer.ID]struct{}{
			peer.ID("live-a"): {}, peer.ID("live-c"): {}, peer.ID("stored-f"): {},
		},
	}
	var rounds [][]peer.ID
	requestPeerRounds(context.Background(), source, topic, []peer.ID{peer.ID("live-a"), peer.ID("live-c")}, 2, noShuffle, func(peers []peer.ID) bool {
		rounds = append(rounds, append([]peer.ID(nil), peers...))
		return false
	})
	if len(rounds) != 3 || len(rounds[0]) != 2 || rounds[0][0] != peer.ID("live-a") || rounds[0][1] != peer.ID("live-c") ||
		len(rounds[1]) != 2 || rounds[1][0] != peer.ID("live-b") || rounds[1][1] != peer.ID("live-d") ||
		len(rounds[2]) != 1 || rounds[2][0] != peer.ID("stored-f") {
		t.Fatalf("request rounds = %q, want two live rounds then untried stored peer", rounds)
	}
	if len(source.gotExcluded) != 4 {
		t.Fatalf("excluded peers = %d, want only the four attempted live peers", len(source.gotExcluded))
	}
	for _, peerID := range []peer.ID{peer.ID("live-a"), peer.ID("live-b"), peer.ID("live-c"), peer.ID("live-d")} {
		if _, excluded := source.gotExcluded[peerID]; !excluded {
			t.Fatalf("attempted peer %q was not excluded from this request's fallback", peerID)
		}
	}
	if _, excluded := source.gotExcluded[peer.ID("live-e")]; excluded {
		t.Fatal("untried live peer was excluded from fallback")
	}

	// A new request starts with a fresh list. The previous exclusion does not
	// ban or remove any live peer from consideration.
	rounds = nil
	requestPeerRounds(context.Background(), source, topic, []peer.ID{peer.ID("live-a"), peer.ID("live-c")}, 2, noShuffle, func(peers []peer.ID) bool {
		rounds = append(rounds, append([]peer.ID(nil), peers...))
		return true
	})
	if len(rounds) != 1 || len(rounds[0]) != 2 || rounds[0][0] != peer.ID("live-a") || rounds[0][1] != peer.ID("live-c") {
		t.Fatalf("next request did not retry previous peers: %q", rounds)
	}
}
