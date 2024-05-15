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

type QuaiTx struct {
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

	// Work fields
	ParentHash *common.Hash
	MixHash    *common.Hash
	WorkNonce  *BlockNonce
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *QuaiTx) copy() TxData {
	cpy := &QuaiTx{
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
		ParentHash: tx.ParentHash,
		MixHash:    tx.MixHash,
		WorkNonce:  tx.WorkNonce,
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
func (tx *QuaiTx) txType() byte              { return QuaiTxType }
func (tx *QuaiTx) chainID() *big.Int         { return tx.ChainID }
func (tx *QuaiTx) accessList() AccessList    { return tx.AccessList }
func (tx *QuaiTx) data() []byte              { return tx.Data }
func (tx *QuaiTx) gas() uint64               { return tx.Gas }
func (tx *QuaiTx) gasFeeCap() *big.Int       { return tx.GasFeeCap }
func (tx *QuaiTx) gasTipCap() *big.Int       { return tx.GasTipCap }
func (tx *QuaiTx) gasPrice() *big.Int        { return tx.GasFeeCap }
func (tx *QuaiTx) value() *big.Int           { return tx.Value }
func (tx *QuaiTx) nonce() uint64             { return tx.Nonce }
func (tx *QuaiTx) to() *common.Address       { return tx.To }
func (tx *QuaiTx) parentHash() *common.Hash  { return tx.ParentHash }
func (tx *QuaiTx) mixHash() *common.Hash     { return tx.MixHash }
func (tx *QuaiTx) workNonce() *BlockNonce    { return tx.WorkNonce }
func (tx *QuaiTx) etxSender() common.Address { panic("internal TX does not have etxSender") }
func (tx *QuaiTx) originatingTxHash() common.Hash {
	panic("internal TX does not have originatingTxHash")
}
func (tx *QuaiTx) etxIndex() uint16 { panic("internal TX does not have etxIndex") }
func (tx *QuaiTx) txIn() TxIns      { panic("internal TX does not have txIn") }
func (tx *QuaiTx) txOut() TxOuts    { panic("internal TX does not have txOut") }
func (tx *QuaiTx) getSchnorrSignature() *schnorr.Signature {
	panic("internal TX does not have getSchnorrSignature")
}

func (tx *QuaiTx) getEcdsaSignatureValues() (v, r, s *big.Int) {
	return tx.V, tx.R, tx.S
}

func (tx *QuaiTx) setEcdsaSignatureValues(chainID, v, r, s *big.Int) {
	tx.ChainID, tx.V, tx.R, tx.S = chainID, v, r, s
}

func (tx *QuaiTx) setTo(to common.Address) {
	tx.To = &to
}
