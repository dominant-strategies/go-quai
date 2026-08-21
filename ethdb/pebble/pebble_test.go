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

func TestBatchPendingReadYourWrites(t *testing.T) {
	db, err := pebble.Open("", &pebble.Options{FS: vfs.NewMem()})
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
	value[0] = 'X'
	deleted, pending := batch.GetPending(key)
	if deleted || !bytes.Equal(pending, []byte("value")) {
		t.Fatalf("pending put not visible: deleted=%t value=%q", deleted, pending)
	}
	pending[0] = 'Y'
	_, pending = batch.GetPending(key)
	if !bytes.Equal(pending, []byte("value")) {
		t.Fatalf("GetPending returned mutable batch storage: %q", pending)
	}

	if err := batch.Delete(key); err != nil {
		t.Fatal(err)
	}
	deleted, pending = batch.GetPending(key)
	if !deleted || pending != nil {
		t.Fatalf("pending delete not visible: deleted=%t value=%q", deleted, pending)
	}
}

func TestBatchPendingClearedAfterWriteAndReset(t *testing.T) {
	db, err := pebble.Open("", &pebble.Options{FS: vfs.NewMem()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	kvdb := &Database{db: db}
	batch := kvdb.NewBatch()
	key := []byte("key")

	batch.SetPending(true)
	if err := batch.Put(key, []byte("written")); err != nil {
		t.Fatal(err)
	}
	if err := batch.Write(); err != nil {
		t.Fatal(err)
	}
	if deleted, pending := batch.GetPending(key); deleted || pending != nil {
		t.Fatalf("pending state survived Write: deleted=%t value=%q", deleted, pending)
	}
	written, err := kvdb.Get(key)
	if err != nil || !bytes.Equal(written, []byte("written")) {
		t.Fatalf("committed value missing: value=%q err=%v", written, err)
	}

	batch.Reset()
	batch.SetPending(true)
	if err := batch.Put(key, []byte("reset")); err != nil {
		t.Fatal(err)
	}
	batch.Reset()
	if deleted, pending := batch.GetPending(key); deleted || pending != nil {
		t.Fatalf("pending state survived Reset: deleted=%t value=%q", deleted, pending)
	}
}
