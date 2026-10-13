package protocol

import (
	"sync/atomic"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
)

// H4: one stream must not queue more than its byte budget.
func TestStreamQueueBudgetLimitsOneStream(t *testing.T) {
	total := new(atomic.Int64)
	b := &streamQueueBudget{streamLimit: 10, total: total, totalLimit: 100}
	if !b.tryAcquire(6) {
		t.Fatal("first message rejected")
	}
	if b.tryAcquire(5) {
		t.Fatal("stream budget exceeded")
	}
	b.release(6)
	if !b.tryAcquire(10) {
		t.Fatal("released bytes were not returned to the stream budget")
	}
	b.release(10)
	if total.Load() != 0 {
		t.Fatalf("total budget leaked %d bytes", total.Load())
	}
}

// H4: many streams together must not queue more than the total budget, so one
// peer cannot open many streams to multiply the per stream limit.
func TestStreamQueueBudgetLimitsAllStreams(t *testing.T) {
	total := new(atomic.Int64)
	a := &streamQueueBudget{streamLimit: 10, total: total, totalLimit: 15}
	b := &streamQueueBudget{streamLimit: 10, total: total, totalLimit: 15}
	if !a.tryAcquire(10) {
		t.Fatal("first stream rejected")
	}
	if b.tryAcquire(10) {
		t.Fatal("total budget exceeded")
	}
	if total.Load() != 10 {
		t.Fatalf("rejected message changed the total budget: %d", total.Load())
	}
	if !b.tryAcquire(5) {
		t.Fatal("second stream rejected inside the total budget")
	}
	a.release(10)
	b.release(5)
	if total.Load() != 0 {
		t.Fatalf("total budget leaked %d bytes", total.Load())
	}
}

// The default budgets must still fit the largest valid stream messages.
func TestStreamQueueBudgetDefaultsFitLargestMessage(t *testing.T) {
	if maxQueuedBytesPerStream < 2*common.MaxStreamMessageSize {
		t.Fatalf("per stream budget %d does not fit two %d byte messages", maxQueuedBytesPerStream, common.MaxStreamMessageSize)
	}
	if maxQueuedBytesTotal < maxQueuedBytesPerStream {
		t.Fatalf("total budget %d is below the per stream budget %d", maxQueuedBytesTotal, maxQueuedBytesPerStream)
	}
}
