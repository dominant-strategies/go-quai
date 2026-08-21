// Copyright 2018 The go-ethereum Authors
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

package memorydb

import (
	"testing"

	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/ethdb/dbtest"
	"github.com/dominant-strategies/go-quai/log"
)

func TestMemoryDB(t *testing.T) {
	t.Run("DatabaseSuite", func(t *testing.T) {
		dbtest.TestDatabaseSuite(t, func() ethdb.KeyValueStore {
			return New(log.Global)
		})
	})
}

func TestBatchPendingReadYourWrites(t *testing.T) {
	db := New(log.Global)
	batch := db.NewBatch()
	batch.SetPending(true)

	key := []byte("key")
	value := []byte("value")
	if err := batch.Put(key, value); err != nil {
		t.Fatal(err)
	}
	deleted, pending := batch.GetPending(key)
	if deleted || string(pending) != string(value) {
		t.Fatalf("pending put not visible: deleted=%t value=%q", deleted, pending)
	}

	if err := batch.Delete(key); err != nil {
		t.Fatal(err)
	}
	deleted, pending = batch.GetPending(key)
	if !deleted || pending != nil {
		t.Fatalf("pending delete not visible: deleted=%t value=%q", deleted, pending)
	}
}
