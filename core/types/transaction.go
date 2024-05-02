// Copyright 2014 The go-ethereum Authors
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
	"bytes"
	"container/heap"
	"errors"
	"io"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/params"
	"google.golang.org/protobuf/proto"

	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/rlp"
)

var (
	ErrInvalidSig         = errors.New("invalid transaction v, r, s values")
	ErrInvalidSchnorrSig  = errors.New("invalid transaction scnhorr signature")
	ErrExpectedProtection = errors.New("transaction signature is not protected")
	ErrTxTypeNotSupported = errors.New("transaction type not supported")
	ErrGasFeeCapTooLow    = errors.New("fee cap less than base fee")
	errEmptyTypedTx       = errors.New("empty typed transaction bytes")
)

// Transaction types.
const (
	QuaiTxType = iota
	ExternalTxType
	QiTxType
)

// Transaction is a Quai transaction.
type Transaction struct {
	inner TxData    // Consensus contents of a transaction
	time  time.Time // Time first seen locally (spam avoidance)

	// caches
	hash       atomic.Value
	size       atomic.Value
	from       atomic.Value
	toChain    atomic.Value
	fromChain  atomic.Value
	confirmCtx atomic.Value // Context at which the ETX may be confirmed
	local      atomic.Value // Whether the transaction is local
}

// NewTx creates a new transaction.
func NewTx(inner TxData) *Transaction {
	tx := new(Transaction)
	tx.setDecoded(inner.copy(), 0)
	return tx
}

// SetInner sets the inner transaction data of a transaction.
func (tx *Transaction) SetInner(inner TxData) {
	tx.setDecoded(inner.copy(), 0)
}

// TxData is the underlying data of a transaction.
//
// This is implemented by QuaiTx, ExternalTx, InternalToExternal, and QiTx.
type TxData interface {
	txType() byte // returns the type ID
	copy() TxData // creates a deep copy and initializes all fields

	chainID() *big.Int
	accessList() AccessList
	data() []byte
	gas() uint64
	gasPrice() *big.Int
	gasTipCap() *big.Int
	gasFeeCap() *big.Int
	value() *big.Int
	nonce() uint64
	to() *common.Address
	etxSender() common.Address
	originatingTxHash() common.Hash
	etxIndex() uint16
	txIn() TxIns
	txOut() TxOuts
	getEcdsaSignatureValues() (v, r, s *big.Int)
	setEcdsaSignatureValues(chainID, v, r, s *big.Int)
	setTo(to common.Address)

	// Schnorr segregated sigs
	getSchnorrSignature() *schnorr.Signature
}

// ProtoEncode serializes tx into the Quai Proto Transaction format
func (tx *Transaction) ProtoEncode() (*ProtoTransaction, error) {
	protoTx := &ProtoTransaction{}
	if tx == nil {
		return protoTx, nil
	}
	// Encoding common fields to all the tx types
	txType := uint64(tx.Type())
	protoTx.Type = &txType

	// Other fields are set conditionally depending on tx type.
	switch tx.Type() {
	case 0:
		nonce := tx.Nonce()
		gas := tx.Gas()
		protoTx.Nonce = &nonce
		protoTx.Gas = &gas
		protoTx.AccessList = tx.AccessList().ProtoEncode()
		protoTx.Value = tx.Value().Bytes()
		if tx.Data() == nil {
			protoTx.Data = []byte{}
		} else {
			protoTx.Data = tx.Data()
		}
		if tx.To() != nil {
			protoTx.To = tx.To().Bytes()
		}
		protoTx.GasFeeCap = tx.GasFeeCap().Bytes()
		protoTx.GasTipCap = tx.GasTipCap().Bytes()
		V, R, S := tx.GetEcdsaSignatureValues()
		protoTx.V = V.Bytes()
		protoTx.R = R.Bytes()
		protoTx.S = S.Bytes()
		protoTx.ChainId = tx.ChainId().Bytes()
	case 1:
		gas := tx.Gas()
		protoTx.Gas = &gas
		protoTx.AccessList = tx.AccessList().ProtoEncode()
		protoTx.Value = tx.Value().Bytes()
		if tx.Data() == nil {
			protoTx.Data = []byte{}
		} else {
			protoTx.Data = tx.Data()
		}
		protoTx.To = tx.To().Bytes()
		protoTx.OriginatingTxHash = tx.OriginatingTxHash().ProtoEncode()
		etxIndex := uint32(tx.ETXIndex())
		protoTx.EtxIndex = &etxIndex
		protoTx.EtxSender = tx.ETXSender().Bytes()
	case 2:
		var err error
		protoTx.TxIns, err = tx.TxIn().ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoTx.TxOuts, err = tx.TxOut().ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoTx.Signature = tx.GetSchnorrSignature().Serialize()
		protoTx.ChainId = tx.ChainId().Bytes()
	}
	return protoTx, nil
}

// ProtoDecode deserializes the ProtoTransaction into the Transaction format
func (tx *Transaction) ProtoDecode(protoTx *ProtoTransaction, location common.Location) error {
	if protoTx.Type == nil {
		return errors.New("missing required field 'Type' in ProtoTransaction")
	}

	txType := protoTx.GetType()

	switch txType {
	case 0:
		if protoTx.Nonce == nil {
			return errors.New("missing required field 'Nonce' in ProtoTransaction")
		}
		if protoTx.Gas == nil {
			return errors.New("missing required field 'Gas' in ProtoTransaction")
		}
		if protoTx.AccessList == nil {
			return errors.New("missing required field 'AccessList' in ProtoTransaction")
		}
		if protoTx.Value == nil {
			return errors.New("missing required field 'Value' in ProtoTransaction")
		}
		if protoTx.GasFeeCap == nil {
			return errors.New("missing required field 'GasFeeCap' in ProtoTransaction")
		}
		if protoTx.GasTipCap == nil {
			return errors.New("missing required field 'GasTipCap' in ProtoTransaction")
		}
		if protoTx.Data == nil {
			return errors.New("missing required field 'Data' in ProtoTransaction")
		}
		if protoTx.ChainId == nil {
			return errors.New("missing required field 'ChainId' in ProtoTransaction")
		}
		var itx QuaiTx
		itx.AccessList = AccessList{}
		itx.AccessList.ProtoDecode(protoTx.GetAccessList(), location)
		if protoTx.To == nil {
			itx.To = nil
		} else {
			to := common.BytesToAddress(protoTx.GetTo(), location)
			itx.To = &to
		}
		itx.ChainID = new(big.Int).SetBytes(protoTx.GetChainId())
		itx.Nonce = protoTx.GetNonce()
		itx.GasTipCap = new(big.Int).SetBytes(protoTx.GetGasTipCap())
		itx.GasFeeCap = new(big.Int).SetBytes(protoTx.GetGasFeeCap())
		itx.Gas = protoTx.GetGas()
		if len(protoTx.GetValue()) == 0 {
			itx.Value = common.Big0
		} else {
			itx.Value = new(big.Int).SetBytes(protoTx.GetValue())
		}
		itx.Data = protoTx.GetData()
		if protoTx.V == nil {
			return errors.New("missing required field 'V' in QuaiTx")
		}
		itx.V = new(big.Int).SetBytes(protoTx.GetV())
		if protoTx.R == nil {
			return errors.New("missing required field 'R' in QuaiTx")
		}
		itx.R = new(big.Int).SetBytes(protoTx.GetR())
		if protoTx.S == nil {
			return errors.New("missing required field 'S' in QuaiTx")
		}
		itx.S = new(big.Int).SetBytes(protoTx.GetS())
		withSignature := itx.V.Sign() != 0 || itx.R.Sign() != 0 || itx.S.Sign() != 0
		if withSignature {
			if err := sanityCheckSignature(itx.V, itx.R, itx.S); err != nil {
				return err
			}
		}
		tx.SetInner(&itx)

	case 1:
		if protoTx.Gas == nil {
			return errors.New("missing required field 'Gas' in ProtoTransaction")
		}
		if protoTx.AccessList == nil {
			return errors.New("missing required field 'AccessList' in ProtoTransaction")
		}
		if protoTx.Value == nil {
			return errors.New("missing required field 'Value' in ProtoTransaction")
		}
		if protoTx.Data == nil {
			return errors.New("missing required field 'Data' in ProtoTransaction")
		}
		if protoTx.To == nil {
			return errors.New("missing required field 'To' in ProtoTransaction")
		}
		if protoTx.OriginatingTxHash == nil {
			return errors.New("missing required field 'OriginatingTxHash' in ProtoTransaction")
		}
		if protoTx.EtxIndex == nil {
			return errors.New("missing required field 'EtxIndex' in ProtoTransaction")
		}

		var etx ExternalTx
		etx.AccessList = AccessList{}
		etx.AccessList.ProtoDecode(protoTx.GetAccessList(), location)
		to := common.BytesToAddress(protoTx.GetTo(), location)
		etx.To = &to

		etx.Gas = protoTx.GetGas()
		etx.Data = protoTx.GetData()
		etx.Value = new(big.Int).SetBytes(protoTx.GetValue())
		etx.OriginatingTxHash = common.BytesToHash(protoTx.GetOriginatingTxHash().Value)
		etx.ETXIndex = uint16(protoTx.GetEtxIndex())
		etx.Sender = common.BytesToAddress(protoTx.GetEtxSender(), location)

		tx.SetInner(&etx)

	case 2:
		if protoTx.TxIns == nil {
			return errors.New("missing required field 'TxIns' in ProtoTransaction")
		}
		if protoTx.TxOuts == nil {
			return errors.New("missing required field 'TxOuts' in ProtoTransaction")
		}
		if protoTx.Signature == nil {
			return errors.New("missing required field 'Signature' in ProtoTransaction")
		}
		if protoTx.ChainId == nil {
			return errors.New("missing required field 'ChainId' in ProtoTransaction")
		}
		var qiTx QiTx
		qiTx.ChainID = new(big.Int).SetBytes(protoTx.GetChainId())

		var err error
		qiTx.TxIn = TxIns{}
		err = qiTx.TxIn.ProtoDecode(protoTx.GetTxIns())
		if err != nil {
			return err
		}
		qiTx.TxOut = TxOuts{}
		err = qiTx.TxOut.ProtoDecode(protoTx.GetTxOuts())
		if err != nil {
			return err
		}
		sig, err := schnorr.ParseSignature(protoTx.GetSignature())
		if err != nil {
			return err
		}
		qiTx.Signature = sig

		tx.SetInner(&qiTx)
	default:
		return errors.New("invalid transaction type")
	}
	tx.time = time.Now()
	return nil
}

func (tx *Transaction) ProtoEncodeTxSigningData() *ProtoTransaction {
	protoTxSigningData := &ProtoTransaction{}
	if tx == nil {
		return protoTxSigningData
	}
	switch tx.Type() {
	case 0:
		txType := uint64(tx.Type())
		protoTxSigningData.Type = &txType
		protoTxSigningData.ChainId = tx.ChainId().Bytes()
		nonce := tx.Nonce()
		gas := tx.Gas()
		protoTxSigningData.Nonce = &nonce
		protoTxSigningData.Gas = &gas
		protoTxSigningData.AccessList = tx.AccessList().ProtoEncode()
		protoTxSigningData.Value = tx.Value().Bytes()
		if tx.Data() == nil {
			protoTxSigningData.Data = []byte{}
		} else {
			protoTxSigningData.Data = tx.Data()
		}
		if tx.To() == nil {
			protoTxSigningData.To = []byte{}
		} else {
			protoTxSigningData.To = tx.To().Bytes()
		}
		protoTxSigningData.GasFeeCap = tx.GasFeeCap().Bytes()
		protoTxSigningData.GasTipCap = tx.GasTipCap().Bytes()
	case 1:
		return protoTxSigningData
	case 2:
		txType := uint64(tx.Type())
		protoTxSigningData.Type = &txType
		protoTxSigningData.ChainId = tx.ChainId().Bytes()
		protoTxSigningData.TxIns, _ = tx.TxIn().ProtoEncode()
		protoTxSigningData.TxOuts, _ = tx.TxOut().ProtoEncode()
	}
	return protoTxSigningData
}

// EncodeRLP implements rlp.Encoder
func (tx *Transaction) EncodeRLP(w io.Writer) error {
	buf := encodeBufferPool.Get().(*bytes.Buffer)
	defer encodeBufferPool.Put(buf)
	buf.Reset()
	if err := tx.encodeTyped(buf); err != nil {
		return err
	}
	return rlp.Encode(w, buf.Bytes())
}

// encodeTyped writes the canonical encoding of a typed transaction to w.
func (tx *Transaction) encodeTyped(w *bytes.Buffer) error {
	w.WriteByte(tx.Type())
	if tx.Type() == QiTxType {
		// custom encode for schnorr signature
		if qiTx, ok := tx.inner.(*QiTx); ok {
			return rlp.Encode(w, qiTx.copyToWire())
		} else {
			return errors.New("failed to encode utxo tx: improper type")
		}
	}
	return rlp.Encode(w, tx.inner)
}

// MarshalBinary returns the canonical encoding of the transaction.
func (tx *Transaction) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	err := tx.encodeTyped(&buf)
	return buf.Bytes(), err
}

// DecodeRLP implements rlp.Decoder
func (tx *Transaction) DecodeRLP(s *rlp.Stream) error {
	kind, _, err := s.Kind()
	if err != nil {
		return err
	}
	if kind == rlp.String {
		var b []byte
		if b, err = s.Bytes(); err != nil {
			return err
		}
		inner, err := tx.decodeTyped(b)
		if err == nil {
			tx.setDecoded(inner, len(b))
		}
		return err
	} else {
		return ErrTxTypeNotSupported
	}
}

// UnmarshalBinary decodes the canonical encoding of transactions.
func (tx *Transaction) UnmarshalBinary(b []byte) error {
	inner, err := tx.decodeTyped(b)
	if err != nil {
		return err
	}
	tx.setDecoded(inner, len(b))
	return nil
}

// decodeTyped decodes a typed transaction from the canonical format.
func (tx *Transaction) decodeTyped(b []byte) (TxData, error) {
	if len(b) == 0 {
		return nil, errEmptyTypedTx
	}
	switch b[0] {
	case QuaiTxType:
		var inner QuaiTx
		err := rlp.DecodeBytes(b[1:], &inner)
		return &inner, err
	case ExternalTxType:
		var inner ExternalTx
		err := rlp.DecodeBytes(b[1:], &inner)
		return &inner, err
	case QiTxType:
		var wire WireQiTx
		err := rlp.DecodeBytes(b[1:], &wire)
		inner := wire.copyFromWire()
		return inner, err
	default:
		return nil, ErrTxTypeNotSupported
	}
}

// setDecoded sets the inner transaction and size after decoding.
func (tx *Transaction) setDecoded(inner TxData, size int) {
	tx.inner = inner
	tx.time = time.Now()
	if size > 0 {
		tx.size.Store(common.StorageSize(size))
	}
}

func sanityCheckSignature(v *big.Int, r *big.Int, s *big.Int) error {
	if !crypto.ValidateSignatureValues(byte(v.Uint64()), r, s) {
		return ErrInvalidSig
	}
	return nil
}

// Type returns the transaction type.
func (tx *Transaction) Type() uint8 {
	return tx.inner.txType()
}

// ChainId returns the chain ID of the transaction. The return value will always be
// non-nil.
func (tx *Transaction) ChainId() *big.Int {
	return tx.inner.chainID()
}

// Data returns the input data of the transaction.
func (tx *Transaction) Data() []byte { return tx.inner.data() }

// AccessList returns the access list of the transaction.
func (tx *Transaction) AccessList() AccessList { return tx.inner.accessList() }

// Gas returns the gas limit of the transaction.
func (tx *Transaction) Gas() uint64 { return tx.inner.gas() }

// GasPrice returns the gas price of the transaction.
func (tx *Transaction) GasPrice() *big.Int { return new(big.Int).Set(tx.inner.gasPrice()) }

// GasTipCap returns the gasTipCap per gas of the transaction.
func (tx *Transaction) GasTipCap() *big.Int { return new(big.Int).Set(tx.inner.gasTipCap()) }

// GasFeeCap returns the fee cap per gas of the transaction.
func (tx *Transaction) GasFeeCap() *big.Int { return new(big.Int).Set(tx.inner.gasFeeCap()) }

// Value returns the ether amount of the transaction.
func (tx *Transaction) Value() *big.Int { return new(big.Int).Set(tx.inner.value()) }

// Nonce returns the sender account nonce of the transaction.
func (tx *Transaction) Nonce() uint64                  { return tx.inner.nonce() }
func (tx *Transaction) ETXSender() common.Address      { return tx.inner.etxSender() }
func (tx *Transaction) OriginatingTxHash() common.Hash { return tx.inner.originatingTxHash() }

func (tx *Transaction) ETXIndex() uint16 { return tx.inner.etxIndex() }

func (tx *Transaction) TxOut() TxOuts { return tx.inner.txOut() }

func (tx *Transaction) TxIn() TxIns { return tx.inner.txIn() }

func (tx *Transaction) GetSchnorrSignature() *schnorr.Signature {
	return tx.inner.getSchnorrSignature()
}

func (tx *Transaction) From(nodeLocation common.Location) *common.Address {
	sc := tx.from.Load()
	if sc != nil {
		sigCache := sc.(sigCache)
		addr := common.Bytes20ToAddress(sigCache.from, nodeLocation)
		return &addr
	} else {
		return nil
	}
}

// To returns the recipient address of the transaction.
// For contract-creation transactions, To returns nil.
func (tx *Transaction) To() *common.Address {
	// Copy the pointed-to address.
	ito := tx.inner.to()
	if ito == nil {
		return nil
	}
	cpy := *ito
	return &cpy
}

func (tx *Transaction) SetGas(addr common.Address) {
	tx.inner.setTo(addr)
}

// Cost returns gas * gasPrice + value.
func (tx *Transaction) Cost() *big.Int {
	total := new(big.Int).Mul(tx.GasPrice(), new(big.Int).SetUint64(tx.Gas()))
	total.Add(total, tx.Value())
	return total
}

// GetEcdsaSignatureValues returns the V, R, S signature values of the transaction.
// The return values should not be modified by the caller.
func (tx *Transaction) GetEcdsaSignatureValues() (v, r, s *big.Int) {
	return tx.inner.getEcdsaSignatureValues()
}

// GasFeeCapCmp compares the fee cap of two transactions.
func (tx *Transaction) GasFeeCapCmp(other *Transaction) int {
	return tx.inner.gasFeeCap().Cmp(other.inner.gasFeeCap())
}

// GasFeeCapIntCmp compares the fee cap of the transaction against the given fee cap.
func (tx *Transaction) GasFeeCapIntCmp(other *big.Int) int {
	return tx.inner.gasFeeCap().Cmp(other)
}

// GasTipCapCmp compares the gasTipCap of two transactions.
func (tx *Transaction) GasTipCapCmp(other *Transaction) int {
	return tx.inner.gasTipCap().Cmp(other.inner.gasTipCap())
}

// GasTipCapIntCmp compares the gasTipCap of the transaction against the given gasTipCap.
func (tx *Transaction) GasTipCapIntCmp(other *big.Int) int {
	return tx.inner.gasTipCap().Cmp(other)
}

// EffectiveGasTip returns the effective miner gasTipCap for the given base fee.
// Note: if the effective gasTipCap is negative, this method returns both error
// the actual negative value, _and_ ErrGasFeeCapTooLow
func (tx *Transaction) EffectiveGasTip(baseFee *big.Int) (*big.Int, error) {
	if baseFee == nil {
		return tx.GasTipCap(), nil
	}
	var err error
	gasFeeCap := tx.GasFeeCap()
	if gasFeeCap.Cmp(baseFee) == -1 {
		err = ErrGasFeeCapTooLow
	}
	return math.BigMin(tx.GasTipCap(), gasFeeCap.Sub(gasFeeCap, baseFee)), err
}

// EffectiveGasTipValue is identical to EffectiveGasTip, but does not return an
// error in case the effective gasTipCap is negative
func (tx *Transaction) EffectiveGasTipValue(baseFee *big.Int) *big.Int {
	effectiveTip, _ := tx.EffectiveGasTip(baseFee)
	return effectiveTip
}

// EffectiveGasTipCmp compares the effective gasTipCap of two transactions assuming the given base fee.
func (tx *Transaction) EffectiveGasTipCmp(other *Transaction, baseFee *big.Int) int {
	if baseFee == nil {
		return tx.GasTipCapCmp(other)
	}
	return tx.EffectiveGasTipValue(baseFee).Cmp(other.EffectiveGasTipValue(baseFee))
}

// EffectiveGasTipIntCmp compares the effective gasTipCap of a transaction to the given gasTipCap.
func (tx *Transaction) EffectiveGasTipIntCmp(other *big.Int, baseFee *big.Int) int {
	if baseFee == nil {
		return tx.GasTipCapIntCmp(other)
	}
	return tx.EffectiveGasTipValue(baseFee).Cmp(other)
}

func (tx *Transaction) IsLocal() bool {
	if local := tx.local.Load(); local != nil {
		return local.(bool)
	}
	return false
}

func (tx *Transaction) SetLocal(local bool) {
	tx.local.Store(local)
}

// Hash returns the transaction hash.
func (tx *Transaction) Hash(location ...byte) (h common.Hash) {
	if hash := tx.hash.Load(); hash != nil {
		return hash.(common.Hash)
	}
	protoTx, _ := tx.ProtoEncode()
	data, _ := proto.Marshal(protoTx)
	h = crypto.Keccak256Hash(data)
	switch tx.Type() {
	case QuaiTxType:
		if len(location) == 2 {
			origin := (uint8(location[0]) * 16) + uint8(location[1])
			h[0] = origin
			h[1] &= 0x7F // 01111111 in binary (set first bit to 0)
			h[2] = origin
			h[3] &= 0x7F
		} else {
			from, err := Sender(NewSigner(tx.ChainId(), common.Location{0, 0}), tx) // location not important when performing ecrecover
			if err != nil {
				return h // Caller of this function will fail with wrong tx hash and will appropriately handle the error
			}
			location := *from.Location()
			origin := (uint8(location[0]) * 16) + uint8(location[1])
			h[0] = origin
			h[1] &= 0x7F
			h[2] = origin
			h[3] &= 0x7F
		}
	case ExternalTxType:
		origin := tx.OriginatingTxHash().Bytes()[2] // destination of the originating tx
		destLoc := *tx.To().Location()
		destination := (uint8(destLoc[0]) * 16) + uint8(destLoc[1])
		h[0] = origin
		if tx.ETXSender().IsInQiLedgerScope() {
			h[1] |= 0x80 // 10000000 in binary (set first bit to 1)
		} else {
			h[1] &= 0x7F // 01111111 in binary (set first bit to 0)
		}
		h[2] = destination
		if tx.To().IsInQiLedgerScope() {
			h[3] |= 0x80
		} else {
			h[3] &= 0x7F
		}
	case QiTxType:
		// the origin of this tx is the *destination* of the utxos being spent
		origin := tx.TxIn()[0].PreviousOutPoint.TxHash[1]
		h[0] = origin
		h[1] |= 0x80 // 10000000 in binary (set first bit to 1)
		h[2] = origin
		h[3] |= 0x80
	}
	tx.hash.Store(h)
	return h
}

// FromChain returns the chain location this transaction originated from
func (tx *Transaction) FromChain(nodeLocation common.Location) common.Location {
	if loc := tx.fromChain.Load(); loc != nil {
		return loc.(common.Location)
	}
	var loc common.Location
	switch tx.Type() {
	case ExternalTxType:
		// External transactions do not have a signature, but instead store the
		// sender explicitly. Use that sender to get the location.
		loc = *tx.inner.(*ExternalTx).Sender.Location()
	default:
		// All other TX types are signed, and should use the signature to determine
		// the sender location
		signer := NewSigner(tx.ChainId(), nodeLocation)
		from, err := Sender(signer, tx)
		if err != nil {
			panic("failed to get transaction sender!")
		}
		loc = *from.Location()
	}
	tx.fromChain.Store(loc)
	return loc
}

// ConfirmationCtx indicates the chain context at which this ETX becomes
// confirmed and referencable to the destination chain
func (tx *Transaction) ConfirmationCtx(nodeLocation common.Location) int {
	if ctx := tx.confirmCtx.Load(); ctx != nil {
		return ctx.(int)
	}
	if tx.ETXSender().Location().Equal(*tx.To().Location()) {
		// If the ETX sender and the destination chain are the same, the ETX is a conversion tx
		return params.ConversionConfirmationContext
	}

	ctx := tx.To().Location().CommonDom(tx.FromChain(nodeLocation)).Context()
	tx.confirmCtx.Store(ctx)
	return ctx
}

// Size returns the true RLP encoded storage size of the transaction, either by
// encoding and returning it, or returning a previously cached value.
func (tx *Transaction) Size() common.StorageSize {
	if size := tx.size.Load(); size != nil {
		return size.(common.StorageSize)
	}
	c := writeCounter(0)
	rlp.Encode(&c, &tx.inner)
	tx.size.Store(common.StorageSize(c))
	return common.StorageSize(c)
}

// WithSignature returns a new transaction with the given signature.
// This signature needs to be in the [R || S || V] format where V is 0 or 1.
func (tx *Transaction) WithSignature(signer Signer, sig []byte) (*Transaction, error) {
	r, s, v, err := signer.SignatureValues(tx, sig)
	if err != nil {
		return nil, err
	}
	cpy := tx.inner.copy()
	cpy.setEcdsaSignatureValues(signer.ChainID(), v, r, s)
	return &Transaction{inner: cpy, time: tx.time}, nil
}

// Transactions implements DerivableList for transactions.
type Transactions []*Transaction

// Len returns the length of s.
func (s Transactions) Len() int { return len(s) }

// EncodeIndex encodes the i'th transaction to w. Note that this does not check for errors
// because we assume that *Transaction will only ever contain valid txs that were either
// constructed by decoding or via public API in this package.
func (s Transactions) EncodeIndex(i int, w *bytes.Buffer) {
	tx := s[i]
	tx.encodeTyped(w)
}

// ProtoEncode encodes the transactions to the ProtoTransactions format
func (s Transactions) ProtoEncode() (*ProtoTransactions, error) {
	protoTxs := &ProtoTransactions{}
	protoTxs.Transactions = make([]*ProtoTransaction, len(s))
	for i, tx := range s {
		protoTx, err := tx.ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoTxs.Transactions[i] = protoTx
	}
	return protoTxs, nil
}

// ProtoDecode decodes the ProtoTransactions into the Transactions format
func (s *Transactions) ProtoDecode(transactions *ProtoTransactions, location common.Location) error {
	for _, protoTx := range transactions.Transactions {
		tx := &Transaction{}
		err := tx.ProtoDecode(protoTx, location)
		if err != nil {
			return err
		}
		*s = append(*s, tx)
	}
	return nil
}

// FilterByLocation returns the subset of transactions with a 'to' address which
// belongs the given chain location
func (s Transactions) FilterToLocation(l common.Location) Transactions {
	filteredList := Transactions{}
	for _, tx := range s {
		toChain := *tx.To().Location()
		if l.Equal(toChain) {
			filteredList = append(filteredList, tx)
		}
	}
	return filteredList
}

// FilterToSlice returns the subset of transactions with a 'to' address which
// belongs to the given slice location, at or above the given minimum context
func (s Transactions) FilterToSlice(slice common.Location, minCtx int) Transactions {
	filteredList := Transactions{}
	for _, tx := range s {
		toChain := tx.To().Location()
		if toChain.InSameSliceAs(slice) {
			filteredList = append(filteredList, tx)
		}
	}
	return filteredList
}

// FilterConfirmationCtx returns the subset of transactions who can be confirmed
// at the given context
func (s Transactions) FilterConfirmationCtx(ctx int, nodeLocation common.Location) Transactions {
	filteredList := Transactions{}
	for _, tx := range s {
		if tx.ConfirmationCtx(nodeLocation) == ctx {
			filteredList = append(filteredList, tx)
		}
	}
	return filteredList
}

// TxDifference returns a new set which is the difference between a and b.
func TxDifference(a, b Transactions) Transactions {
	keep := make(Transactions, 0, len(a))

	remove := make(map[common.Hash]struct{})
	for _, tx := range b {
		remove[tx.Hash()] = struct{}{}
	}

	for _, tx := range a {
		if _, ok := remove[tx.Hash()]; !ok {
			keep = append(keep, tx)
		}
	}

	return keep
}

// TxByNonce implements the sort interface to allow sorting a list of transactions
// by their nonces. This is usually only useful for sorting transactions from a
// single account, otherwise a nonce comparison doesn't make much sense.
type TxByNonce Transactions

func (s TxByNonce) Len() int           { return len(s) }
func (s TxByNonce) Less(i, j int) bool { return s[i].Nonce() < s[j].Nonce() }
func (s TxByNonce) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// TxWithMinerFee wraps a transaction with its gas price or effective miner gasTipCap
type TxWithMinerFee struct {
	tx       *Transaction
	minerFee *big.Int
}

// NewTxWithMinerFee creates a wrapped transaction, calculating the effective
// miner gasTipCap if a base fee is provided.
// Returns error in case of a negative effective miner gasTipCap.
func NewTxWithMinerFee(tx *Transaction, baseFee *big.Int, qiTxFee *big.Int) (*TxWithMinerFee, error) {
	if tx.Type() == QiTxType {
		return &TxWithMinerFee{
			tx:       tx,
			minerFee: qiTxFee,
		}, nil
	}
	minerFee, err := tx.EffectiveGasTip(baseFee)
	if err != nil {
		return nil, err
	}
	return &TxWithMinerFee{
		tx:       tx,
		minerFee: minerFee,
	}, nil
}

// TxByPriceAndTime implements both the sort and the heap interface, making it useful
// for all at once sorting as well as individually adding and removing elements.
type TxByPriceAndTime []*TxWithMinerFee

func (s TxByPriceAndTime) Len() int { return len(s) }
func (s TxByPriceAndTime) Less(i, j int) bool {
	// If the prices are equal, use the time the transaction was first seen for
	// deterministic sorting
	cmp := s[i].minerFee.Cmp(s[j].minerFee)
	if cmp == 0 {
		return s[i].tx.time.Before(s[j].tx.time)
	}
	return cmp > 0
}
func (s TxByPriceAndTime) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s *TxByPriceAndTime) Push(x interface{}) {
	*s = append(*s, x.(*TxWithMinerFee))
}

func (s *TxByPriceAndTime) Pop() interface{} {
	old := *s
	n := len(old)
	x := old[n-1]
	*s = old[0 : n-1]
	return x
}

// TransactionsByPriceAndNonce represents a set of transactions that can return
// transactions in a profit-maximizing sorted order, while supporting removing
// entire batches of transactions for non-executable accounts.
type TransactionsByPriceAndNonce struct {
	txs     map[common.AddressBytes]Transactions // Per account nonce-sorted list of transactions
	heads   TxByPriceAndTime                     // Next transaction for each unique account (price heap)
	signer  Signer                               // Signer for the set of transactions
	baseFee *big.Int                             // Current base fee
}

// NewTransactionsByPriceAndNonce creates a transaction set that can retrieve
// price sorted transactions in a nonce-honouring way.
//
// Note, the input map is reowned so the caller should not interact any more with
// if after providing it to the constructor.
func NewTransactionsByPriceAndNonce(signer Signer, qiTxs map[common.Hash]*TxWithMinerFee, txs map[common.AddressBytes]Transactions, baseFee *big.Int, sort bool) *TransactionsByPriceAndNonce {
	// Initialize a price and received time based heap with the head transactions
	heads := make(TxByPriceAndTime, 0, len(txs))

	for from, accTxs := range txs {
		acc, err := Sender(signer, accTxs[0])
		if err != nil {
			continue
		}
		wrapped, err := NewTxWithMinerFee(accTxs[0], baseFee, nil)
		// Remove transaction if sender doesn't match from, or if wrapping fails.
		if acc.Bytes20() != from || err != nil {
			delete(txs, from)
			continue
		}
		heads = append(heads, wrapped)
		txs[from] = accTxs[1:]
	}
	for _, qiTx := range qiTxs {
		heads = append(heads, qiTx)
	}
	if sort {
		heap.Init(&heads)
	}

	// Assemble and return the transaction set
	return &TransactionsByPriceAndNonce{
		txs:     txs,
		heads:   heads,
		signer:  signer,
		baseFee: baseFee,
	}
}

// Peek returns the next transaction by price.
func (t *TransactionsByPriceAndNonce) Peek() *Transaction {
	if len(t.heads) == 0 {
		return nil
	}
	return t.heads[0].tx
}

func (t *TransactionsByPriceAndNonce) GetFee() *big.Int {
	if len(t.heads) == 0 {
		return nil
	}
	return t.heads[0].minerFee
}

// Shift replaces the current best head with the next one from the same account.
func (t *TransactionsByPriceAndNonce) Shift(acc common.AddressBytes, sort bool) {
	if txs, ok := t.txs[acc]; ok && len(txs) > 0 {
		if wrapped, err := NewTxWithMinerFee(txs[0], t.baseFee, nil); err == nil {
			t.heads[0], t.txs[acc] = wrapped, txs[1:]
			if sort {
				heap.Fix(&t.heads, 0)
			}
			return
		}
	}
	if sort {
		heap.Pop(&t.heads)
	} else if len(t.heads) > 1 {
		t.heads = t.heads[1:]
	} else {
		t.heads = make(TxByPriceAndTime, 0)
	}

}

// Pop the first transaction without sorting
func (t *TransactionsByPriceAndNonce) PopNoSort() {
	if len(t.heads) > 1 {
		t.heads = t.heads[1:]
	} else {
		t.heads = make(TxByPriceAndTime, 0)
	}
}

// Pop removes the best transaction, *not* replacing it with the next one from
// the same account. This should be used when a transaction cannot be executed
// and hence all subsequent ones should be discarded from the same account.
func (t *TransactionsByPriceAndNonce) Pop() {
	heap.Pop(&t.heads)
}

// Message is a fully derived transaction and implements core.Message
//
// NOTE: In a future PR this will be removed.
type Message struct {
	to         *common.Address
	from       common.Address
	nonce      uint64
	amount     *big.Int
	gasLimit   uint64
	gasPrice   *big.Int
	gasFeeCap  *big.Int
	gasTipCap  *big.Int
	data       []byte
	accessList AccessList
	isETX      bool
	etxsender  common.Address // only used in ETX
	txtype     byte
	hash       common.Hash
	lock       *big.Int
}

func NewMessage(from common.Address, to *common.Address, nonce uint64, amount *big.Int, gasLimit uint64, gasPrice, gasFeeCap, gasTipCap *big.Int, data []byte, accessList AccessList, isETX bool) Message {
	return Message{
		from:       from,
		to:         to,
		nonce:      nonce,
		amount:     amount,
		gasLimit:   gasLimit,
		gasPrice:   gasPrice,
		gasFeeCap:  gasFeeCap,
		gasTipCap:  gasTipCap,
		data:       data,
		accessList: accessList,
		isETX:      isETX,
		hash:       common.Hash{},
	}
}

// AsMessage returns the transaction as a core.Message.
func (tx *Transaction) AsMessage(s Signer, baseFee *big.Int) (Message, error) {
	msg := Message{
		gasLimit:   tx.Gas(),
		gasPrice:   new(big.Int).Set(tx.GasPrice()),
		gasFeeCap:  new(big.Int).Set(tx.GasFeeCap()),
		gasTipCap:  new(big.Int).Set(tx.GasTipCap()),
		to:         tx.To(),
		amount:     tx.Value(),
		data:       tx.Data(),
		accessList: tx.AccessList(),
		isETX:      false,
		txtype:     tx.Type(),
		hash:       tx.Hash(),
	}
	// If baseFee provided, set gasPrice to effectiveGasPrice.
	if baseFee != nil {
		msg.gasPrice = math.BigMin(msg.gasPrice.Add(msg.gasTipCap, baseFee), msg.gasFeeCap)
	}
	var err error
	if tx.Type() == ExternalTxType {
		msg.from = common.ZeroAddress(s.Location())
		msg.etxsender, err = Sender(s, tx)
		msg.isETX = true
	} else {
		msg.from, err = Sender(s, tx)
		msg.nonce = tx.Nonce()
	}
	return msg, err
}

// AsMessageWithSender returns the transaction as a core.Message.
func (tx *Transaction) AsMessageWithSender(s Signer, baseFee *big.Int, sender *common.InternalAddress) (Message, error) {
	msg := Message{
		gasLimit:   tx.Gas(),
		gasPrice:   new(big.Int).Set(tx.GasPrice()),
		gasFeeCap:  new(big.Int).Set(tx.GasFeeCap()),
		gasTipCap:  new(big.Int).Set(tx.GasTipCap()),
		to:         tx.To(),
		amount:     tx.Value(),
		data:       tx.Data(),
		accessList: tx.AccessList(),
		isETX:      false,
		txtype:     tx.Type(),
		hash:       tx.Hash(),
		lock:       new(big.Int),
	}
	// If baseFee provided, set gasPrice to effectiveGasPrice.
	if baseFee != nil {
		msg.gasPrice = math.BigMin(msg.gasPrice.Add(msg.gasTipCap, baseFee), msg.gasFeeCap)
	}
	var err error
	if tx.Type() == ExternalTxType {
		msg.from = common.ZeroAddress(s.Location())
		msg.etxsender, err = Sender(s, tx)
		msg.isETX = true
	} else {
		if sender != nil {
			msg.from = common.NewAddressFromData(sender)
		} else {
			msg.from, err = Sender(s, tx)
		}
		msg.nonce = tx.Nonce()
	}
	return msg, err
}

func (m Message) From() common.Address      { return m.from }
func (m Message) To() *common.Address       { return m.to }
func (m Message) GasPrice() *big.Int        { return m.gasPrice }
func (m Message) GasFeeCap() *big.Int       { return m.gasFeeCap }
func (m Message) GasTipCap() *big.Int       { return m.gasTipCap }
func (m Message) Value() *big.Int           { return m.amount }
func (m Message) Gas() uint64               { return m.gasLimit }
func (m Message) Nonce() uint64             { return m.nonce }
func (m Message) Data() []byte              { return m.data }
func (m Message) AccessList() AccessList    { return m.accessList }
func (m Message) IsETX() bool               { return m.isETX }
func (m Message) ETXSender() common.Address { return m.etxsender }
func (m Message) Type() byte                { return m.txtype }
func (m Message) Hash() common.Hash         { return m.hash }
func (m Message) Lock() *big.Int            { return m.lock }

func (m *Message) SetValue(v *big.Int) {
	m.amount = v
}

func (m *Message) SetLock(lock *big.Int) {
	m.lock = lock
}

func (m *Message) SetData(data []byte) {
	m.data = data
}

// AccessList is an access list.
type AccessList []AccessTuple

// AccessTuple is the element type of an access list.
type AccessTuple struct {
	Address     common.Address `json:"address"        gencodec:"required"`
	StorageKeys []common.Hash  `json:"storageKeys"    gencodec:"required"`
}

// StorageKeys returns the total number of storage keys in the access list.
func (al AccessList) StorageKeys() int {
	sum := 0
	for _, tuple := range al {
		sum += len(tuple.StorageKeys)
	}
	return sum
}

// ProtoEncode serializes al into the Quai Proto AccessList format
func (al AccessList) ProtoEncode() *ProtoAccessList {
	protoAccessList := &ProtoAccessList{}
	protoAccessList.AccessTuples = make([]*ProtoAccessTuple, len(al))
	for i, tuple := range al {
		storageKeys := make([]*common.ProtoHash, len(tuple.StorageKeys))
		for j, key := range tuple.StorageKeys {
			storageKeys[j] = key.ProtoEncode()
		}
		protoAccessList.AccessTuples[i] = &ProtoAccessTuple{
			Address:    tuple.Address.Bytes(),
			StorageKey: storageKeys,
		}
	}
	return protoAccessList
}

// ProtoDecode deserializes the ProtoAccessList into the AccessList format
func (al *AccessList) ProtoDecode(protoAccessList *ProtoAccessList, location common.Location) error {
	for _, protoTuple := range protoAccessList.AccessTuples {
		address := common.BytesToAddress(protoTuple.GetAddress(), location)
		storageKeys := make([]common.Hash, len(protoTuple.StorageKey))
		for i, key := range protoTuple.StorageKey {
			storageKeys[i].ProtoDecode(key)
		}
		*al = append(*al, AccessTuple{Address: address, StorageKeys: storageKeys})
	}
	return nil
}

// This function must only be used by tests
func GetInnerForTesting(tx *Transaction) TxData {
	return tx.inner
}
