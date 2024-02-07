package types

import (
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
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

	MaxDenomination = 16
)

var MaxQi = new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(params.Ether)) // This is just a default; determine correct value later

// Denominations is a map of denomination to number of Qi
var Denominations map[uint8]*big.Int

func init() {
	// Initialize denominations
	Denominations = make(map[uint8]*big.Int)
	Denominations[0] = big.NewInt(1)           // 0.001 Qi
	Denominations[1] = big.NewInt(5)           // 0.005 Qi
	Denominations[2] = big.NewInt(10)          // 0.01 Qi
	Denominations[3] = big.NewInt(50)          // 0.05 Qi
	Denominations[4] = big.NewInt(100)         // 0.1 Qi
	Denominations[5] = big.NewInt(250)         // 0.25 Qi
	Denominations[6] = big.NewInt(500)         // 0.5 Qi
	Denominations[7] = big.NewInt(1000)        // 1 Qi
	Denominations[8] = big.NewInt(5000)        // 5 Qi
	Denominations[9] = big.NewInt(10000)       // 10 Qi
	Denominations[10] = big.NewInt(20000)      // 20 Qi
	Denominations[11] = big.NewInt(50000)      // 50 Qi
	Denominations[12] = big.NewInt(100000)     // 100 Qi
	Denominations[13] = big.NewInt(1000000)    // 1000 Qi
	Denominations[14] = big.NewInt(10000000)   // 10000 Qi
	Denominations[15] = big.NewInt(100000000)  // 100000 Qi
	Denominations[16] = big.NewInt(1000000000) // 1000000
}

type TxIns []TxIn

func (txIns TxIns) ProtoEncode() (*ProtoTxIns, error) {
	protoTxIns := &ProtoTxIns{}
	for _, txIn := range txIns {
		protoTxIn, err := txIn.ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoTxIns.TxIns = append(protoTxIns.TxIns, protoTxIn)
	}
	return protoTxIns, nil
}

func (txIns *TxIns) ProtoDecode(protoTxIns *ProtoTxIns) error {
	for _, protoTxIn := range protoTxIns.TxIns {
		decodedTxIn := &TxIn{}
		err := decodedTxIn.ProtoDecode(protoTxIn)
		if err != nil {
			return err
		}
		*txIns = append(*txIns, *decodedTxIn)
	}
	return nil
}

// TxIn defines a Qi transaction input
type TxIn struct {
	PreviousOutPoint OutPoint
	PubKey           []byte
}

func (txIn TxIn) ProtoEncode() (*ProtoTxIn, error) {
	protoTxIn := &ProtoTxIn{}

	var err error
	protoTxIn.PreviousOutPoint, err = txIn.PreviousOutPoint.ProtoEncode()
	if err != nil {
		return nil, err
	}

	protoTxIn.PubKey = txIn.PubKey
	return protoTxIn, nil
}

func (txIn *TxIn) ProtoDecode(protoTxIn *ProtoTxIn) error {
	err := txIn.PreviousOutPoint.ProtoDecode(protoTxIn.PreviousOutPoint)
	if err != nil {
		return err
	}
	txIn.PubKey = protoTxIn.PubKey
	return nil
}

// OutPoint defines a Qi data type that is used to track previous
type OutPoint struct {
	TxHash common.Hash
	Index  uint32
}

func (outPoint OutPoint) ProtoEncode() (*ProtoOutPoint, error) {
	protoOutPoint := &ProtoOutPoint{}
	protoOutPoint.Hash = outPoint.TxHash.ProtoEncode()
	protoOutPoint.Index = &outPoint.Index
	return protoOutPoint, nil
}

func (outPoint *OutPoint) ProtoDecode(protoOutPoint *ProtoOutPoint) error {
	outPoint.TxHash.ProtoDecode(protoOutPoint.Hash)
	outPoint.Index = *protoOutPoint.Index
	return nil
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

type TxOuts []TxOut

func (txOuts TxOuts) ProtoEncode() (*ProtoTxOuts, error) {
	protoTxOuts := &ProtoTxOuts{}
	for _, txOut := range txOuts {
		protoTxOut, err := txOut.ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoTxOuts.TxOuts = append(protoTxOuts.TxOuts, protoTxOut)
	}
	return protoTxOuts, nil
}

func (txOuts *TxOuts) ProtoDecode(protoTxOuts *ProtoTxOuts) error {
	for _, protoTxOut := range protoTxOuts.TxOuts {
		decodedTxOut := &TxOut{}
		err := decodedTxOut.ProtoDecode(protoTxOut)
		if err != nil {
			return err
		}
		*txOuts = append(*txOuts, *decodedTxOut)
	}
	return nil
}

// TxOut defines a Qi transaction output.
type TxOut struct {
	Denomination uint8
	Address      []byte
}

func (txOut TxOut) ProtoEncode() (*ProtoTxOut, error) {
	protoTxOut := &ProtoTxOut{}

	denomination := uint32(txOut.Denomination)
	protoTxOut.Denomination = &denomination
	protoTxOut.Address = txOut.Address
	return protoTxOut, nil
}

func (txOut *TxOut) ProtoDecode(protoTxOut *ProtoTxOut) error {
	// check if protoTxOut.Denomination is above the max uint8 value
	if *protoTxOut.Denomination > math.MaxUint8 {
		return errors.New("protoTxOut.Denomination is above the max uint8 value")
	}
	txOut.Denomination = uint8(protoTxOut.GetDenomination())
	txOut.Address = protoTxOut.Address
	return nil
}

// NewTxOut returns a new Qi transaction output with the provided
// transaction value and address.
func NewTxOut(denomination uint8, address []byte) *TxOut {
	return &TxOut{
		Denomination: denomination,
		Address:      address,
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
	totalQit := big.NewInt(0)
	for _, txOut := range tx.TxOut() {
		denomination := txOut.Denomination
		if denomination > MaxDenomination {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v", denomination,
				MaxDenomination)
			return errors.New(str)
		}

		// Two's complement int64 overflow guarantees that any overflow
		// is detected and reported.  This is impossible for Bitcoin, but
		// perhaps possible if an alt increases the total money supply.
		totalQit.Add(totalQit, Denominations[denomination])
		if totalQit.Cmp(MaxQi) == 1 {
			str := fmt.Sprintf("total value of all transaction "+
				"outputs is %v which is higher than max "+
				"allowed value of %v", totalQit,
				math.MaxFloat32)
			return errors.New(str)
		}

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
