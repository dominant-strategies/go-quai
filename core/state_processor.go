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

	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/trie"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
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
