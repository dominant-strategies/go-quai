package types

import (
	"encoding/binary"
	"errors"
	"math"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
)

const (
	MaxDenomination = 16

	MaxOutputIndex = math.MaxUint16
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
	Denominations[16] = big.NewInt(1000000000) // 1000000 Qi
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
	TxHash       common.Hash
	Index        uint16
	Denomination uint8
}

func (outPoint OutPoint) Key() string {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, outPoint.Index)
	bytes := append(indexBytes, outPoint.TxHash.Bytes()...)
	return common.Bytes2Hex(bytes)
}

func (outPoint OutPoint) ProtoEncode() (*ProtoOutPoint, error) {
	protoOutPoint := &ProtoOutPoint{}
	protoOutPoint.Hash = outPoint.TxHash.ProtoEncode()
	index := uint32(outPoint.Index)
	protoOutPoint.Index = &index
	denomination := uint32(outPoint.Denomination)
	protoOutPoint.Denomination = &denomination
	return protoOutPoint, nil
}

func (outPoint *OutPoint) ProtoDecode(protoOutPoint *ProtoOutPoint) error {
	outPoint.TxHash.ProtoDecode(protoOutPoint.Hash)
	outPoint.Index = uint16(*protoOutPoint.Index)
	outPoint.Denomination = uint8(*protoOutPoint.Denomination)
	return nil
}

type OutpointAndDenomination struct {
	TxHash       common.Hash
	Index        uint16
	Denomination uint8
}

func (outPoint OutpointAndDenomination) Key() string {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, outPoint.Index)
	bytes := append(indexBytes, outPoint.TxHash.Bytes()...)
	return common.Bytes2Hex(bytes)
}

func (outPoint OutpointAndDenomination) ProtoEncode() (*ProtoOutPointAndDenomination, error) {
	protoOutPoint := &ProtoOutPointAndDenomination{}
	protoOutPoint.Hash = outPoint.TxHash.ProtoEncode()
	index := uint32(outPoint.Index)
	protoOutPoint.Index = &index
	denomination := uint32(outPoint.Denomination)
	protoOutPoint.Denomination = &denomination

	return protoOutPoint, nil
}

func (outPoint *OutpointAndDenomination) ProtoDecode(protoOutPoint *ProtoOutPointAndDenomination) error {
	outPoint.TxHash.ProtoDecode(protoOutPoint.Hash)
	outPoint.Index = uint16(*protoOutPoint.Index)
	outPoint.Denomination = uint8(*protoOutPoint.Denomination)
	return nil
}

// NewOutPoint returns a new Qi transaction outpoint point with the
// provided hash and index.
func NewOutPoint(txHash *common.Hash, index uint16, denomination uint8) *OutPoint {
	return &OutPoint{
		TxHash:       *txHash,
		Index:        index,
		Denomination: denomination,
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
	Lock         *big.Int // Block height the entry unlocks. 0 or nil = unlocked
}

func (txOut TxOut) ProtoEncode() (*ProtoTxOut, error) {
	protoTxOut := &ProtoTxOut{}

	denomination := uint32(txOut.Denomination)
	protoTxOut.Denomination = &denomination
	protoTxOut.Address = txOut.Address
	if txOut.Lock == nil {
		protoTxOut.Lock = big.NewInt(0).Bytes()
	} else {
		protoTxOut.Lock = txOut.Lock.Bytes()
	}
	return protoTxOut, nil
}

func (txOut *TxOut) ProtoDecode(protoTxOut *ProtoTxOut) error {
	// check if protoTxOut.Denomination is above the max uint8 value
	if *protoTxOut.Denomination > math.MaxUint8 {
		return errors.New("protoTxOut.Denomination is above the max uint8 value")
	}
	txOut.Denomination = uint8(protoTxOut.GetDenomination())
	txOut.Address = protoTxOut.Address
	txOut.Lock = new(big.Int).SetBytes(protoTxOut.Lock)
	return nil
}

// NewTxOut returns a new Qi transaction output with the provided
// transaction value and address.
func NewTxOut(denomination uint8, address []byte, lock *big.Int) *TxOut {
	return &TxOut{
		Denomination: denomination,
		Address:      address,
		Lock:         lock,
	}
}

// UtxoEntry houses details about an individual transaction output in a utxo
// view such as whether or not it was contained in a coinbase tx, the height of
// the block that contains the tx, whether or not it is spent, its public key
// script, and how much it pays.
type UtxoEntry struct {
	Denomination uint8
	Address      []byte   // The address of the output holder.
	Lock         *big.Int // Block height the entry unlocks. 0 = unlocked
}

// Clone returns a shallow copy of the utxo entry.
func (entry *UtxoEntry) Clone() *UtxoEntry {
	if entry == nil {
		return nil
	}

	return &UtxoEntry{
		Denomination: entry.Denomination,
		Address:      entry.Address,
		Lock:         new(big.Int).Set(entry.Lock),
	}
}

// NewUtxoEntry returns a new UtxoEntry built from the arguments.
func NewUtxoEntry(txOut *TxOut) *UtxoEntry {
	return &UtxoEntry{
		Denomination: txOut.Denomination,
		Address:      txOut.Address,
		Lock:         txOut.Lock,
	}
}

type AddressUtxos struct {
	Address common.Address
	Utxos   []*UtxoEntry
}
