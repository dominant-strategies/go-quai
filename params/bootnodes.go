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
	"enode://aa76ab25bd9657efc8371103649ad9fc099388f1b303b18ab9b5fbdd43c8bae1e039357eb6a5a3aa86679f58ed0231c88bd207393448a7226d63fdc4651698cb@35.202.3.128",  // us-central1-a
	"enode://7a80da93d6ba5299fccf4dc195c0a9efaee5ef3ef9ae00c7ae4db72f10cd26750b7acaf956a381f3101860b58c931b56a4524051440382f1be5e33dc247a9b52@35.184.84.143", // us-central1-a
	"enode://1ba881701345af5491ec9ced31d2c325bdd296219015856109e8405bfa51665c0dc91b85a93541ba2ae6294d0dee4ef9c8cab7364594234447b30237631e90c6@35.224.101.46", // us-central1-a
}

// GardenBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Garden test network.
var GardenBootnodes = []string{
	"enode://f353567741755a4b35afb4641618b34c7e3c1666b49c9e519e8336c124ba8c2c556f00e9edcd87599994e9674ebdea3503a5ff1948a257c1540fe9f8c0fedf62@35.231.138.241",  // us-east1-b
	"enode://aa3d4daa8ea958c8a4fe3c56a7c5c0509754eec9b2e4eb45ac5fe9eb16bea442da0b50a0ac09e5fdf7711eb6b64df76dcd22e48d69f2f938d19fc60b9a8fd579@34.118.24.175", // europe-central2-a
	"enode://13ed279b8013b61ef41466d4f07679fe17c407af4f6ae09b34042c72635eef314e9020f732a96a739abaa88f51108b76698f6b7335397be99418c371e5c3bcce@34.68.104.77",  // us-central1-a
}

// OrchardBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Orchard test network
var OrchardBootnodes = []string{
	"enode://f99403abcfbee37e3232e6bb4d7fda4d70496585a53857ccb4aff5ec32d0f27186b5097430d5806f20f2003f35cfec5d778598a3945d530f212b7072caab9b8a@35.188.17.207",  // us-central1-b
	"enode://142e48e3c36e5fe21aebf2941f2e63eb4720febe67de17dd84baf010e33c76275567ede53674007ab2848eec53022cd0cb94bcbea10960ae93edb5497c8caa2a@104.198.69.162", // us-central1-a
	"enode://d6d27b273682f8abc7ffff04dc9006bd40f0a079a8ba24da351b714506bb82c1f106ff073fa04983345aef15c876c602209b48b37d8ee10dad581fd1d9db9263@34.23.150.43",   // us-east1-c
}

// GalenaBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Galena test network
var GalenaBootnodes = []string{
	"enode://ee89c22bff79d040fcf3dbaea3bcbe429e68b0ca9e32671027554e96aea3f132f6abed8cf5be514c50b76cf2cab96c7d9064a93f0bbd0903f26df4be01ce0e6a@35.196.124.28", // europe-southwest1-a
	"enode://b39cf3080c8c9165bf0b50a7f6c8ff5a3568649b0c57ae786f630a054722fccfec7e3232594eb37a62a04e7c310a4d4e899ea42c0bd5a5043a248510715e2af9@35.187.55.110", // southamerica-east1-b
	"enode://ce3daf05c462b36bc1a6261b8edb0cd72bf041c1f8fb59100d6a09dc3415d1f136cc8768b12129db73c48085c8e01d7a606be447d3f681f405ab427641599235@34.92.50.205", // asia-northeast3-a
}

var V5Bootnodes = []string{}

const dnsPrefix = "enrtree://ALE24Z2TEZV2XK46RXVB6IIN5HB5WTI4F4SMAVLYCAQIUPU53RSUU@"

// KnownDNSNetwork returns the address of a public DNS-based node list for the given
// genesis hash and protocol.
func KnownDNSNetwork(genesis common.Hash, protocol string) string {
	var net string
	switch genesis {
	case ProgpowColosseumGenesisHash:
		net = "colosseum"
	case ProgpowGardenGenesisHash:
		net = "garden"
	case ProgpowOrchardGenesisHash:
		net = "orchard"
	case ProgpowGalenaGenesisHash:
		net = "galena"
	case Blake3PowColosseumGenesisHash:
		net = "colosseum"
	case Blake3PowGardenGenesisHash:
		net = "garden"
	case Blake3PowOrchardGenesisHash:
		net = "orchard"
	case Blake3PowGalenaGenesisHash:
		net = "galena"
	default:
		return ""
	}
	return dnsPrefix + common.NodeLocation.Name() + "." + net + ".quainodes.io"
}
