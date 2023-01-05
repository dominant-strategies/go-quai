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
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rlp"
)

var (
	EmptyRootHash  = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	EmptyUncleHash = rlpHash([]*Header(nil))
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
	difficulty    []*big.Int       `json:"difficulty"           gencodec:"required"`
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
	Difficulty []*hexutil.Big
	Number     []*hexutil.Big
	GasLimit   []hexutil.Uint64
	GasUsed    []hexutil.Uint64
	BaseFee    []*hexutil.Big
	Time       hexutil.Uint64
	Extra      hexutil.Bytes
	Hash       common.Hash `json:"hash"` // adds call to Hash() in MarshalJSON
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
	Difficulty    []*big.Int
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
	h.difficulty = make([]*big.Int, common.HierarchyDepth)
	h.number = make([]*big.Int, common.HierarchyDepth)
	h.gasLimit = make([]uint64, common.HierarchyDepth)
	h.gasUsed = make([]uint64, common.HierarchyDepth)
	h.baseFee = make([]*big.Int, common.HierarchyDepth)

	for i := 0; i < common.HierarchyDepth; i++ {
		h.root[i] = EmptyRootHash
		h.txHash[i] = EmptyRootHash
		h.receiptHash[i] = EmptyRootHash
		h.etxHash[i] = EmptyRootHash
		h.etxRollupHash[i] = EmptyRootHash
		h.manifestHash[i] = EmptyRootHash
		h.uncleHash[i] = EmptyUncleHash
		h.difficulty[i] = big.NewInt(0)
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
func (h *Header) Difficulty(args ...int) *big.Int {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	return h.difficulty[nodeCtx]
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
func (h *Header) SetDifficulty(val *big.Int, args ...int) {
	nodeCtx := common.NodeLocation.Context()
	if len(args) > 0 {
		nodeCtx = args[0]
	}
	h.difficulty[nodeCtx] = new(big.Int).Set(val)
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
func (h *Header) DifficultyArray() []*big.Int       { return h.difficulty }
func (h *Header) NumberArray() []*big.Int           { return h.number }
func (h *Header) GasLimitArray() []uint64           { return h.gasLimit }
func (h *Header) GasUsedArray() []uint64            { return h.gasUsed }
func (h *Header) BaseFeeArray() []*big.Int          { return h.baseFee }

// Hash returns the block hash of the header, which is simply the keccak256 hash of its
// RLP encoding.
func (h *Header) Hash() common.Hash {
	return rlpHash(h)
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
	return headerSize + common.StorageSize(len(h.extra)+(totalBitLen(h.difficulty)+totalBitLen(h.number))/8)
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
	if h.difficulty == nil || len(h.difficulty) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: difficulty")
	}
	if h.number == nil || len(h.number) != common.HierarchyDepth {
		return fmt.Errorf("field cannot be `nil`: number")
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
		if h.difficulty[i] != nil {
			if diffLen := h.difficulty[i].BitLen(); diffLen > 80 {
				return fmt.Errorf("too large block difficulty[%d]: bitlen %d", i, diffLen)
			}
		}
		if h.baseFee[i] != nil {
			if bfLen := h.baseFee[i].BitLen(); bfLen > 256 {
				return fmt.Errorf("too large base fee: bitlen %d", bfLen)
			}
		}
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

	// Td is used by package core to store the total difficulty
	// of the chain up to and including the block.
	td *big.Int

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
	SubManifest []common.Hash
}

func NewBlock(header *Header, txs []*Transaction, uncles []*Header, etxs []*Transaction, subManifest BlockManifest, receipts []*Receipt, hasher TrieHasher) *Block {
	nodeCtx := common.NodeLocation.Context()
	b := &Block{header: CopyHeader(header), td: new(big.Int)}

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
	cpy.difficulty = make([]*big.Int, common.HierarchyDepth)
	cpy.number = make([]*big.Int, common.HierarchyDepth)
	cpy.baseFee = make([]*big.Int, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		cpy.SetDifficulty(h.Difficulty(i), i)
		cpy.SetNumber(h.Number(i), i)
		cpy.SetBaseFee(h.BaseFee(i), i)
	}
	if len(h.extra) > 0 {
		cpy.extra = make([]byte, len(h.extra))
		copy(cpy.extra, h.extra)
	}
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
func (b *Block) Difficulty(args ...int) *big.Int       { return b.header.Difficulty(args...) }
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

func (b *Block) Header() *Header { return CopyHeader(b.header) }

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
	return rlpHash(uncles)
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
}

type HeaderRoots struct {
	StateRoot    common.Hash
	TxsRoot      common.Hash
	ReceiptsRoot common.Hash
}

// BlockManifest is a list of block hashes, which implements DerivableList
type BlockManifest []common.Hash

// Len returns the length of s.
func (m BlockManifest) Len() int { return len(m) }

// EncodeIndex encodes the i'th blockhash to w.
func (m BlockManifest) EncodeIndex(i int, w *bytes.Buffer) {
	rlp.Encode(w, m[i])
}
