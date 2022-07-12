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
	"math"
	"time"

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
