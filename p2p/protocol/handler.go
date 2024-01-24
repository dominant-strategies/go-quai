package protocol

import (
	"errors"
	"io"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/libp2p/go-libp2p/core/network"
)

func QuaiProtocolHandler(stream network.Stream, node QuaiP2PNode) {
	defer stream.Close()

	log.Debugf("Received a new stream from %s", stream.Conn().RemotePeer())

	// if there is a protocol mismatch, close the stream
	if stream.Protocol() != ProtocolVersion {
		log.Warnf("Invalid protocol: %s", stream.Protocol())
		// TODO: add logic to drop the peer
		return
	}

	// Enter the read loop for the stream and handle messages
	for {
		data, err := common.ReadMessageFromStream(stream)
		if err != nil {
			if errors.Is(err, io.EOF) {
				log.Debugf("stream closed by peer %s", stream.Conn().RemotePeer())
				break
			}

			log.Errorf("error reading message from stream: %s", err)
			// TODO: handle error
			continue
		}
		id, decodedType, loc, hash, err := pb.DecodeQuaiRequest(data)
		if err != nil {
			log.Errorf("error decoding quai request: %s", err)
			// TODO: handle error
			continue
		}
		log.Debugf("Received request id: %d for %T, location %v hash %s from peer %s", id, decodedType, loc, hash, stream.Conn().RemotePeer())

		switch decodedType.(type) {
		case *types.Block:
			err = handleBlockRequest(id, loc, hash, stream, node)
			if err != nil {
				log.Errorf("error handling block request: %s", err)
				// TODO: handle error
				continue
			}
		case *types.Header:
			err = handleHeaderRequest(id, loc, hash, stream, node)
			if err != nil {
				log.Errorf("error handling header request: %s", err)
				// TODO: handle error
				continue
			}
		case *types.Transaction:
			err = handleTransactionRequest(id, loc, hash, stream, node)
			if err != nil {
				log.Errorf("error handling transaction request: %s", err)
				// TODO: handle error
				continue
			}
		default:
			log.Errorf("unsupported request data type: %T", decodedType)
			// TODO: handle error
			continue

		}
	}
	log.Tracef("Exiting Quai Protocol Handler")
}

// Seeks the block in the cache or database and sends it to the peer in a pb.QuaiResponseMessage
func handleBlockRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode) error {
	// check if we have the block in our cache or database
	block := node.GetBlock(hash, loc)
	if block == nil {
		log.Debugf("block not found")
		// TODO: handle block not found
	}
	log.Debugf("block found %s", block.Hash())
	// create a Quai Message Response with the block
	data, err := pb.EncodeQuaiResponse(id, block)
	if err != nil {
		return err
	}
	err = common.WriteMessageToStream(stream, data)
	if err != nil {
		return err
	}
	log.Debugf("Sent block %s to peer %s", block.Hash(), stream.Conn().RemotePeer())
	return nil
}

// Seeks the header in the cache or database and sends it to the peer in a pb.QuaiResponseMessage
func handleHeaderRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode) error {
	header := node.GetHeader(hash, loc)
	if header == nil {
		log.Debugf("header not found")
		// TODO: handle header not found
		return nil
	}
	log.Debugf("header found %s", header.Hash())
	// create a Quai Message Response with the header
	data, err := pb.EncodeQuaiResponse(id, header)
	if err != nil {
		return err
	}
	err = common.WriteMessageToStream(stream, data)
	if err != nil {
		return err
	}
	log.Debugf("Sent header %s to peer %s", header.Hash(), stream.Conn().RemotePeer())
	return nil
}

func handleTransactionRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode) error {
	panic("TODO: implement")
}
