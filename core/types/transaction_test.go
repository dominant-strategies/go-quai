package types

import (
	"bytes"
	"math/big"
	"reflect"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/stretchr/testify/require"
)

func TestTransactionProtoEncodeDecode(t *testing.T) {
	t.Skip("Todo: Fix failing test")
	// Create a new transaction
	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	inner := &QuaiTx{
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
			MaxOutputIndex),
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

// Quai Transaction tests
func quaiTxData() (*Transaction, common.Hash) {
	to := common.HexToAddress("0x00bcdef0123456789abcdef0123456789abcdef2", common.Location{0, 0})
	address := common.HexToAddress("0x3456789abcdef0123456789abcdef0123456789a", common.Location{0, 0})
	parentHash := common.HexToHash("0x456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef3")
	mixHash := common.HexToHash("0x56789abcdef0123456789abcdef0123456789abcdef0123456789abcdef4")
	workNonce := EncodeNonce(1)
	inner := &QuaiTx{
		ChainID:   new(big.Int).SetUint64(1),
		Nonce:     uint64(1),
		GasTipCap: new(big.Int).SetUint64(1),
		GasFeeCap: new(big.Int).SetUint64(1),
		Gas:       uint64(1),
		To:        &to,
		Value:     new(big.Int).SetUint64(1),
		Data:      []byte{0x04},
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
	tx := NewTx(inner)
	return tx, tx.Hash()
}

func fuzzQuaiTxHashingField(f *testing.F, getField func(TxData) *common.Hash, setField func(*QuaiTx, *common.Hash)) {
	// Create a new transaction
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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

func FuzzQuaiTxHashingGasTipCap(f *testing.F) {
	// Create a new transaction
	tx, hash := quaiTxData()
	f.Add(testUInt64)
	f.Add(tx.inner.gasTipCap().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.gasTipCap()) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.GasTipCap = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.gasTipCap(), bi)
		}
	})
}

func FuzzQuaiTxHashingGasFeeCap(f *testing.F) {
	// Create a new transaction
	tx, hash := quaiTxData()
	f.Add(testUInt64)
	f.Add(tx.inner.gasFeeCap().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.gasFeeCap()) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.GasFeeCap = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.gasFeeCap(), bi)
		}
	})
}

func FuzzQuaiTxHashingGas(f *testing.F) {
	// Create a new transaction
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
	tx, hash := quaiTxData()
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
func etxData() (*Transaction, common.Hash) {
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
		Sender: sender,
	}
	tx := NewTx(inner)
	return tx, tx.Hash()
}

func FuzzEtxOriginatingTxHash(f *testing.F) {
	// Create a new transaction
	tx, hash := etxData()
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

func FuzzEtxIndex(f *testing.F) {
	// Create a new transaction
	tx, hash := etxData()
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
	tx, hash := etxData()
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
	tx, hash := etxData()
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
	tx, hash := etxData()
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
	tx, hash := etxData()
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
	tx, hash := etxData()
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
	tx, hash := etxData()
	f.Add(testByte)
	f.Add(tx.inner.etxSender().Bytes())

	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	//Test Nil case
	nilInner := *tx.inner.(*ExternalTx)
	nilInner.To = nil
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
