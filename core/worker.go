package core

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mapset "github.com/deckarep/golang-set"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru/v2"
	expireLru "github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	// resultQueueSize is the size of channel listening to sealing result.
	resultQueueSize = 10

	// minRecommitInterval is the minimal time interval to recreate the sealing block with
	// any newly arrived transactions.
	minRecommitInterval = 1 * time.Second

	// pendingBlockBodyLimit is maximum number of pending block bodies to be kept in cache.
	pendingBlockBodyLimit = 100

	// c_headerPrintsExpiryTime is how long a header hash is kept in the cache, so that currentInfo
	// is not printed on a Proc frequency
	c_headerPrintsExpiryTime = 2 * time.Minute

	// c_chainSideChanSize is the size of the channel listening to uncle events
	chainSideChanSize = 10

	c_uncleCacheSize = 100
)

// environment is the worker's current environment and holds all
// information of the sealing block generation.
type environment struct {
	signer types.Signer

	state                   *state.StateDB // apply state changes here
	batch                   ethdb.Batch    // batch to write UTXO and coinbase lockup changes (in memory)
	ancestors               mapset.Set     // ancestor set (used for checking uncle parent validity)
	family                  mapset.Set     // family set (used for checking uncle invalidity)
	tcount                  int            // tx count in cycle
	gasPool                 *types.GasPool // available gas used to pack transactions
	primaryCoinbase         common.Address
	etxRLimit               uint64 // Remaining number of cross-region ETXs that can be included
	etxPLimit               uint64 // Remaining number of cross-prime ETXs that can be included
	parentOrder             *int
	wo                      *types.WorkObject
	gasUsedAfterTransaction []uint64
	txs                     []*types.Transaction
	etxs                    []*types.Transaction
	utxoFees                *big.Int
	quaiFees                *big.Int
	subManifest             types.BlockManifest
	receipts                []*types.Receipt
	uncleMu                 sync.RWMutex
	uncles                  map[common.Hash]*types.WorkObjectHeader
	utxosCreate             []common.Hash
	utxosDelete             []common.Hash
	coinbaseLockupsCreated  map[string]common.Hash
	coinbaseLockupsDeleted  map[string]common.Hash
	coinbaseRotatedEpochs   map[string]struct{}
	parentStateSize         *big.Int
	quaiCoinbaseEtxs        map[[21]byte]*big.Int
	deletedUtxos            map[common.Hash]struct{}
	qiGasScalingFactor      float64
	utxoSetSize             uint64
	coinbaseLatestEpoch     uint32
}

// unclelist returns the contained uncles as the list format.
func (env *environment) unclelist() []*types.WorkObjectHeader {
	env.uncleMu.RLock()
	defer env.uncleMu.RUnlock()
	var uncles []*types.WorkObjectHeader
	for _, uncle := range env.uncles {
		uncles = append(uncles, uncle)
	}
	return uncles
}

// Config is the configuration parameters of mining.
type Config struct {
	QuaiCoinbase          common.Address  `toml:",omitempty"` // Public address for Quai mining rewards
	QiCoinbase            common.Address  `toml:",omitempty"` // Public address for Qi mining rewards
	CoinbaseLockup        uint8           `toml:",omitempty"` // Lockup byte the determines number of blocks before mining rewards can be spent
	LockupContractAddress *common.Address `toml:",omitempty"` // Address of the lockup contract to use for coinbase rewards
	MinerPreference       float64         // Determines the relative preference of Quai or Qi [0, 1] respectively
	Notify                []string        `toml:",omitempty"` // HTTP URL list to be notified of new work packages (only useful in ethash).
	NotifyFull            bool            `toml:",omitempty"` // Notify with pending block headers instead of work packages
	ExtraData             hexutil.Bytes   `toml:",omitempty"` // Block extra data set by the miner
	GasFloor              uint64          // Target gas floor for mined blocks.
	GasCeil               uint64          // Target gas ceiling for mined blocks.
	GasPrice              *big.Int        // Minimum gas price for mining a transaction
	Recommit              time.Duration   // The time interval for miner to re-create mining work.
	Noverify              bool            // Disable remote mining solution verification(only useful in ethash).
	WorkShareMining       bool            // Whether to mine work shares from raw transactions.
	WorkShareThreshold    int             // WorkShareThreshold is the minimum fraction of a share that this node will accept to mine a transaction.
	Endpoints             []string        // Holds RPC endpoints to send minimally mined transactions to for further mining/propagation.
}

type transactionOrderingInfo struct {
	txs                     []*types.Transaction
	gasUsedAfterTransaction []uint64
	block                   *types.WorkObject
}

// worker is the main object which takes care of submitting new work to consensus engine
// and gathering the sealing result.
type worker struct {
	config       *Config
	chainConfig  *params.ChainConfig
	engine       consensus.Engine
	hc           *HeaderChain
	txPool       *TxPool
	ephemeralKey *secp256k1.PrivateKey
	// Feeds
	pendingLogsFeed   event.Feed
	pendingHeaderFeed event.Feed

	// Subscriptions
	chainSideCh  chan ChainSideEvent
	chainSideSub event.Subscription

	// Channels
	resultCh                       chan *types.WorkObject
	exitCh                         chan struct{}
	resubmitIntervalCh             chan time.Duration
	fillTransactionsRollingAverage *RollingAverage

	asyncPhFeed event.Feed // asyncPhFeed sends an event after each state root update
	scope       event.SubscriptionScope

	wg sync.WaitGroup

	uncles  *lru.Cache[common.Hash, types.WorkObjectHeader]
	uncleMu sync.RWMutex

	mu                    sync.RWMutex // The lock used to protect the coinbase and extra fields
	quaiCoinbase          common.Address
	qiCoinbase            common.Address
	primaryCoinbase       common.Address
	lockupContractAddress *common.Address // Address of the lockup contract to use for coinbase rewards
	coinbaseLockup        uint8
	minerPreference       float64
	extra                 []byte

	workerDb ethdb.Database

	pendingBlockBody *lru.Cache[common.Hash, types.WorkObject]

	snapshotMu    sync.RWMutex // The lock used to protect the snapshots below
	snapshotBlock *types.WorkObject

	headerPrints *expireLru.LRU[common.Hash, interface{}]

	// atomic status counters
	running int32 // The indicator whether the consensus engine is running or not.

	// noempty is the flag used to control whether the feature of pre-seal empty
	// block is enabled. The default value is false(pre-seal is enabled by default).
	// But in some special scenario the consensus engine will seal blocks instantaneously,
	// in this case this feature will add all empty blocks into canonical chain
	// non-stop and no real transaction will be included.
	noempty uint32

	// External functions
	isLocalBlock func(header *types.WorkObject) bool // Function used to determine whether the specified block is mined by local miner.

	logger *log.Logger
}

type RollingAverage struct {
	durations  []time.Duration
	sum        time.Duration
	windowSize int
	mu         sync.Mutex
}

func (ra *RollingAverage) Add(d time.Duration) {
	ra.mu.Lock()
	defer ra.mu.Unlock()

	if len(ra.durations) == ra.windowSize {
		// Remove the oldest duration from the sum
		ra.sum -= ra.durations[0]
		ra.durations = ra.durations[1:]
	}
	ra.durations = append(ra.durations, d)
	ra.sum += d
}

func (ra *RollingAverage) Average() time.Duration {
	ra.mu.Lock()
	defer ra.mu.Unlock()

	if len(ra.durations) == 0 {
		return 0
	}
	return ra.sum / time.Duration(len(ra.durations))
}

func newWorker(config *Config, chainConfig *params.ChainConfig, db ethdb.Database, engine consensus.Engine, headerchain *HeaderChain, txPool *TxPool, isLocalBlock func(header *types.WorkObject) bool, init bool, processingState bool, logger *log.Logger) *worker {
	worker := &worker{
		config:                         config,
		chainConfig:                    chainConfig,
		engine:                         engine,
		hc:                             headerchain,
		txPool:                         txPool,
		quaiCoinbase:                   config.QuaiCoinbase,
		qiCoinbase:                     config.QiCoinbase,
		lockupContractAddress:          config.LockupContractAddress,
		isLocalBlock:                   isLocalBlock,
		workerDb:                       db,
		chainSideCh:                    make(chan ChainSideEvent, chainSideChanSize),
		resultCh:                       make(chan *types.WorkObject, resultQueueSize),
		exitCh:                         make(chan struct{}),
		resubmitIntervalCh:             make(chan time.Duration),
		fillTransactionsRollingAverage: &RollingAverage{windowSize: 100},
		logger:                         logger,
		coinbaseLockup:                 config.CoinbaseLockup,
		minerPreference:                config.MinerPreference,
	}
	if worker.coinbaseLockup > uint8(len(params.LockupByteToBlockDepth))-1 {
		logger.Errorf("Invalid coinbase lockup value %d, using default value %d", worker.coinbaseLockup, params.DefaultCoinbaseLockup)
		worker.coinbaseLockup = params.DefaultCoinbaseLockup
	}
	if worker.lockupContractAddress != nil {
		logger.Infof("Using lockup contract address %s", worker.lockupContractAddress.String())
	}
	// initialize a uncle cache
	uncles, _ := lru.New[common.Hash, types.WorkObjectHeader](c_uncleCacheSize)
	worker.uncles = uncles
	// Set the GasFloor of the worker to the minGasLimit
	worker.config.GasFloor = params.MinGasLimit(headerchain.CurrentHeader().NumberU64(common.ZONE_CTX))

	phBodyCache, _ := lru.New[common.Hash, types.WorkObject](pendingBlockBodyLimit)
	worker.pendingBlockBody = phBodyCache

	// Sanitize recommit interval if the user-specified one is too short.
	recommit := worker.config.Recommit
	if recommit < minRecommitInterval {
		logger.WithFields(log.Fields{
			"provided": recommit,
			"updated":  minRecommitInterval,
		}).Warn("Sanitizing miner recommit interval")
		recommit = minRecommitInterval
	}

	headerPrints := expireLru.NewLRU[common.Hash, interface{}](1, nil, c_headerPrintsExpiryTime)
	worker.headerPrints = headerPrints

	nodeCtx := headerchain.NodeCtx()
	if headerchain.ProcessingState() && nodeCtx == common.ZONE_CTX {
		worker.chainSideSub = worker.hc.SubscribeChainSideEvent(worker.chainSideCh)
		worker.wg.Add(1)
		go worker.asyncStateLoop()
	}

	worker.ephemeralKey, _ = secp256k1.GeneratePrivateKey()

	worker.start()

	return worker
}

func (w *worker) pickCoinbases() {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Use the MinerPreference to bias the decision
	if rand.Float64() > w.minerPreference {
		// if MinerPreference < 0.5, bias is towards Quai
		w.primaryCoinbase = w.quaiCoinbase
	} else {
		// if MinerPreference > 0.5, bias is towards Qi
		w.primaryCoinbase = w.qiCoinbase
	}
}

// setPrimaryCoinbase sets the coinbase used to initialize the block primary coinbase field.
func (w *worker) setPrimaryCoinbase(addr common.Address) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.primaryCoinbase = addr
}

func (w *worker) GetPrimaryCoinbase() common.Address {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.primaryCoinbase
}

func (w *worker) SetMinerPreference(preference float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.minerPreference = preference
}

func (w *worker) GetMinerPreference() float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.minerPreference
}

func (w *worker) GetLockupByte() uint8 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.coinbaseLockup
}

func (w *worker) SetLockupByte(lockupByte uint8) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.coinbaseLockup = lockupByte
}

func (w *worker) setGasCeil(ceil uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.config.GasCeil = ceil
}

// setExtra sets the content used to initialize the block extra field.
func (w *worker) setExtra(extra []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.extra = extra
}

// setRecommitInterval updates the interval for miner sealing work recommitting.
func (w *worker) setRecommitInterval(interval time.Duration) {
	select {
	case w.resubmitIntervalCh <- interval:
	case <-w.exitCh:
	}
}

// disablePreseal disables pre-sealing feature
func (w *worker) disablePreseal() {
	atomic.StoreUint32(&w.noempty, 1)
}

// enablePreseal enables pre-sealing feature
func (w *worker) enablePreseal() {
	atomic.StoreUint32(&w.noempty, 0)
}

// pending returns the pending state and corresponding block.
func (w *worker) pending() *types.WorkObject {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return w.snapshotBlock
}

// pendingBlock returns pending block.
func (w *worker) pendingBlock() *types.WorkObject {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return w.snapshotBlock
}

// pendingBlockAndReceipts returns pending block and corresponding receipts.
func (w *worker) pendingBlockAndReceipts() (*types.WorkObject, types.Receipts) {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	// snapshot receipts are not stored in the worker anymore, so pending receipts is nil
	return w.snapshotBlock, nil
}

// start sets the running status as 1 and triggers new work submitting.
func (w *worker) start() {
	atomic.StoreInt32(&w.running, 1)
}

// stop sets the running status as 0.
func (w *worker) stop() {
	if w.hc.ProcessingState() && w.hc.NodeCtx() == common.ZONE_CTX {
		w.chainSideSub.Unsubscribe()
	}
	atomic.StoreInt32(&w.running, 0)
	w.close()
}

// isRunning returns an indicator whether worker is running or not.
func (w *worker) isRunning() bool {
	return atomic.LoadInt32(&w.running) == 1
}

// close terminates all background threads maintained by the worker.
// Note the worker does not support being closed multiple times.
func (w *worker) close() {
	atomic.StoreInt32(&w.running, 0)
	close(w.exitCh)
	w.scope.Close()
	w.wg.Wait()
}

func (w *worker) LoadPendingBlockBody() {
	pendingBlockBodykeys := rawdb.ReadPbBodyKeys(w.workerDb)
	for _, key := range pendingBlockBodykeys {
		if key == types.EmptyBodyHash {
			w.pendingBlockBody.Add(key, types.WorkObject{})
		} else {
			w.pendingBlockBody.Add(key, *rawdb.ReadPbCacheBody(w.workerDb, key))
		}
		// Remove the entry from the database so that body is not accumulated over multiple stops
		rawdb.DeletePbCacheBody(w.workerDb, key)
	}
	rawdb.DeleteAllPbBodyKeys(w.workerDb)
}

// StorePendingBlockBody stores the pending block body cache into the db
func (w *worker) StorePendingBlockBody() {
	// store the pendingBodyCache body
	var pendingBlockBodyKeys common.Hashes
	pendingBlockBody := w.pendingBlockBody
	for _, key := range pendingBlockBody.Keys() {
		if value, exist := pendingBlockBody.Peek(key); exist {
			pendingBlockBodyKeys = append(pendingBlockBodyKeys, key)
			if key != types.EmptyBodyHash {
				rawdb.WritePbCacheBody(w.workerDb, key, &value)
			}
		}
	}
	rawdb.WritePbBodyKeys(w.workerDb, pendingBlockBodyKeys)
}

// asyncStateLoop updates the state root for a block and returns the state udpate in a channel
func (w *worker) asyncStateLoop() {
	defer func() {
		if r := recover(); r != nil {
			w.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	defer w.wg.Done() // decrement the wait group after the close of the loop

	ticker := time.NewTicker(2 * time.Second)
	var prevHeader *types.WorkObject = nil
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			go func() {
				defer func() {
					if r := recover(); r != nil {
						w.logger.WithFields(log.Fields{
							"error":      r,
							"stacktrace": string(debug.Stack()),
						}).Error("Go-Quai Panicked")
					}
				}()
				// Dont run the proc if the current header hasnt changed
				if prevHeader == nil {
					prevHeader = w.hc.CurrentBlock()
				} else {
					if prevHeader.Hash() == w.hc.CurrentBlock().Hash() {
						return
					}
				}
				wo := w.hc.CurrentBlock()
				w.hc.headermu.Lock()
				defer w.hc.headermu.Unlock()
				header, err := w.GeneratePendingHeader(wo, true)
				if err != nil {
					w.logger.WithField("err", err).Error("Error generating pending header")
					return
				}
				// Send the updated pendingHeader in the asyncPhFeed
				w.asyncPhFeed.Send(header)
			}()
		case side := <-w.chainSideCh:
			go func() {
				defer func() {
					if r := recover(); r != nil {
						w.logger.WithFields(log.Fields{
							"error":      r,
							"stacktrace": string(debug.Stack()),
						}).Fatal("Go-Quai Panicked")
					}
				}()
				for _, wo := range side.Blocks {
					// Short circuit for duplicate side blocks
					if exists := w.uncles.Contains(wo.Hash()); exists {
						continue
					}
					w.uncles.Add(wo.Hash(), *wo.WorkObjectHeader())
				}
			}()
		case <-w.exitCh:
			return
		case <-w.chainSideSub.Err():
			return
		}
	}
}

// GeneratePendingBlock generates pending block given a commited block.
func (w *worker) GeneratePendingHeader(block *types.WorkObject, fill bool) (*types.WorkObject, error) {
	nodeCtx := w.hc.NodeCtx()

	if block.Hash() != w.hc.CurrentHeader().Hash() && nodeCtx == common.ZONE_CTX {
		return nil, fmt.Errorf("block hash %v is not same as the current header %v", block.Hash(), w.hc.CurrentHeader().Hash())
	}

	var (
		timestamp int64 // timestamp for each round of sealing.
	)

	start := time.Now()
	if nodeCtx == common.ZONE_CTX && block.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
		w.minerPreference = 0 // only mine Quai until the controller kicks in
	}
	w.pickCoinbases()
	// Set the primary coinbase if the worker is running or it's required
	if w.hc.NodeCtx() == common.ZONE_CTX && w.GetPrimaryCoinbase().Equal(common.Address{}) && w.hc.ProcessingState() {
		w.logger.Warn("Refusing to mine without primaryCoinbase")
		return nil, errors.New("etherbase not found")
	} else if w.GetPrimaryCoinbase().Equal(common.Address{}) {
		w.setPrimaryCoinbase(common.Zero)
	}

	work, err := w.prepareWork(&generateParams{
		timestamp:       uint64(timestamp),
		primaryCoinbase: w.GetPrimaryCoinbase(),
	}, block)
	if err != nil {
		return nil, err
	}

	uncles := work.unclelist()
	if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
		// First etx is always the coinbase etx
		// If workshares are included that are not uncles(?)
		// create a placeholder for the coinbase and create etx for the rest of the workshares

		var primeTerminus *types.WorkObject
		if *work.parentOrder == common.PRIME_CTX {
			primeTerminus = block
		} else {
			primeTerminus = w.hc.GetPrimeTerminus(work.wo)
			if primeTerminus == nil {
				return nil, errors.New("prime terminus not found")
			}
		}

		// Fill pending transactions from the txpool
		w.adjustGasLimit(work, block)
		work.utxoFees = big.NewInt(0)
		work.quaiFees = big.NewInt(0)
		start := time.Now()
		if err := w.fillTransactions(work, primeTerminus, block, fill); err != nil {
			return nil, fmt.Errorf("error generating pending header: %v", err)
		}
		if fill {
			w.fillTransactionsRollingAverage.Add(time.Since(start))
			w.logger.WithFields(log.Fields{
				"count":   len(work.txs),
				"elapsed": common.PrettyDuration(time.Since(start)),
				"average": common.PrettyDuration(w.fillTransactionsRollingAverage.Average()),
			}).Info("Filled and sorted pending transactions")
		}

		if work.parentOrder == nil {
			return nil, fmt.Errorf("parent order not set")
		}

		if (w.GetPrimaryCoinbase() == common.Address{}) {
			return nil, fmt.Errorf("primary coinbase is not set")
		}

		// Since the exchange rates are only calculated on prime blocks, the
		// prime terminus exchange rate is used
		exchangeRate := primeTerminus.ExchangeRate()

		// 50% of the fees goes to the calculation  of the averageFees generated,
		// and this is added to the block rewards
		halfQuaiFees := new(big.Int).Div(work.quaiFees, common.Big2)
		halfQiFees := new(big.Int).Div(work.utxoFees, common.Big2)

		// convert the qi fees to quai
		halfQiFeesInQuai := misc.QiToQuai(work.wo, exchangeRate, work.wo.Difficulty(), halfQiFees)
		totalFeesForCapacitor := new(big.Int).Add(halfQuaiFees, halfQiFeesInQuai)

		expectedAvgFees := w.hc.ComputeAverageTxFees(block, totalFeesForCapacitor)
		work.wo.Header().SetAvgTxFees(expectedAvgFees)

		// Set the total fees collected in this block
		totalQiFeesInQuai := misc.QiToQuai(work.wo, exchangeRate, work.wo.Difficulty(), work.utxoFees)
		expectedTotalFees := new(big.Int).Add(work.quaiFees, totalQiFeesInQuai)
		work.wo.Header().SetTotalFees(expectedTotalFees)
		// The fees from transactions in the block is given, in the block itself
		// go through the last WorkSharesInclusionDepth of blocks
		if work.wo.NumberU64(common.ZONE_CTX) > uint64(params.WorkSharesInclusionDepth) {

			targetBlockNumber := work.wo.NumberU64(common.ZONE_CTX) - uint64(params.WorkSharesInclusionDepth)

			targetBlock := w.hc.GetBlockByNumber(targetBlockNumber)

			totalEntropy := big.NewInt(0)
			powHash, err := w.engine.ComputePowHash(targetBlock.WorkObjectHeader())
			if err != nil {
				return nil, err
			}
			zoneThresholdEntropy := w.engine.IntrinsicLogEntropy(powHash)
			totalEntropy = new(big.Int).Add(totalEntropy, zoneThresholdEntropy)

			// First step is to collect all the workshares and uncles at this targetBlockNumber depth, then
			// compute the total entropy of the block, uncles and workshares at this level
			// unclesAtTargetBlockDepth has all the uncles, workshares that is there at the block height
			var sharesAtTargetBlockDepth []*types.WorkObjectHeader
			var entropyOfSharesAtTargetBlockDepth []*big.Int
			sharesAtTargetBlockDepth = append(sharesAtTargetBlockDepth, targetBlock.WorkObjectHeader())
			entropyOfSharesAtTargetBlockDepth = append(entropyOfSharesAtTargetBlockDepth, zoneThresholdEntropy)

			for i := 0; i <= params.WorkSharesInclusionDepth; i++ {
				blockAtHeight := w.hc.GetBlockByNumber(targetBlockNumber + uint64(i))

				var uncles []*types.WorkObjectHeader
				if i == params.WorkSharesInclusionDepth {
					uncles = work.wo.Uncles()
				} else {
					uncles = blockAtHeight.Uncles()
				}

				for _, uncle := range uncles {
					var uncleEntropy *big.Int
					if uncle.NumberU64() == targetBlockNumber {
						_, err := w.engine.VerifySeal(uncle)
						if err != nil {
							// uncle is a workshare
							powHash, err := w.engine.ComputePowHash(uncle)
							if err != nil {
								return nil, err
							}
							uncleEntropy = new(big.Int).Set(w.engine.IntrinsicLogEntropy(powHash))
							totalEntropy = new(big.Int).Add(totalEntropy, uncleEntropy)
						} else {
							// Add the target weight into the uncles
							target := new(big.Int).Div(common.Big2e256, uncle.Difficulty())
							uncleEntropy = w.engine.IntrinsicLogEntropy(common.BytesToHash(target.Bytes()))
							totalEntropy = new(big.Int).Add(totalEntropy, uncleEntropy)
						}
						sharesAtTargetBlockDepth = append(sharesAtTargetBlockDepth, uncle)
						entropyOfSharesAtTargetBlockDepth = append(entropyOfSharesAtTargetBlockDepth, uncleEntropy)
					}
				}
			}

			if totalEntropy.Cmp(common.Big0) == 0 {
				return nil, errors.New("total entropy of all the shares in the target level cannot be zero")
			}

			// Once the total entropy is calculated, the block reward is split
			// between the blocks, uncles and workshares proportional to the block
			// weight
			// get the reward in quai
			blockRewardAtTargetBlock := misc.CalculateQuaiReward(targetBlock.Difficulty(), exchangeRate)
			// add the fee capacitor value
			blockRewardAtTargetBlock = new(big.Int).Add(blockRewardAtTargetBlock, targetBlock.AvgTxFees())
			// add half the fees generated in the block
			blockRewardAtTargetBlock = new(big.Int).Add(blockRewardAtTargetBlock, new(big.Int).Div(targetBlock.TotalFees(), common.Big2))

			// Add an etx for each workshare for it to be rewarded
			for i, share := range sharesAtTargetBlockDepth {

				shareReward := new(big.Int).Mul(blockRewardAtTargetBlock, entropyOfSharesAtTargetBlockDepth[i])
				shareReward = new(big.Int).Div(shareReward, totalEntropy)

				if shareReward.Cmp(blockRewardAtTargetBlock) > 0 {
					return nil, errors.New("share reward cannot be greater than the total block reward")
				}

				uncleCoinbase := share.PrimaryCoinbase()
				var originHash common.Hash
				if uncleCoinbase.IsInQuaiLedgerScope() {
					originHash = common.SetBlockHashForQuai(block.Hash(), w.hc.NodeLocation())
				} else {
					originHash = common.SetBlockHashForQi(block.Hash(), w.hc.NodeLocation())
					// convert the quai reward value into Qi
					shareReward = new(big.Int).Set(misc.QuaiToQi(targetBlock, exchangeRate, targetBlock.Difficulty(), shareReward))
				}
				work.etxs = append(work.etxs, types.NewTx(&types.ExternalTx{To: &uncleCoinbase, Gas: params.TxGas, Value: shareReward, EtxType: types.CoinbaseType, OriginatingTxHash: originHash, ETXIndex: uint16(len(work.etxs)), Sender: uncleCoinbase, Data: share.Data()}))
			}
		}

	}

	if block.NumberU64(common.ZONE_CTX) < params.TimeToStartTx {
		work.wo.Header().SetGasUsed(0)
	}

	// Create a local environment copy, avoid the data race with snapshot state.
	newWo, err := w.FinalizeAssemble(w.hc, work.wo, block, work.state, work.txs, uncles, work.etxs, work.subManifest, work.receipts, work.utxoSetSize, work.utxosCreate, work.utxosDelete)
	if err != nil {
		return nil, err
	}

	work.wo = newWo

	w.printPendingHeaderInfo(work, newWo, start)
	work.utxosCreate = nil
	work.utxosDelete = nil
	work.coinbaseLockupsCreated = nil
	work.coinbaseLockupsDeleted = nil
	work.coinbaseRotatedEpochs = nil
	return newWo, nil
}

func (w *worker) GetKQuaiAndUpdateBit(parent *types.WorkObject) (*big.Int, uint8, error) {
	// Retrieve the parent state to execute on top and start a prefetcher for
	// the miner to speed block sealing up a bit.
	evmRoot := parent.EVMRoot()
	etxRoot := parent.EtxSetRoot()
	quaiStateSize := parent.QuaiStateSize()
	if w.hc.IsGenesisHash(parent.Hash()) {
		evmRoot = types.EmptyRootHash
		etxRoot = types.EmptyRootHash
		quaiStateSize = big.NewInt(0)
	}
	state, err := w.hc.bc.processor.StateAt(evmRoot, etxRoot, quaiStateSize)
	if err != nil {
		return nil, 0, err
	}

	kQuai, err := state.GetKQuai()
	if err != nil {
		return nil, 0, err
	}

	updateBit, err := state.GetUpdateBit()
	if err != nil {
		return nil, 0, err
	}

	return kQuai, updateBit, nil
}

// printPendingHeaderInfo logs the pending header information
func (w *worker) printPendingHeaderInfo(work *environment, block *types.WorkObject, start time.Time) {
	work.uncleMu.RLock()
	if w.CurrentInfo(block) {
		w.logger.WithFields(log.Fields{
			"number":       block.Number(w.hc.NodeCtx()),
			"parent":       block.ParentHash(w.hc.NodeCtx()),
			"sealhash":     block.SealHash(),
			"uncles":       len(work.uncles),
			"txs":          len(work.txs),
			"outboundEtxs": len(block.OutboundEtxs()),
			"gas":          block.GasUsed(),
			"fees":         totalFees(block, work.receipts),
			"elapsed":      common.PrettyDuration(time.Since(start)),
			"utxoRoot":     block.UTXORoot(),
			"etxSetRoot":   block.EtxSetRoot(),
		}).Info("Commit new sealing work")
	} else {
		w.logger.WithFields(log.Fields{
			"number":       block.Number(w.hc.NodeCtx()),
			"parent":       block.ParentHash(w.hc.NodeCtx()),
			"sealhash":     block.SealHash(),
			"uncles":       len(work.uncles),
			"txs":          len(work.txs),
			"outboundEtxs": len(block.OutboundEtxs()),
			"gas":          block.GasUsed(),
			"fees":         totalFees(block, work.receipts),
			"elapsed":      common.PrettyDuration(time.Since(start)),
			"utxoRoot":     block.UTXORoot(),
			"etxSetRoot":   block.EtxSetRoot(),
		}).Debug("Commit new sealing work")
	}
	work.uncleMu.RUnlock()
}

func (w *worker) eventExitLoop() {
	for {
		select {
		case <-w.exitCh:
			return
		}
	}
}

// makeEnv creates a new environment for the sealing block.
func (w *worker) makeEnv(parent *types.WorkObject, proposedWo *types.WorkObject, primaryCoinbase common.Address) (*environment, error) {
	// Retrieve the parent state to execute on top and start a prefetcher for
	// the miner to speed block sealing up a bit.
	evmRoot := parent.EVMRoot()
	etxRoot := parent.EtxSetRoot()
	quaiStateSize := parent.QuaiStateSize()
	utxoSetSize := rawdb.ReadUTXOSetSize(w.workerDb, parent.Hash())
	if w.hc.IsGenesisHash(parent.Hash()) {
		evmRoot = types.EmptyRootHash
		etxRoot = types.EmptyRootHash
		quaiStateSize = big.NewInt(0)
		utxoSetSize = 0
	}
	state, err := w.hc.bc.processor.StateAt(evmRoot, etxRoot, quaiStateSize)
	if err != nil {
		return nil, err
	}

	etxRLimit := (uint64(len(parent.Transactions())) * params.TxGas) / params.ETXRegionMaxFraction
	if etxRLimit < params.ETXRLimitMin {
		etxRLimit = params.ETXRLimitMin
	}
	etxPLimit := (uint64(len(parent.Transactions())) * params.TxGas) / params.ETXPrimeMaxFraction
	if etxPLimit < params.ETXPLimitMin {
		etxPLimit = params.ETXPLimitMin
	}
	// Note the passed coinbase may be different with header.Coinbase.
	env := &environment{
		signer:                 types.MakeSigner(w.chainConfig, proposedWo.Number(w.hc.NodeCtx())),
		state:                  state,
		batch:                  w.workerDb.NewBatch(),
		primaryCoinbase:        primaryCoinbase,
		ancestors:              mapset.NewSet(),
		family:                 mapset.NewSet(),
		wo:                     proposedWo,
		uncles:                 make(map[common.Hash]*types.WorkObjectHeader),
		etxRLimit:              etxRLimit,
		etxPLimit:              etxPLimit,
		parentStateSize:        quaiStateSize,
		quaiCoinbaseEtxs:       make(map[[21]byte]*big.Int),
		deletedUtxos:           make(map[common.Hash]struct{}),
		qiGasScalingFactor:     math.Log(float64(utxoSetSize)),
		utxoSetSize:            utxoSetSize,
		coinbaseLockupsCreated: make(map[string]common.Hash),
		coinbaseLockupsDeleted: make(map[string]common.Hash),
		coinbaseRotatedEpochs:  make(map[string]struct{}),
	}
	coinbaseLockupEpoch := uint32((proposedWo.NumberU64(common.ZONE_CTX) / params.CoinbaseEpochBlocks) + 1) // zero epoch is an invalid state

	env.coinbaseLatestEpoch = coinbaseLockupEpoch
	env.batch.SetPending(true)
	// Keep track of transactions which return errors so they can be removed
	env.tcount = 0
	return env, nil
}

// commitUncle adds the given block to uncle block set, returns error if failed to add.
func (w *worker) commitUncle(env *environment, uncle *types.WorkObjectHeader) error {
	env.uncleMu.Lock()
	defer env.uncleMu.Unlock()
	hash := uncle.Hash()

	for _, ancestor := range w.hc.GetBlocksFromHash(env.wo.ParentHash(common.ZONE_CTX), params.WorkSharesInclusionDepth) {
		for _, uncle := range ancestor.Uncles() {
			env.family.Add(uncle.Hash())
		}
		env.family.Add(ancestor.Hash())
		env.ancestors.Add(ancestor.Hash())
	}

	if _, exist := env.uncles[hash]; exist {
		return errors.New("uncle not unique")
	}
	var workShare bool
	// If the uncle is a workshare, we should allow siblings
	_, err := w.engine.VerifySeal(uncle)
	if err != nil {
		workShare = true
	}
	_, err = w.hc.WorkShareDistance(env.wo, uncle)
	if err != nil {
		return err
	}
	if uncle.PrimaryCoinbase().IsInQiLedgerScope() && env.wo.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
		return errors.New("workshare coinbase is in Qi, but Qi is disabled")
	}
	if !workShare && (env.wo.ParentHash(w.hc.NodeCtx()) == uncle.ParentHash()) {
		return errors.New("uncle is sibling")
	}
	if !env.ancestors.Contains(uncle.ParentHash()) {
		return errors.New("uncle's parent unknown")
	}
	if env.family.Contains(hash) {
		return errors.New("uncle already included")
	}
	env.uncles[hash] = uncle
	return nil
}

func (w *worker) commitTransaction(env *environment, parent *types.WorkObject, tx *types.Transaction) ([]*types.Log, bool, error) {
	if tx == nil {
		return nil, false, errors.New("nil transaction")
	}

	// If the transaction is not an external transaction, gas price cannot be less than the base fee
	if tx.Type() != types.ExternalTxType && tx.GasPrice().Cmp(env.wo.BaseFee()) < 0 {
		return nil, false, errors.New("gas price fee less than base fee")
	}

	minBaseFee := env.wo.BaseFee()
	if tx.Type() != types.ExternalTxType && tx.GasPrice().Cmp(minBaseFee) < 0 {
		return nil, false, errors.New("gas price fee less than min base fee")
	}
	gasUsedForCoinbase := params.TxGas
	if parent.NumberU64(common.ZONE_CTX) < params.TimeToStartTx {
		gasUsedForCoinbase = uint64(0)
	}
	// coinbase tx
	// 1) is a external tx type
	// 2) do not consume any gas
	// 3) do not produce any receipts/logs
	// 4) etx emit threshold numbers
	if types.IsCoinBaseTx(tx) {
		if tx.To() == nil {
			return nil, false, fmt.Errorf("coinbase tx %x has no recipient", tx.Hash())
		}
		if len(tx.Data()) == 0 {
			return nil, false, fmt.Errorf("coinbase tx %x has no lockup data", tx.Hash())
		}
		if _, err := tx.To().InternalAddress(); err != nil {
			return nil, false, fmt.Errorf("invalid coinbase address %v: %v", tx.To(), err)
		}
		lockupByte := tx.Data()[0]
		if parent.NumberU64(common.ZONE_CTX) >= params.TimeToStartTx {
			if err := env.gasPool.SubGas(params.TxGas); err != nil {
				// etxs are taking more gas
				w.logger.Info("Stopped the etx processing because we crossed the block gas limit processing coinbase etxs")
				return nil, false, nil
			}
		}
		if tx.To().IsInQiLedgerScope() { // Qi coinbase
			if env.wo.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
				// This should never happen, but it's here as a backup
				receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusFailed, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()}
				gasUsed := env.wo.GasUsed()
				gasUsed += gasUsedForCoinbase
				env.wo.Header().SetGasUsed(gasUsed)
				env.txs = append(env.txs, tx)
				env.receipts = append(env.receipts, receipt)
				return receipt.Logs, true, nil
			}

			lockup := new(big.Int).SetUint64(params.LockupByteToBlockDepth[lockupByte])
			if lockup.Uint64() < params.ConversionLockPeriod {
				return nil, false, fmt.Errorf("coinbase lockup period is less than the minimum lockup period of %d blocks", params.ConversionLockPeriod)
			}
			lockup.Add(lockup, env.wo.Number(w.hc.NodeCtx()))
			value := params.CalculateCoinbaseValueWithLockup(tx.Value(), lockupByte, env.wo.NumberU64(common.ZONE_CTX))
			if len(tx.Data()) == 1 {
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
						// the ETX hash is guaranteed to be unique
						utxoHash := types.UTXOHash(tx.Hash(), outputIndex, types.NewUtxoEntry(types.NewTxOut(uint8(denomination), tx.To().Bytes(), lockup)))
						env.utxosCreate = append(env.utxosCreate, utxoHash)
						outputIndex++
					}
				}
				receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusLocked, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()}

				gasUsed := env.wo.GasUsed()
				gasUsed += gasUsedForCoinbase
				env.wo.Header().SetGasUsed(gasUsed)
				env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
				env.txs = append(env.txs, tx)
				env.receipts = append(env.receipts, receipt)
				return receipt.Logs, true, nil
			} else if len(tx.Data()) == common.AddressLength+1 || len(tx.Data()) == common.AddressLength+common.AddressLength+1 {
				contractAddr := common.BytesToAddress(tx.Data()[1:common.AddressLength+1], w.chainConfig.Location)
				internal, err := contractAddr.InternalAndQuaiAddress()
				if err != nil {
					return nil, false, fmt.Errorf("coinbase tx %x has invalid recipient: %w", tx.Hash(), err)
				}
				if env.state.GetCode(internal) == nil {
					// Coinbase data is either too long or too small
					// Coinbase reward is lost
					receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusFailed, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()}
					gasUsed := env.wo.GasUsed()
					gasUsed += gasUsedForCoinbase
					env.wo.Header().SetGasUsed(gasUsed)
					env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
					env.txs = append(env.txs, tx)
					env.receipts = append(env.receipts, receipt)
					return []*types.Log{}, false, nil
				}
				var delegate common.Address
				if len(tx.Data()) == common.AddressLength+common.AddressLength+1 {
					delegate = common.BytesToAddress(tx.Data()[common.AddressLength+1:], w.chainConfig.Location)
				} else {
					delegate = common.Zero
				}
				oldCoinbaseLockupKey, newCoinbaseLockupKey, oldCoinbaseLockupHash, newCoinbaseLockupHash, err := vm.AddNewLock(env.state, env.batch, contractAddr, *tx.To(), delegate, common.OneInternal(w.chainConfig.Location), lockupByte, lockup.Uint64(), env.coinbaseLatestEpoch, value, w.chainConfig.Location, false)
				if err != nil || newCoinbaseLockupHash == nil {
					return nil, false, fmt.Errorf("could not add new lock: %w", err)
				}
				// Store the new lockup key every time
				env.coinbaseLockupsCreated[string(newCoinbaseLockupKey)] = *newCoinbaseLockupHash

				if oldCoinbaseLockupHash != nil {
					// We deleted (updated) the old lockup, write it to deleted list but only the first time
					if _, exists := env.coinbaseLockupsDeleted[string(oldCoinbaseLockupKey)]; !exists {
						if _, exists := env.coinbaseRotatedEpochs[string(newCoinbaseLockupKey)]; !exists {
							env.coinbaseLockupsDeleted[string(oldCoinbaseLockupKey)] = *oldCoinbaseLockupHash
							env.utxosDelete = append(env.utxosDelete, *oldCoinbaseLockupHash)
						}
					}
				} else {
					// If we did not delete, we are rotating the epoch and need to store it
					env.coinbaseRotatedEpochs[string(newCoinbaseLockupKey)] = struct{}{}
				}
				receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusLocked, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()} // todo: consider adding the reward to the receipt in a log

				gasUsed := env.wo.GasUsed()
				gasUsed += gasUsedForCoinbase
				env.wo.Header().SetGasUsed(gasUsed)
				env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
				env.txs = append(env.txs, tx)
				env.receipts = append(env.receipts, receipt)
				return receipt.Logs, true, nil
			} else {
				// Coinbase data is either too long or too small
				// Coinbase reward is lost
				receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusFailed, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()}
				gasUsed := env.wo.GasUsed()
				gasUsed += gasUsedForCoinbase
				env.wo.Header().SetGasUsed(gasUsed)
				env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
				env.txs = append(env.txs, tx)
				env.receipts = append(env.receipts, receipt)
				return []*types.Log{}, false, nil
			}

		} else if tx.To().IsInQuaiLedgerScope() { // Quai coinbase
			_, err := tx.To().InternalAndQuaiAddress()
			if err != nil {
				return nil, false, fmt.Errorf("coinbase tx %x has invalid recipient: %w", tx.Hash(), err)
			}
			var receipt *types.Receipt
			if len(tx.Data()) == 1 {
				// Coinbase has no extra data
				// Coinbase is valid
				receipt = &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusLocked, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()}
				gasUsed := env.wo.GasUsed()
				if parent.NumberU64(common.ZONE_CTX) >= params.TimeToStartTx {
					gasUsed += params.TxGas
					receipt.GasUsed = params.TxGas
				}
				env.wo.Header().SetGasUsed(gasUsed)
				env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
				env.txs = append(env.txs, tx)
				env.receipts = append(env.receipts, receipt)
				return []*types.Log{}, false, nil
			} else if len(tx.Data()) != common.AddressLength+1 && len(tx.Data()) != common.AddressLength+common.AddressLength+1 {
				// Coinbase data is either too long or too small
				// Coinbase reward is lost
				receipt = &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusFailed, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()}
				gasUsed := env.wo.GasUsed()
				gasUsed += gasUsedForCoinbase
				env.wo.Header().SetGasUsed(gasUsed)
				env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
				env.txs = append(env.txs, tx)
				env.receipts = append(env.receipts, receipt)
				return []*types.Log{}, false, nil
			}
			// Create params for uint256 lockup, uint256 balance, address recipient
			lockup := new(big.Int).SetUint64(params.LockupByteToBlockDepth[lockupByte])
			if lockup.Uint64() < params.ConversionLockPeriod {
				return nil, false, fmt.Errorf("coinbase lockup period is less than the minimum lockup period of %d blocks", params.ConversionLockPeriod)
			}
			lockup.Add(lockup, env.wo.Number(w.hc.NodeCtx()))
			contractAddr := common.BytesToAddress(tx.Data()[1:], w.chainConfig.Location)
			internal, err := contractAddr.InternalAndQuaiAddress()
			if err != nil {
				return nil, false, fmt.Errorf("coinbase tx %x has invalid recipient: %w", tx.Hash(), err)
			}

			if env.state.GetCode(internal) == nil {
				// Coinbase data is either too long or too small
				// Coinbase reward is lost
				receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusFailed, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()}
				gasUsed := env.wo.GasUsed()
				gasUsed += gasUsedForCoinbase
				env.wo.Header().SetGasUsed(gasUsed)
				env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
				env.txs = append(env.txs, tx)
				env.receipts = append(env.receipts, receipt)
				return []*types.Log{}, false, nil
			}
			var delegate common.Address
			if len(tx.Data()) == common.AddressLength+common.AddressLength+1 {
				delegate = common.BytesToAddress(tx.Data()[common.AddressLength+1:], w.chainConfig.Location)
			} else {
				delegate = common.Zero
			}
			reward := params.CalculateCoinbaseValueWithLockup(tx.Value(), lockupByte, env.wo.NumberU64(common.ZONE_CTX))
			// Add the lockup owned by the smart contract with the miner as beneficiary
			oldCoinbaseLockupKey, newCoinbaseLockupKey, oldCoinbaseLockupHash, newCoinbaseLockupHash, err := vm.AddNewLock(env.state, env.batch, contractAddr, *tx.To(), delegate, common.OneInternal(w.chainConfig.Location), lockupByte, lockup.Uint64(), env.coinbaseLatestEpoch, reward, w.chainConfig.Location, false)
			if err != nil || newCoinbaseLockupHash == nil {
				return nil, false, fmt.Errorf("could not add new lock: %w", err)
			}
			// Store the new lockup key every time
			env.coinbaseLockupsCreated[string(newCoinbaseLockupKey)] = *newCoinbaseLockupHash

			if oldCoinbaseLockupHash != nil {
				// We deleted (updated) the old lockup, write it to deleted list but only the first time
				if _, exists := env.coinbaseLockupsDeleted[string(oldCoinbaseLockupKey)]; !exists {
					if _, exists := env.coinbaseRotatedEpochs[string(newCoinbaseLockupKey)]; !exists {
						env.coinbaseLockupsDeleted[string(oldCoinbaseLockupKey)] = *oldCoinbaseLockupHash
						env.utxosDelete = append(env.utxosDelete, *oldCoinbaseLockupHash)
					}
				}
			} else {
				// If we did not delete, we are rotating the epoch and need to store it
				env.coinbaseRotatedEpochs[string(newCoinbaseLockupKey)] = struct{}{}
			}
			receipt = &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusLocked, GasUsed: gasUsedForCoinbase, TxHash: tx.Hash()} // todo: consider adding the reward to the receipt in a log

			gasUsed := env.wo.GasUsed()
			gasUsed += gasUsedForCoinbase
			env.wo.Header().SetGasUsed(gasUsed)
			env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
			env.receipts = append(env.receipts, receipt)
			env.txs = append(env.txs, tx)

			return receipt.Logs, true, nil
		}
	}
	if tx.Type() == types.ExternalTxType && tx.To().IsInQiLedgerScope() {
		gasUsed := env.wo.GasUsed()
		if tx.EtxType() == types.CoinbaseLockupType {
			// This is either an unlocked Qi coinbase that was redeemed or Wrapped Qi
			// An unlocked/redeemed Quai coinbase ETX is processed below as a standard Quai ETX
			if tx.To().IsInQiLedgerScope() {
				txGas := tx.Gas()
				denominations := misc.FindMinDenominations(tx.Value())
				total := big.NewInt(0)
				outputIndex := uint16(0)
				success := true
				// Iterate over the denominations in descending order
				for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
					// If the denomination count is zero, skip it
					if denominations[uint8(denomination)] == 0 {
						continue
					}

					for j := uint64(0); j < denominations[uint8(denomination)]; j++ {
						if txGas < params.CallValueTransferGas || outputIndex >= types.MaxOutputIndex {
							// No more gas, the rest of the denominations are lost but the tx is still valid
							success = false
							break
						}
						txGas -= params.CallValueTransferGas
						if err := env.gasPool.SubGas(params.CallValueTransferGas); err != nil {
							return nil, false, err
						}
						gasUsed += params.CallValueTransferGas
						utxo := types.NewUtxoEntry(types.NewTxOut(uint8(denomination), tx.To().Bytes(), big.NewInt(0)))
						env.utxosCreate = append(env.utxosCreate, types.UTXOHash(tx.Hash(), outputIndex, utxo))
						total.Add(total, types.Denominations[uint8(denomination)])
						outputIndex++
					}
				}
				receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusSuccessful, GasUsed: tx.Gas() - txGas, TxHash: tx.Hash(),
					Logs: []*types.Log{{
						Address: *tx.To(),
						Topics:  []common.Hash{types.QuaiToQiConversionTopic},
						Data:    total.Bytes(),
					}},
				}
				if !success {
					receipt.Status = types.ReceiptStatusFailed
					receipt.GasUsed = tx.Gas()
				}
				env.wo.Header().SetGasUsed(gasUsed)
				env.txs = append(env.txs, tx)
				env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
				env.receipts = append(env.receipts, receipt)
				return receipt.Logs, true, nil
			}
		}
		if tx.ETXSender().Location().Equal(*tx.To().Location()) { // Quai->Qi conversion
			if env.wo.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
				receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusFailed, GasUsed: 0, TxHash: tx.Hash()}
				env.wo.Header().SetGasUsed(gasUsed)
				env.txs = append(env.txs, tx)
				env.receipts = append(env.receipts, receipt)
				return []*types.Log{}, false, nil
			}
			txGas := tx.Gas()
			var lockup *big.Int
			lockup = new(big.Int).SetUint64(params.ConversionLockPeriod)
			lock := new(big.Int).Add(env.wo.Number(w.hc.NodeCtx()), lockup)
			if env.parentOrder == nil {
				return nil, false, errors.New("parent order not set")
			}
			var primeTerminus *types.WorkObject
			if *env.parentOrder == common.PRIME_CTX {
				primeTerminus = parent
			} else {
				primeTerminus = w.hc.GetPrimeTerminus(env.wo)
				if primeTerminus == nil {
					return nil, false, errors.New("prime terminus not found")
				}
			}
			if txGas < params.TxGas {
				receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusFailed, GasUsed: txGas, TxHash: tx.Hash()}
				gasUsed += txGas
				env.wo.Header().SetGasUsed(gasUsed)
				env.txs = append(env.txs, tx)
				env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
				env.receipts = append(env.receipts, receipt)
				return []*types.Log{}, false, nil
			}
			txGas -= params.TxGas
			if err := env.gasPool.SubGas(params.TxGas); err != nil {
				return nil, false, err
			}
			gasUsed += params.TxGas
			denominations := misc.FindMinDenominations(tx.Value())
			outputIndex := uint16(0)
			total := big.NewInt(0)
			success := true
			// Iterate over the denominations in descending order
			for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
				// If the denomination count is zero, skip it
				if denominations[uint8(denomination)] == 0 {
					continue
				}
				for j := uint64(0); j < denominations[uint8(denomination)]; j++ {
					if txGas < params.CallValueTransferGas || outputIndex >= types.MaxOutputIndex {
						// No more gas, the rest of the denominations are lost but the tx is still valid
						success = false
						break
					}
					txGas -= params.CallValueTransferGas
					if err := env.gasPool.SubGas(params.CallValueTransferGas); err != nil {
						return nil, false, err
					}
					gasUsed += params.CallValueTransferGas
					// the ETX hash is guaranteed to be unique
					utxoHash := types.UTXOHash(tx.Hash(), outputIndex, types.NewUtxoEntry(types.NewTxOut(uint8(denomination), tx.To().Bytes(), lock)))
					env.utxosCreate = append(env.utxosCreate, utxoHash)
					total.Add(total, types.Denominations[uint8(denomination)])
					outputIndex++
				}
			}
			receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusLocked, GasUsed: tx.Gas() - txGas, TxHash: tx.Hash(),
				Logs: []*types.Log{{
					Address: *tx.To(),
					Topics:  []common.Hash{types.QuaiToQiConversionTopic},
					Data:    total.Bytes(),
				}},
			}
			if !success {
				receipt.Status = types.ReceiptStatusFailed
				receipt.GasUsed = tx.Gas()
			}
			env.wo.Header().SetGasUsed(gasUsed)
			env.txs = append(env.txs, tx)
			env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
			env.receipts = append(env.receipts, receipt)
			return receipt.Logs, true, nil
		} else {
			// This Qi ETX should cost more gas
			if err := env.gasPool.SubGas(params.CallValueTransferGas); err != nil {
				return nil, false, err
			}
			utxoHash := types.UTXOHash(tx.OriginatingTxHash(), tx.ETXIndex(), types.NewUtxoEntry(types.NewTxOut(uint8(tx.Value().Uint64()), tx.To().Bytes(), common.Big0)))
			env.utxosCreate = append(env.utxosCreate, utxoHash)
			gasUsed += params.CallValueTransferGas

			env.wo.Header().SetGasUsed(gasUsed)
			receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusSuccessful, GasUsed: params.CallValueTransferGas, TxHash: tx.Hash()}
			env.receipts = append(env.receipts, receipt)
			env.txs = append(env.txs, tx)
			env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
			return []*types.Log{}, false, nil
		}

	} else if tx.Type() == types.ExternalTxType && types.IsConversionTx(tx) && tx.To().IsInQuaiLedgerScope() { // Qi->Quai Conversion
		receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusLocked, GasUsed: params.QiToQuaiConversionGas, TxHash: tx.Hash()}
		gasUsed := env.wo.GasUsed() + params.QiToQuaiConversionGas
		env.wo.Header().SetGasUsed(gasUsed)
		env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
		env.txs = append(env.txs, tx)
		env.receipts = append(env.receipts, receipt)
		return []*types.Log{}, false, nil // The conversion is locked and will be redeemed later
	} else if tx.Type() == types.ExternalTxType && tx.EtxType() == types.WrappingQiType && tx.To().IsInQuaiLedgerScope() { // Qi wrapping ETX
		if len(tx.Data()) != common.AddressLength {
			return nil, false, fmt.Errorf("wrapping Qi ETX %x has invalid data length", tx.Hash())
		}
		if tx.To() == nil {
			return nil, false, fmt.Errorf("wrapping Qi ETX %x has no recipient", tx.Hash())
		}
		ownerContractAddr := common.BytesToAddress(tx.Data(), w.chainConfig.Location)
		if err := vm.WrapQi(env.state, ownerContractAddr, *tx.To(), common.OneInternal(w.chainConfig.Location), tx.Value(), w.chainConfig.Location); err != nil {
			return nil, false, fmt.Errorf("could not wrap Qi: %w", err)
		}
		receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusSuccessful, GasUsed: params.QiToQuaiConversionGas, TxHash: tx.Hash()}
		gasUsed := env.wo.GasUsed() + params.QiToQuaiConversionGas
		env.wo.Header().SetGasUsed(gasUsed)
		env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
		env.txs = append(env.txs, tx)
		env.receipts = append(env.receipts, receipt)
		return []*types.Log{}, false, nil
	}
	snap := env.state.Snapshot()
	// retrieve the gas used int and pass in the reference to the ApplyTransaction
	gasUsed := env.wo.GasUsed()
	stateUsed := env.wo.StateUsed()
	receipt, quaiFees, err := ApplyTransaction(w.chainConfig, parent, *env.parentOrder, w.hc, &env.primaryCoinbase, env.gasPool, env.state, env.wo, tx, &gasUsed, &stateUsed, *w.hc.bc.processor.GetVMConfig(), &env.etxRLimit, &env.etxPLimit, env.batch, w.logger)
	if err != nil {
		w.logger.WithFields(log.Fields{
			"err":     err,
			"tx":      tx.Hash().Hex(),
			"block":   env.wo.Number(w.hc.NodeCtx()),
			"gasUsed": gasUsed,
		}).Debug("Error playing transaction in worker")
		env.state.RevertToSnapshot(snap)
		return nil, false, err
	}
	if receipt.Status == types.ReceiptStatusSuccessful {
		env.etxs = append(env.etxs, receipt.OutboundEtxs...)
		for _, hash := range receipt.CoinbaseLockupDeletedHashes {
			env.utxosDelete = append(env.utxosDelete, *hash)
		}
	}
	// once the gasUsed pointer is updated in the ApplyTransaction it has to be set back to the env.Header.GasUsed
	// This extra step is needed because previously the GasUsed was a public method and direct update of the value
	// was possible.
	env.wo.Header().SetGasUsed(gasUsed)
	env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)
	env.wo.Header().SetStateUsed(stateUsed)
	env.txs = append(env.txs, tx)
	env.quaiFees = new(big.Int).Add(env.quaiFees, quaiFees)
	env.receipts = append(env.receipts, receipt)
	return receipt.Logs, true, nil
}

var qiTxErrs uint64

func (w *worker) commitTransactions(env *environment, primeTerminus *types.WorkObject, parent *types.WorkObject, txs *types.TransactionsByPriceAndNonce) error {
	qiTxsToRemove := make([]*common.Hash, 0)
	gasLimit := env.wo.GasLimit()
	if env.gasPool == nil {
		env.gasPool = new(types.GasPool).AddGas(gasLimit)
	}
	var coalescedLogs []*types.Log
	minEtxGas := gasLimit / params.MinimumEtxGasDivisor
	etxCount := 0
	for {
		etxCount = 0
		for _, tx := range env.txs {
			if tx.Type() == types.ExternalTxType {
				etxCount++
			}
		}
		if parent.NumberU64(common.ZONE_CTX) < params.TimeToStartTx && etxCount > params.MinEtxCount {
			break
		}
		// Add ETXs until minimum gas is used
		if parent.NumberU64(common.ZONE_CTX) >= params.TimeToStartTx && env.wo.GasUsed() >= minEtxGas {
			// included etxs more than min etx gas
			break
		}
		if env.wo.GasUsed() > minEtxGas*params.MaximumEtxGasMultiplier { // sanity check, this should never happen
			w.logger.WithField("Gas Used", env.wo.GasUsed()).Error("Block uses more gas than maximum ETX gas")
			return fmt.Errorf("block uses more gas than maximum ETX gas")
		}
		etx, err := env.state.PopETX()
		if err != nil {
			w.logger.WithField("err", err).Error("Failed to read ETX")
			return fmt.Errorf("failed to read ETX: %w", err)
		}
		if etx == nil {
			break
		}
		env.state.Prepare(etx.Hash(), env.tcount)

		logs, hasLog, err := w.commitTransaction(env, parent, etx)
		if err == nil && hasLog {
			coalescedLogs = append(coalescedLogs, logs...)
			env.tcount++
		} else if err == nil && !hasLog {
			env.tcount++
		} else {
			w.logger.WithField("err", err).Error("Failed to commit an etx")
		}
	}
	firstQiTx := true
	for {
		// If we don't have enough gas for any further transactions then we're done
		if env.gasPool.Gas() < params.TxGas {
			w.logger.WithFields(log.Fields{
				"have": env.gasPool,
				"want": params.TxGas,
			}).Trace("Not enough gas for further transactions")
			break
		}
		// Retrieve the next transaction and abort if all done
		tx := txs.Peek()
		if tx == nil {
			break
		}
		if tx.Type() == types.QiTxType {
			txGas := types.CalculateBlockQiTxGas(tx, env.qiGasScalingFactor, w.hc.NodeLocation())
			if txGas > gasLimit {
				// This QiTx uses too much gas to include in the block
				txs.PopNoSort()
				hash := tx.Hash()
				qiTxsToRemove = append(qiTxsToRemove, &hash)
				continue
			}
			if err := w.processQiTx(tx, env, primeTerminus, parent, firstQiTx); err != nil {
				if strings.Contains(err.Error(), "emits too many") || strings.Contains(err.Error(), "double spends") || strings.Contains(err.Error(), "combine smaller denominations") || strings.Contains(err.Error(), "uses too much gas") || errors.Is(err, types.ErrGasLimitReached) {
					// This is not an invalid tx, our block is just full of ETXs
					// Alternatively, a tx double spends a cached deleted UTXO, likely replaced-by-fee
					txs.PopNoSort()
					continue
				}
				hash := tx.Hash()
				qiTxErrs++
				if qiTxErrs%1000 == 0 {
					w.logger.WithFields(log.Fields{
						"err": err,
						"tx":  hash.Hex(),
					}).Error("Error processing QiTx")
				} else {
					w.logger.WithFields(log.Fields{
						"err": err,
						"tx":  hash.Hex(),
					}).Debug("Error processing QiTx")
				}

				// It's unlikely that this transaction will be valid in the future so remove it asynchronously
				qiTxsToRemove = append(qiTxsToRemove, &hash)
			}
			firstQiTx = false
			txs.PopNoSort()
			continue
		}
		// Error may be ignored here. The error has already been checked
		// during transaction acceptance is the transaction pool.
		//
		// We use the signer regardless of the current hf.
		from, err := types.Sender(env.signer, tx)
		if err != nil {
			w.logger.WithField("err", err).Error("Error calculating the Sender in commitTransactions")
			continue
		}
		// Start executing the transaction
		env.state.Prepare(tx.Hash(), env.tcount)

		logs, hasLog, err := w.commitTransaction(env, parent, tx)
		switch {
		case errors.Is(err, types.ErrGasLimitReached):
			// Pop the current out-of-gas transaction without shifting in the next from the account
			w.logger.WithField("sender", from).Trace("Gas limit exceeded for current block")
			txs.PopNoSort()

		case errors.Is(err, ErrEtxLimitReached):
			// Pop the current transaction without shifting in the next from the account
			w.logger.WithField("sender", from).Trace("Etx limit exceeded for current block")
			txs.PopNoSort()

		case errors.Is(err, ErrNonceTooLow):
			// New head notification data race between the transaction pool and miner, shift
			w.logger.WithFields(log.Fields{
				"sender": from,
				"nonce":  tx.Nonce(),
			}).Trace("Skipping transaction with low nonce")
			txs.Shift(from.Bytes20(), false)

		case errors.Is(err, ErrNonceTooHigh):
			// Reorg notification data race between the transaction pool and miner, skip account =
			w.logger.WithFields(log.Fields{
				"sender": from,
				"nonce":  tx.Nonce(),
			}).Trace("Skipping account with high nonce")
			txs.PopNoSort()

		case errors.Is(err, nil):
			// Everything ok, collect the logs and shift in the next transaction from the same account
			if hasLog {
				coalescedLogs = append(coalescedLogs, logs...)
			}
			env.tcount++
			txs.PopNoSort()

		case errors.Is(err, ErrTxTypeNotSupported):
			// Pop the unsupported transaction without shifting in the next from the account
			w.logger.WithFields(log.Fields{
				"sender": from,
				"type":   tx.Type(),
			}).Error("Skipping unsupported transaction type")
			txs.PopNoSort()

		case strings.Contains(err.Error(), "emits too many cross"): // This is ErrEtxLimitReached with more info
			// Pop the unsupported transaction without shifting in the next from the account
			w.logger.WithFields(log.Fields{
				"sender": from,
				"err":    err,
			}).Trace("Etx limit exceeded for current block")
			txs.PopNoSort()

		default:
			// Strange error, discard the transaction and get the next in line (note, the
			// nonce-too-high clause will prevent us from executing in vain).
			w.logger.WithFields(log.Fields{
				"hash":   tx.Hash(),
				"sender": from,
				"err":    err,
			}).Trace("Transaction failed, account skipped")
			txs.Shift(from.Bytes20(), false)
		}
	}
	if len(qiTxsToRemove) > 0 {
		w.txPool.AsyncRemoveQiTxs(qiTxsToRemove) // non-blocking
	}
	if !w.isRunning() && len(coalescedLogs) > 0 {
		// We don't push the pendingLogsEvent while we are sealing. The reason is that
		// when we are sealing, the worker will regenerate a sealing block every 3 seconds.
		// In order to avoid pushing the repeated pendingLog, we disable the pending log pushing.

		// make a copy, the state caches the logs and these logs get "upgraded" from pending to mined
		// logs by filling in the block hash when the block was mined by the local miner. This can
		// cause a race condition if a log was "upgraded" before the PendingLogsEvent is processed.
		cpy := make([]*types.Log, len(coalescedLogs))
		for i, l := range coalescedLogs {
			cpy[i] = new(types.Log)
			*cpy[i] = *l
		}
		w.pendingLogsFeed.Send(cpy)
	}
	return nil
}

// generateParams wraps various of settings for generating sealing task.
type generateParams struct {
	timestamp       uint64         // The timstamp for sealing task
	forceTime       bool           // Flag whether the given timestamp is immutable or not
	primaryCoinbase common.Address // The fee recipient address for including transaction
}

// prepareWork constructs the sealing task according to the given parameters,
// either based on the last chain head or specified parent. In this function
// the pending transactions are not filled yet, only the empty task returned.
func (w *worker) prepareWork(genParams *generateParams, wo *types.WorkObject) (*environment, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	nodeCtx := w.hc.NodeCtx()

	// Find the parent block for sealing task
	parent := wo
	// Sanity check the timestamp correctness, recap the timestamp
	// to parent+1 if the mutation is allowed.
	timestamp := genParams.timestamp
	if parent.WorkObjectHeader().Time() >= timestamp {
		if genParams.forceTime {
			return nil, fmt.Errorf("invalid timestamp, parent %d given %d", parent.WorkObjectHeader().Time(), timestamp)
		}
		timestamp = parent.WorkObjectHeader().Time() + 1
	}
	// Construct the sealing block header, set the extra field if it's allowed
	num := parent.Number(nodeCtx)
	newWo := types.EmptyWorkObject(nodeCtx)

	// the lockup byte is forced to 0 for the first two months
	if parent.NumberU64(common.ZONE_CTX)+1 < 2*params.BlocksPerMonth {
		newWo.WorkObjectHeader().SetLock(0)
	} else {
		newWo.WorkObjectHeader().SetLock(w.GetLockupByte())
	}

	data := make([]byte, 0, 1)
	data = append(data, w.GetLockupByte())
	if w.lockupContractAddress != nil {
		data = append(data, w.lockupContractAddress.Bytes()...)
	}
	newWo.WorkObjectHeader().SetData(data)
	newWo.SetParentHash(wo.Hash(), nodeCtx)
	if w.hc.IsGenesisHash(parent.Hash()) {
		newWo.SetNumber(big.NewInt(1), nodeCtx)
	} else {
		newWo.SetNumber(big.NewInt(int64(num.Uint64())+1), nodeCtx)
	}
	newWo.WorkObjectHeader().SetTime(timestamp)
	newWo.WorkObjectHeader().SetLocation(w.hc.NodeLocation())

	// Only calculate entropy if the parent is not the genesis block
	_, order, err := w.CalcOrder(parent)
	if err != nil {
		return nil, err
	}
	if !w.hc.IsGenesisHash(parent.Hash()) {
		// Set the parent delta entropy prior to sending to sub
		if nodeCtx != common.PRIME_CTX {
			if order < nodeCtx {
				newWo.Header().SetParentDeltaEntropy(big.NewInt(0), nodeCtx)
			} else {
				newWo.Header().SetParentDeltaEntropy(w.engine.DeltaLogEntropy(w.hc, parent), nodeCtx)
			}
		}
		newWo.Header().SetParentEntropy(w.engine.TotalLogEntropy(w.hc, parent), nodeCtx)
	} else {
		newWo.Header().SetParentEntropy(big.NewInt(0), nodeCtx)
		newWo.Header().SetParentDeltaEntropy(big.NewInt(0), nodeCtx)
	}

	// Only calculate entropy if the parent is not the genesis block
	if !w.hc.IsGenesisHash(parent.Hash()) {
		// Set the parent delta entropy prior to sending to sub
		if nodeCtx != common.PRIME_CTX {
			if order < nodeCtx {
				newWo.Header().SetParentUncledDeltaEntropy(big.NewInt(0), nodeCtx)
			} else {
				newWo.Header().SetParentUncledDeltaEntropy(w.engine.UncledDeltaLogEntropy(w.hc, parent), nodeCtx)
			}
		}
	} else {
		newWo.Header().SetParentUncledDeltaEntropy(big.NewInt(0), nodeCtx)
	}

	// calculate the expansion values - except for the etxEligibleSlices, the
	// zones cannot modify any of the other fields its done in prime
	if nodeCtx == common.PRIME_CTX {
		if parent.NumberU64(common.PRIME_CTX) == 0 {
			newWo.Header().SetEfficiencyScore(0)
			newWo.Header().SetThresholdCount(0)
		} else {
			// compute the efficiency score at each prime block
			efficiencyScore, err := w.hc.ComputeEfficiencyScore(parent)
			if err != nil {
				return nil, err
			}
			newWo.Header().SetEfficiencyScore(efficiencyScore)

			// If the threshold count is zero we have not started considering for the
			// expansion
			if parent.Header().ThresholdCount() == 0 {
				if efficiencyScore > params.TREE_EXPANSION_THRESHOLD {
					newWo.Header().SetThresholdCount(parent.Header().ThresholdCount() + 1)
				} else {
					newWo.Header().SetThresholdCount(0)
				}
			} else {
				// If the efficiency score goes below the threshold,  and we still have
				// not triggered the expansion, reset the threshold count or if we go
				// past the tree expansion trigger window we have to reset the
				// threshold count
				if (parent.Header().ThresholdCount() < params.TREE_EXPANSION_TRIGGER_WINDOW && efficiencyScore < params.TREE_EXPANSION_THRESHOLD) ||
					parent.Header().ThresholdCount() == params.TREE_EXPANSION_TRIGGER_WINDOW+params.TREE_EXPANSION_WAIT_COUNT {
					newWo.Header().SetThresholdCount(0)
				} else {
					newWo.Header().SetThresholdCount(parent.Header().ThresholdCount() + 1)
				}
			}

		}
	}

	if nodeCtx == common.PRIME_CTX {
		newWo.Header().SetPrimeStateRoot(types.EmptyRootHash)
	}
	if nodeCtx == common.REGION_CTX {
		newWo.Header().SetRegionStateRoot(types.EmptyRootHash)
	}
	// compute and set the miner difficulty
	if nodeCtx == common.PRIME_CTX {
		minerDifficulty := w.hc.ComputeMinerDifficulty(parent)
		newWo.Header().SetMinerDifficulty(minerDifficulty)
	}

	// If i write to the state of zone-0-0, this approach wont work in any other zone
	if nodeCtx == common.PRIME_CTX {
		// Until the controller has kicked in, dont update any of the fields,
		// and make sure that the fields are unchanged from the default value
		if newWo.NumberU64(common.PRIME_CTX) <= params.ControllerKickInBlock {
			newWo.Header().SetKQuaiDiscount(params.StartingKQuaiDiscount)
			newWo.Header().SetConversionFlowAmount(params.StartingConversionFlowAmount)
			newWo.Header().SetExchangeRate(params.ExchangeRate)
		} else {

			// use the parent exchange rate
			parentExchangeRate := parent.ExchangeRate()

			// There are 6 steps to the exchange rate calculation
			// 1. Convert the amounts using the parent exchange rate or the exchange
			// rate set in the statedb
			// 2. Using the parent K Quai apply a discount to the converted amount
			// 3. Calculate the new exchange rate for this block
			// 4. Convert the amounts using this newly computed exchange rate
			// 5. Apply the K Quai discount using the new exchange rate
			// 6. Apply the quadratic Conversion Flow discount

			///////// Step 1 //////////
			var conversionFlowAmount = parent.ConversionFlowAmount()

			// Read inbound etxs, this needs to be calculated for the pending manifests
			inboundEtxs := rawdb.ReadInboundEtxs(w.workerDb, parent.Hash())

			// Need a variable to calculate the total hash value that is being converted
			conversionAmountInQuai := big.NewInt(0)
			// Calculate the total conversion amount in hash
			for _, etx := range inboundEtxs {
				// If the etx is conversion
				if types.IsConversionTx(etx) {
					value := etx.Value()
					// If to is in Qi, convert the value into Qi
					if etx.To().IsInQiLedgerScope() {
						conversionAmountInQuai = new(big.Int).Add(conversionAmountInQuai, value)
					}
					// If To is in Quai, convert the value into Quai
					if etx.To().IsInQuaiLedgerScope() {
						value = misc.QiToQuai(parent, parentExchangeRate, parent.MinerDifficulty(), value)
						conversionAmountInQuai = new(big.Int).Add(conversionAmountInQuai, value)
					}
				}
			}

			///////// Step 2 /////////
			// Apply K Quai discount before applying the quadratic discount, conversionAmount = (1-kQuaiDiscount)*conversionAmountInHash
			kQuaiDiscount := parent.KQuaiDiscount()
			conversionAmountAfterKQuaiDiscount := new(big.Int).Mul(conversionAmountInQuai, new(big.Int).Sub(big.NewInt(100), kQuaiDiscount))

			originalEtxValues := make([]*big.Int, len(inboundEtxs))

			for i, etx := range inboundEtxs {

				// store the original etx values
				originalEtxValues[i] = new(big.Int).Set(etx.Value())

				// If the etx is conversion
				if types.IsConversionTx(etx) {
					value := etx.Value()
					// If to is in Qi, convert the value into Qi
					if etx.To().IsInQiLedgerScope() {
						value = misc.QuaiToQi(parent, parentExchangeRate, parent.MinerDifficulty(), value)

						// Apply the slip to each conversion
						value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
						value = new(big.Int).Div(value, conversionAmountInQuai)
					}
					// If To is in Quai, convert the value into Quai
					if etx.To().IsInQuaiLedgerScope() {
						value = misc.QiToQuai(parent, parentExchangeRate, parent.MinerDifficulty(), value)

						// Apply the slip to each conversion
						value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
						value = new(big.Int).Div(value, conversionAmountInQuai)
					}
					etx.SetValue(value)
				}
			}

			//////// Step 3 /////////
			minerDifficulty := newWo.MinerDifficulty()

			// save the actual and realized conversion amount
			actualConversionAmountInQuai := new(big.Int).Set(conversionAmountInQuai)
			realizedConversionAmountInQuai := new(big.Int).Set(conversionAmountAfterKQuaiDiscount)

			// convert map to a slice
			updatedTokenChoiceSet, err := CalculateTokenChoicesSet(w.hc, newWo, parent, parentExchangeRate, inboundEtxs, actualConversionAmountInQuai, realizedConversionAmountInQuai, minerDifficulty)
			if err != nil {
				return nil, err
			}
			// ask the zone for this k quai, need to change the hash that is used to request this info
			storedExchangeRate, updateBit, err := w.hc.GetKQuaiAndUpdateBit(parent.ParentHash(common.ZONE_CTX))
			if err != nil {
				return nil, err
			}
			var exchangeRate *big.Int
			// update is paused, use the written exchange rate
			if parent.NumberU64(common.ZONE_CTX) <= params.BlocksPerYear && updateBit == 0 {
				exchangeRate = storedExchangeRate
			} else {
				exchangeRate, err = CalculateBetaFromMiningChoiceAndConversions(w.hc, parent, exchangeRate, updatedTokenChoiceSet)
				if err != nil {
					return nil, err
				}
			}
			// set the calculated exchange rate
			newWo.Header().SetExchangeRate(exchangeRate)

			//////// Step 4 ////////
			newConversionAmountInQuai := big.NewInt(0)
			for i, etx := range inboundEtxs {
				// If the etx is conversion
				if types.IsConversionTx(etx) {
					// Use the original etx values
					value := originalEtxValues[i]
					// If to is in Qi, convert the value into Qi
					if etx.To().IsInQiLedgerScope() {
						newConversionAmountInQuai = new(big.Int).Add(newConversionAmountInQuai, value)
					}
					// If To is in Quai, convert the value into Quai
					if etx.To().IsInQuaiLedgerScope() {
						value = misc.QiToQuai(newWo, exchangeRate, newWo.MinerDifficulty(), value)
						newConversionAmountInQuai = new(big.Int).Add(newConversionAmountInQuai, value)
					}
					etx.SetValue(value)
				}
			}

			//////// Step 5 ///////
			newkQuaiDiscount := w.hc.ComputeKQuaiDiscount(newWo)
			newWo.Header().SetKQuaiDiscount(newkQuaiDiscount)

			newConversionAmountAfterKQuaiDiscount := new(big.Int).Mul(newConversionAmountInQuai, new(big.Int).Sub(big.NewInt(100), newkQuaiDiscount))
			for _, etx := range inboundEtxs {
				// If the etx is conversion
				if types.IsConversionTx(etx) {
					value := etx.Value()
					// If to is in Qi, convert the value into Qi
					if etx.To().IsInQiLedgerScope() {
						// Apply the slip to each conversion
						value = new(big.Int).Mul(value, newConversionAmountAfterKQuaiDiscount)
						value = new(big.Int).Div(value, newConversionAmountInQuai)
					}
					// If To is in Quai, convert the value into Quai
					if etx.To().IsInQuaiLedgerScope() {
						// Apply the slip to each conversion
						value = new(big.Int).Mul(value, newConversionAmountAfterKQuaiDiscount)
						value = new(big.Int).Div(value, newConversionAmountInQuai)
					}
					etx.SetValue(value)
				}
			}

			/////// Step 6 ////////
			discountedConversionAmount := w.hc.ApplyQuadraticDiscount(newConversionAmountAfterKQuaiDiscount, conversionFlowAmount)
			discountedConversionAmountInInt, _ := discountedConversionAmount.Int64()
			for _, etx := range inboundEtxs {
				// If the etx is conversion
				if types.IsConversionTx(etx) {
					value := etx.Value()
					// If to is in Qi, convert the value into Qi
					if etx.To().IsInQiLedgerScope() {
						// Apply the slip to each conversion
						value = new(big.Int).Mul(value, big.NewInt(discountedConversionAmountInInt))
						value = new(big.Int).Div(value, newConversionAmountAfterKQuaiDiscount)
					}
					// If To is in Quai, convert the value into Quai
					if etx.To().IsInQuaiLedgerScope() {
						// Apply the slip to each conversion
						value = new(big.Int).Mul(value, big.NewInt(discountedConversionAmountInInt))
						value = new(big.Int).Div(value, newConversionAmountAfterKQuaiDiscount)
					}
					etx.SetValue(value)
				}
			}
		}
	}

	if nodeCtx == common.ZONE_CTX {
		expansionNumber, err := w.hc.ComputeExpansionNumber(parent)
		if err != nil {
			return nil, err
		}
		newWo.Header().SetExpansionNumber(expansionNumber)
	}

	// Compute the Prime Terminus
	if nodeCtx == common.ZONE_CTX {
		if order == common.PRIME_CTX {
			// Set the prime terminus
			newWo.Header().SetPrimeTerminusHash(parent.Hash())
			newWo.WorkObjectHeader().SetPrimeTerminusNumber(parent.Number(common.PRIME_CTX))
		} else {
			if w.hc.IsGenesisHash(parent.Hash()) {
				newWo.Header().SetPrimeTerminusHash(parent.Hash())
			} else {
				// carry the prime terminus from the parent block
				newWo.Header().SetPrimeTerminusHash(parent.Header().PrimeTerminusHash())
			}
			newWo.WorkObjectHeader().SetPrimeTerminusNumber(parent.WorkObjectHeader().PrimeTerminusNumber())
		}
	} else {
		if w.hc.IsGenesisHash(parent.Hash()) {
			newWo.WorkObjectHeader().SetPrimeTerminusNumber(big.NewInt(0))
		} else {
			newWo.WorkObjectHeader().SetPrimeTerminusNumber(parent.WorkObjectHeader().PrimeTerminusNumber())
		}
	}

	if nodeCtx == common.PRIME_CTX {
		if w.hc.IsGenesisHash(parent.Hash()) {
			newWo.Header().SetEtxEligibleSlices(parent.Header().EtxEligibleSlices())
		} else {
			newWo.Header().SetEtxEligibleSlices(w.hc.UpdateEtxEligibleSlices(parent, parent.Location()))
		}
	}

	if nodeCtx == common.PRIME_CTX {
		interlinkHashes, err := w.hc.CalculateInterlink(parent)
		if err != nil {
			return nil, err
		}
		interlinkRootHash := types.DeriveSha(interlinkHashes, trie.NewStackTrie(nil))
		newWo.Header().SetInterlinkRootHash(interlinkRootHash)
	}

	// Calculate the base fee for the block and set it
	if nodeCtx == common.ZONE_CTX {
		baseFee := w.hc.CalcBaseFee(parent)
		newWo.Header().SetBaseFee(baseFee)
	}

	// Only zone should calculate state
	if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
		newWo.Header().SetExtra(w.extra)
		newWo.Header().SetStateLimit(misc.CalcStateLimit(parent, w.config.GasCeil))
		if w.isRunning() {
			if w.GetPrimaryCoinbase().Equal(common.Zero) {
				w.logger.Error("Refusing to mine without primary coinbase")
				return nil, errors.New("refusing to mine without primary coinbase")
			} else if w.GetPrimaryCoinbase().IsInQiLedgerScope() && newWo.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
				w.logger.Error("Refusing to mine with Qi primary coinbase before controller kick in block")
				return nil, errors.New("refusing to mine with Qi primary coinbase before controller kick in block")
			}
			newWo.WorkObjectHeader().SetPrimaryCoinbase(w.GetPrimaryCoinbase())
		}

		// Get the latest transactions to be broadcasted from the pool
		if len(w.txPool.broadcastSet) > 0 {
			txsDirty := make(types.Transactions, len(w.txPool.broadcastSet))
			copy(txsDirty, w.txPool.broadcastSet)
			txs := make(types.Transactions, 0)
			for _, tx := range txsDirty {
				if tx != nil {
					txs = append(txs, tx)
				}
			}
			hash := types.DeriveSha(txs, trie.NewStackTrie(nil))
			newWo.WorkObjectHeader().SetTxHash(hash)
			w.txPool.broadcastSetCache.Add(hash, txs)
		}

		// Run the consensus preparation with the default or customized consensus engine.
		if err := w.engine.Prepare(w.hc, newWo, wo); err != nil {
			w.logger.WithField("err", err).Error("Failed to prepare header for sealing")
			return nil, err
		}
		proposedWoHeader := types.NewWorkObjectHeader(newWo.Hash(), newWo.ParentHash(nodeCtx), newWo.Number(nodeCtx), newWo.Difficulty(), newWo.WorkObjectHeader().PrimeTerminusNumber(), newWo.TxHash(), newWo.Nonce(), newWo.Lock(), newWo.Time(), newWo.Location(), newWo.PrimaryCoinbase(), newWo.Data())
		proposedWoBody := types.NewWoBody(newWo.Header(), nil, nil, nil, nil, nil)
		proposedWo := types.NewWorkObject(proposedWoHeader, proposedWoBody, nil)
		env, err := w.makeEnv(parent, proposedWo, w.GetPrimaryCoinbase())
		if err != nil {
			w.logger.WithField("err", err).Error("Failed to create sealing context")
			return nil, err
		}
		blockContext, err := NewEVMBlockContext(proposedWo, parent, w.hc, nil)
		if err != nil {
			return nil, err
		}
		vmenv := vm.NewEVM(blockContext, vm.TxContext{}, env.state, w.chainConfig, *w.hc.bc.processor.GetVMConfig(), env.batch)
		if _, err := RedeemLockedQuai(w.hc, proposedWo, parent, env.state, vmenv); err != nil {
			w.logger.WithField("err", err).Error("Failed to redeem locked Quai")
			return nil, err
		}

		env.parentOrder = &order
		// Accumulate the uncles for the sealing work.
		commitUncles := func(wos *lru.Cache[common.Hash, types.WorkObjectHeader]) {
			var uncles []*types.WorkObjectHeader
			keys := wos.Keys()
			for _, hash := range keys {
				if value, exist := wos.Peek(hash); exist {
					uncle := value
					uncles = append(uncles, &uncle)
				}
			}
			// sort the uncles in the decreasing order of entropy
			sort.Slice(uncles, func(i, j int) bool {
				powHash1, _ := w.engine.ComputePowHash(uncles[i])
				powHash2, _ := w.engine.ComputePowHash(uncles[j])
				return new(big.Int).SetBytes(powHash1.Bytes()).Cmp(new(big.Int).SetBytes(powHash2.Bytes())) < 0
			})
			for _, uncle := range uncles {
				env.uncleMu.RLock()
				if len(env.uncles) == params.MaxWorkShareCount {
					env.uncleMu.RUnlock()
					break
				}
				env.uncleMu.RUnlock()
				if err := w.commitUncle(env, uncle); err != nil {
					w.logger.WithFields(log.Fields{
						"hash":   uncle.Hash(),
						"reason": err,
					}).Trace("Possible uncle rejected")
				} else {
					w.logger.WithField("hash", uncle.Hash()).Debug("Committing new uncle to block")
				}
			}
		}
		if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
			w.uncleMu.RLock()
			// Prefer to locally generated uncle
			commitUncles(w.uncles)
			w.uncleMu.RUnlock()
		}
		return env, nil
	} else {
		proposedWoHeader := types.NewWorkObjectHeader(newWo.Hash(), newWo.ParentHash(nodeCtx), newWo.Number(nodeCtx), newWo.Difficulty(), newWo.WorkObjectHeader().PrimeTerminusNumber(), types.EmptyRootHash, newWo.Nonce(), newWo.Lock(), newWo.Time(), newWo.Location(), newWo.PrimaryCoinbase(), newWo.Data())
		proposedWoBody := types.NewWoBody(newWo.Header(), nil, nil, nil, nil, nil)
		proposedWo := types.NewWorkObject(proposedWoHeader, proposedWoBody, nil)
		return &environment{wo: proposedWo}, nil
	}

}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (w *worker) fillTransactions(env *environment, primeTerminus *types.WorkObject, block *types.WorkObject, fill bool) error {
	// Split the pending transactions into locals and remotes
	// Fill the block with all available pending transactions.
	etxs := false
	newInboundEtxs := rawdb.ReadInboundEtxs(w.workerDb, block.Hash())
	if len(newInboundEtxs) > 0 {
		etxs = true
		env.state.PushETXs(newInboundEtxs) // apply the inbound ETXs from the previous block to the ETX set state
	} else {
		oldestIndex, err := env.state.GetOldestIndex()
		if err != nil {
			w.logger.WithField("err", err).Error("Failed to get oldest index")
			return fmt.Errorf("failed to get oldest index: %w", err)
		}
		// Check if there is at least one ETX in the set
		etx, err := env.state.ReadETX(oldestIndex)
		if err != nil {
			w.logger.WithField("err", err).Error("Failed to read ETX")
			return fmt.Errorf("failed to read ETX: %w", err)
		}
		if etx != nil {
			etxs = true
		}
	}
	w.logger.WithFields(log.Fields{
		"outboundEtxs": etxs,
		"fill":         fill,
		"newInbounds":  len(newInboundEtxs),
	}).Info("ETXs and fill")
	if !fill {
		if etxs {
			return w.commitTransactions(env, primeTerminus, block, &types.TransactionsByPriceAndNonce{})
		}
		return nil
	}

	pending, err := w.txPool.TxPoolPending(false)
	if err != nil {
		w.logger.WithField("err", err).Error("Failed to get pending transactions")
		return fmt.Errorf("failed to get pending transactions: %w", err)
	}

	pendingQiTxs := w.txPool.QiPoolPending()

	exchangeRate := primeTerminus.ExchangeRate()

	// Convert these pendingQiTxs fees into Quai fees
	pendingQiTxsWithQuaiFee := make([]*types.TxWithMinerFee, 0)
	for _, tx := range pendingQiTxs {
		// update the fee
		qiFeeInQuai := misc.QiToQuai(env.wo, exchangeRate, env.wo.Difficulty(), tx.MinerFee())
		minerFeeInQuai := new(big.Int).Div(qiFeeInQuai, big.NewInt(int64(types.CalculateBlockQiTxGas(tx.Tx(), env.qiGasScalingFactor, w.hc.NodeLocation()))))
		minBaseFee := block.BaseFee()
		if minerFeeInQuai.Cmp(minBaseFee) < 0 {
			w.logger.Debugf("qi tx has less fee than min base fee: have %s, want %s", minerFeeInQuai, minBaseFee)
			continue
		}
		qiTx, err := types.NewTxWithMinerFee(tx.Tx(), minerFeeInQuai, time.Now())
		if err != nil {
			w.logger.WithField("err", err).Error("Error creating new tx with miner Fee for Qi TX", tx.Tx().Hash())
			continue
		}
		pendingQiTxsWithQuaiFee = append(pendingQiTxsWithQuaiFee, qiTx)
	}

	if len(pending) > 0 || len(pendingQiTxsWithQuaiFee) > 0 || etxs {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, pendingQiTxsWithQuaiFee, pending, true)
		return w.commitTransactions(env, primeTerminus, block, txs)
	}
	return nil
}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (w *worker) adjustGasLimit(env *environment, parent *types.WorkObject) {
	env.wo.Header().SetGasLimit(CalcGasLimit(parent, w.config.GasCeil))
}

// ComputeManifestHash given a header computes the manifest hash for the header
// and stores it in the database
func (w *worker) ComputeManifestHash(header *types.WorkObject) common.Hash {
	manifest := w.hc.CalculateManifest(header)
	manifestHash := types.DeriveSha(manifest, trie.NewStackTrie(nil))
	return manifestHash
}

func (w *worker) FinalizeAssemble(chain consensus.ChainHeaderReader, newWo *types.WorkObject, parent *types.WorkObject, state *state.StateDB, txs []*types.Transaction, uncles []*types.WorkObjectHeader, etxs []*types.Transaction, subManifest types.BlockManifest, receipts []*types.Receipt, parentUtxoSetSize uint64, utxosCreate, utxosDelete []common.Hash) (*types.WorkObject, error) {
	nodeCtx := w.hc.NodeCtx()
	wo, err := w.engine.FinalizeAndAssemble(chain, newWo, state, txs, uncles, etxs, subManifest, receipts, parentUtxoSetSize, utxosCreate, utxosDelete)
	if err != nil {
		return nil, err
	}

	// Once the uncles list is assembled in the block
	if nodeCtx == common.ZONE_CTX {
		wo.Header().SetUncledEntropy(w.engine.UncledLogEntropy(wo))
	}

	manifestHash := w.ComputeManifestHash(parent)

	if w.hc.ProcessingState() {
		wo.Header().SetManifestHash(manifestHash, nodeCtx)
		if nodeCtx == common.ZONE_CTX {
			// Compute and set etx rollup hash
			var etxRollup types.Transactions
			if w.engine.IsDomCoincident(w.hc, parent) {
				etxRollup = parent.OutboundEtxs()
			} else {
				etxRollup, err = w.hc.CollectEtxRollup(parent)
				if err != nil {
					return nil, err
				}
				etxRollup = append(etxRollup, parent.OutboundEtxs()...)
			}
			// Only include the etxs that are going cross Prime in the rollup and the
			// conversion  and the coinbase tx
			filteredEtxsRollup := types.Transactions{}
			for _, etx := range etxRollup {
				to := etx.To().Location()
				coinbase := types.IsCoinBaseTx(etx)
				conversion := types.IsConversionTx(etx)
				if to.Region() != w.hc.NodeLocation().Region() || conversion || coinbase {
					filteredEtxsRollup = append(filteredEtxsRollup, etx)
				}
			}
			etxRollupHash := types.DeriveSha(filteredEtxsRollup, trie.NewStackTrie(nil))
			wo.Header().SetEtxRollupHash(etxRollupHash)
		}
	}

	return wo, nil
}

// AddPendingBlockBody adds an entry in the lru cache for the given pendingBodyKey
// maps it to body.
func (w *worker) AddPendingWorkObjectBody(wo *types.WorkObject) {
	// do not include the tx hash while storing the body
	woHeaderCopy := types.CopyWorkObjectHeader(wo.WorkObjectHeader())
	woHeaderCopy.SetTxHash(common.Hash{})
	w.pendingBlockBody.Add(woHeaderCopy.SealHash(), *wo)
}

// GetPendingBlockBody gets the block body associated with the given header.
func (w *worker) GetPendingBlockBody(woHeader *types.WorkObjectHeader) (*types.WorkObject, error) {
	// do not include the tx hash while storing the body
	woHeaderCopy := types.CopyWorkObjectHeader(woHeader)
	woHeaderCopy.SetTxHash(common.Hash{})
	body, ok := w.pendingBlockBody.Peek(woHeaderCopy.SealHash())
	if ok {
		return &body, nil
	}
	w.logger.WithField("key", woHeader.SealHash()).Warn("pending block body not found for header")
	return nil, errors.New("pending block body not found")
}

func (w *worker) SubscribeAsyncPendingHeader(ch chan *types.WorkObject) event.Subscription {
	return w.scope.Track(w.asyncPhFeed.Subscribe(ch))
}

// copyReceipts makes a deep copy of the given receipts.
func copyReceipts(receipts []*types.Receipt) []*types.Receipt {
	result := make([]*types.Receipt, len(receipts))
	for i, l := range receipts {
		cpy := *l
		result[i] = &cpy
	}
	return result
}

// totalFees computes total consumed miner fees in ETH. Block transactions and receipts have to have the same order.
func totalFees(block *types.WorkObject, receipts []*types.Receipt) *big.Float {
	feesWei := new(big.Int)
	for i, tx := range block.Transactions() {
		var minerFee = big.NewInt(0)
		if tx.Type() != types.QiTxType {
			minerFee = new(big.Int).Set(tx.GasPrice())
		} else {
			minerFee = new(big.Int).Set(block.BaseFee())
		}
		feesWei.Add(feesWei, new(big.Int).Mul(new(big.Int).SetUint64(receipts[i].GasUsed), minerFee))
	}
	return new(big.Float).Quo(new(big.Float).SetInt(feesWei), new(big.Float).SetInt(big.NewInt(params.Ether)))
}

func (w *worker) AddWorkShare(workShare *types.WorkObjectHeader) error {
	// Don't add the workshare into the list if its farther than the worksharefilterdist
	if workShare.NumberU64()+uint64(params.WorkSharesInclusionDepth) < w.hc.CurrentHeader().NumberU64(common.ZONE_CTX) {
		return nil
	}

	// Dont add the workshare if its not valid
	if valid := w.engine.CheckIfValidWorkShare(workShare); valid != types.Valid {
		return errors.New("work share received from peer is not valid")
	}

	w.uncles.ContainsOrAdd(workShare.Hash(), *workShare)
	return nil
}

func (w *worker) CurrentInfo(header *types.WorkObject) bool {
	if w.headerPrints.Contains(header.Hash()) {
		return false
	}

	w.headerPrints.Add(header.Hash(), nil)
	return header.NumberU64(w.hc.NodeCtx())+c_startingPrintLimit > w.hc.CurrentHeader().NumberU64(w.hc.NodeCtx())
}

func (w *worker) processQiTx(tx *types.Transaction, env *environment, primeTerminus *types.WorkObject, parent *types.WorkObject, firstQiTx bool) error {
	location := w.hc.NodeLocation()
	if tx.Type() != types.QiTxType {
		return fmt.Errorf("tx %032x is not a QiTx", tx.Hash())
	}
	if types.IsCoinBaseTx(tx) {
		return fmt.Errorf("tx %032x is a coinbase QiTx", tx.Hash())
	}
	if tx.ChainId().Cmp(w.chainConfig.ChainID) != 0 {
		return fmt.Errorf("tx %032x has wrong chain ID", tx.Hash())
	}
	if len(tx.Data()) != 0 && len(tx.Data()) != common.AddressLength {
		return fmt.Errorf("tx %v emits UTXO with invalid data length %d", tx.Hash().Hex(), len(tx.Data()))
	}
	gasUsed := env.wo.GasUsed()
	intrinsicGas := types.CalculateIntrinsicQiTxGas(tx, env.qiGasScalingFactor)
	gasUsed += intrinsicGas // the amount of block gas used in this transaction is only the txGas, regardless of ETXs emitted
	if err := env.gasPool.SubGas(intrinsicGas); err != nil {
		return err
	}

	addresses := make(map[common.AddressBytes]struct{})
	totalQitIn := big.NewInt(0)
	utxosDeleteHashes := make([]common.Hash, 0, len(tx.TxIn()))
	inputs := make(map[uint]uint64)
	for _, txIn := range tx.TxIn() {
		utxo := rawdb.GetUTXO(w.workerDb, txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		if utxo == nil {
			return fmt.Errorf("tx %032x spends non-existent UTXO %032x:%d", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		}
		if utxo.Lock != nil && utxo.Lock.Cmp(env.wo.Number(w.hc.NodeCtx())) > 0 {
			return fmt.Errorf("tx %032x spends locked UTXO %032x:%d locked until %s", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, utxo.Lock.String())
		}

		// Perform some spend processing logic
		denomination := utxo.Denomination
		if denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				denomination,
				types.MaxDenomination)
			return errors.New(str)
		}
		addresses[common.AddressBytes(utxo.Address)] = struct{}{}
		totalQitIn.Add(totalQitIn, types.Denominations[denomination])
		utxoHash := types.UTXOHash(txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index, utxo)
		if _, exists := env.deletedUtxos[utxoHash]; exists {
			return fmt.Errorf("tx %032x double spends UTXO %032x:%d", tx.Hash(), txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		}
		env.deletedUtxos[utxoHash] = struct{}{}
		utxosDeleteHashes = append(utxosDeleteHashes, utxoHash)
		inputs[uint(denomination)]++
	}
	var ETXRGas uint64
	var ETXPGas uint64 // Gas used for cross-region and cross-prime ETXs
	etxs := make([]*types.ExternalTx, 0)
	totalQitOut := big.NewInt(0)
	totalConvertQitOut := big.NewInt(0)
	utxosCreateHashes := make([]common.Hash, 0, len(tx.TxOut()))
	conversion := false
	wrapping := false
	var convertAddress common.Address
	outputs := make(map[uint]uint64)
	for txOutIdx, txOut := range tx.TxOut() {
		if txOutIdx > types.MaxOutputIndex {
			return errors.New("transaction has too many outputs")
		}
		if txOut.Denomination > types.MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				txOut.Denomination,
				types.MaxDenomination)
			return errors.New(str)
		}
		if txOut.Lock != nil && txOut.Lock.Sign() != 0 {
			return errors.New("QiTx output has non-zero lock")
		}
		outputs[uint(txOut.Denomination)] += 1
		totalQitOut.Add(totalQitOut, types.Denominations[txOut.Denomination])
		toAddr := common.BytesToAddress(txOut.Address, location)
		if _, exists := addresses[toAddr.Bytes20()]; exists {
			return errors.New("Duplicate address in QiTx outputs: " + toAddr.String())
		}
		addresses[toAddr.Bytes20()] = struct{}{}

		if toAddr.Location().Equal(location) && toAddr.IsInQuaiLedgerScope() && len(tx.Data()) == 0 { // Qi->Quai conversion
			if conversion && !toAddr.Equal(convertAddress) { // All convert outputs must have the same To address for aggregation
				return fmt.Errorf("tx %032x emits multiple convert UTXOs with different To addresses", tx.Hash())
			}
			conversion = true
			convertAddress = toAddr
			totalConvertQitOut.Add(totalConvertQitOut, types.Denominations[txOut.Denomination]) // Add to total conversion output for aggregation
			outputs[uint(txOut.Denomination)] -= 1                                              // This output no longer exists because it has been aggregated
			delete(addresses, toAddr.Bytes20())
			continue
		} else if toAddr.Location().Equal(location) && toAddr.IsInQuaiLedgerScope() && len(tx.Data()) == common.AddressLength { // Wrapped Qi transaction
			ownerContract := common.BytesToAddress(tx.Data(), location)
			if _, err := ownerContract.InternalAndQuaiAddress(); err != nil {
				return err
			}
			wrapping = true
			convertAddress = toAddr
			totalConvertQitOut.Add(totalConvertQitOut, types.Denominations[txOut.Denomination]) // Uses the same path as conversion but takes priority
			outputs[uint(txOut.Denomination)] -= 1                                              // This output no longer exists because it has been aggregated
			delete(addresses, toAddr.Bytes20())
		} else if toAddr.IsInQuaiLedgerScope() {
			return fmt.Errorf("tx %032x emits UTXO with To address not in the Qi ledger scope", tx.Hash())
		}

		if !toAddr.Location().Equal(location) { // This output creates an ETX
			// Cross-region?
			if toAddr.Location().CommonDom(location).Context() == common.REGION_CTX {
				ETXRGas += params.TxGas
			}
			// Cross-prime?
			if toAddr.Location().CommonDom(location).Context() == common.PRIME_CTX {
				ETXPGas += params.TxGas
			}
			if ETXRGas > env.etxRLimit {
				return fmt.Errorf("tx [%v] emits too many cross-region ETXs for block. gas emitted: %d, gas limit: %d", tx.Hash().Hex(), ETXRGas, env.etxRLimit)
			}
			if ETXPGas > env.etxPLimit {
				return fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. gas emitted: %d, gas limit: %d", tx.Hash().Hex(), ETXPGas, env.etxPLimit)
			}
			if !toAddr.IsInQiLedgerScope() {
				return fmt.Errorf("tx [%v] emits UTXO with To address not in the Qi ledger scope", tx.Hash().Hex())
			}

			// We should require some kind of extra fee here
			etxInner := types.ExternalTx{Value: big.NewInt(int64(txOut.Denomination)), To: &toAddr, Sender: common.ZeroAddress(location), EtxType: types.DefaultType, OriginatingTxHash: tx.Hash(), ETXIndex: uint16(txOutIdx), Gas: params.TxGas}
			gasUsed += params.ETXGas
			if err := env.gasPool.SubGas(params.ETXGas); err != nil {
				return err
			}

			if env.parentOrder == nil {
				return errors.New("parent order not set")
			}
			var primeTerminus *types.WorkObject
			if *env.parentOrder == common.PRIME_CTX {
				primeTerminus = parent
			} else {
				primeTerminus = w.hc.GetPrimeTerminus(env.wo)
				if primeTerminus == nil {
					return errors.New("prime terminus not found")
				}
			}
			if !w.hc.CheckIfEtxIsEligible(primeTerminus.EtxEligibleSlices(), *toAddr.Location()) {
				return fmt.Errorf("etx emitted by tx [%v] going to a slice that is not eligible to receive etx %v", tx.Hash().Hex(), *toAddr.Location())
			}
			etxs = append(etxs, &etxInner)
		} else {
			// This output creates a normal UTXO
			utxo := types.NewUtxoEntry(&txOut)
			utxosCreateHashes = append(utxosCreateHashes, types.UTXOHash(tx.Hash(), uint16(txOutIdx), utxo))
		}
	}
	// Ensure the transaction does not spend more than its inputs.
	if totalQitOut.Cmp(totalQitIn) > 0 {
		str := fmt.Sprintf("total value of all transaction inputs for "+
			"transaction %v is %v which is less than the amount "+
			"spent of %v", tx.Hash(), totalQitIn, totalQitOut)
		return errors.New(str)
	}
	txFeeInQit := new(big.Int).Sub(totalQitIn, totalQitOut)
	// Check tx against required base fee and gas
	requiredGas := intrinsicGas + (uint64(len(etxs)) * (params.TxGas + params.ETXGas)) // Each ETX costs extra gas that is paid in the origin
	if requiredGas < intrinsicGas {
		// overflow
		return fmt.Errorf("tx %032x has too many ETXs to calculate required gas", tx.Hash())
	}
	minimumFeeInQuai := new(big.Int).Mul(big.NewInt(int64(requiredGas)), env.wo.BaseFee())

	exchangeRate := primeTerminus.ExchangeRate()

	txFeeInQuai := misc.QiToQuai(env.wo, exchangeRate, env.wo.Difficulty(), txFeeInQit)
	if txFeeInQuai.Cmp(minimumFeeInQuai) < 0 {
		return fmt.Errorf("tx %032x has insufficient fee for base fee * gas, have %d want %d", tx.Hash(), txFeeInQit.Uint64(), minimumFeeInQuai.Uint64())
	}
	if conversion && totalConvertQitOut.Cmp(types.Denominations[params.MinQiConversionDenomination]) < 0 {
		return fmt.Errorf("tx %032x emits convert UTXO with value %d less than minimum conversion denomination", tx.Hash(), totalConvertQitOut.Uint64())
	}
	if conversion || wrapping {
		if conversion && wrapping {
			return fmt.Errorf("tx %032x emits both a conversion and a wrapping UTXO", tx.Hash())
		}
		etxType := types.ConversionType
		data := []byte{}
		if wrapping {
			etxType = types.WrappingQiType
			data = tx.Data()
		}
		// Since this transaction contains a conversion, check if the required conversion gas is paid
		// The user must pay this to the miner now, but it is only added to the block gas limit when the ETX is played in the destination
		requiredGas += params.QiToQuaiConversionGas
		minimumFeeInQuai = new(big.Int).Mul(new(big.Int).SetUint64(requiredGas), env.wo.BaseFee())
		if txFeeInQuai.Cmp(minimumFeeInQuai) < 0 {
			return fmt.Errorf("tx %032x has insufficient fee for base fee * gas, have %d want %d", tx.Hash(), txFeeInQit.Uint64(), minimumFeeInQuai.Uint64())
		}
		ETXPGas += params.QiToQuaiConversionGas // Conversion/wrapping ETXs technically go through Prime
		if ETXPGas > env.etxPLimit {
			return fmt.Errorf("tx [%v] emits too many cross-prime ETXs for block. gas emitted: %d, gas limit: %d", tx.Hash().Hex(), ETXPGas, env.etxPLimit)
		}
		// Value is in Qits not Denomination
		etxInner := types.ExternalTx{Value: totalConvertQitOut, To: &convertAddress, Sender: common.ZeroAddress(location), EtxType: uint64(etxType), OriginatingTxHash: tx.Hash(), Gas: 0, Data: data} // Conversion gas is paid from the converted Quai balance (for new account creation, when redeemed)
		gasUsed += params.ETXGas
		if err := env.gasPool.SubGas(params.ETXGas); err != nil {
			return err
		}
		etxs = append(etxs, &etxInner)
	}
	if gasUsed > env.wo.GasLimit() {
		return fmt.Errorf("tx %032x uses too much gas, have used %d out of %d", tx.Hash(), gasUsed, env.wo.GasLimit())
	}
	env.wo.Header().SetGasUsed(gasUsed)
	env.etxRLimit -= ETXRGas
	env.etxPLimit -= ETXPGas
	for _, etx := range etxs {
		env.etxs = append(env.etxs, types.NewTx(etx))
	}
	env.txs = append(env.txs, tx)
	env.utxoFees.Add(env.utxoFees, txFeeInQit)

	env.utxosDelete = append(env.utxosDelete, utxosDeleteHashes...)
	env.utxosCreate = append(env.utxosCreate, utxosCreateHashes...)
	env.gasUsedAfterTransaction = append(env.gasUsedAfterTransaction, gasUsed)

	if !firstQiTx { // The first transaction in the block can skip denominations check
		if err := CheckDenominations(inputs, outputs); err != nil {
			return err
		}
	}
	receipt := &types.Receipt{Type: tx.Type(), Status: types.ReceiptStatusSuccessful, GasUsed: gasUsed - env.wo.GasUsed(), TxHash: tx.Hash(), OutboundEtxs: env.etxs[len(env.etxs)-len(etxs):]}
	env.receipts = append(env.receipts, receipt)
	// We could add signature verification here, but it's already checked in the mempool and the signature can't be changed, so duplication is largely unnecessary
	return nil
}

func (w *worker) CalcOrder(header *types.WorkObject) (*big.Int, int, error) {
	return w.engine.CalcOrder(w.hc, header)
}
