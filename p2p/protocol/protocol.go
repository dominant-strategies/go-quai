package protocol

import (
	"io"
	"math/big"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/trie"
	"github.com/pkg/errors"
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
			if errors.Is(err, network.ErrReset) || errors.Is(err, io.EOF) {
				return
			}

			log.Global.Errorf("error reading message from stream: %s", err)
			// TODO: handle error
			continue
		}

		quaiMsg, err := pb.DecodeQuaiMessage(data)
		if err != nil {
			log.Global.Errorf("error decoding quai message: %s", err)
			continue
		}

		switch {
		case quaiMsg.GetRequest() != nil:
			handleRequest(quaiMsg.GetRequest(), stream, node)

		case quaiMsg.GetResponse() != nil:
			handleResponse(quaiMsg.GetResponse(), stream, node)

		default:
			log.Global.WithField("quaiMsg", quaiMsg).Errorf("unsupported quai message type")
		}
	}
}

func handleRequest(quaiMsg *pb.QuaiRequestMessage, stream network.Stream, node QuaiP2PNode) {
	id, decodedType, loc, query, err := pb.DecodeQuaiRequest(quaiMsg)
	if err != nil {
		log.Global.WithField("err", err).Errorf("error decoding quai request")
		// TODO: handle error
		return
	}
	switch query.(type) {
	case *common.Hash:
		log.Global.WithFields(log.Fields{
			"requestID":   id,
			"decodedType": decodedType,
			"location":    loc,
			"hash":        query,
			"peer":        stream.Conn().RemotePeer(),
		}).Debug("Received request by hash to handle")
	case *big.Int:
		log.Global.WithFields(log.Fields{
			"requestID":   id,
			"decodedType": decodedType,
			"location":    loc,
			"hash":        query,
			"peer":        stream.Conn().RemotePeer(),
		}).Debug("Received request by number to handle")
	default:
		log.Global.Errorf("unsupported request input data field type: %T", query)
	}

	switch decodedType.(type) {
	case *types.Block:
		requestedHash := &common.Hash{}
		switch query := query.(type) {
		case common.Hash:
			*requestedHash = query
		case *big.Int:
			number := query
			log.Global.Debugf("Looking hash for block %s and location %s", number.String(), loc.Name())
			requestedHash = node.GetBlockHashByNumber(number, loc)
			if requestedHash == nil {
				log.Global.Debugf("block hash not found for block %s and location %s", number.String(), loc.Name())
				// TODO: handle error
				return
			}
			log.Global.Debugf("Found hash for block %s and location: %s", number.String(), loc.Name(), requestedHash)
		default:
			log.Global.Errorf("unsupported query type %v", query)
			// TODO: handle error
			return
		}
		err = handleBlockRequest(id, loc, *requestedHash, stream, node)
		if err != nil {
			log.Global.WithField("err", err).Error("error handling block request")
			// TODO: handle error
			return
		}
	case *types.Header:
		requestedHash := query.(*common.Hash)
		err = handleHeaderRequest(id, loc, *requestedHash, stream, node)
		if err != nil {
			log.Global.WithField("err", err).Error("error handling header request")
			// TODO: handle error
			return
		}
	case *types.Transaction:
		requestedHash := query.(*common.Hash)
		err = handleTransactionRequest(id, loc, *requestedHash, stream, node)
		if err != nil {
			log.Global.WithField("err", err).Error("error handling transaction request")
			// TODO: handle error
			return
		}
	//! Do we need to handle *common.Hash here?
	case *common.Hash:
		number := query.(*big.Int)
		err = handleBlockNumberRequest(id, loc, number, stream, node)
		if err != nil {
			log.Global.WithField("err", err).Error("error handling block number request")
			return
		}
	case trie.TrieNodeRequest:
		requestedHash := query.(*common.Hash)
		err := handleTrieNodeRequest(id, loc, *requestedHash, stream, node)
		if err != nil {
			log.Global.WithField("err", err).Error("error handling trie node request")
		}
	default:
		log.Global.WithField("request type", decodedType).Error("unsupported request data type")
		// TODO: handle error
		return

	}
}

func handleResponse(quaiResp *pb.QuaiResponseMessage, stream network.Stream, node QuaiP2PNode) {
	recvdID, recvdType, err := pb.DecodeQuaiResponse(quaiResp)
	if err != nil {
		log.Global.WithField(
			"err", err,
		).Errorf("error decoding quai response: %s", err)
		return
	}

	dataChan, err := node.GetRequestManager().GetRequestChan(recvdID)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"requestID": recvdID,
			"err":       err,
		}).Error("error associating request ID with data channel")
		return
	}
	dataChan <- recvdType
}
