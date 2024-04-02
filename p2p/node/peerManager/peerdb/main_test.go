package peerdb

import (
	"crypto/rand"
	"os"
	"testing"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Comment / un comment below to see log output while testing
	// log.SetGlobalLogger("", "trace")
	os.Exit(m.Run())
}

// helper functions to run peerdb tests

func setupDB(t *testing.T) (*PeerDB, func()) {
	t.Helper()
	viper.GetViper().Set(utils.DataDirFlag.Name, os.TempDir())
	dbDir := "testdb"
	locationName := "zone-0-0"

	// creat a new peerdb
	ps, err := NewPeerDB(dbDir, locationName)
	require.NoError(t, err)

	return ps, func() {
		// close the db
		err := ps.Close()
		require.NoError(t, err)

		// remove the db file
		dbFile := viper.GetString(utils.DataDirFlag.Name) + "/" + locationName + dbDir
		err = os.RemoveAll(dbFile)
		require.NoError(t, err)
	}
}

// returns a random public key and peer ID
func generateKeyAndID(t *testing.T) ([]byte, peer.ID) {
	_, pubkey, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	peerID, err := peer.IDFromPublicKey(pubkey)
	require.NoError(t, err)
	pubkeyBytes, err := pubkey.Raw()
	require.NoError(t, err)
	return pubkeyBytes, peerID
}

func createPeers(t *testing.T, count int) []*PeerInfo {
	t.Helper()

	// addresses, entropy and protected are placeholders
	addrs := []multiaddr.Multiaddr{
		multiaddr.StringCast("/ip4/127.0.0.1/tcp/4001"),
		multiaddr.StringCast("/ip4/172.17.0.1/udp/4001/quic"),
	}
	entropy := uint64(123456)
	protected := false

	var peers []*PeerInfo
	for i := 0; i < count; i++ {
		pubKey, peerID := generateKeyAndID(t)
		peerInfo := &PeerInfo{
			AddrInfo: peer.AddrInfo{
				ID:    peerID,
				Addrs: addrs,
			},
			PubKey:    pubKey,
			Entropy:   entropy,
			Protected: protected,
		}
		peers = append(peers, peerInfo)
	}
	return peers
}
