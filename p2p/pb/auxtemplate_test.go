package pb

import (
	"bytes"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// createTestAuxTemplate creates a test AuxTemplate with sample data
func createTestAuxTemplate() *types.AuxTemplate {
	var prevHash [32]byte
	copy(prevHash[:], types.EmptyRootHash[:])

	nonce := uint32(42) // Sample nonce to vary data

	coinbaseOut := []byte{0x76, 0xa9, 0x14, byte(nonce)}

	template := &types.AuxTemplate{}
	template.SetPowID(types.Kawpow)
	template.SetPrevHash(prevHash)
	template.SetCoinbaseOut(coinbaseOut)
	template.SetVersion(0x20000000)
	template.SetNBits(0x1d00ffff)
	template.SetSignatureTime(0xffffffff)
	template.SetHeight(uint32(12345 + nonce))

	template.SetMerkleBranch([][]byte{
		bytes.Repeat([]byte{0xaa}, 32),
		bytes.Repeat([]byte{0xbb}, 32),
	})

	template.SetSigs(make([]byte, 64)) // Dummy signature

	return template
}

// TestGossipAuxTemplateEncodeDecode tests encoding and decoding of GossipAuxTemplate message
func TestGossipAuxTemplateEncodeDecode(t *testing.T) {
	// Create test AuxTemplate
	auxTemplate := createTestAuxTemplate()

	// Convert to proto
	protoAuxTemplate := auxTemplate.ProtoEncode()
	require.NotNil(t, protoAuxTemplate)

	// Create GossipAuxTemplate message
	gossipMsg := &GossipAuxTemplate{
		AuxTemplate: protoAuxTemplate,
	}

	// Marshal to bytes
	data, err := proto.Marshal(gossipMsg)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Unmarshal back
	var decoded GossipAuxTemplate
	err = proto.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Verify the decoded message
	require.NotNil(t, decoded.AuxTemplate)
	require.Equal(t, protoAuxTemplate.GetChainId(), decoded.AuxTemplate.GetChainId())
	require.Equal(t, protoAuxTemplate.GetPrevHash(), decoded.AuxTemplate.GetPrevHash())
	require.Equal(t, protoAuxTemplate.GetMerkleBranch(), decoded.AuxTemplate.GetMerkleBranch())

	// Decode back to types.AuxTemplate
	decodedTemplate := &types.AuxTemplate{}
	err = decodedTemplate.ProtoDecode(decoded.AuxTemplate)
	require.NoError(t, err)

	// Verify all fields match original
	require.Equal(t, auxTemplate.PowID(), decodedTemplate.PowID())
	require.Equal(t, auxTemplate.PrevHash(), decodedTemplate.PrevHash())
	require.Equal(t, auxTemplate.CoinbaseOut(), decodedTemplate.CoinbaseOut())
	require.Equal(t, auxTemplate.MerkleBranch(), decodedTemplate.MerkleBranch())
	require.Equal(t, auxTemplate.Version(), decodedTemplate.Version())
	require.Equal(t, auxTemplate.Bits(), decodedTemplate.Bits())
	require.Equal(t, auxTemplate.SignatureTime(), decodedTemplate.SignatureTime())
	require.Equal(t, auxTemplate.Height(), decodedTemplate.Height())
	require.Equal(t, auxTemplate.Sigs(), decodedTemplate.Sigs())
}

// TestQuaiRequestMessageWithAuxTemplate tests AuxTemplate in request messages
func TestQuaiRequestMessageWithAuxTemplate(t *testing.T) {
	auxTemplate := createTestAuxTemplate()
	protoAuxTemplate := auxTemplate.ProtoEncode()

	// Create location
	location := common.Location{1, 2}
	protoLocation := location.ProtoEncode()

	// Create request with AuxTemplate
	request := &QuaiRequestMessage{
		Id:       1234,
		Location: protoLocation,
		Request: &QuaiRequestMessage_AuxTemplate{
			AuxTemplate: protoAuxTemplate,
		},
	}

	// Marshal
	data, err := proto.Marshal(request)
	require.NoError(t, err)

	// Unmarshal
	var decoded QuaiRequestMessage
	err = proto.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Verify
	require.Equal(t, uint32(1234), decoded.GetId())
	require.NotNil(t, decoded.GetAuxTemplate())
	require.Equal(t, protoAuxTemplate.GetChainId(), decoded.GetAuxTemplate().GetChainId())

	// Verify location
	decodedLocation := common.Location{}
	decodedLocation.ProtoDecode(decoded.Location)
	require.Equal(t, location, decodedLocation)
}

// TestQuaiResponseMessageWithAuxTemplate tests AuxTemplate in response messages
func TestQuaiResponseMessageWithAuxTemplate(t *testing.T) {
	auxTemplate := createTestAuxTemplate()
	protoAuxTemplate := auxTemplate.ProtoEncode()

	// Create response with AuxTemplate
	response := &QuaiResponseMessage{
		Id: 5678,
		Response: &QuaiResponseMessage_AuxTemplate{
			AuxTemplate: protoAuxTemplate,
		},
	}

	// Marshal
	data, err := proto.Marshal(response)
	require.NoError(t, err)

	// Unmarshal
	var decoded QuaiResponseMessage
	err = proto.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Verify
	require.Equal(t, uint32(5678), decoded.GetId())
	require.NotNil(t, decoded.GetAuxTemplate())
	require.Equal(t, protoAuxTemplate.GetChainId(), decoded.GetAuxTemplate().GetChainId())
}

// TestEmptyGossipAuxTemplate tests encoding/decoding of empty GossipAuxTemplate
func TestEmptyGossipAuxTemplate(t *testing.T) {
	// Create empty message
	gossipMsg := &GossipAuxTemplate{}

	// Marshal
	data, err := proto.Marshal(gossipMsg)
	require.NoError(t, err)
	// Empty message marshals to empty bytes in proto3

	// Unmarshal
	var decoded GossipAuxTemplate
	err = proto.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Verify it's nil
	require.Nil(t, decoded.AuxTemplate)
}

// TestQuaiMessageWithMultipleFields tests that AuxTemplate doesn't interfere with other fields
func TestQuaiMessageWithMultipleFields(t *testing.T) {
	// Create various message components
	auxTemplate := createTestAuxTemplate()
	protoAuxTemplate := auxTemplate.ProtoEncode()

	hash := common.BytesToHash([]byte{1, 2, 3})
	protoHash := hash.ProtoEncode()

	location := common.Location{0, 1}
	protoLocation := location.ProtoEncode()

	// Create message with multiple fields
	message := &QuaiRequestMessage{
		Id:       999,
		Location: protoLocation,
		Data: &QuaiRequestMessage_Hash{
			Hash: protoHash,
		},
		Request: &QuaiRequestMessage_AuxTemplate{
			AuxTemplate: protoAuxTemplate,
		},
	}

	// Marshal
	data, err := proto.Marshal(message)
	require.NoError(t, err)

	// Unmarshal
	var decoded QuaiRequestMessage
	err = proto.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Verify all fields
	require.Equal(t, uint32(999), decoded.GetId())
	require.NotNil(t, decoded.Location)
	require.NotNil(t, decoded.GetHash())
	require.NotNil(t, decoded.GetAuxTemplate())

	// Decode and verify AuxTemplate
	decodedTemplate := &types.AuxTemplate{}
	err = decodedTemplate.ProtoDecode(decoded.GetAuxTemplate())
	require.NoError(t, err)
	require.Equal(t, auxTemplate.PowID(), decodedTemplate.PowID())
}

// Benchmark tests
func BenchmarkGossipAuxTemplateEncode(b *testing.B) {
	auxTemplate := createTestAuxTemplate()
	protoAuxTemplate := auxTemplate.ProtoEncode()
	gossipMsg := &GossipAuxTemplate{
		AuxTemplate: protoAuxTemplate,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = proto.Marshal(gossipMsg)
	}
}

func BenchmarkGossipAuxTemplateDecode(b *testing.B) {
	auxTemplate := createTestAuxTemplate()
	protoAuxTemplate := auxTemplate.ProtoEncode()
	gossipMsg := &GossipAuxTemplate{
		AuxTemplate: protoAuxTemplate,
	}
	data, _ := proto.Marshal(gossipMsg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var decoded GossipAuxTemplate
		_ = proto.Unmarshal(data, &decoded)
	}
}
