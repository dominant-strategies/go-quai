package types

import (
	"github.com/dominant-strategies/go-quai/common"
)

const (

	// UTXOVersion is the current latest supported transaction version.
	UTXOVersion = 1

	// MaxTxInSequenceNum is the maximum sequence number the sequence field
	// of a transaction input can be.
	MaxTxInSequenceNum uint32 = 0xffffffff

	// MaxPrevOutIndex is the maximum index the index field of a previous
	// outpoint can be.
	MaxPrevOutIndex uint32 = 0xffffffff

	// defaultTxInOutAlloc is the default size used for the backing array for
	// transaction inputs and outputs.  The array will dynamically grow as needed,
	// but this figure is intended to provide enough space for the number of
	// inputs and outputs in a typical transaction without needing to grow the
	// backing array multiple times.
	defaultTxInOutAlloc = 15
)

// TxIn defines a Qi transaction input
type TxIn struct {
	PreviousOutPoint OutPoint
	PubKey           []byte
}

// OutPoint defines a Qi data type that is used to track previous
// transaction outputs.
type OutPoint struct {
	TxHash common.Hash
	Index  uint32
}

// NewTxIn returns a new Qi transaction input
func NewTxIn(prevOut *OutPoint, pubkey []byte) *TxIn {
	return &TxIn{
		PreviousOutPoint: *prevOut,
		PubKey:           pubkey,
	}
}

// TxOut defines a bitcoin transaction output
type TxOut struct {
	Value   uint64
	Address []byte
}

// NewTxOut returns a new Qi transaction output
func NewTxOut(value uint64, address []byte) *TxOut {
	return &TxOut{
		Value:   value,
		Address: address,
	}
}
