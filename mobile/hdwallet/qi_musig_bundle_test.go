package hdwallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"google.golang.org/protobuf/proto"
)

func testQiInputRef(hashByte byte, pubKey []byte, index uint16) QiTxInputParam {
	txHash := make([]byte, 32)
	for i := range txHash {
		txHash[i] = hashByte
	}
	return QiTxInputParam{
		TxHash: "0x" + hex.EncodeToString(txHash),
		Index:  index,
		PubKey: append([]byte(nil), pubKey...),
	}
}

func testQiParams(t *testing.T, pubKeys ...[]byte) *QiTxParams {
	t.Helper()

	inputs := make([]QiTxInputParam, len(pubKeys))
	for i, pubKey := range pubKeys {
		inputs[i] = testQiInputRef(byte(i+1), pubKey, uint16(i))
	}

	return &QiTxParams{
		ChainID:  big.NewInt(9),
		TxInputs: inputs,
		TxOutputs: []QiTxOutputParam{
			{
				Denomination: 7,
				Address:      "0x0000000000000000000000000000000000000001",
				Lock:         0,
			},
			{
				Denomination: 7,
				Address:      "0x0000000000000000000000000000000000000002",
				Lock:         0,
			},
		},
	}
}

func TestQiMuSigBundleSessionRoundTrip(t *testing.T) {
	priv0, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key 0: %v", err)
	}
	priv1, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key 1: %v", err)
	}
	priv2, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key 2: %v", err)
	}

	pub0 := crypto.CompressPubkey(&priv0.PublicKey)
	pub1 := crypto.CompressPubkey(&priv1.PublicKey)
	pub2 := crypto.CompressPubkey(&priv2.PublicKey)
	params := testQiParams(t, pub0, pub1, pub2)

	senderBundle, _, senderNonces, _, err := NewQiMuSigBundleSession(params, map[int]*ecdsa.PrivateKey{
		0: priv0,
		1: priv1,
	})
	if err != nil {
		t.Fatalf("create sender bundle: %v", err)
	}
	recipientBundle, _, recipientNonces, _, err := NewQiMuSigBundleSession(params, map[int]*ecdsa.PrivateKey{
		2: priv2,
	})
	if err != nil {
		t.Fatalf("create recipient bundle: %v", err)
	}

	recipientPartials, err := recipientBundle.CreateLocalPartialBundle(senderNonces)
	if err != nil {
		t.Fatalf("recipient partials: %v", err)
	}

	txBytes, _, err := senderBundle.FinalizeSignedTransaction(recipientNonces, recipientPartials)
	if err != nil {
		t.Fatalf("finalize bundle: %v", err)
	}

	valid, err := VerifySignedQiTransaction(txBytes, common.Location{0, 0})
	if err != nil {
		t.Fatalf("verify finalized tx: %v", err)
	}
	if !valid {
		t.Fatal("expected finalized transaction to verify")
	}
}

func TestVerifySignedQiTransactionRejectsTampering(t *testing.T) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	pub := crypto.CompressPubkey(&priv.PublicKey)
	params := testQiParams(t, pub)
	txBytes, err := SignQiTxWithKeys(params, []*ecdsa.PrivateKey{priv}, common.Location{0, 0})
	if err != nil {
		t.Fatalf("sign qi tx: %v", err)
	}

	valid, err := VerifySignedQiTransaction(txBytes, common.Location{0, 0})
	if err != nil {
		t.Fatalf("verify pristine tx: %v", err)
	}
	if !valid {
		t.Fatal("expected pristine tx to verify")
	}

	tx, err := DecodeTransaction(txBytes, common.Location{0, 0})
	if err != nil {
		t.Fatalf("decode signed tx: %v", err)
	}
	protoTx, err := tx.ProtoEncode()
	if err != nil {
		t.Fatalf("proto encode signed tx: %v", err)
	}
	protoTx.Signature[0] ^= 0x01
	tamperedBytes, err := proto.Marshal(protoTx)
	if err != nil {
		t.Fatalf("marshal tampered tx: %v", err)
	}

	valid, err = VerifySignedQiTransaction(tamperedBytes, common.Location{0, 0})
	if err != nil {
		t.Fatalf("verify tampered tx: %v", err)
	}
	if valid {
		t.Fatal("expected tampered transaction to fail verification")
	}
}
