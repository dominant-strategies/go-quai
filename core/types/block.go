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

// Package types contains data types related to Ethereum consensus.
package types

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
	"lukechampine.com/blake3"
	mathutil "modernc.org/mathutil"
)

var (
	EmptyRootHash  = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	EmptyUncleHash = RlpHash([]*Header(nil))
	EmptyBodyHash  = common.HexToHash("51e1b9c1426a03bf73da3d98d9f384a49ded6a4d705dcdf25433915c3306826c")
	big2e256       = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil) // 2^256
)

const (
	C_mantBits = 64
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

// Header represents a block header in the Ethereum blockchain.
type Header struct {
	parentHash    []common.Hash    `json:"parentHash"           gencodec:"required"`
	uncleHash     []common.Hash    `json:"sha3Uncles"           gencodec:"required"`
	coinbase      []common.Address `json:"miner"                gencodec:"required"`
	root          []common.Hash    `json:"stateRoot"            gencodec:"required"`
	txHash        []common.Hash    `json:"transactionsRoot"     gencodec:"required"`
	etxHash       []common.Hash    `json:"extTransactionsRoot"  gencodec:"required"`
	etxRollupHash []common.Hash    `json:"extRollupRoot"        gencodec:"required"`
	manifestHash  []common.Hash    `json:"manifestHash"         gencodec:"required"`
	receiptHash   []common.Hash    `json:"receiptsRoot"         gencodec:"required"`
	bloom         []Bloom          `json:"logsBloom"            gencodec:"required"`
	difficulty    *big.Int         `json:"difficulty"           gencodec:"required"`
	parentEntropy []*big.Int       `json:"parentEntropy"		gencodec:"required"`
	parentDeltaS  []*big.Int       `json:"parentDeltaS"			gencodec:"required"`
	number        []*big.Int       `json:"number"               gencodec:"required"`
	gasLimit      []uint64         `json:"gasLimit"             gencodec:"required"`
	gasUsed       []uint64         `json:"gasUsed"              gencodec:"required"`
	baseFee       []*big.Int       `json:"baseFeePerGas"        gencodec:"required"`
	location      common.Location  `json:"location"             gencodec:"required"`
	time          uint64           `json:"timestamp"            gencodec:"required"`
	extra         []byte           `json:"extraData"            gencodec:"required"`
	nonce         BlockNonce       `json:"nonce"`
}

// field type overrides for gencodec
type headerMarshaling struct {
	Difficulty    *hexutil.Big
	Number        []*hexutil.Big
	GasLimit      []hexutil.Uint64
	GasUsed       []hexutil.Uint64
	BaseFee       []*hexutil.Big
	ParentEntropy []*hexutil.Big
	ParentDeltaS  []*hexutil.Big
	Time          hexutil.Uint64
	Extra         hexutil.Bytes
	Hash          common.Hash `json:"hash"` // adds call to Hash() in MarshalJSON
}

// "external" header encoding. used for eth protocol, etc.
type extheader struct {
	ParentHash    []common.Hash
	UncleHash     []common.Hash
	Coinbase      []common.Address
	Root          []common.Hash
	TxHash        []common.Hash
	EtxHash       []common.Hash
	EtxRollupHash []common.Hash
	ManifestHash  []common.Hash
	ReceiptHash   []common.Hash
	Bloom         []Bloom
	Difficulty    *big.Int
	ParentEntropy []*big.Int
	ParentDeltaS  []*big.Int
	Number        []*big.Int
	GasLimit      []uint64
	GasUsed       []uint64
	BaseFee       []*big.Int
	Location      common.Location
	Time          uint64
	Extra         []byte
	Nonce         BlockNonce
}

// Construct an empty header
func EmptyHeader() *Header {
	h := &Header{}
	h.parentHash = make([]common.Hash, common.HierarchyDepth)
	h.uncleHash = make([]common.Hash, common.HierarchyDepth)
	h.coinbase = make([]common.Address, common.HierarchyDepth)
	h.root = make([]common.Hash, common.HierarchyDepth)
	h.txHash = make([]common.Hash, common.HierarchyDepth)
	h.etxHash = make([]common.Hash, common.HierarchyDepth)
	h.etxRollupHash = make([]common.Hash, common.HierarchyDepth)
	h.manifestHash = make([]common.Hash, common.HierarchyDepth)
	h.receiptHash = make([]common.Hash, common.HierarchyDepth)
	h.bloom = make([]Bloom, common.HierarchyDepth)
	h.parentEntropy = make([]*big.Int, common.HierarchyDepth)
	h.parentDeltaS = make([]*big.Int, common.HierarchyDepth)
	h.number = make([]*big.Int, common.HierarchyDepth)
	h.gasLimit = make([]uint64, common.HierarchyDepth)
	h.gasUsed = make([]uint64, common.HierarchyDepth)
	h.baseFee = make([]*big.Int, common.HierarchyDepth)
	h.difficulty = big.NewInt(0)

	for i := 0; i < common.HierarchyDepth; i++ {
		h.root[i] = EmptyRootHash
		h.txHash[i] = EmptyRootHash
		h.receiptHash[i] = EmptyRootHash
		h.etxHash[i] = EmptyRootHash
		h.etxRollupHash[i] = EmptyRootHash
		h.manifestHash[i] = EmptyRootHash
		h.uncleHash[i] = EmptyUncleHash
		h.parentEntropy[i] = big.NewInt(0)
		h.parentDeltaS[i] = big.NewInt(0)
		h.number[i] = big.NewInt(0)
		h.baseFee[i] = big.NewInt(0)
	}
	return h
}

// DecodeRLP decodes the Ethereum
func (h *Header) DecodeRLP(s *rlp.Stream) error {
	var eh extheader
	if err := s.Decode(&eh); err != nil {
		return err
	}
	h.parentHash = eh.ParentHash
	h.uncleHash = eh.UncleHash
	h.coinbase = eh.Coinbase
	h.root = eh.Root
	h.txHash = eh.TxHash
	h.etxHash = eh.EtxHash
	h.etxRollupHash = eh.EtxRollupHash
	h.manifestHash = eh.ManifestHash
	h.receiptHash = eh.ReceiptHash
	h.bloom = eh.Bloom
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
	h.nonce = eh.Nonce

	return nil
}

// EncodeRLP serializes b into the Ethereum RLP block format.
func (h *Header) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extheader{
		ParentHash:    h.parentHash,
		UncleHash:     h.uncleHash,
		Coinbase:      h.coinbase,
		Root:          h.root,
		TxHash:        h.txHash,
		EtxHash:       h.etxHash,
		EtxRollupHash: h.etxRollupHash,
		ManifestHash:  h.manifestHash,
		ReceiptHash:   h.receiptHash,
		Bloom:         h.bloom,
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
		Nonce:         h.nonce,
	})
}

// RPCMarshalHeader converts the given header to the RPC output .
func (h *Header) RPCMarshalHeader() map[string]interface{} {
	result := map[string]interface{}{
		"hash":                h.Hash(),
		"parentHash":          h.ParentHashArray(),
		"difficulty":          (*hexutil.Big)(h.Difficulty()),
		"nonce":               h.Nonce(),
		"sha3Uncles":          h.UncleHashArray(),
		"logsBloom":           h.BloomArray(),
		"stateRoot":           h.RootArray(),
		"miner":               h.CoinbaseArray(),
		"extraData":           hexutil.Bytes(h.Extra()),
		"size":                hexutil.Uint64(h.Size()),
		"timestamp":           hexutil.Uint64(h.Time()),
		"transactionsRoot":    h.TxHashArray(),
		"receiptsRoot":        h.ReceiptHashArray(),
		"extTransactionsRoot": h.EtxHashArray(),
		"extRollupRoot":       h.EtxRollupHashArray(),
		"manifestHash":        h.ManifestHashArray(),
		"location":            h.Location(),
	}

	number := make([]*hexutil.Big, common.HierarchyDepth)
	parentEntropy := make([]*hexutil.Big, common.HierarchyDepth)
	parentDeltaS := make([]*hexutil.Big, common.HierarchyDepth)
	gasLimit := make([]hexutil.Uint, common.HierarchyDepth)
	gasUsed := make([]hexutil.Uint, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		number[i] = (*hexutil.Big)(h.Number(i))
		parentEntropy[i] = (*hexutil.Big)(h.ParentEntropy(i))
		parentDeltaS[i] = (*hexutil.Big)(h.ParentDeltaS(i))
		gasLimit[i] = hexutil.Uint(h.GasLimit(i))
		gasUsed[i] = hexutil.Uint(h.GasUsed(i))
	}
	result["number"] = number
	result["parentEntropy"] = parentEntropy
	result["parentDeltaS"] = parentDeltaS
	result["gasLimit"] = gasLimit
	result["gasUsed"] = gasUsed

	if h.BaseFee() != nil {
		results := make([]*hexutil.Big, common.HierarchyDepth)
		for i := 0; i < common.HierarchyDepth; i++ {
			results[i] = (*hexutil.Big)(h.BaseFee(i))
		}
		result["baseFeePerGas"] = results
	}

	return result
}

// Localized accessors
func (h *Header) ParentHash(args ...int) common.Hash {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.parentHash[nodeCtx]
}
func (h *Header) UncleHash(args ...int) common.Hash {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.uncleHash[nodeCtx]
}
func (h *Header) Coinbase(args ...int) common.Address {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.coinbase[nodeCtx]
}
func (h *Header) Root(args ...int) common.Hash {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.root[nodeCtx]
}
func (h *Header) TxHash(args ...int) common.Hash {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.txHash[nodeCtx]
}
func (h *Header) EtxHash(args ...int) common.Hash {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.etxHash[nodeCtx]
}
func (h *Header) EtxRollupHash(args ...int) common.Hash {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.etxRollupHash[nodeCtx]
}

func (h *Header) ParentEntropy(args ...int) *big.Int {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.parentEntropy[nodeCtx]
}

func (h *Header) ParentDeltaS(args ...int) *big.Int {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.parentDeltaS[nodeCtx]
}

func (h *Header) ManifestHash(args ...int) common.Hash {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.manifestHash[nodeCtx]
}
func (h *Header) ReceiptHash(args ...int) common.Hash {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.receiptHash[nodeCtx]
}
func (h *Header) Bloom(args ...int) Bloom {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.bloom[nodeCtx]
}
func (h *Header) Difficulty() *big.Int {
	return h.difficulty
}
func (h *Header) Number(args ...int) *big.Int {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.number[nodeCtx]
}
func (h *Header) NumberU64(args ...int) uint64 {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.number[nodeCtx].Uint64()
}
func (h *Header) GasLimit(args ...int) uint64 {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.gasLimit[nodeCtx]
}
func (h *Header) GasUsed(args ...int) uint64 {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.gasUsed[nodeCtx]
}
func (h *Header) BaseFee(args ...int) *big.Int {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.baseFee[nodeCtx]
}
func (h *Header) Location() common.Location { return h.location }
func (h *Header) Time() uint64              { return h.time }
func (h *Header) Extra() []byte             { return common.CopyBytes(h.extra) }
func (h *Header) Nonce() BlockNonce         { return h.nonce }
func (h *Header) NonceU64() uint64          { return binary.BigEndian.Uint64(h.nonce[:]) }

func (h *Header) SetParentHash(val common.Hash, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.parentHash[nodeCtx] = val
}
func (h *Header) SetUncleHash(val common.Hash, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.uncleHash[nodeCtx] = val
}
func (h *Header) SetCoinbase(val common.Address, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.coinbase[nodeCtx] = val
}
func (h *Header) SetRoot(val common.Hash, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.root[nodeCtx] = val
}
func (h *Header) SetTxHash(val common.Hash, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.txHash[nodeCtx] = val
}
func (h *Header) SetEtxHash(val common.Hash, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.etxHash[nodeCtx] = val
}
func (h *Header) SetEtxRollupHash(val common.Hash, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.etxRollupHash[nodeCtx] = val
}

func (h *Header) SetParentEntropy(val *big.Int, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.parentEntropy[nodeCtx] = val
}

func (h *Header) SetParentDeltaS(val *big.Int, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.parentDeltaS[nodeCtx] = val
}

func (h *Header) SetManifestHash(val common.Hash, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.manifestHash[nodeCtx] = val
}
func (h *Header) SetReceiptHash(val common.Hash, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.receiptHash[nodeCtx] = val
}
func (h *Header) SetBloom(val Bloom, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.bloom[nodeCtx] = val
}
func (h *Header) SetDifficulty(val *big.Int) {
	h.difficulty = new(big.Int).Set(val)
}
func (h *Header) SetNumber(val *big.Int, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.number[nodeCtx] = new(big.Int).Set(val)
}
func (h *Header) SetGasLimit(val uint64, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.gasLimit[nodeCtx] = val
}
func (h *Header) SetGasUsed(val uint64, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.gasUsed[nodeCtx] = val
}
func (h *Header) SetBaseFee(val *big.Int, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.baseFee[nodeCtx] = new(big.Int).Set(val)
}
func (h *Header) SetLocation(val common.Location) { h.location = val }
func (h *Header) SetTime(val uint64)              { h.time = val }
func (h *Header) SetExtra(val []byte) {
	if len(val) > 0 {
		h.extra = make([]byte, len(val))
		copy(h.extra, val)
	}
}
func (h *Header) SetNonce(val BlockNonce) { h.nonce = val }

// Array accessors
func (h *Header) ParentHashArray() []common.Hash    { return h.parentHash }
func (h *Header) UncleHashArray() []common.Hash     { return h.uncleHash }
func (h *Header) CoinbaseArray() []common.Address   { return h.coinbase }
func (h *Header) RootArray() []common.Hash          { return h.root }
func (h *Header) TxHashArray() []common.Hash        { return h.txHash }
func (h *Header) EtxHashArray() []common.Hash       { return h.etxHash }
func (h *Header) EtxRollupHashArray() []common.Hash { return h.etxRollupHash }
func (h *Header) ManifestHashArray() []common.Hash  { return h.manifestHash }
func (h *Header) ReceiptHashArray() []common.Hash   { return h.receiptHash }
func (h *Header) BloomArray() []Bloom               { return h.bloom }
func (h *Header) NumberArray() []*big.Int           { return h.number }
func (h *Header) GasLimitArray() []uint64           { return h.gasLimit }
func (h *Header) GasUsedArray() []uint64            { return h.gasUsed }
func (h *Header) BaseFeeArray() []*big.Int          { return h.baseFee }

// headerData comprises all data fields of the header, excluding the nonce, so
// that the nonce may be independently adjusted in the work algorithm.
type sealData struct {
	ParentHash    []common.Hash
	UncleHash     []common.Hash
	Coinbase      []common.Address
	Root          []common.Hash
	TxHash        []common.Hash
	EtxHash       []common.Hash
	EtxRollupHash []common.Hash
	ManifestHash  []common.Hash
	ReceiptHash   []common.Hash
	Bloom         []Bloom
	Number        []*big.Int
	GasLimit      []uint64
	GasUsed       []uint64
	BaseFee       []*big.Int
	Difficulty    *big.Int
	Location      common.Location
	Time          uint64
	Extra         []byte
	Nonce         BlockNonce
}

// SealHash returns the hash of a block prior to it being sealed.
func (h *Header) SealHash() (hash common.Hash) {
	hasher := blake3.New(32, nil)
	hasher.Reset()
	hdata := sealData{
		ParentHash:    make([]common.Hash, common.HierarchyDepth),
		UncleHash:     make([]common.Hash, common.HierarchyDepth),
		Coinbase:      make([]common.Address, common.HierarchyDepth),
		Root:          make([]common.Hash, common.HierarchyDepth),
		TxHash:        make([]common.Hash, common.HierarchyDepth),
		EtxHash:       make([]common.Hash, common.HierarchyDepth),
		EtxRollupHash: make([]common.Hash, common.HierarchyDepth),
		ManifestHash:  make([]common.Hash, common.HierarchyDepth),
		ReceiptHash:   make([]common.Hash, common.HierarchyDepth),
		Bloom:         make([]Bloom, common.HierarchyDepth),
		Number:        make([]*big.Int, common.HierarchyDepth),
		GasLimit:      make([]uint64, common.HierarchyDepth),
		GasUsed:       make([]uint64, common.HierarchyDepth),
		BaseFee:       make([]*big.Int, common.HierarchyDepth),
		Difficulty:    h.Difficulty(),
		Location:      h.Location(),
		Time:          h.Time(),
		Extra:         h.Extra(),
	}
	for i := 0; i < common.HierarchyDepth; i++ {
		hdata.ParentHash[i] = h.ParentHash(i)
		hdata.UncleHash[i] = h.UncleHash(i)
		hdata.Coinbase[i] = h.Coinbase(i)
		hdata.Root[i] = h.Root(i)
		hdata.TxHash[i] = h.TxHash(i)
		hdata.EtxHash[i] = h.EtxHash(i)
		hdata.EtxRollupHash[i] = h.EtxRollupHash(i)
		hdata.ManifestHash[i] = h.ManifestHash(i)
		hdata.ReceiptHash[i] = h.ReceiptHash(i)
		hdata.Bloom[i] = h.Bloom(i)
		hdata.Number[i] = h.Number(i)
		hdata.GasLimit[i] = h.GasLimit(i)
		hdata.GasUsed[i] = h.GasUsed(i)
		hdata.BaseFee[i] = h.BaseFee(i)
	}
	rlp.Encode(hasher, hdata)
	hash.SetBytes(hasher.Sum(hash[:0]))
	return hash
}

// Hash returns the nonce'd hash of the header. This is just the Blake3 hash of
// SealHash suffixed with a nonce.
func (h *Header) Hash() (hash common.Hash) {
	hasher := blake3.New(32, nil)
	hasher.Reset()
	var hData [40]byte
	copy(hData[:], h.Nonce().Bytes())
	copy(hData[len(h.nonce):], h.SealHash().Bytes())
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
	if h.uncleHash == nil || len(h.uncleHash) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: uncleHash")
	}
	if h.coinbase == nil || len(h.coinbase) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: coinbase")
	}
	if h.root == nil || len(h.root) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: root")
	}
	if h.txHash == nil || len(h.txHash) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: txHash")
	}
	if h.receiptHash == nil || len(h.receiptHash) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: receiptHash")
	}
	if h.etxHash == nil || len(h.etxHash) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: etxHash")
	}
	if h.manifestHash == nil || len(h.manifestHash) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: manifestHash")
	}
	if h.bloom == nil || len(h.bloom) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: bloom")
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
	if h.gasLimit == nil || len(h.gasLimit) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: gasLimit")
	}
	if h.gasUsed == nil || len(h.gasUsed) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: gasUsed")
	}
	if h.baseFee == nil || len(h.baseFee) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: baseFee")
	}
	for i := 0; i < common.HierarchyDepth; i++ {
		if h.number == nil {
			return fmt.Errorf("field cannot be `nil`: number[%d]", i)
		}
		if h.difficulty == nil {
			return fmt.Errorf("field cannot be `nil`: difficulty[%d]", i)
		}
		if h.baseFee == nil {
			return fmt.Errorf("field cannot be `nil`: baseFee[%d]", i)
		}
		if h.number[i] != nil && !h.number[i].IsUint64() {
			return fmt.Errorf("too large block number[%d]: bitlen %d", i, h.number[i].BitLen())
		}
		if h.baseFee[i] != nil {
			if bfLen := h.baseFee[i].BitLen(); bfLen > 256 {
				return fmt.Errorf("too large base fee: bitlen %d", bfLen)
			}
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
func (h *Header) EmptyBody() bool {
	return h.EmptyTxs() && h.EmptyUncles() && h.EmptyEtxs() && h.EmptyManifest()
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
func (h *Header) EmptyManifest() bool {
	return h.ManifestHash() == EmptyRootHash
}

// EmptyUncles returns true if there are no uncles for this header/block.
func (h *Header) EmptyUncles() bool {
	return h.UncleHash() == EmptyRootHash
}

// EmptyReceipts returns true if there are no receipts for this header/block.
func (h *Header) EmptyReceipts() bool {
	return h.ReceiptHash() == EmptyRootHash
}

func (h *Header) CalcS() *big.Int {
	order, err := h.CalcOrder()
	if err != nil {
		return big.NewInt(0)
	}
	intrinsicS := h.CalcIntrinsicS()
	switch order {
	case common.PRIME_CTX:
		totalS := new(big.Int).Add(h.ParentEntropy(common.PRIME_CTX), h.ParentDeltaS(common.REGION_CTX))
		totalS.Add(totalS, h.ParentDeltaS(common.ZONE_CTX))
		totalS.Add(totalS, intrinsicS)
		return totalS
	case common.REGION_CTX:
		totalS := new(big.Int).Add(h.ParentEntropy(common.REGION_CTX), h.ParentDeltaS(common.ZONE_CTX))
		totalS.Add(totalS, intrinsicS)
		return totalS
	case common.ZONE_CTX:
		totalS := new(big.Int).Add(h.ParentEntropy(common.ZONE_CTX), intrinsicS)
		return totalS
	}
	return big.NewInt(0)
}

func (h *Header) CalcPhS() *big.Int {
	switch common.NodeLocation.Context() {
	case common.PRIME_CTX:
		totalS := h.ParentEntropy(common.PRIME_CTX)
		return totalS
	case common.REGION_CTX:
		totalS := new(big.Int).Add(h.ParentEntropy(common.PRIME_CTX), h.ParentDeltaS(common.REGION_CTX))
		return totalS
	case common.ZONE_CTX:
		totalS := new(big.Int).Add(h.ParentEntropy(common.PRIME_CTX), h.ParentDeltaS(common.REGION_CTX))
		totalS.Add(totalS, h.ParentDeltaS(common.ZONE_CTX))
		return totalS
	}
	return big.NewInt(0)
}

func (h *Header) CalcDeltaS() *big.Int {
	order, err := h.CalcOrder()
	if err != nil {
		return big.NewInt(0)
	}
	intrinsicS := h.CalcIntrinsicS()
	switch order {
	case common.PRIME_CTX:
		return big.NewInt(0)
	case common.REGION_CTX:
		totalDeltaS := new(big.Int).Add(h.ParentDeltaS(common.REGION_CTX), h.ParentDeltaS(common.ZONE_CTX))
		totalDeltaS = new(big.Int).Add(totalDeltaS, intrinsicS)
		return totalDeltaS
	case common.ZONE_CTX:
		totalDeltaS := new(big.Int).Add(h.ParentDeltaS(common.ZONE_CTX), intrinsicS)
		return totalDeltaS
	}
	return big.NewInt(0)
}

func (h *Header) CalcOrder() (int, error) {
	intrinsicS := h.CalcIntrinsicS()

	// This is the updated the threshold calculation based on the zone difficulty threshold
	zoneThresholdS := h.CalcIntrinsicS(common.BytesToHash(new(big.Int).Div(big2e256, h.Difficulty()).Bytes()))
	timeFactorHierarchyDepthMultiple := new(big.Int).Mul(params.TimeFactor, big.NewInt(common.HierarchyDepth))

	// Prime case
	primeEntropyThreshold := new(big.Int).Mul(timeFactorHierarchyDepthMultiple, timeFactorHierarchyDepthMultiple)
	primeEntropyThreshold = new(big.Int).Mul(primeEntropyThreshold, zoneThresholdS)
	primeBlockThreshold := new(big.Int).Quo(primeEntropyThreshold, big.NewInt(2))
	primeEntropyThreshold = new(big.Int).Sub(primeEntropyThreshold, primeBlockThreshold)

	primeBlockEntropyThresholdAdder, _ := mathutil.BinaryLog(primeBlockThreshold, 8)
	primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, big.NewInt(int64(primeBlockEntropyThresholdAdder)))

	totalDeltaS := new(big.Int).Add(h.ParentDeltaS(common.REGION_CTX), h.ParentDeltaS(common.ZONE_CTX))
	totalDeltaS.Add(totalDeltaS, intrinsicS)
	if intrinsicS.Cmp(primeBlockEntropyThreshold) > 0 && totalDeltaS.Cmp(primeEntropyThreshold) > 0 {
		return common.PRIME_CTX, nil
	}

	// Region case
	regionEntropyThreshold := new(big.Int).Mul(timeFactorHierarchyDepthMultiple, zoneThresholdS)
	regionBlockThreshold := new(big.Int).Quo(regionEntropyThreshold, big.NewInt(2))
	regionEntropyThreshold = new(big.Int).Sub(regionEntropyThreshold, regionBlockThreshold)

	regionBlockEntropyThresholdAdder, _ := mathutil.BinaryLog(regionBlockThreshold, 8)
	regionBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, big.NewInt(int64(regionBlockEntropyThresholdAdder)))

	totalDeltaS = new(big.Int).Add(h.ParentDeltaS(common.ZONE_CTX), intrinsicS)
	if intrinsicS.Cmp(regionBlockEntropyThreshold) > 0 && totalDeltaS.Cmp(regionEntropyThreshold) > 0 {
		return common.REGION_CTX, nil
	}

	// Zone case
	if intrinsicS.Cmp(zoneThresholdS) > 0 {
		return common.ZONE_CTX, nil
	}
	return -1, errors.New("invalid order")
}

// calcIntrinsicS
func (h *Header) CalcIntrinsicS(args ...common.Hash) *big.Int {
	hash := h.Hash()
	if len(args) > 0 {
		hash = args[0]
	}
	x := new(big.Int).SetBytes(hash.Bytes())
	d := new(big.Int).Div(big2e256, x)
	c, m := mathutil.BinaryLog(d, C_mantBits)
	bigBits := new(big.Int).Mul(big.NewInt(int64(c)), new(big.Int).Exp(big.NewInt(2), big.NewInt(C_mantBits), nil))
	bigBits = new(big.Int).Add(bigBits, m)
	return bigBits
}

// Body is a simple (mutable, non-safe) data container for storing and moving
// a block's data contents (transactions and uncles) together.
type Body struct {
	Transactions    []*Transaction
	Uncles          []*Header
	ExtTransactions []*Transaction
	SubManifest     BlockManifest
}

// Block represents an entire block in the Ethereum blockchain.
type Block struct {
	header          *Header
	uncles          []*Header
	transactions    Transactions
	extTransactions Transactions
	subManifest     BlockManifest

	// caches
	hash atomic.Value
	size atomic.Value

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

func NewBlock(header *Header, txs []*Transaction, uncles []*Header, etxs []*Transaction, subManifest BlockManifest, receipts []*Receipt, hasher TrieHasher) *Block {
	nodeCtx := common.NodeLocation.Context()
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
		b.header.SetBloom(CreateBloom(receipts))
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
		log.Error("attempted to build block with invalid subordinate manifest")
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
	cpy.uncleHash = make([]common.Hash, common.HierarchyDepth)
	cpy.coinbase = make([]common.Address, common.HierarchyDepth)
	cpy.root = make([]common.Hash, common.HierarchyDepth)
	cpy.txHash = make([]common.Hash, common.HierarchyDepth)
	cpy.etxHash = make([]common.Hash, common.HierarchyDepth)
	cpy.etxRollupHash = make([]common.Hash, common.HierarchyDepth)
	cpy.manifestHash = make([]common.Hash, common.HierarchyDepth)
	cpy.receiptHash = make([]common.Hash, common.HierarchyDepth)
	cpy.bloom = make([]Bloom, common.HierarchyDepth)
	cpy.parentEntropy = make([]*big.Int, common.HierarchyDepth)
	cpy.parentDeltaS = make([]*big.Int, common.HierarchyDepth)
	cpy.number = make([]*big.Int, common.HierarchyDepth)
	cpy.gasLimit = make([]uint64, common.HierarchyDepth)
	cpy.gasUsed = make([]uint64, common.HierarchyDepth)
	cpy.baseFee = make([]*big.Int, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		cpy.SetParentHash(h.ParentHash(i), i)
		cpy.SetUncleHash(h.UncleHash(i), i)
		cpy.SetCoinbase(h.Coinbase(i), i)
		cpy.SetRoot(h.Root(i), i)
		cpy.SetTxHash(h.TxHash(i), i)
		cpy.SetEtxHash(h.EtxHash(i), i)
		cpy.SetEtxRollupHash(h.EtxRollupHash(i), i)
		cpy.SetManifestHash(h.ManifestHash(i), i)
		cpy.SetReceiptHash(h.ReceiptHash(i), i)
		cpy.SetBloom(h.Bloom(i), i)
		cpy.SetParentEntropy(h.ParentEntropy(i), i)
		cpy.SetParentDeltaS(h.ParentDeltaS(i), i)
		cpy.SetNumber(h.Number(i), i)
		cpy.SetGasLimit(h.GasLimit(i), i)
		cpy.SetGasUsed(h.GasUsed(i), i)
		cpy.SetBaseFee(h.BaseFee(i), i)
	}
	if len(h.extra) > 0 {
		cpy.extra = make([]byte, len(h.extra))
		copy(cpy.extra, h.extra)
	}
	cpy.SetDifficulty(h.Difficulty())
	cpy.SetLocation(h.location)
	cpy.SetTime(h.time)
	cpy.SetNonce(h.nonce)
	return &cpy
}

// DecodeRLP decodes the Ethereum
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

// EncodeRLP serializes b into the Ethereum RLP block format.
func (b *Block) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extblock{
		Header:      b.header,
		Txs:         b.transactions,
		Uncles:      b.uncles,
		Etxs:        b.extTransactions,
		SubManifest: b.subManifest,
	})
}

// Wrapped header accessors
func (b *Block) ParentHash(args ...int) common.Hash    { return b.header.ParentHash(args...) }
func (b *Block) UncleHash(args ...int) common.Hash     { return b.header.UncleHash(args...) }
func (b *Block) Coinbase(args ...int) common.Address   { return b.header.Coinbase(args...) }
func (b *Block) Root(args ...int) common.Hash          { return b.header.Root(args...) }
func (b *Block) TxHash(args ...int) common.Hash        { return b.header.TxHash(args...) }
func (b *Block) EtxHash(args ...int) common.Hash       { return b.header.EtxHash(args...) }
func (b *Block) EtxRollupHash(args ...int) common.Hash { return b.header.EtxRollupHash(args...) }
func (b *Block) ManifestHash(args ...int) common.Hash  { return b.header.ManifestHash(args...) }
func (b *Block) ReceiptHash(args ...int) common.Hash   { return b.header.ReceiptHash(args...) }
func (b *Block) Bloom(args ...int) Bloom               { return b.header.Bloom(args...) }
func (b *Block) Difficulty(args ...int) *big.Int       { return b.header.Difficulty() }
func (b *Block) ParentEntropy(args ...int) *big.Int    { return b.header.ParentEntropy(args...) }
func (b *Block) ParentDeltaS(args ...int) *big.Int     { return b.header.ParentDeltaS(args...) }
func (b *Block) Number(args ...int) *big.Int           { return b.header.Number(args...) }
func (b *Block) NumberU64(args ...int) uint64          { return b.header.NumberU64(args...) }
func (b *Block) GasLimit(args ...int) uint64           { return b.header.GasLimit(args...) }
func (b *Block) GasUsed(args ...int) uint64            { return b.header.GasUsed(args...) }
func (b *Block) BaseFee(args ...int) *big.Int          { return b.header.BaseFee(args...) }
func (b *Block) Location() common.Location             { return b.header.Location() }
func (b *Block) Time() uint64                          { return b.header.Time() }
func (b *Block) Extra() []byte                         { return b.header.Extra() }
func (b *Block) Nonce() BlockNonce                     { return b.header.Nonce() }
func (b *Block) NonceU64() uint64                      { return b.header.NonceU64() }

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
	if hash := b.hash.Load(); hash != nil {
		return hash.(common.Hash)
	}
	v := b.header.Hash()
	b.hash.Store(v)
	return v
}

type Blocks []*Block

// PendingHeader stores the header and termini value associated with the header.
type PendingHeader struct {
	Header  *Header
	Termini []common.Hash
	Entropy *big.Int
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

type HashAndNumber struct {
	Hash   common.Hash
	Number uint64
}
