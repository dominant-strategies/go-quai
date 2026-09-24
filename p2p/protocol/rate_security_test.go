package protocol

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestInboundRequestRatePersistsAcrossRequests(t *testing.T) {
	id := peer.ID("rate-security-test-peer")
	if err := ProcRequestRate(id, true); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := ProcRequestRate(id, true); err == nil {
		t.Fatal("back-to-back request bypassed rate limit")
	}
	requestRateMu.Lock()
	tracker := inRateTrackers[id]
	tracker.last = time.Now().Add(-100 * time.Millisecond)
	inRateTrackers[id] = tracker
	requestRateMu.Unlock()
	if err := ProcRequestRate(id, true); err != nil {
		t.Fatalf("request after quiet interval: %v", err)
	}
	requestRateMu.Lock()
	delete(inRateTrackers, id)
	requestRateMu.Unlock()
}
