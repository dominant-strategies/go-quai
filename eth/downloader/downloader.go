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

// Package downloader contains the manual full chain synchronisation.
package downloader

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	quai "github.com/dominant-strategies/go-quai"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/state/snapshot"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/eth/fetcher"
	"github.com/dominant-strategies/go-quai/eth/protocols/eth"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics"
)

var (
	MaxBlockFetch     = 128  // Amount of blocks to be fetched per retrieval request
	MaxHeaderFetch    = 192  // Amount of block headers to be fetched per retrieval request
	MaxSkeletonWindow = 1024 // Amount of blocks to be fetched for a skeleton assembly.
	MaxSkeletonSize   = 1024 // Number of header fetches to need for a skeleton assembly
	MaxStateFetch     = 384  // Amount of node state values to allow fetching per request

	PrimeSkeletonDist = 8
	PrimeFetchDepth   = 1000
	RegionFetchDepth  = 7000
	ZoneFetchDepth    = 21000

	maxQueuedHeaders  = 32 * 1024 // [eth/62] Maximum number of headers to queue for import (DOS protection)
	maxHeadersProcess = 2048      // Number of header download results to import at once into the chain

	fsHeaderContCheck = 3 * time.Second // Time interval to check for header continuations during state download
)

var (
	errBusy                    = errors.New("busy")
	errUnknownPeer             = errors.New("peer is unknown or unhealthy")
	errBadPeer                 = errors.New("action from bad peer ignored")
	errStallingPeer            = errors.New("peer is stalling")
	errUnsyncedPeer            = errors.New("unsynced peer")
	errNoPeers                 = errors.New("no peers to keep download active")
	errTimeout                 = errors.New("timeout")
	errEmptyHeaderSet          = errors.New("empty header set by peer")
	errPeersUnavailable        = errors.New("no peers available or all tried for download")
	errInvalidAncestor         = errors.New("retrieved ancestor is invalid")
	errInvalidChain            = errors.New("retrieved hash chain is invalid")
	errInvalidBody             = errors.New("retrieved block body is invalid")
	errCancelContentProcessing = errors.New("content processing canceled (requested)")
	errBadBlockFound           = errors.New("peer sent a bad block")
	errCanceled                = errors.New("syncing canceled (requested)")
	errNoSyncActive            = errors.New("no sync active")
	errTooOld                  = errors.New("peer's protocol version too old")
)

type Downloader struct {
	mode uint32         // Synchronisation mode defining the strategy used (per sync cycle), use d.getMode() to get the SyncMode
	mux  *event.TypeMux // Event multiplexer to announce sync operation events

	queue *queue   // Scheduler for selecting the hashes to download
	peers *peerSet // Set of active peers from which download can proceed

	stateDB ethdb.Database // Database to state sync into (and deduplicate via)

	// Statistics
	syncStatsChainOrigin uint64       // Origin block number where syncing started at
	syncStatsChainHeight uint64       // Highest block number known when syncing started
	syncStatsLock        sync.RWMutex // Lock protecting the sync stats fields

	core Core

	headEntropy *big.Int
	headNumber  uint64

	// Callbacks
	dropPeer peerDropFn // Drops a peer for misbehaving

	// Status
	synchroniseMock func(id string, hash common.Hash) error // Replacement for synchronise during testing
	synchronising   int32
	notified        int32
	committed       int32

	// Channels
	headerCh     chan dataPack        // Channel receiving inbound block headers
	bodyCh       chan dataPack        // Channel receiving inbound block bodies
	bodyWakeCh   chan bool            // Channel to signal the block body fetcher of new tasks
	headerProcCh chan []*types.Header // Channel to feed the header processor new tasks

	// Cancellation and termination
	cancelPeer string         // Identifier of the peer currently being used as the master (cancel on drop)
	cancelCh   chan struct{}  // Channel to cancel mid-flight syncs
	cancelLock sync.RWMutex   // Lock to protect the cancel channel and peer in delivers
	cancelWg   sync.WaitGroup // Make sure all fetcher goroutines have exited.

	quitCh   chan struct{} // Quit channel to signal termination
	quitLock sync.Mutex    // Lock to prevent double closes

	// Testing hooks
	syncInitHook    func(uint64, uint64)  // Method to call upon initiating a new sync run
	bodyFetchHook   func([]*types.Header) // Method to call upon starting a block body fetch
	chainInsertHook func([]*fetchResult)  // Method to call upon inserting a chain of blocks (possibly in multiple invocations)
}

// Core encapsulates functions required to sync a full core.
type Core interface {
	// HasBlock verifies a block's presence in the local chain.
	HasBlock(common.Hash, uint64) bool

	// GetBlockByHash retrieves a block from the local chain.
	GetBlockByHash(common.Hash) *types.Block

	// GetBlockByNumber retrieves a block from the local chain.
	GetBlockByNumber(uint64) *types.Block

	// CurrentHeader retrieves the head of local chain.
	CurrentHeader() *types.Header

	// CurrentLogEntropy returns the logarithm of the total entropy reduction since genesis for our current head block
	CurrentLogEntropy() *big.Int

	// TotalLogS() returns the total entropy reduction if the chain since genesis to the given header
	TotalLogS(header *types.Header) *big.Int

	// AddPendingEtxs adds the pendingEtxs to the database.
	AddPendingEtxs(pendingEtxs types.PendingEtxs) error

	// Snapshots returns the core snapshot tree to paused it during sync.
	Snapshots() *snapshot.Tree

	// Engine
	Engine() consensus.Engine

	// Write block to the database
	WriteBlock(block *types.Block)

	// GetTerminiByHash returns the termini of a given block
	GetTerminiByHash(hash common.Hash) *types.Termini

	// BadHashExistsInChain returns true if any of the specified bad hashes exists on chain
	BadHashExistsInChain() bool

	// IsBlockHashABadHash returns true if block hash exists in the bad hashes list
	IsBlockHashABadHash(hash common.Hash) bool
}

// New creates a new downloader to fetch hashes and blocks from remote peers.
func New(mux *event.TypeMux, core Core, dropPeer peerDropFn) *Downloader {
	dl := &Downloader{
		mux:          mux,
		queue:        newQueue(blockCacheMaxItems, blockCacheInitialItems),
		peers:        newPeerSet(),
		core:         core,
		headNumber:   core.CurrentHeader().NumberU64(),
		headEntropy:  core.CurrentLogEntropy(),
		dropPeer:     dropPeer,
		headerCh:     make(chan dataPack, 1),
		bodyCh:       make(chan dataPack, 1),
		bodyWakeCh:   make(chan bool, 1),
		headerProcCh: make(chan []*types.Header, 10),
		quitCh:       make(chan struct{}),
	}

	return dl
}

// Progress retrieves the synchronisation boundaries, specifically the origin
// block where synchronisation started at (may have failed/suspended); the block
// or header sync is currently at; and the latest known block which the sync targets.
//
// In addition, during the state download phase of fast synchronisation the number
// of processed and the total number of known states are also returned. Otherwise
// these are zero.
func (d *Downloader) Progress() quai.SyncProgress {
	// Lock the current stats and return the progress
	d.syncStatsLock.RLock()
	defer d.syncStatsLock.RUnlock()

	current := uint64(0)
	mode := d.getMode()
	switch {
	case d.core != nil && mode == FullSync:
		current = d.core.CurrentHeader().NumberU64()
	default:
		log.Error("Unknown downloader chain/mode combo", "light", "full", d.core != nil, "mode", mode)
	}
	return quai.SyncProgress{
		StartingBlock: d.syncStatsChainOrigin,
		CurrentBlock:  current,
		HighestBlock:  d.syncStatsChainHeight,
	}
}

// Synchronising returns whether the downloader is currently retrieving blocks.
func (d *Downloader) Synchronising() bool {
	return atomic.LoadInt32(&d.synchronising) > 0
}

// RegisterPeer injects a new download peer into the set of block source to be
// used for fetching hashes and blocks from.
func (d *Downloader) RegisterPeer(id string, version uint, peer Peer) error {
	logger := log.Log
	logger.Trace("Registering sync peer")
	if err := d.peers.Register(newPeerConnection(id, version, peer, logger)); err != nil {
		logger.Error("Failed to register sync peer", "err", err)
		return err
	}
	return nil
}

// HeadEntropy returns the downloader head entropy
func (d *Downloader) HeadEntropy() *big.Int {
	return d.headEntropy
}

// UnregisterPeer remove a peer from the known list, preventing any action from
// the specified peer. An effort is also made to return any pending fetches into
// the queue.
func (d *Downloader) UnregisterPeer(id string) error {
	// Unregister the peer from the active peer set and revoke any fetch tasks
	logger := log.Log
	logger.Trace("Unregistering sync peer")
	if err := d.peers.Unregister(id); err != nil {
		logger.Error("Failed to unregister sync peer", "err", err)
		return err
	}
	d.queue.Revoke(id)

	return nil
}

// Synchronise tries to sync up our local block chain with a remote peer, both
// adding various sanity checks as well as wrapping it with various log entries.
func (d *Downloader) Synchronise(id string, head common.Hash, entropy *big.Int, mode SyncMode) error {
	err := d.synchronise(id, head, entropy, mode)
	switch err {
	case nil, errBusy, errCanceled, errNoFetchesPending:
		return err
	}
	if errors.Is(err, errInvalidChain) || errors.Is(err, errBadPeer) || errors.Is(err, errTimeout) ||
		errors.Is(err, errStallingPeer) || errors.Is(err, errUnsyncedPeer) || errors.Is(err, errEmptyHeaderSet) ||
		errors.Is(err, errPeersUnavailable) || errors.Is(err, errTooOld) || errors.Is(err, errInvalidAncestor) || errors.Is(err, errBadBlockFound) {
		log.Warn("Synchronisation failed, dropping peer", "peer", id, "err", err)
		if d.dropPeer == nil {
			// The dropPeer method is nil when `--copydb` is used for a local copy.
			// Timeouts can occur if e.g. compaction hits at the wrong time, and can be ignored
			log.Warn("Downloader wants to drop peer, but peerdrop-function is not set", "peer", id)
		} else {
			d.dropPeer(id)
		}
		return err
	}
	log.Warn("Synchronisation failed, retrying", "err", err)
	return err
}

// PeerSet retrieves the current peer set of the downloader.
func (d *Downloader) PeerSet() *peerSet {
	return d.peers
}

// synchronise will select the peer and use it for synchronising. If an empty string is given
// it will use the best peer possible and synchronize if its number is higher than our own. If any of the
// checks fail an error will be returned. This method is synchronous
func (d *Downloader) synchronise(id string, hash common.Hash, entropy *big.Int, mode SyncMode) error {
	// Mock out the synchronisation if testing
	if d.synchroniseMock != nil {
		return d.synchroniseMock(id, hash)
	}
	// Make sure only one goroutine is ever allowed past this point at once
	if !atomic.CompareAndSwapInt32(&d.synchronising, 0, 1) {
		return errBusy
	}
	defer atomic.StoreInt32(&d.synchronising, 0)

	// Post a user notification of the sync (only once per session)
	if atomic.CompareAndSwapInt32(&d.notified, 0, 1) {
		log.Info("Block synchronisation started")
	}

	// Reset the queue, peer set and wake channels to clean any internal leftover state
	d.queue.Reset(blockCacheMaxItems, blockCacheInitialItems)
	d.peers.Reset()

	for _, ch := range []chan bool{d.bodyWakeCh} {
		select {
		case <-ch:
		default:
		}
	}
	for _, ch := range []chan dataPack{d.headerCh, d.bodyCh} {
		for empty := false; !empty; {
			select {
			case <-ch:
			default:
				empty = true
			}
		}
	}
	for empty := false; !empty; {
		select {
		case <-d.headerProcCh:
		default:
			empty = true
		}
	}
	// Create cancel channel for aborting mid-flight and mark the master peer
	d.cancelLock.Lock()
	d.cancelCh = make(chan struct{})
	d.cancelPeer = id
	d.cancelLock.Unlock()

	defer d.Cancel() // No matter what, we can't leave the cancel channel open

	// Atomically set the requested sync mode
	atomic.StoreUint32(&d.mode, uint32(mode))

	// Retrieve the origin peer and initiate the downloading process
	p := d.peers.Peer(id)
	if p == nil {
		return errUnknownPeer
	}

	// If the peer entropy is lower than the downloader head entropy
	if d.headEntropy.Cmp(entropy) >= 0 {
		return nil
	}

	// Only start the downloader after we reset from a forked state
	if d.core.BadHashExistsInChain() {
		log.Warn("Bad Hashes still exist on chain, cannot start the downloader yet")
		return nil
	}
	return d.syncWithPeer(p, hash, entropy)
}

func (d *Downloader) getMode() SyncMode {
	return SyncMode(atomic.LoadUint32(&d.mode))
}

// syncWithPeer starts a block synchronization based on the hash chain from the
// specified peer and head hash.
func (d *Downloader) syncWithPeer(p *peerConnection, hash common.Hash, entropy *big.Int) (err error) {
	d.mux.Post(StartEvent{})
	defer func() {
		// reset on error
		if err != nil {
			d.mux.Post(FailedEvent{err})
		} else {
			latest := d.core.CurrentHeader()
			d.mux.Post(DoneEvent{latest})
		}
	}()
	if p.version < eth.QUAI1 {
		return fmt.Errorf("%w: advertized %d < required %d", errTooOld, p.version, eth.QUAI1)
	}
	mode := d.getMode()

	log.Info("Synchronising with the network", "peer", p.id, "eth", p.version, "head", hash, "entropy", entropy, "mode", mode)
	defer func(start time.Time) {
		log.Debug("Synchronisation terminated", "elapsed", common.PrettyDuration(time.Since(start)))
	}(time.Now())

	// Get the latest head of the peer to start the sync from.
	latest, err := d.fetchHead(p)
	if err != nil {
		return err
	}

	// Height of the peer
	peerHeight := latest.Number().Uint64()
	origin := peerHeight

	// TODO: display the correct sync stats
	d.syncStatsLock.Lock()
	if d.syncStatsChainHeight <= origin || d.syncStatsChainOrigin > origin {
		d.syncStatsChainOrigin = origin
	}
	d.syncStatsChainHeight = peerHeight
	d.syncStatsLock.Unlock()

	d.committed = 1

	// Initiate the sync using a concurrent header and content retrieval algorithm
	if d.syncInitHook != nil {
		d.syncInitHook(origin, peerHeight)
	}
	fetchers := []func() error{
		func() error { return d.fetchHeaders(p, origin) }, // Headers are always retrieved
		func() error { return d.fetchBodies(origin) },     // Bodies are retrieved during normal and fast sync
		func() error { return d.processHeaders(origin) },
		func() error { return d.processFullSyncContent(peerHeight) },
	}
	return d.spawnSync(fetchers)
}

// spawnSync runs d.process and all given fetcher functions to completion in
// separate goroutines, returning the first error that appears.
func (d *Downloader) spawnSync(fetchers []func() error) error {
	errc := make(chan error, len(fetchers))
	d.cancelWg.Add(len(fetchers))
	for _, fn := range fetchers {
		fn := fn
		go func() { defer d.cancelWg.Done(); errc <- fn() }()
	}
	// Wait for the first error, then terminate the others.
	var err error
	for i := 0; i < len(fetchers); i++ {
		if i == len(fetchers)-1 {
			// Close the queue when all fetchers have exited.
			// This will cause the block processor to end when
			// it has processed the queue.
			d.queue.Close()
		}
		if err = <-errc; err != nil && err != errCanceled {
			break
		}
	}
	d.queue.Close()
	d.Cancel()
	return err
}

// cancel aborts all of the operations and resets the queue. However, cancel does
// not wait for the running download goroutines to finish. This method should be
// used when cancelling the downloads from inside the downloader.
func (d *Downloader) cancel() {
	// Close the current cancel channel
	d.cancelLock.Lock()
	defer d.cancelLock.Unlock()

	if d.cancelCh != nil {
		select {
		case <-d.cancelCh:
			// Channel was already closed
		default:
			close(d.cancelCh)
		}
	}
}

// Cancel aborts all of the operations and waits for all download goroutines to
// finish before returning.
func (d *Downloader) Cancel() {
	d.cancel()
	d.cancelWg.Wait()
}

// Terminate interrupts the downloader, canceling all pending operations.
// The downloader cannot be reused after calling Terminate.
func (d *Downloader) Terminate() {
	// Close the termination channel (make sure double close is allowed)
	d.quitLock.Lock()
	select {
	case <-d.quitCh:
	default:
		close(d.quitCh)
	}

	d.quitLock.Unlock()

	// Cancel any pending download requests
	d.Cancel()
}

// fetchHead retrieves the head header from a remote peer.
func (d *Downloader) fetchHead(p *peerConnection) (head *types.Header, err error) {
	p.log.Debug("Retrieving remote chain head")

	// Request the advertised remote head block and wait for the response
	latest, _, _, _ := p.peer.Head()
	fetch := 1
	go p.peer.RequestHeadersByHash(latest, fetch, uint64(1), false, true)

	ttl := d.peers.rates.TargetTimeout()
	timeout := time.After(ttl)
	for {
		select {
		case <-d.cancelCh:
			return nil, errCanceled

		case packet := <-d.headerCh:
			// Discard anything not from the origin peer
			if packet.PeerId() != p.id {
				log.Debug("Received headers from incorrect peer", "peer", packet.PeerId())
				break
			}
			// Make sure the peer gave us at least one and at most the requested headers
			headers := packet.(*headerPack).headers
			if len(headers) == 0 || len(headers) > fetch {
				return nil, fmt.Errorf("%w: returned headers %d != requested %d", errBadPeer, len(headers), fetch)
			}
			// The first header needs to be the head, validate against the checkpoint
			// and request.
			head := headers[0]
			if len(headers) == 1 {
				p.log.Debug("Remote head identified", "number", head.Number(), "hash", head.Hash())
				return head, nil
			}
			return head, nil

		case <-timeout:
			p.log.Debug("Waiting for head header timed out", "elapsed", ttl)
			return nil, errTimeout

		case <-d.bodyCh:
		}
	}
}

// fetchHeaders keeps retrieving headers concurrently from the number
// requested, until no more are returned, potentially throttling on the way. To
// facilitate concurrency but still protect against malicious nodes sending bad
// headers, we construct a header chain skeleton using the "origin" peer we are
// syncing with, and fill in the missing headers using anyone else. Headers from
// other peers are only accepted if they map cleanly to the skeleton. If no one
// can fill in the skeleton - not even the origin peer - it's assumed invalid and
// the origin is dropped.
func (d *Downloader) fetchHeaders(p *peerConnection, from uint64) error {
	p.log.Debug("Directing header downloads", "origin", from)
	defer p.log.Debug("Header download terminated")

	// Create a timeout timer, and the associated header fetcher
	skeleton := true
	skeletonHeaders := make([]*types.Header, 0)
	request := time.Now()       // time of the last skeleton fetch request
	timeout := time.NewTimer(0) // timer to dump a non-responsive active peer
	<-timeout.C                 // timeout channel should be initially empty
	defer timeout.Stop()

	// peer height
	peerHeight := from
	nodeCtx := common.NodeLocation.Context()

	localHeight := d.headNumber

	// getFetchPoint returns the next fetch point given the number of headers processed
	// after the previous point.
	updateFetchPoint := func() {
		if localHeight+uint64(MaxSkeletonWindow) < peerHeight {
			from = localHeight + uint64(MaxSkeletonWindow)
		} else {
			from = peerHeight
		}
	}

	var ttl time.Duration
	getHeaders := func(from uint64, to uint64) {
		request = time.Now()

		if skeleton {
			timeout.Reset(1 * time.Minute)
		} else {
			ttl = d.peers.rates.TargetTimeout()
			timeout.Reset(ttl)
		}

		if skeleton {
			// Reset the skeleton headers each time we try to fetch the skeleton.
			skeletonHeaders = make([]*types.Header, 0)
			p.log.Trace("Fetching skeleton headers", "count", MaxHeaderFetch, "from", from)
			if nodeCtx == common.PRIME_CTX {
				go p.peer.RequestHeadersByNumber(from, MaxSkeletonSize, uint64(PrimeSkeletonDist), to, false, true)
			} else {
				go p.peer.RequestHeadersByNumber(from, MaxSkeletonSize, uint64(1), to, true, true)
			}
		} else {
			p.log.Trace("Fetching full headers", "count", MaxHeaderFetch, "from", from)
			go p.peer.RequestHeadersByNumber(from, MaxHeaderFetch, uint64(1), to, false, true)
		}
	}

	// In the case of prime there is no guarantee that during the sync backwards
	// the prime blocks will match. To be tolerant to reorgs and forking, we need
	// to fetch till certain depth. For now we will be syncing till more than 50 prime
	// blocks back.
	updateFetchPoint()
	if nodeCtx == common.PRIME_CTX {
		if localHeight > uint64(PrimeFetchDepth) {
			getHeaders(from, localHeight-uint64(PrimeFetchDepth))
		} else {
			getHeaders(from, 0)
		}
	} else if nodeCtx == common.REGION_CTX {
		if localHeight > uint64(RegionFetchDepth) {
			getHeaders(from, localHeight-uint64(RegionFetchDepth))
		} else {
			getHeaders(from, 0)
		}
	} else {
		if localHeight > uint64(ZoneFetchDepth) {
			getHeaders(from, localHeight-uint64(ZoneFetchDepth))
		} else {
			getHeaders(from, 0)
		}
	}

	first := true

	for {
		select {
		case <-d.cancelCh:
			return errCanceled

		case packet := <-d.headerCh:

			// Make sure the active peer is giving us the skeleton headers
			if packet.PeerId() != p.id {
				log.Debug("Received skeleton from incorrect peer", "peer", packet.PeerId())
				break
			}
			headerReqTimer.UpdateSince(request)
			timeout.Stop()

			headers := packet.(*headerPack).headers

			if skeleton {
				// Only fill the skeleton between the headers we don't know about.
				for i := 0; i < len(headers); i++ {
					skeletonHeaders = append(skeletonHeaders, headers[i])
					commonAncestor := d.core.HasBlock(headers[i].Hash(), headers[i].NumberU64()) && (d.core.GetTerminiByHash(headers[i].Hash()) != nil)
					if commonAncestor {
						break
					}
				}
			}

			if len(skeletonHeaders) > 0 && skeletonHeaders[len(skeletonHeaders)-1].NumberU64() < 8 {
				genesisBlock := d.core.GetBlockByNumber(0)
				skeletonHeaders = append(skeletonHeaders, genesisBlock.Header())
			}

			if len(headers) == 0 {
				continue
			}

			// Prepare the resultStore to fill the skeleton.
			// first bool is used to only set the offset on the first skeleton fetch.
			if len(skeletonHeaders) > 0 && first {
				d.queue.Prepare(skeletonHeaders[len(skeletonHeaders)-1].NumberU64(), FullSync)
				first = false
			}

			// If the skeleton's finished, pull any remaining head headers directly from the origin
			// When the length of skeleton Headers is zero or one, thre is no more skeleton to fetch.
			// If we are at the tail fetch directly from the peer height.
			if skeleton && len(skeletonHeaders) == 1 {
				skeleton = false
				// get the headers directly from peer height
				getHeaders(peerHeight, skeletonHeaders[0].NumberU64())
				continue
			}

			var progressed bool
			// If we received a skeleton batch, resolve internals concurrently
			if skeleton {
				// trim on common, set origin to last entry in the skeleton
				filled, proced, err := d.fillHeaderSkeleton(from, skeletonHeaders)
				if err != nil {
					p.log.Info("Skeleton chain invalid", "err", err)
					return fmt.Errorf("%w: %v", errInvalidChain, err)
				}
				headers = filled[proced:]
				localHeight = skeletonHeaders[0].NumberU64()

				progressed = proced > 0
				updateFetchPoint()
				getHeaders(from, localHeight)
			}

			// Insert all the new headers and fetch the next batch
			// This is only used for getting the tail for prime, region and zone.
			if len(headers) > 0 && !skeleton {
				p.log.Trace("Scheduling new headers", "count", len(headers), "from", from)
				select {
				case d.headerProcCh <- headers:
				case <-d.cancelCh:
					return errCanceled
				}

				if localHeight >= peerHeight {
					continue
				}
			}

			if len(headers) == 0 && !progressed {
				// No headers delivered, or all of them being delayed, sleep a bit and retry
				p.log.Trace("All headers delayed, waiting")
				select {
				case <-time.After(fsHeaderContCheck):
					updateFetchPoint()
					getHeaders(from, localHeight)
					continue
				case <-d.cancelCh:
					return errCanceled
				}
			}

		case <-timeout.C:
			if d.dropPeer == nil {
				// The dropPeer method is nil when `--copydb` is used for a local copy.
				// Timeouts can occur if e.g. compaction hits at the wrong time, and can be ignored
				p.log.Warn("Downloader wants to drop peer, but peerdrop-function is not set", "peer", p.id)
				break
			}
			// Header retrieval timed out, consider the peer bad and drop
			p.log.Debug("Header request timed out", "elapsed", ttl)
			headerTimeoutMeter.Mark(1)
			d.dropPeer(p.id)

			// Finish the sync gracefully instead of dumping the gathered data though
			for _, ch := range []chan bool{d.bodyWakeCh} {
				select {
				case ch <- false:
				case <-d.cancelCh:
				}
			}
			select {
			case d.headerProcCh <- nil:
			case <-d.cancelCh:
			}
			return fmt.Errorf("%w: header request timed out", errBadPeer)
		}
	}
}

// fillHeaderSkeleton concurrently retrieves headers from all our available peers
// and maps them to the provided skeleton header chain.
//
// Any partial results from the beginning of the skeleton is (if possible) forwarded
// immediately to the header processor to keep the rest of the pipeline full even
// in the case of header stalls.
//
// The method returns the entire filled skeleton and also the number of headers
// already forwarded for processing.
func (d *Downloader) fillHeaderSkeleton(from uint64, skeleton []*types.Header) ([]*types.Header, int, error) {
	log.Debug("Filling up skeleton", "from", from)
	d.queue.ScheduleSkeleton(from, skeleton)

	var (
		deliver = func(packet dataPack) (int, error) {
			pack := packet.(*headerPack)
			return d.queue.DeliverHeaders(pack.peerID, pack.headers, d.headerProcCh)
		}
		expire  = func() map[string]int { return d.queue.ExpireHeaders(d.peers.rates.TargetTimeout()) }
		reserve = func(p *peerConnection, count int) (*fetchRequest, bool, bool) {
			return d.queue.ReserveHeaders(p, count), false, false
		}
		fetch = func(p *peerConnection, req *fetchRequest) error {
			return p.FetchHeaders(req.From, int(req.From-req.To))
		}
		capacity = func(p *peerConnection) int { return p.HeaderCapacity(d.peers.rates.TargetRoundTrip()) }
		setIdle  = func(p *peerConnection, accepted int, deliveryTime time.Time) {
			p.SetHeadersIdle(accepted, deliveryTime)
		}
	)
	err := d.fetchParts(d.headerCh, deliver, d.queue.headerContCh, expire,
		d.queue.PendingHeaders, d.queue.InFlightHeaders, reserve,
		nil, fetch, d.queue.CancelHeaders, capacity, d.peers.HeaderIdlePeers, setIdle, "headers")

	log.Debug("Skeleton fill terminated", "err", err)

	filled, proced := d.queue.RetrieveHeaders()
	return filled, proced, err
}

// fetchBodies iteratively downloads the scheduled block bodies, taking any
// available peers, reserving a chunk of blocks for each, waiting for delivery
// and also periodically checking for timeouts.
func (d *Downloader) fetchBodies(from uint64) error {
	log.Debug("Downloading block bodies", "origin", from)

	var (
		deliver = func(packet dataPack) (int, error) {
			pack := packet.(*bodyPack)
			return d.queue.DeliverBodies(pack.peerID, pack.transactions, pack.uncles, pack.extTransactions, pack.manifest)
		}
		expire   = func() map[string]int { return d.queue.ExpireBodies(d.peers.rates.TargetTimeout()) }
		fetch    = func(p *peerConnection, req *fetchRequest) error { return p.FetchBodies(req) }
		capacity = func(p *peerConnection) int { return p.BlockCapacity(d.peers.rates.TargetRoundTrip()) }
		setIdle  = func(p *peerConnection, accepted int, deliveryTime time.Time) { p.SetBodiesIdle(accepted, deliveryTime) }
	)
	err := d.fetchParts(d.bodyCh, deliver, d.bodyWakeCh, expire,
		d.queue.PendingBlocks, d.queue.InFlightBlocks, d.queue.ReserveBodies,
		d.bodyFetchHook, fetch, d.queue.CancelBodies, capacity, d.peers.BodyIdlePeers, setIdle, "bodies")

	log.Debug("Block body download terminated", "err", err)
	return err
}

// fetchParts iteratively downloads scheduled block parts, taking any available
// peers, reserving a chunk of fetch requests for each, waiting for delivery and
// also periodically checking for timeouts.
//
// As the scheduling/timeout logic mostly is the same for all downloaded data
// types, this method is used by each for data gathering and is instrumented with
// various callbacks to handle the slight differences between processing them.
//
// The instrumentation parameters:
//   - errCancel:   error type to return if the fetch operation is cancelled (mostly makes logging nicer)
//   - deliveryCh:  channel from which to retrieve downloaded data packets (merged from all concurrent peers)
//   - deliver:     processing callback to deliver data packets into type specific download queues (usually within `queue`)
//   - wakeCh:      notification channel for waking the fetcher when new tasks are available (or sync completed)
//   - expire:      task callback method to abort requests that took too long and return the faulty peers (traffic shaping)
//   - pending:     task callback for the number of requests still needing download (detect completion/non-completability)
//   - inFlight:    task callback for the number of in-progress requests (wait for all active downloads to finish)
//   - throttle:    task callback to check if the processing queue is full and activate throttling (bound memory use)
//   - reserve:     task callback to reserve new download tasks to a particular peer (also signals partial completions)
//   - fetchHook:   tester callback to notify of new tasks being initiated (allows testing the scheduling logic)
//   - fetch:       network callback to actually send a particular download request to a physical remote peer
//   - cancel:      task callback to abort an in-flight download request and allow rescheduling it (in case of lost peer)
//   - capacity:    network callback to retrieve the estimated type-specific bandwidth capacity of a peer (traffic shaping)
//   - idle:        network callback to retrieve the currently (type specific) idle peers that can be assigned tasks
//   - setIdle:     network callback to set a peer back to idle and update its estimated capacity (traffic shaping)
//   - kind:        textual label of the type being downloaded to display in log messages
func (d *Downloader) fetchParts(deliveryCh chan dataPack, deliver func(dataPack) (int, error), wakeCh chan bool,
	expire func() map[string]int, pending func() int, inFlight func() bool, reserve func(*peerConnection, int) (*fetchRequest, bool, bool),
	fetchHook func([]*types.Header), fetch func(*peerConnection, *fetchRequest) error, cancel func(*fetchRequest), capacity func(*peerConnection) int,
	idle func() ([]*peerConnection, int), setIdle func(*peerConnection, int, time.Time), kind string) error {

	// Create a ticker to detect expired retrieval tasks
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	update := make(chan struct{}, 1)

	// Prepare the queue and fetch block parts until the block header fetcher's done
	finished := false
	for {
		select {
		case <-d.cancelCh:
			return errCanceled

		case packet := <-deliveryCh:
			deliveryTime := time.Now()
			// If the peer was previously banned and failed to deliver its pack
			// in a reasonable time frame, ignore its message.
			if peer := d.peers.Peer(packet.PeerId()); peer != nil {
				// Deliver the received chunk of data and check chain validity
				accepted, err := deliver(packet)
				if errors.Is(err, errInvalidChain) {
					return err
				}
				// Unless a peer delivered something completely else than requested (usually
				// caused by a timed out request which came through in the end), set it to
				// idle. If the delivery's stale, the peer should have already been idled.
				if !errors.Is(err, errStaleDelivery) {
					setIdle(peer, accepted, deliveryTime)
				}
				// Issue a log to the user to see what's going on
				switch {
				case err == nil && packet.Items() == 0:
					peer.log.Trace("Requested data not delivered", "type", kind)
				case err == nil:
					peer.log.Trace("Delivered new batch of data", "type", kind, "count", packet.Stats())
				default:
					peer.log.Debug("Failed to deliver retrieved data", "type", kind, "err", err)
				}
			}
			// Blocks assembled, try to update the progress
			select {
			case update <- struct{}{}:
			default:
			}

		case cont := <-wakeCh:
			// The header fetcher sent a continuation flag, check if it's done
			if !cont {
				finished = true
			}
			// Headers arrive, try to update the progress
			select {
			case update <- struct{}{}:
			default:
			}

		case <-ticker.C:
			// Sanity check update the progress
			select {
			case update <- struct{}{}:
			default:
			}

		case <-update:
			// Short circuit if we lost all our peers
			if d.peers.Len() == 0 {
				return errNoPeers
			}
			// Check for fetch request timeouts and demote the responsible peers
			for pid, fails := range expire() {
				if peer := d.peers.Peer(pid); peer != nil {
					// If a lot of retrieval elements expired, we might have overestimated the remote peer or perhaps
					// ourselves. Only reset to minimal throughput but don't drop just yet. If even the minimal times
					// out that sync wise we need to get rid of the peer.
					//
					// The reason the minimum threshold is 2 is because the downloader tries to estimate the bandwidth
					// and latency of a peer separately, which requires pushing the measures capacity a bit and seeing
					// how response times reacts, to it always requests one more than the minimum (i.e. min 2).
					if fails > 2 {
						peer.log.Trace("Data delivery timed out", "type", kind)
						setIdle(peer, 0, time.Now())
					} else {
						peer.log.Debug("Stalling delivery, dropping", "type", kind)

						if d.dropPeer == nil {
							// The dropPeer method is nil when `--copydb` is used for a local copy.
							// Timeouts can occur if e.g. compaction hits at the wrong time, and can be ignored
							peer.log.Warn("Downloader wants to drop peer, but peerdrop-function is not set", "peer", pid)
						} else {
							d.dropPeer(pid)

							// If this peer was the master peer, abort sync immediately
							d.cancelLock.RLock()
							master := pid == d.cancelPeer
							d.cancelLock.RUnlock()

							if master {
								d.cancel()
								return errTimeout
							}
						}
					}
				}
			}
			// If there's nothing more to fetch, wait or terminate
			if pending() == 0 {
				if !inFlight() && finished {
					log.Debug("Data fetching completed", "type", kind)
					return nil
				}
				break
			}
			// Send a download request to all idle peers, until throttled
			progressed, throttled, running := false, false, inFlight()
			idles, total := idle()
			pendCount := pending()
			for _, peer := range idles {
				// Short circuit if throttling activated
				if throttled {
					break
				}
				// Short circuit if there is no more available task.
				if pendCount = pending(); pendCount == 0 {
					break
				}
				// Reserve a chunk of fetches for a peer. A nil can mean either that
				// no more headers are available, or that the peer is known not to
				// have them.
				request, progress, throttle := reserve(peer, capacity(peer))
				if progress {
					progressed = true
				}
				if throttle {
					throttled = true
					throttleCounter.Inc(1)
				}
				if request == nil {
					continue
				}
				if request.From > 0 {
					peer.log.Trace("Requesting new batch of data", "type", kind, "from", request.From)
				} else {
					if len(request.Headers) != 0 {
						peer.log.Trace("Requesting new batch of data", "type", kind, "count", len(request.Headers), "from", request.Headers[0].Number())
					}
				}
				// Fetch the chunk and make sure any errors return the hashes to the queue
				if fetchHook != nil {
					fetchHook(request.Headers)
				}
				if err := fetch(peer, request); err != nil {
					// Although we could try and make an attempt to fix this, this error really
					// means that we've double allocated a fetch task to a peer. If that is the
					// case, the internal state of the downloader and the queue is very wrong so
					// better hard crash and note the error instead of silently accumulating into
					// a much bigger issue.
					panic(fmt.Sprintf("%v: %s fetch assignment failed", peer, kind))
				}
				running = true
			}
			// Make sure that we have peers available for fetching. If all peers have been tried
			// and all failed throw an error
			if !progressed && !throttled && !running && len(idles) == total && pendCount > 0 {
				return errPeersUnavailable
			}
		}
	}
}

// processHeaders takes batches of retrieved headers from an input channel and
// keeps processing and scheduling them into the header chain and downloader's
// queue until the stream ends or a failure occurs.
func (d *Downloader) processHeaders(origin uint64) error {
	// Keep a count of uncertain headers to roll back
	var (
		rollback    uint64 // Zero means no rollback (fine as you can't unroll the genesis)
		rollbackErr error
		mode        = d.getMode()
	)
	defer func() {
		if rollback > 0 {
			curBlock := d.core.CurrentHeader().NumberU64()
			log.Warn("Rolled back chain segment",
				"block", fmt.Sprintf("%d->%d", curBlock), "reason", rollbackErr)
		}
	}()
	// Wait for batches of headers to process

	for {
		select {
		case <-d.cancelCh:
			rollbackErr = errCanceled
			return errCanceled

		case headers := <-d.headerProcCh:
			// Terminate header processing if we synced up
			if len(headers) == 0 {
				// Notify everyone that headers are fully processed
				for _, ch := range []chan bool{d.bodyWakeCh} {
					select {
					case ch <- false:
					case <-d.cancelCh:
					}
				}
				// If no headers were retrieved at all, the peer violated its TD promise that it had a
				// better chain compared to ours. The only exception is if its promised blocks were
				// already imported by other means (e.g. fetcher):
				//
				// R <remote peer>, L <local node>: Both at block 10
				// R: Mine block 11, and propagate it to L
				// L: Queue block 11 for import
				// L: Notice that R's head and TD increased compared to ours, start sync
				// L: Import of block 11 finishes
				// L: Sync begins, and finds common ancestor at 11
				// L: Request new headers up from 11 (R's number was higher, it must have something)
				// R: Nothing to give
				// Disable any rollback and return
				rollback = 0
				return nil
			}
			// Otherwise split the chunk of headers into batches and process them
			for len(headers) > 0 {
				// Terminate if something failed in between processing chunks
				select {
				case <-d.cancelCh:
					rollbackErr = errCanceled
					return errCanceled
				default:
				}
				// Select the next chunk of headers to import
				limit := maxHeadersProcess
				if limit > len(headers) {
					limit = len(headers)
				}
				chunk := headers[:limit]

				// Unless we're doing light chains, schedule the headers for associated content retrieval
				if mode == FullSync {
					// If we've reached the allowed number of pending headers, stall a bit
					for d.queue.PendingBlocks() >= maxQueuedHeaders {
						select {
						case <-d.cancelCh:
							rollbackErr = errCanceled
							return errCanceled
						case <-time.After(time.Second):
						}
					}
					// Otherwise insert the headers for content retrieval
					inserts := d.queue.Schedule(chunk)
					if len(inserts) != len(chunk) {
						rollbackErr = fmt.Errorf("stale headers: len inserts %v len(chunk) %v", len(inserts), len(chunk))
						return fmt.Errorf("%w: stale headers", errBadPeer)
					}
				}
				headers = headers[limit:]
				origin += uint64(limit)
			}
			// Update the highest block number we know if a higher one is found.
			d.syncStatsLock.Lock()
			if d.syncStatsChainHeight < origin {
				d.syncStatsChainHeight = origin - 1
			}
			d.syncStatsLock.Unlock()

			// Signal the content downloaders of the availablility of new tasks
			for _, ch := range []chan bool{d.bodyWakeCh} {
				select {
				case ch <- true:
				default:
				}
			}
		}
	}
}

// processFullSyncContent takes fetch results from the queue and imports them into the chain.
func (d *Downloader) processFullSyncContent(peerHeight uint64) error {
	for {
		select {
		case <-d.cancelCh:
			return nil
		default:
			results := d.queue.Results(true)
			if len(results) == 0 {
				return nil
			}
			if err := d.importBlockResults(results); err != nil {
				return err
			}
			// If all the blocks are fetched, we exit the sync process
			if d.headNumber == peerHeight {
				return errNoFetchesPending
			}
		}
	}
}

func (d *Downloader) importBlockResults(results []*fetchResult) error {
	// Check for any early termination requests
	if len(results) == 0 {
		return nil
	}
	select {
	case <-d.quitCh:
		return errCancelContentProcessing
	case <-d.cancelCh:
		return errCancelContentProcessing
	default:
	}
	// Retrieve the a batch of results to import
	first, last := results[0].Header, results[len(results)-1].Header
	log.Info("Inserting downloaded chain", "items", len(results),
		"firstnum", first.Number(), "firsthash", first.Hash(),
		"lastnum", last.Number(), "lasthash", last.Hash(),
	)

	for _, result := range results {
		block := types.NewBlockWithHeader(result.Header).WithBody(result.Transactions, result.Uncles, result.ExtTransactions, result.SubManifest)
		if d.core.IsBlockHashABadHash(block.Hash()) {
			return errBadBlockFound
		}
		d.headNumber = block.NumberU64()
		d.headEntropy = d.core.TotalLogS(block.Header())
		d.core.WriteBlock(block)
	}
	return nil
}

// DeliverHeaders injects a new batch of block headers received from a remote
// node into the download schedule.
func (d *Downloader) DeliverHeaders(id string, headers []*types.Header) error {
	return d.deliver(d.headerCh, &headerPack{id, headers}, headerInMeter, headerDropMeter)
}

// DeliverBodies injects a new batch of block bodies received from a remote node.
func (d *Downloader) DeliverBodies(id string, transactions [][]*types.Transaction, uncles [][]*types.Header, extTransactions [][]*types.Transaction, manifests []types.BlockManifest) error {
	return d.deliver(d.bodyCh, &bodyPack{id, transactions, uncles, extTransactions, manifests}, bodyInMeter, bodyDropMeter)
}

// deliver injects a new batch of data received from a remote node.
func (d *Downloader) deliver(destCh chan dataPack, packet dataPack, inMeter, dropMeter metrics.Meter) (err error) {
	// Update the delivery metrics for both good and failed deliveries
	inMeter.Mark(int64(packet.Items()))
	defer func() {
		if err != nil {
			dropMeter.Mark(int64(packet.Items()))
		}
	}()
	// Deliver or abort if the sync is canceled while queuing
	d.cancelLock.RLock()
	cancel := d.cancelCh
	d.cancelLock.RUnlock()
	if cancel == nil {
		return errNoSyncActive
	}
	select {
	case destCh <- packet:
		return nil
	case <-cancel:
		return errNoSyncActive
	}
}

// ValidateEntropyBroadcast validates the entropy broadcast of a block.
func (d *Downloader) ValidateEntropyBroadcast(block *types.Block, syncEntropy *big.Int, threshold *big.Int, peer *eth.Peer) error {
	window := new(big.Int).Mul(threshold, big.NewInt(fetcher.MaxAllowableEntropyDist))
	depth := new(big.Int).Add(block.ParentEntropy(), window)
	// log.Info("Validating entropy broadcast", "block", block.Hash(), "peer", peer.ID(), "depth", depth, "syncEntropy", syncEntropy)
	if depth.Cmp(syncEntropy) < 0 {
		d.dropPeer(peer.ID())
		return fmt.Errorf("entropy broadcast is too old")
	}
	return nil
}

func (d *Downloader) DropPeer(peer *eth.Peer) {
	d.dropPeer(peer.ID())
}
