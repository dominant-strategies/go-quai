package qiwallet

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/core/types"
)

// SignQiTx signs an unsigned QiTx whose inputs are all controlled by the
// provided private keys, given in the same order as the transaction inputs.
// It produces the single aggregate Schnorr signature consensus expects:
// plain BIP-340 Schnorr for one input, and the local MuSig2 protocol across
// the input keys for several, mirroring consensus verification exactly
// (unsorted key aggregation, no tweaks).
//
// Nonces are drawn fresh from crypto/rand for every call. Never substitute
// deterministic or reused nonces in a MuSig2 session: nonce reuse leaks the
// private key. Hardware signers must persist no nonce state across calls.
//
// The returned transaction is fully signed and ready for submission.
func SignQiTx(qiTx *types.QiTx, signer types.Signer, keys []*btcec.PrivateKey) (*types.Transaction, error) {
	if qiTx == nil || signer == nil {
		return nil, errors.New("nil transaction or signer")
	}
	if len(keys) == 0 || len(keys) != len(qiTx.TxIn) {
		return nil, fmt.Errorf("need exactly one key per input: %d keys for %d inputs", len(keys), len(qiTx.TxIn))
	}

	pubKeys := make([]*btcec.PublicKey, len(keys))
	for i, key := range keys {
		if key == nil {
			return nil, fmt.Errorf("nil key at index %d", i)
		}
		pubKeys[i] = key.PubKey()
		txInKey, err := btcec.ParsePubKey(qiTx.TxIn[i].PubKey)
		if err != nil {
			return nil, fmt.Errorf("input %d has an unparseable public key: %w", i, err)
		}
		if !txInKey.IsEqual(pubKeys[i]) {
			return nil, fmt.Errorf("key at index %d does not match input %d's public key", i, i)
		}
	}

	// The signing digest covers type, chain ID, inputs, outputs and data,
	// but not the signature itself, so it can be computed pre-signature.
	digest := signer.Hash(types.NewTx(qiTx))
	var msg [32]byte
	copy(msg[:], digest[:])

	var signature *schnorr.Signature
	if len(keys) == 1 {
		sig, err := schnorr.Sign(keys[0], msg[:])
		if err != nil {
			return nil, err
		}
		signature = sig
	} else {
		sig, err := musig2SignLocal(keys, pubKeys, msg)
		if err != nil {
			return nil, err
		}
		signature = sig
	}

	// Fail fast: verify against the same aggregate the consensus code uses.
	finalKey := pubKeys[0]
	if len(pubKeys) > 1 {
		aggKey, _, _, err := musig2.AggregateKeys(pubKeys, false)
		if err != nil {
			return nil, err
		}
		finalKey = aggKey.FinalKey
	}
	if !signature.Verify(msg[:], finalKey) {
		return nil, errors.New("produced signature does not verify against the aggregate key")
	}

	signed := &types.QiTx{
		ChainID:   qiTx.ChainID,
		TxIn:      qiTx.TxIn,
		TxOut:     qiTx.TxOut,
		Data:      qiTx.Data,
		Signature: signature,
	}
	return types.NewTx(signed), nil
}

// musig2SignLocal runs the two-round MuSig2 protocol entirely locally
// across the given keys, matching consensus verification: keys aggregated
// in input order, unsorted, untweaked.
func musig2SignLocal(keys []*btcec.PrivateKey, pubKeys []*btcec.PublicKey, msg [32]byte) (*schnorr.Signature, error) {
	nonces := make([]*musig2.Nonces, len(keys))
	pubNonces := make([][musig2.PubNonceSize]byte, len(keys))
	for i, key := range keys {
		nonce, err := musig2.GenNonces(musig2.WithPublicKey(key.PubKey()))
		if err != nil {
			return nil, err
		}
		nonces[i] = nonce
		pubNonces[i] = nonce.PubNonce
	}
	combinedNonce, err := musig2.AggregateNonces(pubNonces)
	if err != nil {
		return nil, err
	}
	partialSigs := make([]*musig2.PartialSignature, len(keys))
	for i, key := range keys {
		partial, err := musig2.Sign(nonces[i].SecNonce, key, combinedNonce, pubKeys, msg)
		if err != nil {
			return nil, err
		}
		partialSigs[i] = partial
	}
	return musig2.CombineSigs(partialSigs[0].R, partialSigs), nil
}
