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
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
	"google.golang.org/protobuf/proto"
)

var exponentialBackoffCeilingSecs int64 = 60 // 1 minute

// Client defines typed wrappers for the Quai RPC API.
type Client struct {
	c *rpc.Client
}

// Dial connects a client to the given URL.
func Dial(rawurl string, logger *log.Logger) (*Client, error) {
	return DialContext(context.Background(), rawurl, logger)
}

func DialContext(ctx context.Context, rawurl string, logger *log.Logger) (*Client, error) {
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
		delaySecs := int64(attempts) * 5
		if delaySecs > exponentialBackoffCeilingSecs {
			return nil, err
		}

		// should only get here if the ffmpeg record stream process dies
		logger.WithFields(log.Fields{
			"attempts": attempts,
			"delay":    delaySecs,
			"url":      rawurl,
			"err":      err,
		}).Warn("Attempting to connect to go-quai node. Waiting and retrying...")

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

// SubscribePendingHeader subscribes to notifications about the current pending block on the node.
func (ec *Client) SubscribePendingHeader(ctx context.Context, powId types.PowID, ch chan<- []byte) (quai.Subscription, error) {
	return ec.c.QuaiSubscribe(ctx, ch, "pendingHeader", powId)
}

// SubscribeNewWorkshares subscribes to notifications about new workshares received via P2P.
func (ec *Client) SubscribeNewWorkshares(ctx context.Context, ch chan<- *types.WorkObject) (quai.Subscription, error) {
	return ec.c.QuaiSubscribe(ctx, ch, "newWorkshares")
}

// SubscribeNewHead subscribes to notifications about the current blockchain head
// on the given channel.
func (ec *Client) SubscribeNewHead(ctx context.Context, ch chan<- *types.WorkObject) (quai.Subscription, error) {
	return ec.c.QuaiSubscribe(ctx, ch, "newHeads")
}

// SubscribeNewWorkshares subscribes to notifications about new workshares received via P2P.
func (ec *Client) SubscribeNewWorksharesV2(ctx context.Context, ch chan<- *types.WorkObject) (quai.Subscription, error) {
	return ec.c.QuaiSubscribe(ctx, ch, "newWorksharesV2")
}

// SubscribeNewHead subscribes to notifications about the current blockchain head
// on the given channel.
func (ec *Client) SubscribeNewHeadV2(ctx context.Context, ch chan<- *types.WorkObject) (quai.Subscription, error) {
	return ec.c.QuaiSubscribe(ctx, ch, "newHeadsV2")
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

//// Miner APIS

// GetPendingHeader gets the latest pending header from the chain.
func (ec *Client) GetPendingHeader(ctx context.Context, powId types.PowID) (*types.WorkObject, error) {
	var raw hexutil.Bytes
	err := ec.c.CallContext(ctx, &raw, "quai_getPendingHeader", powId)
	if err != nil {
		return nil, err
	}
	protoWo := &types.ProtoWorkObject{}
	err = proto.Unmarshal(raw, protoWo)
	if err != nil {
		return nil, err
	}
	wo := &types.WorkObject{}
	err = wo.ProtoDecode(protoWo, protoWo.GetWoHeader().GetLocation().Value, types.PEtxObject)
	if err != nil {
		return nil, err
	}
	return wo, nil
}

func (ec *Client) GetWorkShareThreshold(ctx context.Context) (int, error) {
	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, "workshare_getWorkShareThreshold")
	if err != nil {
		return -1, err
	}

	var threshold int
	if err := json.Unmarshal(raw, &threshold); err != nil {
		return -1, err
	}
	return threshold, nil
}

// ReceiveMinedHeader sends a mined block back to the node
func (ec *Client) ReceiveMinedHeader(ctx context.Context, header *types.WorkObject) error {
	protoWo, err := header.ProtoEncode(types.PEtxObject)
	if err != nil {
		return err
	}
	data, err := proto.Marshal(protoWo)
	if err != nil {
		return err
	}
	return ec.c.CallContext(ctx, nil, "quai_receiveMinedHeader", hexutil.Bytes(data))
}

func (ec *Client) ReceiveWorkShare(ctx context.Context, header *types.WorkObjectHeader) error {
	protoWs, err := header.ProtoEncode()
	if err != nil {
		return err
	}
	data, err := proto.Marshal(protoWs)
	if err != nil {
		return err
	}
	return ec.c.CallContext(ctx, nil, "quai_receiveRawWorkShare", hexutil.Bytes(data))
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
	if msg.AccessList != nil {
		arg["accessList"] = msg.AccessList
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

// BaseFee returns the base fee for a tx to be included in the next block.
// If txType is set to "true" returns the Quai base fee in units of Wei.
// If txType is set to "false" returns the Qi base fee in units of Qit.
func (ec *Client) BaseFee(ctx context.Context, txType bool) (*big.Int, error) {
	var hex hexutil.Big
	err := ec.c.CallContext(ctx, &hex, "quai_baseFee", txType)
	if err != nil {
		return nil, err
	}
	return (*big.Int)(&hex), nil
}

// QiRateAtBlock returns the number of Quai needed for a Qi at a given block number or hash.
func (ec *Client) QiRateAtBlock(ctx context.Context, block interface{}) (*big.Int, error) {
	var hex hexutil.Big
	err := ec.c.CallContext(ctx, &hex, "quai_qiRateAtBlock", block)
	if err != nil {
		return nil, err
	}
	return (*big.Int)(&hex), nil
}

// QuaiRateAtBlock returns the number of Qi needed for a Quai at a given block number or hash.
func (ec *Client) QuaiRateAtBlock(ctx context.Context, block interface{}) (*big.Int, error) {
	var hex hexutil.Big
	err := ec.c.CallContext(ctx, &hex, "quai_quaiRateAtBlock", block)
	if err != nil {
		return nil, err
	}
	return (*big.Int)(&hex), nil
}

/// TxPool

func (ec *Client) TxPoolStatus(ctx context.Context) (map[string]hexutil.Uint, error) {
	var result map[string]hexutil.Uint
	err := ec.c.CallContext(ctx, &result, "txpool_status")
	return result, err
}

// Submits a minimally worked workshare to the client node
func (ec *Client) SubmitSubWorkshare(ctx context.Context, wo *types.WorkObject) error {
	protoWo, err := wo.ProtoEncode(types.WorkShareTxObject)
	if err != nil {
		log.Global.WithField("err", err).Error("Unable to encode worked transaction")
		return err
	}
	bytesWo, err := proto.Marshal(protoWo)
	if err != nil {
		log.Global.WithField("err", err).Error("Unable to marshal worked transaction")
		return err
	}
	return ec.c.CallContext(ctx, nil, "workshare_receiveSubWorkshare", hexutil.Bytes(bytesWo))
}

// Submits an AuxTemplate from a subsidy chain
// SignAuxTemplate requests MuSig2 partial signature from go-quai
func (ec *Client) SignAuxTemplate(ctx context.Context, chainID string, templateData hexutil.Bytes, otherParticipantPubKey hexutil.Bytes, subsidyNonce hexutil.Bytes) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := ec.c.CallContext(ctx, &result, "workshare_signAuxTemplate", chainID, templateData, otherParticipantPubKey, subsidyNonce)
	return result, err
}

func (ec *Client) SubmitAuxTemplate(ctx context.Context, chainID string, auxTemplate *types.AuxTemplate) error {
	protoTemplate := auxTemplate.ProtoEncode()
	bytesTemplate, err := proto.Marshal(protoTemplate)
	if err != nil {
		return fmt.Errorf("unable to marshal AuxTemplate: %w", err)
	}
	return ec.c.CallContext(ctx, nil, "workshare_submitAuxTemplate", chainID, hexutil.Bytes(bytesTemplate))
}

func (ec *Client) CalcOrder(ctx context.Context, header *types.WorkObject) (int, error) {
	protoWo, err := header.ProtoEncode(types.PEtxObject)
	if err != nil {
		return -1, err
	}
	data, err := proto.Marshal(protoWo)
	if err != nil {
		return -1, err
	}
	var result hexutil.Uint
	err = ec.c.CallContext(ctx, &result, "quai_calcOrder", hexutil.Bytes(data))
	if err != nil {
		return -1, err
	}
	return int(result), nil
}

// SendTransactionToPoolSharingClient injects a signed transaction into the pending pool for execution.
//
// If the transaction was a contract creation use the TransactionReceipt method to get the
// contract address after the transaction has been mined.
func (ec *Client) SendTransactionToPoolSharingClient(ctx context.Context, tx *types.Transaction) error {
	protoTx, err := tx.ProtoEncode()
	if err != nil {
		return err
	}
	data, err := proto.Marshal(protoTx)
	if err != nil {
		return err
	}
	return ec.c.CallContext(ctx, nil, "quai_receiveTxFromPoolSharingClient", hexutil.Encode(data))
}

func (ec *Client) GetWorkShareP2PThreshold(ctx context.Context) uint64 {
	var threshold hexutil.Uint64
	err := ec.c.CallContext(ctx, &threshold, "quai_getWorkShareP2PThreshold")
	if err != nil {
		return uint64(params.WorkSharesThresholdDiff)
	}
	return uint64(threshold)
}

// GetWorkshareLRUDump returns a dump of all workshares in the LRU caches.
// The limit parameter caps the number of entries returned per list (0 means no limit).
func (ec *Client) GetWorkshareLRUDump(ctx context.Context, limit int) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := ec.c.CallContext(ctx, &result, "debug_getWorkshareLRUDump", limit)
	return result, err
}
