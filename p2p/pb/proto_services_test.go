package pb

import (
	reflect "reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
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
