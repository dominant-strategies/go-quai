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
func (p *P2PNode) requestFromPeer(peerID peer.ID, topic *pubsubManager.Topic, reqData interface{}) (interface{}, error) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	log.Global.WithFields(log.Fields{
		"peerId": peerID,
		"topic":  topic,
	}).Trace("Requesting the data from peer")
	stream, err := p.NewStream(peerID)
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
	requestBytes, err := pb.EncodeQuaiRequest(id, topic.GetLocation(), reqData, topic.GetTopicType())
	if err != nil {
		return nil, err
	}

	// Send the request to the peer
	err = common.WriteMessageToStream(stream, requestBytes)
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
			"peerId": peerID,
		}).Warn("Peer did not respond in time")
		p.peerManager.MarkUnresponsivePeer(peerID, topic)
		return nil, errors.New("peer did not respond in time")
	}

	if recvdType == nil {
		return nil, nil
	}

	// Check the received data type & hash matches the request
	switch topic.GetTopicType().(type) {
	case *types.WorkObject:
		if block, ok := recvdType.(*types.WorkObject); ok {
			switch data := reqData.(type) {
			case *types.WorkObjectBlockView, *types.WorkObjectHeaderView:
				if block.Hash() == data.(*types.WorkObject).Hash() {
					return data, nil
				}
				return nil, errors.Errorf("invalid response: expected block with hash %s, got %s", data.(*types.WorkObject).Hash(), block.Hash())
			case common.Hash:
				if block.Hash() == data {
					return block, nil
				}
				return nil, errors.Errorf("invalid response: expected block with hash %s, got %s", data, block.Hash())
			case *big.Int:
				nodeCtx := topic.GetLocation().Context()
				if block.Number(nodeCtx).Cmp(data) == 0 {
					return block, nil
				}
				return nil, errors.Errorf("invalid response: expected block with number %s, got %s", data, block.Number(nodeCtx))
			}
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
