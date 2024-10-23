// Copyright 2015 The go-ethereum Authors
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

package quai

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/rpc"
	"github.com/dominant-strategies/go-quai/trie"
)

// PublicQuaiAPI provides an API to access Quai full node-related
// information.
type PublicQuaiAPI struct {
	e *Quai
}

// NewPublicQuaiAPI creates a new Quai protocol API for full nodes.
func NewPublicQuaiAPI(e *Quai) *PublicQuaiAPI {
	return &PublicQuaiAPI{e}
}

// PrimaryCoinbase is the address that mining rewards will be send to
func (api *PublicQuaiAPI) PrimaryCoinbase() (common.Address, error) {
	return api.e.core.Slice().GetPrimaryCoinbase(), nil
}

// PublicMinerAPI provides an API to control the miner.
// It offers only methods that operate on data that pose no security risk when it is publicly accessible.
type PublicMinerAPI struct {
	e *Quai
}

// NewPublicMinerAPI create a new PublicMinerAPI instance.
func NewPublicMinerAPI(e *Quai) *PublicMinerAPI {
	return &PublicMinerAPI{e}
}

// Mining returns an indication if this node is currently mining.
func (api *PublicMinerAPI) Mining() bool {
	return api.e.core.IsMining()
}

// PrivateMinerAPI provides private RPC methods to control the miner.
// These methods can be abused by external users and must be considered insecure for use by untrusted users.
type PrivateMinerAPI struct {
	e *Quai
}

// NewPrivateMinerAPI create a new RPC service which controls the miner of this node.
func NewPrivateMinerAPI(e *Quai) *PrivateMinerAPI {
	return &PrivateMinerAPI{e: e}
}

// SetExtra sets the extra data string that is included when this miner mines a block.
func (api *PrivateMinerAPI) SetExtra(extra string) (bool, error) {
	if err := api.e.Core().SetExtra([]byte(extra)); err != nil {
		return false, err
	}
	return true, nil
}

// SetGasPrice sets the minimum accepted gas price for the miner.
func (api *PrivateMinerAPI) SetGasPrice(gasPrice hexutil.Big) bool {
	api.e.lock.Lock()
	api.e.gasPrice = (*big.Int)(&gasPrice)
	api.e.lock.Unlock()

	api.e.Core().SetGasPrice((*big.Int)(&gasPrice))
	return true
}

// SetGasLimit sets the gaslimit to target towards during mining.
func (api *PrivateMinerAPI) SetGasLimit(gasLimit hexutil.Uint64) bool {
	api.e.Core().SetGasCeil(uint64(gasLimit))
	return true
}

// SetPrimaryCoinbase sets the primary coinbase of the miner
func (api *PrivateMinerAPI) SetPrimaryCoinbase(primaryCoinbase common.Address) bool {
	api.e.Core().SetPrimaryCoinbase(primaryCoinbase)
	return true
}

// SetSecondaryCoinbase sets the secondary coinbase of the miner
func (api *PrivateMinerAPI) SetSecondaryCoinbase(secondaryCoinbase common.Address) bool {
	api.e.Core().SetSecondaryCoinbase(secondaryCoinbase)
	return true
}

func (api *PrivateMinerAPI) SetLockupByte(lockupByte hexutil.Uint64) (bool, error) {
	if uint8(lockupByte) > uint8(len(params.LockupByteToBlockDepth)-1) {
		return false, fmt.Errorf("lockup byte %d out of range", lockupByte)
	}
	api.e.Core().SetLockupByte(uint8(lockupByte))
	return true, nil
}

func (api *PrivateMinerAPI) SetMinerPreference(minerPreference string) (bool, error) {
	preferenceFloat, err := strconv.ParseFloat(minerPreference, 64)
	if err != nil {
		return false, err
	}
	if preferenceFloat < 0 || preferenceFloat > 1 {
		return false, errors.New("miner preference must be between 0 and 1")
	}
	api.e.Core().SetMinerPreference(preferenceFloat)
	return true, nil
}

// SetRecommitInterval updates the interval for miner sealing work recommitting.
func (api *PrivateMinerAPI) SetRecommitInterval(interval int) {
	api.e.Core().SetRecommitInterval(time.Duration(interval) * time.Millisecond)
}

// PrivateAdminAPI is the collection of Quai full node-related APIs
// exposed over the private admin endpoint.
type PrivateAdminAPI struct {
	quai *Quai
}

// NewPrivateAdminAPI creates a new API definition for the full node private
// admin methods of the Quai service.
func NewPrivateAdminAPI(quai *Quai) *PrivateAdminAPI {
	return &PrivateAdminAPI{quai: quai}
}

// ExportChain exports the current blockchain into a local file,
// or a range of blocks if first and last are non-nil
func (api *PrivateAdminAPI) ExportChain(file string, first *uint64, last *uint64) (bool, error) {
	if first == nil && last != nil {
		return false, errors.New("last cannot be specified without first")
	}
	if first != nil && last == nil {
		head := api.quai.Core().CurrentHeader().NumberU64(api.quai.core.NodeCtx())
		last = &head
	}
	if _, err := os.Stat(file); err == nil {
		// File already exists. Allowing overwrite could be a DoS vecotor,
		// since the 'file' may point to arbitrary paths on the drive
		return false, errors.New("location would overwrite an existing file")
	}
	// Make sure we can create the file to export into
	out, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.ModePerm)
	if err != nil {
		return false, err
	}
	defer out.Close()

	var writer io.Writer = out
	if strings.HasSuffix(file, ".gz") {
		writer = gzip.NewWriter(writer)
		defer writer.(*gzip.Writer).Close()
	}

	// Export the blockchain
	if first != nil {
		if err := api.quai.Core().ExportN(writer, *first, *last); err != nil {
			return false, err
		}
	} else if err := api.quai.Core().Export(writer); err != nil {
		return false, err
	}
	return true, nil
}

func hasAllBlocks(chain *core.Core, bs []*types.WorkObject) bool {
	for _, b := range bs {
		if !chain.HasBlock(b.Hash(), b.NumberU64(chain.NodeCtx())) {
			return false
		}
	}
	return true
}

// ImportChain imports a blockchain from a local file.
func (api *PrivateAdminAPI) ImportChain(file string) (bool, error) {
	// Make sure the can access the file to import
	in, err := os.Open(file)
	if err != nil {
		return false, err
	}
	defer in.Close()

	var reader io.Reader = in
	if strings.HasSuffix(file, ".gz") {
		if reader, err = gzip.NewReader(reader); err != nil {
			return false, err
		}
	}

	// Run actual the import in pre-configured batches
	stream := rlp.NewStream(reader, 0)

	blocks, index := make([]*types.WorkObject, 0, 2500), 0
	for batch := 0; ; batch++ {
		// Load a batch of blocks from the input file
		for len(blocks) < cap(blocks) {
			block := new(types.WorkObject)
			if err := stream.Decode(block); err == io.EOF {
				break
			} else if err != nil {
				return false, fmt.Errorf("block %d: failed to parse: %v", index, err)
			}
			blocks = append(blocks, block)
			index++
		}
		if len(blocks) == 0 {
			break
		}

		if hasAllBlocks(api.quai.Core(), blocks) {
			blocks = blocks[:0]
			continue
		}
		// Import the batch and reset the buffer
		if _, err := api.quai.Core().InsertChain(blocks); err != nil {
			return false, fmt.Errorf("batch %d: failed to insert: %v", batch, err)
		}
		blocks = blocks[:0]
	}
	return true, nil
}

// PublicDebugAPI is the collection of Quai full node APIs exposed
// over the public debugging endpoint.
type PublicDebugAPI struct {
	quai *Quai
}

// NewPublicDebugAPI creates a new API definition for the full node-
// related public debug methods of the Quai service.
func NewPublicDebugAPI(quai *Quai) *PublicDebugAPI {
	return &PublicDebugAPI{quai: quai}
}

// DumpBlock retrieves the entire state of the database at a given block.
func (api *PublicDebugAPI) DumpBlock(blockNr rpc.BlockNumber) (state.Dump, error) {
	opts := &state.DumpConfig{
		OnlyWithAddresses: true,
		Max:               AccountRangeMaxResults, // Sanity limit over RPC
	}
	if blockNr == rpc.PendingBlockNumber {
		return state.Dump{}, nil
	}
	var block *types.WorkObject
	if blockNr == rpc.LatestBlockNumber {
		block = api.quai.core.CurrentBlock()
	} else {
		block = api.quai.core.GetBlockByNumber(uint64(blockNr))
	}
	if block == nil {
		return state.Dump{}, fmt.Errorf("block #%d not found", blockNr)
	}
	stateDb, err := api.quai.core.StateAt(block.EVMRoot(), block.EtxSetRoot(), block.QuaiStateSize())
	if err != nil {
		return state.Dump{}, err
	}
	return stateDb.RawDump(opts), nil
}

// PrivateDebugAPI is the collection of Quai full node APIs exposed over
// the private debugging endpoint.
type PrivateDebugAPI struct {
	quai *Quai
}

// NewPrivateDebugAPI creates a new API definition for the full node-related
// private debug methods of the Quai service.
func NewPrivateDebugAPI(quai *Quai) *PrivateDebugAPI {
	return &PrivateDebugAPI{quai: quai}
}

// Preimage is a debug API function that returns the preimage for a sha3 hash, if known.
func (api *PrivateDebugAPI) Preimage(ctx context.Context, hash common.Hash) (hexutil.Bytes, error) {
	if preimage := rawdb.ReadPreimage(api.quai.ChainDb(), hash); preimage != nil {
		return preimage, nil
	}
	return nil, errors.New("unknown preimage")
}

// AccountRangeMaxResults is the maximum number of results to be returned per call
const AccountRangeMaxResults = 256

// AccountRange enumerates all accounts in the given block and start point in paging request
func (api *PublicDebugAPI) AccountRange(blockNrOrHash rpc.BlockNumberOrHash, start []byte, maxResults int, nocode, nostorage, incompletes bool) (state.IteratorDump, error) {
	var stateDb *state.StateDB
	var err error

	if number, ok := blockNrOrHash.Number(); ok {
		if number == rpc.PendingBlockNumber {
			// If we're dumping the pending state, we need to request
			// both the pending block as well as the pending state from
			// the miner and operate on those
			stateDb = &state.StateDB{}
		} else {
			var block *types.WorkObject
			if number == rpc.LatestBlockNumber {
				block = api.quai.core.CurrentBlock()
			} else {
				block = api.quai.core.GetBlockByNumber(uint64(number))
			}
			if block == nil {
				return state.IteratorDump{}, fmt.Errorf("block #%d not found", number)
			}
			stateDb, err = api.quai.core.StateAt(block.EVMRoot(), block.EtxSetRoot(), block.QuaiStateSize())
			if err != nil {
				return state.IteratorDump{}, err
			}
		}
	} else if hash, ok := blockNrOrHash.Hash(); ok {
		block := api.quai.core.GetBlockByHash(hash)
		if block == nil {
			return state.IteratorDump{}, fmt.Errorf("block %s not found", hash.Hex())
		}
		stateDb, err = api.quai.core.StateAt(block.EVMRoot(), block.EtxSetRoot(), block.QuaiStateSize())
		if err != nil {
			return state.IteratorDump{}, err
		}
	} else {
		return state.IteratorDump{}, errors.New("either block number or block hash must be specified")
	}

	opts := &state.DumpConfig{
		SkipCode:          nocode,
		SkipStorage:       nostorage,
		OnlyWithAddresses: !incompletes,
		Start:             start,
		Max:               uint64(maxResults),
	}
	if maxResults > AccountRangeMaxResults || maxResults <= 0 {
		opts.Max = AccountRangeMaxResults
	}
	return stateDb.IteratorDump(opts), nil
}

// StorageRangeResult is the result of a debug_storageRangeAt API call.
type StorageRangeResult struct {
	Storage storageMap   `json:"storage"`
	NextKey *common.Hash `json:"nextKey"` // nil if Storage includes the last key in the trie.
}

type storageMap map[common.Hash]storageEntry

type storageEntry struct {
	Key   *common.Hash `json:"key"`
	Value common.Hash  `json:"value"`
}

// StorageRangeAt returns the storage at the given block height and transaction index.
func (api *PrivateDebugAPI) StorageRangeAt(blockHash common.Hash, txIndex int, contractAddress common.Address, keyStart hexutil.Bytes, maxResult int) (StorageRangeResult, error) {
	// Retrieve the block
	block := api.quai.core.GetBlockByHash(blockHash)
	if block == nil {
		return StorageRangeResult{}, fmt.Errorf("block %#x not found", blockHash)
	}
	_, _, statedb, err := api.quai.core.StateAtTransaction(block, txIndex, 0)
	if err != nil {
		return StorageRangeResult{}, err
	}
	internal, err := contractAddress.InternalAndQuaiAddress()
	if err != nil {
		return StorageRangeResult{}, err
	}
	st := statedb.StorageTrie(internal)
	if st == nil {
		return StorageRangeResult{}, fmt.Errorf("account %x doesn't exist", contractAddress)
	}
	return storageRangeAt(st, keyStart, maxResult)
}

func storageRangeAt(st state.Trie, start []byte, maxResult int) (StorageRangeResult, error) {
	it := trie.NewIterator(st.NodeIterator(start))
	result := StorageRangeResult{Storage: storageMap{}}
	for i := 0; i < maxResult && it.Next(); i++ {
		_, content, _, err := rlp.Split(it.Value)
		if err != nil {
			return StorageRangeResult{}, err
		}
		e := storageEntry{Value: common.BytesToHash(content)}
		if preimage := st.GetKey(it.Key); preimage != nil {
			preimage := common.BytesToHash(preimage)
			e.Key = &preimage
		}
		result.Storage[common.BytesToHash(it.Key)] = e
	}
	// Add the 'next key' so clients can continue downloading.
	if it.Next() {
		next := common.BytesToHash(it.Key)
		result.NextKey = &next
	}
	return result, nil
}

// GetModifiedAccountsByNumber returns all accounts that have changed between the
// two blocks specified. A change is defined as a difference in nonce, balance,
// code hash, or storage hash.
//
// With one parameter, returns the list of accounts modified in the specified block.
func (api *PrivateDebugAPI) GetModifiedAccountsByNumber(startNum uint64, endNum *uint64) ([]common.Address, error) {
	var startBlock, endBlock *types.WorkObject

	startBlock = api.quai.core.GetBlockByNumber(startNum)
	if startBlock == nil {
		return nil, fmt.Errorf("start block %x not found", startNum)
	}

	nodeCtx := api.quai.core.NodeCtx()
	if endNum == nil {
		endBlock = startBlock
		startBlock = api.quai.core.GetBlockByHash(startBlock.ParentHash(nodeCtx))
		if startBlock == nil {
			return nil, fmt.Errorf("block %x has no parent", endBlock.Number(nodeCtx))
		}
	} else {
		endBlock = api.quai.core.GetBlockByNumber(*endNum)
		if endBlock == nil {
			return nil, fmt.Errorf("end block %d not found", *endNum)
		}
	}
	return api.getModifiedAccounts(startBlock, endBlock)
}

// GetModifiedAccountsByHash returns all accounts that have changed between the
// two blocks specified. A change is defined as a difference in nonce, balance,
// code hash, or storage hash.
//
// With one parameter, returns the list of accounts modified in the specified block.
func (api *PrivateDebugAPI) GetModifiedAccountsByHash(startHash common.Hash, endHash *common.Hash) ([]common.Address, error) {
	var startBlock, endBlock *types.WorkObject
	startBlock = api.quai.core.GetBlockByHash(startHash)
	if startBlock == nil {
		return nil, fmt.Errorf("start block %x not found", startHash)
	}

	nodeCtx := api.quai.core.NodeCtx()
	if endHash == nil {
		endBlock = startBlock
		startBlock = api.quai.core.GetBlockByHash(startBlock.ParentHash(nodeCtx))
		if startBlock == nil {
			return nil, fmt.Errorf("block %x has no parent", endBlock.Number(nodeCtx))
		}
	} else {
		endBlock = api.quai.core.GetBlockByHash(*endHash)
		if endBlock == nil {
			return nil, fmt.Errorf("end block %x not found", *endHash)
		}
	}
	return api.getModifiedAccounts(startBlock, endBlock)
}

func (api *PrivateDebugAPI) getModifiedAccounts(startBlock, endBlock *types.WorkObject) ([]common.Address, error) {
	nodeLocation := api.quai.core.NodeLocation()
	nodeCtx := api.quai.core.NodeCtx()
	if startBlock.NumberU64(nodeCtx) >= endBlock.NumberU64(nodeCtx) {
		return nil, fmt.Errorf("start block height (%d) must be less than end block height (%d)", startBlock.NumberU64(nodeCtx), endBlock.NumberU64(nodeCtx))
	}
	triedb := api.quai.Core().StateCache().TrieDB()

	oldTrie, err := trie.NewSecure(startBlock.EVMRoot(), triedb)
	if err != nil {
		return nil, err
	}
	newTrie, err := trie.NewSecure(endBlock.EVMRoot(), triedb)
	if err != nil {
		return nil, err
	}
	diff, _ := trie.NewDifferenceIterator(oldTrie.NodeIterator([]byte{}), newTrie.NodeIterator([]byte{}))
	iter := trie.NewIterator(diff)

	var dirty []common.Address
	for iter.Next() {
		key := newTrie.GetKey(iter.Key)
		if key == nil {
			return nil, fmt.Errorf("no preimage found for hash %x", iter.Key)
		}
		dirty = append(dirty, common.BytesToAddress(key, nodeLocation))
	}
	return dirty, nil
}
