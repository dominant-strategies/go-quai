package peerdb

import (
	"os"
	"strings"
	"sync"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/spf13/viper"
	"github.com/syndtr/goleveldb/leveldb"
)

var (
	ErrPeerNotFound = leveldb.ErrNotFound
)

// Returns a new PeerDB instance
func NewPeerDB(dbDirName string, locationName string) (*PeerDB, error) {
	strs := []string{viper.GetString(utils.DataDirFlag.Name), locationName}
	dataDir := strings.Join(strs, "/")
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		err := os.MkdirAll(dataDir, 0755)
		if err != nil {
			log.Global.Errorf("error creating data directory: %s", err)
			return nil, err
		}
	}
	dbPath := dataDir + dbDirName

	log.Global.Debugf("Opening PeerDB with path: %s", dbPath)

	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		return nil, err
	}

	// Initialize the key counter
	peerCounter := initCounter(db)

	log.Global.Debugf("Found %d peers in PeerDB", peerCounter)

	return &PeerDB{
		db:          db,
		peerCounter: peerCounter,
		mu:          sync.Mutex{},
	}, nil
}
