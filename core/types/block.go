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

	"google.golang.org/protobuf/proto"
	"lukechampine.com/blake3"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/rlp"
)

var (
	EmptyRootHash  = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	EmptyUncleHash = RlpHash([]*Header(nil))
	EmptyBodyHash  = common.HexToHash("51e1b9c1426a03bf73da3d98d9f384a49ded6a4d705dcdf25433915c3306826c")
	EmptyHash      = common.Hash{}
	big2e256       = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil) // 2^256
	hasher         = blake3.New(32, nil)
	hasherMu       sync.RWMutex
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

type writeCounter common.StorageSize

func (c *writeCounter) Write(b []byte) (int, error) {
	*c += writeCounter(len(b))
	return len(b), nil
}

// Header represents a block header in the Quai blockchain.
type Header struct {
	parentHash            []common.Hash `json:"parentHash"            gencodec:"required"`
	uncleHash             common.Hash   `json:"sha3Uncles"            gencodec:"required"`
	evmRoot               common.Hash   `json:"evmRoot"               gencodec:"required"`
	utxoRoot              common.Hash   `json:"utxoRoot"              gencodec:"required"`
	txHash                common.Hash   `json:"transactionsRoot"      gencodec:"required"`
	etxHash               common.Hash   `json:"extTransactionsRoot"   gencodec:"required"`
	etxSetRoot            common.Hash   `json:"etxSetRoot"            gencodec:"required"`
	etxRollupHash         common.Hash   `json:"extRollupRoot"         gencodec:"required"`
	manifestHash          []common.Hash `json:"manifestHash"          gencodec:"required"`
	receiptHash           common.Hash   `json:"receiptsRoot"          gencodec:"required"`
	parentEntropy         []*big.Int    `json:"parentEntropy"         gencodec:"required"`
	parentDeltaS          []*big.Int    `json:"parentDeltaS"          gencodec:"required"`
	parentUncledSubDeltaS []*big.Int    `json:"parentUncledSubDeltaS" gencodec:"required"`
	efficiencyScore       uint16        `json:"efficiencyScore"       gencodec:"required"`
	thresholdCount        uint16        `json:"thresholdCount"        gencodec:"required"`
	expansionNumber       uint8         `json:"expansionNumber"    	  gencodec:"required"`
	etxEligibleSlices     common.Hash   `json:"etxEligibleSlices"     gencodec:"required"`
	primeTerminus         common.Hash   `json:"primeTerminus"         gencodec:"required"`
	interlinkRootHash     common.Hash   `json:"interlinkRootHash"     gencodec:"required"`
	uncledS               *big.Int      `json:"uncledLogS"            gencodec:"required"`
	number                []*big.Int    `json:"number"                gencodec:"required"`
	gasLimit              uint64        `json:"gasLimit"              gencodec:"required"`
	gasUsed               uint64        `json:"gasUsed"               gencodec:"required"`
	baseFee               *big.Int      `json:"baseFeePerGas"         gencodec:"required"`
	extra                 []byte        `json:"extraData"             gencodec:"required"`
	stateLimit            uint64        `json:"StateLimit" 			  gencodec:"required"`
	stateUsed             uint64        `json:"StateUsed"             gencodec:"required"`

	// caches
	hash     atomic.Value
	sealHash atomic.Value
}

// field type overrides for gencodec
type headerMarshaling struct {
	Number                []*hexutil.Big
	GasLimit              hexutil.Uint64
	GasUsed               hexutil.Uint64
	BaseFee               *hexutil.Big
	ParentEntropy         []*hexutil.Big
	ParentDeltaS          []*hexutil.Big
	ParentUncledS         []*hexutil.Big
	ParentUncledSubDeltaS []*hexutil.Big
	UncledS               *hexutil.Big
	Time                  hexutil.Uint64
	Extr                  hexutil.Bytes
	Hash                  common.Hash `json:"hash"` // adds call to Hash() in MarshalJSON
}

func EmptyHeader() *Header {
	h := &Header{}

	h.parentHash = make([]common.Hash, common.HierarchyDepth-1)
	h.manifestHash = make([]common.Hash, common.HierarchyDepth)
	h.parentEntropy = make([]*big.Int, common.HierarchyDepth)
	h.parentDeltaS = make([]*big.Int, common.HierarchyDepth)
	h.parentUncledSubDeltaS = make([]*big.Int, common.HierarchyDepth)
	h.number = make([]*big.Int, common.HierarchyDepth-1)
	h.uncledS = big.NewInt(0)
	h.evmRoot = EmptyRootHash
	h.utxoRoot = EmptyRootHash
	h.txHash = EmptyRootHash
	h.etxHash = EmptyRootHash
	h.etxSetRoot = EmptyRootHash
	h.etxRollupHash = EmptyRootHash
	h.uncleHash = EmptyUncleHash
	h.baseFee = big.NewInt(0)
	h.stateLimit = 0
	h.stateUsed = 0
	h.extra = []byte{}
	h.efficiencyScore = 0
	h.thresholdCount = 0
	h.expansionNumber = 0
	h.etxEligibleSlices = EmptyHash
	h.primeTerminus = EmptyRootHash
	h.interlinkRootHash = EmptyRootHash

	for i := 0; i < common.HierarchyDepth; i++ {
		h.manifestHash[i] = EmptyRootHash
		h.parentEntropy[i] = big.NewInt(0)
		h.parentDeltaS[i] = big.NewInt(0)
		h.parentUncledSubDeltaS[i] = big.NewInt(0)
	}
	for i := 0; i < common.HierarchyDepth-1; i++ {
		h.parentHash[i] = EmptyRootHash
		h.number[i] = big.NewInt(0)
	}

	return h
}

// Construct an empty header
func EmptyWorkObject(nodeCtx int) *WorkObject {
	wo := &WorkObject{woHeader: &WorkObjectHeader{}, woBody: &WorkObjectBody{}, tx: &Transaction{}}
	h := EmptyHeader()
	wo.woHeader.SetHeaderHash(EmptyRootHash)
	wo.woHeader.SetParentHash(EmptyRootHash)
	wo.woHeader.SetNumber(big.NewInt(0))
	wo.woHeader.SetDifficulty(big.NewInt(0))
	wo.woHeader.SetPrimeTerminusNumber(big.NewInt(0))
	wo.woHeader.SetTxHash(EmptyRootHash)
	wo.woHeader.SetLocation(common.Location{})
	wo.woHeader.SetNonce(EncodeNonce(0))
	wo.woHeader.SetTime(0)
	wo.woBody.SetHeader(h)
	wo.woBody.SetUncles([]*WorkObjectHeader{})
	wo.woBody.SetTransactions([]*Transaction{})
	wo.woBody.SetExtTransactions([]*Transaction{})
	wo.woBody.SetManifest(BlockManifest{})
	return NewWorkObjectWithHeader(wo, &Transaction{}, nodeCtx, BlockObject)
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
	etxSetRoot := common.ProtoHash{Value: h.EtxSetRoot().Bytes()}
	etxRollupHash := common.ProtoHash{Value: h.EtxRollupHash().Bytes()}
	receiptHash := common.ProtoHash{Value: h.ReceiptHash().Bytes()}
	etxEligibleSlices := common.ProtoHash{Value: h.EtxEligibleSlices().Bytes()}
	primeTerminus := common.ProtoHash{Value: h.PrimeTerminus().Bytes()}
	interlinkRootHash := common.ProtoHash{Value: h.InterlinkRootHash().Bytes()}
	gasLimit := h.GasLimit()
	gasUsed := h.GasUsed()
	stateLimit := h.StateLimit()
	stateUsed := h.StateUsed()
	efficiencyScore := uint64(h.EfficiencyScore())
	thresholdCount := uint64(h.ThresholdCount())
	expansionNumber := uint64(h.ExpansionNumber())

	protoHeader := &ProtoHeader{
		UncleHash:         &uncleHash,
		EvmRoot:           &evmRoot,
		UtxoRoot:          &utxoRoot,
		TxHash:            &txHash,
		EtxHash:           &etxhash,
		EtxSetRoot:        &etxSetRoot,
		EtxRollupHash:     &etxRollupHash,
		ReceiptHash:       &receiptHash,
		PrimeTerminus:     &primeTerminus,
		InterlinkRootHash: &interlinkRootHash,
		EtxEligibleSlices: &etxEligibleSlices,
		UncledS:           h.UncledS().Bytes(),
		GasLimit:          &gasLimit,
		GasUsed:           &gasUsed,
		EfficiencyScore:   &efficiencyScore,
		ThresholdCount:    &thresholdCount,
		ExpansionNumber:   &expansionNumber,
		BaseFee:           h.BaseFee().Bytes(),
		StateLimit:        &stateLimit,
		StateUsed:         &stateUsed,
		Extra:             h.Extra(),
	}

	for i := 0; i < common.HierarchyDepth; i++ {
		protoHeader.ManifestHash = append(protoHeader.ManifestHash, h.ManifestHash(i).ProtoEncode())
		if h.ParentEntropy(i) != nil {
			protoHeader.ParentEntropy = append(protoHeader.ParentEntropy, h.ParentEntropy(i).Bytes())
		}
		if h.ParentDeltaS(i) != nil {
			protoHeader.ParentDeltaS = append(protoHeader.ParentDeltaS, h.ParentDeltaS(i).Bytes())
		}
		if h.ParentUncledSubDeltaS(i) != nil {
			protoHeader.ParentUncledSubDeltaS = append(protoHeader.ParentUncledSubDeltaS, h.ParentUncledSubDeltaS(i).Bytes())
		}
	}
	for i := 0; i < common.HierarchyDepth-1; i++ {
		protoHeader.ParentHash = append(protoHeader.ParentHash, h.ParentHash(i).ProtoEncode())
		if h.Number(i) != nil {
			protoHeader.Number = append(protoHeader.Number, h.Number(i).Bytes())
		}
	}

	return protoHeader, nil
}

// ProtoDecode deserializes the ProtoHeader into the Header format
func (h *Header) ProtoDecode(protoHeader *ProtoHeader, location common.Location) error {
	if protoHeader.ParentHash == nil {
		return errors.New("missing required field 'ParentHash' in Header")
	}
	if protoHeader.UncleHash == nil {
		return errors.New("missing required field 'UncleHash' in Header")
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
	if protoHeader.EtxSetRoot == nil {
		return errors.New("missing required field 'EtxSetRoot' in Header")
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
	if protoHeader.PrimeTerminus == nil {
		return errors.New("missing required field 'PrimeTerminus' in Header")
	}
	if protoHeader.InterlinkRootHash == nil {
		return errors.New("missing required field 'InterlinkRootHash' in Header")
	}
	if protoHeader.BaseFee == nil {
		return errors.New("missing required field 'BaseFee' in Header")
	}
	if protoHeader.ParentEntropy == nil {
		return errors.New("missing required field 'ParentEntropy' in Header")
	}
	if protoHeader.ParentDeltaS == nil {
		return errors.New("missing required field 'ParentDeltaS' in Header")
	}
	if protoHeader.ParentUncledSubDeltaS == nil {
		return errors.New("missing required field 'ParentUncledSubDeltaS' in Header")
	}
	if protoHeader.UncledS == nil {
		return errors.New("missing required field 'UncledS' in Header")
	}
	if protoHeader.Number == nil {
		return errors.New("missing required field 'Number' in Header")
	}
	if protoHeader.EfficiencyScore == nil {
		return errors.New("missing required field 'EfficiencyScore' in Header")
	}
	if protoHeader.ThresholdCount == nil {
		return errors.New("missing required field 'ThresholdCount' in Header")
	}
	if protoHeader.ExpansionNumber == nil {
		return errors.New("missing required field 'ExpansionNumber' in Header")
	}
	if protoHeader.EtxEligibleSlices == nil {
		return errors.New("missing required field 'EtxEligibleSlices' in Header")
	}
	if protoHeader.PrimeTerminus == nil {
		return errors.New("missing required field 'PrimeTerminus' in Header")
	}

	// Initialize the array fields before setting
	h.parentHash = make([]common.Hash, common.HierarchyDepth-1)
	h.manifestHash = make([]common.Hash, common.HierarchyDepth)
	h.parentEntropy = make([]*big.Int, common.HierarchyDepth)
	h.parentDeltaS = make([]*big.Int, common.HierarchyDepth)
	h.parentUncledSubDeltaS = make([]*big.Int, common.HierarchyDepth)
	h.number = make([]*big.Int, common.HierarchyDepth-1)

	for i := 0; i < common.HierarchyDepth; i++ {
		h.SetManifestHash(common.BytesToHash(protoHeader.GetManifestHash()[i].GetValue()), i)
		h.SetParentEntropy(new(big.Int).SetBytes(protoHeader.GetParentEntropy()[i]), i)
		h.SetParentDeltaS(new(big.Int).SetBytes(protoHeader.GetParentDeltaS()[i]), i)
		h.SetParentUncledSubDeltaS(new(big.Int).SetBytes(protoHeader.GetParentUncledSubDeltaS()[i]), i)
	}
	for i := 0; i < common.HierarchyDepth-1; i++ {
		h.SetNumber(new(big.Int).SetBytes(protoHeader.GetNumber()[i]), i)
		h.SetParentHash(common.BytesToHash(protoHeader.GetParentHash()[i].GetValue()), i)
	}

	h.SetUncleHash(common.BytesToHash(protoHeader.GetUncleHash().GetValue()))
	h.SetEVMRoot(common.BytesToHash(protoHeader.GetEvmRoot().GetValue()))
	h.SetUTXORoot(common.BytesToHash(protoHeader.GetUtxoRoot().GetValue()))
	h.SetTxHash(common.BytesToHash(protoHeader.GetTxHash().GetValue()))
	h.SetReceiptHash(common.BytesToHash(protoHeader.GetReceiptHash().GetValue()))
	h.SetEtxHash(common.BytesToHash(protoHeader.GetEtxHash().GetValue()))
	h.SetEtxSetRoot(common.BytesToHash(protoHeader.GetEtxSetRoot().GetValue()))
	h.SetEtxRollupHash(common.BytesToHash(protoHeader.GetEtxRollupHash().GetValue()))
	h.SetPrimeTerminus(common.BytesToHash(protoHeader.GetPrimeTerminus().GetValue()))
	h.SetInterlinkRootHash(common.BytesToHash(protoHeader.GetInterlinkRootHash().GetValue()))
	h.SetUncledS(new(big.Int).SetBytes(protoHeader.GetUncledS()))
	h.SetGasLimit(protoHeader.GetGasLimit())
	h.SetGasUsed(protoHeader.GetGasUsed())
	h.SetBaseFee(new(big.Int).SetBytes(protoHeader.GetBaseFee()))
	h.SetStateLimit((protoHeader.GetStateLimit()))
	h.SetStateUsed((protoHeader.GetStateUsed()))
	h.SetExtra(protoHeader.GetExtra())
	h.SetEfficiencyScore(uint16(protoHeader.GetEfficiencyScore()))
	h.SetThresholdCount(uint16(protoHeader.GetThresholdCount()))
	h.SetExpansionNumber(uint8(protoHeader.GetExpansionNumber()))
	h.SetEtxEligibleSlices(common.BytesToHash(protoHeader.GetEtxEligibleSlices().GetValue()))

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
		"uncledS":             (*hexutil.Big)(h.UncledS()),
		"sha3Uncles":          h.UncleHash(),
		"evmRoot":             h.EVMRoot(),
		"utxoRoot":            h.UTXORoot(),
		"extraData":           hexutil.Bytes(h.Extra()),
		"size":                hexutil.Uint64(h.Size()),
		"transactionsRoot":    h.TxHash(),
		"receiptsRoot":        h.ReceiptHash(),
		"extTransactionsRoot": h.EtxHash(),
		"etxSetRoot":          h.EtxSetRoot(),
		"extRollupRoot":       h.EtxRollupHash(),
		"primeTerminus":       h.PrimeTerminus(),
		"interlinkRootHash":   h.InterlinkRootHash(),
		"manifestHash":        h.ManifestHashArray(),
		"gasLimit":            hexutil.Uint(h.GasLimit()),
		"gasUsed":             hexutil.Uint(h.GasUsed()),
		"efficiencyScore":     hexutil.Uint64(h.EfficiencyScore()),
		"thresholdCount":      hexutil.Uint64(h.ThresholdCount()),
		"expansionNumber":     hexutil.Uint64(h.ExpansionNumber()),
		"etxEligibleSlices":   h.EtxEligibleSlices(),
		"stateLimit":          hexutil.Uint64(h.StateLimit()),
		"stateUsed":           hexutil.Uint64(h.StateUsed()),
	}

	number := make([]*hexutil.Big, common.HierarchyDepth)
	parentEntropy := make([]*hexutil.Big, common.HierarchyDepth)
	parentDeltaS := make([]*hexutil.Big, common.HierarchyDepth)
	parentUncledS := make([]*hexutil.Big, common.HierarchyDepth)
	parentUncledSubDeltaS := make([]*hexutil.Big, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		parentEntropy[i] = (*hexutil.Big)(h.ParentEntropy(i))
		parentDeltaS[i] = (*hexutil.Big)(h.ParentDeltaS(i))
		parentUncledSubDeltaS[i] = (*hexutil.Big)(h.ParentUncledSubDeltaS(i))
	}
	for i := 0; i < common.HierarchyDepth-1; i++ {
		number[i] = (*hexutil.Big)(h.Number(i))
	}

	result["number"] = number
	result["parentEntropy"] = parentEntropy
	result["parentDeltaS"] = parentDeltaS
	result["parentUncledS"] = parentUncledS
	result["parentUncledSubDeltaS"] = parentUncledSubDeltaS

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
func (h *Header) EtxSetRoot() common.Hash {
	return h.etxSetRoot
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
func (h *Header) ParentUncledSubDeltaS(nodeCtx int) *big.Int {
	return h.parentUncledSubDeltaS[nodeCtx]
}
func (h *Header) UncledS() *big.Int {
	return h.uncledS
}
func (h *Header) ManifestHash(nodeCtx int) common.Hash {
	return h.manifestHash[nodeCtx]
}
func (h *Header) ReceiptHash() common.Hash {
	return h.receiptHash
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
func (h *Header) EfficiencyScore() uint16 {
	return h.efficiencyScore
}
func (h *Header) ThresholdCount() uint16 {
	return h.thresholdCount
}
func (h *Header) ExpansionNumber() uint8 {
	return h.expansionNumber
}
func (h *Header) EtxEligibleSlices() common.Hash {
	return h.etxEligibleSlices
}
func (h *Header) BaseFee() *big.Int {
	return h.baseFee
}
func (h *Header) StateLimit() uint64 {
	return h.stateLimit
}
func (h *Header) StateUsed() uint64 {
	return h.stateUsed
}
func (h *Header) Extra() []byte                  { return common.CopyBytes(h.extra) }
func (h *Header) PrimeTerminus() common.Hash     { return h.primeTerminus }
func (h *Header) InterlinkRootHash() common.Hash { return h.interlinkRootHash }

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
func (h *Header) SetEtxSetRoot(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.etxSetRoot = val
}
func (h *Header) SetEtxRollupHash(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.etxRollupHash = val
}
func (h *Header) SetPrimeTerminus(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.primeTerminus = val
}
func (h *Header) SetUncledS(val *big.Int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.uncledS = val
}
func (h *Header) SetInterlinkRootHash(val common.Hash) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.interlinkRootHash = val
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

func (h *Header) SetParentUncledSubDeltaS(val *big.Int, nodeCtx int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.parentUncledSubDeltaS[nodeCtx] = val
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
func (h *Header) SetEfficiencyScore(val uint16) {
	h.hash = atomic.Value{}
	h.sealHash = atomic.Value{}
	h.efficiencyScore = val
}
func (h *Header) SetThresholdCount(val uint16) {
	h.hash = atomic.Value{}
	h.sealHash = atomic.Value{}
	h.thresholdCount = val
}
func (h *Header) SetExpansionNumber(val uint8) {
	h.hash = atomic.Value{}
	h.sealHash = atomic.Value{}
	h.expansionNumber = val
}
func (h *Header) SetEtxEligibleSlices(val common.Hash) {
	h.hash = atomic.Value{}
	h.sealHash = atomic.Value{}
	h.etxEligibleSlices = val
}
func (h *Header) SetBaseFee(val *big.Int) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.baseFee = new(big.Int).Set(val)
}
func (h *Header) SetStateLimit(val uint64) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.stateLimit = val
}
func (h *Header) SetStateUsed(val uint64) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // 	clear sealHash cache
	h.stateUsed = val
}
func (h *Header) SetExtra(val []byte) {
	h.hash = atomic.Value{}     // clear hash cache
	h.sealHash = atomic.Value{} // clear sealHash cache
	h.extra = make([]byte, len(val))
	copy(h.extra, val)
}

// Array accessors
func (h *Header) ParentHashArray() []common.Hash   { return h.parentHash }
func (h *Header) ManifestHashArray() []common.Hash { return h.manifestHash }
func (h *Header) NumberArray() []*big.Int          { return h.number }
func (h *Header) ParentUncledSubDeltaSArray() []*big.Int {
	return h.parentUncledSubDeltaS
}

// ProtoEncode serializes s into the Quai Proto sealData format
func (h *Header) SealEncode() *ProtoHeader {
	uncleHash := common.ProtoHash{Value: h.UncleHash().Bytes()}
	evmRoot := common.ProtoHash{Value: h.EVMRoot().Bytes()}
	utxoRoot := common.ProtoHash{Value: h.UTXORoot().Bytes()}
	txHash := common.ProtoHash{Value: h.TxHash().Bytes()}
	etxhash := common.ProtoHash{Value: h.EtxHash().Bytes()}
	etxSetRoot := common.ProtoHash{Value: h.EtxSetRoot().Bytes()}
	etxRollupHash := common.ProtoHash{Value: h.EtxRollupHash().Bytes()}
	receiptHash := common.ProtoHash{Value: h.ReceiptHash().Bytes()}
	etxEligibleSlices := common.ProtoHash{Value: h.EtxEligibleSlices().Bytes()}
	primeTerminus := common.ProtoHash{Value: h.PrimeTerminus().Bytes()}
	interlinkRootHash := common.ProtoHash{Value: h.InterlinkRootHash().Bytes()}
	efficiencyScore := uint64(h.EfficiencyScore())
	thresholdCount := uint64(h.ThresholdCount())
	expansionNumber := uint64(h.ExpansionNumber())
	gasLimit := h.GasLimit()
	gasUsed := h.GasUsed()
	stateLimit := h.StateLimit()
	stateUsed := h.StateUsed()

	protoSealData := &ProtoHeader{
		UncleHash:         &uncleHash,
		EvmRoot:           &evmRoot,
		UtxoRoot:          &utxoRoot,
		TxHash:            &txHash,
		EtxHash:           &etxhash,
		EtxSetRoot:        &etxSetRoot,
		EtxRollupHash:     &etxRollupHash,
		ReceiptHash:       &receiptHash,
		GasLimit:          &gasLimit,
		GasUsed:           &gasUsed,
		BaseFee:           h.BaseFee().Bytes(),
		StateLimit:        &stateLimit,
		StateUsed:         &stateUsed,
		UncledS:           h.UncledS().Bytes(),
		PrimeTerminus:     &primeTerminus,
		InterlinkRootHash: &interlinkRootHash,
		EtxEligibleSlices: &etxEligibleSlices,
		EfficiencyScore:   &efficiencyScore,
		ThresholdCount:    &thresholdCount,
		ExpansionNumber:   &expansionNumber,
		Extra:             h.Extra(),
	}

	for i := 0; i < common.HierarchyDepth; i++ {
		protoSealData.ManifestHash = append(protoSealData.ManifestHash, h.ManifestHash(i).ProtoEncode())
		if h.ParentEntropy(i) != nil {
			protoSealData.ParentEntropy = append(protoSealData.ParentEntropy, h.ParentEntropy(i).Bytes())
		}
		if h.ParentDeltaS(i) != nil {
			protoSealData.ParentDeltaS = append(protoSealData.ParentDeltaS, h.ParentDeltaS(i).Bytes())
		}
		if h.ParentUncledSubDeltaS(i) != nil {
			protoSealData.ParentUncledSubDeltaS = append(protoSealData.ParentUncledSubDeltaS, h.ParentUncledSubDeltaS(i).Bytes())
		}

	}
	for i := 0; i < common.HierarchyDepth-1; i++ {
		if h.Number(i) != nil {
			protoSealData.Number = append(protoSealData.Number, h.Number(i).Bytes())
		}
		protoSealData.ParentHash = append(protoSealData.ParentHash, h.ParentHash(i).ProtoEncode())
	}
	return protoSealData
}

// SealHash returns the hash of a block prior to it being sealed.
func (h *Header) Hash() (hash common.Hash) {
	hasherMu.Lock()
	defer hasherMu.Unlock()
	hasher.Reset()
	protoSealData := h.SealEncode()
	data, err := proto.Marshal(protoSealData)
	if err != nil {
		// In the case of error while marshalling return empty hash, and caller
		// of this should handle the error
		data = []byte{}
	}
	sum := blake3.Sum256(data[:])
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
	return headerSize + common.StorageSize(len(h.extra)+totalBitLen(h.number)/8)
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
	if h.StateLimit == nil {
		return fmt.Errorf("fields cannot be `nil`: stateLimit")
	}
	if h.StateUsed == nil {
		return fmt.Errorf("fields cannot be `nil`: stateUsed")
	}
	if bfLen := h.baseFee.BitLen(); bfLen > 256 {
		return fmt.Errorf("too large base fee: bitlen %d", bfLen)
	}
	for i := 0; i < common.HierarchyDepth-1; i++ {
		if h.number == nil {
			return fmt.Errorf("field cannot be `nil`: number[%d]", i)
		}
		if h.number[i] != nil && !h.number[i].IsUint64() {
			return fmt.Errorf("too large block number[%d]: bitlen %d", i, h.number[i].BitLen())
		}
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

// CopyHeader creates a deep copy of a block header to prevent side effects from
// modifying a header variable.
func CopyHeader(h *Header) *Header {
	if h == nil {
		return nil
	}
	cpy := *h
	cpy.parentHash = make([]common.Hash, common.HierarchyDepth-1)
	cpy.manifestHash = make([]common.Hash, common.HierarchyDepth)
	cpy.parentEntropy = make([]*big.Int, common.HierarchyDepth)
	cpy.parentDeltaS = make([]*big.Int, common.HierarchyDepth)
	cpy.parentUncledSubDeltaS = make([]*big.Int, common.HierarchyDepth)
	cpy.number = make([]*big.Int, common.HierarchyDepth)
	cpy.number = make([]*big.Int, common.HierarchyDepth-1)
	for i := 0; i < common.HierarchyDepth; i++ {
		cpy.SetManifestHash(h.ManifestHash(i), i)
		cpy.SetParentEntropy(h.ParentEntropy(i), i)
		cpy.SetParentDeltaS(h.ParentDeltaS(i), i)
		cpy.SetParentUncledSubDeltaS(h.ParentUncledSubDeltaS(i), i)
	}
	for i := 0; i < common.HierarchyDepth-1; i++ {
		cpy.SetParentHash(h.ParentHash(i), i)
		cpy.SetNumber(h.Number(i), i)
	}
	cpy.SetUncledS(h.UncledS())
	cpy.SetUncleHash(h.UncleHash())
	cpy.SetEVMRoot(h.EVMRoot())
	cpy.SetUTXORoot(h.UTXORoot())
	cpy.SetTxHash(h.TxHash())
	cpy.SetEtxHash(h.EtxHash())
	cpy.SetEtxSetRoot(h.EtxSetRoot())
	cpy.SetEtxRollupHash(h.EtxRollupHash())
	cpy.SetReceiptHash(h.ReceiptHash())
	cpy.SetPrimeTerminus(h.PrimeTerminus())
	if len(h.extra) > 0 {
		cpy.extra = make([]byte, len(h.extra))
		copy(cpy.extra, h.extra)
	}
	cpy.SetGasLimit(h.GasLimit())
	cpy.SetGasUsed(h.GasUsed())
	cpy.SetEfficiencyScore(h.EfficiencyScore())
	cpy.SetThresholdCount(h.ThresholdCount())
	cpy.SetExpansionNumber(h.ExpansionNumber())
	cpy.SetEtxEligibleSlices(h.EtxEligibleSlices())
	cpy.SetBaseFee(h.BaseFee())
	cpy.SetStateLimit(h.StateLimit())
	cpy.SetStateUsed(h.StateUsed())
	return &cpy
}

// PendingHeader stores the header and termini value associated with the header.
type PendingHeader struct {
	wo      *WorkObject `json:"wo"`
	termini Termini     `json:"termini"`
}

// accessor methods for pending header
func (ph PendingHeader) WorkObject() *WorkObject {
	return ph.wo
}

func (ph PendingHeader) Termini() Termini {
	return ph.termini
}

func (ph *PendingHeader) SetHeader(header *WorkObject) {
	ph.wo = header
}

func (ph *PendingHeader) SetWorkObject(wo *WorkObject) {
	ph.wo = wo
}

func (ph *PendingHeader) SetTermini(termini Termini) {
	ph.termini = CopyTermini(termini)
}

func EmptyPendingHeader() PendingHeader {
	pendingHeader := PendingHeader{}
	pendingHeader.SetTermini(EmptyTermini())
	return pendingHeader
}

func NewPendingHeader(wo *WorkObject, termini Termini) PendingHeader {
	emptyPh := EmptyPendingHeader()
	emptyPh.wo = CopyWorkObject(wo)
	emptyPh.SetTermini(termini)
	return emptyPh
}

func CopyPendingHeader(ph *PendingHeader) *PendingHeader {
	cpy := *ph
	cpy.SetHeader(CopyWorkObject(ph.wo))
	cpy.SetTermini(CopyTermini(ph.Termini()))
	return &cpy
}

// ProtoEncode serializes h into the Quai Proto PendingHeader format
func (ph PendingHeader) ProtoEncode() (*ProtoPendingHeader, error) {
	protoWorkObject, err := ph.WorkObject().ProtoEncode(BlockObject)
	if err != nil {
		return nil, err
	}
	protoTermini := ph.Termini().ProtoEncode()
	return &ProtoPendingHeader{
		Wo:      protoWorkObject,
		Termini: protoTermini,
	}, nil
}

// ProtoEncode deserializes the ProtoHeader into the Header format
func (ph *PendingHeader) ProtoDecode(protoPendingHeader *ProtoPendingHeader, location common.Location) error {
	ph.wo = &WorkObject{}
	err := ph.wo.ProtoDecode(protoPendingHeader.GetWo(), location, BlockObject)
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
	Wo      *WorkObject
	Termini Termini
}

func (t Termini) RPCMarshalTermini() map[string]interface{} {
	result := map[string]interface{}{
		"domTermini": t.DomTermini(),
		"subTermini": t.SubTermini(),
	}
	return result
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
	termini.subTermini = make([]common.Hash, common.MaxWidth)
	termini.domTermini = make([]common.Hash, common.MaxWidth)
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
	t.subTermini = make([]common.Hash, common.MaxWidth)
	for i := 0; i < len(subTermini); i++ {
		t.subTermini[i] = subTermini[i]
	}
}

func (t *Termini) SetDomTermini(domTermini []common.Hash) {
	t.domTermini = make([]common.Hash, common.MaxWidth)
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
	if len(t.subTermini) != common.MaxWidth {
		return false
	}

	if len(t.domTermini) != common.MaxWidth {
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
	domtermini := make([]*common.ProtoHash, common.MaxWidth)
	for i, hash := range t.domTermini {
		domtermini[i] = hash.ProtoEncode()
	}
	subtermini := make([]*common.ProtoHash, common.MaxWidth)
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
