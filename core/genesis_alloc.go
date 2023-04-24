// Copyright 2017 The go-ethereum Authors
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

// Constants containing the genesis allocation of built-in genesis blocks.
// Their content is an RLP-encoded list of (address, balance) tuples.
// Use mkalloc.go to create/update them.

// nolint: misspell
const colosseumAllocData = "\xc0"
const gardenAllocData = "\xc0"
const orchardAllocData = "\xc0"
const galenaAllocData = "\xc0"
const LocalAllocData = "\xf8F\xe2\x94\x01\x01t\u026eh\f\x1a\xa7\x8fB\xe5dzb\xf3S\xb7\xbd\u078c\x03;.<\x9f\u0400<\xe8\x00\x00\x00\xe2\x94\x01\x01\xa8u\xa1t\xb3\xbcV^d$\xa0\x05\x0e\xbc\x1b-\x1d\x82\x8c\x03;.<\x9f\u0400<\xe8\x00\x00\x00"
// const localAllocData = "\xc0"
