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
	loc := common.Location{0, 0}

	hash := &common.Hash{}
	hash.SetBytes([]byte("mockHash"))

	id := uint32(1)

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
		{
			name:         "Block",
			input:        &types.Block{},
			expectedType: reflect.TypeOf(&types.Block{}),
		},
		{
			name:         "Transaction",
			input:        &types.Transaction{},
			expectedType: reflect.TypeOf(&types.Transaction{}),
		},
		{
			name:         "Header",
			input:        &types.Header{},
			expectedType: reflect.TypeOf(&types.Header{}),
		},
		{
			name:         "TrieNode",
			input:        trie.TrieNodeRequest{},
			expectedType: reflect.TypeOf(trie.TrieNodeRequest{}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode the QuaiRequest
			data, err := EncodeQuaiRequest(id, loc, *hash, tc.input)
			require.NoError(t, err)

			// Decode the QuaiRequest
			decodedId, decodedType, decodedLocation, decodedHash, err := DecodeQuaiRequest(data)
			assert.NoError(t, err)
			assert.Equal(t, id, decodedId)
			assert.Equal(t, loc, decodedLocation)
			assert.Equal(t, hash, decodedHash)
			assert.IsType(t, tc.expectedType, reflect.TypeOf(decodedType))
		})
	}
}

func TestEncodeDecodeTrieResponse(t *testing.T) {
	loc := common.Location{0, 0}

	hash := &common.Hash{}
	hash.SetBytes([]byte("mockHash"))

	id := uint32(1)

	trieResp := &trie.TrieNodeResponse{
		NodeData: []byte("mockNodeData"),
	}

	// Encode the QuaiRequest
	data, err := EncodeQuaiResponse(id, trieResp)
	require.NoError(t, err)

	// Decode the QuaiRequest
	decodedId, decodedType, err := DecodeQuaiResponse(data, loc)
	assert.NoError(t, err)
	assert.Equal(t, id, decodedId)
	decodedTrieResp, ok := decodedType.(*trie.TrieNodeResponse)
	assert.True(t, ok)
	assert.Equal(t, trieResp.NodeData, decodedTrieResp.NodeData)
}
