package types

import (
	"bytes"
	"io"
	"time"

	btchash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btcdwire "github.com/btcsuite/btcd/wire"
	"github.com/dominant-strategies/go-quai/common"
)

// BitcoinHeaderWrapper wraps btcdwire.BlockHeader to implement AuxHeaderData
type BitcoinHeaderWrapper struct {
	*btcdwire.BlockHeader
}

func NewBitcoinHeaderWrapper(header *btcdwire.BlockHeader) *BitcoinHeaderWrapper {
	return &BitcoinHeaderWrapper{BlockHeader: header}
}

func (bth *BitcoinHeaderWrapper) BlockHash() common.Hash {
	blockHash := bth.BlockHeader.BlockHash()
	return common.BytesToHash(blockHash[:]).Reverse()
}

func (bth *BitcoinHeaderWrapper) PowHash() common.Hash {
	blockHash := bth.BlockHeader.BlockHash()
	return common.BytesToHash(blockHash[:]).Reverse()
}

func (bth *BitcoinHeaderWrapper) Serialize(wr io.Writer) error {
	return bth.BlockHeader.Serialize(wr)
}

func (bth *BitcoinHeaderWrapper) Deserialize(r io.Reader) error {
	bth.BlockHeader = &btcdwire.BlockHeader{}
	return bth.BlockHeader.Deserialize(r)
}

func NewBitcoinBlockHeader(version int32, prevBlockHash [32]byte, merkleRootHash [32]byte, timestamp uint32, bits uint32, nonce uint32) *BitcoinHeaderWrapper {
	prevHash := btchash.Hash{}
	copy(prevHash[:], prevBlockHash[:])
	merkleRoot := btchash.Hash{}
	copy(merkleRoot[:], merkleRootHash[:])
	header := btcdwire.NewBlockHeader(version, &prevHash, &merkleRoot, bits, nonce)
	header.Timestamp = time.Unix(int64(timestamp), 0)
	return &BitcoinHeaderWrapper{BlockHeader: header}
}

func (bth *BitcoinHeaderWrapper) GetVersion() int32 {
	return bth.BlockHeader.Version
}

func (bth *BitcoinHeaderWrapper) GetPrevBlock() [32]byte {
	return bth.BlockHeader.PrevBlock
}

func (bth *BitcoinHeaderWrapper) GetMerkleRoot() [32]byte {
	return bth.BlockHeader.MerkleRoot
}

func (bth *BitcoinHeaderWrapper) GetTimestamp() uint32 {
	return uint32(bth.BlockHeader.Timestamp.Unix())
}

func (bth *BitcoinHeaderWrapper) GetBits() uint32 {
	return bth.BlockHeader.Bits
}

func (bth *BitcoinHeaderWrapper) GetNonce() uint32 {
	return bth.BlockHeader.Nonce
}

func (bth *BitcoinHeaderWrapper) GetNonce64() uint64 {
	// Standard Bitcoin headers don't have a 64-bit nonce
	return 0
}

func (bth *BitcoinHeaderWrapper) GetHeight() uint32 {
	// Standard Bitcoin headers don't include height
	return 0
}

func (bth *BitcoinHeaderWrapper) GetMixHash() common.Hash {
	// Standard Bitcoin headers don't have a mix hash
	return common.Hash{}
}

func (bth *BitcoinHeaderWrapper) GetSealHash() common.Hash {
	// Standard Bitcoin headers don't have a seal hash
	return common.Hash{}
}

func (bth *BitcoinHeaderWrapper) SetNonce(nonce uint32) {
	bth.BlockHeader.Nonce = nonce
}

func (bth *BitcoinHeaderWrapper) SetNonce64(nonce uint64) {
	// Standard Bitcoin headers don't have a 64-bit nonce, so this is a no-op
}

func (bth *BitcoinHeaderWrapper) SetMixHash(mixHash common.Hash) {
	// Standard Bitcoin headers don't have a mix hash, so this is a no-op
}

func (bth *BitcoinHeaderWrapper) SetHeight(height uint32) {
	// Standard Bitcoin headers don't have a height, so this is a no-op
}

func (bth *BitcoinHeaderWrapper) Copy() AuxHeaderData {
	copiedHeader := *bth.BlockHeader
	copiedHeader.Version = bth.BlockHeader.Version
	copiedHeader.PrevBlock = bth.BlockHeader.PrevBlock
	copiedHeader.MerkleRoot = bth.BlockHeader.MerkleRoot
	copiedHeader.Timestamp = bth.BlockHeader.Timestamp
	copiedHeader.Bits = bth.BlockHeader.Bits
	copiedHeader.Nonce = bth.BlockHeader.Nonce
	return &BitcoinHeaderWrapper{BlockHeader: &copiedHeader}
}

func NewBitcoinCoinbaseTx(height uint32, coinbaseOut []byte, sealHash common.Hash, signatureTime uint32) []byte {
	coinbaseTx := btcdwire.NewMsgTx(1)

	// Create the coinbase input with seal hash in scriptSig
	scriptSig := BuildCoinbaseScriptSigWithNonce(height, 0, 0, sealHash, 1, signatureTime)
	coinbaseTx.AddTxIn(&btcdwire.TxIn{
		PreviousOutPoint: btcdwire.OutPoint{
			Hash:  btchash.Hash{}, // Coinbase has no previous output
			Index: 0xffffffff,     // Coinbase has no previous output
		},
		SignatureScript: scriptSig,
		Sequence:        0xffffffff,
	})

	var buffer bytes.Buffer
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
