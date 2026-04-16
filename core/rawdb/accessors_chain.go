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
	"fmt"
	"math/big"
	"slices"
	"sort"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto/multiset"
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
	if has, err := db.Has(headerKey(number, hash)); !has || err != nil {
		return false
	}
	return true
}

// ReadHeader retrieves the block header corresponding to the hash.
func ReadHeader(db ethdb.Reader, number uint64, hash common.Hash) *types.WorkObject {
	wo := ReadWorkObjectHeaderOnly(db, number, hash, types.BlockObject)
	if wo == nil || wo.Body() == nil || wo.Header() == nil {
		// Try backup function
		return ReadWorkObject(db, number, hash, types.BlockObject)
	}
	return wo
}

// DeleteHeader removes all block header data associated with a hash.
func DeleteHeader(db ethdb.KeyValueWriter, hash common.Hash, number uint64) {
	DeleteWorkObjectHeader(db, number, hash, types.BlockObject)
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
	err = body.ProtoDecode(protoWorkObject, db.Location(), types.BlockObject)
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
	protoBody, err := body.ProtoEncode(types.BlockObject)
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

func ReadWorkShareForDonorHash(db ethdb.Reader, kawpowEngine consensus.Engine, donorHash common.Hash) *types.WorkObjectHeader {
	key := donorHashKey(donorHash)
	blockHashBytes, _ := db.Get(key)
	if len(blockHashBytes) == 0 {
		return nil
	}

	blockHash := common.BytesToHash(blockHashBytes)

	blockNumber := ReadHeaderNumber(db, blockHash)
	if blockNumber == nil {
		return nil
	}
	if *blockNumber == 0 {
		return nil
	}

	block := ReadWorkObject(db, *blockNumber, blockHash, types.BlockObject)
	if block == nil {
		return nil
	}

	// If the block itself is the donor, return it
	if block.AuxPow() != nil {
		blockHash := block.AuxPow().Header().BlockHash()
		if block.AuxPow().PowID() == types.Kawpow {
			// Kawpow uses pow hash as the block hash identifier in explorer and api
			var err error
			blockHash, err = kawpowEngine.ComputePowHash(block.WorkObjectHeader())
			if err != nil {
				db.Logger().WithField("err", err).Warn("Failed to compute pow hash")
				return nil
			}
		}
		if blockHash == donorHash {
			return block.WorkObjectHeader()
		}
	}

	// Otherwise check the uncles for a match
	for _, uncle := range block.Body().Uncles() {
		if uncle.AuxPow() != nil {
			uncleHash := uncle.AuxPow().Header().BlockHash()
			if uncle.AuxPow().PowID() == types.Kawpow {
				// Kawpow uses pow hash as the block hash identifier in explorer and api
				var err error
				uncleHash, err = kawpowEngine.ComputePowHash(uncle)
				if err != nil {
					db.Logger().WithField("err", err).Warn("Failed to compute pow hash")
					return nil
				}
			}
			if uncleHash == donorHash {
				return uncle
			}
		}
	}

	db.Logger().WithField("donor hash", donorHash).Error("Found donor hash stored, but didnt match any uncle or block")
	return nil
}

func WriteWorkShareForDonorHash(db ethdb.KeyValueWriter, kawpowEngine consensus.Engine, hash common.Hash, workObject *types.WorkObject, woType types.WorkObjectView, nodeCtx int) {
	if nodeCtx != common.ZONE_CTX {
		return
	}
	// check the block first
	if workObject.AuxPow() != nil {
		auxHeaderHash := workObject.AuxPow().Header().BlockHash()
		if workObject.AuxPow().PowID() == types.Kawpow {
			// Kawpow uses pow hash as the block hash identifier in explorer and api
			var err error
			auxHeaderHash, err = kawpowEngine.ComputePowHash(workObject.WorkObjectHeader())
			if err != nil {
				db.Logger().WithField("err", err).Warn("Failed to compute pow hash")
				return
			}
		}
		key := donorHashKey(auxHeaderHash)
		// store the work object header hash against the donor block hash
		if err := db.Put(key, hash.Bytes()); err != nil {
			db.Logger().WithField("err", err).Fatal("Failed to store work object header")
		}
	}

	for _, uncle := range workObject.Body().Uncles() {
		if uncle.AuxPow() != nil {
			auxHeaderHash := uncle.AuxPow().Header().BlockHash()
			if uncle.AuxPow().PowID() == types.Kawpow {
				// Kawpow uses pow hash as the block hash identifier in explorer and api
				var err error
				auxHeaderHash, err = kawpowEngine.ComputePowHash(uncle)
				if err != nil {
					db.Logger().WithField("err", err).Warn("Failed to compute pow hash")
					return
				}
			}
			key := donorHashKey(auxHeaderHash)
			// store the work object header hash against the donor block hash
			if err := db.Put(key, hash.Bytes()); err != nil {
				db.Logger().WithField("err", err).Fatal("Failed to store work object header")
			}
		}
	}
}

// WriteBlockHashForWorkShareHash stores the mapping of workshare hash to block
// hash that processed the payment for the workshare
func WriteBlockHashForWorkShareHash(db ethdb.KeyValueWriter, workObject *types.WorkObject) {
	for _, tx := range workObject.Body().Transactions() {
		if tx.Type() == types.ExternalTxType && tx.EtxType() == types.CoinbaseType {
			// workshare hash is stored in the coinbase tx
			if len(tx.Data()) < 32 {
				continue
			}
			workshareHash := tx.Data()[len(tx.Data())-common.HashLength:]
			key := workShareHashToBlockHashKey(common.BytesToHash(workshareHash))
			if err := db.Put(key, workObject.Hash().Bytes()); err != nil {
				db.Logger().WithField("err", err).Warn("Failed to store workshare hash to block hash mapping")
			}
		}
	}
}

// ReadBlockForWorkShareHash retreive's the block that processed the inbound
// payment to the given workshare hash
func ReadBlockForWorkShareHash(db ethdb.Reader, workshareHash common.Hash) *types.WorkObject {
	key := workShareHashToBlockHashKey(workshareHash)
	data, _ := db.Get(key)
	if len(data) == 0 {
		return nil
	}
	blockHash := common.BytesToHash(data)
	blockNumber := ReadHeaderNumber(db, blockHash)
	if blockNumber == nil {
		return nil
	}
	if *blockNumber == 0 {
		return nil
	}
	return ReadWorkObject(db, *blockNumber, blockHash, types.BlockObject)
}

// ReadWorkObjectHeader retreive's the work object header stored in hash.
func ReadWorkObjectHeader(db ethdb.Reader, number uint64, hash common.Hash, woType types.WorkObjectView) *types.WorkObjectHeader {
	var key []byte
	switch woType {
	case types.BlockObject:
		key = headerKey(number, hash)
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
		key = headerKey(workObject.NumberU64(nodeCtx), hash)
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
func DeleteWorkObjectHeader(db ethdb.KeyValueWriter, number uint64, hash common.Hash, woType types.WorkObjectView) {
	var key []byte
	switch woType {
	case types.BlockObject:
		key = headerKey(number, hash)
	}
	if err := db.Delete(key); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete work object header ")
	}
}

// ReadWorkObject retreive's the work object stored in hash.
func ReadWorkObject(db ethdb.Reader, number uint64, hash common.Hash, woType types.WorkObjectView) *types.WorkObject {
	workObjectHeader := ReadWorkObjectHeader(db, number, hash, woType)
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
func ReadWorkObjectWithWorkShares(db ethdb.Reader, number uint64, hash common.Hash) *types.WorkObject {
	workObjectHeader := ReadWorkObjectHeader(db, number, hash, types.BlockObject)
	if workObjectHeader == nil {
		return nil
	}
	workObjectBody := ReadWorkObjectBody(db, hash, types.WorkShareObject)
	if workObjectBody == nil {
		return nil
	}
	return types.NewWorkObject(workObjectHeader, workObjectBody, nil) //TODO: mmtx transaction
}

func ReadWorkObjectHeaderOnly(db ethdb.Reader, number uint64, hash common.Hash, woType types.WorkObjectView) *types.WorkObject {
	workObjectHeader := ReadWorkObjectHeader(db, number, hash, woType)
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
	DeleteWorkObjectHeader(db, number, hash, woType) //TODO: mmtx transaction
	DeleteHeader(db, hash, number)
	DeleteReceipts(db, hash, number)
}

// DeleteWorkObjectWithoutNumber removes all block data associated with a hash, except
// the hash to number mapping.
func DeleteBlockWithoutNumber(db ethdb.KeyValueWriter, hash common.Hash, number uint64, woType types.WorkObjectView) {
	DeleteWorkObjectBody(db, hash)
	DeleteWorkObjectHeader(db, number, hash, woType) //TODO: mmtx transaction
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

// ReadBestPendingHeader retreive's the pending header stored in hash.
func ReadBestPendingHeader(db ethdb.Reader) *types.WorkObject {
	data, _ := db.Get(pendingHeaderPrefix)
	if len(data) == 0 {
		return nil
	}

	protoPendingHeader := new(types.ProtoWorkObject)
	err := proto.Unmarshal(data, protoPendingHeader)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal pending header")
	}

	pendingHeader := new(types.WorkObject)

	err = pendingHeader.ProtoDecode(protoPendingHeader, db.Location(), types.BlockObject)
	if err != nil {
		db.Logger().WithFields(log.Fields{
			"err": err,
		}).Error("Invalid pendingHeader Proto")
		return nil
	}
	return pendingHeader
}

// WriteBestPendingHeader writes the pending header of the terminus hash.
func WriteBestPendingHeader(db ethdb.KeyValueWriter, pendingHeader *types.WorkObject) {

	protoPendingHeader, err := pendingHeader.ProtoEncode(types.BlockObject)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto encode pending header")
	}
	data, err := proto.Marshal(protoPendingHeader)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Marshal pending header")
	}
	if err := db.Put(pendingHeaderPrefix, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store header")
	}
}

// DeleteBestPendingHeader deletes the pending header stored for the header hash.
func DeleteBestPendingHeader(db ethdb.KeyValueWriter) {
	if err := db.Delete(pendingHeaderPrefix); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete slice pending header ")
	}
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
	body := ReadWorkObject(db, number, hash, types.BlockObject)
	if body == nil {
		db.Logger().WithFields(log.Fields{
			"hash":   hash,
			"number": number,
		}).Error("Missing body but have receipt")
		return nil
	}
	if err := receipts.DeriveFields(config, hash, number, body.Transactions()); err != nil {
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

func IsGenesisHash(db ethdb.Reader, hash common.Hash) bool {
	genesisHashes := ReadGenesisHashes(db)
	return slices.Contains(genesisHashes, hash)
}

// FindCommonAncestor returns the last common ancestor of two block headers
func FindCommonAncestor(db ethdb.Reader, a, b *types.WorkObject, nodeCtx int) (*types.WorkObject, error) {
	for bn := b.NumberU64(nodeCtx); a.NumberU64(nodeCtx) > bn; {
		a = ReadHeader(db, a.NumberU64(nodeCtx)-1, a.ParentHash(nodeCtx))
		if IsGenesisHash(db, a.Hash()) {
			return a, nil
		}
		if a == nil {
			return nil, fmt.Errorf("unable to find hash %s", a.ParentHash(nodeCtx).String())
		}
	}
	for an := a.NumberU64(nodeCtx); an < b.NumberU64(nodeCtx); {
		b = ReadHeader(db, b.NumberU64(nodeCtx)-1, b.ParentHash(nodeCtx))
		if IsGenesisHash(db, b.Hash()) {
			return b, nil
		}
		if b == nil {
			return nil, fmt.Errorf("unable to find hash %s", b.ParentHash(nodeCtx).String())
		}
	}
	for a.Hash() != b.Hash() {
		a = ReadHeader(db, a.NumberU64(nodeCtx)-1, a.ParentHash(nodeCtx))
		if a == nil {
			return nil, fmt.Errorf("unable to find hash %s", a.ParentHash(nodeCtx).String())
		}
		b = ReadHeader(db, b.NumberU64(nodeCtx)-1, b.ParentHash(nodeCtx))
		if b == nil {
			return nil, fmt.Errorf("unable to find hash %s", b.ParentHash(nodeCtx).String())
		}
		if IsGenesisHash(db, a.ParentHash(nodeCtx)) && IsGenesisHash(db, b.ParentHash(nodeCtx)) {
			number := ReadHeaderNumber(db, a.ParentHash(nodeCtx))
			header := ReadHeader(db, *number, a.ParentHash(nodeCtx))
			if header == nil {
				return nil, fmt.Errorf("unable to find hash %s", a.ParentHash(nodeCtx).String())
			}
			return header, nil
		}
	}
	return a, nil
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
	return ReadWorkObject(db, *headWorkObjectNumber, headWorkObjectHash, types.BlockObject)
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

func WriteAddressUTXOs(db ethdb.KeyValueWriter, readDb ethdb.Reader, newOutpointsMap map[[20]byte][]*types.OutpointAndDenomination) error {
	for address, outpoints := range newOutpointsMap {
		addressOutpointsProto := &types.ProtoAddressOutPoints{
			OutPoints: make([]*types.ProtoOutPointAndDenomination, 0),
		}

		data, _ := readDb.Get(addressUtxosWithoutHeightKey(address))
		if len(data) != 0 {
			if err := proto.Unmarshal(data, addressOutpointsProto); err != nil {
				return fmt.Errorf("Failed to proto Unmarshal address outpoints: %v", err)
			}
		}

		for _, outpoint := range outpoints {
			outpointProto, err := outpoint.ProtoEncode()
			if err != nil {
				return err
			}
			addressOutpointsProto.OutPoints = append(addressOutpointsProto.OutPoints, outpointProto)
		}
		// Now, marshal utxosProto to protobuf bytes
		data, err := proto.Marshal(addressOutpointsProto)
		if err != nil {
			db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxos")
		}
		if err := db.Put(addressUtxosWithoutHeightKey(address), data); err != nil {
			db.Logger().WithField("err", err).Fatal("Failed to store utxos")
		}
	}
	return nil
}

// Make sure this always runs *after* WriteAddressUTXOs
func DeleteAddressUTXOsWithBatch(batch ethdb.Batch, readDb ethdb.Reader, outpointsToRemoveMap map[[20]byte][]*types.OutPoint) error {
	for address, outpoints := range outpointsToRemoveMap {
		_, data := batch.GetPending(addressUtxosWithoutHeightKey(address))
		if len(data) == 0 {
			data, _ = readDb.Get(addressUtxosWithoutHeightKey(address))
			if len(data) == 0 {
				return fmt.Errorf("No UTXO data found for address %x", address)
			}
		}

		addressOutpointsProto := &types.ProtoAddressOutPoints{
			OutPoints: make([]*types.ProtoOutPointAndDenomination, 0),
		}
		if err := proto.Unmarshal(data, addressOutpointsProto); err != nil {
			return fmt.Errorf("Failed to proto Unmarshal address outpoints: %v", err)
		}
		for _, outpoint := range outpoints {
			for i := 0; i < len(addressOutpointsProto.OutPoints); i++ {
				outpointProto := addressOutpointsProto.OutPoints[i]
				if common.Hash(outpointProto.Hash.GetValue()) == outpoint.TxHash && outpointProto.Index != nil && *outpointProto.Index == uint32(outpoint.Index) {
					if i == len(addressOutpointsProto.OutPoints)-1 {
						// Remove the last element
						addressOutpointsProto.OutPoints = addressOutpointsProto.OutPoints[:i]
					} else {
						// Remove from the outpoints slice
						addressOutpointsProto.OutPoints = slices.Delete(addressOutpointsProto.OutPoints, i, i+1)
						// Decrement i to account for the removed element
						i--
					}
				}
			}
		}

		// Now, marshal addressOutpointsProto to protobuf bytes
		data, err := proto.Marshal(addressOutpointsProto)
		if err != nil {
			return fmt.Errorf("Failed to proto Marshal address outpoints: %v", err)
		}
		if len(data) == 0 {
			// If the data is empty, delete the key from the database
			if err := batch.Delete(addressUtxosWithoutHeightKey(address)); err != nil {
				return fmt.Errorf("Failed to delete address outpoints: %v", err)
			}
		} else {
			// Otherwise, update the key with the new data
			if err := batch.Put(addressUtxosWithoutHeightKey(address), data); err != nil {
				return fmt.Errorf("Failed to store address outpoints: %v", err)
			}
		}
	}
	return nil
}

func ReadAddressUTXOs(db ethdb.Reader, address [20]byte) ([]*types.OutpointAndDenomination, error) {
	// Try to look up the data in leveldb.
	data, _ := db.Get(addressUtxosWithoutHeightKey(address))
	if len(data) == 0 {
		return []*types.OutpointAndDenomination{}, nil
	}
	addressOutpointsProto := &types.ProtoAddressOutPoints{
		OutPoints: make([]*types.ProtoOutPointAndDenomination, 0),
	}
	if err := proto.Unmarshal(data, addressOutpointsProto); err != nil {
		return nil, err
	}
	outpoints := make([]*types.OutpointAndDenomination, 0, len(addressOutpointsProto.OutPoints))

	for _, outpointProto := range addressOutpointsProto.OutPoints {
		outpoint := new(types.OutpointAndDenomination)
		err := outpoint.ProtoDecode(outpointProto)
		if err != nil {
			db.Logger().WithFields(log.Fields{
				"err":      err,
				"outpoint": outpointProto,
			}).Error("Invalid outpointProto")
			return nil, err
		}
		outpoints = append(outpoints, outpoint)
	}

	return outpoints, nil
}

func WriteAddressOutpoints(db ethdb.KeyValueWriter, outpointMap map[[20]byte][]*types.OutpointAndDenomination) error {
	for addressWithBlockHeight, outpoints := range outpointMap {
		if err := WriteOutpointsForAddressAndBlockHeight(db, addressWithBlockHeight, outpoints); err != nil {
			return err
		}
	}
	return nil
}

func WriteOutpointsForAddressAndBlockHeight(db ethdb.KeyValueWriter, address [20]byte, outpoints []*types.OutpointAndDenomination) error {

	addressOutpointsProto := &types.ProtoAddressOutPoints{
		OutPoints: make([]*types.ProtoOutPointAndDenomination, 0, len(outpoints)),
	}

	for _, outpoint := range outpoints {
		outpointProto, err := outpoint.ProtoEncode()
		if err != nil {
			return err
		}

		addressOutpointsProto.OutPoints = append(addressOutpointsProto.OutPoints, outpointProto)
	}

	// Now, marshal utxosProto to protobuf bytes
	data, err := proto.Marshal(addressOutpointsProto)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxos")
	}
	if err := db.Put(addressUtxosKey(address), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store utxos")
	}
	return nil
}

func ReadOutpointsForAddressAtBlock(db ethdb.Reader, address [20]byte) ([]*types.OutpointAndDenomination, error) {
	// Try to look up the data in leveldb.
	data, _ := db.Get(addressUtxosKey(address))
	if len(data) == 0 {
		return []*types.OutpointAndDenomination{}, nil
	}
	addressOutpointsProto := &types.ProtoAddressOutPoints{
		OutPoints: make([]*types.ProtoOutPointAndDenomination, 0),
	}
	if err := proto.Unmarshal(data, addressOutpointsProto); err != nil {
		return nil, err
	}
	outpoints := make([]*types.OutpointAndDenomination, 0, len(addressOutpointsProto.OutPoints))

	for _, outpointProto := range addressOutpointsProto.OutPoints {
		outpoint := new(types.OutpointAndDenomination)
		err := outpoint.ProtoDecode(outpointProto)
		if err != nil {
			db.Logger().WithFields(log.Fields{
				"err":      err,
				"outpoint": outpointProto,
			}).Error("Invalid outpointProto")
			return nil, err
		}
		outpoints = append(outpoints, outpoint)
	}

	return outpoints, nil
}

func ReadOutpointsForAddress(db ethdb.Database, address common.Address) ([]*types.OutpointAndDenomination, error) {
	prefix := append(AddressUtxosPrefix, address.Bytes()[:16]...)
	it := db.NewIterator(prefix, nil)
	defer it.Release()
	outpoints := make([]*types.OutpointAndDenomination, 0)
	for it.Next() {
		if len(it.Key()) != len(AddressUtxosPrefix)+common.AddressLength {
			continue
		}
		addressOutpointsProto := &types.ProtoAddressOutPoints{
			OutPoints: make([]*types.ProtoOutPointAndDenomination, 0),
		}
		if err := proto.Unmarshal(it.Value(), addressOutpointsProto); err != nil {
			db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal addressOutpointsProto")
			return nil, err
		}
		for _, outpointProto := range addressOutpointsProto.OutPoints {
			outpoint := new(types.OutpointAndDenomination)
			if err := outpoint.ProtoDecode(outpointProto); err != nil {
				db.Logger().WithFields(log.Fields{
					"err":      err,
					"outpoint": outpointProto,
				}).Error("Invalid outpointProto")
				return nil, err
			}
			outpoints = append(outpoints, outpoint)
		}
	}
	return outpoints, nil
}

func DeleteOutpointsForAddress(db ethdb.KeyValueWriter, address [20]byte) {
	if err := db.Delete(addressUtxosKey(address)); err != nil {
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

func CreateUTXO(db ethdb.KeyValueWriter, txHash common.Hash, index uint16, utxo *types.UtxoEntry) error {
	utxoProto, err := utxo.ProtoEncode()
	if err != nil {
		return err
	}

	// Now, marshal utxoProto to protobuf bytes
	data, err := proto.Marshal(utxoProto)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxo")
	}

	// And finally, store the data in the database under the appropriate key
	return db.Put(UtxoKey(txHash, index), data)
}

func GetUTXOWithBatch(db ethdb.KeyValueReader, batch ethdb.Batch, txHash common.Hash, index uint16) *types.UtxoEntry {
	deleted, data := batch.GetPending(UtxoKey(txHash, index))
	if deleted {
		return nil
	} else if data != nil && len(data) == 0 {
		return nil
	} else if data == nil {
		data, _ = db.Get(UtxoKey(txHash, index))
		if len(data) == 0 {
			return nil
		}
	}
	utxoProto := new(types.ProtoTxOut)
	if err := proto.Unmarshal(data, utxoProto); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal utxo")
	}

	utxo := new(types.UtxoEntry)
	if err := utxo.ProtoDecode(utxoProto); err != nil {
		db.Logger().WithFields(log.Fields{
			"txHash": txHash,
			"index":  index,
			"err":    err,
		}).Error("Invalid utxo Proto")
		return nil
	}

	return utxo
}

func GetUTXO(db ethdb.KeyValueReader, txHash common.Hash, index uint16) *types.UtxoEntry {
	// Try to look up the data in leveldb.
	data, _ := db.Get(UtxoKey(txHash, index))
	if len(data) == 0 {
		return nil
	}

	utxoProto := new(types.ProtoTxOut)
	if err := proto.Unmarshal(data, utxoProto); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to proto Unmarshal utxo")
	}

	utxo := new(types.UtxoEntry)
	if err := utxo.ProtoDecode(utxoProto); err != nil {
		db.Logger().WithFields(log.Fields{
			"txHash": txHash,
			"index":  index,
			"err":    err,
		}).Error("Invalid utxo Proto")
		return nil
	}

	return utxo
}

func DeleteUTXO(db ethdb.KeyValueWriter, txHash common.Hash, index uint16) {
	if err := db.Delete(UtxoKey(txHash, index)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete utxo")
	}
}

func ReadMultiSet(db ethdb.Reader, blockHash common.Hash) *multiset.MultiSet {
	data, _ := db.Get(multiSetKey(blockHash))
	if len(data) == 0 {
		return nil
	}
	multiSet, err := multiset.FromBytes(data)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to decode multiSet")
	}
	return multiSet
}

func WriteMultiSet(db ethdb.KeyValueWriter, blockHash common.Hash, multiSet *multiset.MultiSet) {
	data := multiSet.Serialize()
	if err := db.Put(multiSetKey(blockHash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store multiSet")
	}
}

func ReadTokenChoicesSet(db ethdb.Reader, blockHash common.Hash) *types.TokenChoiceSet {
	data, _ := db.Get(tokenChoiceSetKey(blockHash))
	if len(data) == 0 {
		return nil
	}
	protoTokenChoiceSet := new(types.ProtoTokenChoiceSet)
	if err := proto.Unmarshal(data, protoTokenChoiceSet); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to unmarshal tokenChoicesSample")
		return nil
	}

	tokenChoiceSet := new(types.TokenChoiceSet)
	if err := tokenChoiceSet.ProtoDecode(protoTokenChoiceSet); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to decode tokenChoicesSample")
		return nil
	}
	return tokenChoiceSet
}

func WriteTokenChoicesSet(db ethdb.KeyValueWriter, blockHash common.Hash, tokenChoiceSet *types.TokenChoiceSet) error {
	protoTokenChoiceSet, err := tokenChoiceSet.ProtoEncode()
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to encode tokenChoicesSample")
		return err
	}
	data, err := proto.Marshal(protoTokenChoiceSet)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to marshal tokenChoicesSample")
		return err
	}
	if err := db.Put(tokenChoiceSetKey(blockHash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store tokenChoicesSample")
		return err
	}
	return nil
}

func DeleteTokenChoicesSet(db ethdb.KeyValueWriter, blockHash common.Hash) {
	if err := db.Delete(tokenChoiceSetKey(blockHash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete token choices set key")
	}
}

func WriteSpentUTXOs(db ethdb.KeyValueWriter, blockHash common.Hash, spentUTXOs []*types.SpentUtxoEntry) error {
	protoSpentUTXOs := &types.ProtoSpentUTXOs{}
	for _, utxo := range spentUTXOs {
		utxoProto, err := utxo.ProtoEncode()
		if err != nil {
			return err
		}
		protoSpentUTXOs.Sutxos = append(protoSpentUTXOs.Sutxos, utxoProto)
	}
	data, err := proto.Marshal(protoSpentUTXOs)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxo")
	}

	// And finally, store the data in the database under the appropriate key
	return db.Put(spentUTXOsKey(blockHash), data)
}

func ReadSpentUTXOs(db ethdb.Reader, blockHash common.Hash) ([]*types.SpentUtxoEntry, error) {
	// Try to look up the data in leveldb.
	data, _ := db.Get(spentUTXOsKey(blockHash))
	if len(data) == 0 {
		return nil, nil
	}

	protoSpentUTXOs := new(types.ProtoSpentUTXOs)
	if err := proto.Unmarshal(data, protoSpentUTXOs); err != nil {
		return nil, err
	}

	spentUTXOs := make([]*types.SpentUtxoEntry, 0, len(protoSpentUTXOs.Sutxos))
	for _, utxoProto := range protoSpentUTXOs.Sutxos {
		utxo := new(types.SpentUtxoEntry)
		if err := utxo.ProtoDecode(utxoProto); err != nil {
			return nil, err
		}
		spentUTXOs = append(spentUTXOs, utxo)
	}
	return spentUTXOs, nil
}

func DeleteSpentUTXOs(db ethdb.KeyValueWriter, blockHash common.Hash) {
	if err := db.Delete(spentUTXOsKey(blockHash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete spent utxos")
	}
}

func WriteCreatedUTXOKeys(db ethdb.KeyValueWriter, blockHash common.Hash, createdUTXOKeys [][]byte) error {
	// Sort each key by the denomination in the key
	sort.Slice(createdUTXOKeys, func(i, j int) bool {
		return createdUTXOKeys[i][len(createdUTXOKeys[i])-1] < createdUTXOKeys[j][len(createdUTXOKeys[j])-1] // the last byte is the denomination
	})
	protoKeys := &types.ProtoKeys{Keys: make([][]byte, 0, len(createdUTXOKeys))}

	protoKeys.Keys = append(protoKeys.Keys, createdUTXOKeys...)

	data, err := proto.Marshal(protoKeys)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxo")
	}
	return db.Put(createdUTXOsKey(blockHash), data)
}

func ReadCreatedUTXOKeys(db ethdb.Reader, blockHash common.Hash) ([][]byte, error) {
	// Try to look up the data in leveldb.
	data, _ := db.Get(createdUTXOsKey(blockHash))
	if len(data) == 0 {
		return nil, nil
	}
	protoKeys := new(types.ProtoKeys)
	if err := proto.Unmarshal(data, protoKeys); err != nil {
		return nil, err
	}
	return protoKeys.Keys, nil
}

func DeleteCreatedUTXOKeys(db ethdb.KeyValueWriter, blockHash common.Hash) {
	if err := db.Delete(createdUTXOsKey(blockHash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete created utxo keys")
	}
}

func WriteCreatedCoinbaseLockupKeys(db ethdb.KeyValueWriter, blockHash common.Hash, keys [][]byte) error {
	protoKeys := &types.ProtoKeys{Keys: make([][]byte, 0, len(keys))}
	protoKeys.Keys = append(protoKeys.Keys, keys...)

	data, err := proto.Marshal(protoKeys)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxo")
	}
	return db.Put(createdCoinbaseLockupsKey(blockHash), data)
}

func ReadCreatedCoinbaseLockupKeys(db ethdb.Reader, blockHash common.Hash) ([][]byte, error) {
	// Try to look up the data in leveldb.
	data, _ := db.Get(createdCoinbaseLockupsKey(blockHash))
	if len(data) == 0 {
		return nil, nil
	}
	protoKeys := new(types.ProtoKeys)
	if err := proto.Unmarshal(data, protoKeys); err != nil {
		return nil, err
	}
	return protoKeys.Keys, nil
}

func DeleteCreatedCoinbaseLockupKeys(db ethdb.KeyValueWriter, blockHash common.Hash) {
	if err := db.Delete(createdCoinbaseLockupsKey(blockHash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete created coinbase lockup keys")
	}
}

type DeletedCoinbaseLockup struct {
	Key   []byte
	Value []byte
}

func WriteDeletedCoinbaseLockups(db ethdb.KeyValueWriter, blockHash common.Hash, deletedLockups []DeletedCoinbaseLockup) error {
	protoKeysAndValues := &types.ProtoKeysAndValues{KeysAndValues: make([]*types.ProtoKeyValue, 0, len(deletedLockups))}
	for _, lockup := range deletedLockups {
		protoKeysAndValues.KeysAndValues = append(protoKeysAndValues.KeysAndValues, &types.ProtoKeyValue{
			Key:   lockup.Key,
			Value: lockup.Value,
		})
	}
	data, err := proto.Marshal(protoKeysAndValues)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxo")
	}
	return db.Put(deletedCoinbaseLockupsKey(blockHash), data)
}

func ReadDeletedCoinbaseLockups(db ethdb.Reader, blockHash common.Hash) ([]*DeletedCoinbaseLockup, error) {
	// Try to look up the data in leveldb.
	data, _ := db.Get(deletedCoinbaseLockupsKey(blockHash))
	if len(data) == 0 {
		return nil, nil
	}
	protoKeysAndValues := new(types.ProtoKeysAndValues)
	if err := proto.Unmarshal(data, protoKeysAndValues); err != nil {
		return nil, err
	}
	deletedLockups := make([]*DeletedCoinbaseLockup, 0, len(protoKeysAndValues.KeysAndValues))
	for _, keyValue := range protoKeysAndValues.KeysAndValues {
		if len(keyValue.Key) != CoinbaseLockupKeyLength {
			return nil, fmt.Errorf("invalid key length %d", len(keyValue.Key))
		}
		deletedLockups = append(deletedLockups, &DeletedCoinbaseLockup{Key: keyValue.Key, Value: keyValue.Value})
	}
	return deletedLockups, nil
}

func DeleteDeletedCoinbaseLockups(db ethdb.KeyValueWriter, blockHash common.Hash) {
	if err := db.Delete(deletedCoinbaseLockupsKey(blockHash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete deleted coinbase lockups")
	}
}

func ReadUTXOSetSize(db ethdb.Reader, blockHash common.Hash) uint64 {
	data, _ := db.Get(utxoSetSizeKey(blockHash))
	if len(data) == 0 {
		return 0
	}
	if len(data) != 8 {
		db.Logger().WithField("data", data).Fatal("Invalid utxo set size data")
	}
	return binary.BigEndian.Uint64(data)
}

func WriteUTXOSetSize(db ethdb.KeyValueWriter, blockHash common.Hash, size uint64) {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, size)
	if err := db.Put(utxoSetSizeKey(blockHash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store utxo set size")
	}
}

func DeleteUTXOSetSize(db ethdb.KeyValueWriter, blockHash common.Hash) {
	if err := db.Delete(utxoSetSizeKey(blockHash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete utxo set size")
	}
}

func ReadLastTrimmedBlock(db ethdb.Reader, blockHash common.Hash) uint64 {
	data, _ := db.Get(lastTrimmedBlockKey(blockHash))
	if len(data) == 0 {
		return 0
	}
	if len(data) != 8 {
		db.Logger().WithField("data", data).Fatal("Invalid last trimmed block data")
	}
	return binary.BigEndian.Uint64(data)
}

func WriteLastTrimmedBlock(db ethdb.KeyValueWriter, blockHash common.Hash, blockHeight uint64) {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, blockHeight)
	if err := db.Put(lastTrimmedBlockKey(blockHash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store last trimmed block")
	}
}

func WritePrunedUTXOKeys(db ethdb.KeyValueWriter, blockHeight uint64, keys [][]byte) error {
	protoKeys := &types.ProtoKeys{Keys: make([][]byte, 0, len(keys))}
	protoKeys.Keys = append(protoKeys.Keys, keys...)

	data, err := proto.Marshal(protoKeys)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxo")
	}
	return db.Put(prunedUTXOsKey(blockHeight), data)
}

func ReadPrunedUTXOKeys(db ethdb.Reader, blockHeight uint64) ([][]byte, error) {
	// Try to look up the data in leveldb.
	data, _ := db.Get(prunedUTXOsKey(blockHeight))
	if len(data) == 0 {
		return nil, nil
	}
	protoKeys := new(types.ProtoKeys)
	if err := proto.Unmarshal(data, protoKeys); err != nil {
		return nil, err
	}
	return protoKeys.Keys, nil
}

func ReadTrimmedUTXOs(db ethdb.Reader, blockHash common.Hash) ([]*types.SpentUtxoEntry, error) {
	// Try to look up the data in leveldb.
	data, _ := db.Get(trimmedUTXOsKey(blockHash))
	if len(data) == 0 {
		return nil, nil
	}

	protoSpentUTXOs := new(types.ProtoSpentUTXOs)
	if err := proto.Unmarshal(data, protoSpentUTXOs); err != nil {
		return nil, err
	}

	spentUTXOs := make([]*types.SpentUtxoEntry, 0, len(protoSpentUTXOs.Sutxos))
	for _, utxoProto := range protoSpentUTXOs.Sutxos {
		utxo := new(types.SpentUtxoEntry)
		if err := utxo.ProtoDecode(utxoProto); err != nil {
			return nil, err
		}
		spentUTXOs = append(spentUTXOs, utxo)
	}
	return spentUTXOs, nil
}

func WriteTrimmedUTXOs(db ethdb.KeyValueWriter, blockHash common.Hash, spentUTXOs []*types.SpentUtxoEntry) error {
	protoSpentUTXOs := &types.ProtoSpentUTXOs{}
	for _, utxo := range spentUTXOs {
		utxoProto, err := utxo.ProtoEncode()
		if err != nil {
			return err
		}
		protoSpentUTXOs.Sutxos = append(protoSpentUTXOs.Sutxos, utxoProto)
	}
	data, err := proto.Marshal(protoSpentUTXOs)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to rlp encode utxo")
	}

	// And finally, store the data in the database under the appropriate key
	return db.Put(trimmedUTXOsKey(blockHash), data)
}

func DeleteTrimmedUTXOs(db ethdb.KeyValueWriter, blockHash common.Hash) {
	if err := db.Delete(trimmedUTXOsKey(blockHash)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete trimmed utxos")
	}
}

func ReadAlreadyPruned(db ethdb.Reader, blockHash common.Hash) bool {
	data, _ := db.Get(alreadyPrunedKey(blockHash))
	return len(data) > 0
}

func WriteAlreadyPruned(db ethdb.KeyValueWriter, blockHash common.Hash) {
	if err := db.Put(alreadyPrunedKey(blockHash), []byte{1}); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store already pruned")
	}
}

// ReadUtxoToBlockHeight reads the block height at which a UTXO was created
// This is not meant to be used in consensus. It is only used in the UTXO indexer or RPC API.
func ReadUtxoToBlockHeight(db ethdb.Reader, txHash common.Hash, index uint16) uint32 {
	data, _ := db.Get(utxoToBlockHeightKey(txHash, index))
	if len(data) == 0 {
		return 0
	}
	if len(data) != 4 {
		db.Logger().WithField("data", data).Fatal("Invalid utxo to block height data")
	}
	return binary.BigEndian.Uint32(data)
}

func WriteUtxoToBlockHeight(db ethdb.KeyValueWriter, txHash common.Hash, index uint16, blockHeight uint32) {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, blockHeight)
	if err := db.Put(utxoToBlockHeightKey(txHash, index), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store utxo to block height")
	}
}

func DeleteUtxoToBlockHeight(db ethdb.KeyValueWriter, txHash common.Hash, index uint16) {
	if err := db.Delete(utxoToBlockHeightKey(txHash, index)); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete utxo to block height")
	}
}

func ReadCoinbaseLockup(db ethdb.KeyValueReader, batch ethdb.Batch, ownerContract common.Address, beneficiaryMiner common.Address, lockupByte byte, epoch uint32) (*big.Int, uint32, uint16, common.Address) {
	deleted, data := batch.GetPending(CoinbaseLockupKey(ownerContract, beneficiaryMiner, lockupByte, epoch))
	if deleted {
		return new(big.Int), 0, 0, common.Zero
	} else if data != nil {
		amount := new(big.Int).SetBytes(data[:32])
		blockHeight := binary.BigEndian.Uint32(data[32:36])
		elements := binary.BigEndian.Uint16(data[36:38])
		var delegate common.Address
		if len(data) == 58 {
			delegate = common.BytesToAddress(data[38:], db.Location())
		} else {
			delegate = common.Zero
		}
		return amount, blockHeight, elements, delegate
	}
	// If the data is not in the batch, try to look up the data in leveldb
	data, _ = db.Get(CoinbaseLockupKey(ownerContract, beneficiaryMiner, lockupByte, epoch))
	if len(data) == 0 {
		return new(big.Int), 0, 0, common.Zero
	}
	amount := new(big.Int).SetBytes(data[:32])
	blockHeight := binary.BigEndian.Uint32(data[32:36])
	elements := binary.BigEndian.Uint16(data[36:38])
	var delegate common.Address
	if len(data) == 58 {
		delegate = common.BytesToAddress(data[38:], db.Location())
	} else {
		delegate = common.Zero
	}
	return amount, blockHeight, elements, delegate
}

func WriteCoinbaseLockup(db ethdb.KeyValueWriter, ownerContract common.Address, beneficiaryMiner common.Address, lockupByte byte, epoch uint32, amount *big.Int, blockHeight uint32, elements uint16, delegate common.Address) ([]byte, error) {
	data := make([]byte, 38)
	amountBytes := amount.Bytes()
	if len(amountBytes) > 32 {
		return nil, fmt.Errorf("amount is too large")
	}
	// Right-align amountBytes in data[:32]
	copy(data[32-len(amountBytes):32], amountBytes)
	binary.BigEndian.PutUint32(data[32:36], blockHeight)
	binary.BigEndian.PutUint16(data[36:38], elements)
	if !delegate.Equal(common.Zero) {
		data = append(data, delegate.Bytes()...)
	}
	key := CoinbaseLockupKey(ownerContract, beneficiaryMiner, lockupByte, epoch)
	if err := db.Put(key, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store coinbase lockup")
	}
	return key, nil
}

func WriteCoinbaseLockupToMap(coinbaseMap map[[CoinbaseLockupKeyLength]byte][]byte, key [CoinbaseLockupKeyLength]byte, amount *big.Int, blockHeight uint32, elements uint16, delegate common.Address) error {
	data := make([]byte, 38)
	amountBytes := amount.Bytes()
	if len(amountBytes) > 32 {
		return fmt.Errorf("amount is too large")
	}
	// Right-align amountBytes in data[:32]
	copy(data[32-len(amountBytes):32], amountBytes)
	binary.BigEndian.PutUint32(data[32:36], blockHeight)
	binary.BigEndian.PutUint16(data[36:38], elements)
	if !delegate.Equal(common.Zero) {
		data = append(data, delegate.Bytes()...)
	}
	coinbaseMap[[CoinbaseLockupKeyLength]byte(key)] = data
	return nil
}

func WriteCoinbaseLockupToSlice(amount *big.Int, blockHeight uint32, elements uint16, delegate common.Address) ([]byte, error) {
	data := make([]byte, 38)
	amountBytes := amount.Bytes()
	if len(amountBytes) > 32 {
		return nil, fmt.Errorf("amount is too large")
	}
	// Right-align amountBytes in data[:32]
	copy(data[32-len(amountBytes):32], amountBytes)
	binary.BigEndian.PutUint32(data[32:36], blockHeight)
	binary.BigEndian.PutUint16(data[36:38], elements)
	if !delegate.Equal(common.Zero) {
		data = append(data, delegate.Bytes()...)
	}
	return data, nil
}

func DeleteCoinbaseLockup(db ethdb.KeyValueWriter, ownerContract common.Address, beneficiaryMiner common.Address, lockupByte byte, epoch uint32) [CoinbaseLockupKeyLength]byte {
	key := CoinbaseLockupKey(ownerContract, beneficiaryMiner, lockupByte, epoch)
	if err := db.Delete(key); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to delete coinbase lockup")
	}
	if len(key) != 47 {
		db.Logger().Fatal("CoinbaseLockupKey is not 47 bytes")
	}
	return [CoinbaseLockupKeyLength]byte(key)
}

func WriteSupplyAnalyticsForBlock(db ethdb.KeyValueWriter, readDb ethdb.Reader, blockHash common.Hash, parentHash common.Hash, supplyAddedQuai, supplyRemovedQuai, supplyAddedQi, supplyRemovedQi *big.Int) error {
	supplyDeltaQuai := new(big.Int).Sub(supplyAddedQuai, supplyRemovedQuai)
	supplyDeltaQi := new(big.Int).Sub(supplyAddedQi, supplyRemovedQi)

	_, _, totalSupplyQuai, _, _, totalSupplyQi, err := ReadSupplyAnalyticsForBlock(readDb, parentHash)
	if err != nil {
		db.Logger().WithField("err", err).Error("Failed to read total supply")
		return err
	}

	totalSupplyQuai.Add(totalSupplyQuai, supplyDeltaQuai)

	totalSupplyQi.Add(totalSupplyQi, supplyDeltaQi)

	protoSupplyAnalytics := &types.ProtoSupplyAnalytics{
		SupplyAddedQuai:   supplyAddedQuai.Bytes(),
		SupplyRemovedQuai: supplyRemovedQuai.Bytes(),
		SupplyAddedQi:     supplyAddedQi.Bytes(),
		SupplyRemovedQi:   supplyRemovedQi.Bytes(),
		TotalSupplyQuai:   totalSupplyQuai.Bytes(),
		TotalSupplyQi:     totalSupplyQi.Bytes(),
	}
	data, err := proto.Marshal(protoSupplyAnalytics)
	if err != nil {
		return err
	}
	return db.Put(supplyAnalyticsKey(blockHash), data)
}

func ReadSupplyAnalyticsForBlock(db ethdb.Reader, blockHash common.Hash) (*big.Int, *big.Int, *big.Int, *big.Int, *big.Int, *big.Int, error) {
	data, _ := db.Get(supplyAnalyticsKey(blockHash))
	if len(data) == 0 {
		return big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), nil
	}
	protoSupplyAnalytics := new(types.ProtoSupplyAnalytics)
	if err := proto.Unmarshal(data, protoSupplyAnalytics); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	supplyAddedQuai := new(big.Int).SetBytes(protoSupplyAnalytics.SupplyAddedQuai)
	supplyRemovedQuai := new(big.Int).SetBytes(protoSupplyAnalytics.SupplyRemovedQuai)
	totalSupplyQuai := new(big.Int).SetBytes(protoSupplyAnalytics.TotalSupplyQuai)
	supplyAddedQi := new(big.Int).SetBytes(protoSupplyAnalytics.SupplyAddedQi)
	supplyRemovedQi := new(big.Int).SetBytes(protoSupplyAnalytics.SupplyRemovedQi)
	totalSupplyQi := new(big.Int).SetBytes(protoSupplyAnalytics.TotalSupplyQi)
	return supplyAddedQuai, supplyRemovedQuai, totalSupplyQuai, supplyAddedQi, supplyRemovedQi, totalSupplyQi, nil
}

func WriteNewLockups(db ethdb.KeyValueWriter, readDb ethdb.Reader, blockHash common.Hash, newLocks map[common.InternalAddress]*big.Int, newUnlocks []common.Unlock) {

	deltas := make(map[common.InternalAddress]*big.Int)
	for addr, amount := range newLocks {
		if _, ok := deltas[addr]; !ok {
			deltas[addr] = new(big.Int).Set(amount)
		} else {
			// Shouldn't be possible
			db.Logger().Errorf("Address %s has multiple lockups\n", addr)
			deltas[addr].Add(deltas[addr], amount)
		}
	}
	for _, unlock := range newUnlocks {
		if _, ok := deltas[unlock.Addr]; !ok {
			deltas[unlock.Addr] = new(big.Int).Neg(unlock.Amt)
		} else {
			deltas[unlock.Addr].Sub(deltas[unlock.Addr], unlock.Amt)
		}
	}
	protoDeltas := &types.ProtoKeysAndValues{KeysAndValues: make([]*types.ProtoKeyValue, 0, len(deltas))}
	for addr, delta := range deltas {
		gobDelta, _ := delta.GobEncode()
		protoDeltas.KeysAndValues = append(protoDeltas.KeysAndValues, &types.ProtoKeyValue{
			Key:   addr.Bytes(),
			Value: gobDelta,
		})
		data, _ := readDb.Get(addressLockupsKey(addr))
		if len(data) > 32 {
			db.Logger().Errorf("Address %s has invalid lockup data %s\n", addr, data)
			continue
		}
		value := new(big.Int).SetBytes(data)
		value.Add(value, delta)
		if value.Sign() < 0 {
			db.Logger().Errorf("Address %s has negative lockup %s delta %s\n", addr, value, delta.String())
			if err := db.Put(addressLockupsKey(addr), []byte{0}); err != nil {
				db.Logger().WithField("err", err).Error("Failed to store new lockups 3")
			}
		} else {
			if err := db.Put(addressLockupsKey(addr), value.Bytes()); err != nil {
				db.Logger().WithField("err", err).Error("Failed to store new lockups 4")
			}
		}
	}
	data, err := proto.Marshal(protoDeltas)
	if err != nil {
		db.Logger().WithField("err", err).Error("Failed to store new lockups")
		return
	}
	if err := db.Put(lockupDeltasKey(blockHash), data); err != nil {
		db.Logger().WithField("err", err).Error("Failed to store new lockups")
	}
}

func UndoNewLockupsForBlock(db ethdb.KeyValueWriter, readDb ethdb.Reader, blockHash common.Hash) {
	data, _ := readDb.Get(lockupDeltasKey(blockHash))
	if len(data) == 0 {
		return
	}
	protoDeltas := new(types.ProtoKeysAndValues)
	if err := proto.Unmarshal(data, protoDeltas); err != nil {
		db.Logger().WithField("err", err).Error("Failed to unmarshal lockup deltas")
		return
	}
	for _, delta := range protoDeltas.KeysAndValues {
		if len(delta.Key) != common.AddressLength {
			db.Logger().Errorf("Invalid address length %d\n", len(delta.Key))
			continue
		}
		addr := common.InternalAddress(delta.Key)
		amount := new(big.Int)
		if err := amount.GobDecode(delta.Value); err != nil {
			db.Logger().WithField("err", err).Error("Failed to decode lockup value")
			continue
		}
		amount.Neg(amount)
		data, _ := readDb.Get(addressLockupsKey(addr))
		if len(data) > 32 {
			db.Logger().Errorf("Address %s has invalid lockup data %s\n", addr, data)
			continue
		}
		value := new(big.Int).SetBytes(data)
		value.Add(value, amount)
		if value.Sign() < 0 {
			db.Logger().Errorf("Address %s has negative lockup in reorg %s amount %s\n", addr, value.String(), amount.String())
			if err := db.Put(addressLockupsKey(addr), []byte{0}); err != nil {
				db.Logger().WithField("err", err).Error("Failed to store new lockups")
			}
		} else {
			if err := db.Put(addressLockupsKey(addr), value.Bytes()); err != nil {
				db.Logger().WithField("err", err).Error("Failed to store new lockups")
			}
		}
	}
}

func ReadLockedBalance(db ethdb.Reader, addr common.InternalAddress) *big.Int {
	data, _ := db.Get(addressLockupsKey(addr))
	if len(data) == 0 {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(data)
}
