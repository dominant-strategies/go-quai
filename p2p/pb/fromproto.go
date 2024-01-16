package pb

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

// Converts a protobuf location type to a custom location type
func convertProtoToLocation(protoLocation *Location) common.Location {
	location := common.Location(protoLocation.GetLocation())
	return location
}

// Converts a protobuf Hash type to a custom Hash type
func convertProtoToHash(protoHash *Hash) common.Hash {
	hash := common.Hash{}
	hash.SetBytes(protoHash.Hash)
	return hash
}

// Converts a protobuf Block type to a custom Block type
func convertProtoToBlock(protoBlock *Block) *types.Block {
	panic("TODO: implement")
}

// Converts a protobuf Header type to a custom Header type
func convertProtoToHeader(protoHeader *Header) *types.Header {
	panic("TODO: implement")
}

// Converts a protobuf Transaction type to a custom Transaction type
func convertProtoToTransaction(protoTransaction *Transaction) *types.Transaction {
	panic("TODO: implement")
}
