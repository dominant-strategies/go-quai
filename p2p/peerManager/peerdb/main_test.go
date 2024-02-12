package peerdb

import (
	"crypto/rand"
	"os"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
)

func TestMain(m *testing.M) {
	// Comment / un comment below to see log output while testing
	// log.SetGlobalLogger("", "trace")
	os.Exit(m.Run())
}

// helper functions to run peerdb tests

func setupDB(t *testing.T) (*leveldb.DB, func()) {
	t.Helper()
	dir := os.TempDir() + "peerstore_test"

	db, err := leveldb.OpenFile(dir, nil)
	require.NoError(t, err)

	return db, func() {
		db.Close()
		os.RemoveAll(dir)
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
