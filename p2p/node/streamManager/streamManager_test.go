package streamManager

import (
	"testing"

	gomock "go.uber.org/mock/gomock"

	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"

	mock_p2p "github.com/dominant-strategies/go-quai/p2p/mocks"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
	mock_protocol "github.com/dominant-strategies/go-quai/p2p/protocol/mocks"
)

func TestOpenStream(t *testing.T) {
	//Test setup
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockNode := mock_protocol.NewMockQuaiP2PNode(ctrl)
	mockHost := mock_p2p.NewMockHost(ctrl)

	sm, err := NewStreamManager(mockNode, mockHost)
	if err != nil {
		t.Fatal("Failed to create stream manager")
	}
	sm.Start()

	mockHost2 := mock_p2p.NewMockHost(ctrl)
	peerID := peer.ID("host2")
	mockHost2.EXPECT().ID().Return(peerID).Times(2)

	// # Error case
	mockHost.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("mock error")).Times(1)

	err = sm.OpenStream(mockHost2.ID())

	require.Error(t, err)

	// # Success case
	MockLibp2pStream := mock_p2p.NewMockStream(ctrl)
	MockConn := mock_p2p.NewMockConn(ctrl)

	mockNode.EXPECT().GetBandwidthCounter().Return(nil).AnyTimes()
	MockLibp2pStream.EXPECT().Close().Return(nil).AnyTimes()
	MockLibp2pStream.EXPECT().Conn().Return(MockConn).Times(1)
	MockLibp2pStream.EXPECT().Protocol().Return(protocol.ProtocolVersion).Times(1)
	MockLibp2pStream.EXPECT().Read(gomock.Any()).Return(0, nil).AnyTimes()
	MockConn.EXPECT().RemotePeer().Return(peerID)
	mockHost.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any()).Return(MockLibp2pStream, nil).Times(1)

	// GetStream Error
	entry, err := sm.GetStream(peerID)
	require.Error(t, err)
	require.Nil(t, entry)

	err = sm.OpenStream(mockHost2.ID())

	require.NoError(t, err)

	// Get Stream Success
	entry, err = sm.GetStream(peerID)
	require.NoError(t, err)
	require.Equal(t, MockLibp2pStream, entry)
}
