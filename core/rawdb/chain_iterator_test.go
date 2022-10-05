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
	"math/big"
	"reflect"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/prque"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rlp"
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
			log.Crit("Failed to init database from freezer", "err", err)
		} else {
			hash = common.BytesToHash(h)
		}
		WriteHeaderNumber(batch, hash, i)
		// If enough data was accumulated in memory or we're at the last block, dump to disk
		if batch.ValueSize() > ethdb.IdealBatchSize {
			if err := batch.Write(); err != nil {
				log.Crit("Failed to write data to db", "err", err)
			}
			batch.Reset()
		}
		// If we've spent too much time already, notify the user of what we're doing
		if time.Since(logged) > 8*time.Second {
			log.Info("Initializing database from freezer", "total", frozen, "number", i, "hash", hash, "elapsed", common.PrettyDuration(time.Since(start)))
			logged = time.Now()
		}
	}
	if err := batch.Write(); err != nil {
		log.Crit("Failed to write data to db", "err", err)
	}
	batch.Reset()

	WriteHeadHeaderHash(db, hash)
	log.Info("Initialized database from freezer", "blocks", frozen, "elapsed", common.PrettyDuration(time.Since(start)))
}

type blockTxHashes struct {
	number uint64
	hashes []common.Hash
}

// iterateTransactions iterates over all transactions in the (canon) block
// number(s) given, and yields the hashes on a channel. If there is a signal
// received from interrupt channel, the iteration will be aborted and result
// channel will be closed.
func iterateTransactions(db ethdb.Database, from uint64, to uint64, reverse bool, interrupt chan struct{}) chan *blockTxHashes {
	// One thread sequentially reads data from db
	type numberRlp struct {
		number uint64
		rlp    rlp.RawValue
	}
	if to == from {
		return nil
	}
	threads := to - from
	if cpus := runtime.NumCPU(); threads > uint64(cpus) {
		threads = uint64(cpus)
	}
	var (
		rlpCh    = make(chan *numberRlp, threads*2)     // we send raw rlp over this channel
		hashesCh = make(chan *blockTxHashes, threads*2) // send hashes over hashesCh
	)
	// lookup runs in one instance
	lookup := func() {
		n, end := from, to
		if reverse {
			n, end = to-1, from-1
		}
		defer close(rlpCh)
		for n != end {
			data := ReadCanonicalBodyRLP(db, n)
			// Feed the block to the aggregator, or abort on interrupt
			select {
			case rlpCh <- &numberRlp{n, data}:
			case <-interrupt:
				return
			}
			if reverse {
				n--
			} else {
				n++
			}
		}
	}
	// process runs in parallel
	nThreadsAlive := int32(threads)
	process := func() {
		defer func() {
			// Last processor closes the result channel
			if atomic.AddInt32(&nThreadsAlive, -1) == 0 {
				close(hashesCh)
			}
		}()
		for data := range rlpCh {
			var body types.Body
			if err := rlp.DecodeBytes(data.rlp, &body); err != nil {
				log.Warn("Failed to decode block body", "block", data.number, "error", err)
				return
			}
			var hashes []common.Hash
			for _, tx := range body.Transactions {
				hashes = append(hashes, tx.Hash())
			}
			result := &blockTxHashes{
				hashes: hashes,
				number: data.number,
			}
			// Feed the block to the aggregator, or abort on interrupt
			select {
			case hashesCh <- result:
			case <-interrupt:
				return
			}
		}
	}
	go lookup() // start the sequential db accessor
	for i := 0; i < int(threads); i++ {
		go process()
	}
	return hashesCh
}

// indexTransactions creates txlookup indices of the specified block range.
//
// This function iterates canonical chain in reverse order, it has one main advantage:
// We can write tx index tail flag periodically even without the whole indexing
// procedure is finished. So that we can resume indexing procedure next time quickly.
//
// There is a passed channel, the whole procedure will be interrupted if any
// signal received.
func indexTransactions(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}, hook func(uint64) bool) {
	// short circuit for invalid range
	if from >= to {
		return
	}
	var (
		hashesCh = iterateTransactions(db, from, to, true, interrupt)
		batch    = db.NewBatch()
		start    = time.Now()
		logged   = start.Add(-7 * time.Second)
		// Since we iterate in reverse, we expect the first number to come
		// in to be [to-1]. Therefore, setting lastNum to means that the
		// prqueue gap-evaluation will work correctly
		lastNum = to
		queue   = prque.New(nil)
		// for stats reporting
		blocks, txs = 0, 0
	)
	for chanDelivery := range hashesCh {
		// Push the delivery into the queue and process contiguous ranges.
		// Since we iterate in reverse, so lower numbers have lower prio, and
		// we can use the number directly as prio marker
		queue.Push(chanDelivery, int64(chanDelivery.number))
		for !queue.Empty() {
			// If the next available item is gapped, return
			if _, priority := queue.Peek(); priority != int64(lastNum-1) {
				break
			}
			// For testing
			if hook != nil && !hook(lastNum-1) {
				break
			}
			// Next block available, pop it off and index it
			delivery := queue.PopItem().(*blockTxHashes)
			lastNum = delivery.number
			WriteTxLookupEntries(batch, delivery.number, delivery.hashes)
			blocks++
			txs += len(delivery.hashes)
			// If enough data was accumulated in memory or we're at the last block, dump to disk
			if batch.ValueSize() > ethdb.IdealBatchSize {
				WriteTxIndexTail(batch, lastNum) // Also write the tail here
				if err := batch.Write(); err != nil {
					log.Crit("Failed writing batch to db", "error", err)
					return
				}
				batch.Reset()
			}
			// If we've spent too much time already, notify the user of what we're doing
			if time.Since(logged) > 8*time.Second {
				log.Info("Indexing transactions", "blocks", blocks, "txs", txs, "tail", lastNum, "total", to-from, "elapsed", common.PrettyDuration(time.Since(start)))
				logged = time.Now()
			}
		}
	}
	// Flush the new indexing tail and the last committed data. It can also happen
	// that the last batch is empty because nothing to index, but the tail has to
	// be flushed anyway.
	WriteTxIndexTail(batch, lastNum)
	if err := batch.Write(); err != nil {
		log.Crit("Failed writing batch to db", "error", err)
		return
	}
	select {
	case <-interrupt:
		log.Debug("Transaction indexing interrupted", "blocks", blocks, "txs", txs, "tail", lastNum, "elapsed", common.PrettyDuration(time.Since(start)))
	default:
		log.Info("Indexed transactions", "blocks", blocks, "txs", txs, "tail", lastNum, "elapsed", common.PrettyDuration(time.Since(start)))
	}
}

// IndexTransactions creates txlookup indices of the specified block range.
//
// This function iterates canonical chain in reverse order, it has one main advantage:
// We can write tx index tail flag periodically even without the whole indexing
// procedure is finished. So that we can resume indexing procedure next time quickly.
//
// There is a passed channel, the whole procedure will be interrupted if any
// signal received.
func IndexTransactions(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}) {
	indexTransactions(db, from, to, interrupt, nil)
}

// indexTransactionsForTesting is the internal debug version with an additional hook.
func indexTransactionsForTesting(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}, hook func(uint64) bool) {
	indexTransactions(db, from, to, interrupt, hook)
}

// unindexTransactions removes txlookup indices of the specified block range.
//
// There is a passed channel, the whole procedure will be interrupted if any
// signal received.
func unindexTransactions(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}, hook func(uint64) bool) {
	// short circuit for invalid range
	if from >= to {
		return
	}
	var (
		hashesCh = iterateTransactions(db, from, to, false, interrupt)
		batch    = db.NewBatch()
		start    = time.Now()
		logged   = start.Add(-7 * time.Second)
		// we expect the first number to come in to be [from]. Therefore, setting
		// nextNum to from means that the prqueue gap-evaluation will work correctly
		nextNum = from
		queue   = prque.New(nil)
		// for stats reporting
		blocks, txs = 0, 0
	)
	// Otherwise spin up the concurrent iterator and unindexer
	for delivery := range hashesCh {
		// Push the delivery into the queue and process contiguous ranges.
		queue.Push(delivery, -int64(delivery.number))
		for !queue.Empty() {
			// If the next available item is gapped, return
			if _, priority := queue.Peek(); -priority != int64(nextNum) {
				break
			}
			// For testing
			if hook != nil && !hook(nextNum) {
				break
			}
			delivery := queue.PopItem().(*blockTxHashes)
			nextNum = delivery.number + 1
			DeleteTxLookupEntries(batch, delivery.hashes)
			txs += len(delivery.hashes)
			blocks++

			// If enough data was accumulated in memory or we're at the last block, dump to disk
			// A batch counts the size of deletion as '1', so we need to flush more
			// often than that.
			if blocks%1000 == 0 {
				WriteTxIndexTail(batch, nextNum)
				if err := batch.Write(); err != nil {
					log.Crit("Failed writing batch to db", "error", err)
					return
				}
				batch.Reset()
			}
			// If we've spent too much time already, notify the user of what we're doing
			if time.Since(logged) > 8*time.Second {
				log.Info("Unindexing transactions", "blocks", blocks, "txs", txs, "total", to-from, "elapsed", common.PrettyDuration(time.Since(start)))
				logged = time.Now()
			}
		}
	}
	// Flush the new indexing tail and the last committed data. It can also happen
	// that the last batch is empty because nothing to unindex, but the tail has to
	// be flushed anyway.
	WriteTxIndexTail(batch, nextNum)
	if err := batch.Write(); err != nil {
		log.Crit("Failed writing batch to db", "error", err)
		return
	}
	select {
	case <-interrupt:
		log.Debug("Transaction unindexing interrupted", "blocks", blocks, "txs", txs, "tail", to, "elapsed", common.PrettyDuration(time.Since(start)))
	default:
		log.Info("Unindexed transactions", "blocks", blocks, "txs", txs, "tail", to, "elapsed", common.PrettyDuration(time.Since(start)))
	}
}

// UnindexTransactions removes txlookup indices of the specified block range.
//
// There is a passed channel, the whole procedure will be interrupted if any
// signal received.
func UnindexTransactions(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}) {
	unindexTransactions(db, from, to, interrupt, nil)
}

// unindexTransactionsForTesting is the internal debug version with an additional hook.
func unindexTransactionsForTesting(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}, hook func(uint64) bool) {
	unindexTransactions(db, from, to, interrupt, hook)
}

func TestChainIterator(t *testing.T) {
	// Construct test chain db
	chainDb := NewMemoryDatabase()

	var block *types.Block
	var txs []*types.Transaction
	to := common.BytesToAddress([]byte{0x11})
	block = types.NewBlock(&types.Header{Number: big.NewInt(int64(0))}, nil, nil, nil, newHasher()) // Empty genesis block
	WriteBlock(chainDb, block)
	WriteCanonicalHash(chainDb, block.Hash(), block.NumberU64())
	for i := uint64(1); i <= 10; i++ {
		var tx *types.Transaction
		if i%2 == 0 {
			tx = types.NewTx(&types.LegacyTx{
				Nonce:    i,
				GasPrice: big.NewInt(11111),
				Gas:      1111,
				To:       &to,
				Value:    big.NewInt(111),
				Data:     []byte{0x11, 0x11, 0x11},
			})
		} else {
			tx = types.NewTx(&types.AccessListTx{
				ChainID:  big.NewInt(1337),
				Nonce:    i,
				GasPrice: big.NewInt(11111),
				Gas:      1111,
				To:       &to,
				Value:    big.NewInt(111),
				Data:     []byte{0x11, 0x11, 0x11},
			})
		}
		txs = append(txs, tx)
		block = types.NewBlock(&types.Header{Number: big.NewInt(int64(i))}, []*types.Transaction{tx}, nil, nil, newHasher())
		WriteBlock(chainDb, block)
		WriteCanonicalHash(chainDb, block.Hash(), block.NumberU64())
	}

	var cases = []struct {
		from, to uint64
		reverse  bool
		expect   []int
	}{
		{0, 11, true, []int{10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}},
		{0, 0, true, nil},
		{0, 5, true, []int{4, 3, 2, 1, 0}},
		{10, 11, true, []int{10}},
		{0, 11, false, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{0, 0, false, nil},
		{10, 11, false, []int{10}},
	}
	for i, c := range cases {
		var numbers []int
		hashCh := iterateTransactions(chainDb, c.from, c.to, c.reverse, nil)
		if hashCh != nil {
			for h := range hashCh {
				numbers = append(numbers, int(h.number))
				if len(h.hashes) > 0 {
					if got, exp := h.hashes[0], txs[h.number-1].Hash(); got != exp {
						t.Fatalf("block %d: hash wrong, got %x exp %x", h.number, got, exp)
					}
				}
			}
		}
		if !c.reverse {
			sort.Ints(numbers)
		} else {
			sort.Sort(sort.Reverse(sort.IntSlice(numbers)))
		}
		if !reflect.DeepEqual(numbers, c.expect) {
			t.Fatalf("Case %d failed, visit element mismatch, want %v, got %v", i, c.expect, numbers)
		}
	}
}

func TestIndexTransactions(t *testing.T) {
	// Construct test chain db
	chainDb := NewMemoryDatabase()

	var block *types.Block
	var txs []*types.Transaction
	to := common.BytesToAddress([]byte{0x11})

	// Write empty genesis block
	block = types.NewBlock(&types.Header{Number: big.NewInt(int64(0))}, nil, nil, nil, newHasher())
	WriteBlock(chainDb, block)
	WriteCanonicalHash(chainDb, block.Hash(), block.NumberU64())

	for i := uint64(1); i <= 10; i++ {
		var tx *types.Transaction
		if i%2 == 0 {
			tx = types.NewTx(&types.LegacyTx{
				Nonce:    i,
				GasPrice: big.NewInt(11111),
				Gas:      1111,
				To:       &to,
				Value:    big.NewInt(111),
				Data:     []byte{0x11, 0x11, 0x11},
			})
		} else {
			tx = types.NewTx(&types.AccessListTx{
				ChainID:  big.NewInt(1337),
				Nonce:    i,
				GasPrice: big.NewInt(11111),
				Gas:      1111,
				To:       &to,
				Value:    big.NewInt(111),
				Data:     []byte{0x11, 0x11, 0x11},
			})
		}
		txs = append(txs, tx)
		block = types.NewBlock(&types.Header{Number: big.NewInt(int64(i))}, []*types.Transaction{tx}, nil, nil, newHasher())
		WriteBlock(chainDb, block)
		WriteCanonicalHash(chainDb, block.Hash(), block.NumberU64())
	}
	// verify checks whether the tx indices in the range [from, to)
	// is expected.
	verify := func(from, to int, exist bool, tail uint64) {
		for i := from; i < to; i++ {
			if i == 0 {
				continue
			}
			number := ReadTxLookupEntry(chainDb, txs[i-1].Hash())
			if exist && number == nil {
				t.Fatalf("Transaction index %d missing", i)
			}
			if !exist && number != nil {
				t.Fatalf("Transaction index %d is not deleted", i)
			}
		}
		number := ReadTxIndexTail(chainDb)
		if number == nil || *number != tail {
			t.Fatalf("Transaction tail mismatch")
		}
	}
	IndexTransactions(chainDb, 5, 11, nil)
	verify(5, 11, true, 5)
	verify(0, 5, false, 5)

	IndexTransactions(chainDb, 0, 5, nil)
	verify(0, 11, true, 0)

	UnindexTransactions(chainDb, 0, 5, nil)
	verify(5, 11, true, 5)
	verify(0, 5, false, 5)

	UnindexTransactions(chainDb, 5, 11, nil)
	verify(0, 11, false, 11)

	// Testing corner cases
	signal := make(chan struct{})
	var once sync.Once
	indexTransactionsForTesting(chainDb, 5, 11, signal, func(n uint64) bool {
		if n <= 8 {
			once.Do(func() {
				close(signal)
			})
			return false
		}
		return true
	})
	verify(9, 11, true, 9)
	verify(0, 9, false, 9)
	IndexTransactions(chainDb, 0, 9, nil)

	signal = make(chan struct{})
	var once2 sync.Once
	unindexTransactionsForTesting(chainDb, 0, 11, signal, func(n uint64) bool {
		if n >= 8 {
			once2.Do(func() {
				close(signal)
			})
			return false
		}
		return true
	})
	verify(8, 11, true, 8)
	verify(0, 8, false, 8)
}
