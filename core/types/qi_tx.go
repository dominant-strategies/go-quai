package types

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
)

type QiTx struct {
	ChainID *big.Int // replay protection
	TxIn    []*TxIn
	TxOut   []*TxOut
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *QiTx) copy() TxData {
	cpy := &QiTx{
		ChainID: new(big.Int),
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}
	cpy.TxIn = make([]*TxIn, len(tx.TxIn))
	cpy.TxOut = make([]*TxOut, len(tx.TxOut))
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
func (tx *QiTx) txIn() []*TxIn   { return tx.TxIn }
func (tx *QiTx) txOut() []*TxOut { return tx.TxOut }

func (tx *QiTx) getEcdsaSignatureValues() (v, r, s *big.Int) {
	panic("Qi TX does not have ECDSA signature values")
}

func (tx *QiTx) setEcdsaSignatureValues(chainID, v, r, s *big.Int) {
	panic("Qi TX does not have ECDSA signature values")
}
