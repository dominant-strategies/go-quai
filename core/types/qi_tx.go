package types

import (
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
)

type QiTx struct {
	ChainID *big.Int // replay protection
	TxIn    []TxIn
	TxOut   []TxOut

	Signature *schnorr.Signature
}

type WireQiTx struct {
	ChainID   *big.Int // replay protection
	TxIn      []TxIn
	TxOut     []TxOut
	Signature []byte
}

type QiTxWithMinerFee struct {
	Tx       *Transaction
	Fee      *big.Int
	FeePerKB uint64
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *QiTx) copy() TxData {
	cpy := &QiTx{
		ChainID: new(big.Int),
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}

	cpy.TxIn = make([]TxIn, len(tx.TxIn))
	cpy.TxOut = make([]TxOut, len(tx.TxOut))
	if tx.Signature != nil {
		cpy.Signature, _ = schnorr.ParseSignature(tx.Signature.Serialize()) // optional: fatal if error is not nil
	} else {
		cpy.Signature = new(schnorr.Signature)
	}
	copy(cpy.TxIn, tx.TxIn)
	copy(cpy.TxOut, tx.TxOut)
	return cpy
}

func (tx *QiTx) copyToWire() *WireQiTx {
	cpy := &WireQiTx{
		ChainID:   new(big.Int),
		Signature: make([]byte, 64),
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}

	cpy.TxIn = make([]TxIn, len(tx.TxIn))
	cpy.TxOut = make([]TxOut, len(tx.TxOut))
	if tx.Signature != nil {
		copy(cpy.Signature, tx.Signature.Serialize())
	} else {
		copy(cpy.Signature, make([]byte, 64))
	}
	copy(cpy.TxIn, tx.TxIn)
	copy(cpy.TxOut, tx.TxOut)
	return cpy
}

func (tx *WireQiTx) copyFromWire() *QiTx {
	cpy := &QiTx{
		ChainID: new(big.Int),
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}

	cpy.TxIn = make([]TxIn, len(tx.TxIn))
	cpy.TxOut = make([]TxOut, len(tx.TxOut))
	if tx.Signature != nil {
		cpy.Signature, _ = schnorr.ParseSignature(tx.Signature) // optional: fatal if error is not nil
	} else {
		cpy.Signature = new(schnorr.Signature)
	}
	copy(cpy.TxIn, tx.TxIn)
	copy(cpy.TxOut, tx.TxOut)
	return cpy
}

// accessors for innerTx.
func (tx *QiTx) txType() byte              { return QiTxType }
func (tx *QiTx) chainID() *big.Int         { return tx.ChainID }
func (tx *QiTx) protected() bool           { return true }
func (tx *QiTx) accessList() AccessList    { panic("Qi TX does not have accessList") }
func (tx *QiTx) data() []byte              { panic("Qi TX does not have data") }
func (tx *QiTx) gas() uint64               { panic("Qi TX does not have gas") }
func (tx *QiTx) gasFeeCap() *big.Int       { panic("Qi TX does not have gasFeeCap") }
func (tx *QiTx) gasTipCap() *big.Int       { panic("Qi TX does not have gasTipCap") }
func (tx *QiTx) gasPrice() *big.Int        { panic("Qi TX does not have gasPrice") }
func (tx *QiTx) value() *big.Int           { panic("Qi TX does not have value") }
func (tx *QiTx) nonce() uint64             { panic("Qi TX does not have nonce") }
func (tx *QiTx) to() *common.Address       { panic("Qi TX does not have to") }
func (tx *QiTx) etxGasLimit() uint64       { panic("Qi TX does not have etxGasLimit") }
func (tx *QiTx) etxGasPrice() *big.Int     { panic("Qi TX does not have etxGasPrice") }
func (tx *QiTx) etxGasTip() *big.Int       { panic("Qi TX does not have etxGasTip") }
func (tx *QiTx) etxData() []byte           { panic("Qi TX does not have etxData") }
func (tx *QiTx) etxAccessList() AccessList { panic("Qi TX does not have etxAccessList") }
func (tx *QiTx) originatingTxHash() common.Hash {
	panic("Qi TX does not have originatingTxHash")
}
func (tx *QiTx) etxIndex() uint16 { panic("Qi TX does not have etxIndex") }
func (tx *QiTx) etxSender() common.Address {
	panic("Qi TX does not have etxSender")
}
func (tx *QiTx) txIn() []TxIn                            { return tx.TxIn }
func (tx *QiTx) txOut() []TxOut                          { return tx.TxOut }
func (tx *QiTx) getSchnorrSignature() *schnorr.Signature { return tx.Signature }

func (tx *QiTx) getEcdsaSignatureValues() (v, r, s *big.Int) {
	panic("Qi TX does not have ECDSA signature values")
}

func (tx *QiTx) setEcdsaSignatureValues(chainID, v, r, s *big.Int) {
	panic("Qi TX does not have ECDSA signature values")
}

func CalculateQiTxGas(transaction *Transaction) uint64 {
	if transaction.Type() != QiTxType {
		panic("CalculateQiTxGas called on a transaction that is not a Qi transaction")
	}
	return uint64(len(transaction.TxIn()))*params.SloadGas + uint64(len(transaction.TxOut()))*params.CallValueTransferGas + params.EcrecoverGas
}
