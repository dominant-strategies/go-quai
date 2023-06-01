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

package eth

import (
	"math/big"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/eth/protocols/eth"
)

// ethPeerInfo represents a short summary of the `eth` sub-protocol metadata known
// about a connected peer.
type ethPeerInfo struct {
	Version uint     `json:"version"` // Quai protocol version negotiated
	Entropy *big.Int `json:"entropy"` // Head Entropy of the peer's blockchain
	Head    string   `json:"head"`    // Hex hash of the peer's best owned block
}

// ethPeer is a wrapper around eth.Peer to maintain a few extra metadata.
type ethPeer struct {
	*eth.Peer

	syncDrop *time.Timer  // Connection dropper if `eth` sync progress isn't validated in time
	lock     sync.RWMutex // Mutex protecting the internal fields
}

// info gathers and returns some `eth` protocol metadata known about a peer.
func (p *ethPeer) info() *ethPeerInfo {
	hash, _, entropy, _ := p.Head()

	return &ethPeerInfo{
		Version: p.Version(),
		Entropy: entropy,
		Head:    hash.Hex(),
	}
}
