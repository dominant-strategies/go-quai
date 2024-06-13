package node

import (
	"math/big"
	"runtime/debug"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pkg/errors"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/node/peerManager"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	"github.com/dominant-strategies/go-quai/p2p/node/requestManager"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/trie"
)

// Opens a stream to the given peer and request some data for the given hash at the given location
func (p *P2PNode) requestFromPeer(peerID peer.ID, topic *pubsubManager.Topic, reqData interface{}, respDataType interface{}) (interface{}, error) {
	log.Global.Info("Entering the requestFromPeer")
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
		log.Global.Info("Exiting the requestFromPeer", "Time taken inside requestFrom peer", common.PrettyDuration(time.Since(start)))
	}()
	log.Global.WithFields(log.Fields{
		"peerId": peerID,
		"topic":  topic,
	}).Trace("Requesting the data from peer")
	stream, err := p.GetStream(peerID)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"peerId": peerID,
			"error":  err,
		}).Error("Failed to open stream to peer")
		return nil, err
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

	if len(requestBytes) == 0 {
		log.Global.Error("Empty request message", "topic", topic.String())
	}
	time1 := time.Now()
	// Send the request to the peer
	err = p.GetPeerManager().WriteMessageToStream(peerID, stream, requestBytes)
	if err != nil {
		return nil, err
	}
	log.Global.Info("Time taken to write message to stream peer", common.PrettyDuration(time.Since(time1)))

	// Get appropriate channel and wait for response
	dataChan, err := p.requestManager.GetRequestChan(id)
	if err != nil {
		return nil, err
	}

	requestTimer := time.NewTimer(requestManager.C_requestTimeout)
	defer requestTimer.Stop()
	time2 := time.Now()
	var recvdType interface{}
	select {
	case recvdType = <-dataChan:
		log.Global.Info("Time taken to get a response from peer", common.PrettyDuration(time.Since(time2)), "peer id", peerID)
		break
	case <-requestTimer.C:
		log.Global.WithFields(log.Fields{
			"requestID": id,
			"peerId":    peerID,
		}).Warn("Peer did not respond in time")
		p.peerManager.MarkUnresponsivePeer(peerID, topic)
		return nil, errors.New("peer did not respond in time")
	}

	if recvdType == nil {
		return nil, nil
	}

	// Check the received data type & hash matches the request
	switch respDataType.(type) {
	// First, check that the recvdType is the same as the expected type
	case *types.WorkObjectBlockView, *types.WorkObjectHeaderView:
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
			default:
				return nil, errors.New("invalid response")
			}
			return nil, errors.Errorf("invalid response: got block with different number")
		}
		return nil, errors.New("block request invalid response")
	case *types.Header:
		if header, ok := recvdType.(*types.Header); ok && header.Hash() == reqData.(common.Hash) {
			return header, nil
		}
	case *types.Transaction:
		if tx, ok := recvdType.(*types.Transaction); ok && tx.Hash() == reqData.(common.Hash) {
			return tx, nil
		}
	case common.Hash:
		if hash, ok := recvdType.(common.Hash); ok {
			return hash, nil
		}
	case *trie.TrieNodeRequest:
		if trieNode, ok := recvdType.(*trie.TrieNodeResponse); ok {
			return trieNode, nil
		}
	default:
		log.Global.Warn("peer returned unexpected type")
	}

	// If this peer responded with an invalid response, ban them for misbehaving.
	p.BanPeer(peerID)
	return nil, errors.New("invalid response")
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
