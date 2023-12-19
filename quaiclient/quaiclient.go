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
package quaiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	quai "github.com/dominant-strategies/go-quai"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
)

var exponentialBackoffCeilingSecs int64 = 60 // 1 minute

// Client defines typed wrappers for the Quai RPC API.
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
		// delaySecs := int64(math.Floor((math.Pow(2, float64(attempts)) - 1) * 0.5))
		delaySecs := int64(1)
		if delaySecs > exponentialBackoffCeilingSecs {
			return nil, err
		}

		// should only get here if the ffmpeg record stream process dies
		log.Warn("Attempting to connect to go-quai node. Waiting and retrying...", "attempts", attempts, "delay", delaySecs, "url", rawurl)

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

type Termini struct {
	Termini []common.Hash `json:"termini"`
}

type appendReturns struct {
	Etxs     types.Transactions `json:"pendingEtxs"`
	SubReorg bool               `json:"subReorg"`
	SetHead  bool               `json:"setHead"`
}

// SubscribePendingHeader subscribes to notifications about the current pending block on the node.
func (ec *Client) SubscribePendingHeader(ctx context.Context, ch chan<- *types.Header) (quai.Subscription, error) {
	return ec.c.QuaiSubscribe(ctx, ch, "pendingHeader")
}

func (ec *Client) Append(ctx context.Context, header *types.Header, manifest types.BlockManifest, domPendingHeader *types.Header, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, bool, bool, error) {
	fields := map[string]interface{}{
		"header":           header.RPCMarshalHeader(),
		"manifest":         manifest,
		"domPendingHeader": domPendingHeader.RPCMarshalHeader(),
		"domTerminus":      domTerminus,
		"domOrigin":        domOrigin,
		"newInboundEtxs":   newInboundEtxs,
	}

	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, "quai_append", fields)
	if err != nil {
		return nil, false, false, err
	}

	// Decode header and transactions.
	var aReturns appendReturns
	if err := json.Unmarshal(raw, &aReturns); err != nil {
		return nil, false, false, err
	}

	return aReturns.Etxs, aReturns.SubReorg, aReturns.SetHead, nil
}

func (ec *Client) DownloadBlocksInManifest(ctx context.Context, hash common.Hash, manifest types.BlockManifest, entropy *big.Int) {
	fields := map[string]interface{}{
		"hash":     hash,
		"manifest": manifest,
		"entropy":  entropy,
	}
	ec.c.CallContext(ctx, nil, "quai_downloadBlocksInManifest", fields)
}

func (ec *Client) SubRelayPendingHeader(ctx context.Context, pendingHeader types.PendingHeader, newEntropy *big.Int, location common.Location, subReorg bool, order int) {
	data := map[string]interface{}{"header": pendingHeader.Header().RPCMarshalHeader()}
	data["NewEntropy"] = newEntropy
	data["termini"] = pendingHeader.Termini().RPCMarshalTermini()
	data["Location"] = location
	data["SubReorg"] = subReorg
	data["Order"] = order

	ec.c.CallContext(ctx, nil, "quai_subRelayPendingHeader", data)
}

func (ec *Client) UpdateDom(ctx context.Context, oldTerminus common.Hash, pendingHeader types.PendingHeader, location common.Location) {
	data := map[string]interface{}{"header": pendingHeader.Header().RPCMarshalHeader()}
	data["OldTerminus"] = oldTerminus
	data["Location"] = location
	data["termini"] = pendingHeader.Termini().RPCMarshalTermini()

	ec.c.CallContext(ctx, nil, "quai_updateDom", data)
}

func (ec *Client) RequestDomToAppendOrFetch(ctx context.Context, hash common.Hash, entropy *big.Int, order int) {
	data := map[string]interface{}{"Hash": hash}
	data["Entropy"] = entropy
	data["Order"] = order

	ec.c.CallContext(ctx, nil, "quai_requestDomToAppendOrFetch", data)
}

func (ec *Client) NewGenesisPendingHeader(ctx context.Context, header *types.Header) {
	ec.c.CallContext(ctx, nil, "quai_newGenesisPendingHeader", header.RPCMarshalHeader())
}

// GetManifest will get the block manifest ending with the parent hash
func (ec *Client) GetManifest(ctx context.Context, blockHash common.Hash) (types.BlockManifest, error) {
	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, "quai_getManifest", blockHash)
	if err != nil {
		return nil, err
	}
	var manifest types.BlockManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// GetPendingEtxsRollupFromSub gets the pendingEtxsRollup from the region
func (ec *Client) GetPendingEtxsRollupFromSub(ctx context.Context, hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	fields := make(map[string]interface{})
	fields["Hash"] = hash
	fields["Location"] = location

	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, "quai_getPendingEtxsRollupFromSub", fields)
	if err != nil {
		return types.PendingEtxsRollup{}, err
	}

	var pEtxsRollup types.PendingEtxsRollup
	if err := json.Unmarshal(raw, &pEtxsRollup); err != nil {
		return types.PendingEtxsRollup{}, err
	}
	return pEtxsRollup, nil
}

// GetPendingEtxsFromSub gets the pendingEtxsRollup from the region
func (ec *Client) GetPendingEtxsFromSub(ctx context.Context, hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	fields := make(map[string]interface{})
	fields["Hash"] = hash
	fields["Location"] = location

	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, "quai_getPendingEtxsFromSub", fields)
	if err != nil {
		return types.PendingEtxs{}, err
	}

	var pEtxs types.PendingEtxs
	if err := json.Unmarshal(raw, &pEtxs); err != nil {
		return types.PendingEtxs{}, err
	}
	return pEtxs, nil
}

func (ec *Client) SendPendingEtxsToDom(ctx context.Context, pEtxs types.PendingEtxs) error {
	fields := make(map[string]interface{})
	fields["header"] = pEtxs.Header.RPCMarshalHeader()
	fields["etxs"] = pEtxs.Etxs
	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, "quai_sendPendingEtxsToDom", fields)
	if err != nil {
		return err
	}
	return nil
}

func (ec *Client) SendPendingEtxsRollupToDom(ctx context.Context, pEtxsRollup types.PendingEtxsRollup) error {
	fields := make(map[string]interface{})
	fields["header"] = pEtxsRollup.Header.RPCMarshalHeader()
	fields["manifest"] = pEtxsRollup.Manifest
	var raw json.RawMessage
	return ec.c.CallContext(ctx, &raw, "quai_sendPendingEtxsRollupToDom", fields)
}

func (ec *Client) GenerateRecoveryPendingHeader(ctx context.Context, pendingHeader *types.Header, checkpointHashes types.Termini) error {
	fields := make(map[string]interface{})
	fields["pendingHeader"] = pendingHeader.RPCMarshalHeader()
	fields["checkpointHashes"] = checkpointHashes.RPCMarshalTermini()
	return ec.c.CallContext(ctx, nil, "quai_generateRecoveryPendingHeader", fields)
}

func (ec *Client) HeaderByHash(ctx context.Context, hash common.Hash) *types.Header {
	var raw json.RawMessage
	ec.c.CallContext(ctx, &raw, "quai_getHeaderByHash", hash)
	var header *types.Header
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil
	}
	return header
}

func (ec *Client) HeaderByNumber(ctx context.Context, number string) *types.Header {
	var raw json.RawMessage
	ec.c.CallContext(ctx, &raw, "quai_getHeaderByNumber", number)
	var header *types.Header
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil
	}
	return header
}

func (ec *Client) SetSyncTarget(ctx context.Context, header *types.Header) {
	fields := header.RPCMarshalHeader()
	ec.c.CallContext(ctx, nil, "quai_setSyncTarget", fields)
}

//// Miner APIS

// GetPendingHeader gets the latest pending header from the chain.
func (ec *Client) GetPendingHeader(ctx context.Context) (*types.Header, error) {
	var pendingHeader *types.Header
	err := ec.c.CallContext(ctx, &pendingHeader, "quai_getPendingHeader")
	if err != nil {
		return nil, err
	}
	return pendingHeader, nil
}

// ReceiveMinedHeader sends a mined block back to the node
func (ec *Client) ReceiveMinedHeader(ctx context.Context, header *types.Header) error {
	data := header.RPCMarshalHeader()
	return ec.c.CallContext(ctx, nil, "quai_receiveMinedHeader", data)
}

// Filters

// SubscribeFilterLogs subscribes to the results of a streaming filter query.
func (ec *Client) SubscribeFilterLogs(ctx context.Context, q quai.FilterQuery, ch chan<- types.Log) (quai.Subscription, error) {
	arg, err := toFilterArg(q)
	if err != nil {
		return nil, err
	}
	return ec.c.QuaiSubscribe(ctx, ch, "logs", arg)
}

func toFilterArg(q quai.FilterQuery) (interface{}, error) {
	arg := map[string]interface{}{
		"address": q.Addresses,
		"topics":  q.Topics,
	}
	if q.BlockHash != nil {
		arg["blockHash"] = *q.BlockHash
		if q.FromBlock != nil || q.ToBlock != nil {
			return nil, fmt.Errorf("cannot specify both BlockHash and FromBlock/ToBlock")
		}
	} else {
		if q.FromBlock == nil {
			arg["fromBlock"] = "0x0"
		} else {
			arg["fromBlock"] = toBlockNumArg(q.FromBlock)
		}
		arg["toBlock"] = toBlockNumArg(q.ToBlock)
	}
	return arg, nil
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
	return arg
}

// FilterLogs executes a filter query.
func (ec *Client) FilterLogs(ctx context.Context, q quai.FilterQuery) ([]types.Log, error) {
	var result []types.Log
	arg, err := toFilterArg(q)
	if err != nil {
		return nil, err
	}
	err = ec.c.CallContext(ctx, &result, "quai_getLogs", arg)
	return result, err
}
