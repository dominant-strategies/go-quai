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
	maxFutureBlocks     = 256
	maxFutureHeads      = 20
	maxTimeFutureBlocks = 30
	maxTimeFutureHeads  = 5
)

type Slice struct {
	hc            *HeaderChain
	currentHeads  []*types.Header
	pendingHeader *types.Header

	txPool *TxPool
	miner  *Miner

	config *params.ChainConfig
	engine consensus.Engine

	quit chan struct{} // slice quit channel

	subClients   []*quaiclient.Client // subClinets is used to check is a coincident block is valid in the subordinate context
	futureBlocks *lru.Cache
	futureHeads  *lru.Cache
	slicemu      sync.RWMutex

	nilHeader *types.Header

	wg sync.WaitGroup // slice processing wait group for shutting down
}

func NewSlice(db ethdb.Database, config *Config, txConfig *TxPoolConfig, isLocalBlock func(block *types.Header) bool, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config) (*Slice, error) {
	sl := &Slice{
		config: chainConfig,
		engine: engine,
	}

	futureBlocks, _ := lru.New(maxFutureBlocks)
	sl.futureBlocks = futureBlocks
	futureHeads, _ := lru.New(maxFutureHeads)
	sl.futureHeads = futureHeads
	sl.currentHeads = make([]*types.Header, 3)

	var err error
	sl.hc, err = NewHeaderChain(db, engine, chainConfig, cacheConfig, vmConfig)
	if err != nil {
		return nil, err
	}

	sl.txPool = NewTxPool(*txConfig, chainConfig, sl.hc)
	sl.miner = New(sl.hc, sl.txPool, config, chainConfig, engine, isLocalBlock)
	sl.miner.SetExtra(sl.miner.MakeExtraData(config.ExtraData))

	sl.currentHeads[0] = sl.hc.genesisHeader
	sl.currentHeads[1] = sl.hc.genesisHeader
	sl.currentHeads[2] = sl.hc.genesisHeader

	// Update the pending header to the genesis Header.
	sl.pendingHeader = sl.hc.genesisHeader

	// only set the subClients if the chain is not zone
	sl.subClients = make([]*quaiclient.Client, 3)
	if types.QuaiNetworkContext != params.ZONE {
		sl.subClients = MakeSubClients(subClientUrls)
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

	if types.QuaiNetworkContext == params.PRIME {
		sl.UpdatePendingHeader(sl.pendingHeader, sl.nilHeader)
	}

	go sl.updateFutureBlocks()
	go sl.updateFutureHeads()
	go sl.sendPendingHeaderToFeed()

	return sl, nil
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

// Config retrieves the slice's chain configuration.
func (sl *Slice) Config() *params.ChainConfig { return sl.config }

// Engine retrieves the header chain's consensus engine.
func (sl *Slice) Engine() consensus.Engine { return sl.engine }

func (sl *Slice) SliceAppend(block *types.Block) error {

	// PCRC
	order, err := sl.engine.GetDifficultyOrder(block.Header())
	if err != nil {
		return err
	}

	_, err = sl.PCRC(block, order)
	if err != nil {
		fmt.Println("Slice error in PCRC", err)
		errUntwist := sl.untwistHead(block, err)
		if errUntwist != nil {
			return errUntwist
		}
		return err
	}

	// CalcTd on the new block
	td, err := sl.CalcTd(block.Header())
	if err != nil {
		return err
	}

	// Append
	// if the context is not zone, we have to wait for the append in the sub
	err = sl.Append(block, td)
	if err != nil {
		return err
	}

	reorg := sl.HLCR(td)

	if reorg {
		if err := sl.SetHeaderChainHead(block.Header()); err != nil {
			return err
		}
		// Update the pending Header.
		currentPending := sl.pendingHeader
		err := sl.UpdatePendingHeader(block.Header(), sl.pendingHeader)
		if err != nil {
			sl.pendingHeader = currentPending
		}
	} else {
		//sl.hc.bc.chainSideFeed.Send(ChainSideEvent{Block: block})
	}

	return nil
}

func (sl *Slice) UpdatePendingHeader(header *types.Header, pendingHeader *types.Header) error {

	fmt.Println("header location: ", header.Location, "header number:", header.Number)
	fmt.Println("pending location: ", pendingHeader.Location, "pending number", pendingHeader.Number)

	if header.Hash() == sl.config.GenesisHashes[types.QuaiNetworkContext] {
		sl.pendingHeader = pendingHeader

		// Collect the pending block.
		localPendingHeader, err := sl.miner.worker.GeneratePendingHeader(header)
		if err != nil {
			fmt.Println("pending block error: ", err)
			return err
		}
		fmt.Println("pending Header: ", localPendingHeader)

		index := types.QuaiNetworkContext

		sl.pendingHeader.ParentHash[index] = localPendingHeader.ParentHash[index]
		sl.pendingHeader.UncleHash[index] = localPendingHeader.UncleHash[index]
		sl.pendingHeader.Number[index] = localPendingHeader.Number[index]
		sl.pendingHeader.Extra[index] = localPendingHeader.Extra[index]
		sl.pendingHeader.BaseFee[index] = localPendingHeader.BaseFee[index]
		sl.pendingHeader.GasLimit[index] = localPendingHeader.GasLimit[index]
		sl.pendingHeader.GasUsed[index] = localPendingHeader.GasUsed[index]
		sl.pendingHeader.TxHash[index] = localPendingHeader.TxHash[index]
		sl.pendingHeader.ReceiptHash[index] = localPendingHeader.ReceiptHash[index]
		sl.pendingHeader.Root[index] = localPendingHeader.Root[index]
		sl.pendingHeader.Difficulty[index] = localPendingHeader.Difficulty[index]
		sl.pendingHeader.Coinbase[index] = localPendingHeader.Coinbase[index]
		sl.pendingHeader.Bloom[index] = localPendingHeader.Bloom[index]
		sl.pendingHeader.Time = localPendingHeader.Time

	} else {
		fmt.Println("header.hash:", header.Hash(), "sl.nilHeader.Hash:", sl.nilHeader.Hash())
		if header.Number[0] != nil && header.Number[1] != nil && header.Number[2] != nil {
			sl.pendingHeader = pendingHeader

			// Collect the pending block.
			localPendingHeader, err := sl.miner.worker.GeneratePendingHeader(header)
			if err != nil {
				fmt.Println("pending block error: ", err)
				return err
			}
			fmt.Println("pending Header: ", localPendingHeader)

			index := types.QuaiNetworkContext

			sl.pendingHeader.ParentHash[index] = localPendingHeader.ParentHash[index]
			sl.pendingHeader.UncleHash[index] = localPendingHeader.UncleHash[index]
			sl.pendingHeader.Number[index] = localPendingHeader.Number[index]
			sl.pendingHeader.Extra[index] = localPendingHeader.Extra[index]
			sl.pendingHeader.BaseFee[index] = localPendingHeader.BaseFee[index]
			sl.pendingHeader.GasLimit[index] = localPendingHeader.GasLimit[index]
			sl.pendingHeader.GasUsed[index] = localPendingHeader.GasUsed[index]
			sl.pendingHeader.TxHash[index] = localPendingHeader.TxHash[index]
			sl.pendingHeader.ReceiptHash[index] = localPendingHeader.ReceiptHash[index]
			sl.pendingHeader.Root[index] = localPendingHeader.Root[index]
			sl.pendingHeader.Difficulty[index] = localPendingHeader.Difficulty[index]
			sl.pendingHeader.Coinbase[index] = localPendingHeader.Coinbase[index]
			sl.pendingHeader.Bloom[index] = localPendingHeader.Bloom[index]

			sl.pendingHeader.Time = localPendingHeader.Time

		} else {
			for index := types.QuaiNetworkContext - 1; index >= 0; index-- {
				if types.QuaiNetworkContext != params.PRIME {
					//index := types.QuaiNetworkContext - 1

					sl.pendingHeader.ParentHash[index] = pendingHeader.ParentHash[index]
					sl.pendingHeader.UncleHash[index] = pendingHeader.UncleHash[index]
					sl.pendingHeader.Number[index] = pendingHeader.Number[index]
					sl.pendingHeader.Extra[index] = pendingHeader.Extra[index]
					sl.pendingHeader.BaseFee[index] = pendingHeader.BaseFee[index]
					sl.pendingHeader.GasLimit[index] = pendingHeader.GasLimit[index]
					sl.pendingHeader.GasUsed[index] = pendingHeader.GasUsed[index]
					sl.pendingHeader.TxHash[index] = pendingHeader.TxHash[index]
					sl.pendingHeader.ReceiptHash[index] = pendingHeader.ReceiptHash[index]
					sl.pendingHeader.Root[index] = pendingHeader.Root[index]
					sl.pendingHeader.Difficulty[index] = pendingHeader.Difficulty[index]
					sl.pendingHeader.Coinbase[index] = pendingHeader.Coinbase[index]
					sl.pendingHeader.Bloom[index] = pendingHeader.Bloom[index]

					sl.pendingHeader.Time = pendingHeader.Time
				}
			}

		}

	}

	// Send the pending blocks down to all the subclients.
	if types.QuaiNetworkContext != params.ZONE {
		for i := range sl.subClients {
			if sl.subClients[i] != nil {
				if header.Hash() == sl.config.GenesisHashes[types.QuaiNetworkContext] {
					fmt.Println("sending on genesis from context: ", types.QuaiNetworkContext, "to sub", i)
					err := sl.subClients[i].UpdatePendingHeader(context.Background(), header, sl.pendingHeader)
					if err != nil {
						return err
					}
				} else if len(header.Location) != 0 {
					if header.Location[types.QuaiNetworkContext]-1 == byte(i) {
						fmt.Println("sending on overlapping context: ", types.QuaiNetworkContext, "to sub", i)
						err := sl.subClients[i].UpdatePendingHeader(context.Background(), header, sl.pendingHeader)
						if err != nil {
							return err
						}
					} else {
						fmt.Println("sending on nilHeader on coordinate context: ", types.QuaiNetworkContext, "to sub", i)
						err := sl.subClients[i].UpdatePendingHeader(context.Background(), sl.nilHeader, sl.pendingHeader)
						if err != nil {
							return err
						}
					}
				} else {
					fmt.Println("sending on nilHeader on coordinate context: ", types.QuaiNetworkContext, "to sub", i)
					err := sl.subClients[i].UpdatePendingHeader(context.Background(), sl.nilHeader, sl.pendingHeader)
					if err != nil {
						return err
					}
				}

			}
		}
	} else {
		fmt.Println("Pending Header location: ", sl.pendingHeader.Location, "Pending Header Number:", sl.pendingHeader.Number)
		fmt.Println("Header location: ", header.Location, "Header Number:", header.Number)
		sl.pendingHeader.Location = sl.config.Location
		sl.miner.worker.pendingHeaderFeed.Send(sl.pendingHeader)
	}
	return nil
}

func (sl *Slice) untwistHead(block *types.Block, err error) error {
	// If we have a twist we may need to redirect head/(s)
	if errors.Is(err, consensus.ErrPrimeTwist) || errors.Is(err, consensus.ErrRegionTwist) {
		// fmt.Println("type of twist in PCRC:", err)
		// fmt.Println("Conditions to reset head:")
		// fmt.Println("currentHeaderHash:", sl.hc.currentHeaderHash)
		// fmt.Println("parentHash:", block.Header().ParentHash[types.QuaiNetworkContext])
		// fmt.Println("subClient.HeadHash:", sl.subClients[block.Header().Location[types.QuaiNetworkContext]-1].GetHeadHash(context.Background()))
		// fmt.Println("currentBlock_sub_parent:", block.Header().ParentHash[types.QuaiNetworkContext+1])

		// Only if the twisted block was mined by or could have been mined by me switch heads
		// This check prevents us from repointing our head if old or non-relavent twisted blocks
		// are presented
		if sl.hc.currentHeaderHash == block.Header().ParentHash[types.QuaiNetworkContext] &&
			sl.subClients[block.Header().Location[types.QuaiNetworkContext]-1].GetHeadHash(context.Background()) == block.Header().ParentHash[types.QuaiNetworkContext+1] {

			// If there is a prime twist this is a PRTP != PRTR so we should drop back to previous slice head
			if errors.Is(err, consensus.ErrPrimeTwist) {
				sl.SetHeaderChainHead(sl.currentHeads[block.Header().Location[types.QuaiNetworkContext]-1])
				return nil
				// If there is a region twist the region needs to fall back to the parent of the previous slice head
			} else if errors.Is(err, consensus.ErrRegionTwist) {
				parentHeader := sl.hc.GetHeaderByHash(sl.currentHeads[block.Header().Location[types.QuaiNetworkContext]-1].ParentHash[types.QuaiNetworkContext])
				if parentHeader != nil {
					sl.SetHeaderChainHead(parentHeader)
					return nil
				}
			}
			return err
		}
	}
	return nil
}

// Append
func (sl *Slice) Append(block *types.Block, td *big.Int) error {
	sl.slicemu.Lock()
	defer sl.slicemu.Unlock()
	err := sl.hc.Append(block)
	if err != nil {
		fmt.Println("Slice error in append", err)
		return err
	}

	// WriteTd
	// Remove this once td is converted to a single value.
	externTd := make([]*big.Int, 3)
	externTd[types.QuaiNetworkContext] = td
	rawdb.WriteTd(sl.hc.headerDb, block.Hash(), block.NumberU64(), externTd)

	if types.QuaiNetworkContext != params.ZONE {
		// Perform the sub append
		err = sl.subClients[block.Header().Location[types.QuaiNetworkContext]-1].Append(context.Background(), block, td)
		// If the append errors out in the sub we can delete the block from the headerchain.
		if err != nil {
			rawdb.DeleteTd(sl.hc.headerDb, block.Header().Hash(), block.Header().Number64())
			rawdb.DeleteHeader(sl.hc.headerDb, block.Header().Hash(), block.Header().Number64())
		}
	}

	return nil
}

func (sl *Slice) SetHeaderChainHead(head *types.Header) error {
	oldHead := sl.hc.CurrentHeader()
	fmt.Println("setting head to:", head.Hash())
	sliceHeaders, err := sl.hc.SetCurrentHeader(head)

	if err != nil {
		return err
	}

	for i, header := range sliceHeaders {
		fmt.Println("sliceHeader[i]:", i, header.Hash())
		if header != nil && types.QuaiNetworkContext != params.ZONE {
			sl.currentHeads[i] = header
		}
	}

	// set head of subs
	if types.QuaiNetworkContext != params.ZONE {
		// Perform the sub append
		err = sl.subClients[head.Location[types.QuaiNetworkContext]-1].SetHeaderChainHead(context.Background(), head)
		// If the append errors out in the sub we can delete the block from the headerchain.
		if err != nil {
			fmt.Println("reverting to old headers")
			sliceHeaders, _ := sl.hc.SetCurrentHeader(oldHead)
			for i, header := range sliceHeaders {
				fmt.Println("sliceHeader[i]:", i, header.Hash())
				if header != nil && types.QuaiNetworkContext != params.ZONE {
					sl.currentHeads[i] = header
				}
			}
		}
	}

	return nil
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

// The purpose of the Previous Coincident Reference Check (PCRC) is to establish
// that we have linked untwisted chains prior to checking HLCR & applying external state transfers.
// NOTE: note that it only guarantees linked & untwisted back to the prime terminus, assuming the
// prime termini match. To check deeper than that, you need to iteratively apply PCRC to get that guarantee.
func (sl *Slice) PCRC(block *types.Block, headerOrder int) (types.PCRCTermini, error) {
	header := block.Header()
	if header.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
		return types.PCRCTermini{}, nil
	}

	slice := header.Location

	switch types.QuaiNetworkContext {
	case params.PRIME:
		PTP, err := sl.PreviousValidCoincident(header, slice, params.PRIME, true)
		if err != nil {
			return types.PCRCTermini{}, err
		}
		PRTP, err := sl.PreviousValidCoincident(header, slice, params.PRIME, false)
		if err != nil {
			return types.PCRCTermini{}, err
		}

		if sl.subClients[slice[0]-1] == nil {
			return types.PCRCTermini{}, nil
		}
		PCRCTermini, err := sl.subClients[slice[0]-1].CheckPCRC(context.Background(), block, headerOrder)
		if err != nil {
			return types.PCRCTermini{}, err
		}

		if (PCRCTermini.PTR == common.Hash{} || PCRCTermini.PRTR == common.Hash{}) {
			return PCRCTermini, consensus.ErrSliceNotSynced
		}

		PCRCTermini.PTP = PTP.Hash()
		PCRCTermini.PRTP = PRTP.Hash()

		if (PTP.Hash() != PCRCTermini.PTR) && (PCRCTermini.PTR != PCRCTermini.PTZ) && (PCRCTermini.PTZ != PTP.Hash()) {
			return types.PCRCTermini{}, errors.New("there exists a Prime twist (PTP != PTR != PTZ")
		}
		if PRTP.Hash() != PCRCTermini.PRTR {
			return types.PCRCTermini{}, consensus.ErrPrimeTwist
		}

		return PCRCTermini, nil

	case params.REGION:
		RTR, err := sl.PreviousValidCoincident(header, slice, params.REGION, true)
		if err != nil {
			return types.PCRCTermini{}, err
		}

		if sl.subClients[slice[1]-1] == nil {
			return types.PCRCTermini{}, nil
		}

		PCRCTermini, err := sl.subClients[slice[1]-1].CheckPCRC(context.Background(), block, headerOrder)
		if err != nil {
			return types.PCRCTermini{}, err
		}

		if (PCRCTermini.RTZ == common.Hash{}) {
			return PCRCTermini, consensus.ErrSliceNotSynced
		}

		if RTR.Hash() != PCRCTermini.RTZ {
			return types.PCRCTermini{}, consensus.ErrRegionTwist
		}
		if headerOrder < params.REGION {
			PTR, err := sl.PreviousValidCoincident(header, slice, params.PRIME, true)
			if err != nil {
				return types.PCRCTermini{}, err
			}
			PRTR, err := sl.PreviousValidCoincident(header, slice, params.PRIME, false)
			if err != nil {
				return types.PCRCTermini{}, err
			}

			PCRCTermini.PTR = PTR.Hash()
			PCRCTermini.PRTR = PRTR.Hash()
		}
		return PCRCTermini, nil

	case params.ZONE:
		PCRCTermini := types.PCRCTermini{}

		// only compute PTZ and RTZ on the coincident block in zone.
		// PTZ and RTZ are essentially a signaling mechanism to know that we are building on the right terminal header.
		// So running this only on a coincident block makes sure that the zones can move and sync past the coincident.
		// Just run RTZ to make sure that its linked. This check decouples this signaling and linking paradigm.

		PTZ, err := sl.PreviousValidCoincident(header, slice, params.PRIME, true)
		if err != nil {
			return types.PCRCTermini{}, err
		}
		PCRCTermini.PTZ = PTZ.Hash()

		RTZ, err := sl.PreviousValidCoincident(header, slice, params.REGION, true)
		if err != nil {
			return types.PCRCTermini{}, err
		}
		PCRCTermini.RTZ = RTZ.Hash()

		return PCRCTermini, nil
	}
	return types.PCRCTermini{}, errors.New("running in unsupported context")
}

// PreviousCoincident searches the path for a block of specified order in the specified slice
func (sl *Slice) PreviousValidCoincident(header *types.Header, slice []byte, order int, fullSliceEqual bool) (*types.Header, error) {

	if header.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
		return sl.hc.GetHeaderByHash(sl.hc.Config().GenesisHashes[0]), nil
	}

	if err := sl.hc.CheckContext(order); err != nil {
		return nil, err
	}
	if err := sl.hc.CheckLocationRange(slice); err != nil {
		return nil, err
	}

	for {
		// If block header is Genesis return it as coincident
		if header.Number[types.QuaiNetworkContext].Cmp(big.NewInt(1)) == 0 {
			return sl.hc.GetHeaderByHash(sl.hc.Config().GenesisHashes[0]), nil
		}

		// Get previous header on local chain by hash
		prevHeader := sl.hc.GetHeaderByHash(header.ParentHash[types.QuaiNetworkContext])
		if prevHeader == nil {
			return nil, consensus.ErrSliceNotSynced
		}
		// Increment previous header
		header = prevHeader

		// Find the order of the header
		difficultyOrder, err := sl.engine.GetDifficultyOrder(header)
		if err != nil {
			return nil, err
		}

		// If we have reached a coincident block of desired order in our desired slice
		var equal bool
		if fullSliceEqual {
			equal = bytes.Equal(header.Location, slice)
		} else {
			equal = header.Location[0] == slice[0]
		}
		if equal && difficultyOrder <= order {
			return header, nil
		}
	}
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

func (sl *Slice) procFutureHeads() {
	headers := make([]*types.Header, 0, sl.futureHeads.Len())
	for _, hash := range sl.futureHeads.Keys() {
		if head, exist := sl.futureHeads.Peek(hash); exist {
			headers = append(headers, head.(*types.Header))
		}
	}
	if len(headers) > 0 {
		sort.Slice(headers, func(i, j int) bool {
			return headers[i].Number64() < headers[j].Number64()
		})
		// Insert one by one as chain insertion needs contiguous ancestry between blocks
		for i := range headers {
			fmt.Println("headers i", headers[i])
			sl.hc.SetCurrentHeader(headers[i])
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

// addFutureHeads checks if the header is within the max allowed window to get
// accepted for future processing, and returns an error if the block is too far
// ahead and was not added.
func (sl *Slice) addFutureHead(header *types.Header) error {
	max := uint64(time.Now().Unix() + maxTimeFutureHeads)
	if header.HeaderTime() > max {
		return fmt.Errorf("future header timestamp %v > allowed %v", header.HeaderTime(), max)
	}
	if !sl.futureHeads.Contains(header.Hash()) {
		// add the header
		sl.futureHeads.Add(header.Hash(), header)
		// remove the parent if it exists in the futureHeads cache after adding the header.
		if sl.futureHeads.Contains(header.Parent()) {
			sl.futureHeads.Remove(header.Parent())
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

func (sl *Slice) updateFutureHeads() {
	futureTimer := time.NewTicker(1 * time.Second)
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			sl.procFutureHeads()
		case <-sl.quit:
			return
		}
	}
}

func (sl *Slice) sendPendingHeaderToFeed() {
	futureTimer := time.NewTicker(3 * time.Second)
	var count int
	defer futureTimer.Stop()
	defer sl.wg.Done()
	for {
		select {
		case <-futureTimer.C:
			if count < 100 {
				sl.miner.worker.pendingHeaderFeed.Send(sl.pendingHeader)
				count++
			}
		case <-sl.quit:
			return
		}
	}
}

func (sl *Slice) GetSliceHeadHash(index byte) common.Hash {
	if index < 3 {
		fmt.Println("current Head:", sl.currentHeads[index].Hash())
		return sl.currentHeads[index].Hash()
	}
	return common.Hash{}
}

func (sl *Slice) GetHeadHash() common.Hash {
	return sl.hc.currentHeaderHash
}
