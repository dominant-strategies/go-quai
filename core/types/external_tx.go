// Copyright 2021 The go-ethereum Authors
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

package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

type ExternalTx struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address `rlp:"nil"` // nil means contract creation
	Value      *big.Int
	AccessList AccessList
	ToLocation []byte

	// Signature values
	V *big.Int `json:"v" gencodec:"required"`
	R *big.Int `json:"r" gencodec:"required"`
	S *big.Int `json:"s" gencodec:"required"`
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *ExternalTx) copy() TxData {
	cpy := &ExternalTx{
		Nonce: tx.Nonce,
		To:    tx.To, // TODO: copy pointed-to address
		Gas:   tx.Gas,
		// These are copied below.
		AccessList: make(AccessList, len(tx.AccessList)),
		Value:      new(big.Int),
		ChainID:    new(big.Int),
		GasTipCap:  new(big.Int),
		GasFeeCap:  new(big.Int),
		ToLocation: common.CopyBytes(tx.ToLocation),
		V:          new(big.Int),
		R:          new(big.Int),
		S:          new(big.Int),
	}
	copy(cpy.AccessList, tx.AccessList)
	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}
	if tx.GasTipCap != nil {
		cpy.GasTipCap.Set(tx.GasTipCap)
	}
	if tx.GasFeeCap != nil {
		cpy.GasFeeCap.Set(tx.GasFeeCap)
	}
	if tx.V != nil {
		cpy.V.Set(tx.V)
	}
	if tx.R != nil {
		cpy.R.Set(tx.R)
	}
	if tx.S != nil {
		cpy.S.Set(tx.S)
	}
	return cpy
}

// accessors for innerTx.
func (tx *ExternalTx) txType() byte           { return ExternalTxType }
func (tx *ExternalTx) chainID() *big.Int      { return tx.ChainID }
func (tx *ExternalTx) protected() bool        { return true }
func (tx *ExternalTx) accessList() AccessList { return tx.AccessList }
func (tx *ExternalTx) data() []byte           { return nil }
func (tx *ExternalTx) gas() uint64            { return tx.Gas }
func (tx *ExternalTx) gasFeeCap() *big.Int    { return tx.GasFeeCap }
func (tx *ExternalTx) gasTipCap() *big.Int    { return tx.GasTipCap }
func (tx *ExternalTx) gasPrice() *big.Int     { return tx.GasFeeCap }
func (tx *ExternalTx) value() *big.Int        { return tx.Value }
func (tx *ExternalTx) nonce() uint64          { return tx.Nonce }
func (tx *ExternalTx) to() *common.Address    { return tx.To }
func (tx *ExternalTx) toLocation() []byte     { return nil }

func (tx *ExternalTx) rawSignatureValues() (v, r, s *big.Int) {
	return tx.V, tx.R, tx.S
}

func (tx *ExternalTx) setSignatureValues(chainID, v, r, s *big.Int) {
	tx.ChainID, tx.V, tx.R, tx.S = chainID, v, r, s
}
