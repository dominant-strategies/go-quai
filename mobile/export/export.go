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
	"unsafe"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/mobile/hdwallet"
	"github.com/google/uuid"
)

// walletStore holds wallets keyed by UUID handle.
var walletStore sync.Map

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
	TxHex string `json:"txHex"`
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

	gasPrice, ok := new(big.Int).SetString(input.GasPrice, 10)
	if !ok {
		return returnError("invalid gasPrice")
	}
	value, ok := new(big.Int).SetString(input.Value, 10)
	if !ok {
		return returnError("invalid value")
	}

	var data []byte
	if input.Data != "" {
		dataHex := strings.TrimPrefix(input.Data, "0x")
		data, err = hex.DecodeString(dataHex)
		if err != nil {
			return returnError("invalid data hex: " + err.Error())
		}
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

	return returnJSON(signedTxOutput{TxHex: "0x" + hex.EncodeToString(signedBytes)})
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

	gasPrice, ok := new(big.Int).SetString(input.GasPrice, 10)
	if !ok {
		return returnError("invalid gasPrice")
	}
	value, ok := new(big.Int).SetString(input.Value, 10)
	if !ok {
		return returnError("invalid value")
	}

	var data []byte
	if input.Data != "" {
		dataHex := strings.TrimPrefix(strings.TrimPrefix(input.Data, "0x"), "0X")
		data, err = hex.DecodeString(dataHex)
		if err != nil {
			return returnError("invalid data hex: " + err.Error())
		}
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
