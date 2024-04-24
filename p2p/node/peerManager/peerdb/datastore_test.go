package peerdb

import (
	"context"
	"encoding/json"
	"math/rand"
	"sync"
	"testing"

	datastore "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"github.com/stretchr/testify/require"
)

func TestPeerDB_PutGetDeletePeer(t *testing.T) {
	ps, teardown := setupDB(t)
	t.Cleanup(teardown)

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
	ps, teardown := setupDB(t)
	t.Cleanup(teardown)

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
	ps, teardown := setupDB(t)
	t.Cleanup(teardown)

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

func TestGetSize(t *testing.T) {
	ps, teardown := setupDB(t)
	defer teardown()

	peers := createPeers(t, 5)

	keys := make([]datastore.Key, len(peers))

	cases := []struct {
		i       int
		Entropy uint64
		PubKey  []byte
	}{
		{
			i:       0,
			Entropy: uint64(12345),
			PubKey:  []byte(""),
		},
		{
			i:       1,
			Entropy: uint64(1234567890),
			PubKey:  []byte("pub"),
		},
		{
			i:       2,
			Entropy: uint64(1),
			PubKey:  []byte("pubkey"),
		},
		{
			i:       3,
			Entropy: uint64(0),
			PubKey:  []byte("pubkey1234567890"),
		},
		{
			i:       4,
			Entropy: uint64(12345678901234),
			PubKey:  []byte("pubkey12345678901234567890"),
		},
	}

	var wg sync.WaitGroup

	// Add value to keys and test first time
	for i, peer := range peers {
		wg.Add(1)
		go func(peer *PeerInfo, i int) {
			defer wg.Done()
			keys[i] = datastore.NewKey(peer.AddrInfo.ID.String())
			value, err := json.Marshal(peer)
			require.NoError(t, err)

			err = ps.Put(context.Background(), keys[i], value)
			require.NoError(t, err)
			size, err := ps.GetSize(context.Background(), keys[i])
			require.NoError(t, err)
			require.Equal(t, len(value), size)
		}(peer, i)
	}
	wg.Wait()

	// Update keys is parallel and check if size is updated
	for i, peer := range peers {
		wg.Add(1)
		go func(peer *PeerInfo, i int) {
			defer wg.Done()
			peer.Entropy = cases[i].Entropy
			updateAndTest(t, ps, peer, keys[i])

			peer.PubKey = cases[i].PubKey
			updateAndTest(t, ps, peer, keys[i])
		}(peer, i)
	}
	wg.Wait()

	//Test with non existent key
	size, err := ps.GetSize(context.Background(), datastore.NewKey("non-existent-key"))
	require.Error(t, err)
	require.Equal(t, 0, size)
}

func updateAndTest(t *testing.T, ps *PeerDB, peer *PeerInfo, key datastore.Key) {
	value, err := json.Marshal(peer)
	require.NoError(t, err)

	err = ps.Put(context.Background(), key, value)
	require.NoError(t, err)
	size, err := ps.GetSize(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, len(value), size)
}

func TestMultipleDatabaseUpdates(t *testing.T) {
	ps, teardown := setupDB(t)
	defer teardown()
	iterations := 10000

	var wg sync.WaitGroup
	wg.Add(1)
	go func(t *testing.T, ps *PeerDB) {
		defer wg.Done()
		for i := 1; i <= iterations; i++ {
			wg.Add(1)
			go func(t *testing.T, ps *PeerDB) {
				defer wg.Done()
				peers := createPeers(t, 10)
				for _, peer := range peers {
					key := datastore.NewKey(peer.AddrInfo.ID.String())
					value, err := json.Marshal(peer)
					require.NoError(t, err)
					err = ps.Put(context.Background(), key, value)
					require.NoError(t, err)
					size, err := ps.GetSize(context.Background(), key)
					require.NoError(t, err)
					require.Equal(t, len(value), size)
					err = ps.Delete(context.Background(), key)
					require.NoError(t, err)
					size, err = ps.GetSize(context.Background(), key)
					require.Error(t, err)
					require.Equal(t, 0, size)
				}
			}(t, ps)
		}
	}(t, ps)

	wg.Add(1)
	go func(t *testing.T, ps *PeerDB) {
		defer wg.Done()
		for i := 1; i <= iterations; i++ {
			wg.Add(1)
			go func(t *testing.T, ps *PeerDB) {
				peers := createPeers(t, 10)
				defer wg.Done()
				for _, peer := range peers {
					// insert into database
					key := datastore.NewKey(peer.AddrInfo.ID.String())
					value, err := json.Marshal(peer)
					require.NoError(t, err)
					err = ps.Put(context.Background(), key, value)
					require.NoError(t, err)
					size, err := ps.GetSize(context.Background(), key)
					require.NoError(t, err)
					require.Equal(t, len(value), size)

					// update the database
					peer.Entropy = rand.Uint64()
					updateAndTest(t, ps, peer, key)
					peer.PubKey = []byte{byte(rand.Intn(256))}
					updateAndTest(t, ps, peer, key)
				}
			}(t, ps)
		}
	}(t, ps)
	wg.Wait()
}
