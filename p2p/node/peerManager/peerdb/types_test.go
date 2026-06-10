package peerdb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPeerInfoProtoDecodeRejectsNil(t *testing.T) {
	var info PeerInfo
	require.Error(t, info.ProtoDecode(nil))
	require.Error(t, info.ProtoDecode(&ProtoPeerInfo{}))
}

func TestAddrInfoProtoDecodeRejectsNil(t *testing.T) {
	var addr AddrInfo
	require.Error(t, addr.ProtoDecode(nil))
}
