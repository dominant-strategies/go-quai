package node

import (
	"math/big"
	"runtime/debug"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libp2pmetrics "github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pkg/errors"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/p2p/node/peerManager"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	"github.com/dominant-strategies/go-quai/p2p/node/requestManager"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
)

// Opens a stream to the given peer and request some data for the given hash at the given location
func (p *P2PNode) requestFromPeer(peerID peer.ID, topic *pubsubManager.Topic, reqData interface{}, respDataType interface{}) (interface{}, error) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()

	// Only proceed if we aren't violating our request rate to that peer
	if protocol.ProcRequestRate(peerID, false) != nil {
		log.Global.Warnf("Exceeded request rate to peer %s", peerID)
		return nil, errors.Errorf("Exceeded request rate to peer %s", peerID)
	}

	log.Global.WithFields(log.Fields{
		"peerId": peerID,
		"topic":  topic,
	}).Trace("Requesting the data from peer")
	// Ensure a stream exists before attempting the request.
	//
	// This is important because GetStream is a cache lookup. Without an explicit
	// open, the old behavior could drop a request on the floor (GetStream returns
	// ErrStreamNotFound and stream creation happens asynchronously elsewhere).
	if err := p.peerManager.OpenStream(peerID); err != nil {
		return nil, err
	}
	stream, err := p.GetStream(peerID)
	if err != nil {
		return nil, err
	}

	// At this point we have a stream.
	if stream == nil {
		return nil, errors.New("stream is unexpectedly nil after successful OpenStream")
	}

	// Get a new request ID
	id := p.requestManager.CreateRequest()

	// Remove request ID from the map of pending requests when cleaning up
	defer p.requestManager.CloseRequest(id)

	// Create the corresponding data request
	requestBytes, err := pb.EncodeQuaiRequest(id, topic.GetLocation(), reqData, respDataType)
	if err != nil {
		return nil, err
	}

	// Send the request to the peer
	err = p.GetPeerManager().WriteMessageToStream(peerID, stream, requestBytes, protocol.ProtocolVersion, p.GetBandwidthCounter())
	if err != nil {
		return nil, err
	}

	// Get appropriate channel and wait for response
	dataChan, err := p.requestManager.GetRequestChan(id)
	if err != nil {
		return nil, err
	}

	var recvdType interface{}
	select {
	case recvdType = <-dataChan:
		break
	case <-time.After(requestManager.C_requestTimeout):
		log.Global.WithFields(log.Fields{
			"requestID": id,
			"peerId":    peerID,
		}).Info("Success Peer did not respond in time")
		p.peerManager.AdjustPeerQuality(peerID, topic.String(), p2p.QualityAdjOnTimeout)
		return nil, errors.New("peer did not respond in time")
	}

	if recvdType == nil {
		p.peerManager.AdjustPeerQuality(peerID, topic.String(), p2p.QualityAdjOnNack)
		return nil, nil
	} else {
		p.peerManager.AdjustPeerQuality(peerID, topic.String(), p2p.QualityAdjOnResponse)
	}

	// Check the received data type & hash matches the request
	switch respDataType.(type) {
	// First, check that the recvdType is the same as the expected type
	case *types.WorkObjectBlockView, *types.WorkObjectHeaderView, []*types.WorkObjectBlockView:
		switch reqData := reqData.(type) {
		case common.Hash:
			// Next, if it was a requestByHash, verify the hash matches
			switch recvdType := recvdType.(type) {
			// Finally, BlockView and HeaderView have different hash functions
			case *types.WorkObjectBlockView:
				if reqData == recvdType.Hash() {
					return recvdType, nil
				}
			case *types.WorkObjectHeaderView:
				if reqData == recvdType.Hash() {
					return recvdType, nil
				}
			default:
				return nil, errors.New("invalid response")
			}
			return nil, errors.Errorf("invalid response: got block with different hash")
		case *big.Int:
			// If it was a requestByNumber, just verify the number matches
			nodeCtx := topic.GetLocation().Context()
			switch block := recvdType.(type) {
			case *types.WorkObjectBlockView:
				if block.Number(nodeCtx).Cmp(reqData) == 0 {
					return recvdType, nil
				}
			case *types.WorkObjectHeaderView:
				if block.Number(nodeCtx).Cmp(reqData) == 0 {
					return recvdType, nil
				}
			case []*types.WorkObjectBlockView:
				return recvdType, nil
			default:
				return nil, errors.New("invalid response")
			}
			return nil, errors.Errorf("invalid response: got block with different number")
		}
		return nil, errors.New("block request invalid response")
	case common.Hash:
		if hash, ok := recvdType.(common.Hash); ok {
			return hash, nil
		}
	default:
		log.Global.Warn("peer returned unexpected type")
	}

	// If this peer responded with an invalid response, ban them for misbehaving.
	p.BanPeer(peerID)
	return nil, errors.New("invalid response")
}

func (p *P2PNode) GetBandwidthCounter() libp2pmetrics.Reporter {
	return p.bandwidthCounter
}

func (p *P2PNode) GetRequestManager() requestManager.RequestManager {
	return p.requestManager
}

func (p *P2PNode) GetPeerManager() peerManager.PeerManager {
	return p.peerManager
}

func (p *P2PNode) GetHostBackend() host.Host {
	return p.peerManager.GetHost()
}
