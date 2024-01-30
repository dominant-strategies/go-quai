package types

import (
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/stretchr/testify/require"
)

func TestManifestEncodeDecode(t *testing.T) {
	// Create a new manifest
	hash1 := common.BytesToHash([]byte{0x01})
	manifest := BlockManifest{hash1}
	manifest = append(manifest, hash1)

	// Encode the manifest to ProtoManifest format
	protoManifest, err := manifest.ProtoEncode()
	if err != nil {
		t.Errorf("Failed to encode manifest: %v", err)
	}

	// Decode the ProtoManifest into a new Manifest
	decodedManifest := BlockManifest{}
	err = decodedManifest.ProtoDecode(protoManifest)
	if err != nil {
		t.Errorf("Failed to decode manifest: %v", err)
	}

	require.Equal(t, manifest, decodedManifest)

}
