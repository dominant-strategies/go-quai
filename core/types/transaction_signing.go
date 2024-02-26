// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package types

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
)

var (
	ErrUnsupportedTxType = errors.New("tx type is not supported by this signer")
	ErrInvalidChainId    = errors.New("invalid chain id for signer")
)

// sigCache is used to cache the derived sender and contains
// the signer used to derive it.
type sigCache struct {
	signer Signer
	from   common.Address
}

// MakeSigner returns a Signer based on the given chain config and block number.
func MakeSigner(config *params.ChainConfig, blockNumber *big.Int) Signer {
	return NewSigner(config.ChainID, config.Location)
}

// LatestSigner returns the 'most permissive' Signer available for the given chain
// configuration. Use this in transaction-handling code where the current block
// number is unknown. If you have the current block number available, use
// MakeSigner instead.
func LatestSigner(config *params.ChainConfig) Signer {
	return NewSigner(config.ChainID, config.Location)
}

// LatestSigner returns the 'most permissive' Signer available for the given chain
// configuration. Use this in transaction-handling code where the current block
// number is unknown. If you have the current block number available, use
// MakeSigner instead.
//
// Use this in transaction-handling code where the current block number and fork
// configuration are unknown. If you have a ChainConfig, use LatestSigner instead.
// If you have a ChainConfig and know the current block number, use MakeSigner instead.
func LatestSignerForChainID(chainID *big.Int, nodeLocation common.Location) Signer {
	return NewSigner(chainID, nodeLocation)
}

// SignTx signs the transaction using the given signer and private key.
func SignTx(tx *Transaction, s Signer, prv *ecdsa.PrivateKey) (*Transaction, error) {
	h := s.Hash(tx)
	sig, err := crypto.Sign(h[:], prv)
	if err != nil {
		return nil, err
	}
	return tx.WithSignature(s, sig)
}

// SignNewTx creates a transaction and signs it.
func SignNewTx(prv *ecdsa.PrivateKey, s Signer, txdata TxData) (*Transaction, error) {
	tx := NewTx(txdata)
	h := s.Hash(tx)
	sig, err := crypto.Sign(h[:], prv)
	if err != nil {
		return nil, err
	}
	return tx.WithSignature(s, sig)
}

// MustSignNewTx creates a transaction and signs it.
// This panics if the transaction cannot be signed.
func MustSignNewTx(prv *ecdsa.PrivateKey, s Signer, txdata TxData) *Transaction {
	tx, err := SignNewTx(prv, s, txdata)
	if err != nil {
		panic(err)
	}
	return tx
}

// Sender returns the address derived from the signature (V, R, S) using secp256k1
// elliptic curve and an error if it failed deriving or upon an incorrect
// signature.
//
// Sender may cache the address, allowing it to be used regardless of
// signing method. The cache is invalidated if the cached signer does
// not match the signer used in the current call.
func Sender(signer Signer, tx *Transaction) (common.Address, error) {
	if tx.Type() == ExternalTxType { // External TX does not have a signature
		return tx.inner.(*ExternalTx).Sender, nil
	}
	if tx.Type() == QiTxType {
		return common.Zero, errors.New("sender field not available for qi type")
	}
	if sc := tx.from.Load(); sc != nil {
		sigCache := sc.(sigCache)
		// If the signer used to derive from in a previous
		// call is not the same as used current, invalidate
		// the cache.
		if sigCache.signer.Equal(signer) {
			return sigCache.from, nil
		}
	}

	addr, err := signer.Sender(tx)
	if err != nil {
		return common.Zero, err
	}
	tx.from.Store(sigCache{signer: signer, from: addr})
	return addr, nil
}

// Signer encapsulates transaction signature handling. The name of this type is slightly
// misleading because Signers don't actually sign, they're just for validating and
// processing of signatures.
type Signer interface {
	// Sender returns the sender address of the transaction.
	Sender(tx *Transaction) (common.Address, error)

	// SignatureValues returns the raw R, S, V values corresponding to the
	// given signature.
	SignatureValues(tx *Transaction, sig []byte) (r, s, v *big.Int, err error)
	ChainID() *big.Int

	// Hash returns 'signature hash', i.e. the transaction hash that is signed by the
	// private key. This hash does not uniquely identify the transaction.
	Hash(tx *Transaction) common.Hash

	// Equal returns true if the given signer is the same as the receiver.
	Equal(Signer) bool

	Location() common.Location
}

// SignerV1 is the mainnet launch version of the signer module. Future versions may be
// defined if protocol changes are required
type SignerV1 struct {
	chainId, chainIdMul *big.Int
	nodeLocation        common.Location
}

// NewSigner instantiates a new signer object
func NewSigner(chainId *big.Int, nodeLocation common.Location) Signer {
	if chainId == nil {
		chainId = new(big.Int)
	}
	return SignerV1{
		chainId:      chainId,
		chainIdMul:   new(big.Int).Mul(chainId, big.NewInt(2)),
		nodeLocation: nodeLocation,
	}
}

func (s SignerV1) Sender(tx *Transaction) (common.Address, error) {
	if tx.Type() == ExternalTxType { // External TX does not have a signature
		return tx.inner.(*ExternalTx).Sender, nil
	}
	if tx.Type() == QiTxType {
		return common.Zero, errors.New("cannot find the sender for a qi transaction type")
	}
	V, R, S := tx.GetEcdsaSignatureValues()
	// DynamicFee txs are defined to use 0 and 1 as their recovery
	// id, add 27 to become equivalent to unprotected signatures.
	V = new(big.Int).Add(V, big.NewInt(27))
	if tx.ChainId().Cmp(s.chainId) != 0 {
		return common.Zero, ErrInvalidChainId
	}
	return recoverPlain(s.Hash(tx), R, S, V, s.nodeLocation)
}

func (s SignerV1) Equal(s2 Signer) bool {
	x, ok := s2.(SignerV1)
	return ok && x.chainId.Cmp(s.chainId) == 0
}

func (s SignerV1) SignatureValues(tx *Transaction, sig []byte) (R, S, V *big.Int, err error) {
	if tx.Type() == ExternalTxType {
		// ETXs do not have signatures, so there is no data to report. This is
		// not an error though, so no error is returned.
		return nil, nil, nil, nil
	}
	// Check that chain ID of tx matches the signer. We also accept ID zero here,
	// because it indicates that the chain ID was not specified in the tx.
	if tx.ChainId().Sign() != 0 && tx.ChainId().Cmp(s.chainId) != 0 {
		return nil, nil, nil, ErrInvalidChainId
	}
	R, S, _ = decodeSignature(sig)
	V = big.NewInt(int64(sig[64]))
	return R, S, V, nil
}

// Hash returns the hash to be signed by the sender.
// It does not uniquely identify the transaction.
func (s SignerV1) Hash(tx *Transaction) common.Hash {
	if tx.Type() == QiTxType {
		return prefixedRlpHash(
			tx.Type(),
			[]interface{}{
				s.chainId,
				tx.TxIn(),
				tx.TxOut(),
			})
	}
	txTo := tx.To()
	var txToBytes []byte
	if txTo == nil {
		txToBytes = []byte{}
	} else {
		txToBytes = txTo.Bytes()
	}
	if tx.Type() == InternalToExternalTxType {
		return prefixedRlpHash(
			tx.Type(),
			[]interface{}{
				s.chainId,
				tx.Nonce(),
				tx.GasTipCap(),
				tx.GasFeeCap(),
				tx.Gas(),
				txToBytes,
				tx.Value(),
				tx.Data(),
				tx.AccessList(),
				tx.ETXGasLimit(),
				tx.ETXGasPrice(),
				tx.ETXGasTip(),
				tx.ETXData(),
				tx.ETXAccessList(),
			})
	}
	return prefixedRlpHash(
		tx.Type(),
		[]interface{}{
			s.chainId,
			tx.Nonce(),
			tx.GasTipCap(),
			tx.GasFeeCap(),
			tx.Gas(),
			txToBytes,
			tx.Value(),
			tx.Data(),
			tx.AccessList(),
		})
}

func (s SignerV1) ChainID() *big.Int {
	return s.chainId
}

func (s SignerV1) Location() common.Location {
	return s.nodeLocation
}

func decodeSignature(sig []byte) (r, s, v *big.Int) {
	if len(sig) != crypto.SignatureLength {
		panic(fmt.Sprintf("wrong size for signature: got %d, want %d", len(sig), crypto.SignatureLength))
	}
	r = new(big.Int).SetBytes(sig[:32])
	s = new(big.Int).SetBytes(sig[32:64])
	v = new(big.Int).SetBytes([]byte{sig[64] + 27})
	return r, s, v
}

func recoverPlain(sighash common.Hash, R, S, Vb *big.Int, nodeLocation common.Location) (common.Address, error) {
	if Vb.BitLen() > 8 {
		return common.Zero, ErrInvalidSig
	}
	V := byte(Vb.Uint64() - 27)
	if !crypto.ValidateSignatureValues(V, R, S) {
		return common.Address{}, ErrInvalidSig
	}
	// encode the signature in uncompressed format
	r, s := R.Bytes(), S.Bytes()
	sig := make([]byte, crypto.SignatureLength)
	copy(sig[32-len(r):32], r)
	copy(sig[64-len(s):64], s)
	sig[64] = V
	// recover the public key from the signature
	pub, err := crypto.Ecrecover(sighash[:], sig)
	if err != nil {
		return common.Zero, err
	}
	if len(pub) == 0 || pub[0] != 4 {
		return common.Zero, errors.New("invalid public key")
	}
	addr := common.BytesToAddress(crypto.Keccak256(pub[1:])[12:], nodeLocation)
	return addr, nil
}
