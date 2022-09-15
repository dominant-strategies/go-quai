// Copyright 2020 The go-ethereum Authors
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

// Package catalyst implements the temporary eth1/eth2 RPC integration.
package catalyst

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus/misc"
	"github.com/spruce-solutions/go-quai/core"
	"github.com/spruce-solutions/go-quai/core/state"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/eth"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/node"
	"github.com/spruce-solutions/go-quai/rpc"
	"github.com/spruce-solutions/go-quai/trie"
)

// Register adds catalyst APIs to the node.
func Register(stack *node.Node, backend *eth.Ethereum) error {
	chainconfig := backend.Core().Config()
	if chainconfig.CatalystBlock == nil {
		return errors.New("catalystBlock is not set in genesis config")
	} else if chainconfig.CatalystBlock.Sign() != 0 {
		return errors.New("catalystBlock of genesis config must be zero")
	}

	log.Warn("Catalyst mode enabled")
	stack.RegisterAPIs([]rpc.API{
		{
			Namespace: "consensus",
			Version:   "1.0",
			Service:   newConsensusAPI(backend),
			Public:    true,
		},
	})
	return nil
}

type consensusAPI struct {
	eth *eth.Ethereum
}

func newConsensusAPI(eth *eth.Ethereum) *consensusAPI {
	return &consensusAPI{eth: eth}
}

// blockExecutionEnv gathers all the data required to execute
// a block, either when assembling it or when inserting it.
type blockExecutionEnv struct {
	core    *core.Core
	state   *state.StateDB
	tcount  int
	gasPool *core.GasPool

	header   *types.Header
	txs      []*types.Transaction
	receipts []*types.Receipt
}

func (env *blockExecutionEnv) commitTransaction(tx *types.Transaction, coinbase common.Address) error {
	vmconfig := *env.core.GetVMConfig()
	snap := env.state.Snapshot()
	receipt, err := core.ApplyTransaction(env.core.Config(), env.core, &coinbase, env.gasPool, env.state, env.header, tx, &env.header.GasUsed[types.QuaiNetworkContext], vmconfig)
	if err != nil {
		env.state.RevertToSnapshot(snap)
		return err
	}
	env.txs = append(env.txs, tx)
	env.receipts = append(env.receipts, receipt)
	return nil
}

func (api *consensusAPI) makeEnv(parent *types.Block, header *types.Header) (*blockExecutionEnv, error) {
	state, err := api.eth.Core().StateAt(parent.Root())
	if err != nil {
		return nil, err
	}
	env := &blockExecutionEnv{
		core:    api.eth.Core(),
		state:   state,
		header:  header,
		gasPool: new(core.GasPool).AddGas(header.GasLimit[types.QuaiNetworkContext]),
	}
	return env, nil
}

func encodeTransactions(txs []*types.Transaction) [][]byte {
	var enc = make([][]byte, len(txs))
	for i, tx := range txs {
		enc[i], _ = tx.MarshalBinary()
	}
	return enc
}

func decodeTransactions(enc [][]byte) ([]*types.Transaction, error) {
	var txs = make([]*types.Transaction, len(enc))
	for i, encTx := range enc {
		var tx types.Transaction
		if err := tx.UnmarshalBinary(encTx); err != nil {
			return nil, fmt.Errorf("invalid transaction %d: %v", i, err)
		}
		txs[i] = &tx
	}
	return txs, nil
}

func insertBlockParamsToBlock(core *core.Core, parent *types.Header, params executableData) (*types.Block, error) {
	config := core.Config()

	txs, err := decodeTransactions(params.Transactions)
	if err != nil {
		return nil, err
	}

	number := big.NewInt(0)
	number.SetUint64(params.Number)

	emptyHash := common.Hash{}
	emptyAddress := common.Address{}
	header := &types.Header{
		ParentHash:  []common.Hash{emptyHash, emptyHash, emptyHash},
		TxHash:      []common.Hash{emptyHash, emptyHash, emptyHash},
		ReceiptHash: []common.Hash{emptyHash, emptyHash, emptyHash},
		UncleHash:   []common.Hash{types.EmptyUncleHash, types.EmptyUncleHash, types.EmptyUncleHash},
		Number:      []*big.Int{big.NewInt(0), big.NewInt(0), big.NewInt(0)},
		Coinbase:    []common.Address{emptyAddress, emptyAddress, emptyAddress},
		Difficulty:  []*big.Int{big.NewInt(1), big.NewInt(1), big.NewInt(1)},
		GasLimit:    []uint64{params.GasLimit, params.GasLimit, params.GasLimit},
		GasUsed:     []uint64{0, 0, 0},
		BaseFee:     []*big.Int{big.NewInt(0), big.NewInt(0), big.NewInt(0)},
		Extra:       [][]byte{[]byte{}, []byte{}, []byte{}},
		Time:        params.Timestamp,
	}

	header.ParentHash[types.QuaiNetworkContext] = params.ParentHash
	header.Coinbase[types.QuaiNetworkContext] = params.Miner
	header.Root[types.QuaiNetworkContext] = params.StateRoot
	header.TxHash[types.QuaiNetworkContext] = types.DeriveSha(types.Transactions(txs), trie.NewStackTrie(nil))
	header.ReceiptHash[types.QuaiNetworkContext] = params.ReceiptRoot
	header.Bloom[types.QuaiNetworkContext] = types.BytesToBloom(params.LogsBloom)
	header.Number[types.QuaiNetworkContext] = number
	header.GasUsed[types.QuaiNetworkContext] = params.GasUsed

	header.BaseFee[types.QuaiNetworkContext] = misc.CalcBaseFee(config, parent, core.GetHeaderByNumber, core.GetUnclesInChain, core.GetGasUsedInChain)

	block := types.NewBlockWithHeader(header).WithBody(txs, nil /* uncles */)
	return block, nil
}

// NewBlock creates an Eth1 block, inserts it in the chain, and either returns true,
// or false + an error. This is a bit redundant for go, but simplifies things on the
// eth2 side.
func (api *consensusAPI) NewBlock(params executableData) (*newBlockResponse, error) {
	parent := api.eth.Core().GetBlockByHash(params.ParentHash)
	if parent == nil {
		return &newBlockResponse{false}, fmt.Errorf("could not find parent %x", params.ParentHash)
	}
	block, err := insertBlockParamsToBlock(api.eth.Core(), parent.Header(), params)
	if err != nil {
		return nil, err
	}
	_, err = api.eth.Core().InsertChainWithoutSealVerification(block)
	return &newBlockResponse{err == nil}, err
}

// Used in tests to add a the list of transactions from a block to the tx pool.
func (api *consensusAPI) addBlockTxs(block *types.Block) error {
	for _, tx := range block.Transactions() {
		api.eth.Core().Slice().TxPool().AddLocal(tx)
	}
	return nil
}

// FinalizeBlock is called to mark a block as synchronized, so
// that data that is no longer needed can be removed.
func (api *consensusAPI) FinalizeBlock(blockHash common.Hash) (*genericResponse, error) {
	return &genericResponse{true}, nil
}

// SetHead is called to perform a force choice.
func (api *consensusAPI) SetHead(newHead common.Hash) (*genericResponse, error) {
	return &genericResponse{true}, nil
}
