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

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
)

type InternalTx struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address `rlp:"nilString"` // nil means contract creation
	Value      *big.Int
	Data       []byte
	AccessList AccessList

	// Signature values
	V *big.Int `json:"v" gencodec:"required"`
	R *big.Int `json:"r" gencodec:"required"`
	S *big.Int `json:"s" gencodec:"required"`
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *InternalTx) copy() TxData {
	cpy := &InternalTx{
		Nonce: tx.Nonce,
		To:    tx.To, // TODO: copy pointed-to address
		Data:  common.CopyBytes(tx.Data),
		Gas:   tx.Gas,
		// These are copied below.
		AccessList: make(AccessList, len(tx.AccessList)),
		Value:      new(big.Int),
		ChainID:    new(big.Int),
		GasTipCap:  new(big.Int),
		GasFeeCap:  new(big.Int),
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
func (tx *InternalTx) txType() byte              { return InternalTxType }
func (tx *InternalTx) chainID() *big.Int         { return tx.ChainID }
func (tx *InternalTx) protected() bool           { return true }
func (tx *InternalTx) accessList() AccessList    { return tx.AccessList }
func (tx *InternalTx) data() []byte              { return tx.Data }
func (tx *InternalTx) gas() uint64               { return tx.Gas }
func (tx *InternalTx) gasFeeCap() *big.Int       { return tx.GasFeeCap }
func (tx *InternalTx) gasTipCap() *big.Int       { return tx.GasTipCap }
func (tx *InternalTx) gasPrice() *big.Int        { return tx.GasFeeCap }
func (tx *InternalTx) value() *big.Int           { return tx.Value }
func (tx *InternalTx) nonce() uint64             { return tx.Nonce }
func (tx *InternalTx) to() *common.Address       { return tx.To }
func (tx *InternalTx) etxGasLimit() uint64       { panic("internal TX does not have etxGasLimit") }
func (tx *InternalTx) etxGasPrice() *big.Int     { panic("internal TX does not have etxGasPrice") }
func (tx *InternalTx) etxGasTip() *big.Int       { panic("internal TX does not have etxGasTip") }
func (tx *InternalTx) etxData() []byte           { panic("internal TX does not have etxData") }
func (tx *InternalTx) etxAccessList() AccessList { panic("internal TX does not have etxAccessList") }
func (tx *InternalTx) etxSender() common.Address { panic("internal TX does not have etxSender") }
func (tx *InternalTx) originatingTxHash() common.Hash {
	panic("internal TX does not have originatingTxHash")
}
func (tx *InternalTx) etxIndex() uint16 { panic("internal TX does not have etxIndex") }
func (tx *InternalTx) txIn() []TxIn     { panic("internal TX does not have txIn") }
func (tx *InternalTx) txOut() []TxOut   { panic("internal TX does not have txOut") }
func (tx *InternalTx) getSchnorrSignature() *schnorr.Signature {
	panic("internal TX does not have getSchnorrSignature")
}

func (tx *InternalTx) getEcdsaSignatureValues() (v, r, s *big.Int) {
	return tx.V, tx.R, tx.S
}

func (tx *InternalTx) setEcdsaSignatureValues(chainID, v, r, s *big.Int) {
	tx.ChainID, tx.V, tx.R, tx.S = chainID, v, r, s
}
