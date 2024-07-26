package pb

import (
	reflect "reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
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
