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

package filters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	quai "github.com/dominant-strategies/go-quai"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
	"google.golang.org/protobuf/proto"
)

const (
	c_pendingHeaderChSize = 20
	MaxFilterRange        = 10000
)

// filter is a helper struct that holds meta information over the filter type
// and associated subscription in the event system.
type filter struct {
	typ      Type
	deadline *time.Timer // filter is inactiv when deadline triggers
	hashes   []common.Hash
	crit     FilterCriteria
	logs     []*types.Log
	s        *Subscription // associated subscription in event system
}

// PublicFilterAPI offers support to create and manage filters. This will allow external clients to retrieve various
// information related to the Quai protocol such as blocks, transactions and logs.
type PublicFilterAPI struct {
	backend             Backend
	mux                 *event.TypeMux
	quit                chan struct{}
	chainDb             ethdb.Database
	events              *EventSystem
	filtersMu           sync.Mutex
	filters             map[rpc.ID]*filter
	timeout             time.Duration
	subscriptionLimit   int
	activeSubscriptions int
}

// NewPublicFilterAPI returns a new PublicFilterAPI instance.
func NewPublicFilterAPI(backend Backend, timeout time.Duration, subscriptionLimit int) *PublicFilterAPI {
	api := &PublicFilterAPI{
		backend:             backend,
		chainDb:             backend.ChainDb(),
		events:              NewEventSystem(backend),
		filters:             make(map[rpc.ID]*filter),
		timeout:             timeout,
		subscriptionLimit:   subscriptionLimit,
		activeSubscriptions: 0,
	}
	go api.timeoutLoop(timeout)

	return api
}

// timeoutLoop runs at the interval set by 'timeout' and deletes filters
// that have not been recently used. It is started when the API is created.
func (api *PublicFilterAPI) timeoutLoop(timeout time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			api.backend.Logger().WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	var toUninstall []*Subscription
	ticker := time.NewTicker(timeout)
	defer ticker.Stop()
	for {
		<-ticker.C
		api.filtersMu.Lock()
		for id, f := range api.filters {
			select {
			case <-f.deadline.C:
				toUninstall = append(toUninstall, f.s)
				delete(api.filters, id)
			default:
				continue
			}
		}
		api.filtersMu.Unlock()

		// Unsubscribes are processed outside the lock to avoid the following scenario:
		// event loop attempts broadcasting events to still active filters while
		// Unsubscribe is waiting for it to process the uninstall request.
		for _, s := range toUninstall {
			s.Unsubscribe()
		}
		toUninstall = nil
	}
}

// NewPendingTransactionFilter creates a filter that fetches pending transaction hashes
// as transactions enter the pending state.
//
// It is part of the filter package because this filter can be used through the
// `eth_getFilterChanges` polling method that is also used for log filters.
//
// https://eth.wiki/json-rpc/API#eth_newpendingtransactionfilter
func (api *PublicFilterAPI) NewPendingTransactionFilter() rpc.ID {
	var (
		pendingTxs   = make(chan []common.Hash)
		pendingTxSub = api.events.SubscribePendingTxs(pendingTxs)
	)

	api.filtersMu.Lock()
	api.filters[pendingTxSub.ID] = &filter{typ: PendingTransactionsSubscription, deadline: time.NewTimer(api.timeout), hashes: make([]common.Hash, 0), s: pendingTxSub}
	api.filtersMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		for {
			select {
			case ph := <-pendingTxs:
				api.filtersMu.Lock()
				if f, found := api.filters[pendingTxSub.ID]; found {
					f.hashes = append(f.hashes, ph...)
				}
				api.filtersMu.Unlock()
			case <-pendingTxSub.Err():
				api.filtersMu.Lock()
				delete(api.filters, pendingTxSub.ID)
				api.filtersMu.Unlock()
				return
			}
		}
	}()

	return pendingTxSub.ID
}

// NewPendingTransactions creates a subscription that is triggered each time a transaction
// enters the transaction pool and was signed from one of the transactions this nodes manages.
func (api *PublicFilterAPI) NewPendingTransactions(ctx context.Context) (*rpc.Subscription, error) {
	if api.activeSubscriptions >= api.subscriptionLimit {
		return &rpc.Subscription{}, errors.New("too many subscribers")
	}

	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
			api.activeSubscriptions -= 1
		}()
		api.activeSubscriptions += 1
		txHashes := make(chan []common.Hash, 128)
		pendingTxSub := api.events.SubscribePendingTxs(txHashes)
		defer pendingTxSub.Unsubscribe()

		for {
			select {
			case hashes := <-txHashes:
				// To keep the original behaviour, send a single tx hash in one notification.
				// TODO(rjl493456442) Send a batch of tx hashes in one notification
				for _, h := range hashes {
					notifier.Notify(rpcSub.ID, h)
				}
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}

// NewBlockFilter creates a filter that fetches blocks that are imported into the chain.
// It is part of the filter package since polling goes with eth_getFilterChanges.
//
// https://eth.wiki/json-rpc/API#eth_newblockfilter
func (api *PublicFilterAPI) NewBlockFilter() rpc.ID {
	var (
		headers   = make(chan *types.WorkObject)
		headerSub = api.events.SubscribeNewHeads(headers)
	)

	api.filtersMu.Lock()
	api.filters[headerSub.ID] = &filter{typ: BlocksSubscription, deadline: time.NewTimer(api.timeout), hashes: make([]common.Hash, 0), s: headerSub}
	api.filtersMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		for {
			select {
			case h := <-headers:
				api.filtersMu.Lock()
				if f, found := api.filters[headerSub.ID]; found {
					f.hashes = append(f.hashes, h.Hash())
				}
				api.filtersMu.Unlock()
			case <-headerSub.Err():
				api.filtersMu.Lock()
				delete(api.filters, headerSub.ID)
				api.filtersMu.Unlock()
				return
			}
		}
	}()

	return headerSub.ID
}

// NewWorkshares sends a notification each time a new workshare is received via P2P.
func (api *PublicFilterAPI) NewWorkshares(ctx context.Context) (*rpc.Subscription, error) {
	if api.activeSubscriptions >= api.subscriptionLimit {
		return &rpc.Subscription{}, errors.New("too many subscribers")
	}

	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
			api.activeSubscriptions -= 1
		}()
		api.activeSubscriptions += 1
		workshares := make(chan *types.WorkObject, 100) // Buffer to prevent blocking
		worksharesSub := api.events.SubscribeNewWorkshares(workshares)
		defer worksharesSub.Unsubscribe()
		api.backend.Logger().Info("NewWorkshares subscription created")
		for {
			select {
			case w := <-workshares:
				// Check if this is actually a block (meets full difficulty target)
				// A block meets the full difficulty, while a workshare only meets the lower threshold
				header := w.WorkObjectHeader()

				if header.AuxPow() != nil {
					switch header.AuxPow().PowID() {
					case types.SHA_BCH, types.SHA_BTC:
						target := new(big.Int).Div(common.Big2e256, header.ShaDiffAndCount().Difficulty())
						powHash := header.AuxPow().Header().PowHash()
						// If it meets the full difficulty target, it's a block, not a workshare
						if new(big.Int).SetBytes(powHash.Bytes()).Cmp(target) >= 0 {
							api.backend.Logger().Error("Skipping block in workshare subscription", "hash", w.Hash().Hex())
							continue
						}
					case types.Scrypt:
						target := new(big.Int).Div(common.Big2e256, header.ScryptDiffAndCount().Difficulty())
						powHash := header.AuxPow().Header().PowHash()
						// If it meets the full difficulty target, it's a block, not a workshare
						if new(big.Int).SetBytes(powHash.Bytes()).Cmp(target) >= 0 {
							api.backend.Logger().Error("Skipping block in workshare subscription", "hash", w.Hash().Hex())
							continue
						}
					default:
						target := new(big.Int).Div(common.Big2e256, header.Difficulty())

						// Get the PoW hash from the engine
						powHash, err := api.backend.Engine(header).ComputePowHash(header)
						if err != nil {
							api.backend.Logger().Error("Failed to compute PoW hash for workshare", "hash", w.Hash().Hex(), "err", err)
							continue
						}

						// If it meets the full difficulty target, it's a block, not a workshare
						if new(big.Int).SetBytes(powHash.Bytes()).Cmp(target) <= 0 {
							api.backend.Logger().Debug("Skipping block in workshare subscription", "hash", w.Hash().Hex())
							continue
						}
					}
				}

				notifier.Notify(rpcSub.ID, w.RPCMarshalWorkObject(api.backend.RpcVersion()))
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}

// NewWorkshares sends a notification each time a new workshare is received via P2P.
func (api *PublicFilterAPI) NewWorksharesV2(ctx context.Context) (*rpc.Subscription, error) {
	if api.activeSubscriptions >= api.subscriptionLimit {
		return &rpc.Subscription{}, errors.New("too many subscribers")
	}

	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
			api.activeSubscriptions -= 1
		}()
		api.activeSubscriptions += 1
		workshares := make(chan *types.WorkObject, 100) // Buffer to prevent blocking
		worksharesSub := api.events.SubscribeNewWorkshares(workshares)
		defer worksharesSub.Unsubscribe()
		api.backend.Logger().Info("NewWorkshares subscription created")
		for {
			select {
			case w := <-workshares:
				// Check if this is actually a block (meets full difficulty target)
				// A block meets the full difficulty, while a workshare only meets the lower threshold
				header := w.WorkObjectHeader()

				if header.AuxPow() != nil {
					switch header.AuxPow().PowID() {
					case types.SHA_BCH, types.SHA_BTC:
						target := new(big.Int).Div(common.Big2e256, header.ShaDiffAndCount().Difficulty())
						powHash := header.AuxPow().Header().PowHash()
						// If it meets the full difficulty target, it's a block, not a workshare
						if new(big.Int).SetBytes(powHash.Bytes()).Cmp(target) >= 0 {
							api.backend.Logger().Error("Skipping block in workshare subscription", "hash", w.Hash().Hex())
							continue
						}
					case types.Scrypt:
						target := new(big.Int).Div(common.Big2e256, header.ScryptDiffAndCount().Difficulty())
						powHash := header.AuxPow().Header().PowHash()
						// If it meets the full difficulty target, it's a block, not a workshare
						if new(big.Int).SetBytes(powHash.Bytes()).Cmp(target) >= 0 {
							api.backend.Logger().Error("Skipping block in workshare subscription", "hash", w.Hash().Hex())
							continue
						}
					default:
						target := new(big.Int).Div(common.Big2e256, header.Difficulty())

						// Get the PoW hash from the engine
						powHash, err := api.backend.Engine(header).ComputePowHash(header)
						if err != nil {
							api.backend.Logger().Error("Failed to compute PoW hash for workshare", "hash", w.Hash().Hex(), "err", err)
							continue
						}

						// If it meets the full difficulty target, it's a block, not a workshare
						if new(big.Int).SetBytes(powHash.Bytes()).Cmp(target) <= 0 {
							api.backend.Logger().Debug("Skipping block in workshare subscription", "hash", w.Hash().Hex())
							continue
						}
					}
				}

				notifier.Notify(rpcSub.ID, w.RPCMarshalWorkObject("v2"))
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}

// NewHeads send a notification each time a new (header) block is appended to the chain.
func (api *PublicFilterAPI) NewHeadsV2(ctx context.Context) (*rpc.Subscription, error) {
	if api.activeSubscriptions >= api.subscriptionLimit {
		return &rpc.Subscription{}, errors.New("too many subscribers")
	}

	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
			api.activeSubscriptions -= 1
		}()
		api.activeSubscriptions += 1
		headers := make(chan *types.WorkObject, 10) // Buffer to prevent blocking
		headersSub := api.events.SubscribeNewHeads(headers)
		defer headersSub.Unsubscribe()

		for {
			select {
			case h := <-headers:
				// Marshal the header data
				marshalHeader := h.RPCMarshalWorkObject("v2")
				notifier.Notify(rpcSub.ID, marshalHeader)
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}

// NewHeads send a notification each time a new (header) block is appended to the chain.
func (api *PublicFilterAPI) NewHeads(ctx context.Context) (*rpc.Subscription, error) {
	if api.activeSubscriptions >= api.subscriptionLimit {
		return &rpc.Subscription{}, errors.New("too many subscribers")
	}

	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
			api.activeSubscriptions -= 1
		}()
		api.activeSubscriptions += 1
		headers := make(chan *types.WorkObject, 10) // Buffer to prevent blocking
		headersSub := api.events.SubscribeNewHeads(headers)
		defer headersSub.Unsubscribe()

		for {
			select {
			case h := <-headers:
				// Marshal the header data
				marshalHeader := h.RPCMarshalWorkObject(api.backend.RpcVersion())
				notifier.Notify(rpcSub.ID, marshalHeader)
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}

// Accesses send a notification each time the specified address is accessed
func (api *PublicFilterAPI) Accesses(ctx context.Context, addr common.Address) (*rpc.Subscription, error) {
	if api.activeSubscriptions >= api.subscriptionLimit {
		return &rpc.Subscription{}, errors.New("too many subscribers")
	}

	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()
	internalAddr, err := addr.InternalAddress()
	if err != nil {
		return nil, err
	}

	go func() {

		headers := make(chan *types.WorkObject)
		headersSub := api.events.SubscribeChainHeadEvent(headers)
		unlocks := make(chan core.UnlocksEvent)
		unlocksSub := api.events.SubscribeUnlocks(unlocks)
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
			headersSub.Unsubscribe()
			unlocksSub.Unsubscribe()
			api.activeSubscriptions -= 1
		}()
		api.activeSubscriptions += 1
		for {
			select {
			case h := <-headers:

				// Marshal the header data
				hash := h.Hash()
				nodeLocation := api.backend.NodeLocation()
				for _, tx := range h.Transactions() {
					// Check for external accesses
					switch tx.Type() {
					case types.QuaiTxType:
						if tx.To() != nil && tx.To().Equal(addr) || tx.From(nodeLocation) != nil && tx.From(nodeLocation).Equal(addr) {
							notifier.Notify(rpcSub.ID, hash)
							break
						}
					case types.ExternalTxType:
						if tx.To().Equal(addr) || tx.ETXSender().Equal(addr) {
							notifier.Notify(rpcSub.ID, hash)
							break
						}
					case types.QiTxType:
						// For Qi Tx go through the tx out and notify if the out
						// address matches the subscription address
						for _, out := range tx.TxOut() {
							if common.BytesToAddress(out.Address, api.backend.NodeLocation()).Equal(addr) {
								notifier.Notify(rpcSub.ID, hash)
								break
							}
						}
					}

					if tx.Type() == types.QuaiTxType {
						// Check for EVM accesses
						for _, access := range tx.AccessList() {
							if access.Address.Equal(addr) {
								notifier.Notify(rpcSub.ID, hash)
								break
							}
						}
					}
				}
			case u := <-unlocks:
				for _, unlock := range u.Unlocks {
					if unlock.Addr == internalAddr {
						notifier.Notify(rpcSub.ID, u.Hash)
						break
					}
				}
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}

// Logs creates a subscription that fires for all new log that match the given filter criteria.
func (api *PublicFilterAPI) Logs(ctx context.Context, crit FilterCriteria) (*rpc.Subscription, error) {
	if api.activeSubscriptions >= api.subscriptionLimit {
		return &rpc.Subscription{}, errors.New("too many subscribers")
	}

	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	var (
		rpcSub      = notifier.CreateSubscription()
		matchedLogs = make(chan []*types.Log)
	)

	logsSub, err := api.events.SubscribeLogs(quai.FilterQuery(crit), matchedLogs)
	if err != nil {
		return nil, err
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
			api.activeSubscriptions -= 1
		}()
		defer logsSub.Unsubscribe()
		api.activeSubscriptions += 1
		for {
			select {
			case logs := <-matchedLogs:
				for _, log := range logs {
					notifier.Notify(rpcSub.ID, &log)
				}
			case <-rpcSub.Err(): // client send an unsubscribe request
				return
			case <-notifier.Closed(): // connection dropped
				return
			}
		}
	}()

	return rpcSub, nil
}

// FilterCriteria represents a request to create a new filter.
// Same as quai.FilterQuery but with UnmarshalJSON() method.
type FilterCriteria quai.FilterQuery

// NewFilter creates a new filter and returns the filter id. It can be
// used to retrieve logs when the state changes. This method cannot be
// used to fetch logs that are already stored in the state.
//
// Default criteria for the from and to block are "latest".
// Using "latest" as block number will return logs for mined blocks.
// Using "pending" as block number returns logs for not yet mined (pending) blocks.
// In case logs are removed (chain reorg) previously returned logs are returned
// again but with the removed property set to true.
//
// In case "fromBlock" > "toBlock" an error is returned.
//
// https://eth.wiki/json-rpc/API#eth_newfilter
func (api *PublicFilterAPI) NewFilter(crit FilterCriteria) (rpc.ID, error) {
	logs := make(chan []*types.Log)
	logsSub, err := api.events.SubscribeLogs(quai.FilterQuery(crit), logs)
	if err != nil {
		return "", err
	}

	api.filtersMu.Lock()
	api.filters[logsSub.ID] = &filter{typ: LogsSubscription, crit: crit, deadline: time.NewTimer(api.timeout), logs: make([]*types.Log, 0), s: logsSub}
	api.filtersMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		for {
			select {
			case l := <-logs:
				api.filtersMu.Lock()
				if f, found := api.filters[logsSub.ID]; found {
					f.logs = append(f.logs, l...)
				}
				api.filtersMu.Unlock()
			case <-logsSub.Err():
				api.filtersMu.Lock()
				delete(api.filters, logsSub.ID)
				api.filtersMu.Unlock()
				return
			}
		}
	}()

	return logsSub.ID, nil
}

// GetLogs returns logs matching the given argument that are stored within the state.
//
// https://eth.wiki/json-rpc/API#eth_getlogs
func (api *PublicFilterAPI) GetLogs(ctx context.Context, crit FilterCriteria) ([]*types.Log, error) {
	var filter *Filter

	var addresses []common.Address
	if crit.Addresses != nil {
		addresses = make([]common.Address, len(crit.Addresses))
		for i, addr := range crit.Addresses {
			addresses[i] = common.Bytes20ToAddress(addr, api.backend.NodeLocation())
		}
	}
	if crit.BlockHash != nil {
		// Block filter requested, construct a single-shot filter
		filter = NewBlockFilter(api.backend, *crit.BlockHash, addresses, crit.Topics)
	} else {
		// Convert the RPC block numbers into internal representations
		begin := rpc.LatestBlockNumber.Int64()
		if crit.FromBlock != nil {
			begin = crit.FromBlock.Int64()
		}
		end := rpc.LatestBlockNumber.Int64()
		if crit.ToBlock != nil {
			end = crit.ToBlock.Int64()
		}
		if end != rpc.LatestBlockNumber.Int64() && begin > end {
			return nil, errors.New("fromBlock must be less than or equal to toBlock")
		} else if end != rpc.LatestBlockNumber.Int64() && end-begin > MaxFilterRange {
			return nil, fmt.Errorf("filter range must be less than or equal to %d", MaxFilterRange)
		}
		// Construct the range filter
		filter = NewRangeFilter(api.backend, begin, end, addresses, crit.Topics, api.backend.Logger())
	}
	// Run the filter and return all the logs
	logs, err := filter.Logs(ctx)
	if err != nil {
		return nil, err
	}
	return returnLogs(logs), err
}

// UninstallFilter removes the filter with the given filter id.
//
// https://eth.wiki/json-rpc/API#eth_uninstallfilter
func (api *PublicFilterAPI) UninstallFilter(id rpc.ID) bool {
	api.filtersMu.Lock()
	f, found := api.filters[id]
	if found {
		delete(api.filters, id)
	}
	api.filtersMu.Unlock()
	if found {
		f.s.Unsubscribe()
	}

	return found
}

// GetFilterLogs returns the logs for the filter with the given id.
// If the filter could not be found an empty array of logs is returned.
//
// https://eth.wiki/json-rpc/API#eth_getfilterlogs
func (api *PublicFilterAPI) GetFilterLogs(ctx context.Context, id rpc.ID) ([]*types.Log, error) {
	api.filtersMu.Lock()
	f, found := api.filters[id]
	api.filtersMu.Unlock()

	if !found || f.typ != LogsSubscription {
		return nil, fmt.Errorf("filter not found")
	}

	var addresses []common.Address
	if f.crit.Addresses != nil {
		addresses = make([]common.Address, len(f.crit.Addresses))
		for i, addr := range f.crit.Addresses {
			addresses[i] = common.Bytes20ToAddress(addr, api.backend.NodeLocation())
		}
	}

	var filter *Filter
	if f.crit.BlockHash != nil {
		// Block filter requested, construct a single-shot filter
		filter = NewBlockFilter(api.backend, *f.crit.BlockHash, addresses, f.crit.Topics)
	} else {
		// Convert the RPC block numbers into internal representations
		begin := rpc.LatestBlockNumber.Int64()
		if f.crit.FromBlock != nil {
			begin = f.crit.FromBlock.Int64()
		}
		end := rpc.LatestBlockNumber.Int64()
		if f.crit.ToBlock != nil {
			end = f.crit.ToBlock.Int64()
		}
		// Construct the range filter
		filter = NewRangeFilter(api.backend, begin, end, addresses, f.crit.Topics, api.backend.Logger())
	}
	// Run the filter and return all the logs
	logs, err := filter.Logs(ctx)
	if err != nil {
		return nil, err
	}
	return returnLogs(logs), nil
}

// GetFilterChanges returns the logs for the filter with the given id since
// last time it was called. This can be used for polling.
//
// For pending transaction and block filters the result is []common.Hash.
// (pending)Log filters return []Log.
//
// https://eth.wiki/json-rpc/API#eth_getfilterchanges
func (api *PublicFilterAPI) GetFilterChanges(id rpc.ID) (interface{}, error) {
	api.filtersMu.Lock()
	defer api.filtersMu.Unlock()

	if f, found := api.filters[id]; found {
		if !f.deadline.Stop() {
			// timer expired but filter is not yet removed in timeout loop
			// receive timer value and reset timer
			<-f.deadline.C
		}
		f.deadline.Reset(api.timeout)

		switch f.typ {
		case PendingTransactionsSubscription, BlocksSubscription:
			hashes := f.hashes
			f.hashes = nil
			return returnHashes(hashes), nil
		case LogsSubscription, MinedAndPendingLogsSubscription:
			logs := f.logs
			f.logs = nil
			return returnLogs(logs), nil
		}
	}

	return []interface{}{}, fmt.Errorf("filter not found")
}

// returnHashes is a helper that will return an empty hash array case the given hash array is nil,
// otherwise the given hashes array is returned.
func returnHashes(hashes []common.Hash) []common.Hash {
	if hashes == nil {
		return []common.Hash{}
	}
	return hashes
}

// returnLogs is a helper that will return an empty log array in case the given logs array is nil,
// otherwise the given logs array is returned.
func returnLogs(logs []*types.Log) []*types.Log {
	if logs == nil {
		return []*types.Log{}
	}
	return logs
}

// UnmarshalJSON sets *args fields with given data.
func (args *FilterCriteria) UnmarshalJSON(data []byte) error {
	type input struct {
		BlockHash *common.Hash     `json:"blockHash"`
		FromBlock *rpc.BlockNumber `json:"fromBlock"`
		ToBlock   *rpc.BlockNumber `json:"toBlock"`
		Addresses interface{}      `json:"address"`
		Topics    []interface{}    `json:"topics"`
	}

	var raw input
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.BlockHash != nil {
		if raw.FromBlock != nil || raw.ToBlock != nil {
			// BlockHash is mutually exclusive with FromBlock/ToBlock criteria
			return fmt.Errorf("cannot specify both BlockHash and FromBlock/ToBlock, choose one or the other")
		}
		args.BlockHash = raw.BlockHash
	} else {
		if raw.FromBlock != nil {
			args.FromBlock = big.NewInt(raw.FromBlock.Int64())
		}

		if raw.ToBlock != nil {
			args.ToBlock = big.NewInt(raw.ToBlock.Int64())
		}
	}

	args.Addresses = []common.AddressBytes{}

	if raw.Addresses != nil {
		// raw.Address can contain a single address or an array of addresses
		switch rawAddr := raw.Addresses.(type) {
		case []interface{}:
			for i, addr := range rawAddr {
				if strAddr, ok := addr.(string); ok {
					addr, err := decodeAddress(strAddr)
					if err != nil {
						return fmt.Errorf("invalid address at index %d: %v", i, err)
					}
					args.Addresses = append(args.Addresses, addr)
				} else {
					return fmt.Errorf("non-string address at index %d", i)
				}
			}
		case string:
			addr, err := decodeAddress(rawAddr)
			if err != nil {
				return fmt.Errorf("invalid address: %v", err)
			}
			args.Addresses = []common.AddressBytes{addr}
		default:
			return errors.New("invalid addresses in query")
		}
	}

	// topics is an array consisting of strings and/or arrays of strings.
	// JSON null values are converted to common.Hash{} and ignored by the filter manager.
	if len(raw.Topics) > 0 {
		args.Topics = make([][]common.Hash, len(raw.Topics))
		for i, t := range raw.Topics {
			switch topic := t.(type) {
			case nil:
				// ignore topic when matching logs

			case string:
				// match specific topic
				top, err := decodeTopic(topic)
				if err != nil {
					return err
				}
				args.Topics[i] = []common.Hash{top}

			case []interface{}:
				// or case e.g. [null, "topic0", "topic1"]
				for _, rawTopic := range topic {
					if rawTopic == nil {
						// null component, match all
						args.Topics[i] = nil
						break
					}
					if topic, ok := rawTopic.(string); ok {
						parsed, err := decodeTopic(topic)
						if err != nil {
							return err
						}
						args.Topics[i] = append(args.Topics[i], parsed)
					} else {
						return fmt.Errorf("invalid topic(s)")
					}
				}
			default:
				return fmt.Errorf("invalid topic(s)")
			}
		}
	}

	return nil
}

func decodeAddress(s string) (common.AddressBytes, error) {
	b, err := hexutil.Decode(s)
	if err == nil && len(b) != common.AddressLength {
		err = fmt.Errorf("hex has invalid length %d after decoding; expected %d for address", len(b), common.AddressLength)
	}
	return common.HexToAddressBytes(s), err
}

func decodeTopic(s string) (common.Hash, error) {
	b, err := hexutil.Decode(s)
	if err == nil && len(b) != common.HashLength {
		err = fmt.Errorf("hex has invalid length %d after decoding; expected %d for topic", len(b), common.HashLength)
	}
	return common.BytesToHash(b), err
}

// PendingHeader sends a notification each time a new pending header is created.
func (api *PublicFilterAPI) PendingHeader(ctx context.Context) (*rpc.Subscription, error) {
	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		header := make(chan *types.WorkObject, c_pendingHeaderChSize)
		headerSub := api.backend.SubscribePendingHeaderEvent(header)
		defer headerSub.Unsubscribe()

		for {
			select {
			case b := <-header:
				go func() {
					defer func() {
						if r := recover(); r != nil {
							api.backend.Logger().WithFields(log.Fields{
								"error":      r,
								"stacktrace": string(debug.Stack()),
							}).Error("Go-Quai Panicked")
						}
					}()

					// Marshal the header data
					// Only keep the Header in the body
					pendingHeaderForMining := b.WithBody(b.Header(), nil, nil, nil, nil, nil)
					pendingHeaderForMining.WorkObjectHeader().SetAuxPow(nil)

					// Marshal the response.
					protoWo, err := pendingHeaderForMining.ProtoEncode(types.PEtxObject)
					if err != nil {
						return
					}
					data, err := proto.Marshal(protoWo)
					if err != nil {
						return
					}
					notifier.Notify(rpcSub.ID, data)
				}()
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}

// BlockTemplateUpdatesCriteria specifies parameters for block template subscription
type BlockTemplateUpdatesCriteria struct {
	Algorithm string `json:"algorithm"` // "kawpow", "sha", "scrypt"
}

// templateState tracks template changes for update detection
type templateState struct {
	sealHash      common.Hash
	parentHash    common.Hash
	height        uint64
	quaiHeight    uint64
	signatureTime uint32
}

// parsePowID converts algorithm string to types.PowID
func parsePowID(algorithm string) (types.PowID, error) {
	switch strings.ToLower(algorithm) {
	case "kawpow":
		return types.Kawpow, nil
	case "sha", "sha256", "sha256d", "sha_bch":
		return types.SHA_BCH, nil
	case "scrypt":
		return types.Scrypt, nil
	default:
		return 0, fmt.Errorf("unsupported algorithm: %s", algorithm)
	}
}

// BlockTemplateUpdates sends a notification when the block template changes.
// Triggers: quaiHeight change, prevHash change, sealHash change (kawpow only)
// Heartbeat: sends current template every 5 seconds if no change occurred
func (api *PublicFilterAPI) BlockTemplateUpdates(ctx context.Context, crit BlockTemplateUpdatesCriteria) (*rpc.Subscription, error) {
	if api.activeSubscriptions >= api.subscriptionLimit {
		return &rpc.Subscription{}, errors.New("too many subscribers")
	}

	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	powID, err := parsePowID(crit.Algorithm)
	if err != nil {
		return &rpc.Subscription{}, err
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				api.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
			api.activeSubscriptions -= 1
		}()
		api.activeSubscriptions += 1

		var lastState *templateState
		heartbeatTicker := time.NewTicker(5 * time.Second)
		defer heartbeatTicker.Stop()

		pendingHeaders := make(chan *types.WorkObject, c_pendingHeaderChSize)
		pendingHeaderSub := api.backend.SubscribePendingHeaderEvent(pendingHeaders)
		defer pendingHeaderSub.Unsubscribe()

		// Helper to get template and check for changes
		checkAndSendTemplate := func(forceUpdate bool) {
			pending, err := api.backend.GetPendingHeader(powID, common.Address{}, []byte{})
			if err != nil {
				api.backend.Logger().WithField("err", err).Debug("Failed to get pending header for template subscription")
				return
			}
			if pending == nil {
				return
			}

			auxPow := pending.AuxPow()
			if auxPow == nil || auxPow.Header() == nil {
				return
			}
			signatureTime, err := types.ExtractSignatureTimeFromCoinbase(types.ExtractScriptSigFromCoinbaseTx(auxPow.Transaction()))
			if err != nil {
				api.backend.Logger().WithField("err", err).Debug("Failed to extract signature time from coinbase")
				signatureTime = 0
			}
			pendingCopy := types.CopyWorkObjectHeader(pending.WorkObjectHeader())
			pendingCopy.SetTime(0)

			newState := &templateState{
				sealHash:      pendingCopy.SealHash(),
				parentHash:    auxPow.Header().PrevBlock(),
				height:        pending.NumberU64(common.ZONE_CTX),
				quaiHeight:    pending.WorkObjectHeader().NumberU64(),
				signatureTime: signatureTime,
			}

			var changed bool
			if lastState == nil {
				changed = true // First template
			} else if lastState.parentHash != newState.parentHash {
				changed = true // New block
			} else if lastState.quaiHeight != newState.quaiHeight {
				changed = true // QuaiHeight changed
			} else if lastState.signatureTime != newState.signatureTime {
				changed = true // Signature time changed
			} else if powID == types.Kawpow && lastState.sealHash != newState.sealHash {
				changed = true // Kawpow: sealHash changed (epoch change)
			}

			if changed || forceUpdate {
				template, err := quaiapi.MarshalAuxPowTemplate(pending, powID, "", "", "")
				if err != nil {
					api.backend.Logger().WithField("err", err).Debug("Failed to marshal block template")
					return
				}
				notifier.Notify(rpcSub.ID, template)
				lastState = newState
				heartbeatTicker.Reset(5 * time.Second)
			}
		}

		// Send initial template immediately
		checkAndSendTemplate(true)

		for {
			select {
			case <-pendingHeaders:
				checkAndSendTemplate(false)
			case <-heartbeatTicker.C:
				// 5s heartbeat - send current template
				checkAndSendTemplate(true)
			case <-rpcSub.Err():
				return
			case <-notifier.Closed():
				return
			}
		}
	}()

	return rpcSub, nil
}
