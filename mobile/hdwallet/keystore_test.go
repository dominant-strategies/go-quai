package hdwallet

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/unicode/norm"
)

type keystoreCryptoJSONCompat struct {
	Cipher       string `json:"cipher"`
	Ciphertext   string `json:"ciphertext"`
	KDF          string `json:"kdf"`
	MAC          string `json:"mac"`
	CipherParams struct {
		IV string `json:"iv"`
	} `json:"cipherparams"`
	KDFParams struct {
		Salt  string `json:"salt"`
		N     int    `json:"n"`
		DKLen int    `json:"dklen"`
		P     int    `json:"p"`
		R     int    `json:"r"`
	} `json:"kdfparams"`
}

type keystoreJSONCompat struct {
	Address   string                    `json:"address"`
	ID        string                    `json:"id"`
	Version   int                       `json:"version"`
	Crypto    *keystoreCryptoJSONCompat `json:"Crypto"`
	CryptoLow *keystoreCryptoJSONCompat `json:"crypto"`
}

func (k *keystoreJSONCompat) cryptoObject() *keystoreCryptoJSONCompat {
	if k.Crypto != nil {
		return k.Crypto
	}
	return k.CryptoLow
}

func decryptKeystorePrivateKeyForTest(t *testing.T, ks *keystoreJSONCompat, password string) []byte {
	t.Helper()

	cryptoObj := ks.cryptoObject()
	if cryptoObj == nil {
		t.Fatal("missing Crypto object")
	}
	if cryptoObj.Cipher != "aes-128-ctr" {
		t.Fatalf("unexpected cipher: %s", cryptoObj.Cipher)
	}
	if cryptoObj.KDF != "scrypt" {
		t.Fatalf("unexpected kdf: %s", cryptoObj.KDF)
	}

	salt, err := hex.DecodeString(cryptoObj.KDFParams.Salt)
	if err != nil {
		t.Fatal(err)
	}
	iv, err := hex.DecodeString(cryptoObj.CipherParams.IV)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := hex.DecodeString(cryptoObj.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := hex.DecodeString(cryptoObj.MAC)
	if err != nil {
		t.Fatal(err)
	}

	derivedKey, err := scrypt.Key([]byte(norm.NFKC.String(password)), salt, cryptoObj.KDFParams.N, cryptoObj.KDFParams.R, cryptoObj.KDFParams.P, cryptoObj.KDFParams.DKLen)
	if err != nil {
		t.Fatal(err)
	}
	wantMAC := crypto.Keccak256(derivedKey[16:32], ciphertext)
	if hex.EncodeToString(wantMAC) != strings.ToLower(hex.EncodeToString(mac)) {
		t.Fatal("keystore MAC mismatch")
	}

	block, err := aes.NewCipher(derivedKey[:16])
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCTR(block, iv).XORKeyStream(plain, ciphertext)
	return plain
}

func TestEncryptPrivateKeyHexToKeystoreJSON(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}
	zone := common.Location{0, 0}
	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := w.GetPrivateKeyForAddress(info.Address)
	if err != nil {
		t.Fatal(err)
	}
	privHex := "0x" + hex.EncodeToString(crypto.FromECDSA(priv))

	keystoreJSON, err := EncryptPrivateKeyHexToKeystoreJSON(privHex, "correct horse battery staple", zone)
	if err != nil {
		t.Fatal(err)
	}

	var ks keystoreJSONCompat
	if err := json.Unmarshal([]byte(keystoreJSON), &ks); err != nil {
		t.Fatal(err)
	}
	if ks.Version != 3 {
		t.Fatalf("expected version 3, got %d", ks.Version)
	}
	if ks.ID == "" {
		t.Fatal("missing keystore id")
	}
	if ks.Address != strings.TrimPrefix(strings.ToLower(info.Address), "0x") {
		t.Fatalf("address mismatch: got %s want %s", ks.Address, strings.TrimPrefix(strings.ToLower(info.Address), "0x"))
	}

	plain := decryptKeystorePrivateKeyForTest(t, &ks, "correct horse battery staple")
	if hex.EncodeToString(plain) != strings.TrimPrefix(privHex, "0x") {
		t.Fatal("decrypted private key mismatch")
	}
}

func TestDecryptKeystoreJSONToPrivateKeyHex(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}
	zone := common.Location{0, 0}
	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := w.GetPrivateKeyForAddress(info.Address)
	if err != nil {
		t.Fatal(err)
	}
	wantPrivHex := "0x" + hex.EncodeToString(crypto.FromECDSA(priv))

	ks, err := EncryptPrivateKeyHexToKeystoreJSON(wantPrivHex, "passphrase", zone)
	if err != nil {
		t.Fatal(err)
	}

	gotPrivHex, gotAddr, err := DecryptKeystoreJSONToPrivateKeyHex(ks, "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if gotPrivHex != wantPrivHex {
		t.Fatalf("private key mismatch:\n  got:  %s\n  want: %s", gotPrivHex, wantPrivHex)
	}
	if strings.ToLower(gotAddr) != strings.ToLower(info.Address) {
		t.Fatalf("address mismatch:\n  got:  %s\n  want: %s", gotAddr, info.Address)
	}
}

func TestDecryptKeystoreJSONToPrivateKeyHex_WrongPassword(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}
	zone := common.Location{0, 0}
	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := w.GetPrivateKeyForAddress(info.Address)
	if err != nil {
		t.Fatal(err)
	}

	ks, err := EncryptPrivateKeyToKeystoreJSON(priv, "correct", zone)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecryptKeystoreJSONToPrivateKeyHex(ks, "wrong"); err == nil {
		t.Fatal("expected wrong-password decrypt to fail")
	}
}
