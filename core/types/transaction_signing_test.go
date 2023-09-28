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
	"fmt"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/crypto/sr25519"
	"github.com/stretchr/testify/require"
)

func TestInternalSigningSecp256k(t *testing.T) {
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)

	fmt.Println(&addr)
	signer := NewSigner(big.NewInt(18))
	txData := &InternalTx{
		Nonce:     0,
		To:        &addr,
		Gas:       21000,
		Value:     big.NewInt(1),
		ChainID:   big.NewInt(1),
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(1),
	}
	tx, err := SignTx(NewTx(txData), signer, key)
	if err != nil {
		t.Fatal(err)
	}

	from, err := Sender(signer, tx)
	if err != nil {
		t.Fatal(err)
	}
	if from.String() != addr.String() {
		t.Errorf("exected from and address to be equal. Got %x want %x", from, addr)
	}
}

func TestInternalSigningRistretto(t *testing.T) {
	keypair, err := sr25519.GenerateKeypair()
	require.NoError(t, err)

	addr := keypair.Public().Address()
	fmt.Println(keypair.Public().Hex())
	fmt.Println(&addr)

	// encoded := keypair.Public().Encode()
	// addr := common.BytesToAddress(encoded)

	pub := keypair.Public().(*sr25519.PublicKey)

	signer := NewSigner(big.NewInt(1))
	txData := &InternalTx{
		Nonce:      0,
		FromPubKey: *pub,
		To:         &addr,
		Gas:        21000,
		Value:      big.NewInt(1),
		ChainID:    big.NewInt(1),
		GasTipCap:  big.NewInt(1),
		GasFeeCap:  big.NewInt(1),
	}

	tx := NewTx(txData)
	msg := signer.Hash(tx)

	sig, err := keypair.Sign(msg.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println(sig)

	tx, err = tx.WithSignature(signer, sig)
	if err != nil {
		t.Fatal(err)
	}

	from, err := Sender(signer, tx)
	if err != nil {
		t.Fatal(err)
	}

	// Stupid check here because we are just loading the from address in Sender from the InternalTx.
	// Need to improve to actually check the sig.
	if from.String() != addr.String() {
		t.Errorf("exected from and address to be equal. Got %x want %x", from, addr)
	}
}
