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

package eth

import (
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/forkid"
	"github.com/dominant-strategies/go-quai/p2p/enode"
	"github.com/dominant-strategies/go-quai/rlp"
)

// ethEntry is the "eth" ENR entry which advertises eth protocol
// on the discovery network.
type ethEntry struct {
	ForkID forkid.ID // Fork identifier

	// Ignore additional fields (for forward compatibility).
	Rest []rlp.RawValue `rlp:"tail"`
}

// ENRKey implements enr.Entry.
func (e ethEntry) ENRKey() string {
	return "eth"
}

// startEthEntryUpdate starts the ENR updater loop.
func (eth *Quai) startEthEntryUpdate(ln *enode.LocalNode) {
	var newHead = make(chan core.ChainHeadEvent, 10)
	sub := eth.core.SubscribeChainHeadEvent(newHead)

	go func() {
		defer sub.Unsubscribe()
		for {
			select {
			case <-newHead:
				ln.Set(eth.currentEthEntry())
			case <-sub.Err():
				// Would be nice to sync with eth.Stop, but there is no
				// good way to do that.
				return
			}
		}
	}()
}

func (eth *Quai) currentEthEntry() *ethEntry {
	return &ethEntry{ForkID: forkid.NewID(eth.core.Config(), eth.core.Genesis().Hash(),
		eth.core.CurrentHeader().Number().Uint64())}
}
