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

package rawdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"
	"sort"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
)

// ReadCanonicalHash retrieves the hash assigned to a canonical block number.
func ReadCanonicalHash(db ethdb.Reader, number uint64) common.Hash {
	data, _ := db.Get(headerHashKey(number))

	if len(data) == 0 {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// WriteCanonicalHash stores the hash assigned to a canonical block number.
func WriteCanonicalHash(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Put(headerHashKey(number), hash.Bytes()); err != nil {
		log.Fatal("Failed to store number to hash mapping", "err", err)
	}
}

// DeleteCanonicalHash removes the number to hash canonical mapping.
func DeleteCanonicalHash(db ethdb.KeyValueWriter, number uint64) {
	if err := db.Delete(headerHashKey(number)); err != nil {
		log.Fatal("Failed to delete number to hash mapping", "err", err)
	}
}

// ReadAllHashes retrieves all the hashes assigned to blocks at a certain heights,
// both canonical and reorged forks included.
func ReadAllHashes(db ethdb.Iteratee, number uint64) []common.Hash {
	prefix := headerKeyPrefix(number)

	hashes := make([]common.Hash, 0, 1)
	it := db.NewIterator(prefix, nil)
	defer it.Release()

	for it.Next() {
		if key := it.Key(); len(key) == len(prefix)+32 {
			hashes = append(hashes, common.BytesToHash(key[len(key)-32:]))
		}
	}
	return hashes
}

// ReadAllCanonicalHashes retrieves all canonical number and hash mappings at the
// certain chain range. If the accumulated entries reaches the given threshold,
// abort the iteration and return the semi-finish result.
func ReadAllCanonicalHashes(db ethdb.Iteratee, from uint64, to uint64, limit int) ([]uint64, []common.Hash) {
	// Short circuit if the limit is 0.
	if limit == 0 {
		return nil, nil
	}
	var (
		numbers []uint64
		hashes  []common.Hash
	)
	// Construct the key prefix of start point.
	start, end := headerHashKey(from), headerHashKey(to)
	it := db.NewIterator(nil, start)
	defer it.Release()

	for it.Next() {
		if bytes.Compare(it.Key(), end) >= 0 {
			break
		}
		if key := it.Key(); len(key) == len(headerPrefix)+8+1 && bytes.Equal(key[len(key)-1:], headerHashSuffix) {
			numbers = append(numbers, binary.BigEndian.Uint64(key[len(headerPrefix):len(headerPrefix)+8]))
			hashes = append(hashes, common.BytesToHash(it.Value()))
			// If the accumulated entries reaches the limit threshold, return.
			if len(numbers) >= limit {
				break
			}
		}
	}
	return numbers, hashes
}

// ReadHeaderNumber returns the header number assigned to a hash.
func ReadHeaderNumber(db ethdb.KeyValueReader, hash common.Hash) *uint64 {
	data, _ := db.Get(headerNumberKey(hash))
	if len(data) != 8 {
		return nil
	}
	number := binary.BigEndian.Uint64(data)
	return &number
}

// WriteHeaderNumber stores the hash->number mapping.
func WriteHeaderNumber(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	key := headerNumberKey(hash)
	enc := encodeBlockNumber(number)
	if err := db.Put(key, enc); err != nil {
		log.Fatal("Failed to store hash to number mapping", "err", err)
	}
}

// DeleteHeaderNumber removes hash->number mapping.
func DeleteHeaderNumber(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(headerNumberKey(hash)); err != nil {
		log.Fatal("Failed to delete hash to number mapping", "err", err)
	}
}

// ReadHeadHeaderHash retrieves the hash of the current canonical head header.
func ReadHeadHeaderHash(db ethdb.KeyValueReader) common.Hash {
	data, _ := db.Get(headHeaderKey)
	if len(data) == 0 {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// WriteHeadHeaderHash stores the hash of the current canonical head header.
func WriteHeadHeaderHash(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Put(headHeaderKey, hash.Bytes()); err != nil {
		log.Fatal("Failed to store last header's hash", "err", err)
	}
}

// ReadHeadBlockHash retrieves the hash of the current canonical head block.
func ReadHeadBlockHash(db ethdb.KeyValueReader) common.Hash {
	data, _ := db.Get(headBlockKey)
	if len(data) == 0 {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// WriteHeadBlockHash stores the head block's hash.
func WriteHeadBlockHash(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Put(headBlockKey, hash.Bytes()); err != nil {
		log.Fatal("Failed to store last block's hash", "err", err)
	}
}

// ReadLastPivotNumber retrieves the number of the last pivot block. If the node
// full synced, the last pivot will always be nil.
func ReadLastPivotNumber(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(lastPivotKey)
	if len(data) == 0 {
		return nil
	}
	var pivot uint64
	if err := rlp.DecodeBytes(data, &pivot); err != nil {
		log.Error("Invalid pivot block number in database", "err", err)
		return nil
	}
	return &pivot
}

// WriteLastPivotNumber stores the number of the last pivot block.
func WriteLastPivotNumber(db ethdb.KeyValueWriter, pivot uint64) {
	enc, err := rlp.EncodeToBytes(pivot)
	if err != nil {
		log.Fatal("Failed to encode pivot block number", "err", err)
	}
	if err := db.Put(lastPivotKey, enc); err != nil {
		log.Fatal("Failed to store pivot block number", "err", err)
	}
}

// ReadFastTrieProgress retrieves the number of tries nodes fast synced to allow
// reporting correct numbers across restarts.
func ReadFastTrieProgress(db ethdb.KeyValueReader) uint64 {
	data, _ := db.Get(fastTrieProgressKey)
	if len(data) == 0 {
		return 0
	}
	return new(big.Int).SetBytes(data).Uint64()
}

// WriteFastTrieProgress stores the fast sync trie process counter to support
// retrieving it across restarts.
func WriteFastTrieProgress(db ethdb.KeyValueWriter, count uint64) {
	if err := db.Put(fastTrieProgressKey, new(big.Int).SetUint64(count).Bytes()); err != nil {
		log.Fatal("Failed to store fast sync trie progress", "err", err)
	}
}

// ReadTxIndexTail retrieves the number of oldest indexed block
// whose transaction indices has been indexed. If the corresponding entry
// is non-existent in database it means the indexing has been finished.
func ReadTxIndexTail(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(txIndexTailKey)
	if len(data) != 8 {
		return nil
	}
	number := binary.BigEndian.Uint64(data)
	return &number
}

// WriteTxIndexTail stores the number of oldest indexed block
// into database.
func WriteTxIndexTail(db ethdb.KeyValueWriter, number uint64) {
	if err := db.Put(txIndexTailKey, encodeBlockNumber(number)); err != nil {
		log.Fatal("Failed to store the transaction index tail", "err", err)
	}
}

// ReadFastTxLookupLimit retrieves the tx lookup limit used in fast sync.
func ReadFastTxLookupLimit(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(fastTxLookupLimitKey)
	if len(data) != 8 {
		return nil
	}
	number := binary.BigEndian.Uint64(data)
	return &number
}

// WriteFastTxLookupLimit stores the txlookup limit used in fast sync into database.
func WriteFastTxLookupLimit(db ethdb.KeyValueWriter, number uint64) {
	if err := db.Put(fastTxLookupLimitKey, encodeBlockNumber(number)); err != nil {
		log.Fatal("Failed to store transaction lookup limit for fast sync", "err", err)
	}
}

// ReadHeaderRLP retrieves a block header in its raw RLP database encoding.
func ReadHeaderRLP(db ethdb.Reader, hash common.Hash, number uint64) rlp.RawValue {
	// First try to look up the data in ancient database. Extra hash
	// comparison is necessary since ancient database only maintains
	// the canonical data.
	data, _ := db.Ancient(freezerHeaderTable, number)
	if len(data) > 0 && crypto.Keccak256Hash(data) == hash {
		return data
	}
	// Then try to look up the data in leveldb.
	data, _ = db.Get(headerKey(number, hash))
	if len(data) > 0 {
		return data
	}
	// In the background freezer is moving data from leveldb to flatten files.
	// So during the first check for ancient db, the data is not yet in there,
	// but when we reach into leveldb, the data was already moved. That would
	// result in a not found error.
	data, _ = db.Ancient(freezerHeaderTable, number)
	if len(data) > 0 && crypto.Keccak256Hash(data) == hash {
		return data
	}
	return nil // Can't find the data anywhere.
}

// HasHeader verifies the existence of a block header corresponding to the hash.
func HasHeader(db ethdb.Reader, hash common.Hash, number uint64) bool {
	if has, err := db.Ancient(freezerHashTable, number); err == nil && common.BytesToHash(has) == hash {
		return true
	}
	if has, err := db.Has(headerKey(number, hash)); !has || err != nil {
		return false
	}
	return true
}

// ReadHeader retrieves the block header corresponding to the hash.
func ReadHeader(db ethdb.Reader, hash common.Hash, number uint64) *types.Header {
	data := ReadHeaderRLP(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	header := new(types.Header)
	if err := rlp.Decode(bytes.NewReader(data), header); err != nil {
		log.Error("Invalid block header RLP", "hash", hash, "err", err)
		return nil
	}
	return header
}

// WriteHeader stores a block header into the database and also stores the hash-
// to-number mapping.
func WriteHeader(db ethdb.KeyValueWriter, header *types.Header) {
	var (
		hash   = header.Hash()
		number = header.NumberU64()
	)
	// Write the hash -> number mapping
	WriteHeaderNumber(db, hash, number)

	// Write the encoded header
	data, err := rlp.EncodeToBytes(header)
	if err != nil {
		log.Fatal("Failed to RLP encode header", "err", err)
	}
	key := headerKey(number, hash)
	if err := db.Put(key, data); err != nil {
		log.Fatal("Failed to store header", "err", err)
	}
}

// DeleteHeader removes all block header data associated with a hash.
func DeleteHeader(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	deleteHeaderWithoutNumber(db, hash, number)
	if err := db.Delete(headerNumberKey(hash)); err != nil {
		log.Fatal("Failed to delete hash to number mapping", "err", err)
	}
}

// deleteHeaderWithoutNumber removes only the block header but does not remove
// the hash to number mapping.
func deleteHeaderWithoutNumber(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(headerKey(number, hash)); err != nil {
		log.Fatal("Failed to delete header", "err", err)
	}
}

// ReadBodyRLP retrieves the block body (transactions and uncles) in RLP encoding.
func ReadBodyRLP(db ethdb.Reader, hash common.Hash, number uint64) rlp.RawValue {
	// First try to look up the data in ancient database. Extra hash
	// comparison is necessary since ancient database only maintains
	// the canonical data.
	data, _ := db.Ancient(freezerBodiesTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data
		}
	}
	// Then try to look up the data in leveldb.
	data, _ = db.Get(blockBodyKey(number, hash))
	if len(data) > 0 {
		return data
	}
	// In the background freezer is moving data from leveldb to flatten files.
	// So during the first check for ancient db, the data is not yet in there,
	// but when we reach into leveldb, the data was already moved. That would
	// result in a not found error.
	data, _ = db.Ancient(freezerBodiesTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data
		}
	}
	return nil // Can't find the data anywhere.
}

// ReadCanonicalBodyRLP retrieves the block body (transactions and uncles) for the canonical
// block at number, in RLP encoding.
func ReadCanonicalBodyRLP(db ethdb.Reader, number uint64) rlp.RawValue {
	// If it's an ancient one, we don't need the canonical hash
	data, _ := db.Ancient(freezerBodiesTable, number)
	if len(data) == 0 {
		// Need to get the hash
		data, _ = db.Get(blockBodyKey(number, ReadCanonicalHash(db, number)))
		// In the background freezer is moving data from leveldb to flatten files.
		// So during the first check for ancient db, the data is not yet in there,
		// but when we reach into leveldb, the data was already moved. That would
		// result in a not found error.
		if len(data) == 0 {
			data, _ = db.Ancient(freezerBodiesTable, number)
		}
	}
	return data
}

// WriteBodyRLP stores an RLP encoded block body into the database.
func WriteBodyRLP(db ethdb.KeyValueWriter, hash common.Hash, number uint64, rlp rlp.RawValue) {
	if err := db.Put(blockBodyKey(number, hash), rlp); err != nil {
		log.Fatal("Failed to store block body", "err", err)
	}
}

// HasBody verifies the existence of a block body corresponding to the hash.
func HasBody(db ethdb.Reader, hash common.Hash, number uint64) bool {
	if has, err := db.Ancient(freezerHashTable, number); err == nil && common.BytesToHash(has) == hash {
		return true
	}
	if has, err := db.Has(blockBodyKey(number, hash)); !has || err != nil {
		return false
	}
	return true
}

// ReadBody retrieves the block body corresponding to the hash.
func ReadBody(db ethdb.Reader, hash common.Hash, number uint64) *types.Body {
	data := ReadBodyRLP(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	body := new(types.Body)
	if err := rlp.Decode(bytes.NewReader(data), body); err != nil {
		log.Error("Invalid block body RLP", "hash", hash, "err", err)
		return nil
	}
	return body
}

// WriteBody stores a block body into the database.
func WriteBody(db ethdb.KeyValueWriter, hash common.Hash, number uint64, body *types.Body) {
	data, err := rlp.EncodeToBytes(body)
	if err != nil {
		log.Fatal("Failed to RLP encode body", "err", err)
	}
	WriteBodyRLP(db, hash, number, data)
}

// DeleteBody removes all block body data associated with a hash.
func DeleteBody(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(blockBodyKey(number, hash)); err != nil {
		log.Fatal("Failed to delete block body", "err", err)
	}
}

// ReadPbCacheBody retrieves the block body corresponding to the hash.
func ReadPbCacheBody(db ethdb.Reader, hash common.Hash) *types.Body {
	data, err := db.Get(pbBodyKey(hash))
	if err != nil {
		log.Error("Failed to read block body", "hash", hash, "err", err)
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	body := new(types.Body)
	if err := rlp.Decode(bytes.NewReader(data), body); err != nil {
		log.Error("Invalid pending block body RLP", "hash", hash, "err", err)
		return nil
	}
	return body
}

// WritePbCacheBody stores a block body into the database.
func WritePbCacheBody(db ethdb.KeyValueWriter, hash common.Hash, body *types.Body) {
	data, err := rlp.EncodeToBytes(body)
	if err != nil {
		log.Fatal("Failed to RLP encode body", "err", err)
	}
	if err := db.Put(pbBodyKey(hash), data); err != nil {
		log.Fatal("Failed to write pbBodyKey", "err", err)
	}
}

// DeletePbCacheBody removes all block body data associated with a hash.
func DeletePbCacheBody(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pbBodyKey(hash)); err != nil {
		log.Fatal("Failed to delete pb cache body", "err", err)
	}
}

// ReadPbBodyKeys retreive's the phBodyKeys of the worker
func ReadPbBodyKeys(db ethdb.Reader) []common.Hash {
	key := pbBodyHashKey()
	data, err := db.Get(key)
	if err != nil {
		log.Error("Error in Reading pbBodyKeys", "err", err)
		return nil
	}
	if len(data) == 0 {
		return []common.Hash{}
	}
	keys := []common.Hash{}
	if err := rlp.DecodeBytes(data, &keys); err != nil {
		return []common.Hash{}
	}
	return keys
}

// WritePbBodyKeys writes the workers pendingHeaderBody keys to the db
func WritePbBodyKeys(db ethdb.KeyValueWriter, hashes []common.Hash) {
	key := pbBodyHashKey()
	data, err := rlp.EncodeToBytes(hashes)
	if err != nil {
		log.Fatal("Failed to RLP encode pending block body keys", "err", err)
	}
	if err := db.Put(key, data); err != nil {
		log.Fatal("Failed to store pending block body keys", "err", err)
	}
}

// DeleteAllPbBodyKeys delete the pendingHeaderBody keys to the db
func DeleteAllPbBodyKeys(db ethdb.KeyValueWriter) {
	key := pbBodyHashKey()

	if err := db.Delete(key); err != nil {
		log.Fatal("Failed to delete pending block body keys", "err", err)
	}
}

// ReadHeadsHashes retreive's the heads hashes of the blockchain.
func ReadTermini(db ethdb.Reader, hash common.Hash) *types.Termini {
	key := terminiKey(hash)
	data, _ := db.Get(key)
	if len(data) == 0 {
		return nil
	}
	var termini types.Termini
	if err := rlp.DecodeBytes(data, &termini); err != nil {
		return nil
	}
	return &termini
}

// WriteTermini writes the heads hashes of the blockchain.
func WriteTermini(db ethdb.KeyValueWriter, index common.Hash, hashes types.Termini) {
	key := terminiKey(index)
	data, err := rlp.EncodeToBytes(hashes)
	if err != nil {
		log.Fatal("Failed to RLP encode termini", "err", err)
	}
	if err := db.Put(key, data); err != nil {
		log.Fatal("Failed to store last block's termini", "err", err)
	}
}

// DeleteTermini writes the heads hashes of the blockchain.
func DeleteTermini(db ethdb.KeyValueWriter, hash common.Hash) {
	key := terminiKey(hash)

	if err := db.Delete(key); err != nil {
		log.Fatal("Failed to delete termini ", "err", err)
	}
}

// ReadPendingHeader retreive's the pending header stored in hash.
func ReadPendingHeader(db ethdb.Reader, hash common.Hash) *types.PendingHeader {
	key := pendingHeaderKey(hash)
	data, _ := db.Get(key)
	if len(data) == 0 {
		return nil
	}

	pendingHeader := new(types.PendingHeader)
	if err := rlp.Decode(bytes.NewReader(data), pendingHeader); err != nil {
		log.Error("Invalid pendingHeader RLP", "Err", err)
		return nil
	}
	return pendingHeader
}

// WritePendingHeader writes the pending header of the terminus hash.
func WritePendingHeader(db ethdb.KeyValueWriter, hash common.Hash, pendingHeader types.PendingHeader) {
	key := pendingHeaderKey(hash)

	// Write the encoded pending header
	data, err := rlp.EncodeToBytes(pendingHeader)
	if err != nil {
		log.Fatal("Failed to RLP encode pending header", "err", err)
	}

	if err := db.Put(key, data); err != nil {
		log.Fatal("Failed to store header", "err", err)
	}
}

// DeletePendingHeader deletes the pending header stored for the header hash.
func DeletePendingHeader(db ethdb.KeyValueWriter, hash common.Hash) {
	key := pendingHeaderKey(hash)
	if err := db.Delete(key); err != nil {
		log.Fatal("Failed to delete slice pending header ", "err", err)
	}
}

// ReadPhCache retreive's the heads hashes of the blockchain.
func ReadPhCache(db ethdb.Reader) map[common.Hash]types.PendingHeader {
	data, _ := db.Get(phCacheKey)
	// get the ph cache keys.
	if len(data) == 0 {
		return make(map[common.Hash]types.PendingHeader)
	}
	hashes := []common.Hash{}
	if err := rlp.DecodeBytes(data, &hashes); err != nil {
		return make(map[common.Hash]types.PendingHeader)
	}

	phCache := make(map[common.Hash]types.PendingHeader)
	// Read the pending header and phBody.
	for _, hash := range hashes {
		pendingHeader := ReadPendingHeader(db, hash)
		if pendingHeader != nil {
			phCache[hash] = *pendingHeader
		}
	}
	return phCache
}

// WritePhCache writes the heads hashes of the blockchain.
func WritePhCache(db ethdb.KeyValueWriter, phCache map[common.Hash]types.PendingHeader) {
	var hashes []common.Hash
	for hash, pendingHeader := range phCache {
		hashes = append(hashes, hash)
		WritePendingHeader(db, hash, pendingHeader)
	}

	data, err := rlp.EncodeToBytes(hashes)
	if err != nil {
		log.Fatal("Failed to RLP encode block total difficulty", "err", err)
	}
	if err := db.Put(phCacheKey, data); err != nil {
		log.Fatal("Failed to store last block's hash", "err", err)
	}
}

// DeletePhCache writes the heads hashes of the blockchain.
func DeletePhCache(db ethdb.KeyValueWriter) {
	if err := db.Delete(phCacheKey); err != nil {
		log.Fatal("Failed to delete ph cache", "err", err)
	}
}

// ReadBestPhKey retreive's the bestPhKey of the blockchain
func ReadBestPhKey(db ethdb.Reader) common.Hash {
	data, _ := db.Get(phHeadKey)
	// get the ph cache keys.
	if len(data) == 0 {
		return common.Hash{}
	}
	bestPhKey := common.Hash{}
	if err := rlp.DecodeBytes(data, &bestPhKey); err != nil {
		return common.Hash{}
	}
	return bestPhKey
}

// WriteBestPhKey writes the bestPhKey of the blockchain
func WriteBestPhKey(db ethdb.KeyValueWriter, bestPhKey common.Hash) {
	data, err := rlp.EncodeToBytes(bestPhKey)
	if err != nil {
		log.Fatal("Failed to RLP encode write best ph key", "err", err)
	}
	if err := db.Put(phHeadKey, data); err != nil {
		log.Fatal("Failed to store last block's hash", "err", err)
	}
}

// DeleteBestPhKey delete the bestPhKey of the blockchain
func DeleteBestPhKey(db ethdb.KeyValueWriter) {
	if err := db.Delete(phHeadKey); err != nil {
		log.Fatal("Failed to delete ph head", "err", err)
	}
}

// ReadHeadsHashes retreive's the heads hashes of the blockchain.
func ReadHeadsHashes(db ethdb.Reader) []common.Hash {
	data, _ := db.Get(headsHashesKey)
	if len(data) == 0 {
		return []common.Hash{}
	}
	hashes := []common.Hash{}
	if err := rlp.DecodeBytes(data, &hashes); err != nil {
		return []common.Hash{}
	}
	return hashes
}

// WriteHeadsHashes writes the heads hashes of the blockchain.
func WriteHeadsHashes(db ethdb.KeyValueWriter, hashes []common.Hash) {
	data, err := rlp.EncodeToBytes(hashes)
	if err != nil {
		log.Fatal("Failed to RLP encode block total difficulty", "err", err)
	}
	if err := db.Put(headsHashesKey, data); err != nil {
		log.Fatal("Failed to store last block's hash", "err", err)
	}
}

// DeleteAllHeadsHashes writes the heads hashes of the blockchain.
func DeleteAllHeadsHashes(db ethdb.KeyValueWriter) {
	if err := db.Delete(headsHashesKey); err != nil {
		log.Fatal("Failed to delete block total difficulty", "err", err)
	}
}

// HasReceipts verifies the existence of all the transaction receipts belonging
// to a block.
func HasReceipts(db ethdb.Reader, hash common.Hash, number uint64) bool {
	if has, err := db.Ancient(freezerHashTable, number); err == nil && common.BytesToHash(has) == hash {
		return true
	}
	if has, err := db.Has(blockReceiptsKey(number, hash)); !has || err != nil {
		return false
	}
	return true
}

// ReadReceiptsRLP retrieves all the transaction receipts belonging to a block in RLP encoding.
func ReadReceiptsRLP(db ethdb.Reader, hash common.Hash, number uint64) rlp.RawValue {
	// First try to look up the data in ancient database. Extra hash
	// comparison is necessary since ancient database only maintains
	// the canonical data.
	data, _ := db.Ancient(freezerReceiptTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data
		}
	}
	// Then try to look up the data in leveldb.
	data, _ = db.Get(blockReceiptsKey(number, hash))
	if len(data) > 0 {
		return data
	}
	// In the background freezer is moving data from leveldb to flatten files.
	// So during the first check for ancient db, the data is not yet in there,
	// but when we reach into leveldb, the data was already moved. That would
	// result in a not found error.
	data, _ = db.Ancient(freezerReceiptTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data
		}
	}
	return nil // Can't find the data anywhere.
}

// ReadRawReceipts retrieves all the transaction receipts belonging to a block.
// The receipt metadata fields are not guaranteed to be populated, so they
// should not be used. Use ReadReceipts instead if the metadata is needed.
func ReadRawReceipts(db ethdb.Reader, hash common.Hash, number uint64) types.Receipts {
	// Retrieve the flattened receipt slice
	data := ReadReceiptsRLP(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	// Convert the receipts from their storage form to their internal representation
	storageReceipts := []*types.ReceiptForStorage{}
	if err := rlp.DecodeBytes(data, &storageReceipts); err != nil {
		log.Error("Invalid receipt array RLP", "hash", hash, "err", err)
		return nil
	}
	receipts := make(types.Receipts, len(storageReceipts))
	for i, storageReceipt := range storageReceipts {
		receipts[i] = (*types.Receipt)(storageReceipt)
	}
	return receipts
}

// ReadReceipts retrieves all the transaction receipts belonging to a block, including
// its correspoinding metadata fields. If it is unable to populate these metadata
// fields then nil is returned.
//
// The current implementation populates these metadata fields by reading the receipts'
// corresponding block body, so if the block body is not found it will return nil even
// if the receipt itself is stored.
func ReadReceipts(db ethdb.Reader, hash common.Hash, number uint64, config *params.ChainConfig) types.Receipts {
	// We're deriving many fields from the block body, retrieve beside the receipt
	receipts := ReadRawReceipts(db, hash, number)
	if receipts == nil {
		return nil
	}
	body := ReadBody(db, hash, number)
	if body == nil {
		log.Error("Missing body but have receipt", "hash", hash, "number", number)
		return nil
	}
	if err := receipts.DeriveFields(config, hash, number, body.Transactions); err != nil {
		log.Error("Failed to derive block receipts fields", "hash", hash, "number", number, "err", err)
		return nil
	}
	return receipts
}

// WriteReceipts stores all the transaction receipts belonging to a block.
func WriteReceipts(db ethdb.KeyValueWriter, hash common.Hash, number uint64, receipts types.Receipts) {
	// Convert the receipts into their storage form and serialize them
	storageReceipts := make([]*types.ReceiptForStorage, len(receipts))
	for i, receipt := range receipts {
		storageReceipts[i] = (*types.ReceiptForStorage)(receipt)
	}
	bytes, err := rlp.EncodeToBytes(storageReceipts)
	if err != nil {
		log.Fatal("Failed to encode block receipts", "err", err)
	}
	// Store the flattened receipt slice
	if err := db.Put(blockReceiptsKey(number, hash), bytes); err != nil {
		log.Fatal("Failed to store block receipts", "err", err)
	}
}

// DeleteReceipts removes all receipt data associated with a block hash.
func DeleteReceipts(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(blockReceiptsKey(number, hash)); err != nil {
		log.Fatal("Failed to delete block receipts", "err", err)
	}
}

// storedReceiptRLP is the storage encoding of a receipt.
// Re-definition in core/types/receipt.go.
type storedReceiptRLP struct {
	PostStateOrStatus []byte
	CumulativeGasUsed uint64
	Logs              []*types.LogForStorage
}

// ReceiptLogs is a barebone version of ReceiptForStorage which only keeps
// the list of logs. When decoding a stored receipt into this object we
// avoid creating the bloom filter.
type receiptLogs struct {
	Logs []*types.Log
}

// DecodeRLP implements rlp.Decoder.
func (r *receiptLogs) DecodeRLP(s *rlp.Stream) error {
	var stored storedReceiptRLP
	if err := s.Decode(&stored); err != nil {
		return err
	}
	r.Logs = make([]*types.Log, len(stored.Logs))
	for i, log := range stored.Logs {
		r.Logs[i] = (*types.Log)(log)
	}
	return nil
}

// DeriveLogFields fills the logs in receiptLogs with information such as block number, txhash, etc.
func deriveLogFields(receipts []*receiptLogs, hash common.Hash, number uint64, txs types.Transactions) error {
	logIndex := uint(0)
	if len(txs) != len(receipts) {
		return errors.New("transaction and receipt count mismatch")
	}
	for i := 0; i < len(receipts); i++ {
		txHash := txs[i].Hash()
		// The derived log fields can simply be set from the block and transaction
		for j := 0; j < len(receipts[i].Logs); j++ {
			receipts[i].Logs[j].BlockNumber = number
			receipts[i].Logs[j].BlockHash = hash
			receipts[i].Logs[j].TxHash = txHash
			receipts[i].Logs[j].TxIndex = uint(i)
			receipts[i].Logs[j].Index = logIndex
			logIndex++
		}
	}
	return nil
}

// ReadLogs retrieves the logs for all transactions in a block. The log fields
// are populated with metadata. In case the receipts or the block body
// are not found, a nil is returned.
func ReadLogs(db ethdb.Reader, hash common.Hash, number uint64) [][]*types.Log {
	// Retrieve the flattened receipt slice
	data := ReadReceiptsRLP(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	receipts := []*receiptLogs{}
	if err := rlp.DecodeBytes(data, &receipts); err != nil {
		log.Error("Invalid receipt array RLP", "hash", hash, "err", err)
		return nil
	}

	body := ReadBody(db, hash, number)
	if body == nil {
		log.Error("Missing body but have receipt", "hash", hash, "number", number)
		return nil
	}
	if err := deriveLogFields(receipts, hash, number, body.Transactions); err != nil {
		log.Error("Failed to derive block receipts fields", "hash", hash, "number", number, "err", err)
		return nil
	}
	logs := make([][]*types.Log, len(receipts))
	for i, receipt := range receipts {
		logs[i] = receipt.Logs
	}
	return logs
}

// ReadBlock retrieves an entire block corresponding to the hash, assembling it
// back from the stored header and body. If either the header or body could not
// be retrieved nil is returned.
//
// Note, due to concurrent download of header and block body the header and thus
// canonical hash can be stored in the database but the body data not (yet).
func ReadBlock(db ethdb.Reader, hash common.Hash, number uint64) *types.Block {
	header := ReadHeader(db, hash, number)
	if header == nil {
		return nil
	}
	body := ReadBody(db, hash, number)
	if body == nil {
		return nil
	}
	return types.NewBlockWithHeader(header).WithBody(body.Transactions, body.Uncles, body.ExtTransactions, body.SubManifest)
}

// WriteBlock serializes a block into the database, header and body separately.
func WriteBlock(db ethdb.KeyValueWriter, block *types.Block) {
	WriteBody(db, block.Hash(), block.NumberU64(), block.Body())
	WriteHeader(db, block.Header())
}

// DeleteBlock removes all block data associated with a hash.
func DeleteBlock(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	DeleteReceipts(db, hash, number)
	DeleteHeader(db, hash, number)
	DeleteBody(db, hash, number)
}

// DeleteBlockWithoutNumber removes all block data associated with a hash, except
// the hash to number mapping.
func DeleteBlockWithoutNumber(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	DeleteReceipts(db, hash, number)
	deleteHeaderWithoutNumber(db, hash, number)
	DeleteBody(db, hash, number)
}

const badBlockToKeep = 10

type badBlock struct {
	Header *types.Header
	Body   *types.Body
}

// badBlockList implements the sort interface to allow sorting a list of
// bad blocks by their number in the reverse order.
type badBlockList []*badBlock

func (s badBlockList) Len() int { return len(s) }
func (s badBlockList) Less(i, j int) bool {
	return s[i].Header.NumberU64() < s[j].Header.NumberU64()
}
func (s badBlockList) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// ReadBadBlock retrieves the bad block with the corresponding block hash.
func ReadBadBlock(db ethdb.Reader, hash common.Hash) *types.Block {
	blob, err := db.Get(badBlockKey)
	if err != nil {
		return nil
	}
	var badBlocks badBlockList
	if err := rlp.DecodeBytes(blob, &badBlocks); err != nil {
		return nil
	}
	for _, bad := range badBlocks {
		if bad.Header.Hash() == hash {
			return types.NewBlockWithHeader(bad.Header).WithBody(bad.Body.Transactions, bad.Body.Uncles, bad.Body.ExtTransactions, bad.Body.SubManifest)
		}
	}
	return nil
}

// ReadAllBadBlocks retrieves all the bad blocks in the database.
// All returned blocks are sorted in reverse order by number.
func ReadAllBadBlocks(db ethdb.Reader) []*types.Block {
	blob, err := db.Get(badBlockKey)
	if err != nil {
		return nil
	}
	var badBlocks badBlockList
	if err := rlp.DecodeBytes(blob, &badBlocks); err != nil {
		return nil
	}
	var blocks []*types.Block
	for _, bad := range badBlocks {
		blocks = append(blocks, types.NewBlockWithHeader(bad.Header).WithBody(bad.Body.Transactions, bad.Body.Uncles, bad.Body.ExtTransactions, bad.Body.SubManifest))
	}
	return blocks
}

// WriteBadBlock serializes the bad block into the database. If the cumulated
// bad blocks exceeds the limitation, the oldest will be dropped.
func WriteBadBlock(db ethdb.KeyValueStore, block *types.Block) {
	blob, err := db.Get(badBlockKey)
	if err != nil {
		log.Warn("Failed to load old bad blocks", "error", err)
	}
	var badBlocks badBlockList
	if len(blob) > 0 {
		if err := rlp.DecodeBytes(blob, &badBlocks); err != nil {
			log.Fatal("Failed to decode old bad blocks", "error", err)
		}
	}
	for _, b := range badBlocks {
		if b.Header.NumberU64() == block.NumberU64() && b.Header.Hash() == block.Hash() {
			log.Info("Skip duplicated bad block", "number", block.NumberU64(), "hash", block.Hash())
			return
		}
	}
	badBlocks = append(badBlocks, &badBlock{
		Header: block.Header(),
		Body:   block.Body(),
	})
	sort.Sort(sort.Reverse(badBlocks))
	if len(badBlocks) > badBlockToKeep {
		badBlocks = badBlocks[:badBlockToKeep]
	}
	data, err := rlp.EncodeToBytes(badBlocks)
	if err != nil {
		log.Fatal("Failed to encode bad blocks", "err", err)
	}
	if err := db.Put(badBlockKey, data); err != nil {
		log.Fatal("Failed to write bad blocks", "err", err)
	}
}

// DeleteBadBlocks deletes all the bad blocks from the database
func DeleteBadBlocks(db ethdb.KeyValueWriter) {
	if err := db.Delete(badBlockKey); err != nil {
		log.Fatal("Failed to delete bad blocks", "err", err)
	}
}

// FindCommonAncestor returns the last common ancestor of two block headers
func FindCommonAncestor(db ethdb.Reader, a, b *types.Header) *types.Header {
	for bn := b.NumberU64(); a.NumberU64() > bn; {
		a = ReadHeader(db, a.ParentHash(), a.NumberU64()-1)
		if a == nil {
			return nil
		}
	}
	for an := a.NumberU64(); an < b.NumberU64(); {
		b = ReadHeader(db, b.ParentHash(), b.NumberU64()-1)
		if b == nil {
			return nil
		}
	}
	for a.Hash() != b.Hash() {
		a = ReadHeader(db, a.ParentHash(), a.NumberU64()-1)
		if a == nil {
			return nil
		}
		b = ReadHeader(db, b.ParentHash(), b.NumberU64()-1)
		if b == nil {
			return nil
		}
	}
	return a
}

// ReadHeadHeader returns the current canonical head header.
func ReadHeadHeader(db ethdb.Reader) *types.Header {
	headHeaderHash := ReadHeadHeaderHash(db)
	if headHeaderHash == (common.Hash{}) {
		return nil
	}
	headHeaderNumber := ReadHeaderNumber(db, headHeaderHash)
	if headHeaderNumber == nil {
		return nil
	}
	return ReadHeader(db, headHeaderHash, *headHeaderNumber)
}

// ReadHeadBlock returns the current canonical head block.
func ReadHeadBlock(db ethdb.Reader) *types.Block {
	headBlockHash := ReadHeadBlockHash(db)
	if headBlockHash == (common.Hash{}) {
		return nil
	}
	headBlockNumber := ReadHeaderNumber(db, headBlockHash)
	if headBlockNumber == nil {
		return nil
	}
	return ReadBlock(db, headBlockHash, *headBlockNumber)
}

// ReadEtxSetRLP retrieves the EtxSet corresponding to a given block, in RLP encoding.
func ReadEtxSetRLP(db ethdb.Reader, hash common.Hash, number uint64) rlp.RawValue {
	// First try to look up the data in ancient database. Extra hash
	// comparison is necessary since ancient database only maintains
	// the canonical data.
	data, _ := db.Ancient(freezerEtxSetsTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data
		}
	}
	// Then try to look up the data in leveldb.
	data, _ = db.Get(etxSetKey(number, hash))
	if len(data) > 0 {
		return data
	}
	// In the background freezer is moving data from leveldb to flatten files.
	// So during the first check for ancient db, the data is not yet in there,
	// but when we reach into leveldb, the data was already moved. That would
	// result in a not found error.
	data, _ = db.Ancient(freezerEtxSetsTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data
		}
	}
	return nil // Can't find the data anywhere.
}

// WriteEtxSetRLP stores the EtxSet corresponding to a given block, in RLP encoding.
func WriteEtxSetRLP(db ethdb.KeyValueWriter, hash common.Hash, number uint64, rlp rlp.RawValue) {
	if err := db.Put(etxSetKey(number, hash), rlp); err != nil {
		log.Fatal("Failed to store etx set", "err", err)
	}
}

type EtxSetEntry struct {
	EtxHash   common.Hash
	EtxHeight uint64
	Etx       types.Transaction
}

// ReadEtxSet retreives the EtxSet corresponding to a given block
func ReadEtxSet(db ethdb.Reader, hash common.Hash, number uint64) types.EtxSet {
	data := ReadEtxSetRLP(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	var entries []EtxSetEntry
	if err := rlp.Decode(bytes.NewReader(data), &entries); err != nil {
		log.Error("Invalid etx set RLP", "hash", hash, "err", err)
		return nil
	}
	etxSet := make(types.EtxSet)
	for _, entry := range entries {
		etxSet[entry.EtxHash] = types.EtxSetEntry{Height: entry.EtxHeight, ETX: entry.Etx}
	}
	return etxSet
}

// WriteEtxSet stores the EtxSet corresponding to a given block
func WriteEtxSet(db ethdb.KeyValueWriter, hash common.Hash, number uint64, etxSet types.EtxSet) {
	var entries []EtxSetEntry
	for etxHash, entry := range etxSet {
		entry := EtxSetEntry{EtxHash: etxHash, EtxHeight: entry.Height, Etx: entry.ETX}
		entries = append(entries, entry)
	}
	data, err := rlp.EncodeToBytes(entries)
	if err != nil {
		log.Fatal("Failed to RLP encode etx set", "err", err)
	}
	WriteEtxSetRLP(db, hash, number, data)
}

// DeleteEtxSet removes all EtxSet data associated with a block.
func DeleteEtxSet(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(etxSetKey(number, hash)); err != nil {
		log.Fatal("Failed to delete etx set", "err", err)
	}
}

// ReadPendingEtxsRLP retrieves the set of pending ETXs for the given block, in RLP encoding
func ReadPendingEtxsRLP(db ethdb.Reader, hash common.Hash) rlp.RawValue {
	// Try to look up the data in leveldb.
	data, _ := db.Get(pendingEtxsKey(hash))
	if len(data) > 0 {
		return data
	}
	return nil // Can't find the data anywhere.
}

// WritePendingEtxsRLP stores the pending ETXs corresponding to a given block, in RLP encoding.
func WritePendingEtxsRLP(db ethdb.KeyValueWriter, hash common.Hash, rlp rlp.RawValue) {
	if err := db.Put(pendingEtxsKey(hash), rlp); err != nil {
		log.Fatal("Failed to store pending etxs", "err", err)
	}
}

// ReadPendingEtxs retreives the pending ETXs corresponding to a given block
func ReadPendingEtxs(db ethdb.Reader, hash common.Hash) *types.PendingEtxs {
	data := ReadPendingEtxsRLP(db, hash)
	if len(data) == 0 {
		return nil
	}
	pendingEtxs := types.PendingEtxs{}
	if err := rlp.Decode(bytes.NewReader(data), &pendingEtxs); err != nil {
		log.Error("Invalid pending etxs RLP", "hash", hash, "err", err)
		return nil
	}
	return &pendingEtxs
}

// WritePendingEtxs stores the pending ETXs corresponding to a given block
func WritePendingEtxs(db ethdb.KeyValueWriter, pendingEtxs types.PendingEtxs) {
	data, err := rlp.EncodeToBytes(pendingEtxs)
	if err != nil {
		log.Fatal("Failed to RLP encode pending etxs", "err", err)
	}
	WritePendingEtxsRLP(db, pendingEtxs.Header.Hash(), data)
}

// DeletePendingEtxs removes all pending ETX data associated with a block.
func DeletePendingEtxs(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pendingEtxsKey(hash)); err != nil {
		log.Fatal("Failed to delete pending etxs", "err", err)
	}
}

// ReadPendingEtxsRollup retreives the pending ETXs rollup corresponding to a given block
func ReadPendingEtxsRollup(db ethdb.Reader, hash common.Hash) *types.PendingEtxsRollup {
	// Try to look up the data in leveldb.
	data, _ := db.Get(pendingEtxsRollupKey(hash))
	if len(data) == 0 {
		return nil
	}
	pendingEtxsRollup := types.PendingEtxsRollup{}
	if err := rlp.Decode(bytes.NewReader(data), &pendingEtxsRollup); err != nil {
		log.Error("Invalid pending etxs rollup rlp", "hash", hash, "err", err)
		return nil
	}
	return &pendingEtxsRollup
}

// WritePendingEtxsRollup stores the pending ETXs rollup corresponding to a given block
func WritePendingEtxsRollup(db ethdb.KeyValueWriter, pendingEtxsRollup types.PendingEtxsRollup) {
	data, err := rlp.EncodeToBytes(pendingEtxsRollup)
	if err != nil {
		log.Fatal("Failed to RLP encode pending etxs rollup", "err", err)
	}
	if err := db.Put(pendingEtxsRollupKey(pendingEtxsRollup.Header.Hash()), data); err != nil {
		log.Fatal("Failed to store pending etxs rollup", "err", err)
	}
}

// DeletePendingEtxsRollup removes all pending ETX rollup data associated with a block.
func DeletePendingEtxsRollup(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pendingEtxsRollupKey(hash)); err != nil {
		log.Fatal("Failed to delete pending etxs rollup", "err", err)
	}
}

// ReadManifest retreives the manifest corresponding to a given block
func ReadManifest(db ethdb.Reader, hash common.Hash) types.BlockManifest {
	// Try to look up the data in leveldb.
	data, _ := db.Get(manifestKey(hash))
	if len(data) == 0 {
		return nil
	}
	manifest := types.BlockManifest{}
	if err := rlp.Decode(bytes.NewReader(data), &manifest); err != nil {
		log.Error("Invalid manifest rlp", "hash", hash, "err", err)
		return nil
	}
	return manifest
}

// WriteManifest stores the manifest corresponding to a given block
func WriteManifest(db ethdb.KeyValueWriter, hash common.Hash, manifest types.BlockManifest) {
	data, err := rlp.EncodeToBytes(manifest)
	if err != nil {
		log.Fatal("Failed to RLP encode manifest", "err", err)
	}
	if err := db.Put(manifestKey(hash), data); err != nil {
		log.Fatal("Failed to store manifest", "err", err)
	}
}

// DeleteManifest removes manifest data associated with a block.
func DeleteManifest(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(manifestKey(hash)); err != nil {
		log.Fatal("Failed to delete manifest", "err", err)
	}
}

// ReadBloomRLP retrieves the bloom for the given block, in RLP encoding
func ReadBloomRLP(db ethdb.Reader, hash common.Hash) rlp.RawValue {
	// Try to look up the data in leveldb.
	data, _ := db.Get(bloomKey(hash))
	if len(data) > 0 {
		return data
	}
	return nil // Can't find the data anywhere.
}

// WriteBloomRLP stores the bloom corresponding to a given block, in RLP encoding.
func WriteBloomRLP(db ethdb.KeyValueWriter, hash common.Hash, rlp rlp.RawValue) {
	if err := db.Put(bloomKey(hash), rlp); err != nil {
		log.Fatal("Failed to store block bloom filter", "err", err)
	}
}

// ReadBloom retreives the bloom corresponding to a given block
func ReadBloom(db ethdb.Reader, hash common.Hash) *types.Bloom {
	data := ReadBloomRLP(db, hash)
	if len(data) == 0 {
		return nil
	}
	bloom := types.Bloom{}
	if err := rlp.Decode(bytes.NewReader(data), &bloom); err != nil {
		log.Error("Invalid bloom RLP", "hash", hash, "err", err)
		return nil
	}
	return &bloom
}

// WriteBloom stores the bloom corresponding to a given block
func WriteBloom(db ethdb.KeyValueWriter, hash common.Hash, bloom types.Bloom) {
	data, err := rlp.EncodeToBytes(bloom)
	if err != nil {
		log.Fatal("Failed to RLP encode pending etxs", "err", err)
	}
	WriteBloomRLP(db, hash, data)
}

// DeleteBloom removes all bloom data associated with a block.
func DeleteBloom(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(bloomKey(hash)); err != nil {
		log.Fatal("Failed to delete bloom", "err", err)
	}
}

// ReadBadHashesList retreives the bad hashes corresponding to the recent fork
func ReadBadHashesList(db ethdb.Reader) types.BlockManifest {
	// Try to look up the data in leveldb.
	data, _ := db.Get(badHashesListPrefix)
	if len(data) == 0 {
		return nil
	}
	badHashes := []common.Hash{}
	if err := rlp.Decode(bytes.NewReader(data), &badHashes); err != nil {
		log.Error("Invalid badHashesList rlp")
		return nil
	}
	return badHashes
}

// WriteBadHashesList stores the bad hashes corresponding to the recent fork
func WriteBadHashesList(db ethdb.KeyValueWriter, badHashes []common.Hash) {
	data, err := rlp.EncodeToBytes(badHashes)
	if err != nil {
		log.Fatal("Failed to RLP encode badHashesList", "err", err)
	}
	if err := db.Put(badHashesListPrefix, data); err != nil {
		log.Fatal("Failed to store badHashesList", "err", err)
	}
}

// DeleteBadHashesList removes badHashesList from the database
func DeleteBadHashesList(db ethdb.KeyValueWriter) {
	if err := db.Delete(badHashesListPrefix); err != nil {
		log.Fatal("Failed to delete badHashesList", "err", err)
	}
}

// WriteInboundEtxs stores the inbound etxs for a given dom block hashes
func WriteInboundEtxs(db ethdb.KeyValueWriter, hash common.Hash, inboundEtxs types.Transactions) {
	data, err := rlp.EncodeToBytes(inboundEtxs)
	if err != nil {
		log.Fatal("Failed to RLP encode inbound etxs", "err", err)
	}
	if err := db.Put(inboundEtxsKey(hash), data); err != nil {
		log.Fatal("Failed to store badHashesList", "err", err)
	}
}

// ReadInboundEtxs reads the inbound etxs from the database
func ReadInboundEtxs(db ethdb.Reader, hash common.Hash) types.Transactions {
	// Try to look up the data in leveldb.
	data, _ := db.Get(inboundEtxsKey(hash))
	if len(data) == 0 {
		return nil
	}
	inboundEtxs := types.Transactions{}
	if err := rlp.Decode(bytes.NewReader(data), &inboundEtxs); err != nil {
		log.Error("Invalid inbound etxs on read", "hash", hash, "err", err)
		return nil
	}
	return inboundEtxs
}
