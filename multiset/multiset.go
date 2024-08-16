// ISC License

// Copyright (c) 2018-2019 The kaspanet developers
// Copyright (c) 2013-2018 The btcsuite developers
// Copyright (c) 2015-2016 The Decred developers
// Copyright (c) 2013-2014 Conformal Systems LLC.

// Permission to use, copy, modify, and distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.

// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.package multiset
package multiset

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/kaspanet/go-muhash"
	"github.com/pkg/errors"
)

type multiset struct {
	ms *muhash.MuHash
}

func (m multiset) Add(data []byte) {
	m.ms.Add(data)
}

func (m multiset) Remove(data []byte) {
	m.ms.Remove(data)
}

func (m multiset) Hash() common.Hash {
	finalizedHash := m.ms.Finalize()
	return common.BytesToHash(finalizedHash[:])
}

func (m multiset) Serialize() []byte {
	return m.ms.Serialize()[:]
}

func (m multiset) Clone() *multiset {
	return &multiset{ms: m.ms.Clone()}
}

// FromBytes deserializes the given bytes slice and returns a multiset.
func FromBytes(multisetBytes []byte) (*multiset, error) {
	serialized := &muhash.SerializedMuHash{}
	if len(serialized) != len(multisetBytes) {
		return nil, errors.Errorf("mutliset bytes expected to be in length of %d but got %d",
			len(serialized), len(multisetBytes))
	}
	copy(serialized[:], multisetBytes)
	ms, err := muhash.DeserializeMuHash(serialized)
	if err != nil {
		return nil, err
	}

	return &multiset{ms: ms}, nil
}

// New returns a new model.Multiset
func New() *multiset {
	return &multiset{ms: muhash.NewMuHash()}
}
