package hdwallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/rlp"
)

// EthereumDynamicFeeTxParams models an EIP-1559 transaction for Base/Ethereum.
// This stays separate from Quai signing because typed Ethereum transactions use
// a different signing preimage and do not carry Quai location semantics.
type EthereumDynamicFeeTxParams struct {
	ChainID              *big.Int
	Nonce                uint64
	MaxPriorityFeePerGas *big.Int
	MaxFeePerGas         *big.Int
	GasLimit             uint64
	To                   string
	Value                *big.Int
	Data                 []byte
	AccessList           []EthereumAccessListTupleParam
}

type EthereumAccessListTupleParam struct {
	Address     string
	StorageKeys []string
}

type ethereumAccessListEntry struct {
	Address     []byte
	StorageKeys [][]byte
}

type ethereumDynamicFeeSigningPayload struct {
	ChainID              *big.Int
	Nonce                uint64
	MaxPriorityFeePerGas *big.Int
	MaxFeePerGas         *big.Int
	GasLimit             uint64
	To                   []byte
	Value                *big.Int
	Data                 []byte
	AccessList           []ethereumAccessListEntry
}

type ethereumDynamicFeeSignedPayload struct {
	ChainID              *big.Int
	Nonce                uint64
	MaxPriorityFeePerGas *big.Int
	MaxFeePerGas         *big.Int
	GasLimit             uint64
	To                   []byte
	Value                *big.Int
	Data                 []byte
	AccessList           []ethereumAccessListEntry
	YParity              uint8
	R                    *big.Int
	S                    *big.Int
}

const ethereumDynamicFeeTxType byte = 0x02

func SignEthereumDynamicFeeTx(params *EthereumDynamicFeeTxParams, privKey *ecdsa.PrivateKey) ([]byte, []byte, error) {
	if params == nil {
		return nil, nil, fmt.Errorf("missing params")
	}
	if params.ChainID == nil || params.ChainID.Sign() <= 0 {
		return nil, nil, fmt.Errorf("invalid chainId")
	}
	if params.MaxPriorityFeePerGas == nil || params.MaxPriorityFeePerGas.Sign() < 0 {
		return nil, nil, fmt.Errorf("invalid maxPriorityFeePerGas")
	}
	if params.MaxFeePerGas == nil || params.MaxFeePerGas.Sign() < 0 {
		return nil, nil, fmt.Errorf("invalid maxFeePerGas")
	}
	if params.MaxFeePerGas.Cmp(params.MaxPriorityFeePerGas) < 0 {
		return nil, nil, fmt.Errorf("maxFeePerGas must be >= maxPriorityFeePerGas")
	}
	if params.Value == nil || params.Value.Sign() < 0 {
		return nil, nil, fmt.Errorf("invalid value")
	}

	toBytes, err := parseOptionalEthereumAddress(params.To)
	if err != nil {
		return nil, nil, err
	}
	accessList, err := parseEthereumAccessList(params.AccessList)
	if err != nil {
		return nil, nil, err
	}

	signingPayload := ethereumDynamicFeeSigningPayload{
		ChainID:              new(big.Int).Set(params.ChainID),
		Nonce:                params.Nonce,
		MaxPriorityFeePerGas: new(big.Int).Set(params.MaxPriorityFeePerGas),
		MaxFeePerGas:         new(big.Int).Set(params.MaxFeePerGas),
		GasLimit:             params.GasLimit,
		To:                   toBytes,
		Value:                new(big.Int).Set(params.Value),
		Data:                 append([]byte(nil), params.Data...),
		AccessList:           accessList,
	}

	encodedSigningPayload, err := rlp.EncodeToBytes(signingPayload)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode signing payload: %w", err)
	}
	sigHash := crypto.Keccak256(append([]byte{ethereumDynamicFeeTxType}, encodedSigningPayload...))
	signature, err := crypto.Sign(sigHash, privKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	signedPayload := ethereumDynamicFeeSignedPayload{
		ChainID:              signingPayload.ChainID,
		Nonce:                signingPayload.Nonce,
		MaxPriorityFeePerGas: signingPayload.MaxPriorityFeePerGas,
		MaxFeePerGas:         signingPayload.MaxFeePerGas,
		GasLimit:             signingPayload.GasLimit,
		To:                   signingPayload.To,
		Value:                signingPayload.Value,
		Data:                 signingPayload.Data,
		AccessList:           signingPayload.AccessList,
		YParity:              signature[64],
		R:                    new(big.Int).SetBytes(signature[:32]),
		S:                    new(big.Int).SetBytes(signature[32:64]),
	}

	encodedSignedPayload, err := rlp.EncodeToBytes(signedPayload)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode signed payload: %w", err)
	}
	rawTx := append([]byte{ethereumDynamicFeeTxType}, encodedSignedPayload...)
	txHash := crypto.Keccak256(rawTx)
	return rawTx, txHash, nil
}

func parseOptionalEthereumAddress(addr string) ([]byte, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, nil
	}
	normalized := strings.TrimPrefix(strings.TrimPrefix(addr, "0x"), "0X")
	if len(normalized) != 40 {
		return nil, fmt.Errorf("invalid ethereum address length")
	}
	toBytes, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("invalid ethereum address: %w", err)
	}
	return toBytes, nil
}

func parseEthereumAccessList(entries []EthereumAccessListTupleParam) ([]ethereumAccessListEntry, error) {
	accessList := make([]ethereumAccessListEntry, 0, len(entries))
	for i, entry := range entries {
		address, err := parseOptionalEthereumAddress(entry.Address)
		if err != nil {
			return nil, fmt.Errorf("invalid accessList[%d].address: %w", i, err)
		}
		storageKeys := make([][]byte, len(entry.StorageKeys))
		for j, key := range entry.StorageKeys {
			normalized := strings.TrimPrefix(strings.TrimPrefix(key, "0x"), "0X")
			keyBytes, err := hex.DecodeString(normalized)
			if err != nil || len(keyBytes) != 32 {
				return nil, fmt.Errorf("invalid accessList[%d].storageKeys[%d]", i, j)
			}
			storageKeys[j] = keyBytes
		}
		accessList = append(accessList, ethereumAccessListEntry{
			Address:     address,
			StorageKeys: storageKeys,
		})
	}
	return accessList, nil
}
