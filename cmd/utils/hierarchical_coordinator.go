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

var (
	c_currentExpansionNumberKey = []byte("cexp")
)

type Child struct {
	hash     common.Hash
	location common.Location
	entropy  *big.Int
}

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

	childrenStore map[common.Hash][common.HierarchyDepth]Children

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
		childrenStore:               make(map[common.Hash][common.HierarchyDepth]Children),
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
			hc.UpdateChildrenStore(head.Block)
		case <-sub.Err():
			return
		}
	}
}

func (hc *HierarchicalCoordinator) UpdateChildrenStore(block *types.WorkObject) {
	backend := *hc.consensus.GetBackend(block.Location())
	_, blockOrder, err := backend.CalcOrder(block)
	if err != nil {
		log.Global.Error("error calculating block order")
		return
	}
	switch blockOrder {
	case common.PRIME_CTX:
		childrenInfo, exists := hc.childrenStore[block.ParentHash(common.ZONE_CTX)]
		if !exists {
			childrenInfo[common.PRIME_CTX] = Children{{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)}}
			childrenInfo[common.REGION_CTX] = Children{{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)}}
			childrenInfo[common.ZONE_CTX] = Children{{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)}}
			hc.childrenStore[block.ParentHash(common.ZONE_CTX)] = childrenInfo
		} else {
			copyChildrenInfo := childrenInfo
			// modify the region and zone entry
			copyChildrenInfo[common.PRIME_CTX] = append(copyChildrenInfo[common.PRIME_CTX], Child{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)})
			copyChildrenInfo[common.REGION_CTX] = append(copyChildrenInfo[common.REGION_CTX], Child{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)})
			copyChildrenInfo[common.ZONE_CTX] = append(copyChildrenInfo[common.ZONE_CTX], Child{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)})
			hc.childrenStore[block.ParentHash(common.ZONE_CTX)] = copyChildrenInfo
		}
	case common.REGION_CTX:
		childrenInfo, exists := hc.childrenStore[block.ParentHash(common.ZONE_CTX)]
		if !exists {
			childrenInfo[common.REGION_CTX] = Children{{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)}}
			childrenInfo[common.ZONE_CTX] = Children{{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)}}
			hc.childrenStore[block.ParentHash(common.ZONE_CTX)] = childrenInfo
		} else {
			copyChildrenInfo := childrenInfo
			// modify the region and zone entry
			copyChildrenInfo[common.REGION_CTX] = append(copyChildrenInfo[common.REGION_CTX], Child{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)})
			copyChildrenInfo[common.ZONE_CTX] = append(copyChildrenInfo[common.ZONE_CTX], Child{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)})
			hc.childrenStore[block.ParentHash(common.ZONE_CTX)] = copyChildrenInfo
		}
	case common.ZONE_CTX:
		childrenInfo, exists := hc.childrenStore[block.ParentHash(common.ZONE_CTX)]
		if !exists {
			childrenInfo[common.ZONE_CTX] = Children{{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)}}
			hc.childrenStore[block.ParentHash(common.ZONE_CTX)] = childrenInfo
		} else {
			copyChildrenInfo := childrenInfo
			// modify the zone entry
			copyChildrenInfo[common.ZONE_CTX] = append(copyChildrenInfo[common.ZONE_CTX], Child{hash: block.Hash(), location: block.Location(), entropy: backend.TotalLogS(block)})
			hc.childrenStore[block.ParentHash(common.ZONE_CTX)] = copyChildrenInfo
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
			hc.BuildPendingHeaders(newBlock)
		case <-hc.quitCh:
			return
		}
	}
}

func (hc *HierarchicalCoordinator) BuildPendingHeaders(block *types.WorkObject) {
	// Pick the leader among all the slices

	var leader *types.WorkObject
	var currentLeaderBackend quaiapi.Backend
	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	log.Global.Error("NumRegions", numRegions, "NumZones", numZones)
	for i := 0; i < int(numRegions); i++ {
		for j := 0; j < int(numZones); j++ {
			location := common.Location{byte(i), byte(j)}
			backend := *hc.consensus.GetBackend(location)
			currentBlock := backend.CurrentBlock()
			// get the currrent header from all the zones
			if leader == nil {
				leader = currentBlock
				currentLeaderBackend = backend
			} else {
				if backend.Engine().TotalLogS(backend, currentBlock).Cmp(backend.Engine().TotalLogS(currentLeaderBackend, leader)) > 0 {
					leader = currentBlock
					currentLeaderBackend = backend
				}
			}
		}
	}

	leaderLocation := leader.Location()

	log.Global.Error("Leader", leader.Hash(), leaderLocation)
	leaderBackend := *hc.consensus.GetBackend(leaderLocation)

	// trace back from the leader and stop after finding a prime block from each region or reach genesis
	_, leaderOrder, err := leaderBackend.CalcOrder(leader)
	if err != nil {
		log.Global.Error("Cannot calculate the order for the leader")
	}

	frontierPoints := make(map[string]*types.WorkObject) // we need prime blocks for each region

	regionPrime := make(map[int]bool)

	switch leaderOrder {
	case common.PRIME_CTX:
		frontierPoints[common.Location{byte(leaderLocation.Region())}.Name()] = leader
		frontierPoints["leaderRegion-"+common.Location{byte(leaderLocation.Region())}.Name()] = leader
		frontierPoints[leaderLocation.Name()] = leader
		regionPrime[leader.Location().Region()] = true
	case common.REGION_CTX:
		frontierPoints["leaderRegion-"+common.Location{byte(leaderLocation.Region())}.Name()] = leader
		frontierPoints[leaderLocation.Name()] = leader
	case common.ZONE_CTX:
		frontierPoints[leaderLocation.Name()] = leader
	}

	backend := hc.GetBackendForLocationAndOrder(leaderLocation, leaderOrder)

	context := leaderOrder
	current := leader
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
		}

		if parentOrder == common.PRIME_CTX {
			regionPrime[parent.Location().Region()] = true
			_, exists := frontierPoints[common.Location{byte(parent.Location().Region())}.Name()]
			if !exists {
				// this makes sure that only the latest prime block for each
				// region is stored in the map
				frontierPoints[common.Location{byte(parent.Location().Region())}.Name()] = parent
			}
			if parent.Location().Region() == leader.Location().Region() {
				_, exists = frontierPoints["leaderRegion-"+common.Location{byte(parent.Location().Region())}.Name()]
				if !exists {
					frontierPoints["leaderRegion-"+common.Location{byte(parent.Location().Region())}.Name()] = parent
				}
			}
		} else if parentOrder == common.REGION_CTX {
			_, exists := frontierPoints["leaderRegion-"+common.Location{byte(parent.Location().Region())}.Name()]
			if !exists {
				frontierPoints["leaderRegion-"+common.Location{byte(parent.Location().Region())}.Name()] = parent
			}
		}

		// Once we collect all the prime blocks for the region we stop
		if len(regionPrime) == int(numRegions) {
			break
		}
		current = parent
	}

	// If an expected region doesnt exist in this list, that means that it hasnt
	// had a Prime block yet, so we need to use the genesis hash
	for key, points := range frontierPoints {
		log.Global.WithFields(log.Fields{"key": key, "hash": points.Hash(), "number": points.NumberArray(), "location": points.Location()}).Error("frontier points")
	}

	// get zone-0-0 leader region
	bestChildInZone := hc.findBestChild(Child{hash: leaderBackend.Config().DefaultGenesisHash, location: common.Location{}, entropy: big.NewInt(0)}, common.ZONE_CTX)
	log.Global.Error("Best child", bestChildInZone.hash.String())

	var bestPrimeHash, bestRegionHash common.Hash
	block, exists := frontierPoints[common.Location{byte(leaderLocation.Region())}.Name()]
	if !exists {
		bestPrimeHash = leaderBackend.Config().DefaultGenesisHash
	} else {
		bestPrimeHash = block.Hash()
	}

	block, exists = frontierPoints["leaderRegion-"+common.Location{byte(leaderLocation.Region())}.Name()]
	if !exists {
		bestRegionHash = leaderBackend.Config().DefaultGenesisHash
	} else {
		bestRegionHash = block.Hash()
	}

	// generatePrimePendingHeader
	primeBackend := *hc.consensus.GetBackend(common.Location{})
	primePendingHeader, err := primeBackend.GeneratePendingHeader(primeBackend.GetBlockByHash(bestPrimeHash), false)
	if err != nil {
		log.Global.Error("error generating prime pending header")
	}
	region0Backend := *hc.consensus.GetBackend(common.Location{byte(0)})
	regionPendingHeader, err := region0Backend.GeneratePendingHeader(region0Backend.GetBlockByHash(bestRegionHash), false)
	if err != nil {
		log.Global.Error("error generating region pending header")
	}
	bestChildBlock := leaderBackend.GetBlockByHash(bestChildInZone.hash)
	zonePendingHeader, err := leaderBackend.GeneratePendingHeader(bestChildBlock, false)
	if err != nil {
		log.Global.Error("error generating zone pending header")
	}

	log.Global.Error("PendingHeaders", primePendingHeader.NumberU64(common.PRIME_CTX), regionPendingHeader.NumberU64(common.REGION_CTX), zonePendingHeader.NumberU64(common.ZONE_CTX))

	fullPh := leaderBackend.MakeFullPendingHeader(primePendingHeader, regionPendingHeader, zonePendingHeader)
	log.Global.Error("Full ph", fullPh.NumberArray())
}

func (hc *HierarchicalCoordinator) findBestChild(node Child, ctx int) Child {
	// Fetch children from the store
	children, exists := hc.childrenStore[node.hash]
	if !exists {
		return node
	}

	var bestChild Child
	var bestEntropy = node.entropy

	for _, child := range children[ctx] {
		childBest := hc.findBestChild(child, ctx)

		if childBest.entropy.Cmp(bestEntropy) > 0 {
			bestChild = childBest
			bestEntropy = childBest.entropy
		}
	}

	if bestChild.Empty() {
		return node
	}
	return bestChild
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
