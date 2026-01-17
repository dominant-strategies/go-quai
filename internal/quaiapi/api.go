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

package quaiapi

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"runtime/debug"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"google.golang.org/protobuf/proto"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/consensus/progpow"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai/abi"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/rpc"
)

// PublicQuaiAPI_Deprecated provides an API to access Quai related information.
// It offers only methods that operate on public data that is freely available to anyone.
type PublicQuaiAPI_Deprecated struct {
	b Backend
}

// NewPublicQuaiAPI_Deprecated creates a new Quai protocol API.
func NewPublicQuaiAPI_Deprecated(b Backend) *PublicQuaiAPI_Deprecated {
	return &PublicQuaiAPI_Deprecated{b}
}

// GasPrice returns a suggestion for a gas price for legacy transactions.
func (s *PublicQuaiAPI_Deprecated) GasPrice(ctx context.Context) (*hexutil.Big, error) {
	tipcap := big.NewInt(0)
	if head := s.b.CurrentHeader(); head.BaseFee() != nil {
		tipcap.Add(tipcap, head.BaseFee())
	}
	return (*hexutil.Big)(tipcap), nil
}

// PublicTxPoolAPI offers and API for the transaction pool. It only operates on data that is non confidential.
type PublicTxPoolAPI struct {
	b Backend
}

// NewPublicTxPoolAPI creates a new tx pool service that gives information about the transaction pool.
func NewPublicTxPoolAPI(b Backend) *PublicTxPoolAPI {
	return &PublicTxPoolAPI{b}
}

// Content returns the transactions contained within the transaction pool.
func (s *PublicTxPoolAPI) Content() map[string]map[string]map[string]*RPCTransaction {
	content := map[string]map[string]map[string]*RPCTransaction{
		"pending": make(map[string]map[string]*RPCTransaction),
		"queued":  make(map[string]map[string]*RPCTransaction),
	}
	pending, queue := s.b.TxPoolContent()
	curHeader := s.b.CurrentHeader()
	// Flatten the pending transactions
	for account, txs := range pending {
		dump := make(map[string]*RPCTransaction)
		for _, tx := range txs {
			dump[fmt.Sprintf("%d", tx.Nonce())] = newRPCPendingTransaction(tx, curHeader, s.b.ChainConfig())
		}
		content["pending"][account.Hex()] = dump
	}
	// Flatten the queued transactions
	for account, txs := range queue {
		dump := make(map[string]*RPCTransaction)
		for _, tx := range txs {
			dump[fmt.Sprintf("%d", tx.Nonce())] = newRPCPendingTransaction(tx, curHeader, s.b.ChainConfig())
		}
		content["queued"][account.Hex()] = dump
	}
	return content
}

// ContentFrom returns the transactions contained within the transaction pool.
func (s *PublicTxPoolAPI) ContentFrom(addr common.Address) map[string]map[string]*RPCTransaction {
	content := make(map[string]map[string]*RPCTransaction, 2)
	pending, queue := s.b.TxPoolContentFrom(addr)
	curHeader := s.b.CurrentHeader()

	// Build the pending transactions
	dump := make(map[string]*RPCTransaction, len(pending))
	for _, tx := range pending {
		dump[fmt.Sprintf("%d", tx.Nonce())] = newRPCPendingTransaction(tx, curHeader, s.b.ChainConfig())
	}
	content["pending"] = dump

	// Build the queued transactions
	dump = make(map[string]*RPCTransaction, len(queue))
	for _, tx := range queue {
		dump[fmt.Sprintf("%d", tx.Nonce())] = newRPCPendingTransaction(tx, curHeader, s.b.ChainConfig())
	}
	content["queued"] = dump

	return content
}

// Status returns the number of pending and queued transaction in the pool.
func (s *PublicTxPoolAPI) Status() map[string]hexutil.Uint {
	pending, queue, qi := s.b.Stats()
	return map[string]hexutil.Uint{
		"pending": hexutil.Uint(pending),
		"queued":  hexutil.Uint(queue),
		"qi":      hexutil.Uint(qi),
	}
}

// Inspect retrieves the content of the transaction pool and flattens it into an
// easily inspectable list.
func (s *PublicTxPoolAPI) Inspect() map[string]map[string]map[string]string {
	content := map[string]map[string]map[string]string{
		"pending": make(map[string]map[string]string),
		"queued":  make(map[string]map[string]string),
	}
	pending, queue := s.b.TxPoolContent()

	// Define a formatter to flatten a transaction into a string
	var format = func(tx *types.Transaction) string {
		if to := tx.To(); to != nil {
			return fmt.Sprintf("%s: %v wei + %v gas × %v wei", tx.To().Hex(), tx.Value(), tx.Gas(), tx.GasPrice())
		}
		return fmt.Sprintf("contract creation: %v wei + %v gas × %v wei", tx.Value(), tx.Gas(), tx.GasPrice())
	}
	// Flatten the pending transactions
	for account, txs := range pending {
		dump := make(map[string]string)
		for _, tx := range txs {
			dump[fmt.Sprintf("%d", tx.Nonce())] = format(tx)
		}
		content["pending"][account.Hex()] = dump
	}
	// Flatten the queued transactions
	for account, txs := range queue {
		dump := make(map[string]string)
		for _, tx := range txs {
			dump[fmt.Sprintf("%d", tx.Nonce())] = format(tx)
		}
		content["queued"][account.Hex()] = dump
	}
	return content
}

// GetRollingFeeInfo returns an array of rolling values according to a 100 block peak filter.
// []*hexutil.Big{min, max, avg}
func (s *PublicTxPoolAPI) GetRollingFeeInfo() ([]*hexutil.Big, error) {
	bigMin, bigMax, bigAvg := s.b.GetRollingFeeInfo()

	if bigMin == nil || bigMax == nil || bigAvg == nil {
		return nil, errors.New("no transactions processed to calculate min, max, or avg")
	}

	return []*hexutil.Big{(*hexutil.Big)(bigMin), (*hexutil.Big)(bigMax), (*hexutil.Big)(bigAvg)}, nil
}

// PublicBlockChainAPI provides an API to access the Quai blockchain.
// It offers only methods that operate on public data that is freely available to anyone.
type PublicBlockChainAPI struct {
	b Backend
}

// NewPublicBlockChainAPI creates a new Quai blockchain API.
func NewPublicBlockChainAPI(b Backend) *PublicBlockChainAPI {
	return &PublicBlockChainAPI{b}
}

// ChainId is the replay-protection chain id for the current Quai chain config.
func (api *PublicBlockChainAPI) ChainId() (*hexutil.Big, error) {
	return (*hexutil.Big)(api.b.ChainConfig().ChainID), nil
}

// BlockNumber returns the block number of the chain head.
func (s *PublicBlockChainAPI) BlockNumber() hexutil.Uint64 {
	header, _ := s.b.HeaderByNumber(context.Background(), rpc.LatestBlockNumber) // latest header should always be available
	return hexutil.Uint64(header.NumberU64(s.b.NodeCtx()))
}

// GetBalance returns the amount of wei for the given address in the state of the
// given block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta
// block numbers are also allowed.
func (s *PublicBlockChainAPI) GetBalance(ctx context.Context, address common.AddressBytes, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	addr := common.Bytes20ToAddress(address, s.b.NodeLocation())
	if addr.IsInQiLedgerScope() {
		state, header, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
		if state == nil || err != nil {
			return nil, err
		}

		if header == nil {
			return nil, errors.New("block header not found")
		}

		currHeader := s.b.CurrentHeader()
		if currHeader == nil {
			return nil, errors.New("current header not found")
		}

		if header.Hash() != currHeader.Hash() {
			return (*hexutil.Big)(big.NewInt(0)), errors.New("qi balance query is only supported for the current block")
		}

		utxos, err := s.b.UTXOsByAddress(ctx, addr)
		if utxos == nil || err != nil {
			return nil, err
		}

		if len(utxos) == 0 {
			return (*hexutil.Big)(big.NewInt(0)), nil
		}

		balance := big.NewInt(0)
		for _, utxo := range utxos {
			if utxo.Lock != nil && header.Number(s.b.NodeCtx()).Cmp(utxo.Lock) < 0 {
				continue
			}
			denomination := utxo.Denomination
			value := types.Denominations[denomination]
			if balance == nil {
				balance = new(big.Int).Set(value)
			} else {
				balance.Add(balance, value)
			}
		}
		return (*hexutil.Big)(balance), nil
	} else {
		state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
		if state == nil || err != nil {
			return nil, err
		}
		internal, err := addr.InternalAndQuaiAddress()
		if err != nil {
			return nil, err
		}
		return (*hexutil.Big)(state.GetBalance(internal)), state.Error()
	}
}

// Result structs for GetProof
type AccountResult struct {
	Address      common.MixedcaseAddress `json:"address"`
	AccountProof []string                `json:"accountProof"`
	Balance      *hexutil.Big            `json:"balance"`
	CodeHash     common.Hash             `json:"codeHash"`
	Nonce        hexutil.Uint64          `json:"nonce"`
	StorageHash  common.Hash             `json:"storageHash"`
	StorageProof []StorageResult         `json:"storageProof"`
}

type StorageResult struct {
	Key   string       `json:"key"`
	Value *hexutil.Big `json:"value"`
	Proof []string     `json:"proof"`
}

// GetProof returns the Merkle-proof for a given account and optionally some storage keys.
func (s *PublicBlockChainAPI) GetProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*AccountResult, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getProof can only be called in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getProof call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	storageTrie := state.StorageTrie(internal)
	storageHash := types.EmptyRootHash
	codeHash := state.GetCodeHash(internal)
	storageProof := make([]StorageResult, len(storageKeys))

	// if we have a storageTrie, (which means the account exists), we can update the storagehash
	if storageTrie != nil {
		storageHash = storageTrie.Hash()
	} else {
		// no storageTrie means the account does not exist, so the codeHash is the hash of an empty bytearray.
		codeHash = crypto.Keccak256Hash(nil)
	}

	// create the proof for the storageKeys
	for i, key := range storageKeys {
		if storageTrie != nil {
			proof, storageError := state.GetStorageProof(internal, common.HexToHash(key))
			if storageError != nil {
				return nil, storageError
			}
			storageProof[i] = StorageResult{key, (*hexutil.Big)(state.GetState(internal, common.HexToHash(key)).Big()), toHexSlice(proof)}
		} else {
			storageProof[i] = StorageResult{key, &hexutil.Big{}, []string{}}
		}
	}

	// create the accountProof
	accountProof, proofErr := state.GetProof(internal)
	if proofErr != nil {
		return nil, proofErr
	}

	return &AccountResult{
		Address:      address.MixedcaseAddress(),
		AccountProof: toHexSlice(accountProof),
		Balance:      (*hexutil.Big)(state.GetBalance(internal)),
		CodeHash:     codeHash,
		Nonce:        hexutil.Uint64(state.GetNonce(internal)),
		StorageHash:  storageHash,
		StorageProof: storageProof,
	}, state.Error()
}

// GetHeaderByNumber returns the requested canonical block header.
// * When blockNr is -1 the chain head is returned.
// * When blockNr is -2 the pending chain head is returned.
func (s *PublicBlockChainAPI) GetHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (map[string]interface{}, error) {
	wo, err := s.b.BlockByNumber(ctx, number)
	if wo != nil && err == nil {
		response := RPCMarshalETHHeader(wo.Header(), wo.WorkObjectHeader()) //TODO: mmtx this function will break once extra fields are stripped from header.
		if number == rpc.PendingBlockNumber {
			// Pending header need to nil out a few fields
			for _, field := range []string{"hash", "nonce", "miner"} {
				response[field] = nil
			}
		}
		return response, err
	}
	return nil, err
}

// GetHeaderByHash returns the requested header by hash.
func (s *PublicBlockChainAPI) GetHeaderByHash(ctx context.Context, hash common.Hash) map[string]interface{} {
	wo := s.b.GetBlockByHash(hash)
	if wo != nil {
		return RPCMarshalETHHeader(wo.Header(), wo.WorkObjectHeader())
	}
	return nil
}

// GetBlockByNumber returns the requested canonical block.
//   - When blockNr is -1 the chain head is returned.
//   - When blockNr is -2 the pending chain head is returned.
//   - When fullTx is true all transactions in the block are returned, otherwise
//     only the transaction hash is returned.
func (s *PublicBlockChainAPI) GetBlockByNumber(ctx context.Context, number rpc.BlockNumber, fullTx bool) (map[string]interface{}, error) {
	block, err := s.b.BlockByNumber(ctx, number)
	if block != nil && err == nil {
		response, err := RPCMarshalETHBlock(block, true, fullTx, s.b.NodeLocation())
		if err == nil && number == rpc.PendingBlockNumber {
			// Pending blocks need to nil out a few fields
			for _, field := range []string{"hash", "nonce", "miner"} {
				response[field] = nil
			}
		}
		return response, err
	}
	return nil, err
}

// GetBlockByHash returns the requested block. When fullTx is true all transactions in the block are returned in full
// detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainAPI) GetBlockByHash(ctx context.Context, hash common.Hash, fullTx bool) (map[string]interface{}, error) {
	block, err := s.b.BlockByHash(ctx, hash)
	if block != nil {
		return RPCMarshalETHBlock(block, true, fullTx, s.b.NodeLocation())
	}
	return nil, err
}

// GetUncleByBlockNumberAndIndex returns the uncle block for the given block hash and index. When fullTx is true
// all transactions in the block are returned in full detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainAPI) GetUncleByBlockNumberAndIndex(ctx context.Context, blockNr rpc.BlockNumber, index hexutil.Uint) (map[string]interface{}, error) {
	block, err := s.b.BlockByNumber(ctx, blockNr)
	if block != nil {
		uncles := block.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			s.b.Logger().WithFields(log.Fields{
				"number": blockNr,
				"hash":   block.Hash(),
				"index":  index,
			}).Debug("Requested uncle not found")
			return nil, nil
		}
		return uncles[index].RPCMarshalWorkObjectHeader(s.b.RpcVersion()), nil
	}
	return nil, err
}

// GetUncleByBlockHashAndIndex returns the uncle block for the given block hash and index. When fullTx is true
// all transactions in the block are returned in full detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainAPI) GetUncleByBlockHashAndIndex(ctx context.Context, blockHash common.Hash, index hexutil.Uint) (map[string]interface{}, error) {
	block, err := s.b.BlockByHash(ctx, blockHash)
	if block != nil {
		uncles := block.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			s.b.Logger().WithFields(log.Fields{
				"number": block.Number(s.b.NodeCtx()),
				"hash":   block.Hash(),
				"index":  index,
			}).Debug("Requested uncle not found")
			return nil, nil
		}
		return uncles[index].RPCMarshalWorkObjectHeader(s.b.RpcVersion()), nil
	}
	return nil, err
}

// GetUncleCountByBlockNumber returns number of uncles in the block for the given block number
func (s *PublicBlockChainAPI) GetUncleCountByBlockNumber(ctx context.Context, blockNr rpc.BlockNumber) *hexutil.Uint {
	if block, _ := s.b.BlockByNumber(ctx, blockNr); block != nil {
		n := hexutil.Uint(len(block.Uncles()))
		return &n
	}
	return nil
}

// GetUncleCountByBlockHash returns number of uncles in the block for the given block hash
func (s *PublicBlockChainAPI) GetUncleCountByBlockHash(ctx context.Context, blockHash common.Hash) *hexutil.Uint {
	if block, _ := s.b.BlockByHash(ctx, blockHash); block != nil {
		n := hexutil.Uint(len(block.Uncles()))
		return &n
	}
	return nil
}

// GetCode returns the code stored at the given address in the state for the given block number.
func (s *PublicBlockChainAPI) GetCode(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getCode can only be called in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getCode call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	code := state.GetCode(internal)
	return code, state.Error()
}

// GetStorageAt returns the storage from the state at the given address, key and
// block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta block
// numbers are also allowed.
func (s *PublicBlockChainAPI) GetStorageAt(ctx context.Context, address common.Address, key string, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getStorageAt can only be called in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getStorageAt call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	res := state.GetState(internal, common.HexToHash(key))
	return res[:], state.Error()
}

// OverrideAccount indicates the overriding fields of account during the execution
// of a message call.
// Note, state and stateDiff can't be specified at the same time. If state is
// set, message execution will only use the data in the given state. Otherwise
// if statDiff is set, all diff will be applied first and then execute the call
// message.
type OverrideAccount struct {
	Nonce     *hexutil.Uint64              `json:"nonce"`
	Code      *hexutil.Bytes               `json:"code"`
	Balance   **hexutil.Big                `json:"balance"`
	State     *map[common.Hash]common.Hash `json:"state"`
	StateDiff *map[common.Hash]common.Hash `json:"stateDiff"`
}

// StateOverride is the collection of overridden accounts.
type StateOverride map[common.AddressBytes]OverrideAccount

// Apply overrides the fields of specified accounts into the given state.
func (diff *StateOverride) Apply(state *state.StateDB, nodeLocation common.Location) error {
	nodeCtx := nodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return errors.New("stateOverride Apply can only be called in zone chain")
	}
	if diff == nil {
		return nil
	}
	for addr, account := range *diff {
		internal, err := common.Bytes20ToAddress(addr, nodeLocation).InternalAndQuaiAddress()
		if err != nil {
			return err
		}
		// Override account nonce.
		if account.Nonce != nil {
			state.SetNonce(internal, uint64(*account.Nonce))
		}
		// Override account(contract) code.
		if account.Code != nil {
			state.SetCode(internal, *account.Code)
		}
		// Override account balance.
		if account.Balance != nil {
			state.SetBalance(internal, (*big.Int)(*account.Balance))
		}
		if account.State != nil && account.StateDiff != nil {
			return fmt.Errorf("account %s has both 'state' and 'stateDiff'", addr.Hex())
		}
		// Replace entire state if caller requires.
		if account.State != nil {
			state.SetStorage(internal, *account.State)
		}
		// Apply state diff into specified accounts.
		if account.StateDiff != nil {
			for key, value := range *account.StateDiff {
				state.SetState(internal, key, value)
			}
		}
	}
	return nil
}

func DoCall(ctx context.Context, b Backend, args TransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, overrides *StateOverride, timeout time.Duration, globalGasCap uint64) (*core.ExecutionResult, error) {
	defer func(start time.Time) {
		b.Logger().WithField("runtime", time.Since(start)).Debug("Executing EVM call finished")
	}(time.Now())
	nodeCtx := b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("doCall can only be called in zone chain")
	}
	if !b.ProcessingState() {
		return nil, errors.New("doCall call can only be made on chain processing the state")
	}
	// Reset to and from in case of type unmarshal error
	if args.To != nil {
		to := common.BytesToAddress(args.To.Bytes(), b.NodeLocation())
		args.To = &to
	}
	if args.From != nil {
		from := common.BytesToAddress(args.From.Bytes(), b.NodeLocation())
		args.From = &from
	}
	state, header, err := b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	if err := overrides.Apply(state, b.NodeLocation()); err != nil {
		return nil, err
	}
	// Setup context so it may be cancelled the call has completed
	// or, in case of unmetered gas, setup a context with a timeout.
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	// Make sure the context is cancelled when the call has completed
	// this makes sure resources are cleaned up.
	defer cancel()

	internal, err := args.from(b.NodeLocation()).InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	nonce := state.GetNonce(internal)
	args.Nonce = (*hexutil.Uint64)(&nonce) // Ignore provided nonce, reset to correct nonce

	// Get a new instance of the EVM.
	msg, err := args.ToMessage(globalGasCap, header.BaseFee(), b.NodeLocation())
	if err != nil {
		return nil, err
	}
	parent, err := b.BlockByHash(ctx, header.ParentHash(b.NodeCtx()))
	if err != nil {
		return nil, err
	}
	tracer := vm.NewAccessListTracer(types.AccessList{}, common.ZeroAddress(b.NodeLocation()), common.ZeroAddress(b.NodeLocation()), vm.ActivePrecompiles(b.ChainConfig().Rules(header.Number(nodeCtx)), b.NodeLocation()))
	evm, vmError, err := b.GetEVM(ctx, msg, state, header, parent, &vm.Config{Tracer: tracer, NoBaseFee: true, Debug: true})
	if err != nil {
		return nil, err
	}
	// Wait for the context to be done and cancel the evm. Even if the
	// EVM has finished, cancelling may be done (repeatedly)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		<-ctx.Done()
		evm.Cancel()
	}()

	// Execute the message.
	gp := new(types.GasPool).AddGas(math.MaxUint64)
	result, err := core.ApplyMessage(evm, msg, gp)
	if err := vmError(); err != nil {
		return nil, err
	}

	// If the timer caused an abort, return an appropriate error message
	if evm.Cancelled() {
		return nil, fmt.Errorf("execution aborted (timeout = %v)", timeout)
	}
	if err != nil {
		return result, fmt.Errorf("err: %w (supplied gas %d)", err, msg.Gas())
	}
	return result, nil
}

func newRevertError(result *core.ExecutionResult, nodeLocation common.Location) *revertError {
	reason, errUnpack := abi.UnpackRevert(result.Revert(), nodeLocation)
	err := errors.New("execution reverted")
	if errUnpack == nil {
		err = fmt.Errorf("execution reverted: %v", reason)
	}
	return &revertError{
		error:  err,
		reason: hexutil.Encode(result.Revert()),
	}
}

// revertError is an API error that encompassas an EVM revertal with JSON error
// code and a binary data blob.
type revertError struct {
	error
	reason string // revert reason hex encoded
}

// ErrorCode returns the JSON error code for a revertal.
func (e *revertError) ErrorCode() int {
	return 3
}

// ErrorData returns the hex encoded revert reason.
func (e *revertError) ErrorData() interface{} {
	return e.reason
}

// Call executes the given transaction on the state for the given block number.
//
// Additionally, the caller can specify a batch of contract for fields overriding.
//
// Note, this function doesn't make and changes in the state/blockchain and is
// useful to execute and retrieve values.
func (s *PublicBlockChainAPI) Call(ctx context.Context, args TransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, overrides *StateOverride) (hexutil.Bytes, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("call can only called in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("evm call can only be made on chain processing the state")
	}
	result, err := DoCall(ctx, s.b, args, blockNrOrHash, overrides, 5*time.Second, s.b.RPCGasCap())
	if err != nil {
		return nil, err
	}
	// If the result contains a revert reason, try to unpack and return it.
	if len(result.Revert()) > 0 {
		return nil, newRevertError(result, s.b.NodeLocation())
	}
	return result.Return(), result.Err
}

func DoEstimateGas(ctx context.Context, b Backend, args TransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, gasCap uint64) (hexutil.Uint64, error) {
	nodeCtx := b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return 0, errors.New("doEstimateGas can only be called in zone chain")
	}
	if !b.ProcessingState() {
		return 0, errors.New("doEstimateGas call can only be made on chain processing the state")
	}
	// Binary search the gas requirement, as it may be higher than the amount used
	var (
		lo  uint64 = params.TxGas - 1
		hi  uint64
		cap uint64
	)
	// Use zero address if sender unspecified.
	if args.From == nil || args.From.Equal(common.Address{}) || args.From.Equal(common.Zero) {
		zero := common.ZeroAddress(b.NodeLocation())
		args.From = &zero
	}
	// Determine the highest gas limit can be used during the estimation.
	if args.Gas != nil && uint64(*args.Gas) >= params.TxGas {
		hi = uint64(*args.Gas)
	} else {
		// Retrieve the block to act as the gas ceiling
		header, err := b.HeaderByNumberOrHash(ctx, blockNrOrHash)
		if err != nil {
			return 0, err
		}
		if header == nil {
			return 0, errors.New("block not found")
		}
		hi = header.GasLimit()
		if hi == 0 {
			hi = params.GasCeil
		}
	}
	// Normalize the max fee per gas the call is willing to spend.
	var feeCap *big.Int
	if args.GasPrice != nil {
		feeCap = args.GasPrice.ToInt()
	} else {
		feeCap = common.Big0
	}
	// Recap the highest gas limit with account's available balance.
	if feeCap.BitLen() != 0 {
		state, _, err := b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
		if err != nil {
			return 0, err
		}
		internal, err := args.From.InternalAndQuaiAddress()
		if err != nil {
			return 0, err
		}
		balance := state.GetBalance(internal) // from can't be nil
		available := new(big.Int).Set(balance)
		if args.Value != nil && args.Value.ToInt().Cmp(big.NewInt(0)) != 0 {
			if args.Value.ToInt().Cmp(available) >= 0 {
				return 0, errors.New("insufficient funds for transfer")
			}
			available.Sub(available, args.Value.ToInt())
		}
		allowance := new(big.Int).Div(available, feeCap)

		// If the allowance is larger than maximum uint64, skip checking
		if allowance.IsUint64() && hi > allowance.Uint64() {
			transfer := args.Value
			if transfer == nil {
				transfer = new(hexutil.Big)
			}
			b.Logger().WithFields(log.Fields{
				"original": hi,
				"balance":  balance,
				"sent":     transfer.ToInt(),
				"maxFee":   feeCap,
				"fundable": allowance,
			}).Debug("Gas estimation capped by limited funds")
			hi = allowance.Uint64()
		}
	}
	// Recap the highest gas allowance with specified gascap.
	if gasCap != 0 && hi > gasCap {
		b.Logger().WithFields(log.Fields{
			"requested": hi,
			"cap":       gasCap,
		}).Warn("Caller gas above allowance, capping")
		hi = gasCap
	}
	cap = hi

	// Create a helper to check if a gas allowance results in an executable transaction
	executable := func(gas uint64) (bool, *core.ExecutionResult, error) {
		args.Gas = (*hexutil.Uint64)(&gas)

		result, err := DoCall(ctx, b, args, blockNrOrHash, nil, 0, gasCap)
		if err != nil {
			if errors.Is(err, core.ErrIntrinsicGas) {
				return true, nil, nil // Special case, raise gas limit
			}
			return true, nil, err // Bail out
		}
		return result.Failed(), result, nil
	}
	// Execute the binary search and hone in on an executable gas limit
	for lo+1 < hi {
		mid := (hi + lo) / 2
		failed, _, err := executable(mid)

		// If the error is not nil(consensus error), it means the provided message
		// call or transaction will never be accepted no matter how much gas it is
		// assigned. Return the error directly, don't struggle any more.
		if err != nil {
			return 0, err
		}
		if failed {
			lo = mid
		} else {
			hi = mid
		}
	}
	// Reject the transaction as invalid if it still fails at the highest allowance
	if hi == cap {
		failed, result, err := executable(hi)
		if err != nil {
			return 0, err
		}
		if failed {
			if result != nil && result.Err != vm.ErrOutOfGas {
				if len(result.Revert()) > 0 {
					return 0, newRevertError(result, b.NodeLocation())
				}
				return 0, result.Err
			}
			// Otherwise, the specified gas cap is too low
			return 0, fmt.Errorf("gas required exceeds allowance (%d)", cap)
		}
	}
	// Add 33% to the final gas estimate
	hi = hi + hi/3
	return hexutil.Uint64(hi), nil
}

// EstimateGas returns an estimate of the amount of gas needed to execute the
// given transaction against the current pending block.
func (s *PublicBlockChainAPI) EstimateGas(ctx context.Context, args TransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Uint64, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return 0, errors.New("estimateGas can only be called in zone chain")
	}
	if !s.b.ProcessingState() {
		return 0, errors.New("estimateGas call can only be made on chain processing the state")
	}
	bNrOrHash := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	if blockNrOrHash != nil {
		bNrOrHash = *blockNrOrHash
	}
	return DoEstimateGas(ctx, s.b, args, bNrOrHash, s.b.RPCGasCap())
}

// ExecutionResult groups all structured logs emitted by the EVM
// while replaying a transaction in debug mode as well as transaction
// execution status, the amount of gas used and the return value
type ExecutionResult struct {
	Gas         uint64         `json:"gas"`
	Failed      bool           `json:"failed"`
	ReturnValue string         `json:"returnValue"`
	StructLogs  []StructLogRes `json:"structLogs"`
}

// StructLogRes stores a structured log emitted by the EVM while replaying a
// transaction in debug mode
type StructLogRes struct {
	Pc      uint64             `json:"pc"`
	Op      string             `json:"op"`
	Gas     uint64             `json:"gas"`
	GasCost uint64             `json:"gasCost"`
	Depth   int                `json:"depth"`
	Error   string             `json:"error,omitempty"`
	Stack   *[]string          `json:"stack,omitempty"`
	Memory  *[]string          `json:"memory,omitempty"`
	Storage *map[string]string `json:"storage,omitempty"`
}

// FormatLogs formats EVM returned structured logs for json output
func FormatLogs(logs []vm.StructLog) []StructLogRes {
	formatted := make([]StructLogRes, len(logs))
	for index, trace := range logs {
		formatted[index] = StructLogRes{
			Pc:      trace.Pc,
			Op:      trace.Op.String(),
			Gas:     trace.Gas,
			GasCost: trace.GasCost,
			Depth:   trace.Depth,
			Error:   trace.ErrorString(),
		}
		if trace.Stack != nil {
			stack := make([]string, len(trace.Stack))
			for i, stackValue := range trace.Stack {
				stack[i] = stackValue.Hex()
			}
			formatted[index].Stack = &stack
		}
		if trace.Memory != nil {
			memory := make([]string, 0, (len(trace.Memory)+31)/32)
			for i := 0; i+32 <= len(trace.Memory); i += 32 {
				memory = append(memory, fmt.Sprintf("%x", trace.Memory[i:i+32]))
			}
			formatted[index].Memory = &memory
		}
		if trace.Storage != nil {
			storage := make(map[string]string)
			for i, storageValue := range trace.Storage {
				storage[fmt.Sprintf("%x", i)] = fmt.Sprintf("%x", storageValue)
			}
			formatted[index].Storage = &storage
		}
	}
	return formatted
}

// RPCMarshalHeader converts the given header to the RPC output .
func RPCMarshalETHHeader(head *types.Header, woHeader *types.WorkObjectHeader) map[string]interface{} {
	result := map[string]interface{}{
		"number":           (*hexutil.Big)(woHeader.Number()),
		"hash":             woHeader.Hash(),
		"parentHash":       woHeader.ParentHash(),
		"nonce":            woHeader.Nonce(),
		"mixHash":          woHeader.MixHash(),
		"sha3Uncles":       head.UncleHash().String(),
		"logsBloom":        types.LegacyBloom{},
		"stateRoot":        head.EVMRoot(),
		"miner":            woHeader.PrimaryCoinbase(),
		"difficulty":       (*hexutil.Big)(woHeader.Difficulty()),
		"extraData":        hexutil.Bytes(head.Extra()),
		"gasLimit":         hexutil.Uint64(head.GasLimit()),
		"gasUsed":          hexutil.Uint64(head.GasUsed()),
		"timestamp":        hexutil.Uint64(woHeader.Time()),
		"transactionsRoot": head.TxHash().String(),
		"receiptsRoot":     head.ReceiptHash().String(),
		"baseFeePerGas":    (*hexutil.Big)(head.BaseFee()),
	}
	return result
}

// RPCMarshalBlock converts the given block to the RPC output which depends on fullTx. If inclTx is true transactions are
// returned. When fullTx is true the returned block contains full transaction details, otherwise it will only contain
// transaction hashes.
func RPCMarshalETHBlock(block *types.WorkObject, inclTx bool, fullTx bool, nodeLocation common.Location) (map[string]interface{}, error) {
	fields := RPCMarshalETHHeader(block.Header(), block.WorkObjectHeader())
	fields["size"] = hexutil.Uint64(block.Size())

	if inclTx {
		formatTx := func(tx *types.Transaction) (interface{}, error) {
			return tx.Hash(), nil
		}
		if fullTx {
			formatTx = func(tx *types.Transaction) (interface{}, error) {
				return newRPCTransactionFromBlockHash(block, tx.Hash(), false, nodeLocation), nil
			}
		}
		txs := block.Transactions()
		transactions := make([]interface{}, len(txs))
		var err error
		for i, tx := range txs {
			if transactions[i], err = formatTx(tx); err != nil {
				return nil, err
			}
		}
		fields["transactions"] = transactions
	}
	uncles := block.Uncles()
	uncleHashes := make([]common.Hash, len(uncles))
	for i, uncle := range uncles {
		uncleHashes[i] = uncle.Hash()
	}
	fields["uncles"] = uncleHashes

	return fields, nil
}

// RPCTransaction represents a transaction that will serialize to the RPC representation of a transaction
type RPCTransaction struct {
	BlockHash         *common.Hash             `json:"blockHash"`
	BlockNumber       *hexutil.Big             `json:"blockNumber"`
	From              *common.MixedcaseAddress `json:"from,omitempty"`
	Gas               hexutil.Uint64           `json:"gas"`
	GasPrice          *hexutil.Big             `json:"gasPrice,omitempty"`
	Hash              common.Hash              `json:"hash,omitempty"`
	Input             hexutil.Bytes            `json:"input"`
	Nonce             hexutil.Uint64           `json:"nonce"`
	To                *common.MixedcaseAddress `json:"to,omitempty"`
	TransactionIndex  *hexutil.Uint64          `json:"transactionIndex"`
	Value             *hexutil.Big             `json:"value,omitempty"`
	Type              hexutil.Uint64           `json:"type"`
	Accesses          *types.AccessList        `json:"accessList,omitempty"`
	ChainID           *hexutil.Big             `json:"chainId,omitempty"`
	V                 *hexutil.Big             `json:"v,omitempty"`
	R                 *hexutil.Big             `json:"r,omitempty"`
	S                 *hexutil.Big             `json:"s,omitempty"`
	TxIn              []types.RPCTxIn          `json:"inputs,omitempty"`
	TxOut             []types.RPCTxOut         `json:"outputs,omitempty"`
	UTXOSignature     hexutil.Bytes            `json:"utxoSignature,omitempty"`
	OriginatingTxHash *common.Hash             `json:"originatingTxHash,omitempty"`
	ETXIndex          *hexutil.Uint64          `json:"etxIndex,omitempty"`
	ETxType           *hexutil.Uint64          `json:"etxType,omitempty"`
	ParentHash        *common.Hash             `json:"parentHash,omitempty"`
	MixHash           *common.Hash             `json:"mixHash,omitempty"`
	WorkNonce         *hexutil.Uint64          `json:"workNonce,omitempty"`
}

// newRPCTransaction returns a transaction that will serialize to the RPC
// representation, with the given location metadata set (if available).
func newRPCTransaction(tx *types.Transaction, blockHash common.Hash, blockNumber uint64, index uint64, baseFee *big.Int, nodeLocation common.Location) *RPCTransaction {
	nodeCtx := nodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return nil
	}

	var result *RPCTransaction
	if tx.Type() == types.QiTxType {
		sig := tx.GetSchnorrSignature().Serialize()
		result = &RPCTransaction{
			Type:          hexutil.Uint64(tx.Type()),
			ChainID:       (*hexutil.Big)(tx.ChainId()),
			Hash:          tx.Hash(),
			UTXOSignature: hexutil.Bytes(sig),
			Input:         hexutil.Bytes(tx.Data()),
		}
		for _, txin := range tx.TxIn() {
			result.TxIn = append(result.TxIn, types.RPCTxIn{PreviousOutPoint: types.OutpointJSON{TxHash: txin.PreviousOutPoint.TxHash, Index: hexutil.Uint64(txin.PreviousOutPoint.Index)}, PubKey: hexutil.Bytes(txin.PubKey)})
		}
		for _, txout := range tx.TxOut() {
			result.TxOut = append(result.TxOut, types.RPCTxOut{Denomination: hexutil.Uint(txout.Denomination), Address: common.BytesToAddress(txout.Address, nodeLocation).MixedcaseAddress(), Lock: (*hexutil.Big)(txout.Lock)})
		}
		if blockHash != (common.Hash{}) {
			result.BlockHash = &blockHash
			result.BlockNumber = (*hexutil.Big)(new(big.Int).SetUint64(blockNumber))
			result.TransactionIndex = (*hexutil.Uint64)(&index)
		}
		return result
	}

	switch tx.Type() {
	case types.QuaiTxType:
		// Determine the signer. For replay-protected transactions, use the most permissive
		// signer, because we assume that signers are backwards-compatible with old
		// transactions. For non-protected transactions, the signer is used
		// because the return value of ChainId is zero for those transactions.
		signer := types.LatestSignerForChainID(tx.ChainId(), nodeLocation)
		from, _ := types.Sender(signer, tx)
		result = &RPCTransaction{
			Type:     hexutil.Uint64(tx.Type()),
			From:     from.MixedcaseAddressPtr(),
			Gas:      hexutil.Uint64(tx.Gas()),
			Hash:     tx.Hash(),
			Input:    hexutil.Bytes(tx.Data()),
			Nonce:    hexutil.Uint64(tx.Nonce()),
			Value:    (*hexutil.Big)(tx.Value()),
			ChainID:  (*hexutil.Big)(tx.ChainId()),
			GasPrice: (*hexutil.Big)(tx.GasPrice()),
		}
		if tx.ParentHash() != nil {
			result.ParentHash = tx.ParentHash()
		}
		if tx.MixHash() != nil {
			result.MixHash = tx.MixHash()
		}
		if tx.WorkNonce() != nil {
			workNonce := hexutil.Uint64(tx.WorkNonce().Uint64())
			result.WorkNonce = &workNonce
		}
		if tx.To() != nil {
			result.To = tx.To().MixedcaseAddressPtr()
		}
	case types.ExternalTxType:
		result = &RPCTransaction{
			Type:  hexutil.Uint64(tx.Type()),
			Gas:   hexutil.Uint64(tx.Gas()),
			Hash:  tx.Hash(),
			Input: hexutil.Bytes(tx.Data()),
			To:    tx.To().MixedcaseAddressPtr(),
			Value: (*hexutil.Big)(tx.Value()),
		}
		originatingTxHash := tx.OriginatingTxHash()
		etxIndex := uint64(tx.ETXIndex())
		sender := tx.ETXSender()
		result.OriginatingTxHash = &originatingTxHash
		result.From = sender.MixedcaseAddressPtr()
		result.ETXIndex = (*hexutil.Uint64)(&etxIndex)
		etxType := tx.EtxType()
		result.ETxType = (*hexutil.Uint64)(&etxType)
	}
	if blockHash != (common.Hash{}) {
		result.BlockHash = &blockHash
		result.BlockNumber = (*hexutil.Big)(new(big.Int).SetUint64(blockNumber))
		result.TransactionIndex = (*hexutil.Uint64)(&index)
	}
	if tx.Type() != types.ExternalTxType {
		v, r, s := tx.GetEcdsaSignatureValues()
		result.V = (*hexutil.Big)(v)
		result.R = (*hexutil.Big)(r)
		result.S = (*hexutil.Big)(s)
	}
	al := tx.AccessList()
	result.Accesses = &al
	return result
}

// newRPCPendingTransaction returns a pending transaction that will serialize to the RPC representation
func newRPCPendingTransaction(tx *types.Transaction, current *types.WorkObject, config *params.ChainConfig) *RPCTransaction {
	var baseFee *big.Int
	if current != nil {
		baseFee = current.BaseFee()
	}
	return newRPCTransaction(tx, common.Hash{}, 0, 0, baseFee, config.Location)
}

// newRPCTransactionFromBlockIndex returns a transaction that will serialize to the RPC representation.
func newRPCTransactionFromBlockIndex(b *types.WorkObject, index uint64, etxs bool, nodeLocation common.Location) *RPCTransaction {
	nodeCtx := nodeLocation.Context()
	var txs types.Transactions
	if etxs {
		txs = b.OutboundEtxs()
	} else {
		txs = b.Transactions()
	}
	if index >= uint64(len(txs)) {
		return nil
	}
	return newRPCTransaction(txs[index], b.Hash(), b.NumberU64(nodeCtx), index, b.BaseFee(), nodeLocation)
}

// newRPCRawTransactionFromBlockIndex returns the bytes of a transaction given a block and a transaction index.
func newRPCRawTransactionFromBlockIndex(b *types.WorkObject, index uint64) hexutil.Bytes {
	txs := b.Transactions()
	if index >= uint64(len(txs)) {
		return nil
	}
	blob, _ := txs[index].MarshalBinary()
	return blob
}

// newRPCTransactionFromBlockHash returns a transaction that will serialize to the RPC representation.
func newRPCTransactionFromBlockHash(b *types.WorkObject, hash common.Hash, etxs bool, nodeLocation common.Location) *RPCTransaction {
	if etxs {
		for idx, tx := range b.OutboundEtxs() {
			if tx.Hash() == hash {
				return newRPCTransactionFromBlockIndex(b, uint64(idx), true, nodeLocation)
			}
		}
	}
	for idx, tx := range b.Transactions() {
		if tx.Hash() == hash {
			return newRPCTransactionFromBlockIndex(b, uint64(idx), false, nodeLocation)
		}
	}
	return nil
}

// accessListResult returns an optional accesslist
// Its the result of the `debug_createAccessList` RPC call.
// It contains an error if the transaction itself failed.
type accessListResult struct {
	Accesslist *types.MixedAccessList `json:"accessList"`
	Error      string                 `json:"error,omitempty"`
	GasUsed    hexutil.Uint64         `json:"gasUsed"`
}

// CreateAccessList creates an AccessList for the given transaction.
// Reexec and BlockNrOrHash can be specified to create the accessList on top of a certain state.
func (s *PublicBlockChainAPI) CreateAccessList(ctx context.Context, args TransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (*accessListResult, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("createAccessList can only be called in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("createAccessList call can only be made on chain processing the state")
	}
	bNrOrHash := rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
	if blockNrOrHash != nil {
		bNrOrHash = *blockNrOrHash
	}
	acl, gasUsed, vmerr, err := AccessList(ctx, s.b, bNrOrHash, args)
	if err != nil {
		return nil, err
	}
	result := &accessListResult{Accesslist: acl.ConvertToMixedCase(), GasUsed: hexutil.Uint64(gasUsed)}
	if vmerr != nil {
		result.Error = vmerr.Error()
	}
	return result, nil
}

// AccessList creates an access list for the given transaction.
// If the accesslist creation fails an error is returned.
// If the transaction itself fails, an vmErr is returned.
func AccessList(ctx context.Context, b Backend, blockNrOrHash rpc.BlockNumberOrHash, args TransactionArgs) (acl types.AccessList, gasUsed uint64, vmErr error, err error) {
	nodeLocation := b.NodeLocation()
	nodeCtx := b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, 0, nil, errors.New("AccessList can only be called in zone chain")
	}
	if !b.ProcessingState() {
		return nil, 0, nil, errors.New("accessList call can only be made on chain processing the state")
	}
	// Retrieve the execution context
	db, header, err := b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if db == nil || err != nil {
		return nil, 0, nil, err
	}

	// Ensure any missing fields are filled, extract the recipient and input data
	if err := args.setDefaults(ctx, b, db); err != nil {
		return nil, 0, nil, err
	}
	var to common.Address
	if args.To != nil {
		to = *args.To
	} else {
		to = crypto.CreateAddress(args.from(nodeLocation), uint64(*args.Nonce), *args.Data, nodeLocation)
		if _, err := to.InternalAndQuaiAddress(); err != nil {
			to, _, err = vm.GrindContract(args.from(nodeLocation), uint64(*args.Nonce), math.MaxUint64, 0, crypto.Keccak256Hash(*args.Data), b.CurrentBlock().Number(nodeCtx), nodeLocation)
			if err != nil {
				return nil, 0, nil, err
			}
		}
	}
	// Retrieve the precompiles since they don't need to be added to the access list
	precompiles := vm.ActivePrecompiles(b.ChainConfig().Rules(header.Number(nodeCtx)), nodeLocation)

	// Create an initial tracer
	prevTracer := vm.NewAccessListTracer(nil, args.from(nodeLocation), to, precompiles)
	if args.AccessList != nil {
		prevTracer = vm.NewAccessListTracer(*args.AccessList, args.from(nodeLocation), to, precompiles)
	}
	for {
		// Retrieve the current access list to expand
		accessList := prevTracer.AccessList(nodeLocation)
		b.Logger().WithField("accessList", accessList).Debug("Creating access list")
		// Set the accesslist to the last al
		args.AccessList = &accessList
		// Copy the original db so we don't modify it
		statedb := db.Copy()

		msg, err := args.ToMessage(b.RPCGasCap(), header.BaseFee(), nodeLocation)
		if err != nil {
			return nil, 0, nil, err
		}

		// Apply the transaction with the access list tracer
		tracer := vm.NewAccessListTracer(accessList, args.from(nodeLocation), to, precompiles)
		config := vm.Config{Tracer: tracer, Debug: true, NoBaseFee: true}
		parent, err := b.BlockByHash(ctx, header.ParentHash(b.NodeCtx()))
		if err != nil {
			return nil, 0, nil, err
		}
		vmenv, _, err := b.GetEVM(ctx, msg, statedb, header, parent, &config)
		if err != nil {
			return nil, 0, nil, err
		}
		res, err := core.ApplyMessage(vmenv, msg, new(types.GasPool).AddGas(msg.Gas()))
		if err != nil {
			return nil, 0, nil, fmt.Errorf("failed to apply transaction: %v err: %v", msg, err)
		}
		if tracer.Equal(prevTracer) {
			return accessList, res.UsedGas, res.Err, nil
		}
		prevTracer = tracer
	}
}

// GetTransactionReceipt returns the transaction receipt for the given transaction hash.
// Moved from PublicTransactionPoolAPI
// eth_getTransactionReceipt
func (s *PublicBlockChainAPI) GetTransactionReceipt(ctx context.Context, hash common.Hash) (map[string]interface{}, error) {
	tx, blockHash, blockNumber, index, err := s.b.GetTransaction(ctx, hash)
	if err != nil {
		return nil, nil
	}
	receipts, err := s.b.GetReceipts(ctx, blockHash)
	if err != nil {
		return nil, err
	}
	receipt := &types.Receipt{}
	for _, r := range receipts {
		if r.TxHash == hash {
			receipt = r
		}
	}

	fields := map[string]interface{}{
		"blockHash":         blockHash,
		"blockNumber":       hexutil.Uint64(blockNumber),
		"transactionHash":   hash,
		"transactionIndex":  hexutil.Uint64(index),
		"gasUsed":           hexutil.Uint64(receipt.GasUsed),
		"cumulativeGasUsed": hexutil.Uint64(receipt.CumulativeGasUsed),
		"contractAddress":   nil,
		"logs":              receipt.Logs,
		"logsBloom":         receipt.Bloom.ToLegacyBloom(),
		"type":              hexutil.Uint(tx.Type()),
	}

	if tx.Type() == types.QuaiTxType {
		if to := tx.To(); to != nil {
			fields["to"] = to.Hex()
		}
		if to := tx.To(); to != nil {
			fields["to"] = to.Hex()
		}
		// Derive the sender.
		bigblock := new(big.Int).SetUint64(blockNumber)
		signer := types.MakeSigner(s.b.ChainConfig(), bigblock)
		from, _ := types.Sender(signer, tx)
		fields["from"] = from.Hex()
	}

	if tx.Type() == types.ExternalTxType {
		fields["originatingTxHash"] = tx.OriginatingTxHash()
		fields["etxType"] = hexutil.Uint(tx.EtxType())
	}

	var outBoundEtxs []*RPCTransaction
	for _, tx := range receipt.OutboundEtxs {
		outBoundEtxs = append(outBoundEtxs, newRPCTransaction(tx, blockHash, blockNumber, index, big.NewInt(0), s.b.NodeLocation()))
	}
	if len(receipt.OutboundEtxs) > 0 {
		fields["outboundEtxs"] = outBoundEtxs
	}
	// Assign the effective gas price paid
	header, err := s.b.HeaderByHash(ctx, blockHash)
	if err != nil {
		return nil, err
	}
	if tx.Type() == types.QuaiTxType {
		gasPrice := new(big.Int).Set(tx.GasPrice())
		fields["effectiveGasPrice"] = hexutil.Uint64(gasPrice.Uint64())
	} else {
		// QiTx
		fields["effectiveGasPrice"] = hexutil.Uint64(header.BaseFee().Uint64())
	}

	// Assign receipt status or post state.
	if len(receipt.PostState) > 0 {
		fields["root"] = hexutil.Bytes(receipt.PostState)
	} else {
		fields["status"] = hexutil.Uint(receipt.Status)
	}
	if receipt.Logs == nil {
		fields["logs"] = [][]*types.Log{}
	}
	// If the ContractAddress is 20 0x0 bytes, assume it is not a contract creation
	if !receipt.ContractAddress.Equal(common.Zero) && !receipt.ContractAddress.Equal(common.Address{}) {
		fields["contractAddress"] = receipt.ContractAddress.Hex()
	}
	return fields, nil
}

// PublicTransactionPoolAPI exposes methods for the RPC interface
type PublicTransactionPoolAPI struct {
	b         Backend
	nonceLock *AddrLocker
	signer    types.Signer
}

// NewPublicTransactionPoolAPI creates a new RPC service with methods specific for the transaction pool.
func NewPublicTransactionPoolAPI(b Backend, nonceLock *AddrLocker) *PublicTransactionPoolAPI {
	// The signer used by the API should always be the 'latest' known one because we expect
	// signers to be backwards-compatible with old transactions.
	signer := types.LatestSigner(b.ChainConfig())
	return &PublicTransactionPoolAPI{b, nonceLock, signer}
}

// GetBlockTransactionCountByNumber returns the number of transactions in the block with the given block number.
func (s *PublicTransactionPoolAPI) GetBlockTransactionCountByNumber(ctx context.Context, blockNr rpc.BlockNumber) *hexutil.Uint {
	if block, _ := s.b.BlockByNumber(ctx, blockNr); block != nil {
		n := hexutil.Uint(len(block.Transactions()))
		return &n
	}
	return nil
}

// GetBlockTransactionCountByHash returns the number of transactions in the block with the given hash.
func (s *PublicTransactionPoolAPI) GetBlockTransactionCountByHash(ctx context.Context, blockHash common.Hash) *hexutil.Uint {
	if block, _ := s.b.BlockByHash(ctx, blockHash); block != nil {
		n := hexutil.Uint(len(block.Transactions()))
		return &n
	}
	return nil
}

// GetTransactionByBlockNumberAndIndex returns the transaction for the given block number and index.
func (s *PublicTransactionPoolAPI) GetTransactionByBlockNumberAndIndex(ctx context.Context, blockNr rpc.BlockNumber, index hexutil.Uint) *RPCTransaction {
	if block, _ := s.b.BlockByNumber(ctx, blockNr); block != nil {
		return newRPCTransactionFromBlockIndex(block, uint64(index), false, s.b.NodeLocation())
	}
	return nil
}

// GetTransactionByBlockHashAndIndex returns the transaction for the given block hash and index.
func (s *PublicTransactionPoolAPI) GetTransactionByBlockHashAndIndex(ctx context.Context, blockHash common.Hash, index hexutil.Uint, nodeLocation common.Location) *RPCTransaction {
	if block, _ := s.b.BlockByHash(ctx, blockHash); block != nil {
		return newRPCTransactionFromBlockIndex(block, uint64(index), false, nodeLocation)
	}
	return nil
}

// GetRawTransactionByBlockNumberAndIndex returns the bytes of the transaction for the given block number and index.
func (s *PublicTransactionPoolAPI) GetRawTransactionByBlockNumberAndIndex(ctx context.Context, blockNr rpc.BlockNumber, index hexutil.Uint) hexutil.Bytes {
	if block, _ := s.b.BlockByNumber(ctx, blockNr); block != nil {
		return newRPCRawTransactionFromBlockIndex(block, uint64(index))
	}
	return nil
}

// GetRawTransactionByBlockHashAndIndex returns the bytes of the transaction for the given block hash and index.
func (s *PublicTransactionPoolAPI) GetRawTransactionByBlockHashAndIndex(ctx context.Context, blockHash common.Hash, index hexutil.Uint) hexutil.Bytes {
	if block, _ := s.b.BlockByHash(ctx, blockHash); block != nil {
		return newRPCRawTransactionFromBlockIndex(block, uint64(index))
	}
	return nil
}

// GetTransactionCount returns the number of transactions the given address has sent for the given block number
func (s *PublicTransactionPoolAPI) GetTransactionCount(ctx context.Context, addr common.MixedcaseAddress, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Uint64, error) {
	if !addr.ValidChecksum() {
		return nil, errors.New("address has invalid checksum")
	}
	address := common.BytesToAddress(addr.Address().Bytes(), s.b.NodeLocation())
	// Ask transaction pool for the nonce which includes pending transactions
	if blockNr, ok := blockNrOrHash.Number(); ok && blockNr == rpc.PendingBlockNumber {
		nonce, err := s.b.GetPoolNonce(ctx, address)
		if err != nil {
			return nil, err
		}
		return (*hexutil.Uint64)(&nonce), nil
	}
	// Resolve block number and use its state to ask for the nonce
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	nonce := state.GetNonce(internal)
	return (*hexutil.Uint64)(&nonce), state.Error()
}

// GetTransactionByHash returns the transaction for the given hash
func (s *PublicTransactionPoolAPI) GetTransactionByHash(ctx context.Context, hash common.Hash) (*RPCTransaction, error) {
	// Try to return an already finalized transaction
	tx, blockHash, blockNumber, index, _ := s.b.GetTransaction(ctx, hash)
	if tx != nil {
		header, err := s.b.HeaderByHash(ctx, blockHash)
		if err != nil {
			return nil, err
		}
		return newRPCTransaction(tx, blockHash, blockNumber, index, header.BaseFee(), s.b.NodeLocation()), nil
	}
	// No finalized transaction, try to retrieve it from the pool
	if tx := s.b.GetPoolTransaction(hash); tx != nil {
		return newRPCPendingTransaction(tx, s.b.CurrentBlock(), s.b.ChainConfig()), nil
	}

	// Transaction unknown, return as such
	return nil, nil
}

// GetRawTransactionByHash returns the bytes of the transaction for the given hash.
func (s *PublicTransactionPoolAPI) GetRawTransactionByHash(ctx context.Context, hash common.Hash) (hexutil.Bytes, error) {
	// Retrieve a finalized transaction, or a pooled otherwise
	tx, _, _, _, err := s.b.GetTransaction(ctx, hash)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		if tx = s.b.GetPoolTransaction(hash); tx == nil {
			// Transaction not found anywhere, abort
			return nil, nil
		}
	}
	// Serialize to RLP and return
	return tx.MarshalBinary()
}

// SubmitTransaction is a helper function that submits tx to txPool and logs a message.
func SubmitTransaction(ctx context.Context, b Backend, tx *types.Transaction) (common.Hash, error) {
	if tx == nil {
		return common.Hash{}, errors.New("transaction is nil")
	}
	nodeLocation := b.NodeLocation()
	nodeCtx := b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return common.Hash{}, errors.New("submitTransaction can only be called in zone chain")
	}
	if !b.ProcessingState() {
		return common.Hash{}, errors.New("submitTransaction call can only be made on chain processing the state")
	}
	if tx.Type() == types.QiTxType {
		// Send the tx to tx pool sharing clients
		if err := b.SendTxToSharingClients(tx); err != nil {
			return common.Hash{}, err
		}
	} else {
		// If the transaction fee cap is already specified, ensure the
		// fee of the given transaction is _reasonable_.
		if err := checkTxFee(tx.GasPrice(), tx.Gas(), b.RPCTxFeeCap()); err != nil {
			return common.Hash{}, err
		}

		// Send the tx to tx pool sharing clients
		if err := b.SendTxToSharingClients(tx); err != nil {
			return common.Hash{}, err
		}
		// Print a log with full tx details for manual investigations and interventions
		signer := types.MakeSigner(b.ChainConfig(), b.CurrentHeader().Number(nodeCtx))
		from, err := types.Sender(signer, tx)
		if err != nil {
			return common.Hash{}, err
		}

		if tx.To() == nil {
			addr := crypto.CreateAddress(from, tx.Nonce(), tx.Data(), nodeLocation)
			b.Logger().WithFields(log.Fields{
				"hash":     tx.Hash().Hex(),
				"from":     from,
				"nonce":    tx.Nonce(),
				"contract": addr.Hex(),
				"value":    tx.Value(),
			}).Debug("Submitted contract creation")
		} else {
			b.Logger().WithFields(log.Fields{
				"hash":      tx.Hash().Hex(),
				"from":      from,
				"nonce":     tx.Nonce(),
				"recipient": tx.To(),
				"value":     tx.Value(),
			}).Debug("Submitted transaction")
		}
	}
	return tx.Hash(), nil
}

// SendRawTransaction will add the signed transaction to the transaction pool.
// The sender is responsible for signing the transaction and using the correct nonce.
func (s *PublicTransactionPoolAPI) SendRawTransaction(ctx context.Context, input hexutil.Bytes) (common.Hash, error) {
	tx := new(types.Transaction)
	protoTransaction := new(types.ProtoTransaction)
	err := proto.Unmarshal(input, protoTransaction)
	if err != nil {
		return common.Hash{}, err
	}
	err = tx.ProtoDecode(protoTransaction, s.b.NodeLocation())
	if err != nil {
		return common.Hash{}, err
	}
	return SubmitTransaction(ctx, s.b, tx)
}

func (s *PublicTransactionPoolAPI) ReceiveTxFromPoolSharingClient(ctx context.Context, input hexutil.Bytes) error {
	tx := new(types.Transaction)
	protoTransaction := new(types.ProtoTransaction)
	err := proto.Unmarshal(input, protoTransaction)
	if err != nil {
		return err
	}
	err = tx.ProtoDecode(protoTransaction, s.b.NodeLocation())
	if err != nil {
		return err
	}

	s.b.Logger().Info("Received a tx from pool sharing client")

	_, err = SubmitTransaction(ctx, s.b, tx)
	if err != nil {
		return err
	}
	return nil
}

// PublicDebugAPI is the collection of Quai APIs exposed over the public
// debugging endpoint.
type PublicDebugAPI struct {
	b Backend
}

// NewPublicDebugAPI creates a new API definition for the public debug methods
// of the Quai service.
func NewPublicDebugAPI(b Backend) *PublicDebugAPI {
	return &PublicDebugAPI{b: b}
}

// GetBlockRlp retrieves the RLP encoded for of a single block.
func (api *PublicDebugAPI) GetBlockRlp(ctx context.Context, number uint64) (string, error) {
	block, _ := api.b.BlockByNumber(ctx, rpc.BlockNumber(number))
	if block == nil {
		return "", fmt.Errorf("block #%d not found", number)
	}
	encoded, err := rlp.EncodeToBytes(block)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", encoded), nil
}

// PrintBlock retrieves a block and returns its pretty printed form.
func (api *PublicDebugAPI) PrintBlock(ctx context.Context, number uint64) (string, error) {
	block, _ := api.b.BlockByNumber(ctx, rpc.BlockNumber(number))
	if block == nil {
		return "", fmt.Errorf("block #%d not found", number)
	}
	return spew.Sdump(block), nil
}

// SeedHash retrieves the seed hash of a block.
func (api *PublicDebugAPI) SeedHash(ctx context.Context, number uint64) (string, error) {
	block, _ := api.b.BlockByNumber(ctx, rpc.BlockNumber(number))
	if block == nil {
		return "", fmt.Errorf("block #%d not found", number)
	}
	return fmt.Sprintf("0x%x", progpow.SeedHash(number)), nil
}

// GetWorkshareLRUDump returns a JSON-encoded dump of all workshares in the LRU caches.
// The limit parameter caps the number of entries returned per list (0 or negative means no limit).
func (s *PublicDebugAPI) GetWorkshareLRUDump(ctx context.Context, limit int) (map[string]interface{}, error) {
	if limit <= 0 {
		limit = 0 // No limit
	}
	dump := s.b.GetWorkshareLRUDump(limit)
	return dump, nil
}

// PrivateDebugAPI is the collection of Quai APIs exposed over the private
// debugging endpoint.
type PrivateDebugAPI struct {
	b Backend
}

// NewPrivateDebugAPI creates a new API definition for the private debug methods
// of the Quai service.
func NewPrivateDebugAPI(b Backend) *PrivateDebugAPI {
	return &PrivateDebugAPI{b: b}
}

// ChaindbProperty returns leveldb properties of the key-value database.
func (api *PrivateDebugAPI) ChaindbProperty(property string) (string, error) {
	if property == "" {
		property = "leveldb.stats"
	} else if !strings.HasPrefix(property, "leveldb.") {
		property = "leveldb." + property
	}
	return api.b.ChainDb().Stat(property)
}

// ChaindbCompact flattens the entire key-value database into a single level,
// removing all unused slots and merging all keys.
func (api *PrivateDebugAPI) ChaindbCompact() error {
	for b := byte(0); b < 255; b++ {
		api.b.Logger().WithField("range", fmt.Sprintf("0x%0.2X-0x%0.2X", b, b+1)).Info("Compacting chain database")
		if err := api.b.ChainDb().Compact([]byte{b}, []byte{b + 1}); err != nil {
			api.b.Logger().WithField("err", err).Error("Database compaction failed")
			return err
		}
	}
	return nil
}

func NewPublicNetAPI(networkVersion uint64) *PublicNetAPI {
	return &PublicNetAPI{networkVersion}
}

// PublicNetAPI offers network related RPC methods
type PublicNetAPI struct {
	networkVersion uint64
}

// Listening returns an indication if the node is listening for network connections.
func (s *PublicNetAPI) Listening() bool {
	return true // always listening
}

// Version returns the current Quai protocol version.
func (s *PublicNetAPI) Version() string {
	return fmt.Sprintf("%d", s.networkVersion)
}

// checkTxFee is an internal function used to check whether the fee of
// the given transaction is _reasonable_(under the cap).
func checkTxFee(gasPrice *big.Int, gas uint64, cap float64) error {
	// Short circuit if there is no cap for transaction fee at all.
	if cap == 0 {
		return nil
	}
	feeEth := new(big.Float).Quo(new(big.Float).SetInt(new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gas))), new(big.Float).SetInt(big.NewInt(params.Ether)))
	feeFloat, _ := feeEth.Float64()
	if feeFloat > cap {
		return fmt.Errorf("tx fee (%.2f ether) exceeds the configured cap (%.2f ether)", feeFloat, cap)
	}
	return nil
}

// toHexSlice creates a slice of hex-strings based on []byte.
func toHexSlice(b [][]byte) []string {
	r := make([]string, len(b))
	for i := range b {
		r[i] = hexutil.Encode(b[i])
	}
	return r
}

type PublicWorkSharesAPI struct {
	b      Backend
	txPool *PublicTransactionPoolAPI
}

// NewPublicWorkSharesAPI creates a new RPC service with methods specific for the transaction pool.
func NewPublicWorkSharesAPI(txpoolAPi *PublicTransactionPoolAPI, b Backend) *PublicWorkSharesAPI {
	api := &PublicWorkSharesAPI{
		b,
		txpoolAPi,
	}

	return api
}

// GetWork returns the current workObjectHeader and the workThreshold to recognize a share
func (s *PublicWorkSharesAPI) GetWork(ctx context.Context) (hexutil.Bytes, int, error) {
	header := s.b.CurrentHeader()

	protoWo, err := header.WorkObjectHeader().ProtoEncode()
	if err != nil {
		return nil, -1, err
	}

	protoBytes, err := proto.Marshal(protoWo)
	if err != nil {
		return nil, -1, err
	}

	return protoBytes, s.b.GetWorkShareThreshold(), nil
}

// GetWorkShareThreshold returns the minimal WorkShareThreshold that this node will accept
func (s *PublicWorkSharesAPI) GetWorkShareThreshold(ctx context.Context) (int, error) {
	return s.b.GetWorkShareThreshold(), nil
}

func (s *PublicWorkSharesAPI) ReceiveSubWorkshare(ctx context.Context, input hexutil.Bytes) error {
	protoSubWorkshare := &types.ProtoWorkObject{}
	err := proto.Unmarshal(input, protoSubWorkshare)
	if err != nil {
		return err
	}

	workShare := &types.WorkObject{}
	workShare.ProtoDecode(protoSubWorkshare, s.b.NodeLocation(), types.WorkShareTxObject)

	// check if the workshare is valid before broadcasting as a sanity
	workShareValidity := s.b.CheckIfValidWorkShare(workShare.WorkObjectHeader())
	if workShareValidity == types.Valid {
		s.b.Logger().WithField("number", workShare.WorkObjectHeader().NumberU64()).Info("Received Work Share")

		// Emit workshare event for real-time updates
		s.b.SendNewWorkshareEvent(workShare)

		shareView := workShare.ConvertToWorkObjectShareView(workShare.Transactions())
		err = s.b.BroadcastWorkShare(shareView, s.b.NodeLocation())
		if err != nil {
			s.b.Logger().WithField("err", err).Error("Error broadcasting work share")
		}
		txEgressCounter.Add(float64(len(shareView.WorkObject.Transactions())))
		s.b.Logger().WithFields(log.Fields{"tx count": len(workShare.Transactions())}).Info("Broadcasted workshares with txs")
		return nil
	} else if workShareValidity == types.Sub {
		tx := workShare.Tx()
		return s.b.SendTx(ctx, tx)
	} else {
		return errors.New("work share is invalid")
	}
}
