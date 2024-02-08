package pb

import (
	"math/big"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/trie"
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
func EncodeQuaiRequest(id uint32, location common.Location, data interface{}, datatype interface{}) ([]byte, error) {
	reqMsg := QuaiRequestMessage{
		Id:       id,
		Location: location.ProtoEncode(),
	}

	switch d := data.(type) {
	case common.Hash:
		reqMsg.Data = &QuaiRequestMessage_Hash{Hash: d.ProtoEncode()}
	case *big.Int:
		reqMsg.Data = &QuaiRequestMessage_Number{Number: d.Bytes()}
	default:
		return nil, errors.Errorf("unsupported request input data field type: %T", data)
	}

	switch datatype.(type) {
	case *types.Block:
		reqMsg.Request = &QuaiRequestMessage_Block{}
	case *types.Header:
		reqMsg.Request = &QuaiRequestMessage_Header{}
	case *types.Transaction:
		reqMsg.Request = &QuaiRequestMessage_Transaction{}
	case common.Hash:
		reqMsg.Request = &QuaiRequestMessage_BlockHash{}
	case trie.TrieNodeRequest:
		reqMsg.Request = &QuaiRequestMessage_TrieNode{}
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
//  4. The request data
//  5. An error
func DecodeQuaiRequest(data []byte) (uint32, interface{}, common.Location, interface{}, error) {
	var reqMsg QuaiRequestMessage
	err := UnmarshalProtoMessage(data, &reqMsg)
	if err != nil {
		return 0, nil, common.Location{}, nil, err
	}

	location := &common.Location{}
	location.ProtoDecode(reqMsg.Location)

	// First Decode the request data field
	var reqData interface{}
	switch d := reqMsg.Data.(type) {
	case *QuaiRequestMessage_Hash:
		hash := &common.Hash{}
		hash.ProtoDecode(d.Hash)
		reqData = hash
	case *QuaiRequestMessage_Number:
		reqData = new(big.Int).SetBytes(d.Number)
	}

	// Decode the request type
	var reqType interface{}
	switch t := reqMsg.Request.(type) {
	case *QuaiRequestMessage_Block:
		reqType = &types.Block{}
	case *QuaiRequestMessage_Header:
		reqType = &types.Header{}
	case *QuaiRequestMessage_Transaction:
		reqType = &types.Transaction{}
	case *QuaiRequestMessage_BlockHash:
		blockHash := &common.Hash{}
		blockHash.ProtoDecode(t.BlockHash)
		reqType = blockHash
	case *QuaiRequestMessage_TrieNode:
		reqType = trie.TrieNodeRequest{}
	default:
		return reqMsg.Id, nil, common.Location{}, common.Hash{}, errors.Errorf("unsupported request type: %T", reqMsg.Request)
	}

	return reqMsg.Id, reqType, *location, reqData, nil
}

// EncodeResponse creates a marshaled protobuf message for a Quai Response.
// Returns the serialized protobuf message.
func EncodeQuaiResponse(id uint32, data interface{}) ([]byte, error) {

	respMsg := QuaiResponseMessage{
		Id: id,
	}

	switch data := data.(type) {
	case *types.Block:
		protoBlock, err := data.ProtoEncode()
		if err != nil {
			return nil, err
		}
		respMsg.Response = &QuaiResponseMessage_Block{Block: protoBlock}
	case *types.Header:
		protoHeader, err := data.ProtoEncode()
		if err != nil {
			return nil, err
		}
		respMsg.Response = &QuaiResponseMessage_Header{Header: protoHeader}
	case *types.Transaction:
		protoTransaction, err := data.ProtoEncode()
		if err != nil {
			return nil, err
		}
		respMsg.Response = &QuaiResponseMessage_Transaction{Transaction: protoTransaction}
	case *trie.TrieNodeResponse:
		protoTrieNode := &trie.ProtoTrieNode{ProtoNodeData: data.NodeData}
		respMsg.Response = &QuaiResponseMessage_TrieNode{TrieNode: protoTrieNode}

	case *common.Hash:
		respMsg.Response = &QuaiResponseMessage_BlockHash{BlockHash: data.ProtoEncode()}
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
func DecodeQuaiResponse(data []byte, sourceLocation common.Location) (uint32, interface{}, error) {
	var respMsg QuaiResponseMessage
	err := UnmarshalProtoMessage(data, &respMsg)
	if err != nil {
		return 0, nil, err
	}

	id := respMsg.Id

	switch respMsg.Response.(type) {
	case *QuaiResponseMessage_Block:
		protoBlock := respMsg.GetBlock()
		block := &types.Block{}
		err := block.ProtoDecode(protoBlock, sourceLocation)
		if err != nil {
			return id, nil, err
		}
		return id, block, nil
	case *QuaiResponseMessage_Header:
		protoHeader := respMsg.GetHeader()
		header := &types.Header{}
		err := header.ProtoDecode(protoHeader)
		if err != nil {
			return id, nil, err
		}
		return id, header, nil
	case *QuaiResponseMessage_Transaction:
		protoTransaction := respMsg.GetTransaction()
		transaction := &types.Transaction{}
		err := transaction.ProtoDecode(protoTransaction, sourceLocation)
		if err != nil {
			return id, nil, err
		}
		return id, transaction, nil
	case *QuaiResponseMessage_BlockHash:
		blockHash := respMsg.GetBlockHash()
		hash := common.Hash{}
		hash.ProtoDecode(blockHash)
		return id, hash, nil
	case *QuaiResponseMessage_TrieNode:
		protoTrieNode := respMsg.GetTrieNode()
		trieNode := &trie.TrieNodeResponse{NodeData: protoTrieNode.ProtoNodeData}
		return id, trieNode, nil
	default:
		return id, nil, errors.Errorf("unsupported response type: %T", respMsg.Response)
	}
}

// Converts a custom go type to a proto type and marhsals it into a protobuf message
func ConvertAndMarshal(data interface{}) ([]byte, error) {
	switch data := data.(type) {
	case *types.Block:
		log.Global.Tracef("marshalling block: %+v", data)
		protoBlock, err := data.ProtoEncode()
		if err != nil {
			return nil, err
		}
		return MarshalProtoMessage(protoBlock)
	case *types.Header:
		log.Global.Tracef("marshalling header: %+v", data)
		protoHeader, err := data.ProtoEncode()
		if err != nil {
			return nil, err
		}
		return MarshalProtoMessage(protoHeader)
	case *types.Transaction:
		log.Global.Tracef("marshalling transaction: %+v", data)
		protoTransaction, err := data.ProtoEncode()
		if err != nil {
			return nil, err
		}
		return MarshalProtoMessage(protoTransaction)
	case common.Hash:
		log.Global.Tracef("marshalling hash: %+v", data)
		protoHash := data.ProtoEncode()
		return MarshalProtoMessage(protoHash)
	default:
		return nil, errors.New("unsupported data type")
	}
}

// Unmarshals a protobuf message into a proto type and converts it to a custom go type
func UnmarshalAndConvert(data []byte, sourceLocation common.Location, dataPtr *interface{}, datatype interface{}) error {
	switch datatype.(type) {
	case *types.Block:
		protoBlock := &types.ProtoBlock{}
		err := UnmarshalProtoMessage(data, protoBlock)
		if err != nil {
			return err
		}
		block := &types.Block{}
		err = block.ProtoDecode(protoBlock, sourceLocation)
		if err != nil {
			return err
		}
		*dataPtr = *block
		return nil
	case *types.Header:
		protoHeader := &types.ProtoHeader{}
		err := UnmarshalProtoMessage(data, protoHeader)
		if err != nil {
			return err
		}
		header := &types.Header{}
		err = header.ProtoDecode(protoHeader)
		if err != nil {
			return err
		}
		*dataPtr = *header
		return nil
	case *types.Transaction:
		protoTransaction := &types.ProtoTransaction{}
		err := UnmarshalProtoMessage(data, protoTransaction)
		if err != nil {
			return err
		}
		transaction := &types.Transaction{}
		err = transaction.ProtoDecode(protoTransaction, sourceLocation)
		if err != nil {
			return err
		}
		*dataPtr = *transaction
		return nil
	case common.Hash:
		protoHash := &common.ProtoHash{}
		err := UnmarshalProtoMessage(data, protoHash)
		if err != nil {
			return err
		}
		hash := common.Hash{}
		hash.ProtoDecode(protoHash)
		*dataPtr = hash
		return nil
	default:
		return errors.New("unsupported data type")
	}
}
