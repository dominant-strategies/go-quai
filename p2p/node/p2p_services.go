package node

import (
	"errors"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
)

// Opens a stream to the given peer and request some data for the given hash at the given location
//
// If a block is not found, an error is returned
func (p *P2PNode) requestFromPeer(peerID peer.ID, location common.Location, hash common.Hash, datatype interface{}) (interface{}, error) {
	// Open a stream to the peer using a specific protocol for block requests
	stream, err := p.NewStream(peerID, protocol.ProtocolVersion)
	if err != nil {
		// TODO: should we report this peer for failure to participate?
		return nil, err
	}
	defer stream.Close()

	// Create the corresponding data request
	blockReqBytes, err := pb.EncodeQuaiRequest(location, hash, datatype)
	if err != nil {
		return nil, err
	}

	// Send the request to the peer
	err = common.WriteMessageToStream(stream, blockReqBytes)
	if err != nil {
		return nil, err
	}

	// Read the response from the peer
	blockResponseBytes, err := common.ReadMessageFromStream(stream)
	if err != nil {
		return nil, err
	}

	// Unmarshal the response
	recvd, err := pb.DecodeQuaiResponse(blockResponseBytes)
	if err != nil {
		// TODO: should we report this peer for an invalid response?
		return nil, err
	}

	// Check the received data type & hash matches the request
	switch datatype.(type) {
	case *types.Block:
		if block, ok := recvd.(*types.Block); ok && block.Hash() == hash {
			return block, nil
		}
	default:
		log.Warn("peer returned unexpected type")
	}

	// If this peer responded with an invalid response, report them for misbehaving.
	p.ReportBadPeer(peerID)
	return nil, errors.New("invalid response")
}

// Creates a Cid from a location to be used as DHT key
func locationToCid(location common.Location) cid.Cid {
	sliceBytes := []byte(location.Name())

	// create a multihash from the slice ID
	mhash, _ := multihash.Encode(sliceBytes, multihash.SHA2_256)

	// create a Cid from the multihash
	return cid.NewCidV1(cid.Raw, mhash)

}
