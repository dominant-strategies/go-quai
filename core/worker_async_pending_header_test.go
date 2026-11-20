package core

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
)

func TestAsyncPendingHeaderRefreshWithFullSubscriber(t *testing.T) {
	const timeout = 5 * time.Second
	w := &worker{
		hc:          new(HeaderChain),
		chainSideCh: make(chan ChainSideEvent),
		exitCh:      make(chan struct{}),
		logger:      log.Global,
	}
	w.chainSideSub = w.hc.SubscribeChainSideEvent(w.chainSideCh)
	defer w.chainSideSub.Unsubscribe()

	updates := make(chan *types.WorkObject, 1)
	w.SubscribeAsyncPendingHeader(updates)
	updates <- new(types.WorkObject)

	ticks := make(chan time.Time)
	header := new(types.WorkObject)
	started := make(chan struct{}, 1)
	var generations atomic.Int32
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.runAsyncStateLoop(ticks, func() (*types.WorkObject, error) {
			if generations.Add(1) == 1 {
				started <- struct{}{}
			}
			return header, nil
		})
	}()

	sendTick := func() {
		t.Helper()
		select {
		case ticks <- time.Now():
		case <-time.After(timeout):
			t.Fatal("refresh loop stopped handling ticks")
		}
	}

	// Model a subscriber that needs the header lock before it can drain more updates.
	locked := make(chan struct{})
	releaseLock := make(chan struct{})
	defer func() {
		close(releaseLock)
		w.close()
	}()
	sendTick()
	select {
	case <-started:
	case <-time.After(timeout):
		t.Fatal("pending header generation did not start")
	}
	go func() {
		w.hc.headermu.Lock()
		defer w.hc.headermu.Unlock()
		close(locked)
		<-releaseLock
	}()
	select {
	case <-locked:
	case <-time.After(timeout):
		t.Fatal("pending header publication held the header lock")
	}

	for i := 0; i < 5; i++ {
		sendTick()
	}
	// Once the loop receives another event, it has processed all the ticks.
	select {
	case w.chainSideCh <- ChainSideEvent{}:
	case <-time.After(timeout):
		t.Fatal("refresh loop stopped handling side blocks")
	}
	<-updates
	select {
	case got := <-updates:
		if got != header {
			t.Fatal("subscriber did not receive the generated header")
		}
	case <-time.After(timeout):
		t.Fatal("pending header publication did not finish")
	}
	t.Cleanup(func() {
		if got := generations.Load(); got != 1 {
			t.Errorf("generated %d pending headers, want 1 while publication was blocked", got)
		}
	})
}
