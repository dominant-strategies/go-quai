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
	"testing"

	"github.com/spruce-solutions/go-quai/consensus/blake3"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/crypto"
	"github.com/spruce-solutions/go-quai/params"
)

func ExampleGenerateChain() {
	var (
		key1, _ = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		key2, _ = crypto.HexToECDSA("8a1f9a8f95be41cd7ccb6168179afb4504aefe388d1e14474d32c45c72ce7b7a")
		key3, _ = crypto.HexToECDSA("49a7b37aa6f6645917e7b807e9d1c00d4fa71f18343b0d4122a4d2df64dd6fee")
		addr1   = crypto.PubkeyToAddress(key1.PublicKey)
		addr2   = crypto.PubkeyToAddress(key2.PublicKey)
		addr3   = crypto.PubkeyToAddress(key3.PublicKey)
		db      = rawdb.NewMemoryDatabase()
	)

	// Ensure that key1 has some funds in the genesis block.
	gspec := &Genesis{
		Config: &params.ChainConfig{HomesteadBlock: new(big.Int)},
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
			tx, _ := types.SignTx(types.NewTransaction(gen.TxNonce(addr1), addr2, big.NewInt(10000), params.TxGas, nil, nil), signer, key1)
			gen.AddTx(tx)
		case 1:
			// In block 2, addr1 sends some more ether to addr2.
			// addr2 passes it on to addr3.
			tx1, _ := types.SignTx(types.NewTransaction(gen.TxNonce(addr1), addr2, big.NewInt(1000), params.TxGas, nil, nil), signer, key1)
			tx2, _ := types.SignTx(types.NewTransaction(gen.TxNonce(addr2), addr3, big.NewInt(1000), params.TxGas, nil, nil), signer, key2)
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
}

func TestGenerateNetworkBlocks(t *testing.T) {
	graph := [3][3][]*types.BlockGenSpec{
		{ // Region1
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"z21_1", "z13_1", "z11_4"}, "final_prime"},
				&types.BlockGenSpec{[3]string{"", "", "final_prime"}, "final_zone11"},
			},
			{},
			{
				&types.BlockGenSpec{[3]string{"", "z11_2", "gen"}, "z13_1"},
			},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z23_1", "z23_1", "gen"}, "z21_1"},
				&types.BlockGenSpec{[3]string{"", "z21_1", "z21_1"}, "final_region2"},
			},
			{},
			{
				&types.BlockGenSpec{[3]string{"final_region3", "gen", "gen"}, "z23_1"},
			},
		},
		{
			{},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z32_1"},
				&types.BlockGenSpec{[3]string{"z11_1", "z33_1", "z32_1"}, "final_region3"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z33_1"},
				&types.BlockGenSpec{[3]string{"", "", "z33_1"}, "final_zone33"},
			},
		},
	}
	blocks, err := GenerateNetworkBlocks(graph)
	if err != nil {
		t.Errorf("Error in GenerateNetworkBlocks call")
	}
	engine := new(blake3.Blake3)
	//Check Criteria 1: Every tagged block should be identifiable in the output map using the tag as the map key
	_, found := blocks["final_prime"]
	if !found {
		t.Errorf("Tag not found in network")
	}

	//Check Criteria 2: Every block should have the correct order. Need to implement an engine to calculate Order
	difficultyMap := map[string]int{
		"z11_1":         1,
		"z11_2":         2,
		"z11_3":         3,
		"z11_4":         3,
		"final_prime":   1,
		"final_zone11":  1,
		"z13_1":         2,
		"z21_1":         1,
		"final_region2": 2,
		"z23_1":         1,
		"z32_1":         3,
		"final_region3": 1,
		"z33_1":         2,
		"final_zone33":  3,
	}
	for tag, expectedOrder := range difficultyMap {
		actualOrder, _ := engine.GetDifficultyOrder(blocks[tag].Header())
		if expectedOrder != actualOrder {
			t.Errorf("Incorrect block difficutly")
		}
	}

	//Check Criteria 3: Block parents should chain as expected. Start at the last block and iterate backwards from it to genesis
	primeChain := []string{"z11_1", "final_region3", "z23_1", "z21_1", "final_prime"}
	region2Chain := []string{"z23_1", "z21_1", "final_region2"}
	zone11Chain := []string{"z11_1", "z11_2", "z11_3", "z11_4", "final_prime", "final_zone11"}

	for i := len(primeChain); i > 0; i-- {
		block := blocks[primeChain[i]]
		prevBlock := blocks[primeChain[i-1]]
		if block.Header().ParentHash[0] != prevBlock.Header().Hash() {
			t.Errorf("Prime blocks chain incorrectly")
		}
	}

	for i := len(region2Chain); i > 0; i-- {
		block := blocks[region2Chain[i]]
		prevBlock := blocks[region2Chain[i-1]]
		if block.Header().ParentHash[1] != prevBlock.Header().Hash() {
			t.Errorf("Region2 blocks chain incorrectly")
		}
	}

	for i := len(zone11Chain); i > 0; i-- {
		block := blocks[zone11Chain[i]]
		prevBlock := blocks[zone11Chain[i-1]]
		if block.Header().ParentHash[2] != prevBlock.Header().Hash() {
			t.Errorf("Zone11 blocks chain incorrectly")
		}
	}

}
