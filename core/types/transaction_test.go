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
)

func TestTransactionProtoEncodeDecode(t *testing.T) {
	t.Skip("Todo: Fix failing test")
	// Create a new transaction
	to := common.BytesToAddress([]byte{0x01}, common.Location{0, 0})
	inner := &QuaiTx{
		ChainID:    new(big.Int).SetUint64(1),
		Nonce:      uint64(0),
		MinerTip:   new(big.Int).SetUint64(0),
		GasPrice:   new(big.Int).SetUint64(0),
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
		ChainID:  new(big.Int).SetUint64(1),
		Nonce:    uint64(1),
		MinerTip: new(big.Int).SetUint64(1),
		GasPrice: new(big.Int).SetUint64(1),
		Gas:      uint64(1),
		To:       &to,
		Value:    new(big.Int).SetUint64(1),
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
	tx := NewTx(inner)
	return tx, tx.Hash()
}

func TestQuaiTxHash(t *testing.T) {
	_, hash := quaiTxData()
	correctHash := common.HexToHash("0x3a203a4f1589fe3a57a68482c048fb28c571b761a42c4cde81767e20a3d0416d")
	require.Equal(t, hash, correctHash, "Hash not equal to expected hash")
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

func FuzzQuaiTxHashingMinerTip(f *testing.F) {
	// Create a new transaction
	tx, hash := quaiTxData()
	f.Add(testUInt64)
	f.Add(tx.inner.minerTip().Uint64())
	// Verify the hash of the transaction
	if hash == (common.Hash{}) {
		f.Errorf("Transaction hash is empty")
	}

	f.Fuzz(func(t *testing.T, i uint64) {
		bi := new(big.Int).SetUint64(i)
		if bi.Cmp(tx.inner.minerTip()) != 0 {
			// change something in the transaction
			newInner := *tx.inner.(*QuaiTx)
			newInner.MinerTip = bi
			// Create a new transaction with the modified inner transaction
			newTx := NewTx(&newInner)

			require.NotEqual(t, newTx.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", tx.inner.minerTip(), bi)
		}
	})
}

func FuzzQuaiTxHashingGasPrice(f *testing.F) {
	// Create a new transaction
	tx, hash := quaiTxData()
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
		Sender:  sender,
		EtxType: 0,
	}
	tx := NewTx(inner)
	return tx, tx.Hash()
}

func TestEtxHash(t *testing.T) {
	_, hash := etxData()
	correctHash := common.HexToHash("0x569200efce076a61575a3661dcb6f59e77e0407279c8db136ef9b2fa23d361ce")
	require.Equal(t, hash, correctHash, "Hash not equal to expected hash")
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

func FuzzEtxType(f *testing.F) {
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
func qiTxData() (*Transaction, common.Hash) {
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
	}
	tx := NewTx(inner)
	return tx, tx.Hash()
}

func TestQiTxHash(t *testing.T) {
	_, hash := qiTxData()
	correctHash := common.HexToHash("0x20b720e4860b3db006d7a19fed6be3efe5619f53f499ef561f42c46bc12b555d")
	require.Equal(t, hash, correctHash, "Hash not equal to expected hash")
}

func FuzzQiTxHashingChainID(f *testing.F) {
	// Create a new transaction
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
	tx, hash := qiTxData()
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
