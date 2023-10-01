package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state/snapshot"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quaiclient"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru"
)

const (
	c_maxPendingEtxBatches            = 1024
	c_maxPendingEtxsRollup            = 256
	c_maxBloomFilters                 = 1024
	c_pendingHeaderChacheBufferFactor = 2
	pendingHeaderGCTime               = 5
	c_terminusIndex                   = 3
	c_startingPrintLimit              = 10
	c_regionRelayProc                 = 3
	c_primeRelayProc                  = 10
	c_asyncPhUpdateChanSize           = 10
	c_phCacheSize                     = 500
	c_pEtxRetryThreshold              = 100 // Number of pEtxNotFound return on a dom block before asking for pEtx/Rollup from sub
)

type pEtxRetry struct {
	hash    common.Hash
	retries uint64
}

type Slice struct {
	hc *HeaderChain

	txPool *TxPool
	miner  *Miner

	sliceDb ethdb.Database
	config  *params.ChainConfig
	engine  consensus.Engine

	quit chan struct{} // slice quit channel

	domClient  *quaiclient.Client
	subClients []*quaiclient.Client

	wg                    sync.WaitGroup
	scope                 event.SubscriptionScope
	pendingEtxsFeed       event.Feed
	pendingEtxsRollupFeed event.Feed
	missingBlockFeed      event.Feed

	pEtxRetryCache *lru.Cache
	asyncPhCh      chan *types.Header
	asyncPhSub     event.Subscription

	bestPhKey common.Hash
	phCache   *lru.Cache

	validator Validator // Block and state validator interface
	phCacheMu sync.RWMutex
	reorgMu   sync.RWMutex

	badHashesCache map[common.Hash]bool
}

func NewSlice(db ethdb.Database, config *Config, txConfig *TxPoolConfig, txLookupLimit *uint64, isLocalBlock func(block *types.Header) bool, chainConfig *params.ChainConfig, slicesRunning []common.Location, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis) (*Slice, error) {
	nodeCtx := common.NodeLocation.Context()
	sl := &Slice{
		config:         chainConfig,
		engine:         engine,
		sliceDb:        db,
		quit:           make(chan struct{}),
		badHashesCache: make(map[common.Hash]bool),
	}

	var err error
	sl.hc, err = NewHeaderChain(db, engine, sl.GetPEtxRollupAfterRetryThreshold, sl.GetPEtxAfterRetryThreshold, chainConfig, cacheConfig, txLookupLimit, vmConfig, slicesRunning)
	if err != nil {
		return nil, err
	}

	sl.validator = NewBlockValidator(chainConfig, sl.hc, engine)

	// tx pool is only used in zone
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.txPool = NewTxPool(*txConfig, chainConfig, sl.hc)
		sl.hc.pool = sl.txPool
	}
	sl.miner = New(sl.hc, sl.txPool, config, db, chainConfig, engine, isLocalBlock, sl.ProcessingState())

	sl.phCache, _ = lru.New(c_phCacheSize)

	sl.pEtxRetryCache, _ = lru.New(c_pEtxRetryThreshold)

	// only set the subClients if the chain is not Zone
	sl.subClients = make([]*quaiclient.Client, 3)
	if nodeCtx != common.ZONE_CTX {
		sl.subClients = makeSubClients(subClientUrls)
	}

	// only set domClient if the chain is not Prime.
	if nodeCtx != common.PRIME_CTX {
		go func() {
			sl.domClient = makeDomClient(domClientUrl)
		}()
	}

	if err := sl.init(genesis); err != nil {
		return nil, err
	}

	sl.CheckForBadHashAndRecover()

	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		go sl.asyncPendingHeaderLoop()
	}

	return sl, nil
}

// Append takes a proposed header and constructs a local block and attempts to hierarchically append it to the block graph.
// If this is called from a dominant context a domTerminus must be provided else a common.Hash{} should be used and domOrigin should be set to true.
// Return of this function is the Etxs generated in the Zone Block, subReorg bool that tells dom if should be mined on, setHead bool that determines if we should set the block as the current head and the error
func (sl *Slice) Append(header *types.Header, domPendingHeader *types.Header, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, bool, bool, error) {
	start := time.Now()

	if header.Hash() == sl.config.GenesisHash {
		return nil, false, false, nil
	}

	// Only print in Info level if block is c_startingPrintLimit behind or less
	if sl.CurrentInfo(header) {
		log.Info("Starting slice append", "hash", header.Hash(), "number", header.NumberArray(), "location", header.Location(), "parent hash", header.ParentHash())
	} else {
		log.Debug("Starting slice append", "hash", header.Hash(), "number", header.NumberArray(), "location", header.Location(), "parent hash", header.ParentHash())
	}

	time0_1 := common.PrettyDuration(time.Since(start))
	// Check if the header hash exists in the BadHashes list
	if sl.IsBlockHashABadHash(header.Hash()) {
		return nil, false, false, ErrBadBlockHash
	}
	time0_2 := common.PrettyDuration(time.Since(start))

	nodeCtx := common.NodeLocation.Context()
	location := header.Location()
	_, order, err := sl.engine.CalcOrder(header)
	if err != nil {
		return nil, false, false, err
	}
	// Don't append the block which already exists in the database.
	if sl.hc.HasHeader(header.Hash(), header.NumberU64()) && (sl.hc.GetTerminiByHash(header.Hash()) != nil) {
		log.Debug("Block has already been appended: ", "Hash: ", header.Hash())
		return nil, false, false, nil
	}
	time1 := common.PrettyDuration(time.Since(start))
	// This is to prevent a crash when we try to insert blocks before domClient is on.
	// Ideally this check should not exist here and should be fixed before we start the slice.
	if sl.domClient == nil && nodeCtx != common.PRIME_CTX {
		return nil, false, false, ErrDomClientNotUp
	}

	batch := sl.sliceDb.NewBatch()

	// Run Previous Coincident Reference Check (PCRC)
	domTerminus, newTermini, err := sl.pcrc(batch, header, domTerminus, domOrigin)
	if err != nil {
		return nil, false, false, err
	}
	log.Debug("PCRC done", "hash", header.Hash(), "number", header.NumberArray(), "termini", newTermini)

	time2 := common.PrettyDuration(time.Since(start))
	// Append the new block
	err = sl.hc.AppendHeader(header)
	if err != nil {
		return nil, false, false, err
	}

	time3 := common.PrettyDuration(time.Since(start))
	// Construct the block locally
	block, err := sl.ConstructLocalBlock(header)
	if err != nil {
		return nil, false, false, err
	}
	time4 := common.PrettyDuration(time.Since(start))

	var pendingHeaderWithTermini types.PendingHeader
	if nodeCtx != common.ZONE_CTX {
		// Upate the local pending header
		pendingHeaderWithTermini, err = sl.generateSlicePendingHeader(block, newTermini, domPendingHeader, domOrigin, true, false)
		if err != nil {
			return nil, false, false, err
		}
	}

	// If this was a coincident block, our dom will be passing us a set of newly
	// confirmed ETXs If this is not a coincident block, we need to build up the
	// list of confirmed ETXs using the subordinate manifest In either case, if
	// we are a dominant node, we need to collect the ETX rollup from our sub.
	if !domOrigin && nodeCtx != common.ZONE_CTX {
		newInboundEtxs, _, err = sl.CollectNewlyConfirmedEtxs(block, block.Location())
		if err != nil {
			log.Error("Error collecting newly confirmed etxs: ", "err", err)
			// Keeping track of the number of times pending etx fails and if it crossed the retry threshold
			// ask the sub for the pending etx/rollup data
			val, exist := sl.pEtxRetryCache.Get(block.Hash())
			var retry uint64
			if exist {
				pEtxCurrent, ok := val.(pEtxRetry)
				if ok {
					retry = pEtxCurrent.retries + 1
				}
			}
			pEtxNew := pEtxRetry{hash: block.Hash(), retries: retry}
			sl.pEtxRetryCache.Add(block.Hash(), pEtxNew)
			return nil, false, false, ErrSubNotSyncedToDom
		}
	}
	time5 := common.PrettyDuration(time.Since(start))

	time6 := common.PrettyDuration(time.Since(start))
	var subPendingEtxs types.Transactions
	var subReorg bool
	var setHead bool
	var time6_1 common.PrettyDuration
	var time6_2 common.PrettyDuration
	var time6_3 common.PrettyDuration
	// Call my sub to append the block, and collect the rolled up ETXs from that sub
	if nodeCtx != common.ZONE_CTX {
		// How to get the sub pending etxs if not running the full node?.
		if sl.subClients[location.SubIndex()] != nil {
			subPendingEtxs, subReorg, setHead, err = sl.subClients[location.SubIndex()].Append(context.Background(), header, block.SubManifest(), pendingHeaderWithTermini.Header(), domTerminus, true, newInboundEtxs)
			if err != nil {
				return nil, false, false, err
			}
			time6_1 = common.PrettyDuration(time.Since(start))
			// Cache the subordinate's pending ETXs
			pEtxs := types.PendingEtxs{header, subPendingEtxs}
			time6_2 = common.PrettyDuration(time.Since(start))
			// Add the pending etx given by the sub in the rollup
			sl.AddPendingEtxs(pEtxs)
			// Only region has the rollup hashes for pendingEtxs
			if nodeCtx == common.REGION_CTX {
				// We also need to store the pendingEtxRollup to the dom
				pEtxRollup := types.PendingEtxsRollup{header, block.SubManifest()}
				sl.AddPendingEtxsRollup(pEtxRollup)
			}
			time6_3 = common.PrettyDuration(time.Since(start))
		}
	}

	time7 := common.PrettyDuration(time.Since(start))

	sl.phCacheMu.Lock()
	defer sl.phCacheMu.Unlock()

	var time8, time9 common.PrettyDuration
	var bestPh types.PendingHeader
	var exist bool
	if nodeCtx == common.ZONE_CTX {
		bestPh, exist = sl.readPhCache(sl.bestPhKey)
		if !exist {
			sl.WriteBestPhKey(sl.config.GenesisHash)
			sl.writePhCache(block.Hash(), pendingHeaderWithTermini)
			bestPh = types.EmptyPendingHeader()
			log.Error("BestPh Key does not exist for", "key", sl.bestPhKey)
		}

		time8 = common.PrettyDuration(time.Since(start))

		tempPendingHeader, err := sl.generateSlicePendingHeader(block, newTermini, domPendingHeader, domOrigin, false, false)
		if err != nil {
			return nil, false, false, err
		}

		subReorg = sl.miningStrategy(bestPh, tempPendingHeader)

		if order < nodeCtx {
			// Store the inbound etxs for dom blocks that did not get picked and use
			// it in the future if dom switch happens
			rawdb.WriteInboundEtxs(sl.sliceDb, block.Hash(), newInboundEtxs.FilterToLocation(common.NodeLocation))
		}

		setHead = sl.poem(sl.engine.TotalLogS(block.Header()), sl.engine.TotalLogS(sl.hc.CurrentHeader()))
		if setHead {
			err := sl.hc.SetCurrentHeader(block.Header())
			if err != nil {
				log.Error("Error setting current header", "err", err, "Hash", block.Hash())
				return nil, false, false, err
			}
		}
		// Upate the local pending header
		pendingHeaderWithTermini, err = sl.generateSlicePendingHeader(block, newTermini, domPendingHeader, domOrigin, subReorg, false)
		if err != nil {
			return nil, false, false, err
		}

		time9 = common.PrettyDuration(time.Since(start))

	}
	sl.updatePhCache(pendingHeaderWithTermini, true, nil, subReorg, common.NodeLocation)

	var updateDom bool
	if subReorg {
		if order == common.ZONE_CTX && pendingHeaderWithTermini.Termini().DomTerminus() != bestPh.Termini().DomTerminus() {
			updateDom = true
		}
		log.Info("Choosing phHeader Append:", "NumberArray:", pendingHeaderWithTermini.Header().NumberArray(), "Number:", pendingHeaderWithTermini.Header().Number(), "ParentHash:", pendingHeaderWithTermini.Header().ParentHash(), "Terminus:", pendingHeaderWithTermini.Termini().DomTerminus())
		sl.WriteBestPhKey(pendingHeaderWithTermini.Termini().DomTerminus())
		block.SetAppendTime(time.Duration(time9))
	}

	// Append has succeeded write the batch
	if err := batch.Write(); err != nil {
		return nil, false, false, err
	}

	if setHead {
		if nodeCtx != common.ZONE_CTX {
			err := sl.hc.SetCurrentHeader(block.Header())
			if err != nil {
				log.Error("Error setting current header", "err", err, "Hash", block.Hash())
				return nil, false, false, err
			}
		}
		sl.hc.chainHeadFeed.Send(ChainHeadEvent{Block: block})
	}

	// Relay the new pendingHeader
	sl.relayPh(block, pendingHeaderWithTermini, domOrigin, block.Location(), subReorg)

	time10 := common.PrettyDuration(time.Since(start))
	log.Info("Times during append:", "t0_1", time0_1, "t0_2", time0_2, "t1:", time1, "t2:", time2, "t3:", time3, "t4:", time4, "t5:", time5, "t6:", time6, "t7:", time7, "t8:", time8, "t9:", time9, "t10:", time10)
	log.Debug("Times during sub append:", "t6_1:", time6_1, "t6_2:", time6_2, "t6_3:", time6_3)
	log.Info("Appended new block", "number", block.Header().NumberArray(), "hash", block.Hash(),
		"difficulty", block.Header().Difficulty(),
		"uncles", len(block.Uncles()), "txs", len(block.Transactions()), "etxs", len(block.ExtTransactions()), "gas", block.GasUsed(), "gasLimit", block.GasLimit(),
		"root", block.Root(),
		"order", order,
		"location", block.Header().Location(),
		"elapsed", common.PrettyDuration(time.Since(start)))

	if nodeCtx == common.ZONE_CTX {
		if updateDom {
			log.Info("Append updateDom", "oldTermini():", bestPh.Termini().DomTerminus(), "newTermini():", pendingHeaderWithTermini.Termini().DomTerminus(), "location:", common.NodeLocation)
			if sl.domClient != nil {
				go sl.domClient.UpdateDom(context.Background(), bestPh.Termini().DomTerminus(), pendingHeaderWithTermini, common.NodeLocation)
			}
		}
		return block.ExtTransactions(), subReorg, setHead, nil
	} else {
		return subPendingEtxs, subReorg, setHead, nil
	}
}

func (sl *Slice) miningStrategy(bestPh types.PendingHeader, pendingHeader types.PendingHeader) bool {
	if bestPh.Header() == nil { // This is the case where we try to append the block before we have not initialized the bestPh
		return true
	}
	subReorg := sl.poem(sl.engine.TotalLogPhS(pendingHeader.Header()), sl.engine.TotalLogPhS(bestPh.Header()))
	return subReorg
}

func (sl *Slice) ProcessingState() bool {
	return sl.hc.ProcessingState()
}

// relayPh sends pendingHeaderWithTermini to subordinates
func (sl *Slice) relayPh(block *types.Block, pendingHeaderWithTermini types.PendingHeader, domOrigin bool, location common.Location, subReorg bool) {
	nodeCtx := common.NodeLocation.Context()

	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		// Send an empty header to miner
		bestPh, exists := sl.readPhCache(sl.bestPhKey)
		if exists {
			bestPh.Header().SetLocation(common.NodeLocation)
			sl.miner.worker.pendingHeaderFeed.Send(bestPh.Header())
			return
		} else {
			log.Warn("Pending Header for Best ph key does not exist", "best ph key", sl.bestPhKey)
		}
	} else if !domOrigin && subReorg {
		for _, i := range sl.randomRelayArray() {
			if sl.subClients[i] != nil {
				sl.subClients[i].SubRelayPendingHeader(context.Background(), pendingHeaderWithTermini, pendingHeaderWithTermini.Header().ParentEntropy(), location, subReorg, nodeCtx)
			}
		}
	}
}

// If a zone changes its best ph key on a dom block, it sends a signal to the
// dom and we can relay that information to the coords, to build on the right dom header
func (sl *Slice) UpdateDom(oldTerminus common.Hash, pendingHeader types.PendingHeader, location common.Location) {
	nodeCtx := common.NodeLocation.Context()
	sl.phCacheMu.Lock()
	defer sl.phCacheMu.Unlock()
	newDomTermini := sl.hc.GetTerminiByHash(pendingHeader.Termini().DomTerminiAtIndex(location.SubIndex()))
	if newDomTermini == nil {
		log.Warn("New Dom Termini doesn't exists in the database for", "hash", pendingHeader.Termini().DomTerminiAtIndex(location.SubIndex()))
		return
	}
	newDomTerminus := newDomTermini.DomTerminus()
	oldDomTermini := sl.hc.GetTerminiByHash(oldTerminus)
	if oldDomTermini == nil {
		log.Warn("Old Dom Termini doesn't exists in the database for", "hash", oldTerminus)
		return
	}
	oldDomTerminus := oldDomTermini.DomTerminus()
	// Find the dom TerminusHash with the newTerminus
	newPh, newDomTerminiExists := sl.readPhCache(newDomTerminus)
	if !newDomTerminiExists {
		log.Warn("Update Dom:", "newTerminus does not exist:", newDomTerminus)
		return
	}
	log.Debug("UpdateDom:", "NewDomTerminus:", newDomTerminus, "OldDomTerminus:", oldDomTerminus, "NewDomTermini:", pendingHeader.Termini().DomTermini(), "Location")
	if nodeCtx == common.REGION_CTX && oldDomTerminus == newPh.Termini().DomTerminus() {
		// Can update
		sl.WriteBestPhKey(newDomTerminus)
		newPh, exists := sl.readPhCache(newDomTerminus)
		if exists {
			for _, i := range sl.randomRelayArray() {
				if sl.subClients[i] != nil {
					log.Info("SubRelay in UpdateDom", "parent Hash:", newPh.Header().ParentHash(), "Number", newPh.Header().NumberArray(), "newTermini:", newPh.Termini().SubTerminiAtIndex(i))
					sl.subClients[i].SubRelayPendingHeader(context.Background(), newPh, pendingHeader.Header().ParentEntropy(common.ZONE_CTX), common.Location{}, true, nodeCtx)
				}
			}
		} else {
			log.Warn("Update Dom:", "phCache at newTerminus does not exist:", newDomTerminus)
		}
		return
	} else {
		// need to update dom
		log.Info("UpdateDom needs to updateDom", "oldDomTermini:", oldDomTerminus, "newDomTermini:", newPh.Termini(), "location:", location)
		if sl.domClient != nil {
			go sl.domClient.UpdateDom(context.Background(), oldDomTerminus, types.NewPendingHeader(pendingHeader.Header(), newPh.Termini()), location)
		} else {
			// Can update
			sl.WriteBestPhKey(newDomTerminus)
			newPh, exists := sl.readPhCache(newDomTerminus)
			if exists {
				for _, i := range sl.randomRelayArray() {
					if sl.subClients[i] != nil {
						log.Info("SubRelay in UpdateDom:", "Parent Hash:", newPh.Header().ParentHash(), "Number", newPh.Header().NumberArray(), "NewTermini:", newPh.Termini().SubTerminiAtIndex(i))
						sl.subClients[i].SubRelayPendingHeader(context.Background(), newPh, pendingHeader.Header().ParentEntropy(common.ZONE_CTX), common.Location{}, true, nodeCtx)
					}
				}
			} else {
				log.Warn("Update Dom:", "phCache at newTerminus does not exist:", newDomTerminus)
			}
			return
		}
	}
}

func (sl *Slice) randomRelayArray() [3]int {
	rand.Seed(time.Now().UnixNano())
	nums := [3]int{0, 1, 2}
	for i := len(nums) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		nums[i], nums[j] = nums[j], nums[i]
	}
	return nums
}

// asyncPendingHeaderLoop waits for the pendingheader updates from the worker and updates the phCache
func (sl *Slice) asyncPendingHeaderLoop() {

	// Subscribe to the AsyncPh updates from the worker
	sl.asyncPhCh = make(chan *types.Header, c_asyncPhUpdateChanSize)
	sl.asyncPhSub = sl.miner.worker.SubscribeAsyncPendingHeader(sl.asyncPhCh)

	for {
		select {
		case asyncPh := <-sl.asyncPhCh:
			sl.phCacheMu.Lock()
			sl.updatePhCache(types.PendingHeader{}, true, asyncPh, true, common.NodeLocation)
			sl.phCacheMu.Unlock()
			bestPh, exists := sl.readPhCache(sl.bestPhKey)
			if exists {
				bestPh.Header().SetLocation(common.NodeLocation)
				sl.miner.worker.pendingHeaderFeed.Send(bestPh.Header())
			}
		case <-sl.asyncPhSub.Err():
			return

		case <-sl.quit:
			return
		}
	}
}

// Read the phCache
func (sl *Slice) readPhCache(hash common.Hash) (types.PendingHeader, bool) {
	if ph, exists := sl.phCache.Get(hash); exists {
		if ph, ok := ph.(types.PendingHeader); ok {
			if ph.Header() != nil {
				return *types.CopyPendingHeader(&ph), exists
			} else {
				return types.PendingHeader{}, false
			}
		}
	} else {
		ph := rawdb.ReadPendingHeader(sl.sliceDb, hash)
		if ph != nil {
			sl.phCache.Add(hash, ph)
			return *types.CopyPendingHeader(ph), exists
		} else {
			return types.PendingHeader{}, false
		}
	}
	return types.PendingHeader{}, false
}

// Write the phCache
func (sl *Slice) writePhCache(hash common.Hash, pendingHeader types.PendingHeader) {
	sl.phCache.Add(hash, pendingHeader)
	rawdb.WritePendingHeader(sl.sliceDb, hash, pendingHeader)
}

// WriteBestPhKey writes the sl.bestPhKey
func (sl *Slice) WriteBestPhKey(hash common.Hash) {
	sl.bestPhKey = hash
	// write the ph head hash to the db.
	rawdb.WriteBestPhKey(sl.sliceDb, hash)
}

// Generate a slice pending header
func (sl *Slice) generateSlicePendingHeader(block *types.Block, newTermini types.Termini, domPendingHeader *types.Header, domOrigin bool, subReorg bool, fill bool) (types.PendingHeader, error) {
	nodeCtx := common.NodeLocation.Context()
	var localPendingHeader *types.Header
	var err error
	if subReorg {
		// Upate the local pending header
		localPendingHeader, err = sl.miner.worker.GeneratePendingHeader(block, fill)
		if err != nil {
			return types.PendingHeader{}, err
		}
	} else {
		// Just compute the necessary information for the pending Header
		// i.e ParentHash field, Number and writing manifest to the disk
		localPendingHeader = types.EmptyHeader()
		localPendingHeader.SetParentHash(block.Hash(), nodeCtx)
		localPendingHeader.SetNumber(big.NewInt(int64(block.NumberU64()) + 1))
		localPendingHeader.SetParentEntropy(sl.engine.TotalLogS(block.Header()))
		if nodeCtx != common.PRIME_CTX {
			if domOrigin {
				localPendingHeader.SetParentDeltaS(big.NewInt(0), nodeCtx)
			} else {
				localPendingHeader.SetParentDeltaS(sl.engine.DeltaLogS(block.Header()), nodeCtx)
			}
		}

		manifestHash := sl.miner.worker.ComputeManifestHash(block.Header())
		localPendingHeader.SetManifestHash(manifestHash)
	}

	// Combine subordinates pending header with local pending header
	pendingHeaderWithTermini := sl.computePendingHeader(types.NewPendingHeader(localPendingHeader, newTermini), domPendingHeader, domOrigin)
	pendingHeaderWithTermini.Header().SetLocation(block.Header().Location())

	return pendingHeaderWithTermini, nil
}

// CollectNewlyConfirmedEtxs collects all newly confirmed ETXs since the last coincident with the given location
func (sl *Slice) CollectNewlyConfirmedEtxs(block *types.Block, location common.Location) (types.Transactions, types.Transactions, error) {
	nodeCtx := common.NodeLocation.Context()
	// Collect rollup of ETXs from the subordinate node's manifest
	subRollup := types.Transactions{}
	var err error
	if nodeCtx < common.ZONE_CTX {
		subRollup, err = sl.hc.CollectSubRollup(block)
		if err != nil {
			return nil, nil, err
		}
	}

	// Filter for ETXs destined to this slice
	newInboundEtxs := subRollup.FilterToSlice(location, nodeCtx)

	// Filter this list to exclude any ETX for which we are not the crossing
	// context node. Such ETXs cannot be used by our subordinate for one of the
	// following reasons:
	// * if we are prime, but common dom was a region node, than the given ETX has
	//   already been confirmed and passed down from the region node
	// * if we are region, but the common dom is prime, then the destination is
	//   not in one of our sub chains
	//
	// Note: here "common dom" refers to the highes context chain which exists in
	// both the origin & destination. See the definition of the `CommonDom()`
	// method for more explanation.
	newlyConfirmedEtxs := newInboundEtxs.FilterConfirmationCtx(nodeCtx)

	// Terminate the search if we reached genesis
	if block.NumberU64() == 0 {
		if block.Hash() != sl.config.GenesisHash {
			return nil, nil, fmt.Errorf("terminated search on bad genesis, block0 hash: %s", block.Hash().String())
		} else {
			return newlyConfirmedEtxs, subRollup, nil
		}
	}
	ancHash := block.ParentHash()
	ancNum := block.NumberU64() - 1
	ancestor := sl.hc.GetBlock(ancHash, ancNum)
	if ancestor == nil {
		return nil, nil, fmt.Errorf("unable to find ancestor, hash: %s", ancHash.String())
	}

	// Terminate the search when we find a block produced by the same sub
	if ancestor.Location().SubIndex() == location.SubIndex() {
		return newlyConfirmedEtxs, subRollup, nil
	}

	// Otherwise recursively process the ancestor and collect its newly confirmed ETXs too
	ancEtxs, _, err := sl.CollectNewlyConfirmedEtxs(ancestor, location)
	if err != nil {
		return nil, nil, err
	}
	newlyConfirmedEtxs = append(ancEtxs, newlyConfirmedEtxs...)
	return newlyConfirmedEtxs, subRollup, nil
}

// PCRC previous coincidence reference check makes sure there are not any cyclic references in the graph and calculates new termini and the block terminus
func (sl *Slice) pcrc(batch ethdb.Batch, header *types.Header, domTerminus common.Hash, domOrigin bool) (common.Hash, types.Termini, error) {
	nodeCtx := common.NodeLocation.Context()
	location := header.Location()

	log.Debug("PCRC:", "Parent Hash:", header.ParentHash(), "Number", header.Number, "Location:", header.Location())
	termini := sl.hc.GetTerminiByHash(header.ParentHash())

	if !termini.IsValid() {
		return common.Hash{}, types.EmptyTermini(), ErrSubNotSyncedToDom
	}

	newTermini := types.CopyTermini(*termini)
	// Set the subtermini
	if nodeCtx != common.ZONE_CTX {
		newTermini.SetSubTerminiAtIndex(header.Hash(), location.SubIndex())
	}

	// Set the terminus
	if nodeCtx == common.PRIME_CTX || domOrigin {
		newTermini.SetDomTerminiAtIndex(header.Hash(), location.DomIndex())
	} else {
		newTermini.SetDomTerminiAtIndex(termini.DomTerminus(), location.DomIndex())
	}

	// Check for a graph cyclic reference
	if domOrigin {
		if termini.DomTerminus() != domTerminus {
			log.Warn("Cyclic Block:", "block number", header.NumberArray(), "hash", header.Hash(), "terminus", domTerminus, "termini", termini.DomTerminus())
			return common.Hash{}, types.EmptyTermini(), errors.New("termini do not match, block rejected due to cyclic reference")
		}
	}

	//Save the termini
	rawdb.WriteTermini(batch, header.Hash(), newTermini)

	if nodeCtx == common.ZONE_CTX {
		return common.Hash{}, newTermini, nil
	}

	return termini.SubTerminiAtIndex(location.SubIndex()), newTermini, nil
}

// POEM compares externS to the currentHead S and returns true if externS is greater
func (sl *Slice) poem(externS *big.Int, currentS *big.Int) bool {
	log.Debug("POEM:", "currentS:", common.BigBitsToBits(currentS), "externS:", common.BigBitsToBits(externS))
	reorg := currentS.Cmp(externS) <= 0
	return reorg
}

// GetPendingHeader is used by the miner to request the current pending header
func (sl *Slice) GetPendingHeader() (*types.Header, error) {
	if ph, exists := sl.readPhCache(sl.bestPhKey); exists {
		return ph.Header(), nil
	} else {
		return nil, errors.New("empty pending header")
	}
}

// GetManifest gathers the manifest of ancestor block hashes since the last
// coincident block.
func (sl *Slice) GetManifest(blockHash common.Hash) (types.BlockManifest, error) {
	manifest := rawdb.ReadManifest(sl.sliceDb, blockHash)
	if manifest != nil {
		return manifest, nil
	}
	return nil, errors.New("manifest not found in the disk")
}

// GetSubManifest gets the block manifest from the subordinate node which
// produced this block
func (sl *Slice) GetSubManifest(slice common.Location, blockHash common.Hash) (types.BlockManifest, error) {
	subIdx := slice.SubIndex()
	if sl.subClients[subIdx] == nil {
		return nil, errors.New("missing requested subordinate node")
	}
	return sl.subClients[subIdx].GetManifest(context.Background(), blockHash)
}

// SendPendingEtxsToDom shares a set of pending ETXs with your dom, so he can reference them when a coincident block is found
func (sl *Slice) SendPendingEtxsToDom(pEtxs types.PendingEtxs) error {
	return sl.domClient.SendPendingEtxsToDom(context.Background(), pEtxs)
}

func (sl *Slice) GetPEtxRollupAfterRetryThreshold(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	pEtx, exists := sl.pEtxRetryCache.Get(blockHash)
	if !exists || pEtx.(pEtxRetry).retries < c_pEtxRetryThreshold {
		return types.PendingEtxsRollup{}, ErrPendingEtxNotFound
	}
	return sl.GetPendingEtxsRollupFromSub(hash, location)
}

// GetPendingEtxsRollupFromSub gets the pending etxs rollup from the appropriate prime
func (sl *Slice) GetPendingEtxsRollupFromSub(hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.PRIME_CTX {
		if sl.subClients[location.SubIndex()] != nil {
			pEtxRollup, err := sl.subClients[location.SubIndex()].GetPendingEtxsRollupFromSub(context.Background(), hash, location)
			if err != nil {
				return types.PendingEtxsRollup{}, err
			} else {
				sl.AddPendingEtxsRollup(pEtxRollup)
				return pEtxRollup, nil
			}
		}
	} else if nodeCtx == common.REGION_CTX {
		block := sl.hc.GetBlockOrCandidateByHash(hash)
		if block != nil {
			return types.PendingEtxsRollup{Header: block.Header(), Manifest: block.SubManifest()}, nil
		}
	}
	return types.PendingEtxsRollup{}, ErrPendingEtxNotFound
}

func (sl *Slice) GetPEtxAfterRetryThreshold(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	pEtx, exists := sl.pEtxRetryCache.Get(blockHash)
	if !exists || pEtx.(pEtxRetry).retries < c_pEtxRetryThreshold {
		return types.PendingEtxs{}, ErrPendingEtxNotFound
	}
	return sl.GetPendingEtxsFromSub(hash, location)
}

// GetPendingEtxsFromSub gets the pending etxs from the appropriate prime
func (sl *Slice) GetPendingEtxsFromSub(hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		if sl.subClients[location.SubIndex()] != nil {
			pEtx, err := sl.subClients[location.SubIndex()].GetPendingEtxsFromSub(context.Background(), hash, location)
			if err != nil {
				return types.PendingEtxs{}, err
			} else {
				sl.AddPendingEtxs(pEtx)
				return pEtx, nil
			}
		}
	}
	block := sl.hc.GetBlockOrCandidateByHash(hash)
	if block != nil {
		return types.PendingEtxs{Header: block.Header(), Etxs: block.ExtTransactions()}, nil
	}
	return types.PendingEtxs{}, ErrPendingEtxNotFound
}

// SubRelayPendingHeader takes a pending header from the sender (ie dominant), updates the phCache with a composited header and relays result to subordinates
func (sl *Slice) SubRelayPendingHeader(pendingHeader types.PendingHeader, newEntropy *big.Int, location common.Location, subReorg bool, order int) {
	nodeCtx := common.NodeLocation.Context()
	var err error

	if nodeCtx == common.REGION_CTX {
		// Adding a guard on the region that was already updated in the synchronous path.
		if location.Region() != common.NodeLocation.Region() {
			err = sl.updatePhCacheFromDom(pendingHeader, common.NodeLocation.Region(), []int{common.PRIME_CTX}, newEntropy, subReorg, location)
			if err != nil {
				return
			}
		}

		for _, i := range sl.randomRelayArray() {
			if sl.subClients[i] != nil {
				if ph, exists := sl.readPhCache(pendingHeader.Termini().SubTerminiAtIndex(common.NodeLocation.Region())); exists {
					sl.subClients[i].SubRelayPendingHeader(context.Background(), ph, newEntropy, location, subReorg, order)
				}
			}
		}
	} else {
		// This check prevents a double send to the miner.
		// If the previous block on which the given pendingHeader was built is the same as the NodeLocation
		// the pendingHeader update has already been sent to the miner for the given location in relayPh.
		if !bytes.Equal(location, common.NodeLocation) {
			updateCtx := []int{common.REGION_CTX}
			if order == common.PRIME_CTX {
				updateCtx = append(updateCtx, common.PRIME_CTX)
			}
			err = sl.updatePhCacheFromDom(pendingHeader, common.NodeLocation.Zone(), updateCtx, newEntropy, subReorg, location)
			if err != nil {
				return
			}
		}

		if !bytes.Equal(location, common.NodeLocation) {
			bestPh, exists := sl.readPhCache(sl.bestPhKey)
			if exists {
				bestPh.Header().SetLocation(common.NodeLocation)
				sl.miner.worker.pendingHeaderFeed.Send(bestPh.Header())
			}
		}
	}
}

// computePendingHeader takes in an localPendingHeaderWithTermini and updates the pending header on the same terminus if the number is greater
func (sl *Slice) computePendingHeader(localPendingHeaderWithTermini types.PendingHeader, domPendingHeader *types.Header, domOrigin bool) types.PendingHeader {
	nodeCtx := common.NodeLocation.Context()

	var cachedPendingHeaderWithTermini types.PendingHeader
	hash := localPendingHeaderWithTermini.Termini().DomTerminus()
	cachedPendingHeaderWithTermini, exists := sl.readPhCache(hash)
	log.Debug("computePendingHeader:", "hash:", hash, "pendingHeader:", cachedPendingHeaderWithTermini, "termini:", cachedPendingHeaderWithTermini.Termini)
	var newPh *types.Header

	if exists {
		if domOrigin {
			newPh = sl.combinePendingHeader(localPendingHeaderWithTermini.Header(), domPendingHeader, nodeCtx, true)
			return types.NewPendingHeader(types.CopyHeader(newPh), localPendingHeaderWithTermini.Termini())
		}
		newPh = sl.combinePendingHeader(localPendingHeaderWithTermini.Header(), cachedPendingHeaderWithTermini.Header(), nodeCtx, true)
		return types.NewPendingHeader(types.CopyHeader(newPh), localPendingHeaderWithTermini.Termini())
	} else {
		if domOrigin {
			newPh = sl.combinePendingHeader(localPendingHeaderWithTermini.Header(), domPendingHeader, nodeCtx, true)
			return types.NewPendingHeader(types.CopyHeader(newPh), localPendingHeaderWithTermini.Termini())
		}
		return localPendingHeaderWithTermini
	}
}

// updatePhCacheFromDom combines the recieved pending header with the pending header stored locally at a given terminus for specified context
func (sl *Slice) updatePhCacheFromDom(pendingHeader types.PendingHeader, terminiIndex int, indices []int, newEntropy *big.Int, subReorg bool, location common.Location) error {
	sl.phCacheMu.Lock()
	defer sl.phCacheMu.Unlock()
	hash := pendingHeader.Termini().SubTerminiAtIndex(terminiIndex)
	localPendingHeader, exists := sl.readPhCache(hash)

	if exists {
		combinedPendingHeader := types.CopyHeader(localPendingHeader.Header())
		for _, i := range indices {
			combinedPendingHeader = sl.combinePendingHeader(pendingHeader.Header(), combinedPendingHeader, i, false)
		}

		localTermini := localPendingHeader.Termini()
		if location.Equal(common.Location{}) {
			for i := 0; i < len(localTermini.DomTermini()); i++ {
				localTermini.SetDomTerminiAtIndex(pendingHeader.Termini().SubTerminiAtIndex(i), i)
			}
		} else {
			domIndex := location.DomIndex()
			localTermini.SetDomTerminiAtIndex(pendingHeader.Termini().SubTerminiAtIndex(domIndex), domIndex)
		}

		bestPh, exists := sl.readPhCache(sl.bestPhKey)
		nodeCtx := common.NodeLocation.Context()
		if nodeCtx == common.ZONE_CTX && exists && sl.bestPhKey != localPendingHeader.Termini().DomTerminus() && !sl.poem(newEntropy, bestPh.Header().ParentEntropy()) {
			log.Warn("Subrelay Rejected", "local dom terminus", localPendingHeader.Termini().DomTerminus(), "Number", combinedPendingHeader.NumberArray(), "best ph key", sl.bestPhKey, "number", bestPh.Header().NumberArray(), "newentropy", newEntropy)
			sl.updatePhCache(types.NewPendingHeader(combinedPendingHeader, localTermini), false, nil, sl.poem(newEntropy, localPendingHeader.Header().ParentEntropy()), location)
			go sl.domClient.UpdateDom(context.Background(), localPendingHeader.Termini().DomTerminus(), bestPh, common.NodeLocation)
			return nil
		}
		// Pick the head
		if subReorg {
			if (localPendingHeader.Header().Root() != types.EmptyRootHash && nodeCtx == common.ZONE_CTX) || nodeCtx == common.REGION_CTX {
				block := sl.hc.GetBlockOrCandidateByHash(localPendingHeader.Header().ParentHash())
				if block != nil {
					log.Info("Choosing phHeader pickPhHead:", "NumberArray:", combinedPendingHeader.NumberArray(), "Number:", combinedPendingHeader.Number(), "ParentHash:", combinedPendingHeader.ParentHash(), "Terminus:", localPendingHeader.Termini().DomTerminus())
					sl.WriteBestPhKey(localPendingHeader.Termini().DomTerminus())
				} else {
					log.Warn("unable to set the current header after the cord update", "Hash", localPendingHeader.Header().ParentHash())
				}
			} else {
				block := sl.hc.GetBlockOrCandidateByHash(localPendingHeader.Header().ParentHash())
				if block != nil {
					newPendingHeader, err := sl.generateSlicePendingHeader(block, localPendingHeader.Termini(), combinedPendingHeader, true, true, false)
					if err != nil {
						log.Error("Error generating slice pending header", "err", err)
						return err
					}
					combinedPendingHeader = types.CopyHeader(newPendingHeader.Header())
					log.Info("Choosing phHeader pickPhHead:", "NumberArray:", combinedPendingHeader.NumberArray(), "ParentHash:", combinedPendingHeader.ParentHash(), "Terminus:", localPendingHeader.Termini().DomTerminus())
					sl.WriteBestPhKey(localPendingHeader.Termini().DomTerminus())
				} else {
					log.Warn("unable to set the current header after the cord update", "Hash", localPendingHeader.Header().ParentHash())
				}
			}
		}

		sl.updatePhCache(types.NewPendingHeader(combinedPendingHeader, localTermini), false, nil, subReorg, location)

		return nil
	}
	log.Warn("no pending header found for", "terminus", hash, "pendingHeaderNumber", pendingHeader.Header().NumberArray(), "Hash", pendingHeader.Header().ParentHash(), "Termini index", terminiIndex, "indices", indices)
	return errors.New("pending header not found in cache")
}

// updatePhCache updates cache given a pendingHeaderWithTermini with the terminus used as the key.
func (sl *Slice) updatePhCache(pendingHeaderWithTermini types.PendingHeader, inSlice bool, localHeader *types.Header, subReorg bool, location common.Location) {

	var exists bool
	if localHeader != nil {
		termini := sl.hc.GetTerminiByHash(localHeader.ParentHash())
		if termini == nil {
			return
		}

		pendingHeaderWithTermini, exists = sl.readPhCache(termini.DomTerminus())
		if exists {
			pendingHeaderWithTermini.SetHeader(sl.combinePendingHeader(localHeader, pendingHeaderWithTermini.Header(), common.ZONE_CTX, true))
		}

		bestPh, exists := sl.readPhCache(sl.bestPhKey)
		if !exists {
			return
		}
		if !sl.miningStrategy(bestPh, pendingHeaderWithTermini) {
			return
		}
	}

	termini := pendingHeaderWithTermini.Termini()

	// This logic implements the Termini Carry
	// * If a terminus exists in the cache, we can just copy the current value and make updates to it
	// * If a terminus doesn't exist in the cache, that means that this is invoked
	// 		on a dom block and we have to carry the parents Termini and make an update
	var cachedTermini types.Termini
	ph, exists := sl.readPhCache(pendingHeaderWithTermini.Termini().DomTerminus())
	if exists {
		cachedTermini = types.CopyTermini(ph.Termini())
	} else {
		parentHeader := sl.hc.GetHeaderOrCandidateByHash(pendingHeaderWithTermini.Header().ParentHash())
		if parentHeader.Hash() == sl.config.GenesisHash {
			ph, _ = sl.readPhCache(sl.config.GenesisHash)
			cachedTermini = types.CopyTermini(ph.Termini())
		} else {
			localTermini := sl.hc.GetTerminiByHash(parentHeader.ParentHash())
			ph, _ = sl.readPhCache(localTermini.DomTerminus())
			cachedTermini = types.CopyTermini(ph.Termini())
		}
	}

	// During the UpdateDom cycle we have to just copy what the Dom gives us
	// otherwise update only the value that is being changed during inslice
	// update and coord update
	if !location.Equal(common.Location{}) {
		cachedTermini.SetDomTerminiAtIndex(termini.DomTerminiAtIndex(location.DomIndex()), location.DomIndex())
	} else {
		for i := 0; i < len(termini.DomTermini()); i++ {
			cachedTermini.SetDomTerminiAtIndex(termini.DomTerminiAtIndex(i), i)
		}
	}
	cachedTermini.SetSubTermini(termini.SubTermini())

	// Update the pendingHeader Cache
	deepCopyPendingHeaderWithTermini := types.NewPendingHeader(types.CopyHeader(pendingHeaderWithTermini.Header()), cachedTermini)
	deepCopyPendingHeaderWithTermini.Header().SetLocation(common.NodeLocation)
	deepCopyPendingHeaderWithTermini.Header().SetTime(uint64(time.Now().Unix()))

	if subReorg || !exists {
		sl.writePhCache(deepCopyPendingHeaderWithTermini.Termini().DomTerminus(), deepCopyPendingHeaderWithTermini)
		log.Info("PhCache update:", "new terminus?:", !exists, "inSlice:", inSlice, "Ph Number:", deepCopyPendingHeaderWithTermini.Header().NumberArray(), "Termini:", deepCopyPendingHeaderWithTermini.Termini())
	}
}

// init checks if the headerchain is empty and if it's empty appends the Knot
// otherwise loads the last stored state of the chain.
func (sl *Slice) init(genesis *Genesis) error {
	// Even though the genesis block cannot have any ETXs, we still need an empty
	// pending ETX entry for that block hash, so that the state processor can build
	// on it
	genesisHash := sl.Config().GenesisHash
	genesisHeader := sl.hc.GetHeader(genesisHash, 0)
	if genesisHeader == nil {
		return errors.New("failed to get genesis header")
	}

	// Loading the badHashes from the data base and storing it in the cache
	badHashes := rawdb.ReadBadHashesList(sl.sliceDb)
	sl.AddToBadHashesList(badHashes)

	// If the headerchain is empty start from genesis
	if sl.hc.Empty() {
		// Initialize slice state for genesis knot
		genesisTermini := types.EmptyTermini()
		for i := 0; i < len(genesisTermini.SubTermini()); i++ {
			genesisTermini.SetSubTerminiAtIndex(genesisHash, i)
		}
		for i := 0; i < len(genesisTermini.DomTermini()); i++ {
			genesisTermini.SetDomTerminiAtIndex(genesisHash, i)
		}

		rawdb.WriteTermini(sl.sliceDb, genesisHash, genesisTermini)
		rawdb.WriteManifest(sl.sliceDb, genesisHash, types.BlockManifest{genesisHash})

		// Append each of the knot blocks
		sl.WriteBestPhKey(genesisHash)
		sl.hc.SetCurrentHeader(genesisHeader)

		// Create empty pending ETX entry for genesis block -- genesis may not emit ETXs
		emptyPendingEtxs := types.Transactions{}
		err := sl.hc.AddPendingEtxs(types.PendingEtxs{genesisHeader, emptyPendingEtxs})
		if err != nil {
			return err
		}
		err = sl.AddPendingEtxsRollup(types.PendingEtxsRollup{genesisHeader, []common.Hash{}})
		if err != nil {
			return err
		}
		err = sl.hc.AddBloom(types.Bloom{}, genesisHeader.Hash())
		if err != nil {
			return err
		}
		rawdb.WriteEtxSet(sl.sliceDb, genesisHash, 0, types.NewEtxSet())

		if common.NodeLocation.Context() == common.PRIME_CTX {
			go sl.NewGenesisPendingHeader(nil)
		}
	} else { // load the phCache and slice current pending header hash
		if err := sl.loadLastState(); err != nil {
			return err
		}
	}
	return nil
}

// constructLocalBlock takes a header and construct the Block locally by getting the body
// from the candidate body db. This method is used when peers give the block as a placeholder
// for the body.
func (sl *Slice) ConstructLocalBlock(header *types.Header) (*types.Block, error) {
	pendingBlockBody := rawdb.ReadBody(sl.sliceDb, header.Hash(), header.NumberU64())
	if pendingBlockBody == nil {
		return nil, ErrBodyNotFound
	}
	// Load uncles because they are not included in the block response.
	txs := make([]*types.Transaction, len(pendingBlockBody.Transactions))
	for i, tx := range pendingBlockBody.Transactions {
		txs[i] = tx
	}
	uncles := make([]*types.Header, len(pendingBlockBody.Uncles))
	for i, uncle := range pendingBlockBody.Uncles {
		uncles[i] = uncle
		log.Debug("Pending Block uncle", "hash: ", uncle.Hash())
	}
	etxs := make([]*types.Transaction, len(pendingBlockBody.ExtTransactions))
	for i, etx := range pendingBlockBody.ExtTransactions {
		etxs[i] = etx
	}
	subManifest := make(types.BlockManifest, len(pendingBlockBody.SubManifest))
	for i, blockHash := range pendingBlockBody.SubManifest {
		subManifest[i] = blockHash
	}
	block := types.NewBlockWithHeader(header).WithBody(txs, uncles, etxs, subManifest)
	if err := sl.validator.ValidateBody(block); err != nil {
		return block, err
	} else {
		return block, nil
	}
}

// constructLocalMinedBlock takes a header and construct the Block locally by getting the block
// body from the workers pendingBlockBodyCache. This method is used when the miner sends in the
// header.
func (sl *Slice) ConstructLocalMinedBlock(header *types.Header) (*types.Block, error) {
	nodeCtx := common.NodeLocation.Context()
	var pendingBlockBody *types.Body
	if nodeCtx == common.ZONE_CTX {
		pendingBlockBody = sl.GetPendingBlockBody(header)
		if pendingBlockBody == nil {
			return nil, ErrBodyNotFound
		}
	} else {
		pendingBlockBody = &types.Body{}
	}
	// Load uncles because they are not included in the block response.
	txs := make([]*types.Transaction, len(pendingBlockBody.Transactions))
	for i, tx := range pendingBlockBody.Transactions {
		txs[i] = tx
	}
	uncles := make([]*types.Header, len(pendingBlockBody.Uncles))
	for i, uncle := range pendingBlockBody.Uncles {
		uncles[i] = uncle
		log.Debug("Pending Block uncle", "hash: ", uncle.Hash())
	}
	etxs := make([]*types.Transaction, len(pendingBlockBody.ExtTransactions))
	for i, etx := range pendingBlockBody.ExtTransactions {
		etxs[i] = etx
	}
	subManifest := make(types.BlockManifest, len(pendingBlockBody.SubManifest))
	for i, blockHash := range pendingBlockBody.SubManifest {
		subManifest[i] = blockHash
	}
	block := types.NewBlockWithHeader(header).WithBody(txs, uncles, etxs, subManifest)
	if err := sl.validator.ValidateBody(block); err != nil {
		return block, err
	} else {
		return block, nil
	}
}

// combinePendingHeader updates the pending header at the given index with the value from given header.
func (sl *Slice) combinePendingHeader(header *types.Header, slPendingHeader *types.Header, index int, inSlice bool) *types.Header {
	// copying the slPendingHeader and updating the copy to remove any shared memory access issues
	combinedPendingHeader := types.CopyHeader(slPendingHeader)

	combinedPendingHeader.SetParentHash(header.ParentHash(index), index)
	combinedPendingHeader.SetNumber(header.Number(index), index)
	combinedPendingHeader.SetManifestHash(header.ManifestHash(index), index)
	combinedPendingHeader.SetParentEntropy(header.ParentEntropy(index), index)
	combinedPendingHeader.SetParentDeltaS(header.ParentDeltaS(index), index)

	if inSlice {
		combinedPendingHeader.SetEtxRollupHash(header.EtxRollupHash())
		combinedPendingHeader.SetDifficulty(header.Difficulty())
		combinedPendingHeader.SetUncleHash(header.UncleHash())
		combinedPendingHeader.SetTxHash(header.TxHash())
		combinedPendingHeader.SetEtxHash(header.EtxHash())
		combinedPendingHeader.SetReceiptHash(header.ReceiptHash())
		combinedPendingHeader.SetRoot(header.Root())
		combinedPendingHeader.SetCoinbase(header.Coinbase())
		combinedPendingHeader.SetBaseFee(header.BaseFee())
		combinedPendingHeader.SetGasLimit(header.GasLimit())
		combinedPendingHeader.SetGasUsed(header.GasUsed())
		combinedPendingHeader.SetExtra(header.Extra())
	}

	return combinedPendingHeader
}

// NewGenesisPendingHeader creates a pending header on the genesis block
func (sl *Slice) NewGenesisPendingHeader(domPendingHeader *types.Header) {
	nodeCtx := common.NodeLocation.Context()
	genesisHash := sl.config.GenesisHash
	// Upate the local pending header
	localPendingHeader, err := sl.miner.worker.GeneratePendingHeader(sl.hc.GetBlockByHash(genesisHash), false)
	if err != nil {
		return
	}

	if nodeCtx == common.PRIME_CTX {
		domPendingHeader = types.CopyHeader(localPendingHeader)
	} else {
		domPendingHeader = sl.combinePendingHeader(localPendingHeader, domPendingHeader, nodeCtx, true)
		domPendingHeader.SetLocation(common.NodeLocation)
	}

	if nodeCtx != common.ZONE_CTX {
		for _, client := range sl.subClients {
			if client != nil {
				client.NewGenesisPendingHeader(context.Background(), domPendingHeader)
				if err != nil {
					return
				}
			}
		}
	}
	genesisTermini := types.EmptyTermini()
	for i := 0; i < len(genesisTermini.SubTermini()); i++ {
		genesisTermini.SetSubTerminiAtIndex(genesisHash, i)
	}
	for i := 0; i < len(genesisTermini.DomTermini()); i++ {
		genesisTermini.SetDomTerminiAtIndex(genesisHash, i)
	}
	if sl.hc.Empty() {
		domPendingHeader.SetTime(uint64(time.Now().Unix()))
		sl.phCache.Add(sl.config.GenesisHash, types.NewPendingHeader(domPendingHeader, genesisTermini))
	}
}

func (sl *Slice) GetPendingBlockBody(header *types.Header) *types.Body {
	return sl.miner.worker.GetPendingBlockBody(header)
}

func (sl *Slice) SubscribeMissingBlockEvent(ch chan<- types.BlockRequest) event.Subscription {
	return sl.scope.Track(sl.missingBlockFeed.Subscribe(ch))
}

// MakeDomClient creates the quaiclient for the given domurl
func makeDomClient(domurl string) *quaiclient.Client {
	if domurl == "" {
		log.Fatal("dom client url is empty")
	}
	domClient, err := quaiclient.Dial(domurl)
	if err != nil {
		log.Fatal("Error connecting to the dominant go-quai client", "err", err)
	}
	return domClient
}

// MakeSubClients creates the quaiclient for the given suburls
func makeSubClients(suburls []string) []*quaiclient.Client {
	subClients := make([]*quaiclient.Client, 3)
	for i, suburl := range suburls {
		if suburl != "" {
			subClient, err := quaiclient.Dial(suburl)
			if err != nil {
				log.Fatal("Error connecting to the subordinate go-quai client for index", "index", i, " err ", err)
			}
			subClients[i] = subClient
		}
	}
	return subClients
}

// loadLastState loads the phCache and the slice pending header hash from the db.
func (sl *Slice) loadLastState() error {
	sl.bestPhKey = rawdb.ReadBestPhKey(sl.sliceDb)
	bestPh := rawdb.ReadPendingHeader(sl.sliceDb, sl.bestPhKey)
	if bestPh != nil {
		sl.writePhCache(sl.bestPhKey, *bestPh)
	}

	if sl.ProcessingState() {
		sl.miner.worker.LoadPendingBlockBody()
	}
	return nil
}

// Stop stores the phCache and the sl.pendingHeader hash value to the db.
func (sl *Slice) Stop() {
	nodeCtx := common.NodeLocation.Context()

	var badHashes []common.Hash
	for hash := range sl.badHashesCache {
		badHashes = append(badHashes, hash)
	}
	rawdb.WriteBadHashesList(sl.sliceDb, badHashes)
	sl.miner.worker.StorePendingBlockBody()

	sl.scope.Close()
	close(sl.quit)

	sl.hc.Stop()
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.asyncPhSub.Unsubscribe()
		sl.txPool.Stop()
	}
	sl.miner.Stop()
}

func (sl *Slice) Config() *params.ChainConfig { return sl.config }

func (sl *Slice) Engine() consensus.Engine { return sl.engine }

func (sl *Slice) HeaderChain() *HeaderChain { return sl.hc }

func (sl *Slice) TxPool() *TxPool { return sl.txPool }

func (sl *Slice) Miner() *Miner { return sl.miner }

func (sl *Slice) CurrentInfo(header *types.Header) bool {
	return sl.miner.worker.CurrentInfo(header)
}

func (sl *Slice) WriteBlock(block *types.Block) {
	sl.hc.WriteBlock(block)
}

func (sl *Slice) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	nodeCtx := common.NodeLocation.Context()
	if err := sl.hc.AddPendingEtxs(pEtxs); err == nil || err.Error() != ErrPendingEtxAlreadyKnown.Error() {
		// Only in the region case we have to send the pendingEtxs to dom from the AddPendingEtxs
		if nodeCtx == common.REGION_CTX {
			// Also the first time when adding the pending etx broadcast it to the peers
			sl.pendingEtxsFeed.Send(pEtxs)
			if sl.domClient != nil {
				sl.domClient.SendPendingEtxsToDom(context.Background(), pEtxs)
			}
		}
	} else if err.Error() == ErrPendingEtxAlreadyKnown.Error() {
		return nil
	} else {
		return err
	}
	return nil
}

func (sl *Slice) AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error {
	if !pEtxsRollup.IsValid(trie.NewStackTrie(nil)) {
		log.Info("PendingEtxRollup is invalid")
		return ErrPendingEtxRollupNotValid
	}
	nodeCtx := common.NodeLocation.Context()
	log.Debug("Received pending ETXs Rollup", "header: ", pEtxsRollup.Header.Hash(), "Len of Rollup", len(pEtxsRollup.Manifest))
	// Only write the pending ETXs if we have not seen them before
	if !sl.hc.pendingEtxsRollup.Contains(pEtxsRollup.Header.Hash()) {
		// Also write to cache for faster access
		sl.hc.pendingEtxsRollup.Add(pEtxsRollup.Header.Hash(), pEtxsRollup)
		// Write to pending ETX rollup database
		rawdb.WritePendingEtxsRollup(sl.sliceDb, pEtxsRollup)

		// Only Prime broadcasts the pendingEtxRollups
		if nodeCtx == common.PRIME_CTX {
			// Also the first time when adding the pending etx rollup broadcast it to the peers
			sl.pendingEtxsRollupFeed.Send(pEtxsRollup)
			// Only in the region case, send the pending etx rollup to the dom
		} else if nodeCtx == common.REGION_CTX {
			if sl.domClient != nil {
				sl.domClient.SendPendingEtxsRollupToDom(context.Background(), pEtxsRollup)
			}
		}
	}
	return nil
}

func (sl *Slice) CheckForBadHashAndRecover() {
	nodeCtx := common.NodeLocation.Context()
	// Lookup the bad hashes list to see if we have it in the database
	for _, fork := range BadHashes {
		var badBlock *types.Block
		var badHash common.Hash
		switch nodeCtx {
		case common.PRIME_CTX:
			badHash = fork.PrimeContext
		case common.REGION_CTX:
			badHash = fork.RegionContext[common.NodeLocation.Region()]
		case common.ZONE_CTX:
			badHash = fork.ZoneContext[common.NodeLocation.Region()][common.NodeLocation.Zone()]
		}
		badBlock = sl.hc.GetBlockByHash(badHash)
		// Node has a bad block in the database
		if badBlock != nil {
			// Start from the current tip and delete every block from the database until this bad hash block
			sl.cleanCacheAndDatabaseTillBlock(badBlock.ParentHash())
			if nodeCtx == common.PRIME_CTX {
				sl.SetHeadBackToRecoveryState(nil, badBlock.ParentHash())
			}
		}
	}
}

// SetHeadBackToRecoveryState sets the heads of the whole hierarchy to the recovery state
func (sl *Slice) SetHeadBackToRecoveryState(pendingHeader *types.Header, hash common.Hash) types.PendingHeader {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.PRIME_CTX {
		localPendingHeaderWithTermini := sl.ComputeRecoveryPendingHeader(hash)
		sl.phCache.Add(hash, localPendingHeaderWithTermini)
		sl.GenerateRecoveryPendingHeader(localPendingHeaderWithTermini.Header(), localPendingHeaderWithTermini.Termini())
	} else {
		localPendingHeaderWithTermini := sl.ComputeRecoveryPendingHeader(hash)
		localPendingHeaderWithTermini.SetHeader(sl.combinePendingHeader(localPendingHeaderWithTermini.Header(), pendingHeader, nodeCtx, true))
		localPendingHeaderWithTermini.Header().SetLocation(common.NodeLocation)
		sl.phCache.Add(hash, localPendingHeaderWithTermini)
		return localPendingHeaderWithTermini
	}
	return types.PendingHeader{}
}

// cleanCacheAndDatabaseTillBlock till delete all entries of header and other
// data structures around slice until the given block hash
func (sl *Slice) cleanCacheAndDatabaseTillBlock(hash common.Hash) {
	currentHeader := sl.hc.CurrentHeader()
	// If the hash is the current header hash, there is nothing to clean from the database
	if hash == currentHeader.Hash() {
		return
	}
	nodeCtx := common.NodeLocation.Context()
	// slice caches
	sl.phCache.Purge()
	sl.miner.worker.pendingBlockBody.Purge()
	rawdb.DeleteBestPhKey(sl.sliceDb)
	// headerchain caches
	sl.hc.headerCache.Purge()
	sl.hc.numberCache.Purge()
	sl.hc.pendingEtxsRollup.Purge()
	sl.hc.pendingEtxs.Purge()
	rawdb.DeleteAllHeadsHashes(sl.sliceDb)
	// bodydb caches
	sl.hc.bc.blockCache.Purge()
	sl.hc.bc.bodyCache.Purge()
	sl.hc.bc.bodyRLPCache.Purge()

	var badHashes []common.Hash
	header := currentHeader
	for {
		rawdb.DeleteBlock(sl.sliceDb, header.Hash(), header.NumberU64())
		rawdb.DeleteCanonicalHash(sl.sliceDb, header.NumberU64())
		rawdb.DeleteHeaderNumber(sl.sliceDb, header.Hash())
		rawdb.DeleteTermini(sl.sliceDb, header.Hash())
		rawdb.DeleteEtxSet(sl.sliceDb, header.Hash(), header.NumberU64())
		if nodeCtx != common.ZONE_CTX {
			pendingEtxsRollup := rawdb.ReadPendingEtxsRollup(sl.sliceDb, header.Hash())
			// First hash in the manifest is always a dom block and it needs to be
			// deleted separately because last hash in the final iteration will be
			// referenced in the next dom block after the restart
			for _, manifestHash := range pendingEtxsRollup.Manifest[1:] {
				rawdb.DeletePendingEtxs(sl.sliceDb, manifestHash)
			}
			rawdb.DeletePendingEtxs(sl.sliceDb, header.Hash())
			rawdb.DeletePendingEtxsRollup(sl.sliceDb, header.Hash())
		}
		// delete the trie node for a given root of the header
		rawdb.DeleteTrieNode(sl.sliceDb, header.Root())
		badHashes = append(badHashes, header.Hash())
		parent := sl.hc.GetHeader(header.ParentHash(), header.NumberU64()-1)
		header = parent
		if header.Hash() == hash || header.Hash() == sl.config.GenesisHash {
			break
		}
	}

	sl.AddToBadHashesList(badHashes)
	// Set the current header
	currentHeader = sl.hc.GetHeaderByHash(hash)
	sl.hc.currentHeader.Store(currentHeader)

	// Recover the snaps
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.hc.bc.processor.snaps, _ = snapshot.New(sl.sliceDb, sl.hc.bc.processor.stateCache.TrieDB(), sl.hc.bc.processor.cacheConfig.SnapshotLimit, currentHeader.Root(), true, true)
	}
}

func (sl *Slice) GenerateRecoveryPendingHeader(pendingHeader *types.Header, checkPointHashes types.Termini) error {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.PRIME_CTX {
		for i := 0; i < common.NumRegionsInPrime; i++ {
			if sl.subClients[i] != nil {
				sl.subClients[i].GenerateRecoveryPendingHeader(context.Background(), pendingHeader, checkPointHashes)
			}
		}
	} else if nodeCtx == common.REGION_CTX {
		newPendingHeader := sl.SetHeadBackToRecoveryState(pendingHeader, checkPointHashes.SubTerminiAtIndex(common.NodeLocation.Region()))
		for i := 0; i < common.NumZonesInRegion; i++ {
			if sl.subClients[i] != nil {
				sl.subClients[i].GenerateRecoveryPendingHeader(context.Background(), newPendingHeader.Header(), newPendingHeader.Termini())
			}
		}
	} else {
		sl.SetHeadBackToRecoveryState(pendingHeader, checkPointHashes.SubTerminiAtIndex(common.NodeLocation.Zone()))
	}
	return nil
}

// ComputeRecoveryPendingHeader generates the pending header at a given hash
// and gets the termini from the database and returns the pending header with
// termini
func (sl *Slice) ComputeRecoveryPendingHeader(hash common.Hash) types.PendingHeader {
	block := sl.hc.GetBlockByHash(hash)
	pendingHeader, err := sl.miner.worker.GeneratePendingHeader(block, false)
	if err != nil {
		log.Error("Error generating pending header during the checkpoint recovery process")
		return types.PendingHeader{}
	}
	termini := sl.hc.GetTerminiByHash(hash)
	sl.WriteBestPhKey(hash)
	return types.NewPendingHeader(pendingHeader, *termini)
}

// AddToBadHashesList adds a given set of badHashes to the BadHashesList
func (sl *Slice) AddToBadHashesList(badHashes []common.Hash) {
	for _, hash := range badHashes {
		sl.badHashesCache[hash] = true
	}
}

// HashExistsInBadHashesList checks if the given hash exists in the badHashesCache
func (sl *Slice) HashExistsInBadHashesList(hash common.Hash) bool {
	// Look for pending ETXs first in pending ETX map, then in database
	_, ok := sl.badHashesCache[hash]
	return ok
}

// IsBlockHashABadHash checks if the given hash exists in BadHashes List
func (sl *Slice) IsBlockHashABadHash(hash common.Hash) bool {
	nodeCtx := common.NodeLocation.Context()
	switch nodeCtx {
	case common.PRIME_CTX:
		for _, fork := range BadHashes {
			if fork.PrimeContext == hash {
				return true
			}
		}
	case common.REGION_CTX:
		for _, fork := range BadHashes {
			if fork.RegionContext[common.NodeLocation.Region()] == hash {
				return true
			}
		}
	case common.ZONE_CTX:
		for _, fork := range BadHashes {
			if fork.ZoneContext[common.NodeLocation.Region()][common.NodeLocation.Zone()] == hash {
				return true
			}
		}
	}
	return sl.HashExistsInBadHashesList(hash)
}
