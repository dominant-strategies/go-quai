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

func setup(t *testing.T) (*gomock.Controller, *mock_protocol.MockQuaiP2PNode, *mock_p2p.MockHost, *basicStreamManager) {
	ctrl := gomock.NewController(t)
	mockNode := mock_protocol.NewMockQuaiP2PNode(ctrl)
	mockHost := mock_p2p.NewMockHost(ctrl)

	sm, err := NewStreamManager(mockNode, mockHost)
	require.NoError(t, err, "Failed to create stream manager")
	sm.Start()

	return ctrl, mockNode, mockHost, sm
}

func TestStreamManager(t *testing.T) {
	ctrl, mockNode, mockHost, sm := setup(t)
	defer ctrl.Finish()

	peerID := peer.ID("mockPeerID")
	mockHost.EXPECT().ID().Return(peerID).Times(2)

	t.Run("Error case - NewStream returns error", func(t *testing.T) {
		mockHost.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("mock error")).Times(1)

		err := sm.OpenStream(mockHost.ID())
		require.Error(t, err, "Expected error when NewStream returns error")
	})

	t.Run("Success case - OpenStream, GetStream and CloseStream", func(t *testing.T) {
		mockLibp2pStream := mock_p2p.NewMockStream(ctrl)
		mockConn := mock_p2p.NewMockConn(ctrl)

		mockNode.EXPECT().GetBandwidthCounter().Return(nil).AnyTimes()
		mockLibp2pStream.EXPECT().Close().Return(nil).AnyTimes()
		mockLibp2pStream.EXPECT().Conn().Return(mockConn).AnyTimes()
		mockLibp2pStream.EXPECT().Protocol().Return(protocol.ProtocolVersion).AnyTimes()
		mockLibp2pStream.EXPECT().Read(gomock.Any()).Return(0, nil).AnyTimes()
		mockConn.EXPECT().RemotePeer().Return(peerID).AnyTimes()
		mockHost.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockLibp2pStream, nil).AnyTimes()

		// GetStream Error
		entry, err := sm.GetStream(peerID)
		require.Error(t, err, "Expected error when stream does not exist")
		require.Nil(t, entry, "Expected nil entry when stream does not exist")

		err = sm.OpenStream(mockHost.ID())
		require.NoError(t, err, "Expected no error when opening stream")

		// Get Stream Success
		entry, err = sm.GetStream(peerID)
		require.NoError(t, err, "Expected no error when getting stream")
		require.Equal(t, mockLibp2pStream, entry, "Expected correct stream entry")

		// Close stream assertions
		err = sm.CloseStream(peerID)
		require.NoError(t, err, "Expected no error when closing stream")

		// Stream already closed
		err = sm.CloseStream(peerID)
		require.Error(t, err, "Expected error when closing already closed stream")
	})

	t.Run("SetP2PBackend", func(t *testing.T) {
		newMockNode := mock_protocol.NewMockQuaiP2PNode(ctrl)
		// Ensure it's a new node
		require.NotSame(t, newMockNode, sm.p2pBackend, "Expected different mock node")
		sm.SetP2PBackend(newMockNode)
		require.Same(t, newMockNode, sm.p2pBackend, "Expected new mock node to be set")
	})

	t.Run("Host accesors", func(t *testing.T) {
		require.Same(t, mockHost, sm.GetHost(), "Expected same host")
		newMockHost := mock_p2p.NewMockHost(ctrl)
		require.NotSame(t, newMockHost, sm.host, "Expected different host")
		sm.SetHost(newMockHost)
		require.Same(t, newMockHost, sm.host, "Expected new host to be set")
	})
}

func TestWriteMessageToStream(t *testing.T) {
	ctrl, mockNode, mockHost, sm := setup(t)
	defer ctrl.Finish()

	peerID := peer.ID("mockPeerID")
	mockHost.EXPECT().ID().Return(peerID).Times(2)

	t.Run("WriteMessageToStream - Stream not found", func(t *testing.T) {
		err := sm.WriteMessageToStream(peerID, nil, nil, protocol.ProtocolVersion, nil)
		require.ErrorIs(t, err, ErrStreamNotFound, "Expected error when stream not found")
	})

	mockLibp2pStream := mock_p2p.NewMockStream(ctrl)
	mockHost.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockLibp2pStream, nil).AnyTimes()

	MockConn := mock_p2p.NewMockConn(ctrl)

	mockNode.EXPECT().GetBandwidthCounter().Return(nil).AnyTimes()
	mockLibp2pStream.EXPECT().Close().Return(nil).AnyTimes()
	mockLibp2pStream.EXPECT().Conn().Return(MockConn).AnyTimes()
	mockLibp2pStream.EXPECT().Protocol().Return(protocol.ProtocolVersion).AnyTimes()
	mockLibp2pStream.EXPECT().Read(gomock.Any()).Return(0, nil).AnyTimes()
	MockConn.EXPECT().RemotePeer().Return(peerID).AnyTimes()

	err := sm.OpenStream(mockHost.ID())
	require.NoError(t, err, "Expected no error when opening stream")

	t.Run("Stream mismatch", func(t *testing.T) {

		anotherStream := mock_p2p.NewMockStream(ctrl)
		err = sm.WriteMessageToStream(peerID, anotherStream, []byte("message"), protocol.ProtocolVersion, nil)
		require.ErrorIs(t, err, ErrStreamMismatch, "Expected error when stream mismatch")
	})

	t.Run("Too many pending requests", func(t *testing.T) {
		// small semaphore to block the stream
		wrappedStream := streamWrapper{
			stream:    mockLibp2pStream,
			semaphore: make(chan struct{}, 1),
			errCount:  0,
		}
		// block semaphore
		wrappedStream.semaphore <- struct{}{}
		sm.streamCache.Add(peerID, wrappedStream)
		err := sm.WriteMessageToStream(peerID, mockLibp2pStream, []byte("message"), protocol.ProtocolVersion, nil)
		require.ErrorIs(t, err, ErrorTooManyPendingRequests, "Expected error when too many pending requests")

		// errCount is maxed out so it should close stream
		wrappedStream.errCount = c_maxPendingRequests
		sm.streamCache.Add(peerID, wrappedStream)

		// make sure stream exists
		entry, err := sm.GetStream(peerID)
		require.NoError(t, err, "Expected no error when getting stream")
		require.Equal(t, mockLibp2pStream, entry, "Expected correct stream entry")

		err = sm.WriteMessageToStream(peerID, mockLibp2pStream, []byte("message"), protocol.ProtocolVersion, nil)
		require.ErrorIs(t, err, ErrorTooManyPendingRequests, "Expected error when too many pending requests")

		// check if stream was closed
		entry, err = sm.GetStream(peerID)
		require.Nil(t, entry, "Expected nil entry")
		require.ErrorIs(t, err, ErrStreamNotFound, "Expected error when stream not found")
	})

	t.Run("Failed to set write deadline", func(t *testing.T) {
		err := sm.OpenStream(mockHost.ID())
		require.NoError(t, err, "Expected no error when opening stream")

		mockLibp2pStream.EXPECT().SetWriteDeadline(gomock.Any()).Return(errors.New("mock error")).Times(1)

		err = sm.WriteMessageToStream(peerID, mockLibp2pStream, []byte("message"), protocol.ProtocolVersion, nil)
		require.Error(t, err, "Expected error when failed to set write deadline")
	})

	t.Run("Failed to write message to stream", func(t *testing.T) {
		mockLibp2pStream.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil).Times(1)
		mockLibp2pStream.EXPECT().Write(gomock.Any()).Return(0, errors.New("mock error")).Times(1)

		err = sm.WriteMessageToStream(peerID, mockLibp2pStream, []byte("message"), protocol.ProtocolVersion, nil)
		require.Error(t, err, "Expected error when failed to write message to stream")
	})

	t.Run("Succes write message to stream", func(t *testing.T) {
		mockLibp2pStream.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil).Times(2)
		mockLibp2pStream.EXPECT().Write(gomock.Any()).Return(10, nil).Times(2)

		// without reporter
		err = sm.WriteMessageToStream(peerID, mockLibp2pStream, []byte("message"), protocol.ProtocolVersion, nil)
		require.NoError(t, err, "Expected no error when writing message to stream")

		//with reporter
		mockReporter := mock_p2p.NewMockReporter(ctrl)
		mockReporter.EXPECT().LogSentMessageStream(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
		err = sm.WriteMessageToStream(peerID, mockLibp2pStream, []byte("message"), protocol.ProtocolVersion, mockReporter)
		require.NoError(t, err, "Expected no error when writing message to stream")
	})
}
