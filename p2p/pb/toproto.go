package pb

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

// Converts a custom Block type to a protobuf Block type
func convertBlockToProto(block *types.Block) *Block {
	panic("TODO: implement")
}

// Converts a custom Header type to a protobuf Header type
func convertHeaderToProto(header *types.Header) *Header {
	panic("TODO: implement")
}

// Converts a custom Transaction type to a protobuf Transaction type
func convertTransactionToProto(transaction *types.Transaction) *Transaction {
	panic("TODO: implement")

}

// Converts a custom Block type to a protobuf Block type
func convertHashToProto(hash common.Hash) *Hash {
	hashBytes := hash.Bytes()
	protoHash := &Hash{
		Hash: hashBytes[:],
	}
	return protoHash
}

// Converts a custom Location type to a protobuf Location type
func convertLocationToProto(location common.Location) *Location {
	protoLocation := Location{
		Location: location,
	}
	return &protoLocation
}
