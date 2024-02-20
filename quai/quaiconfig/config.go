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

// Package quaiconfig contains the configuration of the ETH and LES protocols.
package quaiconfig

import (
	"math/big"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/blake3pow"
	"github.com/dominant-strategies/go-quai/consensus/progpow"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/node"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai/gasprice"
)

type QuaistatsConfig struct {
	URL string `toml:",omitempty"`
}

type QuaiConfig struct {
	Quai      Config
	Node      node.Config
	Quaistats QuaistatsConfig
	Metrics   metrics_config.Config
}

// FullNodeGPO contains default gasprice oracle settings for full node.
var FullNodeGPO = gasprice.Config{
	Blocks:           20,
	Percentile:       60,
	MaxHeaderHistory: 0,
	MaxBlockHistory:  0,
	MaxPrice:         gasprice.DefaultMaxPrice,
	IgnorePrice:      gasprice.DefaultIgnorePrice,
}

// LightClientGPO contains default gasprice oracle settings for light client.
var LightClientGPO = gasprice.Config{
	Blocks:           2,
	Percentile:       60,
	MaxHeaderHistory: 300,
	MaxBlockHistory:  5,
	MaxPrice:         gasprice.DefaultMaxPrice,
	IgnorePrice:      gasprice.DefaultIgnorePrice,
}

// Defaults contains default settings for use on the Quai main net.
var Defaults = Config{
	Progpow:                   progpow.Config{},
	NetworkId:                 1,
	TxLookupLimit:             2350000,
	DatabaseCache:             512,
	TrieCleanCache:            154,
	TrieCleanCacheJournal:     "triecache",
	UTXOTrieCleanCacheJournal: "utxotriecache",
	TrieCleanCacheRejournal:   60 * time.Minute,
	TrieDirtyCache:            256,
	TrieTimeout:               60 * time.Minute,
	SnapshotCache:             102,
	Miner: core.Config{
		GasCeil:  18000000,
		GasPrice: big.NewInt(params.GWei),
		Recommit: 3 * time.Second,
	},
	TxPool:      core.DefaultTxPoolConfig,
	RPCGasCap:   50000000,
	GPO:         FullNodeGPO,
	RPCTxFeeCap: 1, // 1 ether
	DomUrl:      "ws://127.0.0.1:8546",
	SubUrls:     []string{"ws://127.0.0.1:8546", "ws://127.0.0.1:8546", "ws://127.0.0.1:8546"},
}

//go:generate gencodec -type Config -formats toml -out gen_config.go

// Config contains configuration options for of the ETH and LES protocols.
type Config struct {
	// The genesis block, which is inserted if the database is empty.
	// If nil, the Quai main net block is used.
	Genesis *core.Genesis `toml:",omitempty"`

	// Protocol options
	NetworkId uint64 // Network ID to use for selecting peers to connect to

	// This can be set to list of enrtree:// URLs which will be queried for
	// for nodes to connect to.
	EthDiscoveryURLs  []string
	SnapDiscoveryURLs []string

	NoPruning  bool // Whether to disable pruning and flush everything to disk
	NoPrefetch bool // Whether to disable prefetching and only load state on demand

	TxLookupLimit uint64 `toml:",omitempty"` // The maximum number of blocks from head whose tx indices are reserved.

	// Whitelist of required block number -> hash values to accept
	Whitelist map[uint64]common.Hash `toml:"-"`

	// Database options
	SkipBcVersionCheck bool `toml:"-"`
	DatabaseHandles    int  `toml:"-"`
	DatabaseCache      int
	DatabaseFreezer    string

	TrieCleanCache            int
	TrieCleanCacheJournal     string        `toml:",omitempty"` // Disk journal directory for trie cache to survive node restarts
	UTXOTrieCleanCacheJournal string        `toml:",omitempty"` // Disk journal directory for trie cache to survive node restarts
	TrieCleanCacheRejournal   time.Duration `toml:",omitempty"` // Time interval to regenerate the journal for clean cache
	TrieDirtyCache            int
	TrieTimeout               time.Duration
	SnapshotCache             int
	Preimages                 bool

	// Mining options
	Miner core.Config

	// Consensus Engine
	ConsensusEngine string

	// Progpow options
	Progpow progpow.Config

	// Blake3 options
	Blake3Pow blake3pow.Config

	// Transaction pool options
	TxPool core.TxPoolConfig

	// Gas Price Oracle options
	GPO gasprice.Config

	// Enables tracking of SHA3 preimages in the VM
	EnablePreimageRecording bool

	// Miscellaneous options
	DocRoot string `toml:"-"`

	// RPCGasCap is the global gas cap for eth-call variants.
	RPCGasCap uint64

	// RPCTxFeeCap is the global transaction fee(price * gaslimit) cap for
	// send-transction variants. The unit is ether.
	RPCTxFeeCap float64

	// Region location options
	Region int

	// Zone location options
	Zone int

	// Dom node websocket url
	DomUrl string

	// Sub node websocket urls
	SubUrls []string

	// Slices running on the node
	SlicesRunning []common.Location

	// NodeLocation of the node
	NodeLocation common.Location

	// IndexAddressUtxos enables or disables address utxo indexing
	IndexAddressUtxos bool
}

// CreateProgpowConsensusEngine creates a progpow consensus engine for the given chain configuration.
func CreateProgpowConsensusEngine(stack *node.Node, nodeLocation common.Location, config *progpow.Config, notify []string, noverify bool, db ethdb.Database, logger *log.Logger) consensus.Engine {
	// Otherwise assume proof-of-work
	switch config.PowMode {
	case progpow.ModeFake:
		logger.Warn("Progpow used in fake mode")
	case progpow.ModeTest:
		logger.Warn("Progpow used in test mode")
	case progpow.ModeShared:
		logger.Warn("Progpow used in shared mode")
	}
	engine := progpow.New(progpow.Config{
		PowMode:       config.PowMode,
		NotifyFull:    config.NotifyFull,
		DurationLimit: config.DurationLimit,
		NodeLocation:  nodeLocation,
		GasCeil:       config.GasCeil,
		MinDifficulty: config.MinDifficulty,
	}, notify, noverify, logger)
	engine.SetThreads(-1) // Disable CPU mining
	return engine
}

// CreateBlake3ConsensusEngine creates a progpow consensus engine for the given chain configuration.
func CreateBlake3ConsensusEngine(stack *node.Node, nodeLocation common.Location, config *blake3pow.Config, notify []string, noverify bool, db ethdb.Database, logger *log.Logger) consensus.Engine {
	// Otherwise assume proof-of-work
	switch config.PowMode {
	case blake3pow.ModeFake:
		logger.Warn("Progpow used in fake mode")
	case blake3pow.ModeTest:
		logger.Warn("Progpow used in test mode")
	case blake3pow.ModeShared:
		logger.Warn("Progpow used in shared mode")
	}
	engine := blake3pow.New(blake3pow.Config{
		PowMode:       config.PowMode,
		NotifyFull:    config.NotifyFull,
		DurationLimit: config.DurationLimit,
		NodeLocation:  nodeLocation,
		GasCeil:       config.GasCeil,
		MinDifficulty: config.MinDifficulty,
	}, notify, noverify, logger)
	engine.SetThreads(-1) // Disable CPU mining
	return engine
}
