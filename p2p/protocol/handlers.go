package protocol

import (
	"math/big"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/trie"
	"github.com/pkg/errors"
)

// Seeks the block in the cache or database and sends it to the peer in a pb.QuaiResponseMessage
func handleBlockRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode) error {
	log.Global.Tracef("looking up block %s for location %s", hash, loc.Name())
	// check if we have the block in our cache or database
	block := node.GetBlock(hash, loc)
	if block == nil {
		log.Global.Debugf("block %s not found for location %s", hash, loc.Name())
		return nil
	}
	log.Global.Debugf("block found %s for location %s", block.Hash(), loc.Name())
	// create a Quai Message Response with the block
	data, err := pb.EncodeQuaiResponse(id, loc, block)
	if err != nil {
		return err
	}
	err = common.WriteMessageToStream(stream, data)
	if err != nil {
		return err
	}
	log.Global.WithFields(log.Fields{
		"blockHash": block.Hash(),
		"peer":      stream.Conn().RemotePeer(),
	}).Trace("Sent block to peer")
	return nil
}

// Seeks the header in the cache or database and sends it to the peer in a pb.QuaiResponseMessage
func handleHeaderRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode) error {
	header := node.GetHeader(hash, loc)
	if header == nil {
		log.Global.Debugf("header not found")
		// TODO: handle header not found
		return nil
	}
	log.Global.Debugf("header found %s", header.Hash())
	// create a Quai Message Response with the header
	data, err := pb.EncodeQuaiResponse(id, loc, header)
	if err != nil {
		return err
	}
	err = common.WriteMessageToStream(stream, data)
	if err != nil {
		return err
	}
	log.Global.Debugf("Sent header %s to peer %s", header.Hash(), stream.Conn().RemotePeer())
	return nil
}

func handleTransactionRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode) error {
	panic("TODO: implement")
}

// Seeks the block in the cache or database and sends it to the peer in a pb.QuaiResponseMessage
func handleBlockNumberRequest(id uint32, loc common.Location, number *big.Int, stream network.Stream, node QuaiP2PNode) error {
	// check if we have the block in our cache or database
	blockHash := node.GetBlockHashByNumber(number, loc)
	if blockHash == nil {
		log.Global.Tracef("block not found")
		return nil
	}
	log.Global.Tracef("block found %s", blockHash)
	// create a Quai Message Response with the block
	data, err := pb.EncodeQuaiResponse(id, loc, blockHash)
	if err != nil {
		return err
	}

	err = common.WriteMessageToStream(stream, data)
	if err != nil {
		return err
	}
	log.Global.Tracef("Sent block hash %s to peer %s", blockHash, stream.Conn().RemotePeer())
	return nil
}

func handleTrieNodeRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode) error {
	log.Global.Tracef("looking up trie node %s from location %s", hash, loc.Name())
	trieNode, err := node.GetTrieNode(hash, loc)
	if err != nil {
		return errors.Wrapf(err, "error getting trie node for hash %s", hash)
	}

	if trieNode == nil {
		return errors.Errorf("trie node not found for hash %s", hash)
	}
	log.Global.Tracef("trie node found")

	trieNodeResp := &trie.TrieNodeResponse{
		NodeHash: hash,
		NodeData: trieNode,
	}

	data, err := pb.EncodeQuaiResponse(id, loc, trieNodeResp)
	if err != nil {
		return errors.Wrapf(err, "error encoding trie node response")
	}
	log.Global.Tracef("Sending trie node to peer %s", stream.Conn().RemotePeer())
	err = common.WriteMessageToStream(stream, data)
	if err != nil {
		return errors.Wrapf(err, "error writing trie node response to stream")
	}
	log.Global.Tracef("Sent trie node to peer %s", stream.Conn().RemotePeer())
	return nil
}
