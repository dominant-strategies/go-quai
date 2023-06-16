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

var fork1 = HeirarchyBadHashes{
	PrimeContext: common.HexToHash("0x000000d089cce1550f0a745b6b4d8d13eebcf0465bfa11c1cde5641c852f03a1"),
	RegionContext: []common.Hash{
		common.HexToHash("0x000000f219ffd432599f98d70c8f507abc12639d6b023537353ff35ba9b98365"),
		common.HexToHash("0x0000033b9fb8d1ae32037e5af8f6a4dc2e774c10164768a56993eeac8a334b74"),
		common.HexToHash("0x000002798957d932d3ca7330a068349ae45d7e438b6691fb5366694243380b00"),
	},
	ZoneContext: [][]common.Hash{
		[]common.Hash{
			common.HexToHash("0x000000443ccc0b51204fb6ac127a67aa726366132000646987ce9258cc77e08e"),
			common.HexToHash("0x0000002f2db68d82ffd3e1f2936cc29e193bba36b580b124158983115d6b7005"),
			common.HexToHash("0x0000020b95ee752307cd656ee2d85687b7d74aa5c343c54d40f18e91e80724a7"),
		},
		[]common.Hash{
			common.HexToHash("0x0000012e5be2e46a259c8ac530ecb5453e2fbc7c5a4e4c21b11591b80b0be50c"),
			common.HexToHash("0x00000294b3bb77c3147eb85d44b380fc8e90df7671c99d4fde12bfcafbcb3f9f"),
			common.HexToHash("0x000001e24a0642ff7480b4c45595189abd56a67e1790c4f77a0e9f85fc7c9e10"),
		},
		[]common.Hash{
			common.HexToHash("0x000000a41dbda7cc89c17b9e2675d94ab3c1ba67b104542ac2f2c49f7b89f1ad"),
			common.HexToHash("0x0000035bd7386943a57c2e759d04188924f11db05b0db5cbb526f3bef6142f18"),
			common.HexToHash("0x000002ecec801141f06ce178660fe1251349728bc4180e67e9d08f18273cc9f1"),
		},
	},
} 
var BadHashes = []HeirarchyBadHashes{
	fork1,
}
