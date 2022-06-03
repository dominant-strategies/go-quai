package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spruce-solutions/go-quai/ethclient"
)

//GenGraph takes an array of blocks as a parameter and traces through them adding each block to the graph
func GenGraph() {

	//var fakeReader ChainHeaderReader

	//stack, cfg := makeConfigNode(bc.chainConfig.Context)
	//chainDb, err := stack.OpenDatabaseWithFreezer("chaindata", config.DatabaseCache, config.DatabaseHandles, config.DatabaseFreezer, "eth/db/chaindata/", false)

	PrimeURL := "ws://127.0.0.1:8547"
	primeClient, err := ethclient.Dial(PrimeURL)

	//Creating the dot file
	f, err := os.Create("TestGraph.dot")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	//Writing the outline for the graph
	// f.WriteString("digraph G {\nfontname="Helvetica,Arial,sans-serif"\nnode [fontname="Helvetica,Arial,sans-serif", shape = rectangle, style = filled] \nedge [fontname="Helvetica,Arial,sans-serif"]")

	//Creating the outline for the subgraphs in an array string for each context
	// primeSubGraph := []string{"subgraph cluster_Prime {", "label = "Prime""}
	// regionSubGraph := []string{"subgraph cluster_Region1 {", "label = "Region1""}
	// zoneSubGraph := []string{"subgraph cluster_Zone11 {", "label = "Zone11""}/

	primeBlock, err := primeClient.BlockNumber(context.Background())
	//var primeBlocks types.Blocks

	for i := primeBlock; i >= 0; i-- {
		Block, err := primeClient.BlockByNumber(context.Background, new.BigInt(i))
		if err != nil {

		}
		fmt.Println(Block)
	}
}

func main() {
	GenGraph()
}
