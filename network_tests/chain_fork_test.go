package network_tests

import (
	"context"
	"errors"
	"log"
	"math/big"
	"testing"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/core"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/ethclient"
	"github.com/spruce-solutions/go-quai/params"
)

// Block struct to hold all Client fields.
type orderedBlockClients struct {
	primeClient   *ethclient.Client
	regionClients []*ethclient.Client
	zoneClients   [][]*ethclient.Client
}

// Generate blocks to form a network of chains
func (obc *orderedBlockClients) SendBlocksToNodes(blocks map[string]*types.Block) error {
	for _, block := range blocks {
		location := block.Header().Location
		zone := obc.zoneClients[location[0]][location[1]]
		region := obc.regionClients[location[0]]
		prime := obc.primeClient
		zone.SendMinedBlock(context.Background(), block, true, true)
		receiptBlock, receiptErr := zone.GetBlockReceipts(context.Background(), block.Hash())
		if receiptErr != nil {
			return receiptErr
		}
		region.SendExternalBlock(context.Background(), block, receiptBlock.Receipts(), big.NewInt(2))
		prime.SendExternalBlock(context.Background(), block, receiptBlock.Receipts(), big.NewInt(2))
	}
	return nil
}

func (obc *orderedBlockClients) SelectClient(region, zone *int) *ethclient.Client {
	// Decode location
	if region == nil && zone == nil {
		return obc.primeClient
	} else if region != nil && zone == nil {
		return obc.regionClients[*region]
	} else if region != nil && zone != nil {
		return obc.zoneClients[*region][*zone]
	} else {
		return nil
	}
}

func (obc *orderedBlockClients) GetNodeHeadHash(region, zone *int) common.Hash {
	// Select the correct client for the requested location
	client := obc.SelectClient(region, zone)
	if client != nil {
		log.Fatal("Failed to select client for the requested location")
	}

	// Get latest block and return its hash
	number, err := client.BlockNumber(context.Background())
	if err != nil {
		log.Fatal("Failed to get block number")
	}
	header, err := obc.primeClient.HeaderByNumber(context.Background(), big.NewInt(int64(number)))
	if err != nil {
		log.Fatal("Failed to get head")
	}
	return header.Hash()
}

func (obc *orderedBlockClients) GetNodeTotalDifficulties(region, zone *int) ([]*big.Int, error) {
	// Select the correct client for the requested location
	client := obc.SelectClient(region, zone)
	if client != nil {
		log.Fatal("Failed to select client for the requested location")
	}

	number, err := client.BlockNumber(context.Background())
	if err != nil {
		log.Fatal("Failed to get block number")
	}
	block, err := obc.primeClient.BlockByNumber(context.Background(), big.NewInt(int64(number)))
	if err != nil {
		log.Fatal("Failed to get head")
	}
	td := []*big.Int{block.NetworkDifficulty(0), block.NetworkDifficulty(1), block.NetworkDifficulty(2)}
	if td == nil || td[0] == nil || td[1] == nil || td[2] == nil {
		return nil, errors.New("Failed to get total difficulties")
	}
	return td, nil
}

func (obc *orderedBlockClients) resetToGenesis() error {
	primeGenesis, err := obc.primeClient.HeaderByHash(context.Background(), params.RopstenPrimeGenesisHash)
	if err != nil {
		return errors.New("Failed to reset prime node")
	}
	err = obc.primeClient.SendReOrgData(context.Background(), primeGenesis, nil, nil)
	if err != nil {
		return errors.New("Failed to reset prime node")
	}
	regionGenesis, err := obc.primeClient.HeaderByHash(context.Background(), params.RopstenRegionGenesisHash)
	if err != nil {
		return errors.New("Failed to reset prime node")
	}
	for _, c := range obc.regionClients {
		err := c.SendReOrgData(context.Background(), regionGenesis, nil, nil)
		if err != nil {
			return errors.New("Failed to reset region node")
		}
	}
	zoneGenesis, err := obc.primeClient.HeaderByHash(context.Background(), params.RopstenZoneGenesisHash)
	if err != nil {
		return errors.New("Failed to reset prime node")
	}
	for _, region := range obc.zoneClients {
		for _, c := range region {
			err := c.SendReOrgData(context.Background(), zoneGenesis, nil, nil)
			if err != nil {
				return errors.New("Failed to reset region node")
			}
		}
	}
	return nil
}

// getNodeClients takes in a config and retrieves the Prime, Region, and Zone client
// that is used for mining in a slice.
func getNodeClients() (orderedBlockClients, error) {
	primeURL := "ws://127.0.0.1:8547"
	regionURLs := []string{"ws://127.0.0.1:8579", "ws://127.0.0.1:8581", "ws://127.0.0.1:8583"}
	zoneURLs := [][]string{
		{"ws://127.0.0.1:8611", "ws://127.0.0.1:8643", "ws://127.0.0.1:8675"},
		{"ws://127.0.0.1:8613", "ws://127.0.0.1:8645", "ws://127.0.0.1:8677"},
		{"ws://127.0.0.1:8615", "ws://127.0.0.1:8647", "ws://127.0.0.1:8679"},
	}

	// initializing all the clients
	allClients := orderedBlockClients{
		regionClients: make([]*ethclient.Client, 3),
		zoneClients:   make([][]*ethclient.Client, 3),
	}

	for i := range allClients.zoneClients {
		allClients.zoneClients[i] = make([]*ethclient.Client, 3)
	}

	// add Prime to orderedBlockClient array at [0]
	if primeURL != "" {
		primeClient, err := ethclient.Dial(primeURL)
		if err != nil {
			log.Println("Error connecting to Prime mining node ", primeURL)
			return orderedBlockClients{}, errors.New("Unable to connect to prime")
		} else {
			allClients.primeClient = primeClient
		}
	}

	// loop to add Regions to orderedBlockClient
	// remember to set true value for Region to be mined
	for i, URL := range regionURLs {
		regionURL := URL
		if regionURL != "" {
			regionClient, err := ethclient.Dial(regionURL)
			if err != nil {
				log.Println("Error connecting to Region mining node ", URL, " in location ", i)
				return orderedBlockClients{}, errors.New("Unable to connect to region")
			} else {
				allClients.regionClients[i] = regionClient
			}
		}
	}

	// loop to add Zones to orderedBlockClient
	// remember ZoneURLS is a 2D array
	for i, zonesURLs := range zoneURLs {
		for j, zoneURL := range zonesURLs {
			if zoneURL != "" {
				zoneClient, err := ethclient.Dial(zoneURL)
				if err != nil {
					log.Println("Error connecting to Zone mining node ", zoneURL, " in location ", i, " ", j)
					return orderedBlockClients{}, errors.New("Unable to connect to zone")
				} else {
					allClients.zoneClients[i][j] = zoneClient
				}
			}
		}
	}
	return allClients, nil
}

// After running a test scenario, check if the nodes are on the correct heads
func (obc *orderedBlockClients) CheckPassFail(blocks map[string]*types.Block) {
	// Collect all final blocks that were tagged in the blocks map
	finalPrime := blocks["final_prime"]
	finalRegion := []*types.Block{blocks["final_region0"], blocks["final_region1"], blocks["final_region2"]}
	finalZone := [][]*types.Block{
		[]*types.Block{blocks["final_zone00"], blocks["final_zone01"], blocks["final_zone02"]},
		[]*types.Block{blocks["final_zone10"], blocks["final_zone11"], blocks["final_zone12"]},
		[]*types.Block{blocks["final_zone20"], blocks["final_zone21"], blocks["final_zone22"]},
	}

	// Check node heads
	if finalPrime != nil && finalPrime.Hash() != obc.GetNodeHeadHash(nil, nil) {
		log.Fatal("Prime node is on wrong head!")
	}
	for r, region := range finalRegion {
		if region != nil && region.Hash() != obc.GetNodeHeadHash(&r, nil) {
			log.Fatal("Region", r, " node is on wrong head!")
		}
	}
	for r, region := range finalZone {
		for z, zone := range region {
			if region != nil && zone.Hash() != obc.GetNodeHeadHash(&r, &z) {
				log.Fatal("Zone", r, z, " node is on wrong head!")
			}
		}
	}

	// Check node total difficulties
	if finalPrime != nil {
		totalDifficulties, err := obc.GetNodeTotalDifficulties(nil, nil)
		if err != nil {
			log.Fatal("Failed to get Prime difficulties")
		}
		expectedPrimeDiff := new(big.Int).Mul(params.MinimumDifficulty[params.PRIME], finalPrime.Number(params.PRIME))
		expectedRegionDiff := new(big.Int).Mul(params.MinimumDifficulty[params.REGION], finalPrime.Number(params.REGION))
		expectedZoneDiff := new(big.Int).Mul(params.MinimumDifficulty[params.ZONE], finalPrime.Number(params.ZONE))
		if expectedPrimeDiff != totalDifficulties[params.PRIME] ||
			expectedRegionDiff != totalDifficulties[params.REGION] ||
			expectedZoneDiff != totalDifficulties[params.ZONE] {
			log.Fatal("Prime node has wrong total difficulties!")
		}
	}
	for r, region := range finalRegion {
		totalDifficulties, err := obc.GetNodeTotalDifficulties(&r, nil)
		if err != nil {
			log.Fatal("Failed to get region", r, " difficulties")
		}
		expectedPrimeDiff := new(big.Int).Mul(params.MinimumDifficulty[params.PRIME], region.Number(params.PRIME))
		expectedRegionDiff := new(big.Int).Mul(params.MinimumDifficulty[params.REGION], region.Number(params.REGION))
		expectedZoneDiff := new(big.Int).Mul(params.MinimumDifficulty[params.ZONE], region.Number(params.ZONE))
		if expectedPrimeDiff != totalDifficulties[params.PRIME] ||
			expectedRegionDiff != totalDifficulties[params.REGION] ||
			expectedZoneDiff != totalDifficulties[params.ZONE] {
			log.Fatal("Region", r, " has the wrong total difficulties!")
		}
	}
	for r, region := range finalZone {
		for z, zone := range region {
			totalDifficulties, err := obc.GetNodeTotalDifficulties(&r, &z)
			if err != nil {
				log.Fatal("Failed to get zone", r, z, " difficulties")
			}
			expectedPrimeDiff := new(big.Int).Mul(params.MinimumDifficulty[params.PRIME], zone.Number(params.PRIME))
			expectedRegionDiff := new(big.Int).Mul(params.MinimumDifficulty[params.REGION], zone.Number(params.REGION))
			expectedZoneDiff := new(big.Int).Mul(params.MinimumDifficulty[params.ZONE], zone.Number(params.ZONE))
			if expectedPrimeDiff != totalDifficulties[params.PRIME] ||
				expectedRegionDiff != totalDifficulties[params.REGION] ||
				expectedZoneDiff != totalDifficulties[params.ZONE] {
				log.Fatal("Zone", r, z, " has the wrong total difficulties!")
			}
		}
	}
}

//Generate blocks and run the test scenario simulation
func GenAndRun(graph [3][3][]*types.BlockGenSpec) (orderedBlockClients, map[string]*types.Block) {
	// Reset the node to genesis
	clients, err := getNodeClients()
	if err != nil {
		log.Fatal("Error connecting to nodes!")
	}
	if nil != clients.resetToGenesis() {
		log.Fatal("Failed to reset nodes to genesis!")
	}

	// Generate the blocks for this graph
	blocks, err := core.GenerateNetworkBlocks(graph)
	if err != nil {
		log.Fatal("Error generating blocks!")
	}

	// Send internal & external blocks to the node
	err = clients.SendBlocksToNodes(blocks)
	if err != nil {
		log.Fatal("Failed to send all blocks to the node!")
	}
	return clients, blocks
}

// Example test for a fork choice scenario shown in slide00 (not a real slide)
func TestForkChoice_Slide00(t *testing.T) {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{ // Region0
			{ // Zone0
				&types.BlockGenSpec{[3]int{1, 1, 1}, [3]string{}, "z00_1"},
				&types.BlockGenSpec{[3]int{-1, -1, 2}, [3]string{}, ""},
				// ...
				&types.BlockGenSpec{[3]int{-1, 2, 4}, [3]string{}, "z00_4"},
				&types.BlockGenSpec{[3]int{3, 3, 5}, [3]string{}, ""},
				&types.BlockGenSpec{[3]int{-1, -1, 6}, [3]string{}, ""},                // END OF CANONICAL CHAIN
				&types.BlockGenSpec{[3]int{-1, -1, 5}, [3]string{"", "", "z00_4"}, ""}, // Fork at z00_4
				// ...
				&types.BlockGenSpec{[3]int{-1, 3, 8}, [3]string{}, ""},
				&types.BlockGenSpec{[3]int{-1, -1, 5}, [3]string{"", "", "z00_4"}, ""}, // Fork at z00_4
				&types.BlockGenSpec{[3]int{-1, -1, 6}, [3]string{}, ""},
				&types.BlockGenSpec{[3]int{-1, -1, 7}, [3]string{"", "z00_1", ""}, ""}, // Twist to z00_1
			},
			{}, // ... Zone1 omitted
			{}, // ... Zone2 omitted
		},
		{ // Region1
			{ // Zone0
				&types.BlockGenSpec{[3]int{1, 1, 2}, [3]string{"", "", "z00_1"}, ""},
			},
			{}, // ... Zone1 omitted
			{}, // ... Zone2 omitted
		},
		{}, // ... Region2 omitted
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	clients.CheckPassFail(blocks)
}
