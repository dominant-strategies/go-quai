// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package fetcher contains the announcement based header, blocks or transaction synchronisation.
package fetcher

import (
	"errors"
	"math/big"
	"math/rand"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/prque"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics"
	"github.com/dominant-strategies/go-quai/trie"
)

const (
	lightTimeout  = time.Millisecond       // Time allowance before an announced header is explicitly requested
	arriveTimeout = 500 * time.Millisecond // Time allowance before an announced block/transaction is explicitly requested
	gatherSlack   = 100 * time.Millisecond // Interval used to collate almost-expired announces with fetches
	fetchTimeout  = 5 * time.Second        // Maximum allotted time to return an explicitly requested block/transaction
)

const (
	maxUncleDist              = 100  // Maximum allowed backward distance from the chain head
	maxQueueDist              = 32   // Maximum allowed distance from the chain head to queue
	hashLimit                 = 256  // Maximum number of unique blocks or headers a peer may have announced
	blockLimit                = 64   // Maximum number of unique blocks a peer may have delivered
	c_maxAllowableEntropyDist = 3500 // Maximum multiple of zone intrinsic S distance allowed from the current Entropy
)

var (
	blockAnnounceInMeter   = metrics.NewRegisteredMeter("eth/fetcher/block/announces/in", nil)
	blockAnnounceOutTimer  = metrics.NewRegisteredTimer("eth/fetcher/block/announces/out", nil)
	blockAnnounceDropMeter = metrics.NewRegisteredMeter("eth/fetcher/block/announces/drop", nil)
	blockAnnounceDOSMeter  = metrics.NewRegisteredMeter("eth/fetcher/block/announces/dos", nil)

	blockBroadcastInMeter   = metrics.NewRegisteredMeter("eth/fetcher/block/broadcasts/in", nil)
	blockBroadcastOutTimer  = metrics.NewRegisteredTimer("eth/fetcher/block/broadcasts/out", nil)
	blockBroadcastDropMeter = metrics.NewRegisteredMeter("eth/fetcher/block/broadcasts/drop", nil)

	headerFetchMeter = metrics.NewRegisteredMeter("eth/fetcher/block/headers", nil)
	bodyFetchMeter   = metrics.NewRegisteredMeter("eth/fetcher/block/bodies", nil)

	headerFilterInMeter  = metrics.NewRegisteredMeter("eth/fetcher/block/filter/headers/in", nil)
	headerFilterOutMeter = metrics.NewRegisteredMeter("eth/fetcher/block/filter/headers/out", nil)
	bodyFilterInMeter    = metrics.NewRegisteredMeter("eth/fetcher/block/filter/bodies/in", nil)
	bodyFilterOutMeter   = metrics.NewRegisteredMeter("eth/fetcher/block/filter/bodies/out", nil)
)

var errTerminated = errors.New("terminated")

// blockRetrievalFn is a callback type for retrieving a block from the local chain.
type blockRetrievalFn func(common.Hash) *types.Block

// blockWriteFn is a callback type for retrieving a block from the local chain.
type blockWriteFn func(*types.Block)

// headerRequesterFn is a callback type for sending a header retrieval request.
type headerRequesterFn func(common.Hash) error

// bodyRequesterFn is a callback type for sending a body retrieval request.
type bodyRequesterFn func([]common.Hash) error

// headerVerifierFn is a callback type to verify a block's header for fast propagation.
type headerVerifierFn func(header *types.Header) error

// verifySealFn is a callback type to verify seal of a block header and get the PowHash
type verifySealFn func(header *types.Header) (common.Hash, error)

// blockBroadcasterFn is a callback type for broadcasting a block to connected peers.
type blockBroadcasterFn func(block *types.Block, propagate bool)

// chainHeightFn is a callback type to retrieve the current chain height.
type chainHeightFn func() uint64

// currentIntrinsicSFn is a callback type to retrieve the current chain headers intrinsic logS.
type currentIntrinsicSFn func() *big.Int

// currentSFn is a callback type to retrieve the current chain heads Entropy
type currentSFn func() *big.Int

// currentDifficultyFn is a callback type to retrieve the current chain heads difficulty
type currentDifficultyFn func() *big.Int

// peerDropFn is a callback type for dropping a peer detected as malicious.
type peerDropFn func(id string)

// badHashCheckFn is a callback type for checking if a block given by the peer exists in the badHashes list
type badHashCheckFn func(hash common.Hash) bool

// blockAnnounce is the hash notification of the availability of a new block in the
// network.
type blockAnnounce struct {
	hash   common.Hash   // Hash of the block being announced
	number uint64        // Number of the block being announced (0 = unknown | old protocol)
	header *types.Header // Header of the block partially reassembled (new protocol)
	time   time.Time     // Timestamp of the announcement

	origin string // Identifier of the peer originating the notification

	fetchHeader headerRequesterFn // Fetcher function to retrieve the header of an announced block
	fetchBodies bodyRequesterFn   // Fetcher function to retrieve the body of an announced block
}

// headerFilterTask represents a batch of headers needing fetcher filtering.
type headerFilterTask struct {
	peer    string          // The source peer of block headers
	headers []*types.Header // Collection of headers to filter
	time    time.Time       // Arrival time of the headers
}

// bodyFilterTask represents a batch of block bodies (transactions and uncles)
// needing fetcher filtering.
type bodyFilterTask struct {
	peer            string                 // The source peer of block bodies
	transactions    [][]*types.Transaction // Collection of transactions per block bodies
	uncles          [][]*types.Header      // Collection of uncles per block bodies
	extTransactions [][]*types.Transaction // Collection of external transactions per block bodies
	subManifest     []types.BlockManifest  // Collection of manifests per block bodies
	time            time.Time              // Arrival time of the blocks' contents
}

// blockOrHeaderInject represents a schedules import operation.
type blockOrHeaderInject struct {
	origin string

	header *types.Header // Used for light mode fetcher which only cares about header.
	block  *types.Block  // Used for normal mode fetcher which imports full block.
}

// number returns the block number of the injected object.
func (inject *blockOrHeaderInject) number() uint64 {
	if inject.header != nil {
		return inject.header.Number().Uint64()
	}
	return inject.block.NumberU64()
}

// number returns the block hash of the injected object.
func (inject *blockOrHeaderInject) hash() common.Hash {
	if inject.header != nil {
		return inject.header.Hash()
	}
	return inject.block.Hash()
}

// BlockFetcher is responsible for accumulating block announcements from various peers
// and scheduling them for retrieval.
type BlockFetcher struct {
	// Various event channels
	notify chan *blockAnnounce
	inject chan *blockOrHeaderInject

	headerFilter chan chan *headerFilterTask
	bodyFilter   chan chan *bodyFilterTask

	done chan common.Hash
	quit chan struct{}

	// Announce states
	announces  map[string]int                   // Per peer blockAnnounce counts to prevent memory exhaustion
	announced  map[common.Hash][]*blockAnnounce // Announced blocks, scheduled for fetching
	fetching   map[common.Hash]*blockAnnounce   // Announced blocks, currently fetching
	fetched    map[common.Hash][]*blockAnnounce // Blocks with headers fetched, scheduled for body retrieval
	completing map[common.Hash]*blockAnnounce   // Blocks with headers, currently body-completing

	// Block cache
	queue  *prque.Prque                         // Queue containing the import operations (block number sorted)
	queued map[common.Hash]*blockOrHeaderInject // Set of already queued blocks (to dedup imports)

	// Callbacks
	getBlock            blockRetrievalFn    // Retrieves a block from the local chain
	writeBlock          blockWriteFn        // Writes the block to the DB
	verifyHeader        headerVerifierFn    // Checks if a block's headers have a valid proof of work
	verifySeal          verifySealFn        // Checks if blocks PoWHash meets the difficulty requirement
	broadcastBlock      blockBroadcasterFn  // Broadcasts a block to connected peers
	chainHeight         chainHeightFn       // Retrieves the current chain's height
	currentIntrinsicS   currentIntrinsicSFn // Retrieves the current headers intrinsic logS
	currentS            currentSFn          // Retrieves the current heads logS
	currentDifficulty   currentDifficultyFn // Retrieves the current difficulty
	dropPeer            peerDropFn          // Drops a peer for misbehaving
	isBlockHashABadHash badHashCheckFn      // Checks if the block hash exists in the bad hashes list

	// Testing hooks
	announceChangeHook func(common.Hash, bool)           // Method to call upon adding or deleting a hash from the blockAnnounce list
	queueChangeHook    func(common.Hash, bool)           // Method to call upon adding or deleting a block from the import queue
	fetchingHook       func([]common.Hash)               // Method to call upon starting a block (eth/61) or header (eth/62) fetch
	completingHook     func([]common.Hash)               // Method to call upon starting a block body fetch (eth/62)
	importedHook       func(*types.Header, *types.Block) // Method to call upon successful header or block import (both eth/61 and eth/62)
}

// NewBlockFetcher creates a block fetcher to retrieve blocks based on hash announcements.
func NewBlockFetcher(getBlock blockRetrievalFn, writeBlock blockWriteFn, verifyHeader headerVerifierFn, verifySeal verifySealFn, broadcastBlock blockBroadcasterFn, chainHeight chainHeightFn, currentIntrinsicS currentIntrinsicSFn, currentS currentSFn, currentDifficulty currentDifficultyFn, dropPeer peerDropFn, isBlockHashABadHash badHashCheckFn) *BlockFetcher {
	return &BlockFetcher{
		notify:              make(chan *blockAnnounce),
		inject:              make(chan *blockOrHeaderInject),
		headerFilter:        make(chan chan *headerFilterTask),
		bodyFilter:          make(chan chan *bodyFilterTask),
		done:                make(chan common.Hash),
		quit:                make(chan struct{}),
		announces:           make(map[string]int),
		announced:           make(map[common.Hash][]*blockAnnounce),
		fetching:            make(map[common.Hash]*blockAnnounce),
		fetched:             make(map[common.Hash][]*blockAnnounce),
		completing:          make(map[common.Hash]*blockAnnounce),
		queue:               prque.New(nil),
		queued:              make(map[common.Hash]*blockOrHeaderInject),
		getBlock:            getBlock,
		writeBlock:          writeBlock,
		verifyHeader:        verifyHeader,
		verifySeal:          verifySeal,
		broadcastBlock:      broadcastBlock,
		chainHeight:         chainHeight,
		currentIntrinsicS:   currentIntrinsicS,
		currentDifficulty:   currentDifficulty,
		currentS:            currentS,
		dropPeer:            dropPeer,
		isBlockHashABadHash: isBlockHashABadHash,
	}
}

// Start boots up the announcement based synchroniser, accepting and processing
// hash notifications and block fetches until termination requested.
func (f *BlockFetcher) Start() {
	go f.loop()
}

// Stop terminates the announcement based synchroniser, canceling all pending
// operations.
func (f *BlockFetcher) Stop() {
	close(f.quit)
}

// Notify announces the fetcher of the potential availability of a new block in
// the network.
func (f *BlockFetcher) Notify(peer string, hash common.Hash, number uint64, time time.Time,
	headerFetcher headerRequesterFn, bodyFetcher bodyRequesterFn) error {
	block := &blockAnnounce{
		hash:        hash,
		number:      number,
		time:        time,
		origin:      peer,
		fetchHeader: headerFetcher,
		fetchBodies: bodyFetcher,
	}
	select {
	case f.notify <- block:
		return nil
	case <-f.quit:
		return errTerminated
	}
}

// FilterHeaders extracts all the headers that were explicitly requested by the fetcher,
// returning those that should be handled differently.
func (f *BlockFetcher) FilterHeaders(peer string, headers []*types.Header, time time.Time) []*types.Header {
	log.Trace("Filtering headers", "peer", peer, "headers", len(headers))

	// Send the filter channel to the fetcher
	filter := make(chan *headerFilterTask)

	select {
	case f.headerFilter <- filter:
	case <-f.quit:
		return nil
	}
	// Request the filtering of the header list
	select {
	case filter <- &headerFilterTask{peer: peer, headers: headers, time: time}:
	case <-f.quit:
		return nil
	}
	// Retrieve the headers remaining after filtering
	select {
	case task := <-filter:
		return task.headers
	case <-f.quit:
		return nil
	}
}

// FilterBodies extracts all the block bodies that were explicitly requested by
// the fetcher, returning those that should be handled differently.
func (f *BlockFetcher) FilterBodies(peer string, transactions [][]*types.Transaction, uncles [][]*types.Header, etxs [][]*types.Transaction, manifest []types.BlockManifest, time time.Time) ([][]*types.Transaction, [][]*types.Header, [][]*types.Transaction, []types.BlockManifest) {
	log.Trace("Filtering bodies", "peer", peer, "txs", len(transactions), "uncles", len(uncles), "etxs", len(etxs), "manifest", len(manifest))

	// Send the filter channel to the fetcher
	filter := make(chan *bodyFilterTask)

	select {
	case f.bodyFilter <- filter:
	case <-f.quit:
		return nil, nil, nil, nil
	}
	// Request the filtering of the body list
	select {
	case filter <- &bodyFilterTask{peer: peer, transactions: transactions, uncles: uncles, extTransactions: etxs, subManifest: manifest, time: time}:
	case <-f.quit:
		return nil, nil, nil, nil
	}
	// Retrieve the bodies remaining after filtering
	select {
	case task := <-filter:
		return task.transactions, task.uncles, task.extTransactions, task.subManifest
	case <-f.quit:
		return nil, nil, nil, nil
	}
}

// Loop is the main fetcher loop, checking and processing various notification
// events.
func (f *BlockFetcher) loop() {
	// Iterate the block fetching until a quit is requested
	var (
		fetchTimer    = time.NewTimer(0)
		completeTimer = time.NewTimer(0)
	)
	<-fetchTimer.C // clear out the channel
	<-completeTimer.C
	defer fetchTimer.Stop()
	defer completeTimer.Stop()

	for {
		// Clean up any expired block fetches
		for hash, announce := range f.fetching {
			if time.Since(announce.time) > fetchTimeout {
				f.forgetHash(hash)
			}
		}
		// Import any queued blocks
		for !f.queue.Empty() {
			op := f.queue.PopItem().(*blockOrHeaderInject)
			hash := op.hash()
			if f.queueChangeHook != nil {
				f.queueChangeHook(hash, false)
			}
			// Otherwise if fresh and still unknown, try and import
			if f.getBlock(hash) != nil {
				f.forgetBlock(hash)
				continue
			}

			f.ImportBlocks(op.origin, op.block, true)
		}
		// Wait for an outside event to occur
		select {
		case <-f.quit:
			// BlockFetcher terminating, abort all operations
			return

		case notification := <-f.notify:
			// A block was announced, make sure the peer isn't DOSing us
			blockAnnounceInMeter.Mark(1)

			count := f.announces[notification.origin] + 1
			if count > hashLimit {
				log.Debug("Peer exceeded outstanding announces", "peer", notification.origin, "limit", hashLimit)
				blockAnnounceDOSMeter.Mark(1)
				break
			}
			// If we have a valid block number, check that it's potentially useful
			if notification.number > 0 {
				if dist := int64(notification.number) - int64(f.chainHeight()); dist < -maxUncleDist || dist > maxQueueDist {
					log.Debug("Peer discarded announcement", "peer", notification.origin, "number", notification.number, "hash", notification.hash, "distance", dist)
					blockAnnounceDropMeter.Mark(1)
					break
				}
			}
			// All is well, schedule the announce if block's not yet downloading
			if _, ok := f.fetching[notification.hash]; ok {
				break
			}
			if _, ok := f.completing[notification.hash]; ok {
				break
			}
			f.announces[notification.origin] = count
			f.announced[notification.hash] = append(f.announced[notification.hash], notification)
			if f.announceChangeHook != nil && len(f.announced[notification.hash]) == 1 {
				f.announceChangeHook(notification.hash, true)
			}
			if len(f.announced) == 1 {
				f.rescheduleFetch(fetchTimer)
			}

		case op := <-f.inject:
			// A direct block insertion was requested, try and fill any pending gaps
			blockBroadcastInMeter.Mark(1)

			f.enqueue(op.origin, nil, op.block)

		case hash := <-f.done:
			// A pending import finished, remove all traces of the notification
			f.forgetHash(hash)
			f.forgetBlock(hash)

		case <-fetchTimer.C:
			// At least one block's timer ran out, check for needing retrieval
			request := make(map[string][]common.Hash)

			for hash, announces := range f.announced {
				// In current LES protocol(les2/les3), only header announce is
				// available, no need to wait too much time for header broadcast.
				timeout := arriveTimeout - gatherSlack
				if time.Since(announces[0].time) > timeout {
					// Pick a random peer to retrieve from, reset all others
					announce := announces[rand.Intn(len(announces))]
					f.forgetHash(hash)

					// If the block still didn't arrive, queue for fetching
					if f.getBlock(hash) == nil {
						request[announce.origin] = append(request[announce.origin], hash)
						f.fetching[hash] = announce
					}
				}
			}
			// Send out all block header requests
			for peer, hashes := range request {
				log.Trace("Fetching scheduled headers", "peer", peer, "list", hashes)

				// Create a closure of the fetch and schedule in on a new thread
				fetchHeader, hashes := f.fetching[hashes[0]].fetchHeader, hashes
				go func() {
					if f.fetchingHook != nil {
						f.fetchingHook(hashes)
					}
					for _, hash := range hashes {
						headerFetchMeter.Mark(1)
						fetchHeader(hash) // Suboptimal, but protocol doesn't allow batch header retrievals
					}
				}()
			}
			// Schedule the next fetch if blocks are still pending
			f.rescheduleFetch(fetchTimer)

		case <-completeTimer.C:
			// At least one header's timer ran out, retrieve everything
			request := make(map[string][]common.Hash)

			for hash, announces := range f.fetched {
				// Pick a random peer to retrieve from, reset all others
				announce := announces[rand.Intn(len(announces))]
				f.forgetHash(hash)

				// If the block still didn't arrive, queue for completion
				if f.getBlock(hash) == nil {
					request[announce.origin] = append(request[announce.origin], hash)
					f.completing[hash] = announce
				}
			}
			// Send out all block body requests
			for peer, hashes := range request {
				log.Trace("Fetching scheduled bodies", "peer", peer, "list", hashes)

				// Create a closure of the fetch and schedule in on a new thread
				if f.completingHook != nil {
					f.completingHook(hashes)
				}
				bodyFetchMeter.Mark(int64(len(hashes)))
				go f.completing[hashes[0]].fetchBodies(hashes)
			}
			// Schedule the next fetch if blocks are still pending
			f.rescheduleComplete(completeTimer)

		case filter := <-f.headerFilter:
			// Headers arrived from a remote peer. Extract those that were explicitly
			// requested by the fetcher, and return everything else so it's delivered
			// to other parts of the system.
			var task *headerFilterTask
			select {
			case task = <-filter:
			case <-f.quit:
				return
			}
			headerFilterInMeter.Mark(int64(len(task.headers)))

			// Split the batch of headers into unknown ones (to return to the caller),
			// known incomplete ones (requiring body retrievals) and completed blocks.
			unknown, incomplete, complete, lightHeaders := []*types.Header{}, []*blockAnnounce{}, []*types.Block{}, []*blockAnnounce{}
			for _, header := range task.headers {
				hash := header.Hash()

				// Filter fetcher-requested headers from other synchronisation algorithms
				if announce := f.fetching[hash]; announce != nil && announce.origin == task.peer && f.fetched[hash] == nil && f.completing[hash] == nil && f.queued[hash] == nil {
					// If the delivered header does not match the promised number, drop the announcer
					if header.Number().Uint64() != announce.number {
						log.Trace("Invalid block number fetched", "peer", announce.origin, "hash", header.Hash(), "announced", announce.number, "provided", header.Number())
						f.dropPeer(announce.origin)
						f.forgetHash(hash)
						continue
					}
					// Only keep if not imported by other means
					if f.getBlock(hash) == nil {
						announce.header = header
						announce.time = task.time

						// If the block is empty (header only), short circuit into the final import queue
						if header.TxHash() == types.EmptyRootHash && header.UncleHash() == types.EmptyUncleHash && header.EtxHash() == types.EmptyRootHash && header.ManifestHash() == types.EmptyRootHash {
							log.Trace("Block empty, skipping body retrieval", "peer", announce.origin, "number", header.Number(), "hash", header.Hash())

							block := types.NewBlockWithHeader(header)
							block.ReceivedAt = task.time

							complete = append(complete, block)
							f.completing[hash] = announce
							continue
						}
						// Otherwise add to the list of blocks needing completion
						incomplete = append(incomplete, announce)
					} else {
						log.Trace("Block already imported, discarding header", "peer", announce.origin, "number", header.Number(), "hash", header.Hash())
						f.forgetHash(hash)
					}
				} else {
					// BlockFetcher doesn't know about it, add to the return list
					unknown = append(unknown, header)
				}
			}
			headerFilterOutMeter.Mark(int64(len(unknown)))
			select {
			case filter <- &headerFilterTask{headers: unknown, time: task.time}:
			case <-f.quit:
				return
			}
			// Schedule the retrieved headers for body completion
			for _, announce := range incomplete {
				hash := announce.header.Hash()
				if _, ok := f.completing[hash]; ok {
					continue
				}
				f.fetched[hash] = append(f.fetched[hash], announce)
				if len(f.fetched) == 1 {
					f.rescheduleComplete(completeTimer)
				}
			}
			// Schedule the header for light fetcher import
			for _, announce := range lightHeaders {
				f.enqueue(announce.origin, announce.header, nil)
			}
			// Schedule the header-only blocks for import
			for _, block := range complete {
				if announce := f.completing[block.Hash()]; announce != nil {
					f.enqueue(announce.origin, nil, block)
				}
			}

		case filter := <-f.bodyFilter:
			// Block bodies arrived, extract any explicitly requested blocks, return the rest
			var task *bodyFilterTask
			select {
			case task = <-filter:
			case <-f.quit:
				return
			}
			bodyFilterInMeter.Mark(int64(len(task.transactions)))
			blocks := []*types.Block{}
			// abort early if there's nothing explicitly requested
			if len(f.completing) > 0 {
				for i := 0; i < len(task.transactions) && i < len(task.uncles) && i < len(task.extTransactions) && i < len(task.subManifest); i++ {
					// Match up a body to any possible completion request
					var (
						matched      = false
						uncleHash    common.Hash // calculated lazily and reused
						txnHash      common.Hash // calculated lazily and reused
						etxnHash     common.Hash // calculated lazily and reused
						manifestHash common.Hash // calculated lazily and reused
					)
					for hash, announce := range f.completing {
						if f.queued[hash] != nil || announce.origin != task.peer {
							continue
						}
						if uncleHash == (common.Hash{}) {
							uncleHash = types.CalcUncleHash(task.uncles[i])
						}
						if uncleHash != announce.header.UncleHash() {
							continue
						}
						if txnHash == (common.Hash{}) {
							txnHash = types.DeriveSha(types.Transactions(task.transactions[i]), trie.NewStackTrie(nil))
						}
						if txnHash != announce.header.TxHash() {
							continue
						}
						if etxnHash == (common.Hash{}) {
							etxnHash = types.DeriveSha(types.Transactions(task.extTransactions[i]), trie.NewStackTrie(nil))
						}
						if etxnHash != announce.header.EtxHash() {
							continue
						}
						if manifestHash == (common.Hash{}) {
							manifestHash = types.DeriveSha(task.subManifest[i], trie.NewStackTrie(nil))
						}
						if manifestHash != announce.header.ManifestHash() {
							continue
						}
						// Mark the body matched, reassemble if still unknown
						matched = true
						if f.getBlock(hash) == nil {
							block := types.NewBlockWithHeader(announce.header).WithBody(task.transactions[i], task.uncles[i], task.extTransactions[i], task.subManifest[i])
							block.ReceivedAt = task.time
							blocks = append(blocks, block)
						} else {
							f.forgetHash(hash)
						}

					}
					if matched {
						task.transactions = append(task.transactions[:i], task.transactions[i+1:]...)
						task.uncles = append(task.uncles[:i], task.uncles[i+1:]...)
						task.extTransactions = append(task.extTransactions[:i], task.extTransactions[i+1:]...)
						task.subManifest = append(task.subManifest[:i], task.subManifest[i+1:]...)
						i--
						continue
					}
				}
			}
			bodyFilterOutMeter.Mark(int64(len(task.transactions)))
			select {
			case filter <- task:
			case <-f.quit:
				return
			}
			// Schedule the retrieved blocks for ordered import
			for _, block := range blocks {
				if announce := f.completing[block.Hash()]; announce != nil {
					f.enqueue(announce.origin, nil, block)
				}
			}
		}
	}
}

// rescheduleFetch resets the specified fetch timer to the next blockAnnounce timeout.
func (f *BlockFetcher) rescheduleFetch(fetch *time.Timer) {
	// Short circuit if no blocks are announced
	if len(f.announced) == 0 {
		return
	}
	// Otherwise find the earliest expiring announcement
	earliest := time.Now()
	for _, announces := range f.announced {
		if earliest.After(announces[0].time) {
			earliest = announces[0].time
		}
	}
	fetch.Reset(arriveTimeout - time.Since(earliest))
}

// rescheduleComplete resets the specified completion timer to the next fetch timeout.
func (f *BlockFetcher) rescheduleComplete(complete *time.Timer) {
	// Short circuit if no headers are fetched
	if len(f.fetched) == 0 {
		return
	}
	// Otherwise find the earliest expiring announcement
	earliest := time.Now()
	for _, announces := range f.fetched {
		if earliest.After(announces[0].time) {
			earliest = announces[0].time
		}
	}
	complete.Reset(gatherSlack - time.Since(earliest))
}

// enqueue schedules a new header or block import operation, if the component
// to be imported has not yet been seen.
func (f *BlockFetcher) enqueue(peer string, header *types.Header, block *types.Block) {
	var (
		hash   common.Hash
		number uint64
	)
	if header != nil {
		hash, number = header.Hash(), header.Number().Uint64()
	} else {
		hash, number = block.Hash(), block.NumberU64()
	}

	// Schedule the block for future importing
	if _, ok := f.queued[hash]; !ok {
		op := &blockOrHeaderInject{origin: peer}
		if header != nil {
			op.header = header
		} else {
			op.block = block
		}
		f.queued[hash] = op
		f.queue.Push(op, -int64(number))
		if f.queueChangeHook != nil {
			f.queueChangeHook(hash, true)
		}
		log.Debug("Queued delivered header or block", "peer", peer, "number", number, "hash", hash, "queued", f.queue.Size())
	}
}

// importBlocks spawns a new goroutine to run a block insertion into the chain. If the
// block's number is at the same height as the current import phase, it updates
// the phase states accordingly.
func (f *BlockFetcher) ImportBlocks(peer string, block *types.Block, relay bool) {
	hash := block.Hash()
	nodeCtx := common.NodeLocation.Context()

	powhash, err := f.verifySeal(block.Header())
	if err != nil {
		return
	}
	// Check if the Block is atleast half the current difficulty in Zone Context,
	// this makes sure that the nodes don't listen to the forks with the PowHash
	//	with less than 50% of current difficulty
	if nodeCtx == common.ZONE_CTX && new(big.Int).SetBytes(powhash.Bytes()).Cmp(new(big.Int).Div(f.currentDifficulty(), big.NewInt(2))) < 0 {
		return
	}

	currentIntrinsicS := f.currentIntrinsicS()
	MaxAllowableEntropyDist := new(big.Int).Mul(currentIntrinsicS, big.NewInt(c_maxAllowableEntropyDist))
	looseMaxAllowableEntropy := new(big.Int).Div(MaxAllowableEntropyDist, big.NewInt(100))
	looseSyncEntropyDist := new(big.Int).Add(MaxAllowableEntropyDist, looseMaxAllowableEntropy)

	broadCastEntropy := block.ParentEntropy()

	// If someone is mining not within MaxAllowableEntropyDist*currentIntrinsicS dont broadcast
	if relay && f.currentS().Cmp(new(big.Int).Add(broadCastEntropy, MaxAllowableEntropyDist)) > 0 {
		return
	}
	// But don't drop the peers if within 1% of that distance
	if relay && f.currentS().Cmp(new(big.Int).Add(broadCastEntropy, looseSyncEntropyDist)) > 0 {
		if nodeCtx != common.PRIME_CTX {
			f.dropPeer(peer)
		}
		return
	}

	// Run the import on a new thread
	log.Debug("Importing propagated block", "peer", peer, "number", block.Number(), "hash", hash)
	go func() {
		defer func() { f.done <- hash }()

		// If Block broadcasted by the peer exists in the bad block list drop the peer
		if f.isBlockHashABadHash(block.Hash()) {
			f.dropPeer(peer)
			return
		}
		// Quickly validate the header and propagate the block if it passes
		err := f.verifyHeader(block.Header())

		// Including the ErrUnknownAncestor as well because a filter has already
		// been applied for all the blocks that come until here. Since there
		// exists a timedCache where the blocks expire, it is okay to let this
		// block through and broadcast the block.
		if err == nil || err.Error() == consensus.ErrUnknownAncestor.Error() {
			// All ok, quickly propagate to our peers
			blockBroadcastOutTimer.UpdateSince(block.ReceivedAt)

			// Only relay the Mined Blocks that meet the depth criteria
			if relay {
				go f.broadcastBlock(block, true)
			}
		} else if err.Error() == consensus.ErrFutureBlock.Error() {
			// Weird future block, don't fail, but neither propagate
		} else {
			// Something went very wrong, drop the peer
			log.Debug("Propagated block verification failed", "peer", peer, "number", block.Number(), "hash", hash, "err", err)
			f.dropPeer(peer)
			return
		}
		// TODO: verify the Headers work to be in a certain threshold window
		f.writeBlock(block)
		// If import succeeded, broadcast the block
		blockAnnounceOutTimer.UpdateSince(block.ReceivedAt)

		// Only relay the Mined Blocks that meet the depth criteria
		if relay {
			go f.broadcastBlock(block, false)
		}

		// Invoke the testing hook if needed
		if f.importedHook != nil {
			f.importedHook(nil, block)
		}
	}()
}

// forgetHash removes all traces of a block announcement from the fetcher's
// internal state.
func (f *BlockFetcher) forgetHash(hash common.Hash) {
	// Remove all pending announces and decrement DOS counters
	if announceMap, ok := f.announced[hash]; ok {
		for _, announce := range announceMap {
			f.announces[announce.origin]--
			if f.announces[announce.origin] <= 0 {
				delete(f.announces, announce.origin)
			}
		}
		delete(f.announced, hash)
		if f.announceChangeHook != nil {
			f.announceChangeHook(hash, false)
		}
	}
	// Remove any pending fetches and decrement the DOS counters
	if announce := f.fetching[hash]; announce != nil {
		f.announces[announce.origin]--
		if f.announces[announce.origin] <= 0 {
			delete(f.announces, announce.origin)
		}
		delete(f.fetching, hash)
	}

	// Remove any pending completion requests and decrement the DOS counters
	for _, announce := range f.fetched[hash] {
		f.announces[announce.origin]--
		if f.announces[announce.origin] <= 0 {
			delete(f.announces, announce.origin)
		}
	}
	delete(f.fetched, hash)

	// Remove any pending completions and decrement the DOS counters
	if announce := f.completing[hash]; announce != nil {
		f.announces[announce.origin]--
		if f.announces[announce.origin] <= 0 {
			delete(f.announces, announce.origin)
		}
		delete(f.completing, hash)
	}
}

// forgetBlock removes all traces of a queued block from the fetcher's internal
// state.
func (f *BlockFetcher) forgetBlock(hash common.Hash) {
	if insert := f.queued[hash]; insert != nil {
		delete(f.queued, hash)
	}
}
