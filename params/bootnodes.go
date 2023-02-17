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
	"enode://a0353563c3d5e3db1cf729fe77fad8e98360e13f00bf2147b566bf91f99782932d6aafc30a21855017451d415da295c7b3e18d0c4d80c3709265134c3f21bb90@35.202.3.128",
	"enode://d2d4ec50580e60d95e1b2234a106065ffe3b9620e3b3e96d4959e7ffb3e71d6ffc6d452f8d94d451179677a07c2213b6a3af1be24cbea8f7eb4c2e393cee053c@35.224.101.46",
	"enode://9450c09f59e45779f8c84aa2d279ee2a705ef560b8c3aa6128c9fe2d272e63f1d940f8efa775ab3644caac6d8bcbfe2bb2647215c91cd06faeabc08b23888a3b@35.184.84.143",
}

// GardenBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Garden test network.
var GardenBootnodes = []string{
	"enode://75c6cbbb08e45281606fd963c1fbbad763227dc370a9bf36185c7d9e9a66b7f7eebb17572d6932fb108ec37f333e8703656af79a98e651040131174a50124d09@34.64.47.60",   // Asia
	"enode://273a1c529bdf064acbae6ab29c5c533f8caea2dce0ea4b0d5b7b6da58cc5b0de5924cd30bced5aba231a0aa9e5063d3baddc6d77e924d3999640a9c1304c5ef1@34.175.50.212", // Europe
	"enode://e5c5470889825d2d32d6f55ee74d50d7d0040407f1339123572e675c818cc64dcbfbb4e202bc63bdff5e80e6c9ebba81370588d9737d127fbb663614ab7ece62@34.95.212.143", // SouthAmerica
	"enode://b270dad3ea88fd8276d2eb20403c0f34d3eb1765f7dc124c3c41ed862f6adf78789b41ee7a7f0117f9e82a5c0a8ae3d9dae3d7c9200ea62cb82af9af08f36cfb@34.70.251.243", // Central USA
}

// OrchardBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Orchard test network
var OrchardBootnodes = []string{
	"enode://5f03bfac8b38f18e7384d72e48bb6f9a4970ce75b3a36b45041cc1bfde61e2b760b46d9d73e2a40806ccb07f1fb32e9c38d350829c03ed9ba16369c9c24c3664@34.64.202.127",
	"enode://e52f659870791a0358e81db559d5fd257c753a5c6bf483e62922980b50d289f54b1efb6f49e1e20ea4d635ed76681163849fae0a2bd16d2f40ed72332487946a@34.175.158.49",
	"enode://44facffd8d3376d93ac839d9762f830f27ef1d400bf1d7106e7997b1bd2c476bf439c14bcd125e263163e571ddb803b01ef25689aa0d43d50496f4136bcd4710@34.95.228.160",
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
	case OrchardGenesisHash:
		net = "orchard"
	default:
		return ""
	}
	return dnsPrefix + common.NodeLocation.Name() + "." + net + ".quainodes.io"
}
