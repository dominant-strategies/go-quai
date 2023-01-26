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

import "github.com/dominant-strategies/go-quai/common"

// ColosseumBootnodes are the enode URLs of the P2P bootstrap nodes running on
// the Colosseum test network.
var ColosseumBootnodes = []string{
	"enode://1edee38aebefea9150213cdf4306a64499e3bca9058971c443d31a4997b9a096e7a26059472529a6ccbca0a04c4f6fdbf0c880ee97545a12c0f8b656f7d4e7da@35.192.126.165",
	"enode://0f7b74f957d68eb85bbbf8ffe7d0477575406beb647f2353de08ca291a03db1507a70a41f3b4623c3049512342781c8fa9a7f7654cbcc8603e51190430b839a6@34.170.190.144",
	"enode://66c87db18ab3e6322a149ac4b7b1bde3a31f1e505945a358e4c1ebae9dc8a40402ccd96dfed35438448fbf79a7b8ee093581a05c40ab115adaebe81a6946993b@104.197.16.107",
}

// GardenBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Garden test network.
var GardenBootnodes = []string{
	"enode://75c6cbbb08e45281606fd963c1fbbad763227dc370a9bf36185c7d9e9a66b7f7eebb17572d6932fb108ec37f333e8703656af79a98e651040131174a50124d09@34.64.47.60",   // Asia
	"enode://273a1c529bdf064acbae6ab29c5c533f8caea2dce0ea4b0d5b7b6da58cc5b0de5924cd30bced5aba231a0aa9e5063d3baddc6d77e924d3999640a9c1304c5ef1@34.175.50.212", // Europe
	"enode://e5c5470889825d2d32d6f55ee74d50d7d0040407f1339123572e675c818cc64dcbfbb4e202bc63bdff5e80e6c9ebba81370588d9737d127fbb663614ab7ece62@34.95.212.143", // SouthAmerica
	"enode://b270dad3ea88fd8276d2eb20403c0f34d3eb1765f7dc124c3c41ed862f6adf78789b41ee7a7f0117f9e82a5c0a8ae3d9dae3d7c9200ea62cb82af9af08f36cfb@34.70.251.243", // Central USA
}

var V5Bootnodes = []string{}

const dnsPrefix = "enrtree://ALE24Z2TEZV2XK46RXVB6IIN5HB5WTI4F4SMAVLYCAQIUPU53RSUU@"

// KnownDNSNetwork returns the address of a public DNS-based node list for the given
// genesis hash and protocol. See https://github.com/ethereum/discv4-dns-lists for more
// information.
func KnownDNSNetwork(genesis common.Hash, protocol string) string {
	var net string
	switch genesis {
	case ColosseumGenesisHash:
		net = "colosseum"
	case GardenGenesisHash:
		net = "garden"
	default:
		return ""
	}
	return dnsPrefix + common.NodeLocation.Name() + "." + net + ".quainodes.io"
}
