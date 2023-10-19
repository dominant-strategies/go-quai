// Copyright 2020 The go-ethereum Authors
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
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/trie"
)

// handleGetBlockHeaders66 is the eth/66 version of handleGetBlockHeaders
func handleGetBlockHeaders66(backend Backend, msg Decoder, peer *Peer) error {
	// Decode the complex header query
	var query GetBlockHeadersPacket66
	if err := msg.Decode(&query); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	response := answerGetBlockHeadersQuery(backend, query.GetBlockHeadersPacket, peer)
	return peer.ReplyBlockHeaders(query.RequestId, response)
}

func answerGetBlockHeadersQuery(backend Backend, query *GetBlockHeadersPacket, peer *Peer) []*types.Header {
	hashMode := query.Origin.Hash != (common.Hash{})
	first := true
	maxNonCanonical := uint64(100)

	// Gather headers until the fetch or network limits is reached
	var (
		bytes   common.StorageSize
		headers []*types.Header
		unknown bool
	)
	for !unknown && len(headers) < int(query.Amount) && bytes < softResponseLimit &&
		len(headers) < maxHeadersServe {
		// Retrieve the next header satisfying the query
		var origin *types.Header
		if hashMode {
			if first {
				first = false
				origin = backend.Core().GetHeaderOrCandidateByHash(query.Origin.Hash)
				if origin != nil {
					query.Origin.Number = origin.NumberU64()
				}
			} else {
				origin = backend.Core().GetHeaderOrCandidate(query.Origin.Hash, query.Origin.Number)
			}
		} else {
			origin = backend.Core().GetHeaderByNumber(query.Origin.Number)
		}
		if origin == nil {
			break
		}

		// If dom is true only append header to results array if it is a dominant header
		if query.Dom {
			if backend.Core().Engine().IsDomCoincident(backend.Core(), origin) {
				headers = append(headers, origin)
				bytes += estHeaderSize
			}
		} else {
			headers = append(headers, origin)
			bytes += estHeaderSize
			// If dom is false always append header to results array and break when dominant header is found
			if backend.Core().Engine().IsDomCoincident(backend.Core(), origin) {
				break
			}
		}

		// If the to number is reached stop the search
		// By default the To is 0 and if its value is specified we need to stop
		if query.To >= query.Origin.Number && query.Reverse {
			break
		}

		// Advance to the next header of the query
		switch {
		case hashMode && query.Reverse:
			query.Origin.Hash, query.Origin.Number = backend.Core().GetAncestor(query.Origin.Hash, query.Origin.Number, query.Skip, &maxNonCanonical)
			unknown = (query.Origin.Hash == common.Hash{})
		case hashMode && !query.Reverse:
			unknown = true
		case query.Reverse:
			query.Origin.Number -= query.Skip
		case !query.Reverse:
			query.Origin.Number += query.Skip
		}
	}

	return headers
}

func handleGetBlockBodies66(backend Backend, msg Decoder, peer *Peer) error {
	// Decode the block body retrieval message
	var query GetBlockBodiesPacket66
	if err := msg.Decode(&query); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	response := answerGetBlockBodiesQuery(backend, query.GetBlockBodiesPacket, peer)
	return peer.ReplyBlockBodiesRLP(query.RequestId, response)
}

func answerGetBlockBodiesQuery(backend Backend, query GetBlockBodiesPacket, peer *Peer) []rlp.RawValue {
	// Gather blocks until the fetch or network limits is reached
	var (
		bytes  int
		bodies []rlp.RawValue
	)
	for _, hash := range query {
		if bytes >= softResponseLimit || len(bodies) >= maxBodiesServe {
			break
		}
		if data := backend.Core().GetBodyRLP(hash); len(data) != 0 {
			bodies = append(bodies, data)
			bytes += len(data)
		}
	}
	return bodies
}

func handleGetBlock66(backend Backend, msg Decoder, peer *Peer) error {
	// Decode the block retrieval message
	var query GetBlockPacket66
	if err := msg.Decode(&query); err != nil {
		fmt.Println("Error decoding the message", err)
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	log.Debug("Got a block fetch request eth/66: ", "Hash", query.Hash)
	// check if we have the requested block in the database.
	response := backend.Core().GetBlockOrCandidateByHash(query.Hash)
	if response != nil {
		currentHead := backend.Core().CurrentHeader()
		entropy := big.NewInt(0)
		if currentHead != nil {
			entropy = backend.Core().Engine().TotalLogS(currentHead)
		}
		return peer.SendNewBlock(response, entropy, false)
	}
	return nil
}

func handleNewBlockhashes(backend Backend, msg Decoder, peer *Peer) error {
	// A batch of new block announcements just arrived
	ann := new(NewBlockHashesPacket)
	if err := msg.Decode(ann); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	// Mark the hashes as present at the remote node
	for _, block := range *ann {
		peer.markBlock(block.Hash)
	}
	// Deliver them all to the backend for queuing
	return backend.Handle(peer, ann)
}

func handleNewBlock(backend Backend, msg Decoder, peer *Peer) error {
	nodeCtx := common.NodeLocation.Context()
	// Retrieve and decode the propagated block
	ann := new(NewBlockPacket)
	if err := msg.Decode(ann); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	if err := ann.sanityCheck(); err != nil {
		return err
	}
	// Making sure that the region and prime chains have zero txs and etxs in them
	if nodeCtx == common.ZONE_CTX {
		if hash := types.CalcUncleHash(ann.Block.Uncles()); hash != ann.Block.UncleHash() {
			log.Warn("Propagated block has invalid uncles", "have", hash, "exp", ann.Block.UncleHash())
			return nil // TODO: return error eventually, but wait a few releases
		}
		if hash := types.DeriveSha(ann.Block.Transactions(), trie.NewStackTrie(nil)); hash != ann.Block.TxHash() {
			log.Warn("Propagated block has invalid transaction", "have", hash, "exp", ann.Block.TxHash())
			return nil // TODO: return error eventually, but wait a few releases
		}
		if hash := types.DeriveSha(ann.Block.ExtTransactions(), trie.NewStackTrie(nil)); hash != ann.Block.EtxHash() {
			log.Warn("Propagated block has invalid external transaction", "have", hash, "exp", ann.Block.EtxHash())
			return nil // TODO: return error eventually, but wait a few releases
		}
	} else {
		if len(ann.Block.Transactions()) != 0 {
			log.Warn("Propagated block has transactions in the body", "len", len(ann.Block.Transactions()))
		}
		if len(ann.Block.ExtTransactions()) != 0 {
			log.Warn("Propagated block has ext transactions in the body", "len", len(ann.Block.ExtTransactions()))
		}
		if len(ann.Block.Uncles()) != 0 {
			log.Warn("Propagated block has uncles in the body", "len", len(ann.Block.Uncles()))
		}
		// Dom nodes need to validate the subordinate manifest against the subordinate's manifesthash
		if hash := types.DeriveSha(ann.Block.SubManifest(), trie.NewStackTrie(nil)); hash != ann.Block.ManifestHash(nodeCtx+1) {
			log.Warn("Propagated block has invalid subordinate manifest", "peer", peer.id, "block hash", ann.Block.Hash(), "have", hash, "exp", ann.Block.ManifestHash())
			return nil
		}
	}
	ann.Block.ReceivedAt = msg.Time()
	ann.Block.ReceivedFrom = peer

	// Mark the peer as owning the block
	peer.markBlock(ann.Block.Hash())

	return backend.Handle(peer, ann)
}

func handleBlockHeaders66(backend Backend, msg Decoder, peer *Peer) error {
	// A batch of headers arrived to one of our previous requests
	res := new(BlockHeadersPacket66)
	if err := msg.Decode(res); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	requestTracker.Fulfil(peer.id, peer.version, BlockHeadersMsg, res.RequestId)

	return backend.Handle(peer, &res.BlockHeadersPacket)
}

func handleBlockBodies66(backend Backend, msg Decoder, peer *Peer) error {
	// A batch of block bodies arrived to one of our previous requests
	res := new(BlockBodiesPacket66)
	if err := msg.Decode(res); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	requestTracker.Fulfil(peer.id, peer.version, BlockBodiesMsg, res.RequestId)

	return backend.Handle(peer, &res.BlockBodiesPacket)
}

func handleNewPooledTransactionHashes(backend Backend, msg Decoder, peer *Peer) error {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return errors.New("transactions are only handled in zone")
	}
	if !backend.Core().Slice().ProcessingState() {
		return nil
	}
	// New transaction announcement arrived, make sure we have
	// a valid and fresh chain to handle them
	if !backend.AcceptTxs() {
		return nil
	}
	ann := new(NewPooledTransactionHashesPacket)
	if err := msg.Decode(ann); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	// Schedule all the unknown hashes for retrieval
	for _, hash := range *ann {
		peer.markTransaction(hash)
	}
	return backend.Handle(peer, ann)
}

func handleGetPooledTransactions66(backend Backend, msg Decoder, peer *Peer) error {
	// Decode the pooled transactions retrieval message
	var query GetPooledTransactionsPacket66
	if err := msg.Decode(&query); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	hashes, txs := answerGetPooledTransactions(backend, query.GetPooledTransactionsPacket, peer)
	return peer.ReplyPooledTransactionsRLP(query.RequestId, hashes, txs)
}

func answerGetPooledTransactions(backend Backend, query GetPooledTransactionsPacket, peer *Peer) ([]common.Hash, []rlp.RawValue) {
	// Gather transactions until the fetch or network limits is reached
	var (
		bytes  int
		hashes []common.Hash
		txs    []rlp.RawValue
	)
	for _, hash := range query {
		if bytes >= softResponseLimit {
			break
		}
		// Retrieve the requested transaction, skipping if unknown to us
		tx := backend.TxPool().Get(hash)
		if tx == nil {
			continue
		}
		// If known, encode and queue for response packet
		if encoded, err := rlp.EncodeToBytes(tx); err != nil {
			log.Error("Failed to encode transaction", "err", err)
		} else {
			hashes = append(hashes, hash)
			txs = append(txs, encoded)
			bytes += len(encoded)
		}
	}
	return hashes, txs
}

func handleTransactions(backend Backend, msg Decoder, peer *Peer) error {
	nodeCtx := common.NodeLocation.Context()
	// Transactions arrived, make sure we have a valid and fresh chain to handle them
	if nodeCtx != common.ZONE_CTX {
		return errors.New("transactions are only handled in zone")
	}
	if !backend.Core().Slice().ProcessingState() {
		return nil
	}
	if !backend.AcceptTxs() {
		return nil
	}
	// Transactions can be processed, parse all of them and deliver to the pool
	var txs TransactionsPacket
	if err := msg.Decode(&txs); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	for i, tx := range txs {
		// Validate and mark the remote transaction
		if tx == nil {
			return fmt.Errorf("%w: transaction %d is nil", errDecode, i)
		}
		peer.markTransaction(tx.Hash())
	}
	return backend.Handle(peer, &txs)
}

func handlePooledTransactions66(backend Backend, msg Decoder, peer *Peer) error {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return errors.New("transactions are only handled in zone")
	}
	if !backend.Core().Slice().ProcessingState() {
		return nil
	}
	// Transactions arrived, make sure we have a valid and fresh chain to handle them
	if !backend.AcceptTxs() {
		return nil
	}
	// Transactions can be processed, parse all of them and deliver to the pool
	var txs PooledTransactionsPacket66
	if err := msg.Decode(&txs); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	for i, tx := range txs.PooledTransactionsPacket {
		// Validate and mark the remote transaction
		if tx == nil {
			return fmt.Errorf("%w: transaction %d is nil", errDecode, i)
		}
		peer.markTransaction(tx.Hash())
	}
	requestTracker.Fulfil(peer.id, peer.version, PooledTransactionsMsg, txs.RequestId)

	return backend.Handle(peer, &txs.PooledTransactionsPacket)
}
