package core

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

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
	"github.com/dominant-strategies/go-quai/trie"
)

const (
	c_maxPendingEtxBatchesPrime       = 3000
	c_maxPendingEtxBatchesRegion      = 1000
	c_maxPendingEtxsRollup            = 256
	c_maxBloomFilters                 = 25
	c_pendingHeaderChacheBufferFactor = 2
	pendingHeaderGCTime               = 5
	c_terminusIndex                   = 3
	c_startingPrintLimit              = 10
	c_regionRelayProc                 = 3
	c_primeRelayProc                  = 10
	c_asyncPhUpdateChanSize           = 10
	c_phCacheSize                     = 50
	c_pEtxRetryThreshold              = 10 // Number of pEtxNotFound return on a dom block before asking for pEtx/Rollup from sub
	c_currentStateComputeWindow       = 20 // Number of blocks around the current header the state generation is always done
	c_inboundEtxCacheSize             = 10 // Number of inboundEtxs to keep in cache so that, we don't recompute it every time dom is processed
	c_appendTimeCacheSize             = 1000
)

// Core will implement the following interface to enable dom-sub communication
type CoreBackend interface {
	AddPendingEtxs(pEtxs types.PendingEtxs) error
	AddPendingEtxsRollup(pEtxRollup types.PendingEtxsRollup) error
	RequestDomToAppendOrFetch(hash common.Hash, entropy *big.Int, order int)
	Append(header *types.WorkObject, manifest types.BlockManifest, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, error)
	DownloadBlocksInManifest(hash common.Hash, manifest types.BlockManifest, entropy *big.Int)
	GenerateRecoveryPendingHeader(pendingHeader *types.WorkObject, checkpointHashes types.Termini) error
	GetPendingEtxsRollupFromSub(hash common.Hash, location common.Location) (types.PendingEtxsRollup, error)
	GetPendingEtxsFromSub(hash common.Hash, location common.Location) (types.PendingEtxs, error)
	NewGenesisPendingHeader(pendingHeader *types.WorkObject, domTerminus common.Hash, hash common.Hash) error
	GetManifest(blockHash common.Hash) (types.BlockManifest, error)
}

type pEtxRetry struct {
	hash    common.Hash
	retries uint64
}

type Slice struct {
	hc *HeaderChain

	txPool        *TxPool
	miner         *Miner
	expansionFeed event.Feed

	sliceDb ethdb.Database
	config  *params.ChainConfig
	engine  consensus.Engine

	quit chan struct{} // slice quit channel

	domInterface CoreBackend
	subInterface []CoreBackend

	wg               sync.WaitGroup
	scope            event.SubscriptionScope
	missingBlockFeed event.Feed

	pEtxRetryCache *lru.Cache[common.Hash, pEtxRetry]
	asyncPhCh      chan *types.WorkObject
	asyncPhSub     event.Subscription

	inboundEtxsCache *lru.Cache[common.Hash, types.Transactions]

	validator Validator // Block and state validator interface

	badHashesCache map[common.Hash]bool
	logger         *log.Logger

	bestPh atomic.Value

	appendTimeCache *lru.Cache[common.Hash, time.Duration]

	recomputeRequired bool
}

func NewSlice(db ethdb.Database, config *Config, txConfig *TxPoolConfig, txLookupLimit *uint64, isLocalBlock func(block *types.WorkObject) bool, chainConfig *params.ChainConfig, slicesRunning []common.Location, currentExpansionNumber uint8, genesisBlock *types.WorkObject, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis, logger *log.Logger) (*Slice, error) {
	nodeCtx := chainConfig.Location.Context()
	sl := &Slice{
		config:            chainConfig,
		engine:            engine,
		sliceDb:           db,
		quit:              make(chan struct{}),
		badHashesCache:    make(map[common.Hash]bool),
		logger:            logger,
		recomputeRequired: false,
	}

	// This only happens during the expansion
	if genesisBlock != nil {
		sl.AddGenesisHash(genesisBlock.Hash())
	}
	var err error
	sl.hc, err = NewHeaderChain(db, engine, sl.GetPEtxRollupAfterRetryThreshold, sl.GetPEtxAfterRetryThreshold, chainConfig, cacheConfig, txLookupLimit, vmConfig, slicesRunning, currentExpansionNumber, logger)
	if err != nil {
		return nil, err
	}

	sl.validator = NewBlockValidator(chainConfig, sl.hc, engine)

	// tx pool is only used in zone
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.txPool = NewTxPool(*txConfig, chainConfig, sl.hc, logger, sl.sliceDb)
		sl.hc.pool = sl.txPool
	}
	sl.miner = New(sl.hc, sl.txPool, config, db, chainConfig, engine, isLocalBlock, sl.ProcessingState(), sl.logger)

	pEtxRetryCache, _ := lru.New[common.Hash, pEtxRetry](c_pEtxRetryThreshold)
	sl.pEtxRetryCache = pEtxRetryCache

	inboundEtxsCache, _ := lru.New[common.Hash, types.Transactions](c_inboundEtxCacheSize)
	sl.inboundEtxsCache = inboundEtxsCache

	appendTimeCache, _ := lru.New[common.Hash, time.Duration](c_appendTimeCacheSize)
	sl.appendTimeCache = appendTimeCache

	sl.subInterface = make([]CoreBackend, common.MaxWidth)

	if err := sl.init(); err != nil {
		return nil, err
	}

	// This only happens during the expansion
	if genesisBlock != nil {
		sl.WriteGenesisBlock(genesisBlock, chainConfig.Location)
	}

	sl.CheckForBadHashAndRecover()

	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		go sl.asyncPendingHeaderLoop()
		go sl.asyncWorkShareUpdateLoop()
	}

	return sl, nil
}

func (sl *Slice) SetDomInterface(domInterface CoreBackend) {
	sl.domInterface = domInterface
}

// Append takes a proposed header and constructs a local block and attempts to hierarchically append it to the block graph.
// If this is called from a dominant context a domTerminus must be provided else a common.Hash{} should be used and domOrigin should be set to true.
// Return of this function is the Etxs generated in the Zone Block, subReorg bool that tells dom if should be mined on, setHead bool that determines if we should set the block as the current head and the error
func (sl *Slice) Append(header *types.WorkObject, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, error) {
	start := time.Now()
	nodeCtx := sl.NodeCtx()

	if sl.hc.IsGenesisHash(header.Hash()) {
		return nil, nil
	}

	// Only print in Info level if block is c_startingPrintLimit behind or less
	if sl.CurrentInfo(header) {
		sl.logger.WithFields(log.Fields{
			"number":      header.NumberArray(),
			"hash":        header.Hash(),
			"location":    header.Location(),
			"parent hash": header.ParentHash(nodeCtx),
		}).Info("Starting slice append")
	} else {
		sl.logger.WithFields(log.Fields{
			"number":      header.NumberArray(),
			"hash":        header.Hash(),
			"location":    header.Location(),
			"parent hash": header.ParentHash(nodeCtx),
		}).Debug("Starting slice append")
	}

	time0_1 := common.PrettyDuration(time.Since(start))
	// Check if the header hash exists in the BadHashes list
	if sl.IsBlockHashABadHash(header.Hash()) {
		return nil, ErrBadBlockHash
	}
	time0_2 := common.PrettyDuration(time.Since(start))

	location := header.Location()
	_, order, err := sl.CalcOrder(header)
	if err != nil {
		return nil, err
	}

	// Don't append the block which already exists in the database.
	if sl.hc.HasHeader(header.Hash(), header.NumberU64(nodeCtx)) && (sl.hc.GetTerminiByHash(header.Hash()) != nil) {
		sl.logger.WithField("hash", header.Hash()).Debug("Block has already been appended")
		return nil, ErrKnownBlock
	}
	time1 := common.PrettyDuration(time.Since(start))

	batch := sl.sliceDb.NewBatch()

	// Run Previous Coincident Reference Check (PCRC)
	domTerminus, newTermini, err := sl.pcrc(batch, header, domTerminus, domOrigin)
	if err != nil {
		return nil, err
	}
	sl.logger.WithFields(log.Fields{
		"hash":    header.Hash(),
		"number":  header.NumberArray(),
		"termini": newTermini,
	}).Debug("PCRC done")

	time2 := common.PrettyDuration(time.Since(start))
	// Append the new block
	err = sl.hc.AppendHeader(header)
	if err != nil {
		return nil, err
	}
	time3 := common.PrettyDuration(time.Since(start))
	// Construct the block locally
	block, err := sl.ConstructLocalBlock(header)
	if err != nil {
		return nil, err
	}
	time4 := common.PrettyDuration(time.Since(start))

	sl.hc.CalculateManifest(block)
	if nodeCtx == common.PRIME_CTX {
		_, err = sl.hc.CalculateInterlink(block)
		if err != nil {
			return nil, err
		}
	}

	if order < nodeCtx {
		// Store the inbound etxs for all dom blocks and use
		// it in the future if dom switch happens
		// This should be pruned at the re-org tolerance depth
		rawdb.WriteInboundEtxs(sl.sliceDb, block.Hash(), newInboundEtxs)

		// After writing the inbound etxs, we need to just send the inbounds for
		// the given block location down in the region
		if nodeCtx == common.REGION_CTX {
			newInboundEtxs = newInboundEtxs.FilterToSub(block.Location(), nodeCtx, order)
		}
	}

	// If this was a coincident block, our dom will be passing us a set of newly
	// confirmed ETXs If this is not a coincident block, we need to build up the
	// list of confirmed ETXs using the subordinate manifest In either case, if
	// we are a dominant node, we need to collect the ETX rollup from our sub.
	if !domOrigin && nodeCtx != common.ZONE_CTX {
		cachedInboundEtxs, exists := sl.inboundEtxsCache.Get(block.Hash())
		if exists && cachedInboundEtxs != nil {
			newInboundEtxs = cachedInboundEtxs
		} else {
			newInboundEtxs, err = sl.CollectNewlyConfirmedEtxs(block, order)
			if err != nil {
				sl.logger.WithField("err", err).Trace("Error collecting newly confirmed etxs")
				// Keeping track of the number of times pending etx fails and if it crossed the retry threshold
				// ask the sub for the pending etx/rollup data
				val, exist := sl.pEtxRetryCache.Get(block.Hash())
				var retry uint64
				if exist {
					pEtxCurrent := val
					retry = pEtxCurrent.retries + 1
				}
				pEtxNew := pEtxRetry{hash: block.Hash(), retries: retry}
				sl.pEtxRetryCache.Add(block.Hash(), pEtxNew)
				return nil, ErrSubNotSyncedToDom
			}
			sl.inboundEtxsCache.Add(block.Hash(), newInboundEtxs)
		}
	}

	time5 := common.PrettyDuration(time.Since(start))
	var subPendingEtxs types.Transactions
	var time5_1 common.PrettyDuration
	var time5_2 common.PrettyDuration
	var time5_3 common.PrettyDuration
	// Call my sub to append the block, and collect the rolled up ETXs from that sub
	if nodeCtx != common.ZONE_CTX {
		// How to get the sub pending etxs if not running the full node?.
		if sl.subInterface[location.SubIndex(sl.NodeCtx())] != nil {
			subPendingEtxs, err = sl.subInterface[location.SubIndex(sl.NodeCtx())].Append(header, block.Manifest(), domTerminus, true, newInboundEtxs)
			if err != nil {
				return nil, err
			}
			time5_1 = common.PrettyDuration(time.Since(start))
			// Cache the subordinate's pending ETXs
			pEtxs := types.PendingEtxs{header.ConvertToPEtxView(), subPendingEtxs}
			time5_2 = common.PrettyDuration(time.Since(start))
			// Add the pending etx given by the sub in the rollup
			sl.AddPendingEtxs(pEtxs)
			// Only region has the rollup hashes for pendingEtxs
			if nodeCtx == common.REGION_CTX {
				crossPrimeRollup := types.Transactions{}
				subRollup, err := sl.hc.CollectSubRollup(block)
				if err != nil {
					return nil, err
				}
				for _, etx := range subRollup {
					to := etx.To().Location()
					if to.Region() != sl.NodeLocation().Region() || types.IsConversionTx(etx) || types.IsCoinBaseTx(etx) {
						crossPrimeRollup = append(crossPrimeRollup, etx)
					}
				}
				// Rolluphash is specifically for zone rollup, which can only be validated by region
				if nodeCtx == common.REGION_CTX {
					if etxRollupHash := types.DeriveSha(crossPrimeRollup, trie.NewStackTrie(nil)); etxRollupHash != block.EtxRollupHash() {
						return nil, errors.New("sub rollup does not match sub rollup hash")
					}
				}
				// We also need to store the pendingEtxRollup to the dom
				pEtxRollup := types.PendingEtxsRollup{header.ConvertToPEtxView(), crossPrimeRollup}
				sl.AddPendingEtxsRollup(pEtxRollup)
			}
			time5_3 = common.PrettyDuration(time.Since(start))
		}
	}

	time6 := common.PrettyDuration(time.Since(start))

	// Append has succeeded write the batch
	if err := batch.Write(); err != nil {
		return nil, err
	}

	time7 := common.PrettyDuration(time.Since(start))

	if order == sl.NodeCtx() {
		sl.hc.bc.chainFeed.Send(ChainEvent{Block: block, Hash: block.Hash(), Order: order, Entropy: sl.engine.TotalLogEntropy(sl.hc, block)})
	}

	time8 := common.PrettyDuration(time.Since(start))
	// If efficiency score changed on this block compared to the parent block
	// we trigger the expansion using the expansion feed
	if nodeCtx == common.PRIME_CTX {
		parent := sl.hc.GetHeaderByHash(block.ParentHash(nodeCtx))
		if header.ExpansionNumber() > parent.ExpansionNumber() {
			sl.expansionFeed.Send(ExpansionEvent{block})
		}
	}

	time9 := common.PrettyDuration(time.Since(start))
	sl.logger.WithFields(log.Fields{
		"t0_1": time0_1,
		"t0_2": time0_2,
		"t1":   time1,
		"t2":   time2,
		"t3":   time3,
		"t4":   time4,
		"t5":   time5,
		"t6":   time6,
		"t7":   time7,
		"t8":   time8,
		"t9":   time9,
	}).Info("Times during append")

	// store the append time for the block
	appendTime := time.Since(start)
	sl.appendTimeCache.Add(block.Hash(), appendTime)

	sl.logger.WithFields(log.Fields{
		"t5_1": time5_1,
		"t5_2": time5_2,
		"t5_3": time5_3,
	}).Info("Times during sub append")

	intrinsicS := sl.engine.IntrinsicLogEntropy(block.Hash())
	workShare, err := sl.engine.WorkShareLogEntropy(sl.hc, block)
	if err != nil {
		workShare = big.NewInt(0)
	}

	if nodeCtx == common.ZONE_CTX {
		sl.logger.WithFields(block.TransactionsInfo()).Info("Transactions info for Block")
	}

	sl.logger.WithFields(log.Fields{
		"number":               block.NumberArray(),
		"hash":                 block.Hash(),
		"difficulty":           block.Difficulty(),
		"uncles":               len(block.Uncles()),
		"totalTxs":             len(block.Transactions()),
		"iworkShare":           common.BigBitsToBitsFloat(workShare),
		"intrinsicS":           common.BigBitsToBits(intrinsicS),
		"inboundEtxs from dom": len(newInboundEtxs),
		"gas":                  block.GasUsed(),
		"gasLimit":             block.GasLimit(),
		"evmRoot":              block.EVMRoot(),
		"utxoRoot":             block.UTXORoot(),
		"etxSetRoot":           block.EtxSetRoot(),
		"order":                order,
		"location":             block.Location(),
		"elapsed":              common.PrettyDuration(time.Since(start)),
	}).Info("Appended new block")

	if nodeCtx == common.ZONE_CTX {
		return block.OutboundEtxs(), nil
	} else {
		return subPendingEtxs, nil
	}
}

func (sl *Slice) ProcessingState() bool {
	return sl.hc.ProcessingState()
}

func (sl *Slice) randomRelayArray() []int {
	length := len(sl.subInterface)
	nums := []int{}
	for i := 0; i < length; i++ {
		nums = append(nums, i)
	}
	for i := length - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		nums[i], nums[j] = nums[j], nums[i]
	}
	return nums
}

// asyncPendingHeaderLoop waits for the pendingheader updates from the worker and updates the phCache
func (sl *Slice) asyncPendingHeaderLoop() {
	defer func() {
		if r := recover(); r != nil {
			sl.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()

	// Subscribe to the AsyncPh updates from the worker
	sl.asyncPhCh = make(chan *types.WorkObject, c_asyncPhUpdateChanSize)
	sl.asyncPhSub = sl.miner.worker.SubscribeAsyncPendingHeader(sl.asyncPhCh)

	for {
		select {
		case asyncPh := <-sl.asyncPhCh:
			sl.hc.headermu.Lock()
			bestPh := sl.ReadBestPh()
			if asyncPh != nil && bestPh != nil && bestPh.ParentHash(common.ZONE_CTX) == asyncPh.ParentHash(common.ZONE_CTX) {
				combinedPendingHeader := sl.combinePendingHeader(asyncPh, bestPh, common.ZONE_CTX, true)
				sl.SetBestPh(combinedPendingHeader)
			}
			sl.hc.headermu.Unlock()
		case <-sl.asyncPhSub.Err():
			return
		case <-sl.quit:
			return
		}
	}
}

func (sl *Slice) WriteBestPh(bestPh *types.WorkObject) {
	bestPhCopy := types.CopyWorkObject(bestPh)
	sl.bestPh.Store(bestPhCopy)
}

func (sl *Slice) ReadBestPh() *types.WorkObject {
	if sl.bestPh.Load() != nil {
		return sl.bestPh.Load().(*types.WorkObject)
	} else {
		return nil
	}
}

// CollectNewlyConfirmedEtxs collects all newly confirmed ETXs since the last coincident with the given location
func (sl *Slice) CollectNewlyConfirmedEtxs(block *types.WorkObject, blockOrder int) (types.Transactions, error) {
	nodeCtx := sl.NodeCtx()
	blockLocation := block.Location()
	// Collect rollup of ETXs from the subordinate node's manifest
	subRollup := types.Transactions{}
	var err error
	if nodeCtx < common.ZONE_CTX {
		rollup, exists := sl.hc.subRollupCache.Get(block.Hash())
		if exists && rollup != nil {
			subRollup = rollup
			sl.logger.WithFields(log.Fields{
				"Hash": block.Hash(),
				"len":  len(subRollup),
			}).Debug("Found the rollup in cache")
		} else {
			subRollup, err = sl.hc.CollectSubRollup(block)
			if err != nil {
				return nil, err
			}
			sl.hc.subRollupCache.Add(block.Hash(), subRollup)
		}
	}
	// Filter for ETXs destined to this sub
	newInboundEtxs := subRollup.FilterToSub(blockLocation, sl.NodeCtx(), blockOrder)
	newlyConfirmedEtxs := newInboundEtxs
	for {
		ancHash := block.ParentHash(nodeCtx)
		ancNum := block.NumberU64(nodeCtx) - 1
		parent := sl.hc.GetBlock(ancHash, ancNum)
		if parent == nil {
			return nil, fmt.Errorf("unable to find parent, hash: %s", ancHash.String())
		}

		if sl.hc.IsGenesisHash(ancHash) {
			break
		}

		// Terminate the search if the slice to which the given block belongs was
		// not activated yet, expansion number is validated on prime block, so only
		// terminate on prime block, It is possible to make this check more
		// optimized but this is performant enough and in some cases might have to
		// go through few more region blocks than necessary
		var err error
		_, order, err := sl.CalcOrder(parent)
		if err != nil {
			return nil, err
		}
		regions, zones := common.GetHierarchySizeForExpansionNumber(parent.ExpansionNumber())
		if order == common.PRIME_CTX && (blockLocation.Region() > int(regions) || blockLocation.Zone() > int(zones)) {
			break
		}

		// Terminate the search when we find a block produced by the same sub
		if parent.Location().SubIndex(sl.NodeCtx()) == blockLocation.SubIndex(sl.NodeCtx()) && order == nodeCtx {
			break
		}

		subRollup := types.Transactions{}
		if nodeCtx < common.ZONE_CTX {
			rollup, exists := sl.hc.subRollupCache.Get(parent.Hash())
			if exists && rollup != nil {
				subRollup = rollup
				sl.logger.WithFields(log.Fields{
					"Hash": parent.Hash(),
					"len":  len(subRollup),
				}).Debug("Found the rollup in cache")
			} else {
				subRollup, err = sl.hc.CollectSubRollup(parent)
				if err != nil {
					return nil, err
				}
				sl.hc.subRollupCache.Add(parent.Hash(), subRollup)
			}
		}
		// change for the rolldown feature is that in the region when we go
		// back, if we find a prime block, we have to take the transactions that
		// are going towards the given zone
		if nodeCtx == common.REGION_CTX && order < nodeCtx && !blockLocation.Equal(parent.Location()) {
			inboundEtxs := rawdb.ReadInboundEtxs(sl.sliceDb, parent.Hash())
			// Filter the Conversion and Coinbase Txs that needs to go the blockLocation
			filteredInboundEtxs := inboundEtxs.FilterToSub(blockLocation, sl.NodeCtx(), common.PRIME_CTX)
			newlyConfirmedEtxs = append(newlyConfirmedEtxs, filteredInboundEtxs...)
		}

		ancInboundEtxs := subRollup.FilterToSub(blockLocation, sl.NodeCtx(), blockOrder)
		newlyConfirmedEtxs = append(newlyConfirmedEtxs, ancInboundEtxs...)
		block = parent
	}
	return newlyConfirmedEtxs, nil
}

// PCRC previous coincidence reference check makes sure there are not any cyclic references in the graph and calculates new termini and the block terminus
func (sl *Slice) pcrc(batch ethdb.Batch, header *types.WorkObject, domTerminus common.Hash, domOrigin bool) (common.Hash, types.Termini, error) {
	nodeLocation := sl.NodeLocation()
	nodeCtx := sl.NodeCtx()
	location := header.Location()

	sl.logger.WithFields(log.Fields{
		"parent hash": header.ParentHash(nodeCtx),
		"number":      header.NumberArray(),
		"location":    header.Location(),
	}).Debug("PCRC")
	termini := sl.hc.GetTerminiByHash(header.ParentHash(nodeCtx))

	if !termini.IsValid() {
		return common.Hash{}, types.EmptyTermini(), ErrSubNotSyncedToDom
	}

	newTermini := types.CopyTermini(*termini)
	// Set the subtermini
	if nodeCtx != common.ZONE_CTX {
		newTermini.SetSubTerminiAtIndex(header.Hash(), location.SubIndex(nodeCtx))
	}

	// Set the terminus
	if nodeCtx == common.PRIME_CTX || domOrigin {
		newTermini.SetDomTerminiAtIndex(header.Hash(), location.DomIndex(nodeLocation))
	} else {
		newTermini.SetDomTerminiAtIndex(termini.DomTerminus(nodeLocation), location.DomIndex(nodeLocation))
	}

	// Check for a graph cyclic reference
	if domOrigin {
		if !sl.hc.IsGenesisHash(termini.DomTerminus(nodeLocation)) && termini.DomTerminus(nodeLocation) != domTerminus {
			sl.logger.WithFields(log.Fields{
				"block number": header.NumberArray(),
				"hash":         header.Hash(),
				"terminus":     domTerminus,
				"termini":      termini.DomTerminus(nodeLocation),
			}).Warn("Cyclic block")
			return common.Hash{}, types.EmptyTermini(), errors.New("termini do not match, block rejected due to cyclic reference")
		}
	}

	//Save the termini
	rawdb.WriteTermini(batch, header.Hash(), newTermini)

	if nodeCtx == common.ZONE_CTX {
		return common.Hash{}, newTermini, nil
	}

	return termini.SubTerminiAtIndex(location.SubIndex(nodeCtx)), newTermini, nil
}

// GetPendingHeader is used by the miner to request the current pending header
func (sl *Slice) GetPendingHeader() (*types.WorkObject, error) {
	phCopy := types.CopyWorkObject(sl.ReadBestPh())
	return phCopy, nil
}

func (sl *Slice) SetBestPh(pendingHeader *types.WorkObject) {
	pendingHeader.WorkObjectHeader().SetLocation(sl.NodeLocation())
	pendingHeader.WorkObjectHeader().SetTime(uint64(time.Now().Unix()))
	pendingHeader.WorkObjectHeader().SetHeaderHash(pendingHeader.Header().Hash())
	sl.miner.worker.AddPendingWorkObjectBody(pendingHeader)
	rawdb.WriteBestPendingHeader(sl.sliceDb, pendingHeader)
	sl.WriteBestPh(pendingHeader)
	sl.logger.WithFields(log.Fields{"Number": pendingHeader.NumberArray(), "ParentHash": pendingHeader.ParentHashArray()}).Info("Best PH pick")
	sl.miner.worker.pendingHeaderFeed.Send(pendingHeader)
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
	subIdx := slice.SubIndex(sl.NodeCtx())
	if sl.subInterface[subIdx] == nil {
		return nil, errors.New("missing requested subordinate node")
	}
	return sl.subInterface[subIdx].GetManifest(blockHash)
}

// SendPendingEtxsToDom shares a set of pending ETXs with your dom, so he can reference them when a coincident block is found
func (sl *Slice) SendPendingEtxsToDom(pEtxs types.PendingEtxs) error {
	return sl.domInterface.AddPendingEtxs(pEtxs)
}

func (sl *Slice) GetPEtxRollupAfterRetryThreshold(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	pEtx, exists := sl.pEtxRetryCache.Get(blockHash)
	if !exists || pEtx.retries < c_pEtxRetryThreshold {
		return types.PendingEtxsRollup{}, ErrPendingEtxNotFound
	}
	return sl.GetPendingEtxsRollupFromSub(hash, location)
}

// GetPendingEtxsRollupFromSub gets the pending etxs rollup from the appropriate prime
func (sl *Slice) GetPendingEtxsRollupFromSub(hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	nodeCtx := sl.NodeLocation().Context()
	if nodeCtx == common.PRIME_CTX {
		if sl.subInterface[location.SubIndex(sl.NodeCtx())] != nil {
			pEtxRollup, err := sl.subInterface[location.SubIndex(sl.NodeCtx())].GetPendingEtxsRollupFromSub(hash, location)
			if err != nil {
				return types.PendingEtxsRollup{}, err
			} else {
				sl.AddPendingEtxsRollup(pEtxRollup)
				return pEtxRollup, nil
			}
		}
	} else if nodeCtx == common.REGION_CTX {
		block := sl.hc.GetBlockByHash(hash)
		if block != nil {
			subRollup, err := sl.hc.CollectSubRollup(block)
			if err != nil {
				return types.PendingEtxsRollup{}, err
			}
			return types.PendingEtxsRollup{Header: block.ConvertToPEtxView(), EtxsRollup: subRollup}, nil
		}
	}
	return types.PendingEtxsRollup{}, ErrPendingEtxNotFound
}

func (sl *Slice) GetPEtxAfterRetryThreshold(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	pEtx, exists := sl.pEtxRetryCache.Get(blockHash)
	if !exists || pEtx.retries < c_pEtxRetryThreshold {
		return types.PendingEtxs{}, ErrPendingEtxNotFound
	}
	return sl.GetPendingEtxsFromSub(hash, location)
}

// GetPendingEtxsFromSub gets the pending etxs from the appropriate prime
func (sl *Slice) GetPendingEtxsFromSub(hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	nodeCtx := sl.NodeLocation().Context()
	if nodeCtx != common.ZONE_CTX {
		if sl.subInterface[location.SubIndex(sl.NodeCtx())] != nil {
			pEtx, err := sl.subInterface[location.SubIndex(sl.NodeCtx())].GetPendingEtxsFromSub(hash, location)
			if err != nil {
				return types.PendingEtxs{}, err
			} else {
				sl.AddPendingEtxs(pEtx)
				return pEtx, nil
			}
		}
	}
	block := sl.hc.GetBlockByHash(hash)
	if block != nil {
		return types.PendingEtxs{Header: block.ConvertToPEtxView(), OutboundEtxs: block.OutboundEtxs()}, nil
	}
	return types.PendingEtxs{}, ErrPendingEtxNotFound
}

// init checks if the headerchain is empty and if it's empty appends the Knot
// otherwise loads the last stored state of the chain.
func (sl *Slice) init() error {

	// Even though the genesis block cannot have any ETXs, we still need an empty
	// pending ETX entry for that block hash, so that the state processor can build
	// on it
	genesisHash := sl.hc.GetGenesisHashes()[0]
	genesisHeader := sl.hc.GetHeaderByHash(genesisHash)
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

		// Create empty pending ETX entry for genesis block -- genesis may not emit ETXs
		emptyPendingEtxs := types.Transactions{}
		rawdb.WritePendingEtxs(sl.sliceDb, types.PendingEtxs{Header: genesisHeader, OutboundEtxs: emptyPendingEtxs})
		rawdb.WritePendingEtxsRollup(sl.sliceDb, types.PendingEtxsRollup{Header: genesisHeader, EtxsRollup: emptyPendingEtxs})
		err := sl.hc.AddBloom(types.Bloom{}, genesisHeader.Hash())
		if err != nil {
			return err
		}
		// This is just done for the startup process
		sl.hc.SetCurrentHeader(genesisHeader)

		if sl.NodeLocation().Context() == common.PRIME_CTX {
			go sl.NewGenesisPendingHeader(nil, genesisHash, genesisHash)
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
func (sl *Slice) ConstructLocalBlock(header *types.WorkObject) (*types.WorkObject, error) {
	block := rawdb.ReadWorkObject(sl.sliceDb, header.NumberU64(sl.NodeCtx()), header.Hash(), types.BlockObject)
	if block == nil {
		return nil, ErrBodyNotFound
	}
	if err := sl.validator.ValidateBody(block); err != nil {
		return block, err
	} else {
		return block, nil
	}
}

// constructLocalMinedBlock takes a header and construct the Block locally by getting the block
// body from the workers pendingBlockBodyCache. This method is used when the miner sends in the
// header.
func (sl *Slice) ConstructLocalMinedBlock(wo *types.WorkObject) (*types.WorkObject, error) {
	nodeCtx := sl.NodeLocation().Context()
	var pendingBlockBody *types.WorkObject
	if nodeCtx == common.ZONE_CTX {
		pendingBlockBody = sl.GetPendingBlockBody(wo.WorkObjectHeader())
		if pendingBlockBody == nil {
			sl.logger.WithFields(log.Fields{"wo.Hash": wo.Hash(),
				"wo.Header":       wo.HeaderHash(),
				"wo.ParentHash":   wo.ParentHash(common.ZONE_CTX),
				"wo.Difficulty()": wo.Difficulty(),
				"wo.Location()":   wo.Location(),
			}).Error("Pending Block Body not found")
			// we need to recompute another body at the current state
			sl.hc.chainHeadFeed.Send(ChainHeadEvent{sl.hc.CurrentHeader()})
			return nil, ErrBodyNotFound
		}
		if len(pendingBlockBody.OutboundEtxs()) == 0 {
			sl.logger.WithFields(log.Fields{"wo.Hash": wo.Hash(),
				"wo.Header":       wo.HeaderHash(),
				"wo.ParentHash":   wo.ParentHash(common.ZONE_CTX),
				"wo.Difficulty()": wo.Difficulty(),
				"wo.Location()":   wo.Location(),
			}).Error("Pending Block Body has no transactions")
			return nil, ErrBodyNotFound
		}
	} else {
		// If the context is PRIME, there is the interlink hashes that needs to be returned from the database
		var interlinkHashes common.Hashes
		if nodeCtx == common.PRIME_CTX {
			interlinkHashes = rawdb.ReadInterlinkHashes(sl.sliceDb, wo.ParentHash(common.PRIME_CTX))
		}
		wo.Body().SetUncles(nil)
		wo.Body().SetTransactions(nil)
		wo.Body().SetOutboundEtxs(nil)
		wo.Body().SetInterlinkHashes(interlinkHashes)
		pendingBlockBody = types.NewWorkObject(wo.WorkObjectHeader(), wo.Body(), nil)
	}
	// Load uncles because they are not included in the block response.
	txs := make([]*types.Transaction, len(pendingBlockBody.Transactions()))
	for i, tx := range pendingBlockBody.Transactions() {
		txs[i] = tx
	}
	uncles := make([]*types.WorkObjectHeader, len(pendingBlockBody.Uncles()))
	for i, uncle := range pendingBlockBody.Uncles() {
		uncles[i] = uncle
		sl.logger.WithField("hash", uncle.Hash()).Debug("Pending Block uncle")
	}
	etxs := make(types.Transactions, len(pendingBlockBody.OutboundEtxs()))
	for i, etx := range pendingBlockBody.OutboundEtxs() {
		etxs[i] = etx
	}
	subManifest := make(types.BlockManifest, len(pendingBlockBody.Manifest()))
	for i, blockHash := range pendingBlockBody.Manifest() {
		subManifest[i] = blockHash
	}
	interlinkHashes := make(common.Hashes, len(pendingBlockBody.InterlinkHashes()))
	for i, interlinkhash := range pendingBlockBody.InterlinkHashes() {
		interlinkHashes[i] = interlinkhash
	}
	pendingBlockBody.Body().SetTransactions(txs)
	pendingBlockBody.Body().SetUncles(uncles)
	pendingBlockBody.Body().SetOutboundEtxs(etxs)
	pendingBlockBody.Body().SetManifest(subManifest)
	pendingBlockBody.Body().SetInterlinkHashes(interlinkHashes)
	block := types.NewWorkObject(wo.WorkObjectHeader(), pendingBlockBody.Body(), nil)
	if nodeCtx != common.ZONE_CTX {
		subManifestHash := types.DeriveSha(block.Manifest(), trie.NewStackTrie(nil))
		if subManifestHash == types.EmptyRootHash || subManifestHash != block.ManifestHash(nodeCtx+1) {
			// If we have a subordinate chain, it is impossible for the subordinate manifest to be empty
			return block, ErrBadSubManifest
		}
	}
	return block, nil
}

// combinePendingHeader updates the pending header at the given index with the value from given header.
func (sl *Slice) combinePendingHeader(header *types.WorkObject, slPendingHeader *types.WorkObject, index int, inSlice bool) *types.WorkObject {
	// copying the slPendingHeader and updating the copy to remove any shared memory access issues
	combinedPendingHeader := types.CopyWorkObject(slPendingHeader)

	combinedPendingHeader.SetParentHash(header.ParentHash(index), index)
	combinedPendingHeader.SetNumber(header.Number(index), index)
	combinedPendingHeader.Header().SetManifestHash(header.ManifestHash(index), index)
	combinedPendingHeader.Header().SetParentEntropy(header.ParentEntropy(index), index)
	combinedPendingHeader.Header().SetParentDeltaEntropy(header.ParentDeltaEntropy(index), index)
	combinedPendingHeader.Header().SetParentUncledDeltaEntropy(header.ParentUncledDeltaEntropy(index), index)

	if index == common.PRIME_CTX {
		combinedPendingHeader.Header().SetEfficiencyScore(header.EfficiencyScore())
		combinedPendingHeader.Header().SetThresholdCount(header.ThresholdCount())
		combinedPendingHeader.Header().SetExpansionNumber(header.ExpansionNumber())
		combinedPendingHeader.Header().SetEtxEligibleSlices(header.EtxEligibleSlices())
		combinedPendingHeader.Header().SetInterlinkRootHash(header.InterlinkRootHash())
		combinedPendingHeader.Header().SetExchangeRate(header.ExchangeRate())
		combinedPendingHeader.Header().SetQiToQuai(header.QiToQuai())
		combinedPendingHeader.Header().SetQuaiToQi(header.QuaiToQi())
	}

	if inSlice {
		combinedPendingHeader.WorkObjectHeader().SetDifficulty(header.Difficulty())
		combinedPendingHeader.WorkObjectHeader().SetTxHash(header.TxHash())
		combinedPendingHeader.WorkObjectHeader().SetPrimeTerminusNumber(header.PrimeTerminusNumber())
		combinedPendingHeader.WorkObjectHeader().SetLock(header.Lock())
		combinedPendingHeader.WorkObjectHeader().SetPrimaryCoinbase(header.PrimaryCoinbase())

		combinedPendingHeader.Header().SetEtxRollupHash(header.EtxRollupHash())
		combinedPendingHeader.Header().SetUncledEntropy(header.Header().UncledEntropy())
		combinedPendingHeader.Header().SetQuaiStateSize(header.Header().QuaiStateSize())
		combinedPendingHeader.Header().SetUncleHash(header.UncleHash())
		combinedPendingHeader.Header().SetTxHash(header.Header().TxHash())
		combinedPendingHeader.Header().SetOutboundEtxHash(header.OutboundEtxHash())
		combinedPendingHeader.Header().SetEtxSetRoot(header.EtxSetRoot())
		combinedPendingHeader.Header().SetReceiptHash(header.ReceiptHash())
		combinedPendingHeader.Header().SetEVMRoot(header.EVMRoot())
		combinedPendingHeader.Header().SetUTXORoot(header.UTXORoot())
		combinedPendingHeader.Header().SetEtxSetRoot(header.EtxSetRoot())
		combinedPendingHeader.Header().SetBaseFee(header.BaseFee())
		combinedPendingHeader.Header().SetStateLimit(header.StateLimit())
		combinedPendingHeader.Header().SetStateUsed(header.StateUsed())
		combinedPendingHeader.Header().SetGasLimit(header.GasLimit())
		combinedPendingHeader.Header().SetGasUsed(header.GasUsed())
		combinedPendingHeader.Header().SetExtra(header.Extra())
		combinedPendingHeader.Header().SetPrimeTerminusHash(header.PrimeTerminusHash())
		combinedPendingHeader.Header().SetSecondaryCoinbase(header.SecondaryCoinbase())

		combinedPendingHeader.Body().SetTransactions(header.Transactions())
		combinedPendingHeader.Body().SetOutboundEtxs(header.OutboundEtxs())
		combinedPendingHeader.Body().SetUncles(header.Uncles())
		combinedPendingHeader.Body().SetManifest(header.Manifest())
	}

	return combinedPendingHeader
}

func (sl *Slice) IsSubClientsEmpty() bool {
	activeRegions, _ := common.GetHierarchySizeForExpansionNumber(sl.hc.currentExpansionNumber)
	switch sl.NodeCtx() {
	case common.PRIME_CTX:
		for i := 0; i < int(activeRegions); i++ {
			if sl.subInterface[i] == nil {
				return true
			}
		}
	case common.REGION_CTX:
		for _, slice := range sl.ActiveSlices() {
			if sl.subInterface[slice.Zone()] == nil {
				return true
			}
		}
	}
	return false
}

// ActiveSlices returns the active slices for the current expansion number
func (sl *Slice) ActiveSlices() []common.Location {
	currentRegions, currentZones := common.GetHierarchySizeForExpansionNumber(sl.hc.currentExpansionNumber)
	activeSlices := []common.Location{}
	for i := 0; i < int(currentRegions); i++ {
		for j := 0; j < int(currentZones); j++ {
			activeSlices = append(activeSlices, common.Location{byte(i), byte(j)})
		}
	}
	return activeSlices
}

func (sl *Slice) WriteGenesisBlock(block *types.WorkObject, location common.Location) {
	rawdb.WriteManifest(sl.sliceDb, block.Hash(), types.BlockManifest{block.Hash()})
	// Create empty pending ETX entry for genesis block -- genesis may not emit ETXs
	emptyPendingEtxs := types.Transactions{}
	sl.hc.AddPendingEtxs(types.PendingEtxs{block, emptyPendingEtxs})
	sl.AddPendingEtxsRollup(types.PendingEtxsRollup{block, emptyPendingEtxs})
	sl.hc.AddBloom(types.Bloom{}, block.Hash())
	sl.hc.currentHeader.Store(block)
}

// NewGenesisPendingHeader creates a pending header on the genesis block
func (sl *Slice) NewGenesisPendingHeader(domPendingHeader *types.WorkObject, domTerminus common.Hash, genesisHash common.Hash) error {
	defer func() {
		if r := recover(); r != nil {
			sl.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	nodeCtx := sl.NodeLocation().Context()

	if nodeCtx == common.ZONE_CTX && !sl.hc.Empty() {
		return nil
	}
	// Wait until the subclients are all initialized
	if nodeCtx != common.ZONE_CTX {
		for sl.IsSubClientsEmpty() {
			if !sl.IsSubClientsEmpty() {
				break
			}
		}
	}

	// get the genesis block to start from
	genesisBlock := sl.hc.GetBlockByHash(genesisHash)
	genesisTermini := types.EmptyTermini()
	for i := 0; i < len(genesisTermini.SubTermini()); i++ {
		genesisTermini.SetSubTerminiAtIndex(genesisHash, i)
	}
	for i := 0; i < len(genesisTermini.DomTermini()); i++ {
		genesisTermini.SetDomTerminiAtIndex(genesisHash, i)
	}

	// Upate the local pending header
	var localPendingHeader *types.WorkObject
	var err error
	var termini types.Termini
	sl.logger.Infof("NewGenesisPendingHeader location: %v, genesis hash %s", sl.NodeLocation(), genesisHash)
	if sl.hc.IsGenesisHash(genesisHash) {
		localPendingHeader, err = sl.miner.worker.GeneratePendingHeader(genesisBlock, false, nil, false)
		if err != nil {
			sl.logger.WithFields(log.Fields{
				"err": err,
			}).Warn("Error generating the New Genesis Pending Header")
			return nil
		}
		termini = genesisTermini

		if nodeCtx != common.ZONE_CTX {
			localPendingHeader.WorkObjectHeader().SetPrimaryCoinbase(common.Zero)
			localPendingHeader.Body().Header().SetSecondaryCoinbase(common.Zero)
		}

		if nodeCtx == common.PRIME_CTX {
			domPendingHeader = types.CopyWorkObject(localPendingHeader)
		} else {
			domPendingHeader = sl.combinePendingHeader(localPendingHeader, domPendingHeader, nodeCtx, true)
			domPendingHeader.WorkObjectHeader().SetLocation(sl.NodeLocation())
		}

		if nodeCtx != common.ZONE_CTX {
			for i, client := range sl.subInterface {
				if client != nil {
					go func(client CoreBackend) {
						err = client.NewGenesisPendingHeader(domPendingHeader, termini.SubTerminiAtIndex(i), genesisHash)
						if err != nil {
							sl.logger.Error("Error in NewGenesisPendingHeader, err", err)
						}
					}(client)
				}
			}
		}

		domPendingHeader.WorkObjectHeader().SetTime(uint64(time.Now().Unix()))
		sl.SetBestPh(domPendingHeader)
	}
	return nil
}

func (sl *Slice) MakeFullPendingHeader(primePendingHeader, regionPendingHeader, zonePendingHeader *types.WorkObject) *types.WorkObject {
	combinedPendingHeader := sl.combinePendingHeader(regionPendingHeader, primePendingHeader, common.REGION_CTX, true)
	combinedPendingHeader = sl.combinePendingHeader(zonePendingHeader, combinedPendingHeader, common.ZONE_CTX, true)
	sl.SetBestPh(combinedPendingHeader)
	return combinedPendingHeader
}

func (sl *Slice) GeneratePendingHeader(block *types.WorkObject, fill bool) (*types.WorkObject, error) {
	sl.hc.headermu.Lock()

	sl.logger.WithFields(log.Fields{
		"number": block.NumberArray(),
		"hash":   block.Hash(),
		"fill":   fill,
	}).Info("GeneratePendingHeader")
	start := time.Now()
	// set the current header to this block
	switch sl.NodeCtx() {
	case common.PRIME_CTX:
		err := sl.hc.SetCurrentHeader(block)
		if err != nil {
			sl.logger.WithFields(log.Fields{"hash": block.Hash(), "err": err}).Warn("Error setting current header")
			sl.recomputeRequired = true
			sl.hc.headermu.Unlock()
			return nil, err
		}
	case common.REGION_CTX:
		err := sl.hc.SetCurrentHeader(block)
		if err != nil {
			sl.logger.WithFields(log.Fields{"hash": block.Hash(), "err": err}).Warn("Error setting current header")
			sl.recomputeRequired = true
			sl.hc.headermu.Unlock()
			return nil, err
		}
	case common.ZONE_CTX:
		err := sl.hc.SetCurrentHeader(block)
		if err != nil {
			sl.logger.WithFields(log.Fields{"hash": block.Hash(), "err": err}).Warn("Error setting current header")
			sl.recomputeRequired = true
			sl.hc.headermu.Unlock()
			return nil, err
		}
	}
	stateProcessTime := time.Since(start)
	if sl.ReadBestPh() == nil {
		sl.hc.headermu.Unlock()
		return nil, errors.New("best ph is nil")
	}
	bestPhCopy := types.CopyWorkObject(sl.ReadBestPh())
	// If we are trying to recompute the pending header on the same parent block
	// we can return what we already have
	if !sl.recomputeRequired && bestPhCopy != nil && bestPhCopy.ParentHash(sl.NodeCtx()) == block.Hash() {
		sl.hc.headermu.Unlock()
		return bestPhCopy, nil
	}
	sl.recomputeRequired = false

	phStart := time.Now()
	pendingHeader, err := sl.miner.worker.GeneratePendingHeader(block, fill, nil, false)
	if err != nil {
		sl.hc.headermu.Unlock()
		return nil, err
	}
	pendingHeaderCreationTime := time.Since(phStart)

	sl.hc.headermu.Unlock()
	if sl.NodeCtx() == common.ZONE_CTX {
		// Set the block processing times before sending the block in chain head
		// feed
		appendTime, exists := sl.appendTimeCache.Peek(block.Hash())
		if exists {
			block.SetAppendTime(appendTime)
		}
		block.SetStateProcessTime(stateProcessTime)
		block.SetPendingHeaderCreationTime(pendingHeaderCreationTime)

		sl.hc.chainHeadFeed.Send(ChainHeadEvent{block})
	}
	return pendingHeader, nil
}

func (sl *Slice) GetPendingBlockBody(wo *types.WorkObjectHeader) *types.WorkObject {
	blockBody, _ := sl.miner.worker.GetPendingBlockBody(wo)
	return blockBody
}

func (sl *Slice) SubscribeMissingBlockEvent(ch chan<- types.BlockRequest) event.Subscription {
	return sl.scope.Track(sl.missingBlockFeed.Subscribe(ch))
}

// SetSubClient sets the subClient for the given location
func (sl *Slice) SetSubInterface(subInterface CoreBackend, location common.Location) {
	switch sl.NodeCtx() {
	case common.PRIME_CTX:
		sl.subInterface[location.Region()] = subInterface
	case common.REGION_CTX:
		sl.subInterface[location.Zone()] = subInterface
	default:
		sl.logger.WithField("cannot set sub client in zone", location).Fatal("Invalid location")
	}
}

// loadLastState loads the phCache and the slice pending header hash from the db.
func (sl *Slice) loadLastState() error {
	if sl.ProcessingState() {
		sl.miner.worker.LoadPendingBlockBody()
	}
	sl.WriteBestPh(rawdb.ReadBestPendingHeader(sl.sliceDb))
	return nil
}

// Stop stores the phCache and the sl.pendingHeader hash value to the db.
func (sl *Slice) Stop() {
	nodeCtx := sl.NodeLocation().Context()

	var badHashes []common.Hash
	for hash := range sl.badHashesCache {
		badHashes = append(badHashes, hash)
	}
	rawdb.WriteBadHashesList(sl.sliceDb, badHashes)
	sl.miner.worker.StorePendingBlockBody()

	rawdb.WriteBestPendingHeader(sl.sliceDb, sl.ReadBestPh())

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

func (sl *Slice) CurrentInfo(header *types.WorkObject) bool {
	return sl.miner.worker.CurrentInfo(header)
}

func (sl *Slice) WriteBlock(block *types.WorkObject) {
	sl.hc.WriteBlock(block)
}

func (sl *Slice) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	if err := sl.hc.AddPendingEtxs(pEtxs); err != nil {
		return err
	}
	return nil
}

func (sl *Slice) AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error {
	if !pEtxsRollup.IsValid(trie.NewStackTrie(nil)) && !sl.hc.IsGenesisHash(pEtxsRollup.Header.Hash()) {
		sl.logger.Info("PendingEtxRollup is invalid")
		return ErrPendingEtxRollupNotValid
	}
	nodeCtx := sl.NodeLocation().Context()
	sl.logger.WithFields(log.Fields{
		"header": pEtxsRollup.Header.Hash(),
		"len":    len(pEtxsRollup.EtxsRollup),
	}).Info("Received pending ETXs Rollup")
	// Only write the pending ETXs if we have not seen them before
	if !sl.hc.pendingEtxsRollup.Contains(pEtxsRollup.Header.Hash()) {
		// Also write to cache for faster access
		sl.hc.pendingEtxsRollup.Add(pEtxsRollup.Header.Hash(), pEtxsRollup)
		// Write to pending ETX rollup database
		rawdb.WritePendingEtxsRollup(sl.sliceDb, pEtxsRollup)
		if nodeCtx == common.REGION_CTX {
			sl.domInterface.AddPendingEtxsRollup(pEtxsRollup)
		}
	}
	return nil
}

func (sl *Slice) CheckForBadHashAndRecover() {
	nodeCtx := sl.NodeLocation().Context()
	// Lookup the bad hashes list to see if we have it in the database
	for _, fork := range BadHashes {
		var badBlock *types.WorkObject
		var badHash common.Hash
		switch nodeCtx {
		case common.PRIME_CTX:
			badHash = fork.PrimeContext
		case common.REGION_CTX:
			badHash = fork.RegionContext[sl.NodeLocation().Region()]
		case common.ZONE_CTX:
			badHash = fork.ZoneContext[sl.NodeLocation().Region()][sl.NodeLocation().Zone()]
		}
		badBlock = sl.hc.GetBlockByHash(badHash)
		// Node has a bad block in the database
		if badBlock != nil {
			// Start from the current tip and delete every block from the database until this bad hash block
			sl.cleanCacheAndDatabaseTillBlock(badBlock.ParentHash(nodeCtx))
			if nodeCtx == common.PRIME_CTX {
				sl.SetHeadBackToRecoveryState(nil, badBlock.ParentHash(nodeCtx))
			}
		}
	}
}

// SetHeadBackToRecoveryState sets the heads of the whole hierarchy to the recovery state
func (sl *Slice) SetHeadBackToRecoveryState(pendingHeader *types.WorkObject, hash common.Hash) types.PendingHeader {
	nodeCtx := sl.NodeLocation().Context()
	if nodeCtx == common.PRIME_CTX {
		localPendingHeaderWithTermini := sl.ComputeRecoveryPendingHeader(hash)
		sl.GenerateRecoveryPendingHeader(localPendingHeaderWithTermini.WorkObject(), localPendingHeaderWithTermini.Termini())
		sl.SetBestPh(localPendingHeaderWithTermini.WorkObject())
	} else {
		localPendingHeaderWithTermini := sl.ComputeRecoveryPendingHeader(hash)
		localPendingHeaderWithTermini.SetHeader(sl.combinePendingHeader(localPendingHeaderWithTermini.WorkObject(), pendingHeader, nodeCtx, true))
		localPendingHeaderWithTermini.WorkObject().WorkObjectHeader().SetLocation(sl.NodeLocation())
		sl.SetBestPh(localPendingHeaderWithTermini.WorkObject())
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
	nodeCtx := sl.NodeCtx()
	// slice caches
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
	sl.hc.bc.bodyProtoCache.Purge()

	var badHashes []common.Hash
	header := currentHeader
	for {
		rawdb.DeleteWorkObject(sl.sliceDb, header.Hash(), header.NumberU64(nodeCtx), types.BlockObject)
		rawdb.DeleteCanonicalHash(sl.sliceDb, header.NumberU64(nodeCtx))
		rawdb.DeleteHeaderNumber(sl.sliceDb, header.Hash())
		rawdb.DeleteTermini(sl.sliceDb, header.Hash())
		if nodeCtx != common.ZONE_CTX {
			rawdb.DeletePendingEtxs(sl.sliceDb, header.Hash())
			rawdb.DeletePendingEtxsRollup(sl.sliceDb, header.Hash())
		}
		// delete the trie node for a given root of the header
		rawdb.DeleteTrieNode(sl.sliceDb, header.EVMRoot())
		badHashes = append(badHashes, header.Hash())
		parent := sl.hc.GetHeaderByHash(header.ParentHash(nodeCtx))
		header = parent
		if header.Hash() == hash || sl.hc.IsGenesisHash(header.Hash()) {
			break
		}
	}

	sl.AddToBadHashesList(badHashes)
	// Set the current header
	currentHeader = sl.hc.GetHeaderByHash(hash)
	sl.hc.currentHeader.Store(currentHeader)
	rawdb.WriteHeadBlockHash(sl.sliceDb, currentHeader.Hash())

	// Recover the snaps
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.hc.bc.processor.snaps, _ = snapshot.New(sl.sliceDb, sl.hc.bc.processor.stateCache.TrieDB(), sl.hc.bc.processor.cacheConfig.SnapshotLimit, currentHeader.EVMRoot(), true, true, sl.logger)
	}
}

func (sl *Slice) GenerateRecoveryPendingHeader(pendingHeader *types.WorkObject, checkPointHashes types.Termini) error {
	nodeCtx := sl.NodeCtx()
	regions, zones := common.GetHierarchySizeForExpansionNumber(sl.hc.currentExpansionNumber)
	if nodeCtx == common.PRIME_CTX {
		for i := 0; i < int(regions); i++ {
			if sl.subInterface[i] != nil {
				sl.subInterface[i].GenerateRecoveryPendingHeader(pendingHeader, checkPointHashes)
			}
		}
	} else if nodeCtx == common.REGION_CTX {
		newPendingHeader := sl.SetHeadBackToRecoveryState(pendingHeader, checkPointHashes.SubTerminiAtIndex(sl.NodeLocation().Region()))
		for i := 0; i < int(zones); i++ {
			if sl.subInterface[i] != nil {
				sl.subInterface[i].GenerateRecoveryPendingHeader(newPendingHeader.WorkObject(), newPendingHeader.Termini())
			}
		}
	} else {
		sl.SetHeadBackToRecoveryState(pendingHeader, checkPointHashes.SubTerminiAtIndex(sl.NodeLocation().Zone()))
	}
	return nil
}

// ComputeRecoveryPendingHeader generates the pending header at a given hash
// and gets the termini from the database and returns the pending header with
// termini
func (sl *Slice) ComputeRecoveryPendingHeader(hash common.Hash) types.PendingHeader {
	block := sl.hc.GetBlockByHash(hash)
	pendingHeader, err := sl.miner.worker.GeneratePendingHeader(block, false, nil, false)
	if err != nil {
		sl.logger.Error("Error generating pending header during the checkpoint recovery process")
		return types.PendingHeader{}
	}
	termini := sl.hc.GetTerminiByHash(hash)
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
	nodeCtx := sl.NodeCtx()
	switch nodeCtx {
	case common.PRIME_CTX:
		for _, fork := range BadHashes {
			if fork.PrimeContext == hash {
				return true
			}
		}
	case common.REGION_CTX:
		for _, fork := range BadHashes {
			if fork.RegionContext[sl.NodeLocation().Region()] == hash {
				return true
			}
		}
	case common.ZONE_CTX:
		for _, fork := range BadHashes {
			if fork.ZoneContext[sl.NodeLocation().Region()][sl.NodeLocation().Zone()] == hash {
				return true
			}
		}
	}
	return sl.HashExistsInBadHashesList(hash)
}

func (sl *Slice) NodeLocation() common.Location {
	return sl.hc.NodeLocation()
}

func (sl *Slice) NodeCtx() int {
	return sl.hc.NodeCtx()
}

func (sl *Slice) GetSlicesRunning() []common.Location {
	return sl.hc.SlicesRunning()
}

func (sl *Slice) asyncWorkShareUpdateLoop() {
	defer func() {
		if r := recover(); r != nil {
			sl.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	asyncTimer := time.NewTicker(c_asyncWorkShareTimer)
	defer asyncTimer.Stop()
	for {
		select {
		case <-asyncTimer.C:
			// Every time we read the broadcast set, get the next set of transactions and
			// update the phcache
			// Get the latest transactions to be broadcasted from the pool
			if len(sl.txPool.broadcastSet) > 0 {
				txsDirty := make(types.Transactions, len(sl.txPool.broadcastSet))
				copy(txsDirty, sl.txPool.broadcastSet)
				txs := make(types.Transactions, 0)
				for _, tx := range txsDirty {
					if tx != nil {
						txs = append(txs, tx)
					}
				}
				hash := types.DeriveSha(txs, trie.NewStackTrie(nil))
				bestPh := types.CopyWorkObject(sl.ReadBestPh())
				// Update the tx hash
				bestPh.WorkObjectHeader().SetTxHash(hash)
				sl.SetBestPh(bestPh)
				sl.txPool.broadcastSetCache.Add(hash, txs)
			}
		case <-sl.quit:
			return
		}
	}
}

func (sl *Slice) GetTxsFromBroadcastSet(hash common.Hash) (types.Transactions, error) {
	sl.txPool.broadcastSet = types.Transactions{}
	return sl.txPool.GetTxsFromBroadcastSet(hash)
}

func (sl *Slice) CalcOrder(header *types.WorkObject) (*big.Int, int, error) {
	return sl.engine.CalcOrder(sl.hc, header)
}

////// Expansion related logic

const (
	ExpansionNotTriggered = iota
	ExpansionTriggered
	ExpansionConfirmed
	ExpansionCompleted
)

func (sl *Slice) SetCurrentExpansionNumber(expansionNumber uint8) {
	sl.hc.SetCurrentExpansionNumber(expansionNumber)
}

// AddGenesisHash appends the given hash to the genesis hash list
func (sl *Slice) AddGenesisHash(hash common.Hash) {
	genesisHashes := rawdb.ReadGenesisHashes(sl.sliceDb)
	genesisHashes = append(genesisHashes, hash)

	// write the genesis hash to the database
	rawdb.WriteGenesisHashes(sl.sliceDb, genesisHashes)
}

// AddGenesisPendingEtxs adds the genesis pending etxs to the db
func (sl *Slice) AddGenesisPendingEtxs(block *types.WorkObject) {
	sl.hc.pendingEtxs.Add(block.Hash(), types.PendingEtxs{block, types.Transactions{}})
	rawdb.WritePendingEtxs(sl.sliceDb, types.PendingEtxs{block, types.Transactions{}})
}

// SubscribeExpansionEvent subscribes to the expansion feed
func (sl *Slice) SubscribeExpansionEvent(ch chan<- ExpansionEvent) event.Subscription {
	return sl.scope.Track(sl.expansionFeed.Subscribe(ch))
}
