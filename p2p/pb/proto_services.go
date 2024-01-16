package pb

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

// Unmarshals a serialized protobuf slice of bytes into a protocol buffer type
func UnmarshalProtoMessage(data []byte, msg proto.Message) error {
	if err := proto.Unmarshal(data, msg); err != nil {
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

// EncodeRequestMessage creates a marshaled protobuf message for a Quai Request.
// Returns the serialized protobuf message.
func EncodeQuaiRequest(location common.Location, hash common.Hash, datatype interface{}) ([]byte, error) {
	reqMsg := &QuaiRequestMessage{
		Location: convertLocationToProto(location),
		Hash:     convertHashToProto(hash),
	}

	switch d := datatype.(type) {
	case *types.Block:
		reqMsg.Request = &QuaiRequestMessage_Block{Block: convertBlockToProto(d)}
	case *types.Header:
		reqMsg.Request = &QuaiRequestMessage_Header{Header: convertHeaderToProto(d)}
	case *types.Transaction:
		reqMsg.Request = &QuaiRequestMessage_Transaction{Transaction: convertTransactionToProto(d)}
	default:
		return nil, errors.Errorf("unsupported request data type: %T", datatype)
	}

	return MarshalProtoMessage(reqMsg)
}

// DecodeRequestMessage unmarshals a protobuf message into a Quai Request.
// Returns the decoded type, location, and hash.
func DecodeQuaiRequest(data []byte) (interface{}, common.Location, common.Hash, error) {
	var quaiMsg QuaiRequestMessage
	err := UnmarshalProtoMessage(data, &quaiMsg)
	if err != nil {
		return nil, common.Location{}, common.Hash{}, err
	}

	location := convertProtoToLocation(quaiMsg.Location)
	hash := convertProtoToHash(quaiMsg.Hash)

	switch quaiMsg.Request.(type) {
	case *QuaiRequestMessage_Block:
		protoBlock := quaiMsg.GetBlock()
		block := convertProtoToBlock(protoBlock)
		return block, location, hash, nil
	case *QuaiRequestMessage_Header:
		protoHeader := quaiMsg.GetHeader()
		header := convertProtoToHeader(protoHeader)
		return header, location, hash, nil
	case *QuaiRequestMessage_Transaction:
		protoTransaction := quaiMsg.GetTransaction()
		transaction := convertProtoToTransaction(protoTransaction)
		return transaction, location, hash, nil
	default:
		return nil, common.Location{}, common.Hash{}, errors.Errorf("unsupported request type: %T", quaiMsg.Request)
	}
}

// EncodeResponse creates a marshaled protobuf message for a Quai Response.
// Returns the serialized protobuf message.
func EncodeQuaiResponse(data interface{}) ([]byte, error) {

	var quaiMsg *QuaiResponseMessage

	switch data := data.(type) {
	case *types.Block:
		quaiMsg = &QuaiResponseMessage{
			Response: &QuaiResponseMessage_Block{Block: convertBlockToProto(data)},
		}
	case *types.Header:
		quaiMsg = &QuaiResponseMessage{
			Response: &QuaiResponseMessage_Header{Header: convertHeaderToProto(data)},
		}
	case *types.Transaction:
		quaiMsg = &QuaiResponseMessage{
			Response: &QuaiResponseMessage_Transaction{Transaction: convertTransactionToProto(data)},
		}

	default:
		return nil, errors.Errorf("unsupported response data type: %T", data)
	}

	return MarshalProtoMessage(quaiMsg)
}

// Unmarshals a serialized protobuf message into a Quai Response message.
// Returns the decoded type (i.e. *types.Header, *types.Block, etc).
func DecodeQuaiResponse(data []byte) (interface{}, error) {
	var quaiMsg QuaiResponseMessage
	err := UnmarshalProtoMessage(data, &quaiMsg)
	if err != nil {
		return nil, err
	}

	switch quaiMsg.Response.(type) {
	case *QuaiResponseMessage_Block:
		protoBlock := quaiMsg.GetBlock()
		block := convertProtoToBlock(protoBlock)
		return block, nil
	case *QuaiResponseMessage_Header:
		protoHeader := quaiMsg.GetHeader()
		header := convertProtoToHeader(protoHeader)
		return header, nil
	case *QuaiResponseMessage_Transaction:
		protoTransaction := quaiMsg.GetTransaction()
		transaction := convertProtoToTransaction(protoTransaction)
		return transaction, nil
	default:
		return nil, errors.Errorf("unsupported response type: %T", quaiMsg.Response)
	}
}

// Converts a custom go type to a proto type and marhsals it into a protobuf message
func ConvertAndMarshal(data interface{}) ([]byte, error) {
	switch data := data.(type) {
	case *types.Block:
		log.Tracef("marshalling block: %+v", data)
		protoBlock := convertBlockToProto(data)
		return MarshalProtoMessage(protoBlock)
	case *types.Header:
		log.Tracef("marshalling header: %+v", data)
		protoHeader := convertHeaderToProto(data)
		return MarshalProtoMessage(protoHeader)
	case *types.Transaction:
		log.Tracef("marshalling transaction: %+v", data)
		protoTransaction := convertTransactionToProto(data)
		return MarshalProtoMessage(protoTransaction)
	default:
		return nil, errors.New("unsupported data type")
	}
}

// Unmarshals a protobuf message into a proto type and converts it to a custom go type
func UnmarshalAndConvert(data []byte, dataPtr interface{}) error {
	switch dataPtr := dataPtr.(type) {
	case *types.Block:
		protoBlock := new(Block)
		err := UnmarshalProtoMessage(data, protoBlock)
		if err != nil {
			return err
		}
		block := convertProtoToBlock(protoBlock)
		*dataPtr = *block
		return nil
	case *types.Header:
		protoHeader := new(Header)
		err := UnmarshalProtoMessage(data, protoHeader)
		if err != nil {
			return err
		}
		header := convertProtoToHeader(protoHeader)
		*dataPtr = *header
		return nil
	case *types.Transaction:
		protoTransaction := new(Transaction)
		err := UnmarshalProtoMessage(data, protoTransaction)
		if err != nil {
			return err
		}
		transaction := convertProtoToTransaction(protoTransaction)
		*dataPtr = *transaction
		return nil
	default:
		return errors.New("unsupported data type")
	}
}
