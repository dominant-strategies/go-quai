package pb

import (
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalUnmarshalProtoMessage(t *testing.T) {
	// Create a mock Block
	pbBlock := &Block{
		NodeCtx: 1,
	}

	// Marshal the Block
	data, err := MarshalProtoMessage(pbBlock)
	assert.NoError(t, err)

	// Unmarshal the data back into a Block
	var unmarshaledBlock Block
	err = UnmarshalProtoMessage(data, &unmarshaledBlock)
	assert.NoError(t, err)
	assert.Equal(t, pbBlock.NodeCtx, unmarshaledBlock.NodeCtx)

	// Marshal the unmarshaledBlock
	newData, err := MarshalProtoMessage(&unmarshaledBlock)
	assert.NoError(t, err)

	// Check if the marshaled data of both blocks are equal
	assert.Equal(t, data, newData)
}

func TestEncodeDecodeQuaiRequest(t *testing.T) {
	loc := new(common.Location)
	*loc = common.Location([]byte("mockLocation"))

	hash := &common.Hash{}
	hash.SetBytes([]byte("mockHash"))

	// Encode the QuaiRequest
	data, err := EncodeQuaiRequest(QuaiRequestMessage_REQUEST_BLOCK, loc, hash)
	require.NoError(t, err)

	// Decode the QuaiRequest
	decodedAction, decodedLocation, decodedHash, err := DecodeQuaiRequest(data)
	require.NoError(t, err)

	// Check if the decoded values are equal to the original values
	assert.Equal(t, QuaiRequestMessage_REQUEST_BLOCK, decodedAction)
	assert.Equal(t, loc, decodedLocation)
	assert.Equal(t, hash, decodedHash)
}

func TestEncodeDecodeQuaiResponse(t *testing.T) {
	header := new(types.Header)
	header.SetGasLimit(1000)
	header.SetGasUsed(100)

	testCases := []struct {
		name          string
		action        QuaiResponseMessage_ActionType
		data          interface{}
		expectedError bool
	}{
		{
			name:          "encode/decode header",
			action:        QuaiResponseMessage_RESPONSE_HEADER,
			data:          header,
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode the QuaiResponse
			data, err := EncodeQuaiResponse(tc.action, tc.data)
			if tc.expectedError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Decode the QuaiResponse
			decodedAction, decodedData, err := DecodeQuaiResponse(data)
			require.NoError(t, err)

			// Check if the decoded values are equal to the original values
			assert.Equal(t, tc.action, decodedAction)
			assert.Equal(t, tc.data, decodedData)
		})
	}
}

func TestConvertAndUnmarshall(t *testing.T) {
	header := new(types.Header)
	header.SetGasLimit(1000)
	header.SetGasUsed(100)

	// marshall the header
	data, err := ConvertAndMarshal(header)
	require.NoError(t, err)

	// unmarshall the header
	var unmarshalledHeader = types.Header{}
	err = UnmarshalAndConvert(data, &unmarshalledHeader)
	require.NoError(t, err)

	// check if the unmarshalled header is equal to the original header
	assert.Equal(t, header, &unmarshalledHeader)
}
