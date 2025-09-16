package utils

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/quai"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/spf13/viper"
	"github.com/syndtr/goleveldb/leveldb"
	"google.golang.org/protobuf/proto"
)

const (
	// c_expansionChSize is the size of the chain head channel listening to new
	// expansion events
	c_expansionChSize            = 10
	c_recentBlockCacheSize       = 1000
	c_ancestorCheckDist          = 10000
	c_chainEventChSize           = 1000
	c_buildPendingHeadersTimeout = 5 * time.Second
	c_pendingHeaderSize          = 2000
	c_maxHeaderWorkers           = 1
)

var (
	c_currentExpansionNumberKey = []byte("cexp")
)

type Node struct {
	hash     common.Hash
	number   []*big.Int
	location common.Location
	entropy  *big.Int
}

type NodeSet struct {
	nodes map[string]Node
}

func (ch *Node) Empty() bool {
	return ch.hash == common.Hash{} && ch.location.Equal(common.Location{}) && ch.entropy == nil
}

type PendingHeaders struct {
	collection *lru.Cache[string, NodeSet] // Use string to store the big.Int value as a string key
	order      []*big.Int                  // Maintain the order of entropies
}

type HierarchicalCoordinator struct {
	db *leveldb.DB
	// APIS
	consensus quai.ConsensusAPI
	p2p       quai.NetworkingAPI

	logLevel string

	currentExpansionNumber uint8

	slicesRunning []common.Location

	chainSubs []event.Subscription

	recentBlocks  map[string]*lru.Cache[common.Hash, Node]
	recentBlockMu sync.RWMutex

	expansionCh  chan core.ExpansionEvent
	expansionSub event.Subscription
	wg           *sync.WaitGroup

	quitCh chan struct{}

	treeExpansionTriggerStarted bool // flag to indicate if the tree expansion trigger has started

	pendingHeaders *PendingHeaders

	bestEntropy *big.Int

	oneMu                      sync.Mutex
	generateHeaderWorkersCount int

	pendingHeaderBackupCh chan struct{}
}

func NewPendingHeaders() *PendingHeaders {
	pendingHeaders := &PendingHeaders{
		order: []*big.Int{},
	}
	pendingHeaders.collection, _ = lru.NewWithEvict[string, NodeSet](c_pendingHeaderSize, func(key string, value NodeSet) {
		// On eviction, remove the corresponding value from the order slice
		removeFromSlice(key, pendingHeaders)
	})
	return pendingHeaders
}

func (hc *HierarchicalCoordinator) InitPendingHeaders() {
	nodeSet := NodeSet{
		nodes: make(map[string]Node),
	}

	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	//Initialize for prime
	backend := hc.GetBackend(common.Location{})
	genesisBlock := backend.GetBlockByHash(backend.Config().DefaultGenesisHash)
	entropy := backend.TotalLogEntropy(genesisBlock)
	newNode := Node{
		hash:     genesisBlock.Hash(),
		number:   genesisBlock.NumberArray(),
		location: common.Location{},
		entropy:  entropy,
	}
	nodeSet.nodes[common.Location{}.Name()] = newNode

	for i := 0; i < int(numRegions); i++ {
		backend := hc.GetBackend(common.Location{byte(i)})
		entropy := backend.TotalLogEntropy(genesisBlock)
		newNode.location = common.Location{byte(i)}
		newNode.entropy = entropy
		nodeSet.nodes[common.Location{byte(i)}.Name()] = newNode
		for j := 0; j < int(numZones); j++ {
			backend := hc.GetBackend(common.Location{byte(i), byte(j)})
			entropy := backend.TotalLogEntropy(genesisBlock)
			newNode.location = common.Location{byte(i), byte(j)}
			newNode.entropy = entropy
			nodeSet.nodes[common.Location{byte(i), byte(j)}.Name()] = newNode
		}
	}
	hc.Add(new(big.Int).SetUint64(0), nodeSet, hc.pendingHeaders)
}

func (hc *HierarchicalCoordinator) Add(entropy *big.Int, node NodeSet, newPendingHeaders *PendingHeaders) {
	entropyStr := entropy.String()
	if _, exists := newPendingHeaders.collection.Peek(entropyStr); !exists {
		newPendingHeaders.order = append(newPendingHeaders.order, new(big.Int).Set(entropy)) // Store a copy of the big.Int
		newPendingHeaders.collection.Add(entropyStr, node)
	}

	if hc.bestEntropy.Cmp(entropy) < 0 {
		hc.bestEntropy = new(big.Int).Set(entropy)
	}

}

func printNodeSet(nodeSet NodeSet) {
	for nodeName, n := range nodeSet.nodes {
		log.Global.WithFields(log.Fields{
			"hash":     n.hash,
			"number":   n.number,
			"location": n.location,
			"entropy":  common.BigBitsToBits(n.entropy),
			"node":     nodeName,
		}).Info("Node in the node set")
	}
}

func (hc *HierarchicalCoordinator) Get(entropy *big.Int) (NodeSet, bool) {
	entropyStr := entropy.String()
	node, exists := hc.pendingHeaders.collection.Peek(entropyStr)
	return node, exists
}

func removeFromSlice(keyToRemove string, pendingHeaders *PendingHeaders) {
	// Iterate from the beginning to the end of the slice
	for i := 0; i < len(pendingHeaders.order); i++ {
		val := pendingHeaders.order[i]
		if val.String() == keyToRemove {
			// Remove the element by slicing around it
			pendingHeaders.order = append(pendingHeaders.order[:i], pendingHeaders.order[i+1:]...)
		}
	}
}

func (ns *NodeSet) Extendable(wo *types.WorkObject, order int) bool {
	switch order {
	case common.PRIME_CTX:
		if wo.ParentHash(common.PRIME_CTX) == ns.nodes[common.Location{}.Name()].hash &&
			wo.ParentHash(common.REGION_CTX) == ns.nodes[common.Location{byte(wo.Location().Region())}.Name()].hash &&
			wo.ParentHash(common.ZONE_CTX) == ns.nodes[wo.Location().Name()].hash {
			return true
		}
	case common.REGION_CTX:
		if wo.ParentHash(common.REGION_CTX) == ns.nodes[common.Location{byte(wo.Location().Region())}.Name()].hash &&
			wo.ParentHash(common.ZONE_CTX) == ns.nodes[wo.Location().Name()].hash {
			return true
		}
	case common.ZONE_CTX:
		nodeHash := ns.nodes[wo.Location().Name()].hash
		parentHash := wo.ParentHash(common.ZONE_CTX)
		if parentHash == nodeHash {
			return true
		}
	}

	return false
}

func (ns *NodeSet) Entropy(numRegions int, numZones int) *big.Int {
	entropy := new(big.Int)

	entropy.Add(entropy, ns.nodes[common.Location{}.Name()].entropy)
	for i := 0; i < numRegions; i++ {
		entropy.Add(entropy, ns.nodes[common.Location{byte(i)}.Name()].entropy)
		for j := 0; j < numZones; j++ {
			entropy.Add(entropy, ns.nodes[common.Location{byte(i), byte(j)}.Name()].entropy)
		}
	}

	return entropy
}

func (ns *NodeSet) Update(wo *types.WorkObject, entropy *big.Int, order int) {
	newNode := Node{
		hash:     wo.Hash(),
		number:   wo.NumberArray(),
		location: common.Location{},
		entropy:  entropy,
	}
	switch order {
	case common.PRIME_CTX:
		ns.nodes[common.Location{}.Name()] = newNode
		newNode.location = common.Location{byte(wo.Location().Region())}
		ns.nodes[common.Location{byte(wo.Location().Region())}.Name()] = newNode
		newNode.location = wo.Location()
		ns.nodes[wo.Location().Name()] = newNode
	case common.REGION_CTX:
		newNode.location = common.Location{byte(wo.Location().Region())}
		ns.nodes[common.Location{byte(wo.Location().Region())}.Name()] = newNode
		newNode.location = wo.Location()
		ns.nodes[wo.Location().Name()] = newNode
	case common.ZONE_CTX:
		newNode.location = wo.Location()
		ns.nodes[wo.Location().Name()] = newNode
	}
}

func (ns *NodeSet) Copy() NodeSet {
	newNodeSet := NodeSet{
		nodes: make(map[string]Node),
	}
	for k, v := range ns.nodes {
		newNodeSet.nodes[k] = v
	}
	return newNodeSet
}

// NewHierarchicalCoordinator creates a new instance of the HierarchicalCoordinator
func NewHierarchicalCoordinator(p2p quai.NetworkingAPI, logLevel string, nodeWg *sync.WaitGroup, startingExpansionNumber uint64) *HierarchicalCoordinator {
	db, err := OpenBackendDB()
	if err != nil {
		log.Global.WithField("err", err).Fatal("Error opening the backend db")
	}
	if viper.GetBool(ReIndex.Name) {
		ReIndexChainIndexer()
	}
	if viper.GetBool(ValidateIndexer.Name) {
		ValidateChainIndexer()
	}
	hc := &HierarchicalCoordinator{
		wg:                          nodeWg,
		db:                          db,
		p2p:                         p2p,
		logLevel:                    logLevel,
		slicesRunning:               GetRunningZones(),
		treeExpansionTriggerStarted: false,
		quitCh:                      make(chan struct{}),
		recentBlocks:                make(map[string]*lru.Cache[common.Hash, Node]),
		bestEntropy:                 new(big.Int).Set(common.Big0),
		oneMu:                       sync.Mutex{},
		generateHeaderWorkersCount:  0,
		pendingHeaderBackupCh:       make(chan struct{}),
	}
	hc.pendingHeaders = NewPendingHeaders()

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
		log.Global.WithField("err", err).Fatal("Error starting the quai backend ")
	}
	hc.consensus = backend

	hc.InitPendingHeaders()

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

	hc.wg.Add(1)
	go hc.MapConstructProc()

	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)

	backend := *hc.consensus.GetBackend(common.Location{})
	chainEventCh := make(chan core.ChainEvent, c_chainEventChSize)
	chainSub := backend.SubscribeChainEvent(chainEventCh)
	hc.wg.Add(1)
	hc.chainSubs = append(hc.chainSubs, chainSub)
	go hc.ChainEventLoop(chainEventCh, chainSub)

	for i := 0; i < int(numRegions); i++ {
		backend := *hc.consensus.GetBackend(common.Location{byte(i)})
		chainEventCh := make(chan core.ChainEvent, c_chainEventChSize)
		chainSub := backend.SubscribeChainEvent(chainEventCh)
		hc.wg.Add(1)
		hc.chainSubs = append(hc.chainSubs, chainSub)
		go hc.ChainEventLoop(chainEventCh, chainSub)

		for j := 0; j < int(numZones); j++ {
			backend := *hc.consensus.GetBackend(common.Location{byte(i), byte(j)})
			chainEventCh := make(chan core.ChainEvent, c_chainEventChSize)
			chainSub := backend.SubscribeChainEvent(chainEventCh)
			hc.wg.Add(1)
			hc.chainSubs = append(hc.chainSubs, chainSub)
			go hc.ChainEventLoop(chainEventCh, chainSub)
		}
	}
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

	// Nodes need to subscribe to the aux template on the upgrade to kawpow
	if location.Context() == common.ZONE_CTX {
		hc.p2p.Subscribe(location, &types.AuxTemplate{})
	}

	StartNode(stack)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Fatal("Go-Quai Panicked")
			}
		}()
		defer hc.wg.Done()
		<-hc.quitCh
		logger.Info("Context cancelled, shutting down node")
		stack.Close()
		stack.Wait()
	}()
}

func (hc *HierarchicalCoordinator) Stop() {
	close(hc.quitCh)
	for _, chainEventSub := range hc.chainSubs {
		chainEventSub.Unsubscribe()
	}
	hc.expansionSub.Unsubscribe()
	hc.db.Close()
	hc.wg.Wait()
}

func (hc *HierarchicalCoordinator) ConsensusBackend() quai.ConsensusAPI {
	return hc.consensus
}

func (hc *HierarchicalCoordinator) expansionEventLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	defer hc.wg.Done()

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
		case <-hc.quitCh:
			return
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
		log.Global.WithField("err", err).Error("Error setting the current expansion number")
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

///////// QUAI Mining Pick Logic

func (hc *HierarchicalCoordinator) ChainEventLoop(chainEvent chan core.ChainEvent, sub event.Subscription) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	defer hc.wg.Done()

	lastUpdateTime := time.Now()
	for {
		select {
		case head := <-chainEvent:
			// If this is the first block we have after a restart, then we can
			// add this block into the node set directly
			// Since on startup we initialize the pending headers cache with the
			// genesis block, we can check and see if we are in that state
			// We can do that by checking the length of the pendding headers order
			// cache length is 1
			if len(hc.pendingHeaders.order) == 1 {
				// create a nodeset on this block
				nodeSet := NodeSet{
					nodes: make(map[string]Node),
				}

				//Initialize for prime
				backend := hc.GetBackend(common.Location{})
				entropy := backend.TotalLogEntropy(head.Block)
				newNode := Node{
					hash:     head.Block.ParentHash(common.PRIME_CTX),
					number:   head.Block.NumberArray(),
					location: common.Location{},
					entropy:  entropy,
				}
				nodeSet.nodes[common.Location{}.Name()] = newNode

				regionLocation := common.Location{byte(head.Block.Location().Region())}
				backend = hc.GetBackend(regionLocation)
				newNode.hash = head.Block.ParentHash(common.REGION_CTX)
				newNode.location = regionLocation
				newNode.entropy = entropy
				nodeSet.nodes[regionLocation.Name()] = newNode

				zoneLocation := head.Block.Location()
				backend = hc.GetBackend(zoneLocation)
				newNode.hash = head.Block.ParentHash(common.ZONE_CTX)
				newNode.location = zoneLocation
				newNode.entropy = entropy
				nodeSet.nodes[zoneLocation.Name()] = newNode
				hc.Add(entropy, nodeSet, hc.pendingHeaders)
			}

			go hc.ReapplicationLoop(head)
			go hc.ComputeMapPending(head)

			timeSinceLastUpdate := time.Since(lastUpdateTime)
			if timeSinceLastUpdate > 5*time.Second {
				hc.pendingHeaderBackupCh <- struct{}{}
				lastUpdateTime = time.Now()
			}
		case <-hc.quitCh:
			return
		case <-sub.Err():
			return
		}
	}
}

func (hc *HierarchicalCoordinator) MapConstructProc() {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	defer hc.wg.Done()

	for {
		select {
		case <-hc.pendingHeaderBackupCh:
			log.Global.Info("Running the backup calculation on recent blocks")
			hc.PendingHeadersMap()
		case <-hc.quitCh:
			return
		}
	}
}

func (hc *HierarchicalCoordinator) ComputeMapPending(head core.ChainEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	backend := hc.GetBackend(head.Block.Location())
	entropy := backend.TotalLogEntropy(head.Block)
	node := Node{
		hash:     head.Block.Hash(),
		number:   head.Block.NumberArray(),
		entropy:  entropy,
		location: head.Block.Location(),
	}
	hc.recentBlockMu.Lock()
	defer hc.recentBlockMu.Unlock()
	locationCache, exists := hc.recentBlocks[head.Block.Location().Name()]
	if !exists {
		// create a new lru and add this block
		lru, _ := lru.New[common.Hash, Node](c_recentBlockCacheSize)
		lru.Add(head.Block.Hash(), node)
		hc.recentBlocks[head.Block.Location().Name()] = lru
		log.Global.WithFields(log.Fields{"Hash": head.Block.Hash(), "Number": head.Block.NumberArray()}).Debug("Received a chain event and calling build pending headers")
	} else {
		bestBlockHash := locationCache.Keys()[len(locationCache.Keys())-1]
		_, exists := locationCache.Peek(bestBlockHash)
		if exists {
			oldestBlockHash := locationCache.Keys()[0]
			oldestBlock, exists := locationCache.Peek(oldestBlockHash)
			if exists && oldestBlock.entropy.Cmp(node.entropy) < 0 {
				locationCache.Add(head.Block.Hash(), node)
				hc.recentBlocks[head.Block.Location().Name()] = locationCache
				log.Global.WithFields(log.Fields{"Hash": head.Block.Hash(), "Number": head.Block.NumberArray()}).Debug("Received a chain event and calling build pending headers")
			}
		}
	}
}

func (hc *HierarchicalCoordinator) PendingHeadersMap() {

	hc.recentBlockMu.Lock()
	defer hc.recentBlockMu.Unlock()
	var badHashes map[common.Hash]bool
	badHashes = make(map[common.Hash]bool)
	count := 0
	var leaders []Node
search:
	if count > 0 {
		circularShift(leaders)
		badHashes = make(map[common.Hash]bool)
	}

	if count > 2 {
		log.Global.Error("Too many iterations in the build pending headers, skipping generate")
		return
	}
	// Pick the leader among all the slices
	backend := *hc.consensus.GetBackend(common.Location{0, 0})
	defaultGenesisHash := backend.Config().DefaultGenesisHash

	constraintMap := make(map[string]common.Hash)
	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	for i := 0; i < int(numRegions); i++ {
		for j := 0; j < int(numZones); j++ {
			if _, exists := hc.recentBlocks[common.Location{byte(i), byte(j)}.Name()]; !exists {
				backend := hc.GetBackend(common.Location{byte(i), byte(j)})
				genesisBlock := backend.GetBlockByHash(defaultGenesisHash)
				lru, _ := lru.New[common.Hash, Node](c_recentBlockCacheSize)
				lru.Add(genesisBlock.Hash(), Node{hash: genesisBlock.Hash(), number: genesisBlock.NumberArray(), location: common.Location{byte(i), byte(j)}, entropy: big.NewInt(0)})
				hc.recentBlocks[common.Location{byte(i), byte(j)}.Name()] = lru
			}
		}
	}
	leaders = hc.CalculateLeaders(badHashes)
	// Go through all the zones to update the constraint map
	modifiedConstraintMap := constraintMap
	first := true
	for _, leader := range leaders {
		log.Global.WithFields(log.Fields{"Header": leader.hash, "Number": leader.number, "Location": leader.location, "Entropy": leader.entropy}).Debug("Starting leader")
		var err error
		location := leader.location
		backend := hc.GetBackend(location)
		otherNodes := hc.GetNodeListForLocation(location, badHashes)
		for _, node := range otherNodes {
			leaderBlock := backend.GetBlockByHash(node.hash)
			modifiedConstraintMap, err = hc.calculateFrontierPoints(modifiedConstraintMap, leaderBlock, first)
			first = false
			if err != nil {
				log.Global.WithFields(log.Fields{"hash": leaderBlock.Hash().String(), "err": err}).Error("error tracing back from block")
			} else {
				break
			}
		}
	}

	_, exists := modifiedConstraintMap[common.Location{}.Name()]
	if !exists {
		modifiedConstraintMap[common.Location{}.Name()] = defaultGenesisHash
	}

	// Check if regions have twist
	primeTermini := hc.GetBackend(common.Location{}).GetTerminiByHash(modifiedConstraintMap[common.Location{}.Name()])

	for i := 0; i < int(numRegions); i++ {
		regionLocation := common.Location{byte(i)}.Name()
		_, exists := modifiedConstraintMap[regionLocation]
		if !exists {
			modifiedConstraintMap[regionLocation] = defaultGenesisHash
		}

		if !hc.pcrc(modifiedConstraintMap[regionLocation], primeTermini.SubTerminiAtIndex(i), common.Location{byte(i)}, common.REGION_CTX) {
			badHashes[modifiedConstraintMap[regionLocation]] = true
			log.Global.WithFields(log.Fields{"hash": modifiedConstraintMap[regionLocation], "location": regionLocation}).Debug("Best Region doesnt satisfy pcrc")
			count++
			goto search
		}
		regionTermini := hc.GetBackend(common.Location{byte(i)}).GetTerminiByHash(modifiedConstraintMap[regionLocation])
		for j := 0; j < int(numZones); j++ {
			zoneLocation := common.Location{byte(i), byte(j)}.Name()
			_, exists := modifiedConstraintMap[zoneLocation]
			if !exists {
				modifiedConstraintMap[zoneLocation] = defaultGenesisHash
			}
			if !hc.pcrc(modifiedConstraintMap[zoneLocation], regionTermini.SubTerminiAtIndex(j), common.Location{byte(i), byte(j)}, common.ZONE_CTX) {
				badHashes[modifiedConstraintMap[zoneLocation]] = true
				log.Global.WithFields(log.Fields{"hash": modifiedConstraintMap[zoneLocation], "location": zoneLocation}).Debug("Best Zone doesnt satisfy pcrc")
				count++
				goto search
			}
		}
	}
	PrintConstraintMap(modifiedConstraintMap)
	// Build a node set
	nodeSet := NodeSet{nodes: make(map[string]Node)}
	primeNode, err := hc.NodeFromHash(modifiedConstraintMap[common.Location{}.Name()], common.Location{})
	if err != nil {
		log.Global.WithField("err", err).Error("error reading node from hash in prime")
		return
	}
	nodeSet.nodes[common.Location{}.Name()] = primeNode
	for i := 0; i < int(numRegions); i++ {
		regionNode, err := hc.NodeFromHash(modifiedConstraintMap[common.Location{byte(i)}.Name()], common.Location{byte(i)})
		if err != nil {
			log.Global.WithFields(log.Fields{"err": err, "region": i}).Error("error reading node from hash in region ", i, "err ", err)
			return
		}
		nodeSet.nodes[common.Location{byte(i)}.Name()] = regionNode
		for j := 0; j < int(numZones); j++ {
			zoneNode, err := hc.NodeFromHash(modifiedConstraintMap[common.Location{byte(i), byte(j)}.Name()], common.Location{byte(i), byte(j)})
			if err != nil {
				log.Global.WithFields(log.Fields{"err": err, "location": common.Location{byte(i), byte(j)}}).Error("error reading node from hash in zone")
				return
			}
			nodeSet.nodes[common.Location{byte(i), byte(j)}.Name()] = zoneNode
		}
	}
	entropy := nodeSet.Entropy(int(numRegions), int(numZones))
	log.Global.WithFields(log.Fields{"entropy": common.BigBitsToBits(entropy), "best entropy": common.BigBitsToBits(hc.bestEntropy)}).Info("Map Based New Set Entropy")
	printNodeSet(nodeSet)
	hc.oneMu.Lock()
	hc.Add(entropy, nodeSet, hc.pendingHeaders)
	hc.oneMu.Unlock()
}

func (hc *HierarchicalCoordinator) NodeFromHash(hash common.Hash, location common.Location) (Node, error) {
	backend := hc.GetBackend(location)
	header := backend.GetHeaderByHash(hash)
	if header == nil {
		return Node{}, errors.New("header not found")
	}
	return Node{
		hash:     header.Hash(),
		number:   header.NumberArray(),
		location: location,
		entropy:  backend.TotalLogEntropy(header),
	}, nil
}

func (hc *HierarchicalCoordinator) ReapplicationLoop(head core.ChainEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()

	sleepTime := 1

	for {
		select {
		case <-hc.quitCh:
			return
		default:
			hc.BuildPendingHeaders(head.Block, head.Order, head.Entropy)
			time.Sleep(time.Duration(sleepTime) * time.Second)
			sleepTime = sleepTime * 2
			if sleepTime > 65 {
				return
			}
		}
	}
}

func (hc *HierarchicalCoordinator) CalculateLeaders(badHashes map[common.Hash]bool) []Node {
	nodeList := []Node{}
	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	for i := 0; i < int(numRegions); i++ {
		for j := 0; j < int(numZones); j++ {
			cache, exists := hc.recentBlocks[common.Location{byte(i), byte(j)}.Name()]
			if exists {
				var bestNode Node
				keys := cache.Keys()
				for _, key := range keys {
					if _, exists := badHashes[key]; exists {
						continue
					}
					node, _ := cache.Peek(key)
					if bestNode.Empty() {
						bestNode = node
					} else {
						if bestNode.entropy.Cmp(node.entropy) < 0 {
							bestNode = node
						}
					}
				}
				nodeList = append(nodeList, bestNode)
			}
		}
	}

	sort.Slice(nodeList, func(i, j int) bool {
		return nodeList[i].entropy.Cmp(nodeList[j].entropy) > 0
	})

	return nodeList
}

func (hc *HierarchicalCoordinator) GetNodeListForLocation(location common.Location, badHashesList map[common.Hash]bool) []Node {
	recentBlocksCache, exists := hc.recentBlocks[location.Name()]
	if !exists || recentBlocksCache == nil {
		return []Node{}
	}
	nodeList := []Node{}
	for _, key := range recentBlocksCache.Keys() {
		if _, exists := badHashesList[key]; exists {
			continue
		}
		node, _ := recentBlocksCache.Peek(key)
		if node.Empty() {
			continue
		}
		nodeList = append(nodeList, node)
	}
	sort.Slice(nodeList, func(i, j int) bool {
		return nodeList[i].entropy.Cmp(nodeList[j].entropy) > 0
	})
	return nodeList
}

func PrintConstraintMap(constraintMap map[string]common.Hash) {
	log.Global.Debug("constraint map")
	for location, child := range constraintMap {
		log.Global.WithFields(log.Fields(log.Fields{"Location": location, "Header": child})).Debug("constraint map")
	}
}

func (hc *HierarchicalCoordinator) BuildPendingHeaders(wo *types.WorkObject, order int, newEntropy *big.Int) {
	timer := time.NewTimer(c_buildPendingHeadersTimeout)
	defer timer.Stop()
	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)

	hc.oneMu.Lock()
	defer hc.oneMu.Unlock()

	startingLen := len(hc.pendingHeaders.order)
	var entropy *big.Int
	misses := 0
	threshold := 20

	var start time.Time
	newPendingHeaders := NewPendingHeaders()
	start = time.Now()
	log.Global.WithField("len", startingLen).Info("PendingHeadersOrder")
	for i := startingLen - 1; i >= 0; i-- {
		entropy = hc.pendingHeaders.order[i]
		nodeSet, exists := hc.Get(entropy)
		if !exists {
			log.Global.WithFields(log.Fields{"entropy": common.BigBitsToBits(entropy), "order": order, "number": wo.NumberArray(), "hash": wo.Hash()}).Trace("NodeSet not found for entropy")
		}

		if nodeSet.Extendable(wo, order) {
			// update the nodeset
			newNodeSet := nodeSet.Copy()
			newNodeSet.Update(wo, newEntropy, order)

			// Calculate new set entropy
			newSetEntropy := newNodeSet.Entropy(int(numRegions), int(numZones))
			if new(big.Int).Sub(hc.bestEntropy, big.NewInt(30)).Cmp(newSetEntropy) < 0 {
				log.Global.WithFields(log.Fields{"newSetEntropy": common.BigBitsToBits(newSetEntropy), "Best Entropy": common.BigBitsToBits(hc.bestEntropy)}).Info("Pending Headers Cache New Set Entropy")
				printNodeSet(newNodeSet)
			}
			hc.Add(newSetEntropy, newNodeSet, newPendingHeaders)
		} else {
			log.Global.WithFields(log.Fields{"entropy": common.BigBitsToBits(entropy), "order": order, "number": wo.NumberArray(), "hash": wo.Hash()}).Trace("NodeSet not found for entropy")
		}
		misses++
		if misses > threshold {
			break
		}
	}

	for _, entropy := range newPendingHeaders.order {
		newCollection, exists := newPendingHeaders.collection.Peek(entropy.String())
		if exists {
			_, exists = hc.pendingHeaders.collection.Peek(entropy.String())
			if !exists {
				hc.pendingHeaders.order = append(hc.pendingHeaders.order, entropy)
				hc.pendingHeaders.collection.Add(entropy.String(), newCollection)
			}
		}
	}

	bestNode, exists := hc.pendingHeaders.collection.Peek(hc.bestEntropy.String())
	if exists && hc.generateHeaderWorkersCount <= c_maxHeaderWorkers {
		hc.generateHeaderWorkersCount++
		go hc.ComputePendingHeaders(bestNode)
	} else {
		log.Global.Info("Reached the maxHeaderWorkers, skipping GeneratePending")
	}
	sort.Slice(hc.pendingHeaders.order, func(i, j int) bool {
		return hc.pendingHeaders.order[i].Cmp(hc.pendingHeaders.order[j]) < 0 // Sort based on big.Int values
	})

	log.Global.WithField("time since start", time.Since(start)).Info("Time taken to compute pending headers")
}

func (hc *HierarchicalCoordinator) ComputePendingHeaders(nodeSet NodeSet) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	numRegions, numZones := common.GetHierarchySizeForExpansionNumber(hc.currentExpansionNumber)
	var wg sync.WaitGroup
	primeLocation := common.Location{}.Name()
	for i := 0; i < int(numRegions); i++ {
		regionLocation := common.Location{byte(i)}.Name()
		for j := 0; j < int(numZones); j++ {
			zoneLocation := common.Location{byte(i), byte(j)}.Name()

			wg.Add(1)
			go hc.ComputePendingHeader(&wg, nodeSet.nodes[primeLocation].hash, nodeSet.nodes[regionLocation].hash, nodeSet.nodes[zoneLocation].hash, common.Location{byte(i), byte(j)})
		}
	}
	wg.Wait()
	hc.generateHeaderWorkersCount--
}

func circularShift(arr []Node) []Node {
	if len(arr) <= 1 {
		return arr // No need to shift if array has 0 or 1 elements
	}

	shifted := arr[1:]

	return append(shifted, arr[0])
}

// PCRC previous coincidence reference check makes sure there are not any cyclic references in the graph and calculates new termini and the block terminus
func (hc *HierarchicalCoordinator) pcrc(subParentHash common.Hash, domTerminus common.Hash, location common.Location, ctx int) bool {
	backend := hc.GetBackend(location)
	termini := backend.GetTerminiByHash(subParentHash)
	if termini == nil {
		return false
	}
	if termini.DomTerminus(location) != domTerminus {
		return false
	}
	return true
}

func CopyConstraintMap(constraintMap map[string]common.Hash) map[string]common.Hash {
	newMap := make(map[string]common.Hash)
	for k, v := range constraintMap {
		newMap[k] = v
	}
	return newMap
}

func (hc *HierarchicalCoordinator) GetBackend(location common.Location) quaiapi.Backend {
	switch location.Context() {
	case common.PRIME_CTX:
		return *hc.consensus.GetBackend(location)
	case common.REGION_CTX:
		return *hc.consensus.GetBackend(location)
	case common.ZONE_CTX:
		return *hc.consensus.GetBackend(location)
	}
	return nil
}

func (hc *HierarchicalCoordinator) GetContextLocation(location common.Location, ctx int) common.Location {
	switch ctx {
	case common.PRIME_CTX:
		return common.Location{}
	case common.REGION_CTX:
		return common.Location{byte(location.Region())}
	case common.ZONE_CTX:
		return location
	}
	return nil
}

func (hc *HierarchicalCoordinator) calculateFrontierPoints(constraintMap map[string]common.Hash, leader *types.WorkObject, first bool) (map[string]common.Hash, error) {
	leaderLocation := leader.Location()
	leaderBackend := *hc.consensus.GetBackend(leaderLocation)

	if leaderBackend.IsGenesisHash(leader.Hash()) {
		return constraintMap, nil
	}

	// copy the starting constraint map
	startingConstraintMap := CopyConstraintMap(constraintMap)

	// trace back from the leader and stop after finding a prime block from each region or reach genesis
	_, leaderOrder, err := leaderBackend.CalcOrder(leader)
	if err != nil {
		return startingConstraintMap, err
	}

	constraintMap[leader.Location().Name()] = leader.Hash()
	currentOrder := leaderOrder
	current := leader
	iteration := 0
	parent := current
	parentOrder := currentOrder
	finished := false

	for !finished {
		// If there is a change in order update constraint or break
		if parentOrder < currentOrder || iteration == 0 {
			t, exists := constraintMap[string(hc.GetContextLocation(parent.Location(), parentOrder).Name())]
			switch parentOrder {
			case common.PRIME_CTX:
				primeBackend := hc.GetBackend(common.Location{})
				primeTermini := primeBackend.GetTerminiByHash(parent.Hash())
				if primeTermini == nil {
					return startingConstraintMap, errors.New("prime termini shouldnt be nil")
				}
				regionBackend := hc.GetBackend(hc.GetContextLocation(parent.Location(), common.REGION_CTX))
				regionTermini := regionBackend.GetTerminiByHash(parent.Hash())
				if regionTermini == nil {
					return startingConstraintMap, errors.New("region termini shouldnt be nil")
				}
				if exists {
					parentHeader := primeBackend.GetHeaderByHash(parent.Hash())
					if parentHeader == nil {
						return startingConstraintMap, err
					}
					isAncestor := hc.IsAncestor(t, parent.Hash(), parentHeader.Location(), common.PRIME_CTX)
					isProgeny := hc.IsAncestor(parent.Hash(), t, hc.GetContextLocation(parent.Location(), common.PRIME_CTX), common.PRIME_CTX)
					if isAncestor || isProgeny {
						if isAncestor {
							if !isProgeny {
								constraintMap[string(hc.GetContextLocation(parent.Location(), common.PRIME_CTX).Name())] = parent.Hash()
							}

						}
						regionConstraint, exists := constraintMap[hc.GetContextLocation(parent.Location(), common.REGION_CTX).Name()]
						if exists {
							isRegionProgeny := hc.IsAncestor(parent.Hash(), regionConstraint, hc.GetContextLocation(parent.Location(), common.REGION_CTX), common.REGION_CTX)
							if !isRegionProgeny {
								constraintMap[string(hc.GetContextLocation(parent.Location(), common.REGION_CTX).Name())] = parent.Hash()
							}
						} else {
							constraintMap[string(hc.GetContextLocation(parent.Location(), common.REGION_CTX).Name())] = parent.Hash()
						}
						if !first {
							finished = true
						}
					} else {
						return startingConstraintMap, errors.New("zone not in region constraint")
					}
				} else {
					if parent.NumberU64(parentOrder) == 0 {
						constraintMap[common.Location{}.Name()] = current.Hash()
						constraintMap[string(hc.GetContextLocation(current.Location(), common.REGION_CTX).Name())] = current.Hash()
						finished = true
					} else {
						if parentOrder == common.PRIME_CTX && currentOrder == common.REGION_CTX {
							constraintMap[string(hc.GetContextLocation(parent.Location(), common.PRIME_CTX).Name())] = parent.Hash()
						} else if parentOrder == common.PRIME_CTX {
							constraintMap[string(hc.GetContextLocation(parent.Location(), common.PRIME_CTX).Name())] = parent.Hash()
							constraintMap[string(hc.GetContextLocation(parent.Location(), common.REGION_CTX).Name())] = parent.Hash()
						}
					}
				}

			case common.REGION_CTX:
				regionBackend := hc.GetBackend(hc.GetContextLocation(parent.Location(), common.REGION_CTX))
				regionTermini := regionBackend.GetTerminiByHash(parent.Hash())
				if regionTermini == nil {
					return startingConstraintMap, errors.New("termini shouldnt be nil in region")
				}
				if exists {
					parentHeader := regionBackend.GetHeaderByHash(parent.Hash())
					if parentHeader == nil {
						return startingConstraintMap, errors.New("prime parent header shouldnt be nil")
					}
					isAncestor := hc.IsAncestor(t, parent.Hash(), parentHeader.Location(), common.REGION_CTX)
					isProgeny := hc.IsAncestor(parent.Hash(), t, hc.GetContextLocation(parent.Location(), common.REGION_CTX), common.REGION_CTX)
					if isAncestor || isProgeny {
						if isAncestor && !isProgeny {
							constraintMap[string(hc.GetContextLocation(parent.Location(), common.REGION_CTX).Name())] = parent.Hash()
						}
					} else {
						return startingConstraintMap, errors.New("zone not in region constraint")
					}

				} else {
					constraintMap[string(hc.GetContextLocation(parent.Location(), common.REGION_CTX).Name())] = parent.Hash()
				}

			case common.ZONE_CTX:
				constraintMap[parent.Location().Name()] = parent.Hash()
			}
		}

		current = parent
		currentOrder = min(parentOrder, currentOrder)
		var backend quaiapi.Backend
		switch currentOrder {
		case common.PRIME_CTX:
			backend = hc.GetBackend(common.Location{})
		case common.REGION_CTX:
			backend = hc.GetBackend(common.Location{byte(current.Location().Region())})
		case common.ZONE_CTX:
			backend = hc.GetBackend(current.Location())
		}

		if backend.IsGenesisHash(parent.ParentHash(currentOrder)) || backend.IsGenesisHash(parent.Hash()) {
			break
		}
		parent = backend.GetHeaderByHash(parent.ParentHash(currentOrder))

		_, parentOrder, err = backend.CalcOrder(parent)
		if err != nil {
			return startingConstraintMap, err
		}
		iteration++

		if currentOrder == common.PRIME_CTX {
			break
		}

	}
	return constraintMap, nil
}

func (hc *HierarchicalCoordinator) IsAncestor(ancestor common.Hash, header common.Hash, headerLoc common.Location, order int) bool {
	if ancestor == header {
		return true
	}
	backend := hc.GetBackend(hc.GetContextLocation(headerLoc, order))
	for i := 0; i < c_ancestorCheckDist; i++ {
		parent := backend.GetHeaderByHash(header)
		if parent != nil {
			if parent.ParentHash(order) == ancestor {
				return true
			}
			header = parent.ParentHash(order)
		}
	}
	return false
}

func (hc *HierarchicalCoordinator) ComputePendingHeader(wg *sync.WaitGroup, primeNode, regionNode, zoneNode common.Hash, location common.Location) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	defer wg.Done()
	var primePendingHeader, regionPendingHeader, zonePendingHeader *types.WorkObject
	var err error
	primeBackend := *hc.consensus.GetBackend(common.Location{})
	regionBackend := *hc.consensus.GetBackend(common.Location{byte(location.Region())})
	zoneBackend := *hc.consensus.GetBackend(location)
	primeBlock := primeBackend.GetBlockByHash(primeNode)
	if primeBlock == nil {
		log.Global.WithField("hash", primeNode.String()).Error("prime block not found for hash")
	} else {
		primePendingHeader, err = primeBackend.GeneratePendingHeader(primeBlock, false)
		if err != nil {
			log.Global.WithFields(log.Fields{"error": err, "location": location.Name()}).Error("Error generating prime pending header")
		}
	}
	regionBlock := regionBackend.GetBlockByHash(regionNode)
	if regionBlock == nil {
		log.Global.WithField("hash", regionNode.String()).Error("region block not found for hash")
	} else {
		regionPendingHeader, err = regionBackend.GeneratePendingHeader(regionBlock, false)
		if err != nil {
			log.Global.WithFields(log.Fields{"error": err, "location": location.Name()}).Error("Error generating region pending header")
		}
	}
	zoneBlock := zoneBackend.GetBlockByHash(zoneNode)
	if zoneBlock == nil {
		log.Global.WithField("hash", zoneNode.String()).Error("zone block not found for hash")
	} else {
		zonePendingHeader, err = zoneBackend.GeneratePendingHeader(zoneBlock, false)
		if err != nil {
			log.Global.WithFields(log.Fields{"error": err, "location": location.Name()}).Error("Error generating zone pending header")
		}
	}

	// If any of the pending header is nil, return
	if primePendingHeader == nil || regionPendingHeader == nil || zonePendingHeader == nil {
		return
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

func ReIndexChainIndexer() {
	providedDataDir := viper.GetString(DataDirFlag.Name)
	if providedDataDir == "" {
		log.Global.Fatal("Data directory not provided for reindexing")
	}
	dbDir := filepath.Join(filepath.Join(providedDataDir, "zone-0-0/go-quai"), "chaindata")
	ancientDir := filepath.Join(dbDir, "ancient")
	zoneDb, err := rawdb.Open(rawdb.OpenOptions{
		Type:              "leveldb",
		Directory:         dbDir,
		AncientsDirectory: ancientDir,
		Namespace:         "eth/db/chaindata/",
		Cache:             512,
		Handles:           5120,
		ReadOnly:          false,
	}, common.ZONE_CTX, log.Global, common.Location{0, 0})
	if err != nil {
		log.Global.WithField("err", err).Fatal("Error opening the zone db for reindexing")
	}
	core.ReIndexChainIndexer(zoneDb)
	if err := zoneDb.Close(); err != nil {
		log.Global.WithField("err", err).Fatal("Error closing the zone db")
	}
	time.Sleep(10 * time.Second)
}

func ValidateChainIndexer() {
	providedDataDir := viper.GetString(DataDirFlag.Name)
	if providedDataDir == "" {
		log.Global.Fatal("Data directory not provided for reindexing")
	}
	dbDir := filepath.Join(filepath.Join(providedDataDir, "zone-0-0/go-quai"), "chaindata")
	ancientDir := filepath.Join(dbDir, "ancient")
	zoneDb, err := rawdb.Open(rawdb.OpenOptions{
		Type:              "leveldb",
		Directory:         dbDir,
		AncientsDirectory: ancientDir,
		Namespace:         "eth/db/chaindata/",
		Cache:             512,
		Handles:           5120,
		ReadOnly:          false,
	}, common.ZONE_CTX, log.Global, common.Location{0, 0})
	if err != nil {
		log.Global.WithField("err", err).Fatal("Error opening the zone db for reindexing")
	}
	start := time.Now()
	head := rawdb.ReadHeadBlockHash(zoneDb)
	if head == (common.Hash{}) {
		log.Global.Fatal("Head block hash not found")
	}
	headNum := rawdb.ReadHeaderNumber(zoneDb, head)
	latestSetSize := rawdb.ReadUTXOSetSize(zoneDb, head)
	log.Global.Infof("Starting the UTXO indexer validation for height %d set size %d", *headNum, latestSetSize)
	i := 0
	utxosChecked := make(map[[34]byte]uint8)
	it := zoneDb.NewIterator(rawdb.UtxoPrefix, nil)
	for it.Next() {
		if len(it.Key()) != rawdb.UtxoKeyLength {
			continue
		}
		data := it.Value()
		if len(data) == 0 {
			log.Global.Infof("Empty key found")
			continue
		}
		utxoProto := new(types.ProtoTxOut)
		if err := proto.Unmarshal(data, utxoProto); err != nil {
			log.Global.Errorf("Failed to unmarshal ProtoTxOut: %+v data: %+v key: %+v", err, data, it.Key())
			continue
		}

		utxo := new(types.UtxoEntry)
		if err := utxo.ProtoDecode(utxoProto); err != nil {
			log.Global.WithFields(log.Fields{
				"key":  it.Key(),
				"data": data,
				"err":  err,
			}).Error("Invalid utxo Proto")
			continue
		}
		txHash, index, err := rawdb.ReverseUtxoKey(it.Key())
		if err != nil {
			log.Global.WithField("err", err).Error("Failed to parse utxo key")
			continue
		}
		u16 := make([]byte, 2)
		binary.BigEndian.PutUint16(u16, index)
		key := [34]byte(append(txHash.Bytes(), u16...))
		if _, exists := utxosChecked[key]; exists {
			log.Global.WithField("hash", key).Error("Duplicate utxo found")
			continue
		}
		height := rawdb.ReadUtxoToBlockHeight(zoneDb, txHash, index)
		addr20 := common.BytesToAddress(utxo.Address, common.Location{0, 0}).Bytes20()
		binary.BigEndian.PutUint32(addr20[16:], height)
		outpoints, err := rawdb.ReadOutpointsForAddressAtBlock(zoneDb, addr20)
		if err != nil {
			log.Global.WithField("err", err).Error("Error reading outpoints for address")
			continue
		}
		found := false
		for _, outpoint := range outpoints {
			if outpoint.TxHash == txHash && outpoint.Index == index {
				utxosChecked[key] = outpoint.Denomination
				found = true
			}
		}
		if !found {
			log.Global.WithFields(log.Fields{
				"tx":    txHash,
				"index": index,
			}).Error("Utxo not found in outpoints")
			prefix := append(rawdb.AddressUtxosPrefix, addr20.Bytes()[:16]...)
			it2 := zoneDb.NewIterator(prefix, nil)
			for it2.Next() {
				if len(it.Key()) != len(rawdb.AddressUtxosPrefix)+common.AddressLength {
					continue
				}
				addressOutpointsProto := &types.ProtoAddressOutPoints{
					OutPoints: make([]*types.ProtoOutPointAndDenomination, 0),
				}
				if err := proto.Unmarshal(it.Value(), addressOutpointsProto); err != nil {
					log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal addressOutpointsProto")
					continue
				}
				for _, outpointProto := range addressOutpointsProto.OutPoints {
					outpoint := new(types.OutpointAndDenomination)
					if err := outpoint.ProtoDecode(outpointProto); err != nil {
						log.Global.WithFields(log.Fields{
							"err":      err,
							"outpoint": outpointProto,
						}).Error("Invalid outpointProto")
						continue
					}
					if outpoint.TxHash == txHash && outpoint.Index == index {
						log.Global.WithFields(log.Fields{
							"tx":    txHash,
							"index": index,
						}).Error("Utxo found in address outpoints")
						utxosChecked[key] = outpoint.Denomination
						found = true
					}
				}
			}
			it2.Release()
		}
		i++
		if i%100000 == 0 {
			log.Global.Infof("Checked %d utxos out of %d total elapsed %s", i, latestSetSize, common.PrettyDuration(time.Since(start)))
		}
	}
	it.Release()
	log.Global.Infof("Checked %d utxos and %d are good, elapsed %s", i, len(utxosChecked), common.PrettyDuration(time.Since(start)))
	if len(utxosChecked) != int(latestSetSize) {
		log.Global.WithFields(log.Fields{
			"expected": latestSetSize,
			"actual":   len(utxosChecked),
		}).Error("Mismatch in utxo set size")
	}
	log.Global.Infof("Checking for duplicates in Address Outpoints Index...")
	utxosChecked_ := make(map[[34]byte]uint8)
	duplicatesFound := false
	it = zoneDb.NewIterator(rawdb.AddressUtxosPrefix, nil)
	for it.Next() {
		if len(it.Key()) != len(rawdb.AddressUtxosPrefix)+common.AddressLength {
			continue
		}
		addressOutpointsProto := &types.ProtoAddressOutPoints{
			OutPoints: make([]*types.ProtoOutPointAndDenomination, 0),
		}
		if err := proto.Unmarshal(it.Value(), addressOutpointsProto); err != nil {
			log.Global.WithField("err", err).Fatal("Failed to proto Unmarshal addressOutpointsProto")
			continue
		}
		for _, outpointProto := range addressOutpointsProto.OutPoints {
			outpoint := new(types.OutpointAndDenomination)
			if err := outpoint.ProtoDecode(outpointProto); err != nil {
				log.Global.WithFields(log.Fields{
					"err":      err,
					"outpoint": outpointProto,
				}).Error("Invalid outpointProto")
				continue
			}
			u16 := make([]byte, 2)
			binary.BigEndian.PutUint16(u16, outpoint.Index)
			key := [34]byte(append(outpoint.TxHash.Bytes(), u16...))
			if _, exists := utxosChecked_[key]; exists {
				log.Global.WithFields(log.Fields{
					"tx":    outpoint.TxHash.String(),
					"index": outpoint.Index,
				}).Error("Duplicate outpoint found")
				duplicatesFound = true
				continue
			}
			utxosChecked_[key] = outpoint.Denomination
		}
	}
	it.Release()
	if len(utxosChecked_) != int(latestSetSize) {
		log.Global.WithFields(log.Fields{
			"expected": latestSetSize,
			"actual":   len(utxosChecked_),
		}).Error("Mismatch in utxo set size")
		time.Sleep(5 * time.Second)
		if len(utxosChecked_) > len(utxosChecked) {
			log.Global.Infof("Finding diff...")
			for key, val := range utxosChecked_ {
				if _, exists := utxosChecked[key]; !exists {
					txhash := key[:32]
					index := binary.BigEndian.Uint16(key[32:])
					log.Global.WithFields(log.Fields{
						"tx":           common.BytesToHash(txhash).String(),
						"index":        index,
						"denomination": val,
					}).Error("Missing key")
				}
			}
		}
	}
	if duplicatesFound {
		log.Global.Error("Duplicates found in address outpoints")
	} else {
		log.Global.Info("No duplicates found in address-outpoints index. Validation completed")
	}
	if err := zoneDb.Close(); err != nil {
		log.Global.WithField("err", err).Fatal("Error closing the zone db")
	}
	time.Sleep(30 * time.Second)
}
