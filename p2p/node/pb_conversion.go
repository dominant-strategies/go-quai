package node

import (
	"encoding/hex"

	"github.com/dominant-strategies/go-quai/consensus/types"
	"github.com/dominant-strategies/go-quai/p2p/pb"
)

// converts a custom go Block type (types.Block) to a protocol buffer Block type (pb.Block)
func convertToProtoBlock(block types.Block) *pb.Block {
	return &pb.Block{
		Hash: hex.EncodeToString(block.Hash[:]),
		// ... map other fields
	}

}
