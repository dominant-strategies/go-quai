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

// Package ethclient provides a client for the Ethereum RPC API.
package quaiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	quai "github.com/spruce-solutions/go-quai"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/common/hexutil"
	"github.com/spruce-solutions/go-quai/common/math"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/rpc"
)

var exponentialBackoffCeilingSecs int64 = 60 // 1 minute

// Client defines typed wrappers for the Ethereum RPC API.
type Client struct {
	c *rpc.Client
}

type rpcHeaderWithOrder struct {
	tx *types.Transaction
	txExtraInfo
}

type rpcBlock struct {
	Hash         common.Hash      `json:"hash"`
	Transactions []rpcTransaction `json:"transactions"`
	Uncles       []*types.Header  `json:"uncles"`
}

type rpcReceiptBlock struct {
	Hash         common.Hash      `json:"hash"`
	Transactions []rpcTransaction `json:"transactions"`
	Uncles       []*types.Header  `json:"uncles"`
	Receipts     []*types.Receipt `json:"receipts"`
}

type rpcExternalBlock struct {
	Hash         common.Hash      `json:"hash"`
	Transactions []rpcTransaction `json:"transactions"`
	Uncles       []*types.Header  `json:"uncles"`
	Receipts     []*types.Receipt `json:"receipts"`
	Context      *big.Int         `json:"context:`
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

// Dial connects a client to the given URL.
func Dial(rawurl string) (*Client, error) {
	return DialContext(context.Background(), rawurl)
}

func DialContext(ctx context.Context, rawurl string) (*Client, error) {
	connectStatus := false
	attempts := 0

	var c *rpc.Client
	var err error
	for !connectStatus {
		c, err = rpc.DialContext(ctx, rawurl)
		if err == nil {
			break
		}

		attempts += 1
		// exponential back-off implemented
		// delaySecs := int64(math.Floor((math.Pow(2, float64(attempts)) - 1) * 0.5))
		delaySecs := int64(1)
		if delaySecs > exponentialBackoffCeilingSecs {
			return nil, err
		}

		// should only get here if the ffmpeg record stream process dies
		log.Warn("Attempting to connect to go-quai node. Waiting and retrying...", "attempts", attempts, "delay", delaySecs)

		time.Sleep(time.Duration(delaySecs) * time.Second)
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

// WriteStatus status of write
type WriteStatus byte

const (
	NonStatTy WriteStatus = iota
	CanonStatTy
	SideStatTy
	UnknownStatTy
)

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

func (ec *Client) HLCRReorg(ctx context.Context, block *types.Block) (bool, error) {
	var domReorgNeeded bool
	data, err := RPCMarshalBlock(block, true, true)
	if err != nil {
		return false, err
	}
	if err := ec.c.CallContext(ctx, &domReorgNeeded, "quai_hLCRReorg", data); err != nil {
		return false, err
	}
	return domReorgNeeded, nil
}

// SubscribeNewHead subscribes to notifications about the current blockchain head
// on the given channel.
func (ec *Client) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (quai.Subscription, error) {
	return ec.c.EthSubscribe(ctx, ch, "newHeads")
}

// BlockByHash returns the given full block.
//
// Note that loading full blocks requires two requests. Use HeaderByHash
// if you don't need all transactions or uncle headers.
func (ec *Client) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	return ec.getBlock(ctx, "quai_getBlockByHash", hash, true)
}

// GetBlockReceipts returns the receipts of a block by block hash.
func (ec *Client) GetBlockReceipts(ctx context.Context, blockHash common.Hash) (*types.ReceiptBlock, error) {
	return ec.getBlockWithReceipts(ctx, "quai_getBlockWithReceiptsByHash", blockHash)
}

// HeaderByNumber returns a block header from the current canonical chain. If number is
// nil, the latest known header is returned.
func (ec *Client) GetCanonicalHashByNumber(ctx context.Context, number *big.Int) common.Hash {
	var hash common.Hash
	err := ec.c.CallContext(ctx, &hash, "quai_getCanonicalHashByNumber", toBlockNumArg(number))
	if err != nil {
		return common.Hash{}
	}
	return hash
}

// SendMinedBlock sends a mined block back to the node
func (ec *Client) SendMinedBlock(ctx context.Context, block *types.Block, inclTx bool, fullTx bool) error {
	data, err := RPCMarshalBlock(block, inclTx, fullTx)
	if err != nil {
		return err
	}
	return ec.c.CallContext(ctx, nil, "quai_sendMinedBlock", data)
}

func (ec *Client) GetAncestorByLocation(ctx context.Context, hash common.Hash, location []byte) (*types.Header, error) {
	data := map[string]interface{}{"Hash": hash}
	data["Location"] = location

	var header *types.Header
	if err := ec.c.CallContext(ctx, &header, "quai_getAncestorByLocation", data); err != nil {
		return nil, err
	}
	return header, nil
}

// GetExternalBlockByHashAndContext searches the cache for external block
func (ec *Client) GetSubordinateSet(ctx context.Context, hash common.Hash, location []byte) ([]common.Hash, error) {
	data := map[string]interface{}{"Hash": hash}
	data["Location"] = location

	var hashes []common.Hash
	if err := ec.c.CallContext(ctx, &hashes, "quai_getSubordinateSet", data); err != nil {
		return nil, err
	}
	return hashes, nil
}

// GetExternalBlockByHashAndContext searches the cache for external block
func (ec *Client) GetExternalBlockByHashAndContext(ctx context.Context, hash common.Hash, context int) (*types.ExternalBlock, error) {
	data := map[string]interface{}{"Hash": hash}
	data["Context"] = context
	return ec.getExternalBlock(ctx, "quai_getExternalBlockByHashAndContext", data)
}

// GetTerminusAtOrder retrieves subordinate validity and terminus hash for a header and order
func (ec *Client) GetTerminusAtOrder(ctx context.Context, header *types.Header, order int) (common.Hash, error) {
	data := map[string]interface{}{"Header": RPCMarshalHeader(header)}
	data["Order"] = order

	var hash common.Hash
	if err := ec.c.CallContext(ctx, &hash, "quai_getTerminusAtOrder", data); err != nil {
		return common.Hash{}, err
	}
	return hash, nil
}

// CheckPCRC runs PCRC on the node with a given header
func (ec *Client) CheckPCRC(ctx context.Context, block *types.Block, order int) (types.PCRCTermini, error) {
	data, err := RPCMarshalOrderBlock(block, order)
	if err != nil {
		return types.PCRCTermini{}, err
	}

	fmt.Println("check pcrc is crashing: ", block.Hash(), order, data)
	var PCRCTermini types.PCRCTermini
	if err := ec.c.CallContext(ctx, &PCRCTermini, "quai_checkPCRC", data); err != nil {
		return types.PCRCTermini{}, err
	}
	return PCRCTermini, nil
}

func (ec *Client) getExternalBlock(ctx context.Context, method string, args ...interface{}) (*types.ExternalBlock, error) {
	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, method, args...)
	if err != nil {
		return nil, err
	} else if len(raw) == 0 {
		return nil, quai.NotFound
	}
	// Decode header and transactions.
	var head *types.Header
	var body rpcExternalBlock
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	// Quick-verify transaction and uncle lists. This mostly helps with debugging the server.
	if types.IsEqualHashSlice(head.UncleHash, types.EmptyUncleHash) && len(body.Uncles) > 0 {
		return nil, fmt.Errorf("server returned non-empty uncle list but block header indicates no uncles")
	}
	if types.IsEqualHashSlice(head.TxHash, types.EmptyRootHash) && len(body.Transactions) > 0 {
		return nil, fmt.Errorf("server returned non-empty transaction list but block header indicates no transactions")
	}
	// Load uncles because they are not included in the block response.
	uncles := make([]*types.Header, len(body.Uncles))
	for i, uncle := range body.Uncles {
		uncles[i] = uncle
	}

	// Fill the sender cache of transactions in the block.
	txs := make([]*types.Transaction, len(body.Transactions))
	for i, tx := range body.Transactions {
		if tx.From != nil {
			setSenderFromServer(tx.tx, *tx.From, body.Hash)
		}
		txs[i] = tx.tx
	}
	receipts := make([]*types.Receipt, len(body.Receipts))
	for i, receipt := range body.Receipts {
		receipts[i] = receipt
	}
	return types.NewExternalBlockWithHeader(head).WithBody(txs, uncles, receipts, body.Context), nil
}

// RPCMarshalHeader converts the given header to the RPC output .
func RPCMarshalHeader(head *types.Header) map[string]interface{} {
	result := map[string]interface{}{
		"number":            head.Number,
		"hash":              head.Hash(),
		"parentHash":        head.ParentHash,
		"nonce":             head.Nonce,
		"sha3Uncles":        head.UncleHash,
		"logsBloom":         head.Bloom,
		"stateRoot":         head.Root,
		"miner":             head.Coinbase,
		"difficulty":        head.Difficulty,
		"networkDifficulty": head.NetworkDifficulty,
		"extraData":         head.Extra,
		"size":              hexutil.Uint64(head.Size()),
		"gasLimit":          head.GasLimit,
		"gasUsed":           head.GasUsed,
		"timestamp":         head.Time,
		"transactionsRoot":  head.TxHash,
		"receiptsRoot":      head.ReceiptHash,
		"location":          head.Location,
	}

	if head.BaseFee != nil {
		result["baseFeePerGas"] = head.BaseFee
	}

	return result
}

// RPCMarshalOrderBlock converts the block and order as input to PCRC.
func RPCMarshalOrderBlock(block *types.Block, Order int) (map[string]interface{}, error) {
	fields, err := RPCMarshalBlock(block, true, true)
	if err != nil {
		return nil, err
	}
	fields["order"] = Order
	return fields, nil
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
	}

	fields["uncles"] = block.Uncles()

	return fields, nil
}

func (ec *Client) getBlock(ctx context.Context, method string, args ...interface{}) (*types.Block, error) {
	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, method, args...)
	if err != nil {
		return nil, err
	} else if len(raw) == 0 {
		return nil, quai.NotFound
	}
	// Decode header and transactions.
	var head *types.Header
	var body rpcBlock
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	// Quick-verify transaction and uncle lists. This mostly helps with debugging the server.
	if types.IsEqualHashSlice(head.UncleHash, types.EmptyUncleHash) && len(body.Uncles) > 0 {
		return nil, fmt.Errorf("server returned non-empty uncle list but block header indicates no uncles")
	}
	if types.IsEqualHashSlice(head.TxHash, types.EmptyRootHash) && len(body.Transactions) > 0 {
		return nil, fmt.Errorf("server returned non-empty transaction list but block header indicates no transactions")
	}
	// Load uncles.
	uncles := body.Uncles

	// Fill the sender cache of transactions in the block.
	txs := make([]*types.Transaction, len(body.Transactions))
	for i, tx := range body.Transactions {
		if tx.From != nil {
			setSenderFromServer(tx.tx, *tx.From, body.Hash)
		}
		txs[i] = tx.tx
	}
	return types.NewBlockWithHeader(head).WithBody(txs, uncles), nil
}

func (ec *Client) getBlockWithReceipts(ctx context.Context, method string, args ...interface{}) (*types.ReceiptBlock, error) {
	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, method, args...)
	if err != nil {
		return nil, err
	} else if len(raw) == 0 {
		return nil, quai.NotFound
	}
	// Decode header and transactions.
	var head *types.Header
	var body rpcReceiptBlock
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	// Quick-verify transaction and uncle lists. This mostly helps with debugging the server.
	if types.IsEqualHashSlice(head.UncleHash, types.EmptyUncleHash) && len(body.Uncles) > 0 {
		return nil, fmt.Errorf("server returned non-empty uncle list but block header indicates no uncles")
	}
	if types.IsEqualHashSlice(head.TxHash, types.EmptyRootHash) && len(body.Transactions) > 0 {
		return nil, fmt.Errorf("server returned non-empty transaction list but block header indicates no transactions")
	}
	// Load uncles because they are not included in the block response.
	uncles := body.Uncles

	// Fill the sender cache of transactions in the block.
	txs := make([]*types.Transaction, len(body.Transactions))
	for i, tx := range body.Transactions {
		if tx.From != nil {
			setSenderFromServer(tx.tx, *tx.From, body.Hash)
		}
		txs[i] = tx.tx
	}
	receipts := make([]*types.Receipt, len(body.Receipts))
	for i, receipt := range body.Receipts {
		receipts[i] = receipt
	}
	return types.NewReceiptBlockWithHeader(head).WithBody(txs, uncles, receipts), nil
}

// RPCTransaction represents a transaction that will serialize to the RPC representation of a transaction
type RPCTransaction struct {
	BlockHash        *common.Hash      `json:"blockHash"`
	BlockNumber      *hexutil.Big      `json:"blockNumber"`
	From             common.Address    `json:"from"`
	Gas              hexutil.Uint64    `json:"gas"`
	GasPrice         *hexutil.Big      `json:"gasPrice"`
	GasFeeCap        *hexutil.Big      `json:"maxFeePerGas,omitempty"`
	GasTipCap        *hexutil.Big      `json:"maxPriorityFeePerGas,omitempty"`
	Hash             common.Hash       `json:"hash"`
	Input            hexutil.Bytes     `json:"input"`
	Nonce            hexutil.Uint64    `json:"nonce"`
	To               *common.Address   `json:"to"`
	TransactionIndex *hexutil.Uint64   `json:"transactionIndex"`
	Value            *hexutil.Big      `json:"value"`
	Type             hexutil.Uint64    `json:"type"`
	Accesses         *types.AccessList `json:"accessList,omitempty"`
	ChainID          *hexutil.Big      `json:"chainId,omitempty"`
	V                *hexutil.Big      `json:"v"`
	R                *hexutil.Big      `json:"r"`
	S                *hexutil.Big      `json:"s"`
}

// newRPCTransaction returns a transaction that will serialize to the RPC
// representation, with the given location metadata set (if available).
func newRPCTransaction(tx *types.Transaction, blockHash common.Hash, blockNumber uint64, index uint64, baseFee *big.Int) *RPCTransaction {
	// Determine the signer. For replay-protected transactions, use the most permissive
	// signer, because we assume that signers are backwards-compatible with old
	// transactions. For non-protected transactions, the homestead signer signer is used
	// because the return value of ChainId is zero for those transactions.
	var signer types.Signer
	if tx.Protected() {
		signer = types.LatestSignerForChainID(tx.ChainId())
	} else {
		signer = types.HomesteadSigner{}
	}
	from, _ := types.Sender(signer, tx)
	v, r, s := tx.RawSignatureValues()
	result := &RPCTransaction{
		Type:     hexutil.Uint64(tx.Type()),
		From:     from,
		Gas:      hexutil.Uint64(tx.Gas()),
		GasPrice: (*hexutil.Big)(tx.GasPrice()),
		Hash:     tx.Hash(),
		Input:    hexutil.Bytes(tx.Data()),
		Nonce:    hexutil.Uint64(tx.Nonce()),
		To:       tx.To(),
		Value:    (*hexutil.Big)(tx.Value()),
		V:        (*hexutil.Big)(v),
		R:        (*hexutil.Big)(r),
		S:        (*hexutil.Big)(s),
	}
	if blockHash != (common.Hash{}) {
		result.BlockHash = &blockHash
		result.BlockNumber = (*hexutil.Big)(new(big.Int).SetUint64(blockNumber))
		result.TransactionIndex = (*hexutil.Uint64)(&index)
	}
	switch tx.Type() {
	case types.AccessListTxType:
		al := tx.AccessList()
		result.Accesses = &al
		result.ChainID = (*hexutil.Big)(tx.ChainId())
	case types.DynamicFeeTxType:
		al := tx.AccessList()
		result.Accesses = &al
		result.ChainID = (*hexutil.Big)(tx.ChainId())
		result.GasFeeCap = (*hexutil.Big)(tx.GasFeeCap())
		result.GasTipCap = (*hexutil.Big)(tx.GasTipCap())
		// if the transaction has been mined, compute the effective gas price
		if baseFee != nil && blockHash != (common.Hash{}) {
			// price = min(tip, gasFeeCap - baseFee) + baseFee
			price := math.BigMin(new(big.Int).Add(tx.GasTipCap(), baseFee), tx.GasFeeCap())
			result.GasPrice = (*hexutil.Big)(price)
		} else {
			result.GasPrice = (*hexutil.Big)(tx.GasFeeCap())
		}
	}
	return result
}

// newRPCTransactionFromBlockIndex returns a transaction that will serialize to the RPC representation.
func newRPCTransactionFromBlockIndex(b *types.Block, index uint64) *RPCTransaction {
	txs := b.Transactions()
	if index >= uint64(len(txs)) {
		return nil
	}
	return newRPCTransaction(txs[index], b.Hash(), b.NumberU64(), index, b.BaseFee())
}

// newRPCRawTransactionFromBlockIndex returns the bytes of a transaction given a block and a transaction index.
func newRPCRawTransactionFromBlockIndex(b *types.Block, index uint64) hexutil.Bytes {
	txs := b.Transactions()
	if index >= uint64(len(txs)) {
		return nil
	}
	blob, _ := txs[index].MarshalBinary()
	return blob
}

// newRPCTransactionFromBlockHash returns a transaction that will serialize to the RPC representation.
func newRPCTransactionFromBlockHash(b *types.Block, hash common.Hash) *RPCTransaction {
	for idx, tx := range b.Transactions() {
		if tx.Hash() == hash {
			return newRPCTransactionFromBlockIndex(b, uint64(idx))
		}
	}
	return nil
}

// CalcTd calculates the total difficulty for a block
func (ec *Client) CalcTd(ctx context.Context, header *types.Header) ([]*big.Int, error) {
	var td []*big.Int
	err := ec.c.CallContext(ctx, &td, "quai_calcTd", header)
	return td, err
}
