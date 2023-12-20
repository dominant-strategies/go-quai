package pb

import (
	"encoding/hex"

	"github.com/dominant-strategies/go-quai/consensus/types"
)

// converts a custom go Block type (types.Block) to a protocol buffer Block type (pb.Block)
func ConvertToProtoBlock(block types.Block) *Block {
	return &Block{
		Hash: hex.EncodeToString(block.Hash[:]),
		// ... map other fields
	}

}

// Converts a protocol buffer Block type (pb.Block) to a custom go Block type (types.Block)
func ConvertFromProtoBlock(block *Block) types.Block {
	var hash types.Hash
	copy(hash[:], block.Hash)
	// ... map other fields
	return types.Block{
		Hash: hash,
		// ... map other fields
	}
}

// Creates a BlockRequest protocol buffer message
func CreateProtoBlockRequest(hash types.Hash) *BlockRequest {
	return &BlockRequest{
		Hash: hex.EncodeToString(hash[:]),
	}
}
