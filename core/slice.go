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

type Slice struct {
	hc *HeaderChain

	config *params.ChainConfig
	engine consensus.Engine

	quit chan struct{} // slice quit channel

	domClient    *quaiclient.Client   // domClient is used to check if a given dominant block in the chain is canonical in dominant chain.
	subClients   []*quaiclient.Client // subClinets is used to check is a coincident block is valid in the subordinate context
	futureBlocks *lru.Cache           // future blocks are blocks added for later processing

	wg sync.WaitGroup // slice processing wait group for shutting down
}

func NewSlice(db ethdb.Database, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config) (*Slice, error) {
	sl := &Slice{
		config: chainConfig,
		engine: engine,
	}

	futureBlocks, _ := lru.New(maxFutureBlocks)
	sl.futureBlocks = futureBlocks

	var err error
	sl.hc, err = NewHeaderChain(db, engine, chainConfig, cacheConfig, vmConfig)
	if err != nil {
		return nil, err
	}

	// only set the domClient if the chain is not prime
	if types.QuaiNetworkContext != params.PRIME {
		sl.domClient = MakeDomClient(domClientUrl)
	}

	sl.subClients = make([]*quaiclient.Client, 3)
	// only set the subClients if the chain is not region
	if types.QuaiNetworkContext != params.ZONE {
		go func() {
			sl.subClients = MakeSubClients(subClientUrls)
		}()
	}

	go sl.update()

	return sl, nil
}

// HeaderChain retrieves the headerchain.
func (sl *Slice) HeaderChain() *HeaderChain {
	return sl.hc
}

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

// Config retrieves the slice's chain configuration.
func (sl *Slice) Config() *params.ChainConfig { return sl.config }

// Engine retrieves the header chain's consensus engine.
func (sl *Slice) Engine() consensus.Engine { return sl.engine }

// Append
func (sl *Slice) Append(block *types.Block) error {

	order, err := sl.engine.GetDifficultyOrder(block.Header())
	if err != nil {
		return err
	}
	fmt.Println("after difficulty header")

	_, err = sl.PCRC(block, order)
	if err != nil {
		fmt.Println("Slice error in PCRC", err)
		return err
	}
	fmt.Println("after PCRC")

	td, err := sl.CalcTd(block.Header())
	fmt.Println("td for block", td)
	if err != nil {
		if errors.Is(err, consensus.ErrFutureBlock) {
			sl.addFutureBlock(block)
		}
		fmt.Println("Slice error in CalcTd", err)
		return err
	}
	fmt.Println("after calctd, td:", td)

	logs, err := sl.hc.Append(block)
	sl.futureBlocks.Remove(block.Hash())
	if err != nil {
		fmt.Println("Slice error in append", err)
		return err
	}
	fmt.Println("after headerchain append")

	rawdb.WriteTd(sl.hc.headerDb, block.Hash(), block.NumberU64(), td)

	// We have a new possible head call HLCR to potentially set
	currentTd := sl.hc.GetTd(sl.hc.currentHeaderHash, sl.hc.CurrentHeader().Number64())
	fmt.Println("Slice difficulties", sl.hc.currentHeaderHash, currentTd, block.Header().Hash(), td)
	reorg := sl.HLCR(currentTd, td)

	if order < types.QuaiNetworkContext {
		canonical := sl.domClient.GetCanonicalHashByNumber(context.Background(), block.Header().Number[types.QuaiNetworkContext-1])
		fmt.Println("canonical", canonical, block.Header().Number, block.Header().Hash())
		if (canonical == common.Hash{}) {
			fmt.Println("add to future block", block.Header().Number, block.Header().Hash())
			sl.addFutureBlock(block)
			return nil
		}

		// If the header is cononical break else keep looking
		if canonical != block.Header().Hash() {
			reorg = false
		}
	}

	fmt.Println("calling events in slice append", block.Header().Number, block.Header().Hash())
	if reorg {
		err = sl.hc.SetCurrentHeader(block.Header())
		if err != nil {
			return err
		}
		sl.hc.bc.chainFeed.Send(ChainEvent{Block: block, Hash: block.Hash(), Logs: logs})
		if len(logs) > 0 {
			sl.hc.bc.logsFeed.Send(logs)
		}

		// In theory we should fire a ChainHeadEvent when we inject
		// a canonical block, but sometimes we can insert a batch of
		// canonicial blocks. Avoid firing too many ChainHeadEvents,
		// we will fire an accumulated ChainHeadEvent and disable fire
		// event here.
		if true {
			sl.hc.chainHeadFeed.Send(ChainHeadEvent{Block: block})
		}

		// Everytime the total difficulty is written the PreviousCanonicalCoincident(PCC) check is done on our chain
		// and if we have a sub client in the given slice, PCC is triggered there as well
		if types.QuaiNetworkContext != params.PRIME {
			err = sl.PreviousCanonicalCoincident()
			if err != nil {
				return err
			}
		}
		for i := 0; i < len(params.FullerOntology); i++ {
			// check if we have a subsclient on that slice
			if sl.subClients[i] != nil {
				fmt.Println("header hash, location and order: ", block.Header().Hash(), block.Header().Location, types.QuaiNetworkContext)
				sl.subClients[block.Header().Location[1]-1].PCC(context.Background())
			}
		}
		fmt.Println("Set current header", block.Header().Hash())
	} else {
		sl.hc.bc.chainSideFeed.Send(ChainSideEvent{Block: block})
	}

	return nil
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

	if headerOrder < types.QuaiNetworkContext {
		// Run Appendable on every block.
		err := sl.hc.Appendable(block)
		if err != nil {
			return types.PCRCTermini{}, err
		}
	}

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
			return types.PCRCTermini{}, errors.New("there exists a Prime twist (PRTP != PRTR")
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
			return types.PCRCTermini{}, errors.New("there exists a Region twist (RTR != RTZ)")
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
		if headerOrder < params.REGION {
			PTZ, err := sl.PreviousValidCoincident(header, slice, params.PRIME, true)
			if err != nil {
				return types.PCRCTermini{}, err
			}
			PCRCTermini.PTZ = PTZ.Hash()
		}

		if headerOrder < params.ZONE {
			RTZ, err := sl.PreviousValidCoincident(header, slice, params.REGION, true)
			if err != nil {
				return types.PCRCTermini{}, err
			}
			PCRCTermini.RTZ = RTZ.Hash()
		}

		return PCRCTermini, nil
	}
	return types.PCRCTermini{}, errors.New("running in unsupported context")
}

// PreviousCanonicalCoincident searches the path for a cononical block of specified order in the specified slice
func (sl *Slice) PreviousCanonicalCoincident() error {
	header := sl.hc.CurrentHeader()
	slice := header.Location
	order := types.QuaiNetworkContext - 1
	prevTerminalHeader := header
	for {
		if prevTerminalHeader.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
			return nil
		}

		terminalHeader, err := sl.PreviousValidCoincident(prevTerminalHeader, slice, order, true)
		if err != nil {
			return err
		}

		fmt.Println("Running PVCOP for header: ", header.Hash(), header.Number, "terminal Header", terminalHeader.Hash(), terminalHeader.Number)

		if terminalHeader.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
			return nil
		}

		// If the current header is dominant coincident check the status with the dom node
		if order < types.QuaiNetworkContext {
			status := sl.domClient.GetCanonicalHashByNumber(context.Background(), terminalHeader.Number[types.QuaiNetworkContext-1])
			fmt.Println("terminal Header status", status)
			if (status != common.Hash{}) {
				if prevTerminalHeader.Hash() != header.Hash() {
					sl.hc.SetCurrentHeader(sl.hc.GetHeaderByHash(prevTerminalHeader.Parent()))
					return errors.New("subordinate terminus mismatch")
				}
				return nil
			}
		} else if order == types.QuaiNetworkContext {
			return err
		}

		prevTerminalHeader = terminalHeader
	}
}

// PreviousValidCoincident searches the path for a block of specified order in the specified slice
//     *slice - The zone location which defines the slice in which we are validating
//     *order - The order of the conincidence that is desired
//     *path - Search among ancestors of this path in the specified slice
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

// HLCR does hierarchical comparison of two difficulty tuples and returns true if second tuple is greater than the first
func (sl *Slice) HLCR(localDifficulties []*big.Int, externDifficulties []*big.Int) bool {
	if len(externDifficulties) == 0 || len(localDifficulties) == 0 {
		return false
	}
	if localDifficulties[0].Cmp(externDifficulties[0]) < 0 {
		return true
	} else if localDifficulties[0].Cmp(externDifficulties[0]) > 0 {
		return false
	}
	if localDifficulties[1].Cmp(externDifficulties[1]) < 0 {
		return true
	} else if localDifficulties[1].Cmp(externDifficulties[1]) > 0 {
		return false
	}
	if localDifficulties[2].Cmp(externDifficulties[2]) < 0 {
		return true
	} else if localDifficulties[2].Cmp(externDifficulties[2]) > 0 {
		return false
	}
	return false
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
			sl.Append(blocks[i])
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

func (sl *Slice) update() {
	futureTimer := time.NewTicker(1 * time.Second)
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

// CalcTd calculates the TD of the given header using PCRC and CalcHLCRNetDifficulty.
func (sl *Slice) CalcLocalTd(header *types.Header) ([]*big.Int, error) {
	// Iterate ancestors, stopping when a TD value is found in cache or a coincident block is found.
	// If coincident is found, ask dom client for TD at that block
	aggDiff := new(big.Int)
	cursor := header
	for {
		// First, check if this block's TD is already written locally
		cursorTd := sl.hc.GetTd(cursor.Hash(), (*cursor.Number[types.QuaiNetworkContext]).Uint64())
		td := make([]*big.Int, len(cursorTd))
		for i, diff := range cursorTd {
			td[i] = diff
		}

		if td[types.QuaiNetworkContext] != nil {
			// Add the difficulty we accumulated up till this block
			blockTd := big.NewInt(td[types.QuaiNetworkContext].Int64())
			td[types.QuaiNetworkContext] = blockTd.Add(blockTd, aggDiff)
			return td, nil
		}

		// If not written locally, check if this block coincides with a dominant chain
		order, err := sl.engine.GetDifficultyOrder(cursor)
		if err != nil {
			return nil, err
		} else if order < types.QuaiNetworkContext {
			// TODO: Ask dom to CalcTd on coincident block
			td, err = sl.domClient.CalcTd(context.Background(), header)
			if err != nil {
				return nil, err
			} else {
				blockTd := big.NewInt(td[types.QuaiNetworkContext].Int64())
				td[types.QuaiNetworkContext] = blockTd.Add(blockTd, aggDiff)
				fmt.Println("returning coincident", td)
				return td, nil
			}
		}

		// If not cached AND not coincident, aggregate the difficulty and iterate to the parent
		aggDiff = aggDiff.Add(aggDiff, cursor.Difficulty[types.QuaiNetworkContext])
		parentHash := cursor.ParentHash[types.QuaiNetworkContext]
		cursor = sl.hc.GetHeader(cursor.Parent(), (*cursor.Number[types.QuaiNetworkContext]).Uint64()-1)
		if cursor == nil {
			log.Warn("unable to find parent: %s", parentHash)
			return nil, consensus.ErrFutureBlock
		}
	}
}

func (sl *Slice) CalcTd(header *types.Header) ([]*big.Int, error) {
	td, err := sl.CalcLocalTd(header)
	if err != nil {
		return nil, err
	}
	switch types.QuaiNetworkContext {
	case params.PRIME:
		td[1].Set(td[0])
		td[2].Set(td[0])
	case params.REGION:
		td[2].Set(td[1])
	}
	return td, nil
}
