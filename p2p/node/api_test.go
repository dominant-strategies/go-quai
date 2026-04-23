package node

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestRequestFanout(t *testing.T) {
	testcases := []struct {
		name      string
		base      int
		available int
		want      int
	}{
		{name: "no peers", base: 5, available: 0, want: 0},
		{name: "caps at available", base: 5, available: 3, want: 3},
		{name: "uses base fanout", base: 5, available: 8, want: 5},
		{name: "scales with more peers", base: 5, available: 20, want: 7},
		{name: "caps maximum fanout", base: 5, available: 60, want: c_maxRequestFanout},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestFanout(tc.base, tc.available); got != tc.want {
				t.Fatalf("requestFanout(%d, %d) = %d, want %d", tc.base, tc.available, got, tc.want)
			}
		})
	}
}

func TestSelectRequestPeersPrefersOpenStreams(t *testing.T) {
	streamPeers := []peer.ID{"stream-1", "stream-2", "stream-3", "stream-4", "stream-5", "stream-6"}
	fallbackPeers := map[peer.ID]struct{}{
		"fallback-1": {},
		"fallback-2": {},
	}

	selected := selectRequestPeers(streamPeers, fallbackPeers, 5)
	if len(selected) != 5 {
		t.Fatalf("selectRequestPeers returned %d peers, want 5", len(selected))
	}
	streamSet := make(map[peer.ID]struct{}, len(streamPeers))
	for _, peerID := range streamPeers {
		streamSet[peerID] = struct{}{}
	}
	for _, peerID := range selected {
		if _, ok := streamSet[peerID]; !ok {
			t.Fatalf("selectRequestPeers chose non-stream peer %q even though enough streams were available", peerID)
		}
	}
}

func TestSelectRequestPeersUsesFallbackWhenNeeded(t *testing.T) {
	streamPeers := []peer.ID{"stream-1", "stream-2"}
	fallbackPeers := map[peer.ID]struct{}{
		"stream-2":   {},
		"fallback-1": {},
		"fallback-2": {},
		"fallback-3": {},
		"fallback-4": {},
	}

	selected := selectRequestPeers(streamPeers, fallbackPeers, 5)
	if len(selected) != 5 {
		t.Fatalf("selectRequestPeers returned %d peers, want 5", len(selected))
	}

	selectedSet := make(map[peer.ID]struct{}, len(selected))
	for _, peerID := range selected {
		if _, exists := selectedSet[peerID]; exists {
			t.Fatalf("selectRequestPeers returned duplicate peer %q", peerID)
		}
		selectedSet[peerID] = struct{}{}
	}
	for _, peerID := range streamPeers {
		if _, ok := selectedSet[peerID]; !ok {
			t.Fatalf("selectRequestPeers did not include available stream peer %q", peerID)
		}
	}
}
