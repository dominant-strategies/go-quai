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
	PrimeContext: common.HexToHash("0x00001bba9942d13de6f0cc5d87a0bcc5584908be514990d2c442c43f13847093"),
	RegionContext: []common.Hash{
		common.HexToHash("0x000006863c188f1126aba2a2c51090fca01dd4850f2467c85f821e89d249bb2b"),
		common.HexToHash("0x000008b13838f662f006fe14a232f1e6193f1930fbce505bf387fabf8d6754c0"),
		common.HexToHash("0x00000ded81dc992388832a860334bd6ad15fd5cad84a79674577527a886a663f"),
	},
	ZoneContext: [][]common.Hash{
		[]common.Hash{
			common.HexToHash("0x0000083ec920ae605204f98324b2ea1087a24f3250f67b3a9a1b1ea8e9bda5d1"),
			common.HexToHash("0x00000585e8ca5b9ceb1c136c612ab568fca728536afb95533a0c9d421799bd29"),
			common.HexToHash("0x00001144430b40ae978bc73da8dfeb2609279de481260adbb56a1b55bfa9a3b0"),
		},
		[]common.Hash{
			common.HexToHash("0x000016f4c88831435a64983ff886184b96261d47ea5a54386a365c22b4580c0a"),
			common.HexToHash("0x000000c42987d30694e13ce7d5f167aea7d3fb53e6cfc69d6ff893a03eca155e"),
			common.HexToHash("0x0000052ab142f5dc4fb254a4a0a3076692eef3ea055d345c32df472008b6e6fc"),
		},
		[]common.Hash{
			common.HexToHash("0x000007aeb7f5a6b66ea2665dae43fa73cc1c35557c4c582e34b9e4f10dcaca31"),
			common.HexToHash("0x000004fc326b09d88cc3fabe1dec253a303127a13e184e08f5d7ad5bb7560525"),
			common.HexToHash("0x000008e4a6794b21358929019a43eb5127f311114131bef9ba567adcd98b2b08"),
		},
	},
}

var BadHashes = []HeirarchyBadHashes{
	fork1,
}
