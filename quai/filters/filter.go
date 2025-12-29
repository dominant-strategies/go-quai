// Copyright 2014 The go-ethereum Authors
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

package filters

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/bloombits"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
)

type Backend interface {
	ChainDb() ethdb.Database
	HeaderByNumber(ctx context.Context, blockNr rpc.BlockNumber) (*types.WorkObject, error)
	HeaderByHash(ctx context.Context, blockHash common.Hash) (*types.WorkObject, error)
	GetReceipts(ctx context.Context, blockHash common.Hash) (types.Receipts, error)
	GetLogs(ctx context.Context, blockHash common.Hash) ([][]*types.Log, error)
	GetBloom(blockHash common.Hash) (*types.Bloom, error)
	GetBlock(hash common.Hash, number uint64) (*types.WorkObject, error)
	SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
	SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription
	SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription
	SubscribePendingLogsEvent(ch chan<- []*types.Log) event.Subscription
	SubscribePendingHeaderEvent(ch chan<- *types.WorkObject) event.Subscription
	SubscribeNewWorkshareEvent(ch chan<- core.NewWorkshareEvent) event.Subscription
	SubscribeUnlocksEvent(ch chan<- core.UnlocksEvent) event.Subscription
	ProcessingState() bool
	NodeLocation() common.Location
	NodeCtx() int
	Engine(header *types.WorkObjectHeader) consensus.Engine
	GetBestAuxTemplate(powId types.PowID) *types.AuxTemplate
	AddPendingAuxPow(powId types.PowID, sealHash common.Hash, auxpow *types.AuxPow)
	GetPendingHeader(powID types.PowID, coinbase common.Address) (*types.WorkObject, error)

	RpcVersion() string

	BloomStatus() (uint64, uint64)
	ServiceFilter(ctx context.Context, session *bloombits.MatcherSession)
	Logger() *log.Logger
}

// Filter can be used to retrieve and filter logs.
type Filter struct {
	backend Backend

	db        ethdb.Database
	addresses []common.Address
	topics    [][]common.Hash

	block      common.Hash // Block hash if filtering a single block
	begin, end int64       // Range interval if filtering multiple blocks

	matcher *bloombits.Matcher
}

// NewRangeFilter creates a new filter which uses a bloom filter on blocks to
// figure out whether a particular block is interesting or not.
func NewRangeFilter(backend Backend, begin, end int64, addresses []common.Address, topics [][]common.Hash, logger *log.Logger) *Filter {
	// Flatten the address and topic filter clauses into a single bloombits filter
	// system. Since the bloombits are not positional, nil topics are permitted,
	// which get flattened into a nil byte slice.
	var filters [][][]byte
	if len(addresses) > 0 {
		filter := make([][]byte, len(addresses))
		for i, address := range addresses {
			filter[i] = address.Bytes()
		}
		filters = append(filters, filter)
	}
	for _, topicList := range topics {
		filter := make([][]byte, len(topicList))
		for i, topic := range topicList {
			filter[i] = topic.Bytes()
		}
		filters = append(filters, filter)
	}

	// Create a generic filter and convert it into a range filter
	filter := newFilter(backend, addresses, topics)

	filter.begin = begin
	filter.end = end

	return filter
}

// NewBlockFilter creates a new filter which directly inspects the contents of
// a block to figure out whether it is interesting or not.
func NewBlockFilter(backend Backend, block common.Hash, addresses []common.Address, topics [][]common.Hash) *Filter {
	// Create a generic filter and convert it into a block filter
	filter := newFilter(backend, addresses, topics)
	filter.block = block
	return filter
}

// newFilter creates a generic filter that can either filter based on a block hash,
// or based on range queries. The search criteria needs to be explicitly set.
func newFilter(backend Backend, addresses []common.Address, topics [][]common.Hash) *Filter {
	return &Filter{
		backend:   backend,
		addresses: addresses,
		topics:    topics,
		db:        backend.ChainDb(),
	}
}

// Logs searches the blockchain for matching log entries by manually checking
// each block within the specified range using 8 goroutines for parallel processing.
func (f *Filter) Logs(ctx context.Context) ([]*types.Log, error) {
	// If we're doing singleton block filtering, execute and return
	if f.block != (common.Hash{}) {
		workObject, err := f.backend.HeaderByHash(ctx, f.block)
		if err != nil {
			return nil, err
		}
		if workObject == nil {
			return nil, errors.New("unknown block")
		}
		return f.blockLogs(ctx, workObject)
	}

	// Determine the limits of the filter range
	header, _ := f.backend.HeaderByNumber(ctx, rpc.LatestBlockNumber)
	if header == nil {
		return nil, nil
	}
	head := header.NumberU64(common.ZONE_CTX)

	if f.begin == -1 {
		f.begin = int64(head)
	}
	end := uint64(f.end)
	if f.end == -1 {
		end = head
	}
	if end > uint64(f.begin) && end-uint64(f.begin) > MaxFilterRange {
		return nil, fmt.Errorf("filter range exceeds maximum limit of %d blocks", MaxFilterRange)
	}

	// Prepare to collect logs from goroutines
	var (
		logs     []*types.Log
		logsLock sync.Mutex
		wg       sync.WaitGroup
		errChan  = make(chan error, 8)
	)

	totalBlocks := int64(end) - f.begin + 1
	if totalBlocks <= 0 {
		return nil, nil
	}

	// Adjust the number of goroutines if total blocks are fewer than 8
	numGoroutines := 8
	if totalBlocks < int64(numGoroutines) {
		numGoroutines = int(totalBlocks)
	}

	blocksPerGoroutine := totalBlocks / int64(numGoroutines)
	remainderBlocks := totalBlocks % int64(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			// Calculate the start and end blocks for this goroutine
			startBlock := f.begin + int64(i)*blocksPerGoroutine
			endBlock := startBlock + blocksPerGoroutine - 1
			if i == numGoroutines-1 {
				// The last goroutine handles any remaining blocks
				endBlock += remainderBlocks
			}

			// Collect logs from startBlock to endBlock
			var localLogs []*types.Log
			for blockNum := startBlock; blockNum <= endBlock; blockNum++ {
				// Check for context cancellation
				select {
				case <-ctx.Done():
					errChan <- ctx.Err()
					return
				default:
				}

				workObject, err := f.backend.HeaderByNumber(ctx, rpc.BlockNumber(blockNum))
				if err != nil {
					errChan <- err
					return
				}
				if workObject == nil {
					continue
				}

				found, err := f.blockLogs(ctx, workObject)
				if err != nil {
					errChan <- err
					return
				}
				localLogs = append(localLogs, found...)
			}

			// Append localLogs to the main logs slice safely
			logsLock.Lock()
			logs = append(logs, localLogs...)
			logsLock.Unlock()
		}(i)
	}

	// Wait for all goroutines to finish
	wg.Wait()
	close(errChan)

	// Check for errors from goroutines
	for err := range errChan {
		if err != nil {
			return logs, err
		}
	}

	return logs, nil
}

// indexedLogs returns the logs matching the filter criteria based on the bloom
// bits indexed available locally or via the network.
func (f *Filter) indexedLogs(ctx context.Context, end uint64) ([]*types.Log, error) {
	// Create a matcher session and request servicing from the backend
	matches := make(chan uint64, 64)

	session, err := f.matcher.Start(ctx, uint64(f.begin), end, matches)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	f.backend.ServiceFilter(ctx, session)

	// Iterate over the matches until exhausted or context closed
	var logs []*types.Log

	for {
		select {
		case number, ok := <-matches:
			// Abort if all matches have been fulfilled
			if !ok {
				err := session.Error()
				if err == nil {
					f.begin = int64(end) + 1
				}
				return logs, err
			}
			f.begin = int64(number) + 1

			// Retrieve the suggested block and pull any truly matching logs
			workObject, err := f.backend.HeaderByNumber(ctx, rpc.BlockNumber(number))
			if workObject == nil || err != nil {
				return logs, err
			}
			found, err := f.checkMatches(ctx, workObject)
			if err != nil {
				return logs, err
			}
			logs = append(logs, found...)

		case <-ctx.Done():
			return logs, ctx.Err()
		}
	}
}

// unindexedLogs returns the logs matching the filter criteria based on raw block
// iteration and bloom matching.
func (f *Filter) unindexedLogs(ctx context.Context, end uint64) ([]*types.Log, error) {
	var logs []*types.Log

	for ; f.begin <= int64(end); f.begin++ {
		workObject, err := f.backend.HeaderByNumber(ctx, rpc.BlockNumber(f.begin))
		if workObject == nil || err != nil {
			return logs, err
		}
		found, err := f.blockLogs(ctx, workObject)
		if err != nil {
			return logs, err
		}
		logs = append(logs, found...)
	}
	return logs, nil
}

// blockLogs returns the logs matching the filter criteria within a single block.
func (f *Filter) blockLogs(ctx context.Context, workObject *types.WorkObject) (logs []*types.Log, err error) {

	found, err := f.checkMatches(ctx, workObject)
	if err != nil {
		return logs, err
	}
	logs = append(logs, found...)

	return logs, nil
}

// checkMatches checks if the receipts belonging to the given header contain any log events that
// match the filter criteria. This function is called when the bloom filter signals a potential match.
func (f *Filter) checkMatches(ctx context.Context, workObject *types.WorkObject) (logs []*types.Log, err error) {
	// Get the logs of the block
	logsList, err := f.backend.GetLogs(ctx, workObject.Hash())
	if err != nil {
		return nil, err
	}
	var unfiltered []*types.Log
	for _, logs := range logsList {
		unfiltered = append(unfiltered, logs...)
	}
	logs = filterLogs(unfiltered, nil, nil, f.addresses, f.topics)
	if len(logs) > 0 {
		// We have matching logs, check if we need to resolve full logs via the light client
		if logs[0].TxHash == (common.Hash{}) {
			receipts, err := f.backend.GetReceipts(ctx, workObject.Hash())
			if err != nil {
				return nil, err
			}
			unfiltered = unfiltered[:0]
			for _, receipt := range receipts {
				unfiltered = append(unfiltered, receipt.Logs...)
			}
			logs = filterLogs(unfiltered, nil, nil, f.addresses, f.topics)
		}
		return logs, nil
	}
	return nil, nil
}

func includes(addresses []common.Address, a common.Address) bool {
	for _, addr := range addresses {
		if addr.Equal(a) {
			return true
		}
	}

	return false
}

// filterLogs creates a slice of logs matching the given criteria.
func filterLogs(logs []*types.Log, fromBlock, toBlock *big.Int, addresses []common.Address, topics [][]common.Hash) []*types.Log {
	var ret []*types.Log
Logs:
	for _, log := range logs {
		if fromBlock != nil && fromBlock.Int64() >= 0 && fromBlock.Uint64() > log.BlockNumber {
			continue
		}
		if toBlock != nil && toBlock.Int64() >= 0 && toBlock.Uint64() < log.BlockNumber {
			continue
		}

		if len(addresses) > 0 && !includes(addresses, log.Address) {
			continue
		}
		// If the to filtered topics is greater than the amount of topics in logs, skip.
		if len(topics) > len(log.Topics) {
			continue Logs
		}
		for i, sub := range topics {
			match := len(sub) == 0 // empty rule set == wildcard
			for _, topic := range sub {
				if log.Topics[i] == topic {
					match = true
					break
				}
			}
			if !match {
				continue Logs
			}
		}
		ret = append(ret, log)
	}
	return ret
}

func bloomFilter(bloom types.Bloom, addresses []common.Address, topics [][]common.Hash) bool {
	if len(addresses) > 0 {
		var included bool
		for _, addr := range addresses {
			if types.BloomLookup(bloom, addr) {
				included = true
				break
			}
		}
		if !included {
			return false
		}
	}

	for _, sub := range topics {
		included := len(sub) == 0 // empty rule set == wildcard
		for _, topic := range sub {
			if types.BloomLookup(bloom, topic) {
				included = true
				break
			}
		}
		if !included {
			return false
		}
	}
	return true
}
