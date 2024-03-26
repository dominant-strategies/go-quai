package dhtstorage

import (
	"os"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/spf13/viper"
	"github.com/syndtr/goleveldb/leveldb"
)

const c_dbDirName = "dhtstorage"

// DHTStorage implements the ipfs Datastore and libp2p BatchingFeature interface
type DHTStorage struct {
	db *leveldb.DB
}

// Returns a new DHTSTorage instance
func NewDHTStorage() (*DHTStorage, error) {
	dataDir := viper.GetString(utils.DataDirFlag.Name)
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		err := os.MkdirAll(dataDir, 0755)
		if err != nil {
			log.Global.Errorf("error creating data directory: %s", err)
			return nil, err
		}
	}
	dbPath := dataDir + c_dbDirName

	log.Global.Debugf("Opening DHT persistent storage with path: %s", dbPath)

	db, err := leveldb.OpenFile(dbPath, nil)

	if err != nil {
		return nil, err
	}
	return &DHTStorage{
		db: db,
	}, nil
}
