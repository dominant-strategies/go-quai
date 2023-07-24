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
)

type Slice struct {
	hc *HeaderChain

	txPool *TxPool
	miner  *Miner

	sliceDb ethdb.Database
	config  *params.ChainConfig
	engine  consensus.Engine

	quit chan struct{} // slice quit channel

	domClient  *quaiclient.Client
	domUrl     string
	subClients []*quaiclient.Client

	wg                    sync.WaitGroup
	scope                 event.SubscriptionScope
	missingBodyFeed       event.Feed
	pendingEtxsFeed       event.Feed
	pendingEtxsRollupFeed event.Feed
	missingParentFeed     event.Feed

	asyncPhCh  chan *types.Header
	asyncPhSub event.Subscription

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
		domUrl:         domClientUrl,
		quit:           make(chan struct{}),
		badHashesCache: make(map[common.Hash]bool),
	}

	var err error
	sl.hc, err = NewHeaderChain(db, engine, chainConfig, cacheConfig, txLookupLimit, vmConfig, slicesRunning)
	if err != nil {
		return nil, err
	}

	sl.validator = NewBlockValidator(chainConfig, sl.hc, engine)

	// tx pool is only used in zone
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.txPool = NewTxPool(*txConfig, chainConfig, sl.hc)
	}
	sl.miner = New(sl.hc, sl.txPool, config, db, chainConfig, engine, isLocalBlock, sl.ProcessingState())

	sl.phCache, _ = lru.New(c_phCacheSize)

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
func (sl *Slice) Append(header *types.Header, domPendingHeader *types.Header, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, bool, error) {
	start := time.Now()

	// Only print in Info level if block is c_startingPrintLimit behind or less
	if sl.CurrentInfo(header) {
		log.Info("Starting slice append", "hash", header.Hash(), "number", header.NumberArray(), "location", header.Location(), "parent hash", header.ParentHash())
	} else {
		log.Debug("Starting slice append", "hash", header.Hash(), "number", header.NumberArray(), "location", header.Location(), "parent hash", header.ParentHash())
	}

	time0_1 := common.PrettyDuration(time.Since(start))
	// Check if the header hash exists in the BadHashes list
	if sl.IsBlockHashABadHash(header.Hash()) {
		return nil, false, ErrBadBlockHash
	}
	time0_2 := common.PrettyDuration(time.Since(start))

	nodeCtx := common.NodeLocation.Context()
	location := header.Location()
	_, order, err := sl.engine.CalcOrder(header)
	if err != nil {
		return nil, false, err
	}
	// Don't append the block which already exists in the database.
	if sl.hc.HasHeader(header.Hash(), header.NumberU64()) && (sl.hc.GetTerminiByHash(header.Hash()) != nil) {
		log.Warn("Block has already been appended: ", "Hash: ", header.Hash())
		return nil, false, ErrKnownBlock
	}
	time1 := common.PrettyDuration(time.Since(start))
	// This is to prevent a crash when we try to insert blocks before domClient is on.
	// Ideally this check should not exist here and should be fixed before we start the slice.
	if sl.domClient == nil && nodeCtx != common.PRIME_CTX {
		return nil, false, ErrDomClientNotUp
	}

	batch := sl.sliceDb.NewBatch()

	// Run Previous Coincident Reference Check (PCRC)
	domTerminus, newTermini, err := sl.pcrc(batch, header, domTerminus, domOrigin)
	if err != nil {
		return nil, false, err
	}
	log.Info("PCRC done", "hash", header.Hash(), "number", header.NumberArray(), "termini", newTermini)

	time2 := common.PrettyDuration(time.Since(start))
	// Append the new block
	err = sl.hc.AppendHeader(header)
	if err != nil {
		return nil, false, err
	}

	time3 := common.PrettyDuration(time.Since(start))
	// Construct the block locally
	block, err := sl.ConstructLocalBlock(header)
	if err != nil {
		return nil, false, err
	}
	time4 := common.PrettyDuration(time.Since(start))

	var pendingHeaderWithTermini types.PendingHeader
	if nodeCtx != common.ZONE_CTX {
		// Upate the local pending header
		pendingHeaderWithTermini, err = sl.generateSlicePendingHeader(block, newTermini, domPendingHeader, domOrigin, false)
		if err != nil {
			return nil, false, err
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
			return nil, false, ErrSubNotSyncedToDom
		}
	}
	time5 := common.PrettyDuration(time.Since(start))

	time6 := common.PrettyDuration(time.Since(start))
	var subPendingEtxs types.Transactions
	var subReorg bool
	var time6_1 common.PrettyDuration
	var time6_2 common.PrettyDuration
	var time6_3 common.PrettyDuration
	// Call my sub to append the block, and collect the rolled up ETXs from that sub
	if nodeCtx != common.ZONE_CTX {
		// How to get the sub pending etxs if not running the full node?.
		if sl.subClients[location.SubIndex()] != nil {
			subPendingEtxs, subReorg, err = sl.subClients[location.SubIndex()].Append(context.Background(), header, pendingHeaderWithTermini.Header(), domTerminus, true, newInboundEtxs)
			if err != nil {
				return nil, false, err
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

	rawdb.WriteBlock(sl.sliceDb, block)
	var time8, time9 common.PrettyDuration
	bestPh, exist := sl.readPhCache(sl.bestPhKey)
	if !exist {
		sl.bestPhKey = sl.config.GenesisHash
		sl.writePhCache(block.Hash(), pendingHeaderWithTermini)
		bestPh = pendingHeaderWithTermini
		log.Error("BestPh Key does not exist for", "key", sl.bestPhKey)
	}
	if nodeCtx == common.ZONE_CTX {
		time8 = common.PrettyDuration(time.Since(start))

		subReorg = sl.miningStrategy(bestPh, block)

		sl.reorgMu.Lock()
		if subReorg {
			sl.hc.SetCurrentHeader(block.Header())
		}
		// Upate the local pending header
		pendingHeaderWithTermini, err = sl.generateSlicePendingHeader(block, newTermini, domPendingHeader, domOrigin, false)
		if err != nil {
			sl.reorgMu.Unlock()
			return nil, false, err
		}
		sl.reorgMu.Unlock()

		time9 = common.PrettyDuration(time.Since(start))

	}
	sl.phCacheMu.Lock()
	defer sl.phCacheMu.Unlock()
	sl.updatePhCache(pendingHeaderWithTermini, true, nil, subReorg)

	var updateDom bool
	if subReorg {
		if pendingHeaderWithTermini.Termini().DomTerminus() != bestPh.Termini().DomTerminus() && order == common.ZONE_CTX {
			updateDom = true
		}
		log.Info("Choosing phHeader Append:", "NumberArray:", pendingHeaderWithTermini.Header().NumberArray(), "Number:", pendingHeaderWithTermini.Header().Number(), "ParentHash:", pendingHeaderWithTermini.Header().ParentHash(), "Terminus:", pendingHeaderWithTermini.Termini().DomTerminus())
		sl.bestPhKey = pendingHeaderWithTermini.Termini().DomTerminus()
		block.SetAppendTime(time.Duration(time9))
	}

	// Append has succeeded write the batch
	if err := batch.Write(); err != nil {
		return nil, false, err
	}

	if subReorg {
		if nodeCtx != common.ZONE_CTX {
			sl.hc.SetCurrentHeader(block.Header())
		}
		sl.hc.chainHeadFeed.Send(ChainHeadEvent{Block: block})
	}

	// Relay the new pendingHeader
	sl.relayPh(block, pendingHeaderWithTermini, domOrigin, block.Location(), subReorg)

	time10 := common.PrettyDuration(time.Since(start))
	log.Info("times during append:", "t0_1", time0_1, "t0_2", time0_2, "t1:", time1, "t2:", time2, "t3:", time3, "t4:", time4, "t5:", time5, "t6:", time6, "t7:", time7, "t8:", time8, "t9:", time9, "t10:", time10)
	log.Info("times during sub append:", "t6_1:", time6_1, "t6_2:", time6_2, "t6_3:", time6_3)
	log.Info("Appended new block", "number", block.Header().Number(), "hash", block.Hash(),
		"uncles", len(block.Uncles()), "txs", len(block.Transactions()), "etxs", len(block.ExtTransactions()), "gas", block.GasUsed(),
		"root", block.Root(),
		"order", order,
		"elapsed", common.PrettyDuration(time.Since(start)))

	if nodeCtx == common.ZONE_CTX {
		if updateDom {
			log.Info("Append updateDom", "oldTermini():", bestPh.Termini().DomTerminus(), "newTermini():", pendingHeaderWithTermini.Termini().DomTerminus(), "location:", common.NodeLocation)
			if sl.domClient != nil {
				go sl.domClient.UpdateDom(context.Background(), bestPh.Termini().DomTerminus(), pendingHeaderWithTermini.Termini().DomTerminus(), common.NodeLocation)
			}
		}
		return block.ExtTransactions(), subReorg, nil
	} else {
		return subPendingEtxs, subReorg, nil
	}
}
func (sl *Slice) miningStrategy(bestPh types.PendingHeader, block *types.Block) bool {
	if bestPh.Header() == nil { // This is the case where we try to append the block before we have not initialized the bestPh
		return true
	}
	subReorg := sl.poem(sl.engine.TotalLogS(block.Header()), bestPh.Header().ParentEntropy())
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
	} else if !domOrigin {
		for _, i := range sl.randomRelayArray() {
			if sl.subClients[i] != nil {
				sl.subClients[i].SubRelayPendingHeader(context.Background(), pendingHeaderWithTermini, location, subReorg)
			}
		}
	}
}

func (sl *Slice) UpdateDom(oldTerminus common.Hash, newTerminus common.Hash, location common.Location) {
	log.Info("Updating Dom...", "oldTerminUs:", oldTerminus, "newTerminus:", newTerminus, "location:", location)
	// Find the dom TerminusHash with the newTerminus
	newDomTermini := sl.hc.GetTerminiByHash(newTerminus)
	oldDomTermini := sl.hc.GetTerminiByHash(oldTerminus)
	if !newDomTermini.IsValid() || !oldDomTermini.IsValid() {
		return
	}
	bestPh, exist := sl.readPhCache(sl.bestPhKey)
	log.Info("UpdateDom:", "oldTerminus:", oldTerminus, "newTerminus:", newTerminus, "oldDomTerminus:", oldDomTermini.DomTerminus(), "newDomTerminus:", newDomTermini.DomTerminus(), "bestPhKey:", sl.bestPhKey, "bestPh Hash:", bestPh.Header().Hash(), "bestPh Number:", bestPh.Header().NumberArray())
	if exist {
		if newTerminus != bestPh.Header().ParentHash() && bestPh.Termini().DomTerminus() == newDomTermini.DomTerminus() && oldDomTermini.DomTerminus() == newDomTermini.DomTerminus() {
			// Can update
			block := sl.hc.GetBlockByHash(newTerminus)
			if block != nil {
				pendingHeaderWithTermini, err := sl.generateSlicePendingHeader(block, *newDomTermini, types.EmptyHeader(), false, false)
				if err != nil {
					return
				}
				log.Info("pendingHeaderWithTermini:", "parent Hash:", pendingHeaderWithTermini.Header().ParentHash(), "Number", pendingHeaderWithTermini.Header().NumberArray())
				for _, i := range sl.randomRelayArray() {
					if sl.subClients[i] != nil {
						go sl.subClients[i].SubRelayPendingHeader(context.Background(), pendingHeaderWithTermini, location, true)
					}
				}
			}
			return
		}
		if oldDomTermini.DomTerminus() != newDomTermini.DomTerminus() {
			// need to update dom
			log.Info("Append need to updateDom", "oldDomTermini:", oldDomTermini, "newDomTermini:", newDomTermini, "location:", common.NodeLocation)
			if sl.domClient != nil {
				go sl.domClient.UpdateDom(context.Background(), oldDomTermini.DomTerminus(), newDomTermini.DomTerminus(), location)
			} else {
				// Can update
				block := sl.hc.GetBlockByHash(newTerminus)
				if block != nil {
					pendingHeaderWithTermini, err := sl.generateSlicePendingHeader(block, *newDomTermini, types.EmptyHeader(), false, false)
					if err != nil {
						return
					}
					log.Info("pendingHeaderWithTermini:", "parent Hash:", pendingHeaderWithTermini.Header().ParentHash(), "Number", pendingHeaderWithTermini.Header().NumberArray())
					for _, i := range sl.randomRelayArray() {
						if sl.subClients[i] != nil {
							go sl.subClients[i].SubRelayPendingHeader(context.Background(), pendingHeaderWithTermini, location, true)
						}
					}
				}
				return
			}
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
			sl.updatePhCache(types.PendingHeader{}, true, asyncPh, true)
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
	}
	return types.PendingHeader{}, false
}

// Write the phCache
func (sl *Slice) writePhCache(hash common.Hash, pendingHeader types.PendingHeader) {
	sl.phCache.Add(hash, pendingHeader)
}

// Generate a slice pending header
func (sl *Slice) generateSlicePendingHeader(block *types.Block, newTermini types.Termini, domPendingHeader *types.Header, domOrigin bool, fill bool) (types.PendingHeader, error) {
	// Upate the local pending header
	localPendingHeader, err := sl.miner.worker.GeneratePendingHeader(block, fill)
	if err != nil {
		return types.PendingHeader{}, err
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

	log.Debug("PCRC:", "Parent Hash:", header.ParentHash(), "Number", header.Number(), "Location:", header.Location())
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
		newTermini.SetDomTerminus(header.Hash())
	}

	// Set the prime termini
	if nodeCtx == common.REGION_CTX && domOrigin {
		newTermini.SetPrimeTerminiAtIndex(header.Hash(), location.SubIndex())
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

	fmt.Println("Prime Termini", "Hash", header.Hash(), "Termini", newTermini.PrimeTermini())

	return termini.SubTerminiAtIndex(location.SubIndex()), newTermini, nil
}

// POEM compares externS to the currentHead S and returns true if externS is greater
func (sl *Slice) poem(externS *big.Int, currentS *big.Int) bool {
	log.Info("POEM:", "Header hash:", sl.hc.CurrentHeader().Hash(), "currentS:", common.BigBitsToBits(currentS), "externS:", common.BigBitsToBits(externS))
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

// SubRelayPendingHeader takes a pending header from the sender (ie dominant), updates the phCache with a composited header and relays result to subordinates
func (sl *Slice) SubRelayPendingHeader(pendingHeader types.PendingHeader, location common.Location, subReorg bool) {
	nodeCtx := common.NodeLocation.Context()
	var err error

	if nodeCtx == common.REGION_CTX {
		// Adding a guard on the region that was already updated in the synchronous path.

		err = sl.updatePhCacheFromDom(pendingHeader, common.NodeLocation.Region(), []int{common.PRIME_CTX}, location.Region() != common.NodeLocation.Region(), subReorg)
		if err != nil {
			return
		}

		for _, i := range sl.randomRelayArray() {
			if sl.subClients[i] != nil {
				if ph, exists := sl.readPhCache(pendingHeader.Termini().SubTerminiAtIndex(common.NodeLocation.Region())); exists {
					sl.subClients[i].SubRelayPendingHeader(context.Background(), ph, location, subReorg)
				}
			}
		}
	} else {
		// This check prevents a double send to the miner.
		// If the previous block on which the given pendingHeader was built is the same as the NodeLocation
		// the pendingHeader update has already been sent to the miner for the given location in relayPh.

		err = sl.updatePhCacheFromDom(pendingHeader, common.NodeLocation.Zone(), []int{common.PRIME_CTX, common.REGION_CTX}, !bytes.Equal(location, common.NodeLocation), subReorg)
		if err != nil {
			return
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
func (sl *Slice) updatePhCacheFromDom(pendingHeader types.PendingHeader, terminiIndex int, indices []int, update bool, subReorg bool) error {
	hash := pendingHeader.Termini().SubTerminiAtIndex(terminiIndex)
	localPendingHeader, exists := sl.readPhCache(hash)

	if exists {
		combinedPendingHeader := types.CopyHeader(localPendingHeader.Header())
		for _, i := range indices {
			combinedPendingHeader = sl.combinePendingHeader(pendingHeader.Header(), combinedPendingHeader, i, false)
		}

		if update {
			sl.updatePhCache(types.NewPendingHeader(combinedPendingHeader, localPendingHeader.Termini()), false, nil, subReorg)
		}

		// Pick the head
		if subReorg {
			log.Info("Choosing phHeader pickPhHead:", "NumberArray:", localPendingHeader.Header().NumberArray(), "Number:", localPendingHeader.Header().Number(), "ParentHash:", localPendingHeader.Header().ParentHash(), "Terminus:", localPendingHeader.Termini().DomTerminus())
			sl.bestPhKey = localPendingHeader.Termini().DomTerminus()
		}
		return nil
	}
	log.Warn("no pending header found for", "terminus", hash, "pendingHeaderNumber", pendingHeader.Header().NumberArray(), "Hash", pendingHeader.Header().ParentHash(), "Termini index", terminiIndex, "indices", indices)
	return errors.New("pending header not found in cache")
}

// updatePhCache updates cache given a pendingHeaderWithTermini with the terminus used as the key.
func (sl *Slice) updatePhCache(pendingHeaderWithTermini types.PendingHeader, inSlice bool, localHeader *types.Header, subReorg bool) {

	var exists bool
	if localHeader != nil {
		termini := sl.hc.GetTerminiByHash(localHeader.ParentHash())

		pendingHeaderWithTermini, exists = sl.readPhCache(termini.DomTerminus())
		if exists {
			pendingHeaderWithTermini.SetHeader(sl.combinePendingHeader(localHeader, pendingHeaderWithTermini.Header(), common.ZONE_CTX, true))
		}
	}

	// Update the pendingHeader Cache
	deepCopyPendingHeaderWithTermini := types.NewPendingHeader(types.CopyHeader(pendingHeaderWithTermini.Header()), pendingHeaderWithTermini.Termini())
	deepCopyPendingHeaderWithTermini.Header().SetLocation(common.NodeLocation)
	deepCopyPendingHeaderWithTermini.Header().SetTime(uint64(time.Now().Unix()))

	_, exists = sl.readPhCache(pendingHeaderWithTermini.Termini().DomTerminus())

	if subReorg || !exists {
		sl.writePhCache(pendingHeaderWithTermini.Termini().DomTerminus(), deepCopyPendingHeaderWithTermini)
		log.Info("PhCache update:", "new terminus?:", !exists, "inSlice:", inSlice, "Ph Number:", deepCopyPendingHeaderWithTermini.Header().NumberArray(), "Termini:", deepCopyPendingHeaderWithTermini.Termini().DomTerminus())
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
		genesisTermini.SetDomTerminus(genesisHash)
		for i := 0; i < len(genesisTermini.SubTermini()); i++ {
			genesisTermini.SetSubTerminiAtIndex(genesisHash, i)
		}
		for i := 0; i < len(genesisTermini.PrimeTermini()); i++ {
			genesisTermini.SetPrimeTerminiAtIndex(genesisHash, i)
		}

		rawdb.WriteTermini(sl.sliceDb, genesisHash, genesisTermini)
		rawdb.WriteManifest(sl.sliceDb, genesisHash, types.BlockManifest{genesisHash})

		// Append each of the knot blocks
		sl.bestPhKey = genesisHash
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

	if index == common.REGION_CTX {
		for i := 0; i < common.NumZonesInRegion; i++ {
			combinedPendingHeader.SetPrimeEntropyThreshold(header.PrimeEntropyThreshold(i), i)
		}
	}

	if inSlice {
		combinedPendingHeader.SetEtxRollupHash(header.EtxRollupHash())
		combinedPendingHeader.SetDifficulty(header.Difficulty())
		combinedPendingHeader.SetRegionEntropyThreshold(header.RegionEntropyThreshold())
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
	genesisTermini.SetDomTerminus(genesisHash)
	for i := 0; i < len(genesisTermini.SubTermini()); i++ {
		genesisTermini.SetSubTerminiAtIndex(genesisHash, i)
	}
	for i := 0; i < len(genesisTermini.PrimeTermini()); i++ {
		genesisTermini.SetPrimeTerminiAtIndex(genesisHash, i)
	}
	if sl.hc.Empty() {
		sl.phCache.Add(sl.config.GenesisHash, types.NewPendingHeader(domPendingHeader, genesisTermini))
	}
}

func (sl *Slice) GetPendingBlockBody(header *types.Header) *types.Body {
	return sl.miner.worker.GetPendingBlockBody(header)
}

func (sl *Slice) SubscribeMissingParentEvent(ch chan<- common.Hash) event.Subscription {
	return sl.scope.Track(sl.missingParentFeed.Subscribe(ch))
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
	phCache := rawdb.ReadPhCache(sl.sliceDb)
	for key, value := range phCache {
		sl.phCache.Add(key, value)
		// Removing the PendingHeaders from the database
		rawdb.DeletePendingHeader(sl.sliceDb, key)
	}
	rawdb.DeletePhCache(sl.sliceDb)
	sl.bestPhKey = rawdb.ReadBestPhKey(sl.sliceDb)
	sl.miner.worker.LoadPendingBlockBody()
	return nil
}

// Stop stores the phCache and the sl.pendingHeader hash value to the db.
func (sl *Slice) Stop() {
	nodeCtx := common.NodeLocation.Context()
	// write the ph head hash to the db.
	rawdb.WriteBestPhKey(sl.sliceDb, sl.bestPhKey)

	// Create a map to write the cache directly into the database
	phCache := make(map[common.Hash]types.PendingHeader)
	for _, hash := range sl.phCache.Keys() {
		if value, exist := sl.phCache.Peek(hash); exist {
			phCache[hash.(common.Hash)] = value.(types.PendingHeader)
		}
	}
	// Write the ph cache to the dd.
	rawdb.WritePhCache(sl.sliceDb, phCache)

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

func (sl *Slice) SubscribeMissingBody(ch chan<- *types.Header) event.Subscription {
	return sl.scope.Track(sl.missingBodyFeed.Subscribe(ch))
}

func (sl *Slice) SubscribePendingEtxs(ch chan<- types.PendingEtxs) event.Subscription {
	return sl.scope.Track(sl.pendingEtxsFeed.Subscribe(ch))
}

func (sl *Slice) SubscribePendingEtxsRollup(ch chan<- types.PendingEtxsRollup) event.Subscription {
	return sl.scope.Track(sl.pendingEtxsRollupFeed.Subscribe(ch))
}

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
	rawdb.DeletePhCache(sl.sliceDb)
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
	sl.bestPhKey = hash
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
