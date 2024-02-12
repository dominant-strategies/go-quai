package peerdb

import (
	"context"
	"encoding/json"
	"testing"

	datastore "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"github.com/stretchr/testify/require"
)

func TestPeerDB_PutGetDeletePeer(t *testing.T) {
	db, teardown := setupDB(t)
	defer teardown()

	ps := &PeerDB{db: db}

	peerInfo := createPeers(t, 1)[0]

	key := datastore.NewKey(peerInfo.AddrInfo.ID.String())

	// Marshal the peer info as JSON for storage
	value, err := json.Marshal(peerInfo)
	require.NoError(t, err)

	// Testing put functionality
	err = ps.Put(context.Background(), key, value)
	require.NoError(t, err)

	// Testing get functionality
	value, err = ps.Get(context.Background(), key)
	require.NoError(t, err)

	// Unmarshal the value back to a PeerInfo struct
	retrievedPeerInfo := new(PeerInfo)
	err = json.Unmarshal(value, retrievedPeerInfo)
	require.NoError(t, err)

	// Assert that the retrieved peer info matches what was stored
	require.Equal(t, peerInfo, retrievedPeerInfo)

	// Testing delete functionality
	err = ps.Delete(context.Background(), key)
	require.NoError(t, err)

	// Attempting to get the deleted peer info should result in an error
	_, err = ps.Get(context.Background(), key)
	require.Error(t, err)
}

func TestHas(t *testing.T) {
	db, teardown := setupDB(t)
	defer teardown()

	ps := &PeerDB{db: db}

	peerInfo := createPeers(t, 1)[0]

	key := datastore.NewKey(peerInfo.AddrInfo.ID.String())

	value, err := json.Marshal(peerInfo)
	require.NoError(t, err)

	err = ps.Put(context.Background(), key, value)
	require.NoError(t, err)

	// Testing has functionality
	exists, err := ps.Has(context.Background(), key)
	require.NoError(t, err)
	require.True(t, exists)

	err = ps.Delete(context.Background(), key)
	require.NoError(t, err)

	exists, err = ps.Has(context.Background(), key)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestQuery(t *testing.T) {
	db, teardown := setupDB(t)
	defer teardown()

	ps := &PeerDB{db: db}

	peers := createPeers(t, 5)

	for _, peerInfo := range peers {
		key := datastore.NewKey(peerInfo.AddrInfo.ID.String())
		value, err := json.Marshal(peerInfo)
		require.NoError(t, err)
		err = ps.Put(context.Background(), key, value)
		require.NoError(t, err)
	}

	// Test query with limit
	t.Run("Test query with limit", func(t *testing.T) {
		q := query.Query{Limit: 3}
		results, err := ps.Query(context.Background(), q)
		require.NoError(t, err)
		resultLimit, err := results.Rest()
		require.NoError(t, err)
		require.Len(t, resultLimit, 3)
	})

	// Test query with prefix
	t.Run("Test query with prefix", func(t *testing.T) {
		q := query.Query{Prefix: peers[0].AddrInfo.ID.String()[0:10]}
		results, err := ps.Query(context.Background(), q)
		require.NoError(t, err)
		resultsPrefix, err := results.Rest()
		require.NoError(t, err)
		require.Len(t, resultsPrefix, 1)
		key := resultsPrefix[0].Key
		require.Equal(t, key, "/"+peers[0].AddrInfo.ID.String())
	})
}
