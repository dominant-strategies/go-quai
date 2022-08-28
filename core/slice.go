package core

import (
	"context"
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
	maxFutureHeads      = 20
	maxTimeFutureBlocks = 30
	maxTimeFutureHeads  = 5
)

type Slice struct {
	hc *HeaderChain

	txPool *TxPool
	miner  *Miner

	sliceDb ethdb.Database
	config  *params.ChainConfig
	engine  consensus.Engine

	quit chan struct{} // slice quit channel

	subClients   []*quaiclient.Client // subClinets is used to check is a coincident block is valid in the subordinate context
	futureBlocks *lru.Cache

	appendmu sync.RWMutex

	nilHeader *types.Header

	wg sync.WaitGroup // slice processing wait group for shutting down
}

func NewSlice(db ethdb.Database, config *Config, txConfig *TxPoolConfig, isLocalBlock func(block *types.Header) bool, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config) (*Slice, error) {
	sl := &Slice{
		config:  chainConfig,
		engine:  engine,
		sliceDb: db,
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

	// only set the subClients if the chain is not zone
	sl.subClients = make([]*quaiclient.Client, 3)
	if types.QuaiNetworkContext != params.ZONE {
		sl.subClients = MakeSubClients(subClientUrls)
	}

	var pendingHeader *types.Header

	// load the pending header
	pendingHeader = rawdb.ReadPendingHeader(sl.sliceDb, sl.hc.CurrentHeader().Parent())
	if pendingHeader == nil {
		// Update the pending header to the genesis Header.
		pendingHeader = sl.hc.genesisHeader
	}

	if types.QuaiNetworkContext == params.PRIME {
		fmt.Println("update pending from PRIME on initial")
		sl.SetHeaderChainHead(pendingHeader, sl.hc.GetTdByHash(pendingHeader.Hash())[params.PRIME])
	}

	go sl.updateFutureBlocks()

	return sl, nil
}

//NOTES:
// Need header to have slice termini
//
// Append
func (sl *Slice) Append(block *types.Block) error {
	sl.appendmu.Lock()
	defer sl.appendmu.Unlock()

	// CalcTd on the new block
	td, err := sl.CalcTd(block.Header())
	if err != nil {
		return err
	}

	err = sl.hc.Append(block)
	if err != nil {
		fmt.Println("Slice error in append", err)
		return err
	}

	slPendingHeader, err := sl.SetHeaderChainHead(block.Header(), td)
	if err != nil {
		return err
	}
	// WriteTd
	// Remove this once td is converted to a single value.
	externTd := make([]*big.Int, 3)
	externTd[types.QuaiNetworkContext] = td
	rawdb.WriteTd(sl.hc.headerDb, block.Header().Hash(), block.NumberU64(), externTd)

	if types.QuaiNetworkContext != params.ZONE {
		// Perform the sub append
		err = sl.subClients[block.Header().Location[types.QuaiNetworkContext]-1].Append(context.Background(), block, td)
		// If the append errors out in the sub we can delete the block from the headerchain.
		if err != nil {
			rawdb.DeleteTd(sl.hc.headerDb, block.Header().Hash(), block.Header().Number64())
			rawdb.DeleteHeader(sl.hc.headerDb, block.Header().Hash(), block.Header().Number64())
			return err
		}
	}

	return nil
}

func (sl *Slice) SetHeaderChainHead(head *types.Header, td *big.Int) (*types.Header, error) {

	headerOrder, err := sl.engine.GetDifficultyOrder(head)
	if err != nil {
		return sl.nilHeader, err
	}

	reorg := sl.HLCR(td)
	if !reorg && !(headerOrder < types.QuaiNetworkContext) {
		return sl.nilHeader, nil
	}

	// Upate the local pending header
	slPendingHeader, err := sl.miner.worker.GeneratePendingHeader(head)
	if err != nil {
		fmt.Println("pending block error: ", err)
		return nil, err
	}

	return slPendingHeader, nil
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
func (sl *Slice) combinePendingHeader(indexHash common.Hash, header *types.Header, slPendingHeader *types.Header, index int) *types.Header {

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

	slPendingHeader.Location = sl.config.Location
	slPendingHeader.Time = header.Time

	return slPendingHeader
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
			td, _ := sl.CalcTd(blocks[i].Header())
			sl.Append(blocks[i], td)
		}
	}
}

// addFutureBlock checks if the block is within the max allowed window to get
// accepted for future processing, and returns an error if the block is too far
// ahead and was not added.
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

// AddFutureBlocks add batch of blocks to the future blocks queue.
func (sl *Slice) AddFutureBlocks(blocks []*types.Block) error {
	for i := range blocks {
		err := sl.addFutureBlock(blocks[i])
		if err != nil {
			return err
		}
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

func (sl *Slice) GetSliceHeadHash(index byte) common.Hash {
	return common.Hash{}
}

func (sl *Slice) GetHeadHash() common.Hash {
	return sl.hc.currentHeaderHash
}

// Config retrieves the slice's chain configuration.
func (sl *Slice) Config() *params.ChainConfig { return sl.config }

// Engine retrieves the header chain's consensus engine.
func (sl *Slice) Engine() consensus.Engine { return sl.engine }

func (sl *Slice) PendingBlockBody(hash common.Hash) *types.Body {
	return rawdb.ReadPendginBlockBody(sl.sliceDb, hash)
}

// HeaderChain retrieves the headerchain.
func (sl *Slice) HeaderChain() *HeaderChain {
	return sl.hc
}

// TxPool retrieves the txpool.
func (sl *Slice) TxPool() *TxPool {
	return sl.txPool
}

// Miner retrieves the miner.
func (sl *Slice) Miner() *Miner {
	return sl.miner
}
