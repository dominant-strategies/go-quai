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

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/common/prque"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quaiclient"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/sirupsen/logrus"
)

const (
	// chainHeadChanSize is the size of channel listening to ChainHeadEvent.
	chainHeadChanSize = 10

	// txSlotSize is used to calculate how many data slots a single transaction
	// takes up based on its size. The slots are used as DoS protection, ensuring
	// that validating a new transaction remains a constant operation (in reality
	// O(maxslots), where max slots are 4 currently).
	txSlotSize = 32 * 1024

	// txMaxSize is the maximum size a single transaction can have. This field has
	// non-trivial consequences: larger transactions are significantly harder and
	// more expensive to propagate; larger transactions also take more resources
	// to validate whether they fit into the pool or not.
	txMaxSize = 4 * txSlotSize // 128KB

	// c_reorgCounterThreshold determines the frequency of the timing prints
	// around important functions in txpool
	c_reorgCounterThreshold = 200

	// c_broadcastSetCacheSize is the maxminum number of latest broadcastSets that we keep in
	// the pool
	c_broadcastSetCacheSize = 10
)

var (
	// ErrAlreadyKnown is returned if the transactions is already contained
	// within the pool.
	ErrAlreadyKnown = errors.New("already known")

	// ErrInvalidSender is returned if the transaction contains an invalid signature.
	ErrInvalidSender = errors.New("invalid sender")

	// ErrUnderpriced is returned if a transaction's gas price is below the minimum
	// configured for the transaction pool.
	ErrUnderpriced = errors.New("transaction underpriced")

	// ErrTxPoolOverflow is returned if the transaction pool is full and can't accpet
	// another remote transaction.
	ErrTxPoolOverflow = errors.New("txpool is full")

	// ErrReplaceUnderpriced is returned if a transaction is attempted to be replaced
	// with a different one without the required price bump.
	ErrReplaceUnderpriced = errors.New("replacement transaction underpriced")

	// ErrGasLimit is returned if a transaction's requested gas limit exceeds the
	// maximum allowance of the current block.
	errGasLimit = errors.New("exceeds block gas limit")

	// ErrNegativeValue is a sanity error to ensure no one is able to specify a
	// transaction with a negative value.
	ErrNegativeValue = errors.New("negative value")

	// ErrOversizedData is returned if the input data of a transaction is greater
	// than some meaningful limit a user might use. This is not a consensus error
	// making the transaction invalid, rather a DOS protection.
	ErrOversizedData = errors.New("oversized data")
)

var (
	evictionInterval          = time.Minute      // Time interval to check for evictable transactions
	statsReportInterval       = 1 * time.Minute  // Time interval to report transaction pool stats
	qiExpirationCheckInterval = 10 * time.Minute // Time interval to check for expired Qi transactions
	qiExpirationCheckDivisor  = 5                // Check 1/nth of the pool for expired Qi transactions every interval
	txSharingPoolTimeout      = 2 * time.Second  // Time to exit the tx sharing call with client
)

var (
	//
	// TxPool processing metrics
	//
	txpoolMetrics = metrics_config.NewGaugeVec("TxpoolGauges", "Txpool gauges")
	// Pending pool metrics
	pendingDiscardMeter   = txpoolMetrics.WithLabelValues("pending:discard")
	pendingReplaceMeter   = txpoolMetrics.WithLabelValues("pending:replace")
	pendingRateLimitMeter = txpoolMetrics.WithLabelValues("pending:rateLimit") // Dropped due to rate limiting
	pendingNofundsMeter   = txpoolMetrics.WithLabelValues("pending:noFunds")   // Dropped due to out-of-funds

	// Metrics for the queued pool
	queuedDiscardMeter   = txpoolMetrics.WithLabelValues("queued:discard")
	queuedReplaceMeter   = txpoolMetrics.WithLabelValues("queued:replace")
	queuedRateLimitMeter = txpoolMetrics.WithLabelValues("queued:ratelimit") // Dropped due to rate limiting
	queuedNofundsMeter   = txpoolMetrics.WithLabelValues("queued:nofund")    // Dropped due to out-of-funds
	queuedEvictionMeter  = txpoolMetrics.WithLabelValues("queued:eviction")  // Dropped due to lifetime

	// General tx metrics
	knownTxMeter       = txpoolMetrics.WithLabelValues("known")       // Known transaction
	validTxMeter       = txpoolMetrics.WithLabelValues("valid")       // Valid transaction
	invalidTxMeter     = txpoolMetrics.WithLabelValues("invalid")     // Invalid transaction
	underpricedTxMeter = txpoolMetrics.WithLabelValues("underpriced") // Underpriced transaction
	overflowedTxMeter  = txpoolMetrics.WithLabelValues("overflowed")  // Overflowed transaction

	pendingTxGauge = txpoolMetrics.WithLabelValues("pending")
	queuedGauge    = txpoolMetrics.WithLabelValues("queued")
	localTxGauge   = txpoolMetrics.WithLabelValues("local")
	slotsGauge     = txpoolMetrics.WithLabelValues("slots")
	qiTxGauge      = txpoolMetrics.WithLabelValues("qi")

	reheapTimer = metrics_config.NewTimer("Reheap", "Reheap timer")
)

// TxStatus is the current status of a transaction as seen by the pool.
type TxStatus uint

const (
	TxStatusUnknown TxStatus = iota
	TxStatusQueued
	TxStatusPending
	TxStatusIncluded
)

// blockChain provides the state of blockchain and current gas limit to do
// some pre checks in tx pool and event subscribers.
type blockChain interface {
	CurrentBlock() *types.WorkObject
	GetBlock(hash common.Hash, number uint64) *types.WorkObject
	StateAt(root, etxRoot common.Hash, quaiStateSize *big.Int) (*state.StateDB, error)
	SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription
	IsGenesisHash(hash common.Hash) bool
	CheckIfEtxIsEligible(hash common.Hash, location common.Location) bool
	Engine(header *types.WorkObjectHeader) consensus.Engine
	GetHeaderOrCandidateByHash(common.Hash) *types.WorkObject
	NodeCtx() int
	GetHeaderByHash(common.Hash) *types.WorkObject
	GetBlockByHash(common.Hash) *types.WorkObject
	GetMaxTxInWorkShare() uint64
	CheckInCalcOrderCache(common.Hash) (*big.Int, int, bool)
	AddToCalcOrderCache(common.Hash, int, *big.Int)
	CalcBaseFee(*types.WorkObject) *big.Int
	CalcOrder(*types.WorkObject) (*big.Int, int, error)
}

// TxPoolConfig are the configuration parameters of the transaction pool.
type TxPoolConfig struct {
	Locals           []common.InternalAddress // Addresses that should be treated by default as local
	NoLocals         bool                     // Whether local transaction handling should be disabled
	SyncTxWithReturn bool
	Journal          string        // Journal of local transactions to survive node restarts
	Rejournal        time.Duration // Time interval to regenerate the local transaction journal

	PriceLimit uint64 // Minimum gas price to enforce for acceptance into the pool
	PriceBump  uint64 // Minimum price bump percentage to replace an already existing transaction (nonce)

	AccountSlots    uint64        // Number of executable transaction slots guaranteed per account
	GlobalSlots     uint64        // Maximum number of executable transaction slots for all accounts
	MaxSenders      uint64        // Maximum number of senders in the senders cache
	MaxFeesCached   uint64        // Maximum number of Qi fees to store
	SendersChBuffer uint64        // Senders cache channel buffer size
	AccountQueue    uint64        // Maximum number of non-executable transaction slots permitted per account
	GlobalQueue     uint64        // Maximum number of non-executable transaction slots for all accounts
	QiPoolSize      uint64        // Maximum number of Qi transactions to store
	QiTxLifetime    time.Duration // Maximum amount of time Qi transactions are queued
	Lifetime        time.Duration // Maximum amount of time non-executable transaction are queued
	ReorgFrequency  time.Duration // Frequency of reorgs outside of new head events

	SharingClientsEndpoints []string // List of end points of the nodes to share the incoming local transactions with
}

// DefaultTxPoolConfig contains the default configurations for the transaction
// pool.
var DefaultTxPoolConfig = TxPoolConfig{
	Journal:   "transactions.rlp",
	Rejournal: time.Hour,

	PriceLimit: 0,
	PriceBump:  5,

	AccountSlots:    200,
	GlobalSlots:     19000 + 1024, // urgent + floating queue capacity with 4:1 ratio
	MaxSenders:      10000,        // 5 MB - at least 10 blocks worth of transactions in case of reorg or high production rate
	MaxFeesCached:   50000,
	SendersChBuffer: 1024, // at 500 TPS in zone, 2s buffer
	AccountQueue:    200,
	GlobalQueue:     20048,
	QiPoolSize:      10024,
	QiTxLifetime:    30 * time.Minute,
	Lifetime:        5 * time.Minute,
	ReorgFrequency:  1 * time.Second,
}

// sanitize checks the provided user configurations and changes anything that's
// unreasonable or unworkable.
func (config *TxPoolConfig) sanitize(logger *log.Logger) TxPoolConfig {
	conf := *config
	if conf.Rejournal < time.Second {
		logger.WithFields(log.Fields{
			"provided": conf.Rejournal,
			"updated":  time.Second,
		}).Warn("Sanitizing invalid txpool journal time")
		conf.Rejournal = time.Second
	}
	if conf.PriceLimit < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.PriceLimit,
			"updated":  DefaultTxPoolConfig.PriceLimit,
		}).Warn("Sanitizing invalid txpool price limit")
		conf.PriceLimit = DefaultTxPoolConfig.PriceLimit
	}
	if conf.PriceBump < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.PriceBump,
			"updated":  DefaultTxPoolConfig.PriceBump,
		}).Warn("Sanitizing invalid txpool price bump")
		conf.PriceBump = DefaultTxPoolConfig.PriceBump
	}
	if conf.AccountSlots < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.AccountSlots,
			"updated":  DefaultTxPoolConfig.AccountSlots,
		}).Warn("Sanitizing invalid txpool account slots")
		conf.AccountSlots = DefaultTxPoolConfig.AccountSlots
	}
	if conf.GlobalSlots < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.GlobalSlots,
			"updated":  DefaultTxPoolConfig.GlobalSlots,
		}).Warn("Sanitizing invalid txpool global slots")
		conf.GlobalSlots = DefaultTxPoolConfig.GlobalSlots
	}
	if conf.AccountQueue < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.AccountQueue,
			"updated":  DefaultTxPoolConfig.AccountQueue,
		}).Warn("Sanitizing invalid txpool account queue")
		conf.AccountQueue = DefaultTxPoolConfig.AccountQueue
	}
	if conf.GlobalQueue < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.GlobalQueue,
			"updated":  DefaultTxPoolConfig.GlobalQueue,
		}).Warn("Sanitizing invalid txpool global queue")
		conf.GlobalQueue = DefaultTxPoolConfig.GlobalQueue
	}
	if conf.QiPoolSize < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.QiPoolSize,
			"updated":  DefaultTxPoolConfig.QiPoolSize,
		}).Warn("Sanitizing invalid txpool Qi pool size")
		conf.QiPoolSize = DefaultTxPoolConfig.QiPoolSize
	}
	if conf.QiTxLifetime < time.Second {
		logger.WithFields(log.Fields{
			"provided": conf.QiTxLifetime,
			"updated":  DefaultTxPoolConfig.QiTxLifetime,
		}).Warn("Sanitizing invalid txpool Qi transaction lifetime")
		conf.QiTxLifetime = DefaultTxPoolConfig.QiTxLifetime
	}
	if conf.Lifetime < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.Lifetime,
			"updated":  DefaultTxPoolConfig.Lifetime,
		}).Warn("Sanitizing invalid txpool lifetime")
		conf.Lifetime = DefaultTxPoolConfig.Lifetime
	}
	if conf.ReorgFrequency < 1 {
		logger.WithFields(log.Fields{
			"provided": conf.ReorgFrequency,
			"updated":  DefaultTxPoolConfig.ReorgFrequency,
		}).Warn("Sanitizing invalid txpool reorg frequency")
		conf.ReorgFrequency = DefaultTxPoolConfig.ReorgFrequency
	}
	return conf
}

// TxPool contains all currently known transactions. Transactions
// enter the pool when they are received from the network or submitted
// locally. They exit the pool when they are included in the blockchain.
//
// The pool separates processable transactions (which can be applied to the
// current state) and future transactions. Transactions move between those
// two states over time as they are received and processed.
type TxPool struct {
	config          TxPoolConfig
	chainconfig     *params.ChainConfig
	chain           blockChain
	gasPrice        *big.Int
	scope           event.SubscriptionScope
	signer          types.Signer
	mu              sync.RWMutex
	sharingClientMu sync.RWMutex

	currentState       *state.StateDB // Current state in the blockchain head
	qiGasScalingFactor float64
	db                 ethdb.Reader
	pendingNonces      *txNoncer // Pending state tracking virtual nonces
	currentMaxGas      uint64    // Current gas limit for transaction caps

	locals         *accountSet                                     // Set of local transaction to exempt from eviction rules
	journal        *txJournal                                      // Journal of local transaction to back up to disk
	qiPool         *lru.Cache[common.Hash, *types.TxWithMinerFee]  // Qi pool to store Qi transactions
	qiTxFees       *lru.Cache[[16]byte, *big.Int]                  // Recent Qi transaction fees (hash is truncated to 16 bytes to save space)
	pending        map[common.InternalAddress]*txList              // All currently processable transactions
	queue          map[common.InternalAddress]*txList              // Queued but non-processable transactions
	beats          map[common.InternalAddress]time.Time            // Last heartbeat from each known account
	all            *txLookup                                       // All transactions to allow lookups
	priced         *txPricedList                                   // All transactions sorted by price
	senders        *lru.Cache[common.Hash, common.InternalAddress] // Tx hash to sender lookup cache (async populated)
	sendersCh      chan newSender                                  // Channel for async senders cache goroutine
	feesCh         chan newFee                                     // Channel for async Qi fees cache goroutine
	invalidQiTxsCh chan []*common.Hash                             // Channel for async invalid Qi transactions
	SendersMu      sync.RWMutex                                    // Mutex for priority access of senders cache
	localTxsCount  int                                             // count of txs in last 1 min. Purely for logging purpose
	remoteTxsCount int                                             // count of txs in last 1 min. Purely for logging purpose

	broadcastSetCache *lru.Cache[common.Hash, types.Transactions]
	broadcastSetMu    sync.RWMutex
	broadcastSet      types.Transactions

	reOrgCounter int // keeps track of the number of times the runReorg is called, it is reset every c_reorgCounterThreshold times

	chainHeadCh     chan ChainHeadEvent
	chainHeadSub    event.Subscription
	reqResetCh      chan *txpoolResetRequest
	reqPromoteCh    chan *accountSet
	queueTxEventCh  chan *types.Transaction
	reorgDoneCh     chan chan struct{}
	reorgShutdownCh chan struct{}  // requests shutdown of scheduleReorgLoop
	wg              sync.WaitGroup // tracks loop, scheduleReorgLoop

	logger *log.Logger

	poolSharingClients []*quaiclient.Client
	poolSharingTxCh    chan *types.Transaction
}

type txpoolResetRequest struct {
	oldHead, newHead *types.WorkObject
}

type newSender struct {
	hash   common.Hash
	sender common.InternalAddress
}
type newFee struct {
	hash [16]byte
	fee  *big.Int
}

// NewTxPool creates a new transaction pool to gather, sort and filter inbound
// transactions from the network.
func NewTxPool(config TxPoolConfig, chainconfig *params.ChainConfig, chain blockChain, logger *log.Logger, db ethdb.Reader) *TxPool {
	// Pending pool metrics
	pendingDiscardMeter.Set(0)
	pendingReplaceMeter.Set(0)
	pendingRateLimitMeter.Set(0)
	pendingNofundsMeter.Set(0)

	// Metrics for the queued pool
	queuedDiscardMeter.Set(0)
	queuedReplaceMeter.Set(0)
	queuedRateLimitMeter.Set(0)
	queuedNofundsMeter.Set(0)
	queuedEvictionMeter.Set(0)

	// General tx metrics
	knownTxMeter.Set(0)
	validTxMeter.Set(0)
	invalidTxMeter.Set(0)
	underpricedTxMeter.Set(0)
	overflowedTxMeter.Set(0)

	pendingTxGauge.Set(0)
	queuedGauge.Set(0)
	localTxGauge.Set(0)

	// Sanitize the input to ensure no vulnerable gas prices are set
	config = (&config).sanitize(logger)

	// Create the transaction pool with its initial settings
	pool := &TxPool{
		config:             config,
		chainconfig:        chainconfig,
		chain:              chain,
		signer:             types.LatestSigner(chainconfig),
		pending:            make(map[common.InternalAddress]*txList),
		queue:              make(map[common.InternalAddress]*txList),
		beats:              make(map[common.InternalAddress]time.Time),
		sendersCh:          make(chan newSender, config.SendersChBuffer),
		feesCh:             make(chan newFee, config.SendersChBuffer),
		invalidQiTxsCh:     make(chan []*common.Hash, config.SendersChBuffer),
		all:                newTxLookup(),
		chainHeadCh:        make(chan ChainHeadEvent, chainHeadChanSize),
		reqResetCh:         make(chan *txpoolResetRequest, chainHeadChanSize),
		reqPromoteCh:       make(chan *accountSet, chainHeadChanSize),
		queueTxEventCh:     make(chan *types.Transaction, chainHeadChanSize),
		broadcastSet:       make(types.Transactions, 0),
		reorgDoneCh:        make(chan chan struct{}, chainHeadChanSize),
		reorgShutdownCh:    make(chan struct{}),
		gasPrice:           new(big.Int).SetUint64(config.PriceLimit),
		localTxsCount:      0,
		remoteTxsCount:     0,
		reOrgCounter:       0,
		logger:             logger,
		db:                 db,
		poolSharingClients: make([]*quaiclient.Client, len(config.SharingClientsEndpoints)),
		poolSharingTxCh:    make(chan *types.Transaction, 100),
	}

	qiPool, _ := lru.New[common.Hash, *types.TxWithMinerFee](int(config.QiPoolSize))
	pool.qiPool = qiPool

	senders, _ := lru.New[common.Hash, common.InternalAddress](int(config.MaxSenders))
	pool.senders = senders

	qiTxFees, _ := lru.New[[16]byte, *big.Int](int(config.MaxFeesCached))
	pool.qiTxFees = qiTxFees

	broadcastSetCache, _ := lru.New[common.Hash, types.Transactions](c_broadcastSetCacheSize)
	pool.broadcastSetCache = broadcastSetCache

	pool.locals = newAccountSet(pool.signer)
	for _, addr := range config.Locals {
		logger.WithField("address", addr).Debug("Setting new local account")
		pool.locals.add(addr)
	}
	pool.priced = newTxPricedList(pool.all)
	pool.mu.Lock()
	pool.reset(nil, chain.CurrentBlock())
	pool.mu.Unlock()

	// Start the reorg loop early so it can handle requests generated during journal loading.
	pool.wg.Add(1)
	go pool.scheduleReorgLoop()

	pool.wg.Add(1)
	go pool.txListenerLoop()

	// If local transactions and journaling is enabled, load from disk
	if !config.NoLocals && config.Journal != "" {
		pool.journal = newTxJournal(config.Journal, logger)

		if err := pool.journal.load(pool.AddLocals); err != nil {
			logger.WithField("err", err).Warn("Failed to load transaction journal")
		}
		if err := pool.journal.rotate(pool.local()); err != nil {
			logger.WithField("err", err).Warn("Failed to rotate transaction journal")
		}
	}

	logger.WithFields(log.Fields{"sharing endpoints": config.SharingClientsEndpoints, "sync tx with return": config.SyncTxWithReturn}).Info("Tx pool config for sharing tx")

	// connect to the pool sharing clients
	for i := range config.SharingClientsEndpoints {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					pool.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Error("Go-Quai Panicked")
				}
			}()
			pool.createSharingClient(i)
		}()
	}

	// Subscribe events from blockchain and start the main event loop.
	pool.chainHeadSub = pool.chain.SubscribeChainHeadEvent(pool.chainHeadCh)
	pool.wg.Add(1)
	go pool.loop()
	go pool.sendersGoroutine()
	go pool.poolLimiterGoroutine()
	go pool.feesGoroutine()
	go pool.invalidQiTxGoroutine()
	go pool.qiTxExpirationGoroutine()
	return pool
}

// loop is the transaction pool's main event loop, waiting for and reacting to
// outside blockchain events as well as for various reporting and transaction
// eviction events.
func (pool *TxPool) loop() {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	defer pool.wg.Done()

	var (
		// Start the stats reporting and transaction eviction tickers
		report  = time.NewTicker(statsReportInterval)
		evict   = time.NewTicker(evictionInterval)
		journal = time.NewTicker(pool.config.Rejournal)
		// Track the previous head headers for transaction reorgs
		head = pool.chain.CurrentBlock()
	)
	defer report.Stop()
	defer evict.Stop()
	defer journal.Stop()

	for {
		select {
		// Handle ChainHeadEvent
		case ev := <-pool.chainHeadCh:
			if ev.Block != nil {
				pool.requestReset(head, ev.Block)
				head = ev.Block
			}

		// System shutdown.
		case <-pool.chainHeadSub.Err():
			close(pool.reorgShutdownCh)
			return

		// Handle stats reporting ticks
		case <-report.C:
			pool.mu.RLock()
			pending, queued, qi := pool.stats()
			stales := pool.priced.stales
			pool.logger.WithFields(log.Fields{
				"Local Txs":  pool.localTxsCount,
				"Remote Txs": pool.remoteTxsCount,
			}).Info("Added Transactions in last Min", "Local Txs", pool.localTxsCount, "Remote Txs", pool.remoteTxsCount)
			pool.localTxsCount = 0
			pool.remoteTxsCount = 0
			pool.mu.RUnlock()

			pool.logger.WithFields(log.Fields{
				"pending": pending,
				"queued":  queued,
				"stales":  stales,
				"qi":      qi,
			}).Info("Transaction pool status report")
		// Handle inactive account transaction eviction
		case <-evict.C:
			pool.mu.Lock()
			for addr := range pool.queue {
				// Any transactions old enough should be removed
				if time.Since(pool.beats[addr]) > pool.config.Lifetime {
					list := pool.queue[addr].Flatten()
					for _, tx := range list {
						pool.removeTx(tx.Hash(), true)
					}
					queuedEvictionMeter.Add(float64(len(list)))
				}
			}
			for _, txList := range pool.pending {
				txs := txList.Flatten()
				if len(txs) == 0 {
					continue
				}
				if time.Since(txs[0].Time()) > pool.config.Lifetime {
					for _, tx := range txs {
						pool.removeTx(tx.Hash(), true)
					}
					pendingDiscardMeter.Add(float64(len(txs)))
				}
			}
			pool.mu.Unlock()

		// Handle local transaction journal rotation
		case <-journal.C:
			if pool.journal != nil {
				pool.mu.Lock()
				if err := pool.journal.rotate(pool.local()); err != nil {
					pool.logger.WithField("err", err).Warn("Failed to rotate local tx journal")
				}
				pool.mu.Unlock()
			}
		}
	}
}

// Stop terminates the transaction pool.
func (pool *TxPool) Stop() {
	// Unsubscribe all subscriptions registered from txpool
	pool.scope.Close()

	// Unsubscribe subscriptions registered from blockchain
	pool.chainHeadSub.Unsubscribe()
	pool.wg.Wait()

	if pool.journal != nil {
		pool.journal.close()
	}
	for _, client := range pool.poolSharingClients {
		if client != nil {
			client.Close()
		}
	}
	pool.logger.Info("Transaction pool stopped")
}

// GasPrice returns the current gas price enforced by the transaction pool.
func (pool *TxPool) GasPrice() *big.Int {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	return new(big.Int).Set(pool.gasPrice)
}

// SetGasPrice updates the minimum price required by the transaction pool for a
// new transaction, and drops all transactions below this threshold.
func (pool *TxPool) SetGasPrice(price *big.Int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	old := pool.gasPrice
	pool.gasPrice = price
	// if the min miner fee increased, remove transactions below the new threshold
	if price.Cmp(old) > 0 {
		// pool.priced is sorted by GasFeeCap, so we have to iterate through pool.all instead
		drop := pool.all.RemotesBelowTip(price)
		for _, tx := range drop {
			pool.removeTx(tx.Hash(), false)
		}
		pool.priced.Removed(len(drop))
	}

	pool.logger.WithField("price", price).Info("Transaction pool price threshold updated")
}

// Nonce returns the next nonce of an account, with all transactions executable
// by the pool already applied on top.
func (pool *TxPool) Nonce(addr common.InternalAddress) uint64 {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	return pool.pendingNonces.get(addr)
}

// Stats retrieves the current pool stats, namely the number of pending and the
// number of queued (non-executable) transactions.
func (pool *TxPool) Stats() (int, int, int) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	return pool.stats()
}

// stats retrieves the current pool stats, namely the number of pending and the
// number of queued (non-executable) transactions.
func (pool *TxPool) stats() (int, int, int) {
	pending := 0
	for _, list := range pool.pending {
		pending += list.Len()
	}
	queued := 0
	for _, list := range pool.queue {
		queued += list.Len()
	}
	return pending, queued, pool.qiPool.Len()
}

// Content retrieves the data content of the transaction pool, returning all the
// pending as well as queued transactions, grouped by account and sorted by nonce.
func (pool *TxPool) Content() (map[common.InternalAddress]types.Transactions, map[common.InternalAddress]types.Transactions) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	pending := make(map[common.InternalAddress]types.Transactions)
	for addr, list := range pool.pending {
		pending[addr] = list.Flatten()
	}
	queued := make(map[common.InternalAddress]types.Transactions)
	for addr, list := range pool.queue {
		queued[addr] = list.Flatten()
	}
	return pending, queued
}

// ContentFrom retrieves the data content of the transaction pool, returning the
// pending as well as queued transactions of this address, grouped by nonce.
func (pool *TxPool) ContentFrom(addr common.InternalAddress) (types.Transactions, types.Transactions) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	var pending types.Transactions
	if list, ok := pool.pending[addr]; ok {
		pending = list.Flatten()
	}
	var queued types.Transactions
	if list, ok := pool.queue[addr]; ok {
		queued = list.Flatten()
	}
	return pending, queued
}

func (pool *TxPool) QiPoolPending() []*types.TxWithMinerFee {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	return pool.qiPool.Values()
}

func (pool *TxPool) GetTxsFromBroadcastSet(hash common.Hash) (types.Transactions, error) {
	txs, ok := pool.broadcastSetCache.Get(hash)
	if !ok {
		return types.Transactions{}, fmt.Errorf("cannot find the txs in the broadcast set for txhash [%s]", hash)
	}
	return txs, nil
}

// Pending retrieves all currently processable transactions, grouped by origin
// account and sorted by nonce. The returned transaction set is a copy and can be
// freely modified by calling code.
//
// The enforceTips parameter can be used to do an extra filtering on the pending
// transactions and only return those whose **effective** tip is large enough in
// the next pending execution environment.
func (pool *TxPool) TxPoolPending() (map[common.AddressBytes]types.Transactions, error) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	pending := make(map[common.AddressBytes]types.Transactions)
	for addr, list := range pool.pending {
		txs := list.Flatten()

		for i, tx := range txs {
			// make sure that the tx has atleast min base fee as the gas
			// price
			currentBlock := pool.chain.CurrentBlock()
			minBaseFee := currentBlock.BaseFee()
			if minBaseFee.Cmp(tx.GasPrice()) > 0 {
				pool.logger.WithFields(log.Fields{
					"tx":         tx.Hash().String(),
					"gasPrice":   tx.GasPrice().String(),
					"minBaseFee": minBaseFee.String(),
				}).Debug("TX has incorrect or low gas price")
				txs = txs[:i]
				break
			}
		}
		if len(txs) > 0 {
			pending[addr.Bytes20()] = txs
		}
	}
	return pending, nil
}

// Locals retrieves the accounts currently considered local by the pool.
func (pool *TxPool) Locals() []common.InternalAddress {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	return pool.locals.flatten()
}

// local retrieves all currently known local transactions, grouped by origin
// account and sorted by nonce. The returned transaction set is a copy and can be
// freely modified by calling code.
func (pool *TxPool) local() map[common.InternalAddress]types.Transactions {
	txs := make(map[common.InternalAddress]types.Transactions)
	for addr := range pool.locals.accounts {
		if pending := pool.pending[addr]; pending != nil {
			txs[addr] = append(txs[addr], pending.Flatten()...)
		}
		if queued := pool.queue[addr]; queued != nil {
			txs[addr] = append(txs[addr], queued.Flatten()...)
		}
	}
	return txs
}

// validateTx checks whether a transaction is valid according to the consensus
// rules and adheres to some heuristic limits of the local node (price and size).
func (pool *TxPool) validateTx(tx *types.Transaction) error {
	// Reject transactions over defined size to prevent DOS attacks
	if uint64(tx.Size()) > txMaxSize {
		return ErrOversizedData
	}
	// Transactions can't be negative. This may never happen using RLP decoded
	// transactions but may occur if you create a transaction using the RPC.
	if tx.Value().Sign() < 0 {
		return ErrNegativeValue
	}
	// Ensure the transaction doesn't exceed the current block limit gas.
	if pool.currentMaxGas < tx.Gas() {
		return ErrGasLimit(tx.Gas(), pool.currentMaxGas)
	}
	// Sanity check for extremely large numbers
	if tx.GasPrice().BitLen() > 256 {
		return ErrFeeCapVeryHigh
	}
	var internal common.InternalAddress
	addToCache := true
	if sender := tx.From(pool.chainconfig.Location); sender != nil { // Check tx cache first
		var err error
		internal, err = sender.InternalAndQuaiAddress()
		if err != nil {
			return err
		}
	} else if sender, found := pool.PeekSender(tx.Hash()); found {
		internal = sender
		addToCache = false
	} else {
		// Make sure the transaction is signed properly.
		from, err := types.Sender(pool.signer, tx)
		if err != nil {
			pool.logger.WithField("err", err).Error("Error calculating the Sender in validateTx")
			return ErrInvalidSender
		}
		internal, err = from.InternalAndQuaiAddress()
		if err != nil {
			return err
		}
	}
	currentBlock := pool.chain.CurrentBlock()
	minBaseFee := currentBlock.BaseFee()
	if minBaseFee.Cmp(tx.GasPrice()) > 0 {
		pool.logger.WithFields(log.Fields{
			"tx":         tx.Hash().String(),
			"gasPrice":   tx.GasPrice().String(),
			"minBaseFee": minBaseFee.String(),
		}).Debug("TX has incorrect or low gas price")
		return fmt.Errorf("tx has incorrect or low gas price, have %s, want %s", tx.GasPrice().String(), minBaseFee.String())
	}

	// Drop non-local transactions under our own minimal accepted gas price or tip
	if tx.CompareFee(pool.gasPrice) < 0 {
		return ErrUnderpriced
	}
	// Ensure the transaction adheres to nonce ordering
	if pool.currentState.GetNonce(internal) > tx.Nonce() {
		return ErrNonceTooLow
	}
	// Transactor should have enough funds to cover the costs
	// cost == V + GP * GL
	if pool.currentState.GetBalance(internal).Cmp(tx.Cost()) < 0 {
		return ErrInsufficientFunds
	}
	// Ensure the transaction has more gas than the basic tx fee.
	intrGas, err := IntrinsicGas(tx.Data(), tx.AccessList(), tx.To() == nil)
	if err != nil {
		return err
	}
	if tx.Gas() < intrGas {
		pool.logger.WithFields(log.Fields{
			"gasSupplied": tx.Gas(),
			"gasNeeded":   intrGas,
			"tx":          tx,
		}).Warn("tx has insufficient gas")
		return ErrIntrinsicGas
	}
	if addToCache {
		select {
		case pool.sendersCh <- newSender{tx.Hash(), internal}: // Non-blocking
		default:
			pool.logger.Error("sendersCh is full, skipping until there is room")
		}
	}

	return nil
}

// add validates a transaction and inserts it into the non-executable queue for later
// pending promotion and execution. If the transaction is a replacement for an already
// pending or queued one, it overwrites the previous transaction if its price is higher.
//
// If a newly added transaction is marked as local, its sending account will be
// be added to the allowlist, preventing any associated transaction from being dropped
// out of the pool due to pricing constraints.
func (pool *TxPool) add(tx *types.Transaction, local bool) (replaced bool, err error) {
	// If the transaction is already known, discard it
	hash := tx.Hash()
	if pool.all.Get(hash) != nil {
		pool.logger.WithField("hash", hash).Trace("Discarding already known transaction")
		knownTxMeter.Add(1)
		return false, ErrAlreadyKnown
	}
	// Make the local flag. If it's from local source or it's from the network but
	// the sender is marked as local previously, treat it as the local transaction.
	isLocal := local || pool.locals.containsTx(tx)

	// If the transaction fails basic validation, discard it
	if err := pool.validateTx(tx); err != nil {
		pool.logger.WithFields(log.Fields{
			"hash": hash,
			"err":  err,
		}).Trace("Discarding invalid transaction")
		invalidTxMeter.Add(1)
		return false, err
	}
	// If the transaction pool is full, discard underpriced transactions
	if uint64(pool.all.Slots()+numSlots(tx)) > pool.config.GlobalSlots+pool.config.GlobalQueue {
		// If the new transaction is underpriced, don't accept it
		if pool.priced.Underpriced(tx) {
			pool.logger.WithFields(log.Fields{
				"hash":     hash,
				"gasPrice": tx.GasPrice(),
			}).Trace("Discarding underpriced transaction")
			underpricedTxMeter.Add(1)
			return false, ErrUnderpriced
		}
		// New transaction is better than our worse ones, make room for it.
		// If it's a local transaction, forcibly discard all available transactions.
		// Otherwise if we can't make enough room for new one, abort the operation.
		drop, success := pool.priced.Discard(pool.all.Slots()-int(pool.config.GlobalSlots+pool.config.GlobalQueue)+numSlots(tx), false)

		// Special case, we still can't make the room for the new remote one.
		if !success {
			pool.logger.WithField("hash", hash).Trace("Discarding overflown transaction")
			overflowedTxMeter.Add(1)
			return false, ErrTxPoolOverflow
		}
		// Kick out the underpriced remote transactions.
		for _, tx := range drop {
			pool.logger.WithFields(log.Fields{
				"hash":     tx.Hash(),
				"gasPrice": tx.GasPrice(),
			}).Trace("Discarding freshly underpriced transaction")
			pendingDiscardMeter.Add(1)
			pool.removeTx(tx.Hash(), false)
		}
	}
	// Try to replace an existing transaction in the pending pool
	from, err := types.Sender(pool.signer, tx) // already validated
	if err != nil {
		return false, err
	}
	internal, err := from.InternalAndQuaiAddress()
	if err != nil {
		return false, err
	}
	if list := pool.pending[internal]; list != nil && list.Overlaps(tx) {
		// Nonce already pending, check if required price bump is met
		inserted, old := list.Add(tx, pool.config.PriceBump)
		if !inserted {
			pendingDiscardMeter.Add(1)
			return false, ErrReplaceUnderpriced
		}
		// New transaction is better, replace old one
		if old != nil {
			pool.all.Remove(old.Hash(), pool.logger)
			pool.priced.Removed(1)
			pendingReplaceMeter.Add(1)
		}
		pool.all.Add(tx, isLocal)
		pool.priced.Put(tx, isLocal)
		pool.journalTx(internal, tx)
		pool.queueTxEvent(tx)
		pool.logger.WithFields(log.Fields{
			"hash": hash,
			"from": from,
			"to":   tx.To(),
		}).Trace("Pooled new executable transaction")

		// Successful promotion, bump the heartbeat
		pool.beats[internal] = time.Now()
		return old != nil, nil
	}
	// New transaction isn't replacing a pending one, push into queue
	replaced, err = pool.enqueueTx(hash, tx, isLocal, true)
	if err != nil {
		return false, err
	}
	// Mark local addresses and journal local transactions
	if local && !pool.locals.contains(internal) {
		pool.logger.WithField("address", from).Debug("Setting new local account")
		pool.locals.add(internal)
		pool.priced.Removed(pool.all.RemoteToLocals(pool.locals)) // Migrate the remotes if it's marked as local first time.
	}
	if isLocal {
		localTxGauge.Add(1)
	}
	pool.journalTx(internal, tx)
	pool.queueTxEvent(tx)
	pool.logger.WithFields(log.Fields{
		"hash": hash,
		"from": from,
		"to":   tx.To(),
	}).Trace("Pooled new future transaction")
	return replaced, nil
}

// enqueueTx inserts a new transaction into the non-executable transaction queue.
//
// Note, this method assumes the pool lock is held!
func (pool *TxPool) enqueueTx(hash common.Hash, tx *types.Transaction, local bool, addAll bool) (bool, error) {
	// Try to insert the transaction into the future queue
	from, err := types.Sender(pool.signer, tx) // already validated
	if err != nil {
		return false, err
	}
	internal, err := from.InternalAndQuaiAddress()
	if err != nil {
		return false, err
	}
	if pool.queue[internal] == nil {
		pool.queue[internal] = newTxList(false)
	}
	inserted, old := pool.queue[internal].Add(tx, pool.config.PriceBump)
	if !inserted {
		// An older transaction was better, discard this
		queuedDiscardMeter.Add(1)
		return false, ErrReplaceUnderpriced
	}
	// Discard any previous transaction and mark this
	if old != nil {
		pool.all.Remove(old.Hash(), pool.logger)
		pool.priced.Removed(1)
		queuedReplaceMeter.Add(1)
	} else {
		// Nothing was replaced, bump the queued counter
		queuedGauge.Add(1)
	}
	// If the transaction isn't in lookup set but it's expected to be there,
	// show the error pool.logger.
	if pool.all.Get(hash) == nil && !addAll {
		pool.logger.WithField("hash", hash).Error("Missing transaction in lookup set, please report the issue")
	}
	if addAll {
		pool.all.Add(tx, local)
		pool.priced.Put(tx, local)
	}
	// If we never record the heartbeat, do it right now.
	if _, exist := pool.beats[internal]; !exist {
		pool.beats[internal] = time.Now()
	}
	return old != nil, nil
}

// journalTx adds the specified transaction to the local disk journal if it is
// deemed to have been sent from a local account.
func (pool *TxPool) journalTx(from common.InternalAddress, tx *types.Transaction) {
	// Only journal if it's enabled and the transaction is local
	if pool.journal == nil || !pool.locals.contains(from) {
		return
	}
	if err := pool.journal.insert(tx); err != nil {
		pool.logger.WithField("err", err).Warn("Failed to journal local transaction")
	}
}

// promoteTx adds a transaction to the pending (processable) list of transactions
// and returns whether it was inserted or an older was better.
//
// Note, this method assumes the pool lock is held!
func (pool *TxPool) promoteTx(addr common.InternalAddress, hash common.Hash, tx *types.Transaction) bool {
	// Try to insert the transaction into the pending queue
	if pool.pending[addr] == nil {
		pool.pending[addr] = newTxList(true)
	}
	list := pool.pending[addr]

	inserted, old := list.Add(tx, pool.config.PriceBump)
	if !inserted {
		// An older transaction was better, discard this
		pool.all.Remove(hash, pool.logger)
		pool.priced.Removed(1)
		pendingDiscardMeter.Add(1)
		return false
	}
	// Otherwise discard any previous transaction and mark this
	if old != nil {
		pool.all.Remove(old.Hash(), pool.logger)
		pool.priced.Removed(1)
		pendingReplaceMeter.Add(1)
	} else {
		// Nothing was replaced, bump the pending counter
		pendingTxGauge.Add(1)
	}
	// Set the potentially new pending nonce and notify any subsystems of the new tx
	pool.pendingNonces.set(addr, tx.Nonce()+1)

	// Successful promotion, bump the heartbeat
	pool.beats[addr] = time.Now()
	if list.Len()%100 == 0 {
		pool.logger.WithFields(log.Fields{
			"addr": addr,
			"len":  list.Len(),
		}).Info("Another 100 txs added to list")
	}
	return true
}

// AddLocals enqueues a batch of transactions into the pool if they are valid, marking the
// senders as a local ones, ensuring they go around the local pricing constraints.
//
// This method is used to add transactions from the RPC API and performs synchronous pool
// reorganization and event propagation.
func (pool *TxPool) AddLocals(txs []*types.Transaction) []error {
	if len(txs) == 0 {
		return []error{}
	}

	return pool.addTxs(txs, !pool.config.NoLocals, true)
}

// AddLocal enqueues a single local transaction into the pool if it is valid. This is
// a convenience wrapper aroundd AddLocals.
func (pool *TxPool) AddLocal(tx *types.Transaction) error {
	pool.localTxsCount += 1
	tx.SetLocal(true)
	errs := pool.AddLocals([]*types.Transaction{tx})
	return errs[0]
}

// AddRemotes enqueues a batch of transactions into the pool if they are valid. If the
// senders are not among the locally tracked ones, full pricing constraints will apply.
//
// This method is used to add transactions from the p2p network and does not wait for pool
// reorganization and internal event propagation.
func (pool *TxPool) AddRemotes(txs []*types.Transaction) []error {
	if len(txs) == 0 {
		return []error{}
	}
	pool.remoteTxsCount += len(txs)
	return pool.addTxs(txs, false, false)
}

// This is like AddRemotes, but waits for pool reorganization. Tests use this method.
func (pool *TxPool) AddRemotesSync(txs []*types.Transaction) []error {
	return pool.addTxs(txs, false, true)
}

// This is like AddRemotes with a single transaction, but waits for pool reorganization. Tests use this method.
func (pool *TxPool) addRemoteSync(tx *types.Transaction) error {
	errs := pool.AddRemotesSync([]*types.Transaction{tx})
	return errs[0]
}

// AddRemote enqueues a single transaction into the pool if it is valid. This is a convenience
// wrapper around AddRemotes.
//
// Deprecated: use AddRemotes
func (pool *TxPool) AddRemote(tx *types.Transaction) error {
	errs := pool.AddRemotes([]*types.Transaction{tx})
	return errs[0]
}

// addTxs attempts to queue a batch of transactions if they are valid.
func (pool *TxPool) addTxs(txs []*types.Transaction, local, sync bool) []error {
	// Filter out known ones without obtaining the pool lock or recovering signatures
	var (
		errs   = make([]error, len(txs))
		news   = make([]*types.Transaction, 0, len(txs))
		qiNews = make([]*types.Transaction, 0, len(txs))
	)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for i, tx := range txs {
		// If the transaction is known, pre-set the error slot
		if pool.all.Get(tx.Hash()) != nil {
			errs[i] = ErrAlreadyKnown
			knownTxMeter.Add(1)
			continue
		}
		if tx.Type() == types.QiTxType {
			if _, hasTx := pool.qiPool.Get(tx.Hash()); hasTx {
				errs[i] = ErrAlreadyKnown
				continue
			}
			qiNews = append(qiNews, tx)
			continue
		} else if tx.Type() == types.ExternalTxType {
			errs[i] = errors.New("external tx is not supported in tx pool")
			continue
		}
		// Exclude transactions with invalid signatures as soon as
		// possible and cache senders in transactions before
		// obtaining lock
		if sender := tx.From(pool.chainconfig.Location); sender != nil {
			var err error
			_, err = sender.InternalAndQuaiAddress()
			if err != nil {
				errs[i] = err
				invalidTxMeter.Add(1)
				continue
			}
		} else if found := pool.ContainsSender(tx.Hash()); found {
			// if the sender is cached in the tx or in the pool cache, we don't need to add it into the cache
		} else {
			from, err := types.Sender(pool.signer, tx)
			if err != nil {
				errs[i] = ErrInvalidSender
				invalidTxMeter.Add(1)
				continue
			}
			_, err = from.InternalAndQuaiAddress()
			if err != nil {
				errs[i] = err
				invalidTxMeter.Add(1)
				continue
			}
		}

		// Accumulate all unknown transactions for deeper processing
		news = append(news, tx)
	}
	if len(qiNews) > 0 {
		qiErrs := pool.addQiTxs(qiNews)
		var nilSlot = 0
		for _, err := range qiErrs {
			for errs[nilSlot] != nil {
				nilSlot++
			}
			errs[nilSlot] = err
			nilSlot++
		}
	}
	if len(news) == 0 {
		return errs
	}

	// Process all the new transaction and merge any errors into the original slice
	newErrs, dirtyAddrs := pool.addTxsLocked(news, local)

	var nilSlot = 0
	for _, err := range newErrs {
		for errs[nilSlot] != nil {
			nilSlot++
		}
		errs[nilSlot] = err
		nilSlot++
	}
	// Reorg the pool internals if needed and return
	pool.requestPromoteExecutables(dirtyAddrs)
	return errs
}

var txPoolFullErrs uint64
var feesErrs uint64

// addQiTxs adds Qi transactions to the Qi pool.
// The qiMu lock must NOT be held by the caller.
func (pool *TxPool) addQiTxs(txs types.Transactions) []error {
	errs := make([]error, 0)
	currentBlock := pool.chain.CurrentBlock()
	etxRLimit := (uint64(len(currentBlock.Transactions())) * params.TxGas) / params.ETXRegionMaxFraction
	if etxRLimit < params.ETXRLimitMin {
		etxRLimit = params.ETXRLimitMin
	}
	etxPLimit := (uint64(len(currentBlock.Transactions())) * params.TxGas) / params.ETXPrimeMaxFraction
	if etxPLimit < params.ETXPLimitMin {
		etxPLimit = params.ETXPLimitMin
	}
	activeLocations := common.NewChainsAdded(pool.chain.CurrentBlock().ExpansionNumber())
	transactionsWithoutErrors := make([]*types.TxWithMinerFee, 0, len(txs))
	for _, tx := range txs {
		// Reject TX if it emits an output to an inactive chain
		for _, txo := range tx.TxOut() {
			found := false
			for _, activeLoc := range activeLocations {
				if common.IsInChainScope(txo.Address, activeLoc) {
					found = true
				}
			}
			if !found {
				errs = append(errs, fmt.Errorf("Qi TXO emitted to an inactive chain"))
			}
		}

		totalQitIn, err := ValidateQiTxInputs(tx, pool.chain, pool.db, currentBlock, pool.signer, pool.chainconfig.Location, *pool.chainconfig.ChainID)
		if err != nil {
			pool.logger.WithFields(logrus.Fields{
				"tx":  tx.Hash().String(),
				"err": err,
			}).Debug("Invalid Qi transaction")
			errs = append(errs, err)
			continue
		}
		txFee, err := ValidateQiTxOutputsAndSignature(tx, pool.chain, totalQitIn, currentBlock, pool.signer, pool.chainconfig.Location, *pool.chainconfig.ChainID, pool.qiGasScalingFactor, etxRLimit, etxPLimit)
		if err != nil {
			pool.logger.WithFields(logrus.Fields{
				"tx":  tx.Hash().String(),
				"err": err,
			}).Debug("Invalid Qi transaction")
			errs = append(errs, err)
			continue
		}
		txWithMinerFee, err := types.NewTxWithMinerFee(tx, txFee, time.Now())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		transactionsWithoutErrors = append(transactionsWithoutErrors, txWithMinerFee)
	}
	for _, txWithFee := range transactionsWithoutErrors {

		txHash := txWithFee.Tx().Hash()
		pool.qiPool.Add(txHash, txWithFee)
		pool.queueTxEvent(txWithFee.Tx())
		select {
		case pool.sendersCh <- newSender{txHash, common.InternalAddress{}}: // There is no "sender" for Qi transactions, but the sig is good
		default:
			pool.logger.Error("sendersCh is full, skipping until there is room")
		}
		select {
		case pool.feesCh <- newFee{[16]byte(txHash[:]), txWithFee.MinerFee()}:
		default:
			pool.logger.Error("feesCh is full, skipping until there is room")
		}
		pool.logger.WithFields(logrus.Fields{
			"tx":  txHash.String(),
			"fee": txWithFee.MinerFee(),
		}).Debug("Added qi tx to pool")
		qiTxGauge.Add(1)
	}
	return errs
}

func (pool *TxPool) addQiTxsWithoutValidationLocked(txs types.Transactions) {
	for _, tx := range txs {
		hash := tx.Hash()
		if _, exists := pool.qiPool.Get(hash); exists {
			continue
		}
		currentBlock := pool.chain.CurrentBlock()
		hash16 := [16]byte(hash[:])
		fee, exists := pool.qiTxFees.Get(hash16)
		if fee == nil || !exists { // this should almost never happen
			feesErrs++
			if feesErrs%1000 == 0 {
				pool.logger.Errorf("Fee is nil or doesn't exist in cache for tx %s", tx.Hash().String())
			} else {
				pool.logger.Debugf("Fee is nil or doesn't exist in cache for tx %s", tx.Hash().String())
			}
			etxRLimit := (uint64(len(currentBlock.Transactions())) * params.TxGas) / params.ETXRegionMaxFraction
			if etxRLimit < params.ETXRLimitMin {
				etxRLimit = params.ETXRLimitMin
			}
			etxPLimit := (uint64(len(currentBlock.Transactions())) * params.TxGas) / params.ETXPrimeMaxFraction
			if etxPLimit < params.ETXPLimitMin {
				etxPLimit = params.ETXPLimitMin
			}
			totalQitIn, err := ValidateQiTxInputs(tx, pool.chain, pool.db, currentBlock, pool.signer, pool.chainconfig.Location, *pool.chainconfig.ChainID)
			if err != nil {
				pool.logger.WithFields(logrus.Fields{
					"tx":  tx.Hash().String(),
					"err": err,
				}).Debug("Invalid Qi transaction, skipping re-inject")
				continue
			}
			fee, err = ValidateQiTxOutputsAndSignature(tx, pool.chain, totalQitIn, currentBlock, pool.signer, pool.chainconfig.Location, *pool.chainconfig.ChainID, pool.qiGasScalingFactor, etxRLimit, etxPLimit)
			if err != nil {
				pool.logger.WithFields(logrus.Fields{
					"tx":  tx.Hash().String(),
					"err": err,
				}).Debug("Invalid Qi transaction, skipping re-inject")
				continue
			}
			select {
			case pool.feesCh <- newFee{hash16, fee}:
			default:
				pool.logger.Error("feesCh is full, skipping until there is room")
			}
		}
		txWithMinerFee, err := types.NewTxWithMinerFee(tx, fee, time.Now())
		if err != nil {
			pool.logger.Error("Error creating txWithMinerFee: " + err.Error())
			continue
		}
		pool.qiPool.Add(tx.Hash(), txWithMinerFee)
		select {
		case pool.sendersCh <- newSender{tx.Hash(), common.InternalAddress{}}: // There is no "sender" for Qi transactions, but the sig is good
		default:
			pool.logger.Error("sendersCh is full, skipping until there is room")
		}
		pool.logger.WithFields(logrus.Fields{
			"tx":  tx.Hash().String(),
			"fee": 0,
		}).Debug("Added qi tx to pool")
		qiTxGauge.Add(1)
	}
}

func (pool *TxPool) RemoveQiTxs(txs []*common.Hash) {
	txsRemoved := 0
	pool.mu.Lock()
	for _, tx := range txs {
		if _, exists := pool.qiPool.Get(*tx); exists {
			pool.qiPool.Remove(*tx)
			txsRemoved++
		}
	}
	pool.mu.Unlock()
	qiTxGauge.Sub(float64(txsRemoved))
}

// Mempool lock must be held.
func (pool *TxPool) removeQiTxsLocked(txs []*types.Transaction) {
	txsRemoved := 0
	for _, tx := range txs {
		if _, exists := pool.qiPool.Get(tx.Hash()); exists {
			pool.qiPool.Remove(tx.Hash())
			txsRemoved++
		}
	}
	qiTxGauge.Sub(float64(txsRemoved))
}

func (pool *TxPool) AsyncRemoveQiTxs(invalidTxHashes []*common.Hash) {
	select {
	case pool.invalidQiTxsCh <- invalidTxHashes:
	default:
		pool.logger.Error("invalidQiTxsCh is full, skipping until there is room")
	}
}

// addTxsLocked attempts to queue a batch of Quai transactions if they are valid.
// The transaction pool lock must be held.
func (pool *TxPool) addTxsLocked(txs []*types.Transaction, local bool) ([]error, *accountSet) {
	dirty := newAccountSet(pool.signer)
	errs := make([]error, len(txs))
	for i, tx := range txs {
		if tx.Type() == types.QiTxType {
			continue
		}
		replaced, err := pool.add(tx, local)
		errs[i] = err
		if err == nil && !replaced {
			dirty.addTx(tx, pool.logger)
		}
	}
	validTxMeter.Add(float64(len(dirty.accounts)))
	return errs, dirty
}

// Status returns the status (unknown/pending/queued) of a batch of transactions
// identified by their hashes.
func (pool *TxPool) Status(hashes []common.Hash) []TxStatus {
	status := make([]TxStatus, len(hashes))
	for i, hash := range hashes {
		tx := pool.Get(hash)
		if tx == nil {
			continue
		}
		from, err := types.Sender(pool.signer, tx) // already validated
		if err != nil {
			pool.logger.WithField("err", err).Error("Error calculating sender in txpool Status")
			continue
		}
		internal, err := from.InternalAndQuaiAddress()
		if err != nil {
			pool.logger.WithField("err", err).Error("Error calculating internal address in txpool Status")
			continue
		}
		pool.mu.RLock()
		if txList := pool.pending[internal]; txList != nil && txList.txs.items[tx.Nonce()] != nil {
			status[i] = TxStatusPending
		} else if txList := pool.queue[internal]; txList != nil && txList.txs.items[tx.Nonce()] != nil {
			status[i] = TxStatusQueued
		}
		// implicit else: the tx may have been included into a block between
		// checking pool.Get and obtaining the lock. In that case, TxStatusUnknown is correct
		pool.mu.RUnlock()
	}
	return status
}

// Get returns a transaction if it is contained in the pool and nil otherwise.
func (pool *TxPool) Get(hash common.Hash) *types.Transaction {
	tx := pool.all.Get(hash)
	if tx == nil {
		if qiTx, ok := pool.qiPool.Get(hash); ok {
			return qiTx.Tx()
		}
	}
	return tx
}

// Has returns an indicator whether txpool has a transaction cached with the
// given hash.
func (pool *TxPool) Has(hash common.Hash) bool {
	return pool.all.Get(hash) != nil
}

// removeTx removes a single transaction from the queue, moving all subsequent
// transactions back to the future queue.
func (pool *TxPool) removeTx(hash common.Hash, outofbound bool) {
	// Fetch the transaction we wish to delete
	tx := pool.all.Get(hash)
	if tx == nil {
		return
	}
	addr, err := types.Sender(pool.signer, tx) // already validated during insertion
	if err != nil {
		pool.logger.WithField("err", err).Error("Error calculating Sender in removeTx")
		return
	}
	internal, err := addr.InternalAndQuaiAddress()
	if err != nil {
		pool.logger.WithField("err", err).Error("Error calculating InternalAddress in removeTx")
		return
	}
	// Remove it from the list of known transactions
	pool.all.Remove(hash, pool.logger)
	if outofbound {
		pool.priced.Removed(1)
	}
	if pool.locals.contains(internal) {
		localTxGauge.Dec()
	}
	// Remove the transaction from the pending lists and reset the account nonce
	if pending := pool.pending[internal]; pending != nil {
		if removed, invalids := pending.Remove(tx); removed {
			// If no more pending transactions are left, remove the list
			if pending.Empty() {
				delete(pool.pending, internal)
			}
			// Postpone any invalidated transactions
			for _, tx := range invalids {
				// Internal shuffle shouldn't touch the lookup set.
				pool.enqueueTx(tx.Hash(), tx, false, false)
			}
			// Update the account nonce if needed
			pool.pendingNonces.setIfLower(internal, tx.Nonce())
			// Reduce the pending counter
			pendingTxGauge.Sub(float64(len(invalids) + 1))
			return
		}
	}
	// Transaction is in the future queue
	if future := pool.queue[internal]; future != nil {
		if removed, _ := future.Remove(tx); removed {
			// Reduce the queued counter
			queuedGauge.Dec()
		}
		if future.Empty() {
			delete(pool.queue, internal)
			delete(pool.beats, internal)
		}
	}
}

// requestReset requests a pool reset to the new head block.
// The returned channel is closed when the reset has occurred.
func (pool *TxPool) requestReset(oldHead *types.WorkObject, newHead *types.WorkObject) chan struct{} {
	select {
	case pool.reqResetCh <- &txpoolResetRequest{oldHead, newHead}:
		return <-pool.reorgDoneCh
	case <-pool.reorgShutdownCh:
		return pool.reorgShutdownCh
	}
}

// requestPromoteExecutables requests transaction promotion checks for the given addresses.
// The returned channel is closed when the promotion checks have occurred.
func (pool *TxPool) requestPromoteExecutables(set *accountSet) {
	select {
	case pool.reqPromoteCh <- set:
	case <-pool.reorgShutdownCh:
	}
}

// queueTxEvent enqueues a transaction event to be sent in the next reorg run if it is local.
func (pool *TxPool) queueTxEvent(tx *types.Transaction) {
	if tx.IsLocal() {
		select {
		case pool.queueTxEventCh <- tx:
		case <-pool.reorgShutdownCh:
		}
	}
}

// SendTxToSharingClients sends the tx into the pool sharing tx ch and
// if its full logs it
func (pool *TxPool) SendTxToSharingClients(tx *types.Transaction) error {
	// If there are no tx pool sharing clients just submit to the local pool
	if !pool.config.SyncTxWithReturn || len(pool.config.SharingClientsEndpoints) == 0 {
		err := pool.AddLocal(tx)
		if err != nil {
			return err
		}
		select {
		case pool.poolSharingTxCh <- tx:
		default:
			pool.logger.Warn("pool sharing tx ch is full")
		}
		return err
	} else {
		// send to the first client, and then submit to the rest
		client := pool.poolSharingClients[0]
		ctx, cancel := context.WithTimeout(context.Background(), txSharingPoolTimeout)
		defer cancel()
		err := client.SendTransactionToPoolSharingClient(ctx, tx)
		if err != nil {
			pool.logger.WithField("err", err).Error("Error sending transaction to pool sharing client")
		}

		if len(pool.poolSharingClients) > 1 {
			// send to all pool sharing clients
			for _, client := range pool.poolSharingClients[1:] {
				if client != nil {
					go func(*quaiclient.Client, *types.Transaction) {
						ctx, cancel := context.WithTimeout(context.Background(), txSharingPoolTimeout)
						defer cancel()
						sendErr := client.SendTransactionToPoolSharingClient(ctx, tx)
						if sendErr != nil {
							pool.logger.WithField("err", sendErr).Error("Error sending transaction to pool sharing client")
						}
					}(client, tx)
				}
			}
		}

		return err
	}
}

// txListenerLoop listens to tx coming on the pool sharing tx ch and
// then sends the tx into the pool sharing clients
func (pool *TxPool) txListenerLoop() {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	defer pool.wg.Done()

	for {
		select {
		case tx := <-pool.poolSharingTxCh:
			// send to all pool sharing clients
			for _, client := range pool.poolSharingClients {
				if client != nil {
					go func(*quaiclient.Client, *types.Transaction) {
						ctx, cancel := context.WithTimeout(context.Background(), txSharingPoolTimeout)
						defer cancel()
						err := client.SendTransactionToPoolSharingClient(ctx, tx)
						if err != nil {
							pool.logger.WithField("err", err).Error("Error sending transaction to pool sharing client")
						}
					}(client, tx)
				}
			}
		case <-pool.reorgShutdownCh:
			return
		}
	}
}

// createSharingClient creates a quaiclient connection and writes it to the
// SharingClients
func (pool *TxPool) createSharingClient(index int) {
	client, err := quaiclient.Dial(pool.config.SharingClientsEndpoints[index], pool.logger)
	if err != nil {
		pool.logger.WithField("end point", pool.config.SharingClientsEndpoints[index]).Warn("Client was nil trying to send transactions")
		return
	}

	pool.logger.WithField("endpoint", pool.config.SharingClientsEndpoints[index]).Info("Pool sharing client connected")

	// set the created client into the pool sharing client
	pool.poolSharingClients[index] = client
}

// scheduleReorgLoop schedules runs of reset and promoteExecutables. Code above should not
// call those methods directly, but request them being run using requestReset and
// requestPromoteExecutables instead.
func (pool *TxPool) scheduleReorgLoop() {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	defer pool.wg.Done()

	var (
		curDone        chan struct{} // non-nil while runReorg is active
		nextDone       = make(chan struct{})
		launchNextRun  bool
		reset          *txpoolResetRequest
		dirtyAccounts  *accountSet
		queuedEvents   = make(map[common.InternalAddress]*txSortedMap)
		reorgCancelCh  = make(chan struct{})
		queuedQiTxs    = make([]*types.Transaction, 0)
		runReorgTicker = time.NewTicker(pool.config.ReorgFrequency)
	)
	for {
		// Launch next background reorg if needed
		if curDone == nil && launchNextRun {
			// kill any currently running runReorg and launch the next one
			close(reorgCancelCh)
			reorgCancelCh = make(chan struct{})
			// Run the background reorg and announcements
			go pool.runReorg(nextDone, reorgCancelCh, reset, dirtyAccounts, queuedEvents, queuedQiTxs)

			// Prepare everything for the next round of reorg
			curDone, nextDone = nextDone, make(chan struct{})
			launchNextRun = false

			reset, dirtyAccounts = nil, nil
			queuedEvents = make(map[common.InternalAddress]*txSortedMap)
			queuedQiTxs = make([]*types.Transaction, 0)
		}

		select {
		case req := <-pool.reqResetCh:
			// Reset request: update head if request is already pending.
			if reset == nil {
				reset = req
			} else {
				reset.newHead = req.newHead
			}
			launchNextRun = true
			pool.reorgDoneCh <- nextDone

		case req := <-pool.reqPromoteCh:
			// Promote request: update address set if request is already pending.
			if dirtyAccounts == nil {
				dirtyAccounts = req
			} else {
				dirtyAccounts.merge(req)
			}

		case <-runReorgTicker.C:
			// Timer tick: launch the next reorg run
			launchNextRun = true

		case tx := <-pool.queueTxEventCh:
			// Queue up the event, but don't schedule a reorg. It's up to the caller to
			// request one later if they want the events sent.
			if tx.Type() == types.QiTxType {
				queuedQiTxs = append(queuedQiTxs, tx)
			} else {
				addr, err := types.Sender(pool.signer, tx)
				if err != nil {
					pool.logger.WithField("err", err).Error("Error calculating the sender in scheduleReorgLoop")
					continue
				}
				internal, err := addr.InternalAndQuaiAddress()
				if err != nil {
					pool.logger.WithField("err", err).Debug("Failed to queue transaction")
					continue
				}
				if _, ok := queuedEvents[internal]; !ok {
					queuedEvents[internal] = newTxSortedMap()
				}
				queuedEvents[internal].Put(tx)
			}

		case <-curDone:
			curDone = nil

		case <-pool.reorgShutdownCh:
			// Wait for current run to finish.
			if curDone != nil {
				<-curDone
			}
			close(nextDone)
			return
		}
	}
}

// runReorg runs reset and promoteExecutables on behalf of scheduleReorgLoop.
func (pool *TxPool) runReorg(done chan struct{}, cancel chan struct{}, reset *txpoolResetRequest, dirtyAccounts *accountSet, events map[common.InternalAddress]*txSortedMap, queuedQiTxs []*types.Transaction) {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	defer close(done)

	for {
		select {
		case <-cancel:
			return
		default:
			pool.reOrgCounter += 1
			var start time.Time
			if pool.reOrgCounter == c_reorgCounterThreshold {
				start = time.Now()
			}

			var promoteAddrs []common.InternalAddress
			if dirtyAccounts != nil && reset == nil {
				// Only dirty accounts need to be promoted, unless we're resetting.
				// For resets, all addresses in the tx queue will be promoted and
				// the flatten operation can be avoided.
				promoteAddrs = dirtyAccounts.flatten()
			}
			pool.mu.Lock()
			if reset != nil {
				// Reset from the old head to the new, rescheduling any reorged transactions
				pool.reset(reset.oldHead, reset.newHead)

				// Nonces were reset, discard any events that became stale
				for addr := range events {
					events[addr].Forward(pool.pendingNonces.get(addr))
					if events[addr].Len() == 0 {
						delete(events, addr)
					}
				}
				// Reset needs promote for all addresses
				promoteAddrs = make([]common.InternalAddress, 0, len(pool.queue))
				for addr := range pool.queue {
					promoteAddrs = append(promoteAddrs, addr)
				}
			}
			// Check for pending transactions for every account that sent new ones
			promoted := pool.promoteExecutables(promoteAddrs)

			// If a new block appeared, validate the pool of pending transactions. This will
			// remove any transaction that has been included in the block or was invalidated
			// because of another transaction (e.g. higher gas price).
			if reset != nil {
				pool.demoteUnexecutables()
				if reset.newHead != nil {
					pendingBaseFee := pool.chain.CurrentBlock().BaseFee()
					pool.priced.SetBaseFee(pendingBaseFee)
				}
			}
			// Ensure pool.queue and pool.pending sizes stay within the configured limits.
			pool.truncatePending()
			pool.truncateQueue()

			// Update all accounts to the latest known pending nonce
			for addr, list := range pool.pending {
				highestPending := list.LastElement()
				pool.pendingNonces.set(addr, highestPending.Nonce()+1)
			}
			pool.mu.Unlock()

			// Notify subsystems for newly added transactions
			for _, tx := range promoted {
				if !tx.IsLocal() {
					continue
				}
				addr, err := types.Sender(pool.signer, tx)
				if err != nil {
					pool.logger.WithField("err", err).Error("Error calculating the sender in runreorg")
					continue
				}
				internal, err := addr.InternalAndQuaiAddress()
				if err != nil {
					pool.logger.WithField("err", err).Debug("Failed to add transaction event")
					continue
				}
				if _, ok := events[internal]; !ok {
					events[(internal)] = newTxSortedMap()
				}
				events[internal].Put(tx)
			}
			var txs []*types.Transaction
			if len(events) > 0 {
				for _, set := range events {
					txs = append(txs, set.Flatten()...)
				}
			}
			if len(queuedQiTxs) > 0 {
				txs = append(txs, queuedQiTxs...)
			}
			if len(pool.broadcastSet)+len(txs) < int(pool.chain.GetMaxTxInWorkShare()) {
				pool.broadcastSetMu.Lock()
				pool.broadcastSet = append(pool.broadcastSet, txs...)
				pool.broadcastSetMu.Unlock()
			}

			if pool.reOrgCounter == c_reorgCounterThreshold {
				pool.logger.WithField("time", common.PrettyDuration(time.Since(start))).Debug("Time taken to runReorg in txpool")
				pool.reOrgCounter = 0
			}
			return
		}
	}
}

// reset retrieves the current state of the blockchain and ensures the content
// of the transaction pool is valid with regard to the chain state.
// The mempool lock must be held by the caller.
func (pool *TxPool) reset(oldHead, newHead *types.WorkObject) {
	nodeCtx := pool.chainconfig.Location.Context()
	var start time.Time
	if pool.reOrgCounter == c_reorgCounterThreshold {
		start = time.Now()
	}
	// If we're reorging an old state, reinject all dropped transactions
	var reinject types.Transactions

	if oldHead != nil && oldHead.Hash() != newHead.ParentHash(nodeCtx) {
		// If the reorg is too deep, avoid doing it (will happen during fast sync)
		oldNum := oldHead.NumberU64(nodeCtx)
		newNum := newHead.NumberU64(nodeCtx)

		if depth := uint64(math.Abs(float64(oldNum) - float64(newNum))); depth > 64 {
			pool.logger.WithField("depth", depth).Debug("Skipping deep transaction reorg")
		} else {
			// Reorg seems shallow enough to pull in all transactions into memory
			var discarded, included types.Transactions
			var (
				rem = pool.chain.GetBlock(oldHead.Hash(), oldHead.NumberU64(nodeCtx))
				add = pool.chain.GetBlock(newHead.Hash(), newHead.NumberU64(nodeCtx))
			)
			if rem == nil {
				// This can happen if a setHead is performed, where we simply discard the old
				// head from the chain.
				// If that is the case, we don't have the lost transactions any more, and
				// there's nothing to add
				if newNum >= oldNum {
					// If we reorged to a same or higher number, then it's not a case of setHead
					pool.logger.WithFields(log.Fields{
						"old":    oldHead.Hash(),
						"oldnum": oldNum,
						"new":    newHead.Hash(),
						"newnum": newNum,
					}).Warn("Transaction pool reset with missing oldhead")
					return
				}
				// If the reorg ended up on a lower number, it's indicative of setHead being the cause
				pool.logger.WithFields(log.Fields{
					"old":    oldHead.Hash(),
					"oldnum": oldNum,
					"new":    newHead.Hash(),
					"newnum": newNum,
				}).Debug("Skipping transaction reset caused by setHead")
				// We still need to update the current state s.th. the lost transactions can be readded by the user
			} else {
				if rem == nil || add == nil {
					pool.logger.Error("Unrooted chain seen by tx pool")
					return
				}
				for rem.NumberU64(nodeCtx) > add.NumberU64(nodeCtx) {
					discarded = append(discarded, rem.Transactions()...)
					if rem = pool.chain.GetBlock(rem.ParentHash(nodeCtx), rem.NumberU64(nodeCtx)-1); rem == nil {
						pool.logger.WithFields(log.Fields{
							"block": oldHead.Number(nodeCtx),
							"hash":  oldHead.Hash(),
						}).Error("Unrooted old chain seen by tx pool")
						return
					}
				}
				for add.NumberU64(nodeCtx) > rem.NumberU64(nodeCtx) {
					included = append(included, add.Transactions()...)
					if add = pool.chain.GetBlock(add.ParentHash(nodeCtx), add.NumberU64(nodeCtx)-1); add == nil {
						pool.logger.WithFields(log.Fields{
							"block": newHead.Number(nodeCtx),
							"hash":  newHead.Hash(),
						}).Error("Unrooted new chain seen by tx pool")
						return
					}
				}
				for rem.Hash() != add.Hash() {
					discarded = append(discarded, rem.Transactions()...)
					if rem = pool.chain.GetBlock(rem.ParentHash(nodeCtx), rem.NumberU64(nodeCtx)-1); rem == nil {
						pool.logger.WithFields(log.Fields{
							"block": oldHead.Number(nodeCtx),
							"hash":  oldHead.Hash(),
						}).Error("Unrooted old chain seen by tx pool")
						return
					}
					included = append(included, add.Transactions()...)
					if add = pool.chain.GetBlock(add.ParentHash(nodeCtx), add.NumberU64(nodeCtx)-1); add == nil {
						pool.logger.WithFields(log.Fields{
							"block": newHead.Number(nodeCtx),
							"hash":  newHead.Hash(),
						}).Error("Unrooted new chain seen by tx pool")
						return
					}
				}
				reinject = types.TxDifferenceWithoutETXs(discarded, included)
			}
		}
	} else {
		block := pool.chain.GetBlock(newHead.Hash(), newHead.Number(pool.chainconfig.Location.Context()).Uint64())
		pool.removeQiTxsLocked(block.QiTransactionsWithoutCoinbase())
		pool.logger.WithField("count", len(block.QiTransactionsWithoutCoinbase())).Debug("Removed qi txs from pool")
	}
	// Initialize the internal state to the current head
	if newHead == nil {
		newHead = pool.chain.CurrentBlock() // Special case during testing
	}

	evmRoot := newHead.EVMRoot()
	etxRoot := newHead.EtxSetRoot()
	quaiStateSize := newHead.QuaiStateSize()
	if pool.chain.IsGenesisHash(newHead.Hash()) {
		evmRoot = types.EmptyRootHash
		etxRoot = types.EmptyRootHash
		quaiStateSize = big.NewInt(0)
	}
	statedb, err := pool.chain.StateAt(evmRoot, etxRoot, quaiStateSize)
	if err != nil {
		pool.logger.WithField("err", err).Error("Failed to reset txpool state")
		return
	}
	pool.currentState = statedb
	pool.qiGasScalingFactor = math.Log(float64(rawdb.ReadUTXOSetSize(pool.db, newHead.Hash())))
	pool.pendingNonces = newTxNoncer(statedb)
	pool.currentMaxGas = newHead.GasLimit()
	if pool.currentMaxGas == 0 {
		pool.currentMaxGas = params.GenesisGasLimit
	}

	// Inject any transactions discarded due to reorgs
	pool.logger.WithField("count", len(reinject)).Debug("Reinjecting stale transactions")
	senderCacher.recover(pool.signer, reinject)

	qiTxs := make([]*types.Transaction, 0)
	for _, tx := range reinject {
		if tx.Type() == types.QiTxType {
			qiTxs = append(qiTxs, tx)
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pool.logger.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		if len(reinject) > 0 {
			pool.addTxsLocked(reinject, false)
		}
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pool.logger.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		if len(qiTxs) > 0 {
			pool.addQiTxsWithoutValidationLocked(qiTxs)
		}
		wg.Done()
	}()
	wg.Wait()
	if pool.reOrgCounter == c_reorgCounterThreshold {
		pool.logger.WithField("time", common.PrettyDuration(time.Since(start))).Debug("Time taken to resetTxPool")
	}
}

// promoteExecutables moves transactions that have become processable from the
// future queue to the set of pending transactions. During this process, all
// invalidated transactions (low nonce, low balance) are deleted.
func (pool *TxPool) promoteExecutables(accounts []common.InternalAddress) []*types.Transaction {
	var start time.Time
	if pool.reOrgCounter == c_reorgCounterThreshold {
		start = time.Now()
	}
	// Track the promoted transactions to broadcast them at once
	var promoted []*types.Transaction

	// Iterate over all accounts and promote any executable transactions
	for _, addr := range accounts {
		list := pool.queue[addr]
		if list == nil {
			continue // Just in case someone calls with a non existing account
		}
		// Drop all transactions that are deemed too old (low nonce)
		forwards := list.Forward(pool.currentState.GetNonce(addr))
		for _, tx := range forwards {
			hash := tx.Hash()
			pool.all.Remove(hash, pool.logger)
		}
		pool.logger.WithField("count", len(forwards)).Trace("Removed old queued transactions")
		// Drop all transactions that are too costly (low balance or out of gas)
		drops, _ := list.Filter(pool.currentState.GetBalance(addr), pool.currentMaxGas)
		for _, tx := range drops {
			hash := tx.Hash()
			pool.all.Remove(hash, pool.logger)
		}
		pool.logger.WithField("count", len(drops)).Trace("Removed unpayable queued transactions")
		queuedNofundsMeter.Add(float64(len(drops)))

		// Gather all executable transactions and promote them
		readies := list.Ready(pool.pendingNonces.get(addr))
		for _, tx := range readies {
			hash := tx.Hash()
			if pool.promoteTx(addr, hash, tx) {
				promoted = append(promoted, tx)
			}
		}
		pool.logger.WithField("count", len(promoted)).Trace("Promoted executable queued transactions")
		queuedGauge.Sub(float64(len(readies)))

		// Drop all transactions over the allowed limit
		caps := list.Cap(int(pool.config.AccountQueue))
		for _, tx := range caps {
			hash := tx.Hash()
			pool.all.Remove(hash, pool.logger)
			pool.logger.WithField("hash", hash).Trace("Removed cap-exceeding queued transaction")
		}
		queuedRateLimitMeter.Add(float64(len(caps)))
		// Mark all the items dropped as removed
		pool.priced.Removed(len(forwards) + len(drops) + len(caps))
		if pool.locals.contains(addr) {
			localTxGauge.Sub(float64(len(forwards) + len(drops) + len(caps)))
		}
		// Delete the entire queue entry if it became empty.
		if list.Empty() {
			delete(pool.queue, addr)
			delete(pool.beats, addr)
		}
	}
	if pool.reOrgCounter == c_reorgCounterThreshold {
		pool.logger.WithField("time", common.PrettyDuration(time.Since(start))).Debug("Time taken to promoteExecutables")
	}
	return promoted
}

// truncatePending removes transactions from the pending queue if the pool is above the
// pending limit. The algorithm tries to reduce transaction counts by an approximately
// equal number for all for accounts with many pending transactions.
func (pool *TxPool) truncatePending() {
	var start time.Time
	if pool.reOrgCounter == c_reorgCounterThreshold {
		start = time.Now()
	}
	pending := uint64(0)
	for _, list := range pool.pending {
		pending += uint64(list.Len())
	}
	if pending <= pool.config.GlobalSlots {
		return
	}

	pendingBeforeCap := pending
	// Assemble a spam order to penalize large transactors first
	spammers := prque.New(nil)
	for addr, list := range pool.pending {
		// Only evict transactions from high rollers
		if uint64(list.Len()) > pool.config.AccountSlots {
			spammers.Push(addr, int64(list.Len()))
		}
	}
	// Gradually drop transactions from offenders
	offenders := []common.InternalAddress{}
	for pending > pool.config.GlobalSlots && !spammers.Empty() {
		// Retrieve the next offender
		offender, _ := spammers.Pop()
		offenders = append(offenders, offender.(common.InternalAddress))

		// Equalize balances until all the same or below threshold
		if len(offenders) > 1 {
			// Calculate the equalization threshold for all current offenders
			threshold := pool.pending[offender.(common.InternalAddress)].Len()

			// Iteratively reduce all offenders until below limit or threshold reached
			for pending > pool.config.GlobalSlots && pool.pending[offenders[len(offenders)-2]].Len() > threshold {
				for i := 0; i < len(offenders)-1; i++ {
					list := pool.pending[offenders[i]]

					caps := list.Cap(list.Len() - 1)
					for _, tx := range caps {
						// Drop the transaction from the global pools too
						hash := tx.Hash()
						pool.all.Remove(hash, pool.logger)

						// Update the account nonce to the dropped transaction
						pool.pendingNonces.setIfLower(offenders[i], tx.Nonce())
						pool.logger.WithField("hash", hash).Trace("Removed fairness-exceeding pending transaction")
					}
					pool.priced.Removed(len(caps))
					pendingTxGauge.Sub(float64(len(caps)))
					if pool.locals.contains(offenders[i]) {
						localTxGauge.Sub(float64(len(caps)))
					}
					pending--
				}
			}
		}
	}

	// If still above threshold, reduce to limit or min allowance
	if pending > pool.config.GlobalSlots && len(offenders) > 0 {
		for pending > pool.config.GlobalSlots && uint64(pool.pending[offenders[len(offenders)-1]].Len()) > pool.config.AccountSlots {
			for _, addr := range offenders {
				list := pool.pending[addr]

				caps := list.Cap(list.Len() - 1)
				for _, tx := range caps {
					// Drop the transaction from the global pools too
					hash := tx.Hash()
					pool.all.Remove(hash, pool.logger)

					// Update the account nonce to the dropped transaction
					pool.pendingNonces.setIfLower(addr, tx.Nonce())
					pool.logger.WithField("hash", hash).Trace("Removed fairness-exceeding pending transaction")
				}
				pool.priced.Removed(len(caps))
				pendingTxGauge.Sub(float64(len(caps)))
				if pool.locals.contains(addr) {
					localTxGauge.Sub(float64(len(caps)))
				}
				pending--
			}
		}
	}
	pendingRateLimitMeter.Add(float64(pendingBeforeCap - pending))
	if pool.reOrgCounter == c_reorgCounterThreshold {
		pool.logger.WithField("time", common.PrettyDuration(time.Since(start))).Debug("Time taken to truncatePending")
	}
}

// truncateQueue drops the oldes transactions in the queue if the pool is above the global queue limit.
func (pool *TxPool) truncateQueue() {
	var start time.Time
	if pool.reOrgCounter == c_reorgCounterThreshold {
		start = time.Now()
	}
	queued := uint64(0)
	for _, list := range pool.queue {
		queued += uint64(list.Len())
	}
	if queued <= pool.config.GlobalQueue {
		return
	}

	// Sort all accounts with queued transactions by heartbeat
	addresses := make(addressesByHeartbeat, 0, len(pool.queue))
	for addr := range pool.queue {
		addresses = append(addresses, addressByHeartbeat{addr, pool.beats[addr]})
	}
	sort.Sort(addresses)

	// Drop transactions until the total is below the limit
	for drop := queued - pool.config.GlobalQueue; drop > 0 && len(addresses) > 0; {
		addr := addresses[len(addresses)-1]
		list := pool.queue[addr.address]

		addresses = addresses[:len(addresses)-1]

		// Drop all transactions if they are less than the overflow
		if size := uint64(list.Len()); size <= drop {
			for _, tx := range list.Flatten() {
				pool.removeTx(tx.Hash(), true)
			}
			drop -= size
			queuedRateLimitMeter.Add(float64(size))
			continue
		}
		// Otherwise drop only last few transactions
		txs := list.Flatten()
		for i := len(txs) - 1; i >= 0 && drop > 0; i-- {
			pool.removeTx(txs[i].Hash(), true)
			drop--
			queuedRateLimitMeter.Add(1)
		}
	}
	if pool.reOrgCounter == c_reorgCounterThreshold {
		pool.logger.WithField("time", common.PrettyDuration(time.Since(start))).Debug("Time taken to truncateQueue")
	}
}

// demoteUnexecutables removes invalid and processed transactions from the pools
// executable/pending queue and any subsequent transactions that become unexecutable
// are moved back into the future queue.
//
// Note: transactions are not marked as removed in the priced list because re-heaping
// is always explicitly triggered by SetBaseFee and it would be unnecessary and wasteful
// to trigger a re-heap is this function
func (pool *TxPool) demoteUnexecutables() {
	var start time.Time
	if pool.reOrgCounter == c_reorgCounterThreshold {
		start = time.Now()
	}
	// Iterate over all accounts and demote any non-executable transactions
	for addr, list := range pool.pending {
		nonce := pool.currentState.GetNonce(addr)

		// Drop all transactions that are deemed too old (low nonce)
		olds := list.Forward(nonce)
		for _, tx := range olds {
			hash := tx.Hash()
			pool.all.Remove(hash, pool.logger)
			pool.logger.WithField("hash", hash).Trace("Removed old pending transaction")
		}
		// Drop all transactions that are too costly (low balance or out of gas), and queue any invalids back for later
		drops, invalids := list.Filter(pool.currentState.GetBalance(addr), pool.currentMaxGas)
		for _, tx := range drops {
			hash := tx.Hash()
			pool.logger.WithField("hash", hash).Trace("Removed unpayable pending transaction")
			pool.all.Remove(hash, pool.logger)
		}
		pendingNofundsMeter.Add(float64(len(drops)))

		for _, tx := range invalids {
			hash := tx.Hash()
			pool.logger.WithField("hash", hash).Trace("Demoting pending transaction")

			// Internal shuffle shouldn't touch the lookup set.
			pool.enqueueTx(hash, tx, false, false)
		}
		removedTxs := float64(len(olds) + len(drops) + len(invalids))
		pendingTxGauge.Sub(removedTxs)
		if pool.locals.contains(addr) {
			localTxGauge.Sub(removedTxs)
		}
		// If there's a gap in front, alert (should never happen) and postpone all transactions
		if list.Len() > 0 && list.txs.Get(nonce) == nil {
			gapped := list.Cap(0)
			pool.logger.WithField("count", len(gapped)).Error("Demoting invalidated transactions")
			for _, tx := range gapped {
				hash := tx.Hash()

				// Internal shuffle shouldn't touch the lookup set.
				pool.enqueueTx(hash, tx, false, false)
			}
			pendingTxGauge.Sub(float64(len(gapped)))
		}
		// Delete the entire pending entry if it became empty.
		if list.Empty() {
			delete(pool.pending, addr)
		}
	}
	if pool.reOrgCounter == c_reorgCounterThreshold {
		pool.logger.WithField("time", common.PrettyDuration(time.Since(start))).Debug("Time taken to demoteUnexecutables")
	}
}

// PeekSender returns the sender of a stored transaction without updating the LRU cache and without grabbing
// the SendersMu lock.
func (pool *TxPool) PeekSenderNoLock(hash common.Hash) (common.InternalAddress, bool) {
	addr, ok := pool.senders.Peek(hash)
	if ok {
		return addr, true
	}
	return common.InternalAddress{}, false
}

func (pool *TxPool) ContainsSender(hash common.Hash) bool {
	pool.SendersMu.RLock()
	defer pool.SendersMu.RUnlock()
	return pool.senders.Contains(hash)
}

func (pool *TxPool) ContainsOrAddSender(hash common.Hash, sender common.InternalAddress) (bool, bool) {
	pool.SendersMu.Lock()
	defer pool.SendersMu.Unlock()
	return pool.senders.ContainsOrAdd(hash, sender)
}

func (pool *TxPool) PeekSender(hash common.Hash) (common.InternalAddress, bool) {
	pool.SendersMu.RLock()
	defer pool.SendersMu.RUnlock()
	addr, ok := pool.senders.Peek(hash)
	if ok {
		return addr, true
	}
	return common.InternalAddress{}, false
}

// sendersGoroutine asynchronously adds a new sender to the cache
func (pool *TxPool) sendersGoroutine() {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case <-pool.reorgShutdownCh:
			return
		case tx := <-pool.sendersCh:
			// Add transaction to sender cache
			if contains, _ := pool.ContainsOrAddSender(tx.hash, tx.sender); contains {
				pool.logger.WithFields(log.Fields{
					"tx":     tx.hash.String(),
					"sender": tx.sender.String(),
				}).Debug("Tx already seen in sender cache (reorg?)")
			}
		}
	}
}

// feesGoroutine asynchronously adds a new tx fee to the cache
func (pool *TxPool) feesGoroutine() {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case <-pool.reorgShutdownCh:
			return
		case tx := <-pool.feesCh:
			// Add transaction to sender cache
			if contains, _ := pool.qiTxFees.ContainsOrAdd(tx.hash, tx.fee); contains {
				pool.logger.WithFields(log.Fields{
					"tx":  hexutil.Encode(tx.hash[:]),
					"fee": tx.fee.String(),
				}).Debug("Tx already seen in fees cache (reorg?)")
			}
		}
	}
}

// The PoolLimiter routinely checks if the pending or queued tx pools have exceeded their limits
// and if so, removes excess transactions from the pool.
func (pool *TxPool) poolLimiterGoroutine() {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	// Check every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	for {
		select {
		case <-pool.reorgShutdownCh:
			return
		case <-ticker.C:
			pool.mu.RLock()
			queued := uint64(0)
			for _, list := range pool.queue {
				queued += uint64(list.Len())
			}
			pending := uint64(0)
			for _, list := range pool.pending {
				pending += uint64(list.Len())
			}
			pool.mu.RUnlock()
			pool.logger.Infof("PoolSize: Pending: %d, Queued: %d, Number of accounts in queue: %d, Qi Pool: %d", pending, queued, len(pool.queue), pool.qiPool.Len())
			pendingTxGauge.Set(float64(pending))
			queuedGauge.Set(float64(queued))
			if queued > pool.config.GlobalQueue {
				start := time.Now()
				pool.logger.Infof("Queued pool size exceeded limit: %d > %d", queued, pool.config.GlobalQueue)
				pool.mu.Lock()
				for _, list := range pool.queue {
					capacity := int(list.Len() - int(pool.config.AccountQueue))
					if capacity < 0 {
						capacity = 0
					}
					caps := list.Cap(capacity)
					for _, tx := range caps {
						hash := tx.Hash()
						pool.all.Remove(hash, pool.logger)
						pool.removeTx(hash, true)
						pool.logger.WithField("hash", hash).Trace("Removed cap-exceeding queued transaction")
					}
					pool.priced.Removed(len(caps))
					queuedRateLimitMeter.Add(float64(len(caps)))
				}
				pool.mu.Unlock()
				pool.mu.RLock()
				queued = 0
				for _, list := range pool.queue {
					queued += uint64(list.Len())
				}
				pool.mu.RUnlock()
				if queued < pool.config.GlobalQueue {
					continue
				}
				pool.logger.WithField("queued", queued).Error("Failed to truncate queued pool, deleting accounts")
				deleted := 0
				pool.mu.Lock()
				for _, list := range pool.queue {
					txs := list.Flatten()
					for _, tx := range txs {
						pool.removeTx(tx.Hash(), true)
						deleted++
					}
					if len(txs) > int(queued) {
						break
					}
					queued -= uint64(len(txs))
					if queued < pool.config.GlobalQueue {
						break
					}
				}
				pool.mu.Unlock()
				pool.logger.Infof("Truncation took %d ms. Queued pool size after truncation: %d, removed: %d", time.Since(start).Milliseconds(), queued, deleted)
			}
		}
	}

}

func (pool *TxPool) invalidQiTxGoroutine() {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case <-pool.reorgShutdownCh:
			return
		case invalidTxHashes := <-pool.invalidQiTxsCh:
			pool.RemoveQiTxs(invalidTxHashes)
		}
	}
}

func (pool *TxPool) qiTxExpirationGoroutine() {
	defer func() {
		if r := recover(); r != nil {
			pool.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	ticker := time.NewTicker(qiExpirationCheckInterval)
	for {
		select {
		case <-pool.reorgShutdownCh:
			return
		case <-ticker.C:
			// Remove expired QiTxs
			// Grabbing lock is not necessary as LRU already has lock internally
			for i := 0; i < pool.qiPool.Len()/qiExpirationCheckDivisor; i++ {
				_, oldestTx, _ := pool.qiPool.GetOldest()
				if time.Since(oldestTx.Received()) > pool.config.QiTxLifetime {
					pool.qiPool.Remove(oldestTx.Tx().Hash())
				}
			}
		}
	}
}

// addressByHeartbeat is an account address tagged with its last activity timestamp.
type addressByHeartbeat struct {
	address   common.InternalAddress
	heartbeat time.Time
}

type addressesByHeartbeat []addressByHeartbeat

func (a addressesByHeartbeat) Len() int           { return len(a) }
func (a addressesByHeartbeat) Less(i, j int) bool { return a[i].heartbeat.Before(a[j].heartbeat) }
func (a addressesByHeartbeat) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

// accountSet is simply a set of addresses to check for existence, and a signer
// capable of deriving addresses from transactions.
type accountSet struct {
	accounts map[common.InternalAddress]struct{}
	signer   types.Signer
	cache    *[]common.InternalAddress
}

// newAccountSet creates a new address set with an associated signer for sender
// derivations.
func newAccountSet(signer types.Signer, addrs ...common.InternalAddress) *accountSet {
	as := &accountSet{
		accounts: make(map[common.InternalAddress]struct{}),
		signer:   signer,
	}
	for _, addr := range addrs {
		as.add(addr)
	}
	return as
}

// contains checks if a given address is contained within the set.
func (as *accountSet) contains(addr common.InternalAddress) bool {
	_, exist := as.accounts[addr]
	return exist
}

func (as *accountSet) empty() bool {
	return len(as.accounts) == 0
}

// containsTx checks if the sender of a given tx is within the set. If the sender
// cannot be derived, this method returns false.
func (as *accountSet) containsTx(tx *types.Transaction) bool {
	if addr, err := types.Sender(as.signer, tx); err == nil {
		internal, err := addr.InternalAndQuaiAddress()
		if err != nil {
			return false
		}
		return as.contains(internal)
	}
	return false
}

// add inserts a new address into the set to track.
func (as *accountSet) add(addr common.InternalAddress) {
	as.accounts[addr] = struct{}{}
	as.cache = nil
}

// addTx adds the sender of tx into the set.
func (as *accountSet) addTx(tx *types.Transaction, logger *log.Logger) {
	if addr, err := types.Sender(as.signer, tx); err == nil {
		internal, err := addr.InternalAndQuaiAddress()
		if err != nil {
			logger.WithField("err", err).Debug("Failed to add tx to account set")
			return
		}
		as.add(internal)
	}
}

// flatten returns the list of addresses within this set, also caching it for later
// reuse. The returned slice should not be changed!
func (as *accountSet) flatten() []common.InternalAddress {
	if as.cache == nil {
		accounts := make([]common.InternalAddress, 0, len(as.accounts))
		for account := range as.accounts {
			accounts = append(accounts, account)
		}
		as.cache = &accounts
	}
	return *as.cache
}

// merge adds all addresses from the 'other' set into 'as'.
func (as *accountSet) merge(other *accountSet) {
	for addr := range other.accounts {
		as.accounts[addr] = struct{}{}
	}
	as.cache = nil
}

// txLookup is used internally by TxPool to track transactions while allowing
// lookup without mutex contention.
//
// Note, although this type is properly protected against concurrent access, it
// is **not** a type that should ever be mutated or even exposed outside of the
// transaction pool, since its internal state is tightly coupled with the pools
// internal mechanisms. The sole purpose of the type is to permit out-of-bound
// peeking into the pool in TxPool.Get without having to acquire the widely scoped
// TxPool.mu mutex.
//
// This lookup set combines the notion of "local transactions", which is useful
// to build upper-level structure.
type txLookup struct {
	slots   int
	lock    sync.RWMutex
	locals  map[common.Hash]*types.Transaction
	remotes map[common.Hash]*types.Transaction
}

// newTxLookup returns a new txLookup structure.
func newTxLookup() *txLookup {
	return &txLookup{
		locals:  make(map[common.Hash]*types.Transaction),
		remotes: make(map[common.Hash]*types.Transaction),
	}
}

// Range calls f on each key and value present in the map. The callback passed
// should return the indicator whether the iteration needs to be continued.
// Callers need to specify which set (or both) to be iterated.
func (t *txLookup) Range(f func(hash common.Hash, tx *types.Transaction, local bool) bool, local bool, remote bool) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	if local {
		for key, value := range t.locals {
			if !f(key, value, true) {
				return
			}
		}
	}
	if remote {
		for key, value := range t.remotes {
			if !f(key, value, false) {
				return
			}
		}
	}
}

// Get returns a transaction if it exists in the lookup, or nil if not found.
func (t *txLookup) Get(hash common.Hash) *types.Transaction {
	t.lock.RLock()
	defer t.lock.RUnlock()

	if tx := t.locals[hash]; tx != nil {
		return tx
	}
	return t.remotes[hash]
}

// GetLocal returns a transaction if it exists in the lookup, or nil if not found.
func (t *txLookup) GetLocal(hash common.Hash) *types.Transaction {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.locals[hash]
}

// GetRemote returns a transaction if it exists in the lookup, or nil if not found.
func (t *txLookup) GetRemote(hash common.Hash) *types.Transaction {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.remotes[hash]
}

// Count returns the current number of transactions in the lookup.
func (t *txLookup) Count() int {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return len(t.locals) + len(t.remotes)
}

// LocalCount returns the current number of local transactions in the lookup.
func (t *txLookup) LocalCount() int {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return len(t.locals)
}

// RemoteCount returns the current number of remote transactions in the lookup.
func (t *txLookup) RemoteCount() int {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return len(t.remotes)
}

// Slots returns the current number of slots used in the lookup.
func (t *txLookup) Slots() int {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.slots
}

// Add adds a transaction to the lookup.
func (t *txLookup) Add(tx *types.Transaction, local bool) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.slots += numSlots(tx)
	slotsGauge.Set(float64(t.slots))

	if local {
		t.locals[tx.Hash()] = tx
	} else {
		t.remotes[tx.Hash()] = tx
	}
}

// Remove removes a transaction from the lookup.
func (t *txLookup) Remove(hash common.Hash, logger *log.Logger) {
	t.lock.Lock()
	defer t.lock.Unlock()

	tx, ok := t.locals[hash]
	if !ok {
		tx, ok = t.remotes[hash]
	}
	if !ok {
		logger.WithField("hash", hash).Error("No transaction found to be deleted")
		return
	}
	t.slots -= numSlots(tx)
	slotsGauge.Set(float64(t.slots))

	delete(t.locals, hash)
	delete(t.remotes, hash)
}

// RemoteToLocals migrates the transactions belongs to the given locals to locals
// set. The assumption is held the locals set is thread-safe to be used.
func (t *txLookup) RemoteToLocals(locals *accountSet) int {
	t.lock.Lock()
	defer t.lock.Unlock()

	var migrated int
	for hash, tx := range t.remotes {
		if locals.containsTx(tx) {
			t.locals[hash] = tx
			delete(t.remotes, hash)
			migrated += 1
		}
	}
	return migrated
}

// RemotesBelowTip finds all remote transactions below the given tip threshold.
func (t *txLookup) RemotesBelowTip(threshold *big.Int) types.Transactions {
	found := make(types.Transactions, 0, 128)
	t.Range(func(hash common.Hash, tx *types.Transaction, local bool) bool {
		if tx.CompareFee(threshold) < 0 {
			found = append(found, tx)
		}
		return true
	}, false, true) // Only iterate remotes
	return found
}

// numSlots calculates the number of slots needed for a single transaction.
func numSlots(tx *types.Transaction) int {
	return int((tx.Size() + txSlotSize - 1) / txSlotSize)
}

func ErrGasLimit(txGas uint64, limit uint64) error {
	return fmt.Errorf(errGasLimit.Error()+", tx: %d, current limit: %d", txGas, limit)
}
