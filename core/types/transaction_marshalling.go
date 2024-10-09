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
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
)

// txJSON is the JSON representation of transactions.
type txJSON struct {
	Type hexutil.Uint64 `json:"type"`

	// Common transaction fields:
	Nonce         *hexutil.Uint64          `json:"nonce"`
	GasPrice      *hexutil.Big             `json:"gasPrice"`
	MinerTip      *hexutil.Big             `json:"minerTip"`
	Gas           *hexutil.Uint64          `json:"gas"`
	Value         *hexutil.Big             `json:"value"`
	Data          *hexutil.Bytes           `json:"input"`
	To            *common.MixedcaseAddress `json:"to"`
	AccessList    *AccessList              `json:"accessList"`
	TxIn          []TxInJSON               `json:"inputs,omitempty"`
	TxOut         []TxOutJSON              `json:"outputs,omitempty"`
	UTXOSignature *hexutil.Bytes           `json:"utxoSignature,omitempty"`
	EtxType       *hexutil.Uint64          `json:"etxType,omitempty"`
	// Optional fields only present for internal transactions
	ChainID *hexutil.Big `json:"chainId,omitempty"`
	V       *hexutil.Big `json:"v,omitempty"`
	R       *hexutil.Big `json:"r,omitempty"`
	S       *hexutil.Big `json:"s,omitempty"`

	// Optional fields only present for external transactions
	ETXSender         *common.MixedcaseAddress `json:"from,omitempty"`
	OriginatingTxHash *common.Hash             `json:"originatingTxHash,omitempty"`
	ETXIndex          *hexutil.Uint64          `json:"etxIndex,omitempty"`

	// Only used for encoding:
	Hash common.Hash `json:"hash"`
}

type TxOutJSON struct {
	Address      *hexutil.Bytes  `json:"address"`
	Denomination *hexutil.Uint64 `json:"denomination"`
	Lock         *hexutil.Big    `json:"lock,omitempty"`
}
type OutpointJSON struct {
	TxHash common.Hash    `json:"txHash"`
	Index  hexutil.Uint64 `json:"index"`
}
type TxInJSON struct {
	PreviousOutPoint *OutpointJSON  `json:"previousOutPoint"`
	PubKey           *hexutil.Bytes `json:"pubkey"`
}

// MarshalJSON marshals as JSON with a hash.
func (t *Transaction) MarshalJSON() ([]byte, error) {
	var enc txJSON
	// These are set for all tx types.
	enc.Hash = t.Hash()
	enc.Type = hexutil.Uint64(t.Type())

	// Other fields are set conditionally depending on tx type.
	switch tx := t.inner.(type) {
	case *QuaiTx:
		enc.ChainID = (*hexutil.Big)(tx.ChainID)
		enc.AccessList = &tx.AccessList
		enc.Nonce = (*hexutil.Uint64)(&tx.Nonce)
		enc.Gas = (*hexutil.Uint64)(&tx.Gas)
		enc.MinerTip = (*hexutil.Big)(tx.MinerTip)
		enc.GasPrice = (*hexutil.Big)(tx.GasPrice)
		enc.Value = (*hexutil.Big)(tx.Value)
		enc.Data = (*hexutil.Bytes)(&tx.Data)
		if t.To() != nil {
			enc.To = t.To().MixedcaseAddressPtr()
		}
		enc.V = (*hexutil.Big)(tx.V)
		enc.R = (*hexutil.Big)(tx.R)
		enc.S = (*hexutil.Big)(tx.S)
	case *ExternalTx:
		enc.AccessList = &tx.AccessList
		enc.OriginatingTxHash = &tx.OriginatingTxHash
		index := hexutil.Uint64(tx.ETXIndex)
		enc.ETXIndex = &index
		enc.Gas = (*hexutil.Uint64)(&tx.Gas)
		enc.Value = (*hexutil.Big)(tx.Value)
		enc.Data = (*hexutil.Bytes)(&tx.Data)
		enc.To = t.To().MixedcaseAddressPtr()
		enc.ETXSender = tx.Sender.MixedcaseAddressPtr()
		enc.EtxType = (*hexutil.Uint64)(&tx.EtxType)
	case *QiTx:
		sig := tx.Signature.Serialize()
		enc.ChainID = (*hexutil.Big)(tx.ChainID)
		for _, in := range tx.TxIn {
			pubKey := hexutil.Bytes(in.PubKey)
			enc.TxIn = append(enc.TxIn, TxInJSON{
				PreviousOutPoint: &OutpointJSON{
					TxHash: in.PreviousOutPoint.TxHash,
					Index:  hexutil.Uint64(in.PreviousOutPoint.Index),
				},
				PubKey: &pubKey,
			})
		}
		for _, out := range tx.TxOut {
			denom := uint64(out.Denomination)
			address := hexutil.Bytes(out.Address)
			lock := hexutil.Big(*out.Lock)
			enc.TxOut = append(enc.TxOut, TxOutJSON{
				Address:      &address,
				Denomination: (*hexutil.Uint64)(&denom),
				Lock:         &lock,
			})
		}
		enc.UTXOSignature = (*hexutil.Bytes)(&sig)
	}
	return json.Marshal(&enc)
}

// UnmarshalJSON unmarshals from JSON.
func (t *Transaction) UnmarshalJSON(input []byte) error {
	var dec txJSON
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}

	// Decode / verify fields according to transaction type.
	var inner TxData
	switch dec.Type {
	case QuaiTxType:
		var itx QuaiTx
		inner = &itx
		if dec.AccessList == nil {
			return errors.New("missing required field 'accessList' in internal transaction")
		}
		itx.AccessList = *dec.AccessList
		if dec.ChainID == nil {
			return errors.New("missing required field 'chainId' in internal transaction")
		}
		itx.ChainID = (*big.Int)(dec.ChainID)
		if dec.To != nil {
			addr := dec.To.Address()
			itx.To = &addr
		}
		if dec.Nonce == nil {
			return errors.New("missing required field 'nonce' in internal transaction")
		}
		itx.Nonce = uint64(*dec.Nonce)
		if dec.MinerTip == nil {
			return errors.New("missing required field 'minerTip' in internal transaction")
		}
		itx.MinerTip = (*big.Int)(dec.MinerTip)
		if dec.GasPrice == nil {
			return errors.New("missing required field 'gasPrice' in internal transaction")
		}
		itx.GasPrice = (*big.Int)(dec.GasPrice)
		if dec.Gas == nil {
			return errors.New("missing required field 'gas' in internal transaction")
		}
		itx.Gas = uint64(*dec.Gas)
		if dec.Value == nil {
			return errors.New("missing required field 'value' in internal transaction")
		}
		itx.Value = (*big.Int)(dec.Value)
		if dec.Data == nil {
			return errors.New("missing required field 'input' in internal transaction")
		}
		itx.Data = *dec.Data
		if dec.V == nil {
			return errors.New("missing required field 'v' in internal transaction")
		}
		itx.V = (*big.Int)(dec.V)
		if dec.R == nil {
			return errors.New("missing required field 'r' in internal transaction")
		}
		itx.R = (*big.Int)(dec.R)
		if dec.S == nil {
			return errors.New("missing required field 's' in internal transaction")
		}
		itx.S = (*big.Int)(dec.S)
		withSignature := itx.V.Sign() != 0 || itx.R.Sign() != 0 || itx.S.Sign() != 0
		if withSignature && itx.txType() != ExternalTxType {
			if err := sanityCheckSignature(itx.V, itx.R, itx.S); err != nil {
				return err
			}
		}

	case ExternalTxType:
		var etx ExternalTx
		inner = &etx
		if dec.AccessList == nil {
			return errors.New("missing required field 'accessList' in external transaction")
		}
		etx.AccessList = *dec.AccessList
		if dec.To != nil {
			addr := dec.To.Address()
			etx.To = &addr
		}
		if dec.OriginatingTxHash == nil {
			return errors.New("missing required field 'originatingTxHash' in external transaction")
		}
		etx.OriginatingTxHash = *dec.OriginatingTxHash
		if dec.ETXIndex == nil {
			return errors.New("missing required field 'etxIndex' in external transaction")
		}
		etx.ETXIndex = uint16(*dec.ETXIndex)
		if dec.Gas == nil {
			return errors.New("missing required field 'gas' in external transaction")
		}
		etx.Gas = uint64(*dec.Gas)
		if dec.Value == nil {
			return errors.New("missing required field 'value' in external transaction")
		}
		etx.Value = (*big.Int)(dec.Value)
		if dec.Data == nil {
			return errors.New("missing required field 'input' in external transaction")
		}
		etx.Data = *dec.Data
		if dec.ETXSender != nil {
			etx.Sender = dec.ETXSender.Address()
		}
		if dec.EtxType == nil {
			return errors.New("missing required field 'isCoinbase' in external transaction")
		}
		etx.EtxType = uint64(*dec.EtxType)

	case QiTxType:
		var qiTx QiTx
		inner = &qiTx
		qiTx.ChainID = (*big.Int)(dec.ChainID)
		for _, in := range dec.TxIn {
			if in.PreviousOutPoint == nil {
				return errors.New("missing required field 'previousOutPoint' in Qi transaction")
			}
			if in.PubKey == nil {
				return errors.New("missing required field 'pubkey' in Qi transaction")
			}
			qiTx.TxIn = append(qiTx.TxIn, TxIn{
				PreviousOutPoint: OutPoint{
					TxHash: in.PreviousOutPoint.TxHash,
					Index:  uint16(in.PreviousOutPoint.Index),
				},
				PubKey: *in.PubKey,
			})
		}
		for _, out := range dec.TxOut {
			if out.Denomination == nil {
				return errors.New("missing required field 'denomination' in Qi transaction")
			}
			if out.Address == nil {
				return errors.New("missing required field 'address' in Qi transaction")
			}
			qiTx.TxOut = append(qiTx.TxOut, TxOut{
				Address:      *out.Address,
				Denomination: uint8(*out.Denomination),
				Lock:         (*big.Int)(out.Lock),
			})
		}

		sig, err := schnorr.ParseSignature(*dec.UTXOSignature)
		if err != nil {
			return err
		}
		qiTx.Signature = sig

	default:
		return ErrTxTypeNotSupported
	}

	// Now set the inner transaction.
	t.setDecoded(inner, 0)
	if t.Hash() != dec.Hash {
		return fmt.Errorf("transaction hash mismatch: have %v, want %v", t.Hash(), dec.Hash)
	}

	// TODO: check hash here?
	return nil
}
