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

package gasprice

import (
	"context"
	"math/big"
	"sort"
	"sync"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
)

const sampleNumber = 3 // Number of transactions sampled in a block

var (
	DefaultMaxPrice    = big.NewInt(500 * params.GWei)
	DefaultIgnorePrice = big.NewInt(2 * params.Wei)
)

type Config struct {
	Blocks           int
	Percentile       int
	MaxHeaderHistory int
	MaxBlockHistory  int
	Default          *big.Int `toml:",omitempty"`
	MaxPrice         *big.Int `toml:",omitempty"`
	IgnorePrice      *big.Int `toml:",omitempty"`
}

// OracleBackend includes all necessary background APIs for oracle.
type OracleBackend interface {
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
	GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error)
	PendingBlockAndReceipts() (*types.WorkObject, types.Receipts)
	ChainConfig() *params.ChainConfig
}

// Oracle recommends gas prices based on the content of recent
// blocks. Suitable for both light and full clients.
type Oracle struct {
	backend     OracleBackend
	lastHead    common.Hash
	lastPrice   *big.Int
	maxPrice    *big.Int
	ignorePrice *big.Int
	cacheLock   sync.RWMutex
	fetchLock   sync.Mutex

	checkBlocks, percentile           int
	maxHeaderHistory, maxBlockHistory int

	logger *log.Logger
}

// NewOracle returns a new gasprice oracle which can recommend suitable
// gasprice for newly created transaction.
func NewOracle(backend OracleBackend, params Config, logger *log.Logger) *Oracle {
	blocks := params.Blocks
	if blocks < 1 {
		blocks = 1
		logger.WithFields(log.Fields{
			"provided": params.Blocks,
			"updated":  blocks,
		}).Warn("Sanitizing invalid gasprice oracle sample blocks")
	}
	percent := params.Percentile
	if percent < 0 {
		percent = 0
		logger.WithFields(log.Fields{
			"provided": params.Percentile,
			"updated":  percent,
		}).Warn("Sanitizing invalid gasprice oracle sample percentile")
	}
	if percent > 100 {
		percent = 100
		logger.WithFields(log.Fields{
			"provided": params.Percentile,
			"updated":  percent,
		}).Warn("Sanitizing invalid gasprice oracle sample percentile")
	}
	maxPrice := params.MaxPrice
	if maxPrice == nil || maxPrice.Int64() <= 0 {
		maxPrice = DefaultMaxPrice
		logger.WithFields(log.Fields{
			"provided": params.MaxPrice,
			"updated":  maxPrice,
		}).Warn("Sanitizing invalid gasprice oracle price cap")
	}
	ignorePrice := params.IgnorePrice
	if ignorePrice == nil || ignorePrice.Int64() <= 0 {
		ignorePrice = DefaultIgnorePrice
		logger.WithFields(log.Fields{
			"provided": params.IgnorePrice,
			"updated":  ignorePrice,
		}).Warn("Sanitizing invalid gasprice oracle ignore price")
	} else if ignorePrice.Int64() > 0 {
		logger.WithField("threshold", ignorePrice).Info("Gasprice oracle is ignoring threshold set")
	}
	return &Oracle{
		backend:          backend,
		lastPrice:        params.Default,
		maxPrice:         maxPrice,
		ignorePrice:      ignorePrice,
		checkBlocks:      blocks,
		percentile:       percent,
		maxHeaderHistory: params.MaxHeaderHistory,
		maxBlockHistory:  params.MaxBlockHistory,
		logger:           logger,
	}
}

// SuggestTipCap returns a tip cap so that newly created transaction can have a
// very high chance to be included in the following blocks.
//
// Note, for legacy transactions and the legacy eth_gasPrice RPC call, it will be
// necessary to add the basefee to the returned number to fall back to the legacy
// behavior.
func (oracle *Oracle) SuggestTipCap(ctx context.Context) (*big.Int, error) {
	return big.NewInt(0), nil
}

type results struct {
	values []*big.Int
	err    error
}

type txSorter struct {
	txs     []*types.Transaction
	baseFee *big.Int
}

func newSorter(txs []*types.Transaction, baseFee *big.Int) *txSorter {
	return &txSorter{
		txs:     txs,
		baseFee: baseFee,
	}
}

func (s *txSorter) Len() int { return len(s.txs) }
func (s *txSorter) Swap(i, j int) {
	s.txs[i], s.txs[j] = s.txs[j], s.txs[i]
}
func (s *txSorter) Less(i, j int) bool {
	// It's okay to discard the error because a tx would never be
	// accepted into a block with an invalid effective tip.
	tip1, _ := s.txs[i].EffectiveGasTip(s.baseFee)
	tip2, _ := s.txs[j].EffectiveGasTip(s.baseFee)
	return tip1.Cmp(tip2) < 0
}

// getBlockPrices calculates the lowest transaction gas price in a given block
// and sends it to the result channel. If the block is empty or all transactions
// are sent by the miner itself(it doesn't make any sense to include this kind of
// transaction prices for sampling), nil gasprice is returned.
func (oracle *Oracle) getBlockValues(ctx context.Context, signer types.Signer, blockNum uint64, limit int, ignoreUnder *big.Int, result chan results, quit chan struct{}) {
	block, err := oracle.backend.BlockByNumber(ctx, rpc.BlockNumber(blockNum))
	if block == nil {
		select {
		case result <- results{nil, err}:
		case <-quit:
		}
		return
	}
	// Sort the transaction by effective tip in ascending sort.
	txs := make([]*types.Transaction, len(block.Body().Transactions()))
	copy(txs, block.Transactions())
	sorter := newSorter(txs, block.BaseFee())
	sort.Sort(sorter)

	var prices []*big.Int
	for _, tx := range sorter.txs {
		tip, _ := tx.EffectiveGasTip(block.BaseFee())
		if ignoreUnder != nil && tip.Cmp(ignoreUnder) == -1 {
			continue
		}
		sender, err := types.Sender(signer, tx)
		if err == nil && sender != block.Coinbase() {
			prices = append(prices, tip)
			if len(prices) >= limit {
				break
			}
		}
	}
	select {
	case result <- results{prices, nil}:
	case <-quit:
	}
}

type bigIntArray []*big.Int

func (s bigIntArray) Len() int           { return len(s) }
func (s bigIntArray) Less(i, j int) bool { return s[i].Cmp(s[j]) < 0 }
func (s bigIntArray) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
