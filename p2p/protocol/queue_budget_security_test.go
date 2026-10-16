package protocol

import (
	"sync/atomic"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
)

func TestQueueStreamMessageGlobalBudget(t *testing.T) {
	if got := inboundQueuedBytes.Load(); got != 0 {
		t.Fatalf("previous test retained %d bytes", got)
	}
	defer inboundQueuedBytes.Store(0)
	inboundQueuedBytes.Store(maxQueuedBytesTotal - 2)

	first := make(chan []byte, 1)
	second := make(chan []byte, 1)
	firstBytes := new(atomic.Int64)
	secondBytes := new(atomic.Int64)
	if !queueStreamMessage(first, []byte{1, 2}, firstBytes) {
		t.Fatal("message inside the global limit was rejected")
	}
	if queueStreamMessage(second, []byte{1}, secondBytes) || len(second) != 0 {
		t.Fatal("another stream exceeded the global limit")
	}
	if got := inboundQueuedBytes.Load(); got != maxQueuedBytesTotal {
		t.Fatalf("rejected message changed the budget: %d", got)
	}
	if got := secondBytes.Load(); got != 0 {
		t.Fatalf("rejected message changed the stream budget: %d", got)
	}
	inboundQueuedBytes.Add(-int64(len(<-first))) // The worker finishes the first message.
	firstBytes.Add(-2)
	if !queueStreamMessage(second, []byte{1}, secondBytes) {
		t.Fatal("released bytes were not reusable")
	}
}

func TestQueueStreamMessagePreventsOneStreamMonopoly(t *testing.T) {
	if got := inboundQueuedBytes.Load(); got != 0 {
		t.Fatalf("previous test retained %d bytes", got)
	}
	defer inboundQueuedBytes.Store(0)

	largeFrame := make([]byte, common.MaxStreamMessageSize)
	first := make(chan []byte, msgChanSize)
	second := make(chan []byte, 1)
	firstBytes := new(atomic.Int64)
	secondBytes := new(atomic.Int64)
	for i := 0; i < 5; i++ {
		if !queueStreamMessage(first, largeFrame, firstBytes) {
			t.Fatalf("frame %d rejected before the per-stream limit", i)
		}
	}
	if queueStreamMessage(first, largeFrame, firstBytes) {
		t.Fatal("one stream exceeded the per-stream limit")
	}
	if !queueStreamMessage(second, largeFrame, secondBytes) {
		t.Fatal("another stream was starved by the first stream")
	}
	if got := firstBytes.Load(); got != 5*common.MaxStreamMessageSize {
		t.Fatalf("first stream retained %d bytes", got)
	}
}

func TestQueueStreamMessageAllowsSmallBurst(t *testing.T) {
	if got := inboundQueuedBytes.Load(); got != 0 {
		t.Fatalf("previous test retained %d bytes", got)
	}
	defer inboundQueuedBytes.Store(0)

	q := make(chan []byte, msgChanSize)
	streamBytes := new(atomic.Int64)
	for i := 0; i < msgChanSize; i++ {
		if !queueStreamMessage(q, []byte{1}, streamBytes) {
			t.Fatalf("small message %d was rejected", i)
		}
	}
	if queueStreamMessage(q, []byte{1}, streamBytes) {
		t.Fatal("full channel accepted another message")
	}
	if got := inboundQueuedBytes.Load(); got != int64(msgChanSize) {
		t.Fatalf("full-channel rejection leaked bytes: %d", got)
	}
	if got := streamBytes.Load(); got != int64(msgChanSize) {
		t.Fatalf("full-channel rejection leaked stream bytes: %d", got)
	}
}
