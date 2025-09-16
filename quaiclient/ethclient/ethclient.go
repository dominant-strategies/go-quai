// Copyright 2016 The go-ethereum Authors
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

// Package ethclient provides a client for the Quai RPC API.
package ethclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	quai "github.com/dominant-strategies/go-quai"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/rpc"
	"google.golang.org/protobuf/proto"
)

// Client defines typed wrappers for the Quai RPC API.
type Client struct {
	c *rpc.Client
}

// Dial connects a client to the given URL.
func Dial(rawurl string) (*Client, error) {
	return DialContext(context.Background(), rawurl)
}

func DialContext(ctx context.Context, rawurl string) (*Client, error) {
	c, err := rpc.DialContext(ctx, rawurl)
	if err != nil {
		return nil, err
	}
	return NewClient(c), nil
}

// NewClient creates a client that uses the given RPC client.
func NewClient(c *rpc.Client) *Client {
	return &Client{c}
}

func (ec *Client) Close() {
	ec.c.Close()
}

// Blockchain Access

// ChainId retrieves the current chain ID for transaction replay protection.
func (ec *Client) ChainID(ctx context.Context) (*big.Int, error) {
	var result hexutil.Big
	err := ec.c.CallContext(ctx, &result, "eth_chainId")
	if err != nil {
		return nil, err
	}
	return (*big.Int)(&result), err
}

// BlockByHash returns the given full block.
//
// Note that loading full blocks requires two requests. Use HeaderByHash
// if you don't need all transactions or uncle headers.
func (ec *Client) BlockByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error) {
	return ec.getBlock(ctx, "quai_getBlockByHash", hash, true)
}

// BlockByNumber returns a block from the current canonical chain. If number is nil, the
// latest known block is returned.
//
// Note that loading full blocks requires two requests. Use HeaderByNumber
// if you don't need all transactions or uncle headers.
func (ec *Client) BlockByNumber(ctx context.Context, number *big.Int) (*types.WorkObject, error) {
	return ec.getBlock(ctx, "quai_getBlockByNumber", toBlockNumArg(number), true)
}

func (ec *Client) BlockOrCandidateByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error) {
	return ec.getBlock(ctx, "quai_getBlockOrCandidateByHash", hash, true)
}

// BlockNumber returns the most recent block number
func (ec *Client) BlockNumber(ctx context.Context) (uint64, error) {
	var result hexutil.Uint64
	err := ec.c.CallContext(ctx, &result, "eth_blockNumber")
	return uint64(result), err
}

type rpcBlock struct {
	Hash            common.Hash               `json:"hash"`
	Header          *types.Header             `json:"header"`
	Transactions    []rpcTransaction          `json:"transactions"`
	UncleHashes     []*types.WorkObjectHeader `json:"uncles"`
	WorkShares      []*types.WorkObjectHeader `json:"workshares"`
	OutboundEtxs    []rpcTransaction          `json:"outboundEtxs"`
	SubManifest     types.BlockManifest       `json:"manifest"`
	InterlinkHashes common.Hashes             `json:"interlinkHashes"`
}

func (ec *Client) getBlock(ctx context.Context, method string, args ...interface{}) (*types.WorkObject, error) {
	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, method, args...)
	if err != nil {
		return nil, err
	} else if len(raw) == 0 {
		return nil, quai.NotFound
	}
	// Decode header and transactions.
	var head *types.WorkObject
	var body rpcBlock
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	// Fill the sender cache of transactions in the block.
	txs := make([]*types.Transaction, len(body.Transactions))
	for i, tx := range body.Transactions {
		if tx.From != nil {
			setSenderFromServer(tx.tx, *tx.From, body.Hash)
		}
		txs[i] = tx.tx
	}
	etxs := make([]*types.Transaction, len(body.OutboundEtxs))
	for i, etx := range body.OutboundEtxs {
		etxs[i] = etx.tx
	}
	// Fill the sender cache of subordinate block hashes in the block manifest.
	var manifest types.BlockManifest
	copy(manifest, body.SubManifest)
	var interlinkHashes common.Hashes
	copy(interlinkHashes, body.InterlinkHashes)

	uncles := make([]*types.WorkObjectHeader, len(body.UncleHashes)+len(body.WorkShares))
	copy(uncles, body.UncleHashes)
	copy(uncles[len(body.UncleHashes):], body.WorkShares)

	return types.NewWorkObjectWithHeaderAndTx(head.WorkObjectHeader(), nil).WithBody(body.Header, txs, etxs, uncles, manifest, interlinkHashes), nil
}

// HeaderByHash returns the block header with the given hash.
func (ec *Client) HeaderByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error) {
	var head *types.WorkObject
	err := ec.c.CallContext(ctx, &head, "quai_getBlockByHash", hash, false)
	if err == nil && head == nil {
		err = quai.NotFound
	}
	return head, err
}

// HeaderByNumber returns a block header from the current canonical chain. If number is
// nil, the latest known header is returned.
func (ec *Client) HeaderByNumber(ctx context.Context, number *big.Int) (*types.WorkObject, error) {
	var head *types.WorkObject
	err := ec.c.CallContext(ctx, &head, "quai_getBlockByNumber", toBlockNumArg(number), false)
	if err == nil && head == nil {
		err = quai.NotFound
	}
	return head, err
}

type rpcTransaction struct {
	tx *types.Transaction
	txExtraInfo
}

type txExtraInfo struct {
	BlockNumber *string         `json:"blockNumber,omitempty"`
	BlockHash   *common.Hash    `json:"blockHash,omitempty"`
	From        *common.Address `json:"from,omitempty"`
}

func (tx *rpcTransaction) UnmarshalJSON(msg []byte) error {
	if err := json.Unmarshal(msg, &tx.tx); err != nil {
		return err
	}
	return json.Unmarshal(msg, &tx.txExtraInfo)
}

// TransactionByHash returns the transaction with the given hash.
func (ec *Client) TransactionByHash(ctx context.Context, hash common.Hash) (tx *types.Transaction, isPending bool, err error) {
	var json *rpcTransaction
	err = ec.c.CallContext(ctx, &json, "quai_getTransactionByHash", hash)
	if err != nil {
		return nil, false, err
	} else if json == nil {
		return nil, false, quai.NotFound
	}
	if json.From != nil && json.BlockHash != nil {
		setSenderFromServer(json.tx, *json.From, *json.BlockHash)
	}
	return json.tx, json.BlockNumber == nil, nil
}

// TransactionSender returns the sender address of the given transaction. The transaction
// must be known to the remote node and included in the blockchain at the given block and
// index. The sender is the one derived by the protocol at the time of inclusion.
//
// There is a fast-path for transactions retrieved by TransactionByHash and
// TransactionInBlock. Getting their sender address can be done without an RPC interaction.
func (ec *Client) TransactionSender(ctx context.Context, tx *types.Transaction, block common.Hash, index uint) (common.MixedcaseAddress, error) {
	// Try to load the address from the cache.
	sender, err := types.Sender(&senderFromServer{blockhash: block}, tx)
	if err == nil {
		return common.NewMixedcaseAddress(sender), nil
	}
	var meta struct {
		Hash common.Hash
		From common.MixedcaseAddress
	}
	if err = ec.c.CallContext(ctx, &meta, "eth_getTransactionByBlockHashAndIndex", block, hexutil.Uint64(index)); err != nil {
		return common.NewMixedcaseAddress(common.Zero), err
	}
	if meta.Hash == (common.Hash{}) || meta.Hash != tx.Hash() {
		return common.NewMixedcaseAddress(common.Zero), errors.New("wrong inclusion block/index")
	}
	return meta.From, nil
}

// TransactionCount returns the total number of transactions in the given block.
func (ec *Client) TransactionCount(ctx context.Context, blockHash common.Hash) (uint, error) {
	var num hexutil.Uint
	err := ec.c.CallContext(ctx, &num, "eth_getBlockTransactionCountByHash", blockHash)
	return uint(num), err
}

// TransactionInBlock returns a single transaction at index in the given block.
func (ec *Client) TransactionInBlock(ctx context.Context, blockHash common.Hash, index uint) (*types.Transaction, error) {
	var json *rpcTransaction
	err := ec.c.CallContext(ctx, &json, "eth_getTransactionByBlockHashAndIndex", blockHash, hexutil.Uint64(index))
	if err != nil {
		return nil, err
	}
	if json == nil {
		return nil, quai.NotFound
	} else if _, r, _ := json.tx.GetEcdsaSignatureValues(); r == nil && json.tx.Type() != types.ExternalTxType && json.tx.Type() != types.QiTxType {
		return nil, fmt.Errorf("server returned transaction without signature")
	}
	if json.From != nil && json.BlockHash != nil {
		setSenderFromServer(json.tx, *json.From, *json.BlockHash)
	}
	return json.tx, err
}

// TransactionReceipt returns the receipt of a transaction by transaction hash.
// Note that the receipt is not available for pending transactions.
func (ec *Client) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	var r *types.Receipt
	err := ec.c.CallContext(ctx, &r, "quai_getTransactionReceipt", txHash)
	if err == nil {
		if r == nil {
			return nil, quai.NotFound
		}
	}
	return r, err
}

type rpcProgress struct {
	StartingBlock hexutil.Uint64
	CurrentBlock  hexutil.Uint64
	HighestBlock  hexutil.Uint64
	PulledStates  hexutil.Uint64
	KnownStates   hexutil.Uint64
}

// SyncProgress retrieves the current progress of the sync algorithm. If there's
// no sync currently running, it returns nil.
func (ec *Client) SyncProgress(ctx context.Context) (*quai.SyncProgress, error) {
	var raw json.RawMessage
	if err := ec.c.CallContext(ctx, &raw, "eth_syncing"); err != nil {
		return nil, err
	}
	// Handle the possible response types
	var syncing bool
	if err := json.Unmarshal(raw, &syncing); err == nil {
		return nil, nil // Not syncing (always false)
	}
	var progress *rpcProgress
	if err := json.Unmarshal(raw, &progress); err != nil {
		return nil, err
	}
	return &quai.SyncProgress{
		StartingBlock: uint64(progress.StartingBlock),
		CurrentBlock:  uint64(progress.CurrentBlock),
		HighestBlock:  uint64(progress.HighestBlock),
		PulledStates:  uint64(progress.PulledStates),
		KnownStates:   uint64(progress.KnownStates),
	}, nil
}

// SubscribeNewHead subscribes to notifications about the current blockchain head
// on the given channel.
func (ec *Client) SubscribeNewHead(ctx context.Context, ch chan<- *types.WorkObject) (quai.Subscription, error) {
	return ec.c.EthSubscribe(ctx, ch, "newHeads")
}

// SubscribePendingHeader subscribes to notifications about the current pending block on the node.
func (ec *Client) SubscribePendingHeader(ctx context.Context, ch chan<- *types.WorkObject) (quai.Subscription, error) {
	return ec.c.EthSubscribe(ctx, ch, "pendingHeader")
}

// SubscribeNewWorkshares subscribes to notifications about new workshares received via P2P.
func (ec *Client) SubscribeNewWorkshares(ctx context.Context, ch chan<- *types.WorkObject) (quai.Subscription, error) {
	return ec.c.EthSubscribe(ctx, ch, "newWorkshares")
}

// State Access

// NetworkID returns the network ID (also known as the chain ID) for this chain.
func (ec *Client) NetworkID(ctx context.Context) (*big.Int, error) {
	version := new(big.Int)
	var ver string
	if err := ec.c.CallContext(ctx, &ver, "net_version"); err != nil {
		return nil, err
	}
	if _, ok := version.SetString(ver, 10); !ok {
		return nil, fmt.Errorf("invalid net_version result %q", ver)
	}
	return version, nil
}

// BalanceAt returns the wei balance of the given account.
// The block number can be nil, in which case the balance is taken from the latest known block.
func (ec *Client) BalanceAt(ctx context.Context, account common.MixedcaseAddress, blockNumber *big.Int) (*big.Int, error) {
	var result hexutil.Big
	err := ec.c.CallContext(ctx, &result, "quai_getBalance", account.Original(), toBlockNumArg(blockNumber))
	return (*big.Int)(&result), err
}

func (ec *Client) ContractSizeAt(ctx context.Context, account common.MixedcaseAddress, blockNumber *big.Int) (*big.Int, error) {
	var result hexutil.Big
	err := ec.c.CallContext(ctx, &result, "quai_getContractSize", account.Original(), toBlockNumArg(blockNumber))
	return (*big.Int)(&result), err
}

func (ec *Client) GetOutpointsByAddress(ctx context.Context, address common.MixedcaseAddress) ([]*types.OutpointAndDenomination, error) {
	var outpoints []*types.OutpointAndDenomination
	err := ec.c.CallContext(ctx, &outpoints, "quai_getOutpointsByAddress", address.Original())
	return outpoints, err
}

func (ec *Client) GetUTXO(ctx context.Context, txHash common.Hash, index uint16) (*types.UtxoEntry, error) {
	var utxo *types.UtxoEntry
	err := ec.c.CallContext(ctx, &utxo, "quai_getUTXO", txHash, hexutil.Uint64(index))
	return utxo, err
}

// StorageAt returns the value of key in the contract storage of the given account.
// The block number can be nil, in which case the value is taken from the latest known block.
func (ec *Client) StorageAt(ctx context.Context, account common.MixedcaseAddress, key common.Hash, blockNumber *big.Int) ([]byte, error) {
	var result hexutil.Bytes
	err := ec.c.CallContext(ctx, &result, "eth_getStorageAt", account.Original(), key, toBlockNumArg(blockNumber))
	return result, err
}

// CodeAt returns the contract code of the given account.
// The block number can be nil, in which case the code is taken from the latest known block.
func (ec *Client) CodeAt(ctx context.Context, account common.MixedcaseAddress, blockNumber *big.Int) ([]byte, error) {
	var result hexutil.Bytes
	err := ec.c.CallContext(ctx, &result, "eth_getCode", account.Original(), toBlockNumArg(blockNumber))
	return result, err
}

// NonceAt returns the account nonce of the given account.
// The block number can be nil, in which case the nonce is taken from the latest known block.
func (ec *Client) NonceAt(ctx context.Context, account common.MixedcaseAddress, blockNumber *big.Int) (uint64, error) {
	var result hexutil.Uint64
	err := ec.c.CallContext(ctx, &result, "eth_getTransactionCount", account.Original(), toBlockNumArg(blockNumber))
	return uint64(result), err
}

// Pending State

// PendingBalanceAt returns the wei balance of the given account in the pending state.
func (ec *Client) PendingBalanceAt(ctx context.Context, account common.MixedcaseAddress) (*big.Int, error) {
	var result hexutil.Big
	err := ec.c.CallContext(ctx, &result, "eth_getBalance", account.Original(), "pending")
	return (*big.Int)(&result), err
}

// PendingStorageAt returns the value of key in the contract storage of the given account in the pending state.
func (ec *Client) PendingStorageAt(ctx context.Context, account common.MixedcaseAddress, key common.Hash) ([]byte, error) {
	var result hexutil.Bytes
	err := ec.c.CallContext(ctx, &result, "eth_getStorageAt", account.Original(), key, "pending")
	return result, err
}

// PendingCodeAt returns the contract code of the given account in the pending state.
func (ec *Client) PendingCodeAt(ctx context.Context, account common.MixedcaseAddress) ([]byte, error) {
	var result hexutil.Bytes
	err := ec.c.CallContext(ctx, &result, "eth_getCode", account.Original(), "pending")
	return result, err
}

// PendingNonceAt returns the account nonce of the given account in the pending state.
// This is the nonce that should be used for the next transaction.
func (ec *Client) PendingNonceAt(ctx context.Context, account common.MixedcaseAddress) (uint64, error) {
	var result hexutil.Uint64
	err := ec.c.CallContext(ctx, &result, "quai_getTransactionCount", account.Original(), "pending")
	return uint64(result), err
}

// PendingTransactionCount returns the total number of transactions in the pending state.
func (ec *Client) PendingTransactionCount(ctx context.Context) (uint, error) {
	var num hexutil.Uint
	err := ec.c.CallContext(ctx, &num, "eth_getBlockTransactionCountByNumber", "pending")
	return uint(num), err
}

// GetPendingHeader gets the latest pending header from the chain.
func (ec *Client) GetPendingHeader(ctx context.Context) (*types.WorkObject, error) {
	var pendingHeader *types.WorkObject
	err := ec.c.CallContext(ctx, &pendingHeader, "quai_getPendingHeader")
	if err != nil {
		return nil, err
	}
	return pendingHeader, nil
}

// Contract Calling

// CallContract executes a message call transaction, which is directly executed in the VM
// of the node, but never mined into the blockchain.
//
// blockNumber selects the block height at which the call runs. It can be nil, in which
// case the code is taken from the latest known block. Note that state from very old
// blocks might not be available.
func (ec *Client) CallContract(ctx context.Context, msg quai.CallMsg, blockNumber *big.Int) ([]byte, error) {
	var hex hexutil.Bytes
	err := ec.c.CallContext(ctx, &hex, "quai_call", toCallArg(msg), toBlockNumArg(blockNumber))
	if err != nil {
		return nil, err
	}
	return hex, nil
}

// PendingCallContract executes a message call transaction using the EVM.
// The state seen by the contract call is the pending state.
func (ec *Client) PendingCallContract(ctx context.Context, msg quai.CallMsg) ([]byte, error) {
	var hex hexutil.Bytes
	err := ec.c.CallContext(ctx, &hex, "eth_call", toCallArg(msg), "pending")
	if err != nil {
		return nil, err
	}
	return hex, nil
}

// SuggestGasPrice retrieves the currently suggested gas price to allow a timely
// execution of a transaction.
func (ec *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	var hex hexutil.Big
	if err := ec.c.CallContext(ctx, &hex, "quai_gasPrice"); err != nil {
		return nil, err
	}
	return (*big.Int)(&hex), nil
}

// EstimateGas tries to estimate the gas needed to execute a specific transaction based on
// the current pending state of the backend blockchain. There is no guarantee that this is
// the true gas limit requirement as other transactions may be added or removed by miners,
// but it should provide a basis for setting a reasonable default.
func (ec *Client) EstimateGas(ctx context.Context, msg quai.CallMsg) (uint64, error) {
	var hex hexutil.Uint64
	err := ec.c.CallContext(ctx, &hex, "quai_estimateGas", toCallArg(msg))
	if err != nil {
		return 0, err
	}
	return uint64(hex), nil
}

type TransactionArgs struct {
	From                 *common.Address `json:"from"`
	To                   *common.Address `json:"to"`
	Gas                  *hexutil.Uint64 `json:"gas"`
	GasPrice             *hexutil.Big    `json:"gasPrice"`
	MaxFeePerGas         *hexutil.Big    `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *hexutil.Big    `json:"maxPriorityFeePerGas"`
	Value                *hexutil.Big    `json:"value"`
	Nonce                *hexutil.Uint64 `json:"nonce"`

	// We accept "data" and "input" for backwards-compatibility reasons.
	// "input" is the newer name and should be preferred by clients.
	Data  *hexutil.Bytes `json:"data"`
	Input *hexutil.Bytes `json:"input"`

	// Introduced by AccessListTxType transaction.
	AccessList *types.AccessList `json:"accessList,omitempty"`
	ChainID    *hexutil.Big      `json:"chainId,omitempty"`

	// Support for Qi (UTXO) transaction
	TxIn   types.TxIns  `json:"txIn,omitempty"`
	TxOut  types.TxOuts `json:"txOut,omitempty"`
	TxType uint8        `json:"txType,omitempty"`
}

// EstimateGas tries to estimate the gas needed to execute a specific transaction based on
// the current pending state of the backend blockchain. There is no guarantee that this is
// the true gas limit requirement as other transactions may be added or removed by miners,
// but it should provide a basis for setting a reasonable default.
func (ec *Client) EstimateFeeForQi(ctx context.Context, tx *types.Transaction) (*big.Int, error) {
	msg := quai.CallMsg{
		TxType: tx.Type(),
		TxIn:   tx.TxIn(),
		TxOut:  tx.TxOut(),
		Data:   tx.Data(),
	}
	var result hexutil.Big
	err := ec.c.CallContext(ctx, &result, "quai_estimateFeeForQi", toCallArg(msg))
	if err != nil {
		return nil, err
	}
	return (*big.Int)(&result), nil
}

func (ec *Client) GetLatestUTXOSetSize(ctx context.Context) (uint64, error) {
	var result hexutil.Uint64
	err := ec.c.CallContext(ctx, &result, "quai_getLatestUTXOSetSize")
	if err != nil {
		return 0, err
	}
	return uint64(result), nil
}

// AccessListResult returns an optional accesslist
// Its the result of the `quai_createAccessList` RPC call.
// It contains an error if the transaction itself failed.
type AccessListResult struct {
	Accesslist *types.AccessList `json:"accessList"`
	Error      string            `json:"error,omitempty"`
	GasUsed    hexutil.Uint64    `json:"gasUsed"`
}

// CreateAccessList creates an access list for a transaction.
func (ec *Client) CreateAccessList(ctx context.Context, msg quai.CallMsg) (AccessListResult, error) {
	var accessList AccessListResult
	err := ec.c.CallContext(ctx, &accessList, "quai_createAccessList", toCallArg(msg))
	if err != nil {
		return accessList, err
	}
	return accessList, nil
}

// SendTransaction injects a signed transaction into the pending pool for execution.
//
// If the transaction was a contract creation use the TransactionReceipt method to get the
// contract address after the transaction has been mined.
func (ec *Client) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	protoTx, err := tx.ProtoEncode()
	if err != nil {
		return err
	}
	data, err := proto.Marshal(protoTx)
	if err != nil {
		return err
	}
	return ec.c.CallContext(ctx, nil, "quai_sendRawTransaction", hexutil.Encode(data))
}

func toBlockNumArg(number *big.Int) string {
	if number == nil {
		return "latest"
	}
	pending := big.NewInt(-1)
	if number.Cmp(pending) == 0 {
		return "pending"
	}
	return hexutil.EncodeBig(number)
}

func toCallArg(msg quai.CallMsg) interface{} {
	arg := map[string]interface{}{
		"from": msg.From,
		"to":   msg.To,
	}
	if len(msg.Data) > 0 {
		arg["data"] = hexutil.Bytes(msg.Data)
	}
	if msg.Value != nil {
		arg["value"] = (*hexutil.Big)(msg.Value)
	}
	if msg.Gas != 0 {
		arg["gas"] = hexutil.Uint64(msg.Gas)
	}
	if msg.GasPrice != nil {
		arg["gasPrice"] = (*hexutil.Big)(msg.GasPrice)
	}
	if msg.TxType != 0 {
		arg["txType"] = msg.TxType
	}
	txIns := make([]types.RPCTxIn, 0, len(msg.TxIn))
	txOuts := make([]types.RPCTxOut, 0, len(msg.TxOut))
	for _, txin := range msg.TxIn {
		txIns = append(txIns, types.RPCTxIn{PreviousOutPoint: types.OutpointJSON{TxHash: txin.PreviousOutPoint.TxHash, Index: hexutil.Uint64(txin.PreviousOutPoint.Index)}, PubKey: hexutil.Bytes(txin.PubKey)})
	}
	for _, txout := range msg.TxOut {
		txOuts = append(txOuts, types.RPCTxOut{Denomination: hexutil.Uint(txout.Denomination), Address: common.BytesToAddress(txout.Address, common.Location{0, 0}).MixedcaseAddress(), Lock: (*hexutil.Big)(txout.Lock)})
	}
	if msg.TxIn != nil {
		arg["txIn"] = txIns
	}
	if msg.TxOut != nil {
		arg["txOut"] = txOuts
	}
	return arg
}

func (ec *Client) TxPoolStatus(ctx context.Context) (map[string]hexutil.Uint, error) {
	var result map[string]hexutil.Uint
	err := ec.c.CallContext(ctx, &result, "txpool_status")
	return result, err
}
