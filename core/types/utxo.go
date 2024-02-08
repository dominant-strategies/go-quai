package types

import (
	"errors"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
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

// NewOutPoint returns a new Qi transaction outpoint point with the
// provided hash and index.
func NewOutPoint(txHash *common.Hash, index uint32) *OutPoint {
	return &OutPoint{
		TxHash: *txHash,
		Index:  index,
	}
}

// NewTxIn returns a new bitcoin transaction input with the provided
// previous outpoint point and signature script with a default sequence of
// MaxTxInSequenceNum.
func NewTxIn(prevOut *OutPoint, pubkey []byte, witness [][]byte) *TxIn {
	return &TxIn{
		PreviousOutPoint: *prevOut,
		PubKey:           pubkey,
	}
}

// TxOut defines a Qi transaction output.
type TxOut struct {
	Value   uint64
	Address []byte
}

// NewTxOut returns a new Qi transaction output with the provided
// transaction value and address.
func NewTxOut(value uint64, address []byte) *TxOut {
	return &TxOut{
		Value:   value,
		Address: address,
	}
}

// CheckTransactionSanity performs some preliminary checks on a transaction to
// ensure it is sane.  These checks are context free.
func CheckUTXOTransactionSanity(tx *Transaction, location common.Location) error {
	// A transaction must have at least one input.
	if len(tx.TxIn()) == 0 {
		return errors.New("transaction has no inputs")
	}

	// A transaction must have at least one output.
	if len(tx.TxOut()) == 0 {
		return errors.New("transaction has no outputs")
	}

	// TODO: A transaction must not exceed the maximum allowed block payload when
	// serialized.

	// Ensure the transaction amounts are in range.  Each transaction
	// output must not be negative or more than the max allowed per
	// transaction.  Also, the total of all outputs must abide by the same
	// restrictions.  All amounts in a transaction are in a unit value known
	// as a qit.  One bitcoin is a quantity of qit as defined by the
	// QitPerBitcoin constant.
	for _, txOut := range tx.TxOut() {
		if _, err := common.BytesToAddress(txOut.Address, location).InternalAddress(); err != nil {
			return errors.New("invalid output address: " + err.Error())
		}
	}

	// Check for duplicate transaction inputs.
	existingTxOut := make(map[OutPoint]struct{})
	for _, txIn := range tx.TxIn() {
		if _, exists := existingTxOut[txIn.PreviousOutPoint]; exists {
			return errors.New("transaction contains duplicate inputs")
		}
		existingTxOut[txIn.PreviousOutPoint] = struct{}{}
	}

	// Previous transaction outputs referenced by the inputs to this
	// transaction must not be null.
	for _, txIn := range tx.TxIn() {
		if txIn.PreviousOutPoint.Index == math.MaxUint32 && txIn.PreviousOutPoint.TxHash == common.Zero.Hash() {
			return errors.New("transaction " +
				"input refers to previous output that " +
				"is null")
		}
	}

	return nil
}
