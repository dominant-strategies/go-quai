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

package eth

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/forkid"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/eth/downloader"
	"github.com/dominant-strategies/go-quai/eth/fetcher"
	"github.com/dominant-strategies/go-quai/eth/protocols/eth"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	lru "github.com/hashicorp/golang-lru"
)

const (
	// txChanSize is the size of channel listening to NewTxsEvent.
	// The number is referenced from the size of tx pool.
	txChanSize = 4096

	// c_pendingEtxBroadcastChanSize is the size of channel listening to pEtx Event.
	c_pendingEtxBroadcastChanSize = 10

	// c_missingPendingEtxsRollupChanSize is the size of channel listening to missing pEtxsRollup Event.
	c_missingPendingEtxsRollupChanSize = 10

	// c_pendingEtxRollupBroadcastChanSize is the size of channel listening to pEtx rollup Event.
	c_pendingEtxRollupBroadcastChanSize = 10

	// missingPendingEtxsChanSize is the size of channel listening to the MissingPendingEtxsEvent
	missingPendingEtxsChanSize = 10

	// missingParentChanSize is the size of channel listening to the MissingParentEvent
	missingParentChanSize = 10

	// minPeerSend is the threshold for sending the block updates. If
	// sqrt of len(peers) is less than 5 we make the block announcement
	// to as much as minPeerSend peers otherwise send it to sqrt of len(peers).
	minPeerSend = 5

	// minPeerRequest is the threshold for requesting the body. If
	// sqrt of len(peers) is less than minPeerRequest we make the body request
	// to as much as minPeerSend peers otherwise send it to sqrt of len(peers).
	minPeerRequest = 3

	// minPeerSendTx is the minimum number of peers that will receive a new transaction.
	minPeerSendTx = 2

	// c_broadcastCacheSize is the Max number of broadcast block hashes to be kept for Logging
	c_broadcastCacheSize = 10
)

// txPool defines the methods needed from a transaction pool implementation to
// support all the operations needed by the Quai chain protocols.
type txPool interface {
	// Has returns an indicator whether txpool has a transaction
	// cached with the given hash.
	Has(hash common.Hash) bool

	// Get retrieves the transaction from local txpool with given
	// tx hash.
	Get(hash common.Hash) *types.Transaction

	// AddRemotes should add the given transactions to the pool.
	AddRemotes([]*types.Transaction) []error

	// Pending should return pending transactions.
	// The slice should be modifiable by the caller.
	TxPoolPending(enforceTips bool, etxSet types.EtxSet) (map[common.AddressBytes]types.Transactions, error)

	// SubscribeNewTxsEvent should return an event subscription of
	// NewTxsEvent and send events to the given channel.
	SubscribeNewTxsEvent(chan<- core.NewTxsEvent) event.Subscription
}

// handlerConfig is the collection of initialization parameters to create a full
// node network handler.
type handlerConfig struct {
	Database      ethdb.Database         // Database for direct sync insertions
	Core          *core.Core             // Core to serve data from
	TxPool        txPool                 // Transaction pool to propagate from
	Network       uint64                 // Network identifier to adfvertise
	Sync          downloader.SyncMode    // Whether to fast or full sync
	BloomCache    uint64                 // Megabytes to alloc for fast sync bloom
	EventMux      *event.TypeMux         // Legacy event mux, deprecate for `feed`
	Whitelist     map[uint64]common.Hash // Hard coded whitelist for sync challenged
	SlicesRunning []common.Location      // Slices run by the node
}

type handler struct {
	networkID     uint64
	forkFilter    forkid.Filter     // Fork ID filter, constant across the lifetime of the node
	slicesRunning []common.Location // Slices running on the node

	acceptTxs uint32 // Flag whether we're considered synchronised (enables transaction processing)

	database ethdb.Database
	txpool   txPool
	core     *core.Core
	maxPeers int

	downloader   *downloader.Downloader
	blockFetcher *fetcher.BlockFetcher
	txFetcher    *fetcher.TxFetcher
	peers        *peerSet

	eventMux              *event.TypeMux
	txsCh                 chan core.NewTxsEvent
	txsSub                event.Subscription
	minedBlockSub         *event.TypeMuxSubscription
	missingPendingEtxsCh  chan types.HashAndLocation
	missingPendingEtxsSub event.Subscription
	missingParentCh       chan common.Hash
	missingParentSub      event.Subscription

	pEtxCh                chan types.PendingEtxs
	pEtxSub               event.Subscription
	pEtxRollupCh          chan types.PendingEtxsRollup
	pEtxRollupSub         event.Subscription
	missingPEtxsRollupCh  chan common.Hash
	missingPEtxsRollupSub event.Subscription

	whitelist map[uint64]common.Hash

	// channels for fetcher, syncer, txsyncLoop
	txsyncCh chan *txsync
	quitSync chan struct{}

	chainSync *chainSyncer
	wg        sync.WaitGroup
	peerWG    sync.WaitGroup

	broadcastCache *lru.Cache
}

// newHandler returns a handler for all Quai chain management protocol.
func newHandler(config *handlerConfig) (*handler, error) {
	nodeCtx := common.NodeLocation.Context()
	// Create the protocol manager with the base fields
	if config.EventMux == nil {
		config.EventMux = new(event.TypeMux) // Nicety initialization for tests
	}
	h := &handler{
		networkID:     config.Network,
		slicesRunning: config.SlicesRunning,
		forkFilter:    forkid.NewFilter(config.Core),
		eventMux:      config.EventMux,
		database:      config.Database,
		txpool:        config.TxPool,
		core:          config.Core,
		peers:         newPeerSet(),
		whitelist:     config.Whitelist,
		txsyncCh:      make(chan *txsync),
		quitSync:      make(chan struct{}),
	}

	broadcastCache, _ := lru.New(c_broadcastCacheSize)
	h.broadcastCache = broadcastCache

	h.downloader = downloader.New(h.eventMux, h.core, h.removePeer)

	// Construct the fetcher (short sync)
	validator := func(header *types.Header) error {
		return h.core.Engine().VerifyHeader(h.core, header)
	}
	heighter := func() uint64 {
		return h.core.CurrentHeader().NumberU64()
	}
	// writeBlock writes the block to the DB
	writeBlock := func(block *types.Block) {
		if nodeCtx == common.ZONE_CTX && block.NumberU64()-1 == h.core.CurrentHeader().NumberU64() && h.core.ProcessingState() {
			if atomic.LoadUint32(&h.acceptTxs) != 1 {
				atomic.StoreUint32(&h.acceptTxs, 1)
			}
		}
		h.core.WriteBlock(block)
	}
	h.blockFetcher = fetcher.NewBlockFetcher(h.core.GetBlockByHash, writeBlock, validator, h.BroadcastBlock, heighter, h.removePeer, h.core.IsBlockHashABadHash)

	// Only initialize the Tx fetcher in zone
	if nodeCtx == common.ZONE_CTX && h.core.ProcessingState() {
		fetchTx := func(peer string, hashes []common.Hash) error {
			p := h.peers.peer(peer)
			if p == nil {
				return errors.New("unknown peer")
			}
			return p.RequestTxs(hashes)
		}
		h.txFetcher = fetcher.NewTxFetcher(h.txpool.Has, h.txpool.AddRemotes, fetchTx)
	}
	h.chainSync = newChainSyncer(h)
	return h, nil
}

// runEthPeer registers an eth peer into the joint eth peerset, adds it to
// various subsystems and starts handling messages.
func (h *handler) runEthPeer(peer *eth.Peer, handler eth.Handler) error {
	nodeCtx := common.NodeLocation.Context()
	if !h.chainSync.handlePeerEvent(peer) {
		return p2p.DiscQuitting
	}
	h.peerWG.Add(1)
	defer h.peerWG.Done()

	// Execute the Quai handshake
	var (
		genesis = h.core.Genesis()
		head    = h.core.CurrentHeader()
		hash    = head.Hash()
		entropy = h.core.CurrentLogEntropy()
	)
	forkID := forkid.NewID(h.core.Config(), h.core.Genesis().Hash(), h.core.CurrentHeader().Number().Uint64())
	if err := peer.Handshake(h.networkID, h.slicesRunning, entropy, hash, genesis.Hash(), forkID, h.forkFilter); err != nil {
		peer.Log().Debug("Quai handshake failed", "err", err)
		return err
	}
	reject := false // reserved peer slots
	// Ignore maxPeers if this is a trusted peer
	if !peer.Peer.Info().Network.Trusted {
		if reject || h.peers.len() >= h.maxPeers {
			return p2p.DiscTooManyPeers
		}
	}
	peer.Log().Debug("Quai peer connected", "name", peer.Name())

	// Register the peer locally
	if err := h.peers.registerPeer(peer); err != nil {
		peer.Log().Error("Quai peer registration failed", "err", err)
		return err
	}
	defer h.unregisterPeer(peer.ID())

	p := h.peers.peer(peer.ID())
	if p == nil {
		return errors.New("peer dropped during handling")
	}
	// Register the peer in the downloader. If the downloader considers it banned, we disconnect
	if err := h.downloader.RegisterPeer(peer.ID(), peer.Version(), peer); err != nil {
		peer.Log().Error("Failed to register peer in eth syncer", "err", err)
		return err
	}

	h.chainSync.handlePeerEvent(peer)

	if nodeCtx == common.ZONE_CTX && h.core.ProcessingState() {
		// Propagate existing transactions. new transactions appearing
		// after this will be sent via broadcasts.
		h.syncTransactions(peer)
	}

	// If we have any explicit whitelist block hashes, request them
	for number := range h.whitelist {
		if err := peer.RequestHeadersByNumber(number, 1, 1, 0, false, false); err != nil {
			return err
		}
	}
	// Handle incoming messages until the connection is torn down
	return handler(peer)
}

// removePeer requests disconnection of a peer.
func (h *handler) removePeer(id string) {
	peer := h.peers.peer(id)
	if peer != nil {
		peer.Peer.Disconnect(p2p.DiscUselessPeer)
	}
}

// unregisterPeer removes a peer from the downloader, fetchers and main peer set.
func (h *handler) unregisterPeer(id string) {
	// Create a custom logger to avoid printing the entire id
	var logger log.Logger
	if len(id) < 16 {
		// Tests use short IDs, don't choke on them
		logger = log.Log
	} else {
		logger = log.Log
	}
	// Abort if the peer does not exist
	peer := h.peers.peer(id)
	if peer == nil {
		logger.Error("Quai peer removal failed", "err", errPeerNotRegistered)
		return
	}

	h.downloader.UnregisterPeer(id)
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.ZONE_CTX && h.core.ProcessingState() {
		h.txFetcher.Drop(id)
	}

	if err := h.peers.unregisterPeer(id); err != nil {
		logger.Error("Quai peer removal failed", "err", err)
	}
}

func (h *handler) Start(maxPeers int) {
	h.maxPeers = maxPeers

	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.ZONE_CTX && h.core.ProcessingState() {
		// broadcast transactions
		h.wg.Add(1)
		h.txsCh = make(chan core.NewTxsEvent, txChanSize)
		h.txsSub = h.txpool.SubscribeNewTxsEvent(h.txsCh)
		go h.txBroadcastLoop()
	}

	// broadcast pending etxs
	h.wg.Add(1)
	h.missingPendingEtxsCh = make(chan types.HashAndLocation, missingPendingEtxsChanSize)
	h.missingPendingEtxsSub = h.core.SubscribeMissingPendingEtxsEvent(h.missingPendingEtxsCh)
	go h.missingPendingEtxsLoop()

	h.wg.Add(1)
	h.missingParentCh = make(chan common.Hash, missingParentChanSize)
	h.missingParentSub = h.core.SubscribeMissingParentEvent(h.missingParentCh)
	go h.missingParentLoop()

	// broadcast mined blocks
	h.wg.Add(1)
	h.minedBlockSub = h.eventMux.Subscribe(core.NewMinedBlockEvent{})
	go h.minedBroadcastLoop()

	// start sync handlers
	h.wg.Add(1)
	go h.chainSync.loop()
	if nodeCtx == common.ZONE_CTX && h.core.ProcessingState() {
		h.wg.Add(1)
		go h.txsyncLoop64() //Legacy initial tx echange, drop with eth/64.
	}

	h.wg.Add(1)
	h.missingPEtxsRollupCh = make(chan common.Hash, c_pendingEtxBroadcastChanSize)
	h.missingPEtxsRollupSub = h.core.SubscribeMissingPendingEtxsRollupEvent(h.missingPEtxsRollupCh)
	go h.missingPEtxsRollupLoop()

	h.wg.Add(1)
	h.pEtxCh = make(chan types.PendingEtxs, c_pendingEtxBroadcastChanSize)
	h.pEtxSub = h.core.SubscribePendingEtxs(h.pEtxCh)
	go h.broadcastPEtxLoop()

	// broadcast pending etxs rollup
	h.wg.Add(1)
	h.pEtxRollupCh = make(chan types.PendingEtxsRollup, c_pendingEtxRollupBroadcastChanSize)
	h.pEtxRollupSub = h.core.SubscribePendingEtxsRollup(h.pEtxRollupCh)
	go h.broadcastPEtxRollupLoop()
}

func (h *handler) Stop() {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.ZONE_CTX && h.core.ProcessingState() {
		h.txsSub.Unsubscribe() // quits txBroadcastLoop
	}
	h.minedBlockSub.Unsubscribe()         // quits blockBroadcastLoop
	h.missingPendingEtxsSub.Unsubscribe() // quits pendingEtxsBroadcastLoop
	h.missingPEtxsRollupSub.Unsubscribe() // quits missingPEtxsRollupSub
	h.missingParentSub.Unsubscribe()      // quits missingParentLoop
	h.pEtxSub.Unsubscribe()               // quits pEtxSub
	h.pEtxRollupSub.Unsubscribe()         // quits pEtxRollupSub

	// Quit chainSync and txsync64.
	// After this is done, no new peers will be accepted.
	close(h.quitSync)
	h.wg.Wait()

	// Disconnect existing sessions.
	// This also closes the gate for any new registrations on the peer set.
	// sessions which are already established but not added to h.peers yet
	// will exit when they try to register.
	h.peers.close()
	h.peerWG.Wait()

	log.Info("Quai protocol stopped")
}

// BroadcastBlock will either propagate a block to a subset of its peers, or
// will only announce its availability (depending what's requested).
func (h *handler) BroadcastBlock(block *types.Block, propagate bool) {
	hash := block.Hash()
	peers := h.peers.peersWithoutBlock(hash)

	// If propagation is requested, send to a subset of the peer
	if propagate {
		// Send the block to a subset of our peers
		var peerThreshold int
		sqrtNumPeers := int(math.Sqrt(float64(len(peers))))
		if sqrtNumPeers < minPeerSend {
			peerThreshold = len(peers)
		} else {
			peerThreshold = sqrtNumPeers
		}
		transfer := peers[:peerThreshold]
		for _, peer := range transfer {
			peer.AsyncSendNewBlock(block)
		}
		log.Trace("Propagated block", "hash", hash, "recipients", len(transfer), "duration", common.PrettyDuration(time.Since(block.ReceivedAt)))
		return
	}
	// Otherwise if the block is indeed in out own chain, announce it
	if h.core.HasBlock(hash, block.NumberU64()) {
		for _, peer := range peers {
			peer.AsyncSendNewBlockHash(block)
		}
		log.Trace("Announced block", "hash", hash, "recipients", len(peers), "duration", common.PrettyDuration(time.Since(block.ReceivedAt)))
	}
}

// BroadcastTransactions will propagate a batch of transactions
// - To a square root of all peers
// - And, separately, as announcements to all peers which are not known to
// already have the given transaction.
func (h *handler) BroadcastTransactions(txs types.Transactions) {
	var (
		annoCount   int // Count of announcements made
		annoPeers   int
		directCount int // Count of the txs sent directly to peers
		directPeers int // Count of the peers that were sent transactions directly

		txset = make(map[*ethPeer][]common.Hash) // Set peer->hash to transfer directly
		annos = make(map[*ethPeer][]common.Hash) // Set peer->hash to announce

	)
	// Broadcast transactions to a batch of peers not knowing about it
	for _, tx := range txs {
		peers := h.peers.peersWithoutTransaction(tx.Hash())
		// Send the tx unconditionally to a subset of our peers
		numDirect := int(math.Sqrt(float64(len(peers))))
		subset := peers[:numDirect]
		if len(subset) < minPeerSendTx {
			// If we have less peers than the minimum, send to all peers
			if len(peers) < minPeerSendTx {
				subset = peers
			} else {
				// If our subset is less than the minimum, send to the minimum
				subset = peers[:minPeerSendTx] // The high bound is exclusive
			}
		}
		for _, peer := range subset {
			txset[peer] = append(txset[peer], tx.Hash())
		}
		// For the remaining peers, send announcement only
		for _, peer := range peers[numDirect:] {
			annos[peer] = append(annos[peer], tx.Hash())
		}
	}
	for peer, hashes := range txset {
		directPeers++
		directCount += len(hashes)
		peer.AsyncSendTransactions(hashes)
	}
	for peer, hashes := range annos {
		annoPeers++
		annoCount += len(hashes)
		peer.AsyncSendPooledTransactionHashes(hashes)
	}
	log.Debug("Transaction broadcast", "txs", len(txs),
		"announce packs", annoPeers, "announced hashes", annoCount,
		"tx packs", directPeers, "broadcast txs", directCount)
}

// minedBroadcastLoop sends mined blocks to connected peers.
func (h *handler) minedBroadcastLoop() {
	defer h.wg.Done()

	for obj := range h.minedBlockSub.Chan() {
		if ev, ok := obj.Data.(core.NewMinedBlockEvent); ok {
			h.BroadcastBlock(ev.Block, true)  // First propagate block to peers
			h.BroadcastBlock(ev.Block, false) // Only then announce to the rest
		}
	}
}

// txBroadcastLoop announces new transactions to connected peers.
func (h *handler) txBroadcastLoop() {
	defer h.wg.Done()
	for {
		select {
		case event := <-h.txsCh:
			h.BroadcastTransactions(event.Txs)
		case <-h.txsSub.Err():
			return
		}
	}
}

// missingPEtxsRollupLoop listens to the MissingBody event in Slice and calls the blockAnnounces.
func (h *handler) missingPEtxsRollupLoop() {
	defer h.wg.Done()
	for {
		select {
		case _ = <-h.missingPEtxsRollupCh:
			// Check if any of the peers have the body
			// for _, peer := range h.selectSomePeers() {
			// log.Trace("Fetching the missing pending etxs rollup from", "peer", peer.ID(), "hash", hash)
			// peer.RequestOnePendingEtxsRollup(hash)
			// }

		case <-h.missingPEtxsRollupSub.Err():
			return
		}
	}
}

// pendingEtxsBroadcastLoop announces new pendingEtxs to connected peers.
func (h *handler) missingPendingEtxsLoop() {
	defer h.wg.Done()
	for {
		select {
		case hashAndLocation := <-h.missingPendingEtxsCh:
			// Only ask from peers running the slice for the missing pending etxs
			// In the future, peers not responding before the timeout has to be punished
			peersRunningSlice := h.peers.peerRunningSlice(hashAndLocation.Location)
			// If the node doesn't have any peer running that slice, add a warning
			if len(peersRunningSlice) == 0 {
				log.Warn("Node doesn't have peers for given Location", "location", hashAndLocation.Location)
			}
			// Check if any of the peers have the body
			// for _, peer := range peersRunningSlice {
			// log.Trace("Fetching the missing pending etxs from", "peer", peer.ID(), "hash", hashAndLocation.Hash)
			// peer.RequestOnePendingEtxs(hashAndLocation.Hash)
			// }
			if len(peersRunningSlice) == 0 {
				// for _, peer := range h.selectSomePeers() {
				// log.Trace("Fetching the missing pending etxs from", "peer", peer.ID(), "hash", hashAndLocation.Hash)
				// peer.RequestOnePendingEtxs(hashAndLocation.Hash)
				// }
			}
		case <-h.missingPendingEtxsSub.Err():
			return
		}
	}
}

// missingParentLoop announces new pendingEtxs to connected peers.
func (h *handler) missingParentLoop() {
	defer h.wg.Done()
	for {
		select {
		case hash := <-h.missingParentCh:
			// Check if any of the peers have the body
			for _, peer := range h.selectSomePeers() {
				log.Trace("Fetching the missing parent from", "peer", peer.ID(), "hash", hash)
				peer.RequestBlockByHash(hash)
			}
		case <-h.missingParentSub.Err():
			return
		}
	}
}

// pEtxLoop  listens to the pendingEtxs event in Slice and anounces the pEtx to the peer
func (h *handler) broadcastPEtxLoop() {
	defer h.wg.Done()
	for {
		select {
		case pEtx := <-h.pEtxCh:
			h.BroadcastPendingEtxs(pEtx)
		case <-h.pEtxSub.Err():
			return
		}
	}
}

// pEtxRollupLoop  listens to the pendingEtxs event in Slice and anounces the pEtx to the peer
func (h *handler) broadcastPEtxRollupLoop() {
	defer h.wg.Done()
	for {
		select {
		case pEtxRollup := <-h.pEtxRollupCh:
			h.BroadcastPendingEtxsRollup(pEtxRollup)
		case <-h.pEtxRollupSub.Err():
			return
		}
	}
}

// BroadcastPendingEtxs will either propagate a pendingEtxs to a subset of its peers
func (h *handler) BroadcastPendingEtxs(pEtx types.PendingEtxs) {
	hash := pEtx.Header.Hash()
	peers := h.peers.peersWithoutPendingEtxs(hash)

	// Send the block to a subset of our peers
	var peerThreshold int
	sqrtNumPeers := int(math.Sqrt(float64(len(peers))))
	if sqrtNumPeers < minPeerSend {
		peerThreshold = len(peers)
	} else {
		peerThreshold = sqrtNumPeers
	}
	transfer := peers[:peerThreshold]
	// If in region send the pendingEtxs directly, otherwise send the pendingEtxsManifest
	// for _, peer := range transfer {
	// 	peer.SendPendingEtxs(pEtx)
	// }
	log.Trace("Propagated pending etxs", "hash", hash, "recipients", len(transfer), "len", len(pEtx.Etxs))
	return
}

// BroadcastPendingEtxsRollup will either propagate a pending etx rollup to a subset of its peers
func (h *handler) BroadcastPendingEtxsRollup(pEtxRollup types.PendingEtxsRollup) {
	hash := pEtxRollup.Header.Hash()
	peers := h.peers.peersWithoutPendingEtxs(hash)

	// Send the block to a subset of our peers
	var peerThreshold int
	sqrtNumPeers := int(math.Sqrt(float64(len(peers))))
	if sqrtNumPeers < minPeerSend {
		peerThreshold = len(peers)
	} else {
		peerThreshold = sqrtNumPeers
	}
	transfer := peers[:peerThreshold]
	// If in region send the pendingEtxs directly, otherwise send the pendingEtxsManifest
	// for _, peer := range transfer {
	// 	peer.SendPendingEtxsRollup(pEtxRollup)
	// }
	log.Trace("Propagated pending etxs rollup", "hash", hash, "recipients", len(transfer), "len", len(pEtxRollup.Manifest))
	return
}

func (h *handler) selectSomePeers() []*eth.Peer {
	// Get the min(sqrt(len(peers)), minPeerRequest)
	count := int(math.Sqrt(float64(len(h.peers.allPeers()))))
	if count < minPeerRequest {
		count = minPeerRequest
	}
	if count > len(h.peers.allPeers()) {
		count = len(h.peers.allPeers())
	}
	allPeers := h.peers.allPeers()

	// shuffle the filteredPeers
	rand.Shuffle(len(allPeers), func(i, j int) { allPeers[i], allPeers[j] = allPeers[j], allPeers[i] })
	return allPeers[:count]
}
