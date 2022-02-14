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
	"log"
	"math/big"

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
	config    *params.ChainConfig // Chain configuration options
	bc        *BlockChain         // Canonical block chain
	engine    consensus.Engine    // Consensus engine used for block rewards
	blockLink *extBlockLink
}

type extBlockLink struct {
	prime   common.Hash     // Last applied prime hash
	regions []common.Hash   // Last applied region hashes
	zones   [][]common.Hash // Last applied zone hashes
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

	cpyExtBlocks := make([]*types.ExternalBlock, len(externalBlocks))
	copy(cpyExtBlocks, externalBlocks)
	linkErr := p.checkExternalBlockLink(cpyExtBlocks)
	if linkErr != nil {
		return nil, nil, 0, nil, err
	}

	etxs := 0
	for _, externalBlock := range externalBlocks {
		externalBlock.Receipts().DeriveFields(p.config, externalBlock.Hash(), externalBlock.Header().Number[externalBlock.Context().Int64()].Uint64(), externalBlock.Transactions())
		for _, tx := range externalBlock.Transactions() {
			msg, err := tx.AsMessage(types.MakeSigner(p.config, header.Number[types.QuaiNetworkContext]), header.BaseFee[types.QuaiNetworkContext])
			// Quick check to make sure we're adding an external transaction, currently saves us from not passing merkel path in external block
			if !msg.FromExternal() {
				continue
			}
			fmt.Println("Applying etx", tx.Hash().Hex(), msg.From(), msg.To(), msg.Value())
			if err != nil {
				return nil, nil, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
			}
			statedb.Prepare(tx.Hash(), i)
			receipt, err := applyExternalTransaction(msg, p.config, p.bc, nil, gp, statedb, blockNumber, blockHash, externalBlock, tx, usedGas, vmenv)
			if err != nil {
				return nil, nil, 0, nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
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

// GenerateExtBlockLink will generate blockLink struct for the last applied external block hashes for a current context.
// This will be used to check the trace of each set of applied external block sets so that they keep proper lineage to previous
// traces. GenerateExtBlockLink will be used upon start up and the blockLink struct will be continually updated as more blocks are processed.
func (p *StateProcessor) GenerateExtBlockLink() {
	// Get the previous hashes from the first external blocks applied in the new GetExternalBlocks set.
	// Initial the linkBlocks into 3x3 structure.
	linkBlocks := &extBlockLink{
		prime:   p.config.GenesisHashes[0],
		regions: make([]common.Hash, 3),
		zones:   [][]common.Hash{make([]common.Hash, 3), make([]common.Hash, 3), make([]common.Hash, 3)},
	}
	for i := range linkBlocks.regions {
		linkBlocks.regions[i] = p.config.GenesisHashes[1]
		for j := range linkBlocks.zones[i] {
			linkBlocks.zones[i][j] = p.config.GenesisHashes[2]
		}
	}

	// Keep track of what the method started with.
	// Deep copy the struct.
	startingLinkBlocks := &extBlockLink{
		prime:   linkBlocks.prime,
		regions: make([]common.Hash, len(linkBlocks.regions)),
		zones:   make([][]common.Hash, 3),
	}
	copy(startingLinkBlocks.regions, linkBlocks.regions)
	for i := range linkBlocks.zones {
		startingLinkBlocks.zones[i] = make([]common.Hash, len(linkBlocks.zones[i]))
		copy(startingLinkBlocks.zones[i], linkBlocks.zones[i])
	}

	currentHeader := p.bc.CurrentHeader()
	if currentHeader.Number[types.QuaiNetworkContext].Cmp(big.NewInt(1)) < 1 {
		p.blockLink = linkBlocks
		return
	}

	// Need to keep first hash that is put. Region went all the way back to the first region block.
	populated := false
	for !populated {

		// Populate the linkBlocks struct with the block hashes of the last applied ext block of that chain.
		extBlocks, err := p.engine.GetExternalBlocks(p.bc, currentHeader, true)
		if err != nil {
			log.Fatal("GenerateExtBlockLink:", "err", err)
		}
		// Keep track of what the method started with.
		// Deep copy the struct.
		tempLinkBlocks := &extBlockLink{
			prime:   linkBlocks.prime,
			regions: make([]common.Hash, len(linkBlocks.regions)),
			zones:   [][]common.Hash{make([]common.Hash, 3), make([]common.Hash, 3), make([]common.Hash, 3)},
		}

		copy(tempLinkBlocks.regions, linkBlocks.regions)
		for i := range linkBlocks.zones {
			tempLinkBlocks.zones[i] = make([]common.Hash, len(linkBlocks.zones[i]))
			copy(tempLinkBlocks.zones[i], linkBlocks.zones[i])
		}

		tempLinkBlocks = p.SetLinkBlocksToLastApplied(extBlocks, tempLinkBlocks)

		// If our tempLink is new and our starting link hasn't changed.
		if tempLinkBlocks.prime != linkBlocks.prime && startingLinkBlocks.prime == linkBlocks.prime {
			linkBlocks.prime = tempLinkBlocks.prime
		}
		for i := range linkBlocks.regions {
			if tempLinkBlocks.regions[i] != linkBlocks.regions[i] && startingLinkBlocks.regions[i] == linkBlocks.regions[i] {
				linkBlocks.regions[i] = tempLinkBlocks.regions[i]
			}
			for j := range linkBlocks.zones[i] {
				if tempLinkBlocks.zones[i][j] != linkBlocks.zones[i][j] && startingLinkBlocks.zones[i][j] == linkBlocks.zones[i][j] {
					linkBlocks.zones[i][j] = tempLinkBlocks.zones[i][j]
				}
			}
		}

		// Convert config for region and zone location into ints to compare during check.
		regionLoc := int(p.config.Location[0])
		zoneLoc := int(p.config.Location[0])

		// Check if linkBlocks is populated fully for all chains in the hierarchy.
		// Do not set populated to false if we are in Prime as Prime will not have any external blocks.
		tempPopulated := true
		if linkBlocks.prime == p.config.GenesisHashes[0] && types.QuaiNetworkContext != 0 {
			tempPopulated = false
		} else {
			for i := range linkBlocks.regions {

				if linkBlocks.regions[i] == p.config.GenesisHashes[1] && !(regionLoc-1 == i && types.QuaiNetworkContext == 1) {
					tempPopulated = false
					break
				}
				for j := range linkBlocks.zones[i] {
					if linkBlocks.zones[i][j] == p.config.GenesisHashes[2] && !(regionLoc-1 == i && zoneLoc-1 == j) {
						tempPopulated = false
						break
					}
				}
			}
		}

		// Update the populated check with current status of populating the last applied external block hashes.
		populated = tempPopulated

		// Check if we are on block height 1 for current context.
		if currentHeader.Number[types.QuaiNetworkContext].Cmp(big.NewInt(1)) < 1 {
			p.blockLink = linkBlocks
			return
		}

		// Iterate to previous block in current context.
		currentHeader = p.bc.GetHeaderByHash(currentHeader.ParentHash[types.QuaiNetworkContext])
	}
	p.blockLink = linkBlocks
}

// SetLinkBlocksToLastApplied will update the passed in linkBlocks struct with the latest applied external blocks.
func (p *StateProcessor) SetLinkBlocksToLastApplied(externalBlocks []*types.ExternalBlock, linkBlocks *extBlockLink) *extBlockLink {

	// Keep track of what the method started with.
	// Deep copy the struct.
	startingLinkBlocks := &extBlockLink{
		prime:   linkBlocks.prime,
		regions: make([]common.Hash, len(linkBlocks.regions)),
		zones:   make([][]common.Hash, 3),
	}
	copy(startingLinkBlocks.regions, linkBlocks.regions)
	for i := range linkBlocks.zones {
		startingLinkBlocks.zones[i] = make([]common.Hash, len(linkBlocks.zones[i]))
		copy(startingLinkBlocks.zones[i], linkBlocks.zones[i])
	}

	// iterate through the extBlocks, updated the index with the last applied external blocks.
	for _, lastAppliedBlock := range externalBlocks {
		switch lastAppliedBlock.Context().Int64() {
		case 0:
			if linkBlocks.prime == startingLinkBlocks.prime {
				linkBlocks.prime = lastAppliedBlock.Hash()
			}
		case 1:
			if linkBlocks.regions[lastAppliedBlock.Header().Location[0]-1] == startingLinkBlocks.regions[lastAppliedBlock.Header().Location[0]-1] {
				linkBlocks.regions[lastAppliedBlock.Header().Location[0]-1] = lastAppliedBlock.Hash()
			}
		case 2:
			if linkBlocks.zones[lastAppliedBlock.Header().Location[0]-1][lastAppliedBlock.Header().Location[1]-1] == startingLinkBlocks.zones[lastAppliedBlock.Header().Location[0]-1][lastAppliedBlock.Header().Location[1]-1] {
				linkBlocks.zones[lastAppliedBlock.Header().Location[0]-1][lastAppliedBlock.Header().Location[1]-1] = lastAppliedBlock.Hash()
			}
		}
	}

	return linkBlocks
}

func (p *StateProcessor) checkExternalBlockLink(externalBlocks []*types.ExternalBlock) error {
	// Get the previous hashes from the first external blocks applied in the new GetExternalBlocks set.
	// Initial the linkBlocks into 3x3 structure.
	linkBlocks := &extBlockLink{
		prime:   common.Hash{},
		regions: make([]common.Hash, 3),
		zones:   [][]common.Hash{make([]common.Hash, 3), make([]common.Hash, 3), make([]common.Hash, 3)},
	}

	// Reverse ext block set to get the last applied block order
	// if len(externalBlocks) > 0 {
	// 	for i, j := 0, len(externalBlocks)-1; i < j; i, j = i+1, j-1 {
	// 		externalBlocks[i], externalBlocks[j] = externalBlocks[j], externalBlocks[i]
	// 	}
	// }

	for _, externalBlock := range externalBlocks {
		fmt.Println("checkExternalBlockLink: ext block", externalBlock.Header().Number, externalBlock.Context(), externalBlock.Hash())
		context := externalBlock.Context().Int64()
		switch context {
		case 0:
			linkedPreviousHash := externalBlock.Header().ParentHash[externalBlock.Context().Int64()]
			linkBlocks.prime = linkedPreviousHash

		case 1:
			linkedPreviousHash := externalBlock.Header().ParentHash[externalBlock.Context().Int64()]
			linkBlocks.regions[externalBlock.Header().Location[0]-1] = linkedPreviousHash

		case 2:
			linkedPreviousHash := externalBlock.Header().ParentHash[externalBlock.Context().Int64()]
			linkBlocks.zones[externalBlock.Header().Location[0]-1][externalBlock.Header().Location[1]-1] = linkedPreviousHash
		}
	}

	// Verify that the externalBlocks provided link with previous coincident blocks.
	if linkBlocks.prime != (common.Hash{}) && linkBlocks.prime != p.blockLink.prime {
		return fmt.Errorf("error linking expected prime: %d received prime: %d", p.blockLink.prime, linkBlocks.prime)
	} else {
		for i := range linkBlocks.regions {
			if linkBlocks.regions[i] != (common.Hash{}) && linkBlocks.regions[i] != p.blockLink.regions[i] {
				return fmt.Errorf("error linking  region: %d expected region: %d received region: %d", i, p.blockLink.regions[i], linkBlocks.regions[i])
			}
			for j := range linkBlocks.zones[i] {
				if linkBlocks.zones[i][j] != (common.Hash{}) && linkBlocks.zones[i][j] != p.blockLink.zones[i][j] {
					return fmt.Errorf("error linking  region: %d zone: %d expected zone: %d received zone: %d", i, j, p.blockLink.zones[i][j], linkBlocks.zones[i][j])
				}
			}
		}
	}

	// Update array with latest applied externalBlocks.
	p.blockLink = p.SetLinkBlocksToLastApplied(externalBlocks, p.blockLink)

	return nil
}
