package utils

import (
	"fmt"
	"math/big"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/quai"
	"github.com/spf13/viper"
	"github.com/syndtr/goleveldb/leveldb"
	"google.golang.org/protobuf/proto"
)

const (
	// c_expansionChSize is the size of the chain head channel listening to new
	// expansion events
	c_expansionChSize = 10
)

type Constraint int

const (
	FirstPrime Constraint = iota
	FirstRegion
	FirstZone
	SecondPrime
	SecondRegion
	SecondZone
)

var (
	c_currentExpansionNumberKey = []byte("cexp")
)

type Child struct {
	hash       common.Hash
	number     []*big.Int
	location   common.Location
	entropy    *big.Int
	constraint Constraint
	order      int
}

// TODO: add all the field at the end
func (ch *Child) Empty() bool {
	return ch.hash == common.Hash{} && ch.location.Equal(common.Location{}) && ch.entropy == nil
}

type Children []Child

type HierarchicalCoordinator struct {
	db *leveldb.DB
	// APIS
	consensus quai.ConsensusAPI
	p2p       quai.NetworkingAPI

	logLevel string

	currentExpansionNumber uint8

	slicesRunning []common.Location

	newBlockCh    chan *types.WorkObject
	chainHeadSubs []event.Subscription
	chainSubs     []event.Subscription

	bestBlocks map[string]*types.WorkObject

	expansionCh  chan core.ExpansionEvent
	expansionSub event.Subscription
	wg           *sync.WaitGroup

	quitCh chan struct{}

	treeExpansionTriggerStarted bool // flag to indicate if the tree expansion trigger has started
}

// NewHierarchicalCoordinator creates a new instance of the HierarchicalCoordinator
func NewHierarchicalCoordinator(p2p quai.NetworkingAPI, logLevel string, nodeWg *sync.WaitGroup, startingExpansionNumber uint64, quitCh chan struct{}) *HierarchicalCoordinator {
	db, err := OpenBackendDB()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Error opening the backend db")
	}
	hc := &HierarchicalCoordinator{
		wg:                          nodeWg,
		db:                          db,
		p2p:                         p2p,
		logLevel:                    logLevel,
		slicesRunning:               GetRunningZones(),
		treeExpansionTriggerStarted: false,
		quitCh:                      quitCh,
		newBlockCh:                  make(chan *types.WorkObject),
		bestBlocks:                  make(map[string]*types.WorkObject),
	}

	if startingExpansionNumber > common.MaxExpansionNumber {
		log.Global.Fatal("Starting expansion number is greater than the maximum expansion number")
	}

	expansionNumber := hc.readCurrentExpansionNumber()
	if expansionNumber == 0 {
		expansionNumber = startingExpansionNumber
	}
	hc.currentExpansionNumber = uint8(expansionNumber)

	// Start the QuaiBackend and set the consensus backend
	backend, err := hc.StartQuaiBackend()
	if err != nil {
		log.Global.Fatal("Error starting the quai backend")
	}
	hc.consensus = backend

	return hc
}

func (hc *HierarchicalCoordinator) StartHierarchicalCoordinator() error {
	// get the prime backend
	primeApiBackend := *hc.consensus.GetBackend(common.Location{})
	if primeApiBackend == nil {
		log.Global.Fatal("prime backend not found starting the hierarchical coordinator")
	}

	// subscribe to the  chain head feed in prime
	hc.expansionCh = make(chan core.ExpansionEvent, c_expansionChSize)
	hc.expansionSub = primeApiBackend.SubscribeExpansionEvent(hc.expansionCh)

	hc.wg.Add(1)
	go hc.expansionEventLoop()

	hc.chainHeadSubs = []event.Subscription{}

	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	for i := 0; i < int(numRegions); i++ {
		for j := 0; j < int(numZones); j++ {
			backend := *hc.consensus.GetBackend(common.Location{byte(i), byte(j)})
			chainHeadCh := make(chan core.ChainHeadEvent)
			chainHeadSub := backend.SubscribeChainHeadEvent(chainHeadCh)
			hc.chainHeadSubs = append(hc.chainHeadSubs, chainHeadSub)

			// start an chain head event loop to handle the chainhead events
			hc.wg.Add(1)
			go hc.ChainHeadEventLoop(chainHeadCh, chainHeadSub)

			chainEventCh := make(chan core.ChainEvent)
			chainSub := backend.SubscribeChainEvent(chainEventCh)
			hc.wg.Add(1)
			hc.chainSubs = append(hc.chainSubs, chainSub)
			go hc.ChainEventLoop(chainEventCh, chainSub)
		}
	}

	hc.wg.Add(1)
	go hc.NewBlockEventLoop()

	return nil
}

// Create a new instance of the QuaiBackend consensus service
func (hc *HierarchicalCoordinator) StartQuaiBackend() (*quai.QuaiBackend, error) {
	quaiBackend, _ := quai.NewQuaiBackend()
	// Set the consensus backend and subscribe to the new topics
	hc.p2p.SetConsensusBackend(quaiBackend)
	// Set the p2p backend inside the quaiBackend
	quaiBackend.SetP2PApiBackend(hc.p2p)

	currentRegions, currentZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	// Start nodes in separate goroutines
	hc.startNode("prime.log", quaiBackend, nil, nil)
	for i := 0; i < int(currentRegions); i++ {
		nodelogsFileName := "region-" + fmt.Sprintf("%d", i) + ".log"
		hc.startNode(nodelogsFileName, quaiBackend, common.Location{byte(i)}, nil)
	}
	for i := 0; i < int(currentRegions); i++ {
		for j := 0; j < int(currentZones); j++ {
			nodelogsFileName := "zone-" + fmt.Sprintf("%d", i) + "-" + fmt.Sprintf("%d", j) + ".log"
			hc.startNode(nodelogsFileName, quaiBackend, common.Location{byte(i), byte(j)}, nil)
		}
	}

	// Set the Dom Interface for all the regions and zones
	for i := 0; i < int(currentRegions); i++ {
		primeBackend := *quaiBackend.GetBackend(common.Location{})
		regionBackend := *quaiBackend.GetBackend(common.Location{byte(i)})
		// set the Prime with the sub interfaces
		primeBackend.SetSubInterface(regionBackend, common.Location{byte(i)})
		// set the Dom Interface for each region
		regionBackend.SetDomInterface(primeBackend)
	}
	for i := 0; i < int(currentRegions); i++ {
		regionBackend := *quaiBackend.GetBackend(common.Location{byte(i)})
		for j := 0; j < int(currentZones); j++ {
			zoneBackend := *quaiBackend.GetBackend(common.Location{byte(i), byte(j)})
			// Set the Sub Interface for each of the regions
			regionBackend.SetSubInterface(zoneBackend, common.Location{byte(i), byte(j)})
			// Set the Dom Interface for each of the zones
			zoneBackend.SetDomInterface(regionBackend)
		}
	}
	return quaiBackend, nil
}

func (hc *HierarchicalCoordinator) startNode(logPath string, quaiBackend quai.ConsensusAPI, location common.Location, genesisBlock *types.WorkObject) {
	hc.wg.Add(1)
	logger := log.NewLogger(logPath, hc.logLevel, viper.GetInt(LogSizeFlag.Name))
	logger.Info("Starting Node at location", "location", location)
	stack, apiBackend := makeFullNode(hc.p2p, location, hc.slicesRunning, hc.currentExpansionNumber, genesisBlock, logger)
	quaiBackend.SetApiBackend(&apiBackend, location)

	hc.p2p.Subscribe(location, &types.WorkObjectHeaderView{})

	if quaiBackend.ProcessingState(location) && location.Context() == common.ZONE_CTX {
		// Subscribe to the new topics after setting the api backend
		hc.p2p.Subscribe(location, &types.WorkObjectShareView{})
	}

	if location.Context() == common.PRIME_CTX || location.Context() == common.REGION_CTX || quaiBackend.ProcessingState(location) {
		hc.p2p.Subscribe(location, &types.WorkObjectBlockView{})
	}

	StartNode(stack)

	go func() {
		defer hc.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Fatal("Go-Quai Panicked")
			}
		}()
		<-hc.quitCh
		logger.Info("Context cancelled, shutting down node")
		stack.Close()
		stack.Wait()
	}()
}

func (hc *HierarchicalCoordinator) Stop() {
	for _, chainHeadSub := range hc.chainHeadSubs {
		chainHeadSub.Unsubscribe()
	}
	for _, chainEventSub := range hc.chainSubs {
		chainEventSub.Unsubscribe()
	}
	hc.expansionSub.Unsubscribe()
	hc.db.Close()
	close(hc.quitCh)
	hc.wg.Wait()
}

func (hc *HierarchicalCoordinator) ConsensusBackend() quai.ConsensusAPI {
	return hc.consensus
}

func (hc *HierarchicalCoordinator) expansionEventLoop() {
	defer hc.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()

	for {
		select {
		case expansionHead := <-hc.expansionCh:
			log.Global.WithFields(log.Fields{
				"block number": expansionHead.Block.NumberU64(common.PRIME_CTX),
				"hash":         expansionHead.Block.Hash().Hex(),
			}).Info("Expansion Event received in Hierarchical Coordinator")

			// If the header has the same expansion number as the current expansion number, then it is an uncle
			if expansionHead.Block.Header().ExpansionNumber() > hc.currentExpansionNumber {
				// trigger an expansion every prime block
				hc.TriggerTreeExpansion(expansionHead.Block)
			} else {
				newChains := common.NewChainsAdded(hc.currentExpansionNumber)
				for _, chain := range newChains {
					switch chain.Context() {
					case common.REGION_CTX:
						// Add the Pending Etxs into the database so that the existing
						// region can accept the Dom blocks from the new zone
						hc.consensus.AddGenesisPendingEtxs(expansionHead.Block, chain)
					case common.ZONE_CTX:
						// Expansion has already taken place, just update the genesis block
						hc.consensus.WriteGenesisBlock(expansionHead.Block, chain)
					}
				}
			}

		case <-hc.expansionSub.Err():
			return
		}
	}
}

func (hc *HierarchicalCoordinator) TriggerTreeExpansion(block *types.WorkObject) error {
	// set the current expansion on all the backends
	currentRegions, currentZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	newRegions, newZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber + 1)

	newRegionShouldBeAdded := newRegions > currentRegions
	newZoneShouldBeAdded := newZones > currentZones

	// update the current expansion number
	err := hc.writeCurrentExpansionNumber(hc.currentExpansionNumber + 1)
	if err != nil {
		log.Global.Error("Error setting the current expansion number, err: ", err)
		return err
	}

	// If only new zones to be added, go through all the regions and add a new zone
	if !newRegionShouldBeAdded && newZoneShouldBeAdded {
		// add a new zone to all the current active regions
		for i := 0; i < int(currentRegions); i++ {
			logLocation := "zone-" + fmt.Sprintf("%d", i) + "-" + fmt.Sprintf("%d", newZones-1) + ".log"
			hc.startNode(logLocation, hc.consensus, common.Location{byte(i), byte(newZones - 1)}, block)
			// Add the new zone to the new slices list
			// Set the subInterface for the region and Set the DomInterface for the new Zones
			zoneBackend := hc.consensus.GetBackend(common.Location{byte(i), byte(newZones - 1)})
			hc.consensus.SetSubInterface(*zoneBackend, common.Location{byte(i)}, common.Location{byte(i), byte(newZones - 1)})
			regionBackend := hc.consensus.GetBackend(common.Location{byte(i)})
			hc.consensus.SetDomInterface(*regionBackend, common.Location{byte(i)})
			// Add the Pending Etxs into the database so that the existing
			// region can accept the Dom blocks from the new zone
			hc.consensus.AddGenesisPendingEtxs(block, common.Location{byte(i)})
		}

	}

	// If new regions to be added, go through all the regions and add a new region
	if newRegionShouldBeAdded {

		// add a new region
		logLocation := "region-" + fmt.Sprintf("%d", newRegions-1) + ".log"
		hc.startNode(logLocation, hc.consensus, common.Location{byte(newRegions - 1)}, block)

		regionBackend := hc.consensus.GetBackend(common.Location{byte(newRegions - 1)})
		hc.consensus.SetSubInterface(*regionBackend, common.Location{}, common.Location{byte(newRegions - 1)})

		// new region has to activate all the zones
		for i := 0; i < int(newZones); i++ {
			logLocation = "zone-" + fmt.Sprintf("%d", newRegions-1) + "-" + fmt.Sprintf("%d", i) + ".log"
			hc.startNode(logLocation, hc.consensus, common.Location{byte(newRegions - 1), byte(i)}, block)
			// Set the DomInterface for each of the new zones
			hc.consensus.SetDomInterface(*regionBackend, common.Location{byte(newRegions - 1), byte(i)})
		}
	}

	// Giving enough time for the clients to connect before generating the pending header
	time.Sleep(5 * time.Second)

	// Set the current expansion number on all the backends
	hc.consensus.SetCurrentExpansionNumber(hc.currentExpansionNumber)

	// Once the nodes are started, have to set the genesis block
	primeBackend := *hc.consensus.GetBackend(common.Location{})
	primeBackend.NewGenesisPendingHeader(nil, block.Hash(), block.Hash())

	return nil
}

// getCurrentExpansionNumber gets the current expansion number from the database
func (hc *HierarchicalCoordinator) readCurrentExpansionNumber() uint64 {
	currentExpansionNumber, _ := hc.db.Get(c_currentExpansionNumberKey, nil)
	if len(currentExpansionNumber) == 0 {
		// starting expansion number
		return 0
	}
	protoNumber := &common.ProtoNumber{}
	err := proto.Unmarshal(currentExpansionNumber, protoNumber)
	if err != nil {
		Fatalf("error unmarshalling current expansion number: %s", err)
	}
	return protoNumber.Value
}

func (hc *HierarchicalCoordinator) writeCurrentExpansionNumber(number uint8) error {
	// set the current expansion number and write it to the database
	// check if we have reached the max expansion, dont update the expansion
	// number past the max expansion number
	if number > common.MaxExpansionNumber {
		number = common.MaxExpansionNumber
	}
	hc.currentExpansionNumber = number
	protoExpansionNumber := &common.ProtoNumber{Value: uint64(hc.currentExpansionNumber)}
	protoNumber, err := proto.Marshal(protoExpansionNumber)
	if err != nil {
		Fatalf("error marshalling expansion number: %s", err)
	}
	err = hc.db.Put(c_currentExpansionNumberKey, protoNumber, nil)
	if err != nil {
		Fatalf("error setting current expansion number: %s", err)
	}
	return nil
}

func (hc *HierarchicalCoordinator) ChainHeadEventLoop(chainHead chan core.ChainHeadEvent, sub event.Subscription) {
	defer hc.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case head := <-chainHead:
			hc.newBlockCh <- head.Block
		case <-sub.Err():
			return
		}
	}
}

func (hc *HierarchicalCoordinator) ChainEventLoop(chainEvent chan core.ChainEvent, sub event.Subscription) {
	defer hc.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case head := <-chainEvent:
			bestBlock, exists := hc.bestBlocks[head.Block.Location().Name()]
			if !exists {
				hc.bestBlocks[head.Block.Location().Name()] = head.Block
			} else {
				backend := *hc.consensus.GetBackend(head.Block.Location())
				if backend.TotalLogS(bestBlock).Cmp(backend.TotalLogS(head.Block)) < 0 {
					hc.bestBlocks[head.Block.Location().Name()] = head.Block
				}
			}
		case <-sub.Err():
			return
		}
	}
}

// newBlockEventLoop
func (hc *HierarchicalCoordinator) NewBlockEventLoop() {
	defer hc.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case newBlock := <-hc.newBlockCh:
			log.Global.Error("Received a new head event", newBlock.NumberArray())
			hc.BuildPendingHeaders()
		case <-hc.quitCh:
			return
		}
	}
}

func (hc *HierarchicalCoordinator) BuildPendingHeaders() {
	// Pick the leader among all the slices

	constraintMap := make(map[common.Hash]Child)
	leader := hc.calculateLeader()
	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	log.Global.Error("Leader", leader.Hash(), leader.Location())

	hc.calculateFrontierPoints(constraintMap, leader)

	// zone-0-1 current header
	var otherZoneBackend quaiapi.Backend
	var otherZoneCurrentHeader *types.WorkObject
	if leader.Location().Equal(common.Location{0, 1}) {
		block, exists := hc.bestBlocks[common.Location{0, 0}.Name()]
		if exists {
			otherZoneCurrentHeader = block
		} else {
			otherZoneBackend = *hc.consensus.GetBackend(common.Location{0, 0})
			otherZoneCurrentHeader = otherZoneBackend.GetBlockByHash(otherZoneBackend.Config().DefaultGenesisHash)
		}
	} else {
		block, exists := hc.bestBlocks[common.Location{0, 1}.Name()]
		if exists {
			otherZoneCurrentHeader = block
		} else {
			otherZoneBackend = *hc.consensus.GetBackend(common.Location{0, 1})
			otherZoneCurrentHeader = otherZoneBackend.GetBlockByHash(otherZoneBackend.Config().DefaultGenesisHash)
		}
	}
	log.Global.Error("Other Zone Leader", otherZoneCurrentHeader.Hash(), otherZoneCurrentHeader.Location())

	backend := *hc.consensus.GetBackend(common.Location{0, 0})
	defaultGenesisHash := backend.Config().DefaultGenesisHash

	modifiedConstraintMap, newLeader := hc.calculateFrontierPoints(constraintMap, otherZoneCurrentHeader)
	// If an expected region doesnt exist in this list, that means that it hasnt
	// had a Prime block yet, so we need to use the genesis hash
	var bestPrime Child
	bestRegions := make(map[string]Child)
	bestZones := make(map[string]Child)
	for key, child := range modifiedConstraintMap {
		log.Global.WithFields(log.Fields{"hash": key, "number": child.number, "constraint": child.constraint, "location": child.location}).Error("Constraint map")
		switch child.order {
		case common.PRIME_CTX:
			if bestPrime.Empty() {
				bestPrime = child
			} else {
				if bestPrime.number[common.PRIME_CTX].Cmp(child.number[common.PRIME_CTX]) < 0 {
					bestPrime = child
				}
			}
			regionLocation := common.Location{byte(child.location.Region())}.Name()
			_, exists := bestRegions[regionLocation]
			if !exists {
				bestRegions[regionLocation] = child
			} else {
				if bestRegions[regionLocation].number[common.REGION_CTX].Cmp(child.number[common.REGION_CTX]) < 0 {
					bestRegions[regionLocation] = child
				}
			}
			zoneLocation := child.location.Name()
			_, exists = bestZones[zoneLocation]
			if !exists {
				bestZones[zoneLocation] = child
			} else {
				if bestZones[zoneLocation].number[common.ZONE_CTX].Cmp(child.number[common.ZONE_CTX]) < 0 {
					bestZones[zoneLocation] = child
				}
			}

		case common.REGION_CTX:
			regionLocation := common.Location{byte(child.location.Region())}.Name()
			_, exists := bestRegions[regionLocation]
			if !exists {
				bestRegions[regionLocation] = child
			} else {
				if bestRegions[regionLocation].number[common.REGION_CTX].Cmp(child.number[common.REGION_CTX]) < 0 {
					bestRegions[regionLocation] = child
				}
			}
			zoneLocation := child.location.Name()
			_, exists = bestZones[zoneLocation]
			if !exists {
				bestZones[zoneLocation] = child
			} else {
				if bestZones[zoneLocation].number[common.ZONE_CTX].Cmp(child.number[common.ZONE_CTX]) < 0 {
					bestZones[zoneLocation] = child
				}
			}

		case common.ZONE_CTX:
			zoneLocation := child.location.Name()
			_, exists := bestZones[zoneLocation]
			if !exists {
				bestZones[zoneLocation] = child
			} else {
				if bestZones[zoneLocation].number[common.ZONE_CTX].Cmp(child.number[common.ZONE_CTX]) < 0 {
					bestZones[zoneLocation] = child
				}
			}
		}
	}

	if bestPrime.Empty() {
		bestPrime = Child{hash: defaultGenesisHash}
	}

	for i := 0; i < int(numRegions); i++ {
		regionLocation := common.Location{byte(i)}.Name()
		_, exists := bestRegions[regionLocation]
		if !exists {
			bestRegions[regionLocation] = Child{hash: defaultGenesisHash}
		}
		for j := 0; j < int(numZones); j++ {
			zoneLocation := common.Location{byte(i), byte(j)}.Name()
			_, exists := bestZones[zoneLocation]
			if !exists {
				bestZones[zoneLocation] = Child{hash: defaultGenesisHash}
			}
			log.Global.Error("computing the pending header", bestPrime.hash, bestRegions[regionLocation].hash, bestZones[zoneLocation].hash, zoneLocation)
			hc.ComputePendingHeader(bestPrime, bestRegions[regionLocation], bestZones[zoneLocation], common.Location{byte(i), byte(j)})
		}
	}

	log.Global.Error("new leader", newLeader.Hash())

}

func (hc *HierarchicalCoordinator) calculateLeader() *types.WorkObject {
	var leader *types.WorkObject
	backend := *hc.consensus.GetBackend(common.Location{0, 0})
	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	log.Global.Error("NumRegions", numRegions, "NumZones", numZones)
	for i := 0; i < int(numRegions); i++ {
		for j := 0; j < int(numZones); j++ {
			location := common.Location{byte(i), byte(j)}
			currentBlock := hc.bestBlocks[location.Name()]
			if currentBlock == nil {
				continue
			}
			// get the currrent header from all the zones
			if leader == nil {
				leader = currentBlock
			} else {
				if backend.TotalLogS(currentBlock).Cmp(backend.TotalLogS(leader)) > 0 {
					leader = currentBlock
				}
			}
		}
	}
	return leader
}

func CopyConstraintMap(constraintMap map[common.Hash]Child) map[common.Hash]Child {
	newMap := make(map[common.Hash]Child)
	for k, v := range constraintMap {
		newMap[k] = v
	}
	return newMap
}

// Quai Trace algorithm

// If a dom block doesn’t exist it will automatically be a first prime or a first region
// also applies to the zone - done

// If the current is first then parent is always second if it doesnt exist - done

// If the parent exists and is second, we can discard the trace, we restart the trace from the zone parent - done

// If the parent exists and is first we can stop the trace regardless of the current - done

// Every time a prime block is added to the constraint list every hash in its manifest gets added to the the constraint list as second

func AddToConstraintMap(constraintMap map[common.Hash]Child, block *types.WorkObject, constraint Constraint, order int) {
	constraintMap[block.Hash()] = Child{hash: block.Hash(), number: block.NumberArray(), constraint: constraint, order: order, location: block.Location()}
}

func (hc *HierarchicalCoordinator) calculateFrontierPoints(constraintMap map[common.Hash]Child, leader *types.WorkObject) (map[common.Hash]Child, *types.WorkObject) {
	leaderLocation := leader.Location()
	leaderBackend := *hc.consensus.GetBackend(leaderLocation)

	if leaderBackend.IsGenesisHash(leader.Hash()) {
		return constraintMap, leader
	}

	// trace back from the leader and stop after finding a prime block from each region or reach genesis
	_, leaderOrder, err := leaderBackend.CalcOrder(leader)
	if err != nil {
		log.Global.Error("Cannot calculate the order for the leader")
	}

	// check if the leader exists in the constraintMap
	_, exists := constraintMap[leader.Hash()]
	if !exists {
		switch leaderOrder {
		case common.PRIME_CTX:
			AddToConstraintMap(constraintMap, leader, FirstPrime, leaderOrder)
			// get the region backend that belongs to this
			regionBackend := *hc.consensus.GetBackend(common.Location{byte(leader.Location().Region())})
			if len(leader.Manifest()) > 0 {
				for _, hash := range leader.Manifest()[:len(leader.Manifest())-1] {
					block := regionBackend.GetBlockByHash(hash)
					AddToConstraintMap(constraintMap, block, SecondRegion, common.REGION_CTX)
				}
			}
		case common.REGION_CTX:
			AddToConstraintMap(constraintMap, leader, FirstRegion, leaderOrder)
		case common.ZONE_CTX:
			AddToConstraintMap(constraintMap, leader, FirstZone, leaderOrder)
		}
	}

	// copy the starting constraint map
	startingConstraintMap := CopyConstraintMap(constraintMap)

	backend := hc.GetBackendForLocationAndOrder(leaderLocation, leaderOrder)

	context := leaderOrder
	current := leader
	numPrimeBlocks := 0

	if leaderOrder == common.PRIME_CTX {
		numPrimeBlocks++
	}
	for {
		parent := backend.GetHeaderByHash(current.ParentHash(context))

		// TODO: the genesis check has to be done correctly based on the hash
		if parent.NumberU64(context) == 0 {
			break
		}

		_, parentOrder, err := backend.CalcOrder(parent)
		if err != nil {
			log.Global.Error("error calculating the order of the parent")
		}

		if parentOrder < context {
			context = parentOrder
			backend = hc.GetBackendForLocationAndOrder(parent.Location(), parentOrder)
			switch parentOrder {
			case common.PRIME_CTX:
				numPrimeBlocks++
				child, exists := constraintMap[parent.Hash()]
				if !exists {
					AddToConstraintMap(constraintMap, parent, FirstPrime, parentOrder)
					// get the region backend that belongs to this
					regionBackend := *hc.consensus.GetBackend(common.Location{byte(parent.Location().Region())})
					if len(parent.Manifest()) > 0 {
						for _, hash := range parent.Manifest()[:len(parent.Manifest())-1] {
							block := regionBackend.GetBlockByHash(hash)
							AddToConstraintMap(constraintMap, block, SecondRegion, common.REGION_CTX)
						}
					}
				} else {
					if child.constraint == SecondPrime {
						parent := backend.GetBlockByHash(leader.ParentHash(common.ZONE_CTX))
						if parent == nil {
							break
						}
						// Remove this leader from the startingConstraintMap
						delete(startingConstraintMap, leader.Hash())
						return hc.calculateFrontierPoints(startingConstraintMap, parent)
					} else if child.constraint == FirstPrime {
						break
					}
				}
			case common.REGION_CTX:
				child, exists := constraintMap[parent.Hash()]
				if !exists {
					AddToConstraintMap(constraintMap, parent, FirstRegion, parentOrder)
				} else {
					if child.constraint == SecondRegion {
						parent := backend.GetBlockByHash(leader.ParentHash(common.ZONE_CTX))
						if parent == nil {
							break
						}
						// Remove this leader from the startingConstraintMap
						delete(startingConstraintMap, leader.Hash())
						return hc.calculateFrontierPoints(startingConstraintMap, parent)
					} else if child.constraint == FirstRegion {
						break
					}
				}
			}
		} else {
			switch parentOrder {
			case common.PRIME_CTX:
				numPrimeBlocks++
				child, exists := constraintMap[parent.Hash()]
				if !exists {
					AddToConstraintMap(constraintMap, parent, SecondPrime, parentOrder)
					// Add all the hashes in this Primes manifest except for the
					// last index (prev Prime) into the constraintMap
					// get the region backend that belongs to this
					regionBackend := *hc.consensus.GetBackend(common.Location{byte(parent.Location().Region())})
					if len(parent.Manifest()) > 0 {
						for _, hash := range parent.Manifest()[:len(parent.Manifest())-1] {
							block := regionBackend.GetBlockByHash(hash)
							AddToConstraintMap(constraintMap, block, SecondRegion, common.REGION_CTX)
						}
					}
				} else {
					if child.constraint == SecondPrime {
						parent := backend.GetBlockByHash(leader.ParentHash(common.ZONE_CTX))
						if parent == nil {
							break
						}
						// Remove this leader from the startingConstraintMap
						delete(startingConstraintMap, leader.Hash())
						return hc.calculateFrontierPoints(startingConstraintMap, parent)
					} else if child.constraint == FirstPrime {
						break
					}
				}
			case common.REGION_CTX:
				child, exists := constraintMap[parent.Hash()]
				if !exists {
					AddToConstraintMap(constraintMap, parent, SecondRegion, parentOrder)
				} else {
					if child.constraint == SecondRegion {
						parent := backend.GetBlockByHash(parent.ParentHash(common.ZONE_CTX))
						if parent == nil {
							break
						}
						// Remove this leader from the startingConstraintMap
						delete(startingConstraintMap, leader.Hash())
						return hc.calculateFrontierPoints(startingConstraintMap, parent)
					} else if child.constraint == FirstRegion {
						// we can stop the trace
						break
					}
				}
			case common.ZONE_CTX:
				// In zone, if the current is FirstZone, then the subsequent zones become secondZone
				AddToConstraintMap(constraintMap, parent, SecondZone, parentOrder)
			}
		}

		// Once we collect all the prime blocks for the region we stop
		if numPrimeBlocks == 3 {
			break
		}
		current = parent
	}
	return constraintMap, leader
}

func (hc *HierarchicalCoordinator) ComputePendingHeader(primeNode, regionNode, zoneNode Child, location common.Location) {
	primeBackend := *hc.consensus.GetBackend(common.Location{})
	regionBackend := *hc.consensus.GetBackend(common.Location{byte(location.Region())})
	zoneBackend := *hc.consensus.GetBackend(location)
	primeBlock := primeBackend.BlockOrCandidateByHash(primeNode.hash)
	if primeBlock == nil {
		log.Global.Error("prime block is nil", primeBackend.Config().Location)
		panic("prime block is nil")
	}
	primePendingHeader, err := primeBackend.GeneratePendingHeader(primeBlock, false)
	if err != nil {
		log.Global.Error("error generating prime pending header")
	}
	regionBlock := regionBackend.BlockOrCandidateByHash(regionNode.hash)
	if regionBlock == nil {
		log.Global.Error("region block is nil", regionBackend.Config().Location)
		panic("region block is nil")
	}
	regionPendingHeader, err := regionBackend.GeneratePendingHeader(regionBlock, false)
	if err != nil {
		log.Global.Error("error generating region pending header")
	}
	zoneBlock := zoneBackend.GetBlockByHash(zoneNode.hash)
	if zoneBlock == nil {
		log.Global.Error("zone block is nil", zoneBackend.Config().Location)
		panic("zone block is nil")
	}
	zonePendingHeader, err := zoneBackend.GeneratePendingHeader(zoneBlock, false)
	if err != nil {
		log.Global.Error("error generating zone pending header")
	}
	zoneBackend.MakeFullPendingHeader(primePendingHeader, regionPendingHeader, zonePendingHeader)
}

func (hc *HierarchicalCoordinator) GetBackendForLocationAndOrder(location common.Location, order int) quaiapi.Backend {
	switch order {
	case common.PRIME_CTX:
		return *hc.consensus.GetBackend(common.Location{})
	case common.REGION_CTX:
		return *hc.consensus.GetBackend(common.Location{byte(location.Region())})
	case common.ZONE_CTX:
		return *hc.consensus.GetBackend(common.Location{byte(location.Region()), byte(location.Zone())})
	}
	return nil
}
