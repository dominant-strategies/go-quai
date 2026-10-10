package hdwallet

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	quaiCrypto "github.com/dominant-strategies/go-quai/crypto"
)

func recoverAddressFromSignature(t *testing.T, digest []byte, sig []byte) string {
	t.Helper()
	if len(sig) != 65 {
		t.Fatalf("signature length = %d, want 65", len(sig))
	}
	sigRec := make([]byte, len(sig))
	copy(sigRec, sig)
	if sigRec[64] >= 27 {
		sigRec[64] -= 27
	}

	pub, err := quaiCrypto.SigToPub(digest, sigRec)
	if err != nil {
		t.Fatalf("recover pubkey failed: %v", err)
	}
	pubBytes := quaiCrypto.FromECDSAPub(pub)
	return "0x" + hex.EncodeToString(quaiCrypto.Keccak256(pubBytes[1:])[12:])
}

func TestSignPersonalMessage(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}
	info, err := w.DeriveAddress(0, common.Location{0, 0})
	if err != nil {
		t.Fatal(err)
	}

	msg := []byte("hello quai")
	sig, err := w.SignPersonalMessage(info.Address, msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 {
		t.Fatalf("signature length = %d", len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("unexpected v byte %d", sig[64])
	}

	recovered := recoverAddressFromSignature(t, PersonalMessageHash(msg), sig)
	if strings.ToLower(recovered) != strings.ToLower(info.Address) {
		t.Fatalf("recovered address mismatch:\n  got:  %s\n  want: %s", recovered, info.Address)
	}
}

func TestSignRawMessage(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}
	info, err := w.DeriveAddress(0, common.Location{0, 0})
	if err != nil {
		t.Fatal(err)
	}

	msg := []byte{0xde, 0xad, 0xbe, 0xef}
	sig, err := w.SignRawMessage(info.Address, msg)
	if err != nil {
		t.Fatal(err)
	}

	recovered := recoverAddressFromSignature(t, quaiCrypto.Keccak256(msg), sig)
	if strings.ToLower(recovered) != strings.ToLower(info.Address) {
		t.Fatalf("recovered address mismatch:\n  got:  %s\n  want: %s", recovered, info.Address)
	}
}

func TestSignTypedDataV4(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}
	info, err := w.DeriveAddress(0, common.Location{0, 0})
	if err != nil {
		t.Fatal(err)
	}

	typedData := `{
		"types": {
			"EIP712Domain": [
				{"name":"name","type":"string"},
				{"name":"version","type":"string"},
				{"name":"chainId","type":"uint256"},
				{"name":"verifyingContract","type":"address"}
			],
			"Mail": [
				{"name":"from","type":"address"},
				{"name":"contents","type":"string"}
			]
		},
		"primaryType":"Mail",
		"domain":{
			"name":"Quai Wallet",
			"version":"1",
			"chainId":9,
			"verifyingContract":"0x0000000000000000000000000000000000000000"
		},
		"message":{
			"from":"` + info.Address + `",
			"contents":"hello typed data"
		}
	}`

	digest, err := TypedDataV4HashJSON([]byte(typedData))
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 32 {
		t.Fatalf("digest length = %d, want 32", len(digest))
	}

	sig, err := w.SignTypedDataV4(info.Address, []byte(typedData))
	if err != nil {
		t.Fatal(err)
	}
	recovered := recoverAddressFromSignature(t, digest, sig)
	if strings.ToLower(recovered) != strings.ToLower(info.Address) {
		t.Fatalf("recovered address mismatch:\n  got:  %s\n  want: %s", recovered, info.Address)
	}
}
