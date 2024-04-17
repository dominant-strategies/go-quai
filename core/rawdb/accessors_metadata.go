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
	"encoding/json"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"google.golang.org/protobuf/proto"
)

// ReadDatabaseVersion retrieves the version number of the database.
func ReadDatabaseVersion(db ethdb.KeyValueReader) *uint64 {
	enc, _ := db.Get(databaseVersionKey)
	if len(enc) == 0 {
		return nil
	}
	protoNumber := &ProtoNumber{}
	err := proto.Unmarshal(enc, protoNumber)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to decode database version")
	}
	return &protoNumber.Number
}

// WriteDatabaseVersion stores the version number of the database
func WriteDatabaseVersion(db ethdb.KeyValueWriter, version uint64) {
	protoNumber := &ProtoNumber{Number: version}
	data, err := proto.Marshal(protoNumber)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to encode database version")
	}
	if err = db.Put(databaseVersionKey, data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store the database version")
	}
}

// ReadChainConfig retrieves the consensus settings based on the given genesis hash.
func ReadChainConfig(db ethdb.KeyValueReader, hash common.Hash) *params.ChainConfig {
	data, _ := db.Get(configKey(hash))
	if len(data) == 0 {
		return nil
	}
	var config params.ChainConfig
	if err := json.Unmarshal(data, &config); err != nil {
		db.Logger().WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Error("Invalid chain config JSON")
		return nil
	}
	return &config
}

// WriteChainConfig writes the chain config settings to the database.
func WriteChainConfig(db ethdb.KeyValueWriter, hash common.Hash, cfg *params.ChainConfig) {
	if cfg == nil {
		return
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to JSON encode chain config")
	}
	if err := db.Put(configKey(hash), data); err != nil {
		db.Logger().WithField("err", err).Fatal("Failed to store chain config")
	}
}
