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
	"math/rand"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/prque"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics"
	"github.com/dominant-strategies/go-quai/trie"
)

var (
	pendingEtxsAnnounceInMeter   = metrics.NewRegisteredMeter("eth/fetcher/pendingetxs/announces/in", nil)
	pendingEtxsAnnounceOutTimer  = metrics.NewRegisteredTimer("eth/fetcher/pendingetxs/announces/out", nil)
	pendingEtxsAnnounceDropMeter = metrics.NewRegisteredMeter("eth/fetcher/pendingetxs/announces/drop", nil)
	pendingEtxsAnnounceDOSMeter  = metrics.NewRegisteredMeter("eth/fetcher/pendingetxs/announces/dos", nil)

	pendingEtxsBroadcastInMeter   = metrics.NewRegisteredMeter("eth/fetcher/pendinetxs/broadcasts/in", nil)
	pendingEtxsBroadcastOutTimer  = metrics.NewRegisteredTimer("eth/fetcher/pendingetxs/broadcasts/out", nil)
	pendingEtxsBroadcastDropMeter = metrics.NewRegisteredMeter("eth/fetcher/pendingetxs/broadcasts/drop", nil)
	pendingEtxsBroadcastDOSMeter  = metrics.NewRegisteredMeter("eth/fetcher/pendingetxs/broadcasts/dos", nil)

	pendingEtxsFetchMeter = metrics.NewRegisteredMeter("eth/fetcher/pendingetxs/headers", nil)
)

const (
	pendingEtxsLimit = 32 // Maximum number of unique pendingEtxs set a peer may have delivered
)

// pendingEtxsRetrievalFn is a callback type for retrieving a pendingEtxs from the local database.
type pendingEtxsRetrievalFn func(common.Hash) *types.PendingEtxs

// pendingEtxsRequesterFn is a callback type for sending a pendingEtxs retrieval request.
type pendingEtxsRequesterFn func(common.Hash) error

// blockBroadcasterFn is a callback type for broadcasting a block to connected peers.
type pendingEtxsBroadcasterFn func(pendingEtxs types.PendingEtxs, propagate bool)

// chainInsertFn is a callback type to insert a pendingEtxs into the database.
type pendingEtxsInsertFn func(types.PendingEtxs) error

// pendingEtxsAnnounce is the hash notification of the availability of a new pendingEtxs in the
// network.
type pendingEtxsAnnounce struct {
	hash   common.Hash // Hash of the block being announced
	number uint64      // Number of the pendingEtxs header being announced (0 = unknown | old protocol)
	time   time.Time   // Timestamp of the announcement

	origin string // Identifier of the peer originating the notification

	fetchPendingEtxs pendingEtxsRequesterFn // Fetcher function to retrieve the pendingEtxs of an announced block
}

// pendingEtxsInject represents a schedules import operation.
type pendingEtxsInject struct {
	origin      string
	pendingEtxs types.PendingEtxs // Used for normal mode fetcher which imports full pendingEtxs.
}

// PendingEtxsFetcher is responsible for accumulating pendingEtxs announcements from various peers
// and scheduling them for retrieval.
type PendingEtxsFetcher struct {
	// Various event channels
	notify chan *pendingEtxsAnnounce
	inject chan *pendingEtxsInject

	done chan common.Hash
	quit chan struct{}

	// Announce states
	announces  map[string]int                         // Per peer pendingEtxsAnnounce counts to prevent memory exhaustion
	announced  map[common.Hash][]*pendingEtxsAnnounce // Announced pendingEtxs, scheduled for fetching
	fetching   map[common.Hash]*pendingEtxsAnnounce   // Announced pendingEtxs, currently fetching
	fetched    map[common.Hash][]*pendingEtxsAnnounce // Blocks with pendingEtxs fetched, scheduled for body retrieval
	completing map[common.Hash]*pendingEtxsAnnounce   // Blocks with pendingEtxs, currently body-completing

	// Block cache
	queue  *prque.Prque                       // Queue containing the import operations (block number sorted)
	queues map[string]int                     // Per peer block counts to prevent memory exhaustion
	queued map[common.Hash]*pendingEtxsInject // Set of already queued pendingEtxs (to dedup imports)

	// Callbacks
	getPendingEtxs       pendingEtxsRetrievalFn   // Retrieves a pendingEtxs from the local chain
	broadcastPendingEtxs pendingEtxsBroadcasterFn // Broadcasts a pendingEtxs to connected peers
	insertPendingEtxs    pendingEtxsInsertFn      // Injects a pendingEtxs into the etx set
	dropPeer             peerDropFn               // Drops a peer for misbehaving
}

// NewPendingEtxsFetcher creates a pendingEtxs fetcher to retrieve pendingEtxs based on hash announcements.
func NewPendingEtxsFetcher(getPendingEtxs pendingEtxsRetrievalFn, broadcastPendingEtxs pendingEtxsBroadcasterFn, insertPendingEtxs pendingEtxsInsertFn, dropPeer peerDropFn) *PendingEtxsFetcher {
	return &PendingEtxsFetcher{
		notify:               make(chan *pendingEtxsAnnounce),
		done:                 make(chan common.Hash),
		quit:                 make(chan struct{}),
		announces:            make(map[string]int),
		announced:            make(map[common.Hash][]*pendingEtxsAnnounce),
		fetching:             make(map[common.Hash]*pendingEtxsAnnounce),
		fetched:              make(map[common.Hash][]*pendingEtxsAnnounce),
		completing:           make(map[common.Hash]*pendingEtxsAnnounce),
		queue:                prque.New(nil),
		queues:               make(map[string]int),
		queued:               make(map[common.Hash]*pendingEtxsInject),
		getPendingEtxs:       getPendingEtxs,
		broadcastPendingEtxs: broadcastPendingEtxs,
		insertPendingEtxs:    insertPendingEtxs,
		dropPeer:             dropPeer,
	}
}

// Start boots up the announcement based synchroniser, accepting and processing
// hash notifications and pendingEtxs fetches until termination requested.
func (f *PendingEtxsFetcher) Start() {
	go f.loop()
}

// Stop terminates the announcement based synchroniser, canceling all pending
// operations.
func (f *PendingEtxsFetcher) Stop() {
	close(f.quit)
}

// Notify announces the fetcher of the potential availability of a new pendingEtxs in
// the network.
func (f *PendingEtxsFetcher) Notify(peer string, hash common.Hash, number uint64, time time.Time,
	pendingEtxsFetcher pendingEtxsRequesterFn) error {
	pendingEtx := &pendingEtxsAnnounce{
		hash:             hash,
		number:           number,
		time:             time,
		origin:           peer,
		fetchPendingEtxs: pendingEtxsFetcher,
	}
	select {
	case f.notify <- pendingEtx:
		return nil
	case <-f.quit:
		return errTerminated
	}
}

// Enqueue tries to fill gaps the fetcher's future import queue.
func (f *PendingEtxsFetcher) Enqueue(peer string, pendingEtxs types.PendingEtxs) error {
	op := &pendingEtxsInject{
		origin:      peer,
		pendingEtxs: pendingEtxs,
	}
	select {
	case f.inject <- op:
		return nil
	case <-f.quit:
		return errTerminated
	}
}

// Loop is the main fetcher loop, checking and processing various notification
// events.
func (f *PendingEtxsFetcher) loop() {
	// Iterate the pendingEtxs fetching until a quit is requested
	var (
		fetchTimer    = time.NewTimer(0)
		completeTimer = time.NewTimer(0)
	)
	<-fetchTimer.C // clear out the channel
	<-completeTimer.C
	defer fetchTimer.Stop()
	defer completeTimer.Stop()

	for {
		// Clean up any expired pendingEtxs fetches
		for hash, announce := range f.fetching {
			if time.Since(announce.time) > fetchTimeout {
				f.forgetHash(hash)
			}
		}
		// Import any queued pending etx that could potentially fit
		for !f.queue.Empty() {
			op := f.queue.PopItem().(*pendingEtxsInject)
			f.importPendingEtxs(op.origin, op.pendingEtxs)
		}
		// Wait for an outside event to occur
		select {
		case <-f.quit:
			// PendingEtxsFetcher terminating, abort all operations
			return

		case notification := <-f.notify:
			// A pendingEtxs was announced, make sure the peer isn't DOSing us
			blockAnnounceInMeter.Mark(1)

			count := f.announces[notification.origin] + 1
			if count > hashLimit {
				log.Debug("Peer exceeded outstanding announces", "peer", notification.origin, "limit", hashLimit)
				pendingEtxsAnnounceDOSMeter.Mark(1)
				break
			}
			// All is well, schedule the announce if pendingEtxs's not yet downloading
			if _, ok := f.fetching[notification.hash]; ok {
				break
			}
			if _, ok := f.completing[notification.hash]; ok {
				break
			}
			f.announces[notification.origin] = count
			f.announced[notification.hash] = append(f.announced[notification.hash], notification)

			if len(f.announced) == 1 {
				f.rescheduleFetch(fetchTimer)
			}

		case op := <-f.inject:
			// A direct pendingEtxs insertion was requested, try and fill any pending gaps
			pendingEtxsBroadcastInMeter.Mark(1)
			f.enqueue(op.origin, op.pendingEtxs)

		case hash := <-f.done:
			// A pending import finished, remove all traces of the notification
			f.forgetHash(hash)
			f.forgetPendingEtxs(hash)

		case <-fetchTimer.C:
			// At least one block's timer ran out, check for needing retrieval
			request := make(map[string][]common.Hash)

			for hash, announces := range f.announced {
				timeout := arriveTimeout - gatherSlack
				if time.Since(announces[0].time) > timeout {
					// Pick a random peer to retrieve from, reset all others
					f.forgetHash(hash)
				}
			}
			// Send out all pendingEtxs requests
			for peer, hashes := range request {
				log.Trace("Fetching scheduled pendingEtxs", "peer", peer, "list", hashes)

				// Create a closure of the fetch and schedule in on a new thread
				fetchHeader, hashes := f.fetching[hashes[0]].fetchPendingEtxs, hashes
				go func() {
					for _, hash := range hashes {
						headerFetchMeter.Mark(1)
						fetchHeader(hash) // Suboptimal, but protocol doesn't allow batch header retrievals
					}
				}()
			}
			// Schedule the next fetch if pendingEtxs are still pending
			f.rescheduleFetch(fetchTimer)

		case <-completeTimer.C:
			// At least one header's timer ran out, retrieve everything
			request := make(map[string][]common.Hash)

			for hash, announces := range f.fetched {
				// Pick a random peer to retrieve from, reset all others
				announce := announces[rand.Intn(len(announces))]
				f.forgetHash(hash)

				// If the block still didn't arrive, queue for completion
				if f.getPendingEtxs(hash) == nil {
					request[announce.origin] = append(request[announce.origin], hash)
					f.completing[hash] = announce
				}
			}
			// Schedule the next fetch if pendingEtxs are still pending
			f.rescheduleComplete(completeTimer)
		}
	}
}

// rescheduleFetch resets the specified fetch timer to the next pendingEtxsAnnounce timeout.
func (f *PendingEtxsFetcher) rescheduleFetch(fetch *time.Timer) {
	// Short circuit if no pendingEtxs are announced
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
func (f *PendingEtxsFetcher) rescheduleComplete(complete *time.Timer) {
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

// enqueue schedules a new pendingEtxs import operation, if the component
// to be imported has not yet been seen.
func (f *PendingEtxsFetcher) enqueue(peer string, pendingEtxs types.PendingEtxs) {
	var (
		hash   common.Hash
		number uint64
	)
	hash, number = pendingEtxs.Header.Hash(), pendingEtxs.Header.NumberU64()
	// Ensure the peer isn't DOSing us
	count := f.queues[peer] + 1
	if count > pendingEtxsLimit {
		log.Debug("Discarded delivered pendingEtxs, exceeded allowance", "peer", peer, "number", number, "hash", hash, "limit", pendingEtxsLimit)
		pendingEtxsBroadcastDOSMeter.Mark(1)
		f.forgetHash(hash)
		return
	}

	// Schedule the pendingEtxs for future importing
	if _, ok := f.queued[hash]; !ok {
		op := &pendingEtxsInject{origin: peer}
		op.pendingEtxs = pendingEtxs
		f.queues[peer] = count
		f.queued[hash] = op
		f.queue.Push(op, -int64(number))
		log.Debug("Queued delivered pendingEtxs", "peer", peer, "number", number, "hash", hash, "queued", f.queue.Size())
	}
}

// importPendingEtxs spawns a new goroutine to run a pendingEtxs insertion into the database. If the
func (f *PendingEtxsFetcher) importPendingEtxs(peer string, pendingEtxs types.PendingEtxs) {
	hash := pendingEtxs.Header.Hash()
	// Run the import on a new thread
	log.Debug("Importing propagated pendingEtxs", "peer", peer, "number", pendingEtxs.Header.Number(), "hash", pendingEtxs.Header.Hash())
	go func() {
		defer func() { f.done <- hash }()

		// Quickly validate the header and propagate the pendingEtxs if it passes
		trieHasher := trie.NewStackTrie(nil)
		if !pendingEtxs.IsValid(trieHasher) {
			// drop the peer if it sends a invalid pendingEtxs
			f.dropPeer(peer)
			return
		}
		// Run the actual import and log any issues
		if err := f.insertPendingEtxs(pendingEtxs); err != nil {
			return
		}

		// If import succeeded, broadcast the pendingEtxs
		go f.broadcastPendingEtxs(pendingEtxs, true)
	}()
}

// forgetHash removes all traces of a pendingEtxs announcement from the fetcher's
// internal state.
func (f *PendingEtxsFetcher) forgetHash(hash common.Hash) {
	// Remove all pending announces and decrement DOS counters
	if announceMap, ok := f.announced[hash]; ok {
		for _, announce := range announceMap {
			f.announces[announce.origin]--
			if f.announces[announce.origin] <= 0 {
				delete(f.announces, announce.origin)
			}
		}
		delete(f.announced, hash)
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

// forgetPendingEtxs removes all traces of a queued block from the fetcher's internal
// state.
func (f *PendingEtxsFetcher) forgetPendingEtxs(hash common.Hash) {
	if insert := f.queued[hash]; insert != nil {
		f.queues[insert.origin]--
		if f.queues[insert.origin] == 0 {
			delete(f.queues, insert.origin)
		}
		delete(f.queued, hash)
	}
}
