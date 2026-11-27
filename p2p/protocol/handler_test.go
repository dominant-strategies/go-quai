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
	"github.com/dominant-strategies/go-quai/trie"
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

func TestHandleRequestTrieNode(t *testing.T) {
	loc := common.Location{0, 0}
	reqHash := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	blob := []byte{0xde, 0xad, 0xbe, 0xef}

	t.Run("hit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		node := mock_protocol.NewMockQuaiP2PNode(ctrl)
		stream := mock_p2p.NewMockStream(ctrl)
		conn := mock_p2p.NewMockConn(ctrl)
		stream.EXPECT().Conn().Return(conn).AnyTimes()
		conn.EXPECT().RemotePeer().Return(peer.ID("trie-hit-peer")).AnyTimes()
		node.EXPECT().GetTrieNode(reqHash, loc).Return(&trie.TrieNodeResponse{NodeData: blob})
		node.EXPECT().GetBandwidthCounter().Return(nil)
		stream.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil)
		var written []byte
		stream.EXPECT().Write(gomock.Any()).DoAndReturn(func(data []byte) (int, error) {
			written = append([]byte(nil), data...)
			return len(data), nil
		})

		encoded, err := pb.EncodeQuaiRequest(55, loc, reqHash, &trie.TrieNodeResponse{})
		require.NoError(t, err)
		msg, err := pb.DecodeQuaiMessage(encoded)
		require.NoError(t, err)
		handleRequest(msg.GetRequest(), stream, node)

		require.GreaterOrEqual(t, len(written), 5)
		response, err := pb.DecodeQuaiMessage(written[4:])
		require.NoError(t, err)
		id, val, err := pb.DecodeQuaiResponse(response.GetResponse())
		require.NoError(t, err)
		require.Equal(t, uint32(55), id)
		require.Equal(t, &trie.TrieNodeResponse{NodeData: blob}, val)
	})

	t.Run("miss", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		node := mock_protocol.NewMockQuaiP2PNode(ctrl)
		stream := mock_p2p.NewMockStream(ctrl)
		conn := mock_p2p.NewMockConn(ctrl)
		stream.EXPECT().Conn().Return(conn).AnyTimes()
		conn.EXPECT().RemotePeer().Return(peer.ID("trie-miss-peer")).AnyTimes()
		node.EXPECT().GetTrieNode(reqHash, loc).Return(nil)
		node.EXPECT().GetBandwidthCounter().Return(nil)
		stream.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil)
		var written []byte
		stream.EXPECT().Write(gomock.Any()).DoAndReturn(func(data []byte) (int, error) {
			written = append([]byte(nil), data...)
			return len(data), nil
		})

		encoded, err := pb.EncodeQuaiRequest(56, loc, reqHash, &trie.TrieNodeResponse{})
		require.NoError(t, err)
		msg, err := pb.DecodeQuaiMessage(encoded)
		require.NoError(t, err)
		handleRequest(msg.GetRequest(), stream, node)

		require.GreaterOrEqual(t, len(written), 5)
		response, err := pb.DecodeQuaiMessage(written[4:])
		require.NoError(t, err)
		id, val, err := pb.DecodeQuaiResponse(response.GetResponse())
		require.Equal(t, uint32(56), id)
		require.Nil(t, val)
		require.ErrorIs(t, err, pb.EmptyResponse)
	})

	t.Run("wrong query type", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		stream := mock_p2p.NewMockStream(ctrl)
		conn := mock_p2p.NewMockConn(ctrl)
		stream.EXPECT().Conn().Return(conn).AnyTimes()
		conn.EXPECT().RemotePeer().Return(peer.ID("trie-bad-query")).AnyTimes()

		req := &pb.QuaiRequestMessage{
			Id:       57,
			Location: loc.ProtoEncode(),
			Data:     &pb.QuaiRequestMessage_Number{Number: big.NewInt(1).Bytes()},
			Request:  &pb.QuaiRequestMessage_TrieNode{},
		}
		require.NotPanics(t, func() {
			handleRequest(req, stream, nil)
		})
	})
}

func TestHandleRequestTrieNodesBatch(t *testing.T) {
	loc := common.Location{0, 0}
	hashes := []common.Hash{
		common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
		common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
	}
	blobs := [][]byte{{0x01, 0x02}, {0x03, 0x04}}

	t.Run("hit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		node := mock_protocol.NewMockQuaiP2PNode(ctrl)
		stream := mock_p2p.NewMockStream(ctrl)
		conn := mock_p2p.NewMockConn(ctrl)
		stream.EXPECT().Conn().Return(conn).AnyTimes()
		conn.EXPECT().RemotePeer().Return(peer.ID("trie-batch-hit")).AnyTimes()
		node.EXPECT().GetTrieNodes(hashes, loc).Return(&trie.TrieNodesResponse{Nodes: blobs})
		node.EXPECT().GetBandwidthCounter().Return(nil)
		stream.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil)
		var written []byte
		stream.EXPECT().Write(gomock.Any()).DoAndReturn(func(data []byte) (int, error) {
			written = append([]byte(nil), data...)
			return len(data), nil
		})

		encoded, err := pb.EncodeQuaiRequest(58, loc, hashes, &trie.TrieNodesResponse{})
		require.NoError(t, err)
		msg, err := pb.DecodeQuaiMessage(encoded)
		require.NoError(t, err)
		handleRequest(msg.GetRequest(), stream, node)

		require.GreaterOrEqual(t, len(written), 5)
		response, err := pb.DecodeQuaiMessage(written[4:])
		require.NoError(t, err)
		id, val, err := pb.DecodeQuaiResponse(response.GetResponse())
		require.NoError(t, err)
		require.Equal(t, uint32(58), id)
		require.Equal(t, &trie.TrieNodesResponse{Nodes: blobs}, val)
	})

	t.Run("miss", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		node := mock_protocol.NewMockQuaiP2PNode(ctrl)
		stream := mock_p2p.NewMockStream(ctrl)
		conn := mock_p2p.NewMockConn(ctrl)
		stream.EXPECT().Conn().Return(conn).AnyTimes()
		conn.EXPECT().RemotePeer().Return(peer.ID("trie-batch-miss")).AnyTimes()
		node.EXPECT().GetTrieNodes(hashes, loc).Return(nil)
		node.EXPECT().GetBandwidthCounter().Return(nil)
		stream.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil)
		var written []byte
		stream.EXPECT().Write(gomock.Any()).DoAndReturn(func(data []byte) (int, error) {
			written = append([]byte(nil), data...)
			return len(data), nil
		})

		encoded, err := pb.EncodeQuaiRequest(59, loc, hashes, &trie.TrieNodesResponse{})
		require.NoError(t, err)
		msg, err := pb.DecodeQuaiMessage(encoded)
		require.NoError(t, err)
		handleRequest(msg.GetRequest(), stream, node)

		require.GreaterOrEqual(t, len(written), 5)
		response, err := pb.DecodeQuaiMessage(written[4:])
		require.NoError(t, err)
		id, val, err := pb.DecodeQuaiResponse(response.GetResponse())
		require.Equal(t, uint32(59), id)
		require.Nil(t, val)
		require.ErrorIs(t, err, pb.EmptyResponse)
	})

	t.Run("wrong query type", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		stream := mock_p2p.NewMockStream(ctrl)
		conn := mock_p2p.NewMockConn(ctrl)
		stream.EXPECT().Conn().Return(conn).AnyTimes()
		conn.EXPECT().RemotePeer().Return(peer.ID("trie-batch-bad-query")).AnyTimes()

		req := &pb.QuaiRequestMessage{
			Id:       60,
			Location: loc.ProtoEncode(),
			Data:     &pb.QuaiRequestMessage_Hash{Hash: hashes[0].ProtoEncode()},
			Request:  &pb.QuaiRequestMessage_TrieNodes{},
		}
		require.NotPanics(t, func() {
			handleRequest(req, stream, nil)
		})
	})
}
