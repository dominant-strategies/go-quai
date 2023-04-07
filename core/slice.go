package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
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
	maxPendingEtxBlocks               = 256
	c_pendingHeaderCacheLimit         = 100
	c_pendingHeaderChacheBufferFactor = 2
	pendingHeaderGCTime               = 5
	c_terminusIndex                   = 3
	c_startingPrintLimit              = 10
	c_regionRelayProc                 = 3
	c_primeRelayProc                  = 10
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

	scope                  event.SubscriptionScope
	missingBodyFeed        event.Feed
	missingPendingEtxsFeed event.Feed
	missingParentFeed      event.Feed

	pendingEtxs *lru.Cache

	phCachemu sync.RWMutex

	bestPhKey common.Hash
	phCache   map[common.Hash]types.PendingHeader

	validator Validator // Block and state validator interface
}

func NewSlice(db ethdb.Database, config *Config, txConfig *TxPoolConfig, isLocalBlock func(block *types.Header) bool, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis) (*Slice, error) {
	nodeCtx := common.NodeLocation.Context()
	sl := &Slice{
		config:  chainConfig,
		engine:  engine,
		sliceDb: db,
		domUrl:  domClientUrl,
		quit:    make(chan struct{}),
	}

	pendingEtxs, _ := lru.New(maxPendingEtxBlocks)
	sl.pendingEtxs = pendingEtxs

	var err error
	sl.hc, err = NewHeaderChain(db, engine, chainConfig, cacheConfig, vmConfig)
	if err != nil {
		return nil, err
	}
	sl.validator = NewBlockValidator(chainConfig, sl.hc, engine)

	sl.txPool = NewTxPool(*txConfig, chainConfig, sl.hc)
	sl.miner = New(sl.hc, sl.txPool, config, db, chainConfig, engine, isLocalBlock)

	sl.phCache = make(map[common.Hash]types.PendingHeader)

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

	return sl, nil
}

// Append takes a proposed header and constructs a local block and attempts to hierarchically append it to the block graph.
// If this is called from a dominant context a domTerminus must be provided else a common.Hash{} should be used and domOrigin should be set to true.
func (sl *Slice) Append(header *types.Header, domPendingHeader *types.Header, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) ([]types.Transactions, error) {
	start := time.Now()
	// The compute and write of the phCache is split starting here so we need to get the lock
	sl.phCachemu.Lock()
	defer sl.phCachemu.Unlock()

	// Only print in Info level if block is c_startingPrintLimit behind or less
	if sl.CurrentInfo(header) {
		log.Info("Starting slice append", "hash", header.Hash(), "number", header.NumberArray(), "location", header.Location(), "parent hash", header.ParentHash())
	} else {
		log.Debug("Starting slice append", "hash", header.Hash(), "number", header.NumberArray(), "location", header.Location(), "parent hash", header.ParentHash())
	}

	nodeCtx := common.NodeLocation.Context()
	location := header.Location()
	order, err := header.CalcOrder()
	if err != nil {
		return nil, err
	}
	// Don't append the block which already exists in the database.
	if sl.hc.HasHeader(header.Hash(), header.NumberU64()) && (sl.hc.GetTerminiByHash(header.Hash()) != nil) {
		log.Warn("Block has already been appended: ", "Hash: ", header.Hash())
		return nil, ErrKnownBlock
	}
	time1 := common.PrettyDuration(time.Since(start))
	// This is to prevent a crash when we try to insert blocks before domClient is on.
	// Ideally this check should not exist here and should be fixed before we start the slice.
	if sl.domClient == nil && nodeCtx != common.PRIME_CTX {
		return nil, ErrDomClientNotUp
	}
	time2 := common.PrettyDuration(time.Since(start))
	// Construct the block locally
	block, err := sl.ConstructLocalBlock(header)
	if err != nil {
		return nil, err
	}
	time3 := common.PrettyDuration(time.Since(start))
	batch := sl.sliceDb.NewBatch()

	// Run Previous Coincident Reference Check (PCRC)
	domTerminus, newTermini, err := sl.pcrc(batch, block.Header(), domTerminus, domOrigin)
	if err != nil {
		return nil, err
	}
	time4 := common.PrettyDuration(time.Since(start))
	// If this was a coincident block, our dom will be passing us a set of newly confirmed ETXs
	// If this is not a coincident block, we need to build up the list of confirmed ETXs using the subordinate manifest
	subRollup := types.Transactions{}
	if !domOrigin {
		newInboundEtxs, subRollup, err = sl.CollectNewlyConfirmedEtxs(block, block.Location())
		if err != nil {
			log.Debug("Error collecting newly confirmed etxs: ", "err", err)
			return nil, ErrSubNotSyncedToDom
		}
	} else if nodeCtx < common.ZONE_CTX {
		subRollups, err := sl.CollectSubRollups(block)
		if err != nil {
			return nil, err
		}
		subRollup = subRollups[nodeCtx+1]
	}
	time5 := common.PrettyDuration(time.Since(start))

	// Append the new block
	err = sl.hc.Append(batch, block, newInboundEtxs.FilterToLocation(common.NodeLocation))
	if err != nil {
		return nil, err
	}
	time6 := common.PrettyDuration(time.Since(start))
	// Upate the local pending header
	localPendingHeader, err := sl.miner.worker.GeneratePendingHeader(block)
	if err != nil {
		return nil, err
	}
	time7 := common.PrettyDuration(time.Since(start))
	// Combine subordinates pending header with local pending header
	pendingHeaderWithTermini := sl.computePendingHeader(types.PendingHeader{Header: localPendingHeader, Termini: newTermini}, domPendingHeader, domOrigin)
	pendingHeaderWithTermini.Header.SetLocation(header.Location())
	time8 := common.PrettyDuration(time.Since(start))
	s := header.CalcS()

	// Set the parent delta S prior to sending to sub
	if nodeCtx != common.PRIME_CTX {
		if domOrigin {
			pendingHeaderWithTermini.Header.SetParentDeltaS(big.NewInt(0), nodeCtx)
		} else {
			pendingHeaderWithTermini.Header.SetParentDeltaS(header.CalcDeltaS(), nodeCtx)
		}
	}
	time9 := common.PrettyDuration(time.Since(start))
	pendingHeaderWithTermini.Header.SetParentEntropy(s)
	var time9_1 common.PrettyDuration
	var time9_2 common.PrettyDuration
	var time9_3 common.PrettyDuration
	// Call my sub to append the block, and collect the rolled up ETXs from that sub
	localPendingEtxs := []types.Transactions{types.Transactions{}, types.Transactions{}, types.Transactions{}}
	subPendingEtxs := []types.Transactions{types.Transactions{}, types.Transactions{}, types.Transactions{}}
	if nodeCtx != common.ZONE_CTX {

		// How to get the sub pending etxs if not running the full node?.
		if sl.subClients[location.SubIndex()] != nil {
			subPendingEtxs, err = sl.subClients[location.SubIndex()].Append(context.Background(), block.Header(), pendingHeaderWithTermini.Header, domTerminus, true, newInboundEtxs)
			if err != nil {
				return nil, err
			}
			time9_1 = common.PrettyDuration(time.Since(start))
			// Cache the subordinate's pending ETXs
			pEtxs := types.PendingEtxs{block.Header(), subPendingEtxs}
			if !pEtxs.IsValid(trie.NewStackTrie(nil)) {
				return nil, errors.New("sub pending ETXs faild validation")
			}
			time9_2 = common.PrettyDuration(time.Since(start))
			sl.AddPendingEtxs(pEtxs)
			time9_3 = common.PrettyDuration(time.Since(start))
		}
	}
	time10 := common.PrettyDuration(time.Since(start))
	log.Trace("Entropy Calculations", "header", header.Hash(), "S", common.BigBitsToBits(s), "DeltaS", common.BigBitsToBits(header.CalcDeltaS()), "IntrinsicS", common.BigBitsToBits(header.CalcIntrinsicS()))
	// Combine sub's pending ETXs, sub rollup, and our local ETXs into localPendingEtxs
	// e.g. localPendingEtxs[ctx]:
	// * for 'ctx' is dom: empty
	// * for 'ctx' is local: ETXs emitted in this block
	// * for 'ctx' is direct sub: replace sub pending ETXs with sub rollup ETXs
	// * for 'ctx' is indirect sub: copy sub pending ETXs (sub's sub has already been rolled up)
	//
	// We get this in the following three steps:
	// 1) Copy the rollup set for any subordinates of my subordinate (i.e. ctx >= nodeCtx+1)
	// 2) Compute the rollup of my subordinate and assign to ctx = nodeCtx+1
	// 3) Assign local pending ETXs to ctx = nodeCtx
	for ctx := nodeCtx + 2; ctx < common.HierarchyDepth; ctx++ {
		localPendingEtxs[ctx] = make(types.Transactions, len(subPendingEtxs[ctx]))
		copy(localPendingEtxs[ctx], subPendingEtxs[ctx]) // copy pending for each indirect sub
	}
	if nodeCtx < common.ZONE_CTX {
		localPendingEtxs[nodeCtx+1] = make(types.Transactions, len(subRollup))
		copy(localPendingEtxs[nodeCtx+1], subRollup) // overwrite direct sub with sub rollup
	}
	localPendingEtxs[nodeCtx] = make(types.Transactions, len(block.ExtTransactions()))
	copy(localPendingEtxs[nodeCtx], block.ExtTransactions()) // Assign our new ETXs without rolling up

	time11 := common.PrettyDuration(time.Since(start))
	//Append has succeeded write the batch
	if err := batch.Write(); err != nil {
		return nil, err
	}
	time12 := common.PrettyDuration(time.Since(start))
	sl.writeToPhCacheAndPickPhHead(pendingHeaderWithTermini)

	// Relay the new pendingHeader
	go sl.relayPh(pendingHeaderWithTermini, domOrigin, block.Location())
	time13 := common.PrettyDuration(time.Since(start))
	log.Info("times during append:", "t1:", time1, "t2:", time2, "t3:", time3, "t4:", time4, "t5:", time5, "t6:", time6, "t7:", time7, "t8:", time8, "t9:", time9, "t10:", time10, "t11:", time11, "t12:", time12, "t13:", time13)
	log.Info("times during sub append:", "t9_1:", time9_1, "t9_2:", time9_2, "t9_3:", time9_3)
	log.Info("Appended new block", "number", block.Header().Number(), "hash", block.Hash(),
		"uncles", len(block.Uncles()), "txs", len(block.Transactions()), "etxs", len(block.ExtTransactions()), "gas", block.GasUsed(),
		"root", block.Root(),
		"order", order,
		"elapsed", common.PrettyDuration(time.Since(start)))

	return localPendingEtxs, nil
}

// backfillPETXs collects any missing PendingETX objects needed to process the
// given header. This is done by informing the fetcher of any pending ETXs we do
// not have, so that they can be fetched from our peers.
func (sl *Slice) backfillPETXs(header *types.Header, subManifest types.BlockManifest) {
	for _, hash := range subManifest {
		if petxs := rawdb.ReadPendingEtxs(sl.sliceDb, hash); petxs == nil {
			// Send the pendingEtxs to the feed for broadcast
			sl.missingPendingEtxsFeed.Send(hash)
		}
	}
}

// relayPh sends pendingHeaderWithTermini to subordinates
func (sl *Slice) relayPh(pendingHeaderWithTermini types.PendingHeader, domOrigin bool, location common.Location) {
	nodeCtx := common.NodeLocation.Context()

	if nodeCtx == common.ZONE_CTX {
		bestPh, exists := sl.phCache[sl.bestPhKey]
		if exists {
			bestPh.Header.SetLocation(common.NodeLocation)
			sl.miner.worker.pendingHeaderFeed.Send(bestPh.Header)
			return
		}
	} else if !domOrigin {
		for i := range sl.subClients {
			if sl.subClients[i] != nil {
				sl.subClients[i].SubRelayPendingHeader(context.Background(), pendingHeaderWithTermini, location)
			}
		}
	}
}

// CollectEtxsForManifest will gather the full list of ETXs that are referenceable through a given manifest
func (sl *Slice) CollectSubRollups(b *types.Block) ([]types.Transactions, error) {
	nodeCtx := common.NodeLocation.Context()
	subRollups := make([]types.Transactions, 3)
	if nodeCtx < common.ZONE_CTX {
		for _, hash := range b.SubManifest() {
			var pendingEtxs []types.Transactions
			// Look for pending ETXs first in pending ETX cache, then in database
			if res, ok := sl.pendingEtxs.Get(hash); ok && res != nil {
				pendingEtxs = res.([]types.Transactions)
			} else if res := rawdb.ReadPendingEtxs(sl.sliceDb, hash); res != nil {
				pendingEtxs = res.Etxs
			} else {
				log.Warn("unable to find pending etxs for hash in manifest", "hash:", hash.String())
				// Start backfilling the missing pending ETXs needed to process this block
				go sl.backfillPETXs(b.Header(), b.SubManifest())
				return nil, ErrPendingEtxNotFound
			}
			for ctx := nodeCtx; ctx < common.HierarchyDepth; ctx++ {
				subRollups[ctx] = append(subRollups[ctx], pendingEtxs[ctx]...)
			}
		}
		if subRollupHash := types.DeriveSha(subRollups[nodeCtx+1], trie.NewStackTrie(nil)); subRollupHash != b.EtxRollupHash(nodeCtx+1) {
			return nil, errors.New("sub rollup does not match sub rollup hash")
		}
	}
	return subRollups, nil
}

// CollectNewlyConfirmedEtxs collects all newly confirmed ETXs since the last coincident with the given location
func (sl *Slice) CollectNewlyConfirmedEtxs(block *types.Block, location common.Location) (types.Transactions, types.Transactions, error) {
	nodeCtx := common.NodeLocation.Context()
	// Collect rollup of ETXs from the subordinate node's manifest
	referencableEtxs := types.Transactions{}
	subRollup := types.Transactions{}
	if nodeCtx < common.ZONE_CTX {
		subRollups, err := sl.CollectSubRollups(block)
		if err != nil {
			return nil, nil, err
		}
		for _, etxs := range subRollups {
			referencableEtxs = append(referencableEtxs, etxs...)
		}
		referencableEtxs = append(referencableEtxs, block.ExtTransactions()...) // Include ETXs emitted in this block
		subRollup = subRollups[nodeCtx+1]
	}

	// Filter for ETXs destined to this slice
	newInboundEtxs := referencableEtxs.FilterToSlice(location, nodeCtx)

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
	// Terminate the search when we find a block produced by the given location
	if ancestor.Location().Equal(location) {
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
func (sl *Slice) pcrc(batch ethdb.Batch, header *types.Header, domTerminus common.Hash, domOrigin bool) (common.Hash, []common.Hash, error) {
	nodeCtx := common.NodeLocation.Context()
	location := header.Location()

	log.Debug("PCRC:", "Parent Hash:", header.ParentHash(), "Number", header.Number, "Location:", header.Location())
	termini := sl.hc.GetTerminiByHash(header.ParentHash())

	if len(termini) != 4 {
		return common.Hash{}, []common.Hash{}, ErrSubNotSyncedToDom
	}

	newTermini := make([]common.Hash, len(termini))
	for i, terminus := range termini {
		newTermini[i] = terminus
	}

	// Set the subtermini
	if nodeCtx != common.ZONE_CTX {
		newTermini[location.SubIndex()] = header.Hash()
	}

	// Set the terminus
	if nodeCtx == common.PRIME_CTX || domOrigin {
		newTermini[c_terminusIndex] = header.Hash()
	} else {
		newTermini[c_terminusIndex] = termini[c_terminusIndex]
	}

	// Check for a graph cyclic reference
	if domOrigin {
		if termini[c_terminusIndex] != domTerminus {
			log.Warn("Cyclic Block:", "block number", header.NumberArray(), "hash", header.Hash(), "terminus", domTerminus, "termini", termini)
			return common.Hash{}, []common.Hash{}, errors.New("termini do not match, block rejected due to cyclic reference")
		}
	}

	//Save the termini
	rawdb.WriteTermini(batch, header.Hash(), newTermini)

	if nodeCtx == common.ZONE_CTX {
		return common.Hash{}, newTermini, nil
	}

	return termini[location.SubIndex()], newTermini, nil
}

// POEM compares externS to the currentHead S and returns true if externS is greater
func (sl *Slice) poem(externS *big.Int, currentS *big.Int) bool {
	log.Debug("POEM:", "Header hash:", sl.hc.CurrentHeader().Hash(), "currentS:", common.BigBitsToBits(currentS), "externS:", common.BigBitsToBits(externS))
	reorg := currentS.Cmp(externS) < 0
	return reorg
}

// GetPendingHeader is used by the miner to request the current pending header
func (sl *Slice) GetPendingHeader() (*types.Header, error) {
	if ph := sl.phCache[sl.bestPhKey].Header; ph != nil {
		return ph, nil
	} else {
		return nil, errors.New("empty pending header")
	}
}

// GetManifest gathers the manifest of ancestor block hashes since the last
// coincident block.
func (sl *Slice) GetManifest(blockHash common.Hash) (types.BlockManifest, error) {
	header := sl.hc.GetHeaderByHash(blockHash)
	if header == nil {
		return nil, errors.New("block not found")
	}
	return sl.hc.CollectBlockManifest(header)
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

func (sl *Slice) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	log.Debug("Received pending ETXs", "block: ", pEtxs.Header.Hash())
	// Only write the pending ETXs if we have not seen them before
	if !sl.pendingEtxs.Contains(pEtxs.Header.Hash()) {
		// Write to pending ETX database
		rawdb.WritePendingEtxs(sl.sliceDb, pEtxs)
		// Also write to cache for faster access
		sl.pendingEtxs.Add(pEtxs.Header.Hash(), pEtxs.Etxs)
	}
	return nil
}

// SendPendingEtxsToDom shares a set of pending ETXs with your dom, so he can reference them when a coincident block is found
func (sl *Slice) SendPendingEtxsToDom(pEtxs types.PendingEtxs) error {
	return sl.domClient.SendPendingEtxsToDom(context.Background(), pEtxs)
}

// SubRelayPendingHeader takes a pending header from the sender (ie dominant), updates the phCache with a composited header and relays result to subordinates
func (sl *Slice) SubRelayPendingHeader(pendingHeader types.PendingHeader, location common.Location) {
	sl.phCachemu.Lock()
	defer sl.phCachemu.Unlock()
	nodeCtx := common.NodeLocation.Context()

	if nodeCtx == common.REGION_CTX {
		// Adding a guard on the region that was already updated in the synchronous path.
		if location.Region() != common.NodeLocation.Region() {
			err := sl.updatePhCacheFromDom(pendingHeader, common.NodeLocation.Region(), []int{common.PRIME_CTX})
			if err != nil {
				return
			}
		}
		for i := range sl.subClients {
			if sl.subClients[i] != nil {
				sl.subClients[i].SubRelayPendingHeader(context.Background(), sl.phCache[pendingHeader.Termini[common.NodeLocation.Region()]], location)
			}
		}
	} else {
		// This check prevents a double send to the miner.
		// If the previous block on which the given pendingHeader was built is the same as the NodeLocation
		// the pendingHeader update has already been sent to the miner for the given location in relayPh.
		if !bytes.Equal(location, common.NodeLocation) {
			err := sl.updatePhCacheFromDom(pendingHeader, common.NodeLocation.Zone(), []int{common.PRIME_CTX, common.REGION_CTX})
			if err != nil {
				return
			}
			bestPh, exists := sl.phCache[sl.bestPhKey]
			if exists {
				bestPh.Header.SetLocation(common.NodeLocation)
				sl.miner.worker.pendingHeaderFeed.Send(bestPh.Header)
			}
		}
	}
}

// computePendingHeader takes in an localPendingHeaderWithTermini and updates the pending header on the same terminus if the number is greater
func (sl *Slice) computePendingHeader(localPendingHeaderWithTermini types.PendingHeader, domPendingHeader *types.Header, domOrigin bool) types.PendingHeader {
	nodeCtx := common.NodeLocation.Context()

	var cachedPendingHeaderWithTermini types.PendingHeader
	hash := localPendingHeaderWithTermini.Termini[c_terminusIndex]
	cachedPendingHeaderWithTermini, exists := sl.phCache[hash]
	var newPh *types.Header

	if exists {
		newPh = sl.combinePendingHeader(localPendingHeaderWithTermini.Header, cachedPendingHeaderWithTermini.Header, nodeCtx)
		return types.PendingHeader{Header: newPh, Termini: localPendingHeaderWithTermini.Termini}
	} else {
		if domOrigin {
			newPh = sl.combinePendingHeader(localPendingHeaderWithTermini.Header, domPendingHeader, nodeCtx)
			return types.PendingHeader{Header: newPh, Termini: localPendingHeaderWithTermini.Termini}
		}
		return localPendingHeaderWithTermini
	}
}

// updatePhCacheFromDom combines the recieved pending header with the pending header stored locally at a given terminus for specified context
func (sl *Slice) updatePhCacheFromDom(pendingHeader types.PendingHeader, terminiIndex int, indices []int) error {

	nodeCtx := common.NodeLocation.Context()
	hash := pendingHeader.Termini[terminiIndex]
	localPendingHeader, exists := sl.phCache[hash]

	if exists {
		combinedPendingHeader := types.CopyHeader(localPendingHeader.Header)
		for _, i := range indices {
			combinedPendingHeader = sl.combinePendingHeader(pendingHeader.Header, combinedPendingHeader, i)
		}
		if nodeCtx == common.ZONE_CTX {
			combinedPendingHeader.SetDifficulty(localPendingHeader.Header.Difficulty())
			combinedPendingHeader.SetLocation(common.NodeLocation)
		}

		sl.writeToPhCacheAndPickPhHead(types.PendingHeader{Header: combinedPendingHeader, Termini: localPendingHeader.Termini})

		return nil
	}
	log.Warn("no pending header found for", "terminus", hash, "pendingHeaderNumber", pendingHeader.Header.NumberArray(), "Hash", pendingHeader.Header.ParentHash(), "Termini index", terminiIndex, "indices", indices)
	return errors.New("no pending header found in cache")
}

// writePhCache dom writes a given pendingHeaderWithTermini to the cache with the terminus used as the key.
func (sl *Slice) writeToPhCacheAndPickPhHead(pendingHeaderWithTermini types.PendingHeader) {
	bestPh, exist := sl.phCache[sl.bestPhKey]
	if !exist {
		log.Error("BestPh Key does not exist for", "key", sl.bestPhKey)
		return
	}
	oldBestPhEntropy := new(big.Int).Set(bestPh.Entropy)

	// Update the pendingHeader Cache
	oldPh, exist := sl.phCache[pendingHeaderWithTermini.Termini[c_terminusIndex]]
	var deepCopyPendingHeaderWithTermini types.PendingHeader
	newPhEntropy := pendingHeaderWithTermini.Header.CalcPhS()
	deepCopyPendingHeaderWithTermini = types.PendingHeader{Header: types.CopyHeader(pendingHeaderWithTermini.Header), Termini: pendingHeaderWithTermini.Termini, Entropy: newPhEntropy}
	if exist {
		if sl.poem(newPhEntropy, oldPh.Entropy) {
			sl.phCache[pendingHeaderWithTermini.Termini[c_terminusIndex]] = deepCopyPendingHeaderWithTermini
		}
	} else {
		sl.phCache[pendingHeaderWithTermini.Termini[c_terminusIndex]] = deepCopyPendingHeaderWithTermini
	}

	// Pick a phCache Head
	block := sl.hc.GetBlockByHash(pendingHeaderWithTermini.Header.ParentHash())
	if sl.poem(newPhEntropy, oldBestPhEntropy) {
		sl.bestPhKey = pendingHeaderWithTermini.Termini[c_terminusIndex]
		sl.hc.SetCurrentHeader(block.Header())
		sl.hc.chainHeadFeed.Send(ChainHeadEvent{Block: block})
		log.Debug("Choosing new pending header", "Ph Number:", pendingHeaderWithTermini.Header.NumberArray())
	} else {
		sl.hc.chainHeadFeed.Send(ChainHeadEvent{Block: block})
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
	emptyPendingEtxs := []types.Transactions{types.Transactions{}, types.Transactions{}, types.Transactions{}}
	err := sl.AddPendingEtxs(types.PendingEtxs{genesisHeader, emptyPendingEtxs})
	if err != nil {
		return err
	}
	// If the headerchain is empty start from genesis
	if sl.hc.Empty() {
		// Initialize slice state for genesis knot
		genesisTermini := []common.Hash{genesisHash, genesisHash, genesisHash, genesisHash}
		rawdb.WriteTermini(sl.sliceDb, genesisHash, genesisTermini)

		// Append each of the knot blocks
		sl.bestPhKey = genesisHash
		sl.hc.SetCurrentHeader(genesisHeader)
		sl.AddPendingEtxs(types.PendingEtxs{Header: genesisHeader, Etxs: []types.Transactions{}})

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
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.ZONE_CTX && header.EmptyBody() {
		// This shortcut is only available to zone chains. Prime and region chains can
		// never have an empty body, because they will always have at least one block
		// in the subordinate manifest.
		return types.NewBlockWithHeader(header), nil
	}
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
	if nodeCtx == common.ZONE_CTX && header.EmptyBody() {
		// This shortcut is only available to zone chains. Prime and region chains can
		// never have an empty body, because they will always have at least one block
		// in the subordinate manifest.
		return types.NewBlockWithHeader(header), nil
	}
	pendingBlockBody := sl.GetPendingBlockBody(header)
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

// combinePendingHeader updates the pending header at the given index with the value from given header.
func (sl *Slice) combinePendingHeader(header *types.Header, slPendingHeader *types.Header, index int) *types.Header {
	// copying the slPendingHeader and updating the copy to remove any shared memory access issues
	combinedPendingHeader := types.CopyHeader(slPendingHeader)

	combinedPendingHeader.SetParentHash(header.ParentHash(index), index)
	combinedPendingHeader.SetUncleHash(header.UncleHash(index), index)
	combinedPendingHeader.SetNumber(header.Number(index), index)
	combinedPendingHeader.SetExtra(header.Extra())
	combinedPendingHeader.SetBaseFee(header.BaseFee(index), index)
	combinedPendingHeader.SetGasLimit(header.GasLimit(index), index)
	combinedPendingHeader.SetGasUsed(header.GasUsed(index), index)
	combinedPendingHeader.SetTxHash(header.TxHash(index), index)
	combinedPendingHeader.SetEtxHash(header.EtxHash(index), index)
	combinedPendingHeader.SetEtxRollupHash(header.EtxRollupHash(index), index)
	combinedPendingHeader.SetManifestHash(header.ManifestHash(index), index)
	combinedPendingHeader.SetReceiptHash(header.ReceiptHash(index), index)
	combinedPendingHeader.SetRoot(header.Root(index), index)
	combinedPendingHeader.SetParentEntropy(header.ParentEntropy(index), index)
	combinedPendingHeader.SetParentDeltaS(header.ParentDeltaS(index), index)
	combinedPendingHeader.SetCoinbase(header.Coinbase(index), index)
	combinedPendingHeader.SetBloom(header.Bloom(index), index)
	combinedPendingHeader.SetDifficulty(header.Difficulty())

	return combinedPendingHeader
}

// NewGenesisPendingHeader creates a pending header on the genesis block
func (sl *Slice) NewGenesisPendingHeader(domPendingHeader *types.Header) {
	nodeCtx := common.NodeLocation.Context()
	genesisHash := sl.config.GenesisHash
	// Upate the local pending header
	localPendingHeader, err := sl.miner.worker.GeneratePendingHeader(sl.hc.GetBlockByHash(genesisHash))
	if err != nil {
		return
	}

	if nodeCtx == common.PRIME_CTX {
		domPendingHeader = types.CopyHeader(localPendingHeader)
	} else {
		domPendingHeader = sl.combinePendingHeader(localPendingHeader, domPendingHeader, nodeCtx)
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
	genesisTermini := []common.Hash{genesisHash, genesisHash, genesisHash, genesisHash}
	if sl.hc.Empty() {
		sl.phCache[sl.config.GenesisHash] = types.PendingHeader{Header: domPendingHeader, Termini: genesisTermini, Entropy: big.NewInt(0)}
	}
}

func (sl *Slice) GetPendingBlockBody(header *types.Header) *types.Body {
	return sl.miner.worker.GetPendingBlockBody(header)
}

func (sl *Slice) SubscribeMissingPendingEtxsEvent(ch chan<- common.Hash) event.Subscription {
	return sl.scope.Track(sl.missingPendingEtxsFeed.Subscribe(ch))
}

func (sl *Slice) SubscribeMissingParentEvent(ch chan<- common.Hash) event.Subscription {
	return sl.scope.Track(sl.missingParentFeed.Subscribe(ch))
}

// MakeDomClient creates the quaiclient for the given domurl
func makeDomClient(domurl string) *quaiclient.Client {
	if domurl == "" {
		log.Crit("dom client url is empty")
	}
	domClient, err := quaiclient.Dial(domurl)
	if err != nil {
		log.Crit("Error connecting to the dominant go-quai client", "err", err)
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
				log.Crit("Error connecting to the subordinate go-quai client for index", "index", i, " err ", err)
			}
			subClients[i] = subClient
		}
	}
	return subClients
}

// loadLastState loads the phCache and the slice pending header hash from the db.
func (sl *Slice) loadLastState() error {
	sl.phCache = rawdb.ReadPhCache(sl.sliceDb)
	sl.bestPhKey = rawdb.ReadBestPhKey(sl.sliceDb)
	sl.miner.worker.LoadPendingBlockBody()
	return nil
}

// Stop stores the phCache and the sl.pendingHeader hash value to the db.
func (sl *Slice) Stop() {
	// write the ph head hash to the db.
	rawdb.WriteBestPhKey(sl.sliceDb, sl.bestPhKey)
	// Write the ph cache to the dd.
	rawdb.WritePhCache(sl.sliceDb, sl.phCache)

	sl.miner.worker.StorePendingBlockBody()

	sl.scope.Close()
	close(sl.quit)

	sl.hc.Stop()
	sl.txPool.Stop()
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

func (sl *Slice) CurrentInfo(header *types.Header) bool {
	return sl.miner.worker.CurrentInfo(header)
}

func (sl *Slice) WriteBlock(block *types.Block) {
	sl.hc.WriteBlock(block)
}
