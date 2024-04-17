// Copyright 2019 The go-ethereum Authors
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

package rawdb

import (
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
)

// InitDatabaseFromFreezer reinitializes an empty database from a previous batch
// of frozen ancient blocks. The method iterates over all the frozen blocks and
// injects into the database the block hash->number mappings.
func InitDatabaseFromFreezer(db ethdb.Database) {
	// If we can't access the freezer or it's empty, abort
	frozen, err := db.Ancients()
	if err != nil || frozen == 0 {
		return
	}
	var (
		batch  = db.NewBatch()
		start  = time.Now()
		logged = start.Add(-7 * time.Second) // Unindex during import is fast, don't double log
		hash   common.Hash
	)
	for i := uint64(0); i < frozen; i++ {
		// Since the freezer has all data in sequential order on a file,
		// it would be 'neat' to read more data in one go, and let the
		// freezerdb return N items (e.g up to 1000 items per go)
		// That would require an API change in Ancients though
		if h, err := db.Ancient(freezerHashTable, i); err != nil {
			db.Logger().WithField("err", err).Fatal("Failed to init database from freezer")
		} else {
			hash = common.BytesToHash(h)
		}
		WriteHeaderNumber(batch, hash, i)
		// If enough data was accumulated in memory or we're at the last block, dump to disk
		if batch.ValueSize() > ethdb.IdealBatchSize {
			if err := batch.Write(); err != nil {
				db.Logger().WithField("err", err).Fatal("Failed to write data to db")
			}
			batch.Reset()
		}
		// If we've spent too much time already, notify the user of what we're doing
		if time.Since(logged) > 8*time.Second {
			db.Logger().WithFields(log.Fields{
				"total":   frozen,
				"number":  i,
				"hash":    hash,
				"elapsed": common.PrettyDuration(time.Since(start)),
			}).Info("Initializing database from freezer")
			logged = time.Now()
		}
	}
	if err := batch.Write(); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to write data to db")
	}
	batch.Reset()

	WriteHeadHeaderHash(db, hash)
	db.Logger().WithFields(log.Fields{
		"blocks":  frozen,
		"elapsed": common.PrettyDuration(time.Since(start)),
	}).Info("Initialized database from freezer")
}
