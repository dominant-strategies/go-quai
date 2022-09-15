package core

import (
	"io"
	"math/big"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/state"
	"github.com/spruce-solutions/go-quai/core/state/snapshot"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/event"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/params"
	"github.com/spruce-solutions/go-quai/rlp"
)

type Core struct {
	sl     *Slice
	engine consensus.Engine
}

func NewCore(db ethdb.Database, config *Config, isLocalBlock func(block *types.Header) bool, txConfig *TxPoolConfig, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis) (*Core, error) {
	slice, err := NewSlice(db, config, txConfig, isLocalBlock, chainConfig, domClientUrl, subClientUrls, engine, cacheConfig, vmConfig, genesis)
	if err != nil {
		return nil, err
	}

	return &Core{
		sl:     slice,
		engine: engine,
	}, nil
}

// TODO
func (c *Core) InsertChain(blocks types.Blocks) (int, error) {
	domWait := false
	for i, block := range blocks {
		// check the order of the block
		blockOrder, err := c.engine.GetDifficultyOrder(block.Header())
		if err != nil {
			return i, err
		}

		// Write the block body to the db.
		rawdb.WritePendingBlockBody(c.sl.sliceDb, block.Header().Root[types.QuaiNetworkContext], block.Body())

		// if the order of the block is less than the context
		// add the rest of the blocks in the queue to the future blocks.
		if blockOrder == types.QuaiNetworkContext && !domWait {
			_, err = c.sl.Append(block.Header(), common.Hash{}, big.NewInt(0), false, true)
			if err != nil {
				if err == consensus.ErrFutureBlock {
					c.sl.addfutureHeader(block.Header())
				}
				log.Info("InsertChain", "err in Append core: ", err)
				return i, err
			}
		} else {
			domWait = true
			c.sl.addfutureHeader(block.Header())
		}
	}
	return len(blocks), nil
}

// ConstructLocalBlock takes a header and construct the Block locally
func (c *Core) ConstructLocalBlock(header *types.Header) *types.Block {
	return c.sl.ConstructLocalBlock(header)
}

func (c *Core) InsertHeaderChain(headers []*types.Header, checkFreq int) (int, error) {
	return 0, nil
}

func (c *Core) InsertReceiptChain(blocks types.Blocks, receipts []types.Receipts, ancientLimit uint64) (int, error) {
	return 0, nil
}

// InsertChainWithoutSealVerification works exactly the same
// except for seal verification, seal verification is omitted
func (c *Core) InsertChainWithoutSealVerification(block *types.Block) (int, error) {
	return 0, nil
}

func (c *Core) SetTxLookupLimit(limit uint64) {
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

func (c *Core) StopInsert() {
	c.sl.hc.StopInsert()
}

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

// GetTransactionLookup retrieves the lookup associate with the given transaction
// hash from the cache or database.
func (c *Core) GetTransactionLookup(hash common.Hash) *rawdb.LegacyTxLookupEntry {
	return c.sl.hc.bc.processor.GetTransactionLookup(hash)
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

// HasBlock checks if a block is fully present in the database or not.
func (c *Core) HasBlock(hash common.Hash, number uint64) bool {
	return c.sl.hc.bc.HasBlock(hash, number)
}

// HasBlock checks if a block is fully present in the database or not.
func (c *Core) HasFastBlock(hash common.Hash, number uint64) bool {
	return c.sl.hc.bc.HasBlock(hash, number)
}

// HasHeader checks if a block header is present in the database or not, caching
// it if present.
func (c *Core) HasHeader(hash common.Hash, number uint64) bool {
	return c.sl.hc.HasHeader(hash, number)
}

func (c *Core) HasBlockAndState(hash common.Hash, number uint64) bool {
	return c.Processor().HasBlockAndState(hash, number)
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

// GetAncestorWithLocation retrieves the first occurrence of a block with a given location from a given block.
//
// Note: location == hash location returns the same block.
func (c *Core) GetAncestorByLocation(hash common.Hash, location []byte) (*types.Header, error) {
	return c.sl.hc.GetAncestorByLocation(hash, location)
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

// Genesis retrieves the chain's genesis block.
func (c *Core) Genesis() *types.Block {
	return c.GetBlockByHash(c.sl.hc.genesisHeader.Hash())
}

func (c *Core) Stop() {
	c.sl.Stop()
}

// SubscribeChainEvent registers a subscription of ChainEvent.
func (c *Core) SubscribeChainEvent(ch chan<- ChainEvent) event.Subscription {
	return c.sl.hc.bc.SubscribeChainEvent(ch)
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (c *Core) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return c.sl.hc.SubscribeChainHeadEvent(ch)
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (c *Core) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return c.sl.hc.bc.SubscribeChainSideEvent(ch)
}

// GetDifficultyOrder determines the difficulty order of the given header.
func (c *Core) GetDifficultyOrder(header *types.Header) (int, error) {
	return c.sl.engine.GetDifficultyOrder(header)
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

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (c *Core) GetReceiptsByHash(hash common.Hash) types.Receipts {
	return c.sl.hc.bc.processor.GetReceiptsByHash(hash)
}

// GetVMConfig returns the block chain VM config.
func (c *Core) GetVMConfig() *vm.Config {
	return &c.sl.hc.bc.processor.vmConfig
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

// this needs to be deleted
func (c *Core) CurrentFastBlock() *types.Block {
	return c.CurrentBlock()
}

// this needs to be implemented, it is being used by a lot of modules
func (c *Core) SetHead(number uint64) error {
	return nil
}

func (c *Core) GetTerminusAtOrder(header *types.Header, order int) (common.Hash, error) {
	return common.Hash{}, nil
}

func (c *Core) Append(header *types.Header, domTerminus common.Hash, td *big.Int, domOrigin bool, reorg bool) (types.PendingHeader, error) {
	return c.sl.Append(header, domTerminus, td, domOrigin, reorg)
}

func (c *Core) SubRelayPendingHeader(slPendingHeader types.PendingHeader, location []byte, reorg bool) error {
	return c.sl.SubRelayPendingHeader(slPendingHeader, location, reorg)
}

func (c *Core) GetPendingHeader() (*types.Header, error) {
	return c.sl.GetPendingHeader()
}

func (c *Core) TxLookupLimit() uint64 {
	return 0
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

func (c *Core) PendingBlockBody(hash common.Hash) *types.Body {
	return c.sl.PendingBlockBody(hash)
}

// PendingBlockAndReceipts returns the currently pending block and corresponding receipts.
func (c *Core) PendingBlockAndReceipts() (*types.Block, types.Receipts) {
	return c.sl.miner.PendingBlockAndReceipts()
}

func (c *Core) Hashrate() uint64 {
	if pow, ok := c.sl.engine.(consensus.PoW); ok {
		return uint64(pow.Hashrate())
	}
	return 0
}

func (c *Core) Miner() *Miner {
	return c.sl.Miner()
}

func (c *Core) StateAtBlock(block *types.Block, reexec uint64, base *state.StateDB, checkLive bool, preferDisk bool) (statedb *state.StateDB, err error) {
	return c.sl.hc.bc.processor.StateAtBlock(block, reexec, base, checkLive, preferDisk)
}

func (c *Core) StateAtTransaction(block *types.Block, txIndex int, reexec uint64) (Message, vm.BlockContext, *state.StateDB, error) {
	return c.sl.hc.bc.processor.StateAtTransaction(block, txIndex, reexec)
}
