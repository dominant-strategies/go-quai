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
	"math"
	"math/big"
	"time"

	quai "github.com/spruce-solutions/go-quai"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/rpc"
)

var exponentialBackoffCeilingSecs int64 = 60 // 1 minute

// Client defines typed wrappers for the Ethereum RPC API.
type Client struct {
	c *rpc.Client
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
		delaySecs := int64(math.Floor((math.Pow(2, float64(attempts)) - 1) * 0.5))
		if delaySecs > exponentialBackoffCeilingSecs {
			return nil, err
		}

		// should only get here if the ffmpeg record stream process dies
		log.Warn("Attempting to connect to dominant go-quai node. Waiting and retrying...", "attempts", attempts, "delay", delaySecs)

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

// GetBlockStatus returns the status of the block for a given header
func (ec *Client) GetBlockStatus(ctx context.Context, header *types.Header) WriteStatus {
	var blockStatus WriteStatus
	if err := ec.c.CallContext(ctx, &blockStatus, "quai_getBlockStatus", header); err != nil {
		return NonStatTy
	}
	return blockStatus
}

// SubscribeNewHead subscribes to notifications about the current blockchain head
// on the given channel.
func (ec *Client) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (quai.Subscription, error) {
	return ec.c.EthSubscribe(ctx, ch, "newHeads")
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
