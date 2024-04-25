// Copyright 2016 The go-ethereum Authors
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

package core

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/log"
)

// ChainContext supports retrieving headers and consensus parameters from the
// current blockchain to be used during transaction processing.
type ChainContext interface {
	// Engine retrieves the chain's consensus engine.
	Engine() consensus.Engine

	// GetHeader returns the hash corresponding to their hash.
	GetHeader(common.Hash, uint64) *types.WorkObject

	// GetHeader returns a block header from the database by hash.
	// The header might not be on the canonical chain.
	GetHeaderOrCandidate(common.Hash, uint64) *types.WorkObject

	// NodeCtx returns the context of the running node
	NodeCtx() int

	// IsGenesisHash returns true if the given hash is the genesis block hash
	IsGenesisHash(common.Hash) bool

	// GetHeaderByHash returns a block header from the database by hash.
	GetHeaderByHash(common.Hash) *types.WorkObject

	// CheckIfEtxIsEligible checks if the given slice is eligible to accept the
	// etx based on the EtxEligibleSlices
	CheckIfEtxIsEligible(common.Hash, common.Location) bool
}

// NewEVMBlockContext creates a new context for use in the EVM.
func NewEVMBlockContext(header *types.WorkObject, chain ChainContext, author *common.Address) vm.BlockContext {
	var (
		beneficiary common.Address
		baseFee     *big.Int
	)

	// If we don't have an explicit author (i.e. not mining), extract from the header
	if author == nil {
		beneficiary, _ = chain.Engine().Author(header) // Ignore error, we're past header validation
	} else {
		beneficiary = *author
	}
	if header.BaseFee() != nil {
		baseFee = new(big.Int).Set(header.BaseFee())
	}

	timestamp := header.Time() // base case, should only be the case in genesis block or before forkBlock (in testnet)
	if header.Number(chain.NodeCtx()).Uint64() != 0 {
		parent := chain.GetHeaderOrCandidate(header.ParentHash(chain.NodeCtx()), header.Number(chain.NodeCtx()).Uint64()-1)
		if parent != nil {
			timestamp = parent.Time()
		} else {
			log.Global.Fatal("Parent is not in the db, panic", "headerHash", header.Hash(), "parentHash", header.ParentHash(chain.NodeCtx()), "number", header.Number(chain.NodeCtx()).Uint64())
		}
	}

	// Prime terminus determines which location is eligible to except the etx
	primeTerminus := header.PrimeTerminus()
	primeTerminusHeader := chain.GetHeaderByHash(primeTerminus)
	etxEligibleSlices := primeTerminusHeader.EtxEligibleSlices()

	return vm.BlockContext{
		CanTransfer:        CanTransfer,
		Transfer:           Transfer,
		GetHash:            GetHashFn(header, chain),
		Coinbase:           beneficiary,
		BlockNumber:        new(big.Int).Set(header.Number(chain.NodeCtx())),
		Time:               new(big.Int).SetUint64(timestamp),
		Difficulty:         new(big.Int).Set(header.Difficulty()),
		BaseFee:            baseFee,
		GasLimit:           header.GasLimit(),
		CheckIfEtxEligible: chain.CheckIfEtxIsEligible,
		EtxEligibleSlices:  etxEligibleSlices,
	}
}

// NewEVMTxContext creates a new transaction context for a single transaction.
func NewEVMTxContext(msg Message) vm.TxContext {
	return vm.TxContext{
		Origin:     msg.From(),
		GasPrice:   new(big.Int).Set(msg.GasPrice()),
		TxType:     msg.Type(),
		Hash:       msg.Hash(),
		TXGasTip:   msg.GasTipCap(),
		AccessList: msg.AccessList(),
		ETXSender:  msg.ETXSender(),
	}
}

// GetHashFn returns a GetHashFunc which retrieves header hashes by number
func GetHashFn(ref *types.WorkObject, chain ChainContext) func(n uint64) common.Hash {
	// Cache will initially contain [refHash.parent],
	// Then fill up with [refHash.p, refHash.pp, refHash.ppp, ...]
	var cache []common.Hash

	return func(n uint64) common.Hash {
		// If there's no hash cache yet, make one
		if len(cache) == 0 {
			cache = append(cache, ref.ParentHash(chain.NodeCtx()))
		}
		if idx := ref.NumberU64(chain.NodeCtx()) - n - 1; idx < uint64(len(cache)) {
			return cache[idx]
		}
		// No luck in the cache, but we can start iterating from the last element we already know
		lastKnownHash := cache[len(cache)-1]
		lastKnownNumber := ref.NumberU64(chain.NodeCtx()) - uint64(len(cache))

		for {
			header := chain.GetHeader(lastKnownHash, lastKnownNumber)
			if header == nil {
				break
			}
			cache = append(cache, header.ParentHash(chain.NodeCtx()))
			lastKnownHash = header.ParentHash(chain.NodeCtx())
			lastKnownNumber = header.NumberU64(chain.NodeCtx()) - 1
			if n == lastKnownNumber {
				return lastKnownHash
			}
		}
		return common.Hash{}
	}
}

// CanTransfer checks whether there are enough funds in the address' account to make a transfer.
// This does not take the necessary gas in to account to make the transfer valid.
func CanTransfer(db vm.StateDB, addr common.Address, amount *big.Int) bool {
	internalAddr, err := addr.InternalAndQuaiAddress()
	if err != nil {
		return false
	}
	return db.GetBalance(internalAddr).Cmp(amount) >= 0
}

// Transfer subtracts amount from sender and adds amount to recipient using the given Db
func Transfer(db vm.StateDB, sender, recipient common.Address, amount *big.Int) error {
	internalSender, err := sender.InternalAndQuaiAddress()
	if err != nil {
		return err
	}
	internalRecipient, err := recipient.InternalAndQuaiAddress()
	if err != nil {
		return err
	}
	db.SubBalance(internalSender, amount)
	db.AddBalance(internalRecipient, amount)
	return nil
}
