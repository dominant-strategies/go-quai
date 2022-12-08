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

package eth

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/eth/protocols/eth"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/enode"
)

const (
	MaxBlockFetchDist = 50
)

// ethHandler implements the eth.Backend interface to handle the various network
// packets that are sent as replies or broadcasts.
type ethHandler handler

func (h *ethHandler) Core() *core.Core   { return h.core }
func (h *ethHandler) TxPool() eth.TxPool { return h.txpool }

// RunPeer is invoked when a peer joins on the `eth` protocol.
func (h *ethHandler) RunPeer(peer *eth.Peer, hand eth.Handler) error {
	return (*handler)(h).runEthPeer(peer, hand)
}

// PeerInfo retrieves all known `eth` information about a peer.
func (h *ethHandler) PeerInfo(id enode.ID) interface{} {
	if p := h.peers.peer(id.String()); p != nil {
		return p.info()
	}
	return nil
}

// AcceptTxs retrieves whether transaction processing is enabled on the node
// or if inbound transactions should simply be dropped.
func (h *ethHandler) AcceptTxs() bool {
	return atomic.LoadUint32(&h.acceptTxs) == 1
}

// Handle is invoked from a peer's message handler when it receives a new remote
// message that the handler couldn't consume and serve itself.
func (h *ethHandler) Handle(peer *eth.Peer, packet eth.Packet) error {
	// Consume any broadcasts and announces, forwarding the rest to the downloader
	switch packet := packet.(type) {
	case *eth.BlockHeadersPacket:
		return h.handleHeaders(peer, *packet)

	case *eth.BlockBodiesPacket:
		txset, uncleset := packet.Unpack()
		return h.handleBodies(peer, txset, uncleset)

	case *eth.ReceiptsPacket:
		if err := h.downloader.DeliverReceipts(peer.ID(), *packet); err != nil {
			log.Debug("Failed to deliver receipts", "err", err)
		}
		return nil

	case *eth.NewBlockHashesPacket:
		hashes, numbers := packet.Unpack()
		return h.handleBlockAnnounces(peer, hashes, numbers)

	case *eth.NewBlockPacket:
		return h.handleBlockBroadcast(peer, packet.Block)

	case *eth.NewPooledTransactionHashesPacket:
		return h.txFetcher.Notify(peer.ID(), *packet)

	case *eth.TransactionsPacket:
		return h.txFetcher.Enqueue(peer.ID(), *packet, false)

	case *eth.PooledTransactionsPacket:
		return h.txFetcher.Enqueue(peer.ID(), *packet, true)

	default:
		return fmt.Errorf("unexpected eth packet type: %T", packet)
	}
}

// handleHeaders is invoked from a peer's message handler when it transmits a batch
// of headers for the local node to process.
func (h *ethHandler) handleHeaders(peer *eth.Peer, headers []*types.Header) error {
	p := h.peers.peer(peer.ID())
	if p == nil {
		return errors.New("unregistered during callback")
	}
	// If no headers were received, but we're expencting a checkpoint header, consider it that
	if len(headers) == 0 && p.syncDrop != nil {
		// Stop the timer either way, decide later to drop or not
		p.syncDrop.Stop()
		p.syncDrop = nil
	}
	// Filter out any explicitly requested headers, deliver the rest to the downloader
	filter := len(headers) == 1
	if filter {
		// Otherwise if it's a whitelisted block, validate against the set
		if want, ok := h.whitelist[headers[0].Number().Uint64()]; ok {
			if hash := headers[0].Hash(); want != hash {
				peer.Log().Info("Whitelist mismatch, dropping peer", "number", headers[0].Number().Uint64(), "hash", hash, "want", want)
				return errors.New("whitelist block mismatch")
			}
			peer.Log().Debug("Whitelist block verified", "number", headers[0].Number().Uint64(), "hash", want)
		}
		// Irrelevant of the fork checks, send the header to the fetcher just in case
		headers = h.blockFetcher.FilterHeaders(peer.ID(), headers, time.Now())
	}
	if len(headers) > 0 || !filter {
		err := h.downloader.DeliverHeaders(peer.ID(), headers)
		if err != nil {
			log.Debug("Failed to deliver headers", "err", err)
		}
	}
	return nil
}

// handleBodies is invoked from a peer's message handler when it transmits a batch
// of block bodies for the local node to process.
func (h *ethHandler) handleBodies(peer *eth.Peer, txs [][]*types.Transaction, uncles [][]*types.Header) error {
	// Filter out any explicitly requested bodies, deliver the rest to the downloader
	filter := len(txs) > 0 || len(uncles) > 0
	if filter {
		txs, uncles = h.blockFetcher.FilterBodies(peer.ID(), txs, uncles, time.Now())
	}
	if len(txs) > 0 || len(uncles) > 0 || !filter {
		err := h.downloader.DeliverBodies(peer.ID(), txs, uncles)
		if err != nil {
			log.Debug("Failed to deliver bodies", "err", err)
		}
	}
	return nil
}

// handleBlockAnnounces is invoked from a peer's message handler when it transmits a
// batch of block announcements for the local node to process.
func (h *ethHandler) handleBlockAnnounces(peer *eth.Peer, hashes []common.Hash, numbers []uint64) error {
	// Schedule all the unknown hashes for retrieval
	var (
		unknownHashes  = make([]common.Hash, 0, len(hashes))
		unknownNumbers = make([]uint64, 0, len(numbers))
	)
	for i := 0; i < len(hashes); i++ {
		if !h.core.HasBlock(hashes[i], numbers[i]) {
			unknownHashes = append(unknownHashes, hashes[i])
			unknownNumbers = append(unknownNumbers, numbers[i])
		}
	}
	for i := 0; i < len(unknownHashes); i++ {
		h.blockFetcher.Notify(peer.ID(), unknownHashes[i], unknownNumbers[i], time.Now(), peer.RequestOneHeader, peer.RequestBodies)
	}
	return nil
}

// handleBlockBroadcast is invoked from a peer's message handler when it transmits a
// block broadcast for the local node to process.
func (h *ethHandler) handleBlockBroadcast(peer *eth.Peer, block *types.Block) error {
	// Schedule the block for import
	h.blockFetcher.Enqueue(peer.ID(), block)

	// Get the horizon and if we don't have the parent of the block and it has a greater
	// number than the horizon we ask for that parent.
	if !h.core.HasBlock(block.ParentHash(), block.NumberU64()-1) {
		horizon := h.core.GetHorizon()
		if block.NumberU64() > horizon && block.NumberU64() < h.core.CurrentBlock().NumberU64()+MaxBlockFetchDist {
			peer.RequestBlockByHash(block.ParentHash())
		}
	}

	_, number := peer.Head()
	if (block.NumberU64() - 1) > number {
		peer.SetHead(block.ParentHash(), number)
		h.chainSync.handlePeerEvent(peer)
	}
	return nil
}
