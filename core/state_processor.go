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
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	lru "github.com/hashicorp/golang-lru"

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
	TrieCleanLimit       int    // Memory allowance (MB) to use for caching trie nodes in memory
	TrieCleanJournal     string // Disk journal for saving clean cache entries.
	UTXOTrieCleanJournal string
	ETXTrieCleanJournal  string
	TrieCleanRejournal   time.Duration // Time interval to dump clean cache to disk periodically
	TrieCleanNoPrefetch  bool          // Whether to disable heuristic state prefetching for followup blocks
	TrieDirtyLimit       int           // Memory limit (MB) at which to start flushing dirty trie nodes to disk
	TrieTimeLimit        time.Duration // Time limit after which to flush the current in-memory trie to disk
	SnapshotLimit        int           // Memory allowance (MB) to use for caching snapshot entries in memory
	Preimages            bool          // Whether to store preimage of trie key to the disk
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
	cacheConfig   *CacheConfig   // CacheConfig for StateProcessor
	stateCache    state.Database // State database to reuse between imports (contains state cache)
	utxoCache     state.Database // UTXO database to reuse between imports (contains UTXO cache)
	etxCache      state.Database // ETX database to reuse between imports (contains ETX cache)
	receiptsCache *lru.Cache     // Cache for the most recent receipts per block
	txLookupCache *lru.Cache
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
	receiptsCache, _ := lru.New(receiptsCacheLimit)
	txLookupCache, _ := lru.New(txLookupCacheLimit)

	if cacheConfig == nil {
		cacheConfig = defaultCacheConfig
	}

	sp := &StateProcessor{
		config:        config,
		hc:            hc,
		receiptsCache: receiptsCache,
		txLookupCache: txLookupCache,
		vmConfig:      vmConfig,
		cacheConfig:   cacheConfig,
		stateCache: state.NewDatabaseWithConfig(hc.headerDb, &trie.Config{
			Cache:     cacheConfig.TrieCleanLimit,
			Journal:   cacheConfig.TrieCleanJournal,
			Preimages: cacheConfig.Preimages,
		}),
		utxoCache: state.NewDatabaseWithConfig(hc.headerDb, &trie.Config{
			Cache:     cacheConfig.TrieCleanLimit,
			Journal:   cacheConfig.UTXOTrieCleanJournal,
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
		utxoTrieDb := sp.utxoCache.TrieDB()
		etxTrieDb := sp.etxCache.TrieDB()
		sp.wg.Add(1)
		go func() {
			defer sp.wg.Done()
			triedb.SaveCachePeriodically(sp.cacheConfig.TrieCleanJournal, sp.cacheConfig.TrieCleanRejournal, sp.quit)
			utxoTrieDb.SaveCachePeriodically(sp.cacheConfig.UTXOTrieCleanJournal, sp.cacheConfig.TrieCleanRejournal, sp.quit)
			etxTrieDb.SaveCachePeriodically(sp.cacheConfig.ETXTrieCleanJournal, sp.cacheConfig.TrieCleanRejournal, sp.quit)
		}()
	}
	return sp
}

// Process processes the state changes according to the Quai rules by running
// the transaction messages using the statedb and applying any rewards to both
// the processor (coinbase) and any included uncles.
//
// Process returns the receipts and logs accumulated during the process and
// returns the amount of gas that was used in the process. If any of the
// transactions failed to execute due to insufficient gas it will return an error.
func (p *StateProcessor) Process(block *types.WorkObject) (types.Receipts, []*types.Transaction, []*types.Log, *state.StateDB, uint64, error) {
	var (
		receipts     types.Receipts
		usedGas      = new(uint64)
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
		return types.Receipts{}, []*types.Transaction{}, []*types.Log{}, nil, 0, errors.New("parent block is nil for the block given to process")
	}
	time1 := common.PrettyDuration(time.Since(start))

	parentEvmRoot := parent.Header().EVMRoot()
	parentUtxoRoot := parent.Header().UTXORoot()
	parentEtxSetRoot := parent.Header().EtxSetRoot()
	if p.hc.IsGenesisHash(parent.Hash()) {
		parentEvmRoot = types.EmptyRootHash
		parentUtxoRoot = types.EmptyRootHash
		parentEtxSetRoot = types.EmptyRootHash
	}
	// Initialize a statedb
	statedb, err := state.New(parentEvmRoot, parentUtxoRoot, parentEtxSetRoot, p.stateCache, p.utxoCache, p.etxCache, p.snaps, nodeLocation, p.logger)
	if err != nil {
		return types.Receipts{}, []*types.Transaction{}, []*types.Log{}, nil, 0, err
	}
	if len(block.Transactions()) == 0 {
		return types.Receipts{}, []*types.Transaction{}, []*types.Log{}, statedb, 0, nil
	}
	// Apply the previous inbound ETXs to the ETX set state
	prevInboundEtxs := rawdb.ReadInboundEtxs(p.hc.bc.db, header.ParentHash(nodeCtx))
	if len(prevInboundEtxs) > 0 {
		if err := statedb.PushETXs(prevInboundEtxs); err != nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("could not push prev inbound etxs: %w", err)
		}
	}
	time2 := common.PrettyDuration(time.Since(start))

	var timeSenders, timeSign, timePrepare, timeEtx, timeTx time.Duration
	startTimeSenders := time.Now()
	transactions := block.Transactions()
	if types.IsCoinBaseTx(transactions[0], header.ParentHash(nodeCtx), nodeLocation) {
		// coinbase tx is not in senders cache
		transactions = transactions[1:]
	}
	numGoroutines := 4
	if len(transactions) > 1000 {
		numGoroutines = 8
	} else if len(transactions) < 12 {
		numGoroutines = 1
	}
	groupSize := len(transactions) / numGoroutines // Split the block into n groups for parallel processing
	var wg sync.WaitGroup
	numInternalTxs := len(transactions)
	// Each goroutine has a sub-map of senders (temporary cache for senders of internal txs)
	var sendersArray []map[common.Hash]*common.InternalAddress = make([]map[common.Hash]*common.InternalAddress, numGoroutines)
	p.hc.pool.SendersMu.RLock() // Prevent the txpool from grabbing the lock during the entire block tx lookup
	for i := 0; i < numGoroutines; i++ {
		sendersArray[i] = make(map[common.Hash]*common.InternalAddress) // Initialize the sub-map
		wg.Add(1)
		go func(i int) {
			var subTransactions types.Transactions
			if i == numGoroutines-1 {
				subTransactions = transactions[i*groupSize:]
			} else {
				subTransactions = transactions[i*groupSize : (i+1)*groupSize]
			}
			for _, tx := range subTransactions {
				if tx.Type() == types.QuaiTxType {
					if sender, ok := p.hc.pool.PeekSenderNoLock(tx.Hash()); ok {
						sendersArray[i][tx.Hash()] = &sender // This pointer must never be modified
					} else {
						// TODO: calcuate the sender and add it to the pool senders cache in case of reorg (not necessary for now)
					}
				} else if tx.Type() == types.QiTxType {
					if _, ok := p.hc.pool.PeekSenderNoLock(tx.Hash()); ok {
						sendersArray[i][tx.Hash()] = &common.InternalAddress{}
					}
				}
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
	p.hc.pool.SendersMu.RUnlock()

	// Merge the sub-maps into the main map
	senders := make(map[common.Hash]*common.InternalAddress)
	for _, subMap := range sendersArray {
		for k, v := range subMap {
			senders[k] = v
		}
	}
	sendersArray = nil

	timeSenders = time.Since(startTimeSenders)
	blockContext, err := NewEVMBlockContext(header, p.hc, nil)
	if err != nil {
		return nil, nil, nil, nil, 0, err
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
	totalFees := big.NewInt(0)
	qiEtxs := make([]*types.Transaction, 0)
	var totalQiTime time.Duration
	for i, tx := range block.Transactions() {
		if i == 0 && types.IsCoinBaseTx(tx, header.ParentHash(nodeCtx), nodeLocation) {
			// coinbase tx currently exempt from gas and outputs are added after all txs are processed
			continue
		}
		startProcess := time.Now()
		if tx.Type() == types.QiTxType {
			qiTimeBefore := time.Now()
			checkSig := true
			if _, ok := senders[tx.Hash()]; ok {
				checkSig = false
			}
			fees, etxs, err := ProcessQiTx(tx, p.hc, true, checkSig, header, statedb, gp, usedGas, p.hc.pool.signer, p.hc.NodeLocation(), *p.config.ChainID, &etxRLimit, &etxPLimit)
			if err != nil {
				return nil, nil, nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
			}
			for _, etx := range etxs {
				qiEtxs = append(qiEtxs, types.NewTx(etx))
			}
			totalFees.Add(totalFees, fees)
			totalQiTime += time.Since(qiTimeBefore)
			continue
		}

		msg, err := tx.AsMessageWithSender(types.MakeSigner(p.config, header.Number(nodeCtx)), header.BaseFee(), senders[tx.Hash()])
		if err != nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		timeSignDelta := time.Since(startProcess)
		timeSign += timeSignDelta

		startTimePrepare := time.Now()
		statedb.Prepare(tx.Hash(), i)
		timePrepareDelta := time.Since(startTimePrepare)
		timePrepare += timePrepareDelta

		var receipt *types.Receipt
		if tx.Type() == types.ExternalTxType {
			startTimeEtx := time.Now()
			// ETXs MUST be included in order, so popping the first from the queue must equal the first in the block
			etx, err := statedb.PopETX()
			if err != nil {
				return nil, nil, nil, nil, 0, fmt.Errorf("could not pop etx from statedb: %w", err)
			}
			if etx == nil {
				return nil, nil, nil, nil, 0, fmt.Errorf("etx %x is nil", tx.Hash())
			}
			if etx.Hash() != tx.Hash() {
				return nil, nil, nil, nil, 0, fmt.Errorf("invalid external transaction: etx %x is not in order or not found in unspent etx set", tx.Hash())
			}
			if etx.To().IsInQiLedgerScope() {
				if etx.ETXSender().Location().Equal(*etx.To().Location()) { // Quai->Qi Conversion
					lock := new(big.Int).Add(header.Number(nodeCtx), big.NewInt(params.ConversionLockPeriod))
					primeTerminus := p.hc.GetHeaderByHash(header.PrimeTerminus())
					if primeTerminus == nil {
						return nil, nil, nil, nil, 0, fmt.Errorf("could not find prime terminus header %032x", header.PrimeTerminus())
					}
					value := misc.QuaiToQi(primeTerminus, etx.Value()) // convert Quai to Qi
					txGas := etx.Gas()
					denominations := misc.FindMinDenominations(value)
					outputIndex := uint16(0)
					// Iterate over the denominations in descending order
					for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
						// If the denomination count is zero, skip it
						if denominations[uint8(denomination)] == 0 {
							continue
						}
						for j := uint8(0); j < denominations[uint8(denomination)]; j++ {
							if txGas < params.CallValueTransferGas || outputIndex >= types.MaxOutputIndex {
								// No more gas, the rest of the denominations are lost but the tx is still valid
								break
							}
							txGas -= params.CallValueTransferGas
							if err := gp.SubGas(params.CallValueTransferGas); err != nil {
								return nil, nil, nil, nil, 0, err
							}
							*usedGas += params.CallValueTransferGas    // In the future we may want to determine what a fair gas cost is
							totalEtxGas += params.CallValueTransferGas // In the future we may want to determine what a fair gas cost is
							// the ETX hash is guaranteed to be unique
							if err := statedb.CreateUTXO(etx.Hash(), outputIndex, types.NewUtxoEntry(types.NewTxOut(uint8(denomination), etx.To().Bytes(), lock))); err != nil {
								return nil, nil, nil, nil, 0, err
							}
							log.Global.Infof("Converting Quai to Qi %032x with denomination %d index %d lock %d", tx.Hash(), denomination, outputIndex, lock)
							outputIndex++
						}
					}
				} else {
					// There are no more checks to be made as the ETX is worked so add it to the set
					if err := statedb.CreateUTXO(etx.OriginatingTxHash(), etx.ETXIndex(), types.NewUtxoEntry(types.NewTxOut(uint8(etx.Value().Uint64()), etx.To().Bytes(), big.NewInt(0)))); err != nil {
						return nil, nil, nil, nil, 0, err
					}
					// This Qi ETX should cost more gas
					if err := gp.SubGas(params.CallValueTransferGas); err != nil {
						return nil, nil, nil, nil, 0, err
					}
					*usedGas += params.CallValueTransferGas    // In the future we may want to determine what a fair gas cost is
					totalEtxGas += params.CallValueTransferGas // In the future we may want to determine what a fair gas cost is
				}
				timeEtxDelta := time.Since(startTimeEtx)
				timeEtx += timeEtxDelta
				continue
			} else {
				if etx.ETXSender().Location().Equal(*etx.To().Location()) { // Qi->Quai Conversion
					msg.SetLock(new(big.Int).Add(header.Number(nodeCtx), big.NewInt(params.ConversionLockPeriod)))
					primeTerminus := p.hc.GetHeaderByHash(header.PrimeTerminus())
					if primeTerminus == nil {
						return nil, nil, nil, nil, 0, fmt.Errorf("could not find prime terminus header %032x", header.PrimeTerminus())
					}
					// Convert Qi to Quai
					msg.SetValue(misc.QiToQuai(primeTerminus, etx.Value()))
					msg.SetData([]byte{}) // data is not used in conversion
					log.Global.Infof("Converting Qi to Quai for ETX %032x with value %d lock %d", tx.Hash(), msg.Value().Uint64(), msg.Lock().Uint64())
				}
				prevZeroBal := prepareApplyETX(statedb, msg.Value(), nodeLocation)
				receipt, err = applyTransaction(msg, parent, p.config, p.hc, nil, gp, statedb, blockNumber, blockHash, etx, usedGas, vmenv, &etxRLimit, &etxPLimit, p.logger)
				statedb.SetBalance(common.ZeroInternal(nodeLocation), prevZeroBal) // Reset the balance to what it previously was. Residual balance will be lost
				if err != nil {
					return nil, nil, nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
				}
				totalEtxGas += receipt.GasUsed
				timeEtxDelta := time.Since(startTimeEtx)
				timeEtx += timeEtxDelta
			}
		} else if tx.Type() == types.QuaiTxType {
			startTimeTx := time.Now()

			receipt, err = applyTransaction(msg, parent, p.config, p.hc, nil, gp, statedb, blockNumber, blockHash, tx, usedGas, vmenv, &etxRLimit, &etxPLimit, p.logger)
			if err != nil {
				return nil, nil, nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
			}
			timeTxDelta := time.Since(startTimeTx)
			timeTx += timeTxDelta
		} else {
			return nil, nil, nil, nil, 0, ErrTxTypeNotSupported
		}
		receipts = append(receipts, receipt)
		allLogs = append(allLogs, receipt.Logs...)
		i++
	}

	coinbaseTx := block.Transactions()[0]
	// Coinbase check
	if types.IsCoinBaseTx(coinbaseTx, header.ParentHash(nodeCtx), nodeLocation) {
		if coinbaseTx.Type() == types.QiTxType {
			// Public key aggregation for coinbase tx inputs
			pubKeys := make([]*btcec.PublicKey, 0)
			for _, txIn := range coinbaseTx.TxIn() {
				pubKey, err := btcec.ParsePubKey(txIn.PubKey)
				if err != nil {
					return nil, nil, nil, nil, 0, err
				}
				pubKeys = append(pubKeys, pubKey)
			}

			// Ensure the coinbase signature is valid
			var finalKey *btcec.PublicKey
			if len(coinbaseTx.TxIn()) > 1 {
				aggKey, _, _, err := musig2.AggregateKeys(
					pubKeys, false,
				)
				if err != nil {
					return nil, nil, nil, nil, 0, err
				}
				finalKey = aggKey.FinalKey
			} else {
				finalKey = pubKeys[0]
			}
			txDigestHash := p.hc.pool.signer.Hash(coinbaseTx)
			if !coinbaseTx.GetSchnorrSignature().Verify(txDigestHash[:], finalKey) {
				return nil, nil, nil, nil, 0, errors.New("invalid signature for coinbase digest hash " + txDigestHash.String())
			}
			// Ensure the reward is valid
			totalCoinbaseOut := big.NewInt(0)
			for txOutIdx, txOut := range coinbaseTx.TxOut() {
				coinbase := common.BytesToAddress(txOut.Address, nodeLocation)
				if !coinbase.IsInQiLedgerScope() { // a coinbase tx cannot emit a conversion
					return nil, nil, nil, nil, 0, fmt.Errorf("coinbase tx emits UTXO with To address not in the Qi ledger scope")
				}
				if _, err := coinbase.InternalAddress(); err != nil { // a coinbase tx cannot emit an ETX
					return nil, nil, nil, nil, 0, fmt.Errorf("invalid coinbase address %v: %w", coinbase, err)
				}
				if !header.Coinbase().Equal(coinbase) {
					return nil, nil, nil, nil, 0, fmt.Errorf("coinbase tx emits UTXO with To address not equal to block coinbase")
				}
				if txOutIdx > types.MaxOutputIndex {
					return nil, nil, nil, nil, 0, fmt.Errorf("coinbase tx emits UTXO with index %d greater than max uint16", txOutIdx)
				}
				utxo := types.NewUtxoEntry(&txOut)
				if err := statedb.CreateUTXO(coinbaseTx.Hash(), uint16(txOutIdx), utxo); err != nil {
					return nil, nil, nil, nil, 0, fmt.Errorf("could not create UTXO for coinbase tx %032x: %w", coinbaseTx.Hash(), err)
				}
				p.logger.WithFields(log.Fields{
					"txHash":       coinbaseTx.Hash().Hex(),
					"txOutIdx":     txOutIdx,
					"denomination": txOut.Denomination,
				}).Debug("Created Coinbase UTXO")
				totalCoinbaseOut.Add(totalCoinbaseOut, types.Denominations[txOut.Denomination])
			}
			reward := misc.CalculateReward(header)
			maxCoinbaseOut := new(big.Int).Add(reward, totalFees) // TODO: Miner tip will soon no longer exist

			if totalCoinbaseOut.Cmp(maxCoinbaseOut) > 0 {
				return nil, nil, nil, nil, 0, fmt.Errorf("coinbase output value of %v is higher than expected value of %v", totalCoinbaseOut, maxCoinbaseOut)
			}
		} else if coinbaseTx.Type() == types.QuaiTxType {
			// Apply the coinbase transaction to the current state
			reward := misc.CalculateReward(header)
			internal, err := coinbaseTx.To().InternalAddress()
			if err != nil {
				return nil, nil, nil, nil, 0, fmt.Errorf("invalid coinbase address %v: %w", coinbaseTx.To(), err)
			}
			if coinbaseTx.Value().Cmp(reward) > 0 {
				return nil, nil, nil, nil, 0, fmt.Errorf("coinbase tx value %v is greater than expected reward %v", coinbaseTx.Value(), reward)
			}
			// Miner tips are added during EVM tx execution, so not added in the coinbase tx (and will be removed in the future)
			statedb.AddBalance(internal, coinbaseTx.Value())
		} else {
			return nil, nil, nil, nil, 0, errors.New("coinbase tx type not supported")
		}
	}
	etxAvailable := false
	oldestIndex, err := statedb.GetOldestIndex()
	if err != nil {
		return nil, nil, nil, nil, 0, fmt.Errorf("could not get oldest index: %w", err)
	}
	// Check if there is at least one ETX in the set
	etx, err := statedb.ReadETX(oldestIndex)
	if err != nil {
		return nil, nil, nil, nil, 0, fmt.Errorf("could not read etx: %w", err)
	}
	if etx != nil {
		etxAvailable = true
	}
	if (etxAvailable && totalEtxGas < minimumEtxGas) || totalEtxGas > maximumEtxGas {
		return nil, nil, nil, nil, 0, fmt.Errorf("total gas used by ETXs %d is not within the range %d to %d", totalEtxGas, minimumEtxGas, maximumEtxGas)
	}

	time4 := common.PrettyDuration(time.Since(start))
	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	p.engine.Finalize(p.hc, block, statedb)
	time5 := common.PrettyDuration(time.Since(start))

	p.logger.WithFields(log.Fields{
		"signing time":       common.PrettyDuration(timeSign),
		"prepare state time": common.PrettyDuration(timePrepare),
		"etxTime":            common.PrettyDuration(timeEtx),
		"txTime":             common.PrettyDuration(timeTx),
		"totalQiTime":        common.PrettyDuration(totalQiTime),
	}).Info("Total Tx Processing Time")

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
	}).Info("Total Tx Processing Time")

	return receipts, qiEtxs, allLogs, statedb, *usedGas, nil
}

func applyTransaction(msg types.Message, parent *types.WorkObject, config *params.ChainConfig, bc ChainContext, author *common.Address, gp *types.GasPool, statedb *state.StateDB, blockNumber *big.Int, blockHash common.Hash, tx *types.Transaction, usedGas *uint64, evm *vm.EVM, etxRLimit, etxPLimit *int, logger *log.Logger) (*types.Receipt, error) {
	nodeLocation := config.Location
	// Create a new context to be used in the EVM environment.
	txContext := NewEVMTxContext(msg)
	evm.Reset(txContext, statedb)

	// Apply the transaction to the current state (included in the env).
	result, err := ApplyMessage(evm, msg, gp)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("tx %032x emits too many cross-region ETXs for block. emitted: %d, limit: %d", tx.Hash(), ETXRCount, *etxRLimit)
	}
	if ETXPCount > *etxPLimit {
		return nil, fmt.Errorf("tx %032x emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash(), ETXPCount, *etxPLimit)
	}
	*etxRLimit -= ETXRCount
	*etxPLimit -= ETXPCount

	// Update the state with pending changes.
	var root []byte
	statedb.Finalise(true)

	*usedGas += result.UsedGas

	// Create a new receipt for the transaction, storing the intermediate root and gas used
	// by the tx.
	receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: *usedGas, Etxs: result.Etxs}
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
	return receipt, err
}

func ValidateQiTxInputs(tx *types.Transaction, chain ChainContext, statedb *state.StateDB, currentHeader *types.WorkObject, signer types.Signer, location common.Location, chainId big.Int) (*big.Int, error) {
	if tx.Type() != types.QiTxType {
		return nil, fmt.Errorf("tx %032x is not a QiTx", tx.Hash())
	}
	totalQitIn := big.NewInt(0)
	addresses := make(map[common.AddressBytes]struct{})
	for _, txIn := range tx.TxIn() {
		utxo := statedb.GetUTXO(txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
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
	}
	return totalQitIn, nil

}

func ValidateQiTxOutputsAndSignature(tx *types.Transaction, chain ChainContext, totalQitIn *big.Int, currentHeader *types.WorkObject, signer types.Signer, location common.Location, chainId big.Int, etxRLimit, etxPLimit int) (*big.Int, error) {

	intrinsicGas := types.CalculateIntrinsicQiTxGas(tx)
	usedGas := intrinsicGas

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
			primeTerminus := currentHeader.PrimeTerminus()
			primeTerminusHeader := chain.GetHeaderByHash(primeTerminus)
			if primeTerminusHeader == nil {
				return nil, fmt.Errorf("could not find prime terminus header %032x", primeTerminus)
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
	// Check tx against required base fee and gas
	requiredGas := intrinsicGas + (numEtxs * (params.TxGas + params.ETXGas)) // Each ETX costs extra gas that is paid in the origin
	if requiredGas < intrinsicGas {
		// Overflow
		return nil, fmt.Errorf("tx %032x has too many ETXs to calculate required gas", tx.Hash())
	}
	minimumFeeInQuai := new(big.Int).Mul(big.NewInt(int64(requiredGas)), currentHeader.BaseFee())
	minimumFee := misc.QuaiToQi(currentHeader, minimumFeeInQuai)
	if txFeeInQit.Cmp(minimumFee) < 0 {
		return nil, fmt.Errorf("tx %032x has insufficient fee for base fee, have %d want %d", tx.Hash(), txFeeInQit.Uint64(), minimumFee.Uint64())
	}
	// Miner gets remainder of fee after base fee, except in the convert case
	txFeeInQit.Sub(txFeeInQit, minimumFee)
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

	return txFeeInQit, nil
}

// ProcessQiTx processes a QiTx by spending the inputs and creating the outputs.
// Math is performed to verify the fee provided is sufficient to cover the gas cost.
// updateState is set to update the statedb in the case of the state processor, but not in the case of the txpool.
func ProcessQiTx(tx *types.Transaction, chain ChainContext, updateState bool, checkSig bool, currentHeader *types.WorkObject, statedb *state.StateDB, gp *types.GasPool, usedGas *uint64, signer types.Signer, location common.Location, chainId big.Int, etxRLimit, etxPLimit *int) (*big.Int, []*types.ExternalTx, error) {
	// Sanity checks
	if tx == nil || tx.Type() != types.QiTxType {
		return nil, nil, fmt.Errorf("tx %032x is not a QiTx", tx.Hash())
	}
	if tx.ChainId().Cmp(&chainId) != 0 {
		return nil, nil, fmt.Errorf("tx %032x has invalid chain ID", tx.Hash())
	}
	if currentHeader == nil || statedb == nil || gp == nil || usedGas == nil || signer == nil || etxRLimit == nil || etxPLimit == nil {
		return nil, nil, errors.New("one of the parameters is nil")
	}
	intrinsicGas := types.CalculateIntrinsicQiTxGas(tx)
	*usedGas += intrinsicGas
	if err := gp.SubGas(intrinsicGas); err != nil {
		return nil, nil, err
	}
	if *usedGas > currentHeader.GasLimit() {
		return nil, nil, fmt.Errorf("tx %032x uses too much gas, have used %d out of %d", tx.Hash(), *usedGas, currentHeader.GasLimit())
	}

	addresses := make(map[common.AddressBytes]struct{})

	totalQitIn := big.NewInt(0)
	pubKeys := make([]*btcec.PublicKey, 0)
	for _, txIn := range tx.TxIn() {
		utxo := statedb.GetUTXO(txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		if utxo == nil {
			return nil, nil, fmt.Errorf("tx %032x spends non-existent UTXO %032x:%d", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		}
		if utxo.Lock != nil && utxo.Lock.Cmp(currentHeader.Number(location.Context())) > 0 {
			return nil, nil, fmt.Errorf("tx %032x spends locked UTXO %032x:%d locked until %s", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, utxo.Lock.String())
		}
		// Verify the pubkey
		address := crypto.PubkeyBytesToAddress(txIn.PubKey, location)
		entryAddr := common.BytesToAddress(utxo.Address, location)
		if !address.Equal(entryAddr) {
			return nil, nil, fmt.Errorf("tx %032x spends UTXO %032x:%d with invalid pubkey, have %s want %s", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, address.String(), entryAddr.String())
		}
		if checkSig {
			pubKey, err := btcec.ParsePubKey(txIn.PubKey)
			if err != nil {
				return nil, nil, err
			}
			pubKeys = append(pubKeys, pubKey)
		}
		// Check for duplicate addresses. This also checks for duplicate inputs.
		if _, exists := addresses[common.AddressBytes(utxo.Address)]; exists {
			return nil, nil, errors.New("Duplicate address in QiTx inputs: " + common.AddressBytes(utxo.Address).String())
		}
		addresses[common.AddressBytes(utxo.Address)] = struct{}{}

		// Perform some spend processing logic
		denomination := utxo.Denomination
		if denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				denomination,
				types.MaxDenomination)
			return nil, nil, errors.New(str)
		}
		totalQitIn.Add(totalQitIn, types.Denominations[denomination])
		if updateState { // only update the state if requested (txpool check does not need to update the state)
			statedb.DeleteUTXO(txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		}
	}
	var ETXRCount int
	var ETXPCount int
	etxs := make([]*types.ExternalTx, 0)
	totalQitOut := big.NewInt(0)
	totalConvertQitOut := big.NewInt(0)
	conversion := false
	var convertAddress common.Address
	for txOutIdx, txOut := range tx.TxOut() {
		// It would be impossible for a tx to have this many outputs based on block gas limit, but cap it here anyways
		if txOutIdx > types.MaxOutputIndex {
			return nil, nil, fmt.Errorf("tx [%v] exceeds max output index of %d", tx.Hash().Hex(), types.MaxOutputIndex)
		}

		if txOut.Denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				txOut.Denomination,
				types.MaxDenomination)
			return nil, nil, errors.New(str)
		}
		totalQitOut.Add(totalQitOut, types.Denominations[txOut.Denomination])

		toAddr := common.BytesToAddress(txOut.Address, location)

		// Enforce no address reuse
		if _, exists := addresses[toAddr.Bytes20()]; exists {
			return nil, nil, errors.New("Duplicate address in QiTx outputs: " + toAddr.String())
		}
		addresses[toAddr.Bytes20()] = struct{}{}

		if toAddr.Location().Equal(location) && toAddr.IsInQuaiLedgerScope() { // Qi->Quai conversion
			conversion = true
			convertAddress = toAddr
			if txOut.Denomination < params.MinQiConversionDenomination {
				return nil, nil, fmt.Errorf("tx %v emits UTXO with value %d less than minimum denomination %d", tx.Hash().Hex(), txOut.Denomination, params.MinQiConversionDenomination)
			}
			totalConvertQitOut.Add(totalConvertQitOut, types.Denominations[txOut.Denomination]) // Add to total conversion output for aggregation
			delete(addresses, toAddr.Bytes20())
			continue
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
				return nil, nil, fmt.Errorf("tx [%v] emits too many cross-region ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXRCount, etxRLimit)
			}
			if ETXPCount > *etxPLimit {
				return nil, nil, fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXPCount, etxPLimit)
			}
			primeTerminus := currentHeader.PrimeTerminus()
			primeTerminusHeader := chain.GetHeaderByHash(primeTerminus)
			if primeTerminusHeader == nil {
				return nil, nil, fmt.Errorf("could not find prime terminus header %032x", primeTerminus)
			}
			if !toAddr.IsInQiLedgerScope() {
				return nil, nil, fmt.Errorf("tx [%v] emits UTXO with To address not in the Qi ledger scope", tx.Hash().Hex())
			}
			if !chain.CheckIfEtxIsEligible(primeTerminusHeader.EtxEligibleSlices(), *toAddr.Location()) {
				return nil, nil, fmt.Errorf("etx emitted by tx [%v] going to a slice that is not eligible to receive etx %v", tx.Hash().Hex(), *toAddr.Location())
			}

			// We should require some kind of extra fee here
			etxInner := types.ExternalTx{Value: big.NewInt(int64(txOut.Denomination)), To: &toAddr, Sender: common.ZeroAddress(location), OriginatingTxHash: tx.Hash(), ETXIndex: uint16(txOutIdx), Gas: params.TxGas}
			*usedGas += params.ETXGas
			if err := gp.SubGas(params.ETXGas); err != nil {
				return nil, nil, err
			}
			etxs = append(etxs, &etxInner)
		} else {
			// This output creates a normal UTXO
			utxo := types.NewUtxoEntry(&txOut)
			if updateState {
				if err := statedb.CreateUTXO(tx.Hash(), uint16(txOutIdx), utxo); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	// Ensure the transaction does not spend more than its inputs.
	if totalQitOut.Cmp(totalQitIn) == 1 {
		str := fmt.Sprintf("total value of all transaction inputs for "+
			"transaction %v is %v which is less than the amount "+
			"spent of %v", tx.Hash(), totalQitIn, totalQitOut)
		return nil, nil, errors.New(str)
	}

	// the fee to pay the basefee/miner is the difference between inputs and outputs
	txFeeInQit := new(big.Int).Sub(totalQitIn, totalQitOut)
	// Check tx against required base fee and gas
	requiredGas := intrinsicGas + (uint64(len(etxs)) * (params.TxGas + params.ETXGas)) // Each ETX costs extra gas that is paid in the origin
	if requiredGas < intrinsicGas {
		// Overflow
		return nil, nil, fmt.Errorf("tx %032x has too many ETXs to calculate required gas", tx.Hash())
	}
	minimumFeeInQuai := new(big.Int).Mul(big.NewInt(int64(requiredGas)), currentHeader.BaseFee())
	minimumFee := misc.QuaiToQi(currentHeader, minimumFeeInQuai)
	if txFeeInQit.Cmp(minimumFee) < 0 {
		return nil, nil, fmt.Errorf("tx %032x has insufficient fee for base fee, have %d want %d", tx.Hash(), txFeeInQit.Uint64(), minimumFee.Uint64())
	}
	// Miner gets remainder of fee after base fee, except in the convert case
	txFeeInQit.Sub(txFeeInQit, minimumFee)
	if conversion {
		// Since this transaction contains a conversion, the rest of the tx gas is given to conversion
		remainingTxFeeInQuai := misc.QiToQuai(currentHeader, txFeeInQit)
		// Fee is basefee * gas, so gas remaining is fee remaining / basefee
		remainingGas := new(big.Int).Div(remainingTxFeeInQuai, currentHeader.BaseFee())
		if remainingGas.Uint64() > (currentHeader.GasLimit() / params.MinimumEtxGasDivisor) {
			// Limit ETX gas to max ETX gas limit (the rest is burned)
			remainingGas = new(big.Int).SetUint64(currentHeader.GasLimit() / params.MinimumEtxGasDivisor)
		}
		ETXPCount++
		if ETXPCount > *etxPLimit {
			return nil, nil, fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. emitted: %d, limit: %d", tx.Hash().Hex(), ETXPCount, etxPLimit)
		}
		etxInner := types.ExternalTx{Value: totalConvertQitOut, To: &convertAddress, Sender: common.ZeroAddress(location), OriginatingTxHash: tx.Hash(), Gas: remainingGas.Uint64()} // Value is in Qits not Denomination
		*usedGas += params.ETXGas
		if err := gp.SubGas(params.ETXGas); err != nil {
			return nil, nil, err
		}
		etxs = append(etxs, &etxInner)
		txFeeInQit.Sub(txFeeInQit, txFeeInQit) // Fee goes entirely to gas to pay for conversion
	}

	// Ensure the transaction signature is valid
	if checkSig {
		var finalKey *btcec.PublicKey
		if len(tx.TxIn()) > 1 {
			aggKey, _, _, err := musig2.AggregateKeys(
				pubKeys, false,
			)
			if err != nil {
				return nil, nil, err
			}
			finalKey = aggKey.FinalKey
		} else {
			finalKey = pubKeys[0]
		}

		txDigestHash := signer.Hash(tx)
		if !tx.GetSchnorrSignature().Verify(txDigestHash[:], finalKey) {
			return nil, nil, errors.New("invalid signature for digest hash " + txDigestHash.String())
		}
	}

	*etxRLimit -= ETXRCount
	*etxPLimit -= ETXPCount
	return txFeeInQit, etxs, nil
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
	receipts, utxoEtxs, logs, statedb, usedGas, err := p.Process(block)
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
	err = p.validator.ValidateState(block, statedb, receipts, utxoEtxs, usedGas)
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
	utxoRoot, err := statedb.CommitUTXOs()
	if err != nil {
		return nil, err
	}
	etxRoot, err := statedb.CommitETXs()
	if err != nil {
		return nil, err
	}

	time7 := common.PrettyDuration(time.Since(start))
	var time8 common.PrettyDuration
	if err := p.stateCache.TrieDB().Commit(root, false, nil); err != nil {
		return nil, err
	}
	if err := p.utxoCache.TrieDB().Commit(utxoRoot, false, nil); err != nil {
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
	}).Debug("times during state processor apply")
	// Indicate that we have processed the state of the block
	rawdb.WriteProcessedState(batch, block.Hash())
	return logs, nil
}

// ApplyTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. It returns the receipt
// for the transaction, gas used and an error if the transaction failed,
// indicating the block was invalid.
func ApplyTransaction(config *params.ChainConfig, parent *types.WorkObject, bc ChainContext, author *common.Address, gp *types.GasPool, statedb *state.StateDB, header *types.WorkObject, tx *types.Transaction, usedGas *uint64, cfg vm.Config, etxRLimit, etxPLimit *int, logger *log.Logger) (*types.Receipt, error) {
	nodeCtx := config.Location.Context()
	msg, err := tx.AsMessage(types.MakeSigner(config, header.Number(nodeCtx)), header.BaseFee())
	if err != nil {
		return nil, err
	}
	if tx.Type() == types.ExternalTxType && tx.ETXSender().Location().Equal(*tx.To().Location()) { // Qi->Quai Conversion
		msg.SetLock(new(big.Int).Add(header.Number(nodeCtx), big.NewInt(params.ConversionLockPeriod)))
		primeTerminus := bc.GetHeaderByHash(header.PrimeTerminus())
		if primeTerminus == nil {
			return nil, fmt.Errorf("could not find prime terminus header %032x", header.PrimeTerminus())
		}
		// Convert Qi to Quai
		msg.SetValue(misc.QiToQuai(primeTerminus, tx.Value()))
		msg.SetData([]byte{}) // data is not used in conversion
	}
	// Create a new context to be used in the EVM environment
	blockContext, err := NewEVMBlockContext(header, bc, author)
	if err != nil {
		return nil, err
	}
	vmenv := vm.NewEVM(blockContext, vm.TxContext{}, statedb, config, cfg)
	if tx.Type() == types.ExternalTxType {
		prevZeroBal := prepareApplyETX(statedb, msg.Value(), config.Location)
		receipt, err := applyTransaction(msg, parent, config, bc, author, gp, statedb, header.Number(nodeCtx), header.Hash(), tx, usedGas, vmenv, etxRLimit, etxPLimit, logger)
		statedb.SetBalance(common.ZeroInternal(config.Location), prevZeroBal) // Reset the balance to what it previously was (currently a failed external transaction removes all the sent coins from the supply and any residual balance is gone as well)
		return receipt, err
	}
	return applyTransaction(msg, parent, config, bc, author, gp, statedb, header.Number(nodeCtx), header.Hash(), tx, usedGas, vmenv, etxRLimit, etxPLimit, logger)
}

// GetVMConfig returns the block chain VM config.
func (p *StateProcessor) GetVMConfig() *vm.Config {
	return &p.vmConfig
}

// State returns a new mutable state based on the current HEAD block.
func (p *StateProcessor) State() (*state.StateDB, error) {
	return p.StateAt(p.hc.CurrentHeader().EVMRoot(), p.hc.CurrentHeader().UTXORoot(), p.hc.CurrentHeader().EtxSetRoot())
}

// StateAt returns a new mutable state based on a particular point in time.
func (p *StateProcessor) StateAt(root, utxoRoot, etxRoot common.Hash) (*state.StateDB, error) {
	return state.New(root, utxoRoot, etxRoot, p.stateCache, p.utxoCache, p.etxCache, p.snaps, p.hc.NodeLocation(), p.logger)
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
		return receipts.(types.Receipts)
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
		return lookup.(*rawdb.LegacyTxLookupEntry)
	}
	tx, blockHash, blockNumber, txIndex := rawdb.ReadTransaction(p.hc.headerDb, hash)
	if tx == nil {
		return nil
	}
	lookup := &rawdb.LegacyTxLookupEntry{BlockHash: blockHash, BlockIndex: blockNumber, Index: txIndex}
	p.txLookupCache.Add(hash, lookup)
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
		utxoDatabase state.Database
		etxDatabase  state.Database
		report       = true
		nodeLocation = p.hc.NodeLocation()
		nodeCtx      = p.hc.NodeCtx()
		origin       = block.NumberU64(nodeCtx)
	)
	// Check the live database first if we have the state fully available, use that.
	if checkLive {
		statedb, err = p.StateAt(block.EVMRoot(), block.UTXORoot(), block.EtxSetRoot())
		if err == nil {
			return statedb, nil
		}
	}

	var newHeads []*types.WorkObject
	if base != nil {
		// The optional base statedb is given, mark the start point as parent block
		statedb, database, utxoDatabase, etxDatabase, report = base, base.Database(), base.UTXODatabase(), base.ETXDatabase(), false
		current = p.hc.GetHeaderOrCandidate(block.ParentHash(nodeCtx), block.NumberU64(nodeCtx)-1)
	} else {
		// Otherwise try to reexec blocks until we find a state or reach our limit
		current = types.CopyWorkObject(block)

		// Create an ephemeral trie.Database for isolating the live one. Otherwise
		// the internal junks created by tracing will be persisted into the disk.
		database = state.NewDatabaseWithConfig(p.hc.headerDb, &trie.Config{Cache: 16})
		// Create an ephemeral trie.Database for isolating the live one. Otherwise
		// the internal junks created by tracing will be persisted into the disk.
		utxoDatabase = state.NewDatabaseWithConfig(p.hc.headerDb, &trie.Config{Cache: 16})
		// Create an ephemeral trie.Database for isolating the live one. Otherwise
		// the internal junks created by tracing will be persisted into the disk.
		etxDatabase = state.NewDatabaseWithConfig(p.hc.headerDb, &trie.Config{Cache: 16})

		// If we didn't check the dirty database, do check the clean one, otherwise
		// we would rewind past a persisted block (specific corner case is chain
		// tracing from the genesis).
		if !checkLive {
			statedb, err = state.New(current.EVMRoot(), current.UTXORoot(), current.EtxSetRoot(), database, utxoDatabase, etxDatabase, nil, nodeLocation, p.logger)
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
			parent := p.hc.GetHeaderOrCandidate(current.ParentHash(nodeCtx), current.NumberU64(nodeCtx)-1)
			if parent == nil {
				return nil, fmt.Errorf("missing block %v %d", current.ParentHash(nodeCtx), current.NumberU64(nodeCtx)-1)
			}
			current = types.CopyWorkObject(parent)

			statedb, err = state.New(current.EVMRoot(), current.UTXORoot(), current.EtxSetRoot(), database, utxoDatabase, etxDatabase, nil, nodeLocation, p.logger)
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
		currentBlock := rawdb.ReadWorkObject(p.hc.bc.db, current.Hash(), types.BlockObject)
		if currentBlock == nil {
			return nil, errors.New("detached block found trying to regenerate state")
		}
		_, _, _, _, _, err := p.Process(currentBlock)
		if err != nil {
			return nil, fmt.Errorf("processing block %d failed: %v", current.NumberU64(nodeCtx), err)
		}
		// Finalize the state so any modifications are written to the trie
		root, err := statedb.Commit(true)
		if err != nil {
			return nil, fmt.Errorf("stateAtBlock commit failed, number %d root %v: %w",
				current.NumberU64(nodeCtx), current.EVMRoot().Hex(), err)
		}
		utxoRoot, err := statedb.CommitUTXOs()
		if err != nil {
			return nil, fmt.Errorf("stateAtBlock commit failed, number %d root %v: %w",
				current.NumberU64(nodeCtx), current.EVMRoot().Hex(), err)
		}
		etxRoot, err := statedb.CommitETXs()
		if err != nil {
			return nil, fmt.Errorf("stateAtBlock commit failed, number %d root %v: %w",
				current.NumberU64(nodeCtx), current.EVMRoot().Hex(), err)
		}
		statedb, err = state.New(root, utxoRoot, etxRoot, database, utxoDatabase, etxDatabase, nil, nodeLocation, p.logger)
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
		context, err := NewEVMBlockContext(block, p.hc, nil)
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
	if p.cacheConfig.UTXOTrieCleanJournal != "" {
		utxoTrieDB := p.utxoCache.TrieDB()
		utxoTrieDB.SaveCache(p.cacheConfig.UTXOTrieCleanJournal)
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

func (p *StateProcessor) GetUTXOsByAddress(address common.Address) ([]*types.UtxoEntry, error) {
	utxos := rawdb.ReadAddressUtxos(p.hc.bc.db, address)
	return utxos, nil
}
