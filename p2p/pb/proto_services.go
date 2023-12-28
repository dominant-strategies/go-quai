package pb

import (
	"encoding/hex"

	"github.com/dominant-strategies/go-quai/consensus/types"
	"github.com/gogo/protobuf/proto"
)

// Unmarshals a serialized protobuf slice of bytes into a protocol buffer type
func UnmarshalProtoMessage(data []byte, pbMsg proto.Message) error {
	if err := proto.Unmarshal(data, pbMsg); err != nil {
		return err
	}
	return nil
}

// Marshals a protocol buffer type into a serialized protobuf slice of bytes
func MarshalProtoMessage(pbMsg proto.Message) ([]byte, error) {
	data, err := proto.Marshal(pbMsg)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// converts a custom go Block type (types.Block) to a protocol buffer Block type (pb.Block)
func ConvertToProtoBlock(block types.Block) *Block {
	return &Block{
		Hash: hex.EncodeToString(block.Hash[:]),
		// ... map other fields
	}

}

// converts a protocol buffer Block type (pb.Block) to a custom go Block type (types.Block)
func ConvertFromProtoBlock(pbBlock *Block) types.Block {
	var hash types.Hash
	copy(hash[:], pbBlock.Hash)
	// ... map other fields
	return types.Block{
		Hash: hash,
		// ... map other fields
	}
}

// Unmarshals a received serialized protobuf slice of bytes into a custom *types.Block type
func UnmarshalBlock(data []byte) (*types.Block, error) {
	var pbBlock Block
	err := UnmarshalProtoMessage(data, &pbBlock)
	if err != nil {
		return nil, err
	}
	block := ConvertFromProtoBlock(&pbBlock)
	return &block, nil
}

// Marshals a custom *types.Block type into a serialized protobuf slice of bytes
// to be sent over the wire
func MarshalBlock(block *types.Block) ([]byte, error) {
	pbBlock := ConvertToProtoBlock(*block)
	data, err := MarshalProtoMessage(pbBlock)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Creates a BlockRequest protocol buffer message
func CreateProtoBlockRequest(hash types.Hash) *BlockRequest {
	return &BlockRequest{
		Hash: hex.EncodeToString(hash[:]),
	}
}
