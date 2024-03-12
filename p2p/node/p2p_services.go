package node

import (
	"errors"
	"reflect"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/p2p/requestManager"
	"github.com/dominant-strategies/go-quai/trie"
)

// Opens a stream to the given peer and request some data for the given hash at the given location
func (p *P2PNode) requestFromPeer(peerID peer.ID, location common.Location, data interface{}, datatype interface{}) (interface{}, error) {
	datatypeType := reflect.TypeOf(datatype).String()
	log.Global.WithFields(log.Fields{
		"peerId":   peerID,
		"location": location.Name(),
		"data":     data,
		"datatype": datatypeType,
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

	// Create the corresponding data request
	requestBytes, err := pb.EncodeQuaiRequest(id, location, data, datatype)
	if err != nil {
		log.Global.Errorf("Failed to encode request: %v", err)
		return nil, err
	}

	// Send the request to the peer
	err = common.WriteMessageToStream(stream, requestBytes)
	if err != nil {
		log.Global.Errorf("Failed to send request: %v", err)
		return nil, err
	}

	// Get appropriate channel and wait for response
	dataChan, err := p.requestManager.GetRequestChan(id)
	if err != nil {
		log.Global.Errorf("Failed to get request channel: %v", err)
		return nil, err
	}
	recvdType := <-dataChan

	// Remove request ID from the map of pending requests
	p.requestManager.CloseRequest(id)

	// Check the received data type & hash matches the request
	switch datatype.(type) {
	case *types.Block:
		if block, ok := recvdType.(*types.Block); ok && block.Hash() == data.(common.Hash) {
			return block, nil
		}
	case *types.Header:
		if header, ok := recvdType.(*types.Header); ok && header.Hash() == data.(common.Hash) {
			return header, nil
		}
	case *types.Transaction:
		if tx, ok := recvdType.(*types.Transaction); ok && tx.Hash() == data.(common.Hash) {
			return tx, nil
		}
	case common.Hash:
		if hash, ok := recvdType.(common.Hash); ok {
			return hash, nil
		}
	case *trie.TrieNodeResponse:
		if trieNode, ok := recvdType.(*trie.TrieNodeResponse); ok {
			return trieNode, nil
		}
	default:
		recvdTypeType := reflect.TypeOf(recvdType).String()
		log.Global.Warnf("peer returned unexpected type: %s", recvdTypeType)
	}

	// If this peer responded with an invalid response, ban them for misbehaving.
	p.BanPeer(peerID)
	return nil, errors.New("invalid response")
}

func (p *P2PNode) GetRequestManager() requestManager.RequestManager {
	return p.requestManager
}

func (p *P2PNode) GetHostBackend() host.Host {
	return p.Host
}

// Creates a Cid from a location to be used as DHT key
func locationToCid(location common.Location) cid.Cid {
	sliceBytes := []byte(location.Name())

	// create a multihash from the slice ID
	mhash, _ := multihash.Encode(sliceBytes, multihash.SHA2_256)

	// create a Cid from the multihash
	return cid.NewCidV1(cid.Raw, mhash)

}
