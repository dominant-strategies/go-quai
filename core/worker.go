package core

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mapset "github.com/deckarep/golang-set"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru"
	expireLru "github.com/hnlq715/golang-lru"
)

const (
	// resultQueueSize is the size of channel listening to sealing result.
	resultQueueSize = 10

	// resubmitAdjustChanSize is the size of resubmitting interval adjustment channel.
	resubmitAdjustChanSize = 10

	// sealingLogAtDepth is the number of confirmations before logging successful sealing.
	sealingLogAtDepth = 7

	// minRecommitInterval is the minimal time interval to recreate the sealing block with
	// any newly arrived transactions.
	minRecommitInterval = 1 * time.Second

	// staleThreshold is the maximum depth of the acceptable stale block.
	staleThreshold = 7

	// pendingBlockBodyLimit is maximum number of pending block bodies to be kept in cache.
	pendingBlockBodyLimit = 320

	// c_headerPrintsExpiryTime is how long a header hash is kept in the cache, so that currentInfo
	// is not printed on a Proc frequency
	c_headerPrintsExpiryTime = 2 * time.Minute

	// c_chainSideChanSize is the size of the channel listening to uncle events
	chainSideChanSize = 10
)

// environment is the worker's current environment and holds all
// information of the sealing block generation.
type environment struct {
	signer types.Signer

	state     *state.StateDB // apply state changes here
	ancestors mapset.Set     // ancestor set (used for checking uncle parent validity)
	family    mapset.Set     // family set (used for checking uncle invalidity)
	tcount    int            // tx count in cycle
	gasPool   *GasPool       // available gas used to pack transactions
	coinbase  common.Address
	etxRLimit int // Remaining number of cross-region ETXs that can be included
	etxPLimit int // Remaining number of cross-prime ETXs that can be included

	header      *types.Header
	txs         []*types.Transaction
	etxs        []*types.Transaction
	utxoFees    *big.Int
	subManifest types.BlockManifest
	receipts    []*types.Receipt
	uncleMu     sync.RWMutex
	uncles      map[common.Hash]*types.Header
}

// copy creates a deep copy of environment.
func (env *environment) copy(processingState bool, nodeCtx int) *environment {
	if nodeCtx == common.ZONE_CTX && processingState {
		cpy := &environment{
			signer:    env.signer,
			state:     env.state.Copy(),
			ancestors: env.ancestors.Clone(),
			family:    env.family.Clone(),
			tcount:    env.tcount,
			coinbase:  env.coinbase,
			etxRLimit: env.etxRLimit,
			etxPLimit: env.etxPLimit,
			header:    types.CopyHeader(env.header),
			receipts:  copyReceipts(env.receipts),
			utxoFees:  new(big.Int).Set(env.utxoFees),
		}
		if env.gasPool != nil {
			gasPool := *env.gasPool
			cpy.gasPool = &gasPool
		}
		// The content of txs and uncles are immutable, unnecessary
		// to do the expensive deep copy for them.
		cpy.txs = make([]*types.Transaction, len(env.txs))
		copy(cpy.txs, env.txs)
		cpy.etxs = make([]*types.Transaction, len(env.etxs))
		copy(cpy.etxs, env.etxs)

		env.uncleMu.Lock()
		cpy.uncles = make(map[common.Hash]*types.Header)
		for hash, uncle := range env.uncles {
			cpy.uncles[hash] = uncle
		}
		env.uncleMu.Unlock()
		return cpy
	} else {
		return &environment{header: types.CopyHeader(env.header)}
	}
}

// unclelist returns the contained uncles as the list format.
func (env *environment) unclelist() []*types.Header {
	env.uncleMu.RLock()
	defer env.uncleMu.RUnlock()
	var uncles []*types.Header
	for _, uncle := range env.uncles {
		uncles = append(uncles, uncle)
	}
	return uncles
}

// discard terminates the background prefetcher go-routine. It should
// always be called for all created environment instances otherwise
// the go-routine leak can happen.
func (env *environment) discard() {
	if env.state == nil {
		return
	}
	env.state.StopPrefetcher()
}

// task contains all information for consensus engine sealing and result submitting.
type task struct {
	receipts  []*types.Receipt
	state     *state.StateDB
	block     *types.Block
	createdAt time.Time
}

const (
	commitInterruptNone int32 = iota
	commitInterruptNewHead
	commitInterruptResubmit
)

// intervalAdjust represents a resubmitting interval adjustment.
type intervalAdjust struct {
	ratio float64
	inc   bool
}

// Config is the configuration parameters of mining.
type Config struct {
	Etherbase  common.Address `toml:",omitempty"` // Public address for block mining rewards (default = first account)
	Notify     []string       `toml:",omitempty"` // HTTP URL list to be notified of new work packages (only useful in ethash).
	NotifyFull bool           `toml:",omitempty"` // Notify with pending block headers instead of work packages
	ExtraData  hexutil.Bytes  `toml:",omitempty"` // Block extra data set by the miner
	GasFloor   uint64         // Target gas floor for mined blocks.
	GasCeil    uint64         // Target gas ceiling for mined blocks.
	GasPrice   *big.Int       // Minimum gas price for mining a transaction
	Recommit   time.Duration  // The time interval for miner to re-create mining work.
	Noverify   bool           // Disable remote mining solution verification(only useful in ethash).
}

// worker is the main object which takes care of submitting new work to consensus engine
// and gathering the sealing result.
type worker struct {
	config      *Config
	chainConfig *params.ChainConfig
	engine      consensus.Engine
	hc          *HeaderChain
	txPool      *TxPool

	// Feeds
	pendingLogsFeed   event.Feed
	pendingHeaderFeed event.Feed

	// Subscriptions
	chainHeadCh  chan ChainHeadEvent
	chainHeadSub event.Subscription

	chainSideCh  chan ChainSideEvent
	chainSideSub event.Subscription

	// Channels
	taskCh                         chan *task
	resultCh                       chan *types.Block
	exitCh                         chan struct{}
	resubmitIntervalCh             chan time.Duration
	resubmitAdjustCh               chan *intervalAdjust
	fillTransactionsRollingAverage *RollingAverage

	interrupt   chan struct{}
	asyncPhFeed event.Feed // asyncPhFeed sends an event after each state root update
	scope       event.SubscriptionScope

	wg sync.WaitGroup

	localUncles  map[common.Hash]*types.Block // A set of side blocks generated locally as the possible uncle blocks.
	remoteUncles map[common.Hash]*types.Block // A set of side blocks as the possible uncle blocks.
	uncleMu      sync.RWMutex

	mu       sync.RWMutex // The lock used to protect the coinbase and extra fields
	coinbase common.Address
	extra    []byte

	workerDb ethdb.Database

	pendingBlockBody *lru.Cache

	snapshotMu    sync.RWMutex // The lock used to protect the snapshots below
	snapshotBlock *types.Block

	headerPrints *expireLru.Cache

	// atomic status counters
	running int32 // The indicator whether the consensus engine is running or not.
	newTxs  int32 // New arrival transaction count since last sealing work submitting.

	// noempty is the flag used to control whether the feature of pre-seal empty
	// block is enabled. The default value is false(pre-seal is enabled by default).
	// But in some special scenario the consensus engine will seal blocks instantaneously,
	// in this case this feature will add all empty blocks into canonical chain
	// non-stop and no real transaction will be included.
	noempty uint32

	// External functions
	isLocalBlock func(header *types.Header) bool // Function used to determine whether the specified block is mined by local miner.

	// Test hooks
	newTaskHook  func(*task) // Method to call upon receiving a new sealing task.
	fullTaskHook func()      // Method to call before pushing the full sealing task.

	logger *log.Logger
}

type RollingAverage struct {
	windowSize int
	durations  []time.Duration
	sum        time.Duration
}

func (ra *RollingAverage) Add(d time.Duration) {
	if len(ra.durations) == ra.windowSize {
		// Remove the oldest duration from the sum
		ra.sum -= ra.durations[0]
		ra.durations = ra.durations[1:]
	}
	ra.durations = append(ra.durations, d)
	ra.sum += d
}
func (ra *RollingAverage) Average() time.Duration {
	if len(ra.durations) == 0 {
		return 0
	}
	return ra.sum / time.Duration(len(ra.durations))
}

func newWorker(config *Config, chainConfig *params.ChainConfig, db ethdb.Database, engine consensus.Engine, headerchain *HeaderChain, txPool *TxPool, isLocalBlock func(header *types.Header) bool, init bool, processingState bool, logger *log.Logger) *worker {
	worker := &worker{
		config:                         config,
		chainConfig:                    chainConfig,
		engine:                         engine,
		hc:                             headerchain,
		txPool:                         txPool,
		coinbase:                       config.Etherbase,
		isLocalBlock:                   isLocalBlock,
		workerDb:                       db,
		localUncles:                    make(map[common.Hash]*types.Block),
		remoteUncles:                   make(map[common.Hash]*types.Block),
		chainHeadCh:                    make(chan ChainHeadEvent, chainHeadChanSize),
		chainSideCh:                    make(chan ChainSideEvent, chainSideChanSize),
		taskCh:                         make(chan *task),
		resultCh:                       make(chan *types.Block, resultQueueSize),
		exitCh:                         make(chan struct{}),
		interrupt:                      make(chan struct{}),
		resubmitIntervalCh:             make(chan time.Duration),
		resubmitAdjustCh:               make(chan *intervalAdjust, resubmitAdjustChanSize),
		fillTransactionsRollingAverage: &RollingAverage{windowSize: 100},
		logger:                         logger,
	}
	// Set the GasFloor of the worker to the minGasLimit
	worker.config.GasFloor = params.MinGasLimit

	phBodyCache, _ := lru.New(pendingBlockBodyLimit)
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

	headerPrints, _ := expireLru.NewWithExpire(1, c_headerPrintsExpiryTime)
	worker.headerPrints = headerPrints

	nodeCtx := headerchain.NodeCtx()
	if headerchain.ProcessingState() && nodeCtx == common.ZONE_CTX {
		worker.chainHeadSub = worker.hc.SubscribeChainHeadEvent(worker.chainHeadCh)
		worker.chainSideSub = worker.hc.SubscribeChainSideEvent(worker.chainSideCh)
		worker.wg.Add(1)
		go worker.asyncStateLoop()
	}

	return worker
}

// setEtherbase sets the etherbase used to initialize the block coinbase field.
func (w *worker) setEtherbase(addr common.Address) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.coinbase = addr
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
func (w *worker) pending() *types.Block {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return w.snapshotBlock
}

// pendingBlock returns pending block.
func (w *worker) pendingBlock() *types.Block {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return w.snapshotBlock
}

// pendingBlockAndReceipts returns pending block and corresponding receipts.
func (w *worker) pendingBlockAndReceipts() (*types.Block, types.Receipts) {
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
		w.chainHeadSub.Unsubscribe()
		w.chainSideSub.Unsubscribe()
	}
	atomic.StoreInt32(&w.running, 0)
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
			w.pendingBlockBody.Add(key, &types.Body{})
		} else {
			w.pendingBlockBody.Add(key, rawdb.ReadPbCacheBody(w.workerDb, key, w.hc.NodeLocation()))
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
			pendingBlockBodyKeys = append(pendingBlockBodyKeys, key.(common.Hash))
			if key.(common.Hash) != types.EmptyBodyHash {
				rawdb.WritePbCacheBody(w.workerDb, key.(common.Hash), value.(*types.Body))
			}
		}
	}
	rawdb.WritePbBodyKeys(w.workerDb, pendingBlockBodyKeys)
}

// asyncStateLoop updates the state root for a block and returns the state udpate in a channel
func (w *worker) asyncStateLoop() {
	defer w.wg.Done() // decrement the wait group after the close of the loop

	for {
		select {
		case head := <-w.chainHeadCh:

			w.interruptAsyncPhGen()

			go func() {
				select {
				case <-w.interrupt:
					w.interrupt = make(chan struct{})
					return
				default:
					block := head.Block
					header, err := w.GeneratePendingHeader(block, true)
					if err != nil {
						w.logger.WithField("err", err).Error("Error generating pending header")
						return
					}
					// Send the updated pendingHeader in the asyncPhFeed
					w.asyncPhFeed.Send(header)
					return
				}
			}()
		case side := <-w.chainSideCh:
			go func() {
				if side.ResetUncles {
					w.uncleMu.Lock()
					w.localUncles = make(map[common.Hash]*types.Block)
					w.remoteUncles = make(map[common.Hash]*types.Block)
					w.uncleMu.Unlock()
				}
				for _, block := range side.Blocks {

					// Short circuit for duplicate side blocks
					w.uncleMu.RLock()
					if _, exists := w.localUncles[block.Hash()]; exists {
						w.uncleMu.RUnlock()
						continue
					}
					if _, exists := w.remoteUncles[block.Hash()]; exists {
						w.uncleMu.RUnlock()
						continue
					}
					w.uncleMu.RUnlock()
					if w.isLocalBlock != nil && w.isLocalBlock(block.Header()) {
						w.uncleMu.Lock()
						w.localUncles[block.Hash()] = block
						w.uncleMu.Unlock()
					} else {
						w.uncleMu.Lock()
						w.remoteUncles[block.Hash()] = block
						w.uncleMu.Unlock()
					}
				}
			}()
		case <-w.exitCh:
			return
		case <-w.chainHeadSub.Err():
			return
		case <-w.chainSideSub.Err():
			return
		}
	}
}

// GeneratePendingBlock generates pending block given a commited block.
func (w *worker) GeneratePendingHeader(block *types.Block, fill bool) (*types.Header, error) {
	nodeCtx := w.hc.NodeCtx()

	w.interruptAsyncPhGen()

	var (
		interrupt *int32
		timestamp int64 // timestamp for each round of sealing.
	)

	if interrupt != nil {
		atomic.StoreInt32(interrupt, commitInterruptNewHead)
	}
	interrupt = new(int32)
	atomic.StoreInt32(&w.newTxs, 0)

	start := time.Now()
	// Set the coinbase if the worker is running or it's required
	var coinbase common.Address
	if w.hc.NodeCtx() == common.ZONE_CTX && w.coinbase.Equal(common.Address{}) {
		w.logger.Error("Refusing to mine without etherbase")
		return nil, errors.New("etherbase not found")
	} else if w.coinbase.Equal(common.Address{}) {
		w.coinbase = common.Zero
	}
	coinbase = w.coinbase // Use the preset address as the fee recipient

	work, err := w.prepareWork(&generateParams{
		timestamp: uint64(timestamp),
		coinbase:  coinbase,
	}, block)
	if err != nil {
		return nil, err
	}

	if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
		work.txs = append(work.txs, types.NewTx(&types.QiTx{})) // placeholder
		// Fill pending transactions from the txpool
		w.adjustGasLimit(nil, work, block)
		work.utxoFees = big.NewInt(0)
		if fill {
			start := time.Now()
			w.fillTransactions(interrupt, work, block)
			w.fillTransactionsRollingAverage.Add(time.Since(start))
			w.logger.WithFields(log.Fields{
				"count":   len(work.txs),
				"elapsed": common.PrettyDuration(time.Since(start)),
				"average": common.PrettyDuration(w.fillTransactionsRollingAverage.Average()),
			}).Info("Filled and sorted pending transactions")
		}
		coinbaseTx, err := createCoinbaseTxWithFees(work.header, work.utxoFees)
		if err != nil {
			return nil, err
		}
		work.txs[0] = coinbaseTx
	}

	// Create a local environment copy, avoid the data race with snapshot state.
	newBlock, err := w.FinalizeAssemble(w.hc, work.header, block, work.state, work.txs, work.unclelist(), work.etxs, work.subManifest, work.receipts)
	if err != nil {
		return nil, err
	}

	work.header = newBlock.Header()

	w.printPendingHeaderInfo(work, newBlock, start)

	return work.header, nil
}

// printPendingHeaderInfo logs the pending header information
func (w *worker) printPendingHeaderInfo(work *environment, block *types.Block, start time.Time) {
	work.uncleMu.RLock()
	if w.CurrentInfo(block.Header()) {
		w.logger.WithFields(log.Fields{
			"number":   block.Number(w.hc.NodeCtx()),
			"sealhash": block.Header().SealHash(),
			"uncles":   len(work.uncles),
			"txs":      len(work.txs),
			"etxs":     len(block.ExtTransactions()),
			"gas":      block.GasUsed(),
			"fees":     totalFees(block, work.receipts),
			"elapsed":  common.PrettyDuration(time.Since(start)),
		}).Info("Commit new sealing work")
	} else {
		w.logger.WithFields(log.Fields{
			"number":   block.Number(w.hc.NodeCtx()),
			"sealhash": block.Header().SealHash(),
			"uncles":   len(work.uncles),
			"txs":      len(work.txs),
			"etxs":     len(block.ExtTransactions()),
			"gas":      block.GasUsed(),
			"fees":     totalFees(block, work.receipts),
			"elapsed":  common.PrettyDuration(time.Since(start)),
		}).Debug("Commit new sealing work")
	}
	work.uncleMu.RUnlock()
}

// interruptAsyncPhGen kills any async ph generation running
func (w *worker) interruptAsyncPhGen() {
	if w.interrupt != nil {
		close(w.interrupt)
		w.interrupt = nil
	}
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
func (w *worker) makeEnv(parent *types.Block, header *types.Header, coinbase common.Address) (*environment, error) {
	// Retrieve the parent state to execute on top and start a prefetcher for
	// the miner to speed block sealing up a bit.
	state, err := w.hc.bc.processor.StateAt(parent.Root())
	if err != nil {
		return nil, err
	}

	etxRLimit := len(parent.Transactions()) / params.ETXRegionMaxFraction
	if etxRLimit < params.ETXRLimitMin {
		etxRLimit = params.ETXRLimitMin
	}
	etxPLimit := len(parent.Transactions()) / params.ETXPrimeMaxFraction
	if etxPLimit < params.ETXPLimitMin {
		etxPLimit = params.ETXPLimitMin
	}
	// Note the passed coinbase may be different with header.Coinbase.
	env := &environment{
		signer:    types.MakeSigner(w.chainConfig, header.Number(w.hc.NodeCtx())),
		state:     state,
		coinbase:  coinbase,
		ancestors: mapset.NewSet(),
		family:    mapset.NewSet(),
		header:    header,
		uncles:    make(map[common.Hash]*types.Header),
		etxRLimit: etxRLimit,
		etxPLimit: etxPLimit,
	}
	// when 08 is processed ancestors contain 07 (quick block)
	for _, ancestor := range w.hc.GetBlocksFromHash(parent.Hash(), 7) {
		for _, uncle := range ancestor.Uncles() {
			env.family.Add(uncle.Hash())
		}
		env.family.Add(ancestor.Hash())
		env.ancestors.Add(ancestor.Hash())
	}
	// Keep track of transactions which return errors so they can be removed
	env.tcount = 0
	return env, nil
}

// commitUncle adds the given block to uncle block set, returns error if failed to add.
func (w *worker) commitUncle(env *environment, uncle *types.Header) error {
	env.uncleMu.Lock()
	defer env.uncleMu.Unlock()
	hash := uncle.Hash()
	if _, exist := env.uncles[hash]; exist {
		return errors.New("uncle not unique")
	}
	if env.header.ParentHash(w.hc.NodeCtx()) == uncle.ParentHash(w.hc.NodeCtx()) {
		return errors.New("uncle is sibling")
	}
	if !env.ancestors.Contains(uncle.ParentHash(w.hc.NodeCtx())) {
		return errors.New("uncle's parent unknown")
	}
	if env.family.Contains(hash) {
		return errors.New("uncle already included")
	}
	env.uncles[hash] = uncle
	return nil
}

func (w *worker) commitTransaction(env *environment, tx *types.Transaction) ([]*types.Log, error) {
	if tx != nil {
		snap := env.state.Snapshot()
		// retrieve the gas used int and pass in the reference to the ApplyTransaction
		gasUsed := env.header.GasUsed()
		receipt, err := ApplyTransaction(w.chainConfig, w.hc, &env.coinbase, env.gasPool, env.state, env.header, tx, &gasUsed, *w.hc.bc.processor.GetVMConfig(), &env.etxRLimit, &env.etxPLimit, w.logger)
		if err != nil {
			w.logger.WithFields(log.Fields{
				"err":     err,
				"tx":      tx.Hash().Hex(),
				"block":   env.header.Number,
				"gasUsed": gasUsed,
			}).Debug("Error playing transaction in worker")
			env.state.RevertToSnapshot(snap)
			return nil, err
		}
		// once the gasUsed pointer is updated in the ApplyTransaction it has to be set back to the env.Header.GasUsed
		// This extra step is needed because previously the GasUsed was a public method and direct update of the value
		// was possible.
		env.header.SetGasUsed(gasUsed)
		env.txs = append(env.txs, tx)
		env.receipts = append(env.receipts, receipt)
		if receipt.Status == types.ReceiptStatusSuccessful {
			env.etxs = append(env.etxs, receipt.Etxs...)
		}
		return receipt.Logs, nil
	}
	return nil, errors.New("error finding transaction")
}

func (w *worker) commitTransactions(env *environment, txs *types.TransactionsByPriceAndNonce, interrupt *int32) bool {
	gasLimit := env.header.GasLimit
	if env.gasPool == nil {
		env.gasPool = new(GasPool).AddGas(gasLimit())
	}
	var coalescedLogs []*types.Log

	for {
		// In the following three cases, we will interrupt the execution of the transaction.
		// (1) new head block event arrival, the interrupt signal is 1
		// (2) worker start or restart, the interrupt signal is 1
		// (3) worker recreate the sealing block with any newly arrived transactions, the interrupt signal is 2.
		// For the first two cases, the semi-finished work will be discarded.
		// For the third case, the semi-finished work will be submitted to the consensus engine.
		if interrupt != nil && atomic.LoadInt32(interrupt) != commitInterruptNone {
			// Notify resubmit loop to increase resubmitting interval due to too frequent commits.
			if atomic.LoadInt32(interrupt) == commitInterruptResubmit {
				ratio := float64(gasLimit()-env.gasPool.Gas()) / float64(gasLimit())
				if ratio < 0.1 {
					ratio = 0.1
				}
				w.resubmitAdjustCh <- &intervalAdjust{
					ratio: ratio,
					inc:   true,
				}
			}
			return atomic.LoadInt32(interrupt) == commitInterruptNewHead
		}
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
			if err := w.hc.VerifyUTXOsForTx(tx); err != nil { // No need to do sig verification again. Perhaps it should be cached?
				w.logger.WithField("err", err).Error("UTXO tx verification failed")
				txs.PopNoSort()
				continue
			}
			txGas := types.CalculateQiTxGas(tx)
			if err := env.gasPool.SubGas(txGas); err != nil {
				w.logger.WithField("err", err).Error("UTXO tx gas pool error")
				txs.PopNoSort()
				continue
			}
			gasUsed := env.header.GasUsed()
			env.header.SetGasUsed(gasUsed + txGas)
			if fee := txs.GetFee(); fee != nil {
				env.utxoFees.Add(env.utxoFees, fee)
			}
			env.txs = append(env.txs, tx)
			txs.PopNoSort()
			continue
		}
		// Error may be ignored here. The error has already been checked
		// during transaction acceptance is the transaction pool.
		//
		// We use the signer regardless of the current hf.
		from, err := types.Sender(env.signer, tx)
		if err != nil {
			continue
		}
		// Start executing the transaction
		env.state.Prepare(tx.Hash(), env.tcount)

		logs, err := w.commitTransaction(env, tx)
		switch {
		case errors.Is(err, ErrGasLimitReached):
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
			coalescedLogs = append(coalescedLogs, logs...)
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
			}).Error("Transaction failed, account skipped")
			txs.Shift(from.Bytes20(), false)
		}
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
	return false
}

// generateParams wraps various of settings for generating sealing task.
type generateParams struct {
	timestamp uint64         // The timstamp for sealing task
	forceTime bool           // Flag whether the given timestamp is immutable or not
	coinbase  common.Address // The fee recipient address for including transaction
}

// prepareWork constructs the sealing task according to the given parameters,
// either based on the last chain head or specified parent. In this function
// the pending transactions are not filled yet, only the empty task returned.
func (w *worker) prepareWork(genParams *generateParams, block *types.Block) (*environment, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	nodeCtx := w.hc.NodeCtx()

	// Find the parent block for sealing task
	parent := block
	// Sanity check the timestamp correctness, recap the timestamp
	// to parent+1 if the mutation is allowed.
	timestamp := genParams.timestamp
	if parent.Time() >= timestamp {
		if genParams.forceTime {
			return nil, fmt.Errorf("invalid timestamp, parent %d given %d", parent.Time(), timestamp)
		}
		timestamp = parent.Time() + 1
	}
	// Construct the sealing block header, set the extra field if it's allowed
	num := parent.Number(nodeCtx)
	header := types.EmptyHeader()
	header.SetParentHash(block.Header().Hash(), nodeCtx)
	header.SetNumber(big.NewInt(int64(num.Uint64())+1), nodeCtx)
	header.SetTime(timestamp)
	header.SetLocation(w.hc.NodeLocation())

	// Only calculate entropy if the parent is not the genesis block
	if parent.Hash() != w.hc.config.GenesisHash {
		_, order, err := w.engine.CalcOrder(parent.Header())
		if err != nil {
			return nil, err
		}
		// Set the parent delta S prior to sending to sub
		if nodeCtx != common.PRIME_CTX {
			if order < nodeCtx {
				header.SetParentDeltaS(big.NewInt(0), nodeCtx)
			} else {
				header.SetParentDeltaS(w.engine.DeltaLogS(parent.Header()), nodeCtx)
			}
		}
		header.SetParentEntropy(w.engine.TotalLogS(parent.Header()), nodeCtx)
	}

	// Only zone should calculate state
	if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
		header.SetExtra(w.extra)
		header.SetBaseFee(misc.CalcBaseFee(w.chainConfig, parent.Header()))
		if w.isRunning() {
			if w.coinbase.Equal(common.Zero) {
				w.logger.Error("Refusing to mine without etherbase")
				return nil, errors.New("refusing to mine without etherbase")
			}
			header.SetCoinbase(w.coinbase)
		}

		// Run the consensus preparation with the default or customized consensus engine.
		if err := w.engine.Prepare(w.hc, header, block.Header()); err != nil {
			w.logger.WithField("err", err).Error("Failed to prepare header for sealing")
			return nil, err
		}
		env, err := w.makeEnv(parent, header, w.coinbase)
		if err != nil {
			w.logger.WithField("err", err).Error("Failed to create sealing context")
			return nil, err
		}
		// Accumulate the uncles for the sealing work.
		commitUncles := func(blocks map[common.Hash]*types.Block) {
			for hash, uncle := range blocks {
				env.uncleMu.RLock()
				if len(env.uncles) == 2 {
					env.uncleMu.RUnlock()
					break
				}
				env.uncleMu.RUnlock()
				if err := w.commitUncle(env, uncle.Header()); err != nil {
					w.logger.WithFields(log.Fields{
						"hash":   hash,
						"reason": err,
					}).Trace("Possible uncle rejected")
				} else {
					w.logger.WithField("hash", hash).Debug("Committing new uncle to block")
				}
			}
		}
		if nodeCtx == common.ZONE_CTX && w.hc.ProcessingState() {
			w.uncleMu.RLock()
			// Prefer to locally generated uncle
			commitUncles(w.localUncles)
			commitUncles(w.remoteUncles)
			w.uncleMu.RUnlock()
		}
		return env, nil
	} else {
		return &environment{header: header}, nil
	}

}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (w *worker) fillTransactions(interrupt *int32, env *environment, block *types.Block) {
	// Split the pending transactions into locals and remotes
	// Fill the block with all available pending transactions.
	etxSet := rawdb.ReadEtxSet(w.hc.bc.db, block.Hash(), block.NumberU64(w.hc.NodeCtx()), w.hc.NodeLocation())
	if etxSet == nil {
		return
	}
	etxSet.Update(types.Transactions{}, block.NumberU64(w.hc.NodeCtx())+1, w.hc.NodeLocation()) // Prune any expired ETXs
	etxs := make([]*types.Transaction, 0, len(etxSet))

	for _, entry := range etxSet {
		tx := entry.ETX
		if tx.ETXSender().Location().Equal(w.chainConfig.Location) { // Sanity check
			w.logger.WithFields(log.Fields{
				"tx":     tx.Hash().String(),
				"sender": tx.ETXSender().String(),
			}).Error("ETX sender is in our location!")
			continue // skip this tx
		}
		etxs = append(etxs, &tx)
	}

	pending, err := w.txPool.TxPoolPending(true)
	if err != nil {
		return
	}

	pendingUtxoTxs := w.txPool.UTXOPoolPending()

	if len(pending) > 0 || len(pendingUtxoTxs) > 0 || len(etxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, etxs, pending, env.header.BaseFee(), true)
		for _, tx := range pendingUtxoTxs {
			txs.AppendNoSort(tx) // put all utxos at the back for now
		}
		if w.commitTransactions(env, txs, interrupt) {
			return
		}
	}
}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (w *worker) adjustGasLimit(interrupt *int32, env *environment, parent *types.Block) {
	env.header.SetGasLimit(CalcGasLimit(parent.Header(), w.config.GasCeil))
}

// ComputeManifestHash given a header computes the manifest hash for the header
// and stores it in the database
func (w *worker) ComputeManifestHash(header *types.Header) common.Hash {
	manifest := rawdb.ReadManifest(w.workerDb, header.Hash())
	if manifest == nil {
		nodeCtx := w.hc.NodeCtx()
		// Compute and set manifest hash
		manifest = types.BlockManifest{}
		if nodeCtx == common.PRIME_CTX {
			// Nothing to do for prime chain
			manifest = types.BlockManifest{}
		} else if w.engine.IsDomCoincident(w.hc, header) {
			manifest = types.BlockManifest{header.Hash()}
		} else {
			parentManifest := rawdb.ReadManifest(w.workerDb, header.ParentHash(nodeCtx))
			manifest = append(parentManifest, header.Hash())
		}
		// write the manifest into the disk
		rawdb.WriteManifest(w.workerDb, header.Hash(), manifest)
	}
	manifestHash := types.DeriveSha(manifest, trie.NewStackTrie(nil))

	return manifestHash
}

func (w *worker) FinalizeAssemble(chain consensus.ChainHeaderReader, header *types.Header, parent *types.Block, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, etxs []*types.Transaction, subManifest types.BlockManifest, receipts []*types.Receipt) (*types.Block, error) {
	nodeCtx := w.hc.NodeCtx()
	block, err := w.engine.FinalizeAndAssemble(chain, header, state, txs, uncles, etxs, subManifest, receipts)
	if err != nil {
		return nil, err
	}

	manifestHash := w.ComputeManifestHash(parent.Header())

	if w.hc.ProcessingState() {
		block.Header().SetManifestHash(manifestHash, nodeCtx)
		if nodeCtx == common.ZONE_CTX {
			// Compute and set etx rollup hash
			var etxRollup types.Transactions
			if w.engine.IsDomCoincident(w.hc, parent.Header()) {
				etxRollup = parent.ExtTransactions()
			} else {
				etxRollup, err = w.hc.CollectEtxRollup(parent)
				if err != nil {
					return nil, err
				}
				etxRollup = append(etxRollup, parent.ExtTransactions()...)
			}
			etxRollupHash := types.DeriveSha(etxRollup, trie.NewStackTrie(nil))
			block.Header().SetEtxRollupHash(etxRollupHash)
		}

		w.AddPendingBlockBody(block.Header(), block.Body())
	}

	return block, nil
}

// GetPendingBlockBodyKey takes a header and hashes all the Roots together
// and returns the key to be used for the pendingBlockBodyCache.
func (w *worker) getPendingBlockBodyKey(header *types.Header) common.Hash {
	return types.RlpHash([]interface{}{
		header.UncleHash(),
		header.TxHash(),
		header.EtxHash(),
	})
}

// AddPendingBlockBody adds an entry in the lru cache for the given pendingBodyKey
// maps it to body.
func (w *worker) AddPendingBlockBody(header *types.Header, body *types.Body) {
	w.pendingBlockBody.ContainsOrAdd(w.getPendingBlockBodyKey(header), body)
}

// GetPendingBlockBody gets the block body associated with the given header.
func (w *worker) GetPendingBlockBody(header *types.Header) *types.Body {
	key := w.getPendingBlockBodyKey(header)
	body, ok := w.pendingBlockBody.Get(key)
	if ok {
		return body.(*types.Body)
	}
	w.logger.WithField("key", key).Warn("pending block body not found for header")
	return nil
}

func (w *worker) SubscribeAsyncPendingHeader(ch chan *types.Header) event.Subscription {
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
func totalFees(block *types.Block, receipts []*types.Receipt) *big.Float {
	feesWei := new(big.Int)
	for i, tx := range block.QuaiTransactions() {
		minerFee, _ := tx.EffectiveGasTip(block.BaseFee())
		feesWei.Add(feesWei, new(big.Int).Mul(new(big.Int).SetUint64(receipts[i].GasUsed), minerFee))
	}
	return new(big.Float).Quo(new(big.Float).SetInt(feesWei), new(big.Float).SetInt(big.NewInt(params.Ether)))
}

func (w *worker) CurrentInfo(header *types.Header) bool {
	if w.headerPrints.Contains(header.Hash()) {
		return false
	}

	w.headerPrints.Add(header.Hash(), nil)
	return header.NumberU64(w.hc.NodeCtx())+c_startingPrintLimit > w.hc.CurrentHeader().NumberU64(w.hc.NodeCtx())
}
