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

	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/event"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/trie"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/rawdb"
	"github.com/spruce-solutions/go-quai/core/state"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/crypto"
	"github.com/spruce-solutions/go-quai/params"
)

// StateProcessor is a basic Processor, which takes care of transitioning
// state from one point to another.
//
// StateProcessor implements Processor.
type StateProcessor struct {
	config *params.ChainConfig // Chain configuration options
	bc     *BlockChain         // Canonical block chain
	engine consensus.Engine    // Consensus engine used for block rewards
}

// NewStateProcessor initialises a new StateProcessor.
func NewStateProcessor(config *params.ChainConfig, bc *BlockChain, engine consensus.Engine) *StateProcessor {
	return &StateProcessor{
		config: config,
		bc:     bc,
		engine: engine,
	}
}

// Process processes the state changes according to the Ethereum rules by running
// the transaction messages using the statedb and applying any rewards to both
// the processor (coinbase) and any included uncles.
//
// Process returns the receipts and logs accumulated during the process and
// returns the amount of gas that was used in the process. If any of the
// transactions failed to execute due to insufficient gas it will return an error.
func (p *StateProcessor) Process(block *types.Block, statedb *state.StateDB, cfg vm.Config) (types.Receipts, []*types.Log, uint64, []*types.ExternalBlock, error) {
	var (
		receipts    types.Receipts
		usedGas     = new(uint64)
		header      = block.Header()
		blockHash   = block.Hash()
		blockNumber = block.Number()
		allLogs     []*types.Log
		gp          = new(GasPool).AddGas(block.GasLimit())
	)

	blockContext := NewEVMBlockContext(header, p.bc, nil)
	vmenv := vm.NewEVM(blockContext, vm.TxContext{}, statedb, p.config, cfg)

	// Gather external blocks and apply transactions, need to trace own local external block cache based on cache to validate.
	i := 0
	externalBlocks, err := p.engine.GetExternalBlocks(p.bc, header, true)
	if err != nil {
		return nil, nil, uint64(0), nil, err
	}

	etxs := 0
	for _, externalBlock := range externalBlocks {
		externalBlock.Receipts().DeriveFields(p.config, externalBlock.Hash(), externalBlock.Header().Number[externalBlock.Context().Int64()].Uint64(), externalBlock.Transactions())

		hashedTxList := types.DeriveSha(externalBlock.Transactions(), trie.NewStackTrie(nil))
		if externalBlock.Header().TxHash[externalBlock.Context().Int64()] != hashedTxList {
			fmt.Println("Bad external block: Transaction hash not equal to txs", externalBlock.Header().TxHash[externalBlock.Context().Int64()], hashedTxList)
			return nil, nil, uint64(0), nil, fmt.Errorf("bad external block: transaction hash not equal to txs %v, %v", externalBlock.Header().TxHash[externalBlock.Context().Int64()], hashedTxList)
		}

		for _, tx := range externalBlock.Transactions() {
			msg, err := tx.AsMessage(types.MakeSigner(p.config, header.Number[types.QuaiNetworkContext]), header.BaseFee[types.QuaiNetworkContext])
			// Quick check to make sure we're adding an external transaction, currently saves us from not passing merkel path in external block
			if err != nil {
				return nil, nil, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
			}

			if !msg.FromExternal() || !params.CheckETxChainID(p.config.ChainID, tx.ChainId()) {
				continue
			}
			fmt.Println("Applying etx", tx.Hash().Hex(), msg.From(), msg.To(), msg.Value())
			statedb.Prepare(tx.Hash(), i)
			receipt, err := applyExternalTransaction(msg, p.config, p.bc, nil, gp, statedb, blockNumber, blockHash, externalBlock, tx, usedGas, vmenv)
			if err != nil {
				log.Warn("Could not apply etx", "i", i, "hash", tx.Hash().Hex(), "err", err)
				return nil, nil, uint64(0), nil, err
			}
			receipts = append(receipts, receipt)
			allLogs = append(allLogs, receipt.Logs...)
			etxs += 1
			i++
		}
	}

	// Iterate over and process the individual transactions.
	for _, tx := range block.Transactions() {
		msg, err := tx.AsMessage(types.MakeSigner(p.config, header.Number[types.QuaiNetworkContext]), header.BaseFee[types.QuaiNetworkContext])
		if err != nil {
			return nil, nil, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		// All ETxs applied to state must be generated from our cache.
		if msg.FromExternal() {
			continue
		}
		statedb.Prepare(tx.Hash(), i)
		receipt, err := applyTransaction(msg, p.config, p.bc, nil, gp, statedb, blockNumber, blockHash, tx, usedGas, vmenv)
		if err != nil {
			return nil, nil, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		receipts = append(receipts, receipt)
		allLogs = append(allLogs, receipt.Logs...)
		i++
	}

	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	p.engine.Finalize(p.bc, header, statedb, block.Transactions(), block.Uncles())

	return receipts, allLogs, *usedGas, externalBlocks, nil
}

func applyTransaction(msg types.Message, config *params.ChainConfig, bc ChainContext, author *common.Address, gp *GasPool, statedb *state.StateDB, blockNumber *big.Int, blockHash common.Hash, tx *types.Transaction, usedGas *uint64, evm *vm.EVM) (*types.Receipt, error) {
	if config.ChainID.Cmp(tx.ChainId()) != 0 {
		return nil, ErrSenderInoperable
	}

	// Validate Address Operability
	idRange := config.ChainIDRange()
	if int(msg.From().Bytes()[0]) < idRange[0] || int(msg.From().Bytes()[0]) > idRange[1] {
		return nil, ErrSenderInoperable
	}

	// Create a new context to be used in the EVM environment.
	txContext := NewEVMTxContext(msg)
	evm.Reset(txContext, statedb)

	// Apply the transaction to the current state (included in the env).
	result, err := ApplyMessage(evm, msg, gp)
	if err != nil {
		return nil, err
	}

	// Update the state with pending changes.
	var root []byte
	if config.IsByzantium(blockNumber) {
		statedb.Finalise(true)
	} else {
		root = statedb.IntermediateRoot(config.IsEIP158(blockNumber)).Bytes()
	}
	*usedGas += result.UsedGas

	// Create a new receipt for the transaction, storing the intermediate root and gas used
	// by the tx.
	receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: *usedGas}
	if result.Failed() {
		receipt.Status = types.ReceiptStatusFailed
	} else {
		receipt.Status = types.ReceiptStatusSuccessful
	}
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = result.UsedGas

	// If the transaction created a contract, store the creation address in the receipt.
	if msg.To() == nil {
		receipt.ContractAddress = crypto.CreateAddress(evm.TxContext.Origin, tx.Nonce())
	}

	// Set the receipt logs and create the bloom filter.
	receipt.Logs = statedb.GetLogs(tx.Hash(), blockHash)
	receipt.Bloom = types.CreateBloom(types.Receipts{receipt})
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())
	return receipt, err
}

// ApplyTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. It returns the receipt
// for the transaction, gas used and an error if the transaction failed,
// indicating the block was invalid.
func ApplyTransaction(config *params.ChainConfig, bc ChainContext, author *common.Address, gp *GasPool, statedb *state.StateDB, header *types.Header, tx *types.Transaction, usedGas *uint64, cfg vm.Config) (*types.Receipt, error) {
	if header.BaseFee == nil {
		return nil, errors.New("header BaseFee is nil")
	}

	if header.Number == nil {
		return nil, errors.New("header number is nil")
	}

	if tx == nil {
		return nil, errors.New("tx is nil")
	}

	msg, err := tx.AsMessage(types.MakeSigner(config, header.Number[types.QuaiNetworkContext]), header.BaseFee[types.QuaiNetworkContext])
	if err != nil {
		return nil, err
	}
	// Create a new context to be used in the EVM environment
	blockContext := NewEVMBlockContext(header, bc, author)
	vmenv := vm.NewEVM(blockContext, vm.TxContext{}, statedb, config, cfg)
	return applyTransaction(msg, config, bc, author, gp, statedb, header.Number[types.QuaiNetworkContext], header.Hash(), tx, usedGas, vmenv)
}

func applyExternalTransaction(msg types.Message, config *params.ChainConfig, bc ChainContext, author *common.Address, gp *GasPool, statedb *state.StateDB, blockNumber *big.Int, blockHash common.Hash, externalBlock *types.ExternalBlock, tx *types.Transaction, usedGas *uint64, evm *vm.EVM) (*types.Receipt, error) {
	// Create a new context to be used in the EVM environment.
	txContext := NewEVMTxContext(msg)
	evm.Reset(txContext, statedb)
	receipt := externalBlock.ReceiptForTransaction(tx)
	if receipt.Status != 1 {
		return nil, errors.New("receipt status not 1")
	}

	// Triple check we are from external
	if !msg.FromExternal() {
		return nil, errors.New("not an external transaction")
	}

	// Apply the transaction to the current state (included in the env).
	statedb.AddBalance(msg.From(), msg.Value())
	statedb.AddBalance(*msg.To(), msg.Value())

	// Update the state with pending changes.
	if config.IsByzantium(blockNumber) {
		statedb.Finalise(true)
	} else {
		statedb.IntermediateRoot(config.IsEIP158(blockNumber)).Bytes()
	}

	return receipt, nil
}

// ApplyTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. It returns the receipt
// for the transaction, gas used and an error if the transaction failed,
// indicating the block was invalid.
func ApplyExternalTransaction(config *params.ChainConfig, bc ChainContext, author *common.Address, gp *GasPool, statedb *state.StateDB, header *types.Header, externalBlock *types.ExternalBlock, tx *types.Transaction, usedGas *uint64, cfg vm.Config) (*types.Receipt, error) {
	s := types.MakeSigner(config, header.Number[types.QuaiNetworkContext])

	msg, err := tx.AsMessage(s, header.BaseFee[types.QuaiNetworkContext])
	if err != nil {
		return nil, err
	}

	// Validate address origination did not occur in our current chain
	idRange := config.ChainIDRange()
	if int(msg.From().Bytes()[0]) >= idRange[0] && int(msg.From().Bytes()[0]) <= idRange[1] {
		return nil, ErrSenderInoperable
	}

	// Create a new context to be used in the EVM environment
	blockContext := NewEVMBlockContext(header, bc, author)
	vmenv := vm.NewEVM(blockContext, vm.TxContext{}, statedb, config, cfg)
	return applyExternalTransaction(msg, config, bc, author, gp, statedb, header.Number[types.QuaiNetworkContext], header.Hash(), externalBlock, tx, usedGas, vmenv)
}

// GetVMConfig returns the block chain VM config.
func (bc *BlockChain) GetVMConfig() *vm.Config {
	return &bc.vmConfig
}

// Processor returns the current processor.
func (bc *BlockChain) Processor() Processor {
	return bc.processor
}

// State returns a new mutable state based on the current HEAD block.
func (bc *BlockChain) State() (*state.StateDB, error) {
	return bc.StateAt(bc.CurrentBlock().Root())
}

// StateAt returns a new mutable state based on a particular point in time.
func (bc *BlockChain) StateAt(root common.Hash) (*state.StateDB, error) {
	return state.New(root, bc.stateCache, bc.snaps)
}

// StateCache returns the caching database underpinning the blockchain instance.
func (bc *BlockChain) StateCache() state.Database {
	return bc.stateCache
}

// HasState checks if state trie is fully present in the database or not.
func (bc *BlockChain) HasState(hash common.Hash) bool {
	_, err := bc.stateCache.OpenTrie(hash)
	return err == nil
}

// HasBlockAndState checks if a block and associated state trie is fully present
// in the database or not, caching it if present.
func (bc *BlockChain) HasBlockAndState(hash common.Hash, number uint64) bool {
	// Check first that the block itself is known
	block := bc.GetBlock(hash, number)
	if block == nil {
		return false
	}
	return bc.HasState(block.Root())
}

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (bc *BlockChain) GetReceiptsByHash(hash common.Hash) types.Receipts {
	if receipts, ok := bc.receiptsCache.Get(hash); ok {
		return receipts.(types.Receipts)
	}
	number := rawdb.ReadHeaderNumber(bc.db, hash)
	if number == nil {
		return nil
	}
	receipts := rawdb.ReadReceipts(bc.db, hash, *number, bc.chainConfig)
	if receipts == nil {
		return nil
	}
	bc.receiptsCache.Add(hash, receipts)
	return receipts
}

// TrieNode retrieves a blob of data associated with a trie node
// either from ephemeral in-memory cache, or from persistent storage.
func (bc *BlockChain) TrieNode(hash common.Hash) ([]byte, error) {
	return bc.stateCache.TrieDB().Node(hash)
}

// ContractCode retrieves a blob of data associated with a contract hash
// either from ephemeral in-memory cache, or from persistent storage.
func (bc *BlockChain) ContractCode(hash common.Hash) ([]byte, error) {
	return bc.stateCache.ContractCode(common.Hash{}, hash)
}

// ContractCodeWithPrefix retrieves a blob of data associated with a contract
// hash either from ephemeral in-memory cache, or from persistent storage.
//
// If the code doesn't exist in the in-memory cache, check the storage with
// new code scheme.
func (bc *BlockChain) ContractCodeWithPrefix(hash common.Hash) ([]byte, error) {
	type codeReader interface {
		ContractCodeWithPrefix(addrHash, codeHash common.Hash) ([]byte, error)
	}
	return bc.stateCache.(codeReader).ContractCodeWithPrefix(common.Hash{}, hash)
}

// writeBlockWithState writes the block and all associated state to the database,
// but is expects the chain mutex to be held.
func (bc *BlockChain) writeBlockWithState(block *types.Block, receipts []*types.Receipt, logs []*types.Log, state *state.StateDB, linkExtBlocks []*types.ExternalBlock) error {

	fmt.Println("calcTd from wbws")
	externTd, err := bc.CalcTd(block.Header())
	if err != nil {
		return err
	}
	// Irrelevant of the canonical status, write the block itself to the database.
	//
	// Note all the components of block(td, hash->number map, header, body, receipts)
	// should be written atomically. BlockBatch is used for containing all components.
	blockBatch := bc.db.NewBatch()
	rawdb.WriteTd(blockBatch, block.Hash(), block.NumberU64(), externTd)
	rawdb.WriteBlock(blockBatch, block)
	rawdb.WriteReceipts(blockBatch, block.Hash(), block.NumberU64(), receipts)
	rawdb.WritePreimages(blockBatch, state.Preimages())
	if err := blockBatch.Write(); err != nil {
		log.Crit("Failed to write block into disk", "err", err)
	}
	// Commit all cached state changes into underlying memory database.
	root, err := state.Commit(bc.chainConfig.IsEIP158(block.Number()))
	if err != nil {
		return err
	}
	triedb := bc.stateCache.TrieDB()

	// If we're running an archive node, always flush
	if bc.cacheConfig.TrieDirtyDisabled {
		if err := triedb.Commit(root, false, nil); err != nil {
			return err
		}
	} else {
		// Full but not archive node, do proper garbage collection
		triedb.Reference(root, common.Hash{}) // metadata reference to keep trie alive
		bc.triegc.Push(root, -int64(block.NumberU64()))

		if current := block.NumberU64(); current > TriesInMemory {
			// If we exceeded our memory allowance, flush matured singleton nodes to disk
			var (
				nodes, imgs = triedb.Size()
				limit       = common.StorageSize(bc.cacheConfig.TrieDirtyLimit) * 1024 * 1024
			)
			if nodes > limit || imgs > 4*1024*1024 {
				triedb.Cap(limit - ethdb.IdealBatchSize)
			}
			// Find the next state trie we need to commit
			chosen := current - TriesInMemory

			// If we exceeded out time allowance, flush an entire trie to disk
			if bc.gcproc > bc.cacheConfig.TrieTimeLimit {
				// If the header is missing (canonical chain behind), we're reorging a low
				// diff sidechain. Suspend committing until this operation is completed.
				header := bc.GetHeaderByNumber(chosen)
				if header == nil {
					log.Warn("Reorg in progress, trie commit postponed", "number", chosen)
				} else {
					// If we're exceeding limits but haven't reached a large enough memory gap,
					// warn the user that the system is becoming unstable.
					if chosen < lastWrite+TriesInMemory && bc.gcproc >= 2*bc.cacheConfig.TrieTimeLimit {
						log.Info("State in memory for too long, committing", "time", bc.gcproc, "allowance", bc.cacheConfig.TrieTimeLimit, "optimum", float64(chosen-lastWrite)/TriesInMemory)
					}
					// Flush an entire trie and restart the counters
					triedb.Commit(header.Root[types.QuaiNetworkContext], true, nil)
					lastWrite = chosen
					bc.gcproc = 0
				}
			}
			// Garbage collect anything below our required write retention
			for !bc.triegc.Empty() {
				root, number := bc.triegc.Pop()
				if uint64(-number) > chosen {
					bc.triegc.Push(root, number)
					break
				}
				triedb.Dereference(root.(common.Hash))
			}
		}
	}
	return nil
}

// collectLogs collects the logs that were generated or removed during
// the processing of the block that corresponds with the given hash.
// These logs are later announced as deleted or reborn.
func (bc *BlockChain) collectLogs(hash common.Hash, removed bool) []*types.Log {
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	receipts := rawdb.ReadReceipts(bc.db, hash, *number, bc.chainConfig)

	var logs []*types.Log
	for _, receipt := range receipts {
		for _, log := range receipt.Logs {
			l := *log
			if removed {
				l.Removed = true
			}
			logs = append(logs, &l)
		}
	}
	return logs
}

// mergeLogs returns a merged log slice with specified sort order.
func mergeLogs(logs [][]*types.Log, reverse bool) []*types.Log {
	var ret []*types.Log
	if reverse {
		for i := len(logs) - 1; i >= 0; i-- {
			ret = append(ret, logs[i]...)
		}
	} else {
		for i := 0; i < len(logs); i++ {
			ret = append(ret, logs[i]...)
		}
	}
	return ret
}

// reorg takes two blocks, an old chain and a new chain and will reconstruct the
// blocks and inserts them to be part of the new canonical chain and accumulates
// potential missing transactions and post an event about them.
func (bc *BlockChain) reorg(oldBlock, newBlock *types.Block) error {
	var (
		newChain    types.Blocks
		oldChain    types.Blocks
		commonBlock *types.Block

		deletedTxs types.Transactions
		addedTxs   types.Transactions

		deletedLogs [][]*types.Log
		rebirthLogs [][]*types.Log
	)

	// Reduce the longer chain to the same number as the shorter one
	if oldBlock.NumberU64() > newBlock.NumberU64() {
		// Old chain is longer, gather all transactions and logs as deleted ones
		for ; oldBlock != nil && oldBlock.NumberU64() != newBlock.NumberU64(); oldBlock = bc.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1) {
			oldChain = append(oldChain, oldBlock)
			deletedTxs = append(deletedTxs, oldBlock.Transactions()...)

			// Collect deleted logs for notification
			logs := bc.collectLogs(oldBlock.Hash(), true)
			if len(logs) > 0 {
				deletedLogs = append(deletedLogs, logs)
			}
		}
	} else {
		// New chain is longer, stash all blocks away for subsequent insertion
		for ; newBlock != nil && newBlock.NumberU64() != oldBlock.NumberU64(); newBlock = bc.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1) {
			newChain = append(newChain, newBlock)
		}
	}
	if oldBlock == nil {
		return fmt.Errorf("invalid old chain")
	}
	if newBlock == nil {
		return fmt.Errorf("invalid new chain")
	}
	// Both sides of the reorg are at the same number, reduce both until the common
	// ancestor is found
	for {
		// If the common ancestor was found, bail out
		if oldBlock.Hash() == newBlock.Hash() {
			commonBlock = oldBlock

			break
		}
		// Remove an old block as well as stash away a new block
		oldChain = append(oldChain, oldBlock)
		deletedTxs = append(deletedTxs, oldBlock.Transactions()...)

		// Collect deleted logs for notification
		logs := bc.collectLogs(oldBlock.Hash(), true)
		if len(logs) > 0 {
			deletedLogs = append(deletedLogs, logs)
		}

		newChain = append(newChain, newBlock)

		// Step back with both chains
		oldBlock = bc.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1)
		if oldBlock == nil {
			return fmt.Errorf("invalid old chain")
		}
		newBlock = bc.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1)
		if newBlock == nil {
			return fmt.Errorf("invalid new chain")
		}
	}
	// Ensure the user sees large reorgs
	if len(oldChain) > 0 && len(newChain) > 0 {
		logFn := log.Info
		msg := "Chain reorg detected"
		if len(oldChain) > 63 {
			msg = "Large chain reorg detected"
			logFn = log.Warn
		}
		logFn(msg, "number", commonBlock.Number(), "hash", commonBlock.Hash(),
			"drop", len(oldChain), "dropfrom", oldChain[0].Hash(), "add", len(newChain), "addfrom", newChain[0].Hash())
		blockReorgAddMeter.Mark(int64(len(newChain)))
		blockReorgDropMeter.Mark(int64(len(oldChain)))
		blockReorgMeter.Mark(1)
	} else {
		log.Error("Impossible reorg, please file an issue", "oldnum", oldBlock.Number(), "oldhash", oldBlock.Hash(), "newnum", newBlock.Number(), "newhash", newBlock.Hash())
	}
	// Insert the new chain(except the head block(reverse order)),
	// taking care of the proper incremental order.
	for i := len(newChain) - 1; i >= 1; i-- {
		// Insert the block in the canonical way, re-writing history
		bc.writeHeadBlock(newChain[i])

		// Collect reborn logs due to chain reorg
		logs := bc.collectLogs(newChain[i].Hash(), false)
		if len(logs) > 0 {
			rebirthLogs = append(rebirthLogs, logs)
		}

		// Collect the new added transactions.
		addedTxs = append(addedTxs, newChain[i].Transactions()...)
	}
	// Delete useless indexes right now which includes the non-canonical
	// transaction indexes, canonical chain indexes which above the head.
	indexesBatch := bc.db.NewBatch()
	for _, tx := range types.TxDifference(deletedTxs, addedTxs) {
		rawdb.DeleteTxLookupEntry(indexesBatch, tx.Hash())
	}
	// Delete any canonical number assignments above the new head
	number := bc.CurrentBlock().NumberU64()
	for i := number + 1; ; i++ {
		hash := rawdb.ReadCanonicalHash(bc.db, i)
		if hash == (common.Hash{}) {
			break
		}
		rawdb.DeleteCanonicalHash(indexesBatch, i)
	}
	if err := indexesBatch.Write(); err != nil {
		log.Crit("Failed to delete useless indexes", "err", err)
	}
	// If any logs need to be fired, do it now. In theory we could avoid creating
	// this goroutine if there are no events to fire, but realistcally that only
	// ever happens if we're reorging empty blocks, which will only happen on idle
	// networks where performance is not an issue either way.
	if len(deletedLogs) > 0 {
		bc.rmLogsFeed.Send(RemovedLogsEvent{mergeLogs(deletedLogs, true)})
	}
	if len(rebirthLogs) > 0 {
		bc.logsFeed.Send(mergeLogs(rebirthLogs, false))
	}
	if len(oldChain) > 0 {
		for i := len(oldChain) - 1; i >= 0; i-- {
			bc.chainSideFeed.Send(ChainSideEvent{Block: oldChain[i]})
		}
	}
	// Once the common block is found, the reorg data is sent to the reOrg feed
	bc.reOrgFeed.Send(ReOrgRollup{ReOrgHeader: commonBlock.Header(), OldChainHeaders: bc.getAllHeaders(oldChain), NewChainHeaders: bc.getAllHeaders(newChain)})

	return nil
}

// skipBlock returns 'true', if the block being imported can be skipped over, meaning
// that the block does not need to be processed but can be considered already fully 'done'.
func (bc *BlockChain) skipBlock(err error, it *insertIterator) bool {
	// We can only ever bypass processing if the only error returned by the validator
	// is ErrKnownBlock, which means all checks passed, but we already have the block
	// and state.
	if !errors.Is(err, ErrKnownBlock) {
		return false
	}
	// If we're not using snapshots, we can skip this, since we have both block
	// and (trie-) state
	if bc.snaps == nil {
		return true
	}
	var (
		header     = it.current() // header can't be nil
		parentRoot common.Hash
	)
	// If we also have the snapshot-state, we can skip the processing.
	if bc.snaps.Snapshot(header.Root[types.QuaiNetworkContext]) != nil {
		return true
	}
	// In this case, we have the trie-state but not snapshot-state. If the parent
	// snapshot-state exists, we need to process this in order to not get a gap
	// in the snapshot layers.
	// Resolve parent block
	if parent := it.previous(); parent != nil {
		parentRoot = parent.Root[types.QuaiNetworkContext]
	} else if parent = bc.GetHeaderByHash(header.ParentHash[types.QuaiNetworkContext]); parent != nil {
		parentRoot = parent.Root[types.QuaiNetworkContext]
	}
	if parentRoot == (common.Hash{}) {
		return false // Theoretically impossible case
	}
	// Parent is also missing snapshot: we can skip this. Otherwise process.
	if bc.snaps.Snapshot(parentRoot) == nil {
		return true
	}
	return false
}

// maintainTxIndex is responsible for the construction and deletion of the
// transaction index.
//
// User can use flag `txlookuplimit` to specify a "recentness" block, below
// which ancient tx indices get deleted. If `txlookuplimit` is 0, it means
// all tx indices will be reserved.
//
// The user can adjust the txlookuplimit value for each launch after fast
// sync, Geth will automatically construct the missing indices and delete
// the extra indices.
func (bc *BlockChain) maintainTxIndex(ancients uint64) {
	defer bc.wg.Done()

	// Before starting the actual maintenance, we need to handle a special case,
	// where user might init Geth with an external ancient database. If so, we
	// need to reindex all necessary transactions before starting to process any
	// pruning requests.
	if ancients > 0 {
		var from = uint64(0)
		if bc.txLookupLimit != 0 && ancients > bc.txLookupLimit {
			from = ancients - bc.txLookupLimit
		}
		rawdb.IndexTransactions(bc.db, from, ancients, bc.quit)
	}
	// indexBlocks reindexes or unindexes transactions depending on user configuration
	indexBlocks := func(tail *uint64, head uint64, done chan struct{}) {
		defer func() { done <- struct{}{} }()

		// If the user just upgraded Geth to a new version which supports transaction
		// index pruning, write the new tail and remove anything older.
		if tail == nil {
			if bc.txLookupLimit == 0 || head < bc.txLookupLimit {
				// Nothing to delete, write the tail and return
				rawdb.WriteTxIndexTail(bc.db, 0)
			} else {
				// Prune all stale tx indices and record the tx index tail
				rawdb.UnindexTransactions(bc.db, 0, head-bc.txLookupLimit+1, bc.quit)
			}
			return
		}
		// If a previous indexing existed, make sure that we fill in any missing entries
		if bc.txLookupLimit == 0 || head < bc.txLookupLimit {
			if *tail > 0 {
				rawdb.IndexTransactions(bc.db, 0, *tail, bc.quit)
			}
			return
		}
		// Update the transaction index to the new chain state
		if head-bc.txLookupLimit+1 < *tail {
			// Reindex a part of missing indices and rewind index tail to HEAD-limit
			rawdb.IndexTransactions(bc.db, head-bc.txLookupLimit+1, *tail, bc.quit)
		} else {
			// Unindex a part of stale indices and forward index tail to HEAD-limit
			rawdb.UnindexTransactions(bc.db, *tail, head-bc.txLookupLimit+1, bc.quit)
		}
	}
	// Any reindexing done, start listening to chain events and moving the index window
	var (
		done   chan struct{}                  // Non-nil if background unindexing or reindexing routine is active.
		headCh = make(chan ChainHeadEvent, 1) // Buffered to avoid locking up the event feed
	)
	sub := bc.SubscribeChainHeadEvent(headCh)
	if sub == nil {
		return
	}
	defer sub.Unsubscribe()

	for {
		select {
		case head := <-headCh:
			if done == nil {
				done = make(chan struct{})
				go indexBlocks(rawdb.ReadTxIndexTail(bc.db), head.Block.NumberU64(), done)
			}
		case <-done:
			done = nil
		case <-bc.quit:
			if done != nil {
				log.Info("Waiting background transaction indexer to exit")
				<-done
			}
			return
		}
	}
}

// reportBlock logs a bad block error.
func (bc *BlockChain) reportBlock(block *types.Block, receipts types.Receipts, err error) {
	rawdb.WriteBadBlock(bc.db, block)

	var receiptString string
	for i, receipt := range receipts {
		receiptString += fmt.Sprintf("\t %d: cumulative: %v gas: %v contract: %v status: %v tx: %v logs: %v bloom: %x state: %x\n",
			i, receipt.CumulativeGasUsed, receipt.GasUsed, receipt.ContractAddress.Hex(),
			receipt.Status, receipt.TxHash.Hex(), receipt.Logs, receipt.Bloom, receipt.PostState)
	}
	log.Error(fmt.Sprintf(`
########## BAD BLOCK #########
Chain config: %v

Number: %v
Hash: 0x%x
%v

Error: %v
##############################
`, bc.chainConfig, block.Number(), block.Hash(), receiptString, err))
}

// GetTransactionLookup retrieves the lookup associate with the given transaction
// hash from the cache or database.
func (bc *BlockChain) GetTransactionLookup(hash common.Hash) *rawdb.LegacyTxLookupEntry {
	// Short circuit if the txlookup already in the cache, retrieve otherwise
	if lookup, exist := bc.txLookupCache.Get(hash); exist {
		return lookup.(*rawdb.LegacyTxLookupEntry)
	}
	tx, blockHash, blockNumber, txIndex := rawdb.ReadTransaction(bc.db, hash)
	if tx == nil {
		return nil
	}
	lookup := &rawdb.LegacyTxLookupEntry{BlockHash: blockHash, BlockIndex: blockNumber, Index: txIndex}
	bc.txLookupCache.Add(hash, lookup)
	return lookup
}

// SubscribeRemovedLogsEvent registers a subscription of RemovedLogsEvent.
func (bc *BlockChain) SubscribeRemovedLogsEvent(ch chan<- RemovedLogsEvent) event.Subscription {
	return bc.scope.Track(bc.rmLogsFeed.Subscribe(ch))
}

// SubscribeLogsEvent registers a subscription of []*types.Log.
func (bc *BlockChain) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return bc.scope.Track(bc.logsFeed.Subscribe(ch))
}
