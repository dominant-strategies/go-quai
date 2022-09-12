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
	maxFutureBlocks         = 256
	maxTimeFutureBlocks     = 30
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

	futureBlocks *lru.Cache

	phCachemu sync.RWMutex
	appendmu  sync.RWMutex

	nilHeader        *types.Header
	nilPendingHeader types.PendingHeader

	wg sync.WaitGroup // slice processing wait group for shutting down

	pendingHeader common.Hash
	phCache       map[common.Hash]types.PendingHeader
}

func NewSlice(db ethdb.Database, config *Config, txConfig *TxPoolConfig, isLocalBlock func(block *types.Header) bool, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis) (*Slice, error) {
	sl := &Slice{
		config:  chainConfig,
		engine:  engine,
		sliceDb: db,
		domUrl:  domClientUrl,
	}

	futureBlocks, _ := lru.New(maxFutureBlocks)
	sl.futureBlocks = futureBlocks

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
	if types.QuaiNetworkContext != params.ZONE {
		sl.subClients = makeSubClients(subClientUrls)
	}

	// only set domClient if the chain is not Prime.
	if types.QuaiNetworkContext != params.PRIME {
		go func() {
			sl.domClient = makeDomClient(domClientUrl)
		}()
	}

	sl.nilHeader = &types.Header{
		ParentHash:        make([]common.Hash, 3),
		Number:            make([]*big.Int, 3),
		Extra:             make([][]byte, 3),
		Time:              uint64(0),
		BaseFee:           make([]*big.Int, 3),
		GasLimit:          make([]uint64, 3),
		Coinbase:          make([]common.Address, 3),
		Difficulty:        make([]*big.Int, 3),
		NetworkDifficulty: make([]*big.Int, 3),
		Root:              make([]common.Hash, 3),
		TxHash:            make([]common.Hash, 3),
		UncleHash:         make([]common.Hash, 3),
		ReceiptHash:       make([]common.Hash, 3),
		GasUsed:           make([]uint64, 3),
		Bloom:             make([]types.Bloom, 3),
	}
	sl.nilPendingHeader = types.PendingHeader{
		Header:  sl.nilHeader,
		Termini: make([]common.Hash, 3),
	}

	go sl.updateFutureBlocks()
	go sl.updatePendingHeadersCache()

	// Initialize slice state for genesis knot
	genesisHash := sl.Config().GenesisHashes[0]
	genesisTermini := []common.Hash{genesisHash, genesisHash, genesisHash, genesisHash}
	sl.pendingHeader = genesisHash
	rawdb.WriteTermini(sl.sliceDb, genesisHash, genesisTermini)

	time.Sleep(5 * time.Second)
	// Append each of the knot blocks
	if types.QuaiNetworkContext == params.PRIME {
		knot := genesis.Knot[1:]
		for _, block := range knot {
			if block != nil {
				_, err = sl.Append(block, genesisHash, block.Difficulty(), false, false)
				if err != nil {
					log.Warn("Failed to append block", "hash:", block.Hash(), "Number:", block.Number(), "Location:", block.Header().Location, "error:", err)
				}
			}
		}
	}
	return sl, nil
}

// Append takes a proposed block and attempts to hierarchically append it to the block graph.
// If this is called from a dominant context a domTerminus must be provided else a common.Hash{} should be used and domOrigin should be set to true.
func (sl *Slice) Append(block *types.Block, domTerminus common.Hash, td *big.Int, domOrigin bool, reorg bool) (types.PendingHeader, error) {
	log.Info("Starting Append...", "Block.Hash:", block.Hash(), "Number:", block.Number(), "Location:", block.Header().Location)
	batch := sl.sliceDb.NewBatch()

	// Calculate block order

	order, err := sl.engine.GetDifficultyOrder(block.Header())
	if err != nil {
		return sl.nilPendingHeader, err
	}

	// Run Previous Coincident Reference Check (PCRC)
	domTerminus, newTermini, err := sl.pcrc(batch, block.Header(), domTerminus, order)
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
	if types.QuaiNetworkContext != params.ZONE {
		subPendingHeader, err = sl.subClients[block.Header().Location[types.QuaiNetworkContext]-1].Append(context.Background(), block, domTerminus, td, true, reorg)
		if err != nil {
			return sl.nilPendingHeader, err
		}
	}

	// WriteTd
	// Remove this once td is converted to a single value.
	externTd := make([]*big.Int, 3)
	externTd[types.QuaiNetworkContext] = td
	rawdb.WriteTd(batch, block.Header().Hash(), block.NumberU64(), externTd)

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
	if types.QuaiNetworkContext != params.ZONE {
		tempPendingHeader := subPendingHeader.Header
		tempPendingHeader = sl.combinePendingHeader(localPendingHeader, tempPendingHeader, types.QuaiNetworkContext)
		tempPendingHeader.Location = subPendingHeader.Header.Location
		pendingHeader = types.PendingHeader{Header: tempPendingHeader, Termini: newTermini}
	} else {
		pendingHeader = types.PendingHeader{Header: localPendingHeader, Termini: newTermini}
	}

	// Relay the new pendingHeader
	sl.updateCacheAndRelay(pendingHeader, block.Header().Location, order, reorg)

	return pendingHeader, nil
}

// updateCacheAndRelay updates the pending headers cache and sends pending headers to subordinates
func (sl *Slice) updateCacheAndRelay(pendingHeader types.PendingHeader, location []byte, order int, reorg bool) {
	sl.phCachemu.Lock()
	defer sl.phCachemu.Unlock()

	sl.updatePhCache(pendingHeader)
	if order == params.PRIME && types.QuaiNetworkContext == params.PRIME {
		sl.updatePhCacheFromDom(pendingHeader, 3, []int{params.REGION, params.ZONE}, reorg)
		if reorg {
			sl.miner.worker.pendingHeaderFeed.Send(sl.phCache[sl.pendingHeader].Header)
		}
		for i := range sl.subClients {
			sl.subClients[i].SubRelayPendingHeader(context.Background(), sl.phCache[sl.pendingHeader], location, reorg)
		}
	} else if order == params.REGION && types.QuaiNetworkContext == params.REGION {
		sl.updatePhCacheFromDom(pendingHeader, 3, []int{params.ZONE}, reorg)
		if reorg {
			sl.miner.worker.pendingHeaderFeed.Send(sl.phCache[sl.pendingHeader].Header)
		}
		for i := range sl.subClients {
			sl.subClients[i].SubRelayPendingHeader(context.Background(), sl.phCache[sl.pendingHeader], location, reorg)
		}
	} else if order == params.ZONE && types.QuaiNetworkContext == params.ZONE {
		sl.miner.worker.pendingHeaderFeed.Send(sl.phCache[sl.pendingHeader].Header)
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
	}

	// Upate the local pending header
	pendingHeader, err := sl.miner.worker.GeneratePendingHeader(block)
	if err != nil {
		fmt.Println("pending block error: ", err)
		return sl.nilHeader, err
	}

	// Set the Location and time for the pending header
	pendingHeader.Location = sl.config.Location
	pendingHeader.Time = uint64(time.Now().Unix())

	return pendingHeader, nil
}

// PCRC previous coincidence reference check makes sure there are not any cyclic references in the graph and calculates new termini and the block terminus
func (sl *Slice) pcrc(batch ethdb.Batch, header *types.Header, domTerminus common.Hash, order int) (common.Hash, []common.Hash, error) {
	log.Debug("PCRC:", "Parent Hash:", header.Parent(), "Number", header.Number, "Location:", header.Location)
	termini := sl.hc.GetTerminiByHash(header.Parent())

	if len(termini) != 4 {
		return common.Hash{}, []common.Hash{}, errors.New("length of termini not equal to 4")
	}

	newTermini := make([]common.Hash, len(termini))
	for i, terminus := range termini {
		newTermini[i] = terminus
	}

	// Genesis escape for the domTerminus
	if header.ParentHash[0] == sl.config.GenesisHashes[0] {
		domTerminus = sl.config.GenesisHashes[0]
	}

	// Set the subtermini
	if types.QuaiNetworkContext != params.ZONE {
		newTermini[header.Location[types.QuaiNetworkContext]-1] = header.Hash()
	}

	// Set the terminus
	if types.QuaiNetworkContext == params.PRIME || order < types.QuaiNetworkContext {
		newTermini[3] = header.Hash()
	} else {
		newTermini[3] = termini[3]
	}

	// Check for a graph twist
	if order < types.QuaiNetworkContext {
		if termini[3] != domTerminus {
			return common.Hash{}, []common.Hash{}, errors.New("termini do not match, block rejected due to a twist")
		}
	}

	//Save the termini
	rawdb.WriteTermini(batch, header.Hash(), newTermini)

	if types.QuaiNetworkContext == params.ZONE {
		return common.Hash{}, newTermini, nil
	}

	return termini[header.Location[types.QuaiNetworkContext]-1], newTermini, nil
}

// HLCR Hierarchical Longest Chain Rule compares externTd to the currentHead Td and returns true if externTd is greater
func (sl *Slice) hlcr(externTd *big.Int) bool {
	currentTd := sl.hc.GetTdByHash(sl.hc.CurrentHeader().Hash())
	log.Debug("HLCR:", "Header hash:", sl.hc.CurrentHeader().Hash(), "currentTd:", currentTd, "externTd:", externTd)
	reorg := currentTd[types.QuaiNetworkContext].Cmp(externTd) < 0
	//TODO need to handle the equal td case
	return reorg
}

// CalcTd calculates the TD of the given header using PCRC and CalcHLCRNetDifficulty.
func (sl *Slice) calcTd(header *types.Header) (*big.Int, error) {
	priorTd := sl.hc.GetTd(header.Parent(), header.Number64()-1)
	if priorTd[types.QuaiNetworkContext] == nil {
		return nil, consensus.ErrFutureBlock
	}
	Td := priorTd[types.QuaiNetworkContext].Add(priorTd[types.QuaiNetworkContext], header.Difficulty[types.QuaiNetworkContext])
	return Td, nil
}

// GetPendingHeader is used by the miner to request the current pending header
func (sl *Slice) GetPendingHeader() (*types.Header, error) {
	return sl.phCache[sl.pendingHeader].Header, nil
}

// SubRelayPendingHeader takes a pending header from the sender (ie dominant), updates the phCache with a composited header and relays result to subordinates
func (sl *Slice) SubRelayPendingHeader(pendingHeader types.PendingHeader, location []byte, reorg bool) error {
	sl.phCachemu.Lock()
	defer sl.phCachemu.Unlock()

	if types.QuaiNetworkContext == params.REGION {
		sl.updatePhCacheFromDom(pendingHeader, int(sl.config.Location[types.QuaiNetworkContext-1]-1), []int{params.PRIME}, reorg)
		for i := range sl.subClients {
			err := sl.subClients[i].SubRelayPendingHeader(context.Background(), sl.phCache[pendingHeader.Termini[sl.config.Location[types.QuaiNetworkContext-1]-1]], location, reorg)
			if err != nil {
				log.Warn("SubRelayPendingHeader", "err:", err)
			}
		}
	} else {
		sl.updatePhCacheFromDom(pendingHeader, int(sl.config.Location[types.QuaiNetworkContext-1]-1), []int{params.PRIME, params.REGION}, reorg)
		sl.phCache[pendingHeader.Termini[sl.config.Location[types.QuaiNetworkContext-1]-1]].Header.Location = sl.config.Location
		bestPh, exists := sl.phCache[sl.pendingHeader]
		if exists && !bytes.Equal(location, sl.config.Location) {
			sl.miner.worker.pendingHeaderFeed.Send(bestPh.Header)
		}
	}
	return nil
}

// updatePhCache takes in an externPendingHeader and updates the pending header on the same terminus if the number is greater
func (sl *Slice) updatePhCache(externPendingHeader types.PendingHeader) {
	var localPendingHeader types.PendingHeader
	hash := externPendingHeader.Termini[3]
	localPendingHeader, exists := sl.phCache[hash]

	if !exists {
		parentTermini := sl.hc.GetTerminiByHash(hash)
		if len(parentTermini) == 4 && parentTermini[3] != sl.config.GenesisHashes[0] { // TODO: Do we need the length check??
			cachedPendingHeader, exists := sl.phCache[parentTermini[3]]
			if !exists {
				sl.phCache[hash] = externPendingHeader
				return
			} else {
				cachedPendingHeader.Header = sl.combinePendingHeader(externPendingHeader.Header, cachedPendingHeader.Header, types.QuaiNetworkContext)
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

	if externPendingHeader.Header.Number64() > localPendingHeader.Header.Number64() {
		localPendingHeader.Header = sl.combinePendingHeader(externPendingHeader.Header, localPendingHeader.Header, types.QuaiNetworkContext)
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
		localPendingHeader.Header.Location = pendingHeader.Header.Location
		sl.phCache[hash] = localPendingHeader
	}

	// Only set the pendingHeader head if the dom did a reorg
	if reorg {
		sl.pendingHeader = hash
	}
}

// setCurrentPendingHeader compares the externPh parent td to the sl.pendingHeader parent td and sets sl.pendingHeader to the exterPh if the td is greater
func (sl *Slice) setCurrentPendingHeader(externPendingHeader types.PendingHeader) {
	externTd := sl.hc.GetTdByHash(externPendingHeader.Header.Parent())
	currentTd := sl.hc.GetTdByHash(sl.phCache[sl.pendingHeader].Header.Parent())
	log.Debug("setCurrentPendingHeader:", "currentParent:", sl.phCache[sl.pendingHeader].Header.Parent(), "currentTd:", currentTd, "externParent:", externPendingHeader.Header.Parent(), "externTd:", externTd)
	if currentTd[types.QuaiNetworkContext].Cmp(externTd[types.QuaiNetworkContext]) < 0 {
		sl.pendingHeader = externPendingHeader.Termini[3]
	}
}

// gcPendingHeader goes through the phCache and deletes entries older than the pendingHeaderCacheLimit
func (sl *Slice) gcPendingHeaders() {
	sl.phCachemu.Lock()
	defer sl.phCachemu.Unlock()
	for hash, pendingHeader := range sl.phCache {
		if pendingHeader.Header.Number64()+pendingHeaderCacheLimit < sl.hc.CurrentHeader().Number64() {
			delete(sl.phCache, hash)
		}
	}
}

// combinePendingHeader updates the pending header at the given index with the value from given header.
func (sl *Slice) combinePendingHeader(header *types.Header, slPendingHeader *types.Header, index int) *types.Header {
	slPendingHeader.ParentHash[index] = header.ParentHash[index]
	slPendingHeader.UncleHash[index] = header.UncleHash[index]
	slPendingHeader.Number[index] = header.Number[index]
	slPendingHeader.Extra[index] = header.Extra[index]
	slPendingHeader.BaseFee[index] = header.BaseFee[index]
	slPendingHeader.GasLimit[index] = header.GasLimit[index]
	slPendingHeader.GasUsed[index] = header.GasUsed[index]
	slPendingHeader.TxHash[index] = header.TxHash[index]
	slPendingHeader.ReceiptHash[index] = header.ReceiptHash[index]
	slPendingHeader.Root[index] = header.Root[index]
	slPendingHeader.Difficulty[index] = header.Difficulty[index]
	slPendingHeader.Coinbase[index] = header.Coinbase[index]
	slPendingHeader.Bloom[index] = header.Bloom[index]

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

// procFutureBlocks sorts the future block cache and attempts to append
func (sl *Slice) procFutureBlocks() {
	blocks := make([]*types.Block, 0, sl.futureBlocks.Len())
	for _, hash := range sl.futureBlocks.Keys() {
		if block, exist := sl.futureBlocks.Peek(hash); exist {
			blocks = append(blocks, block.(*types.Block))
		}
	}
	if len(blocks) > 0 {
		sort.Slice(blocks, func(i, j int) bool {
			return blocks[i].NumberU64() < blocks[j].NumberU64()
		})

		for i := range blocks {
			var nilHash common.Hash
			sl.Append(blocks[i], nilHash, big.NewInt(0), false, false)
		}
	}
}

// addFutureBlock adds a block to the future block cache
func (sl *Slice) addFutureBlock(block *types.Block) error {
	max := uint64(time.Now().Unix() + maxTimeFutureBlocks)
	if block.Time() > max {
		return fmt.Errorf("future block timestamp %v > allowed %v", block.Time(), max)
	}
	if !sl.futureBlocks.Contains(block.Hash()) {
		sl.futureBlocks.Add(block.Hash(), block)
	}
	return nil
}

// updateFutureBlocks is a time to procFutureBlocks
func (sl *Slice) updateFutureBlocks() {
	futureTimer := time.NewTicker(3 * time.Second)
	defer futureTimer.Stop()
	defer sl.wg.Done()
	for {
		select {
		case <-futureTimer.C:
			sl.procFutureBlocks()
		case <-sl.quit:
			return
		}
	}
}

// updatePendingheadersCache is a timer to gcPendingHeaders
func (sl *Slice) updatePendingHeadersCache() {
	futureTimer := time.NewTicker(pendingHeaderGCTime * time.Minute)
	defer futureTimer.Stop()
	defer sl.wg.Done()
	for {
		select {
		case <-futureTimer.C:
			sl.gcPendingHeaders()
		case <-sl.quit:
			return
		}
	}
}

func (sl *Slice) Config() *params.ChainConfig { return sl.config }

func (sl *Slice) Engine() consensus.Engine { return sl.engine }

func (sl *Slice) HeaderChain() *HeaderChain { return sl.hc }

func (sl *Slice) TxPool() *TxPool { return sl.txPool }

func (sl *Slice) Miner() *Miner { return sl.miner }

func (sl *Slice) PendingBlockBody(hash common.Hash) *types.Body {
	return rawdb.ReadPendginBlockBody(sl.sliceDb, hash)
}
