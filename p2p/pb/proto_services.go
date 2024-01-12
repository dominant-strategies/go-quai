package pb

import (
	"encoding/hex"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/gogo/protobuf/proto"
)

// converts a custom go Block type (types.Block) to a protocol buffer Block type (pb.Block)
func MarshalData(data interface{}) []byte {
	var bytes []byte
	var err error
	switch v := data.(type) {
	case *types.Block:
		bytes, err = MarshalBlock(v)
	default:
		return nil
	}
	if err != nil {
		log.Errorf("Error marshalling data: ", err)
		return nil
	} else {
		return bytes
	}
}

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
		Hash: hex.EncodeToString(block.Hash().Bytes()),
		// ... map other fields
	}

}

// converts a protocol buffer Block type (pb.Block) to a custom go Block type (types.Block)
func ConvertFromProtoBlock(pbBlock *Block) types.Block {
	var hash common.Hash
	copy(hash[:], pbBlock.Hash)
	// ... map other fields
	return types.Block{
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

// Converts a custom go SliceID type (types.SliceID) to a protocol buffer SliceID type (pb.SliceID)
func convertToProtoSlice(slice types.SliceID) *SliceID {
	sliceContext := &Context{
		Location: slice.Context.Location,
		Level:    slice.Context.Level,
	}
	return &SliceID{
		Context: sliceContext,
		Region:  slice.Region,
		Zone:    slice.Zone,
	}

}

// Converts a protocol buffer SliceID type (pb.SliceID) to a custom go SliceID type (types.SliceID)
func ConvertFromProtoSlice(pbSlice *SliceID) types.SliceID {
	sliceContext := types.Context{
		Location: pbSlice.Context.Location,
		Level:    pbSlice.Context.Level,
	}
	return types.SliceID{
		Context: sliceContext,
		Region:  pbSlice.Region,
		Zone:    pbSlice.Zone,
	}
}

// Creates a BlockRequest protocol buffer message
func CreateProtoBlockRequest(hash common.Hash, slice types.SliceID) *BlockRequest {
	pbSlice := convertToProtoSlice(slice)
	return &BlockRequest{
		Hash:    hex.EncodeToString(hash[:]),
		SliceId: pbSlice,
	}
}
