package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/ethclient/quaiclient"
	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/params"
)

const (
	maxFutureHeaders        = 256
	maxTimeFutureHeaders    = 30
	pendingHeaderCacheLimit = 500
	pendingHeaderGCTime     = 5
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

	futureHeaders *lru.Cache

	phCachemu sync.RWMutex

	nilHeader        *types.Header
	nilPendingHeader types.PendingHeader

	wg sync.WaitGroup // slice processing wait group for shutting down

	pendingHeader common.Hash
	phCache       map[common.Hash]types.PendingHeader
}

func NewSlice(db ethdb.Database, config *Config, txConfig *TxPoolConfig, isLocalBlock func(block *types.Header) bool, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis) (*Slice, error) {
	nodeCtx := common.NodeLocation.Context()
	sl := &Slice{
		config:  chainConfig,
		engine:  engine,
		sliceDb: db,
		domUrl:  domClientUrl,
	}

	futureHeaders, _ := lru.New(maxFutureHeaders)
	sl.futureHeaders = futureHeaders

	var err error
	sl.hc, err = NewHeaderChain(db, engine, chainConfig, cacheConfig, vmConfig)
	if err != nil {
		return nil, err
	}

	sl.txPool = NewTxPool(*txConfig, chainConfig, sl.hc)
	sl.miner = New(sl.hc, sl.txPool, config, db, chainConfig, engine, isLocalBlock)
	sl.miner.SetExtra(sl.miner.MakeExtraData(config.ExtraData))

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

	sl.nilPendingHeader = types.PendingHeader{
		Header:  types.EmptyHeader(),
		Termini: make([]common.Hash, 3),
	}

	go sl.updateFutureHeaders()
	go sl.updatePendingHeadersCache()

	// If the headerchain is empty start from genesis
	if sl.hc.Empty() {
		// Initialize slice state for genesis knot
		genesisHash := sl.Config().GenesisHash
		genesisTermini := []common.Hash{genesisHash, genesisHash, genesisHash, genesisHash}
		sl.pendingHeader = genesisHash
		rawdb.WriteTermini(sl.sliceDb, genesisHash, genesisTermini)

		// Append each of the knot blocks

		knot := genesis.Knot[1:]
		for _, block := range knot {
			if block != nil {
				location := block.Header().Location()
				if nodeCtx == common.PRIME_CTX {
					rawdb.WritePendingBlockBody(sl.sliceDb, block.Root(), block.Body())
					_, err = sl.Append(block.Header(), genesisHash, block.Difficulty(), false, false)
					if err != nil {
						log.Warn("Failed to append block", "hash:", block.Hash(), "Number:", block.Number(), "Location:", block.Header().Location(), "error:", err)
					}
				} else if location.Region() == common.NodeLocation.Region() && len(common.NodeLocation) == common.REGION_CTX {
					rawdb.WritePendingBlockBody(sl.sliceDb, block.Root(), block.Body())
				} else if bytes.Equal(location, common.NodeLocation) {
					rawdb.WritePendingBlockBody(sl.sliceDb, block.Root(), block.Body())
				}
			}
		}

	} else { // load the phCache and slice current pending header hash
		if err := sl.loadLastState(); err != nil {
			return nil, err
		}
	}
	return sl, nil
}

// Append takes a proposed header and constructs a local block and attempts to hierarchically append it to the block graph.
// If this is called from a dominant context a domTerminus must be provided else a common.Hash{} should be used and domOrigin should be set to true.
func (sl *Slice) Append(header *types.Header, domTerminus common.Hash, td *big.Int, domOrigin bool, reorg bool) (types.PendingHeader, error) {
	nodeCtx := common.NodeLocation.Context()
	location := header.Location()

	// Construct the block locally
	block := sl.ConstructLocalBlock(header)
	if block == nil {
		// add the block to the future header cache
		sl.addfutureHeader(header)
		return sl.nilPendingHeader, errors.New("could not find the tx and uncle data to match the header root hash")
	}

	start := time.Now()
	log.Info("Starting slice append", "hash", block.Hash(), "number", block.Number(), "location", block.Header().Location())

	batch := sl.sliceDb.NewBatch()

	// Run Previous Coincident Reference Check (PCRC)
	domTerminus, newTermini, err := sl.pcrc(batch, block.Header(), domTerminus)
	if err != nil {
		return sl.nilPendingHeader, err
	}

	// Append the new block
	err = sl.hc.Append(batch, block)
	if err != nil {
		return sl.nilPendingHeader, err
	}

	if !domOrigin {
		// CalcTd on the new block
		td, err = sl.calcTd(block.Header())
		if err != nil {
			return sl.nilPendingHeader, err
		}
		// HLCR
		reorg = sl.hlcr(td)
	}

	// Call my sub to append the block
	var subPendingHeader types.PendingHeader
	if nodeCtx != common.ZONE_CTX {
		subPendingHeader, err = sl.subClients[location.SubLocation()].Append(context.Background(), block.Header(), domTerminus, td, true, reorg)
		if err != nil {
			return sl.nilPendingHeader, err
		}
	}

	// WriteTd
	// Remove this once td is converted to a single value.
	rawdb.WriteTd(batch, block.Header().Hash(), block.NumberU64(), td)

	//Append has succeeded write the batch
	if err := batch.Write(); err != nil {
		return types.PendingHeader{}, err
	}

	// Set my header chain head and generate new pending header
	localPendingHeader, err := sl.setHeaderChainHead(batch, block, reorg)
	if err != nil {
		return sl.nilPendingHeader, err
	}

	// Combine subordinates pending header with local pending header
	var pendingHeader types.PendingHeader
	if nodeCtx != common.ZONE_CTX {
		tempPendingHeader := subPendingHeader.Header
		tempPendingHeader = sl.combinePendingHeader(localPendingHeader, tempPendingHeader, nodeCtx)
		tempPendingHeader.SetLocation(subPendingHeader.Header.Location())
		pendingHeader = types.PendingHeader{Header: tempPendingHeader, Termini: newTermini}
	} else {
		pendingHeader = types.PendingHeader{Header: localPendingHeader, Termini: newTermini}
	}

	isCoincident := sl.engine.HasCoincidentDifficulty(header)
	// Relay the new pendingHeader
	sl.updateCacheAndRelay(pendingHeader, block.Header().Location(), reorg, isCoincident)

	// Remove the header form the future headers cache
	sl.futureHeaders.Remove(block.Hash())

	if isCoincident {
		go sl.procfutureHeaders()
	}

	log.Info("Appended new block", "number", block.Header().Number(), "hash", block.Hash(),
		"uncles", len(block.Uncles()), "txs", len(block.Transactions()), "gas", block.GasUsed(),
		"elapsed", common.PrettyDuration(time.Since(start)),
		"root", block.Root())

	return pendingHeader, nil
}

// constructLocalBlock takes a header and construct the Block locally
func (sl *Slice) ConstructLocalBlock(header *types.Header) *types.Block {
	var block *types.Block
	// check if the header has empty uncle and tx root
	if header.EmptyBody() {
		// construct block with empty transactions and uncles
		block = types.NewBlockWithHeader(header)
	} else {
		pendingBlockBody := sl.PendingBlockBody(header.Root())
		if pendingBlockBody != nil {
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

			block = types.NewBlockWithHeader(header).WithBody(txs, uncles)
			block = block.WithSeal(header)
		}
	}
	return block
}

// updateCacheAndRelay updates the pending headers cache and sends pending headers to subordinates
func (sl *Slice) updateCacheAndRelay(pendingHeader types.PendingHeader, location common.Location, reorg bool, isCoincident bool) {
	nodeCtx := common.NodeLocation.Context()

	sl.phCachemu.Lock()
	defer sl.phCachemu.Unlock()

	sl.updatePhCache(pendingHeader)
	if !isCoincident {
		switch nodeCtx {
		case common.PRIME_CTX:
			sl.updatePhCacheFromDom(pendingHeader, 3, []int{common.REGION_CTX, common.ZONE_CTX}, reorg)
			if reorg {
				sl.miner.worker.pendingHeaderFeed.Send(sl.phCache[sl.pendingHeader].Header)
			}
			for i := range sl.subClients {
				sl.subClients[i].SubRelayPendingHeader(context.Background(), sl.phCache[sl.pendingHeader], location, reorg)
			}
		case common.REGION_CTX:
			sl.updatePhCacheFromDom(pendingHeader, 3, []int{common.ZONE_CTX}, reorg)
			if reorg {
				sl.miner.worker.pendingHeaderFeed.Send(sl.phCache[sl.pendingHeader].Header)
			}
			for i := range sl.subClients {
				sl.subClients[i].SubRelayPendingHeader(context.Background(), sl.phCache[sl.pendingHeader], location, reorg)
			}
		case common.ZONE_CTX:
			sl.miner.worker.pendingHeaderFeed.Send(sl.phCache[sl.pendingHeader].Header)
		}
	}
}

// setHeaderChainHead updates the current chain head and returns a new pending header
func (sl *Slice) setHeaderChainHead(batch ethdb.Batch, block *types.Block, reorg bool) (*types.Header, error) {
	// If reorg is true set to newly appended block
	if reorg {
		err := sl.hc.SetCurrentHeader(block.Header())
		if err != nil {
			return sl.nilHeader, err
		}
		sl.hc.chainHeadFeed.Send(ChainHeadEvent{Block: block})
	} else {
		sl.hc.chainSideFeed.Send(ChainSideEvent{Block: block})
	}

	// Upate the local pending header
	pendingHeader, err := sl.miner.worker.GeneratePendingHeader(block)
	if err != nil {
		return sl.nilHeader, err
	}

	// Set the Location and time for the pending header
	pendingHeader.SetLocation(common.NodeLocation)

	return pendingHeader, nil
}

// PCRC previous coincidence reference check makes sure there are not any cyclic references in the graph and calculates new termini and the block terminus
func (sl *Slice) pcrc(batch ethdb.Batch, header *types.Header, domTerminus common.Hash) (common.Hash, []common.Hash, error) {
	nodeCtx := common.NodeLocation.Context()
	location := header.Location()

	isCoincident := sl.engine.HasCoincidentDifficulty(header)

	log.Debug("PCRC:", "Parent Hash:", header.ParentHash(), "Number", header.Number, "Location:", header.Location())
	termini := sl.hc.GetTerminiByHash(header.ParentHash())

	if len(termini) != 4 {
		return common.Hash{}, []common.Hash{}, errors.New("length of termini not equal to 4")
	}

	newTermini := make([]common.Hash, len(termini))
	for i, terminus := range termini {
		newTermini[i] = terminus
	}

	// Genesis escape for the domTerminus
	if header.ParentHash(common.PRIME_CTX) == sl.config.GenesisHash {
		domTerminus = sl.config.GenesisHash
	}

	// Set the subtermini
	if nodeCtx != common.ZONE_CTX {
		newTermini[location.SubLocation()] = header.Hash()
	}

	// Set the terminus
	if nodeCtx == common.PRIME_CTX || isCoincident {
		newTermini[3] = header.Hash()
	} else {
		newTermini[3] = termini[3]
	}

	// Check for a graph twist
	if isCoincident {
		if termini[3] != domTerminus {
			return common.Hash{}, []common.Hash{}, errors.New("termini do not match, block rejected due to a twist")
		}
	}

	//Save the termini
	rawdb.WriteTermini(batch, header.Hash(), newTermini)

	if nodeCtx == common.ZONE_CTX {
		return common.Hash{}, newTermini, nil
	}

	return termini[location.SubLocation()], newTermini, nil
}

// HLCR Hierarchical Longest Chain Rule compares externTd to the currentHead Td and returns true if externTd is greater
func (sl *Slice) hlcr(externTd *big.Int) bool {
	currentTd := sl.hc.GetTdByHash(sl.hc.CurrentHeader().Hash())
	log.Debug("HLCR:", "Header hash:", sl.hc.CurrentHeader().Hash(), "currentTd:", currentTd, "externTd:", externTd)
	reorg := currentTd.Cmp(externTd) < 0
	//TODO need to handle the equal td case
	return reorg
}

// CalcTd calculates the TD of the given header using PCRC and CalcHLCRNetDifficulty.
func (sl *Slice) calcTd(header *types.Header) (*big.Int, error) {
	priorTd := sl.hc.GetTd(header.ParentHash(), header.NumberU64()-1)
	if priorTd == nil {
		return nil, consensus.ErrFutureBlock
	}
	Td := priorTd.Add(priorTd, header.Difficulty())
	return Td, nil
}

// GetPendingHeader is used by the miner to request the current pending header
func (sl *Slice) GetPendingHeader() (*types.Header, error) {
	return sl.phCache[sl.pendingHeader].Header, nil
}

// SubRelayPendingHeader takes a pending header from the sender (ie dominant), updates the phCache with a composited header and relays result to subordinates
func (sl *Slice) SubRelayPendingHeader(pendingHeader types.PendingHeader, location common.Location, reorg bool) error {
	nodeCtx := common.NodeLocation.Context()

	sl.phCachemu.Lock()
	defer sl.phCachemu.Unlock()

	if nodeCtx == common.REGION_CTX {
		sl.updatePhCacheFromDom(pendingHeader, common.NodeLocation.Region(), []int{common.PRIME_CTX}, reorg)
		for i := range sl.subClients {
			err := sl.subClients[i].SubRelayPendingHeader(context.Background(), sl.phCache[pendingHeader.Termini[common.NodeLocation.Region()]], location, reorg)
			if err != nil {
				log.Warn("SubRelayPendingHeader", "err:", err)
			}
		}
	} else {
		sl.updatePhCacheFromDom(pendingHeader, common.NodeLocation.Zone(), []int{common.PRIME_CTX, common.REGION_CTX}, reorg)
		sl.phCache[pendingHeader.Termini[common.NodeLocation.Zone()]].Header.SetLocation(common.NodeLocation)
		bestPh, exists := sl.phCache[sl.pendingHeader]
		if exists && !bytes.Equal(location, common.NodeLocation) {
			sl.miner.worker.pendingHeaderFeed.Send(bestPh.Header)
		}
	}
	return nil
}

// updatePhCache takes in an externPendingHeader and updates the pending header on the same terminus if the number is greater
func (sl *Slice) updatePhCache(externPendingHeader types.PendingHeader) {
	nodeCtx := common.NodeLocation.Context()

	var localPendingHeader types.PendingHeader
	hash := externPendingHeader.Termini[3]
	localPendingHeader, exists := sl.phCache[hash]

	if !exists {
		parentTermini := sl.hc.GetTerminiByHash(hash)
		if len(parentTermini) == 4 && parentTermini[3] != sl.config.GenesisHash { // TODO: Do we need the length check??
			cachedPendingHeader, exists := sl.phCache[parentTermini[3]]
			if !exists {
				sl.phCache[hash] = externPendingHeader
				return
			} else {
				cachedPendingHeader.Header = sl.combinePendingHeader(externPendingHeader.Header, cachedPendingHeader.Header, nodeCtx)
				cachedPendingHeader.Termini = externPendingHeader.Termini
				sl.phCache[hash] = cachedPendingHeader
				return
			}
		} else { //GENESIS ESCAPE
			sl.phCache[hash] = externPendingHeader
			sl.pendingHeader = hash
			return
		}
	}

	if externPendingHeader.Header.NumberU64() > localPendingHeader.Header.NumberU64() {
		localPendingHeader.Header = sl.combinePendingHeader(externPendingHeader.Header, localPendingHeader.Header, nodeCtx)
		localPendingHeader.Termini = externPendingHeader.Termini

		sl.setCurrentPendingHeader(localPendingHeader)
		sl.phCache[hash] = localPendingHeader
	}
}

// updatePhCacheFromDom combines the recieved pending header with the pending header stored locally at a given terminus for specified context
func (sl *Slice) updatePhCacheFromDom(pendingHeader types.PendingHeader, terminiIndex int, indices []int, reorg bool) {
	var localPendingHeader types.PendingHeader
	hash := pendingHeader.Termini[terminiIndex]
	localPendingHeader, exists := sl.phCache[hash]

	if !exists { //GENESIS ESCAPE
		sl.phCache[hash] = pendingHeader
		sl.pendingHeader = hash
		return
	} else {
		for _, i := range indices {
			localPendingHeader.Header = sl.combinePendingHeader(pendingHeader.Header, localPendingHeader.Header, i)
		}
		localPendingHeader.Header.SetLocation(pendingHeader.Header.Location())
		sl.phCache[hash] = localPendingHeader
	}

	// Only set the pendingHeader head if the dom did a reorg
	if reorg {
		sl.pendingHeader = hash
	}
}

// setCurrentPendingHeader compares the externPh parent td to the sl.pendingHeader parent td and sets sl.pendingHeader to the exterPh if the td is greater
func (sl *Slice) setCurrentPendingHeader(externPendingHeader types.PendingHeader) {
	externTd := sl.hc.GetTdByHash(externPendingHeader.Header.ParentHash())
	currentTd := sl.hc.GetTdByHash(sl.phCache[sl.pendingHeader].Header.ParentHash())
	log.Debug("setCurrentPendingHeader:", "currentParent:", sl.phCache[sl.pendingHeader].Header.ParentHash(), "currentTd:", currentTd, "externParent:", externPendingHeader.Header.ParentHash(), "externTd:", externTd)
	if currentTd.Cmp(externTd) < 0 {
		sl.pendingHeader = externPendingHeader.Termini[3]
	}
}

// gcPendingHeader goes through the phCache and deletes entries older than the pendingHeaderCacheLimit
func (sl *Slice) gcPendingHeaders() {
	sl.phCachemu.Lock()
	defer sl.phCachemu.Unlock()
	for hash, pendingHeader := range sl.phCache {
		if pendingHeader.Header.NumberU64()+pendingHeaderCacheLimit < sl.hc.CurrentHeader().NumberU64() {
			delete(sl.phCache, hash)
		}
	}
}

// combinePendingHeader updates the pending header at the given index with the value from given header.
func (sl *Slice) combinePendingHeader(header *types.Header, slPendingHeader *types.Header, index int) *types.Header {
	slPendingHeader.SetParentHash(header.ParentHash(index), index)
	slPendingHeader.SetUncleHash(header.UncleHash(index), index)
	slPendingHeader.SetNumber(header.Number(index), index)
	slPendingHeader.SetExtra(header.Extra())
	slPendingHeader.SetBaseFee(header.BaseFee(index), index)
	slPendingHeader.SetGasLimit(header.GasLimit(index), index)
	slPendingHeader.SetGasUsed(header.GasUsed(index), index)
	slPendingHeader.SetTxHash(header.TxHash(index), index)
	slPendingHeader.SetReceiptHash(header.ReceiptHash(index), index)
	slPendingHeader.SetRoot(header.Root(index), index)
	slPendingHeader.SetDifficulty(header.Difficulty(index), index)
	slPendingHeader.SetCoinbase(header.Coinbase(index), index)
	slPendingHeader.SetBloom(header.Bloom(index), index)

	return slPendingHeader
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
		if suburl == "" {
			log.Warn("sub client url is empty")
		}
		subClient, err := quaiclient.Dial(suburl)
		if err != nil {
			log.Crit("Error connecting to the subordinate go-quai client for index", "index", i, " err ", err)
		}
		subClients[i] = subClient
	}
	return subClients
}

// procfutureHeaders sorts the future block cache and attempts to append
func (sl *Slice) procfutureHeaders() {
	headers := make([]*types.Header, 0, sl.futureHeaders.Len())
	for _, hash := range sl.futureHeaders.Keys() {
		if header, exist := sl.futureHeaders.Peek(hash); exist {
			headers = append(headers, header.(*types.Header))
		}
	}
	if len(headers) > 0 {
		sort.Slice(headers, func(i, j int) bool {
			return headers[i].NumberU64() < headers[j].NumberU64()
		})

		for i := range headers {
			var nilHash common.Hash
			sl.Append(headers[i], nilHash, big.NewInt(0), false, false)
		}
	}
}

// addfutureHeader adds a block to the future block cache
func (sl *Slice) addfutureHeader(header *types.Header) error {
	max := uint64(time.Now().Unix() + maxTimeFutureHeaders)
	if header.Time() > max {
		return fmt.Errorf("future block timestamp %v > allowed %v", header.Time, max)
	}
	if !sl.futureHeaders.Contains(header.Hash()) {
		sl.futureHeaders.Add(header.Hash(), header)
	}
	return nil
}

// updatefutureHeaders is a time to procfutureHeaders
func (sl *Slice) updateFutureHeaders() {
	futureTimer := time.NewTicker(3 * time.Second)
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			sl.procfutureHeaders()
		case <-sl.quit:
			return
		}
	}
}

// updatePendingheadersCache is a timer to gcPendingHeaders
func (sl *Slice) updatePendingHeadersCache() {
	futureTimer := time.NewTicker(pendingHeaderGCTime * time.Minute)
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			sl.gcPendingHeaders()
		case <-sl.quit:
			return
		}
	}
}

// loadLastState loads the phCache and the slice pending header hash from the db.
func (sl *Slice) loadLastState() error {
	sl.phCache = rawdb.ReadPhCache(sl.sliceDb)
	sl.pendingHeader = rawdb.ReadCurrentPendingHeaderHash(sl.sliceDb)
	return nil
}

// Stop stores the phCache and the sl.pendingHeader hash value to the db.
func (sl *Slice) Stop() {
	// write the ph head hash to the db.
	rawdb.WriteCurrentPendingHeaderHash(sl.sliceDb, sl.pendingHeader)
	// Write the ph cache to the dd.
	rawdb.WritePhCache(sl.sliceDb, sl.phCache)

	sl.hc.Stop()
	sl.txPool.Stop()
	sl.miner.Stop()
}

func (sl *Slice) Config() *params.ChainConfig { return sl.config }

func (sl *Slice) Engine() consensus.Engine { return sl.engine }

func (sl *Slice) HeaderChain() *HeaderChain { return sl.hc }

func (sl *Slice) TxPool() *TxPool { return sl.txPool }

func (sl *Slice) Miner() *Miner { return sl.miner }

func (sl *Slice) PendingBlockBody(hash common.Hash) *types.Body {
	return rawdb.ReadPendingBlockBody(sl.sliceDb, hash)
}
