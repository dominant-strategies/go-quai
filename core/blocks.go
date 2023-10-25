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

import "github.com/dominant-strategies/go-quai/common"

type HeirarchyBadHashes struct {
	PrimeContext  common.Hash
	RegionContext []common.Hash
	ZoneContext   [][]common.Hash
}

var BadHashes = []HeirarchyBadHashes{
	HeirarchyBadHashes{
		PrimeContext: common.HexToHash("0x70ad991983e04ecc480218c5f19c67d089728800d9c59385c26138a0e05a4338"),
		RegionContext: []common.Hash{
			common.HexToHash("0x4d3211f5bc7301ad589cf70391a51c513e7b07de5295ca2bffc31337b32f2366"),
			common.HexToHash("0x56b038de63672ea81e1f61ad8595d7960a2d4d67ae27126ddecd841104064a67"),
			common.HexToHash("0xc491eff2156db47e42d6bcf569db8dadbd51d0d0e10646941194ddfd92b18f26"),
		},
		ZoneContext: [][]common.Hash{
			[]common.Hash{
				common.HexToHash("0x99a0e419913cafa126e3ac80d0d176f02267e6bec6edee82c1fee0fad85b3ca8"),
				common.HexToHash("0x3c8dff9ad5926316e2f71fb95efa35808d52fb392f35c384f5382515a44856bf"),
				common.HexToHash("0xa3361dd358cd3373b1e7a8be1bf2c41a119c3d01314072c4c4f6626f4c0b88a7"),
			},
			[]common.Hash{
				common.HexToHash("0x7e3eef3ded1d56b938e040ec94176dcd047b675763441422bdab866dc9f5cc19"),
				common.HexToHash("0xafc9b3464e2889f683b1c982e2dba68cc325016fc9540cbe4d770a9e609c86aa"),
				common.HexToHash("0xeb38f5a1ffdd12a937e5b4ce16c0328b4d15137301aaf32959ba7493cf0ddb12"),
			},
			[]common.Hash{
				common.HexToHash("0x69d02a02e0127b90013392d820e04d26c19a61d2ba057bf86832e1d4ee74ce75"),
				common.HexToHash("0xcb1930e1215e015fa3da8ff3e587fe94c10fb4d1ea4a25c3339ccda677507aa7"),
				common.HexToHash("0x20874acb12eb1da946f308ae56c06b60a5234f3eb4373f3a8c5b56339ad46370"),
			},
		},
	},
}
