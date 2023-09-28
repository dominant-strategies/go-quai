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

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto/sr25519"
)

type InternalTx struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address `rlp:"nilString"` // nil means contract creation
	FromPubKey sr25519.PublicKey
	Value      *big.Int
	Data       []byte
	AccessList AccessList

	// Signature values
	Signature []byte
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *InternalTx) copy() TxData {
	cpy := &InternalTx{
		Nonce:      tx.Nonce,
		To:         tx.To, // TODO: copy pointed-to address
		FromPubKey: tx.FromPubKey,
		Data:       common.CopyBytes(tx.Data),
		Gas:        tx.Gas,
		// These are copied below.
		AccessList: make(AccessList, len(tx.AccessList)),
		Value:      new(big.Int),
		ChainID:    new(big.Int),
		GasTipCap:  new(big.Int),
		GasFeeCap:  new(big.Int),
		Signature:  common.CopyBytes(tx.Signature),
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
	return cpy
}

// accessors for innerTx.
func (tx *InternalTx) txType() byte                  { return InternalTxType }
func (tx *InternalTx) chainID() *big.Int             { return tx.ChainID }
func (tx *InternalTx) protected() bool               { return true }
func (tx *InternalTx) accessList() AccessList        { return tx.AccessList }
func (tx *InternalTx) data() []byte                  { return tx.Data }
func (tx *InternalTx) gas() uint64                   { return tx.Gas }
func (tx *InternalTx) gasFeeCap() *big.Int           { return tx.GasFeeCap }
func (tx *InternalTx) gasTipCap() *big.Int           { return tx.GasTipCap }
func (tx *InternalTx) gasPrice() *big.Int            { return tx.GasFeeCap }
func (tx *InternalTx) value() *big.Int               { return tx.Value }
func (tx *InternalTx) nonce() uint64                 { return tx.Nonce }
func (tx *InternalTx) to() *common.Address           { return tx.To }
func (tx *InternalTx) fromPubKey() sr25519.PublicKey { return tx.FromPubKey }
func (tx *InternalTx) etxGasLimit() uint64           { panic("internal TX does not have etxGasLimit") }
func (tx *InternalTx) etxGasPrice() *big.Int         { panic("internal TX does not have etxGasPrice") }
func (tx *InternalTx) etxGasTip() *big.Int           { panic("internal TX does not have etxGasTip") }
func (tx *InternalTx) etxData() []byte               { panic("internal TX does not have etxData") }
func (tx *InternalTx) etxAccessList() AccessList     { panic("internal TX does not have etxAccessList") }

func (tx *InternalTx) rawSignatureValues() []byte {
	return tx.Signature
}

func (tx *InternalTx) setSignatureValues(chainID *big.Int, sig []byte) {
	tx.ChainID, tx.Signature = chainID, sig
}
