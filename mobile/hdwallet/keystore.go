package hdwallet

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dominant-strategies/go-quai/common"
	quaiCrypto "github.com/dominant-strategies/go-quai/crypto"
	"github.com/google/uuid"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/unicode/norm"
)

const (
	keystoreVersion = 3
	keyHeaderKDF    = "scrypt"

	keystoreScryptN = 1 << 17 // Match quais.js default N
	keystoreScryptP = 1       // Match quais.js default P

	scryptR     = 8
	scryptDKLen = 32
)

var ErrDecrypt = errors.New("could not decrypt key with given password")

type cipherparamsJSON struct {
	IV string `json:"iv"`
}

type cryptoJSON struct {
	Cipher       string                 `json:"cipher"`
	CipherText   string                 `json:"ciphertext"`
	CipherParams cipherparamsJSON       `json:"cipherparams"`
	KDF          string                 `json:"kdf"`
	KDFParams    map[string]interface{} `json:"kdfparams"`
	MAC          string                 `json:"mac"`
}

// Emit lowercase "crypto" to match go-ethereum output.
type encryptedKeyJSONV3 struct {
	Address string     `json:"address"`
	Crypto  cryptoJSON `json:"crypto"`
	ID      string     `json:"id"`
	Version int        `json:"version"`
}

// Accept either "crypto" (geth) or "Crypto" (some JS tooling).
type encryptedKeyJSONV3Compat struct {
	Address string      `json:"address"`
	Crypto  cryptoJSON  `json:"crypto"`
	CryptoU *cryptoJSON `json:"Crypto"`
	ID      string      `json:"id"`
	Version interface{} `json:"version"`
}

func (k encryptedKeyJSONV3Compat) cryptoObject() (cryptoJSON, error) {
	if k.Crypto.Cipher != "" {
		return k.Crypto, nil
	}
	if k.CryptoU != nil {
		return *k.CryptoU, nil
	}
	return cryptoJSON{}, fmt.Errorf("missing crypto object")
}

func normalizedPassword(password string) string {
	return norm.NFKC.String(password)
}

func addressBytesFromPrivateKey(privKey *ecdsa.PrivateKey) ([]byte, error) {
	pubBytes := quaiCrypto.FromECDSAPub(&privKey.PublicKey)
	if len(pubBytes) == 0 {
		return nil, fmt.Errorf("failed to derive public key")
	}
	return quaiCrypto.Keccak256(pubBytes[1:])[12:], nil
}

func aesCTRXOR(key, inText, iv []byte) ([]byte, error) {
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	stream := cipher.NewCTR(aesBlock, iv)
	outText := make([]byte, len(inText))
	stream.XORKeyStream(outText, inText)
	return outText, nil
}

func encryptDataV3(data, auth []byte, scryptN, scryptP int) (cryptoJSON, error) {
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		panic("reading from crypto/rand failed: " + err.Error())
	}
	derivedKey, err := scrypt.Key(auth, salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return cryptoJSON{}, err
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		panic("reading from crypto/rand failed: " + err.Error())
	}
	cipherText, err := aesCTRXOR(derivedKey[:16], data, iv)
	if err != nil {
		return cryptoJSON{}, err
	}
	mac := quaiCrypto.Keccak256(derivedKey[16:32], cipherText)

	kdfParams := map[string]interface{}{
		"n":     scryptN,
		"r":     scryptR,
		"p":     scryptP,
		"dklen": scryptDKLen,
		"salt":  hex.EncodeToString(salt),
	}
	return cryptoJSON{
		Cipher:       "aes-128-ctr",
		CipherText:   hex.EncodeToString(cipherText),
		CipherParams: cipherparamsJSON{IV: hex.EncodeToString(iv)},
		KDF:          keyHeaderKDF,
		KDFParams:    kdfParams,
		MAC:          hex.EncodeToString(mac),
	}, nil
}

func decodeHexField(name, v string) ([]byte, error) {
	b, err := hex.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", name, err)
	}
	return b, nil
}

func ensureInt(x interface{}) (int, error) {
	switch v := x.(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, err
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("expected numeric kdf param, got %T", x)
	}
}

func getKDFString(params map[string]interface{}, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing kdf param %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("kdf param %q must be string", key)
	}
	return s, nil
}

func getKDFInt(params map[string]interface{}, key string) (int, error) {
	v, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("missing kdf param %q", key)
	}
	return ensureInt(v)
}

func getKDFKey(cj cryptoJSON, auth string) ([]byte, error) {
	saltHex, err := getKDFString(cj.KDFParams, "salt")
	if err != nil {
		return nil, err
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, err
	}
	dkLen, err := getKDFInt(cj.KDFParams, "dklen")
	if err != nil {
		return nil, err
	}
	authBytes := []byte(auth)

	if cj.KDF == keyHeaderKDF {
		n, err := getKDFInt(cj.KDFParams, "n")
		if err != nil {
			return nil, err
		}
		r, err := getKDFInt(cj.KDFParams, "r")
		if err != nil {
			return nil, err
		}
		p, err := getKDFInt(cj.KDFParams, "p")
		if err != nil {
			return nil, err
		}
		return scrypt.Key(authBytes, salt, n, r, p, dkLen)
	}
	if cj.KDF == "pbkdf2" {
		c, err := getKDFInt(cj.KDFParams, "c")
		if err != nil {
			return nil, err
		}
		prf, err := getKDFString(cj.KDFParams, "prf")
		if err != nil {
			return nil, err
		}
		if prf != "hmac-sha256" {
			return nil, fmt.Errorf("unsupported PBKDF2 PRF: %s", prf)
		}
		return pbkdf2.Key(authBytes, salt, c, dkLen, sha256.New), nil
	}
	return nil, fmt.Errorf("unsupported KDF: %s", cj.KDF)
}

func decryptDataV3(cj cryptoJSON, auth string) ([]byte, error) {
	if cj.Cipher != "aes-128-ctr" {
		return nil, fmt.Errorf("cipher not supported: %v", cj.Cipher)
	}
	mac, err := decodeHexField("mac", cj.MAC)
	if err != nil {
		return nil, err
	}
	iv, err := decodeHexField("iv", cj.CipherParams.IV)
	if err != nil {
		return nil, err
	}
	cipherText, err := decodeHexField("ciphertext", cj.CipherText)
	if err != nil {
		return nil, err
	}
	derivedKey, err := getKDFKey(cj, auth)
	if err != nil {
		return nil, err
	}
	if len(derivedKey) < 32 {
		return nil, fmt.Errorf("derived key too short: %d", len(derivedKey))
	}

	calculatedMAC := quaiCrypto.Keccak256(derivedKey[16:32], cipherText)
	if !bytes.Equal(calculatedMAC, mac) {
		return nil, ErrDecrypt
	}
	return aesCTRXOR(derivedKey[:16], cipherText, iv)
}

func encryptPrivateKeyKeystoreJSON(privKey *ecdsa.PrivateKey, password string, location common.Location) (string, error) {
	if privKey == nil {
		return "", fmt.Errorf("private key is nil")
	}
	_ = location // Keystore encryption is address-byte based and chain/location agnostic.

	addrBytes, err := addressBytesFromPrivateKey(privKey)
	if err != nil {
		return "", err
	}
	keyID, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("failed to create uuid: %w", err)
	}

	keyBytes := quaiCrypto.FromECDSA(privKey)
	cryptoStruct, err := encryptDataV3(keyBytes, []byte(normalizedPassword(password)), keystoreScryptN, keystoreScryptP)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt keystore: %w", err)
	}
	payload := encryptedKeyJSONV3{
		Address: hex.EncodeToString(addrBytes),
		Crypto:  cryptoStruct,
		ID:      keyID.String(),
		Version: keystoreVersion,
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal keystore: %w", err)
	}
	return string(out), nil
}

// EncryptPrivateKeyToKeystoreJSON returns an Ethereum/Ethers-style V3 JSON keystore.
func EncryptPrivateKeyToKeystoreJSON(privKey *ecdsa.PrivateKey, password string, location common.Location) (string, error) {
	return encryptPrivateKeyKeystoreJSON(privKey, password, location)
}

// EncryptPrivateKeyHexToKeystoreJSON returns an Ethereum/Ethers-style V3 JSON keystore for a hex private key.
func EncryptPrivateKeyHexToKeystoreJSON(privateKeyHex string, password string, location common.Location) (string, error) {
	keyHex := strings.TrimPrefix(strings.TrimPrefix(privateKeyHex, "0x"), "0X")
	privKey, err := quaiCrypto.HexToECDSA(keyHex)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}
	return encryptPrivateKeyKeystoreJSON(privKey, password, location)
}

func versionAsInt(v interface{}) (int, error) {
	switch x := v.(type) {
	case nil:
		return 0, fmt.Errorf("missing version")
	case int:
		return x, nil
	case float64:
		return int(x), nil
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, err
		}
		return int(n), nil
	case string:
		if x == "1" {
			return 1, nil
		}
		return 0, fmt.Errorf("unsupported version string: %s", x)
	default:
		return 0, fmt.Errorf("invalid version type %T", v)
	}
}

func decryptPrivateKeyKeystoreJSON(keystoreJSON string, password string) (*ecdsa.PrivateKey, string, error) {
	var protected encryptedKeyJSONV3Compat
	if err := json.Unmarshal([]byte(keystoreJSON), &protected); err != nil {
		return nil, "", fmt.Errorf("failed to decrypt keystore: %w", err)
	}

	version, err := versionAsInt(protected.Version)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decrypt keystore: %w", err)
	}
	if version != keystoreVersion {
		if version == 1 {
			return nil, "", fmt.Errorf("failed to decrypt keystore: version 1 keystore not supported")
		}
		return nil, "", fmt.Errorf("failed to decrypt keystore: version not supported: %v", version)
	}

	cryptoObj, err := protected.cryptoObject()
	if err != nil {
		return nil, "", fmt.Errorf("failed to decrypt keystore: %w", err)
	}
	keyBytes, err := decryptDataV3(cryptoObj, normalizedPassword(password))
	if err != nil {
		return nil, "", fmt.Errorf("failed to decrypt keystore: %w", err)
	}
	privKey, err := quaiCrypto.ToECDSA(keyBytes)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decrypt keystore: invalid key: %w", err)
	}

	addrBytes, err := addressBytesFromPrivateKey(privKey)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decrypt keystore: %w", err)
	}
	address := "0x" + hex.EncodeToString(addrBytes)
	if protected.Address != "" && strings.ToLower(strings.TrimPrefix(protected.Address, "0x")) != hex.EncodeToString(addrBytes) {
		return nil, "", fmt.Errorf("failed to decrypt keystore: address mismatch")
	}
	return privKey, address, nil
}

// DecryptKeystoreJSONToPrivateKey decrypts an Ethereum/Ethers-style V3 JSON keystore.
func DecryptKeystoreJSONToPrivateKey(keystoreJSON string, password string) (*ecdsa.PrivateKey, string, error) {
	return decryptPrivateKeyKeystoreJSON(keystoreJSON, password)
}

// DecryptKeystoreJSONToPrivateKeyHex decrypts a V3 keystore and returns the private key hex and address.
func DecryptKeystoreJSONToPrivateKeyHex(keystoreJSON string, password string) (string, string, error) {
	privKey, address, err := decryptPrivateKeyKeystoreJSON(keystoreJSON, password)
	if err != nil {
		return "", "", err
	}
	return "0x" + hex.EncodeToString(quaiCrypto.FromECDSA(privKey)), address, nil
}
