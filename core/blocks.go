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
		PrimeContext: common.HexToHash("0x03f10030572fe5b9d257bb799937099faea80f1f157c757f8a15da4c3e39cbd0"),
		RegionContext: []common.Hash{
			common.HexToHash("0x3da90f4c5f6584f564dcee484fcb3d07ad7acdd3be5e2f17e751ebc872440a36"),
			common.HexToHash("0xda2197e8719a2350fba2a38392e77a453319f668eac5035a5fd1d133df9a014a"),
			common.HexToHash("0xc53ff2a55af00b3915ad46c12d1b418e4f92de4d3663fa1ce152a8e4a7a7c086"),
		},
		ZoneContext: [][]common.Hash{
			[]common.Hash{
				common.HexToHash("0xbbbcddc758135c6ce9a982a7e49fce9174a34b5cb8b9e2ed17026ac309267960"),
				common.HexToHash("0x62a58e2bae1c869f5c67d0d04718a2dd37dbfa462968be3c779ebeaa501ba4f2"),
				common.HexToHash("0x9a35566465f8bc47b8ce904fbbdf4c33d230cb31021b9825f262d5280f14ab97"),
			},
			[]common.Hash{
				common.HexToHash("0x964615a38fc0caefeeb17688e9df01d19f9d559408e2bb80b4158093ec9bb780"),
				common.HexToHash("0xabcf5d688d094a5108419dac68e9dfec9aabf3ba9944080e2eed518b4701bcf8"),
				common.HexToHash("0x0172bc291bab64540cd8692cbcd63453dd360edfa96986ad7754e6111b299206"),
			},
			[]common.Hash{
				common.HexToHash("0x351a335d9775bcf319b3a27ef3aee7db5b8b6d1610308b325d8e006b74d4feb2"),
				common.HexToHash("0x7972d8c316c04eb059e7de85bc9b26b72c6a8bde66add94dc4781de336f3f7e4"),
				common.HexToHash("0x366e9f2ff94d4ba1dbd616805437c10eeae33d52726c83f9ddf3ad450c5bb6ec"),
			},
		},
	},
}
