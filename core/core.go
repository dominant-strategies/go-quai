package core

import (
	"fmt"
	"io"
	"math/big"
	"sort"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/state/snapshot"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
	lru "github.com/hashicorp/golang-lru"
)

const (
	maxFutureHeaders     = 1800
	maxTimeFutureHeaders = 30
)

type Core struct {
	sl     *Slice
	engine consensus.Engine

	futureHeaders *lru.Cache

	quit chan struct{} // core quit channel
}

func NewCore(db ethdb.Database, config *Config, isLocalBlock func(block *types.Header) bool, txConfig *TxPoolConfig, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis) (*Core, error) {
	slice, err := NewSlice(db, config, txConfig, isLocalBlock, chainConfig, domClientUrl, subClientUrls, engine, cacheConfig, vmConfig, genesis)
	if err != nil {
		return nil, err
	}

	c := &Core{
		sl:     slice,
		engine: engine,
		quit:   make(chan struct{}),
	}

	futureHeaders, _ := lru.New(maxFutureHeaders)
	c.futureHeaders = futureHeaders

	go c.updateFutureHeaders()
	return c, nil
}

func (c *Core) InsertChain(blocks types.Blocks) (int, error) {
	nodeCtx := common.NodeLocation.Context()
	domWait := false
	for i, block := range blocks {
		isCoincident := c.sl.engine.IsDomCoincident(block.Header())
		// Write the block body to the CandidateBody database.
		rawdb.WriteCandidateBody(c.sl.sliceDb, block.Hash(), block.Body())

		if !isCoincident && !domWait {
			newPendingEtxs, err := c.sl.Append(block.Header(), types.EmptyHeader(), common.Hash{}, big.NewInt(0), false, true, nil)
			if err != nil {
				if err == consensus.ErrFutureBlock ||
					err.Error() == ErrBodyNotFound.Error() ||
					err.Error() == ErrPendingEtxNotFound.Error() ||
					err.Error() == consensus.ErrPrunedAncestor.Error() ||
					err.Error() == consensus.ErrUnknownAncestor.Error() ||
					err.Error() == ErrSubNotSyncedToDom.Error() ||
					err.Error() == ErrDomClientNotUp.Error() {
					c.addfutureHeader(block.Header())
					return i, ErrAddedFutureCache
				}
				if err == ErrKnownBlock {
					// Remove the header from the future headers cache
					c.futureHeaders.Remove(block.Hash())
					return i, nil
				}

				log.Info("InsertChain", "err in Append core: ", err)
				return i, err
			}
			// Remove the header from the future headers cache since the block append was succesful
			c.futureHeaders.Remove(block.Hash())

			// If we have a dom, send the dom any pending ETXs which will become
			// referencable by this block. When this block is referenced in the dom's
			// subordinate block manifest, then ETXs produced by this block and the rollup
			// of ETXs produced by subordinate chain(s) will become referencable.
			if nodeCtx > common.PRIME_CTX {
				if err := c.SendPendingEtxsToDom(types.PendingEtxs{block.Header(), newPendingEtxs}); err != nil {
					log.Error("failed to send ETXs to domclient", "block: ", block.Hash(), "err", err)
				}
			}
		} else {
			domWait = true
		}
	}
	return len(blocks), nil
}

// procfutureHeaders sorts the future block cache and attempts to append
func (c *Core) procfutureHeaders() {
	headers := make([]*types.Header, 0, c.futureHeaders.Len())
	for _, hash := range c.futureHeaders.Keys() {
		if header, exist := c.futureHeaders.Peek(hash); exist {
			headers = append(headers, header.(*types.Header))
		}
	}
	if len(headers) > 0 {
		sort.Slice(headers, func(i, j int) bool {
			return headers[i].NumberU64() < headers[j].NumberU64()
		})

		for _, head := range headers {
			block, err := c.sl.ConstructLocalBlock(head)
			if err != nil {
				if err.Error() == ErrBodyNotFound.Error() {
					c.addfutureHeader(head)
				}
				log.Debug("could not construct block from future header", "err:", err)
			} else {
				c.InsertChain([]*types.Block{block})
			}
		}
	}
	if c.futureHeaders.Len() == 0 {
		c.sl.downloaderWaitFeed.Send(false)
	}
}

// addfutureHeader adds a block to the future block cache
func (c *Core) addfutureHeader(header *types.Header) error {
	max := uint64(time.Now().Unix() + maxTimeFutureHeaders)
	if header.Time() > max {
		return fmt.Errorf("future block timestamp %v > allowed %v", header.Time(), max)
	}
	if !c.futureHeaders.Contains(header.Hash()) {
		c.futureHeaders.Add(header.Hash(), header)
	}
	return nil
}

// updatefutureHeaders is a time to procfutureHeaders
func (c *Core) updateFutureHeaders() {
	futureTimer := time.NewTicker(3 * time.Second)
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			c.procfutureHeaders()
		case <-c.quit:
			return
		}
	}
}

// InsertChainWithoutSealVerification works exactly the same
// except for seal verification, seal verification is omitted
func (c *Core) InsertChainWithoutSealVerification(block *types.Block) (int, error) {
	return 0, nil
}

func (c *Core) Processor() *StateProcessor {
	return c.sl.hc.bc.processor
}

func (c *Core) Config() *params.ChainConfig {
	return c.sl.hc.bc.chainConfig
}

// Engine retreives the blake3 consensus engine.
func (c *Core) Engine() consensus.Engine {
	return c.engine
}

// Slice retrieves the slice struct.
func (c *Core) Slice() *Slice {
	return c.sl
}

func (c *Core) TxPool() *TxPool {
	return c.sl.txPool
}

func (c *Core) Stop() {
	close(c.quit)
	c.sl.Stop()
}

//---------------//
// Slice methods //
//---------------//

func (c *Core) Append(header *types.Header, domPendingHeader *types.Header, domTerminus common.Hash, td *big.Int, domOrigin bool, reorg bool, newInboundEtxs types.Transactions) ([]types.Transactions, error) {
	newPendingEtxs, err := c.sl.Append(header, domPendingHeader, domTerminus, td, domOrigin, reorg, newInboundEtxs)
	// If dom tries to append the block and sub is not in sync.
	// proc the future header cache.
	go c.procfutureHeaders()
	return newPendingEtxs, err
}

// ConstructLocalBlock takes a header and construct the Block locally
func (c *Core) ConstructLocalMinedBlock(header *types.Header) (*types.Block, error) {
	return c.sl.ConstructLocalMinedBlock(header)
}

func (c *Core) SubRelayPendingHeader(slPendingHeader types.PendingHeader, reorg bool, location common.Location) {
	c.sl.SubRelayPendingHeader(slPendingHeader, reorg, location)
}

func (c *Core) GetPendingHeader() (*types.Header, error) {
	return c.sl.GetPendingHeader()
}

func (c *Core) GetManifest(blockHash common.Hash) (types.BlockManifest, error) {
	return c.sl.GetManifest(blockHash)
}

func (c *Core) GetSubManifest(slice common.Location, blockHash common.Hash) (types.BlockManifest, error) {
	return c.sl.GetSubManifest(slice, blockHash)
}

func (c *Core) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	return c.sl.AddPendingEtxs(pEtxs)
}

func (c *Core) SendPendingEtxsToDom(pEtxs types.PendingEtxs) error {
	return c.sl.SendPendingEtxsToDom(pEtxs)
}

func (c *Core) SubscribeDownloaderWait(ch chan<- bool) event.Subscription {
	return c.sl.SubscribeDownloaderWait(ch)
}

func (c *Core) SubscribeMissingBody(ch chan<- *types.Header) event.Subscription {
	return c.sl.SubscribeMissingBody(ch)
}

//---------------------//
// HeaderChain methods //
//---------------------//

// GetBlock retrieves a block from the database by hash and number,
// caching it if found.
func (c *Core) GetBlock(hash common.Hash, number uint64) *types.Block {
	return c.sl.hc.GetBlock(hash, number)
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (c *Core) GetBlockByHash(hash common.Hash) *types.Block {
	return c.sl.hc.GetBlockByHash(hash)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (c *Core) GetHeaderByNumber(number uint64) *types.Header {
	return c.sl.hc.GetHeaderByNumber(number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (c *Core) GetBlockByNumber(number uint64) *types.Block {
	return c.sl.hc.GetBlockByNumber(number)
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (c *Core) GetBlocksFromHash(hash common.Hash, n int) []*types.Block {
	return c.sl.hc.GetBlocksFromHash(hash, n)
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (c *Core) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	return c.sl.hc.GetUnclesInChain(block, length)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (c *Core) GetGasUsedInChain(block *types.Block, length int) int64 {
	return c.sl.hc.GetGasUsedInChain(block, length)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (c *Core) CalculateBaseFee(header *types.Header) *big.Int {
	return c.sl.hc.CalculateBaseFee(header)
}

// CurrentBlock returns the block for the current header.
func (c *Core) CurrentBlock() *types.Block {
	return c.sl.hc.CurrentBlock()
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (c *Core) CurrentHeader() *types.Header {
	return c.sl.hc.CurrentHeader()
}

// GetTd retrieves a block's total difficulty in the canonical chain from the
// database by hash and number, caching it if found.
func (c *Core) GetTd(hash common.Hash, number uint64) *big.Int {
	return c.sl.hc.GetTd(hash, number)
}

// GetTdByHash retrieves a block's total difficulty in the canonical chain from the
// database by hash, caching it if found.
func (c *Core) GetTdByHash(hash common.Hash) *big.Int {
	return c.sl.hc.GetTdByHash(hash)
}

// GetHeader retrieves a block header from the database by hash and number,
// caching it if found.
func (c *Core) GetHeader(hash common.Hash, number uint64) *types.Header {
	return c.sl.hc.GetHeader(hash, number)
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (c *Core) GetHeaderByHash(hash common.Hash) *types.Header {
	return c.sl.hc.GetHeaderByHash(hash)
}

// HasHeader checks if a block header is present in the database or not, caching
// it if present.
func (c *Core) HasHeader(hash common.Hash, number uint64) bool {
	return c.sl.hc.HasHeader(hash, number)
}

// GetCanonicalHash returns the canonical hash for a given block number
func (c *Core) GetCanonicalHash(number uint64) common.Hash {
	return c.sl.hc.GetCanonicalHash(number)
}

// GetBlockHashesFromHash retrieves a number of block hashes starting at a given
// hash, fetching towards the genesis block.
func (c *Core) GetBlockHashesFromHash(hash common.Hash, max uint64) []common.Hash {
	return c.sl.hc.GetBlockHashesFromHash(hash, max)
}

// GetAncestor retrieves the Nth ancestor of a given block. It assumes that either the given block or
// a close ancestor of it is canonical. maxNonCanonical points to a downwards counter limiting the
// number of blocks to be individually checked before we reach the canonical chain.
//
// Note: ancestor == 0 returns the same block, 1 returns its parent and so on.
func (c *Core) GetAncestor(hash common.Hash, number, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	return c.sl.hc.GetAncestor(hash, number, ancestor, maxNonCanonical)
}

// Genesis retrieves the chain's genesis block.
func (c *Core) Genesis() *types.Block {
	return c.GetBlockByHash(c.sl.hc.genesisHeader.Hash())
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (c *Core) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return c.sl.hc.SubscribeChainHeadEvent(ch)
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (c *Core) GetBody(hash common.Hash) *types.Body {
	return c.sl.hc.GetBody(hash)
}

// GetBodyRLP retrieves a block body in RLP encoding from the database by hash,
// caching it if found.
func (c *Core) GetBodyRLP(hash common.Hash) rlp.RawValue {
	return c.sl.hc.GetBodyRLP(hash)
}

func (c *Core) GetHorizon() uint64 {
	return c.sl.hc.GetHorizon()
}

//--------------------//
// BlockChain methods //
//--------------------//

// HasBlock checks if a block is fully present in the database or not.
func (c *Core) HasBlock(hash common.Hash, number uint64) bool {
	return c.sl.hc.bc.HasBlock(hash, number)
}

// SubscribeChainEvent registers a subscription of ChainEvent.
func (c *Core) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return c.sl.hc.bc.SubscribeChainEvent(ch)
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (c *Core) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return c.sl.hc.bc.SubscribeChainSideEvent(ch)
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (c *Core) SubscribeRemovedLogsEvent(ch chan<- RemovedLogsEvent) event.Subscription {
	return c.sl.hc.bc.SubscribeRemovedLogsEvent(ch)
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (c *Core) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return c.sl.hc.bc.SubscribeLogsEvent(ch)
}

// SubscribeBlockProcessingEvent registers a subscription of bool where true means
// block processing has started while false means it has stopped.
func (c *Core) SubscribeBlockProcessingEvent(ch chan<- bool) event.Subscription {
	return c.sl.hc.bc.SubscribeBlockProcessingEvent(ch)
}

// Export writes the active chain to the given writer.
func (c *Core) Export(w io.Writer) error {
	return c.sl.hc.Export(w)
}

// ExportN writes a subset of the active chain to the given writer.
func (c *Core) ExportN(w io.Writer, first uint64, last uint64) error {
	return c.sl.hc.ExportN(w, first, last)
}

// Snapshots returns the blockchain snapshot tree.
func (c *Core) Snapshots() *snapshot.Tree {
	return nil
}

// this needs to be implemented, it is being used by a lot of modules
func (c *Core) SetHead(number uint64) error {
	return nil
}

func (c *Core) TxLookupLimit() uint64 {
	return 0
}

func (c *Core) SubscribeNewTxsEvent(ch chan<- NewTxsEvent) event.Subscription {
	return c.sl.txPool.SubscribeNewTxsEvent(ch)
}

func (c *Core) SetExtra(extra []byte) error {
	return c.sl.miner.SetExtra(extra)
}

//---------------//
// Miner methods //
//---------------//

func (c *Core) Miner() *Miner {
	return c.sl.Miner()
}

func (c *Core) Hashrate() uint64 {
	if pow, ok := c.sl.engine.(consensus.PoW); ok {
		return uint64(pow.Hashrate())
	}
	return 0
}

func (c *Core) SetRecommitInterval(interval time.Duration) {
	c.sl.miner.SetRecommitInterval(interval)
}

// SetGasCeil sets the gaslimit to strive for when mining blocks post 1559.
// For pre-1559 blocks, it sets the ceiling.
func (c *Core) SetGasCeil(ceil uint64) {
	c.sl.miner.SetGasCeil(ceil)
}

// EnablePreseal turns on the preseal mining feature. It's enabled by default.
// Note this function shouldn't be exposed to API, it's unnecessary for users
// (miners) to actually know the underlying detail. It's only for outside project
// which uses this library.
func (c *Core) EnablePreseal() {
	c.sl.miner.EnablePreseal()
}

// DisablePreseal turns off the preseal mining feature. It's necessary for some
// fake consensus engine which can seal blocks instantaneously.
// Note this function shouldn't be exposed to API, it's unnecessary for users
// (miners) to actually know the underlying detail. It's only for outside project
// which uses this library.
func (c *Core) DisablePreseal() {
	c.sl.miner.DisablePreseal()
}

func (c *Core) StopMining() {
	c.sl.miner.StopMining()
}

// Pending returns the currently pending block and associated state.
func (c *Core) Pending() (*types.Block, *state.StateDB) {
	return c.sl.miner.Pending()
}

// PendingBlock returns the currently pending block.
//
// Note, to access both the pending block and the pending state
// simultaneously, please use Pending(), as the pending state can
// change between multiple method calls
func (c *Core) PendingBlock() *types.Block {
	return c.sl.miner.PendingBlock()
}

// PendingBlockAndReceipts returns the currently pending block and corresponding receipts.
func (c *Core) PendingBlockAndReceipts() (*types.Block, types.Receipts) {
	return c.sl.miner.PendingBlockAndReceipts()
}

// Method to retrieve uncles from the worker in case not found in normal DB.
func (c *Core) GetUncle(hash common.Hash) *types.Block {
	if uncle, exist := c.sl.miner.worker.localUncles[hash]; exist {
		return uncle
	}
	if uncle, exist := c.sl.miner.worker.remoteUncles[hash]; exist {
		return uncle
	}
	return nil
}

func (c *Core) SetEtherbase(addr common.Address) {
	c.sl.miner.SetEtherbase(addr)
}

// SubscribePendingLogs starts delivering logs from pending transactions
// to the given channel.
func (c *Core) SubscribePendingLogs(ch chan<- []*types.Log) event.Subscription {
	return c.sl.miner.worker.pendingLogsFeed.Subscribe(ch)
}

// SubscribePendingBlock starts delivering the pending block to the given channel.
func (c *Core) SubscribePendingHeader(ch chan<- *types.Header) event.Subscription {
	return c.sl.miner.SubscribePendingHeader(ch)
}

// SubscribeHeaderRoots starts delivering the header roots update to the given channel.
func (c *Core) SubscribeHeaderRoots(ch chan<- types.HeaderRoots) event.Subscription {
	return c.sl.miner.SubscribeHeaderRoots(ch)
}

func (c *Core) IsMining() bool { return c.sl.miner.Mining() }

//-------------------------//
// State Processor methods //
//-------------------------//

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (c *Core) GetReceiptsByHash(hash common.Hash) types.Receipts {
	return c.sl.hc.bc.processor.GetReceiptsByHash(hash)
}

// GetVMConfig returns the block chain VM config.
func (c *Core) GetVMConfig() *vm.Config {
	return &c.sl.hc.bc.processor.vmConfig
}

// GetTransactionLookup retrieves the lookup associate with the given transaction
// hash from the cache or database.
func (c *Core) GetTransactionLookup(hash common.Hash) *rawdb.LegacyTxLookupEntry {
	return c.sl.hc.bc.processor.GetTransactionLookup(hash)
}

func (c *Core) HasBlockAndState(hash common.Hash, number uint64) bool {
	return c.Processor().HasBlockAndState(hash, number)
}

// ContractCode retrieves a blob of data associated with a contract hash
// either from ephemeral in-memory cache, or from persistent storage.
func (c *Core) ContractCode(hash common.Hash) ([]byte, error) {
	return c.sl.hc.bc.processor.ContractCode(hash)
}

// State returns a new mutable state based on the current HEAD block.
func (c *Core) State() (*state.StateDB, error) {
	return c.sl.hc.bc.processor.State()
}

// StateAt returns a new mutable state based on a particular point in time.
func (c *Core) StateAt(root common.Hash) (*state.StateDB, error) {
	return state.New(root, c.sl.hc.bc.processor.stateCache, nil)
}

// StateCache returns the caching database underpinning the blockchain instance.
func (c *Core) StateCache() state.Database {
	return c.sl.hc.bc.processor.stateCache
}

// ContractCodeWithPrefix retrieves a blob of data associated with a contract
// hash either from ephemeral in-memory cache, or from persistent storage.
//
// If the code doesn't exist in the in-memory cache, check the storage with
// new code scheme.
func (c *Core) ContractCodeWithPrefix(hash common.Hash) ([]byte, error) {
	return c.sl.hc.bc.processor.ContractCodeWithPrefix(hash)
}
func (c *Core) StateAtBlock(block *types.Block, reexec uint64, base *state.StateDB, checkLive bool) (statedb *state.StateDB, err error) {
	return c.sl.hc.bc.processor.StateAtBlock(block, reexec, base, checkLive)
}

func (c *Core) StateAtTransaction(block *types.Block, txIndex int, reexec uint64) (Message, vm.BlockContext, *state.StateDB, error) {
	return c.sl.hc.bc.processor.StateAtTransaction(block, txIndex, reexec)
}

func (c *Core) TrieNode(hash common.Hash) ([]byte, error) {
	return c.sl.hc.bc.processor.TrieNode(hash)
}

//----------------//
// TxPool methods //
//----------------//

func (c *Core) SetGasPrice(price *big.Int) {
	c.sl.txPool.SetGasPrice(price)
}

func (c *Core) AddLocal(tx *types.Transaction) error {
	return c.sl.txPool.AddLocal(tx)
}

func (c *Core) TxPoolPending(enforceTips bool) (map[common.Address]types.Transactions, error) {
	return c.sl.txPool.TxPoolPending(enforceTips, nil)
}

func (c *Core) Get(hash common.Hash) *types.Transaction {
	return c.sl.txPool.Get(hash)
}

func (c *Core) Nonce(addr common.Address) uint64 {
	return c.sl.txPool.Nonce(addr)
}

func (c *Core) Stats() (int, int) {
	return c.sl.txPool.Stats()
}

func (c *Core) Content() (map[common.Address]types.Transactions, map[common.Address]types.Transactions) {
	return c.sl.txPool.Content()
}

func (c *Core) ContentFrom(addr common.Address) (types.Transactions, types.Transactions) {
	return c.sl.txPool.ContentFrom(addr)
}
