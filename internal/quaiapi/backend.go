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

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/bloombits"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// Backend interface provides the common API services (that are provided by
// both full and light clients) with access to necessary functions.
type Backend interface {
	// General Quai API
	EventMux() *event.TypeMux

	// General Quai API
	ChainDb() ethdb.Database
	ExtRPCEnabled() bool
	RPCGasCap() uint64    // global gas cap for eth_call over rpc: DoS protection
	RPCTxFeeCap() float64 // global tx fee cap for all transaction related APIs

	// Blockchain API
	NodeLocation() common.Location
	NodeCtx() int
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
	HeaderByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error)
	HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.WorkObject, error)
	CurrentHeader() *types.WorkObject
	CurrentBlock() *types.WorkObject
	CurrentLogEntropy() *big.Int
	TotalLogEntropy(header *types.WorkObject) *big.Int
	CalcOrder(header *types.WorkObject) (*big.Int, int, error)
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
	BlockByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error)
	BlockOrCandidateByHash(hash common.Hash) *types.WorkObject
	BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.WorkObject, error)
	StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*state.StateDB, *types.WorkObject, error)
	StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*state.StateDB, *types.WorkObject, error)
	AddressOutpoints(ctx context.Context, address common.Address) ([]*types.OutpointAndDenomination, error)
	GetOutpointsByAddressAndRange(ctx context.Context, address common.Address, start, end uint32) ([]*types.OutpointAndDenomination, error)
	UTXOsByAddress(ctx context.Context, address common.Address) ([]*types.UtxoEntry, error)
	GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error)
	GetEVM(ctx context.Context, msg core.Message, state *state.StateDB, header *types.WorkObject, parent *types.WorkObject, vmConfig *vm.Config) (*vm.EVM, func() error, error)
	SetCurrentExpansionNumber(expansionNumber uint8)
	SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
	SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription
	WriteBlock(block *types.WorkObject)
	Append(header *types.WorkObject, manifest types.BlockManifest, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, error)
	DownloadBlocksInManifest(hash common.Hash, manifest types.BlockManifest, entropy *big.Int)
	ConstructLocalMinedBlock(header *types.WorkObject) (*types.WorkObject, error)
	InsertBlock(ctx context.Context, block *types.WorkObject) (int, error)
	PendingBlock() *types.WorkObject
	RequestDomToAppendOrFetch(hash common.Hash, entropy *big.Int, order int)
	NewGenesisPendingHeader(pendingHeader *types.WorkObject, domTerminus common.Hash, hash common.Hash) error
	GetPendingHeader(powID types.PowID, coinbase common.Address, extraData []byte) (*types.WorkObject, error)
	GetPendingBlockBody(powId types.PowID, sealHash common.Hash) *types.WorkObject
	AddPendingAuxPow(powId types.PowID, sealHash common.Hash, auxpow *types.AuxPow)
	SubmitBlock(raw hexutil.Bytes, powId types.PowID) (common.Hash, uint64, types.WorkShareValidity, error)
	ReceiveMinedHeader(woHeader *types.WorkObject) error
	ReceiveWorkShare(workShare *types.WorkObjectHeader) error
	GetTxsFromBroadcastSet(hash common.Hash) (types.Transactions, error)
	GetManifest(blockHash common.Hash) (types.BlockManifest, error)
	GetSubManifest(slice common.Location, blockHash common.Hash) (types.BlockManifest, error)
	AddPendingEtxs(pEtxs types.PendingEtxs) error
	AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error
	PendingBlockAndReceipts() (*types.WorkObject, types.Receipts)
	GenerateRecoveryPendingHeader(pendingHeader *types.WorkObject, checkpointHashes types.Termini) error
	GetPendingEtxsRollupFromSub(hash common.Hash, location common.Location) (types.PendingEtxsRollup, error)
	GetPendingEtxsFromSub(hash common.Hash, location common.Location) (types.PendingEtxs, error)
	ProcessingState() bool
	GetSlicesRunning() []common.Location
	SetSubInterface(subInterface core.CoreBackend, location common.Location)
	AddGenesisPendingEtxs(block *types.WorkObject)
	SubscribeExpansionEvent(ch chan<- core.ExpansionEvent) event.Subscription
	WriteGenesisBlock(block *types.WorkObject, location common.Location)
	SendWorkShare(workShare *types.WorkObjectHeader) error
	SendAuxPowTemplate(auxTemplate *types.AuxTemplate) error
	GetBestAuxTemplate(powId types.PowID) *types.AuxTemplate
	CheckIfValidWorkShare(workShare *types.WorkObjectHeader) types.WorkShareValidity
	SetDomInterface(domInterface core.CoreBackend)
	BroadcastWorkShare(workShare *types.WorkObjectShareView, location common.Location) error
	GetMaxTxInWorkShare() uint64
	GetExpansionNumber() uint8
	SuggestFinalityDepth(ctx context.Context, qiValue *big.Int, correlatedRisk *big.Int) (*big.Int, error)
	WorkShareDistance(wo *types.WorkObject, ws *types.WorkObjectHeader) (*big.Int, error)
	GeneratePendingHeader(block *types.WorkObject, fill bool) (*types.WorkObject, error)
	MakeFullPendingHeader(primePh, regionPh, zonePh *types.WorkObject) *types.WorkObject
	CheckInCalcOrderCache(hash common.Hash) (*big.Int, int, bool)
	AddToCalcOrderCache(hash common.Hash, order int, intrinsicS *big.Int)
	GetPrimeBlock(blockHash common.Hash) *types.WorkObject
	GetKQuaiAndUpdateBit(blockHash common.Hash) (*big.Int, uint8, error)
	GetWorkShareThreshold() int
	GetMinerEndpoints() []string
	GetWorkShareP2PThreshold() int
	SetWorkShareP2PThreshold(threshold int)
	GetWorkshareLRUDump(limit int) map[string]interface{}
	UncleWorkShareClassification(workshare *types.WorkObjectHeader) types.WorkShareValidity
	CheckWorkThreshold(header *types.WorkObjectHeader, threshold int) bool
	VerifySeal(header *types.WorkObjectHeader) (common.Hash, error)
	ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error)
	GetBlockByHash(hash common.Hash) *types.WorkObject
	Database() ethdb.Database
	CalcBaseFee(wo *types.WorkObject) *big.Int
	Config() *params.ChainConfig
	GetTerminiByHash(hash common.Hash) *types.Termini
	GetHeaderByHash(hash common.Hash) *types.WorkObject
	IsGenesisHash(hash common.Hash) bool
	GetQuaiHeaderForDonorHash(donorHash common.Hash) *types.WorkObjectHeader
	GetBlockForWorkShareHash(workshareHash common.Hash) *types.WorkObject

	BadHashExistsInChain() bool
	IsBlockHashABadHash(hash common.Hash) bool

	// Validator methods that checks the sanity of the Body
	SanityCheckWorkObjectBlockViewBody(wo *types.WorkObject) error
	SanityCheckWorkObjectHeaderViewBody(wo *types.WorkObject) error
	SanityCheckWorkObjectShareViewBody(wo *types.WorkObject) error
	ApplyPoWFilter(wo *types.WorkObject) pubsub.ValidationResult

	// Transaction pool API
	SendTx(ctx context.Context, signedTx *types.Transaction) error
	SendRemoteTxs(txs types.Transactions) []error
	GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error)
	GetPoolTransactions() (types.Transactions, error)
	GetPoolTransaction(txHash common.Hash) *types.Transaction
	GetPoolNonce(ctx context.Context, addr common.Address) (uint64, error)
	Stats() (pending int, queued int, qi int)
	TxPoolContent() (map[common.InternalAddress]types.Transactions, map[common.InternalAddress]types.Transactions)
	TxPoolContentFrom(addr common.Address) (types.Transactions, types.Transactions)
	GetPoolGasPrice() *big.Int
	SendTxToSharingClients(tx *types.Transaction) error
	GetRollingFeeInfo() (min, max, avg *big.Int)

	// Filter API
	BloomStatus() (uint64, uint64)
	GetLogs(ctx context.Context, blockHash common.Hash) ([][]*types.Log, error)
	ServiceFilter(ctx context.Context, session *bloombits.MatcherSession)
	SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription
	SubscribePendingLogsEvent(ch chan<- []*types.Log) event.Subscription
	SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription
	SubscribePendingHeaderEvent(ch chan<- *types.WorkObject) event.Subscription
	SubscribeNewWorkshareEvent(ch chan<- core.NewWorkshareEvent) event.Subscription
	SendNewWorkshareEvent(workshare *types.WorkObject)

	ChainConfig() *params.ChainConfig
	Engine(header *types.WorkObjectHeader) consensus.Engine

	RpcVersion() string

	// Logger
	Logger() *log.Logger

	// P2P apis
	BroadcastBlock(block *types.WorkObject, location common.Location) error
	BroadcastHeader(header *types.WorkObject, location common.Location) error
	BroadcastAuxTemplate(auxTemplate *types.AuxTemplate, location common.Location) error
}

func GetAPIs(apiBackend Backend) []rpc.API {
	nodeCtx := apiBackend.NodeCtx()
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
		{
			Namespace: "net",
			Version:   "1.0",
			Service:   NewPublicNetAPI(apiBackend.ChainConfig().ChainID.Uint64()),
			Public:    true,
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
		apis = append(apis, rpc.API{
			Namespace: "workshare",
			Version:   "1.0",
			Service:   NewPublicWorkSharesAPI(apis[7].Service.(*PublicTransactionPoolAPI), apiBackend),
			Public:    true,
		})
	}

	return apis
}
