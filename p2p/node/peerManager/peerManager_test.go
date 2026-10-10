package peerManager

import (
	"testing"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

const testProtectedPeer = "/ip4/44.239.114.142/tcp/4002/p2p/12D3KooWRLGCnSvu46DxsNecMRi1coeR3rE3do1zTjPBnnJsWeYH"

func TestLoadConfiguredPeers(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set(utils.NonPenalizedPeersFlag.Name, []string{testProtectedPeer})

	peers, err := LoadConfiguredPeers(utils.NonPenalizedPeersFlag.Name)
	require.NoError(t, err)
	require.Len(t, peers, 1)
	require.Equal(t, "12D3KooWRLGCnSvu46DxsNecMRi1coeR3rE3do1zTjPBnnJsWeYH", peers[0].ID.String())
}

// Both separators must work regardless of source: the CLI splits on commas via
// pflag's StringSlice, env vars split on whitespace via viper's cast.
func TestLoadConfiguredPeersAcceptsBothSeparators(t *testing.T) {
	const otherPeer = "/ip4/35.87.10.1/tcp/4002/p2p/12D3KooWAsSneYwJ2RV93FpYZUpdXmZLz6FJZNUNddMcmrdhg7vx"

	for name, value := range map[string]interface{}{
		"pre-split":       []string{testProtectedPeer, otherPeer},
		"comma-separated": []string{testProtectedPeer + "," + otherPeer},
		"space-separated": []string{testProtectedPeer + " " + otherPeer},
		"mixed":           []string{testProtectedPeer + ", " + otherPeer},
		"raw string":      testProtectedPeer + "," + otherPeer,
	} {
		t.Run(name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)

			viper.Set(utils.StaticPeersFlag.Name, value)

			peers, err := LoadStaticPeers()
			require.NoError(t, err)
			require.Len(t, peers, 2)
			require.Equal(t, "12D3KooWRLGCnSvu46DxsNecMRi1coeR3rE3do1zTjPBnnJsWeYH", peers[0].ID.String())
			require.Equal(t, "12D3KooWAsSneYwJ2RV93FpYZUpdXmZLz6FJZNUNddMcmrdhg7vx", peers[1].ID.String())
		})
	}
}

// Exercises the real CLI binding rather than viper.Set, because that is where the
// separator is decided: CreateAndBindFlag registers a pflag StringSlice, which
// hands us one un-split element when the operator separates with spaces.
func TestStaticPeersFlagThroughCLIBinding(t *testing.T) {
	const otherPeer = "/ip4/35.87.10.1/tcp/4002/p2p/12D3KooWAsSneYwJ2RV93FpYZUpdXmZLz6FJZNUNddMcmrdhg7vx"

	for name, arg := range map[string]string{
		"comma-separated": testProtectedPeer + "," + otherPeer,
		"space-separated": testProtectedPeer + " " + otherPeer,
	} {
		t.Run(name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)

			cmd := &cobra.Command{Use: "test"}
			utils.CreateAndBindFlag(utils.StaticPeersFlag, cmd)
			require.NoError(t, cmd.PersistentFlags().Parse([]string{"--" + utils.StaticPeersFlag.Name + "=" + arg}))

			peers, err := LoadStaticPeers()
			require.NoError(t, err)
			require.Len(t, peers, 2)
		})
	}
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
