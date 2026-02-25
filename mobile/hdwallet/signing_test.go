package hdwallet

import (
	"math/big"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
)

func TestSignQuaiTx(t *testing.T) {
	// Create a wallet and derive an address
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}

	zone := common.Location{0, 0}
	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	privKey, err := w.GetPrivateKeyForAddress(info.Address)
	if err != nil {
		t.Fatal(err)
	}

	// Create and sign a QuaiTx
	params := &QuaiTxParams{
		ChainID:  big.NewInt(9000),
		Nonce:    0,
		GasPrice: big.NewInt(1000000000), // 1 Gwei
		Gas:      21000,
		To:       info.Address, // send to self
		Value:    big.NewInt(1000000000000000000), // 1 QUAI
		Data:     nil,
	}

	signedBytes, err := SignQuaiTx(params, privKey, zone)
	if err != nil {
		t.Fatal(err)
	}

	if len(signedBytes) == 0 {
		t.Fatal("signed transaction bytes should not be empty")
	}

	// Decode the signed transaction
	tx, err := DecodeTransaction(signedBytes, zone)
	if err != nil {
		t.Fatal(err)
	}

	if tx.Type() != types.QuaiTxType {
		t.Fatalf("expected QuaiTxType, got %d", tx.Type())
	}
	if tx.Nonce() != 0 {
		t.Fatalf("expected nonce 0, got %d", tx.Nonce())
	}
	if tx.Gas() != 21000 {
		t.Fatalf("expected gas 21000, got %d", tx.Gas())
	}

	// Recover the sender
	signer := types.NewSigner(big.NewInt(9000), zone)
	sender, err := types.Sender(signer, tx)
	if err != nil {
		t.Fatalf("failed to recover sender: %v", err)
	}

	// Verify the sender matches our derived address
	expectedAddr := crypto.PubkeyToAddress(privKey.PublicKey, zone)
	if sender.Hex() != expectedAddr.Hex() {
		t.Fatalf("sender mismatch:\n  got:  %s\n  want: %s", sender.Hex(), expectedAddr.Hex())
	}

	t.Logf("Signed QuaiTx: %d bytes, sender: %s", len(signedBytes), sender.Hex())
}

func TestSignQuaiTx_RoundTrip(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}

	zone := common.Location{0, 0}
	info, _ := w.DeriveAddress(0, zone)
	privKey, _ := w.GetPrivateKeyForAddress(info.Address)

	params := &QuaiTxParams{
		ChainID:  big.NewInt(9000),
		Nonce:    42,
		GasPrice: big.NewInt(2000000000),
		Gas:      50000,
		To:       info.Address,
		Value:    big.NewInt(500),
		Data:     []byte{0xde, 0xad},
	}

	signedBytes, err := SignQuaiTx(params, privKey, zone)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := DecodeTransaction(signedBytes, zone)
	if err != nil {
		t.Fatal(err)
	}

	if tx.Nonce() != 42 {
		t.Fatalf("nonce mismatch: got %d", tx.Nonce())
	}
	if tx.Gas() != 50000 {
		t.Fatalf("gas mismatch: got %d", tx.Gas())
	}
	if tx.Value().Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("value mismatch: got %s", tx.Value().String())
	}
}

func TestSignQiTx(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQi)
	if err != nil {
		t.Fatal(err)
	}

	zone := common.Location{0, 0}
	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	privKey, err := w.GetPrivateKeyForAddress(info.Address)
	if err != nil {
		t.Fatal(err)
	}

	// Compressed public key for the input
	pubKeyBytes := crypto.CompressPubkey(&privKey.PublicKey)

	// Create a fake previous outpoint
	params := &QiTxParams{
		ChainID: big.NewInt(9000),
		TxInputs: []QiTxInputParam{
			{
				TxHash: "0x0000000000000000000000000000000000000000000000000000000000000001",
				Index:  0,
				PubKey: pubKeyBytes,
			},
		},
		TxOutputs: []QiTxOutputParam{
			{
				Denomination: 5,
				Address:      info.Address,
				Lock:         0,
			},
		},
	}

	signedBytes, err := SignQiTx(params, privKey, zone)
	if err != nil {
		t.Fatal(err)
	}

	if len(signedBytes) == 0 {
		t.Fatal("signed QiTx bytes should not be empty")
	}

	// Decode the signed transaction
	tx, err := DecodeTransaction(signedBytes, zone)
	if err != nil {
		t.Fatal(err)
	}

	if tx.Type() != types.QiTxType {
		t.Fatalf("expected QiTxType, got %d", tx.Type())
	}

	t.Logf("Signed QiTx: %d bytes", len(signedBytes))
}

func TestDecodeTransaction_Invalid(t *testing.T) {
	_, err := DecodeTransaction([]byte{0xff, 0xfe}, common.Location{0, 0})
	if err == nil {
		t.Fatal("expected error for invalid proto bytes")
	}
}

func TestBenchmark100ECDSASignatures(t *testing.T) {
	const count = 100

	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}

	zone := common.Location{0, 0}
	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	privKey, err := w.GetPrivateKeyForAddress(info.Address)
	if err != nil {
		t.Fatal(err)
	}

	// Empty data payload
	paramsEmpty := &QuaiTxParams{
		ChainID:  big.NewInt(9000),
		Nonce:    0,
		GasPrice: big.NewInt(1000000000),
		Gas:      21000,
		To:       info.Address,
		Value:    big.NewInt(1000000000000000000),
		Data:     nil,
	}

	start := time.Now()
	for i := 0; i < count; i++ {
		paramsEmpty.Nonce = uint64(i)
		_, err := SignQuaiTx(paramsEmpty, privKey, zone)
		if err != nil {
			t.Fatalf("failed at signature %d: %v", i, err)
		}
	}
	elapsedEmpty := time.Since(start)
	perSigEmpty := elapsedEmpty / count

	// 10 KB data payload
	bigData := make([]byte, 10*1024)
	for i := range bigData {
		bigData[i] = byte(i % 256)
	}

	paramsData := &QuaiTxParams{
		ChainID:  big.NewInt(9000),
		Nonce:    0,
		GasPrice: big.NewInt(1000000000),
		Gas:      200000,
		To:       info.Address,
		Value:    big.NewInt(0),
		Data:     bigData,
	}

	start = time.Now()
	for i := 0; i < count; i++ {
		paramsData.Nonce = uint64(i)
		_, err := SignQuaiTx(paramsData, privKey, zone)
		if err != nil {
			t.Fatalf("failed at signature %d (10KB): %v", i, err)
		}
	}
	elapsedData := time.Since(start)
	perSigData := elapsedData / count

	t.Logf("Empty data: %d sigs in %s (%.2f ms/sig)", count, elapsedEmpty, float64(perSigEmpty.Microseconds())/1000.0)
	t.Logf("10 KB data: %d sigs in %s (%.2f ms/sig)", count, elapsedData, float64(perSigData.Microseconds())/1000.0)
}
