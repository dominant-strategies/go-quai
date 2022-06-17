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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/common/hexutil"
	"github.com/spruce-solutions/go-quai/params"
	"github.com/spruce-solutions/go-quai/rlp"
)

var (
	EmptyRootHash      = []common.Hash{common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"), common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"), common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")}
	EmptyUncleHash     = []common.Hash{rlpHash([]*Header(nil)), rlpHash([]*Header(nil)), rlpHash([]*Header(nil))}
	ContextDepth       = 3
	QuaiNetworkContext = 0
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

type HeaderBundle struct {
	Header  *Header
	Context int
}

//go:generate gencodec -type Header -field-override headerMarshaling -out gen_header_json.go

// Header represents a block header in the Ethereum blockchain.
type Header struct {
	ParentHash        []common.Hash    `json:"parentHash"       gencodec:"required"`
	UncleHash         []common.Hash    `json:"sha3Uncles"       gencodec:"required"`
	Coinbase          []common.Address `json:"miner"            gencodec:"required"`
	Root              []common.Hash    `json:"stateRoot"        gencodec:"required"`
	TxHash            []common.Hash    `json:"transactionsRoot" gencodec:"required"`
	ReceiptHash       []common.Hash    `json:"receiptsRoot"     gencodec:"required"`
	Bloom             []Bloom          `json:"logsBloom"        gencodec:"required"`
	Difficulty        []*big.Int       `json:"difficulty"       gencodec:"required"`
	NetworkDifficulty []*big.Int       `json:"networkDifficulty"  gencodec:"required"`
	Number            []*big.Int       `json:"number"           gencodec:"required"`
	GasLimit          []uint64         `json:"gasLimit"         gencodec:"required"`
	GasUsed           []uint64         `json:"gasUsed"          gencodec:"required"`
	Time              uint64           `json:"timestamp"        gencodec:"required"`
	Extra             [][]byte         `json:"extraData"        gencodec:"required"`
	Nonce             BlockNonce       `json:"nonce"`

	// Originating location of the block.
	Location []byte `json:"location"       gencodec:"required"`

	// BaseFee was added by EIP-1559 and is ignored in legacy headers.
	BaseFee []*big.Int `json:"baseFeePerGas" rlp:"optional"`
}

// field type overrides for gencodec
type headerMarshaling struct {
	Difficulty        []*hexutil.Big
	NetworkDifficulty []*hexutil.Big
	Number            []*hexutil.Big
	GasLimit          []hexutil.Uint64
	GasUsed           []hexutil.Uint64
	Time              hexutil.Uint64
	Extra             []hexutil.Bytes
	BaseFee           []*hexutil.Big
	Location          []byte
	Hash              common.Hash `json:"hash"` // adds call to Hash() in MarshalJSON
}

// Hash returns the block hash of the header, which is simply the keccak256 hash of its
// RLP encoding.
func (h *Header) Hash() common.Hash {
	return rlpHash(h)
}

// Returns current MapContext for a given block.
func (h *Header) MapContext() ([]int, error) {
	return currentBlockOntology(h.Number)
}

var headerSize = common.StorageSize(reflect.TypeOf(Header{}).Size())

// Size returns the approximate memory used by all internal contents. It is used
// to approximate and limit the memory consumption of various caches.
func (h *Header) Size() common.StorageSize {
	return headerSize + common.StorageSize(len(h.Extra)+(TotalBitLen(h.Difficulty)+TotalBitLen(h.Number))/24)
}

// TotalBitLen returns the BitLen at each element in a big.Int slice.
func TotalBitLen(array []*big.Int) int {
	bitLen := 0
	for i := 0; i < ContextDepth; i++ {
		item := array[i]
		if item != nil {
			bitLen += item.BitLen()
		}
	}
	return bitLen
}

// SanityCheck checks a few basic things -- these checks are way beyond what
// any 'sane' production values should hold, and can mainly be used to prevent
// that the unbounded fields are stuffed with junk data to add processing
// overhead
func (h *Header) SanityCheck() error {
	for i := 0; i < ContextDepth; i++ {
		if h.Number[i] != nil && !h.Number[i].IsUint64() {
			return fmt.Errorf("too large block number: bitlen %d", h.Number[i].BitLen())
		}
		if h.Difficulty[i] != nil {
			if diffLen := h.Difficulty[i].BitLen(); diffLen > 80 {
				return fmt.Errorf("too large block difficulty: bitlen %d", diffLen)
			}
		}
		if eLen := len(h.Extra[i]); eLen > 100*1024 {
			return fmt.Errorf("too large block extradata: size %d", eLen)
		}
		if h.BaseFee[i] != nil {
			if bfLen := h.BaseFee[i].BitLen(); bfLen > 256 {
				return fmt.Errorf("too large base fee: bitlen %d", bfLen)
			}
		}
	}
	return nil
}

// EmptyBody returns true if there is no additional 'body' to complete the header
// that is: no transactions and no uncles.
func (h *Header) EmptyBody() bool {
	return IsEqualHashSlice(h.TxHash, EmptyRootHash) && IsEqualHashSlice(h.UncleHash, EmptyUncleHash)
}

// EmptyReceipts returns true if there are no receipts for this header/block.
func (h *Header) EmptyReceipts() bool {
	return IsEqualHashSlice(h.ReceiptHash, EmptyRootHash)
}

// IsEqualHashSlice compares each hash in a headers slice of hashes
func IsEqualHashSlice(a, b []common.Hash) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Body is a simple (mutable, non-safe) data container for storing and moving
// a block's data contents (transactions and uncles) together.
type Body struct {
	Transactions []*Transaction
	Uncles       []*Header
}

// Block represents an entire block in the Ethereum blockchain.
type Block struct {
	header       *Header
	uncles       []*Header
	transactions Transactions

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

// Block represents an entire block in the Ethereum blockchain.
type ReceiptBlock struct {
	header       *Header
	uncles       []*Header
	transactions Transactions
	receipts     []*Receipt

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

// rlp block encoding. used for eth protocol, etc.
type rlpblock struct {
	Header *Header
	Txs    []*Transaction
	Uncles []*Header
}

type ExternalBody struct {
	Transactions Transactions
	Receipts     []*Receipt
	Context      *big.Int
}

// ExternalBlock represents an entire Quai block with multiple contexts.
type ExternalBlock struct {
	header       *Header
	transactions Transactions
	receipts     []*Receipt
	context      *big.Int

	// caches
	hash atomic.Value
	size atomic.Value
}

// rlp external block encoding. used for eth protocol, etc.
type rlpexternalblock struct {
	Header   *Header
	Txs      []*Transaction
	Receipts []*Receipt
	Context  *big.Int
}

// DecodeRLP decodes the Ethereum
func (b *ExternalBlock) DecodeRLP(s *rlp.Stream) error {
	var eb rlpexternalblock
	_, size, _ := s.Kind()
	if err := s.Decode(&eb); err != nil {
		return err
	}
	b.header, b.context, b.transactions, b.receipts = eb.Header, eb.Context, eb.Txs, eb.Receipts
	b.size.Store(common.StorageSize(rlp.ListSize(size)))
	return nil
}

// EncodeRLP serializes b into the Ethereum RLP block format.
func (b *ExternalBlock) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, rlpexternalblock{
		Header:   b.header,
		Txs:      b.transactions,
		Context:  b.context,
		Receipts: b.receipts,
	})
}

// NewExternalBlockWithHeader creates a block with the given header data. The
// header data is copied, changes to header and to the field values
// will not affect the block.
func NewExternalBlockWithHeader(header *Header) *ExternalBlock {
	return &ExternalBlock{header: CopyHeader(header)}
}

// WithBody returns a new block with the given transaction and uncle contents.
func (b *ExternalBlock) ExternalBlockWithBody(transactions []*Transaction, receipts []*Receipt, context *big.Int) *ExternalBlock {
	block := &ExternalBlock{
		header:       CopyHeader(b.header),
		transactions: make([]*Transaction, len(transactions)),
		receipts:     make([]*Receipt, len(receipts)),
		context:      context,
	}
	copy(block.transactions, transactions)
	copy(block.receipts, receipts)
	return block
}

// Simple access methods for ExternalBlocks
func (b *ExternalBlock) Header() *Header            { return CopyHeader(b.header) }
func (b *ExternalBlock) Transactions() Transactions { return b.transactions }
func (b *ExternalBlock) Receipts() Receipts         { return b.receipts }
func (b *ExternalBlock) Context() *big.Int          { return b.context }
func (b *ExternalBlock) CacheKey() []byte {
	hash := b.header.Hash()
	return ExtBlockCacheKey(b.header.Number[b.context.Int64()].Uint64(), b.context.Uint64(), hash)
}

// Returns current MapContext for a given block.
func (b *ExternalBlock) MapContext() ([]int, error) {
	return currentBlockOntology(b.header.Number)
}

// encodeBlockNumber encodes a block number as big endian uint64
func encodeBlockNumber(number uint64) []byte {
	enc := make([]byte, 8)
	binary.BigEndian.PutUint64(enc, number)
	return enc
}

// extBlockBodyKey = blockBodyPrefix + num (uint64 big endian) + location + context + hash
func ExtBlockCacheKey(number uint64, context uint64, hash common.Hash) []byte {
	return append(append(append([]byte("e"), encodeBlockNumber(number)...), encodeBlockNumber(context)...), hash.Bytes()...)
}

// Body returns the non-header content of the block.
func (b *ExternalBlock) Body() *ExternalBody {
	return &ExternalBody{b.transactions, b.receipts, b.context}
}

// ReceiptForTransaction searches receipts within an external block for a specific transaction
func (b *ExternalBlock) ReceiptForTransaction(tx *Transaction) *Receipt {
	for _, receipt := range b.receipts {
		if receipt.TxHash == tx.Hash() {
			return receipt
		}
	}
	return &Receipt{}
}

func (b *ExternalBlock) Hash() common.Hash {
	if hash := b.hash.Load(); hash != nil {
		return hash.(common.Hash)
	}
	v := b.header.Hash()
	b.hash.Store(v)
	return v
}

// Size returns the true RLP encoded storage size of the block, either by encoding
// and returning it, or returning a previsouly cached value.
func (b *ExternalBlock) Size() common.StorageSize {
	if size := b.size.Load(); size != nil {
		return size.(common.StorageSize)
	}
	c := writeCounter(0)
	rlp.Encode(&c, b)
	b.size.Store(common.StorageSize(c))
	return common.StorageSize(c)
}

func (b *ExternalBlock) UnmarshalJSON(data []byte) error {
	var res []interface{}
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}

	fmt.Println(res)

	return nil
}

// NewBlock creates a new block. The input data is copied,
// changes to header and to the field values will not affect the
// block.
//
// The values of TxHash, UncleHash, ReceiptHash and Bloom in header
// are ignored and set to values derived from the given txs, uncles
// and receipts.
func NewBlock(header *Header, txs []*Transaction, uncles []*Header, receipts []*Receipt, hasher TrieHasher) *Block {
	b := &Block{header: CopyHeader(header), td: new(big.Int)}
	// TODO: panic if len(txs) != len(receipts)
	if len(txs) == 0 {
		b.header.TxHash = EmptyRootHash
	} else {
		b.header.TxHash = []common.Hash{DeriveSha(Transactions(txs), hasher), DeriveSha(Transactions(txs), hasher), DeriveSha(Transactions(txs), hasher)}
		b.transactions = make(Transactions, len(txs))
		copy(b.transactions, txs)
	}

	if len(receipts) == 0 {
		b.header.ReceiptHash = EmptyRootHash
	} else {
		b.header.ReceiptHash = []common.Hash{DeriveSha(Receipts(receipts), hasher), DeriveSha(Receipts(receipts), hasher), DeriveSha(Receipts(receipts), hasher)}
		b.header.Bloom = []Bloom{CreateBloom(receipts), CreateBloom(receipts), CreateBloom(receipts)}
	}

	if len(uncles) == 0 {
		b.header.UncleHash = EmptyUncleHash
	} else {
		b.header.UncleHash = []common.Hash{CalcUncleHash(uncles), CalcUncleHash(uncles), CalcUncleHash(uncles)}
		b.uncles = make([]*Header, len(uncles))
		for i := range uncles {
			b.uncles[i] = CopyHeader(uncles[i])
		}
	}

	return b
}

// NewBlockWithHeader creates a block with the given header data. The
// header data is copied, changes to header and to the field values
// will not affect the block.
func NewBlockWithHeader(header *Header) *Block {
	return &Block{header: CopyHeader(header)}
}

// NewReceiptBlockWithHeader creates a receipt block with the given header data. The
// header data is copied, changes to header and to the field values
// will not affect the block.
func NewReceiptBlockWithHeader(header *Header) *ReceiptBlock {
	return &ReceiptBlock{header: CopyHeader(header)}
}

// NewEmptyHeader returns a header with intialized fields ContextDepth deep
func NewEmptyHeader() *Header {
	header := &Header{
		ParentHash:        make([]common.Hash, ContextDepth),
		Number:            make([]*big.Int, ContextDepth),
		Extra:             make([][]byte, ContextDepth),
		Time:              uint64(0),
		BaseFee:           make([]*big.Int, ContextDepth),
		GasLimit:          make([]uint64, ContextDepth),
		Coinbase:          make([]common.Address, ContextDepth),
		Difficulty:        make([]*big.Int, ContextDepth),
		NetworkDifficulty: make([]*big.Int, ContextDepth),
		Root:              make([]common.Hash, ContextDepth),
		TxHash:            make([]common.Hash, ContextDepth),
		UncleHash:         make([]common.Hash, ContextDepth),
		ReceiptHash:       make([]common.Hash, ContextDepth),
		GasUsed:           make([]uint64, ContextDepth),
		Bloom:             make([]Bloom, ContextDepth),
	}

	return header
}

// CopyHeader creates a deep copy of a block header to prevent side effects from
// modifying a header variable.
func CopyHeader(h *Header) *Header {
	cpy := *h
	for i := 0; i < ContextDepth; i++ {
		if len(h.Difficulty) > i && h.Difficulty[i] != nil {
			cpy.Difficulty[i].Set(h.Difficulty[i])
		}
		if len(h.NetworkDifficulty) > i && h.NetworkDifficulty[i] != nil {
			cpy.NetworkDifficulty[i].Set(h.NetworkDifficulty[i])
		}
		if len(h.Number) > i && h.Number[i] != nil {
			cpy.Number[i].Set(h.Number[i])
		}
		if len(h.BaseFee) > i && h.BaseFee != nil && h.BaseFee[i] != nil {
			cpy.BaseFee[i] = new(big.Int).Set(h.BaseFee[i])
		}
		if len(h.Extra) > i {
			if len(h.Extra[i]) > 0 {
				cpy.Extra[i] = make([]byte, len(h.Extra[i]))
				copy(cpy.Extra[i], h.Extra[i])
			}
		}
	}
	return &cpy
}

// DecodeRLP decodes the Ethereum
func (b *Block) DecodeRLP(s *rlp.Stream) error {
	var eb rlpblock
	_, size, _ := s.Kind()
	if err := s.Decode(&eb); err != nil {
		return err
	}
	b.header, b.uncles, b.transactions = eb.Header, eb.Uncles, eb.Txs
	b.size.Store(common.StorageSize(rlp.ListSize(size)))
	return nil
}

// EncodeRLP serializes b into the Ethereum RLP block format.
func (b *Block) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, rlpblock{
		Header: b.header,
		Txs:    b.transactions,
		Uncles: b.uncles,
	})
}

// TODO: copies

func (b *Block) Uncles() []*Header          { return b.uncles }
func (b *Block) Transactions() Transactions { return b.transactions }

func (b *ReceiptBlock) Uncles() []*Header          { return b.uncles }
func (b *ReceiptBlock) Transactions() Transactions { return b.transactions }
func (b *ReceiptBlock) Receipts() []*Receipt       { return b.receipts }

func (b *Block) Transaction(hash common.Hash) *Transaction {
	for _, transaction := range b.transactions {
		if transaction.Hash() == hash {
			return transaction
		}
	}
	return nil
}

func (b *Block) Number(params ...int) *big.Int {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	// Temp check
	if len(b.header.Number) < context {
		return nil
	}
	return b.header.Number[context]
}
func (b *Block) GasLimit(params ...int) uint64 {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	return b.header.GasLimit[context]
}
func (b *Block) GasUsed(params ...int) uint64 {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	return b.header.GasUsed[context]
}
func (b *Block) Difficulty(params ...int) *big.Int {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	if b.header.Difficulty[context] == nil {
		return nil
	}
	return new(big.Int).Set(b.header.Difficulty[context])
}
func (b *Block) NetworkDifficulty(params ...int) *big.Int {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	if b.header.NetworkDifficulty[context] == nil {
		return nil
	}
	return new(big.Int).Set(b.header.NetworkDifficulty[context])
}
func (b *Block) Time() uint64 { return b.header.Time }
func (b *Block) NumberU64(params ...int) uint64 {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	if b.header.Number[context] == nil {
		return 0
	}
	return b.header.Number[context].Uint64()
}
func (b *Block) Nonce(params ...int) uint64 {
	return binary.BigEndian.Uint64(b.header.Nonce[:])
}
func (b *Block) Bloom(params ...int) Bloom {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	return b.header.Bloom[context]
}
func (b *Block) Coinbase(params ...int) common.Address {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	return b.header.Coinbase[context]
}
func (b *Block) Root(params ...int) common.Hash {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	return b.header.Root[context]
}
func (b *Block) ParentHash(params ...int) common.Hash {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}

	return b.header.ParentHash[context]
}
func (b *Block) TxHash(params ...int) common.Hash {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	return b.header.TxHash[context]
}
func (b *Block) ReceiptHash(params ...int) common.Hash {
	return b.header.ReceiptHash[QuaiNetworkContext]
}
func (b *Block) UncleHash(params ...int) common.Hash {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	return b.header.UncleHash[context]
}
func (b *Block) Extra(params ...int) []byte {
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}
	return common.CopyBytes(b.header.Extra[context])
}
func (b *Block) BaseFee(params ...int) *big.Int {
	if b.header.BaseFee == nil {
		return nil
	}
	context := QuaiNetworkContext
	if len(params) > 0 {
		context = params[0]
	}

	return new(big.Int).Set(b.header.BaseFee[context])
}

// TODO
func (b *Block) MapContext() ([]int, error) {
	return currentBlockOntology(b.header.Number)
}

func (b *Block) Header() *Header { return CopyHeader(b.header) }

func (b *ReceiptBlock) Header() *Header { return CopyHeader(b.header) }

// Body returns the non-header content of the block.
func (b *Block) Body() *Body { return &Body{b.transactions, b.uncles} }

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
		return EmptyUncleHash[0]
	}
	return rlpHash(uncles)
}

// WithSeal returns a new block with the data from b but the header replaced with
// the sealed one.
func (b *Block) WithSeal(header *Header) *Block {
	cpy := *header

	return &Block{
		header:       &cpy,
		transactions: b.transactions,
		uncles:       b.uncles,
	}
}

// WithBody returns a new block with the given transaction and uncle contents.
func (b *Block) WithBody(transactions []*Transaction, uncles []*Header) *Block {
	block := &Block{
		header:       CopyHeader(b.header),
		transactions: make([]*Transaction, len(transactions)),
		uncles:       make([]*Header, len(uncles)),
	}
	copy(block.transactions, transactions)
	for i := range uncles {
		block.uncles[i] = CopyHeader(uncles[i])
	}
	return block
}

// WithSeal returns a new receipt block with the data from b but the header replaced with
// the sealed one.
func (b *ReceiptBlock) WithSeal(header *Header) *ReceiptBlock {
	cpy := *header

	return &ReceiptBlock{
		header:       &cpy,
		transactions: b.transactions,
		uncles:       b.uncles,
		receipts:     b.receipts,
	}
}

// WithBody returns a new block with the given transaction and uncle contents.
func (b *ReceiptBlock) WithBody(transactions []*Transaction, uncles []*Header, receipts []*Receipt) *ReceiptBlock {
	block := &ReceiptBlock{
		header:       CopyHeader(b.header),
		transactions: make([]*Transaction, len(transactions)),
		uncles:       make([]*Header, len(uncles)),
		receipts:     make([]*Receipt, len(receipts)),
	}
	copy(block.transactions, transactions)
	for i := range uncles {
		block.uncles[i] = CopyHeader(uncles[i])
	}
	copy(block.receipts, receipts)
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

// currentBlockOntology is used to retrieve the MapContext of a given block.
func currentBlockOntology(number []*big.Int) ([]int, error) {
	forkNumber := number[0]

	switch {
	case forkNumber.Cmp(params.FullerMapContext) >= 0:
		return params.FullerOntology, nil
	default:
		return nil, errors.New("invalid number passed to currentBlockOntology")
	}
}
