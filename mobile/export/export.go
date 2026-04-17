package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/mobile/hdwallet"
	"github.com/google/uuid"
)

// walletStore holds wallets keyed by UUID handle.
var walletStore sync.Map

type qiMuSigBundleHandle struct {
	session   *hdwallet.QiMuSigBundleSession
	createdAt time.Time
}

// qiMuSigBundleStore keeps short-lived interactive bundle sessions keyed by a
// UUID handle. The store lives at the FFI boundary because Swift only exchanges
// opaque session IDs, while the underlying Go session contains non-serializable
// MuSig contexts and nonce-registration state.
var qiMuSigBundleStore sync.Map

// --- Helper types for JSON input/output ---

type createWalletInput struct {
	Phrase   string `json:"phrase"`
	Password string `json:"password"`
	CoinType uint32 `json:"coinType"`
}

type createRandomInput struct {
	CoinType uint32 `json:"coinType"`
}

type walletIDOutput struct {
	WalletID string `json:"walletId"`
	Phrase   string `json:"phrase,omitempty"`
}

type deriveAddressInput struct {
	WalletID string `json:"walletId"`
	Account  uint32 `json:"account"`
	Zone     []byte `json:"zone"`
}

type deriveAtIndexInput struct {
	WalletID string `json:"walletId"`
	Account  uint32 `json:"account"`
	Change   bool   `json:"change"`
	Index    uint32 `json:"index"`
}

type getPrivateKeyInput struct {
	WalletID string `json:"walletId"`
	Address  string `json:"address"`
}

type privateKeyOutput struct {
	PrivateKey string `json:"privateKey"`
}

type signQuaiTxInput struct {
	WalletID   string                 `json:"walletId"`
	Address    string                 `json:"address"`
	ChainID    int64                  `json:"chainId"`
	Nonce      uint64                 `json:"nonce"`
	GasPrice   string                 `json:"gasPrice"`
	Gas        uint64                 `json:"gas"`
	To         string                 `json:"to"`
	Value      string                 `json:"value"`
	Data       string                 `json:"data"`
	AccessList []accessListEntryInput `json:"accessList,omitempty"`
	Zone       []byte                 `json:"zone"`
}

type signEthereumTxInput struct {
	WalletID             string                 `json:"walletId"`
	Address              string                 `json:"address"`
	ChainID              int64                  `json:"chainId"`
	Nonce                uint64                 `json:"nonce"`
	MaxPriorityFeePerGas string                 `json:"maxPriorityFeePerGas"`
	MaxFeePerGas         string                 `json:"maxFeePerGas"`
	GasLimit             uint64                 `json:"gasLimit"`
	To                   string                 `json:"to"`
	Value                string                 `json:"value"`
	Data                 string                 `json:"data"`
	AccessList           []accessListEntryInput `json:"accessList,omitempty"`
}

type signQiTxInput struct {
	WalletID  string                     `json:"walletId"`
	Address   string                     `json:"address"`
	ChainID   int64                      `json:"chainId"`
	TxInputs  []hdwallet.QiTxInputParam  `json:"txInputs"`
	TxOutputs []hdwallet.QiTxOutputParam `json:"txOutputs"`
	Zone      []byte                     `json:"zone"`
}

type signQiTxWithAddressesInput struct {
	WalletID         string                     `json:"walletId"`
	SigningAddresses []string                   `json:"signingAddresses"`
	ChainID          int64                      `json:"chainId"`
	TxInputs         []hdwallet.QiTxInputParam  `json:"txInputs"`
	TxOutputs        []hdwallet.QiTxOutputParam `json:"txOutputs"`
	Zone             []byte                     `json:"zone"`
}

type qiMuSigBundleCreateInput struct {
	WalletID            string                     `json:"walletId"`
	ChainID             int64                      `json:"chainId"`
	TxInputs            []hdwallet.QiTxInputParam  `json:"txInputs"`
	TxOutputs           []hdwallet.QiTxOutputParam `json:"txOutputs"`
	LocalOwnedInputRefs []string                   `json:"localOwnedInputRefs"`
	Zone                []byte                     `json:"zone"`
}

type qiMuSigNonceJSON struct {
	SignerPosition int    `json:"signerPosition"`
	PublicNonce    string `json:"publicNonce"`
}

type qiMuSigPartialJSON struct {
	SignerPosition   int    `json:"signerPosition"`
	PartialSignature string `json:"partialSignature"`
}

type qiMuSigBundleCreateOutput struct {
	SessionID            string             `json:"sessionId"`
	SigningHash          string             `json:"signingHash"`
	OrderedSignerPubKeys []string           `json:"orderedSignerPubKeys"`
	LocalPublicNonces    []qiMuSigNonceJSON `json:"localPublicNonces"`
}

type qiMuSigLocalPartialBundleInput struct {
	SessionID          string             `json:"sessionId"`
	RemotePublicNonces []qiMuSigNonceJSON `json:"remotePublicNonces"`
}

type qiMuSigLocalPartialBundleOutput struct {
	LocalPartialSignatures []qiMuSigPartialJSON `json:"localPartialSignatures"`
}

type qiMuSigFinalizeInput struct {
	SessionID               string               `json:"sessionId"`
	RemotePublicNonces      []qiMuSigNonceJSON   `json:"remotePublicNonces"`
	RemotePartialSignatures []qiMuSigPartialJSON `json:"remotePartialSignatures"`
}

type qiMuSigFinalizeOutput struct {
	TxHex     string `json:"txHex"`
	Signature string `json:"signature"`
}

type verifySignedQiTransactionInput struct {
	TxHex string `json:"txHex"`
	Zone  []byte `json:"zone"`
}

type qiPaymentCodeInput struct {
	WalletID string `json:"walletId"`
	Account  uint32 `json:"account"`
}

type validateQiPaymentCodeInput struct {
	PaymentCode string `json:"paymentCode"`
}

type deriveQiPaymentCodeAddressInput struct {
	WalletID                string `json:"walletId"`
	CounterpartyPaymentCode string `json:"counterpartyPaymentCode"`
	Account                 uint32 `json:"account"`
	Index                   uint32 `json:"index"`
	Zone                    []byte `json:"zone"`
}

type paymentCodeOutput struct {
	PaymentCode string `json:"paymentCode"`
}

type decodeInput struct {
	ProtoHex string `json:"protoHex"`
	Zone     []byte `json:"zone"`
}

type serializeInput struct {
	WalletID string `json:"walletId"`
}

type mnemonicInput struct {
	Phrase string `json:"phrase"`
}

type encryptKeystoreInput struct {
	PrivateKey string `json:"privateKey"`
	Password   string `json:"password"`
	Zone       []byte `json:"zone"`
}

type decryptKeystoreInput struct {
	KeystoreJSON string `json:"keystoreJson"`
	Password     string `json:"password"`
}

type privateKeyAddressInput struct {
	PrivateKey string `json:"privateKey"`
	Zone       []byte `json:"zone"`
}

type canonicalizeAddressInput struct {
	Address string `json:"address"`
	Zone    []byte `json:"zone"`
}

type signQuaiTxPrivateKeyInput struct {
	PrivateKey string                 `json:"privateKey"`
	ChainID    int64                  `json:"chainId"`
	Nonce      uint64                 `json:"nonce"`
	GasPrice   string                 `json:"gasPrice"`
	Gas        uint64                 `json:"gas"`
	To         string                 `json:"to"`
	Value      string                 `json:"value"`
	Data       string                 `json:"data"`
	AccessList []accessListEntryInput `json:"accessList,omitempty"`
	Zone       []byte                 `json:"zone"`
}

type signEthereumTxPrivateKeyInput struct {
	PrivateKey           string                 `json:"privateKey"`
	ChainID              int64                  `json:"chainId"`
	Nonce                uint64                 `json:"nonce"`
	MaxPriorityFeePerGas string                 `json:"maxPriorityFeePerGas"`
	MaxFeePerGas         string                 `json:"maxFeePerGas"`
	GasLimit             uint64                 `json:"gasLimit"`
	To                   string                 `json:"to"`
	Value                string                 `json:"value"`
	Data                 string                 `json:"data"`
	AccessList           []accessListEntryInput `json:"accessList,omitempty"`
}

type accessListEntryInput struct {
	Address     string   `json:"address"`
	StorageKeys []string `json:"storageKeys"`
}

type signMessageInput struct {
	WalletID   string `json:"walletId"`
	Address    string `json:"address"`
	Message    string `json:"message"`
	Encoding   string `json:"encoding,omitempty"`   // "hex" or "utf8"; default auto
	MessageHex string `json:"messageHex,omitempty"` // backwards/explicit hex path
}

type signMessagePrivateKeyInput struct {
	PrivateKey string `json:"privateKey"`
	Message    string `json:"message"`
	Encoding   string `json:"encoding,omitempty"`
	MessageHex string `json:"messageHex,omitempty"`
}

type signTypedDataInput struct {
	WalletID      string          `json:"walletId"`
	Address       string          `json:"address"`
	TypedData     json.RawMessage `json:"typedData,omitempty"`
	TypedDataJSON string          `json:"typedDataJson,omitempty"`
}

type signTypedDataPrivateKeyInput struct {
	PrivateKey    string          `json:"privateKey"`
	TypedData     json.RawMessage `json:"typedData,omitempty"`
	TypedDataJSON string          `json:"typedDataJson,omitempty"`
}

type validOutput struct {
	Valid bool `json:"valid"`
}

type signedTxOutput struct {
	TxHex  string `json:"txHex"`
	TxHash string `json:"txHash,omitempty"`
}

type signatureOutput struct {
	Signature string `json:"signature"`
}

type keystoreOutput struct {
	KeystoreJSON string `json:"keystoreJson"`
}

type decryptKeystoreOutput struct {
	PrivateKey string `json:"privateKey"`
	Address    string `json:"address"`
}

type addressOutput struct {
	Address string `json:"address"`
}

// --- Helper functions ---

func returnJSON(v interface{}) *C.char {
	data, err := json.Marshal(v)
	if err != nil {
		return returnError(err.Error())
	}
	return C.CString(string(data))
}

func returnError(msg string) *C.char {
	errObj := map[string]string{"error": msg}
	data, _ := json.Marshal(errObj)
	return C.CString(string(data))
}

func getWallet(id string) (*hdwallet.HDWallet, error) {
	val, ok := walletStore.Load(id)
	if !ok {
		return nil, fmt.Errorf("wallet %s not found", id)
	}
	return val.(*hdwallet.HDWallet), nil
}

func parseDecimalBigInt(value string, field string) (*big.Int, error) {
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, fmt.Errorf("invalid %s", field)
	}
	return parsed, nil
}

func parseOptionalHexData(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	dataHex := strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	data, err := hex.DecodeString(dataHex)
	if err != nil {
		return nil, fmt.Errorf("invalid data hex: %w", err)
	}
	return data, nil
}

func toEthereumAccessList(entries []accessListEntryInput) []hdwallet.EthereumAccessListTupleParam {
	result := make([]hdwallet.EthereumAccessListTupleParam, 0, len(entries))
	for _, entry := range entries {
		result = append(result, hdwallet.EthereumAccessListTupleParam{
			Address:     entry.Address,
			StorageKeys: entry.StorageKeys,
		})
	}
	return result
}

func newID() string {
	return uuid.New().String()
}

func decodeMessageInput(input signMessageInput) ([]byte, error) {
	// Prefer explicit hex field if provided.
	if input.MessageHex != "" {
		msgHex := strings.TrimPrefix(strings.TrimPrefix(input.MessageHex, "0x"), "0X")
		msgBytes, err := hex.DecodeString(msgHex)
		if err != nil {
			return nil, fmt.Errorf("invalid messageHex: %w", err)
		}
		return msgBytes, nil
	}

	switch strings.ToLower(input.Encoding) {
	case "hex":
		msgHex := strings.TrimPrefix(strings.TrimPrefix(input.Message, "0x"), "0X")
		msgBytes, err := hex.DecodeString(msgHex)
		if err != nil {
			return nil, fmt.Errorf("invalid message hex: %w", err)
		}
		return msgBytes, nil
	case "utf8", "text":
		return []byte(input.Message), nil
	case "":
		if strings.HasPrefix(input.Message, "0x") || strings.HasPrefix(input.Message, "0X") {
			msgHex := strings.TrimPrefix(strings.TrimPrefix(input.Message, "0x"), "0X")
			msgBytes, err := hex.DecodeString(msgHex)
			if err != nil {
				return nil, fmt.Errorf("invalid message hex: %w", err)
			}
			return msgBytes, nil
		}
		return []byte(input.Message), nil
	default:
		return nil, fmt.Errorf("unsupported encoding %q", input.Encoding)
	}
}

func typedDataPayloadBytes(input signTypedDataInput) ([]byte, error) {
	if len(input.TypedData) > 0 {
		return input.TypedData, nil
	}
	if input.TypedDataJSON != "" {
		return []byte(input.TypedDataJSON), nil
	}
	return nil, fmt.Errorf("missing typedData payload")
}

func typedDataPayloadBytesFromPrivateKey(input signTypedDataPrivateKeyInput) ([]byte, error) {
	if len(input.TypedData) > 0 {
		return input.TypedData, nil
	}
	if input.TypedDataJSON != "" {
		return []byte(input.TypedDataJSON), nil
	}
	return nil, fmt.Errorf("missing typedData payload")
}

func decodeMessageInputFromPrivateKey(input signMessagePrivateKeyInput) ([]byte, error) {
	return decodeMessageInput(signMessageInput{
		Message:    input.Message,
		Encoding:   input.Encoding,
		MessageHex: input.MessageHex,
	})
}

func normalizeZone(zone []byte) common.Location {
	loc := common.Location(zone)
	if len(loc) < 2 {
		return common.Location{0, 0}
	}
	return loc
}

func toHDWalletAccessList(entries []accessListEntryInput) []hdwallet.QuaiAccessListTupleParam {
	if len(entries) == 0 {
		return nil
	}
	out := make([]hdwallet.QuaiAccessListTupleParam, len(entries))
	for i, entry := range entries {
		out[i] = hdwallet.QuaiAccessListTupleParam{
			Address:     entry.Address,
			StorageKeys: append([]string(nil), entry.StorageKeys...),
		}
	}
	return out
}

func cleanupExpiredQiMuSigBundles() {
	now := time.Now()
	qiMuSigBundleStore.Range(func(key, value any) bool {
		handle, ok := value.(*qiMuSigBundleHandle)
		if !ok || now.Sub(handle.createdAt) > 10*time.Minute {
			qiMuSigBundleStore.Delete(key)
		}
		return true
	})
}

func getQiMuSigBundle(id string) (*hdwallet.QiMuSigBundleSession, error) {
	value, ok := qiMuSigBundleStore.Load(id)
	if !ok {
		return nil, fmt.Errorf("qi musig bundle %s not found", id)
	}
	handle, ok := value.(*qiMuSigBundleHandle)
	if !ok {
		qiMuSigBundleStore.Delete(id)
		return nil, fmt.Errorf("qi musig bundle %s is invalid", id)
	}
	if time.Since(handle.createdAt) > 10*time.Minute {
		qiMuSigBundleStore.Delete(id)
		return nil, fmt.Errorf("qi musig bundle %s expired", id)
	}
	return handle.session, nil
}

func normalizeQiInputRef(input hdwallet.QiTxInputParam) string {
	txHash := strings.TrimSpace(input.TxHash)
	txHash = strings.TrimPrefix(strings.TrimPrefix(txHash, "0x"), "0X")
	return strings.ToLower(txHash) + fmt.Sprintf(":%d", input.Index)
}

func parseQiMuSigNonceBundle(entries []qiMuSigNonceJSON) ([]hdwallet.QiMuSigNonceParam, error) {
	bundle := make([]hdwallet.QiMuSigNonceParam, len(entries))
	for i, entry := range entries {
		nonceHex := strings.TrimPrefix(strings.TrimPrefix(entry.PublicNonce, "0x"), "0X")
		nonceBytes, err := hex.DecodeString(nonceHex)
		if err != nil {
			return nil, fmt.Errorf("invalid remote public nonce %d: %w", i, err)
		}
		bundle[i] = hdwallet.QiMuSigNonceParam{
			SignerPosition: entry.SignerPosition,
			PublicNonce:    nonceBytes,
		}
	}
	return bundle, nil
}

func parseQiMuSigPartialBundle(entries []qiMuSigPartialJSON) ([]hdwallet.QiMuSigPartialParam, error) {
	bundle := make([]hdwallet.QiMuSigPartialParam, len(entries))
	for i, entry := range entries {
		partialHex := strings.TrimPrefix(strings.TrimPrefix(entry.PartialSignature, "0x"), "0X")
		partialBytes, err := hex.DecodeString(partialHex)
		if err != nil {
			return nil, fmt.Errorf("invalid remote partial signature %d: %w", i, err)
		}
		bundle[i] = hdwallet.QiMuSigPartialParam{
			SignerPosition:   entry.SignerPosition,
			PartialSignature: partialBytes,
		}
	}
	return bundle, nil
}

func encodeQiMuSigNonceBundle(entries []hdwallet.QiMuSigNonceParam) []qiMuSigNonceJSON {
	bundle := make([]qiMuSigNonceJSON, len(entries))
	for i, entry := range entries {
		bundle[i] = qiMuSigNonceJSON{
			SignerPosition: entry.SignerPosition,
			PublicNonce:    "0x" + hex.EncodeToString(entry.PublicNonce),
		}
	}
	return bundle
}

func encodeQiMuSigPartialBundle(entries []hdwallet.QiMuSigPartialParam) []qiMuSigPartialJSON {
	bundle := make([]qiMuSigPartialJSON, len(entries))
	for i, entry := range entries {
		bundle[i] = qiMuSigPartialJSON{
			SignerPosition:   entry.SignerPosition,
			PartialSignature: "0x" + hex.EncodeToString(entry.PartialSignature),
		}
	}
	return bundle
}

// --- Exported C functions ---

//export CreateWalletFromPhrase
func CreateWalletFromPhrase(jsonInput *C.char) *C.char {
	var input createWalletInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := hdwallet.NewHDWalletFromPhrase(input.Phrase, input.Password, input.CoinType)
	if err != nil {
		return returnError(err.Error())
	}

	id := newID()
	walletStore.Store(id, w)
	return returnJSON(walletIDOutput{WalletID: id})
}

//export CreateRandomWallet
func CreateRandomWallet(jsonInput *C.char) *C.char {
	var input createRandomInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := hdwallet.NewRandomHDWallet(input.CoinType)
	if err != nil {
		return returnError(err.Error())
	}

	id := newID()
	walletStore.Store(id, w)
	return returnJSON(walletIDOutput{WalletID: id, Phrase: w.Phrase()})
}

//export DestroyWallet
func DestroyWallet(walletId *C.char) {
	walletStore.Delete(C.GoString(walletId))
}

//export DeriveAddress
func DeriveAddress(jsonInput *C.char) *C.char {
	var input deriveAddressInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	zone := common.Location(input.Zone)
	info, err := w.DeriveAddress(input.Account, zone)
	if err != nil {
		return returnError(err.Error())
	}

	return returnJSON(info)
}

//export DeriveAddressAtIndex
func DeriveAddressAtIndex(jsonInput *C.char) *C.char {
	var input deriveAtIndexInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	info, err := w.DeriveAddressAtIndex(input.Account, input.Change, input.Index)
	if err != nil {
		return returnError(err.Error())
	}

	return returnJSON(info)
}

//export GetQiPaymentCode
func GetQiPaymentCode(jsonInput *C.char) *C.char {
	var input qiPaymentCodeInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	paymentCode, err := w.GetQiPaymentCode(input.Account)
	if err != nil {
		return returnError(err.Error())
	}

	return returnJSON(paymentCodeOutput{PaymentCode: paymentCode})
}

//export ValidateQiPaymentCode
func ValidateQiPaymentCode(jsonInput *C.char) *C.char {
	var input validateQiPaymentCodeInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	return returnJSON(validOutput{Valid: hdwallet.ValidateQiPaymentCode(input.PaymentCode)})
}

//export DeriveQiPaymentChannelSendAddress
func DeriveQiPaymentChannelSendAddress(jsonInput *C.char) *C.char {
	start := time.Now()
	var input deriveQiPaymentCodeAddressInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	info, err := w.DeriveQiPaymentChannelSendAddress(
		input.CounterpartyPaymentCode,
		common.Location(input.Zone),
		input.Account,
		input.Index,
	)
	if err != nil {
		return returnError(err.Error())
	}

	fmt.Printf(
		"[WalletInfo] FFI DeriveQiPaymentChannelSendAddress walletId=%s startIndex=%d resolvedIndex=%d goElapsedMs=%d ffiElapsedMs=%d attempts=%d\n",
		input.WalletID,
		input.Index,
		info.Index,
		info.DerivationElapsedMs,
		time.Since(start).Milliseconds(),
		info.DerivationAttempts,
	)

	return returnJSON(info)
}

//export DeriveQiPaymentChannelReceiveAddress
func DeriveQiPaymentChannelReceiveAddress(jsonInput *C.char) *C.char {
	var input deriveQiPaymentCodeAddressInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	info, err := w.DeriveQiPaymentChannelReceiveAddress(
		input.CounterpartyPaymentCode,
		common.Location(input.Zone),
		input.Account,
		input.Index,
	)
	if err != nil {
		return returnError(err.Error())
	}

	return returnJSON(info)
}

//export GetPrivateKey
func GetPrivateKey(jsonInput *C.char) *C.char {
	var input getPrivateKeyInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	privKey, err := w.GetPrivateKeyForAddress(input.Address)
	if err != nil {
		return returnError(err.Error())
	}

	keyHex := "0x" + hex.EncodeToString(crypto.FromECDSA(privKey))
	return returnJSON(privateKeyOutput{PrivateKey: keyHex})
}

//export SignQuaiTransaction
func SignQuaiTransaction(jsonInput *C.char) *C.char {
	var input signQuaiTxInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	privKey, err := w.GetPrivateKeyForAddress(input.Address)
	if err != nil {
		return returnError(err.Error())
	}

	gasPrice, err := parseDecimalBigInt(input.GasPrice, "gasPrice")
	if err != nil {
		return returnError(err.Error())
	}
	value, err := parseDecimalBigInt(input.Value, "value")
	if err != nil {
		return returnError(err.Error())
	}
	data, err := parseOptionalHexData(input.Data)
	if err != nil {
		return returnError(err.Error())
	}

	zone := common.Location(input.Zone)
	params := &hdwallet.QuaiTxParams{
		ChainID:    big.NewInt(input.ChainID),
		Nonce:      input.Nonce,
		GasPrice:   gasPrice,
		Gas:        input.Gas,
		To:         input.To,
		Value:      value,
		Data:       data,
		AccessList: toHDWalletAccessList(input.AccessList),
	}

	signedBytes, err := hdwallet.SignQuaiTx(params, privKey, zone)
	if err != nil {
		return returnError(err.Error())
	}

	return returnJSON(signedTxOutput{TxHex: "0x" + hex.EncodeToString(signedBytes)})
}

//export SignEthereumTransaction
func SignEthereumTransaction(jsonInput *C.char) *C.char {
	var input signEthereumTxInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}
	privKey, err := w.GetPrivateKeyForAddress(input.Address)
	if err != nil {
		return returnError(err.Error())
	}

	maxPriorityFeePerGas, err := parseDecimalBigInt(input.MaxPriorityFeePerGas, "maxPriorityFeePerGas")
	if err != nil {
		return returnError(err.Error())
	}
	maxFeePerGas, err := parseDecimalBigInt(input.MaxFeePerGas, "maxFeePerGas")
	if err != nil {
		return returnError(err.Error())
	}
	value, err := parseDecimalBigInt(input.Value, "value")
	if err != nil {
		return returnError(err.Error())
	}
	data, err := parseOptionalHexData(input.Data)
	if err != nil {
		return returnError(err.Error())
	}

	params := &hdwallet.EthereumDynamicFeeTxParams{
		ChainID:              big.NewInt(input.ChainID),
		Nonce:                input.Nonce,
		MaxPriorityFeePerGas: maxPriorityFeePerGas,
		MaxFeePerGas:         maxFeePerGas,
		GasLimit:             input.GasLimit,
		To:                   input.To,
		Value:                value,
		Data:                 data,
		AccessList:           toEthereumAccessList(input.AccessList),
	}
	rawTx, txHash, err := hdwallet.SignEthereumDynamicFeeTx(params, privKey)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signedTxOutput{
		TxHex:  "0x" + hex.EncodeToString(rawTx),
		TxHash: "0x" + hex.EncodeToString(txHash),
	})
}

//export SignQiTransaction
func SignQiTransaction(jsonInput *C.char) *C.char {
	var input signQiTxInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	privKey, err := w.GetPrivateKeyForAddress(input.Address)
	if err != nil {
		return returnError(err.Error())
	}

	zone := common.Location(input.Zone)
	params := &hdwallet.QiTxParams{
		ChainID:   big.NewInt(input.ChainID),
		TxInputs:  input.TxInputs,
		TxOutputs: input.TxOutputs,
	}

	signedBytes, err := hdwallet.SignQiTx(params, privKey, zone)
	if err != nil {
		return returnError(err.Error())
	}
	valid, err := hdwallet.VerifySignedQiTransaction(signedBytes, zone)
	if err != nil {
		return returnError(err.Error())
	}
	if !valid {
		return returnError("failed local verification for signed Qi transaction")
	}

	return returnJSON(signedTxOutput{TxHex: "0x" + hex.EncodeToString(signedBytes)})
}

//export SignQiTransactionWithAddresses
func SignQiTransactionWithAddresses(jsonInput *C.char) *C.char {
	var input signQiTxWithAddressesInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}
	if len(input.SigningAddresses) == 0 {
		if len(input.TxInputs) == 0 {
			return returnError("at least one signing input is required")
		}
	}

	var privKeys []*ecdsa.PrivateKey
	if len(input.SigningAddresses) > 0 {
		privKeys = make([]*ecdsa.PrivateKey, len(input.SigningAddresses))
		for i, address := range input.SigningAddresses {
			privKey, err := w.GetPrivateKeyForAddress(address)
			if err != nil {
				return returnError(err.Error())
			}
			privKeys[i] = privKey
		}
	} else {
		privKeys = make([]*ecdsa.PrivateKey, len(input.TxInputs))
		for i, txInput := range input.TxInputs {
			privKey, err := w.GetPrivateKeyForQiInput(txInput)
			if err != nil {
				return returnError(err.Error())
			}
			privKeys[i] = privKey
		}
	}

	zone := common.Location(input.Zone)
	params := &hdwallet.QiTxParams{
		ChainID:   big.NewInt(input.ChainID),
		TxInputs:  input.TxInputs,
		TxOutputs: input.TxOutputs,
	}

	signedBytes, err := hdwallet.SignQiTxWithKeys(params, privKeys, zone)
	if err != nil {
		return returnError(err.Error())
	}
	valid, err := hdwallet.VerifySignedQiTransaction(signedBytes, zone)
	if err != nil {
		return returnError(err.Error())
	}
	if !valid {
		return returnError("failed local verification for signed Qi transaction")
	}

	return returnJSON(signedTxOutput{TxHex: "0x" + hex.EncodeToString(signedBytes)})
}

//export CreateQiMuSigBundleSession
func CreateQiMuSigBundleSession(jsonInput *C.char) *C.char {
	var input qiMuSigBundleCreateInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}
	if len(input.TxInputs) == 0 {
		return returnError("at least one Qi input is required")
	}
	if len(input.LocalOwnedInputRefs) == 0 {
		return returnError("at least one localOwnedInputRef is required")
	}

	params := &hdwallet.QiTxParams{
		ChainID:   big.NewInt(input.ChainID),
		TxInputs:  input.TxInputs,
		TxOutputs: input.TxOutputs,
	}

	localRefSet := make(map[string]struct{}, len(input.LocalOwnedInputRefs))
	for _, ref := range input.LocalOwnedInputRefs {
		normalized := strings.ToLower(strings.TrimSpace(ref))
		if normalized == "" {
			continue
		}
		normalized = strings.TrimPrefix(strings.TrimPrefix(normalized, "0x"), "0X")
		localRefSet[normalized] = struct{}{}
	}
	if len(localRefSet) == 0 {
		return returnError("at least one valid localOwnedInputRef is required")
	}

	localSigners := make(map[int]*ecdsa.PrivateKey, len(localRefSet))
	for position, txInput := range input.TxInputs {
		if _, ok := localRefSet[normalizeQiInputRef(txInput)]; !ok {
			continue
		}
		privKey, err := w.GetPrivateKeyForQiInput(txInput)
		if err != nil {
			return returnError(err.Error())
		}
		localSigners[position] = privKey
	}
	if len(localSigners) == 0 {
		return returnError("no localOwnedInputRefs matched the supplied txInputs")
	}

	session, signingHash, nonceBundle, orderedPubKeys, err := hdwallet.NewQiMuSigBundleSession(params, localSigners)
	if err != nil {
		return returnError(err.Error())
	}

	cleanupExpiredQiMuSigBundles()
	sessionID := newID()
	qiMuSigBundleStore.Store(sessionID, &qiMuSigBundleHandle{
		session:   session,
		createdAt: time.Now(),
	})

	orderedPubKeyHex := make([]string, len(orderedPubKeys))
	for i, pubKey := range orderedPubKeys {
		orderedPubKeyHex[i] = "0x" + hex.EncodeToString(pubKey)
	}

	return returnJSON(qiMuSigBundleCreateOutput{
		SessionID:            sessionID,
		SigningHash:          "0x" + hex.EncodeToString(signingHash[:]),
		OrderedSignerPubKeys: orderedPubKeyHex,
		LocalPublicNonces:    encodeQiMuSigNonceBundle(nonceBundle),
	})
}

//export CreateQiMuSigLocalPartialBundle
func CreateQiMuSigLocalPartialBundle(jsonInput *C.char) *C.char {
	var input qiMuSigLocalPartialBundleInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	session, err := getQiMuSigBundle(input.SessionID)
	if err != nil {
		return returnError(err.Error())
	}
	remoteNonces, err := parseQiMuSigNonceBundle(input.RemotePublicNonces)
	if err != nil {
		return returnError(err.Error())
	}

	partials, err := session.CreateLocalPartialBundle(remoteNonces)
	if err != nil {
		return returnError(err.Error())
	}

	return returnJSON(qiMuSigLocalPartialBundleOutput{
		LocalPartialSignatures: encodeQiMuSigPartialBundle(partials),
	})
}

//export FinalizeQiMuSigSignedTransaction
func FinalizeQiMuSigSignedTransaction(jsonInput *C.char) *C.char {
	var input qiMuSigFinalizeInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	session, err := getQiMuSigBundle(input.SessionID)
	if err != nil {
		return returnError(err.Error())
	}
	remoteNonces, err := parseQiMuSigNonceBundle(input.RemotePublicNonces)
	if err != nil {
		return returnError(err.Error())
	}
	remotePartials, err := parseQiMuSigPartialBundle(input.RemotePartialSignatures)
	if err != nil {
		return returnError(err.Error())
	}

	txBytes, signatureBytes, err := session.FinalizeSignedTransaction(remoteNonces, remotePartials)
	if err != nil {
		return returnError(err.Error())
	}
	valid, err := hdwallet.VerifySignedQiTransaction(txBytes, common.Location{0, 0})
	if err != nil {
		return returnError(err.Error())
	}
	if !valid {
		return returnError("failed local verification for finalized Qi transaction")
	}
	qiMuSigBundleStore.Delete(input.SessionID)

	return returnJSON(qiMuSigFinalizeOutput{
		TxHex:     "0x" + hex.EncodeToString(txBytes),
		Signature: "0x" + hex.EncodeToString(signatureBytes),
	})
}

//export VerifySignedQiTransaction
func VerifySignedQiTransaction(jsonInput *C.char) *C.char {
	var input verifySignedQiTransactionInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	txHex := strings.TrimPrefix(strings.TrimPrefix(input.TxHex, "0x"), "0X")
	txBytes, err := hex.DecodeString(txHex)
	if err != nil {
		return returnError("invalid tx hex: " + err.Error())
	}
	valid, err := hdwallet.VerifySignedQiTransaction(txBytes, normalizeZone(input.Zone))
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(validOutput{Valid: valid})
}

//export DecodeProtoTransaction
func DecodeProtoTransaction(jsonInput *C.char) *C.char {
	var input decodeInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	protoHex := strings.TrimPrefix(input.ProtoHex, "0x")
	protoBytes, err := hex.DecodeString(protoHex)
	if err != nil {
		return returnError("invalid hex: " + err.Error())
	}

	zone := common.Location(input.Zone)
	tx, err := hdwallet.DecodeTransaction(protoBytes, zone)
	if err != nil {
		return returnError(err.Error())
	}

	txJSON, err := tx.MarshalJSON()
	if err != nil {
		return returnError("failed to marshal tx: " + err.Error())
	}

	return C.CString(string(txJSON))
}

//export SerializeWallet
func SerializeWallet(jsonInput *C.char) *C.char {
	var input serializeInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}

	data, err := w.Serialize()
	if err != nil {
		return returnError(err.Error())
	}

	return C.CString(string(data))
}

//export DeserializeWalletFromJSON
func DeserializeWalletFromJSON(jsonInput *C.char) *C.char {
	w, err := hdwallet.DeserializeWallet([]byte(C.GoString(jsonInput)))
	if err != nil {
		return returnError(err.Error())
	}

	id := newID()
	walletStore.Store(id, w)
	return returnJSON(walletIDOutput{WalletID: id})
}

//export ValidateMnemonic
func ValidateMnemonic(jsonInput *C.char) *C.char {
	var input mnemonicInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	return returnJSON(validOutput{Valid: hdwallet.IsValidMnemonic(input.Phrase)})
}

//export EncryptPrivateKeyToKeystore
func EncryptPrivateKeyToKeystore(jsonInput *C.char) *C.char {
	var input encryptKeystoreInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	zone := common.Location(input.Zone)
	if len(zone) < 2 {
		zone = common.Location{0, 0}
	}

	keystoreJSON, err := hdwallet.EncryptPrivateKeyHexToKeystoreJSON(input.PrivateKey, input.Password, zone)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(keystoreOutput{KeystoreJSON: keystoreJSON})
}

//export DecryptKeystoreToPrivateKey
func DecryptKeystoreToPrivateKey(jsonInput *C.char) *C.char {
	var input decryptKeystoreInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	privateKey, address, err := hdwallet.DecryptKeystoreJSONToPrivateKeyHex(input.KeystoreJSON, input.Password)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(decryptKeystoreOutput{
		PrivateKey: privateKey,
		Address:    address,
	})
}

//export GetAddressForPrivateKey
func GetAddressForPrivateKey(jsonInput *C.char) *C.char {
	var input privateKeyAddressInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}
	keyHex := strings.TrimPrefix(strings.TrimPrefix(input.PrivateKey, "0x"), "0X")
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return returnError(err.Error())
	}
	addr := crypto.PubkeyToAddress(privKey.PublicKey, normalizeZone(input.Zone))
	return returnJSON(addressOutput{Address: addr.Hex()})
}

//export CanonicalizeAddress
func CanonicalizeAddress(jsonInput *C.char) *C.char {
	var input canonicalizeAddressInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}
	if !common.IsHexAddress(input.Address) {
		return returnError("invalid address")
	}
	addr := common.HexToAddress(input.Address, normalizeZone(input.Zone))
	return returnJSON(addressOutput{Address: addr.Hex()})
}

//export SignQuaiTransactionWithPrivateKey
func SignQuaiTransactionWithPrivateKey(jsonInput *C.char) *C.char {
	var input signQuaiTxPrivateKeyInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}
	keyHex := strings.TrimPrefix(strings.TrimPrefix(input.PrivateKey, "0x"), "0X")
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return returnError(err.Error())
	}

	gasPrice, err := parseDecimalBigInt(input.GasPrice, "gasPrice")
	if err != nil {
		return returnError(err.Error())
	}
	value, err := parseDecimalBigInt(input.Value, "value")
	if err != nil {
		return returnError(err.Error())
	}
	data, err := parseOptionalHexData(input.Data)
	if err != nil {
		return returnError(err.Error())
	}

	params := &hdwallet.QuaiTxParams{
		ChainID:    big.NewInt(input.ChainID),
		Nonce:      input.Nonce,
		GasPrice:   gasPrice,
		Gas:        input.Gas,
		To:         input.To,
		Value:      value,
		Data:       data,
		AccessList: toHDWalletAccessList(input.AccessList),
	}

	signedBytes, err := hdwallet.SignQuaiTx(params, privKey, normalizeZone(input.Zone))
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signedTxOutput{TxHex: "0x" + hex.EncodeToString(signedBytes)})
}

//export SignEthereumTransactionWithPrivateKey
func SignEthereumTransactionWithPrivateKey(jsonInput *C.char) *C.char {
	var input signEthereumTxPrivateKeyInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}
	keyHex := strings.TrimPrefix(strings.TrimPrefix(input.PrivateKey, "0x"), "0X")
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return returnError(err.Error())
	}

	maxPriorityFeePerGas, err := parseDecimalBigInt(input.MaxPriorityFeePerGas, "maxPriorityFeePerGas")
	if err != nil {
		return returnError(err.Error())
	}
	maxFeePerGas, err := parseDecimalBigInt(input.MaxFeePerGas, "maxFeePerGas")
	if err != nil {
		return returnError(err.Error())
	}
	value, err := parseDecimalBigInt(input.Value, "value")
	if err != nil {
		return returnError(err.Error())
	}
	data, err := parseOptionalHexData(input.Data)
	if err != nil {
		return returnError(err.Error())
	}

	params := &hdwallet.EthereumDynamicFeeTxParams{
		ChainID:              big.NewInt(input.ChainID),
		Nonce:                input.Nonce,
		MaxPriorityFeePerGas: maxPriorityFeePerGas,
		MaxFeePerGas:         maxFeePerGas,
		GasLimit:             input.GasLimit,
		To:                   input.To,
		Value:                value,
		Data:                 data,
		AccessList:           toEthereumAccessList(input.AccessList),
	}
	rawTx, txHash, err := hdwallet.SignEthereumDynamicFeeTx(params, privKey)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signedTxOutput{
		TxHex:  "0x" + hex.EncodeToString(rawTx),
		TxHash: "0x" + hex.EncodeToString(txHash),
	})
}

//export SignPersonalMessage
func SignPersonalMessage(jsonInput *C.char) *C.char {
	var input signMessageInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}
	msg, err := decodeMessageInput(input)
	if err != nil {
		return returnError(err.Error())
	}
	sig, err := w.SignPersonalMessage(input.Address, msg)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signatureOutput{Signature: "0x" + hex.EncodeToString(sig)})
}

//export SignPersonalMessageWithPrivateKey
func SignPersonalMessageWithPrivateKey(jsonInput *C.char) *C.char {
	var input signMessagePrivateKeyInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}
	keyHex := strings.TrimPrefix(strings.TrimPrefix(input.PrivateKey, "0x"), "0X")
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return returnError(err.Error())
	}
	msg, err := decodeMessageInputFromPrivateKey(input)
	if err != nil {
		return returnError(err.Error())
	}
	sig, err := hdwallet.SignPersonalMessage(privKey, msg)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signatureOutput{Signature: "0x" + hex.EncodeToString(sig)})
}

//export SignRawMessage
func SignRawMessage(jsonInput *C.char) *C.char {
	var input signMessageInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}
	msg, err := decodeMessageInput(input)
	if err != nil {
		return returnError(err.Error())
	}
	sig, err := w.SignRawMessage(input.Address, msg)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signatureOutput{Signature: "0x" + hex.EncodeToString(sig)})
}

//export SignRawMessageWithPrivateKey
func SignRawMessageWithPrivateKey(jsonInput *C.char) *C.char {
	var input signMessagePrivateKeyInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}
	keyHex := strings.TrimPrefix(strings.TrimPrefix(input.PrivateKey, "0x"), "0X")
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return returnError(err.Error())
	}
	msg, err := decodeMessageInputFromPrivateKey(input)
	if err != nil {
		return returnError(err.Error())
	}
	sig, err := hdwallet.SignRawMessage(privKey, msg)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signatureOutput{Signature: "0x" + hex.EncodeToString(sig)})
}

//export SignTypedDataV4
func SignTypedDataV4(jsonInput *C.char) *C.char {
	var input signTypedDataInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}

	w, err := getWallet(input.WalletID)
	if err != nil {
		return returnError(err.Error())
	}
	payload, err := typedDataPayloadBytes(input)
	if err != nil {
		return returnError(err.Error())
	}
	sig, err := w.SignTypedDataV4(input.Address, payload)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signatureOutput{Signature: "0x" + hex.EncodeToString(sig)})
}

//export SignTypedDataV4WithPrivateKey
func SignTypedDataV4WithPrivateKey(jsonInput *C.char) *C.char {
	var input signTypedDataPrivateKeyInput
	if err := json.Unmarshal([]byte(C.GoString(jsonInput)), &input); err != nil {
		return returnError(err.Error())
	}
	keyHex := strings.TrimPrefix(strings.TrimPrefix(input.PrivateKey, "0x"), "0X")
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return returnError(err.Error())
	}
	payload, err := typedDataPayloadBytesFromPrivateKey(input)
	if err != nil {
		return returnError(err.Error())
	}
	sig, err := hdwallet.SignTypedDataV4(privKey, payload)
	if err != nil {
		return returnError(err.Error())
	}
	return returnJSON(signatureOutput{Signature: "0x" + hex.EncodeToString(sig)})
}

//export FreeString
func FreeString(s *C.char) {
	C.free(unsafe.Pointer(s))
}

func main() {}
