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

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/big"
	"runtime/debug"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/prque"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/state/snapshot"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/multiset"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
)

const (
	receiptsCacheLimit = 32
	txLookupCacheLimit = 1024
	TriesInMemory      = 128

	// BlockChainVersion ensures that an incompatible database forces a resync from scratch.
	//
	// Changelog:
	//
	// - Version 4
	//   The following incompatible database changes were added:
	//   * the `BlockNumber`, `TxHash`, `TxIndex`, `BlockHash` and `Index` fields of log are deleted
	//   * the `Bloom` field of receipt is deleted
	//   * the `BlockIndex` and `TxIndex` fields of txlookup are deleted
	// - Version 5
	//  The following incompatible database changes were added:
	//    * the `TxHash`, `GasCost`, and `ContractAddress` fields are no longer stored for a receipt
	//    * the `TxHash`, `GasCost`, and `ContractAddress` fields are computed by looking up the
	//      receipts' corresponding block
	// - Version 6
	//  The following incompatible database changes were added:
	//    * Transaction lookup information stores the corresponding block number instead of block hash
	// - Version 7
	//  The following incompatible database changes were added:
	//    * Use freezer as the ancient database to maintain all ancient data
	// - Version 8
	//  The following incompatible database changes were added:
	//    * New scheme for contract code in order to separate the codes and trie nodes
	BlockChainVersion uint64 = 8
)

// CacheConfig contains the configuration values for the trie caching/pruning
// that's resident in a blockchain.
type CacheConfig struct {
	TrieCleanLimit      int    // Memory allowance (MB) to use for caching trie nodes in memory
	TrieCleanJournal    string // Disk journal for saving clean cache entries.
	ETXTrieCleanJournal string
	TrieCleanRejournal  time.Duration // Time interval to dump clean cache to disk periodically
	TrieCleanNoPrefetch bool          // Whether to disable heuristic state prefetching for followup blocks
	TrieDirtyLimit      int           // Memory limit (MB) at which to start flushing dirty trie nodes to disk
	TrieTimeLimit       time.Duration // Time limit after which to flush the current in-memory trie to disk
	SnapshotLimit       int           // Memory allowance (MB) to use for caching snapshot entries in memory
	Preimages           bool          // Whether to store preimage of trie key to the disk
}

// defaultCacheConfig are the default caching values if none are specified by the
// user (also used during testing).
var defaultCacheConfig = &CacheConfig{
	TrieCleanLimit: 256,
	TrieDirtyLimit: 256,
	TrieTimeLimit:  5 * time.Minute,
	SnapshotLimit:  256,
}

// StateProcessor is a basic Processor, which takes care of transitioning
// state from one point to another.
//
// StateProcessor implements Processor.
type StateProcessor struct {
	config        *params.ChainConfig // Chain configuration options
	hc            *HeaderChain        // Canonical block chain
	engine        consensus.Engine    // Consensus engine used for block rewards
	logsFeed      event.Feed
	rmLogsFeed    event.Feed
	cacheConfig   *CacheConfig                            // CacheConfig for StateProcessor
	stateCache    state.Database                          // State database to reuse between imports (contains state cache)
	etxCache      state.Database                          // ETX database to reuse between imports (contains ETX cache)
	receiptsCache *lru.Cache[common.Hash, types.Receipts] // Cache for the most recent receipts per block
	txLookupCache *lru.Cache[common.Hash, rawdb.LegacyTxLookupEntry]
	validator     Validator // Block and state validator interface
	prefetcher    Prefetcher
	vmConfig      vm.Config

	scope         event.SubscriptionScope
	wg            sync.WaitGroup // chain processing wait group for shutting down
	quit          chan struct{}  // state processor quit channel
	txLookupLimit uint64

	snaps  *snapshot.Tree
	triegc *prque.Prque  // Priority queue mapping block numbers to tries to gc
	gcproc time.Duration // Accumulates canonical block processing for trie dumping
	logger *log.Logger
}

// NewStateProcessor initialises a new StateProcessor.
func NewStateProcessor(config *params.ChainConfig, hc *HeaderChain, engine consensus.Engine, vmConfig vm.Config, cacheConfig *CacheConfig, txLookupLimit *uint64) *StateProcessor {

	if cacheConfig == nil {
		cacheConfig = defaultCacheConfig
	}

	sp := &StateProcessor{
		config:      config,
		hc:          hc,
		vmConfig:    vmConfig,
		cacheConfig: cacheConfig,
		stateCache: state.NewDatabaseWithConfig(hc.headerDb, &trie.Config{
			Cache:     cacheConfig.TrieCleanLimit,
			Journal:   cacheConfig.TrieCleanJournal,
			Preimages: cacheConfig.Preimages,
		}),
		etxCache: state.NewDatabaseWithConfig(hc.headerDb, &trie.Config{
			Cache:     cacheConfig.TrieCleanLimit,
			Journal:   cacheConfig.ETXTrieCleanJournal,
			Preimages: cacheConfig.Preimages,
		}),
		engine: engine,
		triegc: prque.New(nil),
		quit:   make(chan struct{}),
		logger: hc.logger,
	}
	sp.validator = NewBlockValidator(config, hc, engine)

	receiptsCache, _ := lru.New[common.Hash, types.Receipts](receiptsCacheLimit)
	sp.receiptsCache = receiptsCache

	txLookupCache, _ := lru.New[common.Hash, rawdb.LegacyTxLookupEntry](txLookupCacheLimit)
	sp.txLookupCache = txLookupCache

	// Load any existing snapshot, regenerating it if loading failed
	if sp.cacheConfig.SnapshotLimit > 0 {
		// TODO: If the state is not available, enable snapshot recovery
		head := hc.CurrentHeader()
		sp.snaps, _ = snapshot.New(hc.headerDb, sp.stateCache.TrieDB(), sp.cacheConfig.SnapshotLimit, head.EVMRoot(), true, false, sp.logger)
	}
	if txLookupLimit != nil {
		sp.txLookupLimit = *txLookupLimit
	}
	// If periodic cache journal is required, spin it up.
	if sp.cacheConfig.TrieCleanRejournal > 0 {
		if sp.cacheConfig.TrieCleanRejournal < time.Minute {
			sp.logger.WithFields(log.Fields{
				"provided": sp.cacheConfig.TrieCleanRejournal,
				"updated":  time.Minute,
			}).Warn("Sanitizing invalid trie cache journal time")
			sp.cacheConfig.TrieCleanRejournal = time.Minute
		}
		triedb := sp.stateCache.TrieDB()
		etxTrieDb := sp.etxCache.TrieDB()
		sp.wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					hc.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Error("Go-Quai Panicked")
				}
			}()
			defer sp.wg.Done()
			triedb.SaveCachePeriodically(sp.cacheConfig.TrieCleanJournal, sp.cacheConfig.TrieCleanRejournal, sp.quit)
			etxTrieDb.SaveCachePeriodically(sp.cacheConfig.ETXTrieCleanJournal, sp.cacheConfig.TrieCleanRejournal, sp.quit)
		}()
	}
	return sp
}

type UtxosCreatedDeleted struct {
	UtxosCreatedKeys   [][]byte
	UtxosCreatedHashes []common.Hash
	UtxosDeleted       []*types.SpentUtxoEntry
	UtxosDeletedHashes []common.Hash
}

// Process processes the state changes according to the Quai rules by running
// the transaction messages using the statedb and applying any rewards to both
// the processor (coinbase) and any included uncles.
//
// Process returns the receipts and logs accumulated during the process and
// returns the amount of gas that was used in the process. If any of the
// transactions failed to execute due to insufficient gas it will return an error.
func (p *StateProcessor) Process(block *types.WorkObject, batch ethdb.Batch) (types.Receipts, []*types.Transaction, []*types.Log, *state.StateDB, uint64, uint64, uint64, *multiset.MultiSet, error) {
	var (
		receipts     types.Receipts
		usedGas      = new(uint64)
		usedState    = new(uint64)
		header       = types.CopyWorkObject(block)
		blockHash    = block.Hash()
		nodeLocation = p.hc.NodeLocation()
		nodeCtx      = p.hc.NodeCtx()
		blockNumber  = block.Number(nodeCtx)
		allLogs      []*types.Log
		gp           = new(types.GasPool).AddGas(block.GasLimit())
	)
	start := time.Now()
	parent := p.hc.GetBlock(block.ParentHash(nodeCtx), block.NumberU64(nodeCtx)-1)
	if parent == nil {
		return types.Receipts{}, []*types.Transaction{}, []*types.Log{}, nil, 0, 0, 0, nil, errors.New("parent block is nil for the block given to process")
	}
	time1 := common.PrettyDuration(time.Since(start))

	parentEvmRoot := parent.Header().EVMRoot()
	parentEtxSetRoot := parent.Header().EtxSetRoot()
	parentQuaiStateSize := parent.QuaiStateSize()
	parentUtxoSetSize := rawdb.ReadUTXOSetSize(p.hc.bc.db, header.ParentHash(nodeCtx))
	if p.hc.IsGenesisHash(parent.Hash()) {
		parentEvmRoot = types.EmptyRootHash
		parentEtxSetRoot = types.EmptyRootHash
		parentQuaiStateSize = big.NewInt(0)
		parentUtxoSetSize = 0
	}
	qiScalingFactor := math.Log(float64(parentUtxoSetSize))
	// Initialize a statedb
	statedb, err := state.New(parentEvmRoot, parentEtxSetRoot, parentQuaiStateSize, p.stateCache, p.etxCache, p.snaps, nodeLocation, p.logger)
	if err != nil {
		return types.Receipts{}, []*types.Transaction{}, []*types.Log{}, nil, 0, 0, 0, nil, err
	}
	utxosCreatedDeleted := new(UtxosCreatedDeleted) // utxos created and deleted in this block
	// Apply the previous inbound ETXs to the ETX set state
	prevInboundEtxs := rawdb.ReadInboundEtxs(p.hc.bc.db, header.ParentHash(nodeCtx))
	if len(prevInboundEtxs) > 0 {
		if err := statedb.PushETXs(prevInboundEtxs); err != nil {
			return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not push prev inbound etxs: %w", err)
		}
	}
	time2 := common.PrettyDuration(time.Since(start))

	var timeSign, timePrepare, timeQiToQuai, timeQuaiToQi, timeCoinbase, timeEtx, timeTx time.Duration
	startTimeSenders := time.Now()
	senders := make(map[common.Hash]*common.InternalAddress) // temporary cache for senders of internal txs
	numInternalTxs := 0
	p.hc.pool.SendersMu.RLock()               // Prevent the txpool from grabbing the lock during the entire block tx lookup
	for _, tx := range block.Transactions() { // get all senders of internal txs from cache
		if tx.Type() == types.QuaiTxType {
			numInternalTxs++
			if sender, ok := p.hc.pool.PeekSenderNoLock(tx.Hash()); ok {
				senders[tx.Hash()] = &sender // This pointer must never be modified
			} else {
				// TODO: calcuate the sender and add it to the pool senders cache in case of reorg (not necessary for now)
			}
		} else if tx.Type() == types.QiTxType {
			numInternalTxs++
			if _, ok := p.hc.pool.PeekSenderNoLock(tx.Hash()); ok {
				senders[tx.Hash()] = &common.InternalAddress{}
			}
		}
	}
	p.hc.pool.SendersMu.RUnlock()
	timeSenders := time.Since(startTimeSenders)

	blockContext, err := NewEVMBlockContext(header, parent, p.hc, nil)
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}
	vmenv := vm.NewEVM(blockContext, vm.TxContext{}, statedb, p.config, p.vmConfig)
	time3 := common.PrettyDuration(time.Since(start))

	// Iterate over and process the individual transactions.
	etxRLimit := len(parent.Transactions()) / params.ETXRegionMaxFraction
	if etxRLimit < params.ETXRLimitMin {
		etxRLimit = params.ETXRLimitMin
	}
	etxPLimit := len(parent.Transactions()) / params.ETXPrimeMaxFraction
	if etxPLimit < params.ETXPLimitMin {
		etxPLimit = params.ETXPLimitMin
	}
	minimumEtxGas := header.GasLimit() / params.MinimumEtxGasDivisor // 20% of the block gas limit
	maximumEtxGas := minimumEtxGas * params.MaximumEtxGasMultiplier  // 40% of the block gas limit
	totalEtxGas := uint64(0)
	quaiFees := big.NewInt(0)
	qiFees := big.NewInt(0)
	emittedEtxs := make([]*types.Transaction, 0)
	quaiCoinbaseEtxs := make(map[[21]byte]*big.Int)
	var totalQiTime time.Duration
	var totalEtxAppendTime time.Duration
	var totalEtxCoinbaseTime time.Duration
	totalQiProcessTimes := make(map[string]time.Duration)
	firstQiTx := true

	nonEtxExists := false

	primeTerminus := p.hc.GetHeaderByHash(header.PrimeTerminusHash())
	if primeTerminus == nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not find prime terminus header %032x", header.PrimeTerminusHash())
	}
	// Set the min gas price to the lowest gas price in the transaction If that
	// value is not the basefee mentioned in the block, the block is invalid In
	// the case of the Qi transactions, its converted into Quai at the rate
	// defined in the prime terminus
	var minGasPrice *big.Int
	for i, tx := range block.Transactions() {
		startProcess := time.Now()

		if tx.Type() == types.QiTxType {
			qiTimeBefore := time.Now()
			checkSig := true
			if _, ok := senders[tx.Hash()]; ok {
				checkSig = false
			}
			qiTxFee, fees, etxs, err, timing := ProcessQiTx(tx, p.hc, checkSig, firstQiTx, header, batch, p.hc.headerDb, gp, usedGas, p.hc.pool.signer, p.hc.NodeLocation(), *p.config.ChainID, qiScalingFactor, &etxRLimit, &etxPLimit, utxosCreatedDeleted)
			if err != nil {
				return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
			}
			firstQiTx = false
			startEtxAppend := time.Now()
			for _, etx := range etxs {
				emittedEtxs = append(emittedEtxs, types.NewTx(etx))
			}
			totalEtxAppendTime += time.Since(startEtxAppend)
			startEtxCoinbase := time.Now()

			qiFees.Add(qiFees, fees)

			// convert the fee to quai
			qiTxFeeInQuai := misc.QiToQuai(parent, qiTxFee)
			// get the gas price by dividing the fee by qiTxGas
			qiGasPrice := new(big.Int).Div(qiTxFeeInQuai, big.NewInt(int64(types.CalculateBlockQiTxGas(tx, qiScalingFactor, p.hc.NodeLocation()))))
			if minGasPrice == nil {
				minGasPrice = new(big.Int).Set(qiGasPrice)
			} else {
				if minGasPrice.Cmp(qiGasPrice) > 0 {
					minGasPrice = new(big.Int).Set(qiGasPrice)
				}
			}

			totalEtxCoinbaseTime += time.Since(startEtxCoinbase)
			totalQiTime += time.Since(qiTimeBefore)
			totalQiProcessTimes["Sanity Checks"] += timing["Sanity Checks"]
			totalQiProcessTimes["Input Processing"] += timing["Input Processing"]
			totalQiProcessTimes["Output Processing"] += timing["Output Processing"]
			totalQiProcessTimes["Fee Verification"] += timing["Fee Verification"]
			totalQiProcessTimes["Signature Check"] += timing["Signature Check"]

			nonEtxExists = true

			continue
		}

		msg, err := tx.AsMessageWithSender(types.MakeSigner(p.config, header.Number(nodeCtx)), header.BaseFee(), senders[tx.Hash()])
		if err != nil {
			return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		timeSignDelta := time.Since(startProcess)
		timeSign += timeSignDelta

		startTimePrepare := time.Now()
		statedb.Prepare(tx.Hash(), i)
		timePrepareDelta := time.Since(startTimePrepare)
		timePrepare += timePrepareDelta

		var receipt *types.Receipt
		var addReceipt bool
		if tx.Type() == types.ExternalTxType {
			startTimeEtx := time.Now()
			// ETXs MUST be included in order, so popping the first from the queue must equal the first in the block
			etx, err := statedb.PopETX()
			if err != nil {
				return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not pop etx from statedb: %w", err)
			}
			if etx == nil {
				return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("etx %x is nil", tx.Hash())
			}
			if etx.Hash() != tx.Hash() {
				return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("invalid external transaction: etx %x is not in order or not found in unspent etx set", tx.Hash())
			}

			// check if the tx is a coinbase tx
			// coinbase tx
			// 1) is a external tx type
			// 2) do not consume any gas
			// 3) do not produce any receipts/logs
			// 4) etx emit threshold numbers
			if types.IsCoinBaseTx(tx) {
				if tx.To() == nil {
					return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("coinbase tx %x has no recipient", tx.Hash())
				}
				if len(tx.Data()) == 0 {
					return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("coinbase tx %x has no lockup byte", tx.Hash())
				}
				if _, err := tx.To().InternalAddress(); err != nil {
					return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("coinbase tx %x has invalid recipient: %w", tx.Hash(), err)
				}
				lockupByte := tx.Data()[0]
				if tx.To().IsInQiLedgerScope() {
					if int(lockupByte) > len(params.LockupByteToBlockDepth) {
						return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("coinbase lockup byte %d is out of range", lockupByte)
					}
					lockup := new(big.Int).SetUint64(params.LockupByteToBlockDepth[lockupByte])
					if lockup.Uint64() < params.ConversionLockPeriod {
						return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("coinbase lockup period is less than the minimum lockup period of %d blocks", params.ConversionLockPeriod)
					}
					value := params.CalculateCoinbaseValueWithLockup(tx.Value(), lockupByte)
					denominations := misc.FindMinDenominations(value)
					outputIndex := uint16(0)
					// Iterate over the denominations in descending order
					for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
						// If the denomination count is zero, skip it
						if denominations[uint8(denomination)] == 0 {
							continue
						}
						for j := uint64(0); j < denominations[uint8(denomination)]; j++ {
							if outputIndex >= types.MaxOutputIndex {
								// No more gas, the rest of the denominations are lost but the tx is still valid
								break
							}
							utxo := types.NewUtxoEntry(types.NewTxOut(uint8(denomination), tx.To().Bytes(), lockup))
							// the ETX hash is guaranteed to be unique
							if err := rawdb.CreateUTXO(batch, etx.Hash(), outputIndex, utxo); err != nil {
								return nil, nil, nil, nil, 0, 0, 0, nil, err
							}
							utxosCreatedDeleted.UtxosCreatedHashes = append(utxosCreatedDeleted.UtxosCreatedHashes, types.UTXOHash(etx.Hash(), outputIndex, utxo))
							utxosCreatedDeleted.UtxosCreatedKeys = append(utxosCreatedDeleted.UtxosCreatedKeys, rawdb.UtxoKeyWithDenomination(etx.Hash(), outputIndex, utxo.Denomination))
							p.logger.Debugf("Creating UTXO for coinbase %032x with denomination %d index %d\n", tx.Hash(), denomination, outputIndex)
							outputIndex++
						}
					}
				} else if tx.To().IsInQuaiLedgerScope() {
					// Combine similar lockups (to address and lockup period) into a single lockup entry
					coinbaseAddrWithLockup := [21]byte(append(tx.To().Bytes(), lockupByte))
					if val, ok := quaiCoinbaseEtxs[coinbaseAddrWithLockup]; ok {
						// Combine the values of the coinbase transactions
						val.Add(val, tx.Value())
						quaiCoinbaseEtxs[coinbaseAddrWithLockup] = val
					} else {
						// Add the new coinbase transaction to the map
						quaiCoinbaseEtxs[coinbaseAddrWithLockup] = tx.Value() // this creates a new *big.Int
					}
				}
				// subtract the minimum tx gas from the gas pool
				if err := gp.SubGas(params.TxGas); err != nil {
					return nil, nil, nil, nil, 0, 0, 0, nil, err
				}
				*usedGas += params.TxGas
				totalEtxGas += params.TxGas
				timeDelta := time.Since(startTimeEtx)
				timeCoinbase += timeDelta
				continue
			}
			if etx.To().IsInQiLedgerScope() {
				if etx.ETXSender().Location().Equal(*etx.To().Location()) { // Quai->Qi Conversion
					lock := new(big.Int).Add(header.Number(nodeCtx), new(big.Int).SetUint64(params.ConversionLockPeriod))
					value := etx.Value()
					txGas := etx.Gas()
					if txGas < params.TxGas {
						continue
					}
					txGas -= params.TxGas
					if err := gp.SubGas(params.TxGas); err != nil {
						return nil, nil, nil, nil, 0, 0, 0, nil, err
					}
					*usedGas += params.TxGas
					totalEtxGas += params.TxGas
					denominations := misc.FindMinDenominations(value)
					outputIndex := uint16(0)
					// Iterate over the denominations in descending order
					for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
						// If the denomination count is zero, skip it
						if denominations[uint8(denomination)] == 0 {
							continue
						}
						for j := uint64(0); j < denominations[uint8(denomination)]; j++ {
							if txGas < params.CallValueTransferGas || outputIndex >= types.MaxOutputIndex {
								// No more gas, the rest of the denominations are lost but the tx is still valid
								break
							}
							txGas -= params.CallValueTransferGas
							if err := gp.SubGas(params.CallValueTransferGas); err != nil {
								return nil, nil, nil, nil, 0, 0, 0, nil, err
							}
							*usedGas += params.CallValueTransferGas    // In the future we may want to determine what a fair gas cost is
							totalEtxGas += params.CallValueTransferGas // In the future we may want to determine what a fair gas cost is
							utxo := types.NewUtxoEntry(types.NewTxOut(uint8(denomination), etx.To().Bytes(), lock))
							// the ETX hash is guaranteed to be unique
							if err := rawdb.CreateUTXO(batch, etx.Hash(), outputIndex, utxo); err != nil {
								return nil, nil, nil, nil, 0, 0, 0, nil, err
							}
							utxosCreatedDeleted.UtxosCreatedHashes = append(utxosCreatedDeleted.UtxosCreatedHashes, types.UTXOHash(etx.Hash(), outputIndex, utxo))
							utxosCreatedDeleted.UtxosCreatedKeys = append(utxosCreatedDeleted.UtxosCreatedKeys, rawdb.UtxoKeyWithDenomination(etx.Hash(), outputIndex, utxo.Denomination))
							p.logger.Infof("Converting Quai to Qi %032x with denomination %d index %d lock %d\n", tx.Hash(), denomination, outputIndex, lock)
							outputIndex++
						}
					}
				} else {
					utxo := types.NewUtxoEntry(types.NewTxOut(uint8(etx.Value().Uint64()), etx.To().Bytes(), big.NewInt(0)))
					// There are no more checks to be made as the ETX is worked so add it to the set
					if err := rawdb.CreateUTXO(batch, etx.OriginatingTxHash(), etx.ETXIndex(), utxo); err != nil {
						return nil, nil, nil, nil, 0, 0, 0, nil, err
					}
					utxosCreatedDeleted.UtxosCreatedHashes = append(utxosCreatedDeleted.UtxosCreatedHashes, types.UTXOHash(etx.OriginatingTxHash(), etx.ETXIndex(), utxo))
					utxosCreatedDeleted.UtxosCreatedKeys = append(utxosCreatedDeleted.UtxosCreatedKeys, rawdb.UtxoKeyWithDenomination(etx.OriginatingTxHash(), etx.ETXIndex(), utxo.Denomination))
					// This Qi ETX should cost more gas
					if err := gp.SubGas(params.CallValueTransferGas); err != nil {
						return nil, nil, nil, nil, 0, 0, 0, nil, err
					}
					*usedGas += params.CallValueTransferGas    // In the future we may want to determine what a fair gas cost is
					totalEtxGas += params.CallValueTransferGas // In the future we may want to determine what a fair gas cost is
				}
				timeDelta := time.Since(startTimeEtx)
				timeQuaiToQi += timeDelta
				continue
			} else {
				if etx.ETXSender().Location().Equal(*etx.To().Location()) { // Qi->Quai Conversion
					msg.SetLock(new(big.Int).Add(header.Number(nodeCtx), new(big.Int).SetUint64(params.ConversionLockPeriod)))
					msg.SetValue(etx.Value())
					msg.SetData([]byte{}) // data is not used in conversion
					p.logger.Infof("Converting Qi to Quai for ETX %032x with value %d lock %d\n", tx.Hash(), msg.Value().Uint64(), msg.Lock().Uint64())
				}
				fees := big.NewInt(0)
				prevZeroBal := prepareApplyETX(statedb, msg.Value(), nodeLocation)
				receipt, fees, err = applyTransaction(msg, parent, p.config, p.hc, gp, statedb, blockNumber, blockHash, etx, usedGas, usedState, vmenv, &etxRLimit, &etxPLimit, p.logger)
				statedb.SetBalance(common.ZeroInternal(nodeLocation), prevZeroBal) // Reset the balance to what it previously was. Residual balance will be lost
				if err != nil {
					return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
				}
				addReceipt = true

				quaiFees.Add(quaiFees, fees)

				totalEtxGas += receipt.GasUsed
				timeDelta := time.Since(startTimeEtx)
				timeQiToQuai += timeDelta
			}
		} else if tx.Type() == types.QuaiTxType {
			startTimeTx := time.Now()

			fees := big.NewInt(0)
			receipt, fees, err = applyTransaction(msg, parent, p.config, p.hc, gp, statedb, blockNumber, blockHash, tx, usedGas, usedState, vmenv, &etxRLimit, &etxPLimit, p.logger)
			if err != nil {
				return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
			}
			addReceipt = true
			timeTxDelta := time.Since(startTimeTx)
			timeTx += timeTxDelta

			quaiFees.Add(quaiFees, fees)

			// update the min gas price if the gas price in the tx is less than
			// the min gas price
			if minGasPrice == nil {
				minGasPrice = new(big.Int).Set(tx.GasPrice())
			} else {
				if minGasPrice.Cmp(tx.GasPrice()) > 0 {
					minGasPrice = new(big.Int).Set(tx.GasPrice())
				}
			}

		} else {
			return nil, nil, nil, nil, 0, 0, 0, nil, ErrTxTypeNotSupported
		}
		for _, etx := range receipt.OutboundEtxs {
			if receipt.Status == types.ReceiptStatusSuccessful {
				emittedEtxs = append(emittedEtxs, etx)
			}
		}
		if addReceipt {
			receipts = append(receipts, receipt)
			allLogs = append(allLogs, receipt.Logs...)
		}
		i++
	}

	if nonEtxExists && block.BaseFee().Cmp(big.NewInt(0)) == 0 {
		return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("block base fee is nil though non etx transactions exist")
	}

	if minGasPrice != nil && block.BaseFee().Cmp(minGasPrice) != 0 {
		return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("invalid base fee used (remote: %d local: %d)", block.BaseFee(), minGasPrice)
	}

	etxAvailable := false
	oldestIndex, err := statedb.GetOldestIndex()
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not get oldest index: %w", err)
	}
	// Check if there is at least one ETX in the set
	etx, err := statedb.ReadETX(oldestIndex)
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("could not read etx: %w", err)
	}
	if etx != nil {
		etxAvailable = true
	}
	if (etxAvailable && totalEtxGas < minimumEtxGas) || totalEtxGas > maximumEtxGas {
		p.logger.Errorf("prevInboundEtxs: %d, oldestIndex: %d, etxHash: %s", len(prevInboundEtxs), oldestIndex.Int64(), etx.Hash().Hex())
		return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("total gas used by ETXs %d is not within the range %d to %d", totalEtxGas, minimumEtxGas, maximumEtxGas)
	}
	lockupContractAddress := vm.LockupContractAddresses[[2]byte{nodeLocation[0], nodeLocation[1]}]
	for coinbaseAddrWithLockup, value := range quaiCoinbaseEtxs {
		addr := common.BytesToAddress(coinbaseAddrWithLockup[:20], nodeLocation)
		lockupByte := coinbaseAddrWithLockup[20]
		lockup := new(big.Int).SetUint64(params.LockupByteToBlockDepth[lockupByte])
		if lockup.Uint64() < params.ConversionLockPeriod {
			return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("coinbase lockup period is less than the minimum lockup period of %d blocks", params.ConversionLockPeriod)
		}
		value := params.CalculateCoinbaseValueWithLockup(value, lockupByte)
		gasUsed, _, err := vm.AddNewLock(parent.QuaiStateSize(), statedb, addr, new(types.GasPool).AddGas(math.MaxUint64), lockup, value, lockupContractAddress)
		if err != nil {
			return nil, nil, nil, nil, 0, 0, 0, nil, err
		}
		p.logger.Debugf("Creating Lockup for coinbase addr %s with value %d lock %d gasUsed %d\n", addr.String(), value.Uint64(), lockup.Int64(), gasUsed)
	}

	quaiCoinbase, err := block.QuaiCoinbase()
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}
	qiCoinbase, err := block.QiCoinbase()
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}

	primaryCoinbase := block.PrimaryCoinbase()
	secondaryCoinbase := block.SecondaryCoinbase()

	// parent hash encoding for populating into the originating tx hash for coinbase
	origin := block.ParentHash(nodeCtx)
	origin[0] = byte(block.Location().Region())
	origin[1] = byte(block.Location().Zone())
	// If the primary coinbase belongs to a ledger and there is no fees
	// for other ledger, there is no etxs emitted for the other ledger
	if bytes.Equal(block.PrimaryCoinbase().Bytes(), quaiCoinbase.Bytes()) {
		coinbaseReward := misc.CalculateReward(parent, block.WorkObjectHeader())
		blockReward := new(big.Int).Add(coinbaseReward, quaiFees)
		coinbaseEtx := types.NewTx(&types.ExternalTx{To: &primaryCoinbase, Gas: params.TxGas, Value: blockReward, EtxType: types.CoinbaseType, OriginatingTxHash: origin, ETXIndex: uint16(len(emittedEtxs)), Sender: primaryCoinbase, Data: []byte{block.Lock()}})
		emittedEtxs = append(emittedEtxs, coinbaseEtx)
		if qiFees.Cmp(big.NewInt(0)) != 0 {
			coinbaseEtx := types.NewTx(&types.ExternalTx{To: &secondaryCoinbase, Gas: params.TxGas, Value: qiFees, EtxType: types.CoinbaseType, OriginatingTxHash: origin, ETXIndex: uint16(len(emittedEtxs)), Sender: secondaryCoinbase, Data: []byte{block.Lock()}})
			emittedEtxs = append(emittedEtxs, coinbaseEtx)
		}
	} else if bytes.Equal(block.PrimaryCoinbase().Bytes(), qiCoinbase.Bytes()) {
		coinbaseReward := misc.CalculateReward(parent, block.WorkObjectHeader())
		blockReward := new(big.Int).Add(coinbaseReward, qiFees)
		coinbaseEtx := types.NewTx(&types.ExternalTx{To: &primaryCoinbase, Gas: params.TxGas, Value: blockReward, EtxType: types.CoinbaseType, OriginatingTxHash: origin, ETXIndex: uint16(len(emittedEtxs)), Sender: primaryCoinbase, Data: []byte{block.Lock()}})
		emittedEtxs = append(emittedEtxs, coinbaseEtx)
		if quaiFees.Cmp(big.NewInt(0)) != 0 {
			coinbaseEtx := types.NewTx(&types.ExternalTx{To: &secondaryCoinbase, Gas: params.TxGas, Value: quaiFees, EtxType: types.CoinbaseType, OriginatingTxHash: origin, ETXIndex: uint16(len(emittedEtxs)), Sender: secondaryCoinbase, Data: []byte{block.Lock()}})
			emittedEtxs = append(emittedEtxs, coinbaseEtx)
		}
	}
	// Add an etx for each workshare for it to be rewarded
	for _, uncle := range block.Uncles() {
		reward := misc.CalculateReward(parent, uncle)
		uncleCoinbase := uncle.PrimaryCoinbase()
		emittedEtxs = append(emittedEtxs, types.NewTx(&types.ExternalTx{To: &uncleCoinbase, Gas: params.TxGas, Value: reward, EtxType: types.CoinbaseType, OriginatingTxHash: origin, ETXIndex: uint16(len(emittedEtxs)), Sender: uncleCoinbase, Data: []byte{uncle.Lock()}}))
	}

	updatedTokenChoiceSet, err := CalculateTokenChoicesSet(p.hc, parent, emittedEtxs)
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}
	var exchangeRate *big.Int
	var beta0, beta1 *big.Float
	if parent.NumberU64(common.ZONE_CTX) > params.ControllerKickInBlock {
		exchangeRate, beta0, beta1, err = CalculateExchangeRate(p.hc, parent, updatedTokenChoiceSet)
		if err != nil {
			return nil, nil, nil, nil, 0, 0, 0, nil, err
		}
	} else {
		exchangeRate = parent.ExchangeRate()
		betas := rawdb.ReadBetas(p.hc.headerDb, parent.Hash())
		beta0 = betas.Beta0()
		beta1 = betas.Beta1()
	}
	err = rawdb.WriteTokenChoicesSet(batch, block.Hash(), &updatedTokenChoiceSet)
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}
	err = rawdb.WriteBetas(batch, block.Hash(), beta0, beta1)
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}
	if block.ExchangeRate().Cmp(exchangeRate) != 0 {
		return nil, nil, nil, nil, 0, 0, 0, nil, fmt.Errorf("invalid exchange rate used (remote: %d local: %d)", block.ExchangeRate(), exchangeRate)
	}
	for _, etx := range emittedEtxs {
		// If the etx is conversion
		if types.IsConversionTx(etx) {
			value := etx.Value()
			// If to is in Qi, convert the value into Qi
			if etx.To().IsInQiLedgerScope() {
				value = misc.QuaiToQi(block, value)
			}
			// If to is in Quai, convert the value into Quai
			if etx.To().IsInQuaiLedgerScope() {
				value = misc.QiToQuai(block, value)
			}
			etx.SetValue(value)
		}
	}

	time4 := common.PrettyDuration(time.Since(start))
	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	multiSet, utxoSetSize, err := p.engine.Finalize(p.hc, batch, block, statedb, false, parentUtxoSetSize, utxosCreatedDeleted.UtxosCreatedHashes, utxosCreatedDeleted.UtxosDeletedHashes)
	if err != nil {
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}
	time5 := common.PrettyDuration(time.Since(start))

	p.logger.WithFields(log.Fields{
		"signing time":       common.PrettyDuration(timeSign),
		"prepare state time": common.PrettyDuration(timePrepare),
		"qiToQuai time":      common.PrettyDuration(timeQiToQuai),
		"quaiToQi time":      common.PrettyDuration(timeQuaiToQi),
		"coinbase time":      common.PrettyDuration(timeCoinbase),
		"etxTime":            common.PrettyDuration(timeEtx),
		"txTime":             common.PrettyDuration(timeTx),
		"totalQiTime":        common.PrettyDuration(totalQiTime),
	}).Info("Total Qi Tx Processing Time")

	p.logger.WithFields(log.Fields{
		"Input Processing":       common.PrettyDuration(totalQiProcessTimes["Input Processing"]),
		"Output Processing":      common.PrettyDuration(totalQiProcessTimes["Output Processing"]),
		"Fee Verification":       common.PrettyDuration(totalQiProcessTimes["Fee Verification"]),
		"Signature Verification": common.PrettyDuration(totalQiProcessTimes["Signature Check"]),
		"Sanity Checks":          common.PrettyDuration(totalQiProcessTimes["Sanity Checks"]),
	}).Info("Qi Tx Processing Breakdown")

	p.logger.WithFields(log.Fields{
		"time1": time1,
		"time2": time2,
		"time3": time3,
		"time4": time4,
		"time5": time5,
	}).Info("Time taken in Process")

	p.logger.WithFields(log.Fields{
		"signing time":                common.PrettyDuration(timeSign),
		"senders cache time":          common.PrettyDuration(timeSenders),
		"percent cached internal txs": fmt.Sprintf("%.2f", float64(len(senders))/float64(numInternalTxs)*100),
		"prepare state time":          common.PrettyDuration(timePrepare),
		"etx time":                    common.PrettyDuration(timeEtx),
		"tx time":                     common.PrettyDuration(timeTx),
		"numTxs":                      len(block.Transactions()),
	}).Info("Total Tx Processing Time")
	if err := rawdb.WriteSpentUTXOs(batch, blockHash, utxosCreatedDeleted.UtxosDeleted); err != nil { // Could do this in Apply instead
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}
	if err := rawdb.WriteCreatedUTXOKeys(batch, blockHash, utxosCreatedDeleted.UtxosCreatedKeys); err != nil { // Could do this in Apply instead
		return nil, nil, nil, nil, 0, 0, 0, nil, err
	}
	return receipts, emittedEtxs, allLogs, statedb, *usedGas, *usedState, utxoSetSize, multiSet, nil
}

func applyTransaction(msg types.Message, parent *types.WorkObject, config *params.ChainConfig, bc ChainContext, gp *types.GasPool, statedb *state.StateDB, blockNumber *big.Int, blockHash common.Hash, tx *types.Transaction, usedGas *uint64, usedState *uint64, evm *vm.EVM, etxRLimit, etxPLimit *int, logger *log.Logger) (*types.Receipt, *big.Int, error) {
	nodeLocation := config.Location
	// Create a new context to be used in the EVM environment.
	txContext := NewEVMTxContext(msg)
	evm.Reset(txContext, statedb)

	// Apply the transaction to the current state (included in the env).
	result, err := ApplyMessage(evm, msg, gp)
	if err != nil {
		return nil, nil, err
	}

	var ETXRCount int
	var ETXPCount int
	for _, tx := range result.Etxs {
		// Count which ETXs are cross-region
		if tx.To().Location().CommonDom(nodeLocation).Context() == common.REGION_CTX {
			ETXRCount++
		}
		// Count which ETXs are cross-prime
		if tx.To().Location().CommonDom(nodeLocation).Context() == common.PRIME_CTX {
			ETXPCount++
		}
	}
	if ETXRCount > *etxRLimit {
		return nil, nil, fmt.Errorf("tx %032x emits too many cross-region ETXs for block. emitted: %d, limit: %d", tx.Hash(), ETXRCount, *etxRLimit)
	}
	if ETXPCount > *etxPLimit {
		return nil, nil, fmt.Errorf("tx %032x emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash(), ETXPCount, *etxPLimit)
	}
	*etxRLimit -= ETXRCount
	*etxPLimit -= ETXPCount

	// Update the state with pending changes.
	var root []byte
	statedb.Finalise(true)

	*usedGas += result.UsedGas
	*usedState += result.UsedState

	// Create a new receipt for the transaction, storing the intermediate root and gas used
	// by the tx.
	receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: *usedGas, OutboundEtxs: result.Etxs}
	if result.Failed() {
		receipt.Status = types.ReceiptStatusFailed
		logger.WithField("err", result.Err).Debug("Transaction failed")
	} else {
		receipt.Status = types.ReceiptStatusSuccessful
		// If the transaction created a contract, store the creation address in the receipt.
		if result.ContractAddr != nil {
			receipt.ContractAddress = *result.ContractAddr
		}
	}
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = result.UsedGas

	// Set the receipt logs and create the bloom filter.
	receipt.Logs = statedb.GetLogs(tx.Hash(), blockHash)
	receipt.Bloom = types.CreateBloom(types.Receipts{receipt})
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())
	return receipt, result.QuaiFees, err
}

func ValidateQiTxInputs(tx *types.Transaction, chain ChainContext, db ethdb.Reader, currentHeader *types.WorkObject, signer types.Signer, location common.Location, chainId big.Int) (*big.Int, error) {
	if tx.Type() != types.QiTxType {
		return nil, fmt.Errorf("tx %032x is not a QiTx", tx.Hash())
	}
	totalQitIn := big.NewInt(0)
	addresses := make(map[common.AddressBytes]struct{})
	inputs := make(map[uint]uint64)
	for _, txIn := range tx.TxIn() {
		utxo := rawdb.GetUTXO(db, txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		if utxo == nil {
			return nil, fmt.Errorf("tx %032x spends non-existent UTXO %032x:%d", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		}
		if utxo.Lock != nil && utxo.Lock.Cmp(currentHeader.Number(location.Context())) > 0 {
			return nil, fmt.Errorf("tx %032x spends locked UTXO %032x:%d locked until %s", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, utxo.Lock.String())
		}
		address := crypto.PubkeyBytesToAddress(txIn.PubKey, location)
		entryAddr := common.BytesToAddress(utxo.Address, location)
		if !address.Equal(entryAddr) {
			return nil, fmt.Errorf("tx %032x spends UTXO %032x:%d with invalid pubkey, have %s want %s", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, address.String(), entryAddr.String())
		}
		// Check for duplicate addresses. This also checks for duplicate inputs.
		if _, exists := addresses[common.AddressBytes(utxo.Address)]; exists {
			return nil, errors.New("Duplicate address in QiTx inputs: " + common.AddressBytes(utxo.Address).String())
		}
		addresses[common.AddressBytes(utxo.Address)] = struct{}{}

		// Perform some spend processing logic
		denomination := utxo.Denomination
		if denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				denomination,
				types.MaxDenomination)
			return nil, errors.New(str)
		}
		totalQitIn.Add(totalQitIn, types.Denominations[denomination])
		inputs[uint(denomination)]++
	}
	outputs := make(map[uint]uint64)
	for _, txOut := range tx.TxOut() {
		if txOut.Denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				txOut.Denomination,
				types.MaxDenomination)
			return nil, errors.New(str)
		}
		outputs[uint(txOut.Denomination)]++
		if common.IsConversionOutput(txOut.Address, location) { // Qi->Quai conversion
			outputs[uint(txOut.Denomination)] -= 1 // This output no longer exists because it has been aggregated
		}
	}
	return totalQitIn, nil

}

func ValidateQiTxOutputsAndSignature(tx *types.Transaction, chain ChainContext, totalQitIn *big.Int, currentHeader *types.WorkObject, signer types.Signer, location common.Location, chainId big.Int, qiScalingFactor float64, etxRLimit, etxPLimit int) (*big.Int, error) {

	intrinsicGas := types.CalculateIntrinsicQiTxGas(tx, qiScalingFactor)
	usedGas := intrinsicGas

	primeTerminusHash := currentHeader.PrimeTerminusHash()
	primeTerminusHeader := chain.GetHeaderByHash(primeTerminusHash)
	if primeTerminusHeader == nil {
		return nil, fmt.Errorf("could not find prime terminus header %032x", primeTerminusHash)
	}

	var ETXRCount int
	var ETXPCount int
	numEtxs := uint64(0)
	totalQitOut := big.NewInt(0)
	totalConvertQitOut := big.NewInt(0)
	conversion := false
	pubKeys := make([]*btcec.PublicKey, 0, len(tx.TxIn()))
	addresses := make(map[common.AddressBytes]struct{})
	for _, txIn := range tx.TxIn() {
		pubKey, err := btcec.ParsePubKey(txIn.PubKey)
		if err != nil {
			return nil, err
		}
		pubKeys = append(pubKeys, pubKey)
		addresses[crypto.PubkeyBytesToAddress(txIn.PubKey, location).Bytes20()] = struct{}{}
	}
	for txOutIdx, txOut := range tx.TxOut() {
		// It would be impossible for a tx to have this many outputs based on block gas limit, but cap it here anyways
		if txOutIdx > types.MaxOutputIndex {
			return nil, fmt.Errorf("tx [%v] exceeds max output index of %d", tx.Hash().Hex(), types.MaxOutputIndex)
		}

		if txOut.Denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				txOut.Denomination,
				types.MaxDenomination)
			return nil, errors.New(str)
		}
		totalQitOut.Add(totalQitOut, types.Denominations[txOut.Denomination])

		toAddr := common.BytesToAddress(txOut.Address, location)

		// Enforce no address reuse
		if _, exists := addresses[toAddr.Bytes20()]; exists {
			return nil, errors.New("Duplicate address in QiTx outputs: " + toAddr.String())
		}
		addresses[toAddr.Bytes20()] = struct{}{}

		if toAddr.Location().Equal(location) && toAddr.IsInQuaiLedgerScope() { // Qi->Quai conversion
			conversion = true
			if txOut.Denomination < params.MinQiConversionDenomination {
				return nil, fmt.Errorf("tx %v emits UTXO with value %d less than minimum denomination %d", tx.Hash().Hex(), txOut.Denomination, params.MinQiConversionDenomination)
			}
			totalConvertQitOut.Add(totalConvertQitOut, types.Denominations[txOut.Denomination]) // Add to total conversion output for aggregation
			delete(addresses, toAddr.Bytes20())
			continue
		} else if toAddr.IsInQuaiLedgerScope() {
			return nil, fmt.Errorf("tx [%v] emits UTXO with To address not in the Qi ledger scope", tx.Hash().Hex())
		}

		if !toAddr.Location().Equal(location) { // This output creates an ETX
			// Cross-region?
			if toAddr.Location().CommonDom(location).Context() == common.REGION_CTX {
				ETXRCount++
			}
			// Cross-prime?
			if toAddr.Location().CommonDom(location).Context() == common.PRIME_CTX {
				ETXPCount++
			}
			if ETXRCount > etxRLimit {
				return nil, fmt.Errorf("tx [%v] emits too many cross-region ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXRCount, etxRLimit)
			}
			if ETXPCount > etxPLimit {
				return nil, fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXPCount, etxPLimit)
			}
			primeTerminusHash := currentHeader.PrimeTerminusHash()
			primeTerminusHeader := chain.GetHeaderByHash(primeTerminusHash)
			if primeTerminusHeader == nil {
				return nil, fmt.Errorf("could not find prime terminus header %032x", primeTerminusHash)
			}
			if !toAddr.IsInQiLedgerScope() {
				return nil, fmt.Errorf("tx [%v] emits UTXO with To address not in the Qi ledger scope", tx.Hash().Hex())
			}
			if !chain.CheckIfEtxIsEligible(primeTerminusHeader.EtxEligibleSlices(), *toAddr.Location()) {
				return nil, fmt.Errorf("etx emitted by tx [%v] going to a slice that is not eligible to receive etx %v", tx.Hash().Hex(), *toAddr.Location())
			}

			// We should require some kind of extra fee here
			usedGas += params.ETXGas
			numEtxs++
		}
	}
	// Ensure the transaction does not spend more than its inputs.
	if totalQitOut.Cmp(totalQitIn) == 1 {
		str := fmt.Sprintf("total value of all transaction inputs for "+
			"transaction %v is %v which is less than the amount "+
			"spent of %v", tx.Hash(), totalQitIn, totalQitOut)
		return nil, errors.New(str)
	}

	// the fee to pay the basefee/miner is the difference between inputs and outputs
	txFeeInQit := new(big.Int).Sub(totalQitIn, totalQitOut)
	txFee := new(big.Int).Set(txFeeInQit)
	// Check tx against required base fee and gas
	requiredGas := intrinsicGas + (numEtxs * (params.TxGas + params.ETXGas)) // Each ETX costs extra gas that is paid in the origin
	if requiredGas < intrinsicGas {
		// Overflow
		return nil, fmt.Errorf("tx %032x has too many ETXs to calculate required gas", tx.Hash())
	}
	minimumFeeInQuai := new(big.Int).Mul(big.NewInt(int64(requiredGas)), currentHeader.BaseFee())
	txFeeInQuai := misc.QiToQuai(currentHeader, txFeeInQit)
	if txFeeInQuai.Cmp(minimumFeeInQuai) < 0 {
		return nil, fmt.Errorf("tx %032x has insufficient fee for base fee, have %d want %d", tx.Hash(), txFeeInQuai.Uint64(), minimumFeeInQuai.Uint64())
	}
	if conversion {
		ETXPCount++
		if ETXPCount > etxPLimit {
			return nil, fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXPCount, etxPLimit)
		}
		usedGas += params.ETXGas
		txFeeInQit.Sub(txFeeInQit, txFeeInQit) // Fee goes entirely to gas to pay for conversion
	}

	if usedGas > currentHeader.GasLimit() {
		return nil, fmt.Errorf("tx %032x uses too much gas, have used %d out of %d", tx.Hash(), usedGas, currentHeader.GasLimit())
	}

	// Ensure the transaction signature is valid
	var finalKey *btcec.PublicKey
	if len(tx.TxIn()) > 1 {
		aggKey, _, _, err := musig2.AggregateKeys(
			pubKeys, false,
		)
		if err != nil {
			return nil, err
		}
		finalKey = aggKey.FinalKey
	} else {
		finalKey = pubKeys[0]
	}

	txDigestHash := signer.Hash(tx)
	if !tx.GetSchnorrSignature().Verify(txDigestHash[:], finalKey) {
		return nil, fmt.Errorf("invalid signature for tx %032x digest hash %032x", tx.Hash(), txDigestHash)
	}

	return txFee, nil
}

func ProcessQiTx(tx *types.Transaction, chain ChainContext, checkSig bool, isFirstQiTx bool, currentHeader *types.WorkObject, batch ethdb.Batch, db ethdb.Reader, gp *types.GasPool, usedGas *uint64, signer types.Signer, location common.Location, chainId big.Int, qiScalingFactor float64, etxRLimit, etxPLimit *int, utxosCreatedDeleted *UtxosCreatedDeleted) (*big.Int, *big.Int, []*types.ExternalTx, error, map[string]time.Duration) {
	var elapsedTime time.Duration
	stepTimings := make(map[string]time.Duration)

	qiTxFee := big.NewInt(0)
	// Start timing for sanity checks
	stepStart := time.Now()
	// Sanity checks
	if tx == nil || tx.Type() != types.QiTxType {
		return nil, nil, nil, fmt.Errorf("tx %032x is not a QiTx", tx.Hash()), nil
	}
	if tx.ChainId().Cmp(&chainId) != 0 {
		return nil, nil, nil, fmt.Errorf("tx %032x has invalid chain ID", tx.Hash()), nil
	}
	if currentHeader == nil || batch == nil || gp == nil || usedGas == nil || signer == nil || etxRLimit == nil || etxPLimit == nil {
		return nil, nil, nil, errors.New("one of the parameters is nil"), nil
	}
	intrinsicGas := types.CalculateIntrinsicQiTxGas(tx, qiScalingFactor)
	*usedGas += intrinsicGas
	if err := gp.SubGas(intrinsicGas); err != nil {
		return nil, nil, nil, err, nil
	}
	if *usedGas > currentHeader.GasLimit() {
		return nil, nil, nil, fmt.Errorf("tx %032x uses too much gas, have used %d out of %d", tx.Hash(), *usedGas, currentHeader.GasLimit()), nil
	}
	elapsedTime = time.Since(stepStart)
	stepTimings["Sanity Checks"] = elapsedTime

	// Start timing for input processing
	stepStart = time.Now()
	addresses := make(map[common.AddressBytes]struct{})
	inputs := make(map[uint]uint64)
	totalQitIn := big.NewInt(0)
	pubKeys := make([]*btcec.PublicKey, 0)
	for _, txIn := range tx.TxIn() {
		utxo := rawdb.GetUTXO(db, txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		if utxo == nil {
			return nil, nil, nil, fmt.Errorf("tx %032x spends non-existent UTXO %032x:%d", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index), nil
		}
		if utxo.Lock != nil && utxo.Lock.Cmp(currentHeader.Number(location.Context())) > 0 {
			return nil, nil, nil, fmt.Errorf("tx %032x spends locked UTXO %032x:%d locked until %s", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, utxo.Lock.String()), nil
		}
		// Verify the pubkey
		address := crypto.PubkeyBytesToAddress(txIn.PubKey, location)
		entryAddr := common.BytesToAddress(utxo.Address, location)
		if !address.Equal(entryAddr) {
			return nil, nil, nil, fmt.Errorf("tx %032x spends UTXO %032x:%d with invalid pubkey, have %s want %s", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, address.String(), entryAddr.String()), nil
		}
		if checkSig {
			pubKey, err := btcec.ParsePubKey(txIn.PubKey)
			if err != nil {
				return nil, nil, nil, err, nil
			}
			pubKeys = append(pubKeys, pubKey)
		}
		// Check for duplicate addresses. This also checks for duplicate inputs.
		if _, exists := addresses[common.AddressBytes(utxo.Address)]; exists {
			return nil, nil, nil, errors.New("Duplicate address in QiTx inputs: " + common.AddressBytes(utxo.Address).String()), nil
		}
		addresses[common.AddressBytes(utxo.Address)] = struct{}{}

		// Perform some spend processing logic
		denomination := utxo.Denomination
		if denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				denomination,
				types.MaxDenomination)
			return nil, nil, nil, errors.New(str), nil
		}
		totalQitIn.Add(totalQitIn, types.Denominations[denomination])
		inputs[uint(denomination)]++

		rawdb.DeleteUTXO(batch, txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		utxosCreatedDeleted.UtxosDeletedHashes = append(utxosCreatedDeleted.UtxosDeletedHashes, types.UTXOHash(txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, utxo))
		utxosCreatedDeleted.UtxosDeleted = append(utxosCreatedDeleted.UtxosDeleted, &types.SpentUtxoEntry{OutPoint: txIn.PreviousOutPoint, UtxoEntry: utxo})
	}
	elapsedTime = time.Since(stepStart)
	stepTimings["Input Processing"] = elapsedTime

	primeTerminusHash := currentHeader.PrimeTerminusHash()
	primeTerminusHeader := chain.GetHeaderByHash(primeTerminusHash)
	if primeTerminusHeader == nil {
		return nil, nil, nil, fmt.Errorf("could not find prime terminus header %032x", primeTerminusHash), nil
	}

	// Start timing for output processing
	stepStart = time.Now()
	var ETXRCount int
	var ETXPCount int
	etxs := make([]*types.ExternalTx, 0)
	outputs := make(map[uint]uint64)
	totalQitOut := big.NewInt(0)
	totalConvertQitOut := big.NewInt(0)
	conversion := false
	var convertAddress common.Address
	for txOutIdx, txOut := range tx.TxOut() {
		// It would be impossible for a tx to have this many outputs based on block gas limit, but cap it here anyways
		if txOutIdx > types.MaxOutputIndex {
			return nil, nil, nil, fmt.Errorf("tx [%v] exceeds max output index of %d", tx.Hash().Hex(), types.MaxOutputIndex), nil
		}

		if txOut.Denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				txOut.Denomination,
				types.MaxDenomination)
			return nil, nil, nil, errors.New(str), nil
		}
		totalQitOut.Add(totalQitOut, types.Denominations[txOut.Denomination])

		toAddr := common.BytesToAddress(txOut.Address, location)

		// Enforce no address reuse
		if _, exists := addresses[toAddr.Bytes20()]; exists {
			return nil, nil, nil, errors.New("Duplicate address in QiTx outputs: " + toAddr.String()), nil
		}
		addresses[toAddr.Bytes20()] = struct{}{}
		outputs[uint(txOut.Denomination)]++

		if toAddr.Location().Equal(location) && toAddr.IsInQuaiLedgerScope() { // Qi->Quai conversion
			conversion = true
			convertAddress = toAddr
			if txOut.Denomination < params.MinQiConversionDenomination {
				return nil, nil, nil, fmt.Errorf("tx %v emits UTXO with value %d less than minimum denomination %d", tx.Hash().Hex(), txOut.Denomination, params.MinQiConversionDenomination), nil
			}
			totalConvertQitOut.Add(totalConvertQitOut, types.Denominations[txOut.Denomination]) // Add to total conversion output for aggregation
			outputs[uint(txOut.Denomination)] -= 1                                              // This output no longer exists because it has been aggregated
			delete(addresses, toAddr.Bytes20())
			continue
		} else if toAddr.IsInQuaiLedgerScope() {
			return nil, nil, nil, fmt.Errorf("tx %v emits UTXO with To address not in the Qi ledger scope", tx.Hash().Hex()), nil
		}

		if !toAddr.Location().Equal(location) { // This output creates an ETX
			// Cross-region?
			if toAddr.Location().CommonDom(location).Context() == common.REGION_CTX {
				ETXRCount++
			}
			// Cross-prime?
			if toAddr.Location().CommonDom(location).Context() == common.PRIME_CTX {
				ETXPCount++
			}
			if ETXRCount > *etxRLimit {
				return nil, nil, nil, fmt.Errorf("tx [%v] emits too many cross-region ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXRCount, etxRLimit), nil
			}
			if ETXPCount > *etxPLimit {
				return nil, nil, nil, fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXPCount, etxPLimit), nil
			}
			if !toAddr.IsInQiLedgerScope() {
				return nil, nil, nil, fmt.Errorf("tx [%v] emits UTXO with To address not in the Qi ledger scope", tx.Hash().Hex()), nil
			}
			if !chain.CheckIfEtxIsEligible(primeTerminusHeader.EtxEligibleSlices(), *toAddr.Location()) {
				return nil, nil, nil, fmt.Errorf("etx emitted by tx [%v] going to a slice that is not eligible to receive etx %v", tx.Hash().Hex(), *toAddr.Location()), nil
			}

			// We should require some kind of extra fee here
			etxInner := types.ExternalTx{Value: big.NewInt(int64(txOut.Denomination)), To: &toAddr, Sender: common.ZeroAddress(location), EtxType: types.DefaultType, OriginatingTxHash: tx.Hash(), ETXIndex: uint16(txOutIdx), Gas: params.TxGas}
			*usedGas += params.ETXGas
			if err := gp.SubGas(params.ETXGas); err != nil {
				return nil, nil, nil, err, nil
			}
			etxs = append(etxs, &etxInner)
		} else {
			// This output creates a normal UTXO
			utxo := types.NewUtxoEntry(&txOut)
			if err := rawdb.CreateUTXO(batch, tx.Hash(), uint16(txOutIdx), utxo); err != nil {
				return nil, nil, nil, err, nil
			}
			utxosCreatedDeleted.UtxosCreatedHashes = append(utxosCreatedDeleted.UtxosCreatedHashes, types.UTXOHash(tx.Hash(), uint16(txOutIdx), utxo))
			utxosCreatedDeleted.UtxosCreatedKeys = append(utxosCreatedDeleted.UtxosCreatedKeys, rawdb.UtxoKeyWithDenomination(tx.Hash(), uint16(txOutIdx), utxo.Denomination))
		}
	}
	elapsedTime = time.Since(stepStart)
	stepTimings["Output Processing"] = elapsedTime

	// Start timing for fee verification
	stepStart = time.Now()
	// Ensure the transaction does not spend more than its inputs.
	if totalQitOut.Cmp(totalQitIn) == 1 {
		str := fmt.Sprintf("total value of all transaction inputs for "+
			"transaction %v is %v which is less than the amount "+
			"spent of %v", tx.Hash(), totalQitIn, totalQitOut)
		return nil, nil, nil, errors.New(str), nil
	}

	// the fee to pay the basefee/miner is the difference between inputs and outputs
	txFeeInQit := new(big.Int).Sub(totalQitIn, totalQitOut)
	qiTxFee = new(big.Int).Set(txFeeInQit)
	// Check tx against required base fee and gas
	requiredGas := intrinsicGas + (uint64(len(etxs)) * (params.TxGas + params.ETXGas)) // Each ETX costs extra gas that is paid in the origin
	if requiredGas < intrinsicGas {
		// Overflow
		return nil, nil, nil, fmt.Errorf("tx %032x has too many ETXs to calculate required gas", tx.Hash()), nil
	}
	minimumFeeInQuai := new(big.Int).Mul(big.NewInt(int64(requiredGas)), currentHeader.BaseFee())
	parent := chain.GetBlockByHash(currentHeader.ParentHash(common.ZONE_CTX))
	if parent == nil {
		return nil, nil, nil, fmt.Errorf("parent cannot be found for the block"), nil
	}
	txFeeInQuai := misc.QiToQuai(parent, txFeeInQit)
	if txFeeInQuai.Cmp(minimumFeeInQuai) < 0 {
		return nil, nil, nil, fmt.Errorf("tx %032x has insufficient fee for base fee, have %d want %d", tx.Hash(), txFeeInQuai.Uint64(), minimumFeeInQuai.Uint64()), nil
	}
	if conversion {
		// Since this transaction contains a conversion, the rest of the tx gas is given to conversion
		remainingTxFeeInQuai := misc.QiToQuai(parent, txFeeInQit)
		// Fee is basefee * gas, so gas remaining is fee remaining / basefee
		remainingGas := new(big.Int).Div(remainingTxFeeInQuai, currentHeader.BaseFee())
		if remainingGas.Uint64() > (currentHeader.GasLimit() / params.MinimumEtxGasDivisor) {
			// Limit ETX gas to max ETX gas limit (the rest is burned)
			remainingGas = new(big.Int).SetUint64(currentHeader.GasLimit() / params.MinimumEtxGasDivisor)
		}
		ETXPCount++
		if ETXPCount > *etxPLimit {
			return nil, nil, nil, fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXPCount, etxPLimit), nil
		}
		etxInner := types.ExternalTx{Value: totalConvertQitOut, To: &convertAddress, Sender: common.ZeroAddress(location), EtxType: types.ConversionType, OriginatingTxHash: tx.Hash(), Gas: remainingGas.Uint64()} // Value is in Qits not Denomination
		*usedGas += params.ETXGas
		if err := gp.SubGas(params.ETXGas); err != nil {
			return nil, nil, nil, err, nil
		}
		etxs = append(etxs, &etxInner)
		txFeeInQit.Sub(txFeeInQit, txFeeInQit) // Fee goes entirely to gas to pay for conversion
	}
	elapsedTime = time.Since(stepStart)
	stepTimings["Fee Verification"] = elapsedTime

	// Start timing for signature check
	stepStart = time.Now()
	if !isFirstQiTx {
		if err := CheckDenominations(inputs, outputs); err != nil {
			return nil, nil, nil, err, nil
		}
	}
	// Ensure the transaction signature is valid
	if checkSig {
		var finalKey *btcec.PublicKey
		if len(tx.TxIn()) > 1 {
			aggKey, _, _, err := musig2.AggregateKeys(
				pubKeys, false,
			)
			if err != nil {
				return nil, nil, nil, err, nil
			}
			finalKey = aggKey.FinalKey
		} else {
			finalKey = pubKeys[0]
		}

		txDigestHash := signer.Hash(tx)
		if !tx.GetSchnorrSignature().Verify(txDigestHash[:], finalKey) {
			return nil, nil, nil, errors.New("invalid signature for digest hash " + txDigestHash.String()), nil
		}
	}

	*etxRLimit -= ETXRCount
	*etxPLimit -= ETXPCount
	elapsedTime = time.Since(stepStart)
	stepTimings["Signature Check"] = elapsedTime

	return qiTxFee, txFeeInQit, etxs, nil, stepTimings
}

// Go through all denominations largest to smallest, check if the input exists as the output, if not, convert it to the respective number of bills for the next smallest denomination, then repeat the check. Subtract the 'carry' when the outputs match the carry for that denomination.
func CheckDenominations(inputs, outputs map[uint]uint64) error {
	carries := make(map[uint]uint64)
	for i := types.MaxDenomination; i >= 1; i-- {
		// Calculate total inputs including carry from the previous denomination
		totalInputs := inputs[uint(i)] + carries[uint(i)]

		// Check if the total inputs are sufficient to cover the outputs
		if outputs[uint(i)] <= totalInputs {
			// Calculate the difference (excess input) and carry it to the next smaller denomination
			diff := new(big.Int).SetUint64(totalInputs - outputs[uint(i)])
			carries[uint(i-1)] += diff.Mul(diff, new(big.Int).Div(types.Denominations[uint8(i)], types.Denominations[uint8(i-1)])).Uint64()
		} else {
			return fmt.Errorf("tx attempts to combine smaller denominations into larger one for denomination %d", i)
		}
	}

	return nil
}

// Apply State
func (p *StateProcessor) Apply(batch ethdb.Batch, block *types.WorkObject) ([]*types.Log, error) {
	nodeCtx := p.hc.NodeCtx()
	start := time.Now()
	blockHash := block.Hash()

	parentHash := block.ParentHash(nodeCtx)
	if p.hc.IsGenesisHash(block.ParentHash(nodeCtx)) {
		parent := p.hc.GetHeaderByHash(parentHash)
		if parent == nil {
			return nil, errors.New("failed to load parent block")
		}
	}
	time1 := common.PrettyDuration(time.Since(start))
	time2 := common.PrettyDuration(time.Since(start))
	// Process our block
	receipts, etxs, logs, statedb, usedGas, usedState, utxoSetSize, multiSet, err := p.Process(block, batch)
	if err != nil {
		return nil, err
	}
	if block.Hash() != blockHash {
		p.logger.WithFields(log.Fields{
			"oldHash": blockHash,
			"newHash": block.Hash(),
		}).Warn("Block hash changed after Processing the block")
	}
	time3 := common.PrettyDuration(time.Since(start))
	err = p.validator.ValidateState(block, statedb, receipts, etxs, multiSet, usedGas, usedState)
	if err != nil {
		return nil, err
	}
	time4 := common.PrettyDuration(time.Since(start))
	rawdb.WriteReceipts(batch, block.Hash(), block.NumberU64(nodeCtx), receipts)
	time4_5 := common.PrettyDuration(time.Since(start))
	// Create bloom filter and write it to cache/db
	bloom := types.CreateBloom(receipts)
	p.hc.AddBloom(bloom, block.Hash())
	time5 := common.PrettyDuration(time.Since(start))
	rawdb.WritePreimages(batch, statedb.Preimages())
	time6 := common.PrettyDuration(time.Since(start))
	// Commit all cached state changes into underlying memory database.
	root, err := statedb.Commit(true)
	if err != nil {
		return nil, err
	}
	etxRoot, err := statedb.CommitEtxs()
	if err != nil {
		return nil, err
	}

	time7 := common.PrettyDuration(time.Since(start))
	var time8 common.PrettyDuration
	if err := p.stateCache.TrieDB().Commit(root, false, nil); err != nil {
		return nil, err
	}
	if err := p.etxCache.TrieDB().Commit(etxRoot, false, nil); err != nil {
		return nil, err
	}
	time8 = common.PrettyDuration(time.Since(start))

	p.logger.WithFields(log.Fields{
		"t1":   time1,
		"t2":   time2,
		"t3":   time3,
		"t4":   time4,
		"t4.5": time4_5,
		"t5":   time5,
		"t6":   time6,
		"t7":   time7,
		"t8":   time8,
	}).Info("times during state processor apply")
	rawdb.WriteMultiSet(batch, block.Hash(), multiSet)
	rawdb.WriteUTXOSetSize(batch, block.Hash(), utxoSetSize)
	// Indicate that we have processed the state of the block
	rawdb.WriteProcessedState(batch, block.Hash())
	return logs, nil
}

// ApplyTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. It returns the receipt
// for the transaction, gas used and an error if the transaction failed,
// indicating the block was invalid.
func ApplyTransaction(config *params.ChainConfig, parent *types.WorkObject, parentOrder int, bc ChainContext, author *common.Address, gp *types.GasPool, statedb *state.StateDB, header *types.WorkObject, tx *types.Transaction, usedGas *uint64, usedState *uint64, cfg vm.Config, etxRLimit, etxPLimit *int, logger *log.Logger) (*types.Receipt, *big.Int, error) {
	nodeCtx := config.Location.Context()
	msg, err := tx.AsMessage(types.MakeSigner(config, header.Number(nodeCtx)), header.BaseFee())
	if err != nil {
		return nil, nil, err
	}
	if tx.Type() == types.ExternalTxType && tx.ETXSender().Location().Equal(*tx.To().Location()) { // Qi->Quai Conversion
		msg.SetLock(new(big.Int).Add(header.Number(nodeCtx), new(big.Int).SetUint64(params.ConversionLockPeriod)))
		var primeTerminus *types.WorkObject
		if parentOrder == common.PRIME_CTX {
			primeTerminus = parent
		} else {
			primeTerminus = bc.GetHeaderByHash(header.PrimeTerminusHash())
			if primeTerminus == nil {
				return nil, nil, fmt.Errorf("could not find prime terminus header %032x", header.PrimeTerminusHash())
			}
		}
		// Convert Qi to Quai
		msg.SetValue(misc.QiToQuai(parent, tx.Value()))
		msg.SetData([]byte{}) // data is not used in conversion
	}
	// Create a new context to be used in the EVM environment
	blockContext, err := NewEVMBlockContext(header, parent, bc, author)
	if err != nil {
		return nil, nil, err
	}
	vmenv := vm.NewEVM(blockContext, vm.TxContext{}, statedb, config, cfg)
	if tx.Type() == types.ExternalTxType {
		prevZeroBal := prepareApplyETX(statedb, msg.Value(), config.Location)
		receipt, quaiFees, err := applyTransaction(msg, parent, config, bc, gp, statedb, header.Number(nodeCtx), header.Hash(), tx, usedGas, usedState, vmenv, etxRLimit, etxPLimit, logger)
		statedb.SetBalance(common.ZeroInternal(config.Location), prevZeroBal) // Reset the balance to what it previously was (currently a failed external transaction removes all the sent coins from the supply and any residual balance is gone as well)
		return receipt, quaiFees, err
	}
	return applyTransaction(msg, parent, config, bc, gp, statedb, header.Number(nodeCtx), header.Hash(), tx, usedGas, usedState, vmenv, etxRLimit, etxPLimit, logger)
}

// GetVMConfig returns the block chain VM config.
func (p *StateProcessor) GetVMConfig() *vm.Config {
	return &p.vmConfig
}

// State returns a new mutable state based on the current HEAD block.
func (p *StateProcessor) State() (*state.StateDB, error) {
	return p.StateAt(p.hc.CurrentHeader().EVMRoot(), p.hc.CurrentHeader().EtxSetRoot(), p.hc.CurrentHeader().QuaiStateSize())
}

// StateAt returns a new mutable state based on a particular point in time.
func (p *StateProcessor) StateAt(root, etxRoot common.Hash, quaiStateSize *big.Int) (*state.StateDB, error) {
	return state.New(root, etxRoot, quaiStateSize, p.stateCache, p.etxCache, p.snaps, p.hc.NodeLocation(), p.logger)
}

// StateCache returns the caching database underpinning the blockchain instance.
func (p *StateProcessor) StateCache() state.Database {
	return p.stateCache
}

// HasState checks if state trie is fully present in the database or not.
func (p *StateProcessor) HasState(hash common.Hash) bool {
	_, err := p.stateCache.OpenTrie(hash)
	return err == nil
}

// HasBlockAndState checks if a block and associated state trie is fully present
// in the database or not, caching it if present.
func (p *StateProcessor) HasBlockAndState(hash common.Hash, number uint64) bool {
	// Check first that the block itself is known
	block := p.hc.GetBlock(hash, number)
	if block == nil {
		return false
	}
	return p.HasState(block.EVMRoot())
}

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (p *StateProcessor) GetReceiptsByHash(hash common.Hash) types.Receipts {
	if receipts, ok := p.receiptsCache.Get(hash); ok {
		return receipts
	}
	number := rawdb.ReadHeaderNumber(p.hc.headerDb, hash)
	if number == nil {
		return nil
	}
	receipts := rawdb.ReadReceipts(p.hc.headerDb, hash, *number, p.hc.config)
	if receipts == nil {
		return nil
	}
	p.receiptsCache.Add(hash, receipts)
	return receipts
}

// GetTransactionLookup retrieves the lookup associate with the given transaction
// hash from the cache or database.
func (p *StateProcessor) GetTransactionLookup(hash common.Hash) *rawdb.LegacyTxLookupEntry {
	// Short circuit if the txlookup already in the cache, retrieve otherwise
	if lookup, exist := p.txLookupCache.Get(hash); exist {
		return &lookup
	}
	tx, blockHash, blockNumber, txIndex := rawdb.ReadTransaction(p.hc.headerDb, hash)
	if tx == nil {
		return nil
	}
	lookup := &rawdb.LegacyTxLookupEntry{BlockHash: blockHash, BlockIndex: blockNumber, Index: txIndex}
	p.txLookupCache.Add(hash, *lookup)
	return lookup
}

// ContractCode retrieves a blob of data associated with a contract hash
// either from ephemeral in-memory cache, or from persistent storage.
func (p *StateProcessor) ContractCode(hash common.Hash) ([]byte, error) {
	return p.stateCache.ContractCode(common.Hash{}, hash)
}

// either from ephemeral in-memory cache, or from persistent storage.
func (p *StateProcessor) TrieNode(hash common.Hash) ([]byte, error) {
	return p.stateCache.TrieDB().Node(hash)
}

// ContractCodeWithPrefix retrieves a blob of data associated with a contract
// hash either from ephemeral in-memory cache, or from persistent storage.
//
// If the code doesn't exist in the in-memory cache, check the storage with
// new code scheme.
func (p *StateProcessor) ContractCodeWithPrefix(hash common.Hash) ([]byte, error) {
	type codeReader interface {
		ContractCodeWithPrefix(addrHash, codeHash common.Hash) ([]byte, error)
	}
	return p.stateCache.(codeReader).ContractCodeWithPrefix(common.Hash{}, hash)
}

// StateAtBlock retrieves the state database associated with a certain block.
// If no state is locally available for the given block, a number of blocks
// are attempted to be reexecuted to generate the desired state. The optional
// base layer statedb can be passed then it's regarded as the statedb of the
// parent block.
// Parameters:
//   - block: The block for which we want the state (== state at the evmRoot of the parent)
//   - reexec: The maximum number of blocks to reprocess trying to obtain the desired state
//   - base: If the caller is tracing multiple blocks, the caller can provide the parent state
//     continuously from the callsite.
//   - checklive: if true, then the live 'blockchain' state database is used. If the caller want to
//     perform Commit or other 'save-to-disk' changes, this should be set to false to avoid
//     storing trash persistently
func (p *StateProcessor) StateAtBlock(block *types.WorkObject, reexec uint64, base *state.StateDB, checkLive bool) (statedb *state.StateDB, err error) {
	var (
		current      *types.WorkObject
		database     state.Database
		etxDatabase  state.Database
		report       = true
		nodeLocation = p.hc.NodeLocation()
		nodeCtx      = p.hc.NodeCtx()
		origin       = block.NumberU64(nodeCtx)
		batch        = p.hc.headerDb.NewBatch()
	)
	// Check the live database first if we have the state fully available, use that.
	if checkLive {
		statedb, err = p.StateAt(block.EVMRoot(), block.EtxSetRoot(), block.QuaiStateSize())
		if err == nil {
			return statedb, nil
		}
	}

	var newHeads []*types.WorkObject
	if base != nil {
		// The optional base statedb is given, mark the start point as parent block
		statedb, database, etxDatabase, report = base, base.Database(), base.ETXDatabase(), false
		current = p.hc.GetHeaderOrCandidateByHash(block.ParentHash(nodeCtx))
	} else {
		// Otherwise try to reexec blocks until we find a state or reach our limit
		current = types.CopyWorkObject(block)

		// Create an ephemeral trie.Database for isolating the live one. Otherwise
		// the internal junks created by tracing will be persisted into the disk.
		database = state.NewDatabaseWithConfig(p.hc.headerDb, &trie.Config{Cache: 16})
		// Create an ephemeral trie.Database for isolating the live one. Otherwise
		// the internal junks created by tracing will be persisted into the disk.
		etxDatabase = state.NewDatabaseWithConfig(p.hc.headerDb, &trie.Config{Cache: 16})

		// If we didn't check the dirty database, do check the clean one, otherwise
		// we would rewind past a persisted block (specific corner case is chain
		// tracing from the genesis).
		if !checkLive {
			statedb, err = state.New(current.EVMRoot(), current.EtxSetRoot(), current.QuaiStateSize(), database, etxDatabase, nil, nodeLocation, p.logger)
			if err == nil {
				return statedb, nil
			}
		}
		newHeads = append(newHeads, current)
		// Database does not have the state for the given block, try to regenerate
		for i := uint64(0); i < reexec; i++ {
			if current.NumberU64(nodeCtx) == 0 {
				return nil, errors.New("genesis state is missing")
			}
			parent := p.hc.GetHeaderOrCandidateByHash(current.ParentHash(nodeCtx))
			if parent == nil {
				return nil, fmt.Errorf("missing block %v %d", current.ParentHash(nodeCtx), current.NumberU64(nodeCtx)-1)
			}
			current = types.CopyWorkObject(parent)

			statedb, err = state.New(current.EVMRoot(), current.EtxSetRoot(), current.QuaiStateSize(), database, etxDatabase, nil, nodeLocation, p.logger)
			if err == nil {
				break
			}
			newHeads = append(newHeads, current)
		}
		if err != nil {
			switch err.(type) {
			case *trie.MissingNodeError:
				return nil, fmt.Errorf("required historical state unavailable (reexec=%d)", reexec)
			default:
				return nil, err
			}
		}
	}
	// State was available at historical point, regenerate
	var (
		start  = time.Now()
		logged time.Time
		parent common.Hash
	)
	for i := len(newHeads) - 1; i >= 0; i-- {
		current := newHeads[i]
		// Print progress logs if long enough time elapsed
		if time.Since(logged) > 8*time.Second && report {
			p.logger.WithFields(log.Fields{
				"block":     current.NumberU64(nodeCtx) + 1,
				"target":    origin,
				"remaining": origin - current.NumberU64(nodeCtx) - 1,
				"elapsed":   time.Since(start),
			}).Info("Regenerating historical state")
			logged = time.Now()
		}
		currentBlock := rawdb.ReadWorkObject(p.hc.bc.db, current.NumberU64(nodeCtx), current.Hash(), types.BlockObject)
		if currentBlock == nil {
			return nil, errors.New("detached block found trying to regenerate state")
		}
		_, _, _, _, _, _, _, _, err := p.Process(currentBlock, batch)
		if err != nil {
			return nil, fmt.Errorf("processing block %d failed: %v", current.NumberU64(nodeCtx), err)
		}
		// Finalize the state so any modifications are written to the trie
		root, err := statedb.Commit(true)
		if err != nil {
			return nil, fmt.Errorf("stateAtBlock commit failed, number %d root %v: %w",
				current.NumberU64(nodeCtx), current.EVMRoot().Hex(), err)
		}
		etxRoot, err := statedb.CommitEtxs()
		if err != nil {
			return nil, fmt.Errorf("stateAtBlock commit failed, number %d root %v: %w",
				current.NumberU64(nodeCtx), current.EVMRoot().Hex(), err)
		}
		statedb, err = state.New(root, etxRoot, currentBlock.QuaiStateSize(), database, etxDatabase, nil, nodeLocation, p.logger)
		if err != nil {
			return nil, fmt.Errorf("state reset after block %d failed: %v", current.NumberU64(nodeCtx), err)
		}
		database.TrieDB().Reference(root, common.Hash{})
		if parent != (common.Hash{}) {
			database.TrieDB().Dereference(parent)
		}
		parent = root
	}
	if report {
		nodes, imgs := database.TrieDB().Size()
		p.logger.WithFields(log.Fields{
			"block":   current.NumberU64(nodeCtx),
			"elapsed": time.Since(start),
			"nodes":   nodes,
			"preimgs": imgs,
		}).Info("Historical state regenerated")
	}
	return statedb, nil
}

// stateAtTransaction returns the execution environment of a certain transaction.
func (p *StateProcessor) StateAtTransaction(block *types.WorkObject, txIndex int, reexec uint64) (Message, vm.BlockContext, *state.StateDB, error) {
	nodeCtx := p.hc.NodeCtx()
	// Short circuit if it's genesis block.
	if block.NumberU64(nodeCtx) == 0 {
		return nil, vm.BlockContext{}, nil, errors.New("no transaction in genesis")
	}
	// Create the parent state database
	parent := p.hc.GetBlock(block.ParentHash(nodeCtx), block.NumberU64(nodeCtx)-1)
	if parent == nil {
		return nil, vm.BlockContext{}, nil, fmt.Errorf("parent %#x not found", block.ParentHash(nodeCtx))
	}
	// Lookup the statedb of parent block from the live database,
	// otherwise regenerate it on the flight.
	statedb, err := p.StateAtBlock(parent, reexec, nil, true)
	if err != nil {
		return nil, vm.BlockContext{}, nil, err
	}
	if txIndex == 0 && len(block.Transactions()) == 0 {
		return nil, vm.BlockContext{}, statedb, nil
	}
	// Recompute transactions up to the target index.
	signer := types.MakeSigner(p.hc.Config(), block.Number(nodeCtx))
	for idx, tx := range block.Transactions() {
		// Assemble the transaction call message and return if the requested offset
		msg, _ := tx.AsMessage(signer, block.BaseFee())
		txContext := NewEVMTxContext(msg)
		context, err := NewEVMBlockContext(block, parent, p.hc, nil)
		if err != nil {
			return nil, vm.BlockContext{}, nil, err
		}
		if idx == txIndex {
			return msg, context, statedb, nil
		}
		// Not yet the searched for transaction, execute on top of the current state
		vmenv := vm.NewEVM(context, txContext, statedb, p.hc.Config(), vm.Config{})
		statedb.Prepare(tx.Hash(), idx)
		if _, err := ApplyMessage(vmenv, msg, new(types.GasPool).AddGas(tx.Gas())); err != nil {
			return nil, vm.BlockContext{}, nil, fmt.Errorf("transaction %#x failed: %v", tx.Hash(), err)
		}
		// Ensure any modifications are committed to the state
		statedb.Finalise(true)
	}
	return nil, vm.BlockContext{}, nil, fmt.Errorf("transaction index %d out of range for block %#x", txIndex, block.Hash())
}

func (p *StateProcessor) Stop() {
	// Ensure all live cached entries be saved into disk, so that we can skip
	// cache warmup when node restarts.
	if p.cacheConfig.TrieCleanJournal != "" {
		triedb := p.stateCache.TrieDB()
		triedb.SaveCache(p.cacheConfig.TrieCleanJournal)
	}
	if p.cacheConfig.ETXTrieCleanJournal != "" {
		etxTrieDB := p.etxCache.TrieDB()
		etxTrieDB.SaveCache(p.cacheConfig.ETXTrieCleanJournal)
	}
	close(p.quit)
	p.logger.Info("State Processor stopped")
}

func prepareApplyETX(statedb *state.StateDB, value *big.Int, nodeLocation common.Location) *big.Int {
	prevZeroBal := statedb.GetBalance(common.ZeroInternal(nodeLocation)) // Get current zero address balance
	statedb.SetBalance(common.ZeroInternal(nodeLocation), value)         // Use zero address at temp placeholder and set it to value
	return prevZeroBal
}
