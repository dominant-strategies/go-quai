package node

import (
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
	"github.com/pkg/errors"
)

// Opens a stream to the given peer and requests a block for the given hash and slice.
//
// If a block is not found, an error is returned
func (p *P2PNode) requestBlockFromPeer(hash common.Hash, location common.Location, peerID peer.ID) (*types.Block, error) {
	// Open a stream to the peer using a specific protocol for block requests
	stream, err := p.NewStream(peerID, protocol.ProtocolVersion)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	// create a block request protobuf message
	blockReq := pb.CreateProtoBlockRequest(hash, location)

	// Marshal the block request into a byte array
	blockReqBytes, err := pb.MarshalProtoMessage(blockReq)
	if err != nil {
		return nil, err
	}

	// Send the block request to the peer
	err = common.WriteMessageToStream(stream, blockReqBytes)
	if err != nil {
		return nil, err
	}

	// Read the response from the peer
	blockResponseBytes, err := common.ReadMessageFromStream(stream)
	if err != nil {
		return nil, err
	}

	// Unmarshal the response into a block
	var pbBlockResponse pb.BlockResponse
	err = pb.UnmarshalProtoMessage(blockResponseBytes, &pbBlockResponse)
	if err != nil {
		log.Errorf("error unmarshalling block response: %s", err)
		return nil, err
	}

	// check if the response contains a block
	if pbBlockResponse.Found {
		// convert the block to a custom go block type
		block := pb.ConvertFromProtoBlock(pbBlockResponse.Block)
		return &block, nil
	}

	// If the response does not contain a block, return an error
	return nil, errors.New("block not found")
}

// Creates a Cid from a location to be used as DHT key
func locationToCid(location common.Location) cid.Cid {
	sliceBytes := []byte(location.Name())

	// create a multihash from the slice ID
	mhash, _ := multihash.Encode(sliceBytes, multihash.SHA2_256)

	// create a Cid from the multihash
	return cid.NewCidV1(cid.Raw, mhash)

}
