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

type InternalToExternalTx struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address   `rlp:"nilString"` // nil means contract creation
	FromPubKey sr25519.PublicKey `rlp:"nilString"`
	Value      *big.Int
	Data       []byte     // this probably is not applicable
	AccessList AccessList // this probably is not applicable

	ETXGasLimit   uint64
	ETXGasPrice   *big.Int
	ETXGasTip     *big.Int
	ETXData       []byte
	ETXAccessList AccessList

	// Signature values
	Signature []byte
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *InternalToExternalTx) copy() TxData {
	cpy := &InternalToExternalTx{
		Nonce:      tx.Nonce,
		To:         tx.To, // TODO: copy pointed-to address
		FromPubKey: tx.FromPubKey,
		Data:       common.CopyBytes(tx.Data),
		Gas:        tx.Gas,
		// These are copied below.
		AccessList:    make(AccessList, len(tx.AccessList)),
		Value:         new(big.Int),
		ChainID:       new(big.Int),
		GasTipCap:     new(big.Int),
		GasFeeCap:     new(big.Int),
		ETXGasLimit:   tx.ETXGasLimit,
		ETXGasPrice:   new(big.Int),
		ETXGasTip:     new(big.Int),
		ETXData:       common.CopyBytes(tx.ETXData),
		ETXAccessList: make(AccessList, len(tx.ETXAccessList)),
		Signature:     common.CopyBytes(tx.Signature),
	}
	copy(cpy.AccessList, tx.AccessList)
	copy(cpy.ETXAccessList, tx.ETXAccessList)
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
	if tx.ETXGasPrice != nil {
		cpy.ETXGasPrice.Set(tx.ETXGasPrice)
	}
	if tx.ETXGasTip != nil {
		cpy.ETXGasTip.Set(tx.ETXGasTip)
	}
	return cpy
}

// accessors for innerTx.
func (tx *InternalToExternalTx) txType() byte                  { return InternalToExternalTxType }
func (tx *InternalToExternalTx) chainID() *big.Int             { return tx.ChainID }
func (tx *InternalToExternalTx) protected() bool               { return true }
func (tx *InternalToExternalTx) accessList() AccessList        { return tx.AccessList }
func (tx *InternalToExternalTx) data() []byte                  { return tx.Data }
func (tx *InternalToExternalTx) gas() uint64                   { return tx.Gas }
func (tx *InternalToExternalTx) gasFeeCap() *big.Int           { return tx.GasFeeCap }
func (tx *InternalToExternalTx) gasTipCap() *big.Int           { return tx.GasTipCap }
func (tx *InternalToExternalTx) gasPrice() *big.Int            { return tx.GasFeeCap }
func (tx *InternalToExternalTx) value() *big.Int               { return tx.Value }
func (tx *InternalToExternalTx) nonce() uint64                 { return tx.Nonce }
func (tx *InternalToExternalTx) to() *common.Address           { return tx.To }
func (tx *InternalToExternalTx) fromPubKey() sr25519.PublicKey { return tx.FromPubKey }
func (tx *InternalToExternalTx) etxGasLimit() uint64           { return tx.ETXGasLimit }
func (tx *InternalToExternalTx) etxGasPrice() *big.Int         { return tx.ETXGasPrice }
func (tx *InternalToExternalTx) etxGasTip() *big.Int           { return tx.ETXGasTip }
func (tx *InternalToExternalTx) etxData() []byte               { return tx.ETXData }
func (tx *InternalToExternalTx) etxAccessList() AccessList     { return tx.ETXAccessList }

func (tx *InternalToExternalTx) rawSignatureValues() []byte {
	return tx.Signature
}

func (tx *InternalToExternalTx) setSignatureValues(chainID *big.Int, sig []byte) {
	tx.ChainID, tx.Signature = chainID, sig
}
