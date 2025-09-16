package types

import (
	"bytes"
	"io"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
	ltchash "github.com/dominant-strategies/ltcd/chaincfg/chainhash"
	ltcdwire "github.com/dominant-strategies/ltcd/wire"
)

// LitecoinHeaderWrapper wraps ltcdwire.BlockHeader to implement AuxHeaderData
type LitecoinHeaderWrapper struct {
	*ltcdwire.BlockHeader
}

func NewLitecoinHeaderWrapper(header *ltcdwire.BlockHeader) *LitecoinHeaderWrapper {
	return &LitecoinHeaderWrapper{BlockHeader: header}
}

func (ltc *LitecoinHeaderWrapper) PowHash() common.Hash {
	blockHash := ltc.BlockHeader.PowHash()
	return common.BytesToHash(blockHash[:]).Reverse()
}

func (ltc *LitecoinHeaderWrapper) Serialize(wr io.Writer) error {
	return ltc.BlockHeader.Serialize(wr)
}

func (ltc *LitecoinHeaderWrapper) Deserialize(r io.Reader) error {
	ltc.BlockHeader = &ltcdwire.BlockHeader{}
	return ltc.BlockHeader.Deserialize(r)
}

func NewLitecoinBlockHeader(version int32, prevBlockHash [32]byte, merkleRootHash [32]byte, time uint32, bits uint32, nonce uint32) *LitecoinHeaderWrapper {
	prevHash := ltchash.Hash{}
	copy(prevHash[:], prevBlockHash[:])
	merkleRoot := ltchash.Hash{}
	copy(merkleRoot[:], merkleRootHash[:])
	header := ltcdwire.NewBlockHeader(version, &prevHash, &merkleRoot, bits, nonce)
	return &LitecoinHeaderWrapper{BlockHeader: header}
}

func (ltc *LitecoinHeaderWrapper) GetVersion() int32 {
	return ltc.BlockHeader.Version
}

func (ltc *LitecoinHeaderWrapper) GetPrevBlock() [32]byte {
	return [32]byte(ltc.BlockHeader.PrevBlock)
}

func (ltc *LitecoinHeaderWrapper) GetMerkleRoot() [32]byte {
	return [32]byte(ltc.BlockHeader.MerkleRoot)
}

func (ltc *LitecoinHeaderWrapper) GetTimestamp() uint32 {
	return uint32(ltc.BlockHeader.Timestamp.Unix())
}

func (ltc *LitecoinHeaderWrapper) GetBits() uint32 {
	return ltc.BlockHeader.Bits
}

func (ltc *LitecoinHeaderWrapper) GetNonce() uint32 {
	return ltc.BlockHeader.Nonce
}

func (ltc *LitecoinHeaderWrapper) GetNonce64() uint64 {
	// Standard Litecoin headers don't have a 64-bit nonce
	return 0
}

func (ltc *LitecoinHeaderWrapper) GetMixHash() common.Hash {
	// Standard Litecoin headers don't have a mix hash
	return common.Hash{}
}

func (ltc *LitecoinHeaderWrapper) GetHeight() uint32 {
	// Standard Litecoin headers don't include height
	return 0
}

func (ltc *LitecoinHeaderWrapper) GetSealHash() common.Hash {
	// Standard Litecoin headers don't have a seal hash
	return common.Hash{}
}

func (ltc *LitecoinHeaderWrapper) SetNonce(nonce uint32) {
	ltc.BlockHeader.Nonce = nonce
}

func (ltc *LitecoinHeaderWrapper) SetNonce64(nonce uint64) {
	// Standard Litecoin headers don't have a 64-bit nonce, so this is a no-op
}

func (ltc *LitecoinHeaderWrapper) SetMixHash(mixHash common.Hash) {
	// Standard Litecoin headers don't have a mix hash, so this is a no-op
}

func (ltc *LitecoinHeaderWrapper) SetHeight(height uint32) {
	// Standard Litecoin headers don't have a height, so this is a no-op
}

func (ltc *LitecoinHeaderWrapper) Copy() AuxHeaderData {
	copiedHeader := *ltc.BlockHeader
	copiedHeader.Version = ltc.BlockHeader.Version
	copiedHeader.PrevBlock = ltc.BlockHeader.PrevBlock
	copiedHeader.MerkleRoot = ltc.BlockHeader.MerkleRoot
	copiedHeader.Timestamp = ltc.BlockHeader.Timestamp
	copiedHeader.Bits = ltc.BlockHeader.Bits
	copiedHeader.Nonce = ltc.BlockHeader.Nonce
	return &LitecoinHeaderWrapper{BlockHeader: &copiedHeader}
}

// CoinbaseTx functions

func NewLitecoinCoinbaseTx(height uint32, coinbaseOut []byte, auxMerkleRoot common.Hash, signatureTime uint32) []byte {
	coinbaseTx := ltcdwire.NewMsgTx(2)
	// Create the coinbase input with seal hash in scriptSig
	scriptSig := BuildCoinbaseScriptSigWithNonce(height, 0, 0, auxMerkleRoot, params.MerkleSize, signatureTime)
	coinbaseTx.AddTxIn(&ltcdwire.TxIn{
		PreviousOutPoint: ltcdwire.OutPoint{
			Hash:  ltchash.Hash{}, // Coinbase has no previous output
			Index: 0xffffffff,     // Coinbase has no previous output
		},
		SignatureScript: scriptSig,
		Sequence:        0xffffffff,
	})

	var buffer bytes.Buffer
	// Always use non-witness serialization for the template
	// The witness nonce will be added later if needed
	coinbaseTx.SerializeNoWitness(&buffer)

	// The empty serialization adds output count (1 byte = 0x00) + locktime (4 bytes) = 5 bytes
	// We need to trim these before appending the real coinbaseOut
	// Note: coinbaseOut already includes outputs AND locktime (from ExtractCoinbaseOutFromCoinbaseTx)
	raw := buffer.Bytes()
	if len(raw) < 5 {
		return append([]byte{}, coinbaseOut...)
	}

	// Trim empty output count (1 byte) + locktime (4 bytes) = 5 bytes
	trimmed := raw[:len(raw)-5]

	// Append coinbaseOut which contains [output count] [outputs] [locktime]
	return append(trimmed, coinbaseOut...)
}
