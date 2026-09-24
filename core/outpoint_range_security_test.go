package core

import (
	"math"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
)

type countingRangeDB struct {
	ethdb.Database
	reads int
}

func (db *countingRangeDB) Get(key []byte) ([]byte, error) {
	db.reads++
	if db.reads > 1 {
		panic("uint32 range wrapped")
	}
	return nil, nil
}

func TestOutpointRangeStopsAtMaxUint32(t *testing.T) {
	db := &countingRangeDB{Database: rawdb.NewMemoryDatabase(log.Global)}
	c := &Core{sl: &Slice{sliceDb: db}}
	if _, err := c.GetOutpointsByAddressAndRange(common.ZeroAddress(common.Location{0, 0}), math.MaxUint32, math.MaxUint32); err != nil {
		t.Fatal(err)
	}
	if db.reads != 1 {
		t.Fatalf("got %d database reads, want 1", db.reads)
	}
}
