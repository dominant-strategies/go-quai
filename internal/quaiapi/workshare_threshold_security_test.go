package quaiapi

import (
	"context"
	"testing"

	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/params"
)

type thresholdTestBackend struct {
	Backend
	threshold int
}

func (b *thresholdTestBackend) SetWorkShareP2PThreshold(threshold int) { b.threshold = threshold }

// H5: the threshold is used as an exponent for every gossiped share, so a very
// large value must be rejected.
func TestSetWorkShareP2PThresholdIsCapped(t *testing.T) {
	backend := &thresholdTestBackend{threshold: params.WorkShareP2PThresholdDiff}
	api := &PublicBlockChainQuaiAPI{b: backend}

	for _, value := range []uint64{uint64(params.MaxWorkShareP2PThresholdDiff) + 1, 1 << 40, 1 << 63} {
		if err := api.SetWorkShareP2PThreshold(context.Background(), hexutil.Uint64(value)); err == nil {
			t.Fatalf("threshold %d accepted", value)
		}
		if backend.threshold != params.WorkShareP2PThresholdDiff {
			t.Fatalf("rejected threshold %d changed the value to %d", value, backend.threshold)
		}
	}
	if err := api.SetWorkShareP2PThreshold(context.Background(), hexutil.Uint64(params.MaxWorkShareP2PThresholdDiff)); err != nil {
		t.Fatalf("maximum threshold rejected: %v", err)
	}
	if backend.threshold != params.MaxWorkShareP2PThresholdDiff {
		t.Fatalf("threshold is %d, want %d", backend.threshold, params.MaxWorkShareP2PThresholdDiff)
	}
	if err := api.SetWorkShareP2PThreshold(context.Background(), hexutil.Uint64(params.WorkSharesThresholdDiff-1)); err == nil {
		t.Fatal("threshold below the workshare threshold accepted")
	}
}
