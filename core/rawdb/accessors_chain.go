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
	"encoding/binary"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
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
		db.Logger().WithField("err", err).Fatal("Failed to store number to hash mapping")
	}
}

// DeleteCanonicalHash removes the number to hash canonical mapping.
func DeleteCanonicalHash(db ethdb.KeyValueWriter, number uint64) {
	if err := db.Delete(headerHashKey(number)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete number to hash mapping")
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
		db.Logger().WithField("err", err).Fatal("Failed to store hash to number mapping")
	}
}

// DeleteHeaderNumber removes hash->number mapping.
func DeleteHeaderNumber(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(headerNumberKey(hash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete hash to number mapping")
	}
}

func ReadProcessedState(db ethdb.KeyValueReader, hash common.Hash) bool {
	data, _ := db.Get(processedStateKey(hash))
	if len(data) == 0 {
		return false
	}
	return data[0] == 1
}

func WriteProcessedState(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Put(processedStateKey(hash), []byte{1}); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store processed state for block " + hash.String())
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
		db.Logger().WithField("err", err).Fatal("Failed to store last header's hash")
	}
}

// ReadHeadBlockHash retrieves the hash of the current canonical head block.
func ReadHeadBlockHash(db ethdb.KeyValueReader) common.Hash {
	data, _ := db.Get(headWorkObjectKey)
	if len(data) == 0 {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// WriteHeadBlockHash stores the head block's hash.
func WriteHeadBlockHash(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Put(headWorkObjectKey, hash.Bytes()); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store last block's hash")
	}
}

// HasHeader verifies the existence of a block header corresponding to the hash.
func HasHeader(db ethdb.Reader, hash common.Hash, number uint64) bool {
	if has, err := db.Ancient(freezerHashTable, number); err == nil && common.BytesToHash(has) == hash {
		return true
	}
	if has, err := db.Has(blockWorkObjectHeaderKey(hash)); !has || err != nil {
		return false
	}
	return true
}

// ReadHeader retrieves the block header corresponding to the hash.
func ReadHeader(db ethdb.Reader, hash common.Hash) *types.WorkObject {
	wo := ReadWorkObjectHeaderOnly(db, hash, types.BlockObject)
	if wo == nil || wo.Body() == nil || wo.Header() == nil {
		// Try backup function
		return ReadWorkObject(db, hash, types.BlockObject)
	}
	return wo
}

// DeleteHeader removes all block header data associated with a hash.
func DeleteHeader(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	DeleteWorkObjectHeader(db, hash, types.BlockObject)
	DeleteHeaderNumber(db, hash)
}

// HasBody verifies the existence of a block body corresponding to the hash.
func HasBody(db ethdb.Reader, hash common.Hash, number uint64) bool {
	if has, err := db.Ancient(freezerHashTable, number); err == nil && common.BytesToHash(has) == hash {
		return true
	}
	if has, err := db.Has(workObjectBodyKey(hash)); !has || err != nil {
		return false
	}
	return true
}

// ReadPbCacheBody retrieves the block body corresponding to the hash.
func ReadPbCacheBody(db ethdb.Reader, hash common.Hash) *types.WorkObject {
	data, err := db.Get(pbBodyKey(hash))
	if err != nil {
		db.Logger().WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Failed to read block body")
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	protoWorkObject := new(types.ProtoWorkObject)
	if err := proto.Unmarshal(data, protoWorkObject); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal body")
	}
	body := new(types.WorkObject)
	err = body.ProtoDecode(protoWorkObject, db.Location(), types.PhObject)
	if err != nil {
		db.Logger().WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid pending block body Proto")
		return nil
	}
	return body
}

// WritePbCacheBody stores a block body into the database.
func WritePbCacheBody(db ethdb.KeyValueWriter, hash common.Hash, body *types.WorkObject) {
	protoBody, err := body.ProtoEncode(types.PhObject)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto encode body")
	}
	data, err := proto.Marshal(protoBody)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to Proto Marshal encode body")
	}
	if err := db.Put(pbBodyKey(hash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to write pbBodyKey")
	}
}

// DeletePbCacheBody removes all block body data associated with a hash.
func DeletePbCacheBody(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pbBodyKey(hash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete pb cache body")
	}
}

// ReadPbBodyKeys retreive's the phBodyKeys of the worker
func ReadPbBodyKeys(db ethdb.Reader) common.Hashes {
	key := pbBodyHashKey()
	data, err := db.Get(key)
	if err != nil {
		db.Logger().WithField("err", err).Error("Error in Reading pbBodyKeys")
		return nil
	}
	if len(data) == 0 {
		return common.Hashes{}
	}
	protoKeys := new(common.ProtoHashes)
	err = proto.Unmarshal(data, protoKeys)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal pbBodyKeys")
	}

	keys := &common.Hashes{}
	keys.ProtoDecode(protoKeys)
	if err != nil {
		db.Logger().WithFields(log.Fields{
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
		db.Logger().WithField("err", err).Fatal("Failed to Proto marshal pbBodyKeys")
	}
	if err := db.Put(key, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store pending block body keys")
	}
}

// DeleteAllPbBodyKeys delete the pendingHeaderBody keys to the db
func DeleteAllPbBodyKeys(db ethdb.KeyValueWriter) {
	key := pbBodyHashKey()

	if err := db.Delete(key); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete pending block body keys")
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
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal termini")
	}
	termini := new(types.Termini)
	err = termini.ProtoDecode(protoTermini)
	if err != nil {
		db.Logger().WithFields(log.Fields{
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
		db.Logger().WithField("err", err).Fatal("Failed to Proto marshal termini")
	}
	if err := db.Put(key, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store last block's termini")
	}
}

// DeleteTermini writes the heads hashes of the blockchain.
func DeleteTermini(db ethdb.KeyValueWriter, hash common.Hash) {
	key := terminiKey(hash)

	if err := db.Delete(key); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete termini ")
	}
}

// ReadWorkObjectHeader retreive's the work object header stored in hash.
func ReadWorkObjectHeader(db ethdb.Reader, hash common.Hash, woType types.WorkObjectView) *types.WorkObjectHeader {
	var key []byte
	switch woType {
	case types.BlockObject:
		key = blockWorkObjectHeaderKey(hash)
	case types.TxObject:
		key = txWorkObjectHeaderKey(hash)
	case types.PhObject:
		key = phWorkObjectHeaderKey(hash)
	}
	data, _ := db.Get(key)
	if len(data) == 0 {
		return nil
	}
	protoWorkObjectHeader := new(types.ProtoWorkObjectHeader)
	err := proto.Unmarshal(data, protoWorkObjectHeader)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal work object header")
	}
	workObjectHeader := new(types.WorkObjectHeader)
	err = workObjectHeader.ProtoDecode(protoWorkObjectHeader, db.Location())
	if err != nil {
		db.Logger().WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid work object header Proto")
		return nil
	}
	return workObjectHeader
}

// WriteWorkObjectHeader writes the work object header of the terminus hash.
func WriteWorkObjectHeader(db ethdb.KeyValueWriter, hash common.Hash, workObject *types.WorkObject, woType types.WorkObjectView, nodeCtx int) {
	var key []byte
	switch woType {
	case types.BlockObject:
		key = blockWorkObjectHeaderKey(hash)
	case types.TxObject:
		key = txWorkObjectHeaderKey(hash)
	case types.PhObject:
		key = phWorkObjectHeaderKey(hash)
	}
	protoWorkObjectHeader, err := workObject.WorkObjectHeader().ProtoEncode()
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto encode work object header")
	}
	data, err := proto.Marshal(protoWorkObjectHeader)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal work object header")
	}
	if err := db.Put(key, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store work object header")
	}
}

// DeleteWorkObjectHeader deletes the work object header stored for the header hash.
func DeleteWorkObjectHeader(db ethdb.KeyValueWriter, hash common.Hash, woType types.WorkObjectView) {
	var key []byte
	switch woType {
	case types.BlockObject:
		key = blockWorkObjectHeaderKey(hash)
	case types.TxObject:
		key = txWorkObjectHeaderKey(hash)
	case types.PhObject:
		key = phWorkObjectHeaderKey(hash)
	}
	if err := db.Delete(key); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete work object header ")
	}
}

// ReadWorkObject retreive's the work object stored in hash.
func ReadWorkObject(db ethdb.Reader, hash common.Hash, woType types.WorkObjectView) *types.WorkObject {
	workObjectHeader := ReadWorkObjectHeader(db, hash, woType)
	if workObjectHeader == nil {
		return nil
	}
	workObjectBody := ReadWorkObjectBody(db, hash, types.BlockObject)
	if workObjectBody == nil {
		return nil
	}
	return types.NewWorkObject(workObjectHeader, workObjectBody, nil) //TODO: mmtx transaction
}

// ReadWorkObjectWithWorkShares retreive's the work object stored in hash.
func ReadWorkObjectWithWorkShares(db ethdb.Reader, hash common.Hash) *types.WorkObject {
	workObjectHeader := ReadWorkObjectHeader(db, hash, types.BlockObject)
	if workObjectHeader == nil {
		return nil
	}
	workObjectBody := ReadWorkObjectBody(db, hash, types.WorkShareObject)
	if workObjectBody == nil {
		return nil
	}
	return types.NewWorkObject(workObjectHeader, workObjectBody, nil) //TODO: mmtx transaction
}

func ReadWorkObjectHeaderOnly(db ethdb.Reader, hash common.Hash, woType types.WorkObjectView) *types.WorkObject {
	workObjectHeader := ReadWorkObjectHeader(db, hash, woType)
	if workObjectHeader == nil {
		return nil
	}
	workObjectBodyHeaderOnly := ReadWorkObjectBodyHeaderOnly(db, hash)
	if workObjectBodyHeaderOnly == nil {
		return nil
	}
	return types.NewWorkObject(workObjectHeader, workObjectBodyHeaderOnly, nil)
}

// WriteWorkObject writes the work object of the terminus hash.
func WriteWorkObject(db ethdb.KeyValueWriter, hash common.Hash, workObject *types.WorkObject, woType types.WorkObjectView, nodeCtx int) {
	WriteWorkObjectBody(db, hash, workObject, woType, nodeCtx)
	WriteWorkObjectHeader(db, hash, workObject, woType, nodeCtx)
}

// DeleteWorkObject deletes the work object stored for the header hash.
func DeleteWorkObject(db ethdb.KeyValueWriter, hash common.Hash, number uint64, woType types.WorkObjectView) {
	DeleteWorkObjectBody(db, hash)
	DeleteWorkObjectHeader(db, hash, woType) //TODO: mmtx transaction
	DeleteHeader(db, hash, number)
	DeleteReceipts(db, hash, number)
}

// DeleteWorkObjectWithoutNumber removes all block data associated with a hash, except
// the hash to number mapping.
func DeleteBlockWithoutNumber(db ethdb.KeyValueWriter, hash common.Hash, number uint64, woType types.WorkObjectView) {
	DeleteWorkObjectBody(db, hash)
	DeleteWorkObjectHeader(db, hash, woType) //TODO: mmtx transaction
	DeleteReceipts(db, hash, number)
}

// ReadWorkObjectBody retreive's the work object body stored in hash.
func ReadWorkObjectBody(db ethdb.Reader, hash common.Hash, woType types.WorkObjectView) *types.WorkObjectBody {
	key := workObjectBodyKey(hash)
	data, _ := db.Get(key)
	if len(data) == 0 {
		return nil
	}
	protoWorkObjectBody := new(types.ProtoWorkObjectBody)
	err := proto.Unmarshal(data, protoWorkObjectBody)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal work object body")
		return nil
	}
	workObjectBody := new(types.WorkObjectBody)
	err = workObjectBody.ProtoDecode(protoWorkObjectBody, db.Location(), woType)
	if err != nil {
		db.Logger().WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid work object body Proto")
		return nil
	}
	return workObjectBody
}

func ReadWorkObjectBodyHeaderOnly(db ethdb.Reader, hash common.Hash) *types.WorkObjectBody {
	key := workObjectBodyKey(hash)
	data, _ := db.Get(key)
	if len(data) == 0 {
		return nil
	}
	protoWorkObjectBody := new(types.ProtoWorkObjectBody)
	err := proto.Unmarshal(data, protoWorkObjectBody)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal work object body")
		return nil
	}
	workObjectBody := new(types.WorkObjectBody)
	err = workObjectBody.ProtoDecodeHeader(protoWorkObjectBody, db.Location())
	if workObjectBody.Header() == nil || err != nil {
		log.Global.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("workObjectBody Header is nil")
		return nil
	}
	return workObjectBody
}

// WriteWorkObjectBody writes the work object body of the terminus hash.
func WriteWorkObjectBody(db ethdb.KeyValueWriter, hash common.Hash, workObject *types.WorkObject, woType types.WorkObjectView, nodeCtx int) {

	key := workObjectBodyKey(hash)
	WriteHeaderNumber(db, hash, workObject.NumberU64(nodeCtx))

	protoWorkObjectBody, err := workObject.Body().ProtoEncode(woType)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto encode work object body")
	}
	data, err := proto.Marshal(protoWorkObjectBody)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal work object body")
	}
	if err := db.Put(key, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store work object body")
	}
}

// DeleteWorkObjectBody deletes the work object body stored for the header hash.
func DeleteWorkObjectBody(db ethdb.KeyValueWriter, hash common.Hash) {
	key := workObjectBodyKey(hash)
	if err := db.Delete(key); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete work object body ")
	}
}

// ReadPendingHeader retreive's the pending header stored in hash.
func ReadPendingHeader(db ethdb.Reader, hash common.Hash) *types.PendingHeader {
	key := pendingHeaderKey(hash)
	data, _ := db.Get(key)
	if len(data) == 0 {
		db.Logger().WithField("hash", hash).Debug("Pending Header is nil")
		return nil
	}

	protoPendingHeader := new(types.ProtoPendingHeader)
	err := proto.Unmarshal(data, protoPendingHeader)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal pending header")
	}

	pendingHeader := new(types.PendingHeader)

	err = pendingHeader.ProtoDecode(protoPendingHeader, db.Location())
	if err != nil {
		db.Logger().WithFields(log.Fields{
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
		db.Logger().WithField("err", err).Fatal("Failed to proto encode pending header")
	}
	data, err := proto.Marshal(protoPendingHeader)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal pending header")
	}
	if err := db.Put(key, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store header")
	}
}

// DeletePendingHeader deletes the pending header stored for the header hash.
func DeletePendingHeader(db ethdb.KeyValueWriter, hash common.Hash) {
	key := pendingHeaderKey(hash)
	if err := db.Delete(key); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete slice pending header ")
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
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal best ph key")
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
		db.Logger().WithField("err", err).Fatal("Failed to Proto marshal best ph key")
	}
	if err := db.Put(phHeadKey, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store last block's hash")
	}
}

// DeleteBestPhKey delete the bestPhKey of the blockchain
func DeleteBestPhKey(db ethdb.KeyValueWriter) {
	if err := db.Delete(phHeadKey); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete ph head")
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
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal heads hashes")
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
		db.Logger().WithField("err", err).Fatal("Failed to Proto marshal heads hashes")
	}
	if err := db.Put(headsHashesKey, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store last block's hash")
	}
}

// DeleteAllHeadsHashes writes the heads hashes of the blockchain.
func DeleteAllHeadsHashes(db ethdb.KeyValueWriter) {
	if err := db.Delete(headsHashesKey); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete block total difficulty")
	}
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
func ReadRawReceipts(db ethdb.Reader, hash common.Hash, number uint64) types.Receipts {
	// Retrieve the flattened receipt slice
	data := ReadReceiptsProto(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	protoReceipt := new(types.ProtoReceiptsForStorage)
	err := proto.Unmarshal(data, protoReceipt)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal receipt")
		return nil
	}
	// Convert the receipts from their storage form to their internal representation
	storageReceipts := new(types.ReceiptsForStorage)
	err = storageReceipts.ProtoDecode(protoReceipt, db.Location())
	if err != nil {
		db.Logger().WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid receipt array Proto")
		return nil
	}
	receipts := make(types.Receipts, len(*storageReceipts))
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
	receipts := ReadRawReceipts(db, hash, number)
	if receipts == nil {
		return nil
	}
	body := ReadWorkObject(db, hash, types.BlockObject)
	if body == nil {
		db.Logger().WithFields(log.Fields{
			"hash":   hash,
			"number": number,
		}).Error("Missing body but have receipt")
		return nil
	}
	if err := receipts.DeriveFields(config, hash, number, body.TransactionsWithReceipts()); err != nil {
		db.Logger().WithFields(log.Fields{
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
	// Store the flattened receipt slice
	if err := db.Put(blockReceiptsKey(number, hash), receipts.Bytes(db.Logger())); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store block receipts")
	}
}

// DeleteReceipts removes all receipt data associated with a block hash.
func DeleteReceipts(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(blockReceiptsKey(number, hash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete block receipts")
	}
}

const badWorkObjectToKeep = 10

type badWorkObject struct {
	woHeader *types.WorkObjectHeader
	woBody   *types.WorkObjectBody
	tx       types.Transaction
}

// ProtoEncode returns the protobuf encoding of the bad workObject.
func (b badWorkObject) ProtoEncode() *ProtoBadWorkObject {
	protoWorkObjectHeader, err := b.woHeader.ProtoEncode()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode header")
	}
	protoWorkObjectBody, err := b.woBody.ProtoEncode(types.BlockObject)
	if err != nil {
		log.Global.WithField("err", err).Fatal("Failed to proto encode body")
	}
	return &ProtoBadWorkObject{
		WoHeader: protoWorkObjectHeader,
		WoBody:   protoWorkObjectBody,
	}
}

// ProtoDecode decodes the protobuf encoding of the bad workObject.
func (b *badWorkObject) ProtoDecode(pb *ProtoBadWorkObject, location common.Location) error {
	woHeader := new(types.WorkObjectHeader)
	if err := woHeader.ProtoDecode(pb.WoHeader, location); err != nil {
		return err
	}
	b.woHeader = woHeader
	woBody := new(types.WorkObjectBody)
	if err := woBody.ProtoDecode(pb.WoBody, b.woHeader.Location(), types.BlockObject); err != nil {
		return err
	}
	b.woBody = woBody
	return nil
}

// badWorkObjectList implements the sort interface to allow sorting a list of
// bad blocks by their number in the reverse order.
type badWorkObjectList []*badWorkObject

func (s badWorkObjectList) Len() int { return len(s) }
func (s badWorkObjectList) Less(i, j int) bool {
	return s[i].woHeader.NumberU64() < s[j].woHeader.NumberU64()
}
func (s badWorkObjectList) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s badWorkObjectList) ProtoEncode() *ProtoBadWorkObjects {
	protoList := make([]*ProtoBadWorkObject, len(s))
	for i, bad := range s {
		protoList[i] = bad.ProtoEncode()
	}
	return &ProtoBadWorkObjects{BadWorkObjects: protoList}
}

func (s *badWorkObjectList) ProtoDecode(pb *ProtoBadWorkObjects, location common.Location) error {
	list := make(badWorkObjectList, len(pb.BadWorkObjects))
	for i, protoBlock := range pb.BadWorkObjects {
		block := new(badWorkObject)
		if err := block.ProtoDecode(protoBlock, location); err != nil {
			return err
		}
		list[i] = block
	}
	*s = list
	return nil
}

// ReadBadWorkObject retrieves the bad workObject with the corresponding workObject hash.
func ReadBadWorkObject(db ethdb.Reader, hash common.Hash) *types.WorkObject {
	blob, err := db.Get(badWorkObjectKey)
	if err != nil {
		return nil
	}
	protoBadWorkObjects := new(ProtoBadWorkObjects)
	err = proto.Unmarshal(blob, protoBadWorkObjects)
	if err != nil {
		return nil
	}

	badWorkObjects := new(badWorkObjectList)
	err = badWorkObjects.ProtoDecode(protoBadWorkObjects, db.Location())
	if err != nil {
		return nil
	}
	for _, bad := range *badWorkObjects {
		if bad.woHeader.Hash() == hash {
			return types.NewWorkObject(bad.woHeader, bad.woBody, nil)
		}
	}
	return nil
}

// FindCommonAncestor returns the last common ancestor of two block headers
func FindCommonAncestor(db ethdb.Reader, a, b *types.WorkObject, nodeCtx int) *types.WorkObject {
	for bn := b.NumberU64(nodeCtx); a.NumberU64(nodeCtx) > bn; {
		a = ReadHeader(db, a.ParentHash(nodeCtx))
		if a == nil {
			return nil
		}
	}
	for an := a.NumberU64(nodeCtx); an < b.NumberU64(nodeCtx); {
		b = ReadHeader(db, b.ParentHash(nodeCtx))
		if b == nil {
			return nil
		}
	}
	for a.Hash() != b.Hash() {
		a = ReadHeader(db, a.ParentHash(nodeCtx))
		if a == nil {
			return nil
		}
		b = ReadHeader(db, b.ParentHash(nodeCtx))
		if b == nil {
			return nil
		}
	}
	return a
}

// ReadHeadBlock returns the current canonical head block.
func ReadHeadBlock(db ethdb.Reader) *types.WorkObject {
	headWorkObjectHash := ReadHeadBlockHash(db)
	if headWorkObjectHash == (common.Hash{}) {
		return nil
	}
	headWorkObjectNumber := ReadHeaderNumber(db, headWorkObjectHash)
	if headWorkObjectNumber == nil {
		return nil
	}
	return ReadWorkObject(db, headWorkObjectHash, types.BlockObject)
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
		db.Logger().WithField("err", err).Fatal("Failed to store pending etxs")
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
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal pending etxs")
	}
	pendingEtxs := new(types.PendingEtxs)
	if err := pendingEtxs.ProtoDecode(protoPendingEtxs, db.Location()); err != nil {
		db.Logger().WithFields(log.Fields{
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
		db.Logger().WithField("err", err).Fatal("Failed to proto encode pending etxs")
	}
	data, err := proto.Marshal(protoPendingEtxs)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal pending etxs")
	}
	WritePendingEtxsProto(db, pendingEtxs.Header.Hash(), data)
}

// DeletePendingEtxs removes all pending ETX data associated with a block.
func DeletePendingEtxs(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pendingEtxsKey(hash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete pending etxs")
	}
}

// ReadPendingEtxsRollup retreives the pending ETXs rollup corresponding to a given block
func ReadPendingEtxsRollup(db ethdb.Reader, hash common.Hash) *types.PendingEtxsRollup {
	// Try to look up the data in leveldb.
	data, _ := db.Get(pendingEtxsRollupKey(hash))
	if len(data) == 0 {
		return nil
	}
	protoPendingEtxsRollup := new(types.ProtoPendingEtxsRollup)
	err := proto.Unmarshal(data, protoPendingEtxsRollup)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal pending etxs rollup")
	}
	pendingEtxsRollup := new(types.PendingEtxsRollup)
	err = pendingEtxsRollup.ProtoDecode(protoPendingEtxsRollup, db.Location())
	if err != nil {
		db.Logger().WithFields(log.Fields{
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
		db.Logger().WithField("err", err).Fatal("Failed to proto encode pending etxs rollup")
	}
	data, err := proto.Marshal(protoPendingEtxsRollup)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal pending etxs rollup")
	}
	if err := db.Put(pendingEtxsRollupKey(pendingEtxsRollup.Header.Hash()), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store pending etxs rollup")
	}
}

// DeletePendingEtxsRollup removes all pending ETX rollup data associated with a block.
func DeletePendingEtxsRollup(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(pendingEtxsRollupKey(hash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete pending etxs rollup")
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
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal manifest")
	}
	manifest := new(types.BlockManifest)
	err = manifest.ProtoDecode(protoManifest)
	if err != nil {
		db.Logger().WithFields(log.Fields{
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
		db.Logger().WithField("err", err).Fatal("Failed to proto encode manifest")
	}
	data, err := proto.Marshal(protoManifest)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal manifest")
	}
	if err := db.Put(manifestKey(hash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store manifest")
	}
}

// DeleteManifest removes manifest data associated with a block.
func DeleteManifest(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(manifestKey(hash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete manifest")
	}
}

// ReadInterlinkHashes retreives the interlinkhashes corresponding to a given block
func ReadInterlinkHashes(db ethdb.Reader, hash common.Hash) common.Hashes {
	// Try to look up the data in leveldb.
	data, _ := db.Get(interlinkHashKey(hash))
	if len(data) == 0 {
		return nil
	}
	protoInterlinkHashes := new(common.ProtoHashes)
	err := proto.Unmarshal(data, protoInterlinkHashes)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal interlink hashes")
	}
	interlinkHashes := new(common.Hashes)
	interlinkHashes.ProtoDecode(protoInterlinkHashes)
	return *interlinkHashes
}

// WriteInterlinkHashes stores the interlink hashes corresponding to a given block
func WriteInterlinkHashes(db ethdb.KeyValueWriter, hash common.Hash, interlinkHashes common.Hashes) {
	protoInterlinkHashes := interlinkHashes.ProtoEncode()
	data, err := proto.Marshal(protoInterlinkHashes)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal interlink hashes")
	}
	if err := db.Put(interlinkHashKey(hash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store interlink hashes")
	}
}

// DeleteInterlinkHashes removes interlinkHashes data associated with a block.
func DeleteInterlinkHashes(db ethdb.KeyValueWriter, hash common.Hash) {
	if err := db.Delete(interlinkHashKey(hash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete interlink hashes")
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
		db.Logger().WithField("err", err).Fatal("Failed to store block bloom filter")
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
		db.Logger().WithField("err", err).Fatal("Failed to Proto encode pending etxs")
	}
	WriteBloomProto(db, hash, data)
}

// DeleteBloom removes all bloom data associated with a block.
func DeleteBloom(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(bloomKey(hash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete bloom")
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
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal badHashesList")
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
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal badHashesList")
	}
	if err := db.Put(badHashesListPrefix, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store badHashesList")
	}
}

// DeleteBadHashesList removes badHashesList from the database
func DeleteBadHashesList(db ethdb.KeyValueWriter) {
	if err := db.Delete(badHashesListPrefix); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete badHashesList")
	}
}

// WriteInboundEtxs stores the inbound etxs for a given dom block hashes
func WriteInboundEtxs(db ethdb.KeyValueWriter, hash common.Hash, inboundEtxs types.Transactions) {
	protoInboundEtxs, err := inboundEtxs.ProtoEncode()
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto encode inbound etxs")
	}
	data, err := proto.Marshal(protoInboundEtxs)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal inbound etxs")
	}
	if err := db.Put(inboundEtxsKey(hash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store badHashesList")
	}
}

// ReadInboundEtxs reads the inbound etxs from the database
func ReadInboundEtxs(db ethdb.Reader, hash common.Hash) types.Transactions {
	// Try to look up the data in leveldb.
	data, err := db.Get(inboundEtxsKey(hash))
	if err != nil {
		return nil
	}
	protoInboundEtxs := new(types.ProtoTransactions)
	err = proto.Unmarshal(data, protoInboundEtxs)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal inbound etxs")
	}
	inboundEtxs := types.Transactions{}
	err = inboundEtxs.ProtoDecode(protoInboundEtxs, db.Location())
	if err != nil {
		db.Logger().WithFields(log.Fields{
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
		db.Logger().WithField("err", err).Fatal("Failed to delete inbound etxs")
	}
}

func WriteAddressOutpoints(db ethdb.KeyValueWriter, outpointMap map[string]map[string]*types.OutpointAndDenomination) error {
	for address, outpoints := range outpointMap {
		if err := WriteOutpointsForAddress(db, address, outpoints); err != nil {
			return err
		}
	}
	return nil
}

func WriteOutpointsForAddress(db ethdb.KeyValueWriter, address string, outpoints map[string]*types.OutpointAndDenomination) error {

	addressOutpointsProto := &types.ProtoAddressOutPoints{
		OutPoints: make(map[string]*types.ProtoOutPointAndDenomination, len(outpoints)),
	}

	for _, outpoint := range outpoints {
		outpointProto, err := outpoint.ProtoEncode()
		if err != nil {
			return err
		}

		addressOutpointsProto.OutPoints[outpoint.Key()] = outpointProto
	}

	// Now, marshal utxosProto to protobuf bytes
	data, err := proto.Marshal(addressOutpointsProto)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxos")
	}
	if err := db.Put(addressUtxosKey(address), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store utxos")
	}

	// And finally, store the data in the database under the appropriate key
	return db.Put(AddressUtxosPrefix, data)
}

func ReadOutpointsForAddress(db ethdb.Reader, address string) map[string]*types.OutpointAndDenomination {
	// Try to look up the data in leveldb.
	data, _ := db.Get(addressUtxosKey(address))
	if len(data) == 0 {
		return make(map[string]*types.OutpointAndDenomination)
	}
	addressOutpointsProto := &types.ProtoAddressOutPoints{
		OutPoints: make(map[string]*types.ProtoOutPointAndDenomination),
	}
	if err := proto.Unmarshal(data, addressOutpointsProto); err != nil {
		return nil
	}
	outpoints := map[string]*types.OutpointAndDenomination{}

	for _, outpointProto := range addressOutpointsProto.OutPoints {
		outpoint := new(types.OutpointAndDenomination)
		err := outpoint.ProtoDecode(outpointProto)
		if err != nil {
			db.Logger().WithFields(log.Fields{
				"err":      err,
				"outpoint": outpointProto,
			}).Error("Invalid outpointProto")
			return nil
		}
		outpoints[outpoint.Key()] = outpoint
	}

	return outpoints
}

func DeleteAddressUtxos(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	if err := db.Delete(AddressUtxosPrefix); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete utxos")
	}
}

func WriteGenesisHashes(db ethdb.KeyValueWriter, hashes common.Hashes) {
	protoHashes := hashes.ProtoEncode()
	data, err := proto.Marshal(protoHashes)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal genesis hashes")
	}

	if err := db.Put(genesisHashesKey, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store genesis hashes")
	}
}

func ReadGenesisHashes(db ethdb.Reader) common.Hashes {
	data, _ := db.Get(genesisHashesKey)
	if len(data) == 0 {
		return common.Hashes{}
	}
	protoHashes := new(common.ProtoHashes)
	err := proto.Unmarshal(data, protoHashes)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal genesis hashes")
	}

	hashes := common.Hashes{}
	hashes.ProtoDecode(protoHashes)

	return hashes
}

func DeleteGenesisHashes(db ethdb.KeyValueWriter) {
	if err := db.Delete(genesisHashesKey); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete genesis hashes")
	}
}
