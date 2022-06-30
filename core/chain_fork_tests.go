package core

import (
	"errors"
	"math/big"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/core/types"
)

// Generate blocks to form a network of chains
func SendNetworkBlocksToNode(graph [3][3][]*blockGenSpec, blocks []*types.Block) error {
	return errors.New("Not implemented")
}

func GetNodeHeadAndTD() (common.Hash, [3]*big.Int, error) {
	return common.Hash{}, [3]*big.Int{}, errors.New("Not implemented")
}

// Example test for a fork choice scenario shown in slide00 (not a real slide)
func ForkChoiceTest_Slide00() {
	// Define the network graph here
	graph := [3][3][]*blockGenSpec{
		{ // Region0
			{ // Zone0
				&blockGenSpec{[3]int{1, 1, 1}, [3]string{}, "z00_1"},
				&blockGenSpec{[3]int{-1, -1, 2}, [3]string{}, ""},
				// ...
				&blockGenSpec{[3]int{-1, 2, 4}, [3]string{}, "z00_4"},
				&blockGenSpec{[3]int{3, 3, 5}, [3]string{}, ""},
				&blockGenSpec{[3]int{-1, -1, 6}, [3]string{}, ""},                // END OF CANONICAL CHAIN
				&blockGenSpec{[3]int{-1, -1, 5}, [3]string{"", "", "z00_4"}, ""}, // Fork at z00_4
				// ...
				&blockGenSpec{[3]int{-1, 3, 8}, [3]string{}, ""},
				&blockGenSpec{[3]int{-1, -1, 5}, [3]string{"", "", "z00_4"}, ""}, // Fork at z00_4
				&blockGenSpec{[3]int{-1, -1, 6}, [3]string{}, ""},
				&blockGenSpec{[3]int{-1, -1, 7}, [3]string{"", "z00_1", ""}, ""}, // Twist to z00_1
			},
			{}, // ... Zone1 omitted
			{}, // ... Zone2 omitted
		},
		{ // Region1
			{ // Zone0
				&blockGenSpec{[3]int{1, 1, 2}, [3]string{"", "", "z00_1"}, ""},
			},
			{}, // ... Zone1 omitted
			{}, // ... Zone2 omitted
		},
		{}, // ... Region2 omitted
	}

	// Generate the blocks for this graph
	blocks, err := GenerateNetworkBlocks(graph)
	if err != nil {
		panic("Error generating blocks!")
	}

	// Send internal & external blocks to the node
	err = SendNetworkBlocksToNode(graph, blocks)
	if err != nil {
		panic("Failed to send all blocks to the node!")
	}

	// Confirm the node arrived at the expected head, with the expected total difficulty
	expectedHead := common.Hash{}
	expectedTD := [3]*big.Int{big.NewInt(0), big.NewInt(0), big.NewInt(0)}
	head, td, err := GetNodeHeadAndTD()
	if err != nil {
		panic("Error getting node head & total difficulty!")
	} else if head != expectedHead {
		panic("Node is on the wrong head!")
	} else if td != expectedTD {
		panic("Node has the wrong total difficulty!")
	}
}
