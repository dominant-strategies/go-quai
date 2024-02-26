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
	"errors"
	"fmt"
	"io"
	"math/big"
	"unsafe"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
)

//go:generate gencodec -type Receipt -field-override receiptMarshaling -out gen_receipt_json.go

var (
	receiptStatusFailedRLP     = []byte{}
	receiptStatusSuccessfulRLP = []byte{0x01}
)

// This error is returned when a typed receipt is decoded, but the string is empty.
var errEmptyTypedReceipt = errors.New("empty typed receipt bytes")

const (
	// ReceiptStatusFailed is the status code of a transaction if execution failed.
	ReceiptStatusFailed = uint64(0)

	// ReceiptStatusSuccessful is the status code of a transaction if execution succeeded.
	ReceiptStatusSuccessful = uint64(1)
)

// Receipt represents the results of a transaction.
type Receipt struct {
	// Consensus fields: These fields are defined by the Yellow Paper
	Type              uint8  `json:"type,omitempty"`
	PostState         []byte `json:"root"`
	Status            uint64 `json:"status"`
	CumulativeGasUsed uint64 `json:"cumulativeGasUsed" gencodec:"required"`
	Bloom             Bloom  `json:"logsBloom"         gencodec:"required"`
	Logs              Logs   `json:"logs"              gencodec:"required"`

	// Implementation fields: These fields are added by quai when processing a transaction.
	// They are stored in the chain database.
	TxHash          common.Hash    `json:"transactionHash" gencodec:"required"`
	ContractAddress common.Address `json:"contractAddress"`
	GasUsed         uint64         `json:"gasUsed" gencodec:"required"`

	// Inclusion information: These fields provide information about the inclusion of the
	// transaction corresponding to this receipt.
	BlockHash        common.Hash  `json:"blockHash,omitempty"`
	BlockNumber      *big.Int     `json:"blockNumber,omitempty"`
	TransactionIndex uint         `json:"transactionIndex"`
	Etxs             Transactions `json:"etxs"`
}

type receiptMarshaling struct {
	Type              hexutil.Uint64
	PostState         hexutil.Bytes
	Status            hexutil.Uint64
	CumulativeGasUsed hexutil.Uint64
	GasUsed           hexutil.Uint64
	BlockNumber       *hexutil.Big
	TransactionIndex  hexutil.Uint
}

// receiptRLP is the consensus encoding of a receipt.
type receiptRLP struct {
	PostStateOrStatus []byte
	CumulativeGasUsed uint64
	Bloom             Bloom
	Logs              []*Log
	Etxs              []*Transaction
}

// storedReceiptRLP is the storage encoding of a receipt used in database version 4.
type storedReceiptRLP struct {
	PostStateOrStatus []byte
	CumulativeGasUsed uint64
	TxHash            common.Hash
	ContractAddress   common.Address
	Logs              []*LogForStorage
	Etxs              []*Transaction
	GasUsed           uint64
}

// NewReceipt creates a barebone transaction receipt, copying the init fields.
// Deprecated: create receipts using a struct literal instead.
func NewReceipt(root []byte, failed bool, cumulativeGasUsed uint64) *Receipt {
	r := &Receipt{
		Type:              InternalTxType,
		PostState:         common.CopyBytes(root),
		CumulativeGasUsed: cumulativeGasUsed,
	}
	if failed {
		r.Status = ReceiptStatusFailed
	} else {
		r.Status = ReceiptStatusSuccessful
	}
	return r
}

// EncodeRLP implements rlp.Encoder, and flattens the consensus fields of a receipt
// into an RLP stream.
func (r *Receipt) EncodeRLP(w io.Writer) error {
	data := &receiptRLP{r.statusEncoding(), r.CumulativeGasUsed, r.Bloom, r.Logs, r.Etxs}
	buf := encodeBufferPool.Get().(*bytes.Buffer)
	defer encodeBufferPool.Put(buf)
	buf.Reset()
	buf.WriteByte(r.Type)
	if err := rlp.Encode(buf, data); err != nil {
		return err
	}
	return rlp.Encode(w, buf.Bytes())
}

// DecodeRLP implements rlp.Decoder, and loads the consensus fields of a receipt
// from an RLP stream.
func (r *Receipt) DecodeRLP(s *rlp.Stream) error {
	kind, _, err := s.Kind()
	if err != nil {
		return err
	}
	if kind == rlp.String {
		b, err := s.Bytes()
		if err != nil {
			return err
		}
		if len(b) == 0 {
			return errEmptyTypedReceipt
		}
		r.Type = b[0]
		if r.Supported() {
			var dec receiptRLP
			if err := rlp.DecodeBytes(b[1:], &dec); err != nil {
				return err
			}
			return r.setFromRLP(dec)
		}
		return ErrTxTypeNotSupported
	} else {
		return rlp.ErrExpectedString
	}
}

func (r *Receipt) setFromRLP(data receiptRLP) error {
	r.CumulativeGasUsed, r.Bloom, r.Logs = data.CumulativeGasUsed, data.Bloom, data.Logs
	return r.setStatus(data.PostStateOrStatus)
}

func (r *Receipt) setStatus(postStateOrStatus []byte) error {
	switch {
	case bytes.Equal(postStateOrStatus, receiptStatusSuccessfulRLP):
		r.Status = ReceiptStatusSuccessful
	case bytes.Equal(postStateOrStatus, receiptStatusFailedRLP):
		r.Status = ReceiptStatusFailed
	case len(postStateOrStatus) == len(common.Hash{}):
		r.PostState = postStateOrStatus
	default:
		return fmt.Errorf("invalid receipt status %x", postStateOrStatus)
	}
	return nil
}

func (r *Receipt) statusEncoding() []byte {
	if len(r.PostState) == 0 {
		if r.Status == ReceiptStatusFailed {
			return receiptStatusFailedRLP
		}
		return receiptStatusSuccessfulRLP
	}
	return r.PostState
}

// Size returns the approximate memory used by all internal contents. It is used
// to approximate and limit the memory consumption of various caches.
func (r *Receipt) Size() common.StorageSize {
	size := common.StorageSize(unsafe.Sizeof(*r)) + common.StorageSize(len(r.PostState))
	size += common.StorageSize(len(r.Logs)) * common.StorageSize(unsafe.Sizeof(Log{}))
	for _, log := range r.Logs {
		size += common.StorageSize(len(log.Topics)*common.HashLength + len(log.Data))
	}
	return size
}

// ReceiptsForStorage is a list of ReceiptForStorage.
type ReceiptsForStorage []*ReceiptForStorage

// ProtoEncode converts the receipts to a protobuf representation.
func (rs ReceiptsForStorage) ProtoEncode() (*ProtoReceiptsForStorage, error) {
	protoReceipts := make([]*ProtoReceiptForStorage, len(rs))
	for i, r := range rs {
		protoReceipt, err := r.ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoReceipts[i] = protoReceipt
	}
	return &ProtoReceiptsForStorage{Receipts: protoReceipts}, nil
}

// ProtoDecode converts the protobuf to a receipts representation.
func (rs *ReceiptsForStorage) ProtoDecode(protoReceipts *ProtoReceiptsForStorage, location common.Location) error {
	for _, protoReceipt := range protoReceipts.Receipts {
		receipt := new(ReceiptForStorage)
		err := receipt.ProtoDecode(protoReceipt, location)
		if err != nil {
			return err
		}
		*rs = append(*rs, receipt)
	}
	return nil
}

// ReceiptForStorage is a wrapper around a Receipt that flattens and parses the
// entire content of a receipt, as opposed to only the consensus fields originally.
type ReceiptForStorage Receipt

func (r *ReceiptForStorage) ProtoEncode() (*ProtoReceiptForStorage, error) {
	ProtoReceiptForStorage := &ProtoReceiptForStorage{
		PostStateOrStatus: (*Receipt)(r).statusEncoding(),
		CumulativeGasUsed: r.CumulativeGasUsed,
		ContractAddress:   r.ContractAddress.ProtoEncode(),
	}
	protoEtxs, err := r.Etxs.ProtoEncode()
	if err != nil {
		return nil, err
	}
	ProtoReceiptForStorage.Etxs = protoEtxs

	protoLogs := &ProtoLogsForStorage{}
	protoLogs.Logs = make([]*ProtoLogForStorage, len(r.Logs))
	for i, log := range r.Logs {
		protoLog := (*LogForStorage)(log).ProtoEncode()
		protoLogs.Logs[i] = protoLog
	}
	return ProtoReceiptForStorage, nil
}

func (r *ReceiptForStorage) ProtoDecode(protoReceipt *ProtoReceiptForStorage, location common.Location) error {
	if protoReceipt == nil {
		return errors.New("protoReceipt is nil in ProtoDecode")
	}
	if err := (*Receipt)(r).setStatus(protoReceipt.PostStateOrStatus); err != nil {
		return err
	}
	r.CumulativeGasUsed = protoReceipt.CumulativeGasUsed
	r.TxHash.ProtoDecode(protoReceipt.GetTxHash())
	err := r.ContractAddress.ProtoDecode(protoReceipt.ContractAddress, location)
	if err != nil {
		return err
	}
	r.GasUsed = protoReceipt.GetGasUsed()
	for i, protoLog := range protoReceipt.Logs.GetLogs() {
		log := new(LogForStorage)
		err := log.ProtoDecode(protoLog, location)
		if err != nil {
			return err
		}
		r.Logs[i] = (*Log)(log)
	}
	for i, protoEtx := range protoReceipt.Etxs.GetTransactions() {
		etx := new(Transaction)
		err := etx.ProtoDecode(protoEtx, location)
		if err != nil {
			return err
		}
		r.Etxs[i] = etx
	}
	r.Bloom = CreateBloom(Receipts{(*Receipt)(r)})
	return nil
}

// EncodeRLP implements rlp.Encoder, and flattens all content fields of a receipt
// into an RLP stream.
func (r *ReceiptForStorage) EncodeRLP(w io.Writer) error {
	enc := &storedReceiptRLP{
		PostStateOrStatus: (*Receipt)(r).statusEncoding(),
		CumulativeGasUsed: r.CumulativeGasUsed,
		Logs:              make([]*LogForStorage, len(r.Logs)),
		Etxs:              make([]*Transaction, len(r.Etxs)),
	}
	for i, log := range r.Logs {
		enc.Logs[i] = (*LogForStorage)(log)
	}
	for i, etx := range r.Etxs {
		enc.Etxs[i] = (*Transaction)(etx)
	}
	return rlp.Encode(w, enc)
}

// DecodeRLP implements rlp.Decoder, and loads both consensus and implementation
// fields of a receipt from an RLP stream.
func (r *ReceiptForStorage) DecodeRLP(s *rlp.Stream) error {
	// Retrieve the entire receipt blob as we need to try multiple decoders
	blob, err := s.Raw()
	if err != nil {
		return err
	}
	return decodeStoredReceiptRLP(r, blob)
}

func decodeStoredReceiptRLP(r *ReceiptForStorage, blob []byte) error {
	var stored storedReceiptRLP
	if err := rlp.DecodeBytes(blob, &stored); err != nil {
		return err
	}
	if err := (*Receipt)(r).setStatus(stored.PostStateOrStatus); err != nil {
		return err
	}
	r.CumulativeGasUsed = stored.CumulativeGasUsed
	r.TxHash = stored.TxHash
	r.ContractAddress = stored.ContractAddress
	r.GasUsed = stored.GasUsed
	r.Logs = make([]*Log, len(stored.Logs))
	for i, log := range stored.Logs {
		r.Logs[i] = (*Log)(log)
	}
	r.Etxs = make([]*Transaction, len(stored.Etxs))
	for i, etx := range stored.Etxs {
		r.Etxs[i] = (*Transaction)(etx)
	}
	r.Bloom = CreateBloom(Receipts{(*Receipt)(r)})

	return nil
}

// Receipts implements DerivableList for receipts.
type Receipts []*Receipt

// Len returns the number of receipts in this list.
func (rs Receipts) Len() int { return len(rs) }

// Supported returns true if the receipt type is supported
func (r Receipt) Supported() bool {
	return r.Type == InternalTxType || r.Type == ExternalTxType || r.Type == InternalToExternalTxType
}

// EncodeIndex encodes the i'th receipt to w.
func (rs Receipts) EncodeIndex(i int, w *bytes.Buffer) {
	if r := rs[i]; r.Supported() {
		data := &receiptRLP{r.statusEncoding(), r.CumulativeGasUsed, r.Bloom, r.Logs, r.Etxs}
		w.WriteByte(r.Type)
		rlp.Encode(w, data)
	}
	// For unsupported types, write nothing. Since this is for
	// DeriveSha, the error will be caught matching the derived hash
	// to the block.
}

// DeriveFields fills the receipts with their computed fields based on consensus
// data and contextual infos like containing block and transactions.
func (r Receipts) DeriveFields(config *params.ChainConfig, hash common.Hash, number uint64, txs Transactions) error {
	signer := MakeSigner(config, new(big.Int).SetUint64(number))

	logIndex := uint(0)
	for i := 0; i < len(r); i++ {
		// The transaction type and hash can be retrieved from the transaction itself
		r[i].Type = txs[i].Type()
		r[i].TxHash = txs[i].Hash()

		// block location fields
		r[i].BlockHash = hash
		r[i].BlockNumber = new(big.Int).SetUint64(number)
		r[i].TransactionIndex = uint(i)

		// The contract address can be derived from the transaction itself
		if r[i].ContractAddress.Equal(common.Address{}) && txs[i].To() == nil {
			// Deriving the signer is expensive, only do if it's actually needed
			from, err := Sender(signer, txs[i])
			if err != nil {
				return err
			}
			r[i].ContractAddress = crypto.CreateAddress(from, txs[i].Nonce(), txs[i].Data(), config.Location)
		}
		// The used gas can be calculated based on previous r
		if i == 0 {
			r[i].GasUsed = r[i].CumulativeGasUsed
		} else {
			r[i].GasUsed = r[i].CumulativeGasUsed - r[i-1].CumulativeGasUsed
		}
		// The derived log fields can simply be set from the block and transaction
		for j := 0; j < len(r[i].Logs); j++ {
			r[i].Logs[j].BlockNumber = number
			r[i].Logs[j].BlockHash = hash
			r[i].Logs[j].TxHash = r[i].TxHash
			r[i].Logs[j].TxIndex = uint(i)
			r[i].Logs[j].Index = logIndex
			logIndex++
		}
	}
	return nil
}
