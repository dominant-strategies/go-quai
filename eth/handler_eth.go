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
	// Cannot Handshake with a peer before finishing the bad hashes cleanup
	if h.core.BadHashExistsInChain() {
		log.Warn("Bad Hashes still exist on chain, cannot handshake with any peer yet")
		return nil
	}
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
		txset, uncleset, etxset, manifestset := packet.Unpack()
		return h.handleBodies(peer, txset, uncleset, etxset, manifestset)

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

	case *eth.PendingEtxsPacket:
		return h.handlePendingEtxs(*&packet.PendingEtxs)

	case *eth.PendingEtxsRollupPacket:
		return h.handlePendingEtxsRollup(peer, *&packet.PendingEtxsRollup)

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
func (h *ethHandler) handleBodies(peer *eth.Peer, txs [][]*types.Transaction, uncles [][]*types.Header, etxs [][]*types.Transaction, manifest []types.BlockManifest) error {
	// Filter out any explicitly requested bodies, deliver the rest to the downloader
	filter := len(txs) > 0 || len(uncles) > 0 || len(etxs) > 0 || len(manifest) > 0
	if filter {
		txs, uncles, etxs, manifest = h.blockFetcher.FilterBodies(peer.ID(), txs, uncles, etxs, manifest, time.Now())
	}
	if len(txs) > 0 || len(uncles) > 0 || len(etxs) > 0 || len(manifest) > 0 || !filter {
		err := h.downloader.DeliverBodies(peer.ID(), txs, uncles, etxs, manifest)
		if err != nil {
			log.Debug("Failed to deliver bodies", "err", err)
		}
	}
	return nil
}

// handleBlockAnnounces is invoked from a peer's message handler when it transmits a
// batch of block announcements for the local node to process.
func (h *ethHandler) handleBlockAnnounces(peer *eth.Peer, hashes []common.Hash, numbers []uint64) error {
	// Do not handle any broadcast until we finish resetting from the bad state.
	// This should be a very small time window
	if h.Core().BadHashExistsInChain() {
		log.Warn("Bad Hashes still exist on chain, cannot listen to Block Hash announcements yet")
		return nil
	}
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
	// Do not handle any broadcast until we finish resetting from the bad state.
	// This should be a very small time window
	if h.Core().BadHashExistsInChain() {
		log.Warn("Bad Hashes still exist on chain, cannot handle block broadcast yet")
		return nil
	}
	// Schedule the block for import
	h.blockFetcher.Enqueue(peer.ID(), block)

	if block != nil && !h.broadcastCache.Contains(block.Hash()) {
		log.Info("Received Block Broadcast", "Hash", block.Hash(), "Number", block.Header().NumberArray())
		h.broadcastCache.Add(block.Hash(), true)
	}

	blockS := h.core.TotalLogS(block.Header())
	_, _, peerEntropy, _ := peer.Head()
	if blockS != nil && peerEntropy != nil {
		if peerEntropy.Cmp(blockS) < 0 {
			log.Info("Starting the downloader: Peer entropy is less than the announced entropy", "peer Entropy", peerEntropy, "announced block entropy", blockS)
			peer.SetHead(block.Hash(), block.Number(), blockS, block.ReceivedAt)
			h.chainSync.handlePeerEvent(peer)
		}
	}
	return nil
}

func (h *ethHandler) handlePendingEtxs(pendingEtxs types.PendingEtxs) error {
	// err := h.core.AddPendingEtxs(pendingEtxs)
	// if err != nil {
	// 	log.Error("Error in handling pendingEtxs broadcast", "err", err)
	// 	return err
	// }
	return nil
}

func (h *ethHandler) handlePendingEtxsRollup(peer *eth.Peer, pEtxsRollup types.PendingEtxsRollup) error {
	// err := h.core.AddPendingEtxsRollup(pEtxsRollup)
	// if err != nil {
	// 	log.Error("Error in handling pendingEtxs rollup broadcast", "err", err)
	// 	return err
	// }
	// // For each hash in manifest request for the pendingEtxs
	// for _, hash := range pEtxsRollup.Manifest {
	// 	peer.RequestOnePendingEtxs(hash)
	// }
	// return nil
	return nil
}
