package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

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

type Slice struct {
	hc *HeaderChain

	txPool *TxPool
	miner  *Miner

	sliceDb ethdb.Database

	config *params.ChainConfig
	engine consensus.Engine

	quit chan struct{} // slice quit channel

	subClients []*quaiclient.Client // subClinets is used to check is a coincident block is valid in the subordinate context
	slicemu    sync.RWMutex
	appendmu   sync.RWMutex

	nilHeader *types.Header

	wg sync.WaitGroup // slice processing wait group for shutting down
}

func NewSlice(db ethdb.Database, config *Config, txConfig *TxPoolConfig, isLocalBlock func(block *types.Header) bool, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config) (*Slice, error) {
	sl := &Slice{
		config:  chainConfig,
		engine:  engine,
		sliceDb: db,
	}

	var err error
	sl.hc, err = NewHeaderChain(db, engine, chainConfig, cacheConfig, vmConfig)
	if err != nil {
		return nil, err
	}

	sl.txPool = NewTxPool(*txConfig, chainConfig, sl.hc)
	sl.miner = New(sl.hc, sl.txPool, config, chainConfig, engine, isLocalBlock)
	sl.miner.SetExtra(sl.miner.MakeExtraData(config.ExtraData))

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

	var pendingHeader *types.Header
	// only set the currentheads and the genesis header to genesis value if the blockchain is empty.
	if sl.hc.empty() {
		// Update the pending header to the genesis Header.
		pendingHeader = sl.hc.genesisHeader

		currentHeads := make([]*types.Header, 3)
		currentHeads[0] = sl.hc.genesisHeader
		currentHeads[1] = sl.hc.genesisHeader
		currentHeads[2] = sl.hc.genesisHeader
		rawdb.WriteSliceCurrentHeads(sl.sliceDb, currentHeads, sl.hc.genesisHeader.Hash())
	} else {
		// load the pending header
		pendingHeader = rawdb.ReadPendingHeader(sl.sliceDb, sl.hc.currentHeaderHash)
	}

	if types.QuaiNetworkContext == params.PRIME {
		sl.UpdatePendingHeader(pendingHeader, sl.nilHeader)
	}

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
	sl.slicemu.Lock()
	defer sl.slicemu.Unlock()

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
		fmt.Println("parent hash: ", block.Header().Parent())
		currentPending := rawdb.ReadPendingHeader(sl.sliceDb, block.Header().Parent())
		if err := sl.SetHeaderChainHead(block.Header()); err != nil {
			return err
		}

		fmt.Println("current pending: ", currentPending)

		// Update the pending Header.
		err := sl.UpdatePendingHeader(block.Header(), currentPending)
		if err != nil {
			// TODO: call updatePendingHeader on the parent of the block just appended
			return err
		}
	} else {
		//sl.hc.bc.chainSideFeed.Send(ChainSideEvent{Block: block})
	}

	return nil
}

func (sl *Slice) UpdatePendingHeader(header *types.Header, pendingHeader *types.Header) error {

	var slPendingHeader *types.Header

	if header.Hash() == sl.config.GenesisHashes[types.QuaiNetworkContext] {
		// Collect the pending block.
		localPendingHeader, err := sl.miner.worker.GeneratePendingHeader(header)
		if err != nil {
			fmt.Println("pending block error: ", err)
			return err
		}
		fmt.Println("pending Header: ", localPendingHeader)
		sl.writePendingHeader(header, pendingHeader, localPendingHeader, types.QuaiNetworkContext)
		slPendingHeader = localPendingHeader
	} else {
		if header.Number[0] != nil && header.Number[1] != nil && header.Number[2] != nil {
			// Collect the pending block.
			localPendingHeader, err := sl.miner.worker.GeneratePendingHeader(header)
			if err != nil {
				fmt.Println("pending block error: ", err)
				return err
			}
			fmt.Println("pending Header: ", localPendingHeader)
			fmt.Println("current pending Header: ", pendingHeader)
			sl.writePendingHeader(header, pendingHeader, localPendingHeader, types.QuaiNetworkContext)
		} else {
			for index := types.QuaiNetworkContext - 1; index >= 0; index-- {
				if types.QuaiNetworkContext != params.PRIME {
					sl.writePendingHeader(header, rawdb.ReadPendingHeader(sl.sliceDb, sl.hc.currentHeaderHash), pendingHeader, index)
				}
			}
		}
	}

	if header.Hash() != slPendingHeader.Hash() {
		// pending header after update in the context
		slPendingHeader = rawdb.ReadPendingHeader(sl.sliceDb, header.Hash())
	}
	fmt.Println("slPendingHeader: ", slPendingHeader)

	// Send the pending blocks down to all the subclients.
	if types.QuaiNetworkContext != params.ZONE {
		for i := range sl.subClients {
			if sl.subClients[i] != nil {
				if header.Hash() == sl.config.GenesisHashes[types.QuaiNetworkContext] {
					fmt.Println("sending on genesis from context: ", types.QuaiNetworkContext, "to sub", i)
					err := sl.subClients[i].UpdatePendingHeader(context.Background(), header, slPendingHeader)
					if err != nil {
						return err
					}
				} else if len(header.Location) != 0 {
					if header.Location[types.QuaiNetworkContext]-1 == byte(i) {
						fmt.Println("sending on overlapping context: ", types.QuaiNetworkContext, "to sub", i)
						err := sl.subClients[i].UpdatePendingHeader(context.Background(), header, slPendingHeader)
						if err != nil {
							return err
						}
					} else {
						fmt.Println("sending on nilHeader on coordinate context: ", types.QuaiNetworkContext, "to sub", i)
						err := sl.subClients[i].UpdatePendingHeader(context.Background(), sl.nilHeader, slPendingHeader)
						if err != nil {
							return err
						}
					}
				} else {
					fmt.Println("sending on nilHeader on coordinate context: ", types.QuaiNetworkContext, "to sub", i)
					err := sl.subClients[i].UpdatePendingHeader(context.Background(), sl.nilHeader, slPendingHeader)
					if err != nil {
						return err
					}
				}
			}
		}
	} else {
		slPendingHeader.Location = sl.config.Location
		fmt.Println("location :", slPendingHeader.Location)
		sl.miner.worker.pendingHeaderFeed.Send(slPendingHeader)
	}
	return nil
}

// writePendingHeader updates the slice pending header at the given index with the value from given header.
func (sl *Slice) writePendingHeader(appendingHeader *types.Header, pendingHeader *types.Header, header *types.Header, index int) {
	pendingHeader.ParentHash[index] = header.ParentHash[index]
	pendingHeader.UncleHash[index] = header.UncleHash[index]
	pendingHeader.Number[index] = header.Number[index]
	pendingHeader.Extra[index] = header.Extra[index]
	pendingHeader.BaseFee[index] = header.BaseFee[index]
	pendingHeader.GasLimit[index] = header.GasLimit[index]
	pendingHeader.GasUsed[index] = header.GasUsed[index]
	pendingHeader.TxHash[index] = header.TxHash[index]
	pendingHeader.ReceiptHash[index] = header.ReceiptHash[index]
	pendingHeader.Root[index] = header.Root[index]
	pendingHeader.Difficulty[index] = header.Difficulty[index]
	pendingHeader.Coinbase[index] = header.Coinbase[index]
	pendingHeader.Bloom[index] = header.Bloom[index]
	pendingHeader.Time = header.Time

	fmt.Println("pending header on write: ", pendingHeader)
	if pendingHeader.ParentHash[index] == sl.Config().GenesisHashes[index] {
		rawdb.WritePendingHeader(sl.sliceDb, pendingHeader.ParentHash[index], pendingHeader)
	}
	// write the pending header to the datebase
	rawdb.WritePendingHeader(sl.sliceDb, appendingHeader.Hash(), pendingHeader)
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
			if errors.Is(err, consensus.ErrPrimeTwist) || errors.Is(err, consensus.ErrRegionTwist) {
				currentHeads := rawdb.ReadSliceCurrentHeads(sl.sliceDb, sl.hc.currentHeaderHash)
				err = sl.SetHeaderChainHead(currentHeads[block.Header().Location[types.QuaiNetworkContext]-1])
				if err != nil {
					return err
				}
				err = sl.UpdatePendingHeader(sl.hc.CurrentHeader(), rawdb.ReadPendingHeader(sl.sliceDb, sl.hc.currentHeaderHash))
				if err != nil {
					return err
				}

				reorgNumber := currentHeads[block.Header().Location[types.QuaiNetworkContext]-1].Number64()
				for i := 0; i < params.FullerOntology[types.QuaiNetworkContext]; i++ {
					if currentHeads[i].Number64() > reorgNumber && block.Header().Location[types.QuaiNetworkContext]-1 != byte(i) {
						err = sl.subClients[i].SetHeaderChainHead(context.Background(), currentHeads[i])
						if err != nil {
							return err
						}
						err = sl.subClients[i].UpdatePendingHeader(context.Background(), currentHeads[i], rawdb.ReadPendingHeader(sl.sliceDb, sl.hc.currentHeaderHash))
						if err != nil {
							return err
						}
					}
				}
				return err
			}
			return err
		}
	}
	return nil
}

// Append
func (sl *Slice) Append(block *types.Block, td *big.Int) error {
	sl.appendmu.Lock()
	defer sl.appendmu.Unlock()

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
			return err
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
		if header != nil && types.QuaiNetworkContext != params.ZONE {
			if header.Hash() != params.NilHeaderHash {
				sl.writeCurrentHeads(header, i)
			}
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
					if header.Hash() != params.NilHeaderHash {
						sl.writeCurrentHeads(header, i)
					}
				}
			}
			return err
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

func (sl *Slice) writeCurrentHeads(header *types.Header, index int) {
	// get the current heads of the parent
	currentHeads := rawdb.ReadSliceCurrentHeads(sl.sliceDb, header.Parent())
	currentHeads[index] = header
	// write the currentHeads
	rawdb.WriteSliceCurrentHeads(sl.sliceDb, currentHeads, header.Hash())
}

func (sl *Slice) Stop() {
	// stop the headerchain
	sl.hc.Stop()
}

func (sl *Slice) sendPendingHeaderToFeed() {
	futureTimer := time.NewTicker(3 * time.Second)
	var count int
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			if count < 100 {
				pendingHeader := rawdb.ReadPendingHeader(sl.sliceDb, sl.hc.currentHeaderHash)
				if types.QuaiNetworkContext == params.ZONE {
					pendingHeader.Location = sl.config.Location
				}
				sl.miner.worker.pendingHeaderFeed.Send(pendingHeader)
				count++
			}
		case <-sl.quit:
			return
		}
	}
}

func (sl *Slice) GetHeadHash() common.Hash {
	return sl.hc.currentHeaderHash
}
