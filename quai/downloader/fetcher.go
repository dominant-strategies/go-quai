package downloader

import (
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/trie"
	"github.com/pkg/errors"
)

const (
	// c_fetchTimeout is the timeout for fetching a structure from the network
	c_fetchTimeout = 30 * time.Second

	// c_numTrieWorkers is the number of concurrent workers to fetch trie nodes
	c_numTrieWorkers = 10

	// c_fetchRetries is the number of times to retry fetching a trie node
	c_fetchRetries = 3

	// c_fetcherQueueSize is the size of the queue for trie node hashes
	c_fetcherQueueSize = 1000000
)

type fetcher struct {
	p2pNode     P2PNode
	queue       chan common.Hash     // queue for trie node hashes that need to be fetched
	fetched     map[common.Hash]bool // map to keep track of fetched or scheduled hashes to avoid duplicates
	mu          sync.Mutex           // mutex to protect the fetched map
	db          ethdb.Database       // local database to save the fetched trie nodes
	quitCh      chan struct{}        // channel to signal the fetcher to stop
	pendingJobs int64                // Atomic counter to track pending jobs
	doneCh      chan struct{}        // channel to signal workers that all jobs are done
}

// FetchBlock fetches a single block by its number.
func (d *fetcher) fetchBlock(loc common.Location, blockNumber *big.Int) (*types.Block, error) {
	blockChan := d.p2pNode.Request(loc, blockNumber, &types.Block{})
	select {
	case block := <-blockChan:
		if block == nil {
			return nil, errors.Errorf("received nil for request block %d", blockNumber)
		}
		return block.(*types.Block), nil
	case <-time.After(c_fetchTimeout):
		return nil, errors.Errorf("timeout fetching block %d", blockNumber)
	case <-d.quitCh:
		return nil, errors.New("fetcher stopped")
	}
}

// FetchStateTrie fetches the state trie of a block by its root hash.
func (f *fetcher) fetchStateTrie(loc common.Location, blockHash, rootHash common.Hash) error {
	// Initialize the fetched map
	f.fetched = make(map[common.Hash]bool)

	// Initialize the queue
	f.queue = make(chan common.Hash, c_fetcherQueueSize)

	// Initialize the done channel
	f.doneCh = make(chan struct{})

	// Start with the root hash
	log.Global.Tracef("enqueuing root hash %x (location: %s)", rootHash, loc.Name())
	f.queue <- rootHash
	atomic.AddInt64(&f.pendingJobs, 1)

	// Start c_numTrieWorkers workers to fetch and process trie nodes
	wg := sync.WaitGroup{}
	for i := 0; i < c_numTrieWorkers; i++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				log.Global.Tracef("worker: %d - waiting for next trie node hash in queue (location %s)", w, loc.Name())
				select {
				case nodeHash := <-f.queue:
					log.Global.Tracef("worker: %d - received trie node hash %s (location %s)", w, nodeHash, loc.Name())
					err := f.processNode(loc, blockHash, nodeHash)
					if err != nil {
						panic("Implement me")
					}
					log.Global.Tracef("worker: %d - succesfully processed trie node %s (location %s)", w, nodeHash, loc.Name())

				case <-f.quitCh:
					return
				case <-f.doneCh:
					log.Global.Debugf("worker: %d - done processing trie nodes (location %s)", w, loc.Name())
					return
				}
			}
		}(i)
	}

	// Start the guardian routine to monitor the number of pending jobs
	f.startGuardianRoutine()

	// Wait for all workers to finish
	wg.Wait()

	return nil
}

// ProcessNode fetches a trie node from the network and processes it.
// It verifies the trie node's hash and saves it to local storage.
// If the trie node is a fullNode, it enqueues its children for processing.
func (f *fetcher) processNode(loc common.Location, blockHash, nodeHash common.Hash) error {
	log.Global.Tracef("processing trie node %s (location %s)", nodeHash, loc.Name())
	defer func() {
		atomic.AddInt64(&f.pendingJobs, -1) // Decrement the pending jobs counter
		log.Global.Tracef("pending jobs: %d", atomic.LoadInt64(&f.pendingJobs))
	}()
	// check if the node has already been fetched
	if f.isFetched(nodeHash) {
		log.Global.Tracef("trie node %s already fetched", nodeHash)
		return nil
	}

	trieNodeResp, err := f.fetchTrieNode(loc, blockHash, nodeHash)
	if err != nil {
		log.Global.Tracef("failed to fetch trie node %s: %v (location: %s)", nodeHash, err, loc.Name())
		return err
	}

	if !verifyNodeHash(trieNodeResp, nodeHash[:]) {
		log.Global.Errorf("hash verification failed for trie node %x - node data: %x (location: %s)", nodeHash, trieNodeResp.NodeData, loc.Name())
		// TODO: Handle invalid trie node hash. Report bad peer, etc.
		panic("Implement")
	}

	// save the trie node to local storage
	err = f.commit(trieNodeResp)
	if err != nil {
		return err
	}
	log.Global.Tracef("trie node %s committed to database", nodeHash)

	// Mark the node as fetched
	f.addFetched(nodeHash)

	// Get the trie node from the response
	trieNode := trieNodeResp.GetTrieNode()

	// If trieNode is a fullNode, enqueue its children for processing
	if trieNode.IsFullNode() {
		log.Global.Tracef("trie node %s is a full node with %d childHashes", nodeHash, len(trieNode.ChildHashes()))
		for _, childHash := range trieNode.ChildHashes() {
			if f.isFetched(childHash) {
				log.Global.Tracef("child trie node %s already fetched. Skipping...", childHash)
				continue
			}
			atomic.AddInt64(&f.pendingJobs, 1)
			f.queue <- childHash
		}
	}

	return nil
}

// FetchTrieNode sends a network request to fetch a trie node.
func (f *fetcher) fetchTrieNode(loc common.Location, blockHash common.Hash, nodeHash common.Hash) (*trie.TrieNodeResponse, error) {
	log.Global.Tracef("fetching trie node %s from block %s and location %s", nodeHash, blockHash, loc.Name())
	trieNodeReq := &trie.TrieNodeRequest{}
	retries := 0
	for {
		// Send the request to the network
		log.Global.Tracef("sending trie node request for %s (location %s) - retry %d", nodeHash, loc.Name(), retries)
		trieChan := f.p2pNode.Request(loc, nodeHash, trieNodeReq)
		select {
		case trieNode := <-trieChan:
			trieNodeResp, ok := trieNode.(*trie.TrieNodeResponse)
			if !ok {
				return nil, errors.Errorf("received unexpected response type %T", trieNode)
			}
			log.Global.Tracef("fetched trie node for %x: resp.NodeHash: %x, datalength: %d ", nodeHash, trieNodeResp.NodeHash, len(trieNodeResp.NodeData))
			return trieNodeResp, nil
		case <-time.After(c_fetchTimeout):
			// Retry fetching the trie node
			retries++
			if retries > c_fetchRetries {
				return nil, errors.Errorf("timeout fetching trie node %s", nodeHash)
			}
		case <-f.quitCh:
			return nil, errors.New("fetcher stopped")
		}
	}

}

// Commit saves the trie node to local storage.
func (f *fetcher) commit(trieNodeResp *trie.TrieNodeResponse) error {
	return f.db.Put(trieNodeResp.NodeHash[:], trieNodeResp.NodeData)
}

// IsFetched returns true if the trie node has already been fetched.
func (f *fetcher) isFetched(nodeHash common.Hash) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetched[nodeHash]
}

// AddFetched marks the trie node as fetched.
func (f *fetcher) addFetched(nodeHash common.Hash) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched[nodeHash] = true
}

// StartGuardian spawns a go routine to monitor the number of pending jobs and close the queue when all jobs are done
func (f *fetcher) startGuardianRoutine() {
	go func() {
		for {
			select {
			case <-f.quitCh:
				return
			default:
				time.Sleep(5 * time.Second)
				if atomic.LoadInt64(&f.pendingJobs) == 0 {
					log.Global.Debugf("All pending trie nodes have been processed. Pending jobs: %d: ", atomic.LoadInt64(&f.pendingJobs))
					close(f.doneCh)
					return
				}
				log.Global.Tracef("Pending trie nodes: %d", atomic.LoadInt64(&f.pendingJobs))
			}
		}
	}()
}
