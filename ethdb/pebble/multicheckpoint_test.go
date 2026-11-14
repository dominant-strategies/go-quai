package pebble

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
)

type checkpointManifest struct {
	CreatedAtUTC string                    `json:"created_at_utc"`
	DurationMS   int64                     `json:"duration_ms"`
	Chains       map[string]checkpointInfo `json:"chains"`
}

type checkpointInfo struct {
	Marker string `json:"marker"`
	Writes uint64 `json:"writes_at_checkpoint"`
}

type testChain struct {
	name   string
	db     *Database
	writes atomic.Uint64
}

func TestMultiDatabaseOnlineCheckpoint(t *testing.T) {
	root := t.TempDir()
	liveRoot := filepath.Join(root, "live")
	snapshotRoot := filepath.Join(root, "snapshot")

	logger := log.NewLogger(
		"test",
		filepath.Join(root, "test.log"),
		0,
	)

	names := []string{
		"prime",
		"region-0",
		"zone-0-0",
	}

	chains := make([]*testChain, 0, len(names))

	for _, name := range names {
		db, err := New(
			filepath.Join(liveRoot, name, "chaindata"),
			16,
			16,
			"multi-checkpoint-test",
			false,
			logger,
			common.Location{},
		)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}

		chains = append(chains, &testChain{
			name: name,
			db:   db,
		})
	}

	defer func() {
		for _, chain := range chains {
			chain.db.Close()
		}
	}()

	// Stable state that must exist in every checkpoint.
	for _, chain := range chains {
		key := []byte("canonical-marker")
		value := []byte("marker-" + chain.name)

		if err := chain.db.Put(key, value); err != nil {
			t.Fatalf("seed %s marker: %v", chain.name, err)
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Simulate active chain writes against all three DBs.
	for _, chain := range chains {
		chain := chain
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := uint64(0); ; i++ {
				select {
				case <-stop:
					return
				default:
				}

				key := []byte(fmt.Sprintf(
					"live-%s-%012d",
					chain.name,
					i,
				))

				value := []byte(fmt.Sprintf(
					"value-%012d",
					i,
				))

				if err := chain.db.Put(key, value); err != nil {
					t.Errorf(
						"writer %s failed: %v",
						chain.name,
						err,
					)
					return
				}

				chain.writes.Store(i + 1)
			}
		}()
	}

	manifest := checkpointManifest{
		CreatedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		Chains:       make(map[string]checkpointInfo),
	}

	start := time.Now()

	// Sequentially checkpoint all three DBs while writers continue.
	for _, chain := range chains {
		dest := filepath.Join(
			snapshotRoot,
			chain.name,
			"chaindata",
		)

		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("mkdir %s: %v", chain.name, err)
		}

		before := chain.writes.Load()

		cpStart := time.Now()

		if err := chain.db.Checkpoint(dest); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf(
				"checkpoint %s: %v",
				chain.name,
				err,
			)
		}

		cpDuration := time.Since(cpStart)

		after := chain.writes.Load()

		t.Logf(
			"%s checkpoint duration=%s writes_before=%d writes_after=%d",
			chain.name,
			cpDuration,
			before,
			after,
		)

		manifest.Chains[chain.name] = checkpointInfo{
			Marker: "marker-" + chain.name,
			Writes: after,
		}
	}

	manifest.DurationMS = time.Since(start).Milliseconds()

	close(stop)
	wg.Wait()

	// Write snapshot manifest.
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(snapshotRoot, "manifest.json"),
		manifestBytes,
		0644,
	); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	t.Logf(
		"total multi-db capture duration=%dms",
		manifest.DurationMS,
	)

	// Independently reopen every checkpoint.
	for _, chain := range chains {
		dir := filepath.Join(
			snapshotRoot,
			chain.name,
			"chaindata",
		)

		cp, err := cockroachpebble.Open(
			dir,
			&cockroachpebble.Options{
				ReadOnly: true,
			},
		)
		if err != nil {
			t.Fatalf(
				"open checkpoint %s: %v",
				chain.name,
				err,
			)
		}

		value, closer, err := cp.Get(
			[]byte("canonical-marker"),
		)
		if err != nil {
			cp.Close()
			t.Fatalf(
				"checkpoint %s marker read: %v",
				chain.name,
				err,
			)
		}

		got := string(value)
		closer.Close()

		want := "marker-" + chain.name

		if got != want {
			cp.Close()
			t.Fatalf(
				"%s marker=%q want=%q",
				chain.name,
				got,
				want,
			)
		}

		if err := cp.Close(); err != nil {
			t.Fatalf(
				"close checkpoint %s: %v",
				chain.name,
				err,
			)
		}

		t.Logf(
			"%s checkpoint independently verified",
			chain.name,
		)
	}
}
