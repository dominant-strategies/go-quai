package dhtstorage

import (
	"context"

	datastore "github.com/ipfs/go-datastore"
	"github.com/syndtr/goleveldb/leveldb"
)

// Implements the libp2p Batching feature interface:
// https://pkg.go.dev/github.com/ipfs/go-datastore#BatchingFeature

// Ensure DHTStorage implements the BatchingFeature interface
var _ datastore.BatchingFeature = (*DHTStorage)(nil)

// LevelDBBatch wraps LevelDB's own Batch type to implement the Batch interface
type LevelDBBatch struct {
	db    *leveldb.DB
	batch *leveldb.Batch
}

// Appends a put operation to the batch
func (b *LevelDBBatch) Put(ctx context.Context, key datastore.Key, value []byte) error {
	b.batch.Put(key.Bytes(), value)
	return nil
}

// Appends a delete operation to the batch
func (b *LevelDBBatch) Delete(ctx context.Context, key datastore.Key) error {
	b.batch.Delete(key.Bytes())
	return nil
}

// Commits the batch
func (b *LevelDBBatch) Commit(ctx context.Context) error {
	return b.db.Write(b.batch, nil)
}

// Returns a LevelDBBatch instance that implements the Batch interface
func (s *DHTStorage) Batch(ctx context.Context) (datastore.Batch, error) {
	return &LevelDBBatch{
		db:    s.db,
		batch: new(leveldb.Batch),
	}, nil
}
