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

// Package types contains data types related to Quai consensus.
package types

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
	"lukechampine.com/blake3"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rlp"
)

var (
	EmptyRootHash   = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	EmptyUncleHash  = RlpHash([]*Header(nil))
	EmptyBodyHash   = common.HexToHash("51e1b9c1426a03bf73da3d98d9f384a49ded6a4d705dcdf25433915c3306826c")
	EmptyEtxSetHash = crypto.Keccak256Hash([]byte{})
	big2e256        = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil) // 2^256
	hasher          = blake3.New(32, nil)
	hasherMu        sync.RWMutex
)

// A BlockNonce is a 64-bit hash which proves (combined with the
// mix-hash) that a sufficient amount of computation has been carried
// out on a block.
type BlockNonce [8]byte

// EncodeNonce converts the given integer to a block nonce.
func EncodeNonce(i uint64) BlockNonce {
	var n BlockNonce
	binary.BigEndian.PutUint64(n[:], i)
	return n
}

// Bytes() returns the raw bytes of the block nonce
func (n BlockNonce) Bytes() []byte {
	return n[:]
}

// Uint64 returns the integer value of a block nonce.
func (n BlockNonce) Uint64() uint64 {
	return binary.BigEndian.Uint64(n[:])
}

// MarshalText encodes n as a hex string with 0x prefix.
func (n BlockNonce) MarshalText() ([]byte, error) {
	return hexutil.Bytes(n[:]).MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (n *BlockNonce) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedText("BlockNonce", input, n[:])
}

//go:generate gencodec -type Header -field-override headerMarshaling -out gen_header_json.go

// Header represents a block header in the Quai blockchain.
type Header struct {
	parentHash    []common.Hash   `json:"parentHash"           gencodec:"required"`
	uncleHash     common.Hash     `json:"sha3Uncles"           gencodec:"required"`
	coinbase      common.Address  `json:"miner"                gencodec:"required"`
	evmRoot       common.Hash     `json:"evmRoot"            gencodec:"required"`
	utxoRoot      common.Hash     `json:"utxoRoot"               gencodec:"required"`
	txHash        common.Hash     `json:"transactionsRoot"     gencodec:"required"`
	etxHash       common.Hash     `json:"extTransactionsRoot"  gencodec:"required"`
	etxSetHash    common.Hash     `json:"etxSetHash"        gencodec:"required"`
	etxRollupHash common.Hash     `json:"extRollupRoot"        gencodec:"required"`
	manifestHash  []common.Hash   `json:"manifestHash"         gencodec:"required"`
	receiptHash   common.Hash     `json:"receiptsRoot"         gencodec:"required"`
	difficulty    *big.Int        `json:"difficulty"           gencodec:"required"`
	parentEntropy []*big.Int      `json:"parentEntropy"        gencodec:"required"`
	parentDeltaS  []*big.Int      `json:"parentDeltaS"         gencodec:"required"`
	number        []*big.Int      `json:"number"               gencodec:"required"`
	gasLimit      uint64          `json:"gasLimit"             gencodec:"required"`
	gasUsed       uint64          `json:"gasUsed"              gencodec:"required"`
	baseFee       *big.Int        `json:"baseFeePerGas"        gencodec:"required"`
	location      common.Location `json:"location"             gencodec:"required"`
	time          uint64          `json:"timestamp"            gencodec:"required"`
	extra         []byte          `json:"extraData"            gencodec:"required"`
	mixHash       common.Hash     `json:"mixHash"              gencodec:"required"`
	nonce         BlockNonce      `json:"nonce"`

	// caches
	hash      atomic.Value
	sealHash  atomic.Value
	PowHash   atomic.Value
	PowDigest atomic.Value
}

// field type overrides for gencodec
type headerMarshaling struct {
	Difficulty    *hexutil.Big
	Number        []*hexutil.Big
	GasLimit      hexutil.Uint64
	GasUsed       hexutil.Uint64
	BaseFee       *hexutil.Big
	ParentEntropy []*hexutil.Big
	ParentDeltaS  []*hexutil.Big
	Time          hexutil.Uint64
	Extra         hexutil.Bytes
	Hash          common.Hash `json:"hash"` // adds call to Hash() in MarshalJSON
}

// "external" header encoding. used for eth protocol, etc.
type extheader struct {
	ParentHash    []common.Hash
	UncleHash     common.Hash
	Coinbase      common.Address
	EVMRoot       common.Hash
	UTXORoot      common.Hash
	TxHash        common.Hash
	EtxHash       common.Hash
	EtxSetHash    common.Hash
	EtxRollupHash common.Hash
	ManifestHash  []common.Hash
	ReceiptHash   common.Hash
	Difficulty    *big.Int
	ParentEntropy []*big.Int
	ParentDeltaS  []*big.Int
	Number        []*big.Int
	GasLimit      uint64
	GasUsed       uint64
	BaseFee       *big.Int
	Location      common.Location
	Time          uint64
	Extra         []byte
	MixHash       common.Hash
	Nonce         BlockNonce
}

// Construct an empty header
func EmptyHeader() *Header {
	h := &Header{}
	h.parentHash = make([]common.Hash, common.HierarchyDepth)
	h.manifestHash = make([]common.Hash, common.HierarchyDepth)
	h.parentEntropy = make([]*big.Int, common.HierarchyDepth)
	h.parentDeltaS = make([]*big.Int, common.HierarchyDepth)
	h.number = make([]*big.Int, common.HierarchyDepth)
	h.difficulty = big.NewInt(0)
	h.evmRoot = EmptyRootHash
	h.utxoRoot = EmptyRootHash
	h.mixHash = EmptyRootHash
	h.txHash = EmptyRootHash
	h.etxHash = EmptyRootHash
	h.etxSetHash = EmptyEtxSetHash
	h.etxRollupHash = EmptyRootHash
	h.uncleHash = EmptyUncleHash
	h.baseFee = big.NewInt(0)
	h.extra = []byte{}

	for i := 0; i < common.HierarchyDepth; i++ {
		h.manifestHash[i] = EmptyRootHash
		h.parentEntropy[i] = big.NewInt(0)
		h.parentDeltaS[i] = big.NewInt(0)
		h.number[i] = big.NewInt(0)
	}
	return h
}

// DecodeRLP decodes the Quai header format into h.
func (h *Header) DecodeRLP(s *rlp.Stream) error {
	var eh extheader
	if err := s.Decode(&eh); err != nil {
		return err
	}
	h.parentHash = eh.ParentHash
	h.uncleHash = eh.UncleHash
	h.coinbase = common.BytesToAddress(eh.Coinbase.Bytes(), eh.Location)
	h.evmRoot = eh.EVMRoot
	h.utxoRoot = eh.UTXORoot
	h.txHash = eh.TxHash
	h.etxHash = eh.EtxHash
	h.etxSetHash = eh.EtxSetHash
	h.etxRollupHash = eh.EtxRollupHash
	h.manifestHash = eh.ManifestHash
	h.receiptHash = eh.ReceiptHash
	h.difficulty = eh.Difficulty
	h.parentEntropy = eh.ParentEntropy
	h.parentDeltaS = eh.ParentDeltaS
	h.number = eh.Number
	h.gasLimit = eh.GasLimit
	h.gasUsed = eh.GasUsed
	h.baseFee = eh.BaseFee
	h.location = eh.Location
	h.time = eh.Time
	h.extra = eh.Extra
	h.mixHash = eh.MixHash
	h.nonce = eh.Nonce

	return nil
}

// EncodeRLP serializes h into the Quai RLP block format.
func (h *Header) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extheader{
		ParentHash:    h.parentHash,
		UncleHash:     h.uncleHash,
		Coinbase:      h.coinbase,
		EVMRoot:       h.evmRoot,
		UTXORoot:      h.utxoRoot,
		TxHash:        h.txHash,
		EtxHash:       h.etxHash,
		EtxSetHash:    h.etxSetHash,
		EtxRollupHash: h.etxRollupHash,
		ManifestHash:  h.manifestHash,
		ReceiptHash:   h.receiptHash,
		Difficulty:    h.difficulty,
		ParentEntropy: h.parentEntropy,
		ParentDeltaS:  h.parentDeltaS,
		Number:        h.number,
		GasLimit:      h.gasLimit,
		GasUsed:       h.gasUsed,
		BaseFee:       h.baseFee,
		Location:      h.location,
		Time:          h.time,
		Extra:         h.extra,
		MixHash:       h.mixHash,
		Nonce:         h.nonce,
	})
}

// ProtoEncode serializes h into the Quai Proto Header format
func (h *Header) ProtoEncode() (*ProtoHeader, error) {
	if h == nil {
		return nil, errors.New("header to be proto encoded is nil")
	}

	uncleHash := common.ProtoHash{Value: h.UncleHash().Bytes()}
	evmRoot := common.ProtoHash{Value: h.EVMRoot().Bytes()}
	utxoRoot := common.ProtoHash{Value: h.UTXORoot().Bytes()}
	txHash := common.ProtoHash{Value: h.TxHash().Bytes()}
	etxhash := common.ProtoHash{Value: h.EtxHash().Bytes()}
	etxSetHash := common.ProtoHash{Value: h.EtxSetHash().Bytes()}
	etxRollupHash := common.ProtoHash{Value: h.EtxRollupHash().Bytes()}
	receiptHash := common.ProtoHash{Value: h.ReceiptHash().Bytes()}
	mixHash := common.ProtoHash{Value: h.MixHash().Bytes()}
	gasLimit := h.GasLimit()
	gasUsed := h.GasUsed()
	time := h.Time()
	nonce := h.Nonce().Uint64()

	protoHeader := &ProtoHeader{
		UncleHash:     &uncleHash,
		Coinbase:      h.Coinbase().Bytes(),
		EvmRoot:       &evmRoot,
		UtxoRoot:      &utxoRoot,
		TxHash:        &txHash,
		EtxHash:       &etxhash,
		EtxSetHash:    &etxSetHash,
		EtxRollupHash: &etxRollupHash,
		ReceiptHash:   &receiptHash,
		Difficulty:    h.Difficulty().Bytes(),
		GasLimit:      &gasLimit,
		GasUsed:       &gasUsed,
		BaseFee:       h.BaseFee().Bytes(),
		Location:      h.Location().ProtoEncode(),
		Time:          &time,
		Extra:         h.Extra(),
		MixHash:       &mixHash,
		Nonce:         &nonce,
	}

	for i := 0; i < common.HierarchyDepth; i++ {
		protoHeader.ParentHash = append(protoHeader.ParentHash, h.ParentHash(i).ProtoEncode())
		protoHeader.ManifestHash = append(protoHeader.ManifestHash, h.ManifestHash(i).ProtoEncode())
		if h.ParentEntropy(i) != nil {
			protoHeader.ParentEntropy = append(protoHeader.ParentEntropy, h.ParentEntropy(i).Bytes())
		}
		if h.ParentDeltaS(i) != nil {
			protoHeader.ParentDeltaS = append(protoHeader.ParentDeltaS, h.ParentDeltaS(i).Bytes())
		}
		if h.Number(i) != nil {
			protoHeader.Number = append(protoHeader.Number, h.Number(i).Bytes())
		}
	}
	return protoHeader, nil
}

// ProtoDecode deserializes the ProtoHeader into the Header format
func (h *Header) ProtoDecode(protoHeader *ProtoHeader) error {
	if protoHeader.ParentHash == nil {
		return errors.New("missing required field 'ParentHash' in Header")
	}
	if protoHeader.UncleHash == nil {
		return errors.New("missing required field 'UncleHash' in Header")
	}
	if protoHeader.Coinbase == nil {
		return errors.New("missing required field 'Coinbase' in Header")
	}
	if protoHeader.EvmRoot == nil {
		return errors.New("missing required field 'Root' in Header")
	}
	if protoHeader.UtxoRoot == nil {
		return errors.New("missing required field 'UTXORoot' in Header")
	}
	if protoHeader.TxHash == nil {
		return errors.New("missing required field 'TxHash' in Header")
	}
	if protoHeader.EtxHash == nil {
		return errors.New("missing required field 'EtxHash' in Header")
	}
	if protoHeader.EtxSetHash == nil {
		return errors.New("missing required field 'EtxSetHash' in Header")
	}
	if protoHeader.EtxRollupHash == nil {
		return errors.New("missing required field 'EtxRollupHash' in Header")
	}
	if protoHeader.ManifestHash == nil {
		return errors.New("missing required field 'ManifestHash' in Header")
	}
	if protoHeader.ReceiptHash == nil {
		return errors.New("missing required field 'ReceiptHash' in Header")
	}
	if protoHeader.Difficulty == nil {
		return errors.New("missing required field 'Difficulty' in Header")
	}
	if protoHeader.BaseFee == nil {
		return errors.New("missing required field 'BaseFee' in Header")
	}
	if protoHeader.MixHash == nil {
		return errors.New("missing required field 'MixHash' in Header")
	}
	if protoHeader.ParentEntropy == nil {
		return errors.New("missing required field 'ParentEntropy' in Header")
	}
	if protoHeader.ParentDeltaS == nil {
		return errors.New("missing required field 'ParentDeltaS' in Header")
	}
	if protoHeader.Number == nil {
		return errors.New("missing required field 'Number' in Header")
	}
	if protoHeader.Location == nil {
		return errors.New("missing required field 'Location' in Header")
	}
	if protoHeader.MixHash == nil {
		return errors.New("missing required field 'MixHash' in Header")
	}

	// Initialize the array fields before setting
	h.parentHash = make([]common.Hash, common.HierarchyDepth)
	h.manifestHash = make([]common.Hash, common.HierarchyDepth)
	h.parentEntropy = make([]*big.Int, common.HierarchyDepth)
	h.parentDeltaS = make([]*big.Int, common.HierarchyDepth)
	h.number = make([]*big.Int, common.HierarchyDepth)

	for i := 0; i < common.HierarchyDepth; i++ {
		h.SetParentHash(common.BytesToHash(protoHeader.GetParentHash()[i].GetValue()), i)
		h.SetManifestHash(common.BytesToHash(protoHeader.GetManifestHash()[i].GetValue()), i)
		h.SetParentEntropy(new(big.Int).SetBytes(protoHeader.GetParentEntropy()[i]), i)
		h.SetParentDeltaS(new(big.Int).SetBytes(protoHeader.GetParentDeltaS()[i]), i)
		h.SetNumber(new(big.Int).SetBytes(protoHeader.GetNumber()[i]), i)
	}

	h.SetUncleHash(common.BytesToHash(protoHeader.GetUncleHash().GetValue()))
	h.SetCoinbase(common.BytesToAddress(protoHeader.GetCoinbase(), protoHeader.GetLocation().GetValue()))
	h.SetEVMRoot(common.BytesToHash(protoHeader.GetEvmRoot().GetValue()))
	h.SetUTXORoot(common.BytesToHash(protoHeader.GetUtxoRoot().GetValue()))
	h.SetTxHash(common.BytesToHash(protoHeader.GetTxHash().GetValue()))
	h.SetReceiptHash(common.BytesToHash(protoHeader.GetReceiptHash().GetValue()))
	h.SetEtxHash(common.BytesToHash(protoHeader.GetEtxHash().GetValue()))
	h.SetEtxSetHash(common.BytesToHash(protoHeader.GetEtxSetHash().GetValue()))
	h.SetEtxRollupHash(common.BytesToHash(protoHeader.GetEtxRollupHash().GetValue()))
	h.SetDifficulty(new(big.Int).SetBytes(protoHeader.GetDifficulty()))
	h.SetGasLimit(protoHeader.GetGasLimit())
	h.SetGasUsed(protoHeader.GetGasUsed())
	h.SetBaseFee(new(big.Int).SetBytes(protoHeader.GetBaseFee()))
	h.SetTime(protoHeader.GetTime())
	h.SetExtra(protoHeader.GetExtra())
	h.SetMixHash(common.BytesToHash(protoHeader.GetMixHash().GetValue()))
	h.SetNonce(uint64ToByteArr(protoHeader.GetNonce()))
	h.SetLocation(protoHeader.GetLocation().GetValue())

	return nil
}

// helper to convert uint64 into a byte array
func uint64ToByteArr(val uint64) [8]byte {
	var arr [8]byte
	binary.BigEndian.PutUint64(arr[:], val)
	return arr
}

// RPCMarshalHeader converts the given header to the RPC output .
func (h *Header) RPCMarshalHeader() map[string]interface{} {
	result := map[string]interface{}{
		"hash":                h.Hash(),
		"parentHash":          h.ParentHashArray(),
		"difficulty":          (*hexutil.Big)(h.Difficulty()),
		"nonce":               h.Nonce(),
		"sha3Uncles":          h.UncleHash(),
		"evmRoot":             h.EVMRoot(),
		"utxoRoot":            h.UTXORoot(),
		"miner":               h.Coinbase(),
		"extraData":           hexutil.Bytes(h.Extra()),
		"size":                hexutil.Uint64(h.Size()),
		"timestamp":           hexutil.Uint64(h.Time()),
		"transactionsRoot":    h.TxHash(),
		"receiptsRoot":        h.ReceiptHash(),
		"extTransactionsRoot": h.EtxHash(),
		"etxSetHash":          h.EtxSetHash(),
		"extRollupRoot":       h.EtxRollupHash(),
		"manifestHash":        h.ManifestHashArray(),
		"gasLimit":            hexutil.Uint(h.GasLimit()),
		"gasUsed":             hexutil.Uint(h.GasUsed()),
		"location":            hexutil.Bytes(h.Location()),
		"mixHash":             h.MixHash(),
	}

	number := make([]*hexutil.Big, common.HierarchyDepth)
	parentEntropy := make([]*hexutil.Big, common.HierarchyDepth)
	parentDeltaS := make([]*hexutil.Big, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		number[i] = (*hexutil.Big)(h.Number(i))
		parentEntropy[i] = (*hexutil.Big)(h.ParentEntropy(i))
		parentDeltaS[i] = (*hexutil.Big)(h.ParentDeltaS(i))
	}
	result["number"] = number
	result["parentEntropy"] = parentEntropy
	result["parentDeltaS"] = parentDeltaS

	if h.BaseFee() != nil {
		result["baseFeePerGas"] = (*hexutil.Big)(h.BaseFee())
	}

	return result
}

// Localized accessors
func (h *Header) ParentHash(nodeCtx int) common.Hash {
	return h.parentHash[nodeCtx]
}
func (h *Header) UncleHash() common.Hash {
	return h.uncleHash
}
func (h *Header) Coinbase() common.Address {
	return h.coinbase
}
func (h *Header) EVMRoot() common.Hash {
	return h.evmRoot
}
func (h *Header) UTXORoot() common.Hash {
	return h.utxoRoot
}
func (h *Header) TxHash() common.Hash {
	return h.txHash
}
func (h *Header) EtxHash() common.Hash {
	return h.etxHash
}
func (h *Header) EtxSetHash() common.Hash {
	return h.etxSetHash
}
func (h *Header) EtxRollupHash() common.Hash {
	return h.etxRollupHash
}
func (h *Header) ParentEntropy(nodeCtx int) *big.Int {
	return h.parentEntropy[nodeCtx]
}
func (h *Header) ParentDeltaS(nodeCtx int) *big.Int {
	return h.parentDeltaS[nodeCtx]
}
func (h *Header) ManifestHash(nodeCtx int) common.Hash {
	return h.manifestHash[nodeCtx]
}
func (h *Header) ReceiptHash() common.Hash {
	return h.receiptHash
}
func (h *Header) Difficulty() *big.Int {
	return h.difficulty
}
func (h *Header) Number(nodeCtx int) *big.Int {
	return h.number[nodeCtx]
}
func (h *Header) NumberU64(nodeCtx int) uint64 {
	return h.number[nodeCtx].Uint64()
}
func (h *Header) GasLimit() uint64 {
	return h.gasLimit
}
func (h *Header) GasUsed() uint64 {
	return h.gasUsed
}
func (h *Header) BaseFee() *big.Int {
	return h.baseFee
}
func (h *Header) Location() common.Location { return h.location }
func (h *Header) Time() uint64              { return h.time }
func (h *Header) Extra() []byte             { return common.CopyBytes(h.extra) }
func (h *Header) MixHash() common.Hash      { return h.mixHash }
func (h *Header) Nonce() BlockNonce         { return h.nonce }
func (h *Header) NonceU64() uint64          { return binary.BigEndian.Uint64(h.nonce[:]) }

func (h *Header) SetParentHash(val common.Hash, nodeCtx int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.parentHash[nodeCtx] = val
}
func (h *Header) SetUncleHash(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.uncleHash = val
}
func (h *Header) SetCoinbase(val common.Address) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.coinbase = val
}
func (h *Header) SetEVMRoot(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.evmRoot = val
}
func (h *Header) SetUTXORoot(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.utxoRoot = val
}
func (h *Header) SetTxHash(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.txHash = val
}
func (h *Header) SetEtxHash(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.etxHash = val
}
func (h *Header) SetEtxSetHash(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.etxSetHash = val
}
func (h *Header) SetEtxRollupHash(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.etxRollupHash = val
}

func (h *Header) SetParentEntropy(val *big.Int, nodeCtx int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.parentEntropy[nodeCtx] = val
}

func (h *Header) SetParentDeltaS(val *big.Int, nodeCtx int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.parentDeltaS[nodeCtx] = val
}

func (h *Header) SetManifestHash(val common.Hash, nodeCtx int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.manifestHash[nodeCtx] = val
}
func (h *Header) SetReceiptHash(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.receiptHash = val
}
func (h *Header) SetDifficulty(val *big.Int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.difficulty = new(big.Int).Set(val)
}
func (h *Header) SetNumber(val *big.Int, nodeCtx int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.number[nodeCtx] = new(big.Int).Set(val)
}
func (h *Header) SetGasLimit(val uint64) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.gasLimit = val
}
func (h *Header) SetGasUsed(val uint64) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.gasUsed = val
}
func (h *Header) SetBaseFee(val *big.Int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.baseFee = new(big.Int).Set(val)
}
func (h *Header) SetLocation(val common.Location) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.location = val
}
func (h *Header) SetTime(val uint64) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.time = val
}
func (h *Header) SetExtra(val []byte) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.extra = make([]byte, len(val))
	copy(h.extra, val)
}
func (h *Header) SetMixHash(val common.Hash) {
	h.hash = atomic.Value{} // clear hash cache
	h.mixHash = val
}
func (h *Header) SetNonce(val BlockNonce) {
	h.hash = atomic.Value{} // clear hash cache, but NOT sealHash
	h.nonce = val
}

// Array accessors
func (h *Header) ParentHashArray() []common.Hash   { return h.parentHash }
func (h *Header) ManifestHashArray() []common.Hash { return h.manifestHash }
func (h *Header) NumberArray() []*big.Int          { return h.number }

// ProtoEncode serializes s into the Quai Proto sealData format
func (h *Header) SealEncode() *ProtoHeader {
	uncleHash := common.ProtoHash{Value: h.UncleHash().Bytes()}
	evmRoot := common.ProtoHash{Value: h.EVMRoot().Bytes()}
	utxoRoot := common.ProtoHash{Value: h.UTXORoot().Bytes()}
	txHash := common.ProtoHash{Value: h.TxHash().Bytes()}
	etxhash := common.ProtoHash{Value: h.EtxHash().Bytes()}
	etxSetHash := common.ProtoHash{Value: h.EtxSetHash().Bytes()}
	etxRollupHash := common.ProtoHash{Value: h.EtxRollupHash().Bytes()}
	receiptHash := common.ProtoHash{Value: h.ReceiptHash().Bytes()}
	mixHash := common.ProtoHash{Value: h.MixHash().Bytes()}
	gasLimit := h.GasLimit()
	gasUsed := h.GasUsed()
	time := h.Time()

	protoSealData := &ProtoHeader{
		UncleHash:     &uncleHash,
		Coinbase:      h.Coinbase().Bytes(),
		EvmRoot:       &evmRoot,
		UtxoRoot:      &utxoRoot,
		TxHash:        &txHash,
		EtxHash:       &etxhash,
		EtxSetHash:    &etxSetHash,
		EtxRollupHash: &etxRollupHash,
		ReceiptHash:   &receiptHash,
		Difficulty:    h.Difficulty().Bytes(),
		GasLimit:      &gasLimit,
		GasUsed:       &gasUsed,
		BaseFee:       h.BaseFee().Bytes(),
		Location:      h.Location().ProtoEncode(),
		Time:          &time,
		Extra:         h.Extra(),
		MixHash:       &mixHash,
	}

	for i := 0; i < common.HierarchyDepth; i++ {
		protoSealData.ParentHash = append(protoSealData.ParentHash, h.ParentHash(i).ProtoEncode())
		protoSealData.ManifestHash = append(protoSealData.ManifestHash, h.ManifestHash(i).ProtoEncode())
		if h.ParentEntropy(i) != nil {
			protoSealData.ParentEntropy = append(protoSealData.ParentEntropy, h.ParentEntropy(i).Bytes())
		}
		if h.ParentDeltaS(i) != nil {
			protoSealData.ParentDeltaS = append(protoSealData.ParentDeltaS, h.ParentDeltaS(i).Bytes())
		}
		if h.Number(i) != nil {
			protoSealData.Number = append(protoSealData.Number, h.Number(i).Bytes())
		}
	}
	return protoSealData
}

// SealHash returns the hash of a block prior to it being sealed.
func (h *Header) SealHash() (hash common.Hash) {
	hasherMu.Lock()
	defer hasherMu.Unlock()
	hasher.Reset()
	protoSealData := h.SealEncode()
	data, err := proto.Marshal(protoSealData)
	if err != nil {
		log.Global.Error("Failed to marshal seal data ", "err", err)
	}
	sum := blake3.Sum256(data[:])
	hash.SetBytes(sum[:])
	return hash
}

// Hash returns the nonce'd hash of the header. This is just the Blake3 hash of
// SealHash suffixed with a nonce.
func (h *Header) Hash() (hash common.Hash) {
	sealHash := h.SealHash().Bytes()
	hasherMu.Lock()
	defer hasherMu.Unlock()
	hasher.Reset()
	var hData [40]byte
	copy(hData[:], h.Nonce().Bytes())
	copy(hData[len(h.nonce):], sealHash)
	sum := blake3.Sum256(hData[:])
	hash.SetBytes(sum[:])
	return hash
}

// totalBitLen returns the cumulative BitLen for each element in a big.Int slice.
func totalBitLen(array []*big.Int) int {
	bitLen := 0
	for _, item := range array {
		if item != nil {
			bitLen += item.BitLen()
		}
	}
	return bitLen
}

var headerSize = common.StorageSize(reflect.TypeOf(Header{}).Size())

// Size returns the approximate memory used by all internal contents. It is used
// to approximate and limit the memory consumption of various caches.
func (h *Header) Size() common.StorageSize {
	return headerSize + common.StorageSize(len(h.extra)+(h.difficulty.BitLen()+totalBitLen(h.number))/8)
}

// SanityCheck checks a few basic things -- these checks are way beyond what
// any 'sane' production values should hold, and can mainly be used to prevent
// that the unbounded fields are stuffed with junk data to add processing
// overhead
func (h *Header) SanityCheck() error {
	if h.parentHash == nil || len(h.parentHash) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: parentHash")
	}
	if h.manifestHash == nil || len(h.manifestHash) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: manifestHash")
	}
	if h.difficulty == nil {
		return fmt.Errorf("field cannot be `nil`: difficulty")
	}
	if h.number == nil || len(h.number) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: number")
	}
	if h.parentEntropy == nil || len(h.parentEntropy) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: parentEntropy")
	}
	if h.parentDeltaS == nil || len(h.parentDeltaS) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: parentDeltaS")
	}
	if h.baseFee == nil {
		return fmt.Errorf("field cannot be `nil`: baseFee")
	}
	if bfLen := h.baseFee.BitLen(); bfLen > 256 {
		return fmt.Errorf("too large base fee: bitlen %d", bfLen)
	}
	for i := 0; i < common.HierarchyDepth; i++ {
		if h.number == nil {
			return fmt.Errorf("field cannot be `nil`: number[%d]", i)
		}
		if h.number[i] != nil && !h.number[i].IsUint64() {
			return fmt.Errorf("too large block number[%d]: bitlen %d", i, h.number[i].BitLen())
		}
	}
	if diffLen := h.difficulty.BitLen(); diffLen > 80 {
		return fmt.Errorf("too large block difficulty: bitlen %d", diffLen)
	}
	if eLen := len(h.extra); eLen > 100*1024 {
		return fmt.Errorf("too large block extradata: size %d", eLen)
	}
	return nil
}

// EmptyBody returns true if there is no additional 'body' to complete the header
// that is: no transactions and no uncles.
func (h *Header) EmptyBody(nodeCtx int) bool {
	return h.EmptyTxs() && h.EmptyUncles() && h.EmptyEtxs() && h.EmptyManifest(nodeCtx)
}

// EmptyTxs returns true if there are no txs for this header/block.
func (h *Header) EmptyTxs() bool {
	return h.TxHash() == EmptyRootHash
}

// EmptyEtxs returns true if there are no etxs for this header/block.
func (h *Header) EmptyEtxs() bool {
	return h.EtxHash() == EmptyRootHash
}

// EmptyEtxs returns true if there are no etxs for this header/block.
func (h *Header) EmptyEtxRollup() bool {
	return h.EtxRollupHash() == EmptyRootHash
}

// EmptyTxs returns true if there are no txs for this header/block.
func (h *Header) EmptyManifest(nodeCtx int) bool {
	return h.ManifestHash(nodeCtx) == EmptyRootHash
}

// EmptyUncles returns true if there are no uncles for this header/block.
func (h *Header) EmptyUncles() bool {
	return h.UncleHash() == EmptyRootHash
}

// EmptyReceipts returns true if there are no receipts for this header/block.
func (h *Header) EmptyReceipts() bool {
	return h.ReceiptHash() == EmptyRootHash
}

// Body is a simple (mutable, non-safe) data container for storing and moving
// a block's data contents (transactions and uncles) together.
type Body struct {
	Transactions    Transactions
	Uncles          []*Header
	ExtTransactions Transactions
	SubManifest     BlockManifest
}

// ProtoEncode serializes b into the Quai Proto Body format
func (b *Body) ProtoEncode() (*ProtoBody, error) {
	protoTransactions, err := b.Transactions.ProtoEncode()
	if err != nil {
		return nil, err
	}
	protoExtTransactions, err := b.ExtTransactions.ProtoEncode()
	if err != nil {
		return nil, err
	}
	protoUncles := &ProtoHeaders{}
	for _, unc := range b.Uncles {
		protoUncle, err := unc.ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoUncles.Headers = append(protoUncles.Headers, protoUncle)
	}
	protoManifest, err := b.SubManifest.ProtoEncode()
	if err != nil {
		return nil, err
	}

	return &ProtoBody{
		Txs:      protoTransactions,
		Uncles:   protoUncles,
		Etxs:     protoExtTransactions,
		Manifest: protoManifest,
	}, nil
}

// ProtoDecode deserializes the ProtoBody into the Body format
func (b *Body) ProtoDecode(protoBody *ProtoBody, location common.Location) error {
	if protoBody.Txs == nil {
		return errors.New("missing required field 'Txs' in Body")
	}
	if protoBody.Uncles == nil {
		return errors.New("missing required field 'Uncles' in Body")
	}
	if protoBody.Etxs == nil {
		return errors.New("missing required field 'Etxs' in Body")
	}
	if protoBody.Manifest == nil {
		return errors.New("missing required field 'Manifest' in Body")
	}

	b.Transactions = Transactions{}
	err := b.Transactions.ProtoDecode(protoBody.GetTxs(), location)
	if err != nil {
		return err
	}
	b.ExtTransactions = Transactions{}
	err = b.ExtTransactions.ProtoDecode(protoBody.GetEtxs(), location)
	if err != nil {
		return err
	}
	b.SubManifest = BlockManifest{}
	err = b.SubManifest.ProtoDecode(protoBody.GetManifest())
	if err != nil {
		return err
	}
	b.Uncles = make([]*Header, len(protoBody.GetUncles().GetHeaders()))
	for i, protoUncle := range protoBody.GetUncles().GetHeaders() {
		uncle := &Header{}
		err = uncle.ProtoDecode(protoUncle)
		if err != nil {
			return err
		}
		b.Uncles[i] = uncle
	}
	return nil
}

func (b *Body) QuaiTransactions() []*Transaction {
	quaiTxs := make([]*Transaction, 0)
	for _, t := range b.Transactions {
		if t.Type() != QiTxType {
			quaiTxs = append(quaiTxs, t)
		}
	}
	return quaiTxs
}

// Block represents an entire block in the Quai blockchain.
type Block struct {
	header          *Header
	uncles          []*Header
	transactions    Transactions
	extTransactions Transactions
	subManifest     BlockManifest

	// caches
	size       atomic.Value
	appendTime atomic.Value

	// These fields are used by package eth to track
	// inter-peer block relay.
	ReceivedAt   time.Time
	ReceivedFrom interface{}
}

// "external" block encoding. used for eth protocol, etc.
type extblock struct {
	Header      *Header
	Txs         []*Transaction
	Uncles      []*Header
	Etxs        []*Transaction
	SubManifest BlockManifest
}

func NewBlock(header *Header, txs []*Transaction, uncles []*Header, etxs []*Transaction, subManifest BlockManifest, receipts []*Receipt, hasher TrieHasher, nodeCtx int) *Block {
	b := &Block{header: CopyHeader(header)}

	// TODO: panic if len(txs) != len(receipts)
	if len(txs) == 0 {
		b.header.SetTxHash(EmptyRootHash)
	} else {
		b.header.SetTxHash(DeriveSha(Transactions(txs), hasher))
		b.transactions = make(Transactions, len(txs))
		copy(b.transactions, txs)
	}

	if len(receipts) == 0 {
		b.header.SetReceiptHash(EmptyRootHash)
	} else {
		b.header.SetReceiptHash(DeriveSha(Receipts(receipts), hasher))
	}

	if len(uncles) == 0 {
		b.header.SetUncleHash(EmptyUncleHash)
	} else {
		b.header.SetUncleHash(CalcUncleHash(uncles))
		b.uncles = make([]*Header, len(uncles))
		for i := range uncles {
			b.uncles[i] = CopyHeader(uncles[i])
		}
	}

	if len(etxs) == 0 {
		b.header.SetEtxHash(EmptyRootHash)
	} else {
		b.header.SetEtxHash(DeriveSha(Transactions(etxs), hasher))
		b.extTransactions = make(Transactions, len(etxs))
		copy(b.extTransactions, etxs)
	}

	// Since the subordinate's manifest lives in our body, we still need to check
	// that the manifest matches the subordinate's manifest hash, but we do not set
	// the subordinate's manifest hash.
	subManifestHash := EmptyRootHash
	if len(subManifest) != 0 {
		subManifestHash = DeriveSha(subManifest, hasher)
		b.subManifest = make(BlockManifest, len(subManifest))
		copy(b.subManifest, subManifest)
	}
	if nodeCtx < common.ZONE_CTX && subManifestHash != b.Header().ManifestHash(nodeCtx+1) {
		log.Global.Error("attempted to build block with invalid subordinate manifest")
		return nil
	}

	return b
}

// NewBlockWithHeader creates a block with the given header data. The
// header data is copied, changes to header and to the field values
// will not affect the block.
func NewBlockWithHeader(header *Header) *Block {
	return &Block{header: CopyHeader(header)}
}

// CopyHeader creates a deep copy of a block header to prevent side effects from
// modifying a header variable.
func CopyHeader(h *Header) *Header {
	cpy := *h
	cpy.parentHash = make([]common.Hash, common.HierarchyDepth)
	cpy.manifestHash = make([]common.Hash, common.HierarchyDepth)
	cpy.parentEntropy = make([]*big.Int, common.HierarchyDepth)
	cpy.parentDeltaS = make([]*big.Int, common.HierarchyDepth)
	cpy.number = make([]*big.Int, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		cpy.SetParentHash(h.ParentHash(i), i)
		cpy.SetManifestHash(h.ManifestHash(i), i)
		cpy.SetParentEntropy(h.ParentEntropy(i), i)
		cpy.SetParentDeltaS(h.ParentDeltaS(i), i)
		cpy.SetNumber(h.Number(i), i)
	}
	cpy.SetUncleHash(h.UncleHash())
	cpy.SetCoinbase(h.Coinbase())
	cpy.SetEVMRoot(h.EVMRoot())
	cpy.SetUTXORoot(h.UTXORoot())
	cpy.SetTxHash(h.TxHash())
	cpy.SetEtxHash(h.EtxHash())
	cpy.SetEtxSetHash(h.EtxSetHash())
	cpy.SetEtxRollupHash(h.EtxRollupHash())
	cpy.SetReceiptHash(h.ReceiptHash())
	if len(h.extra) > 0 {
		cpy.extra = make([]byte, len(h.extra))
		copy(cpy.extra, h.extra)
	}
	cpy.SetDifficulty(h.Difficulty())
	cpy.SetGasLimit(h.GasLimit())
	cpy.SetGasUsed(h.GasUsed())
	cpy.SetBaseFee(h.BaseFee())
	cpy.SetLocation(h.location)
	cpy.SetTime(h.time)
	cpy.SetNonce(h.nonce)
	return &cpy
}

// DecodeRLP decodes the Quai RLP encoding into b.
func (b *Block) DecodeRLP(s *rlp.Stream) error {
	var eb extblock
	_, size, _ := s.Kind()
	if err := s.Decode(&eb); err != nil {
		return err
	}
	b.header, b.uncles, b.transactions, b.extTransactions, b.subManifest = eb.Header, eb.Uncles, eb.Txs, eb.Etxs, eb.SubManifest
	b.size.Store(common.StorageSize(rlp.ListSize(size)))
	return nil
}

// EncodeRLP serializes b into the Quai RLP block format.
func (b *Block) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extblock{
		Header:      b.header,
		Txs:         b.transactions,
		Uncles:      b.uncles,
		Etxs:        b.extTransactions,
		SubManifest: b.subManifest,
	})
}

// ProtoEncode serializes h into the Quai Proto Block format
func (b *Block) ProtoEncode() (*ProtoBlock, error) {
	protoHeader, err := b.header.ProtoEncode()
	if err != nil {
		return nil, err
	}
	protoBody, err := b.Body().ProtoEncode()
	if err != nil {
		return nil, err
	}
	protoBlock := &ProtoBlock{
		Header: protoHeader,
		Body:   protoBody,
	}
	return protoBlock, nil
}

// ProtoEncode deserializes th ProtoHeader into the Header format
func (b *Block) ProtoDecode(protoBlock *ProtoBlock, location common.Location) error {
	b.header = &Header{}
	err := b.header.ProtoDecode(protoBlock.GetHeader())
	if err != nil {
		return err
	}
	body := &Body{}
	err = body.ProtoDecode(protoBlock.GetBody(), location)
	if err != nil {
		return err
	}
	b.transactions = body.Transactions
	b.extTransactions = body.ExtTransactions
	b.uncles = body.Uncles
	b.subManifest = body.SubManifest
	return nil
}

// Wrapped header accessors
func (b *Block) ParentHash(nodeCtx int) common.Hash   { return b.header.ParentHash(nodeCtx) }
func (b *Block) UncleHash() common.Hash               { return b.header.UncleHash() }
func (b *Block) Coinbase() common.Address             { return b.header.Coinbase() }
func (b *Block) EVMRoot() common.Hash                 { return b.header.EVMRoot() }
func (b *Block) UTXORoot() common.Hash                { return b.header.UTXORoot() }
func (b *Block) TxHash() common.Hash                  { return b.header.TxHash() }
func (b *Block) EtxHash() common.Hash                 { return b.header.EtxHash() }
func (b *Block) EtxSetHash() common.Hash              { return b.header.EtxSetHash() }
func (b *Block) EtxRollupHash() common.Hash           { return b.header.EtxRollupHash() }
func (b *Block) ManifestHash(nodeCtx int) common.Hash { return b.header.ManifestHash(nodeCtx) }
func (b *Block) ReceiptHash() common.Hash             { return b.header.ReceiptHash() }
func (b *Block) Difficulty(nodeCtx int) *big.Int      { return b.header.Difficulty() }
func (b *Block) ParentEntropy(nodeCtx int) *big.Int   { return b.header.ParentEntropy(nodeCtx) }
func (b *Block) ParentDeltaS(nodeCtx int) *big.Int    { return b.header.ParentDeltaS(nodeCtx) }
func (b *Block) Number(nodeCtx int) *big.Int          { return b.header.Number(nodeCtx) }
func (b *Block) NumberU64(nodeCtx int) uint64         { return b.header.NumberU64(nodeCtx) }
func (b *Block) GasLimit() uint64                     { return b.header.GasLimit() }
func (b *Block) GasUsed() uint64                      { return b.header.GasUsed() }
func (b *Block) BaseFee() *big.Int                    { return b.header.BaseFee() }
func (b *Block) Location() common.Location            { return b.header.Location() }
func (b *Block) Time() uint64                         { return b.header.Time() }
func (b *Block) Extra() []byte                        { return b.header.Extra() }
func (b *Block) Nonce() BlockNonce                    { return b.header.Nonce() }
func (b *Block) NonceU64() uint64                     { return b.header.NonceU64() }

// TODO: copies

func (b *Block) Uncles() []*Header          { return b.uncles }
func (b *Block) Transactions() Transactions { return b.transactions }
func (b *Block) Transaction(hash common.Hash) *Transaction {
	for _, transaction := range b.Transactions() {
		if transaction.Hash() == hash {
			return transaction
		}
	}
	return nil
}
func (b *Block) ExtTransactions() Transactions { return b.extTransactions }
func (b *Block) ExtTransaction(hash common.Hash) *Transaction {
	for _, transaction := range b.ExtTransactions() {
		if transaction.Hash() == hash {
			return transaction
		}
	}
	return nil
}
func (b *Block) SubManifest() BlockManifest { return b.subManifest }

func (b *Block) Header() *Header { return b.header }

func (b *Block) QiTransactions() []*Transaction {
	// TODO: cache the UTXO loop
	qiTxs := make([]*Transaction, 0)
	for _, t := range b.Transactions() {
		if t.Type() == QiTxType {
			qiTxs = append(qiTxs, t)
		}
	}
	return qiTxs
}

func (b *Block) QuaiTransactions() []*Transaction {
	quaiTxs := make([]*Transaction, 0)
	for _, t := range b.Transactions() {
		if t.Type() != QiTxType && (t.To() == nil || t.To().IsInQuaiLedgerScope()) {
			quaiTxs = append(quaiTxs, t)
		}
	}
	return quaiTxs
}

// Body returns the non-header content of the block.
func (b *Block) Body() *Body {
	return &Body{b.transactions, b.uncles, b.extTransactions, b.subManifest}
}

// Size returns the true RLP encoded storage size of the block, either by encoding
// and returning it, or returning a previsouly cached value.
func (b *Block) Size() common.StorageSize {
	if size := b.size.Load(); size != nil {
		return size.(common.StorageSize)
	}
	c := writeCounter(0)
	rlp.Encode(&c, b)
	b.size.Store(common.StorageSize(c))
	return common.StorageSize(c)
}

// SanityCheck can be used to prevent that unbounded fields are
// stuffed with junk data to add processing overhead
func (b *Block) SanityCheck() error {
	return b.header.SanityCheck()
}

type writeCounter common.StorageSize

func (c *writeCounter) Write(b []byte) (int, error) {
	*c += writeCounter(len(b))
	return len(b), nil
}

func CalcUncleHash(uncles []*Header) common.Hash {
	if len(uncles) == 0 {
		return EmptyUncleHash
	}
	return RlpHash(uncles)
}

// WithSeal returns a new block with the data from b but the header replaced with
// the sealed one.
func (b *Block) WithSeal(header *Header) *Block {
	return &Block{
		header:          CopyHeader(header),
		transactions:    b.transactions,
		uncles:          b.uncles,
		extTransactions: b.extTransactions,
		subManifest:     b.subManifest,
	}
}

// WithBody returns a new block with the given transaction and uncle contents, for a single context
func (b *Block) WithBody(transactions []*Transaction, uncles []*Header, extTransactions []*Transaction, subManifest BlockManifest) *Block {
	block := &Block{
		header:          CopyHeader(b.header),
		transactions:    make([]*Transaction, len(transactions)),
		uncles:          make([]*Header, len(uncles)),
		extTransactions: make([]*Transaction, len(extTransactions)),
		subManifest:     make(BlockManifest, len(subManifest)),
	}
	copy(block.transactions, transactions)
	copy(block.extTransactions, extTransactions)
	copy(block.subManifest, subManifest)
	for i := range uncles {
		block.uncles[i] = CopyHeader(uncles[i])
	}
	return block
}

// Hash returns the keccak256 hash of b's header.
// The hash is computed on the first call and cached thereafter.
func (b *Block) Hash() common.Hash {
	return b.header.Hash()
}

// GetAppendTime returns the appendTime of the block
// The appendTime is computed on the first call and cached thereafter.
func (b *Block) GetAppendTime() time.Duration {
	if appendTime := b.appendTime.Load(); appendTime != nil {
		if val, ok := appendTime.(time.Duration); ok {
			return val
		}
	}
	return -1
}

func (b *Block) SetAppendTime(appendTime time.Duration) {
	b.appendTime.Store(appendTime)
}

type Blocks []*Block

// PendingHeader stores the header and termini value associated with the header.
type PendingHeader struct {
	header  *Header `json:"header"`
	termini Termini `json:"termini"`
}

// accessor methods for pending header
func (ph PendingHeader) Header() *Header {
	return ph.header
}
func (ph PendingHeader) Termini() Termini {
	return ph.termini
}

func (ph *PendingHeader) SetHeader(header *Header) {
	ph.header = CopyHeader(header)
}

func (ph *PendingHeader) SetTermini(termini Termini) {
	ph.termini = CopyTermini(termini)
}

func EmptyPendingHeader() PendingHeader {
	pendingHeader := PendingHeader{}
	pendingHeader.SetTermini(EmptyTermini())
	return pendingHeader
}

func NewPendingHeader(header *Header, termini Termini) PendingHeader {
	emptyPh := EmptyPendingHeader()
	emptyPh.SetHeader(header)
	emptyPh.SetTermini(termini)
	return emptyPh
}

func CopyPendingHeader(ph *PendingHeader) *PendingHeader {
	cpy := *ph
	cpy.SetHeader(CopyHeader(ph.Header()))
	cpy.SetTermini(CopyTermini(ph.Termini()))
	return &cpy
}

// ProtoEncode serializes h into the Quai Proto PendingHeader format
func (ph PendingHeader) ProtoEncode() (*ProtoPendingHeader, error) {
	protoHeader, err := ph.Header().ProtoEncode()
	if err != nil {
		return nil, err
	}
	protoTermini := ph.Termini().ProtoEncode()
	return &ProtoPendingHeader{
		Header:  protoHeader,
		Termini: protoTermini,
	}, nil
}

// ProtoEncode deserializes the ProtoHeader into the Header format
func (ph *PendingHeader) ProtoDecode(protoPendingHeader *ProtoPendingHeader) error {
	ph.header = &Header{}
	err := ph.header.ProtoDecode(protoPendingHeader.GetHeader())
	if err != nil {
		return err
	}
	ph.termini = Termini{}
	err = ph.termini.ProtoDecode(protoPendingHeader.GetTermini())
	if err != nil {
		return err
	}
	return nil
}

// "external" pending header encoding. used for rlp
type extPendingHeader struct {
	Header  *Header
	Termini Termini
}

func (t Termini) RPCMarshalTermini() map[string]interface{} {
	result := map[string]interface{}{
		"domTermini": t.DomTermini(),
		"subTermini": t.SubTermini(),
	}
	return result
}

// DecodeRLP decodes the Quai RLP encoding into pending header format.
func (p *PendingHeader) DecodeRLP(s *rlp.Stream) error {
	var eb extPendingHeader
	if err := s.Decode(&eb); err != nil {
		return err
	}
	p.header, p.termini = eb.Header, eb.Termini
	return nil
}

// EncodeRLP serializes b into the Quai RLP format.
func (p PendingHeader) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extPendingHeader{
		Header:  p.header,
		Termini: p.termini,
	})
}

// Termini stores the dom terminus (i.e the previous dom block) and
// subTermini(i.e the dom blocks that have occured in the subordinate chains)
type Termini struct {
	domTermini []common.Hash `json:"domTermini"`
	subTermini []common.Hash `json:"subTermini"`
}

func (t Termini) String() string {
	return fmt.Sprintf("{DomTermini: [%v, %v, %v], SubTermini: [%v, %v, %v]}",
		t.DomTerminiAtIndex(0), t.DomTerminiAtIndex(1), t.DomTerminiAtIndex(2),
		t.SubTerminiAtIndex(0), t.SubTerminiAtIndex(1), t.SubTerminiAtIndex(2),
	)
}

func CopyTermini(termini Termini) Termini {
	newTermini := EmptyTermini()
	for i, t := range termini.domTermini {
		newTermini.SetDomTerminiAtIndex(t, i)
	}
	for i, t := range termini.subTermini {
		newTermini.SetSubTerminiAtIndex(t, i)
	}
	return newTermini
}

func EmptyTermini() Termini {
	termini := Termini{}
	termini.subTermini = make([]common.Hash, common.HierarchyDepth)
	termini.domTermini = make([]common.Hash, common.HierarchyDepth)
	return termini
}

func (t Termini) DomTerminus(nodeLocation common.Location) common.Hash {
	return t.domTermini[nodeLocation.DomIndex(nodeLocation)]
}

func (t Termini) DomTermini() []common.Hash {
	return t.domTermini
}

func (t Termini) SubTermini() []common.Hash {
	return t.subTermini
}

func (t Termini) SubTerminiAtIndex(index int) common.Hash {
	return t.subTermini[index]
}

func (t Termini) DomTerminiAtIndex(index int) common.Hash {
	return t.domTermini[index]
}

func (t *Termini) SetDomTerminiAtIndex(val common.Hash, index int) {
	t.domTermini[index] = val
}

func (t *Termini) SetSubTermini(subTermini []common.Hash) {
	t.subTermini = make([]common.Hash, len(subTermini))
	for i := 0; i < len(subTermini); i++ {
		t.subTermini[i] = subTermini[i]
	}
}

func (t *Termini) SetDomTermini(domTermini []common.Hash) {
	t.domTermini = make([]common.Hash, len(domTermini))
	for i := 0; i < len(domTermini); i++ {
		t.domTermini[i] = domTermini[i]
	}
}

func (t *Termini) SetSubTerminiAtIndex(val common.Hash, index int) {
	t.subTermini[index] = val
}

func (t *Termini) IsValid() bool {
	if t == nil {
		return false
	}
	if len(t.subTermini) != common.NumZonesInRegion {
		return false
	}

	if len(t.domTermini) != common.NumZonesInRegion {
		return false
	}

	return true
}

// "external termini" pending header encoding. used for rlp
type extTermini struct {
	DomTermini []common.Hash
	SubTermini []common.Hash
}

// DecodeRLP decodes the Quai RLP encoding into pending header format.
func (t *Termini) DecodeRLP(s *rlp.Stream) error {
	var et extTermini
	if err := s.Decode(&et); err != nil {
		return err
	}
	t.domTermini, t.subTermini = et.DomTermini, et.SubTermini
	return nil
}

// EncodeRLP serializes b into the Quai RLP format.
func (t Termini) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extTermini{
		DomTermini: t.domTermini,
		SubTermini: t.subTermini,
	})
}

// ProtoEncode serializes t into the Quai Proto Termini format
func (t Termini) ProtoEncode() *ProtoTermini {
	domtermini := make([]*common.ProtoHash, len(t.domTermini))
	for i, hash := range t.domTermini {
		domtermini[i] = hash.ProtoEncode()
	}
	subtermini := make([]*common.ProtoHash, len(t.subTermini))
	for i, hash := range t.subTermini {
		subtermini[i] = hash.ProtoEncode()
	}
	return &ProtoTermini{
		DomTermini: domtermini,
		SubTermini: subtermini,
	}
}

// ProtoDecode deserializes th ProtoTermini into the Termini format
func (t *Termini) ProtoDecode(protoTermini *ProtoTermini) error {
	if protoTermini.DomTermini == nil {
		return errors.New("missing required field 'DomTermini' in Termini")
	}
	if protoTermini.SubTermini == nil {
		return errors.New("missing required field 'SubTermini' in Termini")
	}
	t.domTermini = make([]common.Hash, len(protoTermini.GetDomTermini()))
	for i, protoHash := range protoTermini.GetDomTermini() {
		hash := &common.Hash{}
		hash.ProtoDecode(protoHash)
		t.domTermini[i] = *hash
	}
	t.subTermini = make([]common.Hash, len(protoTermini.GetSubTermini()))
	for i, protoHash := range protoTermini.GetSubTermini() {
		hash := &common.Hash{}
		hash.ProtoDecode(protoHash)
		t.subTermini[i] = *hash
	}
	return nil
}

// BlockManifest is a list of block hashes, which implements DerivableList
type BlockManifest []common.Hash

// Len returns the length of s.
func (m BlockManifest) Len() int { return len(m) }

// EncodeIndex encodes the i'th blockhash to w.
func (m BlockManifest) EncodeIndex(i int, w *bytes.Buffer) {
	rlp.Encode(w, m[i])
}

// Size returns the approximate memory used by all internal contents. It is used
// to approximate and limit the memory consumption of various caches.
func (m BlockManifest) Size() common.StorageSize {
	return common.StorageSize(m.Len() * common.HashLength)
}

// ProtoEncode serializes m into the Quai Proto BlockManifest format
func (m BlockManifest) ProtoEncode() (*ProtoManifest, error) {
	var hashes []*common.ProtoHash
	for _, hash := range m {
		hashes = append(hashes, hash.ProtoEncode())
	}
	return &ProtoManifest{Manifest: hashes}, nil
}

// ProtoDecode deserializes th ProtoManifest into the BlockManifest format
func (m *BlockManifest) ProtoDecode(protoManifest *ProtoManifest) error {
	for _, protoHash := range protoManifest.Manifest {
		hash := &common.Hash{}
		hash.ProtoDecode(protoHash)
		*m = append(*m, *hash)
	}
	return nil
}

type HashAndNumber struct {
	Hash   common.Hash
	Number uint64
}

type HashAndLocation struct {
	Hash     common.Hash
	Location common.Location
}

type BlockRequest struct {
	Hash    common.Hash
	Entropy *big.Int
}
