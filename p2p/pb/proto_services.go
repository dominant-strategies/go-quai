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
func EncodeQuaiRequest(id uint32, location common.Location, hash common.Hash, datatype interface{}) ([]byte, error) {
	reqMsg := QuaiRequestMessage{
		Id:       id,
		Location: convertLocationToProto(location),
		Hash:     convertHashToProto(hash),
	}

	switch datatype.(type) {
	case *types.Block:
		reqMsg.Request = &QuaiRequestMessage_Block{}
	case *types.Header:
		reqMsg.Request = &QuaiRequestMessage_Header{}
	case *types.Transaction:
		reqMsg.Request = &QuaiRequestMessage_Transaction{}
	default:
		return nil, errors.Errorf("unsupported request data type: %T", datatype)
	}

	return MarshalProtoMessage(&reqMsg)
}

// DecodeRequestMessage unmarshals a protobuf message into a Quai Request.
// Returns:
//  1. The request ID
//  2. The decoded type (i.e. *types.Header, *types.Block, etc)
//  3. The location
//  4. The hash
//  5. An error
func DecodeQuaiRequest(data []byte) (uint32, interface{}, common.Location, common.Hash, error) {
	var reqMsg QuaiRequestMessage
	err := UnmarshalProtoMessage(data, &reqMsg)
	if err != nil {
		return 0, nil, common.Location{}, common.Hash{}, err
	}

	location := convertProtoToLocation(reqMsg.Location)
	hash := convertProtoToHash(reqMsg.Hash)
	id := reqMsg.Id

	switch reqMsg.Request.(type) {
	case *QuaiRequestMessage_Block:
		return id, &types.Block{}, location, hash, nil
	case *QuaiRequestMessage_Header:
		return id, &types.Header{}, location, hash, nil
	case *QuaiRequestMessage_Transaction:
		return id, &types.Transaction{}, location, hash, nil
	default:
		return 0, nil, common.Location{}, common.Hash{}, errors.Errorf("unsupported request type: %T", reqMsg.Request)
	}
}

// EncodeResponse creates a marshaled protobuf message for a Quai Response.
// Returns the serialized protobuf message.
func EncodeQuaiResponse(id uint32, data interface{}) ([]byte, error) {

	respMsg := QuaiResponseMessage{
		Id: id,
	}

	switch data := data.(type) {
	case *types.Block:
		respMsg.Response = &QuaiResponseMessage_Block{Block: convertBlockToProto(data)}
	case *types.Header:
		respMsg.Response = &QuaiResponseMessage_Header{Header: convertHeaderToProto(data)}
	case *types.Transaction:
		respMsg.Response = &QuaiResponseMessage_Transaction{Transaction: convertTransactionToProto(data)}

	default:
		return nil, errors.Errorf("unsupported response data type: %T", data)
	}

	return MarshalProtoMessage(&respMsg)
}

// Unmarshals a serialized protobuf message into a Quai Response message.
// Returns:
//  1. The request ID
//  2. The decoded type (i.e. *types.Header, *types.Block, etc)
//  3. An error
func DecodeQuaiResponse(data []byte) (uint32, interface{}, error) {
	var respMsg QuaiResponseMessage
	err := UnmarshalProtoMessage(data, &respMsg)
	if err != nil {
		return 0, nil, err
	}

	id := respMsg.Id

	switch respMsg.Response.(type) {
	case *QuaiResponseMessage_Block:
		protoBlock := respMsg.GetBlock()
		block := convertProtoToBlock(protoBlock)
		return id, block, nil
	case *QuaiResponseMessage_Header:
		protoHeader := respMsg.GetHeader()
		header := convertProtoToHeader(protoHeader)
		return id, header, nil
	case *QuaiResponseMessage_Transaction:
		protoTransaction := respMsg.GetTransaction()
		transaction := convertProtoToTransaction(protoTransaction)
		return id, transaction, nil
	default:
		return id, nil, errors.Errorf("unsupported response type: %T", respMsg.Response)
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
