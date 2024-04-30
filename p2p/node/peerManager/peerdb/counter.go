package peerdb

import (
	"github.com/dominant-strategies/go-quai/log"
	"github.com/syndtr/goleveldb/leveldb"
)

// returns the number of peers (keys) stored in the database
func initCounter(db *leveldb.DB) int {
	iter := db.NewIterator(nil, nil)
	defer iter.Release()
	var counter int
	for iter.Next() {
		counter++
	}
	return counter
}

func (pdb *PeerDB) incrementPeerCount() {
	pdb.mu.Lock()
	defer pdb.mu.Unlock()
	pdb.peerCounter++
}

func (pdb *PeerDB) decrementPeerCount() {
	pdb.mu.Lock()
	defer pdb.mu.Unlock()
	if pdb.peerCounter == 0 {
		log.Global.Errorf("Peer counter is already at 0")
		return
	}
	pdb.peerCounter--
}

// Returns the number of peers stored in the database
func (pdb *PeerDB) GetPeerCount() int {
	return pdb.peerCounter
}
