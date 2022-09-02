package core

import (
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
	maxFutureBlocks     = 256
	maxTimeFutureBlocks = 30
	pendingHeaderLimit  = 10
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

	appendmu sync.RWMutex

	nilHeader        *types.Header
	nilPendingHeader types.PendingHeader

	wg sync.WaitGroup // slice processing wait group for shutting down

	pendingHeader types.PendingHeader
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
		sl.subClients = MakeSubClients(subClientUrls)
	}

	domDoneCh := make(chan struct{})
	// only set domClient if the chain is not Prime.
	if types.QuaiNetworkContext != params.PRIME {
		go func(done chan struct{}) {
			sl.domClient = MakeDomClient(domClientUrl)
			done <- struct{}{}
		}(domDoneCh)
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
		Td:      big.NewInt(0),
	}

	go sl.updateFutureBlocks()

	genesisHash := sl.Config().GenesisHashes[0]
	genesisTermini := []common.Hash{genesisHash, genesisHash, genesisHash, genesisHash}
	fmt.Println("write termini for genesisHash", genesisHash, genesisTermini)
	rawdb.WriteTermini(sl.hc.headerDb, genesisHash, genesisTermini)

	time.Sleep(5 * time.Second)
	// Remove nil character from RLP read
	if types.QuaiNetworkContext == params.PRIME {
		knot := genesis.Knot[1:]
		for _, block := range knot {
			if block != nil {
				_, err = sl.Append(block, common.Hash{}, block.Difficulty(), false, true)
				if err != nil {
					fmt.Println("Failed to append block, hash:", block.Hash(), "Number:", block.Number(), "Location:", block.Header().Location)
				}
			}
		}
	}

	return sl, nil
}

func (sl *Slice) Append(block *types.Block, domTerminus common.Hash, td *big.Int, domReorg bool, currentContextOrigin bool) (types.PendingHeader, error) {
	sl.appendmu.Lock()
	defer sl.appendmu.Unlock()

	fmt.Println("Starting Append... Block.Hash:", block.Hash(), "Number:", block.Number(), "Location:", block.Header().Location)
	batch := sl.sliceDb.NewBatch()

	//PCRC
	domTerminus, err := sl.PCRC(batch, block.Header(), domTerminus)
	if err != nil {
		return sl.nilPendingHeader, err
	}

	// Append the new block
	err = sl.hc.Append(batch, block)
	if err != nil {
		fmt.Println("Slice error in append", err)
		return sl.nilPendingHeader, err
	}

	if currentContextOrigin {
		// CalcTd on the new block
		td, err = sl.CalcTd(block.Header())
		if err != nil {
			return sl.nilPendingHeader, err
		}
	}

	localPendingHeader, err := sl.setHeaderChainHead(batch, block, td, domReorg, currentContextOrigin)
	tempPendingHeader := types.CopyHeader(localPendingHeader.Header)
	if err != nil {
		return sl.nilPendingHeader, err
	}
	// WriteTd
	// Remove this once td is converted to a single value.
	externTd := make([]*big.Int, 3)
	externTd[types.QuaiNetworkContext] = td
	rawdb.WriteTd(sl.hc.headerDb, block.Header().Hash(), block.NumberU64(), externTd)

	if types.QuaiNetworkContext != params.ZONE {
		// Perform the sub append
		subPendingHeader, err := sl.subClients[block.Header().Location[types.QuaiNetworkContext]-1].Append(context.Background(), block, domTerminus, td, true, false)
		if err != nil {
			return sl.nilPendingHeader, err
		}
		fmt.Println("RET SUB PENDING", subPendingHeader.Header.Location, subPendingHeader.Header.Root)
		tempPendingHeader = subPendingHeader.Header
		tempPendingHeader = sl.combinePendingHeader(localPendingHeader.Header, tempPendingHeader, types.QuaiNetworkContext)
		fmt.Println("TEMP PENDING", tempPendingHeader.Location, tempPendingHeader.Root)
	}

	//Append has succeeded write the batch
	if err := batch.Write(); err != nil {
		return types.PendingHeader{}, err
	}

	pendingHeader := types.PendingHeader{Header: tempPendingHeader, Termini: localPendingHeader.Termini, Td: localPendingHeader.Td}

	order, err := sl.engine.GetDifficultyOrder(block.Header())
	if err != nil {
		return types.PendingHeader{}, err
	}

	fmt.Println("BEFORE SEND", order, types.QuaiNetworkContext, pendingHeader.Header.Root)
	if order == params.PRIME && types.QuaiNetworkContext == params.PRIME {
		bestPh := sl.GetBestPendingHeader(pendingHeader, pendingHeader.Header.Parent())
		sl.AddIfBestPendingHeader(bestPh)
		sl.miner.worker.pendingHeaderFeed.Send(bestPh.Header)
		fmt.Println("Append Hash:", bestPh.Header.Hash(), "Number:", bestPh.Header.Number, "Parents:", bestPh.Header.Parent(), "bestPh:", bestPh)
		for i := range sl.subClients {
			sl.subClients[i].SubRelayPendingHeader(context.Background(), bestPh)
		}
	} else if order == params.REGION && types.QuaiNetworkContext == params.REGION {
		bestPh := sl.GetBestPendingHeader(pendingHeader, pendingHeader.Termini[3])
		bestPh.Header = sl.combinePendingHeader(bestPh.Header, pendingHeader.Header, params.PRIME)
		bestPh.Td = pendingHeader.Td
		sl.AddIfBestPendingHeader(bestPh)
		fmt.Println("Append Hash:", bestPh.Header.Hash(), "Number:", bestPh.Header.Number, "Parents:", bestPh.Header.Parent(), "bestPh:", bestPh)
		sl.domClient.DomRelayPendingHeader(context.Background(), bestPh)
		for i := range sl.subClients {
			sl.subClients[i].SubRelayPendingHeader(context.Background(), bestPh)
		}
	} else if order == params.ZONE && types.QuaiNetworkContext == params.ZONE {
		bestPh := sl.GetBestPendingHeader(pendingHeader, pendingHeader.Termini[3])
		bestPh.Header = sl.combinePendingHeader(bestPh.Header, pendingHeader.Header, params.PRIME)
		bestPh.Header = sl.combinePendingHeader(bestPh.Header, pendingHeader.Header, params.REGION)
		bestPh.Td = pendingHeader.Td
		sl.AddIfBestPendingHeader(bestPh)
		fmt.Println("Append Hash:", bestPh.Header.Hash(), "Number:", bestPh.Header.Number, "Parents:", bestPh.Header.Parent(), "bestPh:", bestPh)
		sl.domClient.DomRelayPendingHeader(context.Background(), bestPh)
	}
	return types.PendingHeader{Header: tempPendingHeader, Termini: sl.pendingHeader.Termini, Td: sl.pendingHeader.Td}, nil
}

func (sl *Slice) setHeaderChainHead(batch ethdb.Batch, block *types.Block, td *big.Int, domReorg bool, currentContextOrigin bool) (types.PendingHeader, error) {

	if currentContextOrigin {
		reorg := sl.HLCR(td)
		if reorg {
			_, err := sl.hc.SetCurrentHeader(batch, block.Header())
			if err != nil {
				return sl.nilPendingHeader, err
			}
		}
	} else {
		if domReorg {
			_, err := sl.hc.SetCurrentHeader(batch, block.Header())
			if err != nil {
				return sl.nilPendingHeader, err
			}
		}
	}

	// Upate the local pending header
	slPendingHeader, err := sl.miner.worker.GeneratePendingHeader(block)
	if err != nil {
		fmt.Println("pending block error: ", err)
		return sl.nilPendingHeader, err
	}

	slPendingHeader.Location = sl.config.Location
	slPendingHeader.Time = uint64(time.Now().Unix())

	termini := rawdb.ReadTermini(sl.sliceDb, block.Header().Hash())

	return types.PendingHeader{Header: slPendingHeader, Termini: termini, Td: td}, nil
}

// PCRC
func (sl *Slice) PCRC(batch ethdb.Batch, header *types.Header, domTerminus common.Hash) (common.Hash, error) {
	fmt.Println("PCRC Parent.Hash:", header.ParentHash, "Number", header.Number, "Location:", header.Location, "index:", types.QuaiNetworkContext)
	termini := sl.hc.GetTerminiByHash(header.Parent())

	if termini == nil {
		return common.Hash{}, consensus.ErrFutureBlock
	}
	fmt.Println("Dom Terminus: ", domTerminus)
	fmt.Println("Termini: ", termini)

	newTermini := make([]common.Hash, len(termini))
	for i, terminus := range termini {
		newTermini[i] = terminus
	}

	if len(termini) != 4 {
		return common.Hash{}, errors.New("length of termini not equal to 4")
	}

	if types.QuaiNetworkContext != params.ZONE {
		newTermini[header.Location[types.QuaiNetworkContext]-1] = header.Hash()
	}

	order, err := sl.engine.GetDifficultyOrder(header)
	if err != nil {
		return common.Hash{}, err
	}

	if header.ParentHash[0] == sl.config.GenesisHashes[0] {
		domTerminus = sl.config.GenesisHashes[0]
	}

	if order < types.QuaiNetworkContext {
		newTermini[3] = header.Hash()
	} else {
		newTermini[3] = termini[3]
	}

	fmt.Println("header location: ", header.Location, newTermini, termini)
	if order < types.QuaiNetworkContext {
		if termini[3] != domTerminus {
			return common.Hash{}, errors.New("termini do not match, block rejected due to a twist")
		}
	}

	//Save the termini
	rawdb.WriteTermini(sl.sliceDb, header.Hash(), newTermini)

	fmt.Println("Termini before return", newTermini)

	if types.QuaiNetworkContext == params.ZONE {
		return common.Hash{}, nil
	}
	return termini[header.Location[types.QuaiNetworkContext]-1], nil

}

// HLCR
func (sl *Slice) HLCR(externTd *big.Int) bool {
	currentTd := sl.hc.GetTdByHash(sl.hc.CurrentHeader().Hash())
	return currentTd[types.QuaiNetworkContext].Cmp(externTd) < 0
}

// CalcTd calculates the TD of the given header using PCRC and CalcHLCRNetDifficulty.
func (sl *Slice) CalcTd(header *types.Header) (*big.Int, error) {
	priorTd := sl.hc.GetTd(header.Parent(), header.Number64()-1)
	if priorTd[types.QuaiNetworkContext] == nil {
		return nil, consensus.ErrFutureBlock
	}
	Td := priorTd[types.QuaiNetworkContext].Add(priorTd[types.QuaiNetworkContext], header.Difficulty[types.QuaiNetworkContext])
	return Td, nil
}

// writePendingHeader updates the slice pending header at the given index with the value from given header.
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

func (sl *Slice) GetPendingHeader() (*types.Header, error) {
	// get the current header and get the pending header associated with the termini
	fmt.Println("GetPendingHeader CurrentHeader", sl.hc.CurrentHeader().Hash())
	termini := sl.hc.GetTerminiByHash(sl.hc.CurrentHeader().Hash())
	fmt.Println("termini:", termini)
	fmt.Println("sl.phCache:", sl.phCache)
	fmt.Println("sl.phCahce[termini[3]]:", sl.phCache[termini[3]])
	header := sl.phCache[termini[3]].Header
	fmt.Println("header", header)
	return header, nil
}

func (sl *Slice) SubRelayPendingHeader(pendingHeader types.PendingHeader) error {
	localBestPh, _ := sl.updatePhCache(pendingHeader)
	fmt.Println("SubRelayPendingHeader Local Hash:", localBestPh.Header.Hash(), "Number:", localBestPh.Header.Number, "Parents:", localBestPh.Header.ParentHash, "bestPh:", localBestPh)
	if types.QuaiNetworkContext != params.ZONE {
		for i := range sl.subClients {
			err := sl.subClients[i].SubRelayPendingHeader(context.Background(), localBestPh)
			if err != nil {
				fmt.Println("SubRelayPendingHeader err:", err)
			}
		}
	} else {
		localBestPh.Header.Location = sl.config.Location

		for i := range sl.phCache {
			fmt.Println("SubRelayPendingHeader Cache Location:", sl.phCache[i].Header.Location, "Number:", sl.phCache[i].Header.Number, "td", sl.phCache[i].Td, "key:", i, "termini", sl.phCache[i].Termini, "ParentHash:", sl.phCache[i].Header.ParentHash)
		}
		sl.miner.worker.pendingHeaderFeed.Send(localBestPh.Header)
	}
	return nil
}

func (sl *Slice) DomRelayPendingHeader(pendingHeader types.PendingHeader) error {
	localBestPh, _ := sl.updatePhCache(pendingHeader)
	fmt.Println("DomRelayPendingHeader Local Hash:", localBestPh.Header.Hash(), "Number:", localBestPh.Header.Number, "Parents:", localBestPh.Header.ParentHash, "bestPh:", localBestPh)
	if types.QuaiNetworkContext != params.PRIME {
		err := sl.domClient.DomRelayPendingHeader(context.Background(), pendingHeader)
		if err != nil {
			fmt.Println("DomRelayPendingHeader err:", err)
		}
	} else {

		for i := range sl.phCache {
			fmt.Println("DomRelayPendingHeader Cache Location:", sl.phCache[i].Header.Location, "Number:", sl.phCache[i].Header.Number, "td", sl.phCache[i].Td, "key:", i, "termini", sl.phCache[i].Termini, "ParentHash:", sl.phCache[i].Header.ParentHash)
		}
		sl.miner.worker.pendingHeaderFeed.Send(localBestPh.Header)
	}
	return nil
}

func (sl *Slice) GetBestPendingHeader(externPendingHeader types.PendingHeader, hash common.Hash) types.PendingHeader {
	pendingHeader, exists := sl.phCache[hash]
	if !exists {
		var err error
		block := sl.hc.GetBlockByHash(sl.hc.CurrentHeader().Hash())
		if block == nil {
			log.Error("Couldn't find block in GetBestPendingHeader to initialize pHcache")
		}
		td := sl.hc.GetTdByHash(block.Hash())
		pendingHeader, err = sl.setHeaderChainHead(sl.sliceDb.NewBatch(), block, td[types.QuaiNetworkContext], false, true)
		if err != nil {
			log.Error("Unable to initialize the pHCache, err", err)
		}
		sl.phCache[hash] = pendingHeader
	}
	return pendingHeader
}

func (sl *Slice) AddIfBestPendingHeader(externPendingHeader types.PendingHeader) error {
	if types.QuaiNetworkContext != params.ZONE {
		for i := 0; i < len(externPendingHeader.Termini)-1; i++ {
			hash := externPendingHeader.Termini[i]
			pendingHeader, exists := sl.phCache[hash]
			if !exists {
				sl.phCache[hash] = externPendingHeader
			} else {
				fmt.Println("localPendingHeader index:", i, "Hash:", pendingHeader.Header.Hash(), "Number:", pendingHeader.Header.Number, "Td:", pendingHeader.Td, "Parents:", pendingHeader.Header.Parent(), "bestPh:", pendingHeader)
				fmt.Println("externPendingHeader index:", i, "Hash:", externPendingHeader.Header.Hash(), "Number:", externPendingHeader.Header.Number, "Td:", externPendingHeader.Td, "Parents:", externPendingHeader.Header.Parent(), "bestPh:", externPendingHeader)
				if externPendingHeader.Header.Number64() > pendingHeader.Header.Number64() {
					pendingHeader.Header = sl.combinePendingHeader(externPendingHeader.Header, pendingHeader.Header, types.QuaiNetworkContext)
					pendingHeader.Td = externPendingHeader.Td
					fmt.Println("resultPendingHeader index:", i, "Hash:", pendingHeader.Header.Hash(), "Number:", pendingHeader.Header.Number, "Td:", pendingHeader.Td, "Parents:", pendingHeader.Header.Parent(), "bestPh:", pendingHeader)
					sl.phCache[hash] = pendingHeader
				}
			}
		}
	} else {
		hash := externPendingHeader.Termini[3]
		pendingHeader, exists := sl.phCache[hash]
		if !exists {
			sl.phCache[hash] = externPendingHeader
		} else {
			if externPendingHeader.Header.Number64() > pendingHeader.Header.Number64() {
				pendingHeader.Header = sl.combinePendingHeader(externPendingHeader.Header, pendingHeader.Header, types.QuaiNetworkContext)
				pendingHeader.Td = externPendingHeader.Td
				sl.phCache[hash] = pendingHeader
			}
		}
	}

	return nil
}

func (sl *Slice) updatePhCache(pendingHeader types.PendingHeader) (types.PendingHeader, error) {
	var slicePendingHeader types.PendingHeader
	if types.QuaiNetworkContext != params.ZONE {
		for i := 0; i < len(pendingHeader.Termini)-1; i++ {
			hash := pendingHeader.Termini[i]
			localPendingHeader, exists := sl.phCache[hash]
			if !exists {
				sl.phCache[hash] = pendingHeader
				slicePendingHeader = pendingHeader
			} else {
				fmt.Println("localPendingHeader index:", i, "Hash:", localPendingHeader.Header.Hash(), "Number:", localPendingHeader.Header.Number, "Td:", localPendingHeader.Td, "Parents:", localPendingHeader.Header.Parent(), "bestPh:", localPendingHeader)
				fmt.Println("pendingHeader index:", i, "Hash:", pendingHeader.Header.Hash(), "Number:", pendingHeader.Header.Number, "Td:", pendingHeader.Td, "Parents:", pendingHeader.Header.Parent(), "bestPh:", pendingHeader)
				localPendingHeader.Header = sl.combinePendingHeader(pendingHeader.Header, localPendingHeader.Header, types.QuaiNetworkContext)
				localPendingHeader.Td = pendingHeader.Td
				fmt.Println("resultPendingHeader index:", i, "Hash:", localPendingHeader.Header.Hash(), "Number:", localPendingHeader.Header.Number, "Td:", localPendingHeader.Td, "Parents:", localPendingHeader.Header.Parent(), "bestPh:", localPendingHeader)
				sl.phCache[hash] = localPendingHeader
				if sl.config.Location[types.QuaiNetworkContext] == byte(i) {
					slicePendingHeader = localPendingHeader
				}
			}
		}
	} else {
		hash := pendingHeader.Termini[3]
		localPendingHeader, exists := sl.phCache[hash]
		if !exists {
			sl.phCache[hash] = pendingHeader
			slicePendingHeader = pendingHeader
		} else {

			localPendingHeader.Header = sl.combinePendingHeader(pendingHeader.Header, localPendingHeader.Header, types.QuaiNetworkContext)
			localPendingHeader.Td = pendingHeader.Td
			sl.phCache[hash] = localPendingHeader
			slicePendingHeader = localPendingHeader
		}
	}

	return slicePendingHeader, nil
}

//TODO
//DeleteBestPendingHeader
//

// MakeDomClient creates the quaiclient for the given domurl
func MakeDomClient(domurl string) *quaiclient.Client {
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
func MakeSubClients(suburls []string) []*quaiclient.Client {
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
		// Insert one by one as chain insertion needs contiguous ancestry between blocks
		for i := range blocks {
			fmt.Println("blocks in future blocks", blocks[i].Header().Number, blocks[i].Header().Hash())
		}
		// Insert one by one as chain insertion needs contiguous ancestry between blocks
		for i := range blocks {
			var nilHash common.Hash
			sl.Append(blocks[i], nilHash, big.NewInt(0), false, true)
		}
	}
}

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

func (sl *Slice) GetSliceHeadHash(index byte) common.Hash { return common.Hash{} }

func (sl *Slice) GetHeadHash() common.Hash { return sl.hc.currentHeaderHash }

func (sl *Slice) Config() *params.ChainConfig { return sl.config }

func (sl *Slice) Engine() consensus.Engine { return sl.engine }

func (sl *Slice) HeaderChain() *HeaderChain { return sl.hc }

func (sl *Slice) TxPool() *TxPool { return sl.txPool }

func (sl *Slice) Miner() *Miner { return sl.miner }

func (sl *Slice) PendingBlockBody(hash common.Hash) *types.Body {
	return rawdb.ReadPendginBlockBody(sl.sliceDb, hash)
}
