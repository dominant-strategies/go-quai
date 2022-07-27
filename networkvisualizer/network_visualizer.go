//Networkvisualizer is a testing tool that generates a graph of the network
//AssembleGraph is the main runner of the program and takes three parameters
//Start/End(int) specifies the range of blocks you would like to include in the graph, leaving both values 0 will default to the 100 most recent blocks
//The parameters for the program can be modified on the line that calls AssembleGraph within main
//The program can be run with the command: 							go run network_visualizer.go
//The generated DOT file can be viewed with a VSCode extension:		tintinweb.graphviz-interactive-preview
//Aternatively the DOT file can be converted into other image formats using a dot command
//A full node must be running for the tool to work properly
package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/ethclient"
	"github.com/spruce-solutions/go-quai/rlp"
	"golang.org/x/crypto/sha3"
	"gopkg.in/urfave/cli.v1"
)

var (
	prime, _        = ethclient.Dial("ws://127.0.0.1:8547")
	region1, _      = ethclient.Dial("ws://127.0.0.1:8579")
	region2, _      = ethclient.Dial("ws://127.0.0.1:8581")
	region3, _      = ethclient.Dial("ws://127.0.0.1:8583")
	zone11, _       = ethclient.Dial("ws://127.0.0.1:8611")
	zone12, _       = ethclient.Dial("ws://127.0.0.1:8643")
	zone13, _       = ethclient.Dial("ws://127.0.0.1:8675")
	zone21, _       = ethclient.Dial("ws://127.0.0.1:8613")
	zone22, _       = ethclient.Dial("ws://127.0.0.1:8645")
	zone23, _       = ethclient.Dial("ws://127.0.0.1:8677")
	zone31, _       = ethclient.Dial("ws://127.0.0.1:8615")
	zone32, _       = ethclient.Dial("ws://127.0.0.1:8647")
	zone33, _       = ethclient.Dial("ws://127.0.0.1:8679")
	primeSubGraph   = "subgraph cluster_Prime { label = \"Prime\" node [color = red]"
	region1SubGraph = "subgraph cluster_Region1 { label = \"Region1\" node [color = green]"
	region2SubGraph = "subgraph cluster_Region2 { label = \"Region2\" node [color = dodgerblue]"
	region3SubGraph = "subgraph cluster_Region3 { label = \"Region3\" node [color = orange]"
	zone11SubGraph  = "subgraph cluster_Zone11 { label = \"Zone11\" node [color = lawngreen]"
	zone12SubGraph  = "subgraph cluster_Zone12 { label = \"Zone12\" node [color = limegreen]"
	zone13SubGraph  = "subgraph cluster_Zone13 { label = \"Zone13\" node [color = mediumspringgreen]"
	zone21SubGraph  = "subgraph cluster_Zone21 { label = \"Zone21\" node [color = aqua]"
	zone22SubGraph  = "subgraph cluster_Zone22 { label = \"Zone22\" node [color = blue]"
	zone23SubGraph  = "subgraph cluster_Zone23 { label = \"Zone23\" node [color = \"#8a4cee\"]"
	zone31SubGraph  = "subgraph cluster_Zone31 { label = \"Zone31\" node [color = darkorange1]"
	zone32SubGraph  = "subgraph cluster_Zone32 { label = \"Zone32\" node [color = orangered2]"
	zone33SubGraph  = "subgraph cluster_Zone33 { label = \"Zone33\" node [color = \"#c55200\"]"
	uncleSubGraph   = []string{"subgraph cluster_Uncles { label = \"Uncles\""}
	//Length of the hash that each node will show
	hashLength = 10
	compressed bool
	start      int
	end        int
	f          *os.File
	//Destination for Flag arguments
	StartFlag      int
	RangeFlag      int
	CompressedFlag = true
	LiveFlag       = false
	UnclesFlag     = false
	SaveFileFlag   = "TestGraph.dot"
	//Inclusion flag to be implemented
	//inclusionFlag =
)

type Chain struct {
	client    *ethclient.Client //Used for retrieving the Block information from the DB
	subGraph  string            //Used to store initial subgraph formatting for respective chain
	nodes     []node            //Contains the nodes of each chain
	edges     []string          //Contains the edges for each chain
	order     int               //Stores the order of the chain being dealt with
	subChains []Chain           //Contains each subordinate chain
	startLoc  int
	endLoc    int
}

type node struct {
	nodehash string
	number   *big.Int
}

func main() {
	f, _ = os.Create("TestGraph.dot")
	defer f.Close()
	//Initializing all the chains to be used in the graph for each Region/Zone/Prime
	zone11Chain := Chain{zone11, zone11SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	zone12Chain := Chain{zone12, zone12SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	zone13Chain := Chain{zone13, zone13SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	region1Chain := Chain{region1, region1SubGraph, []node{}, []string{}, 1, []Chain{zone11Chain, zone12Chain, zone13Chain}, 0, 0}
	zone21Chain := Chain{zone21, zone21SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	zone22Chain := Chain{zone22, zone22SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	zone23Chain := Chain{zone23, zone23SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	region2Chain := Chain{region2, region2SubGraph, []node{}, []string{}, 1, []Chain{zone21Chain, zone22Chain, zone23Chain}, 0, 0}
	zone31Chain := Chain{zone31, zone31SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	zone32Chain := Chain{zone32, zone32SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	zone33Chain := Chain{zone33, zone33SubGraph, []node{}, []string{}, 2, []Chain{}, 0, 0}
	region3Chain := Chain{region3, region3SubGraph, []node{}, []string{}, 1, []Chain{zone31Chain, zone32Chain, zone33Chain}, 0, 0}
	primeChain := Chain{prime, primeSubGraph, []node{}, []string{}, 0, []Chain{region1Chain, region2Chain, region3Chain}, 0, 0}
	chains := make([]Chain, 0)
	chains = append(chains, primeChain, region1Chain, region2Chain, region3Chain, zone11Chain, zone12Chain, zone13Chain, zone21Chain, zone22Chain, zone23Chain, zone31Chain, zone32Chain, zone33Chain)

	app := cli.NewApp()
	app.Name = "visualizenetwork"
	app.Usage = "Generates graphs of Quai Network"
	app.Action = func(c *cli.Context) error {
		return nil
	}
	//Slice of flags used by CLI, connnect the Destination value to respective flag variable
	app.Flags = []cli.Flag{
		cli.IntFlag{
			Name:        "start",
			Value:       0,
			Usage:       "Determines the start block for the graph in terms of block number",
			Destination: &StartFlag,
		},
		cli.IntFlag{
			Name:        "range",
			Value:       100,
			Usage:       "Sets how many blocks to include in the graph(default = 100)",
			Destination: &RangeFlag,
		},
		cli.BoolTFlag{
			Name:        "compressed",
			Usage:       "Hides blocks inbetween coincident blocks, aside from those within the specified range(default = true)",
			Destination: &CompressedFlag,
		},
		cli.BoolFlag{
			Name:        "live",
			Usage:       "Allows for the graph to update real-time(default = false)",
			Destination: &LiveFlag,
		},
		cli.BoolFlag{
			Name:        "uncles",
			Usage:       "Includes uncle blocks in the live version of the graph, only works if live is true(default = false)",
			Destination: &UnclesFlag,
		},
		cli.StringFlag{
			Name:        "savefile",
			Value:       "TestGraph.dot",
			Usage:       "Allows for specification of output file for the graph(default = \"TestGraph.dot\")",
			Destination: &SaveFileFlag,
		},
	}
	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
	//Opening IO file to write to, WiP for flag options to specify file
	f, err = os.Create(SaveFileFlag)
	if err != nil {
		panic(err)
	}
	if start == 0 && end == 0 {
		for i := range chains {
			blockNum, _ := chains[i].client.BlockNumber(context.Background())
			chains[i].startLoc = int(blockNum) - 100
			if chains[i].startLoc < 1 {
				chains[i].startLoc = 1
			}
			chains[i].endLoc = int(blockNum)
		}
	} else {
		for i := range chains {
			blockNum, _ := chains[i].client.BlockNumber(context.Background())
			chains[i].startLoc = start
			chains[i].endLoc = end
			if chains[i].endLoc > int(blockNum) {
				chains[i].endLoc = int(blockNum)
			}
		}
	}

	AssembleGraph(chains)
}

// Hash returns the block hash of the header, which is simply the keccak256 hash of its RLP encoding.
func rlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewLegacyKeccak256()
	rlp.Encode(hw, x)
	hw.Sum(h[:0])
	return h
}

//Main runner of the tool AssembleGraph takes a chain with initialized ethclients and fills out the other fiels as well as generates the DOT file
//Start and End indicates the block range you want in the graph
//If start and end are left 0 AssembleGraph will default to the 100 most recent nodes
func AssembleGraph(chains []Chain) {
	//Writing the outline for the graph
	for i := 0; i < len(chains); i++ {
		chain := chains[i].client
		//subGraph := chains[i].subGraph
		order := chains[i].order

		//Iterates through the blocks in the chain
		for j := chains[i].startLoc; j <= chains[i].endLoc; j++ {
			blockHeader, err := chain.HeaderByNumber(context.Background(), big.NewInt(int64(j)))
			if err != nil {
				panic(err)
			}
			blockHash := rlpHash(blockHeader)
			if order == 0 {
				AddCoincident(chains, blockHash)
			} else if order == 1 {
				AddCoincident(chains, blockHash)
			}
			chains[i].AddNode(blockHash, j, blockHeader)

			if j != chains[i].startLoc {

				parentHeader, _ := chain.HeaderByHash(context.Background(), blockHeader.ParentHash[order])
				parentHash := rlpHash(parentHeader).String()[2 : hashLength+2]
				bHash := blockHash.String()[2 : hashLength+2]
				chains[i].AddEdge(true, fmt.Sprintf("%d", chains[i].order)+parentHash, fmt.Sprintf("%d", chains[i].order)+bHash, "")

			}
		}
	}
	chains = OrderChains(chains)
	writeToDOT(chains)
}

//AddCoincident Goes through Region and Prime chains and connects all blocks found in the network with the same hash
func AddCoincident(chains []Chain, hash common.Hash) {
	uncle := true
	for i := 0; i < len(chains); i++ {
		header, err := chains[i].client.HeaderByHash(context.Background(), hash)
		if err == nil {
			chains[i].AddNode(hash, 0, header)
			if int(header.Number[chains[i].order].Int64()) > chains[i].endLoc && !compressed {
				chains[i].endLoc = int(header.Number[chains[i].order].Int64())
			}
			if int(header.Number[chains[i].order].Int64()) < chains[i].startLoc && !compressed {
				chains[i].startLoc = int(header.Number[chains[i].order].Int64())
			}
			if chains[i].order < 2 {
				chains[i].AddEdge(false, fmt.Sprintf("%d", chains[i].order)+hash.String()[2:hashLength+2], fmt.Sprintf("%d", chains[i].order+1)+hash.String()[2:hashLength+2], "")
				AddCoincident(chains[i].subChains, hash)
			}
			uncle = false
		}
		if uncle && i == len(chains)-1 {
			AddUncle(hash, chains[0].order)
		}
	}
}

//Adds a Node to the chain if it doesn't already exist.
func (c *Chain) AddNode(hash common.Hash, num int, header *types.Header) {
	if !ContainsNode("\n\""+fmt.Sprint(c.order)+hash.String()[2:hashLength+2]+"\" [label = \""+hash.String()[2:hashLength+2]+"\\n "+header.Number[c.order].String()+"\"]", c.nodes) {
		tempNode := node{}
		if num == 0 && c.order == 0 {
			tempNode = node{"\n\"" + fmt.Sprint(c.order) + hash.String()[2:hashLength+2] + "\" [label = \"" + hash.String()[2:hashLength+2] + "\\n [" + header.Number[0].String() + "," + header.Number[1].String() + "," + header.Number[2].String() + "]\"]", header.Number[c.order]}
			c.nodes = append(c.nodes, tempNode)
		} else if num == 0 {
			tempNode = node{"\n\"" + fmt.Sprint(c.order) + hash.String()[2:hashLength+2] + "\" [label = \"" + hash.String()[2:hashLength+2] + "\\n " + header.Number[c.order].String() + "\"]", header.Number[c.order]}
			c.nodes = append(c.nodes, tempNode)
		} else {
			tempNode = node{"\n\"" + fmt.Sprint(c.order) + hash.String()[2:hashLength+2] + "\" [label = \"" + hash.String()[2:hashLength+2] + "\\n " + fmt.Sprint(num) + "\"]", big.NewInt(int64(num))}
			c.nodes = append(c.nodes, tempNode)
		}
	}
}

func AddUncle(hash common.Hash, order int) {
	uncleSubGraph = append(uncleSubGraph, "\n\""+fmt.Sprint(order)+hash.String()[2:hashLength+2]+"\" [label = \""+hash.String()[2:hashLength+2]+"\"]")
}

//Adds an edge to the chain FROM string1 TO string2. The bool parameter will take away the direction of the edge if it is false.
func (c *Chain) AddEdge(dir bool, node1 string, node2 string, color string) {
	if dir {
		if !Contains("\n\""+node1+"\" -> \""+node2+"\"", c.edges) {
			if color != "" {
				c.edges = append(c.edges, "\n\""+node1+"\" -> \""+node2+"\" [color = \""+color+"\"]")
			} else {
				c.edges = append(c.edges, "\n\""+node1+"\" -> \""+node2+"\"")
			}
		}
	} else {
		if !Contains("\n\""+node1+"\" -> \""+node2+"\" [dir = none]", c.edges) {
			c.edges = append(c.edges, "\n\""+node1+"\" -> \""+node2+"\" [dir = none]")
		}
	}
}

//Checks to see if a node already exists
func ContainsNode(s string, list []node) bool {
	for _, a := range list {
		modHash := a.nodehash
		if strings.Contains(modHash, s) {
			return true
		}
	}
	return false
}

//Checks to see if the list of strings contains the string passed as the first parameter. Used to check if a Node already exists in the the list.
func Contains(s string, list []string) bool {
	for _, a := range list {
		if strings.Contains(a, s) {
			return true
		}
	}
	return false
}

func OrderChains(chains []Chain) []Chain {
	//Insertion sorting the chains in order for next steps to be executed properly
	for i := 0; i < len(chains); i++ {
		for j := 1; j < len(chains[i].nodes); j++ {
			for k := j; k >= 1 && int(chains[i].nodes[k].number.Int64()) < int(chains[i].nodes[k-1].number.Int64()); k-- {
				chains[i].nodes[k], chains[i].nodes[k-1] = chains[i].nodes[k-1], chains[i].nodes[k]
			}
		}
	}
	for i := 0; i < len(chains); i++ {
		for j := 0; j < len(chains[i].nodes)-1; j++ {
			if i != 0 {
				chains[i].AddEdge(true, chains[i].nodes[j].nodehash[2:hashLength+3], chains[i].nodes[j+1].nodehash[2:hashLength+3], "blue")
			}
		}
	}
	return chains
}

//Function for writing a DOT file that generates the graph
func writeToDOT(chains []Chain) {
	f.WriteString("digraph G {\nfontname=\"Helvetica,Arial,sans-serif\"\nnode [fontname=\"Helvetica,Arial,sans-serif\", shape = rectangle, style = filled] \nedge [fontname=\"Helvetica,Arial,sans-serif\"]")
	for _, n := range chains {
		f.WriteString(n.subGraph)
		for _, s := range n.nodes {
			f.WriteString(s.nodehash)
		}
		f.WriteString("}\n")

	}
	for _, n := range uncleSubGraph {
		f.WriteString(n)
	}
	f.WriteString("}\n")
	for _, n := range chains {
		for _, s := range n.edges {
			f.WriteString(s)
		}
	}
	f.WriteString("\n}")
}
