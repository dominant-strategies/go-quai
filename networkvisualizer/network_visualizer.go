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
	"math/big"
	"os"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/ethclient"
	"github.com/spruce-solutions/go-quai/rlp"
	"golang.org/x/crypto/sha3"
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
)

type Chain struct {
	client    *ethclient.Client //Used for retrieving the Block information from the DB
	subGraph  string            //Used to store initial subgraph formatting for respective chain
	nodes     []node            //Contains the nodes of each chain
	edges     []string          //Contains the edges for each chain
	order     int               //Stores the order of the chain being dealt with
	subChains []Chain           //Contains each subordinate chain
}

type node struct {
	nodehash string
	number   *big.Int
}

func main() {
	//Initializing all the chains to be used in the graph for each Region/Zone/Prime
	zone11Chain := Chain{zone11, zone11SubGraph, []node{}, []string{}, 2, []Chain{}}
	zone12Chain := Chain{zone12, zone12SubGraph, []node{}, []string{}, 2, []Chain{}}
	zone13Chain := Chain{zone13, zone13SubGraph, []node{}, []string{}, 2, []Chain{}}
	region1Chain := Chain{region1, region1SubGraph, []node{}, []string{}, 1, []Chain{zone11Chain, zone12Chain, zone13Chain}}
	zone21Chain := Chain{zone21, zone21SubGraph, []node{}, []string{}, 2, []Chain{}}
	zone22Chain := Chain{zone22, zone22SubGraph, []node{}, []string{}, 2, []Chain{}}
	zone23Chain := Chain{zone23, zone23SubGraph, []node{}, []string{}, 2, []Chain{}}
	region2Chain := Chain{region2, region2SubGraph, []node{}, []string{}, 1, []Chain{zone21Chain, zone22Chain, zone23Chain}}
	zone31Chain := Chain{zone31, zone31SubGraph, []node{}, []string{}, 2, []Chain{}}
	zone32Chain := Chain{zone32, zone32SubGraph, []node{}, []string{}, 2, []Chain{}}
	zone33Chain := Chain{zone33, zone33SubGraph, []node{}, []string{}, 2, []Chain{}}
	region3Chain := Chain{region3, region3SubGraph, []node{}, []string{}, 1, []Chain{zone31Chain, zone32Chain, zone33Chain}}
	primeChain := Chain{prime, primeSubGraph, []node{}, []string{}, 0, []Chain{region1Chain, region2Chain, region3Chain}}
	chains := make([]Chain, 0)
	chains = append(chains, primeChain, region1Chain, region2Chain, region3Chain, zone11Chain, zone12Chain, zone13Chain, zone21Chain, zone22Chain, zone23Chain, zone31Chain, zone32Chain, zone33Chain)
	//Parameters for the program can be modified here.
	AssembleGraph(0, 0, chains)
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
func AssembleGraph(start int, end int, chains []Chain) {
	f, _ := os.Create("TestGraph.dot")
	defer f.Close()

	//Writing the outline for the graph
	f.WriteString("digraph G {\nfontname=\"Helvetica,Arial,sans-serif\"\nnode [fontname=\"Helvetica,Arial,sans-serif\", shape = rectangle, style = filled] \nedge [fontname=\"Helvetica,Arial,sans-serif\"]")

	for i := 0; i < len(chains); i++ {
		chain := chains[i].client
		//subGraph := chains[i].subGraph
		order := chains[i].order
		//Fetches the number of blocks in the respective chain
		numBlocks, _ := chain.BlockNumber(context.Background())

		//Sets the default range if start and end parameters are 0. Also checks to see if they are out of bounds
		if start == 0 && end == 0 {
			end = int(numBlocks)
			start = int(numBlocks) - 100
		}
		if end > int(numBlocks) {
			end = int(numBlocks)
		}
		if start < 0 {
			start = 1
		}

		//Iterates through the blocks in the chain
		for j := start; j <= end; j++ {
			blockHeader, err := chain.HeaderByNumber(context.Background(), big.NewInt(int64(j)))
			if err != nil {
				panic(err)
			}
			blockHash := rlpHash(blockHeader)
			if order == 0 || order == 1 {
				AddCoincident(chains, blockHash)
			}
			if j != start {
				parentHeader, _ := chain.HeaderByHash(context.Background(), blockHeader.ParentHash[order])
				parentHash := rlpHash(parentHeader).String()[2:7]
				bHash := blockHash.String()[2:7]
				chains[i].AddEdge(true, fmt.Sprintf("%d", chains[i].order)+parentHash, fmt.Sprintf("%d", chains[i].order)+bHash)

			}
			chains[i].AddNode(blockHash, j)

		}
		start = 0
		end = 0
	}
	writeToDOT(chains, f)
	f.WriteString("\n}")
}

//AddCoincident Goes through Region and Prime chains and connects all blocks found in the network with the same hash
func AddCoincident(chains []Chain, hash common.Hash) {
	uncle := true
	for i := 0; i < len(chains); i++ {
		_, err := chains[i].client.HeaderByHash(context.Background(), hash)
		if err == nil {
			chains[i].AddNode(hash, 0)
			if chains[i].order < 2 {
				chains[i].AddEdge(false, fmt.Sprintf("%d", chains[i].order)+hash.String()[2:7], fmt.Sprintf("%d", chains[i].order+1)+hash.String()[2:7])
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
func (c *Chain) AddNode(hash common.Hash, num int) {
	if !ContainsNode("\n\""+fmt.Sprint(c.order)+hash.String()[2:7]+"\" [label = \""+hash.String()[2:7]+"\"]", c.nodes) {
		tempNode := node{}
		if num == 0 {
			blockHeader, _ := c.client.HeaderByHash(context.Background(), hash)
			tempNode = node{"\n\"" + fmt.Sprint(c.order) + hash.String()[2:7] + "\" [label = \"" + hash.String()[2:7] + "\\n " + blockHeader.Number[c.order].String() + "\"]", blockHeader.Number[c.order]}
			c.nodes = append(c.nodes, tempNode)
		} else {
			tempNode = node{"\n\"" + fmt.Sprint(c.order) + hash.String()[2:7] + "\" [label = \"" + hash.String()[2:7] + "\\n " + fmt.Sprint(num) + "\"]", big.NewInt(int64(num))}
			c.nodes = append(c.nodes, tempNode)
		}
	}
}

func AddUncle(hash common.Hash, order int) {
	uncleSubGraph = append(uncleSubGraph, "\n\""+fmt.Sprint(order)+hash.String()[2:7]+"\" [label = \""+hash.String()[2:7]+"\"]")
}

//Adds an edge to the chain FROM string1 TO string2. The bool parameter will take away the direction of the edge if it is false.
func (c *Chain) AddEdge(dir bool, node1 string, node2 string) {
	if dir {
		if !Contains("\n\""+node1+"\" -> \""+node2+"\"", c.edges) {
			c.edges = append(c.edges, "\n\""+node1+"\" -> \""+node2+"\"")
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
		modHash := a.nodehash[:25] + "\"]"
		if modHash == s {
			return true
		}
	}
	return false
}

//Checks to see if the list of strings contains the string passed as the first parameter. Used to check if a Node already exists in the the list.
func Contains(s string, list []string) bool {
	for _, a := range list {
		if a == s {
			return true
		}
	}
	return false
}

//Function for writing a DOT file that generates the graph
func writeToDOT(chains []Chain, file *os.File) {
	for _, n := range chains {
		file.WriteString(n.subGraph)
		for _, s := range n.nodes {
			file.WriteString(s.nodehash)
		}
		file.WriteString("}\n")

	}
	for _, n := range uncleSubGraph {
		file.WriteString(n)
	}
	file.WriteString("}\n")
	for _, n := range chains {
		for _, s := range n.edges {
			file.WriteString(s)
		}
	}
}
