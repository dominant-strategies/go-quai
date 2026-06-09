package protocol

import (
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	mock_p2p "github.com/dominant-strategies/go-quai/p2p/mocks"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
)

func TestHandleRequestRejectsBlockHashRequestWithHashQuery(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	peerID := peer.ID("malformed-peer")
	stream := mock_p2p.NewMockStream(ctrl)
	conn := mock_p2p.NewMockConn(ctrl)
	stream.EXPECT().Conn().Return(conn).AnyTimes()
	conn.EXPECT().RemotePeer().Return(peerID).AnyTimes()

	req := &pb.QuaiRequestMessage{
		Id:       1,
		Location: common.Location{0, 0}.ProtoEncode(),
		Data:     &pb.QuaiRequestMessage_Hash{Hash: common.Hash{}.ProtoEncode()},
		Request:  &pb.QuaiRequestMessage_BlockHash{},
	}

	require.NotPanics(t, func() {
		handleRequest(req, stream, nil)
	})
}
