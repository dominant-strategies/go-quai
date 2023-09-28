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

// package quaiapi implements the general Quai API functions.
package quaiapi

import (
	"context"
	"math/big"

	quai "github.com/dominant-strategies/go-quai"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/bloombits"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/eth/downloader"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
)

// Backend interface provides the common API services (that are provided by
// both full and light clients) with access to necessary functions.
type Backend interface {
	// General Quai API
	SyncProgress() quai.SyncProgress
	EventMux() *event.TypeMux

	// General Quai API
	Downloader() *downloader.Downloader
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*big.Int, [][]*big.Int, []*big.Int, []float64, error)
	ChainDb() ethdb.Database
	ExtRPCEnabled() bool
	RPCGasCap() uint64    // global gas cap for eth_call over rpc: DoS protection
	RPCTxFeeCap() float64 // global tx fee cap for all transaction related APIs

	// Blockchain API
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error)
	HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error)
	HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Header, error)
	CurrentHeader() *types.Header
	CurrentBlock() *types.Block
	CurrentLogEntropy() *big.Int
	TotalLogS(header *types.Header) *big.Int
	CalcOrder(header *types.Header) (*big.Int, int, error)
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error)
	BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error)
	BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Block, error)
	StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*state.StateDB, *types.Header, error)
	StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error)
	GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error)
	GetEVM(ctx context.Context, msg core.Message, state *state.StateDB, header *types.Header, vmConfig *vm.Config) (*vm.EVM, func() error, error)
	SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
	SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription
	WriteBlock(block *types.Block)
	Append(header *types.Header, manifest types.BlockManifest, domPendingHeader *types.Header, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, bool, bool, error)
	DownloadBlocksInManifest(manifest types.BlockManifest, entropy *big.Int)
	ConstructLocalMinedBlock(header *types.Header) (*types.Block, error)
	InsertBlock(ctx context.Context, block *types.Block) (int, error)
	PendingBlock() *types.Block
	SubRelayPendingHeader(pendingHeader types.PendingHeader, newEntropy *big.Int, location common.Location, subReorg bool, order int)
	UpdateDom(oldTerminus common.Hash, pendingHeader types.PendingHeader, location common.Location)
	RequestDomToAppendOrFetch(hash common.Hash, entropy *big.Int, order int)
	NewGenesisPendingHeader(pendingHeader *types.Header)
	GetPendingHeader() (*types.Header, error)
	GetManifest(blockHash common.Hash) (types.BlockManifest, error)
	GetSubManifest(slice common.Location, blockHash common.Hash) (types.BlockManifest, error)
	AddPendingEtxs(pEtxs types.PendingEtxs) error
	AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error
	PendingBlockAndReceipts() (*types.Block, types.Receipts)
	GenerateRecoveryPendingHeader(pendingHeader *types.Header, checkpointHashes types.Termini) error
	ProcessingState() bool

	// Transaction pool API
	SendTx(ctx context.Context, signedTx *types.Transaction) error
	GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error)
	GetPoolTransactions() (types.Transactions, error)
	GetPoolTransaction(txHash common.Hash) *types.Transaction
	GetPoolNonce(ctx context.Context, addr common.Address) (uint64, error)
	Stats() (pending int, queued int)
	TxPoolContent() (map[common.InternalAddress]types.Transactions, map[common.InternalAddress]types.Transactions)
	TxPoolContentFrom(addr common.Address) (types.Transactions, types.Transactions)
	SubscribeNewTxsEvent(chan<- core.NewTxsEvent) event.Subscription

	// Filter API
	BloomStatus() (uint64, uint64)
	GetLogs(ctx context.Context, blockHash common.Hash) ([][]*types.Log, error)
	ServiceFilter(ctx context.Context, session *bloombits.MatcherSession)
	SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription
	SubscribePendingLogsEvent(ch chan<- []*types.Log) event.Subscription
	SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription
	SubscribePendingHeaderEvent(ch chan<- *types.Header) event.Subscription

	ChainConfig() *params.ChainConfig
	Engine() consensus.Engine
}

func GetAPIs(apiBackend Backend) []rpc.API {
	nodeCtx := common.NodeLocation.Context()
	nonceLock := new(AddrLocker)
	apis := []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicQuaiAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "quai",
			Version:   "1.0",
			Service:   NewPublicQuaiAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicBlockChainAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "quai",
			Version:   "1.0",
			Service:   NewPublicBlockChainQuaiAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPublicDebugAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPrivateDebugAPI(apiBackend),
		},
	}
	if nodeCtx == common.ZONE_CTX {
		apis = append(apis, rpc.API{
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicTransactionPoolAPI(apiBackend, nonceLock),
			Public:    true,
		})
		apis = append(apis, rpc.API{
			Namespace: "quai",
			Version:   "1.0",
			Service:   NewPublicTransactionPoolAPI(apiBackend, nonceLock),
			Public:    true,
		})
		apis = append(apis, rpc.API{
			Namespace: "txpool",
			Version:   "1.0",
			Service:   NewPublicTxPoolAPI(apiBackend),
			Public:    true,
		})
	}

	return apis
}
