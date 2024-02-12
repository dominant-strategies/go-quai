package peerdb

import (
	"context"

	"github.com/dominant-strategies/go-quai/log"
	datastore "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// ipfs Datastore interface implementation

// ensure PeerDB implements the datastore interface
var _ datastore.Datastore = (*PeerDB)(nil)

// Get retrieves the object `value` named by `key`.
// Get will return ErrNotFound if the key is not mapped to a value.
func (p *PeerDB) Get(_ context.Context, key datastore.Key) (value []byte, err error) {
	leveldbKey := key.Bytes()

	value, err = p.db.Get(leveldbKey, nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return nil, datastore.ErrNotFound
		}
		return nil, err
	}
	return value, nil
}

// Has returns whether the `key` is mapped to a `value`.
// In some contexts, it may be much cheaper only to check for existence of
// a value, rather than retrieving the value itself. (e.g. HTTP HEAD).
// The default implementation is found in `GetBackedHas`.
func (p *PeerDB) Has(_ context.Context, key datastore.Key) (exists bool, err error) {
	leveldbKey := key.Bytes()
	return p.db.Has(leveldbKey, nil)
}

// GetSize returns the size of the `value` named by `key`.
// In some contexts, it may be much cheaper to only get the size of the
// value rather than retrieving the value itself.
func (p *PeerDB) GetSize(_ context.Context, key datastore.Key) (size int, err error) {
	leveldbKey := key.Bytes()

	value, err := p.db.Get(leveldbKey, nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return 0, datastore.ErrNotFound
		}
		return 0, err
	}
	return len(value), nil

}

// Query searches the datastore and returns a query result. This function
// may return before the query actually runs. To wait for the query:
//
//	result, _ := ds.Query(q)
//
//	use the channel interface; result may come in at different times:
//	for entry := range result.Next() { ... }
//
//	or wait for the query to be completely done:
//	entries, _ := result.Rest()
//	for entry := range entries { ... }
func (p *PeerDB) Query(ctx context.Context, q query.Query) (query.Results, error) {
	var iterRange *util.Range
	if q.Prefix != "" {
		if q.Prefix[0] != '/' {
			q.Prefix = "/" + q.Prefix
		}
		log.Global.Tracef("Querying with prefix: %s", q.Prefix)
		iterRange = util.BytesPrefix([]byte(q.Prefix))
	}

	var limit bool
	if q.Limit > 0 {
		limit = true
	}

	// TODO: Implement query filtering based on query.Filters.

	iter := p.db.NewIterator(iterRange, nil)
	defer iter.Release()

	entries := make([]query.Entry, 0)
	for iter.Next() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		key := string(iter.Key())
		value := iter.Value()
		log.Global.Tracef("Query result: %s -> %s", key, value)
		entries = append(entries, query.Entry{Key: key, Value: value})

		if limit && len(entries) >= q.Limit {
			break
		}
	}

	if err := iter.Error(); err != nil {
		return nil, err
	}

	result := query.ResultsWithEntries(q, entries)

	return result, nil
}

// Put stores the object `value` named by `key`.
//
// The generalized Datastore interface does not impose a value type,
// allowing various datastore middleware implementations (which do not
// handle the values directly) to be composed together.
//
// Ultimately, the lowest-level datastore will need to do some value checking
// or risk getting incorrect values. It may also be useful to expose a more
// type-safe interface to your application, and do the checking up-front.
func (p *PeerDB) Put(_ context.Context, key datastore.Key, value []byte) error {
	leveldbKey := key.Bytes()
	if err := p.db.Put(leveldbKey, value, nil); err != nil {
		return err
	}
	p.incrementPeerCount()
	return nil
}

// Delete removes the value for given `key`. If the key is not in the
// datastore, this method returns no error.
func (p *PeerDB) Delete(_ context.Context, key datastore.Key) error {
	leveldbKey := key.Bytes()
	if err := p.db.Delete(leveldbKey, nil); err != nil {
		return err
	}
	p.decrementPeerCount()
	return nil
}

// Sync Method
// Sync guarantees that any Put or Delete calls under prefix that returned
// before Sync(prefix) was called will be observed after Sync(prefix)
// returns, even if the program crashes. If Put/Delete operations already
// satisfy these requirements then Sync may be a no-op.
//
// If the prefix fails to Sync this method returns an error.
func (p *PeerDB) Sync(ctx context.Context, prefix datastore.Key) error {
	panic("TODO: implement")
}

// Close closes the datastore.
// If the datastore is already closed, this returns ErrClosed.
func (p *PeerDB) Close() error {
	return p.db.Close()
}
