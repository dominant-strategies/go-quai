// Copyright 2016 The go-ethereum Authors
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

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/spruce-solutions/go-quai/common"
	"golang.org/x/crypto/sha3"
)

// Genesis hashes to enforce below configs on.
var (
	MainnetPrimeGenesisHash  = common.HexToHash("0x8dd9cfdf62bc5d7a0401d2b525a43ddde3f3392e41b443be1628057193812f0e")
	MainnetRegionGenesisHash = common.HexToHash("0x8dd9cfdf62bc5d7a0401d2b525a43ddde3f3392e41b443be1628057193812f0e")
	MainnetZoneGenesisHash   = common.HexToHash("0x8dd9cfdf62bc5d7a0401d2b525a43ddde3f3392e41b443be1628057193812f0e")
	RopstenPrimeGenesisHash  = common.HexToHash("0x9c0d89a5294e4635a49f6d4c9f9e1a361cb76bfffa71be2ff01a64f3c5dd0dcc")
	RopstenRegionGenesisHash = common.HexToHash("0x9c0d89a5294e4635a49f6d4c9f9e1a361cb76bfffa71be2ff01a64f3c5dd0dcc")
	RopstenZoneGenesisHash   = common.HexToHash("0x9c0d89a5294e4635a49f6d4c9f9e1a361cb76bfffa71be2ff01a64f3c5dd0dcc")
	RopstenGenesisHash       = common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d")
	RinkebyGenesisHash       = common.HexToHash("0x6341fd3daf94b748c72ced5a5b26028f2474f5f00d824504e4fa37a75767e177")
	GoerliGenesisHash        = common.HexToHash("0xbf7e331f7f7c1dd2e05159666b3bf8bc7a8a3a9eb1d518969eab529dd9b88c1a")
	CalaverasGenesisHash     = common.HexToHash("0xeb9233d066c275efcdfed8037f4fc082770176aefdbcb7691c71da412a5670f2")
)

// TrustedCheckpoints associates each known checkpoint with the genesis hash of
// the chain it belongs to.
var TrustedCheckpoints = map[common.Hash]*TrustedCheckpoint{
	MainnetPrimeGenesisHash: MainnetTrustedCheckpoint,
	RopstenGenesisHash:      RopstenTrustedCheckpoint,
}

// CheckpointOracles associates each known checkpoint oracles with the genesis hash of
// the chain it belongs to.
var CheckpointOracles = map[common.Hash]*CheckpointOracleConfig{
	MainnetPrimeGenesisHash: MainnetCheckpointOracle,
	RopstenGenesisHash:      RopstenCheckpointOracle,
}
var mainnetValidChains = []*big.Int{big.NewInt(9000), big.NewInt(9100), big.NewInt(9101), big.NewInt(9102), big.NewInt(9103), big.NewInt(9200), big.NewInt(9201), big.NewInt(9202), big.NewInt(9203), big.NewInt(9300), big.NewInt(9301), big.NewInt(9302), big.NewInt(9303)}

var testnetValidChains = []*big.Int{big.NewInt(12000), big.NewInt(12100), big.NewInt(12101), big.NewInt(12102), big.NewInt(12103), big.NewInt(12200), big.NewInt(12201), big.NewInt(12202), big.NewInt(12203), big.NewInt(12300), big.NewInt(12301), big.NewInt(12302), big.NewInt(12303)}

var (
	// MainnetPrimeChainConfig is the chain parameters to run a node on the main network.
	MainnetPrimeChainConfig = &ChainConfig{
		ChainID:             big.NewInt(9000),
		Context:             0,
		Location:            []byte{0, 0},
		FullerMapContext:    big.NewInt(0),
		Ethash:              new(EthashConfig),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		MuirGlacierBlock:    big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
	}

	MainnetRegionChainConfigs = []ChainConfig{
		ChainConfig{
			ChainID:             big.NewInt(9100),
			Context:             1,
			Location:            []byte{1, 0},
			FullerMapContext:    big.NewInt(0),
			Ethash:              new(EthashConfig),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			MuirGlacierBlock:    big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
			GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
		},
		ChainConfig{
			ChainID:             big.NewInt(9200),
			Context:             1,
			Location:            []byte{2, 0},
			FullerMapContext:    big.NewInt(0),
			Ethash:              new(EthashConfig),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			MuirGlacierBlock:    big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
			GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
		},
		ChainConfig{
			ChainID:             big.NewInt(9300),
			Context:             1,
			Location:            []byte{3, 0},
			FullerMapContext:    big.NewInt(0),
			Ethash:              new(EthashConfig),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			MuirGlacierBlock:    big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
			GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
		},
	}

	MainnetZoneChainConfigs = [][]ChainConfig{
		[]ChainConfig{
			ChainConfig{
				ChainID:             big.NewInt(9101),
				Context:             2,
				Location:            []byte{1, 1},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(9102),
				Context:             2,
				Location:            []byte{1, 2},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(9103),
				Context:             2,
				Location:            []byte{1, 3},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			}},
		[]ChainConfig{
			ChainConfig{
				ChainID:             big.NewInt(9201),
				Context:             2,
				Location:            []byte{2, 1},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(9202),
				Context:             2,
				Location:            []byte{2, 2},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(9203),
				Context:             2,
				Location:            []byte{2, 3},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			}},
		[]ChainConfig{
			ChainConfig{
				ChainID:             big.NewInt(9301),
				Context:             2,
				Location:            []byte{3, 1},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(9302),
				Context:             2,
				Location:            []byte{3, 2},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(9303),
				Context:             2,
				Location:            []byte{3, 3},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{MainnetPrimeGenesisHash, MainnetRegionGenesisHash, MainnetZoneGenesisHash},
			},
		},
	}

	// RopstenPrimeChainConfig is the chain parameters to run a node on the test network.
	RopstenPrimeChainConfig = &ChainConfig{
		ChainID:             big.NewInt(12000),
		Context:             0,
		Location:            []byte{0, 0},
		FullerMapContext:    big.NewInt(0),
		Ethash:              new(EthashConfig),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		MuirGlacierBlock:    big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
	}

	RopstenRegionChainConfigs = []ChainConfig{
		ChainConfig{
			ChainID:             big.NewInt(12100),
			Context:             1,
			Location:            []byte{1, 0},
			FullerMapContext:    big.NewInt(0),
			Ethash:              new(EthashConfig),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			MuirGlacierBlock:    big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
			GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
		},
		ChainConfig{
			ChainID:             big.NewInt(12200),
			Context:             1,
			Location:            []byte{2, 0},
			FullerMapContext:    big.NewInt(0),
			Ethash:              new(EthashConfig),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			MuirGlacierBlock:    big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
			GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
		},
		ChainConfig{
			ChainID:             big.NewInt(12300),
			Context:             1,
			Location:            []byte{3, 0},
			FullerMapContext:    big.NewInt(0),
			Ethash:              new(EthashConfig),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			MuirGlacierBlock:    big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
			GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
		},
	}

	RopstenZoneChainConfigs = [][]ChainConfig{
		[]ChainConfig{
			ChainConfig{
				ChainID:             big.NewInt(12101),
				Context:             2,
				Location:            []byte{1, 1},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(12102),
				Context:             2,
				Location:            []byte{1, 2},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(12103),
				Context:             2,
				Location:            []byte{1, 3},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			}},
		[]ChainConfig{
			ChainConfig{
				ChainID:             big.NewInt(12201),
				Context:             2,
				Location:            []byte{2, 1},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(12202),
				Context:             2,
				Location:            []byte{2, 2},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(12203),
				Context:             2,
				Location:            []byte{2, 3},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			}},
		[]ChainConfig{
			ChainConfig{
				ChainID:             big.NewInt(12301),
				Context:             2,
				Location:            []byte{3, 1},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(12302),
				Context:             2,
				Location:            []byte{3, 2},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			},
			ChainConfig{
				ChainID:             big.NewInt(12303),
				Context:             2,
				Location:            []byte{3, 3},
				FullerMapContext:    big.NewInt(0),
				Ethash:              new(EthashConfig),
				HomesteadBlock:      big.NewInt(0),
				EIP150Block:         big.NewInt(0),
				EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
				EIP155Block:         big.NewInt(0),
				EIP158Block:         big.NewInt(0),
				ByzantiumBlock:      big.NewInt(0),
				ConstantinopleBlock: big.NewInt(0),
				PetersburgBlock:     big.NewInt(0),
				IstanbulBlock:       big.NewInt(0),
				MuirGlacierBlock:    big.NewInt(0),
				BerlinBlock:         big.NewInt(0),
				LondonBlock:         big.NewInt(0),
				GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
			},
		},
	}

	// MainnetTrustedCheckpoint contains the light client trusted checkpoint for the main network.
	MainnetTrustedCheckpoint = &TrustedCheckpoint{}

	// MainnetCheckpointOracle contains a set of configs for the main network oracle.
	MainnetCheckpointOracle = &CheckpointOracleConfig{}

	// RopstenChainConfig contains the chain parameters to run a node on the Ropsten test network.
	RopstenChainConfig = &ChainConfig{
		ChainID:             big.NewInt(3),
		FullerMapContext:    big.NewInt(0),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP150Hash:          common.HexToHash("0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d"),
		EIP155Block:         big.NewInt(10),
		EIP158Block:         big.NewInt(10),
		ByzantiumBlock:      big.NewInt(1_700_000),
		ConstantinopleBlock: big.NewInt(4_230_000),
		PetersburgBlock:     big.NewInt(4_939_394),
		IstanbulBlock:       big.NewInt(6_485_846),
		MuirGlacierBlock:    big.NewInt(7_117_117),
		BerlinBlock:         big.NewInt(9_812_189),
		LondonBlock:         big.NewInt(10_499_401),
		Ethash:              new(EthashConfig),
		GenesisHashes:       []common.Hash{RopstenPrimeGenesisHash, RopstenRegionGenesisHash, RopstenZoneGenesisHash},
	}

	// RopstenTrustedCheckpoint contains the light client trusted checkpoint for the Ropsten test network.
	RopstenTrustedCheckpoint = &TrustedCheckpoint{
		SectionIndex: 329,
		SectionHead:  common.HexToHash("0xe66f7038333a01fb95dc9ea03e5a2bdaf4b833cdcb9e393b9127e013bd64d39b"),
		CHTRoot:      common.HexToHash("0x1b0c883338ac0d032122800c155a2e73105fbfebfaa50436893282bc2d9feec5"),
		BloomRoot:    common.HexToHash("0x3cc98c88d283bf002378246f22c653007655cbcea6ed89f98d739f73bd341a01"),
	}

	// RopstenCheckpointOracle contains a set of configs for the Ropsten test network oracle.
	RopstenCheckpointOracle = &CheckpointOracleConfig{
		Address: common.HexToAddress("0xEF79475013f154E6A65b54cB2742867791bf0B84"),
		Signers: []common.Address{
			common.HexToAddress("0x32162F3581E88a5f62e8A61892B42C46E2c18f7b"), // Peter
			common.HexToAddress("0x78d1aD571A1A09D60D9BBf25894b44e4C8859595"), // Martin
			common.HexToAddress("0x286834935f4A8Cfb4FF4C77D5770C2775aE2b0E7"), // Zsolt
			common.HexToAddress("0xb86e2B0Ab5A4B1373e40c51A7C712c70Ba2f9f8E"), // Gary
			common.HexToAddress("0x0DF8fa387C602AE62559cC4aFa4972A7045d6707"), // Guillaume
		},
		Threshold: 2,
	}

	// AllEthashProtocolChanges contains every protocol change (EIPs) introduced
	// and accepted by the Ethereum core developers into the Ethash consensus.
	//
	// This configuration is intentionally not using keyed fields to force anyone
	// adding flags to the config to also have to set these fields.
	AllEthashProtocolChanges = &ChainConfig{
		ChainID:             big.NewInt(1337),
		Context:             0,
		Location:            []byte{0, 0},
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP150Hash:          common.Hash{},
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		MuirGlacierBlock:    big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		CatalystBlock:       big.NewInt(0),
		Ethash:              new(EthashConfig),
		Clique:              nil,
		GenesisHashes:       nil,
		FullerMapContext:    big.NewInt(0)}

	// AllCliqueProtocolChanges contains every protocol change (EIPs) introduced
	// and accepted by the Ethereum core developers into the Clique consensus.
	//
	// This configuration is intentionally not using keyed fields to force anyone
	// adding flags to the config to also have to set these fields.
	AllCliqueProtocolChanges = &ChainConfig{
		ChainID:             big.NewInt(1337),
		Context:             0,
		Location:            []byte{0, 0},
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP150Hash:          common.Hash{},
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		MuirGlacierBlock:    big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		CatalystBlock:       big.NewInt(0),
		Ethash:              nil,
		Clique:              &CliqueConfig{Period: 0, Epoch: 30000},
		GenesisHashes:       nil,
		FullerMapContext:    big.NewInt(0)}

	TestChainConfig = &ChainConfig{big.NewInt(1), 0, []byte{0, 0}, big.NewInt(0), big.NewInt(0), common.Hash{}, big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), nil, new(EthashConfig), nil, nil, big.NewInt(0)}
	TestRules       = TestChainConfig.Rules(new(big.Int))
)

// TrustedCheckpoint represents a set of post-processed trie roots (CHT and
// BloomTrie) associated with the appropriate section index and head hash. It is
// used to start light syncing from this checkpoint and avoid downloading the
// entire header chain while still being able to securely access old headers/logs.
type TrustedCheckpoint struct {
	SectionIndex uint64      `json:"sectionIndex"`
	SectionHead  common.Hash `json:"sectionHead"`
	CHTRoot      common.Hash `json:"chtRoot"`
	BloomRoot    common.Hash `json:"bloomRoot"`
}

// HashEqual returns an indicator comparing the itself hash with given one.
func (c *TrustedCheckpoint) HashEqual(hash common.Hash) bool {
	if c.Empty() {
		return hash == common.Hash{}
	}
	return c.Hash() == hash
}

// Hash returns the hash of checkpoint's four key fields(index, sectionHead, chtRoot and bloomTrieRoot).
func (c *TrustedCheckpoint) Hash() common.Hash {
	var sectionIndex [8]byte
	binary.BigEndian.PutUint64(sectionIndex[:], c.SectionIndex)

	w := sha3.NewLegacyKeccak256()
	w.Write(sectionIndex[:])
	w.Write(c.SectionHead[:])
	w.Write(c.CHTRoot[:])
	w.Write(c.BloomRoot[:])

	var h common.Hash
	w.Sum(h[:0])
	return h
}

// Empty returns an indicator whether the checkpoint is regarded as empty.
func (c *TrustedCheckpoint) Empty() bool {
	return c.SectionHead == (common.Hash{}) || c.CHTRoot == (common.Hash{}) || c.BloomRoot == (common.Hash{})
}

// CheckpointOracleConfig represents a set of checkpoint contract(which acts as an oracle)
// config which used for light client checkpoint syncing.
type CheckpointOracleConfig struct {
	Address   common.Address   `json:"address"`
	Signers   []common.Address `json:"signers"`
	Threshold uint64           `json:"threshold"`
}

// ChainConfig is the core config which determines the blockchain settings.
//
// ChainConfig is stored in the database on a per block basis. This means
// that any network, identified by its genesis block, can have its own
// set of configuration options.
type ChainConfig struct {
	ChainID *big.Int `json:"chainId"` // chainId identifies the current chain and is used for replay protection

	Context  int    // Context defines the index in which the chain operates at
	Location []byte // Location for a given block

	HomesteadBlock *big.Int `json:"homesteadBlock,omitempty"` // Homestead switch block (nil = no fork, 0 = already homestead)
	// EIP150 implements the Gas price changes (https://github.com/ethereum/EIPs/issues/150)
	EIP150Block *big.Int    `json:"eip150Block,omitempty"` // EIP150 HF block (nil = no fork)
	EIP150Hash  common.Hash `json:"eip150Hash,omitempty"`  // EIP150 HF hash (needed for header only clients as only gas pricing changed)

	EIP155Block *big.Int `json:"eip155Block,omitempty"` // EIP155 HF block
	EIP158Block *big.Int `json:"eip158Block,omitempty"` // EIP158 HF block

	ByzantiumBlock      *big.Int `json:"byzantiumBlock,omitempty"`      // Byzantium switch block (nil = no fork, 0 = already on byzantium)
	ConstantinopleBlock *big.Int `json:"constantinopleBlock,omitempty"` // Constantinople switch block (nil = no fork, 0 = already activated)
	PetersburgBlock     *big.Int `json:"petersburgBlock,omitempty"`     // Petersburg switch block (nil = same as Constantinople)
	IstanbulBlock       *big.Int `json:"istanbulBlock,omitempty"`       // Istanbul switch block (nil = no fork, 0 = already on istanbul)
	MuirGlacierBlock    *big.Int `json:"muirGlacierBlock,omitempty"`    // Eip-2384 (bomb delay) switch block (nil = no fork, 0 = already activated)
	BerlinBlock         *big.Int `json:"berlinBlock,omitempty"`         // Berlin switch block (nil = no fork, 0 = already on berlin)
	LondonBlock         *big.Int `json:"londonBlock,omitempty"`         // London switch block (nil = no fork, 0 = already on london)

	CatalystBlock *big.Int `json:"catalystBlock,omitempty"` // Catalyst switch block (nil = no fork, 0 = already on catalyst)

	// Various consensus engines
	Ethash *EthashConfig `json:"ethash,omitempty"`
	Clique *CliqueConfig `json:"clique,omitempty"`

	// Genesis Hashes
	GenesisHashes []common.Hash

	// Quai Network Ontology
	FullerMapContext *big.Int // Block number effective for Fuller Map Context ontology
}

// EthashConfig is the consensus engine configs for proof-of-work based sealing.
type EthashConfig struct{}

// String implements the stringer interface, returning the consensus engine details.
func (c *EthashConfig) String() string {
	return "ethash"
}

// CliqueConfig is the consensus engine configs for proof-of-authority based sealing.
type CliqueConfig struct {
	Period uint64 `json:"period"` // Number of seconds between blocks to enforce
	Epoch  uint64 `json:"epoch"`  // Epoch length to reset votes and checkpoint
}

// String implements the stringer interface, returning the consensus engine details.
func (c *CliqueConfig) String() string {
	return "clique"
}

// String implements the fmt.Stringer interface.
func (c *ChainConfig) String() string {
	var engine interface{}
	switch {
	case c.Ethash != nil:
		engine = c.Ethash
	case c.Clique != nil:
		engine = c.Clique
	default:
		engine = "unknown"
	}
	return fmt.Sprintf("{ChainID: %v Homestead: %v EIP150: %v EIP155: %v EIP158: %v Byzantium: %v Constantinople: %v Petersburg: %v Istanbul: %v, Muir Glacier: %v, Berlin: %v, London: %v, Engine: %v, GenesisHashes: %v, Fuller: %v}",
		c.ChainID,
		c.HomesteadBlock,
		c.EIP150Block,
		c.EIP155Block,
		c.EIP158Block,
		c.ByzantiumBlock,
		c.ConstantinopleBlock,
		c.PetersburgBlock,
		c.IstanbulBlock,
		c.MuirGlacierBlock,
		c.BerlinBlock,
		c.LondonBlock,
		engine,
		c.GenesisHashes,
		c.FullerMapContext,
	)
}

// IsHomestead returns whether num is either equal to the homestead block or greater.
func (c *ChainConfig) IsHomestead(num *big.Int) bool {
	return isForked(c.HomesteadBlock, num)
}

// IsEIP150 returns whether num is either equal to the EIP150 fork block or greater.
func (c *ChainConfig) IsEIP150(num *big.Int) bool {
	return isForked(c.EIP150Block, num)
}

// IsEIP155 returns whether num is either equal to the EIP155 fork block or greater.
func (c *ChainConfig) IsEIP155(num *big.Int) bool {
	return isForked(c.EIP155Block, num)
}

// IsEIP158 returns whether num is either equal to the EIP158 fork block or greater.
func (c *ChainConfig) IsEIP158(num *big.Int) bool {
	return isForked(c.EIP158Block, num)
}

// IsByzantium returns whether num is either equal to the Byzantium fork block or greater.
func (c *ChainConfig) IsByzantium(num *big.Int) bool {
	return isForked(c.ByzantiumBlock, num)
}

// IsConstantinople returns whether num is either equal to the Constantinople fork block or greater.
func (c *ChainConfig) IsConstantinople(num *big.Int) bool {
	return isForked(c.ConstantinopleBlock, num)
}

// IsMuirGlacier returns whether num is either equal to the Muir Glacier (EIP-2384) fork block or greater.
func (c *ChainConfig) IsMuirGlacier(num *big.Int) bool {
	return isForked(c.MuirGlacierBlock, num)
}

// IsPetersburg returns whether num is either
// - equal to or greater than the PetersburgBlock fork block,
// - OR is nil, and Constantinople is active
func (c *ChainConfig) IsPetersburg(num *big.Int) bool {
	return isForked(c.PetersburgBlock, num) || c.PetersburgBlock == nil && isForked(c.ConstantinopleBlock, num)
}

// IsIstanbul returns whether num is either equal to the Istanbul fork block or greater.
func (c *ChainConfig) IsIstanbul(num *big.Int) bool {
	return isForked(c.IstanbulBlock, num)
}

// IsBerlin returns whether num is either equal to the Berlin fork block or greater.
func (c *ChainConfig) IsBerlin(num *big.Int) bool {
	return isForked(c.BerlinBlock, num)
}

// IsLondon returns whether num is either equal to the London fork block or greater.
func (c *ChainConfig) IsLondon(num *big.Int) bool {
	return isForked(c.LondonBlock, num)
}

// IsCatalyst returns whether num is either equal to the Merge fork block or greater.
func (c *ChainConfig) IsCatalyst(num *big.Int) bool {
	return isForked(c.CatalystBlock, num)
}

// IsFuller returns whether num is either equal to the Merge fork block or greater.
func (c *ChainConfig) IsFuller(num *big.Int) bool {
	return isForked(c.FullerMapContext, num)
}

// CheckCompatible checks whether scheduled fork transitions have been imported
// with a mismatching chain configuration.
func (c *ChainConfig) CheckCompatible(newcfg *ChainConfig, height uint64) *ConfigCompatError {
	bhead := new(big.Int).SetUint64(height)

	// Iterate checkCompatible to find the lowest conflict.
	var lasterr *ConfigCompatError
	for {
		err := c.checkCompatible(newcfg, bhead)
		if err == nil || (lasterr != nil && err.RewindTo == lasterr.RewindTo) {
			break
		}
		lasterr = err
		bhead.SetUint64(err.RewindTo)
	}
	return lasterr
}

// CheckConfigForkOrder checks that we don't "skip" any forks, geth isn't pluggable enough
// to guarantee that forks can be implemented in a different order than on official networks
func (c *ChainConfig) CheckConfigForkOrder() error {
	type fork struct {
		name     string
		block    *big.Int
		optional bool // if true, the fork may be nil and next fork is still allowed
	}
	var lastFork fork
	for _, cur := range []fork{
		{name: "homesteadBlock", block: c.HomesteadBlock},
		{name: "eip150Block", block: c.EIP150Block},
		{name: "eip155Block", block: c.EIP155Block},
		{name: "eip158Block", block: c.EIP158Block},
		{name: "byzantiumBlock", block: c.ByzantiumBlock},
		{name: "constantinopleBlock", block: c.ConstantinopleBlock},
		{name: "petersburgBlock", block: c.PetersburgBlock},
		{name: "istanbulBlock", block: c.IstanbulBlock},
		{name: "muirGlacierBlock", block: c.MuirGlacierBlock, optional: true},
		{name: "berlinBlock", block: c.BerlinBlock},
		{name: "londonBlock", block: c.LondonBlock},
		{name: "c.FullerMapContext", block: c.FullerMapContext},
	} {
		if lastFork.name != "" {
			// Next one must be higher number
			if lastFork.block == nil && cur.block != nil {
				return fmt.Errorf("unsupported fork ordering: %v not enabled, but %v enabled at %v",
					lastFork.name, cur.name, cur.block)
			}
			if lastFork.block != nil && cur.block != nil {
				if lastFork.block.Cmp(cur.block) > 0 {
					return fmt.Errorf("unsupported fork ordering: %v enabled at %v, but %v enabled at %v",
						lastFork.name, lastFork.block, cur.name, cur.block)
				}
			}
		}
		// If it was optional and not set, then ignore it
		if !cur.optional || cur.block != nil {
			lastFork = cur
		}
	}
	return nil
}

func (c *ChainConfig) checkCompatible(newcfg *ChainConfig, head *big.Int) *ConfigCompatError {
	if isForkIncompatible(c.HomesteadBlock, newcfg.HomesteadBlock, head) {
		return newCompatError("Homestead fork block", c.HomesteadBlock, newcfg.HomesteadBlock)
	}
	if isForkIncompatible(c.EIP150Block, newcfg.EIP150Block, head) {
		return newCompatError("EIP150 fork block", c.EIP150Block, newcfg.EIP150Block)
	}
	if isForkIncompatible(c.EIP155Block, newcfg.EIP155Block, head) {
		return newCompatError("EIP155 fork block", c.EIP155Block, newcfg.EIP155Block)
	}
	if isForkIncompatible(c.EIP158Block, newcfg.EIP158Block, head) {
		return newCompatError("EIP158 fork block", c.EIP158Block, newcfg.EIP158Block)
	}
	if c.IsEIP158(head) && !configNumEqual(c.ChainID, newcfg.ChainID) {
		return newCompatError("EIP158 chain ID", c.EIP158Block, newcfg.EIP158Block)
	}
	if isForkIncompatible(c.ByzantiumBlock, newcfg.ByzantiumBlock, head) {
		return newCompatError("Byzantium fork block", c.ByzantiumBlock, newcfg.ByzantiumBlock)
	}
	if isForkIncompatible(c.ConstantinopleBlock, newcfg.ConstantinopleBlock, head) {
		return newCompatError("Constantinople fork block", c.ConstantinopleBlock, newcfg.ConstantinopleBlock)
	}
	if isForkIncompatible(c.PetersburgBlock, newcfg.PetersburgBlock, head) {
		// the only case where we allow Petersburg to be set in the past is if it is equal to Constantinople
		// mainly to satisfy fork ordering requirements which state that Petersburg fork be set if Constantinople fork is set
		if isForkIncompatible(c.ConstantinopleBlock, newcfg.PetersburgBlock, head) {
			return newCompatError("Petersburg fork block", c.PetersburgBlock, newcfg.PetersburgBlock)
		}
	}
	if isForkIncompatible(c.IstanbulBlock, newcfg.IstanbulBlock, head) {
		return newCompatError("Istanbul fork block", c.IstanbulBlock, newcfg.IstanbulBlock)
	}
	if isForkIncompatible(c.MuirGlacierBlock, newcfg.MuirGlacierBlock, head) {
		return newCompatError("Muir Glacier fork block", c.MuirGlacierBlock, newcfg.MuirGlacierBlock)
	}
	if isForkIncompatible(c.BerlinBlock, newcfg.BerlinBlock, head) {
		return newCompatError("Berlin fork block", c.BerlinBlock, newcfg.BerlinBlock)
	}
	if isForkIncompatible(c.LondonBlock, newcfg.LondonBlock, head) {
		return newCompatError("London fork block", c.LondonBlock, newcfg.LondonBlock)
	}
	if isForkIncompatible(c.FullerMapContext, newcfg.FullerMapContext, head) {
		return newCompatError("Fuller ontology block", c.FullerMapContext, newcfg.FullerMapContext)
	}
	return nil
}

// isForkIncompatible returns true if a fork scheduled at s1 cannot be rescheduled to
// block s2 because head is already past the fork.
func isForkIncompatible(s1, s2, head *big.Int) bool {
	return (isForked(s1, head) || isForked(s2, head)) && !configNumEqual(s1, s2)
}

// isForked returns whether a fork scheduled at block s is active at the given head block.
func isForked(s, head *big.Int) bool {
	if s == nil || head == nil {
		return false
	}
	return s.Cmp(head) <= 0
}

func configNumEqual(x, y *big.Int) bool {
	if x == nil {
		return y == nil
	}
	if y == nil {
		return x == nil
	}
	return x.Cmp(y) == 0
}

// ConfigCompatError is raised if the locally-stored blockchain is initialised with a
// ChainConfig that would alter the past.
type ConfigCompatError struct {
	What string
	// block numbers of the stored and new configurations
	StoredConfig, NewConfig *big.Int
	// the block number to which the local chain must be rewound to correct the error
	RewindTo uint64
}

func newCompatError(what string, storedblock, newblock *big.Int) *ConfigCompatError {
	var rew *big.Int
	switch {
	case storedblock == nil:
		rew = newblock
	case newblock == nil || storedblock.Cmp(newblock) < 0:
		rew = storedblock
	default:
		rew = newblock
	}
	err := &ConfigCompatError{what, storedblock, newblock, 0}
	if rew != nil && rew.Sign() > 0 {
		err.RewindTo = rew.Uint64() - 1
	}
	return err
}

func (err *ConfigCompatError) Error() string {
	return fmt.Sprintf("mismatching %s in database (have %d, want %d, rewindto %d)", err.What, err.StoredConfig, err.NewConfig, err.RewindTo)
}

// Rules wraps ChainConfig and is merely syntactic sugar or can be used for functions
// that do not have or require information about the block.
//
// Rules is a one time interface meaning that it shouldn't be used in between transition
// phases.
type Rules struct {
	ChainID                                                 *big.Int
	IsHomestead, IsEIP150, IsEIP155, IsEIP158               bool
	IsByzantium, IsConstantinople, IsPetersburg, IsIstanbul bool
	IsBerlin, IsLondon, IsCatalyst                          bool
	IsFuller, IsTuring, IsLovelace                          bool
}

// Rules ensures c's ChainID is not nil.
func (c *ChainConfig) Rules(num *big.Int) Rules {
	chainID := c.ChainID
	if chainID == nil {
		chainID = new(big.Int)
	}
	return Rules{
		ChainID:          new(big.Int).Set(chainID),
		IsHomestead:      c.IsHomestead(num),
		IsEIP150:         c.IsEIP150(num),
		IsEIP155:         c.IsEIP155(num),
		IsEIP158:         c.IsEIP158(num),
		IsByzantium:      c.IsByzantium(num),
		IsConstantinople: c.IsConstantinople(num),
		IsPetersburg:     c.IsPetersburg(num),
		IsIstanbul:       c.IsIstanbul(num),
		IsBerlin:         c.IsBerlin(num),
		IsLondon:         c.IsLondon(num),
		IsCatalyst:       c.IsCatalyst(num),
		IsFuller:         c.IsFuller(num),
	}
}

// bytePrefixList expands the space with proper size for chain IDs to be indexed
var (
	mainnetBytePrefixList = make([][]int, 20000)
	testnetBytePrefixList = make([][]int, 20000)
)

func init() {
	mainnetBytePrefixList[9000] = []int{0, 9}
	mainnetBytePrefixList[9100] = []int{10, 19}
	mainnetBytePrefixList[9101] = []int{20, 29}
	mainnetBytePrefixList[9102] = []int{30, 39}
	mainnetBytePrefixList[9103] = []int{40, 49}
	mainnetBytePrefixList[9200] = []int{50, 59}
	mainnetBytePrefixList[9201] = []int{60, 69}
	mainnetBytePrefixList[9202] = []int{70, 79}
	mainnetBytePrefixList[9203] = []int{80, 89}
	mainnetBytePrefixList[9300] = []int{90, 99}
	mainnetBytePrefixList[9301] = []int{100, 109}
	mainnetBytePrefixList[9302] = []int{110, 119}
	mainnetBytePrefixList[9303] = []int{120, 129}

	testnetBytePrefixList[12000] = []int{0, 9}
	testnetBytePrefixList[12100] = []int{10, 19}
	testnetBytePrefixList[12101] = []int{20, 29}
	testnetBytePrefixList[12102] = []int{30, 39}
	testnetBytePrefixList[12103] = []int{40, 49}
	testnetBytePrefixList[12200] = []int{50, 59}
	testnetBytePrefixList[12201] = []int{60, 69}
	testnetBytePrefixList[12202] = []int{70, 79}
	testnetBytePrefixList[12203] = []int{80, 89}
	testnetBytePrefixList[12300] = []int{90, 99}
	testnetBytePrefixList[12301] = []int{100, 109}
	testnetBytePrefixList[12302] = []int{110, 119}
	testnetBytePrefixList[12303] = []int{120, 129}
}

// ChainIDRange returns the byte lookup based off a configs chainID
func (c *ChainConfig) ChainIDRange() []int {
	for _, inSet := range mainnetValidChains {
		if c.ChainID.Cmp(inSet) == 0 {
			lookup := mainnetBytePrefixList[c.ChainID.Int64()]
			return lookup
		}
	}

	for _, inSet := range testnetValidChains {
		if c.ChainID.Cmp(inSet) == 0 {
			lookup := testnetBytePrefixList[c.ChainID.Int64()]
			return lookup
		}
	}
	return nil
}

// LookupChainIDRange returns the byte lookup based off a configs chainID
func LookupChainIDRange(index *big.Int) []int {
	for _, inSet := range mainnetValidChains {
		if index.Cmp(inSet) == 0 {
			lookup := mainnetBytePrefixList[index.Int64()]
			return lookup
		}
	}

	for _, inSet := range testnetValidChains {
		if index.Cmp(inSet) == 0 {
			lookup := testnetBytePrefixList[index.Int64()]
			return lookup
		}
	}
	return nil
}

// ValidChainID takes in a chain ID and checks against the valid list
func ValidChainID(id *big.Int, chainId *big.Int) bool {
	for _, inSet := range mainnetValidChains {
		if chainId.Cmp(inSet) == 0 {
			for _, valid := range mainnetValidChains {
				if id.Cmp(valid) == 0 {
					return true
				}
			}
		}
	}

	for _, inSet := range testnetValidChains {
		if chainId.Cmp(inSet) == 0 {
			for _, valid := range testnetValidChains {
				if id.Cmp(valid) == 0 {
					return true
				}
			}
		}
	}
	return false
}

// CheckETxChainID takes in a chain ID and checks against the valid list
func CheckETxChainID(ourID *big.Int, txID *big.Int) bool {
	for _, inSet := range mainnetValidChains {
		if ourID.Cmp(inSet) == 0 {
			for _, valid := range mainnetValidChains {
				if txID.Cmp(valid) == 0 {
					return true
				}
			}
		}
	}

	for _, inSet := range testnetValidChains {
		if ourID.Cmp(inSet) == 0 {
			for _, valid := range testnetValidChains {
				if txID.Cmp(valid) == 0 {
					return true
				}
			}
		}
	}
	return false
}

// CurrentOntology is used to retrieve the MapContext of a given block.
func (c *ChainConfig) CurrentOntology(number []*big.Int) ([]int, error) {
	forkNumber := number[0]

	switch {
	case forkNumber.Cmp(c.FullerMapContext) >= 0: // Fuller = 0
		return FullerOntology, nil
	default:
		return nil, errors.New("invalid block number passed to ontology")
	}
}
