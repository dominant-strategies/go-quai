package node

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/p2p/node/peerManager"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	libp2pmetrics "github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2pprotocol "github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"
)

type missingResponsePeerManager struct {
	peerManager.PeerManager
	adjustments int
	score       int
}

func (m *missingResponsePeerManager) GetStream(peer.ID) (network.Stream, error) { return nil, nil }
func (m *missingResponsePeerManager) WriteMessageToStream(peer.ID, network.Stream, []byte, libp2pprotocol.ID, libp2pmetrics.Reporter) error {
	return nil
}
func (m *missingResponsePeerManager) AdjustPeerQuality(_ peer.ID, _ string, adjust func(int) int) {
	m.adjustments++
	m.score = adjust(100)
}

type missingResponseRequestManager struct{ result chan interface{} }

func (m *missingResponseRequestManager) CreateRequest() uint32 { return 1 }
func (m *missingResponseRequestManager) CloseRequest(uint32)   {}
func (m *missingResponseRequestManager) GetRequestChan(uint32) (chan interface{}, error) {
	return m.result, nil
}

type blockingDialPeerManager struct {
	missingResponsePeerManager
	coldPeer peer.ID
	started  chan struct{}
	release  chan struct{}
}

func (m *blockingDialPeerManager) GetStream(id peer.ID) (network.Stream, error) {
	if id == m.coldPeer {
		close(m.started)
		<-m.release
		return nil, errors.New("dial failed")
	}
	return nil, nil
}

func TestMissingByNumberResponseDoesNotPenalizePeer(t *testing.T) {
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, &types.WorkObjectBlockView{})
	require.NoError(t, err)
	for _, test := range []struct {
		name        string
		request     interface{}
		adjustments int
		score       int
	}{
		{"missing number", big.NewInt(100), 0, 0},
		{"missing hash", common.Hash{1}, 1, 90},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := &missingResponsePeerManager{}
			responses := make(chan interface{}, 1)
			responses <- nil
			node := &P2PNode{ctx: context.Background(), peerManager: manager, requestManager: &missingResponseRequestManager{result: responses}, requestSlots: make(chan struct{}, 1)}
			result, err := node.requestFromPeer(peer.ID("missing-response-"+test.name), topic, test.request, &types.WorkObjectBlockView{})
			require.NoError(t, err)
			require.Nil(t, result)
			require.Equal(t, test.adjustments, manager.adjustments)
			require.Equal(t, test.score, manager.score)
		})
	}
}

func TestColdDialDoesNotHoldRequestSlot(t *testing.T) {
	coldPeer := peer.ID("cold-dial-peer")
	manager := &blockingDialPeerManager{
		coldPeer: coldPeer,
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	responses := make(chan interface{}, 1)
	responses <- nil
	node := &P2PNode{
		ctx:            context.Background(),
		peerManager:    manager,
		requestManager: &missingResponseRequestManager{result: responses},
		requestSlots:   make(chan struct{}, 1),
	}
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, &types.WorkObjectBlockView{})
	require.NoError(t, err)
	coldDone := make(chan error, 1)
	go func() {
		_, err := node.requestFromPeer(coldPeer, topic, big.NewInt(100), &types.WorkObjectBlockView{})
		coldDone <- err
	}()
	<-manager.started
	require.Empty(t, node.requestSlots, "dial must not reserve a response slot")

	// A request with an already available stream can still use the slot.
	result, err := node.requestFromPeer(peer.ID("warm-peer"), topic, big.NewInt(100), &types.WorkObjectBlockView{})
	require.NoError(t, err)
	require.Nil(t, result)
	close(manager.release)
	require.ErrorContains(t, <-coldDone, "dial failed")
}
