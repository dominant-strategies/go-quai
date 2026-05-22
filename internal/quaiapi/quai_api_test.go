package quaiapi

import (
	"testing"

	"github.com/dominant-strategies/go-quai/common/hexutil"
)

type testNetBackend struct {
	total    uint
	incoming uint
	outgoing uint
}

func (b testNetBackend) PeerCount() uint {
	return b.total
}

func (b testNetBackend) PeerCountByDirection() (uint, uint) {
	return b.incoming, b.outgoing
}

func TestPublicNetAPIPeerCounts(t *testing.T) {
	api := NewPublicNetAPI(1, testNetBackend{
		total:    5,
		incoming: 2,
		outgoing: 3,
	})

	if got := api.PeerCount(); got != hexutil.Uint(5) {
		t.Fatalf("expected peer count 5, got %d", got)
	}

	byDirection := api.PeerCountByDirection()
	if byDirection.Incoming != hexutil.Uint(2) {
		t.Fatalf("expected incoming peer count 2, got %d", byDirection.Incoming)
	}
	if byDirection.Outgoing != hexutil.Uint(3) {
		t.Fatalf("expected outgoing peer count 3, got %d", byDirection.Outgoing)
	}
}
