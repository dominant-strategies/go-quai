// Copyright 2023 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

//go:build (arm64 || amd64) && !openbsd

package pebble

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/ethdb/dbtest"
)

func TestPebbleDB(t *testing.T) {
	t.Run("DatabaseSuite", func(t *testing.T) {
		dbtest.TestDatabaseSuite(t, func() ethdb.KeyValueStore {
			db, err := pebble.Open("", &pebble.Options{
				FS: vfs.NewMem(),
			})
			if err != nil {
				t.Fatal(err)
			}
			return &Database{
				db: db,
			}
		})
	})
}

func TestPebbleBatchPending(t *testing.T) {
	db, err := pebble.Open("", &pebble.Options{
		FS: vfs.NewMem(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	kvdb := &Database{db: db}
	batch := kvdb.NewBatch()
	batch.SetPending(true)

	key := []byte("key")
	value := []byte("value")
	if err := batch.Put(key, value); err != nil {
		t.Fatal(err)
	}
	deleted, pending := batch.GetPending(key)
	if deleted {
		t.Fatal("GetPending reported a put key as deleted")
	}
	if !bytes.Equal(pending, value) {
		t.Fatalf("GetPending returned %q, want %q", pending, value)
	}

	if err := batch.Delete(key); err != nil {
		t.Fatal(err)
	}
	deleted, pending = batch.GetPending(key)
	if !deleted {
		t.Fatal("GetPending did not report a deleted key")
	}
	if pending != nil {
		t.Fatalf("GetPending returned pending data for deleted key: %q", pending)
	}
}

func TestPebbleStatProperties(t *testing.T) {
	db, err := pebble.Open("", &pebble.Options{
		FS: vfs.NewMem(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	kvdb := &Database{db: db}
	if err := kvdb.Put([]byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}

	stats, err := kvdb.Stat("leveldb.stats")
	if err != nil {
		t.Fatalf("Stat(leveldb.stats) returned unexpected error: %v", err)
	}
	if !strings.Contains(stats, "WAL") {
		t.Fatalf("Stat(leveldb.stats) returned unexpected output: %q", stats)
	}

	iostats, err := kvdb.Stat("leveldb.iostats")
	if err != nil {
		t.Fatalf("Stat(leveldb.iostats) returned unexpected error: %v", err)
	}
	if !strings.Contains(iostats, "Read(MB):") || !strings.Contains(iostats, "Write(MB):") {
		t.Fatalf("Stat(leveldb.iostats) returned unexpected output: %q", iostats)
	}

	kvdb.onWriteStallBegin(pebble.WriteStallBeginInfo{})
	time.Sleep(time.Millisecond)
	kvdb.onWriteStallEnd()
	writedelay, err := kvdb.Stat("leveldb.writedelay")
	if err != nil {
		t.Fatalf("Stat(leveldb.writedelay) returned unexpected error: %v", err)
	}
	if !strings.Contains(writedelay, "DelayN:1") || !strings.Contains(writedelay, "Paused:false") {
		t.Fatalf("Stat(leveldb.writedelay) returned unexpected output: %q", writedelay)
	}

	compcount, err := kvdb.Stat("leveldb.compcount")
	if err != nil {
		t.Fatalf("Stat(leveldb.compcount) returned unexpected error: %v", err)
	}
	if !strings.Contains(compcount, "MemComp:") || !strings.Contains(compcount, "Level0Comp:") {
		t.Fatalf("Stat(leveldb.compcount) returned unexpected output: %q", compcount)
	}

	if _, err := kvdb.Stat("leveldb.unknown"); err == nil {
		t.Fatal("Stat(leveldb.unknown) succeeded unexpectedly")
	}
}
