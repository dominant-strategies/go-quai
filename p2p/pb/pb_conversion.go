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
