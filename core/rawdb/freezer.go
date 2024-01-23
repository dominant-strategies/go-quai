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
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/prometheus/tsdb/fileutil"
)

var (
	// errReadOnly is returned if the freezer is opened in read only mode. All the
	// mutations are disallowed.
	errReadOnly = errors.New("read only")

	// errUnknownTable is returned if the user attempts to read from a table that is
	// not tracked by the freezer.
	errUnknownTable = errors.New("unknown table")

	// errOutOrderInsertion is returned if the user attempts to inject out-of-order
	// binary blobs into the freezer.
	errOutOrderInsertion = errors.New("the append operation is out-order")

	// errSymlinkDatadir is returned if the ancient directory specified by user
	// is a symbolic link.
	errSymlinkDatadir = errors.New("symbolic link datadir is not supported")
)

const (
	// freezerRecheckInterval is the frequency to check the key-value database for
	// chain progression that might permit new blocks to be frozen into immutable
	// storage.
	freezerRecheckInterval = time.Minute

	// freezerBatchLimit is the maximum number of blocks to freeze in one batch
	// before doing an fsync and deleting it from the key-value store.
	freezerBatchLimit = 30000
)

// freezer is an memory mapped append-only database to store immutable chain data
// into flat files:
//
//   - The append only nature ensures that disk writes are minimized.
//   - The memory mapping ensures we can max out system memory for caching without
//     reserving it for go-quai. This would also reduce the memory requirements
//     of Quai, and thus also GC overhead.
type freezer struct {
	// WARNING: The `frozen` field is accessed atomically. On 32 bit platforms, only
	// 64-bit aligned fields can be atomic. The struct is guaranteed to be so aligned,
	// so take advantage of that (https://golang.org/pkg/sync/atomic/#pkg-note-BUG).
	frozen    uint64 // Number of blocks already frozen
	threshold uint64 // Number of recent blocks not to freeze (params.FullImmutabilityThreshold apart from tests)

	readonly     bool
	tables       map[string]*freezerTable // Data tables for storing everything
	instanceLock fileutil.Releaser        // File-system lock to prevent double opens

	trigger chan chan struct{} // Manual blocking freeze trigger, test determinism

	quit      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once

	logger *log.Logger
}

// newFreezer creates a chain freezer that moves ancient chain data into
// append-only flat file containers.
func newFreezer(datadir string, namespace string, readonly bool, logger *log.Logger) (*freezer, error) {
	// Create the initial freezer object
	// Ensure the datadir is not a symbolic link if it exists.
	if info, err := os.Lstat(datadir); !os.IsNotExist(err) {
		if info.Mode()&os.ModeSymlink != 0 {
			logger.WithField("path", datadir).Warn("Symbolic link datadir is not supported")
			return nil, errSymlinkDatadir
		}
	}
	// Leveldb uses LOCK as the filelock filename. To prevent the
	// name collision, we use FLOCK as the lock name.
	lock, _, err := fileutil.Flock(filepath.Join(datadir, "FLOCK"))
	if err != nil {
		return nil, err
	}
	// Open all the supported data tables
	freezer := &freezer{
		readonly:     readonly,
		threshold:    params.FullImmutabilityThreshold,
		tables:       make(map[string]*freezerTable),
		instanceLock: lock,
		trigger:      make(chan chan struct{}),
		quit:         make(chan struct{}),
		logger:       logger,
	}
	for name, disableSnappy := range FreezerNoSnappy {
		table, err := newTable(datadir, name, disableSnappy)
		if err != nil {
			for _, table := range freezer.tables {
				table.Close()
			}
			lock.Release()
			return nil, err
		}
		freezer.tables[name] = table
	}
	if err := freezer.repair(); err != nil {
		for _, table := range freezer.tables {
			table.Close()
		}
		lock.Release()
		return nil, err
	}
	logger.WithFields(log.Fields{
		"database": datadir,
		"readonly": readonly,
	}).Info("Opened ancient database")
	return freezer, nil
}

// Close terminates the chain freezer, unmapping all the data files.
func (f *freezer) Close() error {
	var errs []error
	f.closeOnce.Do(func() {
		close(f.quit)
		// Wait for any background freezing to stop
		f.wg.Wait()
		for _, table := range f.tables {
			if err := table.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := f.instanceLock.Release(); err != nil {
			errs = append(errs, err)
		}
	})
	if errs != nil {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

// HasAncient returns an indicator whether the specified ancient data exists
// in the freezer.
func (f *freezer) HasAncient(kind string, number uint64) (bool, error) {
	if table := f.tables[kind]; table != nil {
		return table.has(number), nil
	}
	return false, nil
}

// Ancient retrieves an ancient binary blob from the append-only immutable files.
func (f *freezer) Ancient(kind string, number uint64) ([]byte, error) {
	if table := f.tables[kind]; table != nil {
		return table.Retrieve(number)
	}
	return nil, errUnknownTable
}

// Ancients returns the length of the frozen items.
func (f *freezer) Ancients() (uint64, error) {
	return atomic.LoadUint64(&f.frozen), nil
}

// AncientSize returns the ancient size of the specified category.
func (f *freezer) AncientSize(kind string) (uint64, error) {
	if table := f.tables[kind]; table != nil {
		return table.size()
	}
	return 0, errUnknownTable
}

// AppendAncient injects all binary blobs belong to block at the end of the
// append-only immutable table files.
//
// Notably, this function is lock free but kind of thread-safe. All out-of-order
// injection will be rejected. But if two injections with same number happen at
// the same time, we can get into the trouble.
func (f *freezer) AppendAncient(number uint64, hash, header, body, receipts, etxSet []byte) (err error) {
	if f.readonly {
		return errReadOnly
	}
	// Ensure the binary blobs we are appending is continuous with freezer.
	if atomic.LoadUint64(&f.frozen) != number {
		return errOutOrderInsertion
	}
	// Rollback all inserted data if any insertion below failed to ensure
	// the tables won't out of sync.
	defer func() {
		if err != nil {
			rerr := f.repair()
			if rerr != nil {
				f.logger.WithField("err", rerr).Fatal("Failed to repair freezer")
			}
			f.logger.WithFields(log.Fields{
				"number": number,
				"err":    err,
			}).Error("Failed to append ancient")
		}
	}()
	// Inject all the components into the relevant data tables
	if err := f.tables[freezerHashTable].Append(f.frozen, hash[:]); err != nil {
		f.logger.WithFields(log.Fields{
			"number": f.frozen,
			"hash":   hash,
			"err":    err,
		}).Error("Failed to append ancient hash")
		return err
	}
	if err := f.tables[freezerHeaderTable].Append(f.frozen, header); err != nil {
		f.logger.WithFields(log.Fields{
			"number": f.frozen,
			"hash":   hash,
			"err":    err,
		}).Error("Failed to append ancient header")
		return err
	}
	if err := f.tables[freezerBodiesTable].Append(f.frozen, body); err != nil {
		f.logger.WithFields(log.Fields{
			"number": f.frozen,
			"hash":   hash,
			"err":    err,
		}).Error("Failed to append ancient body")
		return err
	}
	if err := f.tables[freezerReceiptTable].Append(f.frozen, receipts); err != nil {
		f.logger.WithFields(log.Fields{
			"number": f.frozen,
			"hash":   hash,
			"err":    err,
		}).Error("Failed to append ancient receipts")
		return err
	}
	if err := f.tables[freezerEtxSetsTable].Append(f.frozen, etxSet); err != nil {
		f.logger.WithFields(log.Fields{
			"number": f.frozen,
			"hash":   hash,
			"err":    err,
		}).Error("Failed to append ancient etx set")
		return err
	}
	atomic.AddUint64(&f.frozen, 1) // Only modify atomically
	return nil
}

// TruncateAncients discards any recent data above the provided threshold number.
func (f *freezer) TruncateAncients(items uint64) error {
	if f.readonly {
		return errReadOnly
	}
	if atomic.LoadUint64(&f.frozen) <= items {
		return nil
	}
	for _, table := range f.tables {
		if err := table.truncate(items); err != nil {
			return err
		}
	}
	atomic.StoreUint64(&f.frozen, items)
	return nil
}

// Sync flushes all data tables to disk.
func (f *freezer) Sync() error {
	var errs []error
	for _, table := range f.tables {
		if err := table.Sync(); err != nil {
			errs = append(errs, err)
		}
	}
	if errs != nil {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

// freeze is a background thread that periodically checks the blockchain for any
// import progress and moves ancient data from the fast database into the freezer.
//
// This functionality is deliberately broken off from block importing to avoid
// incurring additional data shuffling delays on block propagation.
func (f *freezer) freeze(db ethdb.KeyValueStore, nodeCtx int) {
	nfdb := &nofreezedb{KeyValueStore: db}

	var (
		backoff   bool
		triggered chan struct{} // Used in tests
	)
	for {
		select {
		case <-f.quit:
			f.logger.Info("Freezer shutting down")
			return
		default:
		}
		if backoff {
			// If we were doing a manual trigger, notify it
			if triggered != nil {
				triggered <- struct{}{}
				triggered = nil
			}
			select {
			case <-time.NewTimer(freezerRecheckInterval).C:
				backoff = false
			case triggered = <-f.trigger:
				backoff = false
			case <-f.quit:
				return
			}
		}
		// Retrieve the freezing threshold.
		hash := ReadHeadBlockHash(nfdb)
		if hash == (common.Hash{}) {
			f.logger.Debug("Current full block hash unavailable") // new chain, empty database
			backoff = true
			continue
		}
		number := ReadHeaderNumber(nfdb, hash)
		threshold := atomic.LoadUint64(&f.threshold)

		switch {
		case number == nil:
			f.logger.WithField("hash", hash).Error("Current full block number unavailable")
			backoff = true
			continue

		case *number < threshold:
			f.logger.WithFields(log.Fields{
				"number":    *number,
				"hash":      hash,
				"threshold": threshold,
			}).Debug("Current full block not old enough")
			backoff = true
			continue

		case *number-threshold <= f.frozen:
			f.logger.WithFields(log.Fields{
				"number": *number,
				"hash":   hash,
				"frozen": f.frozen,
			}).Debug("Ancient blocks frozen already")
			backoff = true
			continue
		}
		head := ReadHeader(nfdb, hash, *number)
		if head == nil {
			f.logger.WithFields(log.Fields{
				"number": *number,
				"hash":   hash,
			}).Error("Current full block header unavailable")
			backoff = true
			continue
		}
		// Seems we have data ready to be frozen, process in usable batches
		limit := *number - threshold
		if limit-f.frozen > freezerBatchLimit {
			limit = f.frozen + freezerBatchLimit
		}
		var (
			start    = time.Now()
			first    = f.frozen
			ancients = make([]common.Hash, 0, limit-f.frozen)
		)
		for f.frozen <= limit {
			// Retrieves all the components of the canonical block
			hash := ReadCanonicalHash(nfdb, f.frozen)
			if hash == (common.Hash{}) {
				f.logger.WithField("number", f.frozen).Error("Canonical hash missing, can't freeze")
				break
			}
			header := ReadHeaderRLP(nfdb, hash, f.frozen)
			if len(header) == 0 {
				f.logger.WithFields(log.Fields{
					"number": f.frozen,
					"hash":   hash,
				}).Error("Block header missing, can't freeze")
				break
			}
			body := ReadBodyRLP(nfdb, hash, f.frozen)
			if len(body) == 0 {
				f.logger.WithFields(log.Fields{
					"number": f.frozen,
					"hash":   hash,
				}).Error("Block body missing, can't freeze")
				break
			}
			receipts := ReadReceiptsRLP(nfdb, hash, f.frozen)
			if len(receipts) == 0 {
				f.logger.WithFields(log.Fields{
					"number": f.frozen,
					"hash":   hash,
				}).Error("Block receipts missing, can't freeze")
				break
			}
			etxSet := ReadEtxSetRLP(nfdb, hash, f.frozen)
			if len(etxSet) == 0 {
				f.logger.WithFields(log.Fields{
					"number": f.frozen,
					"hash":   hash,
				}).Error("Total etxset missing, can't freeze")
				break
			}
			f.logger.WithFields(log.Fields{
				"number": f.frozen,
				"hash":   hash,
			}).Trace("Deep froze ancient block")
			// Inject all the components into the relevant data tables
			if err := f.AppendAncient(f.frozen, hash[:], header, body, receipts, etxSet); err != nil {
				break
			}
			ancients = append(ancients, hash)
		}
		// Batch of blocks have been frozen, flush them before wiping from leveldb
		if err := f.Sync(); err != nil {
			f.logger.WithField("err", err).Fatal("Failed to flush frozen tables")
		}
		// Wipe out all data from the active database
		batch := db.NewBatch()
		for i := 0; i < len(ancients); i++ {
			// Always keep the genesis block in active database
			if first+uint64(i) != 0 {
				DeleteBlockWithoutNumber(batch, ancients[i], first+uint64(i))
				DeleteCanonicalHash(batch, first+uint64(i))
			}
		}
		if err := batch.Write(); err != nil {
			f.logger.WithField("err", err).Fatal("Failed to delete frozen canonical blocks")
		}
		batch.Reset()

		// Wipe out side chains also and track dangling side chians
		var dangling []common.Hash
		for number := first; number < f.frozen; number++ {
			// Always keep the genesis block in active database
			if number != 0 {
				dangling = ReadAllHashes(db, number)
				for _, hash := range dangling {
					f.logger.WithFields(log.Fields{
						"number": number,
						"hash":   hash,
					}).Trace("Deleting side chain")
					DeleteBlock(batch, hash, number)
				}
			}
		}
		if err := batch.Write(); err != nil {
			f.logger.WithField("err", err).Fatal("Failed to delete frozen side blocks")
		}
		batch.Reset()

		// Step into the future and delete and dangling side chains
		if f.frozen > 0 {
			tip := f.frozen
			for len(dangling) > 0 {
				drop := make(map[common.Hash]struct{})
				for _, hash := range dangling {
					f.logger.WithFields(log.Fields{
						"number": tip - 1,
						"hash":   hash,
					}).Trace("Deleting parent from freezer")
					drop[hash] = struct{}{}
				}
				children := ReadAllHashes(db, tip)
				for i := 0; i < len(children); i++ {
					// Dig up the child and ensure it's dangling
					child := ReadHeader(nfdb, children[i], tip)
					if child == nil {
						f.logger.WithFields(log.Fields{
							"number": tip,
							"hash":   children[i],
						}).Error("Missing dangling header")
						continue
					}
					if _, ok := drop[child.ParentHash(nodeCtx)]; !ok {
						children = append(children[:i], children[i+1:]...)
						i--
						continue
					}
					// Delete all block data associated with the child
					f.logger.WithFields(log.Fields{
						"number": tip,
						"hash":   children[i],
						"parent": child.ParentHash(nodeCtx),
					}).Debug("Deleting dangling block")
					DeleteBlock(batch, children[i], tip)
				}
				dangling = children
				tip++
			}
			if err := batch.Write(); err != nil {
				f.logger.WithField("err", err).Fatal("Failed to delete dangling side blocks")
			}
		}
		// Log something friendly for the user
		context := []interface{}{
			"blocks", f.frozen - first, "elapsed", common.PrettyDuration(time.Since(start)), "number", f.frozen - 1,
		}
		if n := len(ancients); n > 0 {
			context = append(context, []interface{}{"hash", ancients[n-1]}...)
		}
		f.logger.WithField("context", context).Info("Deep froze chain segment")

		// Avoid database thrashing with tiny writes
		if f.frozen-first < freezerBatchLimit {
			backoff = true
		}
	}
}

// repair truncates all data tables to the same length.
func (f *freezer) repair() error {
	min := uint64(math.MaxUint64)
	for _, table := range f.tables {
		items := atomic.LoadUint64(&table.items)
		if min > items {
			min = items
		}
	}
	for _, table := range f.tables {
		if err := table.truncate(min); err != nil {
			return err
		}
	}
	atomic.StoreUint64(&f.frozen, min)
	return nil
}
