// Copyright 2019 The go-ethereum Authors
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
	lru "github.com/hashicorp/golang-lru"
	"sync"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/state"
)

const (
	c_maxNonceCache = 600 //Maximum number of entries that we can hold in the nonces cahce
)

// txNoncer is a tiny virtual state database to manage the executable nonces of
// accounts in the pool, falling back to reading from a real state database if
// an account is unknown.
type txNoncer struct {
	fallback *state.StateDB
	nonces   *lru.Cache
	lock     sync.Mutex
}

// newTxNoncer creates a new virtual state database to track the pool nonces.
func newTxNoncer(statedb *state.StateDB) *txNoncer {
	n := &txNoncer{
		fallback: statedb.Copy(),
	}
	nonces, _ := lru.New(c_maxNonceCache)
	n.nonces = nonces

	return n
}

// get returns the current nonce of an account, falling back to a real state
// database if the account is unknown.
func (txn *txNoncer) get(addr common.InternalAddress) uint64 {
	// We use mutex for get operation is the underlying
	// state will mutate db even for read access.
	txn.lock.Lock()
	defer txn.lock.Unlock()
	txn.nonces.ContainsOrAdd(addr, txn.fallback.GetNonce(addr))
	nonce, _ := txn.nonces.Get(addr)
	return nonce.(uint64)
}

// set inserts a new virtual nonce into the virtual state database to be returned
// whenever the pool requests it instead of reaching into the real state database.
func (txn *txNoncer) set(addr common.InternalAddress, nonce uint64) {
	txn.lock.Lock()
	defer txn.lock.Unlock()
	txn.nonces.Add(addr, nonce)
}

// setIfLower updates a new virtual nonce into the virtual state database if the
// the new one is lower.
func (txn *txNoncer) setIfLower(addr common.InternalAddress, nonce uint64) {
	txn.lock.Lock()
	defer txn.lock.Unlock()
	txn.nonces.ContainsOrAdd(addr, txn.fallback.GetNonce(addr))
	currentNonce, _ := txn.nonces.Peek(addr)
	if currentNonce.(uint64) <= nonce {
		return
	}
	txn.nonces.Add(addr, nonce)
}
