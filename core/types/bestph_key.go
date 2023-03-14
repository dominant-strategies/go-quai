package types

import (
	"io"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/rlp"
)

type BestPhKeyObject struct {
	key       common.Hash
	entropy   *big.Int
	blockHash common.Hash
}

func NewBestPhKey(key common.Hash, entropy *big.Int, blockHash common.Hash) BestPhKeyObject {
	newBestPhKeyObject := BestPhKeyObject{}
	newBestPhKeyObject.SetKey(key)
	newBestPhKeyObject.SetEntropy(entropy)
	newBestPhKeyObject.SetBlockHash(blockHash)
	return newBestPhKeyObject
}

func (b *BestPhKeyObject) SetKey(key common.Hash) {
	b.key = key
}

func (h *BestPhKeyObject) SetEntropy(val *big.Int) {
	h.entropy = val
}

func (b *BestPhKeyObject) SetBlockHash(blockHash common.Hash) {
	b.blockHash = blockHash
}

func (b *BestPhKeyObject) Key() common.Hash {
	return b.key
}

func (b *BestPhKeyObject) Entropy() *big.Int {
	return b.entropy
}

func (b *BestPhKeyObject) BlockHash() common.Hash {
	return b.blockHash
}

type extBestPhKeyObject struct {
	Key       common.Hash
	Entropy   *big.Int
	BlockHash common.Hash
}

// DecodeRLP decodes the Ethereum
func (b *BestPhKeyObject) DecodeRLP(s *rlp.Stream) error {
	var eb extBestPhKeyObject
	if err := s.Decode(&eb); err != nil {
		return err
	}
	b.key, b.entropy, b.blockHash = eb.Key, eb.Entropy, eb.BlockHash
	return nil
}

// EncodeRLP serializes b into the Ethereum RLP block format.
func (b *BestPhKeyObject) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extBestPhKeyObject{
		Key:       b.key,
		Entropy:   b.entropy,
		BlockHash: b.blockHash,
	})
}
