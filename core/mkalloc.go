// Copyright 2017 The go-ethereum Authors
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

// +build none

/*

   The mkalloc tool creates the genesis allocation constants in genesis_alloc.go
   It outputs a const declaration that contains an RLP-encoded list of (address, balance) tuples.

       go run mkalloc.go genesis.json

*/
package main

import (
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"io/ioutil"
	// "encoding/json"

	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/rlp"
)

type allocList []allocItem

func (a allocList) Len() int           { return len(a) }
func (a allocList) Less(i, j int) bool { return a[i].Addr.Cmp(a[j].Addr) < 0 }
func (a allocList) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

type allocItem struct {
	Addr    *big.Int
	Balance *big.Int
}

func makelist(alloc core.GenesisAlloc) []allocItem {
	a := make(allocList, 0, len(alloc))
	// a := [len(alloc)]allocItem{}
	for addr, account := range alloc {
		bigAddr := new(big.Int).SetBytes(addr.Bytes())
		a = append(a, allocItem{bigAddr, account.Balance})
	}
	sort.Sort(a)
	return a
}

func makeAlloc(g core.Genesis) string {
	a := makelist(g.Alloc)
	data, err := rlp.EncodeToBytes(a)
	if err != nil {
		panic(err)
	}
	return strconv.QuoteToASCII(string(data))
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: mkalloc genesis.json")
		os.Exit(1)
	}

	g := new(core.Genesis)
	allocMap := map[common.UnprefixedAddress]GenesisAccount
	// file, err := os.Open(os.Args[1])
	// if err != nil {
	// 	panic(err)
	// }
	// // if err := json.NewDecoder(file).Decode(g); err != nil {
	// if err := json.NewDecoder(file).Decode(allocList) {
	// 	panic(err)
	// }
	// fmt.Println("const allocData =", makealloc(g))

	data, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		os.Exit(1)
	}

	// var inputGenesis core.Genesis

	fmt.Printf("data: ", data)

	err = json.Unmarshal(data, allocMap)
	if err != nil {
		fmt.Printf("Error unmarshalling JSON: %v\n", err)
		os.Exit(1)
	}

	g.Alloc = allocMap

	allocStr := makeAlloc(g)

	fmt.Printf("Alloc: %+v\n", allocStr)

}
