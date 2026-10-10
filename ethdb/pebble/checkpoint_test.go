package pebble

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	cockroachpebble "github.com/cockroachdb/pebble"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
)

func TestOnlineCheckpoint(t *testing.T) {
	root := t.TempDir()
	liveDir := filepath.Join(root, "live")
	checkpointDir := filepath.Join(root, "checkpoint")

	logger := log.NewLogger("test", filepath.Join(root, "test.log"), 0)

	db, err := New(
		liveDir,
		16,
		16,
		"checkpoint-test",
		false,
		logger,
		common.Location{},
	)
	if err != nil {
		t.Fatalf("open live db: %v", err)
	}
	defer db.Close()

	// State that must exist in both the live DB and checkpoint.
	if err := db.Put([]byte("before"), []byte("state-A")); err != nil {
		t.Fatalf("write before checkpoint: %v", err)
	}

	// Capture the DB while it remains open.
	if err := db.Checkpoint(checkpointDir); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	// This write occurs strictly after the checkpoint.
	if err := db.Put([]byte("after"), []byte("state-B")); err != nil {
		t.Fatalf("write after checkpoint: %v", err)
	}

	// Live database must contain both values.
	got, err := db.Get([]byte("before"))
	if err != nil {
		t.Fatalf("live get before: %v", err)
	}
	if string(got) != "state-A" {
		t.Fatalf("live before = %q, want %q", got, "state-A")
	}

	got, err = db.Get([]byte("after"))
	if err != nil {
		t.Fatalf("live get after: %v", err)
	}
	if string(got) != "state-B" {
		t.Fatalf("live after = %q, want %q", got, "state-B")
	}

	// Open the checkpoint independently, read-only.
	checkpoint, err := cockroachpebble.Open(
		checkpointDir,
		&cockroachpebble.Options{ReadOnly: true},
	)
	if err != nil {
		t.Fatalf("open checkpoint: %v", err)
	}
	defer checkpoint.Close()

	// State written before Checkpoint must exist.
	value, closer, err := checkpoint.Get([]byte("before"))
	if err != nil {
		t.Fatalf("checkpoint get before: %v", err)
	}
	if string(value) != "state-A" {
		closer.Close()
		t.Fatalf("checkpoint before = %q, want %q", value, "state-A")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("close checkpoint value: %v", err)
	}

	// State written afterward must NOT exist.
	_, closer, err = checkpoint.Get([]byte("after"))
	if closer != nil {
		closer.Close()
	}
	if !errors.Is(err, cockroachpebble.ErrNotFound) {
		t.Fatalf("checkpoint unexpectedly contains post-checkpoint write; err=%v", err)
	}
}

func TestOnlineCheckpointConcurrentWrites(t *testing.T) {
	root := t.TempDir()
	liveDir := filepath.Join(root, "live")

	logger := log.NewLogger("test", filepath.Join(root, "test.log"), 0)

	db, err := New(
		liveDir,
		16,
		16,
		"checkpoint-concurrent-test",
		false,
		logger,
		common.Location{},
	)
	if err != nil {
		t.Fatalf("open live db: %v", err)
	}
	defer db.Close()

	// Seed immutable baseline state. Every checkpoint must contain this.
	const baselineKeys = 1000
	for i := 0; i < baselineKeys; i++ {
		key := []byte(fmt.Sprintf("baseline-%08d", i))
		value := []byte(fmt.Sprintf("value-%08d", i))
		if err := db.Put(key, value); err != nil {
			t.Fatalf("seed baseline %d: %v", i, err)
		}
	}

	var (
		stop      = make(chan struct{})
		writerErr = make(chan error, 1)
		writes    atomic.Uint64
	)

	// Continuously mutate the live DB while checkpoints are being created.
	go func() {
		for i := uint64(0); ; i++ {
			select {
			case <-stop:
				writerErr <- nil
				return
			default:
			}

			key := []byte(fmt.Sprintf("live-%012d", i))
			value := []byte(fmt.Sprintf("payload-%012d", i))

			if err := db.Put(key, value); err != nil {
				writerErr <- err
				return
			}
			writes.Store(i + 1)
		}
	}()

	// Produce several checkpoints while writes continue.
	const checkpointCount = 10

	for n := 0; n < checkpointCount; n++ {
		checkpointDir := filepath.Join(
			root,
			fmt.Sprintf("checkpoint-%02d", n),
		)

		before := writes.Load()

		if err := db.Checkpoint(checkpointDir); err != nil {
			close(stop)
			<-writerErr
			t.Fatalf("checkpoint %d: %v", n, err)
		}

		after := writes.Load()

		cp, err := cockroachpebble.Open(
			checkpointDir,
			&cockroachpebble.Options{ReadOnly: true},
		)
		if err != nil {
			close(stop)
			<-writerErr
			t.Fatalf("open checkpoint %d: %v", n, err)
		}

		// Verify baseline state is intact in every checkpoint.
		for i := 0; i < baselineKeys; i++ {
			key := []byte(fmt.Sprintf("baseline-%08d", i))
			want := fmt.Sprintf("value-%08d", i)

			value, closer, err := cp.Get(key)
			if err != nil {
				cp.Close()
				close(stop)
				<-writerErr
				t.Fatalf(
					"checkpoint %d missing baseline key %d: %v",
					n, i, err,
				)
			}

			got := string(value)
			closer.Close()

			if got != want {
				cp.Close()
				close(stop)
				<-writerErr
				t.Fatalf(
					"checkpoint %d baseline key %d = %q, want %q",
					n, i, got, want,
				)
			}
		}

		// A key definitely created after Checkpoint returned must not
		// magically appear in the checkpoint.
		postKeyNum := after + 1000000
		postKey := []byte(fmt.Sprintf("post-%012d", postKeyNum))

		if err := db.Put(postKey, []byte("written-after-checkpoint")); err != nil {
			cp.Close()
			close(stop)
			<-writerErr
			t.Fatalf("write post-checkpoint key: %v", err)
		}

		_, closer, err := cp.Get(postKey)
		if closer != nil {
			closer.Close()
		}
		if !errors.Is(err, cockroachpebble.ErrNotFound) {
			cp.Close()
			close(stop)
			<-writerErr
			t.Fatalf(
				"checkpoint %d contains explicit post-checkpoint key; err=%v",
				n, err,
			)
		}

		if err := cp.Close(); err != nil {
			close(stop)
			<-writerErr
			t.Fatalf("close checkpoint %d: %v", n, err)
		}

		t.Logf(
			"checkpoint %d valid; concurrent writes before=%d after=%d",
			n, before, after,
		)
	}

	close(stop)

	if err := <-writerErr; err != nil {
		t.Fatalf("writer failed: %v", err)
	}

	t.Logf("total concurrent writes: %d", writes.Load())
}
