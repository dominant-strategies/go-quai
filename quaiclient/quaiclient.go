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
	"math/big"
	"time"

	"github.com/dominant-strategies/go-quai/common"
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

func (ec *Client) DownloadBlocksInManifest(ctx context.Context, manifest types.BlockManifest, entropy *big.Int) {
	fields := map[string]interface{}{
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
