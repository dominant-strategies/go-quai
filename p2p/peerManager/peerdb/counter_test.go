package peerdb

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/ipfs/go-datastore"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestCounter(t *testing.T) {
	viper.GetViper().Set(utils.DataDirFlag.Name, os.TempDir())
	dbDir := "testdb"

	// creat a new peerdb
	ps, err := NewPeerDB(dbDir)
	require.NoError(t, err)

	// create 10 peers
	peers := createPeers(t, 10)

	t.Cleanup(
		func() {
			// close the db
			err := ps.Close()
			require.NoError(t, err)

			// remove the db file
			dbFile := viper.GetString(utils.DataDirFlag.Name) + "/" + dbDir
			err = os.RemoveAll(dbFile)
			require.NoError(t, err)
		},
	)

	t.Run("Test increment counter", func(t *testing.T) {
		for _, peer := range peers {
			key := datastore.NewKey(peer.AddrInfo.ID.String())
			value, err := json.Marshal(peer)
			require.NoError(t, err)
			err = ps.Put(context.Background(), key, value)
			require.NoError(t, err)
		}
		// verify the counter
		require.Equal(t, 10, ps.GetPeerCount())
	})

	t.Run("Test init counter", func(t *testing.T) {
		// close the db
		err = ps.Close()
		require.NoError(t, err)

		// reopen the db
		ps, err = NewPeerDB(dbDir)
		require.NoError(t, err)

		// verify the counter
		require.Equal(t, 10, ps.GetPeerCount())
	})

	t.Run("Test decrement counter", func(t *testing.T) {
		// delete 5 peers
		for i := 0; i < 5; i++ {
			key := datastore.NewKey(peers[i].AddrInfo.ID.String())
			err = ps.Delete(context.Background(), key)
			require.NoError(t, err)
		}
		// verify the counter
		require.Equal(t, 5, ps.GetPeerCount())
	})
}
