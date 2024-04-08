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
	"math/big"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
	"github.com/dominant-strategies/go-quai/trie"
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

// PublicBlockChainQuaiAPI provides an API to access the Quai blockchain.
// It offers only methods that operate on public data that is freely available to anyone.
type PublicBlockChainQuaiAPI struct {
	b Backend
}

// NewPublicBlockChainQuaiAPI creates a new Quai blockchain API.
func NewPublicBlockChainQuaiAPI(b Backend) *PublicBlockChainQuaiAPI {
	return &PublicBlockChainQuaiAPI{b}
}

// ChainId is the replay-protection chain id for the current Quai chain config.
func (api *PublicBlockChainQuaiAPI) ChainId() (*hexutil.Big, error) {
	return (*hexutil.Big)(api.b.ChainConfig().ChainID), nil
}

// NodeLocation is the access call to the location of the node.
func (api *PublicBlockChainQuaiAPI) NodeLocation() []hexutil.Uint64 {
	return api.b.NodeLocation().RPCMarshal()
}

// BlockNumber returns the block number of the chain head.
func (s *PublicBlockChainQuaiAPI) BlockNumber() hexutil.Uint64 {
	header, _ := s.b.HeaderByNumber(context.Background(), rpc.LatestBlockNumber) // latest header should always be available
	return hexutil.Uint64(header.NumberU64(s.b.NodeCtx()))

}

// GetBalance returns the amount of wei for the given address in the state of the
// given block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta
// block numbers are also allowed.
func (s *PublicBlockChainQuaiAPI) GetBalance(ctx context.Context, address common.AddressBytes, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getBalance call can only be made in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getBalance call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	addr := common.Bytes20ToAddress(address, s.b.NodeLocation())
	internal, err := addr.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	return (*hexutil.Big)(state.GetBalance(internal)), state.Error()
}

func (s *PublicBlockChainQuaiAPI) GetQiBalance(ctx context.Context, address common.Address) (*hexutil.Big, error) {
	utxos, err := s.b.UTXOsByAddress(ctx, address)
	if utxos == nil || err != nil {
		return nil, err
	}

	if len(utxos) == 0 {
		return (*hexutil.Big)(big.NewInt(0)), nil
	}

	var balance *big.Int
	for _, utxo := range utxos {
		denomination := utxo.Denomination
		value := types.Denominations[denomination]
		if balance == nil {
			balance = new(big.Int).Set(value)
		} else {
			balance.Add(balance, value)
		}
	}
	return (*hexutil.Big)(balance), nil
}

// GetProof returns the Merkle-proof for a given account and optionally some storage keys.
func (s *PublicBlockChainQuaiAPI) GetProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*AccountResult, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getProof call can only be made in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getProof call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}

	storageTrie := state.StorageTrie(internal)
	storageHash := types.EmptyRootHash
	codeHash := state.GetCodeHash(internal)
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
			proof, storageError := state.GetStorageProof(internal, common.HexToHash(key))
			if storageError != nil {
				return nil, storageError
			}
			storageProof[i] = StorageResult{key, (*hexutil.Big)(state.GetState(internal, common.HexToHash(key)).Big()), toHexSlice(proof)}
		} else {
			storageProof[i] = StorageResult{key, &hexutil.Big{}, []string{}}
		}
	}

	// create the accountProof
	accountProof, proofErr := state.GetProof(internal)
	if proofErr != nil {
		return nil, proofErr
	}

	return &AccountResult{
		Address:      address,
		AccountProof: toHexSlice(accountProof),
		Balance:      (*hexutil.Big)(state.GetBalance(internal)),
		CodeHash:     codeHash,
		Nonce:        hexutil.Uint64(state.GetNonce(internal)),
		StorageHash:  storageHash,
		StorageProof: storageProof,
	}, state.Error()
}

// GetHeaderByNumber returns the requested canonical block header.
// * When blockNr is -1 the chain head is returned.
// * When blockNr is -2 the pending chain head is returned.
func (s *PublicBlockChainQuaiAPI) GetHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (map[string]interface{}, error) {
	header, err := s.b.HeaderByNumber(ctx, number)
	if header != nil && err == nil {
		response := header.RPCMarshalWorkObject()
		if number == rpc.PendingBlockNumber {
			// Pending header need to nil out a few fields
			for _, field := range []string{"hash", "nonce", "miner"} {
				response[field] = nil
			}
		}
		return response, err
	}
	return nil, err
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
		return header.RPCMarshalWorkObject()
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
			s.b.Logger().WithFields(log.Fields{
				"number": block.Number(s.b.NodeCtx()),
				"hash":   block.Hash(),
				"index":  index,
			}).Debug("Requested uncle not found")
			return nil, nil
		}
		return uncles[index].RPCMarshalWorkObjectHeader(), nil
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
			s.b.Logger().WithFields(log.Fields{
				"number": block.Number(s.b.NodeCtx()),
				"hash":   blockHash,
				"index":  index,
			}).Debug("Requested uncle not found")
			return nil, nil
		}
		return uncles[index].RPCMarshalWorkObjectHeader(), nil
	}
	pendBlock, _ := s.b.PendingBlockAndReceipts()
	if pendBlock != nil && pendBlock.Hash() == blockHash {
		uncles := pendBlock.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			s.b.Logger().WithFields(log.Fields{
				"number": block.Number(s.b.NodeCtx()),
				"hash":   blockHash,
				"index":  index,
			}).Debug("Requested uncle not found in pending block")
			return nil, nil
		}
		return uncles[index].RPCMarshalWorkObjectHeader(), nil
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
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getCode can only called in a zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getCode call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	code := state.GetCode(internal)
	return code, state.Error()
}

// GetStorageAt returns the storage from the state at the given address, key and
// block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta block
// numbers are also allowed.
func (s *PublicBlockChainQuaiAPI) GetStorageAt(ctx context.Context, address common.Address, key string, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getStorageAt can only called in a zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getStorageAt call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	res := state.GetState(internal, common.HexToHash(key))
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
		return nil, newRevertError(result, s.b.NodeLocation())
	}
	return result.Return(), result.Err
}

// EstimateGas returns an estimate of the amount of gas needed to execute the
// given transaction against the current pending block.
func (s *PublicBlockChainQuaiAPI) EstimateGas(ctx context.Context, args TransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Uint64, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return 0, errors.New("estimateGas can only called in a zone chain")
	}
	if !s.b.ProcessingState() {
		return 0, errors.New("estimateGas call can only be made on chain processing the state")
	}
	bNrOrHash := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	if blockNrOrHash != nil {
		bNrOrHash = *blockNrOrHash
	}
	switch args.TxType {
	case types.QiTxType:
		return args.CalculateQiTxGas()
	case types.QuaiTxType:
		return DoEstimateGas(ctx, s.b, args, bNrOrHash, s.b.RPCGasCap())
	default:
		return 0, errors.New("unsupported tx type")
	}
}

// BaseFee returns the base fee for a tx to be included in the next block.
// If txType is set to "true" returns the Quai base fee in units of Wei.
// If txType is set to "false" returns the Qi base fee in units of Qit.
func (s *PublicBlockChainQuaiAPI) BaseFee(ctx context.Context, txType bool) (*big.Int, error) {
	header := s.b.CurrentBlock()
	if header == nil {
		return nil, errors.New("no header available")
	}

	chainCfg := s.b.ChainConfig()
	if chainCfg == nil {
		return nil, errors.New("no chain config available")
	}

	if txType {
		return misc.CalcBaseFee(chainCfg, header), nil
	}

	// TODO: implement Qi base fee calculation
	panic("Qi base fee calculation not implemented")
}

// RPCMarshalBlock converts the given block to the RPC output which depends on fullTx. If inclTx is true transactions are
// returned. When fullTx is true the returned block contains full transaction details, otherwise it will only contain
// transaction hashes.
func RPCMarshalBlock(block *types.WorkObject, inclTx bool, fullTx bool, nodeLocation common.Location) (map[string]interface{}, error) {
	fields := block.Header().RPCMarshalHeader()
	fields["size"] = hexutil.Uint64(block.Size())

	if inclTx {
		formatTx := func(tx *types.Transaction) (interface{}, error) {
			return tx.Hash(), nil
		}
		formatEtx := formatTx
		if fullTx {
			formatTx = func(tx *types.Transaction) (interface{}, error) {
				return newRPCTransactionFromBlockHash(block, tx.Hash(), false, nodeLocation), nil
			}
			formatEtx = func(tx *types.Transaction) (interface{}, error) {
				return newRPCTransactionFromBlockHash(block, tx.Hash(), true, nodeLocation), nil
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
			if extTransactions[i], err = formatEtx(etx); err != nil {
				return nil, err
			}
		}
		fields["extTransactions"] = extTransactions
	}

	fields["uncles"] = block.Uncles()
	fields["subManifest"] = block.Manifest()
	fields["interlinkHashes"] = block.InterlinkHashes()

	return fields, nil
}

// rpcMarshalReOrgData converts the reOrgData obtained to the right header format
func RPCMarshalReOrgData(header *types.Header, newHeaders []*types.Header, oldHeaders []*types.Header) (map[string]interface{}, error) {
	fields := map[string]interface{}{"header": header.RPCMarshalHeader()}

	fieldNewHeaders := make([]interface{}, len(newHeaders))
	for i, newHeader := range newHeaders {
		fieldNewHeaders[i] = newHeader.RPCMarshalHeader()
	}

	fieldOldHeaders := make([]interface{}, len(oldHeaders))
	for i, oldHeader := range oldHeaders {
		fieldOldHeaders[i] = oldHeader.RPCMarshalHeader()
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
func (s *PublicBlockChainQuaiAPI) rpcMarshalHeader(ctx context.Context, header *types.WorkObject) map[string]interface{} {
	fields := header.RPCMarshalWorkObject()
	fields["totalEntropy"] = (*hexutil.Big)(s.b.TotalLogS(header))
	return fields
}

// rpcMarshalBlock uses the generalized output filler, then adds the total difficulty field, which requires
// a `PublicBlockchainAPI`.
func (s *PublicBlockChainQuaiAPI) rpcMarshalBlock(ctx context.Context, b *types.WorkObject, inclTx bool, fullTx bool) (map[string]interface{}, error) {
	fields, err := RPCMarshalBlock(b, inclTx, fullTx, s.b.NodeLocation())
	if err != nil {
		return nil, err
	}
	_, order, err := s.b.CalcOrder(b)
	if err != nil {
		return nil, err
	}
	fields["order"] = order
	fields["totalEntropy"] = (*hexutil.Big)(s.b.TotalLogS(b))
	return fields, err
}

// CreateAccessList creates an AccessList for the given transaction.
// Reexec and BlockNrOrHash can be specified to create the accessList on top of a certain state.
func (s *PublicBlockChainQuaiAPI) CreateAccessList(ctx context.Context, args TransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (*accessListResult, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("createAccessList can only be called in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("createAccessList call can only be made on chain processing the state")
	}
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

func (s *PublicBlockChainQuaiAPI) fillSubordinateManifest(b *types.WorkObject) (*types.WorkObject, error) {
	nodeCtx := s.b.NodeCtx()
	if b.ManifestHash(nodeCtx+1) == types.EmptyRootHash {
		return nil, errors.New("cannot fill empty subordinate manifest")
	} else if subManifestHash := types.DeriveSha(b.Manifest(), trie.NewStackTrie(nil)); subManifestHash == b.ManifestHash(nodeCtx+1) {
		// If the manifest hashes match, nothing to do
		return b, nil
	} else {
		subParentHash := b.ParentHash(nodeCtx + 1)
		var subManifest types.BlockManifest
		if subParent, err := s.b.BlockByHash(context.Background(), subParentHash); err == nil && subParent != nil {
			// If we have the the subordinate parent in our chain, that means that block
			// was also coincident. In this case, the subordinate manifest resets, and
			// only consists of the subordinate parent hash.
			subManifest = types.BlockManifest{subParentHash}
		} else {
			// Otherwise we need to reconstruct the sub manifest, by getting the
			// parent's sub manifest and appending the parent hash.
			subManifest, err = s.b.GetSubManifest(b.Location(), subParentHash)
			if err != nil {
				return nil, err
			}
		}
		if len(subManifest) == 0 {
			return nil, errors.New("reconstructed sub manifest is empty")
		}
		if subManifest == nil || b.ManifestHash(nodeCtx+1) != types.DeriveSha(subManifest, trie.NewStackTrie(nil)) {
			return nil, errors.New("reconstructed sub manifest does not match manifest hash")
		}
		return types.NewWorkObjectWithHeaderAndTx(b.WorkObjectHeader(), b.Tx()).WithBody(b.Header(), b.Transactions(), b.ExtTransactions(), b.Uncles(), subManifest, b.InterlinkHashes()), nil
	}
}

// ReceiveMinedHeader will run checks on the block and add to canonical chain if valid.
func (s *PublicBlockChainQuaiAPI) ReceiveMinedHeader(ctx context.Context, raw json.RawMessage) error {
	nodeCtx := s.b.NodeCtx()
	// Decode header and transactions.
	var woHeader *types.WorkObject
	if err := json.Unmarshal(raw, &woHeader); err != nil {
		return err
	}
	woHeader.Header().SetCoinbase(common.BytesToAddress(woHeader.Coinbase().Bytes(), s.b.NodeLocation()))
	block, err := s.b.ConstructLocalMinedBlock(woHeader)
	if err != nil && err.Error() == core.ErrBadSubManifest.Error() && nodeCtx < common.ZONE_CTX {
		s.b.Logger().Info("filling sub manifest")
		// If we just mined this block, and we have a subordinate chain, its possible
		// the subordinate manifest in our block body is incorrect. If so, ask our sub
		// for the correct manifest and reconstruct the block.
		var err error
		block, err = s.fillSubordinateManifest(block)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Broadcast the block and announce chain insertion event
	if block.Header() != nil {
		err := s.b.BroadcastBlock(block, s.b.NodeLocation())
		if err != nil {
			log.Global.WithField("err", err).Error("Error broadcasting block")
		}
	}
	s.b.Logger().WithFields(log.Fields{
		"number":   block.Number(s.b.NodeCtx()),
		"location": block.Location(),
	}).Info("Received mined header")

	return nil
}

type tdBlock struct {
	Header           *types.WorkObject   `json:"header"`
	Manifest         types.BlockManifest `json:"manifest"`
	DomPendingHeader *types.WorkObject   `json:"domPendingHeader"`
	DomTerminus      common.Hash         `json:"domTerminus"`
	DomOrigin        bool                `json:"domOrigin"`
	NewInboundEtxs   types.Transactions  `json:"newInboundEtxs"`
}

func (s *PublicBlockChainQuaiAPI) Append(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	// Decode header and transactions.
	var body tdBlock

	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	body.Header.Header().SetCoinbase(common.BytesToAddress(body.Header.Coinbase().Bytes(), s.b.NodeLocation()))
	pendingEtxs, subReorg, setHead, err := s.b.Append(body.Header, body.Manifest, body.DomPendingHeader, body.DomTerminus, body.DomOrigin, body.NewInboundEtxs)
	if err != nil {
		return nil, err
	}
	// Marshal the output for decoding
	fields := map[string]interface{}{
		"pendingEtxs": pendingEtxs,
		"subReorg":    subReorg,
		"setHead":     setHead,
	}

	return fields, nil

}

type DownloadBlocksInManifestArgs struct {
	Hash     common.Hash         `json:"hash"`
	Manifest types.BlockManifest `json:"manifest"`
	Entropy  *big.Int            `json:"entropy"`
}

func (s *PublicBlockChainQuaiAPI) DownloadBlocksInManifest(ctx context.Context, raw json.RawMessage) {
	var manifest DownloadBlocksInManifestArgs
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return
	}
	s.b.DownloadBlocksInManifest(manifest.Hash, manifest.Manifest, manifest.Entropy)
}

type SubRelay struct {
	Header     *types.WorkObject `json:"header"`
	Termini    types.Termini     `json:"termini"`
	NewEntropy *big.Int
	Location   common.Location
	SubReorg   bool
	Order      int
}

func (s *PublicBlockChainQuaiAPI) SubRelayPendingHeader(ctx context.Context, raw json.RawMessage) {
	var subRelay SubRelay
	if err := json.Unmarshal(raw, &subRelay); err != nil {
		return
	}
	pendingHeader := types.NewPendingHeader(subRelay.Header, subRelay.Termini)
	s.b.SubRelayPendingHeader(pendingHeader, subRelay.NewEntropy, subRelay.Location, subRelay.SubReorg, subRelay.Order)
}

type DomUpdate struct {
	OldTerminus common.Hash
	Header      *types.WorkObject `json:"header"`
	Termini     types.Termini     `json:"termini"`
	Location    common.Location
}

func (s *PublicBlockChainQuaiAPI) UpdateDom(ctx context.Context, raw json.RawMessage) {
	var domUpdate DomUpdate
	if err := json.Unmarshal(raw, &domUpdate); err != nil {
		s.b.Logger().WithField("err", err).Error("Error unmarshaling domUpdate in api")
		return
	}
	pendingHeader := types.NewPendingHeader(domUpdate.Header, domUpdate.Termini)
	s.b.UpdateDom(domUpdate.OldTerminus, pendingHeader, domUpdate.Location)
}

type RequestDomToAppendOrFetchArgs struct {
	Hash    common.Hash
	Entropy *big.Int
	Order   int
}

func (s *PublicBlockChainQuaiAPI) RequestDomToAppendOrFetch(ctx context.Context, raw json.RawMessage) {
	var requestDom RequestDomToAppendOrFetchArgs
	if err := json.Unmarshal(raw, &requestDom); err != nil {
		return
	}
	s.b.RequestDomToAppendOrFetch(requestDom.Hash, requestDom.Entropy, requestDom.Order)
}

type NewGenesisPendingHeaderArgs struct {
	PendingHeader *types.WorkObject `json:"header"`
	Hash          common.Hash       `json:"genesisHash"`
	DomTerminus   common.Hash       `json:"domTerminus"`
}

func (s *PublicBlockChainQuaiAPI) NewGenesisPendingHeader(ctx context.Context, raw json.RawMessage) {
	var genesis NewGenesisPendingHeaderArgs
	if err := json.Unmarshal(raw, &genesis); err != nil {
		return
	}
	s.b.NewGenesisPendingHeader(genesis.PendingHeader, genesis.DomTerminus, genesis.Hash)
}

func (s *PublicBlockChainQuaiAPI) GetPendingHeader(ctx context.Context) (map[string]interface{}, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getPendingHeader can only be called in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getPendingHeader call can only be made on chain processing the state")
	}
	pendingHeader, err := s.b.GetPendingHeader()
	if err != nil {
		return nil, err
	} else if pendingHeader == nil {
		return nil, errors.New("no pending header found")
	}
	// Marshal the response.
	marshaledPh := pendingHeader.RPCMarshalWorkObject()
	return marshaledPh, nil
}

func (s *PublicBlockChainQuaiAPI) GetManifest(ctx context.Context, raw json.RawMessage) (types.BlockManifest, error) {
	var blockHash common.Hash
	if err := json.Unmarshal(raw, &blockHash); err != nil {
		return nil, err
	}
	manifest, err := s.b.GetManifest(blockHash)
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

type SendPendingEtxsToDomArgs struct {
	Header         types.WorkObject     `json:"header"`
	NewPendingEtxs []types.Transactions `json:"newPendingEtxs"`
}

func (s *PublicBlockChainQuaiAPI) SendPendingEtxsToDom(ctx context.Context, raw json.RawMessage) error {
	var pEtxs types.PendingEtxs
	if err := json.Unmarshal(raw, &pEtxs); err != nil {
		return err
	}
	return s.b.AddPendingEtxs(pEtxs)
}

type SendPendingEtxsRollupToDomArgs struct {
	Header     *types.WorkObject  `json:"header"`
	EtxsRollup types.Transactions `json:"etxsrollup"`
}

func (s *PublicBlockChainQuaiAPI) SendPendingEtxsRollupToDom(ctx context.Context, raw json.RawMessage) error {
	var pEtxsRollup SendPendingEtxsRollupToDomArgs
	if err := json.Unmarshal(raw, &pEtxsRollup); err != nil {
		return err
	}
	return s.b.AddPendingEtxsRollup(types.PendingEtxsRollup{Header: pEtxsRollup.Header, EtxsRollup: pEtxsRollup.EtxsRollup})
}

type GenerateRecoveryPendingHeaderArgs struct {
	PendingHeader    *types.WorkObject `json:"pendingHeader"`
	CheckpointHashes types.Termini     `json:"checkpointHashes"`
}

func (s *PublicBlockChainQuaiAPI) GenerateRecoveryPendingHeader(ctx context.Context, raw json.RawMessage) error {
	var pHandcheckPointHashes GenerateRecoveryPendingHeaderArgs
	if err := json.Unmarshal(raw, &pHandcheckPointHashes); err != nil {
		return err
	}
	return s.b.GenerateRecoveryPendingHeader(pHandcheckPointHashes.PendingHeader, pHandcheckPointHashes.CheckpointHashes)
}

type GetPendingEtxsRollupFuncArgs struct {
	Hash     common.Hash
	Location common.Location
}

func (s *PublicBlockChainQuaiAPI) GetPendingEtxsRollupFromSub(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var getPEtxsRollup GetPendingEtxsFuncArgs
	if err := json.Unmarshal(raw, &getPEtxsRollup); err != nil {
		return nil, err
	}
	pEtxsRollup, err := s.b.GetPendingEtxsRollupFromSub(getPEtxsRollup.Hash, getPEtxsRollup.Location)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]interface{})
	fields["header"] = pEtxsRollup.Header.RPCMarshalWorkObject()
	fields["etxsrollup"] = pEtxsRollup.EtxsRollup

	return fields, nil
}

type GetPendingEtxsFuncArgs struct {
	Hash     common.Hash
	Location common.Location
}

func (s *PublicBlockChainQuaiAPI) GetPendingEtxsFromSub(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var getPEtxs GetPendingEtxsFuncArgs
	if err := json.Unmarshal(raw, &getPEtxs); err != nil {
		return nil, err
	}
	pEtxs, err := s.b.GetPendingEtxsFromSub(getPEtxs.Hash, getPEtxs.Location)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]interface{})
	fields["header"] = pEtxs.Header.RPCMarshalWorkObject()
	fields["etxs"] = pEtxs.Etxs

	return fields, nil
}

// ListRunningChains returns the running locations where the node is serving data.
func (s *PublicBlockChainQuaiAPI) ListRunningChains() []common.Location {
	return s.b.GetSlicesRunning()
}

func (s *PublicBlockChainQuaiAPI) GetProtocolExpansionNumber() int {
	// TODO: Implement this
	return 0
}

// Calculate the amount of Quai that Qi can be converted to. Expect the current Header and the Qi amount in "qits", returns the quai amount in "its"
func (s *PublicBlockChainQuaiAPI) QiRateAtBlock(ctx context.Context, blockRef interface{}, qiAmount uint64) *big.Int {
	var header *types.WorkObject
	var err error
	switch b := blockRef.(type) {
	case common.Hash:
		header, err = s.b.HeaderByHash(ctx, b)
	case uint64:
		header, err = s.b.HeaderByNumber(ctx, rpc.BlockNumber(b))
	}
	if err != nil {
		return nil
	}

	return misc.QiToQuai(header, new(big.Int).SetUint64(qiAmount))
}

// Calculate the amount of Qi that Quai can be converted to. Expect the current Header and the Quai amount in "its", returns the Qi amount in "qits"
func (s *PublicBlockChainQuaiAPI) QuaiRateAtBlock(ctx context.Context, blockRef interface{}, quaiAmount uint64) *big.Int {
	var header *types.WorkObject
	var err error
	switch b := blockRef.(type) {
	case common.Hash:
		header, err = s.b.HeaderByHash(ctx, b)
	case uint64:
		header, err = s.b.HeaderByNumber(ctx, rpc.BlockNumber(b))
	}
	if err != nil {
		return nil
	}

	return misc.QuaiToQi(header, new(big.Int).SetUint64(quaiAmount))
}
