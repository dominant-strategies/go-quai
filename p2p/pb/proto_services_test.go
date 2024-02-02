package pb

import (
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeQuaiRequest(t *testing.T) {
	loc := common.Location{0, 0}

	hash := &common.Hash{}
	hash.SetBytes([]byte("mockHash"))

	id := uint32(1)

	// Encode the QuaiRequest
	data, err := EncodeQuaiRequest(id, loc, *hash, &types.Block{})
	require.NoError(t, err)

	// Decode the QuaiRequest
	decodedId, decodedType, decodedLocation, decodedHash, err := DecodeQuaiRequest(data)

	t.Log(decodedId, decodedType, decodedLocation, decodedHash)

}
