package downloader

import (
	"math/big"
	"sync"

	"bytes"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
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
	log.Global.Tracef("Requesting block %d for location %s", blockNumber, loc.Name())
	block, err := d.f.fetchBlock(loc, blockNumber)
	if err != nil {
		return errors.Errorf("failed to fetch block %d (location %s): %v", blockNumber, loc.Name(), err)
	}
	log.Global.Tracef("fetched block %d for location %s (hash %s)", blockNumber, loc.Name(), block.Hash())

	log.Global.Tracef("Fetching state trie for EVM root %s and location %s", block.Header().EVMRoot(), loc.Name())
	err = d.f.fetchStateTrie(loc, block.Hash(), block.Header().EVMRoot())
	if err != nil {
		return errors.Errorf("failed to fetch state trie for block %d (location %s): %v", blockNumber, loc.Name(), err)
	}

	return nil
}

// VerifyNodeHash verifies a expected hash against the RLP encoding of the received trie node
func verifyNodeHash(rlpBytes []byte, expectedHash []byte) bool {
	hash := crypto.Keccak256(rlpBytes)
	return bytes.Equal(hash, expectedHash)
}
