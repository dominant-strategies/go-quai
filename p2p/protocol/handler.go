package protocol

import (
	"errors"
	"io"
	"math/big"
	"os"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/trie"
)

// QuaiProtocolHandler handles all the incoming requests and responds with corresponding data
func QuaiProtocolHandler(stream network.Stream, node QuaiP2PNode) {
	defer stream.Close()

	log.Global.Debugf("Received a new stream from %s", stream.Conn().RemotePeer())

	// if there is a protocol mismatch, close the stream
	if stream.Protocol() != ProtocolVersion {
		log.Global.Warnf("Invalid protocol: %s", stream.Protocol())
		// TODO: add logic to drop the peer
		return
	}

	// Enter the read loop for the stream and handle messages
	for {
		data, err := common.ReadMessageFromStream(stream)
		if err != nil {
			if errors.Is(err, network.ErrReset) || errors.Is(err, io.EOF) || errors.Is(err, os.ErrDeadlineExceeded) {
				return
			}

			log.Global.Errorf("error reading message from stream: %s", err)
			// TODO: handle error
			continue
		}
		id, decodedType, loc, query, err := pb.DecodeQuaiRequest(data)
		if err != nil {
			log.Global.Errorf("error decoding quai request: %s", err)
			// TODO: handle error
			continue
		}
		switch query.(type) {
		case *common.Hash:
			log.Global.Debugf("Received request id: %d for %T, location %v hash %s from peer %s", id, decodedType, loc, query, stream.Conn().RemotePeer())
		case *big.Int:
			log.Global.Debugf("Received request id: %d for %T, location %v number %s from peer %s", id, decodedType, loc, query, stream.Conn().RemotePeer())
		default:
			log.Global.Errorf("unsupported request input data field type: %T", query)
		}

		switch decodedType.(type) {
		case *types.Block:
			requestedHash := query.(*common.Hash)
			err = handleBlockRequest(id, loc, *requestedHash, stream, node)
			if err != nil {
				log.Global.Errorf("error handling block request: %s", err)
				// TODO: handle error
				continue
			}
		case *types.Header:
			requestedHash := query.(*common.Hash)
			err = handleHeaderRequest(id, loc, *requestedHash, stream, node)
			if err != nil {
				log.Global.Errorf("error handling header request: %s", err)
				// TODO: handle error
				continue
			}
		case *types.Transaction:
			requestedHash := query.(*common.Hash)
			err = handleTransactionRequest(id, loc, *requestedHash, stream, node)
			if err != nil {
				log.Global.Errorf("error handling transaction request: %s", err)
				// TODO: handle error
				continue
			}
		case *common.Hash:
			number := query.(*big.Int)
			err = handleBlockNumberRequest(id, loc, number, stream, node)
			if err != nil {
				log.Global.Errorf("error handling block number request: %s", err)
				continue
			}
		case trie.TrieNodeRequest:
			requestedHash := query.(*common.Hash)
			err := handleTrieNodeRequest(id, loc, *requestedHash, stream, node)
			if err != nil {
				log.Global.Errorf("error handling trie node request: %s", err)
			}
		default:
			log.Global.Errorf("unsupported request data type: %T", decodedType)
			// TODO: handle error
			continue

		}
	}
	log.Global.Tracef("Exiting Quai Protocol Handler")
}

// Seeks the block in the cache or database and sends it to the peer in a pb.QuaiResponseMessage
func handleBlockRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode) error {
	// check if we have the block in our cache or database
	block := node.GetBlock(hash, loc)
	if block == nil {
		log.Global.Debugf("block not found")
		return nil
	}
	log.Global.Debugf("block found %s", block.Hash())
	// create a Quai Message Response with the block
	data, err := pb.EncodeQuaiResponse(id, block)
	if err != nil {
		return err
	}
	err = common.WriteMessageToStream(stream, data)
	if err != nil {
		return err
	}
	log.Global.Debugf("Sent block %s to peer %s", block.Hash(), stream.Conn().RemotePeer())
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
	data, err := pb.EncodeQuaiResponse(id, header)
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
	data, err := pb.EncodeQuaiResponse(id, blockHash)
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
	trieNode := node.GetTrieNode(hash, loc)
	if trieNode == nil {
		log.Global.Tracef("trie node not found")
		return nil
	}
	log.Global.Tracef("trie node found")
	data, err := pb.EncodeQuaiResponse(id, trieNode)
	if err != nil {
		return err
	}
	err = common.WriteMessageToStream(stream, data)
	if err != nil {
		return err
	}
	log.Global.Tracef("Sent trie node to peer %s", stream.Conn().RemotePeer())
	return nil
}
