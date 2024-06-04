package peerdb

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/spf13/viper"
	"github.com/syndtr/goleveldb/leveldb"
)

var (
	ErrPeerNotFound = leveldb.ErrNotFound
)

// Returns a new PeerDB instance or the existing one if it already exists
func NewPeerDB(dbDirName string, locationName string) (*PeerDB, error) {
	strs := []string{viper.GetString(utils.DataDirFlag.Name), locationName}
	dataDir := filepath.Join(strs...)

	// Check if the directory exists and create it if it doesn't
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		err := os.MkdirAll(dataDir, 0755)
		if err != nil {
			log.Global.WithField("err", err).Warn("error creating data directory")
			return nil, err
		}
	}

	dbPath := filepath.Join(dataDir, dbDirName)

	// Check if the database file already exists
	if _, err := os.Stat(dbPath); err == nil {
		// Database file exists, open it
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
	} else if os.IsNotExist(err) {
		// Database file does not exist, create a new one
		db, err := leveldb.OpenFile(dbPath, nil)
		if err != nil {
			return nil, err
		}

		// Initialize the key counter
		peerCounter := initCounter(db)

		return &PeerDB{
			db:          db,
			peerCounter: peerCounter,
			mu:          sync.Mutex{},
		}, nil
	} else {
		// Some other error occurred
		return nil, err
	}
}
