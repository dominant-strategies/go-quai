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

package eth

import (
	"context"
	"errors"
	"math/big"

	quai "github.com/dominant-strategies/go-quai"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/bloombits"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/eth/downloader"
	"github.com/dominant-strategies/go-quai/eth/gasprice"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
)

// QuaiAPIBackend implements quaiapi.Backend for full nodes
type QuaiAPIBackend struct {
	extRPCEnabled bool
	eth           *Quai
	gpo           *gasprice.Oracle
}

// ChainConfig returns the active chain configuration.
func (b *QuaiAPIBackend) ChainConfig() *params.ChainConfig {
	return b.eth.core.Config()
}

func (b *QuaiAPIBackend) TxPool() *core.TxPool {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.eth.core.TxPool()
}

func (b *QuaiAPIBackend) CurrentBlock() *types.Block {
	return b.eth.core.CurrentBlock()
}

// CurrentLogEntropy returns the logarithm of the total entropy reduction since genesis for our current head block
func (b *QuaiAPIBackend) CurrentLogEntropy() *big.Int {
	return b.eth.core.CurrentLogEntropy()
}

// TotalLogS returns the total entropy reduction if the chain since genesis to the given header
func (b *QuaiAPIBackend) TotalLogS(header *types.Header) *big.Int {
	return b.eth.core.TotalLogS(header)
}

// CalcOrder returns the order of the block within the hierarchy of chains
func (b *QuaiAPIBackend) CalcOrder(header *types.Header) (*big.Int, int, error) {
	return b.eth.core.CalcOrder(header)
}

func (b *QuaiAPIBackend) SetHead(number uint64) {
	b.eth.handler.downloader.Cancel()
	b.eth.core.SetHead(number)
}

func (b *QuaiAPIBackend) HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error) {
	// Pending block is only known by the miner
	if number == rpc.PendingBlockNumber {
		block := b.eth.core.PendingBlock()
		return block.Header(), nil
	}
	// Otherwise resolve and return the block
	if number == rpc.LatestBlockNumber {
		return b.eth.core.CurrentHeader(), nil
	}
	return b.eth.core.GetHeaderByNumber(uint64(number)), nil
}

func (b *QuaiAPIBackend) HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Header, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.HeaderByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header := b.eth.core.GetHeaderByHash(hash)
		if header == nil {
			return nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && b.eth.core.GetCanonicalHash(header.Number().Uint64()) != hash {
			return nil, errors.New("hash is not currently canonical")
		}
		return header, nil
	}
	return nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *QuaiAPIBackend) HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
	return b.eth.core.GetHeaderByHash(hash), nil
}

func (b *QuaiAPIBackend) BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error) {
	// Pending block is only known by the miner
	if number == rpc.PendingBlockNumber {
		block := b.eth.core.PendingBlock()
		return block, nil
	}
	// Otherwise resolve and return the block
	if number == rpc.LatestBlockNumber {
		number = rpc.BlockNumber(b.eth.core.CurrentHeader().NumberU64())
	}
	block := b.eth.core.GetBlockByNumber(uint64(number))
	if block != nil {
		return block, nil
	}
	return nil, errors.New("block is nil api backend")
}

func (b *QuaiAPIBackend) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	return b.eth.core.GetBlockByHash(hash), nil
}

func (b *QuaiAPIBackend) BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Block, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.BlockByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header := b.eth.core.GetHeaderByHash(hash)
		if header == nil {
			return nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && b.eth.core.GetCanonicalHash(header.Number().Uint64()) != hash {
			return nil, errors.New("hash is not currently canonical")
		}
		block := b.eth.core.GetBlock(hash, header.Number().Uint64())
		if block == nil {
			return nil, errors.New("header found, but block body is missing")
		}
		return block, nil
	}
	return nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *QuaiAPIBackend) PendingBlockAndReceipts() (*types.Block, types.Receipts) {
	return b.eth.core.PendingBlockAndReceipts()
}

func (b *QuaiAPIBackend) StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*state.StateDB, *types.Header, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, nil, errors.New("stateAndHeaderByNumber can only be called in zone chain")
	}
	// Pending state is only known by the miner
	if number == rpc.PendingBlockNumber {
		block := b.eth.core.Pending()
		return &state.StateDB{}, block.Header(), nil
	}
	// Otherwise resolve the block number and return its state
	header, err := b.HeaderByNumber(ctx, number)
	if err != nil {
		return nil, nil, err
	}
	if header == nil {
		return nil, nil, errors.New("header not found")
	}
	stateDb, err := b.eth.Core().StateAt(header.Root())
	return stateDb, header, err
}

func (b *QuaiAPIBackend) StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, nil, errors.New("stateAndHeaderByNumberOrHash can only be called in zone chain")
	}
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.StateAndHeaderByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header, err := b.HeaderByHash(ctx, hash)
		if err != nil {
			return nil, nil, err
		}
		if header == nil {
			return nil, nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && b.eth.core.GetCanonicalHash(header.Number().Uint64()) != hash {
			return nil, nil, errors.New("hash is not currently canonical")
		}
		stateDb, err := b.eth.Core().StateAt(header.Root())
		return stateDb, header, err
	}
	return nil, nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *QuaiAPIBackend) GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getReceipts can only be called in zone chain")
	}
	return b.eth.core.GetReceiptsByHash(hash), nil
}

// GetBloom returns the bloom for the given block hash
func (b *QuaiAPIBackend) GetBloom(hash common.Hash) (*types.Bloom, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getBloom can only be called in zone chain")
	}
	return b.eth.core.Slice().HeaderChain().GetBloom(hash)
}

func (b *QuaiAPIBackend) GetLogs(ctx context.Context, hash common.Hash) ([][]*types.Log, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getLogs can only be called in zone chain")
	}
	receipts := b.eth.core.GetReceiptsByHash(hash)
	if receipts == nil {
		return nil, nil
	}
	logs := make([][]*types.Log, len(receipts))
	for i, receipt := range receipts {
		logs[i] = receipt.Logs
	}
	return logs, nil
}

func (b *QuaiAPIBackend) GetEVM(ctx context.Context, msg core.Message, state *state.StateDB, header *types.Header, vmConfig *vm.Config) (*vm.EVM, func() error, error) {
	vmError := func() error { return nil }
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, vmError, errors.New("getEvm can only be called in zone chain")
	}
	if vmConfig == nil {
		vmConfig = b.eth.core.GetVMConfig()
	}
	txContext := core.NewEVMTxContext(msg)
	context := core.NewEVMBlockContext(header, b.eth.Core(), nil)
	return vm.NewEVM(context, txContext, state, b.eth.core.Config(), *vmConfig), vmError, nil
}

func (b *QuaiAPIBackend) SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.eth.Core().SubscribeRemovedLogsEvent(ch)
}

func (b *QuaiAPIBackend) SubscribePendingLogsEvent(ch chan<- []*types.Log) event.Subscription {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.eth.core.SubscribePendingLogs(ch)
}

func (b *QuaiAPIBackend) SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription {
	return b.eth.Core().SubscribeChainEvent(ch)
}

func (b *QuaiAPIBackend) SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription {
	return b.eth.Core().SubscribeChainHeadEvent(ch)
}

func (b *QuaiAPIBackend) SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription {
	return b.eth.Core().SubscribeChainSideEvent(ch)
}

func (b *QuaiAPIBackend) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.eth.Core().SubscribeLogsEvent(ch)
}

func (b *QuaiAPIBackend) SendTx(ctx context.Context, signedTx *types.Transaction) error {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return errors.New("sendTx can only be called in zone chain")
	}
	return b.eth.Core().AddLocal(signedTx)
}

func (b *QuaiAPIBackend) GetPoolTransactions() (types.Transactions, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getPoolTransactions can only be called in zone chain")
	}
	pending, err := b.eth.core.TxPoolPending(false)
	if err != nil {
		return nil, err
	}
	var txs types.Transactions
	for _, batch := range pending {
		txs = append(txs, batch...)
	}
	return txs, nil
}

func (b *QuaiAPIBackend) GetPoolTransaction(hash common.Hash) *types.Transaction {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.eth.core.Get(hash)
}

func (b *QuaiAPIBackend) GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, common.Hash{}, 0, 0, errors.New("getTransaction can only be called in zone chain")
	}
	tx, blockHash, blockNumber, index := rawdb.ReadTransaction(b.eth.ChainDb(), txHash)
	return tx, blockHash, blockNumber, index, nil
}

func (b *QuaiAPIBackend) GetPoolNonce(ctx context.Context, addr common.Address) (uint64, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return 0, errors.New("getPoolNonce can only be called in zone chain")
	}
	return b.eth.core.Nonce(addr), nil
}

func (b *QuaiAPIBackend) Stats() (pending int, queued int) {
	return b.eth.core.Stats()
}

func (b *QuaiAPIBackend) TxPoolContent() (map[common.InternalAddress]types.Transactions, map[common.InternalAddress]types.Transactions) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, nil
	}
	return b.eth.core.Content()
}

func (b *QuaiAPIBackend) TxPoolContentFrom(addr common.Address) (types.Transactions, types.Transactions) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, nil
	}
	return b.eth.core.ContentFrom(addr)
}

func (b *QuaiAPIBackend) SubscribeNewTxsEvent(ch chan<- core.NewTxsEvent) event.Subscription {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.eth.core.SubscribeNewTxsEvent(ch)
}

func (b *QuaiAPIBackend) Downloader() *downloader.Downloader {
	return b.eth.Downloader()
}

func (b *QuaiAPIBackend) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("suggestTipCap can only be called in zone chain")
	}
	return b.gpo.SuggestTipCap(ctx)
}

func (b *QuaiAPIBackend) FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (firstBlock *big.Int, reward [][]*big.Int, baseFee []*big.Int, gasUsedRatio []float64, err error) {
	return b.gpo.FeeHistory(ctx, blockCount, lastBlock, rewardPercentiles)
}

func (b *QuaiAPIBackend) ChainDb() ethdb.Database {
	return b.eth.ChainDb()
}

func (b *QuaiAPIBackend) EventMux() *event.TypeMux {
	return b.eth.EventMux()
}

func (b *QuaiAPIBackend) ExtRPCEnabled() bool {
	return b.extRPCEnabled
}

func (b *QuaiAPIBackend) RPCGasCap() uint64 {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return 0
	}
	return b.eth.config.RPCGasCap
}

func (b *QuaiAPIBackend) RPCTxFeeCap() float64 {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return 0
	}
	return b.eth.config.RPCTxFeeCap
}

func (b *QuaiAPIBackend) BloomStatus() (uint64, uint64) {
	sections, _, _ := b.eth.bloomIndexer.Sections()
	return params.BloomBitsBlocks, sections
}

func (b *QuaiAPIBackend) ServiceFilter(ctx context.Context, session *bloombits.MatcherSession) {
	for i := 0; i < bloomFilterThreads; i++ {
		go session.Multiplex(bloomRetrievalBatch, bloomRetrievalWait, b.eth.bloomRequests)
	}
}

func (b *QuaiAPIBackend) Engine() consensus.Engine {
	return b.eth.engine
}

func (b *QuaiAPIBackend) CurrentHeader() *types.Header {
	return b.eth.core.CurrentHeader()
}

func (b *QuaiAPIBackend) StateAtBlock(ctx context.Context, block *types.Block, reexec uint64, base *state.StateDB, checkLive bool) (*state.StateDB, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("stateAtBlock can only be called in zone chain")
	}
	return b.eth.core.StateAtBlock(block, reexec, base, checkLive)
}

func (b *QuaiAPIBackend) StateAtTransaction(ctx context.Context, block *types.Block, txIndex int, reexec uint64) (core.Message, vm.BlockContext, *state.StateDB, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil, vm.BlockContext{}, nil, errors.New("stateAtTransaction can only be called in zone chain")
	}
	return b.eth.core.StateAtTransaction(block, txIndex, reexec)
}

func (b *QuaiAPIBackend) SyncProgress() quai.SyncProgress {
	return b.eth.Downloader().Progress()
}

func (b *QuaiAPIBackend) Append(header *types.Header, domPendingHeader *types.Header, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, bool, error) {
	return b.eth.core.Append(header, domPendingHeader, domTerminus, domOrigin, newInboundEtxs)
}

func (b *QuaiAPIBackend) ConstructLocalMinedBlock(header *types.Header) (*types.Block, error) {
	return b.eth.core.ConstructLocalMinedBlock(header)
}

func (b *QuaiAPIBackend) InsertBlock(ctx context.Context, block *types.Block) (int, error) {
	return b.eth.core.InsertChain([]*types.Block{block})
}

func (b *QuaiAPIBackend) WriteBlock(block *types.Block) {
	b.eth.core.WriteBlock(block)
}

func (b *QuaiAPIBackend) PendingBlock() *types.Block {
	return b.eth.core.PendingBlock()
}

func (b *QuaiAPIBackend) SubRelayPendingHeader(pendingHeader types.PendingHeader, location common.Location, subReorg bool) {
	b.eth.core.SubRelayPendingHeader(pendingHeader, location, subReorg)
}

func (b *QuaiAPIBackend) UpdateDom(oldTerminus common.Hash, newTerminus common.Hash, location common.Location) {
	b.eth.core.UpdateDom(oldTerminus, newTerminus, location)
}

func (b *QuaiAPIBackend) RequestDomToAppendOrFetch(hash common.Hash, order int) {
	b.eth.core.RequestDomToAppendOrFetch(hash, order)
}

func (b *QuaiAPIBackend) ProcessingState() bool {
	return b.eth.core.ProcessingState()
}

func (b *QuaiAPIBackend) NewGenesisPendingHeader(pendingHeader *types.Header) {
	b.eth.core.NewGenesisPendigHeader(pendingHeader)
}

func (b *QuaiAPIBackend) GetPendingHeader() (*types.Header, error) {
	return b.eth.core.GetPendingHeader()
}

func (b *QuaiAPIBackend) GetManifest(blockHash common.Hash) (types.BlockManifest, error) {
	return b.eth.core.GetManifest(blockHash)
}

func (b *QuaiAPIBackend) GetSubManifest(slice common.Location, blockHash common.Hash) (types.BlockManifest, error) {
	return b.eth.core.GetSubManifest(slice, blockHash)
}

func (b *QuaiAPIBackend) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	return b.eth.core.AddPendingEtxs(pEtxs)
}

func (b *QuaiAPIBackend) AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error {
	return b.eth.core.AddPendingEtxsRollup(pEtxsRollup)
}

func (b *QuaiAPIBackend) SubscribePendingHeaderEvent(ch chan<- *types.Header) event.Subscription {
	return b.eth.core.SubscribePendingHeader(ch)
}

func (b *QuaiAPIBackend) GenerateRecoveryPendingHeader(pendingHeader *types.Header, checkpointHashes types.Termini) error {
	return b.eth.core.GenerateRecoveryPendingHeader(pendingHeader, checkpointHashes)
}
