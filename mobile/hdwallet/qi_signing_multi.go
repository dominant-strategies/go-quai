package hdwallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	btcmusig2 "github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"google.golang.org/protobuf/proto"
)

func buildUnsignedQiTx(params *QiTxParams) (*types.QiTx, [32]byte, error) {
	var sigHash [32]byte

	txIns := make(types.TxIns, len(params.TxInputs))
	for i, in := range params.TxInputs {
		hashHex := strings.TrimPrefix(in.TxHash, "0x")
		hashBytes, err := hex.DecodeString(hashHex)
		if err != nil {
			return nil, sigHash, fmt.Errorf("invalid tx hash at input %d: %w", i, err)
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

	txOuts := make(types.TxOuts, len(params.TxOutputs))
	for i, out := range params.TxOutputs {
		addrHex := strings.TrimPrefix(out.Address, "0x")
		addrBytes, err := hex.DecodeString(addrHex)
		if err != nil {
			return nil, sigHash, fmt.Errorf("invalid address at output %d: %w", i, err)
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
	// Qi signatures commit to the protobuf signing payload, not the fully encoded
	// transaction bytes, so the signing hash must be built from ProtoEncodeTxSigningData.
	signingData := tx.ProtoEncodeTxSigningData()
	data, err := proto.Marshal(signingData)
	if err != nil {
		return nil, sigHash, fmt.Errorf("failed to marshal signing data: %w", err)
	}
	copy(sigHash[:], crypto.Keccak256(data))
	return inner, sigHash, nil
}

func encodeSignedQiTx(inner *types.QiTx) ([]byte, error) {
	signedTx := types.NewTx(inner)
	protoTx, err := signedTx.ProtoEncode()
	if err != nil {
		return nil, fmt.Errorf("failed to proto-encode qi transaction: %w", err)
	}
	return proto.Marshal(protoTx)
}

// SignQiTxWithKeys signs a Qi transaction with one or more private keys. When
// more than one key is provided, a local MuSig2 aggregate signature is created
// across the provided signer set in the same order as the corresponding inputs.
func SignQiTxWithKeys(params *QiTxParams, privKeys []*ecdsa.PrivateKey, location common.Location) ([]byte, error) {
	if len(privKeys) == 0 {
		return nil, fmt.Errorf("no signing keys provided")
	}

	inner, sigHash, err := buildUnsignedQiTx(params)
	if err != nil {
		return nil, err
	}

	if len(privKeys) == 1 {
		// Single-input-owner Qi spends use a plain Schnorr signature.
		btcPrivKey, _ := btcec.PrivKeyFromBytes(crypto.FromECDSA(privKeys[0]))
		sig, err := schnorr.Sign(btcPrivKey, sigHash[:])
		if err != nil {
			return nil, fmt.Errorf("schnorr sign failed: %w", err)
		}
		inner.Signature = sig
		return encodeSignedQiTx(inner)
	}

	// For multi-key local signing we aggregate the signer set with MuSig2 so the
	// chain still sees a single Schnorr signature on the transaction.
	btcecPrivKeys := make([]*btcec.PrivateKey, len(privKeys))
	signerPubKeys := make([]*btcec.PublicKey, len(privKeys))
	for i, privKey := range privKeys {
		btcPrivKey, _ := btcec.PrivKeyFromBytes(crypto.FromECDSA(privKey))
		btcecPrivKeys[i] = btcPrivKey
		signerPubKeys[i] = btcPrivKey.PubKey()
	}

	sessions := make([]*btcmusig2.Session, len(btcecPrivKeys))
	nonces := make([][btcmusig2.PubNonceSize]byte, len(btcecPrivKeys))
	for i, btcPrivKey := range btcecPrivKeys {
		ctx, err := btcmusig2.NewContext(
			btcPrivKey,
			false,
			btcmusig2.WithKnownSigners(signerPubKeys),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create musig context %d: %w", i, err)
		}
		session, err := ctx.NewSession()
		if err != nil {
			return nil, fmt.Errorf("failed to create musig session %d: %w", i, err)
		}
		sessions[i] = session
		nonces[i] = session.PublicNonce()
	}

	for i := range sessions {
		for j := range nonces {
			if i == j {
				continue
			}
			// Every signer must register every other participant's public nonce
			// before partial signatures can be produced.
			if _, err := sessions[i].RegisterPubNonce(nonces[j]); err != nil {
				return nil, fmt.Errorf("failed to register nonce %d->%d: %w", j, i, err)
			}
		}
	}

	partials := make([]*btcmusig2.PartialSignature, len(sessions))
	for i := range sessions {
		partial, err := sessions[i].Sign(sigHash)
		if err != nil {
			return nil, fmt.Errorf("failed to create musig partial %d: %w", i, err)
		}
		partials[i] = partial
	}

	combiner := sessions[0]
	for i := 1; i < len(partials); i++ {
		if _, err := combiner.CombineSig(partials[i]); err != nil {
			return nil, fmt.Errorf("failed to combine partial %d: %w", i, err)
		}
	}

	finalSig := combiner.FinalSig()
	if finalSig == nil {
		return nil, fmt.Errorf("musig final signature not available")
	}

	inner.Signature = finalSig
	_ = location
	return encodeSignedQiTx(inner)
}
