package utils

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/quai"
	"github.com/dominant-strategies/go-quai/quaiclient"
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

type HierarchicalCoordinator struct {
	db *leveldb.DB
	// APIS
	consensus quai.ConsensusAPI
	p2p       quai.NetworkingAPI

	logLevel string

	currentExpansionNumber uint8

	slicesRunning []common.Location

	expansionCh  chan core.ExpansionEvent
	expansionSub event.Subscription
	wg           *sync.WaitGroup

	quitCh chan struct{}

	treeExpansionTriggerStarted bool // flag to indicate if the tree expansion trigger has started
}

// NewHierarchicalCoordinator creates a new instance of the HierarchicalCoordinator
func NewHierarchicalCoordinator(p2p quai.NetworkingAPI, logLevel string, nodeWg *sync.WaitGroup, startingExpansionNumber uint64) *HierarchicalCoordinator {
	db, err := OpenBackendDB()
	if err != nil {
		log.Global.Fatal("Error opening the backend db")
	}
	hc := &HierarchicalCoordinator{
		wg:                          nodeWg,
		db:                          db,
		p2p:                         p2p,
		logLevel:                    logLevel,
		slicesRunning:               GetRunningZones(),
		treeExpansionTriggerStarted: false,
		quitCh:                      make(chan struct{}),
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

	return quaiBackend, nil
}

func (hc *HierarchicalCoordinator) startNode(logPath string, quaiBackend quai.ConsensusAPI, location common.Location, genesisBlock *types.WorkObject) {
	hc.wg.Add(1)
	logger := log.NewLogger(logPath, hc.logLevel)
	logger.Info("Starting Node at location", "location", location)
	stack, apiBackend := makeFullNode(hc.p2p, location, hc.slicesRunning, hc.currentExpansionNumber, genesisBlock, logger)
	quaiBackend.SetApiBackend(&apiBackend, location)

	// Subscribe to the new topics after setting the api backend
	hc.p2p.Subscribe(location, &types.WorkObject{})
	hc.p2p.Subscribe(location, common.Hash{})
	hc.p2p.Subscribe(location, &types.Transactions{})

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
			// Add the subclient to the already existing regions
			suburl := fmt.Sprintf("ws://127.0.0.1:%d", 8100+20*i+(int(newZones)-1))
			subClient, err := quaiclient.Dial(suburl, nil)
			if err != nil {
				log.Global.WithFields(log.Fields{
					"index": i,
					"err":   err,
				}).Fatal("Error connecting to the subordinate go-quai client")
			}
			// Set the subclient for the region
			hc.consensus.SetSubClient(subClient, common.Location{byte(i)}, common.Location{byte(i), byte(newZones - 1)})
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

		// Update the SubClient for the Prime
		// Add the subclient to the already existing regions
		suburl := fmt.Sprintf("ws://127.0.0.1:%d", 8002+(newRegions-1))
		subClient, err := quaiclient.Dial(suburl, nil)
		if err != nil {
			log.Global.WithFields(log.Fields{
				"index": newRegions - 1,
				"err":   err,
			}).Fatal("Error connecting to the subordinate go-quai client")
		}
		hc.consensus.SetSubClient(subClient, common.Location{}, common.Location{byte(newRegions - 1)})

		// new region has to activate all the zones
		for i := 0; i < int(newZones); i++ {
			logLocation = "zone-" + fmt.Sprintf("%d", newRegions-1) + "-" + fmt.Sprintf("%d", i) + ".log"
			hc.startNode(logLocation, hc.consensus, common.Location{byte(newRegions - 1), byte(i)}, block)
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
