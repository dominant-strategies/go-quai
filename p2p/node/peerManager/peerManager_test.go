package peerManager

import (
	"testing"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

const testProtectedPeer = "/ip4/44.239.114.142/tcp/4002/p2p/12D3KooWRLGCnSvu46DxsNecMRi1coeR3rE3do1zTjPBnnJsWeYH"

func TestLoadConfiguredPeers(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set(utils.NonPenalizedPeersFlag.Name, []string{testProtectedPeer})

	peers, err := loadConfiguredPeers(utils.NonPenalizedPeersFlag.Name)
	require.NoError(t, err)
	require.Len(t, peers, 1)
	require.Equal(t, "12D3KooWRLGCnSvu46DxsNecMRi1coeR3rE3do1zTjPBnnJsWeYH", peers[0].ID.String())
}

func TestAdjustPeerQualitySkipsProtectedPeer(t *testing.T) {
	protectedPeerID := peer.ID("12D3KooWRLGCnSvu46DxsNecMRi1coeR3rE3do1zTjPBnnJsWeYH")
	pm := &BasicPeerManager{
		protectedPeers: map[peer.ID]struct{}{
			protectedPeerID: {},
		},
	}

	adjCalled := false
	pm.AdjustPeerQuality(protectedPeerID, "topic", func(current int) int {
		adjCalled = true
		return current + 1
	})

	require.False(t, adjCalled)
	require.True(t, pm.IsProtectedPeer(protectedPeerID))
}
