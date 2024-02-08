package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
)

func TestSingleSigner(t *testing.T) {

	location := common.Location{0, 0}
	// ECDSA key
	key, err := crypto.HexToECDSA("345debf66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		fmt.Println(err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey, location)
	fmt.Println(addr.Hex())

	b, err := hex.DecodeString("345debf66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		fmt.Println(err)
	}

	// btcec key for schnorr use
	btcecKey, _ := btcec.PrivKeyFromBytes(b)

	// Spendable out, could come from anywhere
	coinbaseOutput := &TxOut{
		Denomination: uint8(1),
		Address:      addr.Bytes(),
	}

	fmt.Println(coinbaseOutput)

	coinbaseBlockHash := common.HexToHash("000000000000000000000000000000000000000000000000000012")
	coinbaseIndex := uint32(0)

	// key = hash(blockHash, index)
	// Find hash / index for originUtxo / imagine this is block hash
	prevOut := *NewOutPoint(&coinbaseBlockHash, coinbaseIndex)

	in := TxIn{
		PreviousOutPoint: prevOut,
		PubKey:           crypto.FromECDSAPub(&key.PublicKey),
	}

	newOut := TxOut{
		Denomination: uint8(1),
		// Value:    blockchain.CalcBlockSubsidy(nextBlockHeight, params),
		Address: addr.Bytes(),
	}

	utxo := &QiTx{
		TxIn:  []TxIn{in},
		TxOut: []TxOut{newOut},
	}

	tx := NewTx(utxo)
	txHash := tx.Hash().Bytes()

	sig, err := schnorr.Sign(btcecKey, txHash[:])
	if err != nil {
		t.Fatalf("schnorr signing failed!")
	}

	// Finally we'll combined all the nonces, and ensure that it validates
	// as a single schnorr signature.
	if !sig.Verify(txHash[:], btcecKey.PubKey()) {
		t.Fatalf("final sig is invalid!")
	}
}

func TestMultiSigners(t *testing.T) {

	location := common.Location{0, 0}
	// ECDSA key
	key1, err := crypto.HexToECDSA("345debf66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		fmt.Println(err)
	}
	addr1 := crypto.PubkeyToAddress(key1.PublicKey, location)
	fmt.Println(addr1.Hex())

	b1, err := hex.DecodeString("345debf66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		fmt.Println(err)
	}

	// btcec key for schnorr use
	btcecKey1, _ := btcec.PrivKeyFromBytes(b1)

	key2, err := crypto.HexToECDSA("000000f66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		fmt.Println(err)
	}
	addr2 := crypto.PubkeyToAddress(key2.PublicKey, location)
	fmt.Println(addr2.Hex())

	b2, err := hex.DecodeString("000000f66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		fmt.Println(err)
	}

	btcecKey2, _ := btcec.PrivKeyFromBytes(b2)

	// Spendable out, could come from anywhere
	coinbaseOutput := &TxOut{
		Denomination: uint8(1),
		Address:      addr1.Bytes(),
	}

	fmt.Println(coinbaseOutput)

	coinbaseIndex := uint32(0)

	coinbaseBlockHash1 := common.HexToHash("00000000000000000000000000000000000000000000000000000")
	coinbaseBlockHash2 := common.HexToHash("00000000000000000000000000000000000000000000000000001")

	// key = hash(blockHash, index)
	// Find hash / index for originUtxo / imagine this is block hash
	prevOut1 := *NewOutPoint(&coinbaseBlockHash1, coinbaseIndex)
	prevOut2 := *NewOutPoint(&coinbaseBlockHash2, coinbaseIndex)

	in1 := TxIn{
		PreviousOutPoint: prevOut1,
		PubKey:           crypto.FromECDSAPub(&key1.PublicKey),
	}

	in2 := TxIn{
		PreviousOutPoint: prevOut2,
		PubKey:           crypto.FromECDSAPub(&key2.PublicKey),
	}

	newOut1 := TxOut{
		Denomination: uint8(1),
		Address:      addr1.Bytes(),
	}

	newOut2 := TxOut{
		Denomination: uint8(1),
		Address:      addr1.Bytes(),
	}

	utxo := &QiTx{
		TxIn:  []TxIn{in1, in2},
		TxOut: []TxOut{newOut1, newOut2},
	}

	tx := NewTx(utxo)
	txHash := sha256.Sum256(tx.Hash().Bytes())

	keys := []*btcec.PrivateKey{btcecKey1, btcecKey2}
	signSet := []*btcec.PublicKey{btcecKey1.PubKey(), btcecKey2.PubKey()}

	var combinedKey *btcec.PublicKey
	var ctxOpts []musig2.ContextOption

	ctxOpts = append(ctxOpts, musig2.WithKnownSigners(signSet))

	// Now that we have all the signers, we'll make a new context, then
	// generate a new session for each of them(which handles nonce
	// generation).
	signers := make([]*musig2.Session, len(keys))
	for i, signerKey := range keys {
		signCtx, err := musig2.NewContext(
			signerKey, false, ctxOpts...,
		)
		if err != nil {
			t.Fatalf("unable to generate context: %v", err)
		}

		if combinedKey == nil {
			combinedKey, err = signCtx.CombinedKey()
			if err != nil {
				t.Fatalf("combined key not available: %v", err)
			}
		}

		session, err := signCtx.NewSession()
		if err != nil {
			t.Fatalf("unable to generate new session: %v", err)
		}
		signers[i] = session
	}

	// Next, in the pre-signing phase, we'll send all the nonces to each
	// signer.
	var wg sync.WaitGroup
	for i, signCtx := range signers {
		signCtx := signCtx

		wg.Add(1)
		go func(idx int, signer *musig2.Session) {
			defer wg.Done()

			for j, otherCtx := range signers {
				if idx == j {
					continue
				}

				nonce := otherCtx.PublicNonce()
				haveAll, err := signer.RegisterPubNonce(nonce)
				if err != nil {
					t.Fatalf("unable to add public nonce")
				}

				if j == len(signers)-1 && !haveAll {
					t.Fatalf("all public nonces should have been detected")
				}
			}
		}(i, signCtx)
	}

	wg.Wait()

	// In the final step, we'll use the first signer as our combiner, and
	// generate a signature for each signer, and then accumulate that with
	// the combiner.
	combiner := signers[0]
	for i := range signers {
		signer := signers[i]
		partialSig, err := signer.Sign(txHash)
		if err != nil {
			t.Fatalf("unable to generate partial sig: %v", err)
		}

		// We don't need to combine the signature for the very first
		// signer, as it already has that partial signature.
		if i != 0 {
			haveAll, err := combiner.CombineSig(partialSig)
			if err != nil {
				t.Fatalf("unable to combine sigs: %v", err)
			}

			if i == len(signers)-1 && !haveAll {
				t.Fatalf("final sig wasn't reconstructed")
			}
		}
	}

	aggKey, _, _, _ := musig2.AggregateKeys(
		signSet, false,
	)

	fmt.Println("aggKey", aggKey.FinalKey)
	fmt.Println("combinedKey", combinedKey)

	if !aggKey.FinalKey.IsEqual(combinedKey) {
		t.Fatalf("aggKey is invalid!")
	}

	// Finally we'll combined all the nonces, and ensure that it validates
	// as a single schnorr signature.
	finalSig := combiner.FinalSig()
	if !finalSig.Verify(txHash[:], combinedKey) {
		t.Fatalf("final sig is invalid!")
	}
}
