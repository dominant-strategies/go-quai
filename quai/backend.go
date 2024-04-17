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
	"github.com/dominant-strategies/go-quai/quai/gasprice"
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
	engine   consensus.Engine

	bloomRequests     chan chan *bloombits.Retrieval // Channel receiving bloom data retrieval requests
	bloomIndexer      *core.ChainIndexer             // Bloom indexer operating during block imports
	closeBloomHandler chan struct{}

	APIBackend *QuaiAPIBackend

	gasPrice  *big.Int
	etherbase common.Address

	lock sync.RWMutex // Protects the variadic fields (e.g. gas price and etherbase)

	logger *log.Logger
}

// New creates a new Quai object (including the
// initialisation of the common Quai object)
func New(stack *node.Node, p2p NetworkingAPI, config *quaiconfig.Config, nodeCtx int, currentExpansionNumber uint8, startingExpansionNumber uint64, genesisBlock *types.WorkObject, logger *log.Logger) (*Quai, error) {
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
		chainConfig, _, genesisErr = core.SetupGenesisBlockWithOverride(chainDb, config.Genesis, config.NodeLocation, startingExpansionNumber, logger)
		if genesisErr != nil {
			return nil, genesisErr
		}
	} else {
		if (config.NodeLocation.Context() == common.PRIME_CTX) ||
			(config.NodeLocation.Region() == 0 && nodeCtx == common.REGION_CTX) ||
			(bytes.Equal(config.NodeLocation, common.Location{0, 0}) && nodeCtx == common.ZONE_CTX) {
			var genesisErr error
			chainConfig, _, genesisErr = core.SetupGenesisBlockWithOverride(chainDb, config.Genesis, config.NodeLocation, startingExpansionNumber, logger)
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
		etherbase:         config.Miner.Etherbase,
		bloomRequests:     make(chan chan *bloombits.Retrieval),
		logger:            logger,
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
	logger.WithFields(log.Fields{
		"Ctx":          nodeCtx,
		"NodeLocation": config.NodeLocation,
		"Genesis":      config.Genesis.Config.Location,
		"chainConfig":  chainConfig.Location,
	}).Info("Node")
	if config.ConsensusEngine == "blake3" {
		blake3Config := config.Blake3Pow
		blake3Config.NotifyFull = config.Miner.NotifyFull
		blake3Config.NodeLocation = config.NodeLocation
		quai.engine = quaiconfig.CreateBlake3ConsensusEngine(stack, config.NodeLocation, &blake3Config, config.Miner.Notify, config.Miner.Noverify, chainDb, logger)
	} else {
		// Transfer mining-related config to the progpow config.
		progpowConfig := config.Progpow
		progpowConfig.NodeLocation = config.NodeLocation
		progpowConfig.NotifyFull = config.Miner.NotifyFull
		quai.engine = quaiconfig.CreateProgpowConsensusEngine(stack, config.NodeLocation, &progpowConfig, config.Miner.Notify, config.Miner.Noverify, chainDb, logger)
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
			TrieCleanLimit:       config.TrieCleanCache,
			TrieCleanJournal:     stack.ResolvePath(config.TrieCleanCacheJournal),
			UTXOTrieCleanJournal: stack.ResolvePath(config.UTXOTrieCleanCacheJournal),
			TrieCleanRejournal:   config.TrieCleanCacheRejournal,
			TrieCleanNoPrefetch:  config.NoPrefetch,
			TrieDirtyLimit:       config.TrieDirtyCache,
			TrieTimeLimit:        config.TrieTimeout,
			SnapshotLimit:        config.SnapshotCache,
			Preimages:            config.Preimages,
		}
	)

	var (
		indexerConfig = &core.IndexerConfig{
			IndexAddressUtxos: config.IndexAddressUtxos,
		}
	)

	if config.TxPool.Journal != "" {
		config.TxPool.Journal = stack.ResolvePath(config.TxPool.Journal)
	}

	logger.WithField("url", quai.config.DomUrl).Info("Dom client")
	quai.core, err = core.NewCore(chainDb, &config.Miner, quai.isLocalBlock, &config.TxPool, &config.TxLookupLimit, chainConfig, quai.config.SlicesRunning, currentExpansionNumber, genesisBlock, quai.config.DomUrl, quai.config.SubUrls, quai.engine, cacheConfig, vmConfig, indexerConfig, config.Genesis, logger)
	if err != nil {
		return nil, err
	}

	// Only index bloom if processing state
	if quai.core.ProcessingState() && nodeCtx == common.ZONE_CTX {
		quai.bloomIndexer = core.NewBloomIndexer(chainDb, params.BloomBitsBlocks, params.BloomConfirms, chainConfig.Location.Context(), logger)
		quai.bloomIndexer.Start(quai.Core().Slice().HeaderChain())
	}

	// Set the p2p Networking API
	quai.p2p = p2p

	quai.handler = newHandler(quai.p2p, quai.core, config.NodeLocation, logger)
	// Start the handler
	quai.handler.Start()

	quai.APIBackend = &QuaiAPIBackend{stack.Config().ExtRPCEnabled(), quai, nil}
	// Gasprice oracle is only initiated in zone chains
	if nodeCtx == common.ZONE_CTX && quai.core.ProcessingState() {
		gpoParams := config.GPO
		if gpoParams.Default == nil {
			gpoParams.Default = config.Miner.GasPrice
		}
		quai.APIBackend.gpo = gasprice.NewOracle(quai.APIBackend, gpoParams, logger)
	}

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
			Service:   filters.NewPublicFilterAPI(s.APIBackend, 5*time.Minute),
			Public:    true,
		}, {
			Namespace: "quai",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.APIBackend, 5*time.Minute),
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

func (s *Quai) Etherbase() (eb common.Address, err error) {
	s.lock.RLock()
	etherbase := s.etherbase
	s.lock.RUnlock()

	if !etherbase.Equal(common.Zero) {
		return etherbase, nil
	}

	return common.Zero, fmt.Errorf("etherbase must be explicitly specified")
}

// isLocalBlock checks whether the specified block is mined
// by local miner accounts.
//
// We regard two types of accounts as local miner account: etherbase
// and accounts specified via `txpool.locals` flag.
func (s *Quai) isLocalBlock(header *types.WorkObject) bool {
	author, err := s.engine.Author(header)
	if err != nil {
		s.logger.WithFields(log.Fields{
			"number": header.NumberU64(s.core.NodeCtx()),
			"hash":   header.Hash(),
			"err":    err,
		}).Warn("Failed to retrieve block author")
		return false
	}
	// Check whether the given address is etherbase.
	s.lock.RLock()
	etherbase := s.etherbase
	s.lock.RUnlock()
	if author.Equal(etherbase) {
		return true
	}
	internal, err := author.InternalAddress()
	if err != nil {
		s.logger.WithField("err", err).Error("Failed to retrieve author internal address")
	}
	// Check whether the given address is specified by `txpool.local`
	// CLI flag.
	for _, account := range s.config.TxPool.Locals {
		if account == internal {
			return true
		}
	}
	return false
}

// shouldPreserve checks whether we should preserve the given block
// during the chain reorg depending on whether the author of block
// is a local account.
func (s *Quai) shouldPreserve(block *types.WorkObject) bool {
	return s.isLocalBlock(block)
}

func (s *Quai) Core() *core.Core                 { return s.core }
func (s *Quai) EventMux() *event.TypeMux         { return s.eventMux }
func (s *Quai) Engine() consensus.Engine         { return s.engine }
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

// Stop implements node.Lifecycle, terminating all internal goroutines used by the
// Quai protocol.
func (s *Quai) Stop() error {

	if s.core.ProcessingState() && s.core.NodeCtx() == common.ZONE_CTX {
		// Then stop everything else.
		s.bloomIndexer.Close()
		close(s.closeBloomHandler)
	}
	s.core.Stop()
	s.chainDb.Close()
	s.eventMux.Stop()
	s.handler.Stop()

	return nil
}
