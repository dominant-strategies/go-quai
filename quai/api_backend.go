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

package quai

import (
	"context"
	"errors"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/bloombits"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai/gasprice"
	"github.com/dominant-strategies/go-quai/rpc"
)

// QuaiAPIBackend implements quaiapi.Backend for full nodes
type QuaiAPIBackend struct {
	extRPCEnabled bool
	quai          *Quai
	gpo           *gasprice.Oracle
}

// ChainConfig returns the active chain configuration.
func (b *QuaiAPIBackend) ChainConfig() *params.ChainConfig {
	return b.quai.core.Config()
}

func (b *QuaiAPIBackend) TxPool() *core.TxPool {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.quai.core.TxPool()
}

func (b *QuaiAPIBackend) NodeLocation() common.Location {
	return b.quai.core.NodeLocation()
}

func (b *QuaiAPIBackend) NodeCtx() int {
	return b.quai.core.NodeCtx()
}

func (b *QuaiAPIBackend) CurrentBlock() *types.WorkObject {
	return b.quai.core.CurrentBlock()
}

// CurrentLogEntropy returns the logarithm of the total entropy reduction since genesis for our current head block
func (b *QuaiAPIBackend) CurrentLogEntropy() *big.Int {
	return b.quai.core.CurrentLogEntropy()
}

// TotalLogS returns the total entropy reduction if the chain since genesis to the given header
func (b *QuaiAPIBackend) TotalLogS(header *types.WorkObject) *big.Int {
	return b.quai.core.TotalLogS(header)
}

// CalcOrder returns the order of the block within the hierarchy of chains
func (b *QuaiAPIBackend) CalcOrder(header *types.WorkObject) (*big.Int, int, error) {
	return b.quai.core.CalcOrder(header)
}

func (b *QuaiAPIBackend) HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error) {
	// Pending block is only known by the miner
	if number == rpc.PendingBlockNumber {
		block := b.quai.core.PendingBlock()
		return block, nil
	}
	// Otherwise resolve and return the block
	if number == rpc.LatestBlockNumber {
		return b.quai.core.CurrentBlock(), nil
	}
	return b.quai.core.GetHeaderByNumber(uint64(number)), nil
}

func (b *QuaiAPIBackend) HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.WorkObject, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.HeaderByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header := b.quai.core.GetHeaderByHash(hash)
		if header == nil {
			return nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && b.quai.core.GetCanonicalHash(header.NumberU64(b.NodeCtx())) != hash {
			return nil, errors.New("hash is not currently canonical")
		}
		return header, nil
	}
	return nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *QuaiAPIBackend) HeaderByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error) {
	return b.quai.core.GetHeaderByHash(hash), nil
}

func (b *QuaiAPIBackend) BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error) {
	// Pending block is only known by the miner
	if number == rpc.PendingBlockNumber {
		block := b.quai.core.PendingBlock()
		return block, nil
	}
	// Otherwise resolve and return the block
	if number == rpc.LatestBlockNumber {
		number = rpc.BlockNumber(b.quai.core.CurrentHeader().NumberU64(b.NodeCtx()))
	}
	block := b.quai.core.GetBlockByNumber(uint64(number))
	if block != nil {
		return block, nil
	}
	return nil, errors.New("block is nil api backend")
}

func (b *QuaiAPIBackend) BlockByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error) {
	return b.quai.core.GetBlockByHash(hash), nil
}

func (b *QuaiAPIBackend) BlockOrCandidateByHash(hash common.Hash) *types.WorkObject {
	return b.quai.core.GetBlockOrCandidateByHash(hash)
}

func (b *QuaiAPIBackend) BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.WorkObject, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.BlockByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header := b.quai.core.GetHeaderByHash(hash)
		if header == nil {
			return nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && b.quai.core.GetCanonicalHash(header.NumberU64(b.NodeCtx())) != hash {
			return nil, errors.New("hash is not currently canonical")
		}
		block := b.quai.core.GetBlock(hash, header.NumberU64(b.NodeCtx()))
		if block == nil {
			return nil, errors.New("header found, but block body is missing")
		}
		return block, nil
	}
	return nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *QuaiAPIBackend) PendingBlockAndReceipts() (*types.WorkObject, types.Receipts) {
	return b.quai.core.PendingBlockAndReceipts()
}

func (b *QuaiAPIBackend) StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*state.StateDB, *types.WorkObject, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, nil, errors.New("stateAndHeaderByNumber can only be called in zone chain")
	}
	// Pending state is only known by the miner
	if number == rpc.PendingBlockNumber {
		block := b.quai.core.Pending()
		return &state.StateDB{}, block, nil
	}
	// Otherwise resolve the block number and return its state
	header, err := b.HeaderByNumber(ctx, number)
	if err != nil {
		return nil, nil, err
	}
	if header == nil {
		return nil, nil, errors.New("header not found")
	}
	stateDb, err := b.quai.Core().StateAt(header.EVMRoot(), header.UTXORoot(), header.EtxSetRoot(), header.QuaiStateSize())
	return stateDb, header, err
}

func (b *QuaiAPIBackend) StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*state.StateDB, *types.WorkObject, error) {
	nodeCtx := b.quai.core.NodeCtx()
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
		if blockNrOrHash.RequireCanonical && b.quai.core.GetCanonicalHash(header.NumberU64(b.NodeCtx())) != hash {
			return nil, nil, errors.New("hash is not currently canonical")
		}
		stateDb, err := b.quai.Core().StateAt(header.EVMRoot(), header.UTXORoot(), header.EtxSetRoot(), header.QuaiStateSize())
		return stateDb, header, err
	}
	return nil, nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (b *QuaiAPIBackend) AddressOutpoints(ctx context.Context, address common.Address) (map[string]*types.OutpointAndDenomination, error) {
	return b.quai.core.GetOutpointsByAddress(address), nil
}

func (b *QuaiAPIBackend) UTXOsByAddressAtState(ctx context.Context, state *state.StateDB, address common.Address) ([]*types.UtxoEntry, error) {
	return b.quai.core.GetUTXOsByAddressAtState(state, address)
}

func (b *QuaiAPIBackend) GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getReceipts can only be called in zone chain")
	}
	return b.quai.core.GetReceiptsByHash(hash), nil
}

// GetBloom returns the bloom for the given block hash
func (b *QuaiAPIBackend) GetBloom(hash common.Hash) (*types.Bloom, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getBloom can only be called in zone chain")
	}
	return b.quai.core.Slice().HeaderChain().GetBloom(hash)
}

func (b *QuaiAPIBackend) GetLogs(ctx context.Context, hash common.Hash) ([][]*types.Log, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getLogs can only be called in zone chain")
	}
	receipts := b.quai.core.GetReceiptsByHash(hash)
	if receipts == nil {
		return nil, nil
	}
	logs := make([][]*types.Log, len(receipts))
	for i, receipt := range receipts {
		logs[i] = receipt.Logs
	}
	return logs, nil
}

func (b *QuaiAPIBackend) GetEVM(ctx context.Context, msg core.Message, state *state.StateDB, header *types.WorkObject, parent *types.WorkObject, vmConfig *vm.Config) (*vm.EVM, func() error, error) {
	vmError := func() error { return nil }
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, vmError, errors.New("getEvm can only be called in zone chain")
	}
	if vmConfig == nil {
		vmConfig = b.quai.core.GetVMConfig()
	}
	txContext := core.NewEVMTxContext(msg)
	context, err := core.NewEVMBlockContext(header, parent, b.quai.Core(), nil)
	if err != nil {
		return nil, vmError, err
	}
	return vm.NewEVM(context, txContext, state, b.quai.core.Config(), *vmConfig), vmError, nil
}

func (b *QuaiAPIBackend) SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.quai.Core().SubscribeRemovedLogsEvent(ch)
}

func (b *QuaiAPIBackend) SubscribePendingLogsEvent(ch chan<- []*types.Log) event.Subscription {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.quai.core.SubscribePendingLogs(ch)
}

func (b *QuaiAPIBackend) SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription {
	return b.quai.Core().SubscribeChainEvent(ch)
}

func (b *QuaiAPIBackend) SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription {
	return b.quai.Core().SubscribeChainHeadEvent(ch)
}

func (b *QuaiAPIBackend) SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription {
	return b.quai.Core().SubscribeChainSideEvent(ch)
}

func (b *QuaiAPIBackend) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.quai.Core().SubscribeLogsEvent(ch)
}

func (b *QuaiAPIBackend) SendTx(ctx context.Context, signedTx *types.Transaction) error {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return errors.New("sendTx can only be called in zone chain")
	}
	return b.quai.Core().AddLocal(signedTx)
}

func (b *QuaiAPIBackend) SendRemoteTx(remoteTx *types.Transaction) error {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return errors.New("sendTx can only be called in zone chain")
	}
	b.quai.Core().AddRemote(remoteTx)
	return nil
}

func (b *QuaiAPIBackend) SendRemoteTxs(remoteTxs types.Transactions) []error {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return []error{errors.New("SendRemoteTxs can only be called in zone chain")}
	}
	b.quai.Core().AddRemotes(remoteTxs)
	return nil
}

func (b *QuaiAPIBackend) GetPoolTransactions() (types.Transactions, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getPoolTransactions can only be called in zone chain")
	}
	pending, err := b.quai.core.TxPoolPending(false)
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
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}
	return b.quai.core.Get(hash)
}

func (b *QuaiAPIBackend) GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, common.Hash{}, 0, 0, errors.New("getTransaction can only be called in zone chain")
	}
	tx, blockHash, blockNumber, index := rawdb.ReadTransaction(b.quai.ChainDb(), txHash)
	if tx == nil {
		return nil, common.Hash{}, 0, 0, errors.New("transaction not found")
	}
	return tx, blockHash, blockNumber, index, nil
}

func (b *QuaiAPIBackend) GetPoolNonce(ctx context.Context, addr common.Address) (uint64, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return 0, errors.New("getPoolNonce can only be called in zone chain")
	}
	return b.quai.core.Nonce(addr), nil
}

func (b *QuaiAPIBackend) Stats() (pending int, queued int, qi int) {
	return b.quai.core.Stats()
}

func (b *QuaiAPIBackend) TxPoolContent() (map[common.InternalAddress]types.Transactions, map[common.InternalAddress]types.Transactions) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, nil
	}
	return b.quai.core.Content()
}

func (b *QuaiAPIBackend) TxPoolContentFrom(addr common.Address) (types.Transactions, types.Transactions) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, nil
	}
	return b.quai.core.ContentFrom(addr)
}

func (b *QuaiAPIBackend) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("suggestTipCap can only be called in zone chain")
	}
	return b.gpo.SuggestTipCap(ctx)
}

func (b *QuaiAPIBackend) FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (firstBlock *big.Int, reward [][]*big.Int, baseFee []*big.Int, gasUsedRatio []float64, err error) {
	return b.gpo.FeeHistory(ctx, blockCount, lastBlock, rewardPercentiles)
}

func (b *QuaiAPIBackend) ChainDb() ethdb.Database {
	return b.quai.ChainDb()
}

func (b *QuaiAPIBackend) EventMux() *event.TypeMux {
	return b.quai.EventMux()
}

func (b *QuaiAPIBackend) ExtRPCEnabled() bool {
	return b.extRPCEnabled
}

func (b *QuaiAPIBackend) RPCGasCap() uint64 {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return 0
	}
	return b.quai.config.RPCGasCap
}

func (b *QuaiAPIBackend) RPCTxFeeCap() float64 {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return 0
	}
	return b.quai.config.RPCTxFeeCap
}

func (b *QuaiAPIBackend) BloomStatus() (uint64, uint64) {
	sections, _, _ := b.quai.bloomIndexer.Sections()
	return params.BloomBitsBlocks, sections
}

func (b *QuaiAPIBackend) ServiceFilter(ctx context.Context, session *bloombits.MatcherSession) {
	for i := 0; i < bloomFilterThreads; i++ {
		go session.Multiplex(bloomRetrievalBatch, bloomRetrievalWait, b.quai.bloomRequests)
	}
}

func (b *QuaiAPIBackend) Engine() consensus.Engine {
	return b.quai.engine
}

func (b *QuaiAPIBackend) CurrentHeader() *types.WorkObject {
	return b.quai.core.CurrentHeader()
}

func (b *QuaiAPIBackend) StateAtBlock(ctx context.Context, block *types.WorkObject, reexec uint64, base *state.StateDB, checkLive bool) (*state.StateDB, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("stateAtBlock can only be called in zone chain")
	}
	return b.quai.core.StateAtBlock(block, reexec, base, checkLive)
}

func (b *QuaiAPIBackend) StateAtTransaction(ctx context.Context, block *types.WorkObject, txIndex int, reexec uint64) (core.Message, vm.BlockContext, *state.StateDB, error) {
	nodeCtx := b.quai.core.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, vm.BlockContext{}, nil, errors.New("stateAtTransaction can only be called in zone chain")
	}
	return b.quai.core.StateAtTransaction(block, txIndex, reexec)
}

func (b *QuaiAPIBackend) Append(header *types.WorkObject, manifest types.BlockManifest, domPendingHeader *types.WorkObject, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, bool, error) {
	return b.quai.core.Append(header, manifest, domPendingHeader, domTerminus, domOrigin, newInboundEtxs)
}

func (b *QuaiAPIBackend) DownloadBlocksInManifest(hash common.Hash, manifest types.BlockManifest, entropy *big.Int) {
	b.quai.core.DownloadBlocksInManifest(hash, manifest, entropy)
}

func (b *QuaiAPIBackend) ConstructLocalMinedBlock(header *types.WorkObject) (*types.WorkObject, error) {
	return b.quai.core.ConstructLocalMinedBlock(header)
}

func (b *QuaiAPIBackend) GetPendingBlockBody(woHeader *types.WorkObjectHeader) *types.WorkObject {
	return b.quai.core.GetPendingBlockBody(woHeader)
}

func (b *QuaiAPIBackend) GetTxsFromBroadcastSet(hash common.Hash) (types.Transactions, error) {
	return b.quai.core.GetTxsFromBroadcastSet(hash)
}

func (b *QuaiAPIBackend) InsertBlock(ctx context.Context, block *types.WorkObject) (int, error) {
	return b.quai.core.InsertChain([]*types.WorkObject{block})
}

func (b *QuaiAPIBackend) WriteBlock(block *types.WorkObject) {
	b.quai.core.WriteBlock(block)
}

func (b *QuaiAPIBackend) PendingBlock() *types.WorkObject {
	return b.quai.core.PendingBlock()
}

func (b *QuaiAPIBackend) SubRelayPendingHeader(pendingHeader types.PendingHeader, newEntropy *big.Int, location common.Location, subReorg bool, order int, updateDomLocation common.Location) {
	b.quai.core.SubRelayPendingHeader(pendingHeader, newEntropy, location, subReorg, order, updateDomLocation)
}

func (b *QuaiAPIBackend) UpdateDom(oldDomReference common.Hash, pendingHeader *types.WorkObject, location common.Location) {
	b.quai.core.UpdateDom(oldDomReference, pendingHeader, location)
}

func (b *QuaiAPIBackend) RequestDomToAppendOrFetch(hash common.Hash, entropy *big.Int, order int) {
	b.quai.core.RequestDomToAppendOrFetch(hash, entropy, order)
}

func (b *QuaiAPIBackend) ProcessingState() bool {
	return b.quai.core.ProcessingState()
}

func (b *QuaiAPIBackend) NewGenesisPendingHeader(pendingHeader *types.WorkObject, domTerminus common.Hash, genesisHash common.Hash) error {
	return b.quai.core.NewGenesisPendigHeader(pendingHeader, domTerminus, genesisHash)
}

func (b *QuaiAPIBackend) SetCurrentExpansionNumber(expansionNumber uint8) {
	b.quai.core.SetCurrentExpansionNumber(expansionNumber)
}

func (b *QuaiAPIBackend) WriteGenesisBlock(block *types.WorkObject, location common.Location) {
	b.quai.core.WriteGenesisBlock(block, location)
}

func (b *QuaiAPIBackend) GetPendingHeader() (*types.WorkObject, error) {
	return b.quai.core.GetPendingHeader()
}

func (b *QuaiAPIBackend) GetManifest(blockHash common.Hash) (types.BlockManifest, error) {
	return b.quai.core.GetManifest(blockHash)
}

func (b *QuaiAPIBackend) GetSubManifest(slice common.Location, blockHash common.Hash) (types.BlockManifest, error) {
	return b.quai.core.GetSubManifest(slice, blockHash)
}

func (b *QuaiAPIBackend) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	return b.quai.core.AddPendingEtxs(pEtxs)
}

func (b *QuaiAPIBackend) AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error {
	return b.quai.core.AddPendingEtxsRollup(pEtxsRollup)
}

func (b *QuaiAPIBackend) SubscribePendingHeaderEvent(ch chan<- *types.WorkObject) event.Subscription {
	return b.quai.core.SubscribePendingHeader(ch)
}

func (b *QuaiAPIBackend) GenerateRecoveryPendingHeader(pendingHeader *types.WorkObject, checkpointHashes types.Termini) error {
	return b.quai.core.GenerateRecoveryPendingHeader(pendingHeader, checkpointHashes)
}

func (b *QuaiAPIBackend) GetPendingEtxsRollupFromSub(hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	return b.quai.core.GetPendingEtxsRollupFromSub(hash, location)
}

func (b *QuaiAPIBackend) GetPendingEtxsFromSub(hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	return b.quai.core.GetPendingEtxsFromSub(hash, location)
}

func (b *QuaiAPIBackend) Logger() *log.Logger {
	return b.quai.logger
}

func (b *QuaiAPIBackend) GetSlicesRunning() []common.Location {
	return b.quai.core.GetSlicesRunning()
}

func (b *QuaiAPIBackend) SetSubInterface(subInterface core.CoreBackend, location common.Location) {
	b.quai.core.SetSubInterface(subInterface, location)
}

func (b *QuaiAPIBackend) AddGenesisPendingEtxs(block *types.WorkObject) {
	b.quai.core.AddGenesisPendingEtxs(block)
}

func (b *QuaiAPIBackend) SubscribeExpansionEvent(ch chan<- core.ExpansionEvent) event.Subscription {
	return b.quai.core.SubscribeExpansionEvent(ch)
}

func (b *QuaiAPIBackend) SendWorkShare(workShare *types.WorkObjectHeader) error {
	return b.quai.core.SendWorkShare(workShare)
}

func (b *QuaiAPIBackend) CheckIfValidWorkShare(workShare *types.WorkObjectHeader) bool {
	return b.quai.core.CheckIfValidWorkShare(workShare)
}

func (b *QuaiAPIBackend) SetDomInterface(domInterface core.CoreBackend) {
	b.quai.core.SetDomInterface(domInterface)
}

func (b *QuaiAPIBackend) GetMaxTxInWorkShare() uint64 {
	return b.quai.core.GetMaxTxInWorkShare()
}

func (b *QuaiAPIBackend) BadHashExistsInChain() bool {
	return b.quai.core.BadHashExistsInChain()
}

func (b *QuaiAPIBackend) IsBlockHashABadHash(hash common.Hash) bool {
	return b.quai.core.IsBlockHashABadHash(hash)
}

func (b *QuaiAPIBackend) ComputeEfficiencyScore(header *types.WorkObject) uint16 {
	return b.quai.core.ComputeEfficiencyScore(header)
}

func (b *QuaiAPIBackend) Config() *params.ChainConfig {
	return b.quai.core.Config()
}

func (b *QuaiAPIBackend) GetBlockByHash(hash common.Hash) *types.WorkObject {
	return b.quai.core.GetBlockByHash(hash)
}

func (b *QuaiAPIBackend) GetHeaderByHash(hash common.Hash) *types.WorkObject {
	return b.quai.core.GetHeaderByHash(hash)
}

func (b *QuaiAPIBackend) GetHeaderByNumber(number uint64) *types.WorkObject {
	return b.quai.core.GetHeaderByNumber(number)
}

func (b *QuaiAPIBackend) GetTerminiByHash(hash common.Hash) *types.Termini {
	return b.quai.core.GetTerminiByHash(hash)
}

func (b *QuaiAPIBackend) IsGenesisHash(hash common.Hash) bool {
	return b.quai.core.IsGenesisHash(hash)
}

func (b *QuaiAPIBackend) UpdateEtxEligibleSlices(header *types.WorkObject, location common.Location) common.Hash {
	return b.quai.core.UpdateEtxEligibleSlices(header, location)
}

func (b *QuaiAPIBackend) WriteAddressOutpoints(outpointsMap map[string]map[string]*types.OutpointAndDenomination) error {
	return b.quai.core.WriteAddressOutpoints(outpointsMap)
}

func (b *QuaiAPIBackend) SanityCheckWorkObjectBlockViewBody(wo *types.WorkObject) error {
	return b.quai.core.SanityCheckWorkObjectBlockViewBody(wo)
}

func (b *QuaiAPIBackend) SanityCheckWorkObjectHeaderViewBody(wo *types.WorkObject) error {
	return b.quai.core.SanityCheckWorkObjectHeaderViewBody(wo)
}

func (b *QuaiAPIBackend) SanityCheckWorkObjectShareViewBody(wo *types.WorkObject) error {
	return b.quai.core.SanityCheckWorkObjectShareViewBody(wo)
}

// ///////////////////////////
// /////// P2P ///////////////
// ///////////////////////////
func (b *QuaiAPIBackend) BroadcastBlock(block *types.WorkObject, location common.Location) error {
	return b.quai.p2p.Broadcast(location, block.ConvertToBlockView())
}

func (b *QuaiAPIBackend) BroadcastHeader(header *types.WorkObject, location common.Location) error {
	return b.quai.p2p.Broadcast(location, header.ConvertToHeaderView())
}

func (b *QuaiAPIBackend) BroadcastWorkShare(workShare *types.WorkObjectShareView, location common.Location) error {
	return b.quai.p2p.Broadcast(location, workShare)
}
