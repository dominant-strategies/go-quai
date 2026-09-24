package types

import (
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/rlp"
)

func malformedQiKeyTx(last byte, index uint16) *Transaction {
	key := make([]byte, 65)
	key[0] = 4
	key[64] = last
	return NewTx(&QiTx{
		ChainID: big.NewInt(9),
		TxIn: TxIns{{PreviousOutPoint: OutPoint{
			TxHash: common.Hash{4}, Index: index,
		}, PubKey: key}},
		Signature: new(schnorr.Signature),
	})
}

func TestQiDecoderRejectsOffCurveUncompressedKey(t *testing.T) {
	invalid := malformedQiKeyTx(1, 1)
	index := uint32(1)
	protoInput := &ProtoTxIn{
		PreviousOutPoint: &ProtoOutPoint{Hash: common.Hash{4}.ProtoEncode(), Index: &index},
		PubKey:           invalid.TxIn()[0].PubKey,
	}
	var input TxIn
	if err := input.ProtoDecode(protoInput); err == nil {
		t.Fatal("protobuf decoder accepted off-curve key")
	}
	binary, err := invalid.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Transaction
	if err := decoded.UnmarshalBinary(binary); err == nil {
		t.Fatal("RLP decoder accepted off-curve key")
	}

	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	protoInput.PubKey = crypto.FromECDSAPub(&private.PublicKey)
	if err := input.ProtoDecode(protoInput); err != nil {
		t.Fatalf("valid uncompressed key rejected: %v", err)
	}
}

func TestMalformedLocalQiInputsCannotCollideInHashes(t *testing.T) {
	a, b := malformedQiKeyTx(1, 1), malformedQiKeyTx(2, 2)
	if a.Hash() == b.Hash() {
		t.Fatal("distinct invalid inputs collided in transaction hash")
	}
	signer := NewSigner(big.NewInt(9), common.Location{0, 0})
	if signer.Hash(a) == signer.Hash(b) {
		t.Fatal("distinct invalid inputs collided in signing hash")
	}
}

func TestQiRLPDecoderRejectsMalformedSignature(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wire := malformedQiKeyTx(1, 1).inner.(*QiTx).copyToWire()
	wire.TxIn[0].PubKey = crypto.FromECDSAPub(&private.PublicKey)
	wire.Signature = []byte{1}
	encoded, err := rlp.EncodeToBytes(wire)
	if err != nil {
		t.Fatal(err)
	}
	var tx Transaction
	if err := tx.UnmarshalBinary(append([]byte{QiTxType}, encoded...)); err == nil {
		t.Fatal("accepted malformed Qi signature")
	}
}
