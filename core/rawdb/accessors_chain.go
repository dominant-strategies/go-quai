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
	"google.golang.org/protobuf/proto"
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
		log.Global.WithField("err", err).Fatal("Failed to store number to hash mapping")
	}
}

// DeleteCanonicalHash removes the number to hash canonical mapping.
func DeleteCanonicalHash(db ethdb.KeyValueWriter, number uint64) {
	if err := db.Delete(headerHashKey(number)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete number to hash mapping")
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
		log.Global.WithField("err", err).Fatal("Failed to store hash to number mapping")
	}
}

// DeleteHeaderNumber removes hash->number mapping.
func DeleteHeaderNumber(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(headerNumberKey(hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete hash to number mapping")
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
		log.Global.WithField("err", err).Fatal("Failed to store last header's hash")
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
		log.Global.WithField("err", err).Fatal("Failed to store last block's hash")
	}
}

// ReadLastPivotNumber retrieves the number of the last pivot block. If the node
// full synced, the last pivot will always be nil.
func ReadLastPivotNumber(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(lastPivotKey)
	if len(data) == 0 {
		return nil
	}
	protoPivot := new(ProtoNumber)
	err := proto.Unmarshal(data, protoPivot)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal pivot block number")
	}
	return &protoPivot.Number
}

// WriteLastPivotNumber stores the number of the last pivot block.
func WriteLastPivotNumber(db ethdb.KeyValueWriter, pivot uint64) {
	protoPivot := new(ProtoNumber)
	protoPivot.Number = pivot
	enc, err := proto.Marshal(protoPivot)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to encode pivot block number")
	}
	if err := db.Put(lastPivotKey, enc); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store pivot block number")
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
		log.Global.WithField("err", err).Fatal("Failed to store fast sync trie progress")
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
		log.Global.WithField("err", err).Fatal("Failed to store the transaction index tail")
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
		log.Global.WithField("err", err).Fatal("Failed to store transaction lookup limit for fast sync")
	}
}

// ReadHeaderProto retrieves a block header in its raw proto database encoding.
func ReadHeaderProto(db ethdb.Reader, hash common.Hash, number uint64) []byte {
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
	data := ReadHeaderProto(db, hash, number)
	if len(data) == 0 {
		log.Global.Warn("proto header is nil")
		return nil
	}
	protoHeader := new(types.ProtoHeader)
	err := proto.Unmarshal(data, protoHeader)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal header")
	}
	header := new(types.Header)
	err = header.ProtoDecode(protoHeader)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid block header Proto")
		return nil
	}
	return header
}

// WriteHeader stores a block header into the database and also stores the hash-
// to-number mapping.
func WriteHeader(db ethdb.KeyValueWriter, header *types.Header, nodeCtx int) {
	var (
		hash   = header.Hash()
		number = header.NumberU64(nodeCtx)
	)
	// Write the hash -> number mapping
	WriteHeaderNumber(db, hash, number)

	// Write the encoded header
	protoHeader, err := header.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode header")
	}
	data, err := proto.Marshal(protoHeader)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal header")
	}
	key := headerKey(number, hash)
	if err := db.Put(key, data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store header")
	}
}

// DeleteHeader removes all block header data associated with a hash.
func DeleteHeader(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	deleteHeaderWithoutNumber(db, hash, number)
	if err := db.Delete(headerNumberKey(hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete hash to number mapping")
	}
}

// deleteHeaderWithoutNumber removes only the block header but does not remove
// the hash to number mapping.
func deleteHeaderWithoutNumber(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(headerKey(number, hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete header")
	}
}

// ReadBodyProto retrieves the block body (transactions and uncles) in protobuf encoding.
func ReadBodyProto(db ethdb.Reader, hash common.Hash, number uint64) []byte {
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

// ReadCanonicalBodyProto retrieves the block body (transactions and uncles) for the canonical
// block at number, in Proto encoding.
func ReadCanonicalBodyProto(db ethdb.Reader, number uint64) []byte {
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

// WriteBodyProto stores an Proto encoded block body into the database.
func WriteBodyProto(db ethdb.KeyValueWriter, hash common.Hash, number uint64, data []byte) {
	if err := db.Put(blockBodyKey(number, hash), data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store block body")
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
func ReadBody(db ethdb.Reader, hash common.Hash, number uint64, location common.Location) *types.Body {
	data := ReadBodyProto(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	protoBody := new(types.ProtoBody)
	err := proto.Unmarshal(data, protoBody)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal body")
	}
	body := new(types.Body)
	err = body.ProtoDecode(protoBody, location)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid block body Proto")
		return nil
	}
	return body
}

// WriteBody stores a block body into the database.
func WriteBody(db ethdb.KeyValueWriter, hash common.Hash, number uint64, body *types.Body) {
	protoBody, err := body.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode body")
	}
	data, err := proto.Marshal(protoBody)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal body")
	}
	WriteBodyProto(db, hash, number, data)
}

// DeleteBody removes all block body data associated with a hash.
func DeleteBody(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(blockBodyKey(number, hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete block body")
	}
}

// ReadPbCacheBody retrieves the block body corresponding to the hash.
func ReadPbCacheBody(db ethdb.Reader, hash common.Hash, location common.Location) *types.Body {
	data, err := db.Get(pbBodyKey(hash))
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Failed to read block body")
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	protoBody := new(types.ProtoBody)
	if err := proto.Unmarshal(data, protoBody); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal body")
	}
	body := new(types.Body)
	body.ProtoDecode(protoBody, location)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid pending block body Proto")
		return nil
	}
	return body
}

// WritePbCacheBody stores a block body into the database.
func WritePbCacheBody(db ethdb.KeyValueWriter, hash common.Hash, body *types.Body) {
	protoBody, err := body.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode body")
	}
	data, err := proto.Marshal(protoBody)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to Proto Marshal encode body")
	}
	if err := db.Put(pbBodyKey(hash), data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to write pbBodyKey")
	}
}

// DeletePbCacheBody removes all block body data associated with a hash.
func DeletePbCacheBody(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pbBodyKey(hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete pb cache body")
	}
}

// ReadPbBodyKeys retreive's the phBodyKeys of the worker
func ReadPbBodyKeys(db ethdb.Reader) common.Hashes {
	key := pbBodyHashKey()
	data, err := db.Get(key)
	if err != nil {
		log.Global.WithField("err", err).Error("Error in Reading pbBodyKeys")
		return nil
	}
	if len(data) == 0 {
		return common.Hashes{}
	}
	protoKeys := new(common.ProtoHashes)
	err = proto.Unmarshal(data, protoKeys)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal pbBodyKeys")
	}

	keys := &common.Hashes{}
	keys.ProtoDecode(protoKeys)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"err": err,
		}).Error("Invalid pbBodyKeys Proto")
		return nil
	}
	return *keys
}

// WritePbBodyKeys writes the workers pendingHeaderBody keys to the db
func WritePbBodyKeys(db ethdb.KeyValueWriter, hashes common.Hashes) {
	key := pbBodyHashKey()
	protoHashes := hashes.ProtoEncode()
	data, err := proto.Marshal(protoHashes)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to Proto marshal pbBodyKeys")
	}
	if err := db.Put(key, data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store pending block body keys")
	}
}

// DeleteAllPbBodyKeys delete the pendingHeaderBody keys to the db
func DeleteAllPbBodyKeys(db ethdb.KeyValueWriter) {
	key := pbBodyHashKey()

	if err := db.Delete(key); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete pending block body keys")
	}
}

// ReadHeadsHashes retreive's the heads hashes of the blockchain.
func ReadTermini(db ethdb.Reader, hash common.Hash) *types.Termini {
	key := terminiKey(hash)
	data, _ := db.Get(key)
	if len(data) == 0 {
		return nil
	}
	protoTermini := new(types.ProtoTermini)
	err := proto.Unmarshal(data, protoTermini)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal termini")
	}
	termini := new(types.Termini)
	err = termini.ProtoDecode(protoTermini)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid termini Proto")
		return nil
	}
	return termini
}

// WriteTermini writes the heads hashes of the blockchain.
func WriteTermini(db ethdb.KeyValueWriter, index common.Hash, hashes types.Termini) {
	key := terminiKey(index)
	protoTermini := hashes.ProtoEncode()
	data, err := proto.Marshal(protoTermini)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to Proto marshal termini")
	}
	if err := db.Put(key, data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store last block's termini")
	}
}

// DeleteTermini writes the heads hashes of the blockchain.
func DeleteTermini(db ethdb.KeyValueWriter, hash common.Hash) {
	key := terminiKey(hash)

	if err := db.Delete(key); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete termini ")
	}
}

// ReadPendingHeader retreive's the pending header stored in hash.
func ReadPendingHeader(db ethdb.Reader, hash common.Hash) *types.PendingHeader {
	key := pendingHeaderKey(hash)
	data, _ := db.Get(key)
	if len(data) == 0 {
		log.Global.WithField("key", key).Debug("Pending Header is nil")
		return nil
	}

	protoPendingHeader := new(types.ProtoPendingHeader)
	err := proto.Unmarshal(data, protoPendingHeader)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal pending header")
	}

	pendingHeader := new(types.PendingHeader)

	err = pendingHeader.ProtoDecode(protoPendingHeader)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid pendingHeader Proto")
		return nil
	}
	return pendingHeader
}

// WritePendingHeader writes the pending header of the terminus hash.
func WritePendingHeader(db ethdb.KeyValueWriter, hash common.Hash, pendingHeader types.PendingHeader) {
	key := pendingHeaderKey(hash)

	protoPendingHeader, err := pendingHeader.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode pending header")
	}
	data, err := proto.Marshal(protoPendingHeader)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal pending header")
	}
	if err := db.Put(key, data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store header")
	}
}

// DeletePendingHeader deletes the pending header stored for the header hash.
func DeletePendingHeader(db ethdb.KeyValueWriter, hash common.Hash) {
	key := pendingHeaderKey(hash)
	if err := db.Delete(key); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete slice pending header ")
	}
}

// ReadBestPhKey retreive's the bestPhKey of the blockchain
func ReadBestPhKey(db ethdb.Reader) common.Hash {
	data, _ := db.Get(phHeadKey)
	// get the ph cache keys.
	if len(data) == 0 {
		return common.Hash{}
	}
	protoBestPhKey := new(common.ProtoHash)
	if err := proto.Unmarshal(data, protoBestPhKey); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal best ph key")
	}
	bestPhKey := new(common.Hash)
	bestPhKey.ProtoDecode(protoBestPhKey)
	return *bestPhKey
}

// WriteBestPhKey writes the bestPhKey of the blockchain
func WriteBestPhKey(db ethdb.KeyValueWriter, bestPhKey common.Hash) {
	protoPhKey := bestPhKey.ProtoEncode()
	data, err := proto.Marshal(protoPhKey)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to Proto marshal best ph key")
	}
	if err := db.Put(phHeadKey, data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store last block's hash")
	}
}

// DeleteBestPhKey delete the bestPhKey of the blockchain
func DeleteBestPhKey(db ethdb.KeyValueWriter) {
	if err := db.Delete(phHeadKey); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete ph head")
	}
}

// ReadHeadsHashes retreive's the heads hashes of the blockchain.
func ReadHeadsHashes(db ethdb.Reader) common.Hashes {
	data, _ := db.Get(headsHashesKey)
	if len(data) == 0 {
		return []common.Hash{}
	}
	protoHeadsHashes := new(common.ProtoHashes)
	err := proto.Unmarshal(data, protoHeadsHashes)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal heads hashes")
	}
	headsHashes := new(common.Hashes)
	headsHashes.ProtoDecode(protoHeadsHashes)
	return *headsHashes
}

// WriteHeadsHashes writes the heads hashes of the blockchain.
func WriteHeadsHashes(db ethdb.KeyValueWriter, hashes common.Hashes) {
	protoHeadsHashes := hashes.ProtoEncode()
	data, err := proto.Marshal(protoHeadsHashes)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to Proto marshal heads hashes")
	}
	if err := db.Put(headsHashesKey, data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store last block's hash")
	}
}

// DeleteAllHeadsHashes writes the heads hashes of the blockchain.
func DeleteAllHeadsHashes(db ethdb.KeyValueWriter) {
	if err := db.Delete(headsHashesKey); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete block total difficulty")
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

// ReadReceiptsProto retrieves all the transaction receipts belonging to a block in Proto encoding.
func ReadReceiptsProto(db ethdb.Reader, hash common.Hash, number uint64) []byte {
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
func ReadRawReceipts(db ethdb.Reader, hash common.Hash, number uint64, location common.Location) types.Receipts {
	// Retrieve the flattened receipt slice
	data := ReadReceiptsProto(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	protoReceipt := new(types.ProtoReceiptsForStorage)
	err := proto.Unmarshal(data, protoReceipt)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal receipt")
		return nil
	}
	// Convert the receipts from their storage form to their internal representation
	storageReceipts := new(types.ReceiptsForStorage)
	err = storageReceipts.ProtoDecode(protoReceipt, location)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid receipt array Proto")
		return nil
	}
	var receipts types.Receipts
	for i, storageReceipt := range *storageReceipts {
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
	receipts := ReadRawReceipts(db, hash, number, config.Location)
	if receipts == nil {
		return nil
	}
	body := ReadBody(db, hash, number, config.Location)
	if body == nil {
		log.Global.WithFields(log.Fields{
			"hash":   hash,
			"number": number,
		}).Error("Missing body but have receipt")
		return nil
	}
	if err := receipts.DeriveFields(config, hash, number, body.Transactions); err != nil {
		log.Global.WithFields(log.Fields{
			"hash":   hash,
			"number": number,
			"err":    err,
		}).Error("Failed to derive block receipts fields")
		return nil
	}
	return receipts
}

// WriteReceipts stores all the transaction receipts belonging to a block.
func WriteReceipts(db ethdb.KeyValueWriter, hash common.Hash, number uint64, receipts types.Receipts) {
	// Convert the receipts into their storage form and serialize them
	storageReceipts := make(types.ReceiptsForStorage, len(receipts))
	for i, receipt := range receipts {
		storageReceipts[i] = (*types.ReceiptForStorage)(receipt)
	}

	protoReceipts, err := storageReceipts.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode receipt")
	}
	bytes, err := proto.Marshal(protoReceipts)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal receipt")
	}
	// Store the flattened receipt slice
	if err := db.Put(blockReceiptsKey(number, hash), bytes); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store block receipts")
	}
}

// DeleteReceipts removes all receipt data associated with a block hash.
func DeleteReceipts(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(blockReceiptsKey(number, hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete block receipts")
	}
}

// ReadBlock retrieves an entire block corresponding to the hash, assembling it
// back from the stored header and body. If either the header or body could not
// be retrieved nil is returned.
//
// Note, due to concurrent download of header and block body the header and thus
// canonical hash can be stored in the database but the body data not (yet).
func ReadBlock(db ethdb.Reader, hash common.Hash, number uint64, location common.Location) *types.Block {
	header := ReadHeader(db, hash, number)
	if header == nil {
		return nil
	}
	body := ReadBody(db, hash, number, location)
	if body == nil {
		return nil
	}
	return types.NewBlockWithHeader(header).WithBody(body.Transactions, body.Uncles, body.ExtTransactions, body.SubManifest)
}

// WriteBlock serializes a block into the database, header and body separately.
func WriteBlock(db ethdb.KeyValueWriter, block *types.Block, nodeCtx int) {
	WriteBody(db, block.Hash(), block.NumberU64(nodeCtx), block.Body())
	WriteHeader(db, block.Header(), nodeCtx)
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

// ProtoEncode returns the protobuf encoding of the bad block.
func (b badBlock) ProtoEncode() *ProtoBadBlock {
	protoHeader, err := b.Header.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode header")
	}
	protoBody, err := b.Body.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode body")
	}
	return &ProtoBadBlock{
		Header: protoHeader,
		Body:   protoBody,
	}
}

// ProtoDecode decodes the protobuf encoding of the bad block.
func (b *badBlock) ProtoDecode(pb *ProtoBadBlock, location common.Location) error {
	header := new(types.Header)
	if err := header.ProtoDecode(pb.Header); err != nil {
		return err
	}
	b.Header = header
	body := new(types.Body)
	if err := body.ProtoDecode(pb.Body, location); err != nil {
		return err
	}
	b.Body = body
	return nil
}

// badBlockList implements the sort interface to allow sorting a list of
// bad blocks by their number in the reverse order.
type badBlockList []*badBlock

func (s badBlockList) Len() int { return len(s) }
func (s badBlockList) Less(i, j int) bool {
	return s[i].Header.NumberU64(common.ZONE_CTX) < s[j].Header.NumberU64(common.ZONE_CTX)
}
func (s badBlockList) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s badBlockList) ProtoEncode() *ProtoBadBlocks {
	protoList := make([]*ProtoBadBlock, len(s))
	for i, bad := range s {
		protoList[i] = bad.ProtoEncode()
	}
	return &ProtoBadBlocks{BadBlocks: protoList}
}

func (s *badBlockList) ProtoDecode(pb *ProtoBadBlocks, location common.Location) error {
	list := make(badBlockList, len(pb.BadBlocks))
	for i, protoBlock := range pb.BadBlocks {
		block := new(badBlock)
		if err := block.ProtoDecode(protoBlock, location); err != nil {
			return err
		}
		list[i] = block
	}
	*s = list
	return nil
}

// ReadBadBlock retrieves the bad block with the corresponding block hash.
func ReadBadBlock(db ethdb.Reader, hash common.Hash, location common.Location) *types.Block {
	blob, err := db.Get(badBlockKey)
	if err != nil {
		return nil
	}
	protoBadBlocks := new(ProtoBadBlocks)
	err = proto.Unmarshal(blob, protoBadBlocks)
	if err != nil {
		return nil
	}

	badBlocks := new(badBlockList)
	err = badBlocks.ProtoDecode(protoBadBlocks, location)
	if err != nil {
		return nil
	}
	for _, bad := range *badBlocks {
		if bad.Header.Hash() == hash {
			return types.NewBlockWithHeader(bad.Header).WithBody(bad.Body.Transactions, bad.Body.Uncles, bad.Body.ExtTransactions, bad.Body.SubManifest)
		}
	}
	return nil
}

// ReadAllBadBlocks retrieves all the bad blocks in the database.
// All returned blocks are sorted in reverse order by number.
func ReadAllBadBlocks(db ethdb.Reader, location common.Location) []*types.Block {
	blob, err := db.Get(badBlockKey)
	if err != nil {
		return nil
	}

	protoBadBlocks := new(ProtoBadBlocks)
	err = proto.Unmarshal(blob, protoBadBlocks)
	if err != nil {
		return nil
	}
	badBlocks := new(badBlockList)

	err = badBlocks.ProtoDecode(protoBadBlocks, location)
	if err != nil {
		return nil
	}
	var blocks []*types.Block
	for _, bad := range *badBlocks {
		blocks = append(blocks, types.NewBlockWithHeader(bad.Header).WithBody(bad.Body.Transactions, bad.Body.Uncles, bad.Body.ExtTransactions, bad.Body.SubManifest))
	}
	return blocks
}

// WriteBadBlock serializes the bad block into the database. If the cumulated
// bad blocks exceeds the limitation, the oldest will be dropped.
func WriteBadBlock(db ethdb.KeyValueStore, block *types.Block, location common.Location) {
	blob, err := db.Get(badBlockKey)
	if err != nil {
		log.Global.WithField("err", err).Warn("Failed to load old bad blocks")
	}

	protoBadBlocks := new(ProtoBadBlocks)
	err = proto.Unmarshal(blob, protoBadBlocks)
	if err != nil {
		log.Global.WithField("err", err).Warn("Failed to proto Unmarshal bad blocks")
	}
	badBlocksList := badBlockList{}
	if len(blob) > 0 {
		err := badBlocksList.ProtoDecode(protoBadBlocks, location)
		if err != nil {
			log.Global.WithField("err", err).Fatal("Failed to decode old bad blocks")
		}
	}
	badBlocks := badBlocksList
	nodeCtx := location.Context()
	for _, b := range badBlocks {
		if b.Header.NumberU64(nodeCtx) == block.NumberU64(nodeCtx) && b.Header.Hash() == block.Hash() {
			log.Global.WithFields(log.Fields{
				"number": block.NumberU64(nodeCtx),
				"hash":   block.Hash(),
			}).Info("Skip duplicated bad block")
			return
		}
	}
	badBlocks = append(badBlocks, &badBlock{
		Header: block.Header(),
		Body:   block.Body(),
	})
	sort.Sort(sort.Reverse(badBlocks))
	if len(badBlocks) > badBlockToKeep {
		blocks := badBlocks
		badBlocks = blocks[:badBlockToKeep]
	}
	protoBadBlocks = badBlocks.ProtoEncode()
	data, err := proto.Marshal(protoBadBlocks)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to encode bad blocks")
	}
	if err := db.Put(badBlockKey, data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to write bad blocks")
	}
}

// DeleteBadBlocks deletes all the bad blocks from the database
func DeleteBadBlocks(db ethdb.KeyValueWriter) {
	if err := db.Delete(badBlockKey); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete bad blocks")
	}
}

// FindCommonAncestor returns the last common ancestor of two block headers
func FindCommonAncestor(db ethdb.Reader, a, b *types.Header, nodeCtx int) *types.Header {
	for bn := b.NumberU64(nodeCtx); a.NumberU64(nodeCtx) > bn; {
		a = ReadHeader(db, a.ParentHash(nodeCtx), a.NumberU64(nodeCtx)-1)
		if a == nil {
			return nil
		}
	}
	for an := a.NumberU64(nodeCtx); an < b.NumberU64(nodeCtx); {
		b = ReadHeader(db, b.ParentHash(nodeCtx), b.NumberU64(nodeCtx)-1)
		if b == nil {
			return nil
		}
	}
	for a.Hash() != b.Hash() {
		a = ReadHeader(db, a.ParentHash(nodeCtx), a.NumberU64(nodeCtx)-1)
		if a == nil {
			return nil
		}
		b = ReadHeader(db, b.ParentHash(nodeCtx), b.NumberU64(nodeCtx)-1)
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
func ReadHeadBlock(db ethdb.Reader, location common.Location) *types.Block {
	headBlockHash := ReadHeadBlockHash(db)
	if headBlockHash == (common.Hash{}) {
		return nil
	}
	headBlockNumber := ReadHeaderNumber(db, headBlockHash)
	if headBlockNumber == nil {
		return nil
	}
	return ReadBlock(db, headBlockHash, *headBlockNumber, location)
}

// ReadEtxSetProto retrieves the EtxSet corresponding to a given block, in Proto encoding.
func ReadEtxSetProto(db ethdb.Reader, hash common.Hash, number uint64) ([]byte, error) {
	// First try to look up the data in ancient database. Extra hash
	// comparison is necessary since ancient database only maintains
	// the canonical data.
	data, _ := db.Ancient(freezerEtxSetsTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data, nil
		}
	}
	var err error
	// Then try to look up the data in leveldb.
	data, err = db.Get(etxSetKey(number, hash))
	if err == nil {
		return data, nil
	}
	// In the background freezer is moving data from leveldb to flatten files.
	// So during the first check for ancient db, the data is not yet in there,
	// but when we reach into leveldb, the data was already moved. That would
	// result in a not found error.
	data, _ = db.Ancient(freezerEtxSetsTable, number)
	if len(data) > 0 {
		h, _ := db.Ancient(freezerHashTable, number)
		if common.BytesToHash(h) == hash {
			return data, nil
		}
	}
	return nil, errors.New("etx set not found") // Can't find the data anywhere.
}

// WriteEtxSetProto stores the EtxSet corresponding to a given block, in Proto encoding.
func WriteEtxSetProto(db ethdb.KeyValueWriter, hash common.Hash, number uint64, data []byte) {
	if err := db.Put(etxSetKey(number, hash), data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store etx set")
	}
}

// ReadEtxSet retreives the EtxSet corresponding to a given block
func ReadEtxSet(db ethdb.Reader, hash common.Hash, number uint64, location common.Location) types.EtxSet {
	data, err := ReadEtxSetProto(db, hash, number)
	if err != nil {
		return nil
	}
	protoEtxSet := new(types.ProtoEtxSet)
	if err := proto.Unmarshal(data, protoEtxSet); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal etx set")
	}
	etxSet := make(types.EtxSet)
	err = etxSet.ProtoDecode(protoEtxSet, location)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid etx set Proto")
		return nil
	}
	return etxSet
}

// WriteEtxSet stores the EtxSet corresponding to a given block
func WriteEtxSet(db ethdb.KeyValueWriter, hash common.Hash, number uint64, etxSet types.EtxSet) {
	protoEtxSet := etxSet.ProtoEncode()

	data, err := proto.Marshal(protoEtxSet)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal etx set")
	}
	WriteEtxSetProto(db, hash, number, data)
}

// DeleteEtxSet removes all EtxSet data associated with a block.
func DeleteEtxSet(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(etxSetKey(number, hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete etx set")
	}
}

// ReadPendingEtxsProto retrieves the set of pending ETXs for the given block, in Proto encoding
func ReadPendingEtxsProto(db ethdb.Reader, hash common.Hash) []byte {
	// Try to look up the data in leveldb.
	data, _ := db.Get(pendingEtxsKey(hash))
	if len(data) > 0 {
		return data
	}
	return nil // Can't find the data anywhere.
}

// WritePendingEtxsProto stores the pending ETXs corresponding to a given block, in Proto encoding.
func WritePendingEtxsProto(db ethdb.KeyValueWriter, hash common.Hash, data []byte) {
	if err := db.Put(pendingEtxsKey(hash), data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store pending etxs")
	}
}

// ReadPendingEtxs retreives the pending ETXs corresponding to a given block
func ReadPendingEtxs(db ethdb.Reader, hash common.Hash) *types.PendingEtxs {
	data := ReadPendingEtxsProto(db, hash)
	if len(data) == 0 {
		return nil
	}
	protoPendingEtxs := new(types.ProtoPendingEtxs)
	if err := proto.Unmarshal(data, protoPendingEtxs); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal pending etxs")
	}
	pendingEtxs := new(types.PendingEtxs)
	if err := pendingEtxs.ProtoDecode(protoPendingEtxs); err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid pending etxs Proto")
		return nil
	}
	return pendingEtxs
}

// WritePendingEtxs stores the pending ETXs corresponding to a given block
func WritePendingEtxs(db ethdb.KeyValueWriter, pendingEtxs types.PendingEtxs) {
	protoPendingEtxs, err := pendingEtxs.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode pending etxs")
	}
	data, err := proto.Marshal(protoPendingEtxs)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal pending etxs")
	}
	WritePendingEtxsProto(db, pendingEtxs.Header.Hash(), data)
}

// DeletePendingEtxs removes all pending ETX data associated with a block.
func DeletePendingEtxs(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pendingEtxsKey(hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete pending etxs")
	}
}

// ReadPendingEtxsRollup retreives the pending ETXs rollup corresponding to a given block
func ReadPendingEtxsRollup(db ethdb.Reader, hash common.Hash, location common.Location) *types.PendingEtxsRollup {
	// Try to look up the data in leveldb.
	data, _ := db.Get(pendingEtxsRollupKey(hash))
	if len(data) == 0 {
		return nil
	}
	protoPendingEtxsRollup := new(types.ProtoPendingEtxsRollup)
	err := proto.Unmarshal(data, protoPendingEtxsRollup)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal pending etxs rollup")
	}
	pendingEtxsRollup := new(types.PendingEtxsRollup)
	err = pendingEtxsRollup.ProtoDecode(protoPendingEtxsRollup, location)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid pending etxs rollup Proto")
		return nil
	}
	return pendingEtxsRollup
}

// WritePendingEtxsRollup stores the pending ETXs rollup corresponding to a given block
func WritePendingEtxsRollup(db ethdb.KeyValueWriter, pendingEtxsRollup types.PendingEtxsRollup) {
	protoPendingEtxsRollup, err := pendingEtxsRollup.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode pending etxs rollup")
	}
	data, err := proto.Marshal(protoPendingEtxsRollup)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal pending etxs rollup")
	}
	if err := db.Put(pendingEtxsRollupKey(pendingEtxsRollup.Header.Hash()), data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store pending etxs rollup")
	}
}

// DeletePendingEtxsRollup removes all pending ETX rollup data associated with a block.
func DeletePendingEtxsRollup(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pendingEtxsRollupKey(hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete pending etxs rollup")
	}
}

// ReadManifest retreives the manifest corresponding to a given block
func ReadManifest(db ethdb.Reader, hash common.Hash) types.BlockManifest {
	// Try to look up the data in leveldb.
	data, _ := db.Get(manifestKey(hash))
	if len(data) == 0 {
		return nil
	}
	protoManifest := new(types.ProtoManifest)
	err := proto.Unmarshal(data, protoManifest)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal manifest")
	}
	manifest := new(types.BlockManifest)
	err = manifest.ProtoDecode(protoManifest)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid manifest Proto")
		return nil
	}
	return *manifest
}

// WriteManifest stores the manifest corresponding to a given block
func WriteManifest(db ethdb.KeyValueWriter, hash common.Hash, manifest types.BlockManifest) {
	protoManifest, err := manifest.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode manifest")
	}
	data, err := proto.Marshal(protoManifest)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal manifest")
	}
	if err := db.Put(manifestKey(hash), data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store manifest")
	}
}

// DeleteManifest removes manifest data associated with a block.
func DeleteManifest(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(manifestKey(hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete manifest")
	}
}

// ReadBloomProto retrieves the bloom for the given block, in bytes
func ReadBloomProto(db ethdb.Reader, hash common.Hash) []byte {
	// Try to look up the data in leveldb.
	data, _ := db.Get(bloomKey(hash))
	if len(data) > 0 {
		return data
	}
	return nil // Can't find the data anywhere.
}

// WriteBloomProto stores the bloom corresponding to a given block, in proto bug encoding
func WriteBloomProto(db ethdb.KeyValueWriter, hash common.Hash, data []byte) {
	if err := db.Put(bloomKey(hash), data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store block bloom filter")
	}
}

// ReadBloom retreives the bloom corresponding to a given block
func ReadBloom(db ethdb.Reader, hash common.Hash) *types.Bloom {
	data := ReadBloomProto(db, hash)
	if len(data) == 0 {
		return nil
	}
	bloom := types.Bloom{}
	bloom.ProtoDecode(data)
	return &bloom
}

// WriteBloom stores the bloom corresponding to a given block
func WriteBloom(db ethdb.KeyValueWriter, hash common.Hash, bloom types.Bloom) {
	data, err := bloom.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to Proto encode pending etxs")
	}
	WriteBloomProto(db, hash, data)
}

// DeleteBloom removes all bloom data associated with a block.
func DeleteBloom(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(bloomKey(hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete bloom")
	}
}

// ReadBadHashesList retreives the bad hashes corresponding to the recent fork
func ReadBadHashesList(db ethdb.Reader) common.Hashes {
	// Try to look up the data in leveldb.
	data, _ := db.Get(badHashesListPrefix)
	if len(data) == 0 {
		return nil
	}
	protoBadHashes := new(common.ProtoHashes)
	err := proto.Unmarshal(data, protoBadHashes)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal badHashesList")
	}
	badHashes := &common.Hashes{}
	badHashes.ProtoDecode(protoBadHashes)
	return *badHashes
}

// WriteBadHashesList stores the bad hashes corresponding to the recent fork
func WriteBadHashesList(db ethdb.KeyValueWriter, badHashes common.Hashes) {
	protoBadHashes := badHashes.ProtoEncode()
	data, err := proto.Marshal(protoBadHashes)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal badHashesList")
	}
	if err := db.Put(badHashesListPrefix, data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store badHashesList")
	}
}

// DeleteBadHashesList removes badHashesList from the database
func DeleteBadHashesList(db ethdb.KeyValueWriter) {
	if err := db.Delete(badHashesListPrefix); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete badHashesList")
	}
}

// WriteInboundEtxs stores the inbound etxs for a given dom block hashes
func WriteInboundEtxs(db ethdb.KeyValueWriter, hash common.Hash, inboundEtxs types.Transactions) {
	protoInboundEtxs, err := inboundEtxs.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode inbound etxs")
	}
	data, err := proto.Marshal(protoInboundEtxs)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Marshal inbound etxs")
	}
	if err := db.Put(inboundEtxsKey(hash), data); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to store badHashesList")
	}
}

// ReadInboundEtxs reads the inbound etxs from the database
func ReadInboundEtxs(db ethdb.Reader, hash common.Hash, location common.Location) types.Transactions {
	// Try to look up the data in leveldb.
	data, err := db.Get(inboundEtxsKey(hash))
	if err != nil {
		return nil
	}
	protoInboundEtxs := new(types.ProtoTransactions)
	err = proto.Unmarshal(data, protoInboundEtxs)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal inbound etxs")
	}
	inboundEtxs := types.Transactions{}
	err = inboundEtxs.ProtoDecode(protoInboundEtxs, location)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid inbound etxs Proto")
		return nil
	}
	return inboundEtxs
}

// DeleteInboundEtxs deletes the inbound etxs from the database
func DeleteInboundEtxs(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(inboundEtxsKey(hash)); err != nil {
		log.Global.WithField("err", err).Fatal("Failed to delete inbound etxs")
	}
}

func WriteUtxo(db ethdb.KeyValueWriter, hash common.Hash, utxo *types.UtxoEntry) {
	data, err := rlp.EncodeToBytes(utxo)
	if err != nil {
		log.Global.Fatal("Failed to RLP encode inbound etxs", "err", err)
	}
	if err := db.Put(utxoKey(hash), data); err != nil {
		log.Global.Fatal("Failed to store badHashesList", "err", err)
	}
}

func ReadUtxo(db ethdb.Reader, hash common.Hash) *types.UtxoEntry {
	// Try to look up the data in leveldb.
	data, _ := db.Get(utxoKey(hash))
	if len(data) == 0 {
		return nil
	}
	utxo := new(types.UtxoEntry)
	if err := rlp.Decode(bytes.NewReader(data), utxo); err != nil {
		log.Global.Error("Invalid utxo RLP", "utxo", utxo, "err", err)
		return nil
	}
	return utxo
}

// DeleteUtxo deletes utxos from the database
func DeleteUtxo(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(utxoKey(hash)); err != nil {
		log.Global.Fatal("Failed to delete utxo", "err", err)
	}
}

func WriteSpentUTXOs(db ethdb.KeyValueWriter, hash common.Hash, spentUTXOs *[]types.SpentTxOut) {
	data, err := rlp.EncodeToBytes(spentUTXOs)
	if err != nil {
		log.Global.Fatal("Failed to RLP encode spent utxos", "err", err)
	}
	if err := db.Put(spentUTXOsKey(hash), data); err != nil {
		log.Global.Fatal("Failed to store spent utxos", "err", err)
	}
}

func ReadSpentUTXOs(db ethdb.Reader, hash common.Hash) []types.SpentTxOut {
	// Try to look up the data in leveldb.
	data, _ := db.Get(spentUTXOsKey(hash))
	if len(data) == 0 {
		return nil
	}
	spentUTXOs := []types.SpentTxOut{}
	if err := rlp.Decode(bytes.NewReader(data), &spentUTXOs); err != nil {
		log.Global.Error("Invalid spent utxos RLP", "err", err)
		return nil
	}
	return spentUTXOs
}
