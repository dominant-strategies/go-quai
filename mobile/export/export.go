package main

/*
#include <stdlib.h>
*/
import "C"
import (
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
	WalletID string   `json:"walletId"`
	Address  string   `json:"address"`
	ChainID  int64    `json:"chainId"`
	Nonce    uint64   `json:"nonce"`
	GasPrice string   `json:"gasPrice"`
	Gas      uint64   `json:"gas"`
	To       string   `json:"to"`
	Value    string   `json:"value"`
	Data     string   `json:"data"`
	Zone     []byte   `json:"zone"`
}

type signQiTxInput struct {
	WalletID  string                     `json:"walletId"`
	Address   string                     `json:"address"`
	ChainID   int64                      `json:"chainId"`
	TxInputs  []hdwallet.QiTxInputParam  `json:"txInputs"`
	TxOutputs []hdwallet.QiTxOutputParam `json:"txOutputs"`
	Zone      []byte                     `json:"zone"`
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

type validOutput struct {
	Valid bool `json:"valid"`
}

type signedTxOutput struct {
	TxHex string `json:"txHex"`
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
		ChainID:  big.NewInt(input.ChainID),
		Nonce:    input.Nonce,
		GasPrice: gasPrice,
		Gas:      input.Gas,
		To:       input.To,
		Value:    value,
		Data:     data,
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

//export FreeString
func FreeString(s *C.char) {
	C.free(unsafe.Pointer(s))
}

func main() {}
