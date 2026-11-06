package protocol

import (
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	mock_p2p "github.com/dominant-strategies/go-quai/p2p/mocks"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	mock_protocol "github.com/dominant-strategies/go-quai/p2p/protocol/mocks"
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

func TestHandleRequestRepliesWhenBlockNumberIsMissing(t *testing.T) {
	loc := common.Location{0, 0}
	for _, test := range []struct {
		name string
		view interface{}
	}{
		{"header", &types.WorkObjectHeaderView{}},
		{"block", &types.WorkObjectBlockView{}},
		{"blocks", []*types.WorkObjectBlockView{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			node := mock_protocol.NewMockQuaiP2PNode(ctrl)
			stream := mock_p2p.NewMockStream(ctrl)
			conn := mock_p2p.NewMockConn(ctrl)
			stream.EXPECT().Conn().Return(conn).AnyTimes()
			conn.EXPECT().RemotePeer().Return(peer.ID("missing-" + test.name)).AnyTimes()
			node.EXPECT().GetBlockHashByNumber(gomock.Any(), loc).Return(nil)
			node.EXPECT().GetBandwidthCounter().Return(nil)
			stream.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil)
			var written []byte
			stream.EXPECT().Write(gomock.Any()).DoAndReturn(func(data []byte) (int, error) {
				written = append([]byte(nil), data...)
				return len(data), nil
			})

			encoded, err := pb.EncodeQuaiRequest(42, loc, big.NewInt(100), test.view)
			require.NoError(t, err)
			message, err := pb.DecodeQuaiMessage(encoded)
			require.NoError(t, err)
			handleRequest(message.GetRequest(), stream, node)

			require.GreaterOrEqual(t, len(written), 5)
			require.Equal(t, uint32(len(written)-4), binary.BigEndian.Uint32(written[:4]))
			response, err := pb.DecodeQuaiMessage(written[4:])
			require.NoError(t, err)
			id, value, err := pb.DecodeQuaiResponse(response.GetResponse())
			require.Equal(t, uint32(42), id)
			require.Nil(t, value)
			require.ErrorIs(t, err, pb.EmptyResponse)
		})
	}
}
