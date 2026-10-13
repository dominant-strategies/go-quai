package filters

import (
	"context"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
)

// rangeTestBackend has a head at a fixed height and no blocks below it. It
// counts the block lookups that a range filter makes.
type rangeTestBackend struct {
	Backend
	head    *types.WorkObject
	lookups atomic.Int64
}

func (b *rangeTestBackend) ChainDb() ethdb.Database { return nil }

func (b *rangeTestBackend) HeaderByNumber(_ context.Context, number rpc.BlockNumber) (*types.WorkObject, error) {
	if number == rpc.LatestBlockNumber {
		return b.head, nil
	}
	b.lookups.Add(1)
	return nil, nil
}

func newRangeTestBackend(head uint64) *rangeTestBackend {
	wh := &types.WorkObjectHeader{}
	wh.SetNumber(new(big.Int).SetUint64(head))
	return &rangeTestBackend{head: types.NewWorkObject(wh, types.EmptyWorkObjectBody(), nil)}
}

// H6: "pending" as fromBlock was -2, which skipped the range limit, so the
// filter scanned every block from genesis to the head.
func TestRangeFilterTagsResolveToHead(t *testing.T) {
	const head = 5 * MaxFilterRange
	for _, tt := range []struct {
		name       string
		begin, end rpc.BlockNumber
	}{
		{"pending to latest", rpc.PendingBlockNumber, rpc.LatestBlockNumber},
		{"pending to pending", rpc.PendingBlockNumber, rpc.PendingBlockNumber},
		{"latest to pending", rpc.LatestBlockNumber, rpc.PendingBlockNumber},
	} {
		backend := newRangeTestBackend(head)
		filter := NewRangeFilter(backend, tt.begin.Int64(), tt.end.Int64(), nil, nil, log.Global)
		if _, err := filter.Logs(context.Background()); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := backend.lookups.Load(); got != 1 {
			t.Fatalf("%s: looked up %d blocks, want only the head", tt.name, got)
		}
	}
}

// H6: an explicit range wider than MaxFilterRange must be rejected before any
// block is read, also when the end is a tag.
func TestRangeFilterRejectsWideRange(t *testing.T) {
	const head = 5 * MaxFilterRange
	for _, end := range []int64{rpc.LatestBlockNumber.Int64(), rpc.PendingBlockNumber.Int64(), head} {
		backend := newRangeTestBackend(head)
		filter := NewRangeFilter(backend, 0, end, nil, nil, log.Global)
		_, err := filter.Logs(context.Background())
		if err == nil || !strings.Contains(err.Error(), "maximum limit") {
			t.Fatalf("end %d: got %v, want a range limit error", end, err)
		}
		if backend.lookups.Load() != 0 {
			t.Fatalf("end %d: read %d blocks before rejecting the range", end, backend.lookups.Load())
		}
	}
}

func TestResolveFilterRange(t *testing.T) {
	for _, tt := range []struct {
		begin, end         int64
		wantBegin, wantEnd uint64
	}{
		{-2, -1, 100, 100},
		{-1, -2, 100, 100},
		{10, -1, 10, 100},
		{10, 20, 10, 20},
	} {
		begin, end := resolveFilterRange(tt.begin, tt.end, 100)
		if begin != tt.wantBegin || end != tt.wantEnd {
			t.Fatalf("resolveFilterRange(%d, %d) = %d, %d, want %d, %d", tt.begin, tt.end, begin, end, tt.wantBegin, tt.wantEnd)
		}
	}
}
