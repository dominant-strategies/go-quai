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
	"testing"

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
	inner, err := pebble.Open("", &pebble.Options{
		FS: vfs.NewMem(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()

	db := &Database{db: inner}
	batch := db.NewBatch()

	key := []byte("pending-key")
	value := []byte("pending-value")

	// Pending tracking is opt-in.
	if err := batch.Put(key, value); err != nil {
		t.Fatal(err)
	}
	if deleted, data := batch.GetPending(key); deleted || data != nil {
		t.Fatalf("unexpected pending value before SetPending: deleted=%t data=%q", deleted, data)
	}

	batch.Reset()
	batch.SetPending(true)

	// A pending write should be visible before the batch is committed.
	if err := batch.Put(key, value); err != nil {
		t.Fatal(err)
	}
	deleted, data := batch.GetPending(key)
	if deleted {
		t.Fatal("pending write reported as deleted")
	}
	if string(data) != string(value) {
		t.Fatalf("pending write mismatch: have %q want %q", data, value)
	}

	// A later delete of the same key should replace the pending write.
	if err := batch.Delete(key); err != nil {
		t.Fatal(err)
	}
	deleted, data = batch.GetPending(key)
	if !deleted || data != nil {
		t.Fatalf("pending delete mismatch: deleted=%t data=%q", deleted, data)
	}

	// A later write should replace the pending delete.
	replacement := []byte("replacement")
	if err := batch.Put(key, replacement); err != nil {
		t.Fatal(err)
	}
	deleted, data = batch.GetPending(key)
	if deleted {
		t.Fatal("replacement write reported as deleted")
	}
	if string(data) != string(replacement) {
		t.Fatalf("replacement mismatch: have %q want %q", data, replacement)
	}

	// Writing the batch clears its pending overlay.
	if err := batch.Write(); err != nil {
		t.Fatal(err)
	}
	if deleted, data := batch.GetPending(key); deleted || data != nil {
		t.Fatalf("pending state survived Write: deleted=%t data=%q", deleted, data)
	}

	// Reset must also clear pending tracking and pending values.
	batch.Reset()
	batch.SetPending(true)
	if err := batch.Put(key, []byte("reset-value")); err != nil {
		t.Fatal(err)
	}
	batch.Reset()

	if deleted, data := batch.GetPending(key); deleted || data != nil {
		t.Fatalf("pending state survived Reset: deleted=%t data=%q", deleted, data)
	}
}
