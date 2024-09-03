package types

import (
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
)

type QiTx struct {
	ChainID *big.Int // replay protection
	TxIn    TxIns    `json:"txIns"`
	TxOut   TxOuts   `json:"txOuts"`

	Signature *schnorr.Signature

	// Work fields
	ParentHash *common.Hash
	MixHash    *common.Hash
	WorkNonce  *BlockNonce
}

type WireQiTx struct {
	ChainID   *big.Int // replay protection
	TxIn      TxIns
	TxOut     TxOuts
	Signature []byte
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *QiTx) copy() TxData {
	cpy := &QiTx{
		ChainID:    new(big.Int),
		ParentHash: tx.ParentHash,
		MixHash:    tx.MixHash,
		WorkNonce:  tx.WorkNonce,
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
func (tx *QiTx) txType() byte                            { return QiTxType }
func (tx *QiTx) chainID() *big.Int                       { return tx.ChainID }
func (tx *QiTx) txIn() TxIns                             { return tx.TxIn }
func (tx *QiTx) txOut() TxOuts                           { return tx.TxOut }
func (tx *QiTx) getSchnorrSignature() *schnorr.Signature { return tx.Signature }
func (tx *QiTx) parentHash() *common.Hash                { return tx.ParentHash }
func (tx *QiTx) mixHash() *common.Hash                   { return tx.MixHash }
func (tx *QiTx) workNonce() *BlockNonce                  { return tx.WorkNonce }
func (tx *QiTx) accessList() AccessList                  { panic("Qi TX does not have accessList") }
func (tx *QiTx) data() []byte                            { panic("Qi TX does not have data") }
func (tx *QiTx) gas() uint64                             { panic("Qi TX does not have gas") }
func (tx *QiTx) minerTip() *big.Int                      { panic("Qi TX does not have minerTip") }
func (tx *QiTx) gasPrice() *big.Int                      { panic("Qi TX does not have gasPrice") }
func (tx *QiTx) value() *big.Int                         { panic("Qi TX does not have value") }
func (tx *QiTx) nonce() uint64                           { panic("Qi TX does not have nonce") }
func (tx *QiTx) to() *common.Address                     { panic("Qi TX does not have to") }
func (tx *QiTx) originatingTxHash() common.Hash {
	panic("Qi TX does not have originatingTxHash")
}
func (tx *QiTx) etxIndex() uint16 { panic("Qi TX does not have etxIndex") }
func (tx *QiTx) etxSender() common.Address {
	panic("Qi TX does not have etxSender")
}
func (tx *QiTx) etxType() uint64 { panic("Qi TX does not have etxType") }

func (tx *QiTx) getEcdsaSignatureValues() (v, r, s *big.Int) {
	panic("Qi TX does not have ECDSA signature values")
}

func (tx *QiTx) setEcdsaSignatureValues(chainID, v, r, s *big.Int) {
	panic("Qi TX does not have ECDSA signature values")
}

func (tx *QiTx) isCoinbase() bool {
	panic("qi TX does not have isCoinbase field")
}

// CalculateQiTxGas calculates the total amount of gas a Qi tx uses (for fee calculation)
func CalculateQiTxGas(transaction *Transaction, qiScalingFactor float64, location common.Location) uint64 {
	if transaction.Type() != QiTxType {
		panic("CalculateQiTxGas called on a transaction that is not a Qi transaction")
	}
	txGas := CalculateIntrinsicQiTxGas(transaction, qiScalingFactor)
	for _, output := range transaction.TxOut() {
		toAddr := common.AddressBytes(output.Address)
		if !location.Equal(*toAddr.Location()) {
			// This output creates an ETX
			txGas += params.ETXGas + params.TxGas
		} else if location.Equal(*toAddr.Location()) && toAddr.IsInQuaiLedgerScope() {
			// This output creates a conversion
			txGas += params.ETXGas + params.TxGas + params.ColdSloadCost(big.NewInt(0), big.NewInt(0)) + params.ColdSloadCost(big.NewInt(0), big.NewInt(0)) + params.SstoreSetGas(big.NewInt(0), big.NewInt(0)) + params.SstoreSetGas(big.NewInt(0), big.NewInt(0))
		}
	}
	return txGas
}

// CalculateBlockQiTxGas calculates the amount of gas a Qi tx uses in a block (for block gas limit calculation)
func CalculateBlockQiTxGas(transaction *Transaction, qiScalingFactor float64, location common.Location) uint64 {
	if transaction.Type() != QiTxType {
		panic("CalculateQiTxGas called on a transaction that is not a Qi transaction")
	}
	txGas := CalculateIntrinsicQiTxGas(transaction, qiScalingFactor)
	for _, output := range transaction.TxOut() {
		toAddr := common.AddressBytes(output.Address)
		if !location.Equal(*toAddr.Location()) {
			// This output creates an ETX
			txGas += params.ETXGas
		} else if location.Equal(*toAddr.Location()) && toAddr.IsInQuaiLedgerScope() {
			// This output creates a conversion
			txGas += params.ETXGas
		}
	}
	return txGas
}

// CalculateIntrinsicQiTxGas calculates the intrinsic gas for a Qi tx without ETXs
func CalculateIntrinsicQiTxGas(transaction *Transaction, scalingFactor float64) uint64 {
	if transaction.Type() != QiTxType {
		panic("CalculateIntrinsicQiTxGas called on a transaction that is not a Qi transaction")
	}
	baseRate := uint64(len(transaction.TxIn()))*params.SloadGas + uint64(len(transaction.TxOut()))*params.CallValueTransferGas + params.EcrecoverGas
	return params.CalculateQiGasWithUTXOSetSizeScalingFactor(scalingFactor, baseRate)
}

func (tx *QiTx) setTo(to common.Address) {
	panic("Cannot set To on a Qi transaction")
}
