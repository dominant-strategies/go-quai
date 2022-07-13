package network_tests

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"testing"
	"time"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus/blake3"
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
func (obc *orderedBlockClients) SendBlocksToNodes(blocks map[string]types.Block) error {
	for tag, block := range blocks {
		location := block.Header().Location
		order, err := blake3.NewFaker().GetDifficultyOrder(block.Header())
		if err != nil {
			return err
		}
		zone := obc.zoneClients[location[0]-1][location[1]-1]
		region := obc.regionClients[location[0]-1]
		prime := obc.primeClient
		// Pretty prints for test dialog
		prettyBlock := block.Hash().Bytes()[0:4]
		prettyParents := [][]byte{block.Header().ParentHash[0].Bytes()[0:4], block.Header().ParentHash[1].Bytes()[0:4], block.Header().ParentHash[2].Bytes()[0:4]}
		switch order {
		case params.PRIME:
			log.Printf("Sending PRIME block:\t%02x -> [%02x, %02x, %02x] (%s)", prettyBlock, prettyParents[0], prettyParents[1], prettyParents[2], tag)
		case params.REGION:
			log.Printf("Sending REGION[%d] block:\t%02x -> [________, %02x, %02x] (%s)", location[0], prettyBlock, prettyParents[1], prettyParents[2], tag)
		case params.ZONE:
			log.Printf("Sending ZONE[%d][%d] block: \t%02x -> [________, ________, %02x] (%s)", location[0], location[1], prettyBlock, prettyParents[2], tag)
		default:
			return fmt.Errorf("Unknown block order: %d", order)
		}
		// Send external blocks first
		prime.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.PRIME)))
		prime.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.REGION)))
		prime.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.ZONE)))
		region.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.PRIME)))
		region.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.REGION)))
		region.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.ZONE)))
		zone.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.PRIME)))
		zone.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.REGION)))
		zone.SendExternalBlock(context.Background(), &block, nil, big.NewInt(int64(params.ZONE)))
		// Send full blocks after slice has external blocks
		err = prime.SendMinedBlock(context.Background(), &block, true, true)
		if err != nil {
			return err
		}
		err = region.SendMinedBlock(context.Background(), &block, true, true)
		if err != nil {
			return err
		}
		err = zone.SendMinedBlock(context.Background(), &block, true, true)
		if err != nil {
			return err
		}
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
	if client == nil {
		log.Fatal("Failed to select client for the requested location")
	}

	// Get latest block and return its hash
	number, err := client.BlockNumber(context.Background())
	if err != nil {
		log.Fatal("Failed to get block number")
	}
	header, err := client.HeaderByNumber(context.Background(), big.NewInt(int64(number)))
	if err != nil {
		fmt.Errorf("Failed to get [%d, %d] head: %s", region, zone, err)
	}
	return header.Hash()
}

func (obc *orderedBlockClients) GetNodeTotalDifficulties(region, zone *int) ([]*big.Int, error) {
	// Select the correct client for the requested location
	client := obc.SelectClient(region, zone)
	if client == nil {
		log.Fatal("Failed to select client for the requested location")
	}
	td := client.GetHeadTd(context.Background())
	if td == nil || td[0] == nil || td[1] == nil || td[2] == nil {
		return nil, errors.New("Failed to get total difficulties")
	}
	return td, nil
}

func (obc *orderedBlockClients) resetToGenesis() error {
	// Reset Prime client
	block1, _ := obc.primeClient.HeaderByNumber(context.Background(), big.NewInt(1))
	if block1 != nil {
		err := obc.primeClient.SendReOrgData(context.Background(), block1, []*types.Header{}, []*types.Header{})
		if err != nil {
			return errors.New("Failed to reset prime node")
		}
	}
	// Reset region clients
	for r, c := range obc.regionClients {
		block1, _ := c.HeaderByNumber(context.Background(), big.NewInt(1))
		if block1 != nil {
			err := c.SendReOrgData(context.Background(), block1, []*types.Header{}, []*types.Header{})
			if err != nil {
				return fmt.Errorf("Failed to reset region[%d] node", r)
			}
		}
	}
	// Reset zone clients
	for r, region := range obc.zoneClients {
		for z, c := range region {
			block1, _ := c.HeaderByNumber(context.Background(), big.NewInt(1))
			if block1 != nil {
				err := c.SendReOrgData(context.Background(), block1, []*types.Header{}, []*types.Header{})
				if err != nil {
					return fmt.Errorf("Failed to reset zone[%d][%d] node", r, z)
				}
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
func (obc *orderedBlockClients) CheckPassFail(blocks map[string]types.Block) {
	finalPrimeTag := "final_prime"
	finalRegionTag := []string{"final_region1", "final_region2", "final_region3"}
	finalZoneTag := [][]string{
		[]string{"final_zone11", "final_zone12", "final_zone13"},
		[]string{"final_zone21", "final_zone22", "final_zone23"},
		[]string{"final_zone31", "final_zone32", "final_zone33"},
	}

	// Check node heads
	if block, exists := blocks[finalPrimeTag]; exists {
		actual := obc.GetNodeHeadHash(nil, nil)
		expected := block.Hash()
		if actual != expected {
			log.Fatal("Prime node is on wrong head!\nexpected:\t", expected, "\nactual:\t\t", actual)
		}
		totalDifficulties, err := obc.GetNodeTotalDifficulties(nil, nil)
		if err != nil {
			log.Fatal("Failed to get Prime difficulties")
		}
		expectedPrimeDiff := new(big.Int).Mul(params.MinimumDifficulty[params.PRIME], block.Number(params.PRIME))
		expectedRegionDiff := new(big.Int).Mul(params.MinimumDifficulty[params.REGION], block.Number(params.REGION))
		expectedZoneDiff := new(big.Int).Mul(params.MinimumDifficulty[params.ZONE], block.Number(params.ZONE))
		if expectedPrimeDiff != totalDifficulties[params.PRIME] ||
			expectedRegionDiff != totalDifficulties[params.REGION] ||
			expectedZoneDiff != totalDifficulties[params.ZONE] {
			log.Fatal("Prime node has wrong total difficulties!")
		}
	}
	for r, tag := range finalRegionTag {
		if block, exists := blocks[tag]; exists {
			actual := obc.GetNodeHeadHash(&r, nil)
			expected := block.Hash()
			if actual != expected {
				log.Fatal("Region[", r, "]node is on wrong head!\nexpected:\t", expected, "\nactual:\t\t", actual)
			}
			totalDifficulties, err := obc.GetNodeTotalDifficulties(&r, nil)
			if err != nil {
				log.Fatal("Failed to get region[", r, "] difficulties")
			}
			expectedPrimeDiff := new(big.Int).Mul(params.MinimumDifficulty[params.PRIME], block.Number(params.PRIME))
			expectedRegionDiff := new(big.Int).Mul(params.MinimumDifficulty[params.REGION], block.Number(params.REGION))
			expectedZoneDiff := new(big.Int).Mul(params.MinimumDifficulty[params.ZONE], block.Number(params.ZONE))
			if expectedPrimeDiff != totalDifficulties[params.PRIME] ||
				expectedRegionDiff != totalDifficulties[params.REGION] ||
				expectedZoneDiff != totalDifficulties[params.ZONE] {
				log.Fatal("Region[", r, "] node has wrong total difficulties!")
			}
		}
	}
	for r, zoneTags := range finalZoneTag {
		for z, tag := range zoneTags {
			if block, exists := blocks[tag]; exists {
				actual := obc.GetNodeHeadHash(&r, &z)
				expected := block.Hash()
				if actual != expected {
					log.Fatal("Zone[", r, "][", z, "] node is on wrong head!\nexpected:\t", expected, "\nactual:\t\t", actual)
				}
				totalDifficulties, err := obc.GetNodeTotalDifficulties(&r, &z)
				if err != nil {
					log.Fatal("Failed to get zone[", r, "][", z, "] difficulties")
				}
				expectedPrimeDiff := new(big.Int).Mul(params.MinimumDifficulty[params.PRIME], block.Number(params.PRIME))
				expectedRegionDiff := new(big.Int).Mul(params.MinimumDifficulty[params.REGION], block.Number(params.REGION))
				expectedZoneDiff := new(big.Int).Mul(params.MinimumDifficulty[params.ZONE], block.Number(params.ZONE))
				if expectedPrimeDiff != totalDifficulties[params.PRIME] ||
					expectedRegionDiff != totalDifficulties[params.REGION] ||
					expectedZoneDiff != totalDifficulties[params.ZONE] {
					log.Fatal("Zone[", r, "][", z, "] node has wrong total difficulties!")
				}
			}
		}
	}
}

//Generate blocks and run the test scenario simulation
func (obc *orderedBlockClients) GenAndRun(graph [3][3][]*types.BlockGenSpec) map[string]types.Block {
	// Reset the node to genesis
	err := obc.resetToGenesis()
	if err != nil {
		log.Fatalf("Faile to reset nodes to genesis: %s", err)
	}
	// Generate the blocks for this graph
	genesisBlock, err := obc.primeClient.BlockByNumber(context.Background(), big.NewInt(0))
	if err != nil {
		log.Fatal("Failed to get genesis block!")
	}
	log.Printf("Genesis block hash: %s", genesisBlock.Hash())
	blocks, err := core.GenerateNetworkBlocks(genesisBlock, graph)
	if err != nil {
		log.Fatalf("Error generating blocks: %s", err)
	}
	// Send internal & external blocks to the node
	err = obc.SendBlocksToNodes(blocks)
	if err != nil {
		log.Fatal("Failed to send all blocks to the node!")
	}
	return blocks
}

// Example test for a fork choice scenario shown in slide00 (not a real slide)
func TestForkChoice_Slide00(t *testing.T) {
	clients, err := getNodeClients()
	if err != nil {
		log.Fatal("Error connecting to nodes!")
	}
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"final_zone31", "z11_4", "z11_4"}, "final_prime"},
				&types.BlockGenSpec{[3]string{"", "", "final_prime"}, "final_zone11"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "z11_4", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "", "z11_11"}, "z11_12"},
				&types.BlockGenSpec{[3]string{"", "z11_4", "z11_12"}, "z11_13"},
			},
			nil, // Zone12 omitted
			nil, // Zone13 omitted
		},
		{
			nil, // Zone21 omitted
			nil, // Zone22 omitted
			nil, // Zone23 omitted
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z11_1", "gen", "gen"}, "final_zone31"},
			},
			nil, // Zone32 omitted
			nil, // Zone33 omitted
		},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	blocks := clients.GenAndRun(graph)
	time.Sleep(2 * time.Second) // Sleep a bit to allow nodes to reprocess future blocks

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	clients.CheckPassFail(blocks)
}
