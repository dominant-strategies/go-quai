// Copyright 2021 The go-ethereum Authors
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

package tracers

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/rpc"
)

const (
	// defaultTraceTimeout is the amount of time a single transaction can execute
	// by default before being forcefully aborted.
	defaultTraceTimeout = 5 * time.Second

	// defaultTraceReexec is the number of blocks the tracer is willing to go back
	// and reexecute to produce missing historical state necessary to run a specific
	// trace.
	defaultTraceReexec = uint64(128)
)

// Backend interface provides the common API services (that are provided by
// both full and light clients) with access to necessary functions.
type Backend interface {
	HeaderByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error)
	GetHeaderOrCandidateByHash(hash common.Hash) *types.WorkObject
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
	BlockByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error)
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
	GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error)
	RPCGasCap() uint64
	ChainConfig() *params.ChainConfig
	Engine() consensus.Engine
	ChainDb() ethdb.Database
	StateAtBlock(ctx context.Context, block *types.WorkObject, reexec uint64, base *state.StateDB, checkLive bool) (*state.StateDB, error)
	StateAtTransaction(ctx context.Context, block *types.WorkObject, txIndex int, reexec uint64) (core.Message, vm.BlockContext, *state.StateDB, error)
	Logger() *log.Logger
	NodeLocation() common.Location
	CheckIfEtxIsEligible(hash common.Hash, location common.Location) bool
	IsGenesisHash(hash common.Hash) bool
	NodeCtx() int
	AddToCalcOrderCache(common.Hash, int, *big.Int)
	CalcBaseFee(wo *types.WorkObject) *big.Int
	CheckInCalcOrderCache(hash common.Hash) (*big.Int, int, bool)
}

// API is the collection of tracing APIs exposed over the private debugging endpoint.
type API struct {
	backend Backend
}

// NewAPI creates a new API definition for the tracing methods of the Ethereum service.
func NewAPI(backend Backend) *API {
	return &API{backend: backend}
}

type chainContext struct {
	api *API
	ctx context.Context
}

func (context *chainContext) Engine() consensus.Engine {
	return context.api.backend.Engine()
}

func (context *chainContext) GetHeader(hash common.Hash, number uint64) *types.WorkObject {
	header, err := context.api.backend.HeaderByNumber(context.ctx, rpc.BlockNumber(number))
	if err != nil {
		return nil
	}
	if header.Hash() == hash {
		return header
	}
	header, err = context.api.backend.HeaderByHash(context.ctx, hash)
	if err != nil {
		return nil
	}
	return header
}

func (context *chainContext) CheckIfEtxIsEligible(hash common.Hash, location common.Location) bool {
	return context.api.backend.CheckIfEtxIsEligible(hash, location)
}

func (context *chainContext) GetHeaderByHash(hash common.Hash) *types.WorkObject {
	header, err := context.api.backend.HeaderByHash(context.ctx, hash)
	if err != nil {
		return nil
	}
	return header
}

func (context *chainContext) IsGenesisHash(hash common.Hash) bool {
	return context.api.backend.IsGenesisHash(hash)
}

func (context *chainContext) NodeCtx() int {
	return context.api.backend.NodeCtx()
}

func (context *chainContext) GetHeaderOrCandidateByHash(hash common.Hash) *types.WorkObject {
	return context.api.backend.GetHeaderOrCandidateByHash(hash)
}

func (context *chainContext) GetBlockByHash(hash common.Hash) *types.WorkObject {
	block, err := context.api.backend.BlockByHash(context.ctx, hash)
	if err != nil {
		return nil
	}
	return block
}

func (context *chainContext) AddToCalcOrderCache(hash common.Hash, order int, entropy *big.Int) {
	context.api.backend.AddToCalcOrderCache(hash, order, entropy)
}

func (context *chainContext) CalcBaseFee(wo *types.WorkObject) *big.Int {
	return context.api.backend.CalcBaseFee(wo)
}

func (context *chainContext) CheckInCalcOrderCache(hash common.Hash) (*big.Int, int, bool) {
	return context.api.backend.CheckInCalcOrderCache(hash)
}

// chainContext construts the context reader which is used by the evm for reading
// the necessary chain context.
func (api *API) chainContext(ctx context.Context) core.ChainContext {
	return &chainContext{api: api, ctx: ctx}
}

// blockByNumber is the wrapper of the chain access function offered by the backend.
// It will return an error if the block is not found.
func (api *API) blockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error) {
	block, err := api.backend.BlockByNumber(ctx, number)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("block #%d not found", number)
	}
	return block, nil
}

// blockByHash is the wrapper of the chain access function offered by the backend.
// It will return an error if the block is not found.
func (api *API) blockByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error) {
	block, err := api.backend.BlockByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("block %s not found", hash.Hex())
	}
	return block, nil
}

// blockByNumberAndHash is the wrapper of the chain access function offered by
// the backend. It will return an error if the block is not found.
//
// Note this function is friendly for the light client which can only retrieve the
// historical(before the CHT) header/block by number.
func (api *API) blockByNumberAndHash(ctx context.Context, number rpc.BlockNumber, hash common.Hash) (*types.WorkObject, error) {
	block, err := api.blockByNumber(ctx, number)
	if err != nil {
		return nil, err
	}
	if block.Hash() == hash {
		return block, nil
	}
	return api.blockByHash(ctx, hash)
}

// TraceConfig holds extra parameters to trace functions.
type TraceConfig struct {
	*vm.LogConfig
	Tracer  *string
	Timeout *string
	Reexec  *uint64
}

// TraceCallConfig is the config for traceCall API. It holds one more
// field to override the state for tracing.
type TraceCallConfig struct {
	*vm.LogConfig
	Tracer         *string
	Timeout        *string
	Reexec         *uint64
	StateOverrides *quaiapi.StateOverride
}

// StdTraceConfig holds extra parameters to standard-json trace functions.
type StdTraceConfig struct {
	vm.LogConfig
	Reexec *uint64
	TxHash common.Hash
}

// txTraceResult is the result of a single transaction trace.
type txTraceResult struct {
	Result interface{} `json:"result,omitempty"` // Trace results produced by the tracer
	Error  string      `json:"error,omitempty"`  // Trace failure produced by the tracer
}

// blockTraceTask represents a single block trace task when an entire chain is
// being traced.
type blockTraceTask struct {
	statedb *state.StateDB    // Intermediate state prepped for tracing
	block   *types.WorkObject // Block to trace the transactions from
	rootref common.Hash       // Trie root reference held for this task
	results []*txTraceResult  // Trace results procudes by the task
}

// blockTraceResult represets the results of tracing a single block when an entire
// chain is being traced.
type blockTraceResult struct {
	Block  hexutil.Uint64   `json:"block"`  // Block number corresponding to this trace
	Hash   common.Hash      `json:"hash"`   // Block hash corresponding to this trace
	Traces []*txTraceResult `json:"traces"` // Trace results produced by the task
}

// txTraceTask represents a single transaction trace task when an entire block
// is being traced.
type txTraceTask struct {
	statedb *state.StateDB // Intermediate state prepped for tracing
	index   int            // Transaction offset in the block
}

// TraceChain returns the structured logs created during the execution of EVM
// between two blocks (excluding start) and returns them as a JSON object.
func (api *API) TraceChain(ctx context.Context, start, end rpc.BlockNumber, config *TraceConfig) (*rpc.Subscription, error) { // Fetch the block interval that we want to trace
	from, err := api.blockByNumber(ctx, start)
	if err != nil {
		return nil, err
	}
	to, err := api.blockByNumber(ctx, end)
	if err != nil {
		return nil, err
	}
	if from.Number(common.ZONE_CTX).Cmp(to.Number(common.ZONE_CTX)) >= 0 {
		return nil, fmt.Errorf("end block (#%d) needs to come after start block (#%d)", end, start)
	}
	return api.traceChain(ctx, from, to, config)
}

// traceChain configures a new tracer according to the provided configuration, and
// executes all the transactions contained within. The return value will be one item
// per transaction, dependent on the requested tracer.
func (api *API) traceChain(ctx context.Context, start, end *types.WorkObject, config *TraceConfig) (*rpc.Subscription, error) {
	// Tracing a chain is a **long** operation, only do with subscriptions
	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}
	sub := notifier.CreateSubscription()

	// Prepare all the states for tracing. Note this procedure can take very
	// long time. Timeout mechanism is necessary.
	reexec := defaultTraceReexec
	if config != nil && config.Reexec != nil {
		reexec = *config.Reexec
	}
	blocks := int(end.NumberU64(common.ZONE_CTX) - start.NumberU64(common.ZONE_CTX))
	threads := runtime.NumCPU()
	if threads > blocks {
		threads = blocks
	}
	var (
		pend     = new(sync.WaitGroup)
		tasks    = make(chan *blockTraceTask, threads)
		results  = make(chan *blockTraceTask, threads)
		localctx = context.Background()
	)
	for th := 0; th < threads; th++ {
		pend.Add(1)
		go func() {
			defer pend.Done()

			// Fetch and execute the next block trace tasks
			for task := range tasks {
				parent, err := api.backend.BlockByHash(ctx, task.block.ParentHash(common.ZONE_CTX))
				if err != nil {
					api.backend.Logger().Warn("Tracing failed", "block", task.block.NumberU64(common.ZONE_CTX), "err", err)
					break
				}
				if parent == nil {
					api.backend.Logger().Warn("Tracing failed", "block", task.block.NumberU64(common.ZONE_CTX), "err", errors.New("parent not found"))
					break
				}
				signer := types.MakeSigner(api.backend.ChainConfig(), task.block.Number(common.ZONE_CTX))
				blockCtx, err := core.NewEVMBlockContext(task.block, parent, api.chainContext(localctx), nil)
				if err != nil {
					break
				}
				// Trace all the transactions contained within
				for i, tx := range task.block.Transactions() {
					if types.IsCoinBaseTx(tx) || tx.Type() != types.QuaiTxType {
						continue
					}
					msg, _ := tx.AsMessage(signer, task.block.BaseFee())
					txctx := &Context{
						BlockHash: task.block.Hash(),
						TxIndex:   i,
						TxHash:    tx.Hash(),
					}
					res, err := api.traceTx(localctx, msg, txctx, blockCtx, task.statedb, config)
					if err != nil {
						task.results[i] = &txTraceResult{Error: err.Error()}
						api.backend.Logger().Warn("Tracing failed", "hash", tx.Hash(), "block", task.block.NumberU64(common.ZONE_CTX), "err", err)
						break
					}
					// Only delete empty objects if EIP158/161 (a.k.a Spurious Dragon) is in effect
					task.statedb.Finalize(true)
					task.results[i] = &txTraceResult{Result: res}
				}
				// Stream the result back to the user or abort on teardown
				select {
				case results <- task:
				case <-notifier.Closed():
					return
				}
			}
		}()
	}
	// Start a goroutine to feed all the blocks into the tracers
	begin := time.Now()

	go func() {
		var (
			logged  time.Time
			number  uint64
			traced  uint64
			failed  error
			parent  common.Hash
			statedb *state.StateDB
		)
		// Ensure everything is properly cleaned up on any exit path
		defer func() {
			close(tasks)
			pend.Wait()

			switch {
			case failed != nil:
				api.backend.Logger().Warn("Chain tracing failed", "start", start.NumberU64(common.ZONE_CTX), "end", end.NumberU64(common.ZONE_CTX), "transactions", traced, "elapsed", time.Since(begin), "err", failed)
			case number < end.NumberU64(common.ZONE_CTX):
				api.backend.Logger().Warn("Chain tracing aborted", "start", start.NumberU64(common.ZONE_CTX), "end", end.NumberU64(common.ZONE_CTX), "abort", number, "transactions", traced, "elapsed", time.Since(begin))
			default:
				api.backend.Logger().Info("Chain tracing finished", "start", start.NumberU64(common.ZONE_CTX), "end", end.NumberU64(common.ZONE_CTX), "transactions", traced, "elapsed", time.Since(begin))
			}
			close(results)
		}()
		// Feed all the blocks both into the tracer, as well as fast process concurrently
		for number = start.NumberU64(common.ZONE_CTX); number < end.NumberU64(common.ZONE_CTX); number++ {
			// Stop tracing if interruption was requested
			select {
			case <-notifier.Closed():
				return
			default:
			}
			// Print progress logs if long enough time elapsed
			if time.Since(logged) > 8*time.Second {
				logged = time.Now()
				api.backend.Logger().Info("Tracing chain segment", "start", start.NumberU64(common.ZONE_CTX), "end", end.NumberU64(common.ZONE_CTX), "current", number, "transactions", traced, "elapsed", time.Since(begin))
			}
			// Retrieve the parent state to trace on top
			block, err := api.blockByNumber(localctx, rpc.BlockNumber(number))
			if err != nil {
				failed = err
				break
			}
			// Prepare the statedb for tracing. Don't use the live database for
			// tracing to avoid persisting state junks into the database.
			statedb, err = api.backend.StateAtBlock(localctx, block, reexec, statedb, false)
			if err != nil {
				failed = err
				break
			}
			if statedb.Database().TrieDB() != nil {
				// Hold the reference for tracer, will be released at the final stage
				statedb.Database().TrieDB().Reference(block.EVMRoot(), common.Hash{})

				// Release the parent state because it's already held by the tracer
				if parent != (common.Hash{}) {
					statedb.Database().TrieDB().Dereference(parent)
				}
			}
			parent = block.EVMRoot()

			next, err := api.blockByNumber(localctx, rpc.BlockNumber(number+1))
			if err != nil {
				failed = err
				break
			}
			// Send the block over to the concurrent tracers (if not in the fast-forward phase)
			txs := next.Transactions()
			select {
			case tasks <- &blockTraceTask{statedb: statedb.Copy(), block: next, rootref: block.EVMRoot(), results: make([]*txTraceResult, len(txs))}:
			case <-notifier.Closed():
				return
			}
			traced += uint64(len(txs))
		}
	}()

	// Keep reading the trace results and stream the to the user
	go func() {
		var (
			done = make(map[uint64]*blockTraceResult)
			next = start.NumberU64(common.ZONE_CTX) + 1
		)
		for res := range results {
			// Queue up next received result
			result := &blockTraceResult{
				Block:  hexutil.Uint64(res.block.NumberU64(common.ZONE_CTX)),
				Hash:   res.block.Hash(),
				Traces: res.results,
			}
			done[uint64(result.Block)] = result

			// Dereference any parent tries held in memory by this task
			if res.statedb.Database().TrieDB() != nil {
				res.statedb.Database().TrieDB().Dereference(res.rootref)
			}
			// Stream completed traces to the user, aborting on the first error
			for result, ok := done[next]; ok; result, ok = done[next] {
				if len(result.Traces) > 0 || next == end.NumberU64(common.ZONE_CTX) {
					notifier.Notify(sub.ID, result)
				}
				delete(done, next)
				next++
			}
		}
	}()
	return sub, nil
}

// TraceBlockByNumber returns the structured logs created during the execution of
// EVM and returns them as a JSON object.
func (api *API) TraceBlockByNumber(ctx context.Context, number rpc.BlockNumber, config *TraceConfig) ([]*txTraceResult, error) {
	block, err := api.blockByNumber(ctx, number)
	if err != nil {
		return nil, err
	}
	return api.traceBlock(ctx, block, config)
}

// TraceBlockByHash returns the structured logs created during the execution of
// EVM and returns them as a JSON object.
func (api *API) TraceBlockByHash(ctx context.Context, hash common.Hash, config *TraceConfig) ([]*txTraceResult, error) {
	block, err := api.blockByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	return api.traceBlock(ctx, block, config)
}

// TraceBlock returns the structured logs created during the execution of EVM
// and returns them as a JSON object.
func (api *API) TraceBlock(ctx context.Context, blob []byte, config *TraceConfig) ([]*txTraceResult, error) {
	block := new(types.WorkObject)
	if err := rlp.Decode(bytes.NewReader(blob), block); err != nil {
		return nil, fmt.Errorf("could not decode block: %v", err)
	}
	return api.traceBlock(ctx, block, config)
}

// TraceBlockFromFile returns the structured logs created during the execution of
// EVM and returns them as a JSON object.
func (api *API) TraceBlockFromFile(ctx context.Context, file string, config *TraceConfig) ([]*txTraceResult, error) {
	blob, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("could not read file: %v", err)
	}
	return api.TraceBlock(ctx, blob, config)
}

// StandardTraceBlockToFile dumps the structured logs created during the
// execution of EVM to the local file system and returns a list of files
// to the caller.
func (api *API) StandardTraceBlockToFile(ctx context.Context, hash common.Hash, config *StdTraceConfig) ([]string, error) {
	block, err := api.blockByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	return api.standardTraceBlockToFile(ctx, block, config)
}

// traceBlock configures a new tracer according to the provided configuration, and
// executes all the transactions contained within. The return value will be one item
// per transaction, dependent on the requestd tracer.
func (api *API) traceBlock(ctx context.Context, block *types.WorkObject, config *TraceConfig) ([]*txTraceResult, error) {
	if block.NumberU64(common.ZONE_CTX) == 0 {
		return nil, errors.New("genesis is not traceable")
	}
	parent, err := api.blockByNumberAndHash(ctx, rpc.BlockNumber(block.NumberU64(common.ZONE_CTX)-1), block.ParentHash(common.ZONE_CTX))
	if err != nil {
		return nil, err
	}
	reexec := defaultTraceReexec
	if config != nil && config.Reexec != nil {
		reexec = *config.Reexec
	}
	statedb, err := api.backend.StateAtBlock(ctx, parent, reexec, nil, true)
	if err != nil {
		return nil, err
	}
	// Execute all the transaction contained within the block concurrently
	var (
		signer  = types.MakeSigner(api.backend.ChainConfig(), block.Number(common.ZONE_CTX))
		txs     = block.Transactions()
		results = make([]*txTraceResult, len(txs))

		pend = new(sync.WaitGroup)
		jobs = make(chan *txTraceTask, len(txs))
	)
	threads := runtime.NumCPU()
	if threads > len(txs) {
		threads = len(txs)
	}
	blockCtx, err := core.NewEVMBlockContext(block, parent, api.chainContext(ctx), nil)
	if err != nil {
		return nil, err
	}
	blockHash := block.Hash()
	for th := 0; th < threads; th++ {
		pend.Add(1)
		go func() {
			defer pend.Done()
			// Fetch and execute the next transaction trace tasks
			for task := range jobs {
				msg, _ := txs[task.index].AsMessage(signer, block.BaseFee())
				txctx := &Context{
					BlockHash: blockHash,
					TxIndex:   task.index,
					TxHash:    txs[task.index].Hash(),
				}
				res, err := api.traceTx(ctx, msg, txctx, blockCtx, task.statedb, config)
				if err != nil {
					results[task.index] = &txTraceResult{Error: err.Error()}
					continue
				}
				results[task.index] = &txTraceResult{Result: res}
			}
		}()
	}
	// Feed the transactions into the tracers and return
	var failed error
	for i, tx := range txs {
		// Send the trace task over for execution
		jobs <- &txTraceTask{statedb: statedb.Copy(), index: i}

		// Generate the next state snapshot fast without tracing
		msg, _ := tx.AsMessage(signer, block.BaseFee())
		statedb.Prepare(tx.Hash(), i)
		vmenv := vm.NewEVM(blockCtx, core.NewEVMTxContext(msg), statedb, api.backend.ChainConfig(), vm.Config{}, nil)
		if _, err := core.ApplyMessage(vmenv, msg, new(types.GasPool).AddGas(msg.Gas())); err != nil {
			failed = err
			break
		}
		// Finalize the state so any modifications are written to the trie
		// Only delete empty objects if EIP158/161 (a.k.a Spurious Dragon) is in effect
		statedb.Finalize(true)
	}
	close(jobs)
	pend.Wait()

	// If execution failed in between, abort
	if failed != nil {
		return nil, failed
	}
	return results, nil
}

// standardTraceBlockToFile configures a new tracer which uses standard JSON output,
// and traces either a full block or an individual transaction. The return value will
// be one filename per transaction traced.
func (api *API) standardTraceBlockToFile(ctx context.Context, block *types.WorkObject, config *StdTraceConfig) ([]string, error) {
	// If we're tracing a single transaction, make sure it's present
	if config != nil && config.TxHash != (common.Hash{}) {
		if !containsTx(block, config.TxHash) {
			return nil, fmt.Errorf("transaction %#x not found in block", config.TxHash)
		}
	}
	if block.NumberU64(common.ZONE_CTX) == 0 {
		return nil, errors.New("genesis is not traceable")
	}
	parent, err := api.blockByNumberAndHash(ctx, rpc.BlockNumber(block.NumberU64(common.ZONE_CTX)-1), block.ParentHash(common.ZONE_CTX))
	if err != nil {
		return nil, err
	}
	reexec := defaultTraceReexec
	if config != nil && config.Reexec != nil {
		reexec = *config.Reexec
	}
	statedb, err := api.backend.StateAtBlock(ctx, parent, reexec, nil, true)
	if err != nil {
		return nil, err
	}
	// Retrieve the tracing configurations, or use default values
	var (
		logConfig vm.LogConfig
		txHash    common.Hash
	)
	if config != nil {
		logConfig = config.LogConfig
		txHash = config.TxHash
	}
	logConfig.Debug = true

	// Execute transaction, either tracing all or just the requested one
	var (
		dumps       []string
		signer      = types.MakeSigner(api.backend.ChainConfig(), block.Number(common.ZONE_CTX))
		chainConfig = api.backend.ChainConfig()
		vmctx, _    = core.NewEVMBlockContext(block, parent, api.chainContext(ctx), nil)
		canon       = true
	)
	// Check if there are any overrides: the caller may wish to enable a future
	// fork when executing this block. Note, such overrides are only applicable to the
	// actual specified block, not any preceding blocks that we have to go through
	// in order to obtain the state.
	// Therefore, it's perfectly valid to specify `"futureForkBlock": 0`, to enable `futureFork`

	if config != nil && config.Overrides != nil {
		// Copy the config, to not screw up the main config
		// Note: the Clique-part is _not_ deep copied
		chainConfigCopy := new(params.ChainConfig)
		*chainConfigCopy = *chainConfig
		chainConfig = chainConfigCopy
	}
	for i, tx := range block.Transactions() {
		if types.IsCoinBaseTx(tx) || tx.Type() != types.QuaiTxType {
			continue
		}
		// Prepare the trasaction for un-traced execution
		var (
			msg, _    = tx.AsMessage(signer, block.BaseFee())
			txContext = core.NewEVMTxContext(msg)
			vmConf    vm.Config
			dump      *os.File
			writer    *bufio.Writer
			err       error
		)
		// If the transaction needs tracing, swap out the configs
		if tx.Hash() == txHash || txHash == (common.Hash{}) {
			// Generate a unique temporary file to dump it into
			prefix := fmt.Sprintf("block_%#x-%d-%#x-", block.Hash().Bytes()[:4], i, tx.Hash().Bytes()[:4])
			if !canon {
				prefix = fmt.Sprintf("%valt-", prefix)
			}
			dump, err = ioutil.TempFile(os.TempDir(), prefix)
			if err != nil {
				return nil, err
			}
			dumps = append(dumps, dump.Name())

			// Swap out the noop logger to the standard tracer
			writer = bufio.NewWriter(dump)
			vmConf = vm.Config{
				Debug:                   true,
				Tracer:                  vm.NewJSONLogger(&logConfig, writer),
				EnablePreimageRecording: true,
			}
		}
		// Execute the transaction and flush any traces to disk
		vmenv := vm.NewEVM(vmctx, txContext, statedb, chainConfig, vmConf, nil)
		statedb.Prepare(tx.Hash(), i)
		_, err = core.ApplyMessage(vmenv, msg, new(types.GasPool).AddGas(msg.Gas()))
		if writer != nil {
			writer.Flush()
		}
		if dump != nil {
			dump.Close()
			api.backend.Logger().Info("Wrote standard trace", "file", dump.Name())
		}
		if err != nil {
			return dumps, err
		}
		// Finalize the state so any modifications are written to the trie
		// Only delete empty objects if EIP158/161 (a.k.a Spurious Dragon) is in effect
		statedb.Finalize(true)

		// If we've traced the transaction we were looking for, abort
		if tx.Hash() == txHash {
			break
		}
	}
	return dumps, nil
}

// containsTx reports whether the transaction with a certain hash
// is contained within the specified block.
func containsTx(block *types.WorkObject, hash common.Hash) bool {
	for _, tx := range block.Transactions() {
		if tx.Hash() == hash {
			return true
		}
	}
	return false
}

// TraceTransaction returns the structured logs created during the execution of EVM
// and returns them as a JSON object.
func (api *API) TraceTransaction(ctx context.Context, hash common.Hash, config *TraceConfig) (interface{}, error) {
	_, blockHash, blockNumber, index, err := api.backend.GetTransaction(ctx, hash)
	if err != nil {
		return nil, err
	}
	// It shouldn't happen in practice.
	if blockNumber == 0 {
		return nil, errors.New("genesis is not traceable")
	}
	reexec := defaultTraceReexec
	if config != nil && config.Reexec != nil {
		reexec = *config.Reexec
	}
	block, err := api.blockByNumberAndHash(ctx, rpc.BlockNumber(blockNumber), blockHash)
	if err != nil {
		return nil, err
	}
	msg, vmctx, statedb, err := api.backend.StateAtTransaction(ctx, block, int(index), reexec)
	if err != nil {
		return nil, err
	}
	txctx := &Context{
		BlockHash: blockHash,
		TxIndex:   int(index),
		TxHash:    hash,
	}
	return api.traceTx(ctx, msg, txctx, vmctx, statedb, config)
}

// TraceCall lets you trace a given eth_call. It collects the structured logs
// created during the execution of EVM if the given transaction was added on
// top of the provided block and returns them as a JSON object.
// You can provide -2 as a block number to trace on top of the pending block.
func (api *API) TraceCall(ctx context.Context, args quaiapi.TransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, config *TraceCallConfig) (interface{}, error) {
	// Try to retrieve the specified block
	var (
		err   error
		block *types.WorkObject
	)
	if hash, ok := blockNrOrHash.Hash(); ok {
		block, err = api.blockByHash(ctx, hash)
	} else if number, ok := blockNrOrHash.Number(); ok {
		block, err = api.blockByNumber(ctx, number)
	} else {
		return nil, errors.New("invalid arguments; neither block nor hash specified")
	}
	if err != nil {
		return nil, err
	}
	// try to recompute the state
	reexec := defaultTraceReexec
	if config != nil && config.Reexec != nil {
		reexec = *config.Reexec
	}
	statedb, err := api.backend.StateAtBlock(ctx, block, reexec, nil, true)
	if err != nil {
		return nil, err
	}
	// Apply the customized state rules if required.
	if config != nil {
		if err := config.StateOverrides.Apply(statedb, api.backend.NodeLocation()); err != nil {
			return nil, err
		}
	}
	// Execute the trace
	msg, err := args.ToMessage(api.backend.RPCGasCap(), block.BaseFee(), api.backend.NodeLocation())
	if err != nil {
		return nil, err
	}
	parent, err := api.backend.BlockByHash(ctx, block.ParentHash(common.ZONE_CTX))
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, errors.New("could not find the parent block")
	}
	vmctx, err := core.NewEVMBlockContext(block, parent, api.chainContext(ctx), nil)
	if err != nil {
		return nil, err
	}

	var traceConfig *TraceConfig
	if config != nil {
		traceConfig = &TraceConfig{
			LogConfig: config.LogConfig,
			Tracer:    config.Tracer,
			Timeout:   config.Timeout,
			Reexec:    config.Reexec,
		}
	}
	return api.traceTx(ctx, msg, new(Context), vmctx, statedb, traceConfig)
}

// traceTx configures a new tracer according to the provided configuration, and
// executes the given message in the provided environment. The return value will
// be tracer dependent.
func (api *API) traceTx(ctx context.Context, message core.Message, txctx *Context, vmctx vm.BlockContext, statedb *state.StateDB, config *TraceConfig) (interface{}, error) {
	// Assemble the structured logger or the JavaScript tracer
	var (
		tracer    vm.Tracer
		err       error
		txContext = core.NewEVMTxContext(message)
	)
	switch {
	case config != nil && config.Tracer != nil:
		// Define a meaningful timeout of a single transaction trace
		timeout := defaultTraceTimeout
		if config.Timeout != nil {
			if timeout, err = time.ParseDuration(*config.Timeout); err != nil {
				return nil, err
			}
		}
		// Constuct the JavaScript tracer to execute with
		if tracer, err = New(*config.Tracer, txctx, api.backend.Logger(), api.backend.NodeLocation()); err != nil {
			return nil, err
		}
		// Handle timeouts and RPC cancellations
		deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
		go func() {
			<-deadlineCtx.Done()
			if deadlineCtx.Err() == context.DeadlineExceeded {
				tracer.(*Tracer).Stop(errors.New("execution timeout"))
			}
		}()
		defer cancel()

	case config == nil:
		tracer = vm.NewStructLogger(nil)

	default:
		tracer = vm.NewStructLogger(config.LogConfig)
	}
	// Run the transaction with tracing enabled.
	vmenv := vm.NewEVM(vmctx, txContext, statedb, api.backend.ChainConfig(), vm.Config{Debug: true, Tracer: tracer, NoBaseFee: true}, nil)

	// Call Prepare to clear out the statedb access list
	statedb.Prepare(txctx.TxHash, txctx.TxIndex)

	result, err := core.ApplyMessage(vmenv, message, new(types.GasPool).AddGas(message.Gas()))
	if err != nil {
		return nil, fmt.Errorf("tracing failed: %w", err)
	}

	// Depending on the tracer type, format and return the output.
	switch tracer := tracer.(type) {
	case *vm.StructLogger:
		// If the result contains a revert reason, return it.
		returnVal := fmt.Sprintf("%x", result.Return())
		if len(result.Revert()) > 0 {
			returnVal = fmt.Sprintf("%x", result.Revert())
		}
		return &quaiapi.ExecutionResult{
			Gas:         result.UsedGas,
			Failed:      result.Failed(),
			ReturnValue: returnVal,
			StructLogs:  quaiapi.FormatLogs(tracer.StructLogs()),
		}, nil

	case *Tracer:
		return tracer.GetResult()

	default:
		panic(fmt.Sprintf("bad tracer type %T", tracer))
	}
}

// APIs return the collection of RPC services the tracer package offers.
func APIs(backend Backend) []rpc.API {
	// Append all the local APIs and return
	return []rpc.API{
		{
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewAPI(backend),
			Public:    false,
		},
	}
}
