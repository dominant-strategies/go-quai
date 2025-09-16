// Copyright 2014 The go-ethereum Authors
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

// Package quai implements the Quai protocol.
package quai

import (
	"bytes"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/bloombits"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state/pruner"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/node"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai/filters"
	"github.com/dominant-strategies/go-quai/quai/quaiconfig"
	"github.com/dominant-strategies/go-quai/rpc"
)

// Config contains the configuration options of the ETH protocol.
// Deprecated: use ethconfig.Config instead.
type Config = quaiconfig.Config

// Quai implements the Quai full node service.
type Quai struct {
	config *quaiconfig.Config

	// Handlers
	core *core.Core

	p2p NetworkingAPI

	handler *handler

	// DB interfaces
	chainDb ethdb.Database // Block chain database

	eventMux *event.TypeMux
	engine   []consensus.Engine

	bloomRequests     chan chan *bloombits.Retrieval // Channel receiving bloom data retrieval requests
	bloomIndexer      *core.ChainIndexer             // Bloom indexer operating during block imports
	closeBloomHandler chan struct{}

	APIBackend *QuaiAPIBackend

	quaiCoinbase common.Address
	qiCoinbase   common.Address

	gasPrice *big.Int

	lock sync.RWMutex // Protects the variadic fields (e.g. gas price and etherbase)

	logger    *log.Logger
	maxWsSubs int

	rpcVersion string
}

// New creates a new Quai object (including the
// initialisation of the common Quai object)
func New(stack *node.Node, p2p NetworkingAPI, config *quaiconfig.Config, nodeCtx int, currentExpansionNumber uint8, startingExpansionNumber uint64, genesisBlock *types.WorkObject, logger *log.Logger, maxWsSubs int) (*Quai, error) {
	// Ensure configuration values are compatible and sane
	if config.Miner.GasPrice == nil || config.Miner.GasPrice.Cmp(common.Big0) <= 0 {
		logger.WithFields(log.Fields{
			"provided": config.Miner.GasPrice,
			"updated":  quaiconfig.Defaults.Miner.GasPrice,
		}).Warn("Sanitizing invalid miner gas price")
		config.Miner.GasPrice = new(big.Int).Set(quaiconfig.Defaults.Miner.GasPrice)
	}
	if config.NoPruning && config.TrieDirtyCache > 0 {
		if config.SnapshotCache > 0 {
			config.TrieCleanCache += config.TrieDirtyCache * 3 / 5
			config.SnapshotCache += config.TrieDirtyCache * 2 / 5
		} else {
			config.TrieCleanCache += config.TrieDirtyCache
		}
		config.TrieDirtyCache = 0
	}
	logger.WithFields(log.Fields{
		"clean": common.StorageSize(config.TrieCleanCache) * 1024 * 1024,
		"dirty": common.StorageSize(config.TrieDirtyCache) * 1024 * 1024,
	}).Info("Allocated trie memory caches")

	// Assemble the Quai object
	chainDb, err := stack.OpenDatabaseWithFreezer("chaindata", config.DatabaseCache, config.DatabaseHandles, config.DatabaseFreezer, "eth/db/chaindata/", false, config.NodeLocation)
	if err != nil {
		return nil, err
	}
	// Only run the genesis block setup for Prime and region-0 and zone-0-0, for everything else it is setup through the expansion trigger
	chainConfig := config.Genesis.Config
	// This is not the normal protocol start, starting the protocol at a
	// different expansion number is only to run experiments
	if startingExpansionNumber > 0 {
		var genesisErr error
		chainConfig, _, genesisErr = core.SetupGenesisBlockWithOverride(chainDb, config.Genesis, config.GenesisNonce, config.GenesisExtra, config.NodeLocation, startingExpansionNumber, logger)
		if genesisErr != nil {
			return nil, genesisErr
		}
	} else {
		if (config.NodeLocation.Context() == common.PRIME_CTX) ||
			(config.NodeLocation.Region() == 0 && nodeCtx == common.REGION_CTX) ||
			(bytes.Equal(config.NodeLocation, common.Location{0, 0}) && nodeCtx == common.ZONE_CTX) {
			var genesisErr error
			chainConfig, _, genesisErr = core.SetupGenesisBlockWithOverride(chainDb, config.Genesis, config.GenesisNonce, config.GenesisExtra, config.NodeLocation, startingExpansionNumber, logger)
			if genesisErr != nil {
				return nil, genesisErr
			}
		} else {
			// This only happens during the expansion
			if genesisBlock != nil {
				// write the block to the database
				rawdb.WriteWorkObject(chainDb, genesisBlock.Hash(), genesisBlock, types.BlockObject, nodeCtx)
				rawdb.WriteHeadBlockHash(chainDb, genesisBlock.Hash())
				// Initialize slice state for genesis knot
				genesisTermini := types.EmptyTermini()
				for i := 0; i < len(genesisTermini.SubTermini()); i++ {
					genesisTermini.SetSubTerminiAtIndex(genesisBlock.Hash(), i)
				}
				for i := 0; i < len(genesisTermini.DomTermini()); i++ {
					genesisTermini.SetDomTerminiAtIndex(genesisBlock.Hash(), i)
				}
				rawdb.WriteTermini(chainDb, genesisBlock.Hash(), genesisTermini)
			}
		}
	}

	logger.WithField("location", &chainConfig).Warn("Memory location of chainConfig")

	if err := pruner.RecoverPruning(stack.ResolvePath(""), chainDb, stack.ResolvePath(config.TrieCleanCacheJournal), config.NodeLocation, logger); err != nil {
		logger.WithField("err", err).Error("Failed to recover state")
	}
	quai := &Quai{
		config:            config,
		chainDb:           chainDb,
		eventMux:          stack.EventMux(),
		closeBloomHandler: make(chan struct{}),
		gasPrice:          config.Miner.GasPrice,
		quaiCoinbase:      config.Miner.QuaiCoinbase,
		qiCoinbase:        config.Miner.QiCoinbase,
		bloomRequests:     make(chan chan *bloombits.Retrieval),
		logger:            logger,
		maxWsSubs:         maxWsSubs,
		rpcVersion:        config.RpcVersion,
	}

	// Copy the chainConfig
	newChainConfig := params.ChainConfig{
		ChainID:         chainConfig.ChainID,
		ConsensusEngine: chainConfig.ConsensusEngine,
		Blake3Pow:       chainConfig.Blake3Pow,
		Progpow:         chainConfig.Progpow,
		Location:        chainConfig.Location,
	}
	chainConfig = &newChainConfig

	logger.WithField("chainConfig", config.NodeLocation).Info("Chain Config")
	chainConfig.Location = config.NodeLocation // TODO: See why this is necessary
	chainConfig.DefaultGenesisHash = config.DefaultGenesisHash
	chainConfig.IndexAddressUtxos = config.IndexAddressUtxos
	chainConfig.TelemetryEnabled = config.TelemetryEnabled
	logger.WithFields(log.Fields{
		"Ctx":          nodeCtx,
		"NodeLocation": config.NodeLocation,
		"Genesis":      config.Genesis.Config.Location,
		"chainConfig":  chainConfig.Location,
	}).Info("Node")

	// powConfig holds config for pow
	powConfig := config.PowConfig
	powConfig.NodeLocation = config.NodeLocation
	powConfig.NotifyFull = config.Miner.NotifyFull
	powConfig.GenAllocs = config.GenesisAllocs

	if config.ConsensusEngine == "blake3" {
		quai.engine = make([]consensus.Engine, 1)
		quai.engine[0] = quaiconfig.CreateBlake3ConsensusEngine(stack, config.NodeLocation, &powConfig, config.Miner.Notify, config.Miner.Noverify, config.Miner.WorkShareThreshold, chainDb, logger)
	} else {
		quai.engine = make([]consensus.Engine, params.TotalPowEngines)
		quai.engine[types.Progpow] = quaiconfig.CreateProgpowConsensusEngine(stack, config.NodeLocation, &powConfig, config.Miner.Notify, config.Miner.Noverify, chainDb, logger)
		quai.engine[types.Kawpow] = quaiconfig.CreateKawPowConsensusEngine(stack, config.NodeLocation, &powConfig, config.Miner.Notify, config.Miner.Noverify, chainDb, logger)
	}
	logger.WithField("config", config).Info("Initialized chain configuration")

	bcVersion := rawdb.ReadDatabaseVersion(chainDb)
	var dbVer = "<nil>"
	if bcVersion != nil {
		dbVer = fmt.Sprintf("%d", *bcVersion)
	}

	logger.WithFields(log.Fields{
		"network":   config.NetworkId,
		"dbversion": dbVer,
	}).Info("Initialising Quai protocol")

	if !config.SkipBcVersionCheck {
		if bcVersion != nil && *bcVersion > core.BlockChainVersion {
			return nil, fmt.Errorf("database version is v%d, Quai %s only supports v%d", *bcVersion, params.Version.Full(), core.BlockChainVersion)
		} else if bcVersion == nil || *bcVersion < core.BlockChainVersion {
			if bcVersion != nil { // only print warning on upgrade, not on init
				logger.WithFields(log.Fields{
					"from": dbVer,
					"to":   core.BlockChainVersion,
				}).Warn("Upgrading database version")
			}
			rawdb.WriteDatabaseVersion(chainDb, core.BlockChainVersion)
		}
	}
	var (
		vmConfig = vm.Config{
			EnablePreimageRecording: config.EnablePreimageRecording,
		}
		cacheConfig = &core.CacheConfig{
			TrieCleanLimit:      config.TrieCleanCache,
			TrieCleanJournal:    stack.ResolvePath(config.TrieCleanCacheJournal),
			ETXTrieCleanJournal: stack.ResolvePath(config.ETXTrieCleanCacheJournal),
			TrieCleanRejournal:  config.TrieCleanCacheRejournal,
			TrieCleanNoPrefetch: config.NoPrefetch,
			TrieDirtyLimit:      config.TrieDirtyCache,
			TrieTimeLimit:       config.TrieTimeout,
			SnapshotLimit:       config.SnapshotCache,
			Preimages:           config.Preimages,
		}
	)

	if config.TxPool.Journal != "" {
		config.TxPool.Journal = stack.ResolvePath(config.TxPool.Journal)
	}

	quai.core, err = core.NewCore(chainDb, &config.Miner, powConfig, &config.TxPool, &config.TxLookupLimit, chainConfig, quai.config.SlicesRunning, currentExpansionNumber, genesisBlock, quai.engine, cacheConfig, vmConfig, config.Genesis, logger)
	if err != nil {
		return nil, err
	}

	// Only index bloom if processing state
	if quai.core.ProcessingState() && nodeCtx == common.ZONE_CTX {
		quai.bloomIndexer = core.NewBloomIndexer(chainDb, params.BloomBitsBlocks, params.BloomConfirms, chainConfig.Location.Context(), logger, config.IndexAddressUtxos)
		quai.bloomIndexer.Start(quai.Core().Slice().HeaderChain(), newChainConfig)
	}

	// Set the p2p Networking API
	quai.p2p = p2p

	quai.handler = newHandler(quai.p2p, quai.core, config.NodeLocation, logger)
	// Start the handler
	quai.handler.Start()

	quai.APIBackend = &QuaiAPIBackend{stack.Config().ExtRPCEnabled(), quai}

	// Register the backend on the node
	stack.RegisterAPIs(quai.APIs())
	stack.RegisterLifecycle(quai)
	// Check for unclean shutdown
	return quai, nil
}

// APIs return the collection of RPC services the go-quai package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *Quai) APIs() []rpc.API {
	apis := quaiapi.GetAPIs(s.APIBackend)

	// Append all the local APIs and return
	return append(apis, []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicQuaiAPI(s),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicMinerAPI(s),
			Public:    true,
		}, {
			Namespace: "miner",
			Version:   "1.0",
			Service:   NewPrivateMinerAPI(s),
			Public:    false,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.APIBackend, 5*time.Minute, s.maxWsSubs),
			Public:    true,
		}, {
			Namespace: "quai",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.APIBackend, 5*time.Minute, s.maxWsSubs),
			Public:    true,
		}, {
			Namespace: "admin",
			Version:   "1.0",
			Service:   NewPrivateAdminAPI(s),
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPublicDebugAPI(s),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPrivateDebugAPI(s),
		},
	}...)
}

func (s *Quai) Core() *core.Core         { return s.core }
func (s *Quai) EventMux() *event.TypeMux { return s.eventMux }
func (s *Quai) Engine(header *types.WorkObjectHeader) consensus.Engine {
	// Return the default engine (Progpow)
	return s.core.GetEngineForHeader(header)
}
func (s *Quai) ChainDb() ethdb.Database          { return s.chainDb }
func (s *Quai) IsListening() bool                { return true } // Always listening
func (s *Quai) ArchiveMode() bool                { return s.config.NoPruning }
func (s *Quai) BloomIndexer() *core.ChainIndexer { return s.bloomIndexer }

// Start implements node.Lifecycle, starting all internal goroutines needed by the
// Quai protocol implementation.
func (s *Quai) Start() error {

	if s.core.ProcessingState() && s.core.NodeCtx() == common.ZONE_CTX {
		// Start the bloom bits servicing goroutines
		s.startBloomHandlers(params.BloomBitsBlocks)
	}

	return nil
}

func (s *Quai) SetWorkShareP2PThreshold(threshold int) {
	s.config.WorkShareP2PThreshold = threshold
}

// Stop implements node.Lifecycle, terminating all internal goroutines used by the
// Quai protocol.
func (s *Quai) Stop() error {

	if s.core.ProcessingState() && s.core.NodeCtx() == common.ZONE_CTX {
		// Then stop everything else.
		s.bloomIndexer.Close()
		close(s.closeBloomHandler)
	}
	s.handler.Stop()
	s.core.Stop()
	s.chainDb.Close()
	s.eventMux.Stop()

	return nil
}
