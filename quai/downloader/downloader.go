package downloader

import (
	"math/big"
	"sync"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/pkg/errors"
)

type Downloader struct {
	p2pNode P2PNode
	f       *fetcher
}

func NewDownloader(p2pNode P2PNode, chainDb ethdb.Database, quitCh chan struct{}) *Downloader {
	f := &fetcher{
		p2pNode: p2pNode,
		db:      chainDb,
		mu:      sync.Mutex{},
		quitCh:  quitCh,
	}
	return &Downloader{
		p2pNode: p2pNode,
		f:       f,
	}
}

func (d *Downloader) StartSnapSync(loc common.Location, blockNumber *big.Int) error {
	log.Global.Debugf("Requesting block %d for location %s", blockNumber, loc.Name())
	block, err := d.f.fetchBlock(loc, blockNumber)
	if err != nil {
		return errors.Errorf("failed to fetch block %d (location %s): %v", blockNumber, loc.Name(), err)
	}
	log.Global.Debugf("fetched block %d for location %s (hash %s)", blockNumber, loc.Name(), block.Hash())

	log.Global.Debugf("Fetching state trie for EVM root %s and location %s", block.Header().EVMRoot(), loc.Name())
	err = d.f.fetchStateTrie(loc, block.Hash(), block.Header().EVMRoot())
	if err != nil {
		return errors.Errorf("failed to fetch state trie for block %d (location %s): %v", blockNumber, loc.Name(), err)
	}

	return nil
}
