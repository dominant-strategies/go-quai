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

package params

import "github.com/spruce-solutions/go-quai/common"

// MainnetBootnodes are the enode URLs of the P2P bootstrap nodes running on
// the main Quai Network.
var MainnetBootnodes = []string{
	"enode://a14dc9ceaebdcfb1149ba1dd489e8743f268b517c511c215976e2e71cb39e778b6b9e767ae05e9574cdbd10733458ebf94e06ce18524f4c4b7673e378b0bddd0@140.82.19.149", // vultr-full-node-1
	"enode://794367029090f82f56b079aa55b4f95f1767ff8de78482ed2a2604d970065017e1da77ac8492a1588abf9673635414f7383068328e8428d1e46902a140eddf14@45.76.19.78",   // vultr-full-node-2
	"enode://9f8acf7988b21ee7bb5772502173e7bc902843190ce31857d13052474dd167ce479577f536cda07dafc84d63fbaad36016274aca74d2c69215f7cc081ef05e04@45.32.77.104",  // vultr-full-node-3
}

// RopstenBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Ropsten test network.
var RopstenBootnodes = []string{}

// RinkebyBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Rinkeby test network.
var RinkebyBootnodes = []string{}

// GoerliBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Görli test network.
var GoerliBootnodes = []string{}

var V5Bootnodes = []string{}

const dnsPrefix = "enrtree://AKA3AM6LPBYEUDMVNU3BSVQJ5AD45Y7YPOHJLEF6W26QOE4VTUDPE@"

// KnownDNSNetwork returns the address of a public DNS-based node list for the given
// genesis hash and protocol. See https://github.com/ethereum/discv4-dns-lists for more
// information.
func KnownDNSNetwork(genesis common.Hash, protocol string) string {
	var net string
	switch genesis {
	case MainnetPrimeGenesisHash:
		net = "mainnet-prime"
	case MainnetRegionGenesisHash:
		net = "mainnet-region"
	case MainnetZoneGenesisHash:
		net = "mainnet-zone"
	case RopstenPrimeGenesisHash:
		net = "ropsten-prime"
	case RopstenRegionGenesisHash:
		net = "ropsten-region"
	case RopstenZoneGenesisHash:
		net = "ropsten-zone"
	case RinkebyGenesisHash:
		net = "rinkeby"
	case GoerliGenesisHash:
		net = "goerli"
	default:
		return ""
	}
	return dnsPrefix + protocol + "." + net + ".ethdisco.net"
}
