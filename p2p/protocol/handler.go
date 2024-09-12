package protocol

import (
	"context"
	"errors"
	"io"
	"math/big"
	"runtime/debug"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/sirupsen/logrus"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
)

const (
	numWorkers                 = 10  // Number of workers per stream
	msgChanSize                = 100 // 100 requests per stream
	protocolName               = "quai"
	C_NumPrimeBlocksToDownload = 10
)

// QuaiProtocolHandler handles all the incoming requests and responds with corresponding data
func QuaiProtocolHandler(stream network.Stream, node QuaiP2PNode) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	defer stream.Close()

	log.Global.Debugf("Received a new stream from %s", stream.Conn().RemotePeer())

	// if there is a protocol mismatch, close the stream
	if stream.Protocol() != ProtocolVersion {
		log.Global.Warnf("Invalid protocol: %s", stream.Protocol())
		// TODO: add logic to drop the peer
		return
	}
	// Create a channel for messages
	msgChan := make(chan []byte, msgChanSize)
	full := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Fatal("Go-Quai Panicked")
			}
		}()
		for {
			select {
			case message := <-msgChan:
				handleMessage(message, stream, node)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Enter the read loop for the stream and handle messages
	bwc := node.GetBandwidthCounter()
	for {
		data, err := common.ReadMessageFromStream(stream, ProtocolVersion, bwc)
		if err != nil {
			if errors.Is(err, network.ErrReset) || errors.Is(err, io.EOF) {
				return
			}

			log.Global.Errorf("error reading message from stream: %s", err)
			// TODO: handle error
			return
		}

		// Send to worker goroutines
		select {
		case msgChan <- data:
		case <-ctx.Done():
			return
		default:
			if full%1000 == 0 {
				log.Global.WithField("stream with peer", stream.Conn().RemotePeer()).Warnf("QuaiProtocolHandler message channel is full. Lost messages: %d", full)
			}
			full++
		}

	}
}

func handleMessage(data []byte, stream network.Stream, node QuaiP2PNode) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	quaiMsg, err := pb.DecodeQuaiMessage(data)
	if err != nil {
		log.Global.Errorf("error decoding quai message: %s", err)
		return
	}

	switch {
	case quaiMsg.GetRequest() != nil:
		handleRequest(quaiMsg.GetRequest(), stream, node)
		if messageMetrics != nil {
			messageMetrics.WithLabelValues("requests").Inc()
		}

	case quaiMsg.GetResponse() != nil:
		handleResponse(quaiMsg.GetResponse(), node)
		if messageMetrics != nil {
			messageMetrics.WithLabelValues("responses").Inc()
		}

	default:
		log.Global.WithFields(log.Fields{"quaiMsg": quaiMsg, "data": data, "peer": stream.Conn().RemotePeer()}).Errorf("unsupported quai message type")
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
			"number":      query,
			"peer":        stream.Conn().RemotePeer(),
		}).Debug("Received request by number to handle")
	default:
		log.Global.Errorf("unsupported request input data field type: %T", query)
	}

	switch decodedType.(type) {
	case *types.WorkObjectHeaderView, *types.WorkObjectBlockView, []*types.WorkObjectBlockView:
		var requestedView types.WorkObjectView
		switch decodedType.(type) {
		case *types.WorkObjectHeaderView:
			requestedView = types.HeaderObject
		case *types.WorkObjectBlockView:
			requestedView = types.BlockObject
		case []*types.WorkObjectBlockView:
			requestedView = types.BlockObjects
		}

		requestedHash := &common.Hash{}
		switch query := query.(type) {
		case *common.Hash:
			requestedHash = query
		case *big.Int:
			number := query
			log.Global.Tracef("Looking hash for block %s and location %s", number.String(), loc.Name())
			requestedHash = node.GetBlockHashByNumber(number, loc)
			if requestedHash == nil {
				log.Global.Debugf("block hash not found for block %s and location %s", number.String(), loc.Name())
				// TODO: handle error
				return
			}
			log.Global.Tracef("Found hash %s for block %s and location: %s", requestedHash, number.String(), loc.Name())
		default:
			log.Global.Errorf("unsupported query type %v", query)
			// TODO: handle error
			return
		}
		err = handleBlockRequest(id, loc, *requestedHash, stream, node, requestedView)
		if err != nil {
			log.Global.WithFields(
				logrus.Fields{
					"peer": stream.Conn().RemotePeer(),
					"err":  err,
				}).Error("error handling block request")
			// TODO: handle error
			return
		}
		if messageMetrics != nil {
			messageMetrics.WithLabelValues("blocks").Inc()
		}
	case *common.Hash:
		number := query.(*big.Int)
		err = handleBlockNumberRequest(id, loc, number, stream, node)
		if err != nil {
			log.Global.WithField("err", err).Error("error handling block number request")
			return
		}
	default:
		log.Global.WithField("request type", decodedType).Error("unsupported request data type")
		// TODO: handle error
		return

	}
}

func handleResponse(quaiResp *pb.QuaiResponseMessage, node QuaiP2PNode) {
	recvdID, recvdType, err := pb.DecodeQuaiResponse(quaiResp)
	if err != nil && err.Error() != pb.EmptyResponse.Error() {
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

	select {
	case dataChan <- recvdType:
	default:
	}
}

// Seeks the block in the cache or database and sends it to the peer in a pb.QuaiResponseMessage
func handleBlockRequest(id uint32, loc common.Location, hash common.Hash, stream network.Stream, node QuaiP2PNode, view types.WorkObjectView) error {
	// check if we have the block in our cache or database
	fullWO := node.GetWorkObject(hash, loc)
	var data []byte
	var err error
	var block interface{}
	if fullWO == nil {
		// If we dont have the data, still respond with empty
		block = nil
		log.Global.Debugf("block not found")
	} else {
		log.Global.Debugf("block found %s", fullWO.Hash())
		switch view {
		case types.HeaderObject:
			block = fullWO.ConvertToHeaderView()
		case types.BlockObject:
			block = fullWO.ConvertToBlockView()
		case types.BlockObjects:
			// This is the case in which the Prime is asking for the next c_NumPrimeBlocksToDownload blocks
			block = node.GetWorkObjectsFrom(hash, loc, C_NumPrimeBlocksToDownload)
		}
	}
	var requestDataType interface{}
	switch view {
	case types.BlockObject:
		requestDataType = &types.WorkObjectBlockView{}
	case types.HeaderObject:
		requestDataType = &types.WorkObjectHeaderView{}
	case types.BlockObjects:
		requestDataType = []*types.WorkObjectBlockView{}
	}
	// create a Quai Message Response with the block
	data, err = pb.EncodeQuaiResponse(id, loc, requestDataType, block)
	if err != nil {
		return err
	}
	err = common.WriteMessageToStream(stream, data, ProtocolVersion, node.GetBandwidthCounter())
	if err != nil {
		return err
	}
	return nil
}

// Seeks the block in the cache or database and sends it to the peer in a pb.QuaiResponseMessage
func handleBlockNumberRequest(id uint32, loc common.Location, number *big.Int, stream network.Stream, node QuaiP2PNode) error {
	// check if we have the block in our cache or database
	blockHash := node.GetBlockHashByNumber(number, loc)
	if blockHash != nil {
		log.Global.Tracef("block found %s", blockHash)
	}
	// create a Quai Message Response with the block
	data, err := pb.EncodeQuaiResponse(id, loc, &common.Hash{}, blockHash)
	if err != nil {
		return err
	}
	err = common.WriteMessageToStream(stream, data, ProtocolVersion, node.GetBandwidthCounter())
	if err != nil {
		return err
	}
	log.Global.Tracef("Sent block hash %s to peer %s", blockHash, stream.Conn().RemotePeer())
	return nil
}
