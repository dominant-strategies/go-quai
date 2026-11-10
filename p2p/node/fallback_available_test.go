package node

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	mock_p2p "github.com/dominant-strategies/go-quai/p2p/mocks"
	"github.com/dominant-strategies/go-quai/p2p/node/peerManager"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	"github.com/dominant-strategies/go-quai/p2p/node/streamManager"
	proto "github.com/dominant-strategies/go-quai/p2p/protocol"
	mock_protocol "github.com/dominant-strategies/go-quai/p2p/protocol/mocks"
	libp2pmetrics "github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2pprotocol "github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
)

type availableFallbackPeers struct {
	peerManager.PeerManager
	streams streamManager.StreamManager
	live    peer.ID
	stored  peer.ID
	block   *types.WorkObjectBlockView
	replies chan interface{}
}

func (m *availableFallbackPeers) GetStreamPeers() []peer.ID  { return []peer.ID{m.live} }
func (m *availableFallbackPeers) GetPeerQuality(peer.ID) int { return 0 }
func (m *availableFallbackPeers) GetPeersExcluding(_ *pubsubManager.Topic, excluded map[peer.ID]struct{}) []peer.ID {
	if _, tried := excluded[m.live]; !tried {
		return nil
	}
	return []peer.ID{m.stored}
}
func (m *availableFallbackPeers) GetStream(id peer.ID) (network.Stream, error) {
	if id == m.live {
		return nil, streamManager.ErrStreamNotFound
	}
	return m.streams.GetStream(id)
}
func (m *availableFallbackPeers) WriteMessageToStream(_ peer.ID, _ network.Stream, _ []byte, _ libp2pprotocol.ID, _ libp2pmetrics.Reporter) error {
	m.replies <- m.block
	return nil
}
func (m *availableFallbackPeers) AdjustPeerQuality(peer.ID, string, func(int) int) {}

func TestStoredPeerWithAvailableBlockAnswersCurrentFallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	host := mock_p2p.NewMockHost(ctrl)
	backend := mock_protocol.NewMockQuaiP2PNode(ctrl)
	streams, err := streamManager.NewStreamManager(backend, host)
	require.NoError(t, err)
	defer streams.Stop()

	stored := peer.ID("reachable-stored-block-peer")
	live := peer.ID("failed-live-block-peer")
	stream := mock_p2p.NewMockStream(ctrl)
	conn := mock_p2p.NewMockConn(ctrl)
	host.EXPECT().NewStream(gomock.Any(), stored, gomock.Any()).Return(stream, nil).Times(1)
	backend.EXPECT().GetBandwidthCounter().Return(nil).AnyTimes()
	stream.EXPECT().Close().Return(nil).AnyTimes()
	stream.EXPECT().Conn().Return(conn).AnyTimes()
	stream.EXPECT().Protocol().Return(proto.ProtocolVersion).AnyTimes()
	stream.EXPECT().Read(gomock.Any()).Return(0, nil).AnyTimes()
	conn.EXPECT().RemotePeer().Return(stored).AnyTimes()

	header := &types.WorkObjectHeader{}
	header.SetNumber(big.NewInt(100))
	block := types.NewWorkObject(header, types.EmptyWorkObjectBody(), nil).ConvertToBlockView()
	replies := make(chan interface{}, 1)
	manager := &availableFallbackPeers{streams: streams, live: live, stored: stored, block: block, replies: replies}
	node := &P2PNode{ctx: context.Background(), peerManager: manager, requestManager: &missingResponseRequestManager{result: replies}, requestSlots: make(chan struct{}, 1)}
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, &types.WorkObjectBlockView{})
	require.NoError(t, err)
	result := make(chan interface{}, 1)
	var attempted []peer.ID
	requestPeerRounds(context.Background(), manager, topic, nil, 1, noShuffle, func(peers []peer.ID) bool {
		for _, id := range peers {
			attempted = append(attempted, id)
			if node.requestAndWait(id, topic, big.NewInt(100), &types.WorkObjectBlockView{}, result, 0, nil) {
				return true
			}
		}
		return false
	})
	require.Equal(t, []peer.ID{live, stored}, attempted)
	select {
	case value := <-result:
		require.Same(t, block, value)
	default:
		t.Fatal("reachable stored peer did not answer the current request")
	}
}

type validatingFallbackPeers struct {
	peerManager.PeerManager
	bad, good peer.ID
	replies   chan interface{}
	responses map[peer.ID]interface{}
	attempted []peer.ID
	quality   map[peer.ID]int
}

func (m *validatingFallbackPeers) GetStreamPeers() []peer.ID { return []peer.ID{m.bad} }
func (m *validatingFallbackPeers) GetPeerQuality(id peer.ID) int {
	return m.quality[id]
}
func (m *validatingFallbackPeers) GetPeersExcluding(_ *pubsubManager.Topic, excluded map[peer.ID]struct{}) []peer.ID {
	if _, tried := excluded[m.bad]; tried {
		return []peer.ID{m.good}
	}
	return nil
}
func (m *validatingFallbackPeers) GetStream(peer.ID) (network.Stream, error) { return nil, nil }
func (m *validatingFallbackPeers) WriteMessageToStream(id peer.ID, _ network.Stream, _ []byte, _ libp2pprotocol.ID, _ libp2pmetrics.Reporter) error {
	m.attempted = append(m.attempted, id)
	m.replies <- m.responses[id]
	return nil
}
func (m *validatingFallbackPeers) AdjustPeerQuality(id peer.ID, _ string, adjust func(int) int) {
	m.quality[id] = adjust(m.quality[id])
}

func TestInvalidPeerResponseFallsBackToAnotherPeer(t *testing.T) {
	bad, good := peer.ID("invalid-prime-batch-peer"), peer.ID("valid-prime-batch-peer")
	badBatch := []*types.WorkObjectBlockView{}
	validHeader := &types.WorkObjectHeader{}
	validHeader.SetNumber(big.NewInt(100))
	primeHeader := types.EmptyHeader()
	primeHeader.SetNumber(big.NewInt(100), common.PRIME_CTX)
	body := types.EmptyWorkObjectBody()
	body.SetHeader(primeHeader)
	goodBatch := []*types.WorkObjectBlockView{types.NewWorkObject(validHeader, body, nil).ConvertToBlockView()}
	replies := make(chan interface{}, 1)
	manager := &validatingFallbackPeers{
		bad: bad, good: good, replies: replies,
		responses: map[peer.ID]interface{}{bad: badBatch, good: goodBatch},
		quality:   map[peer.ID]int{bad: 100, good: 100},
	}
	node := &P2PNode{ctx: context.Background(), peerManager: manager, requestManager: &missingResponseRequestManager{result: replies}, requestSlots: make(chan struct{}, 1)}
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, []*types.WorkObjectBlockView{})
	require.NoError(t, err)
	result := make(chan interface{}, 1)
	validate := func(response interface{}) error {
		batch, ok := response.([]*types.WorkObjectBlockView)
		if !ok || len(batch) != 1 || batch[0].NumberU64(common.PRIME_CTX) != 100 {
			return errors.New("invalid prime batch")
		}
		return nil
	}
	requestPeerRounds(context.Background(), manager, topic, nil, 1, noShuffle, func(peers []peer.ID) bool {
		for _, id := range peers {
			if node.requestAndWait(id, topic, big.NewInt(100), []*types.WorkObjectBlockView{}, result, 0, validate) {
				return true
			}
		}
		return false
	})
	require.Equal(t, []peer.ID{bad, good}, manager.attempted)
	select {
	case got := <-result:
		require.Equal(t, goodBatch, got)
	default:
		t.Fatal("valid peer's batch was not delivered")
	}
	require.Less(t, manager.quality[bad], 100)
	require.Greater(t, manager.quality[good], 100)
}
