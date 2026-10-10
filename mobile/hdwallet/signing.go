package hdwallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"google.golang.org/protobuf/proto"
)

// QuaiTxParams holds parameters for creating a Quai (account-based) transaction.
type QuaiTxParams struct {
	ChainID  *big.Int
	Nonce    uint64
	GasPrice *big.Int
	Gas      uint64
	To       string // hex address, empty for contract creation
	Value    *big.Int
	Data     []byte
	// AccessList is an optional EIP-2930 access list. Quai contract interactions
	// require this to be present on chain.
	AccessList []QuaiAccessListTupleParam
}

// QuaiAccessListTupleParam represents one access-list entry.
type QuaiAccessListTupleParam struct {
	Address     string
	StorageKeys []string
}

// QiTxParams holds parameters for creating a Qi (UTXO-based) transaction.
//
// Data is optional; when set, it populates the tx-level Data field on the
// resulting QiTx. The state processor classifies the tx by Data length:
//   - 20 bytes  → Qi wrapping (owner contract address for wrapping balance)
//   - MaxQiTxDataLength → Qi→Quai conversion with slippage/refund payload
//
// Leave nil for ordinary Qi-to-Qi UTXO transfers.
type QiTxParams struct {
	ChainID   *big.Int
	TxInputs  []QiTxInputParam
	TxOutputs []QiTxOutputParam
	Data      []byte
}

// QiTxInputParam represents a UTXO input.
type QiTxInputParam struct {
	TxHash                  string // hex-encoded previous tx hash
	Index                   uint16
	PubKey                  []byte // 33 or 65 byte public key
	DerivationKind          string
	Account                 uint32
	Change                  bool
	DerivationIndex         uint32
	CounterpartyPaymentCode string
}

// QiTxOutputParam represents a UTXO output.
type QiTxOutputParam struct {
	Denomination uint8
	Address      string // hex address
	Lock         uint64 // 0 = unlocked
}

// SignQuaiTx creates a QuaiTx, signs it with ECDSA, and returns protobuf-encoded bytes.
func SignQuaiTx(params *QuaiTxParams, privKey *ecdsa.PrivateKey, location common.Location) ([]byte, error) {
	var to *common.Address
	if params.To != "" {
		addr := common.HexToAddress(params.To, location)
		to = &addr
	}

	accessList := make(types.AccessList, 0, len(params.AccessList))
	for i, tuple := range params.AccessList {
		if !common.IsHexAddress(tuple.Address) {
			return nil, fmt.Errorf("invalid accessList[%d].address", i)
		}
		storageKeys := make([]common.Hash, len(tuple.StorageKeys))
		for j, keyHex := range tuple.StorageKeys {
			keyHex = strings.TrimPrefix(strings.TrimPrefix(keyHex, "0x"), "0X")
			keyBytes, err := hex.DecodeString(keyHex)
			if err != nil || len(keyBytes) != 32 {
				return nil, fmt.Errorf("invalid accessList[%d].storageKeys[%d]", i, j)
			}
			storageKeys[j] = common.BytesToHash(keyBytes)
		}
		accessList = append(accessList, types.AccessTuple{
			Address:     common.HexToAddress(tuple.Address, location),
			StorageKeys: storageKeys,
		})
	}

	inner := &types.QuaiTx{
		ChainID:    params.ChainID,
		Nonce:      params.Nonce,
		GasPrice:   params.GasPrice,
		Gas:        params.Gas,
		To:         to,
		Value:      params.Value,
		Data:       params.Data,
		AccessList: accessList,
	}

	tx := types.NewTx(inner)
	signer := types.NewSigner(params.ChainID, location)

	signedTx, err := types.SignTx(tx, signer, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	protoTx, err := signedTx.ProtoEncode()
	if err != nil {
		return nil, fmt.Errorf("failed to proto-encode transaction: %w", err)
	}

	return proto.Marshal(protoTx)
}

// SignQiTx creates a QiTx, signs it with Schnorr, and returns protobuf-encoded bytes.
func SignQiTx(params *QiTxParams, privKey *ecdsa.PrivateKey, location common.Location) ([]byte, error) {
	// Build TxIn
	txIns := make(types.TxIns, len(params.TxInputs))
	for i, in := range params.TxInputs {
		hashHex := strings.TrimPrefix(in.TxHash, "0x")
		hashBytes, err := hex.DecodeString(hashHex)
		if err != nil {
			return nil, fmt.Errorf("invalid tx hash at input %d: %w", i, err)
		}
		var txHash common.Hash
		txHash.SetBytes(hashBytes)
		txIns[i] = types.TxIn{
			PreviousOutPoint: types.OutPoint{
				TxHash: txHash,
				Index:  in.Index,
			},
			PubKey: in.PubKey,
		}
	}

	// Build TxOut
	txOuts := make(types.TxOuts, len(params.TxOutputs))
	for i, out := range params.TxOutputs {
		addrHex := strings.TrimPrefix(out.Address, "0x")
		addrBytes, err := hex.DecodeString(addrHex)
		if err != nil {
			return nil, fmt.Errorf("invalid address at output %d: %w", i, err)
		}
		lock := new(big.Int).SetUint64(out.Lock)
		txOuts[i] = types.TxOut{
			Denomination: out.Denomination,
			Address:      addrBytes,
			Lock:         lock,
		}
	}

	inner := &types.QiTx{
		ChainID: params.ChainID,
		TxIn:    txIns,
		TxOut:   txOuts,
		Data:    params.Data,
	}

	tx := types.NewTx(inner)

	// Get the signing hash
	signingData := tx.ProtoEncodeTxSigningData()
	data, err := proto.Marshal(signingData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal signing data: %w", err)
	}
	sigHash := crypto.Keccak256(data)

	// Sign with Schnorr
	btcPrivKey, _ := btcec.PrivKeyFromBytes(crypto.FromECDSA(privKey))
	sig, err := schnorr.Sign(btcPrivKey, sigHash)
	if err != nil {
		return nil, fmt.Errorf("schnorr sign failed: %w", err)
	}

	// Reconstruct with signature
	inner.Signature = sig
	signedTx := types.NewTx(inner)

	protoTx, err := signedTx.ProtoEncode()
	if err != nil {
		return nil, fmt.Errorf("failed to proto-encode qi transaction: %w", err)
	}

	return proto.Marshal(protoTx)
}

// DecodeTransaction decodes protobuf-encoded transaction bytes into a Transaction.
func DecodeTransaction(protoBytes []byte, location common.Location) (*types.Transaction, error) {
	protoTx := new(types.ProtoTransaction)
	if err := proto.Unmarshal(protoBytes, protoTx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal proto: %w", err)
	}

	tx := new(types.Transaction)
	if err := tx.ProtoDecode(protoTx, location); err != nil {
		return nil, fmt.Errorf("failed to decode transaction: %w", err)
	}
	return tx, nil
}
