package types

import (
	"bytes"
	"math/big"
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func QuaiTxData() *Transaction {
	to := common.HexToAddress("0x00bcdef0123456789abcdef0123456789abcdef2", common.Location{0, 0})
	address := common.HexToAddress("0x0056789abcdef0123456789abcdef0123456789a", common.Location{0, 0})
	parentHash := common.HexToHash("0x456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef3")
	mixHash := common.HexToHash("0x56789abcdef0123456789abcdef0123456789abcdef0123456789abcdef4")
	workNonce := EncodeNonce(1)
	inner := &QuaiTx{
		ChainID:  big.NewInt(1),
		Nonce:    1,
		GasPrice: big.NewInt(1),
		Gas:      1,
		To:       &to,
		Value:    big.NewInt(1),
		Data:     []byte{0x04},
		AccessList: AccessList{AccessTuple{
			Address:     address,
			StorageKeys: []common.Hash{common.HexToHash("0x23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef1")},
		},
		},
		V:          new(big.Int).SetUint64(1),
		R:          new(big.Int).SetUint64(1),
		S:          new(big.Int).SetUint64(1),
		ParentHash: &parentHash,
		MixHash:    &mixHash,
		WorkNonce:  &workNonce,
	}
	return NewTx(inner)
}

func QiTxData() *Transaction {
	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	in := TxIn{
		PreviousOutPoint: *NewOutPoint(&common.Hash{},
			MaxOutputIndex),
		PubKey: []byte{0x04, 0x50, 0x49, 0x5c, 0xb2, 0xf9, 0x53, 0x5c, 0x68, 0x4e, 0xbe, 0x46, 0x87, 0xb5, 0x01, 0xc0, 0xd4, 0x1a, 0x62, 0x3d, 0x68, 0xc1, 0x18, 0xb8, 0xdc, 0xec, 0xd3, 0x93, 0x37, 0x0f, 0x1d, 0x90, 0xe6, 0x5c, 0x4c, 0x6c, 0x44, 0xcd, 0x3f, 0xe8, 0x09, 0xb4, 0x1d, 0xfa, 0xc9, 0x06, 0x0a, 0xd8, 0x4c, 0xb5, 0x7e, 0x2d, 0x57, 0x5f, 0xad, 0x24, 0xd2, 0x5a, 0x7e, 0xfa, 0x33, 0x96, 0xe7, 0x3c, 0x10},
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

	return NewTx(utxo)
}

func TestTransactionProtoEncodeDecode(t *testing.T) {
	// Create a new transaction
	tx := QuaiTxData()
	originalTxHash := tx.Hash()

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
	require.Equal(t, protoTx, secondProtoTx)

	// Compare the original transaction hash and the decoded transaction hash
	decodedTxHash := decodedTx.Hash()

	require.Equal(t, originalTxHash, decodedTxHash)
}

func TestUTXOTransactionEncode(t *testing.T) {
	// Create a new transaction
	tx := QiTxData()

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

// Quai Transaction tests
func TestQuaiTxHash(t *testing.T) {
	tx := QuaiTxData()
	hash := tx.Hash()
	correctHash := common.HexToHash("0x8e508e2bfc0df3034684cbd0c154c0e9facbe108a6d0ab524ace531f91fc6155")
	require.Equal(t, correctHash, hash, "Hash not equal to expected hash")
}

func fuzzQuaiTxHashingField(f *testing.F, getField func(TxData) *common.Hash, setField func(*QuaiTx, *common.Hash)) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		bHash := common.BytesToHash(b)
		hashField := getField(tx.inner)
		if bHash != *hashField {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)

			setField(&newInner, &bHash)
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", getField(tx.inner), bHash)
		}
	})
}

func FuzzQuaiTxHashingChainID(f *testing.F) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.chainID().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.chainID()) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.ChainID = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.chainID(), bi)
		}
	})
}

func FuzzQuaiTxHashingV(f *testing.F) {
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	v, _, _ := tx.inner.getEcdsaSignatureValues()
	f.Add(v.Uint64())
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(v) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.V = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", v, bi)
		}
	})
}

func FuzzQuaiTxHashingR(f *testing.F) {
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	_, r, _ := tx.inner.getEcdsaSignatureValues()
	f.Add(r.Uint64())
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(r) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.V = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", r, bi)
		}
	})
}

func FuzzQuaiTxHashingS(f *testing.F) {
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	_, _, s := tx.inner.getEcdsaSignatureValues()
	f.Add(s.Uint64())
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(s) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.V = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", s, bi)
		}
	})
}

func FuzzQuaiTxHashingNonce(f *testing.F) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.nonce())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		if tx.inner.nonce() != i {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.Nonce = i
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.nonce(), i)
		}
	})
}

func FuzzQuaiTxHashingGasPrice(f *testing.F) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.gasPrice().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.gasPrice()) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.GasPrice = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.gasPrice(), bi)
		}
	})
}

func FuzzQuaiTxHashingGas(f *testing.F) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.gas())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		if tx.inner.gas() != i {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.Gas = i
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.gas(), i)
		}
	})
}

func FuzzQuaiTxHashingTo(f *testing.F) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.inner.to().Bytes())

	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	//Test Nil case
	nilInner := *tx.inner.(*QuaiTx)
	nilInner.To = nil
	// Create a new transaction with the modified inner transaction
	nilTx := NewTx(&nilInner)
	require.NotEqual(f, nilTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.to(), nil)

	f.Fuzz(func(t *testing.T, b []byte) {
		if !tx.inner.to().Equal(common.BytesToAddress(b, common.Location{0, 0})) {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			a := common.BytesToAddress(b, common.Location{0, 0})
			newInner.To = &a
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.to(), a)
		}
	})
}

func FuzzQuaiTxHashingValue(f *testing.F) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.value().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.value()) != 0 {

			newInner := *tx.inner.(*QuaiTx)
			newInner.Value = bi
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.value(), bi)
		}
	})
}

func FuzzQuaiTxHashingData(f *testing.F) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		if !bytes.Equal(tx.inner.data(), b) {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)

			newInner.Data = b
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.to(), b)
		}
	})
}

func FuzzQuaiTxHashingAccessList(f *testing.F) {
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		alItem := tx.inner.(*QuaiTx).accessList()[0]

		//Test with new address
		if !bytes.Equal(alItem.Address.Bytes(), b) {
			newInner := *tx.inner.(*QuaiTx)
			newInner.AccessList[0].Address = common.BytesToAddress(b, common.Location{0, 0})
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", alItem.Address, newInner.AccessList[0].Address)
		}

		//Test with new storage key
		if !bytes.Equal(alItem.Address.Hash().Bytes(), b) {
			newInner := *tx.inner.(*QuaiTx)
			newInner.AccessList[0].StorageKeys = []common.Hash{common.BytesToHash(b)}
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.accessList().StorageKeys(), newInner.AccessList[0].StorageKeys)
		}
	})
}

func FuzzQuaiTxParentHash(f *testing.F) {
	fuzzQuaiTxHashingField(f,
		func(it TxData) *common.Hash { return it.parentHash() },
		func(it *QuaiTx, h *common.Hash) { it.ParentHash = h })
}

func FuzzQuaiTxMixHash(f *testing.F) {
	fuzzQuaiTxHashingField(f,
		func(it TxData) *common.Hash { return it.mixHash() },
		func(it *QuaiTx, h *common.Hash) { it.MixHash = h })
}

func FuzzQuaiTxHashingWorkNonce(f *testing.F) {
	// Create a new transaction
	tx := QuaiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.workNonce().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		if tx.inner.workNonce().Uint64() != i {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newNonce := EncodeNonce(i)
			newInner.WorkNonce = &newNonce
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.workNonce(), i)
		}
	})
}

func TestQiAddressScope(t *testing.T) {
	addr := common.HexToAddress("0x001a1C308B372Fe50E7eA2Df8323d57a08a89f83", common.Location{0, 0})
	t.Log(addr.IsInQiLedgerScope())
	t.Log(addr.IsInQuaiLedgerScope())
}

// ETX hash tests
func etxData() *Transaction {
	to := common.HexToAddress("0x00bcdef0123456789abcdef0123456789abcdef2", common.Location{0, 0})
	address := common.HexToAddress("0x3456789abcdef0123456789abcdef0123456789a", common.Location{0, 0})
	sender := common.HexToAddress("0x89abcdef0123456789abcdef0123456789abcdef0123456789abcdef7", common.Location{0, 0})

	inner := &ExternalTx{
		OriginatingTxHash: common.HexToHash("0x456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef3"),
		Gas:               uint64(1),
		ETXIndex:          uint16(1),
		To:                &to,
		Value:             new(big.Int).SetUint64(1),
		Data:              []byte{0x04},
		AccessList: AccessList{
			AccessTuple{
				Address:     address,
				StorageKeys: []common.Hash{common.HexToHash("0x23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef1")},
			},
		},
		Sender:  sender,
		EtxType: 0,
	}
	tx := NewTx(inner)
	return tx
}

func TestEtxHash(t *testing.T) {
	tx := etxData()
	hash := tx.Hash()
	correctHash := common.HexToHash("0x568300a9abbc999e5209f01c272e93eda5d0b3cf3a6d41132e0f6a545639e18a")
	require.Equal(t, correctHash, hash, "Hash not equal to expected hash")
}

func FuzzEtxOriginatingTxHash(f *testing.F) {
	// Create a new transaction
	tx := etxData()
	hash := tx.Hash()
	f.Add(testByte)
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		bHash := common.BytesToHash(b)
		hashField := tx.inner.originatingTxHash()
		if bHash != hashField {
			// change something in the transaction
			newInner := *tx.inner.(*ExternalTx)

			newInner.OriginatingTxHash = bHash
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.originatingTxHash(), bHash)
		}
	})
}

func FuzzEtxType(f *testing.F) {
	// Create a new transaction
	tx := etxData()
	hash := tx.Hash()
	f.Add(testUInt16)
	f.Add(tx.inner.etxIndex())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint16) {
		if tx.inner.etxIndex() != i {
			// change something in the transaction
			newInner := *tx.inner.(*ExternalTx)
			newInner.ETXIndex = i
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.etxIndex(), i)
		}
	})
}

func FuzzEtxIndex(f *testing.F) {
	// Create a new transaction
	tx := etxData()
	hash := tx.Hash()
	f.Add(testUInt16)
	f.Add(tx.inner.etxIndex())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint16) {
		if tx.inner.etxIndex() != i {
			// change something in the transaction
			newInner := *tx.inner.(*ExternalTx)
			newInner.ETXIndex = i
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.etxIndex(), i)
		}
	})
}

func FuzzEtxGas(f *testing.F) {
	// Create a new transaction
	tx := etxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.gas())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		if tx.inner.gas() != i {
			// change something in the transaction
			newInner := *tx.inner.(*ExternalTx)
			newInner.Gas = i
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.gas(), i)
		}
	})
}

func FuzzEtxTo(f *testing.F) {
	// Create a new transaction
	tx := etxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.inner.to().Bytes())

	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	//Test Nil case
	nilInner := *tx.inner.(*ExternalTx)
	var temp [common.AddressLength]byte
	nilInner.To = &common.Address{}
	emptyAddress := common.Bytes20ToAddress(temp, common.Location{0, 0})
	nilInner.To = &emptyAddress
	// Create a new transaction with the modified inner transaction
	nilTx := NewTx(&nilInner)
	require.NotEqual(f, nilTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.to(), nil)

	f.Fuzz(func(t *testing.T, b []byte) {
		if !tx.inner.to().Equal(common.BytesToAddress(b, common.Location{0, 0})) {
			// change something in the transaction
			newInner := *tx.inner.(*ExternalTx)
			a := common.BytesToAddress(b, common.Location{0, 0})
			newInner.To = &a
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.to(), a)
		}
	})
}

func FuzzEtxValue(f *testing.F) {
	// Create a new transaction
	tx := etxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.value().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.value()) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*ExternalTx)
			newInner.Value = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.value(), bi)
		}
	})
}

func FuzzEtxData(f *testing.F) {
	// Create a new transaction
	tx := etxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.inner.data())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		if !bytes.Equal(tx.inner.data(), b) {
			// change something in the transaction
			newInner := *tx.inner.(*ExternalTx)

			newInner.Data = b
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.to(), b)
		}
	})
}

func FuzzEtxAccessList(f *testing.F) {
	tx := etxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.inner.(*ExternalTx).accessList()[0].Address.Bytes())
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		alItem := tx.inner.(*ExternalTx).accessList()[0]

		//Test with new address
		if !bytes.Equal(alItem.Address.Bytes(), b) {
			newInner := *tx.inner.(*ExternalTx)
			newInner.AccessList[0].Address = common.BytesToAddress(b, common.Location{0, 0})
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", alItem.Address, newInner.AccessList[0].Address)
		}

		//Test with new storage key
		if !bytes.Equal(alItem.Address.Hash().Bytes(), b) {
			newInner := *tx.inner.(*ExternalTx)
			newInner.AccessList[0].StorageKeys = []common.Hash{common.BytesToHash(b)}
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.accessList().StorageKeys(), newInner.AccessList[0].StorageKeys)
		}
	})
}

func FuzzEtxSender(f *testing.F) {
	// Create a new transaction
	tx := etxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.inner.etxSender().Bytes())

	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		if !tx.inner.etxSender().Equal(common.BytesToAddress(b, common.Location{0, 0})) {
			// change something in the transaction
			newInner := *tx.inner.(*ExternalTx)
			newInner.Sender = common.BytesToAddress(b, common.Location{0, 0})
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.etxSender(), newInner.Sender)
		}
	})
}

// QiTx hash tests
func qiTxData() *Transaction {
	parentHash := common.HexToHash("0x456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef3")
	mixHash := common.HexToHash("0x56789abcdef0123456789abcdef0123456789abcdef0123456789abcdef4")
	txHash := common.HexToHash("0x3a203a4f1589fe3a57a68482c048fb28c571b761a42c4cde81767e20a3d0416d")
	workNonce := EncodeNonce(1)

	outpoint := NewOutPoint(&txHash, 1)
	inner := &QiTx{
		ChainID:    new(big.Int).SetUint64(1),
		TxIn:       TxIns{*NewTxIn(outpoint, []byte{0x04, 0x50, 0x49, 0x5c, 0xb2, 0xf9, 0x53, 0x5c, 0x68, 0x4e, 0xbe, 0x46, 0x87, 0xb5, 0x01, 0xc0, 0xd4, 0x1a, 0x62, 0x3d, 0x68, 0xc1, 0x18, 0xb8, 0xdc, 0xec, 0xd3, 0x93, 0x37, 0x0f, 0x1d, 0x90, 0xe6, 0x5c, 0x4c, 0x6c, 0x44, 0xcd, 0x3f, 0xe8, 0x09, 0xb4, 0x1d, 0xfa, 0xc9, 0x06, 0x0a, 0xd8, 0x4c, 0xb5, 0x7e, 0x2d, 0x57, 0x5f, 0xad, 0x24, 0xd2, 0x5a, 0x7e, 0xfa, 0x33, 0x96, 0xe7, 0x3c, 0x10}, [][]byte{{0x01}})},
		TxOut:      TxOuts{*NewTxOut(1, []byte{0x2}, big.NewInt(1))},
		ParentHash: &parentHash,
		MixHash:    &mixHash,
		WorkNonce:  &workNonce,
		Data:       []byte{0x01},
	}
	tx := NewTx(inner)
	return tx
}

func TestQiTxHash(t *testing.T) {
	tx := qiTxData()
	hash := tx.Hash()
	correctHash := common.HexToHash("0x3af33a88f7fc439f4f0a30faec9d2cfe439bcbd802cd515100f7b4d24590e88c")
	require.Equal(t, correctHash, hash, "Hash not equal to expected hash")
}

func FuzzQiTxHashingChainID(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.chainID().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.chainID()) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QiTx)
			newInner.ChainID = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.chainID(), bi)
		}
	})
}

func FuzzQiTxHashingTxInOutPointTxHash(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.TxIn()[0].PreviousOutPoint.TxHash.Bytes())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		//change PreviousOutPoint TxHash
		h := common.BytesToHash(b)
		if h != tx.TxIn()[0].PreviousOutPoint.TxHash {
			op := NewOutPoint(&h, tx.TxIn()[0].PreviousOutPoint.Index)
			newInner := *tx.inner.(*QiTx)
			// Create a deep copy of the TxIn slice
			newInner.TxIn = make(TxIns, len(tx.inner.(*QiTx).TxIn))
			copy(newInner.TxIn, tx.inner.(*QiTx).TxIn)
			newInner.TxIn[0].PreviousOutPoint = *op
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision TxHash\noriginal: %v, modified: %v", tx.TxIn()[0].PreviousOutPoint.TxHash, &h)
		}
	})
}

func FuzzQiTxHashingTxInOutPointIndex(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testUInt16)
	f.Add(tx.TxIn()[0].PreviousOutPoint.Index)
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}
	f.Fuzz(func(t *testing.T, ui uint16) {
		//change previousOutPoint Index
		if ui != tx.TxIn()[0].PreviousOutPoint.Index {
			op := NewOutPoint(&tx.TxIn()[0].PreviousOutPoint.TxHash, ui)
			newInner := *tx.inner.(*QiTx)
			// Create a deep copy of the TxIn slice
			newInner.TxIn = make(TxIns, len(tx.inner.(*QiTx).TxIn))
			copy(newInner.TxIn, tx.inner.(*QiTx).TxIn)
			newInner.TxIn[0].PreviousOutPoint = *op
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision \noriginal: %v, modified: %v", tx.TxIn()[0].PreviousOutPoint.Index, ui)
		}
	})
}

func FuzzQiTxHashingTxInOutPointPubKey(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.TxIn()[0].PubKey)
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if !bytes.Equal(b, tx.TxIn()[0].PubKey) {
			newInner := *tx.inner.(*QiTx)
			// Create a deep copy of the TxIn slice
			newInner.TxIn = make(TxIns, len(tx.inner.(*QiTx).TxIn))
			copy(newInner.TxIn, tx.inner.(*QiTx).TxIn)
			newInner.TxIn[0].PubKey = b
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision on PreviousOutPoing\noriginal: %v, modified: %v", tx.TxIn()[0].PubKey, b)
		}
	})
}

func FuzzQiTxHashingTxOutDenomination(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testUInt8)
	f.Add(tx.TxOut()[0].Denomination)
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}
	f.Fuzz(func(t *testing.T, ui uint8) {
		//change previousOutPoint Index
		if ui != tx.TxOut()[0].Denomination {
			newInner := *tx.inner.(*QiTx)
			// Create a deep copy of the TxOut slice
			newInner.TxOut = make(TxOuts, len(tx.inner.(*QiTx).TxOut))
			copy(newInner.TxOut, tx.inner.(*QiTx).TxOut)
			newInner.TxOut[0].Denomination = ui
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision \noriginal: %v, modified: %v", tx.TxOut()[0].Denomination, ui)
		}
	})
}

func FuzzQiTxHashingTxOutAddress(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.inner.txOut()[0].Address)

	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if !bytes.Equal(b, tx.TxOut()[0].Address) {
			newInner := *tx.inner.(*QiTx)
			// Create a deep copy of the TxOut slice
			newInner.TxOut = make(TxOuts, len(tx.inner.(*QiTx).TxOut))
			copy(newInner.TxOut, tx.inner.(*QiTx).TxOut)
			// Change Address Value and create a new TX
			newInner.TxOut[0].Address = b
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision on PreviousOutPoing\noriginal: %v, modified: %v", tx.TxOut()[0].Address, b)
		}
	})
}

func FuzzQiTxHashingTxOutLock(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.txOut()[0].Lock.Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.txOut()[0].Lock) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QiTx)
			// Create a deep copy of the TxOut slice
			newInner.TxOut = make(TxOuts, len(tx.inner.(*QiTx).TxOut))
			copy(newInner.TxOut, tx.inner.(*QiTx).TxOut)
			newInner.TxOut[0].Lock = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.txOut()[0].Lock, bi)
		}
	})
}

func FuzzQiTxHashingSignature(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	f.Add(tx.GetSchnorrSignature().Serialize())

	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		privKey, _ := btcec.PrivKeyFromBytes(b)
		if !bytes.Equal(b, tx.GetSchnorrSignature().Serialize()) {
			newInner := *tx.inner.(*QiTx)
			// Change Signature Value and create a new TX
			var err error
			newInner.Signature, err = schnorr.Sign(privKey, tx.Hash().Bytes()[:])
			if err != nil {
				t.Fatalf("schnorr signing failed!")
			}
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision on PreviousOutPoing\noriginal: %v, modified: %v", tx.TxOut()[0].Address, b)
		}
	})
}

func fuzzQitxHashingField(f *testing.F, getField func(TxData) *common.Hash, setField func(*QiTx, *common.Hash)) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testByte)
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		bHash := common.BytesToHash(b)
		hashField := getField(tx.inner)
		if bHash != *hashField {
			// change something in the transaction
			newInner := *tx.inner.(*QiTx)

			setField(&newInner, &bHash)
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)
			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", getField(tx.inner), bHash)
		}
	})
}

func FuzzQitxParentHash(f *testing.F) {
	fuzzQitxHashingField(f,
		func(it TxData) *common.Hash { return it.parentHash() },
		func(it *QiTx, h *common.Hash) { it.ParentHash = h })
}

func FuzzQitxMixHash(f *testing.F) {
	fuzzQitxHashingField(f,
		func(it TxData) *common.Hash { return it.mixHash() },
		func(it *QiTx, h *common.Hash) { it.MixHash = h })
}

func FuzzQiTxHashingWorkNonce(f *testing.F) {
	// Create a new transaction
	tx := qiTxData()
	hash := tx.Hash()
	f.Add(testUInt64)
	f.Add(tx.inner.workNonce().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		if tx.inner.workNonce().Uint64() != i {
			// change something in the transaction
			newInner := *tx.inner.(*QiTx)
			newNonce := EncodeNonce(i)
			newInner.WorkNonce = &newNonce
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.workNonce(), i)
		}
	})
}

func TestCoinbaseTxEncodeDecode(t *testing.T) {
	toAddr := common.HexToAddress("0x0013e45aa16163F2663015b6695894D918866d19", common.Location{0, 0})
	coinbaseTx := NewTx(&ExternalTx{To: &toAddr, Value: big.NewInt(10), Data: []byte{1}, EtxType: DefaultType, OriginatingTxHash: common.HexToHash("0xa"), Sender: common.HexToAddress("0x0", common.Location{0, 0})})

	// Encode transaction
	protoTx, err := coinbaseTx.ProtoEncode()
	require.Equal(t, err, nil)

	// Decode transaction
	tx := Transaction{}
	err = tx.ProtoDecode(protoTx, common.Location{0, 0})
	require.Equal(t, err, nil)

	reflect.DeepEqual(coinbaseTx, tx)

	require.Equal(t, coinbaseTx.Hash(), tx.Hash())
}

func TestTxNilDecode(t *testing.T) {
	// If empty data is sent from the wire or from rpc
	// proto.Unmarshal should read it and not return any error
	protoTransaction := new(ProtoTransaction)
	err := proto.Unmarshal(nil, protoTransaction)
	require.Equal(t, err, nil)

	// This empty protoTransaction struct should not crash the proto decode
	tx := Transaction{}
	err = tx.ProtoDecode(protoTransaction, common.Location{0, 0})
	require.NotEqual(t, err, nil)
}

func TestNilChainIDDecode(t *testing.T) {
	// get an empty quai tx
	quaiTx := QuaiTxData()

	protoQuaiTx, err := quaiTx.ProtoEncode()
	require.Equal(t, err, nil)

	// set the proto quai tx chain id to nil
	protoQuaiTx.ChainId = nil

	tx := Transaction{}
	err = tx.ProtoDecode(protoQuaiTx, common.Location{0, 0})

	// This should print missing required field 'ChainId'
	t.Log(err)

	require.NotEqual(t, err, nil)
}

func TestLocal(t *testing.T) {
	tests := []struct {
		name string
		tx   *Transaction
	}{
		{
			"QuaiTx",
			QuaiTxData(),
		},
		{
			"QiTx",
			qiTxData(),
		},
		{
			"ExternalTx",
			etxData(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.tx.SetLocal(true)
			require.True(t, tt.tx.IsLocal())

			tt.tx.SetLocal(false)
			require.False(t, tt.tx.IsLocal())
		})
	}
}

func TestTransactionHash(t *testing.T) {
	tests := []struct {
		name        string
		tx          func() *Transaction
		shouldEqual bool
	}{
		{
			"QuaiTx",
			QuaiTxData,
			// Should not equal because our VRS are made up and don't map to any zone
			// This means that we are actually using the location variable
			false,
		},
		{
			"QiTx",
			qiTxData,
			true,
		},
		{
			"ExternalTx",
			etxData,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, loc := range locations {
				// Generate two different transactions to avoid caching
				originalHash := tt.tx().Hash()
				locProvidedHash := tt.tx().Hash(loc...)

				if tt.shouldEqual {
					require.Equal(t, originalHash, locProvidedHash)
				} else {
					require.NotEqual(t, originalHash, locProvidedHash)
				}
			}
		})
	}
}
