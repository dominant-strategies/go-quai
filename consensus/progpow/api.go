// Copyright 2018 The go-ethereum Authors
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

package progpow

import (
	"errors"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

var errProgpowStopped = errors.New("progpow stopped")

// API exposes progpow related methods for the RPC interface.
type API struct {
	progpow *Progpow
}

// GetWork returns a work package for external miner.
//
// The work package consists of 3 strings:
//
//	result[0] - 32 bytes hex encoded current block header pow-hash
//	result[1] - 32 bytes hex encoded seed hash used for DAG
//	result[2] - 32 bytes hex encoded boundary condition ("target"), 2^256/difficulty
//	result[3] - hex encoded block number
func (api *API) GetWork() ([4]string, error) {
	if api.progpow.remote == nil {
		return [4]string{}, errors.New("not supported")
	}

	var (
		workCh = make(chan [4]string, 1)
		errc   = make(chan error, 1)
	)
	select {
	case api.progpow.remote.fetchWorkCh <- &sealWork{errc: errc, res: workCh}:
	case <-api.progpow.remote.exitCh:
		return [4]string{}, errProgpowStopped
	}
	select {
	case work := <-workCh:
		return work, nil
	case err := <-errc:
		return [4]string{}, err
	}
}

// SubmitWork can be used by external miner to submit their POW solution.
// It returns an indication if the work was accepted.
// Note either an invalid solution, a stale work a non-existent work will return false.
func (api *API) SubmitWork(nonce types.BlockNonce, hash, digest common.Hash) bool {
	if api.progpow.remote == nil {
		return false
	}

	var errc = make(chan error, 1)
	select {
	case api.progpow.remote.submitWorkCh <- &mineResult{
		nonce: nonce,
		hash:  hash,
		errc:  errc,
	}:
	case <-api.progpow.remote.exitCh:
		return false
	}
	err := <-errc
	return err == nil
}
