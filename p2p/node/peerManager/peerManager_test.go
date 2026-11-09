package peerManager

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/p2p/node/peerManager/peerdb"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	"github.com/dominant-strategies/go-quai/p2p/node/streamManager"
	"github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/crypto"
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

func TestGetPeersExcludingSamplesBeyondFailedLivePeers(t *testing.T) {
	viper.Set(utils.DataDirFlag.Name, t.TempDir())
	t.Cleanup(viper.Reset)
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, &types.WorkObjectBlockView{})
	require.NoError(t, err)
	db, err := peerdb.NewPeerDB("bestPeersDB", topic.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var ids []peer.ID
	for i := 0; i < 4; i++ {
		_, publicKey, err := crypto.GenerateEd25519Key(rand.Reader)
		require.NoError(t, err)
		id, err := peer.IDFromPublicKey(publicKey)
		require.NoError(t, err)
		ids = append(ids, id)
		require.NoError(t, db.Put(context.Background(), datastore.NewKey(id.String()), []byte{1}))
	}
	pm := &BasicPeerManager{
		peerDBs: map[string][]*peerdb.PeerDB{topic.String(): {db, db, db}},
		ctx:     context.Background(),
	}
	excluded := map[peer.ID]struct{}{ids[0]: {}, ids[1]: {}, ids[2]: {}}
	selected := pm.GetPeersExcluding(topic, excluded)
	require.Equal(t, []peer.ID{ids[3]}, selected)
	// Concurrent failed requests for this topic must not each rescan LevelDB
	// and query DHT; bootstrap lookups without exclusions remain available.
	require.Empty(t, pm.GetPeersExcluding(topic, excluded))
	require.NotEmpty(t, pm.GetPeers(topic))
}

type coolingStreamManager struct {
	streamManager.StreamManager
	peer    peer.ID
	cooling bool
}

func (m *coolingStreamManager) DialCooldownRemaining(id peer.ID) time.Duration {
	if m.cooling && id == m.peer {
		return time.Minute
	}
	return 0
}

func TestGetPeersExcludingSkipsCoolingDialPeer(t *testing.T) {
	viper.Set(utils.DataDirFlag.Name, t.TempDir())
	t.Cleanup(viper.Reset)
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, &types.WorkObjectBlockView{})
	require.NoError(t, err)
	db, err := peerdb.NewPeerDB("bestPeersDB", topic.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ids := make([]peer.ID, 0, 2)
	for i := 0; i < 2; i++ {
		_, publicKey, err := crypto.GenerateEd25519Key(rand.Reader)
		require.NoError(t, err)
		id, err := peer.IDFromPublicKey(publicKey)
		require.NoError(t, err)
		ids = append(ids, id)
		require.NoError(t, db.Put(context.Background(), datastore.NewKey(id.String()), []byte{1}))
	}
	streams := &coolingStreamManager{peer: ids[0], cooling: true}
	pm := &BasicPeerManager{
		peerDBs:       map[string][]*peerdb.PeerDB{topic.String(): {db, db, db}},
		streamManager: streams,
		ctx:           context.Background(),
	}
	require.Equal(t, map[peer.ID]struct{}{ids[1]: {}}, pm.GetPeers(topic))
	streams.cooling = false
	require.Equal(t, map[peer.ID]struct{}{ids[0]: {}, ids[1]: {}}, pm.GetPeers(topic))
}

func TestGetPeersExcludingPrefersQualityBuckets(t *testing.T) {
	viper.Set(utils.DataDirFlag.Name, t.TempDir())
	t.Cleanup(viper.Reset)
	topic, err := pubsubManager.NewTopic(common.Hash{}, common.Location{0, 0}, &types.WorkObjectBlockView{})
	require.NoError(t, err)
	dbs := make([]*peerdb.PeerDB, 3)
	ids := make([][]peer.ID, 3)
	for bucket, name := range dbNames {
		db, err := peerdb.NewPeerDB(name, topic.String())
		require.NoError(t, err)
		dbs[bucket] = db
		t.Cleanup(func() { require.NoError(t, db.Close()) })
		for range 2 {
			_, publicKey, err := crypto.GenerateEd25519Key(rand.Reader)
			require.NoError(t, err)
			id, err := peer.IDFromPublicKey(publicKey)
			require.NoError(t, err)
			ids[bucket] = append(ids[bucket], id)
			require.NoError(t, db.Put(context.Background(), datastore.NewKey(id.String()), []byte{1}))
		}
	}
	pm := &BasicPeerManager{peerDBs: map[string][]*peerdb.PeerDB{topic.String(): dbs}, ctx: context.Background()}

	first := pm.GetPeersExcluding(topic, nil)
	require.Len(t, first, topic.GetRequestDegree())
	require.ElementsMatch(t, ids[Best], first[:2])
	require.Contains(t, ids[Responsive], first[2])
	for _, id := range ids[LastResort] {
		require.NotContains(t, first, id)
	}

	excluded := map[peer.ID]struct{}{ids[Best][0]: {}, ids[Best][1]: {}}
	second := pm.GetPeersExcluding(topic, excluded)
	require.Len(t, second, topic.GetRequestDegree())
	require.ElementsMatch(t, ids[Responsive], second[:2])
	require.Contains(t, ids[LastResort], second[2])
	for _, id := range ids[Best] {
		require.NotContains(t, second, id)
	}
}
