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
	"math/big"

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
func chainsValidator(chains [][]*types.Block, primeChain BlockChain, regionChain BlockChain, zoneChain BlockChain, ordersPool []chainOrders) (validNetwork [3]BlockChain) {
	// ordersPool = permutePoool(ordersPool)

	for i := range ordersPool {
		chain := chains[i]
		if _, err := primeChain.InsertChain(chain); err != nil {
			print(err)
		}
		// insert each block at their respective contexts
		/*for i, order := range orders.orders {
			switch order {
			case 0:
				if chain[i] != primeChain.genesisBlock {
					if _, err := primeChain.InsertChain(types.Blocks{chain[i]}); err != nil {
						print(err)
					}
				}
			case 1:
				if _, err := regionChain.InsertChain(types.Blocks{chain[i]}); err != nil {
					print(err)
				}
			case 2:
				if _, err := zoneChain.InsertChain(types.Blocks{chain[i]}); err != nil {
					print(err)
				}
			}
		}*/
	}

	validNetwork = [3]BlockChain{primeChain, regionChain, zoneChain}

	return validNetwork
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
		db = rawdb.NewMemoryDatabase()
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

	// ordersPool constructor - feed notation in here to construct
	// sets of orders that can be passed into the generator one at
	// a time and placed into a pool

	// IMPORTANT TO NOTE: sequence of blocks generated must be sequential,
	// i.e. you cannot generate a 75th block w/out first generating the 74th block
	// AFTER generation, ordersPool can be permuted in all possible ways and
	// passed into the Runner

	// establish desired contexts for generated blocks
	// orders must descend i.e. a Prime block must come before any Region blocks, and a Region block must come before any Zone blocks
	chainOrders1 := chainOrders{
		orders:      []int{0, 1, 2, 2, 1, 0}, // {0,1,2,2,1} represents Prime, Region, Zone, Zone, Region
		startNumber: []int{0, 0, 0},
	}
	ordersPool := []chainOrders{chainOrders1}

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

	// Generator section
	// loop over GenerateNetwork
	chains := [][]*types.Block{}
	for _, orders := range ordersPool {
		// parent := blockchainPrime.GetBlockByNumber(uint64(orders.parent[0]))
		chain, _ := GenerateNetwork(primeConfig, regionConfig, zoneConfig,
			parent, orders.orders, orders.startNumber,
			blake3.NewFaker(), db, func(i int, gen *BlockGen) {
				switch i { // preserve this section for testing transaction data
				/* case 0:

				case 1:

				case 2:

				case 3:

				case 4:
				*/
				}
			})
		chains = append(chains, chain)
	}

	// Runner section
	validNetwork := chainsValidator(chains, *blockchainPrime, *blockchainRegion, *blockchainZone, ordersPool)
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

// struct for interpreting notation/constructing chains
type blockConstructor struct {
	numbers [3]int // prime idx, region idx, zone idx
	// -1 for contexts the block does not coincide with
	parentTags [3]string // (optionally) Override the parents to point to tagged blocks. Empty strings are ignored.
	tag        string    // (optionally) Give this block a named tag. Empty strings are ignored.
}

// Constant Genesis definition
var genesisBlock = blockConstructor{[3]int{0, 0, 0}, [3]string{}, ""}

type chainOrders struct {
	orders      []int // sequence of orders to grind
	startNumber []int // Number to start at (also used to derive parent)
	parent      []int //
	parentTags  [3]string
	tag         string
}

// simple example graph
// [3][3][100]*blockConstructor = 3 regions, each w/ 3 zones, each w/ 100 blocks
var networkGraph = [1][1][6]*blockConstructor{
	{ // Region1
		{ // Zone1
			&genesisBlock,
			&blockConstructor{[3]int{0, 1, 1}, [3]string{}, ""},
			&blockConstructor{[3]int{0, 1, 2}, [3]string{}, ""},
			&blockConstructor{[3]int{0, 1, 3}, [3]string{}, ""},
			&blockConstructor{[3]int{0, 2, 4}, [3]string{}, ""},
			&blockConstructor{[3]int{1, 3, 5}, [3]string{}, ""},
		},
	},
}

/*
func BlockInterpreter(networkGraph [][][]*blockConstructor) []chainOrders {

	chains := []chainOrders{}
	for _, regionSlice := range networkGraph {
		for _, zoneSlice := range regionSlice {
			chain := chainOrders{}
			prime := 0
			region := 0
			zone := 0

			for i, block := range zoneSlice {
				if i == 0 {
					chain.parent = []int{prime, region, zone}
					chain.parentTags = block.parentTags
					chain.tag = block.tag
				}
				if block == genesisBlock {
					chain.orders[i] = 0 // selects order in slice
					chain.startNumber = []int{0, 0, 0}
					chain.parent = nil // remember to assign genesis
				} else {

				}
			}
		}
	}

	return chains
}
*/
