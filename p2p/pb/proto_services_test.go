package pb

import (
	reflect "reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/trie"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeRequest(t *testing.T) {
	t.Skip("Fix broken test")
	loc := common.Location{0, 0}

	hash := &common.Hash{}
	hash.SetBytes([]byte("mockHash"))

	id := uint32(1)

	//TODO: Add transaction, workobject and header to test cases
	testCases := []struct {
		name         string
		input        interface{}
		expectedType reflect.Type
	}{
		{
			name:         "Hash",
			input:        common.Hash{},
			expectedType: reflect.TypeOf(common.Hash{}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode the QuaiRequest
			data, err := EncodeQuaiRequest(id, loc, *hash, tc.input)
			require.NoError(t, err)

			// Decode the QuaiRequest
			quaiMsg, err := DecodeQuaiMessage(data)
			if err != nil {
				t.Fatal(err)
			}
			decodedId, decodedType, decodedLocation, decodedHash, err := DecodeQuaiRequest(quaiMsg.GetRequest())
			assert.NoError(t, err)
			assert.Equal(t, id, decodedId)
			assert.Equal(t, loc, decodedLocation)
			assert.Equal(t, hash, decodedHash)
			assert.IsType(t, tc.expectedType, reflect.TypeOf(decodedType))
		})
	}
}

func TestDecodeQuaiRequestRejectsNil(t *testing.T) {
	_, _, _, _, err := DecodeQuaiRequest(nil)
	require.Error(t, err)
}

func TestDecodeQuaiResponseRejectsNil(t *testing.T) {
	_, _, err := DecodeQuaiResponse(nil)
	require.Error(t, err)
}

func TestEncodeQuaiResponseBlockHash(t *testing.T) {
	hash := common.Hash{1, 2, 3}
	encoded, err := EncodeQuaiResponse(7, common.Location{}, &hash, &hash)
	require.NoError(t, err)
	message, err := DecodeQuaiMessage(encoded)
	require.NoError(t, err)
	id, decoded, err := DecodeQuaiResponse(message.GetResponse())
	require.NoError(t, err)
	require.Equal(t, uint32(7), id)
	require.Equal(t, hash, decoded)
	var missing *common.Hash
	encoded, err = EncodeQuaiResponse(8, common.Location{}, &hash, missing)
	require.NoError(t, err)
	message, err = DecodeQuaiMessage(encoded)
	require.NoError(t, err)
	_, decoded, err = DecodeQuaiResponse(message.GetResponse())
	require.NoError(t, err)
	require.Equal(t, common.Hash{}, decoded)
}

func TestEmptyWorkObjectResponseRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name string
		view interface{}
	}{
		{"header", &types.WorkObjectHeaderView{}},
		{"block", &types.WorkObjectBlockView{}},
		{"blocks", []*types.WorkObjectBlockView{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := EncodeQuaiResponse(42, common.Location{0, 0}, test.view, nil)
			require.NoError(t, err)
			message, err := DecodeQuaiMessage(encoded)
			require.NoError(t, err)
			id, result, err := DecodeQuaiResponse(message.GetResponse())
			require.Equal(t, uint32(42), id)
			require.Nil(t, result)
			require.ErrorIs(t, err, EmptyResponse)
		})
	}
}

func TestTrieNodeRequestResponseRoundTrip(t *testing.T) {
	loc := common.Location{0, 0}
	reqHash := common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")

	encodedReq, err := EncodeQuaiRequest(99, loc, reqHash, &trie.TrieNodeResponse{})
	require.NoError(t, err)
	msg, err := DecodeQuaiMessage(encodedReq)
	require.NoError(t, err)
	id, reqType, decodedLoc, query, err := DecodeQuaiRequest(msg.GetRequest())
	require.NoError(t, err)
	require.Equal(t, uint32(99), id)
	require.Equal(t, loc, decodedLoc)
	require.IsType(t, &trie.TrieNodeResponse{}, reqType)
	require.Equal(t, &reqHash, query)

	// Hit round-trip
	blob := []byte{0xca, 0xfe, 0xba, 0xbe}
	encodedResp, err := EncodeQuaiResponse(99, loc, &trie.TrieNodeResponse{}, &trie.TrieNodeResponse{NodeData: blob})
	require.NoError(t, err)
	respMsg, err := DecodeQuaiMessage(encodedResp)
	require.NoError(t, err)
	respID, decodedResp, err := DecodeQuaiResponse(respMsg.GetResponse())
	require.NoError(t, err)
	require.Equal(t, uint32(99), respID)
	require.Equal(t, &trie.TrieNodeResponse{NodeData: blob}, decodedResp)

	// Miss round-trip -> EmptyResponse
	for _, missing := range []interface{}{nil, (*trie.TrieNodeResponse)(nil), &trie.TrieNodeResponse{}} {
		encodedEmpty, err := EncodeQuaiResponse(100, loc, &trie.TrieNodeResponse{}, missing)
		require.NoError(t, err)
		emptyMsg, err := DecodeQuaiMessage(encodedEmpty)
		require.NoError(t, err)
		emptyID, emptyVal, err := DecodeQuaiResponse(emptyMsg.GetResponse())
		require.Equal(t, uint32(100), emptyID)
		require.Nil(t, emptyVal)
		require.ErrorIs(t, err, EmptyResponse)
	}
}

func TestTrieNodesBatchRequestResponseRoundTrip(t *testing.T) {
	loc := common.Location{0, 0}
	hashes := []common.Hash{
		common.HexToHash("0x0100000000000000000000000000000000000000000000000000000000000001"),
		common.HexToHash("0x0200000000000000000000000000000000000000000000000000000000000002"),
	}

	encodedReq, err := EncodeQuaiRequest(101, loc, hashes, &trie.TrieNodesResponse{})
	require.NoError(t, err)
	msg, err := DecodeQuaiMessage(encodedReq)
	require.NoError(t, err)
	id, reqType, decodedLoc, query, err := DecodeQuaiRequest(msg.GetRequest())
	require.NoError(t, err)
	require.Equal(t, uint32(101), id)
	require.Equal(t, loc, decodedLoc)
	require.IsType(t, &trie.TrieNodesResponse{}, reqType)
	require.Equal(t, hashes, query)

	// Hit round-trip
	nodes := [][]byte{{0x01, 0x02}, {0x03, 0x04, 0x05}}
	encodedResp, err := EncodeQuaiResponse(101, loc, &trie.TrieNodesResponse{}, &trie.TrieNodesResponse{Nodes: nodes})
	require.NoError(t, err)
	respMsg, err := DecodeQuaiMessage(encodedResp)
	require.NoError(t, err)
	respID, decodedResp, err := DecodeQuaiResponse(respMsg.GetResponse())
	require.NoError(t, err)
	require.Equal(t, uint32(101), respID)
	require.Equal(t, &trie.TrieNodesResponse{Nodes: nodes}, decodedResp)

	// Empty batch miss -> EmptyResponse
	for _, missing := range []interface{}{nil, (*trie.TrieNodesResponse)(nil), &trie.TrieNodesResponse{}} {
		encodedEmpty, err := EncodeQuaiResponse(102, loc, &trie.TrieNodesResponse{}, missing)
		require.NoError(t, err)
		emptyMsg, err := DecodeQuaiMessage(encodedEmpty)
		require.NoError(t, err)
		emptyID, emptyVal, err := DecodeQuaiResponse(emptyMsg.GetResponse())
		require.Equal(t, uint32(102), emptyID)
		require.Nil(t, emptyVal)
		require.ErrorIs(t, err, EmptyResponse)
	}

	// Reject empty hash list on encode and decode
	_, err = EncodeQuaiRequest(103, loc, []common.Hash{}, &trie.TrieNodesResponse{})
	require.Error(t, err)
	_, _, _, _, err = DecodeQuaiRequest(&QuaiRequestMessage{
		Id:       103,
		Location: loc.ProtoEncode(),
		Data:     &QuaiRequestMessage_Hashes{Hashes: &ProtoHashList{}},
		Request:  &QuaiRequestMessage_TrieNodes{},
	})
	require.Error(t, err)

	// Reject oversized hash list on encode and decode
	tooMany := make([]common.Hash, trie.MaxTrieNodeBatchSize+1)
	_, err = EncodeQuaiRequest(104, loc, tooMany, &trie.TrieNodesResponse{})
	require.Error(t, err)
	protoTooMany := make([]*common.ProtoHash, trie.MaxTrieNodeBatchSize+1)
	for i := range protoTooMany {
		protoTooMany[i] = (common.Hash{}).ProtoEncode()
	}
	_, _, _, _, err = DecodeQuaiRequest(&QuaiRequestMessage{
		Id:       104,
		Location: loc.ProtoEncode(),
		Data:     &QuaiRequestMessage_Hashes{Hashes: &ProtoHashList{Hashes: protoTooMany}},
		Request:  &QuaiRequestMessage_TrieNodes{},
	})
	require.Error(t, err)

	// Reject invalid hash length on decode
	_, _, _, _, err = DecodeQuaiRequest(&QuaiRequestMessage{
		Id:       105,
		Location: loc.ProtoEncode(),
		Data:     &QuaiRequestMessage_Hashes{Hashes: &ProtoHashList{Hashes: []*common.ProtoHash{{Value: []byte{1, 2, 3}}}}},
		Request:  &QuaiRequestMessage_TrieNodes{},
	})
	require.Error(t, err)

	// Reject oversized response batch on decode
	tooManyNodes := make([][]byte, trie.MaxTrieNodeBatchSize+1)
	for i := range tooManyNodes {
		tooManyNodes[i] = []byte{0x01}
	}
	_, _, err = DecodeQuaiResponse(&QuaiResponseMessage{
		Id:       106,
		Location: loc.ProtoEncode(),
		Response: &QuaiResponseMessage_TrieNodes{TrieNodes: &ProtoTrieNodes{Nodes: tooManyNodes}},
	})
	require.Error(t, err)
}
