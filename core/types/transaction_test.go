package types

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/stretchr/testify/require"
)

func TestTransactionProtoEncodeDecode(t *testing.T) {
	// Create a new transaction
	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	inner := &InternalTx{
		ChainID:    new(big.Int).SetUint64(1),
		Nonce:      uint64(0),
		GasTipCap:  new(big.Int).SetUint64(0),
		GasFeeCap:  new(big.Int).SetUint64(0),
		Gas:        uint64(0),
		To:         &to,
		Value:      new(big.Int).SetUint64(0),
		Data:       []byte{0x04},
		AccessList: AccessList{},
		V:          new(big.Int).SetUint64(0),
		R:          new(big.Int).SetUint64(0),
		S:          new(big.Int).SetUint64(0),
	}
	tx := NewTx(inner)

	// Encode the transaction to ProtoTransaction format
	protoTx, err := tx.ProtoEncode()
	if err != nil {
		t.Errorf("Failed to encode transaction: %v", err)
	}

	t.Log("protoTx", protoTx)

	// Decode the ProtoTransaction into a new Transaction
	decodedTx := &Transaction{}
	err = decodedTx.ProtoDecode(protoTx, common.Location{})
	if err != nil {
		t.Errorf("Failed to decode transaction: %v", err)
	}

	// Encode the transaction to ProtoTransaction format
	secondProtoTx, err := decodedTx.ProtoEncode()
	if err != nil {
		t.Errorf("Failed to encode transaction: %v", err)
	}
	t.Log("secondProtoTx", secondProtoTx)

	// Compare the original transaction and the decoded transaction
	if !reflect.DeepEqual(tx, decodedTx) {
		t.Errorf("Decoded transaction does not match the original transaction")
	}
	require.Equal(t, protoTx, secondProtoTx)
}

func TestUTXOTransactionEncode(t *testing.T) {
	// Create a new transaction
	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	in := TxIn{
		PreviousOutPoint: *NewOutPoint(&common.Hash{},
			MaxPrevOutIndex),
	}

	newOut := TxOut{
		Denomination: uint8(1),
		Address:      to.Bytes(),
	}

	utxo := &QiTx{
		ChainID: big.NewInt(1337),
		TxIn:    TxIns{in},
		TxOut:   TxOuts{newOut},
	}

	tx := NewTx(utxo)

	// Encode the transaction to ProtoTransaction format
	protoTx, err := tx.ProtoEncode()
	if err != nil {
		t.Errorf("Failed to encode transaction: %v", err)
	}

	t.Log("protoTx", protoTx)

	// Decode the ProtoTransaction into a new Transaction
	decodedTx := &Transaction{}
	err = decodedTx.ProtoDecode(protoTx, common.Location{})
	if err != nil {
		t.Errorf("Failed to decode transaction: %v", err)
	}

	// Encode the transaction to ProtoTransaction format
	secondProtoTx, err := decodedTx.ProtoEncode()
	if err != nil {
		t.Errorf("Failed to encode transaction: %v", err)
	}
	t.Log("secondProtoTx", secondProtoTx)

	require.Equal(t, protoTx, secondProtoTx)

}

func TestTransactionHashing(t *testing.T) {
	// Create a new transaction
	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	inner := &InternalTx{
		ChainID:    new(big.Int).SetUint64(1),
		Nonce:      uint64(0),
		GasTipCap:  new(big.Int).SetUint64(0),
		GasFeeCap:  new(big.Int).SetUint64(0),
		Gas:        uint64(0),
		To:         &to,
		Value:      new(big.Int).SetUint64(0),
		Data:       []byte{0x04},
		AccessList: AccessList{},
		V:          new(big.Int).SetUint64(0),
		R:          new(big.Int).SetUint64(0),
		S:          new(big.Int).SetUint64(0),
	}
	tx := NewTx(inner)

	// Calculate the hash of the transaction
	hash := tx.Hash()

	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		t.Errorf("Transaction hash is empty")
	}

	// change something in the transaction
	newInner := inner
	newInner.Nonce = uint64(1)

	// Create a new transaction with the modified inner transaction
	newTx := NewTx(newInner)
	txHash := newTx.Hash()

	require.NotEqual(t, hash, txHash)

}

func TestQiAddressScope(t *testing.T) {
	addr := common.HexToAddress("0x001a1C308B372Fe50E7eA2Df8323d57a08a89f83", common.Location{0, 0})
	t.Log(addr.IsInQiLedgerScope())
	t.Log(addr.IsInQuaiLedgerScope())
}
