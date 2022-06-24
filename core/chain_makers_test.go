// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"fmt"
	"math"
	"math/big"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus/blake3"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/crypto"
	"github.com/spruce-solutions/go-quai/params"
)

/*
func ExampleGenerateChain() {
	var (
		key1, _  = crypto.HexToECDSA("e5406fa9618589dbebc2ff870ab671290e194b0512ec9b85be47287bb59d83dd")
		key2, _  = crypto.HexToECDSA("36050ddb1cee3a529c0859c15c48e19835629a79ff91520a4299bc232a132ce5")
		key3, _  = crypto.HexToECDSA("7f677908d2305884aa3b4b909c32e4752c6ec30c6f68eb240c7366c652dda351")
		addr1    = crypto.PubkeyToAddress(key1.PublicKey)
		addr2    = crypto.PubkeyToAddress(key2.PublicKey)
		addr3    = crypto.PubkeyToAddress(key3.PublicKey)
		db       = rawdb.NewMemoryDatabase()
		gasPrice = big.NewInt(1)
	)

	// Ensure that key1 has some funds in the genesis block.
	// genesisHashes := []common.Hash{params.RopstenPrimeGenesisHash, params.RopstenRegionGenesisHash, params.RopstenZoneGenesisHash}
	gspec := &Genesis{ // params.TestChainConfig config parameters
		Config: &params.ChainConfig{big.NewInt(1337), 0, []byte{0, 0}, []int{3, 3, 3}, big.NewInt(0), big.NewInt(0), common.Hash{}, big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), nil, new(params.blake3Config), nil, nil},
		Alloc:  GenesisAlloc{addr1: {Balance: big.NewInt(1000000)}},
	}
	genesis := gspec.MustCommit(db)

	// This call generates a chain of 5 blocks. The function runs for
	// each block and adds different features to gen based on the
	// block index.
	signer := types.HomesteadSigner{}
	chain, _ := GenerateChain(gspec.Config, genesis, blake3.NewFaker(), db, 5, func(i int, gen *BlockGen) {
		switch i {
		case 0:
			// In block 1, addr1 sends addr2 some ether.
			tx, _ := types.SignTx(types.NewTransaction(gen.TxNonce(addr1), addr2, big.NewInt(100000), params.TxGas, gasPrice, nil), signer, key1)
			gen.AddTx(tx)
		case 1:
			// In block 2, addr1 sends some more ether to addr2.
			// addr2 passes it on to addr3.
			tx1, _ := types.SignTx(types.NewTransaction(gen.TxNonce(addr1), addr2, big.NewInt(10000), params.TxGas, gasPrice, nil), signer, key1)
			tx2, _ := types.SignTx(types.NewTransaction(gen.TxNonce(addr2), addr3, big.NewInt(1000), params.TxGas, gasPrice, nil), signer, key2)
			gen.AddTx(tx1)
			gen.AddTx(tx2)
		case 2:
			// Block 3 is empty but was mined by addr3.
			gen.SetCoinbase(addr3)
			gen.SetExtra([]byte("yeehaw"))
		case 3:
			// Block 4 includes blocks 2 and 3 as uncle headers (with modified extra data).
			b2 := gen.PrevBlock(1).Header()
			b2.Extra = [][]byte{[]byte("foo"), []byte("foo"), []byte("foo")}
			gen.AddUncle(b2)
			b3 := gen.PrevBlock(2).Header()
			b3.Extra = [][]byte{[]byte("foo"), []byte("foo"), []byte("foo")}
			gen.AddUncle(b3)
		}
	})

	// Import the chain. This runs all block validation rules.
	blockchain, _ := NewBlockChain(db, nil, gspec.Config, blake3.NewFaker(), vm.Config{}, nil, nil)
	defer blockchain.Stop()

	if _, err := blockchain.InsertChain(chain); err != nil {
		// fmt.Printf("insert error (block %d): %v\n", chain[i].NumberU64(), err)
		// return
	}

	state, _ := blockchain.State()
	fmt.Printf("last block: #%d\n", blockchain.CurrentBlock().Number())
	fmt.Println("balance of addr1:", state.GetBalance(addr1))
	fmt.Println("balance of addr2:", state.GetBalance(addr2))
	fmt.Println("balance of addr3:", state.GetBalance(addr3))
	// Output:
	// last block: #5
	// balance of addr1: 989000
	// balance of addr2: 10000
	// balance of addr3: 19687500000000001000
} */

// Runner function
func chainsValidator(chain []*types.Block, primeChain BlockChain, regionChain BlockChain, zoneChain BlockChain, specsPool []blockSpecs) (validNetwork [3]BlockChain) {
	// pass blocks in through all possible methods to verify similar handling
	// e.g. rpc

	// handle permutation *outside* this function
	// will need to hit this function twice - first to create blocks (with forks), second to test fork scenarios

	if _, err := primeChain.InsertChain(chain); err != nil {
		print(err)
	}

	validNetwork = [3]BlockChain{primeChain, regionChain, zoneChain}

	return validNetwork
}

// struct for notation
type blockConstructor struct {
	numbers [3]int // prime idx, region idx, zone idx
	// -1 for contexts the block does not coincide with
	fork          int       // specify what fork to construct block in (0 must be first chain grinded)
	parentFork    int       // select what fork to point to (if same as fork int then will be same)
	parentNumbers [3]int    // necessary to select correct block to fork from
	parentTags    [3]string // (optionally) Override the parents to point to tagged blocks. Empty strings are ignored.
	tag           string    // (optionally) Give this block a named tag. Empty strings are ignored.
}

// Constant Genesis definition in notation
var genesisBlock = blockConstructor{[3]int{0, 0, 0}, 0, 0, [3]int{0, 0, 0}, [3]string{}, ""}

type blockSpecs struct {
	order         int    // order to grind in
	number        [3]int // Number of block to be created
	parentNumbers [3]int
	fork          int
	parentFork    int
	hash          common.Hash
	slice         params.ChainConfig // chain config (used to infer slice)
	parentTags    [3]string
	tag           string
}

// simple example graph
// [3][3][100]*blockConstructor = 3 regions, each w/ 3 zones, each w/ 100 blocks
var networkGraphSample = [3][3][]*blockConstructor{
	{ // Region1
		{ // Zone1
			&genesisBlock,
			&blockConstructor{[3]int{-1, 1, 1}, 0, 0, [3]int{0, 0, 0}, [3]string{}, ""},
			&blockConstructor{[3]int{-1, -1, 2}, 0, 0, [3]int{0, 1, 1}, [3]string{}, ""},
			&blockConstructor{[3]int{-1, -1, 3}, 0, 0, [3]int{0, 1, 2}, [3]string{}, ""},
			&blockConstructor{[3]int{-1, 2, 4}, 0, 0, [3]int{0, 1, 3}, [3]string{}, ""},
			&blockConstructor{[3]int{1, 3, 5}, 0, 0, [3]int{0, 2, 4}, [3]string{}, ""},
		},
	},
}

func BlockInterpreter(networkGraph [3][3][]*blockConstructor) []blockSpecs {
	// an array of blockSpecs will be constructed to be looped over GenerateNetwork
	// this will generate the blocks in sequential order (the chains will then be validated separately again)
	// Prime blocks then Region blocks and finally Zone blocks last

	// loop to find number of forks; result used to derive correct block numbers
	totalForks := 0
	for _, regions := range networkGraph {
		for _, zones := range regions {
			for _, construct := range zones {
				if construct.fork >= totalForks {
					totalForks = construct.fork + 1
				}
			}
		}
	}

	// initialize array to derive respective number values in forks
	forkedNumbers := [][3]int{}
	n := 0
	for n <= totalForks {
		forkedNumbers = append(forkedNumbers, [3]int{0, 0, 0})
		n++
	}

	specs := []blockSpecs{}
	primeConfig := params.MainnetPrimeChainConfig
	for r, regions := range networkGraph {
		regionConfig := params.MainnetRegionChainConfigs[r]
		for z, zones := range regions {
			zoneConfig := params.MainnetZoneChainConfigs[r][z]
			for _, block := range zones {
				spec := blockSpecs{}
				if block.numbers[0] != -1 {
					spec.order = 0
					if block == &genesisBlock { // genesis is special case
						spec.number = block.numbers
					} else {
						spec.number[0] = block.parentNumbers[0] + 1
						spec.number[1] = block.parentNumbers[1] + 1
						spec.number[2] = block.parentNumbers[2] + 1
					}
					spec.parentNumbers = block.parentNumbers
					spec.fork = block.fork
					spec.parentFork = block.parentFork
					spec.slice = *primeConfig
					forkedNumbers[block.fork] = spec.number
				} else {
					if block.numbers[1] != -1 {
						spec.order = 1
						spec.number[0] = block.parentNumbers[0]
						spec.number[1] = block.parentNumbers[1] + 1
						spec.number[2] = block.parentNumbers[2] + 1
						spec.parentNumbers = block.parentNumbers
						spec.fork = block.fork
						spec.parentFork = block.parentFork
						spec.slice = regionConfig
						forkedNumbers[block.fork] = spec.number
					} else {
						spec.order = 2
						spec.number[0] = block.parentNumbers[0]
						spec.number[1] = block.parentNumbers[1]
						spec.number[2] = block.parentNumbers[2] + 1
						spec.parentNumbers = block.parentNumbers
						spec.fork = block.fork
						spec.parentFork = block.parentFork
						spec.slice = zoneConfig
						forkedNumbers[block.fork] = spec.number
					}
				}
				specs = append(specs, spec)
			}
		}
	}

	// organize specs by fork id
	forkArrays := make([][]blockSpecs, totalForks)
	for _, spec := range specs {
		forkArrays[spec.fork] = append(forkArrays[spec.fork], spec)
	}

	// will need to order chains consecutively for proper block generation
	sequentialSpecs := []blockSpecs{} // once ordered put specs in this array
	// sequence within fork chains
	for _, forkArray := range forkArrays {
		parent := findParent(forkArray)
		for len(forkArray) > 0 { // beware infinite loop!
			// find next in sequence then append to sequentialSpecs
			for i, spec := range forkArray {
				// TO DO
				// needs substantial revision of logic
				// take parentNumber and cf. order to find next in sequence
				// loop over zones then regions
				// fast removal of element
				forkArray[i] = forkArray[len(forkArray)-1]
				forkArray = forkArray[:len(forkArray)-1]
				break
			}
		}
	}

	return sequentialSpecs
}

// returns the first block number that can be used to build a fork chain
func findParent(specs []blockSpecs) (parentNumbers [3]int) {
	nPrime, nRegion, nZone := math.MaxInt, math.MaxInt, math.MaxInt
	for _, spec := range specs {
		if spec.number[0] <= nPrime {
			nPrime = spec.number[0]
			if spec.number[1] <= nRegion {
				nRegion = spec.number[1]
				if spec.number[2] <= nZone {
					nZone = spec.number[2]
					parentNumbers = spec.parentNumbers
				}
			}
		}

	}
	return parentNumbers
}

// ExampleGenerateNetwork follows the logic of ExampleGenerateChain but
// with additional parameters to specify intended context of blocks.
// This makes it possible to test interchain linkages, external transactions,
// and more.
func ExampleGenerateNetwork() {
	// keys might need to be changed to conform to Guarded Address Space standards
	var (
		key1, _ = crypto.HexToECDSA("e5406fa9618589dbebc2ff870ab671290e194b0512ec9b85be47287bb59d83dd")
		// key2, _  = crypto.HexToECDSA("36050ddb1cee3a529c0859c15c48e19835629a79ff91520a4299bc232a132ce5")
		// key3, _  = crypto.HexToECDSA("7f677908d2305884aa3b4b909c32e4752c6ec30c6f68eb240c7366c652dda351")
		addr1 = crypto.PubkeyToAddress(key1.PublicKey)
		// addr2    = crypto.PubkeyToAddress(key2.PublicKey)
		// addr3    = crypto.PubkeyToAddress(key3.PublicKey)
		db = rawdb.NewMemoryDatabase() // first db necessary to grind blocks with forks
		// second rawdb.NewMemoryDatabase object to decide fork scenarios
	)

	// Ensure that key1 has some funds in the genesis block.
	gspecPrime := &Genesis{
		Config: params.MainnetPrimeChainConfig,
		Alloc:  GenesisAlloc{addr1: {Balance: big.NewInt(1000000)}},
	}

	// start a database object and commit genesis blocks to it
	primeConfig, genesis, err := SetupGenesisBlock(db, gspecPrime)
	if err != nil {
		fmt.Println(err)
	}

	// load Region and Zone configs
	regionConfig := params.MainnetRegionChainConfigs[0]
	zoneConfig := params.MainnetZoneChainConfigs[0][0]

	// create Geneis blocks in respective chains
	genesisPrime := gspecPrime.MustCommit(db)

	// establish desired contexts for generated blocks
	// orders must descend i.e. a Prime block must come before any Region blocks, and a Region block must come before any Zone blocks
	specsPool := BlockInterpreter(networkGraphSample)

	// Import the chain. This runs all block validation rules.
	blockchainPrime, _ := NewBlockChain(db, defaultCacheConfig, primeConfig, blake3.NewFaker(), vm.Config{}, nil, nil)
	defer blockchainPrime.Stop()
	blockchainRegion, _ := NewBlockChain(db, defaultCacheConfig, &regionConfig, blake3.NewFaker(), vm.Config{}, nil, nil)
	defer blockchainRegion.Stop()
	blockchainZone, _ := NewBlockChain(db, defaultCacheConfig, &zoneConfig, blake3.NewFaker(), vm.Config{}, nil, nil)
	defer blockchainZone.Stop()

	if genesis != blockchainPrime.genesisBlock.Hash() {
		fmt.Println("wrong genesis")
	}

	// temp
	parent := genesisPrime

	// genesis handling - should only trigger once, necessary to generate genesis block first and only once
	var genesisCheck bool = false
	if specsPool[0].number == [3]int{0, 0, 0} {
		genesisCheck = true
	}

	// Generator section
	// loop over GenerateNetwork
	blockPool := []*types.Block{}
	for _, specs := range specsPool {
		// function here to derive appropriate parent post-genesis cases

		block := GenerateBlock(genesisCheck, &specs.slice,
			parent, specs.order, specs.number,
			blake3.NewFaker(), db)
		if genesisCheck {
			genesisCheck = false
		}
		specs.hash = block.Hash()
		// mini-Runner section (must grind blocks in order to derive parents for forks)
		blockchainPrime.InsertChain(types.Blocks{block})
		blockPool = append(blockPool, block)
	}

	// loop over runner section
	// Runner section
	validNetwork := chainsValidator(blockPool, *blockchainPrime, *blockchainRegion, *blockchainZone, specsPool)
	blockchainPrime = &validNetwork[0]
	blockchainRegion = &validNetwork[1]
	blockchainZone = &validNetwork[2]

	statePrime, _ := blockchainPrime.State()
	stateRegion, _ := blockchainRegion.State()
	stateZone, _ := blockchainZone.State()

	fmt.Println("balance of addr1 in Prime:", statePrime.GetBalance(addr1))
	fmt.Println("balance of addr1 in Region 1:", stateRegion.GetBalance(addr1))
	fmt.Println("balance of addr1 in Zone 1-1:", stateZone.GetBalance(addr1))
	// Output:
	// Current Header Number [0 0 0] 0xc9bada59c70cb15feeab18e408a5e9b1938e7abdca9b0bed1193b52d9b6edc2e
	// Current Header Number [0 0 0] 0xc9bada59c70cb15feeab18e408a5e9b1938e7abdca9b0bed1193b52d9b6edc2e
	// Current Header Number [0 0 0] 0xc9bada59c70cb15feeab18e408a5e9b1938e7abdca9b0bed1193b52d9b6edc2e
	// balance of addr1 in Prime: 1000000
	// balance of addr1 in Region 1: 1000000
	// balance of addr1 in Zone 1-1: 1000000
}
