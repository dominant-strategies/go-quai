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

package quaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
)

// PublicQuaiAPI provides an API to access Quai related information.
// It offers only methods that operate on public data that is freely available to anyone.
type PublicQuaiAPI struct {
	b Backend
}

// NewPublicQuaiAPI creates a new Quai protocol API.
func NewPublicQuaiAPI(b Backend) *PublicQuaiAPI {
	return &PublicQuaiAPI{b}
}

// GasPrice returns a suggestion for a gas price for legacy transactions.
func (s *PublicQuaiAPI) GasPrice(ctx context.Context) (*hexutil.Big, error) {
	tipcap, err := s.b.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, err
	}
	if head := s.b.CurrentHeader(); head.BaseFee() != nil {
		tipcap.Add(tipcap, head.BaseFee())
	}
	return (*hexutil.Big)(tipcap), err
}

// MaxPriorityFeePerGas returns a suggestion for a gas tip cap for dynamic fee transactions.
func (s *PublicQuaiAPI) MaxPriorityFeePerGas(ctx context.Context) (*hexutil.Big, error) {
	tipcap, err := s.b.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, err
	}
	return (*hexutil.Big)(tipcap), err
}

func (s *PublicQuaiAPI) FeeHistory(ctx context.Context, blockCount rpc.DecimalOrHex, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*feeHistoryResult, error) {
	oldest, reward, baseFee, gasUsed, err := s.b.FeeHistory(ctx, int(blockCount), lastBlock, rewardPercentiles)
	if err != nil {
		return nil, err
	}
	results := &feeHistoryResult{
		OldestBlock:  (*hexutil.Big)(oldest),
		GasUsedRatio: gasUsed,
	}
	if reward != nil {
		results.Reward = make([][]*hexutil.Big, len(reward))
		for i, w := range reward {
			results.Reward[i] = make([]*hexutil.Big, len(w))
			for j, v := range w {
				results.Reward[i][j] = (*hexutil.Big)(v)
			}
		}
	}
	if baseFee != nil {
		results.BaseFee = make([]*hexutil.Big, len(baseFee))
		for i, v := range baseFee {
			results.BaseFee[i] = (*hexutil.Big)(v)
		}
	}
	return results, nil
}

// Syncing returns false in case the node is currently not syncing with the network. It can be up to date or has not
// yet received the latest block headers from its pears. In case it is synchronizing:
// - startingBlock: block number this node started to synchronise from
// - currentBlock:  block number this node is currently importing
// - highestBlock:  block number of the highest block header this node has received from peers
// - pulledStates:  number of state entries processed until now
// - knownStates:   number of known state entries that still need to be pulled
func (s *PublicQuaiAPI) Syncing() (interface{}, error) {
	progress := s.b.SyncProgress()

	// Return not syncing if the synchronisation already completed
	if progress.CurrentBlock >= progress.HighestBlock {
		return false, nil
	}
	// Otherwise gather the block sync stats
	return map[string]interface{}{
		"startingBlock": hexutil.Uint64(progress.StartingBlock),
		"currentBlock":  hexutil.Uint64(progress.CurrentBlock),
		"highestBlock":  hexutil.Uint64(progress.HighestBlock),
		"pulledStates":  hexutil.Uint64(progress.PulledStates),
		"knownStates":   hexutil.Uint64(progress.KnownStates),
	}, nil
}

// PublicBlockChainQuaiAPI provides an API to access the Quai blockchain.
// It offers only methods that operate on public data that is freely available to anyone.
type PublicBlockChainQuaiAPI struct {
	b Backend
}

// NewPublicBlockChainQuaiAPI creates a new Quai blockchain API.
func NewPublicBlockChainQuaiAPI(b Backend) *PublicBlockChainQuaiAPI {
	return &PublicBlockChainQuaiAPI{b}
}

// ChainId is the EIP-155 replay-protection chain id for the current Quai chain config.
func (api *PublicBlockChainQuaiAPI) ChainId() (*hexutil.Big, error) {
	// if current block is at or past the EIP-155 replay-protection fork block, return chainID from config
	if config := api.b.ChainConfig(); config.IsEIP155(api.b.CurrentBlock().Number()) {
		return (*hexutil.Big)(config.ChainID), nil
	}
	return nil, fmt.Errorf("chain not synced beyond EIP-155 replay-protection fork block")
}

// BlockNumber returns the block number of the chain head.
func (s *PublicBlockChainQuaiAPI) BlockNumber() hexutil.Uint64 {
	header, _ := s.b.HeaderByNumber(context.Background(), rpc.LatestBlockNumber) // latest header should always be available
	return hexutil.Uint64(header.NumberU64())

}

// GetBalance returns the amount of wei for the given address in the state of the
// given block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta
// block numbers are also allowed.
func (s *PublicBlockChainQuaiAPI) GetBalance(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	balance, err := state.GetBalance(address)
	if err != nil {
		return nil, err
	}
	return (*hexutil.Big)(balance), state.Error()
}

// GetProof returns the Merkle-proof for a given account and optionally some storage keys.
func (s *PublicBlockChainQuaiAPI) GetProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*AccountResult, error) {
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}

	storageTrie, err := state.StorageTrie(address)
	if err != nil {
		return nil, err
	}
	storageHash := types.EmptyRootHash
	codeHash, err := state.GetCodeHash(address)
	if err != nil {
		return nil, err
	}
	storageProof := make([]StorageResult, len(storageKeys))

	// if we have a storageTrie, (which means the account exists), we can update the storagehash
	if storageTrie != nil {
		storageHash = storageTrie.Hash()
	} else {
		// no storageTrie means the account does not exist, so the codeHash is the hash of an empty bytearray.
		codeHash = crypto.Keccak256Hash(nil)
	}

	// create the proof for the storageKeys
	for i, key := range storageKeys {
		if storageTrie != nil {
			proof, storageError := state.GetStorageProof(address, common.HexToHash(key))
			if storageError != nil {
				return nil, storageError
			}
			val, err := state.GetState(address, common.HexToHash(key))
			if err != nil {
				return nil, err
			}
			storageProof[i] = StorageResult{key, (*hexutil.Big)(val.Big()), toHexSlice(proof)}
		} else {
			storageProof[i] = StorageResult{key, &hexutil.Big{}, []string{}}
		}
	}

	// create the accountProof
	accountProof, proofErr := state.GetProof(address)
	if proofErr != nil {
		return nil, proofErr
	}

	balance, err := state.GetBalance(address)
	if err != nil {
		return nil, err
	}
	nonce, err := state.GetNonce(address)
	if err != nil {
		return nil, err
	}
	return &AccountResult{
		Address:      address,
		AccountProof: toHexSlice(accountProof),
		Balance:      (*hexutil.Big)(balance),
		CodeHash:     codeHash,
		Nonce:        hexutil.Uint64(nonce),
		StorageHash:  storageHash,
		StorageProof: storageProof,
	}, state.Error()
}

// GetHeaderByHash returns the requested header by hash.
func (s *PublicBlockChainQuaiAPI) GetHeaderHashByNumber(ctx context.Context, number rpc.BlockNumber) common.Hash {
	header, err := s.b.HeaderByNumber(ctx, number)
	if err != nil {
		return common.Hash{}
	}
	return header.Hash()
}

// GetHeaderByHash returns the requested header by hash.
func (s *PublicBlockChainQuaiAPI) GetHeaderByHash(ctx context.Context, hash common.Hash) map[string]interface{} {
	header, _ := s.b.HeaderByHash(ctx, hash)
	if header != nil {
		return s.rpcMarshalHeader(ctx, header)
	}
	return nil
}

// GetBlockByNumber returns the requested canonical block.
//   - When blockNr is -1 the chain head is returned.
//   - When blockNr is -2 the pending chain head is returned.
//   - When fullTx is true all transactions in the block are returned, otherwise
//     only the transaction hash is returned.
func (s *PublicBlockChainQuaiAPI) GetBlockByNumber(ctx context.Context, number rpc.BlockNumber, fullTx bool) (map[string]interface{}, error) {
	block, err := s.b.BlockByNumber(ctx, number)
	if block != nil && err == nil {
		response, err := s.rpcMarshalBlock(ctx, block, true, fullTx)
		if err == nil && number == rpc.PendingBlockNumber {
			// Pending blocks need to nil out a few fields
			for _, field := range []string{"hash", "nonce", "miner"} {
				response[field] = nil
			}
		}
		return response, err
	}
	return nil, err
}

// GetBlockByHash returns the requested block. When fullTx is true all transactions in the block are returned in full
// detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainQuaiAPI) GetBlockByHash(ctx context.Context, hash common.Hash, fullTx bool) (map[string]interface{}, error) {
	block, err := s.b.BlockByHash(ctx, hash)
	if block != nil {
		return s.rpcMarshalBlock(ctx, block, true, fullTx)
	}
	return nil, err
}

// GetUncleByBlockNumberAndIndex returns the uncle block for the given block hash and index. When fullTx is true
// all transactions in the block are returned in full detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainQuaiAPI) GetUncleByBlockNumberAndIndex(ctx context.Context, blockNr rpc.BlockNumber, index hexutil.Uint) (map[string]interface{}, error) {
	block, err := s.b.BlockByNumber(ctx, blockNr)
	if block != nil {
		uncles := block.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			log.Debug("Requested uncle not found", "number", blockNr, "hash", block.Hash(), "index", index)
			return nil, nil
		}
		block = types.NewBlockWithHeader(uncles[index])
		return s.rpcMarshalBlock(ctx, block, false, false)
	}
	return nil, err
}

// GetUncleByBlockHashAndIndex returns the uncle block for the given block hash and index. When fullTx is true
// all transactions in the block are returned in full detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainQuaiAPI) GetUncleByBlockHashAndIndex(ctx context.Context, blockHash common.Hash, index hexutil.Uint) (map[string]interface{}, error) {
	block, err := s.b.BlockByHash(ctx, blockHash)
	if block != nil {
		uncles := block.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			log.Debug("Requested uncle not found", "number", block.Number(), "hash", blockHash, "index", index)
			return nil, nil
		}
		block = types.NewBlockWithHeader(uncles[index])
		return s.rpcMarshalBlock(ctx, block, false, false)
	}
	pendBlock, _ := s.b.PendingBlockAndReceipts()
	if pendBlock != nil && pendBlock.Hash() == blockHash {
		uncles := pendBlock.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			log.Debug("Requested uncle not found in pending block", "number", block.Number(), "hash", blockHash, "index", index)
			return nil, nil
		}
		block = types.NewBlockWithHeader(uncles[index])
		return s.rpcMarshalBlock(ctx, block, false, false)
	}
	return nil, err
}

// GetUncleCountByBlockNumber returns number of uncles in the block for the given block number
func (s *PublicBlockChainQuaiAPI) GetUncleCountByBlockNumber(ctx context.Context, blockNr rpc.BlockNumber) *hexutil.Uint {
	if block, _ := s.b.BlockByNumber(ctx, blockNr); block != nil {
		n := hexutil.Uint(len(block.Uncles()))
		return &n
	}
	return nil
}

// GetUncleCountByBlockHash returns number of uncles in the block for the given block hash
func (s *PublicBlockChainQuaiAPI) GetUncleCountByBlockHash(ctx context.Context, blockHash common.Hash) *hexutil.Uint {
	if block, _ := s.b.BlockByHash(ctx, blockHash); block != nil {
		n := hexutil.Uint(len(block.Uncles()))
		return &n
	}
	return nil
}

// GetCode returns the code stored at the given address in the state for the given block number.
func (s *PublicBlockChainQuaiAPI) GetCode(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	code, err := state.GetCode(address)
	if err != nil {
		return hexutil.Bytes{}, err
	}
	return code, state.Error()
}

// GetStorageAt returns the storage from the state at the given address, key and
// block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta block
// numbers are also allowed.
func (s *PublicBlockChainQuaiAPI) GetStorageAt(ctx context.Context, address common.Address, key string, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	res, err := state.GetState(address, common.HexToHash(key))
	if err != nil {
		return hexutil.Bytes{}, err
	}
	return res[:], state.Error()
}

// Call executes the given transaction on the state for the given block number.
//
// Additionally, the caller can specify a batch of contract for fields overriding.
//
// Note, this function doesn't make and changes in the state/blockchain and is
// useful to execute and retrieve values.
func (s *PublicBlockChainQuaiAPI) Call(ctx context.Context, args TransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, overrides *StateOverride) (hexutil.Bytes, error) {
	result, err := DoCall(ctx, s.b, args, blockNrOrHash, overrides, 5*time.Second, s.b.RPCGasCap())
	if err != nil {
		return nil, err
	}
	// If the result contains a revert reason, try to unpack and return it.
	if len(result.Revert()) > 0 {
		return nil, newRevertError(result)
	}
	return result.Return(), result.Err
}

// EstimateGas returns an estimate of the amount of gas needed to execute the
// given transaction against the current pending block.
func (s *PublicBlockChainQuaiAPI) EstimateGas(ctx context.Context, args TransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Uint64, error) {
	bNrOrHash := rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
	if blockNrOrHash != nil {
		bNrOrHash = *blockNrOrHash
	}
	return DoEstimateGas(ctx, s.b, args, bNrOrHash, s.b.RPCGasCap())
}

// RPCMarshalHeader converts the given header to the RPC output .
func RPCMarshalHeader(head *types.Header) map[string]interface{} {
	result := map[string]interface{}{
		"hash":                head.Hash(),
		"parentHash":          head.ParentHashArray(),
		"nonce":               head.Nonce(),
		"sha3Uncles":          head.UncleHashArray(),
		"logsBloom":           head.BloomArray(),
		"stateRoot":           head.RootArray(),
		"miner":               head.CoinbaseArray(),
		"extraData":           hexutil.Bytes(head.Extra()),
		"size":                hexutil.Uint64(head.Size()),
		"timestamp":           hexutil.Uint64(head.Time()),
		"transactionsRoot":    head.TxHashArray(),
		"receiptsRoot":        head.ReceiptHashArray(),
		"extTransactionsRoot": head.EtxHashArray(),
		"manifestHash":        head.ManifestHashArray(),
		"location":            head.Location(),
	}

	number := make([]*hexutil.Big, common.HierarchyDepth)
	difficulty := make([]*hexutil.Big, common.HierarchyDepth)
	gasLimit := make([]hexutil.Uint, common.HierarchyDepth)
	gasUsed := make([]hexutil.Uint, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		number[i] = (*hexutil.Big)(head.Number(i))
		difficulty[i] = (*hexutil.Big)(head.Difficulty(i))
		gasLimit[i] = hexutil.Uint(head.GasLimit(i))
		gasUsed[i] = hexutil.Uint(head.GasUsed(i))
	}
	result["number"] = number
	result["difficulty"] = difficulty
	result["gasLimit"] = gasLimit
	result["gasUsed"] = gasUsed

	if head.BaseFee() != nil {
		results := make([]*hexutil.Big, common.HierarchyDepth)
		for i := 0; i < common.HierarchyDepth; i++ {
			results[i] = (*hexutil.Big)(head.BaseFee(i))
		}
		result["baseFeePerGas"] = results
	}

	return result
}

// RPCMarshalBlock converts the given block to the RPC output which depends on fullTx. If inclTx is true transactions are
// returned. When fullTx is true the returned block contains full transaction details, otherwise it will only contain
// transaction hashes.
func RPCMarshalBlock(block *types.Block, inclTx bool, fullTx bool) (map[string]interface{}, error) {
	fields := RPCMarshalHeader(block.Header())
	fields["size"] = hexutil.Uint64(block.Size())

	if inclTx {
		formatTx := func(tx *types.Transaction) (interface{}, error) {
			return tx.Hash(), nil
		}
		if fullTx {
			formatTx = func(tx *types.Transaction) (interface{}, error) {
				return newRPCTransactionFromBlockHash(block, tx.Hash()), nil
			}
		}
		txs := block.Transactions()
		transactions := make([]interface{}, len(txs))
		var err error
		for i, tx := range txs {
			if transactions[i], err = formatTx(tx); err != nil {
				return nil, err
			}
		}
		fields["transactions"] = transactions
		etxs := block.ExtTransactions()
		extTransactions := make([]interface{}, len(etxs))
		for i, etx := range etxs {
			if extTransactions[i], err = formatTx(etx); err != nil {
				return nil, err
			}
		}
		fields["extTransactions"] = extTransactions
		manifest := block.SubManifest()
		subBlockHashes := make([]interface{}, len(manifest))
		for i, blockHash := range manifest {
			subBlockHashes[i] = blockHash
		}
	}

	fields["uncles"] = block.Uncles()
	fields["subManifest"] = block.SubManifest()

	return fields, nil
}

// rpcMarshalReOrgData converts the reOrgData obtained to the right header format
func RPCMarshalReOrgData(header *types.Header, newHeaders []*types.Header, oldHeaders []*types.Header) (map[string]interface{}, error) {
	fields := map[string]interface{}{"header": RPCMarshalHeader(header)}

	fieldNewHeaders := make([]interface{}, len(newHeaders))
	for i, newHeader := range newHeaders {
		fieldNewHeaders[i] = RPCMarshalHeader(newHeader)
	}

	fieldOldHeaders := make([]interface{}, len(oldHeaders))
	for i, oldHeader := range oldHeaders {
		fieldOldHeaders[i] = RPCMarshalHeader(oldHeader)
	}

	fields["newHeaders"] = fieldNewHeaders
	fields["oldHeaders"] = fieldOldHeaders
	return fields, nil
}

// RPCMarshalHash convert the hash into a the correct interface.
func RPCMarshalHash(hash common.Hash) (map[string]interface{}, error) {
	fields := map[string]interface{}{"Hash": hash}
	return fields, nil
}

// rpcMarshalHeader uses the generalized output filler, then adds the total difficulty field, which requires
// a `PublicBlockchainQuaiAPI`.
func (s *PublicBlockChainQuaiAPI) rpcMarshalHeader(ctx context.Context, header *types.Header) map[string]interface{} {
	fields := RPCMarshalHeader(header)
	fields["totalDifficulty"] = (*hexutil.Big)(s.b.GetTd(ctx, header.Hash()))
	return fields
}

// rpcMarshalBlock uses the generalized output filler, then adds the total difficulty field, which requires
// a `PublicBlockchainAPI`.
func (s *PublicBlockChainQuaiAPI) rpcMarshalBlock(ctx context.Context, b *types.Block, inclTx bool, fullTx bool) (map[string]interface{}, error) {
	fields, err := RPCMarshalBlock(b, inclTx, fullTx)
	if err != nil {
		return nil, err
	}
	if inclTx {
		fields["totalDifficulty"] = (*hexutil.Big)(s.b.GetTd(ctx, b.Hash()))
	}
	return fields, err
}

// CreateAccessList creates a EIP-2930 type AccessList for the given transaction.
// Reexec and BlockNrOrHash can be specified to create the accessList on top of a certain state.
func (s *PublicBlockChainQuaiAPI) CreateAccessList(ctx context.Context, args TransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (*accessListResult, error) {
	bNrOrHash := rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
	if blockNrOrHash != nil {
		bNrOrHash = *blockNrOrHash
	}
	acl, gasUsed, vmerr, err := AccessList(ctx, s.b, bNrOrHash, args)
	if err != nil {
		return nil, err
	}
	result := &accessListResult{Accesslist: &acl, GasUsed: hexutil.Uint64(gasUsed)}
	if vmerr != nil {
		result.Error = vmerr.Error()
	}
	return result, nil
}

// ReceiveMinedHeader will run checks on the block and add to canonical chain if valid.
func (s *PublicBlockChainQuaiAPI) ReceiveMinedHeader(ctx context.Context, raw json.RawMessage) error {
	// Decode header and transactions.
	var header *types.Header
	if err := json.Unmarshal(raw, &header); err != nil {
		return err
	}
	block := s.b.ConstructLocalBlock(header)
	if block == nil {
		return errors.New("could not find the block body in db")
	}
	// Broadcast the block and announce chain insertion event
	if block.Header() != nil {
		s.b.EventMux().Post(core.NewMinedBlockEvent{Block: block})
	}
	log.Info("Retrieved mined block", "number", header.Number(), "location", header.Location())
	s.b.InsertBlock(ctx, block)

	return nil
}

type tdBlock struct {
	Td          *big.Int    `json:"td"`
	DomTerminus common.Hash `json:"domTerminus"`
	DomOrigin   bool        `json:"domOrigin"`
	Reorg       bool        `json:"reorg"`
}

func (s *PublicBlockChainQuaiAPI) Append(ctx context.Context, raw json.RawMessage) error {
	// Decode header and transactions.
	var head *types.Header
	var body tdBlock
	if err := json.Unmarshal(raw, &head); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}

	err := s.b.Append(head, body.DomTerminus, body.Td, body.DomOrigin, body.Reorg)
	if err != nil {
		return err
	}
	return nil
}

type SubRelay struct {
	Header   *types.Header
	Termini  []common.Hash
	Location common.Location
	Reorg    bool
}

func (s *PublicBlockChainQuaiAPI) SubRelayPendingHeader(ctx context.Context, raw json.RawMessage) error {
	var subRelay SubRelay
	if err := json.Unmarshal(raw, &subRelay); err != nil {
		return err
	}
	pendingHeader := types.PendingHeader{Header: subRelay.Header, Termini: subRelay.Termini}
	return s.b.SubRelayPendingHeader(pendingHeader, subRelay.Reorg)
}

func (s *PublicBlockChainQuaiAPI) GetPendingHeader(ctx context.Context) (map[string]interface{}, error) {
	pendingHeader, err := s.b.GetPendingHeader()
	if err != nil {
		return nil, err
	}
	// Marshal the response.
	marshaledPh := RPCMarshalHeader(pendingHeader)
	return marshaledPh, nil
}
