package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
)

type KeyAggVectors struct {
	Pubkeys        []string `json:"pubkeys"`
	Tweaks         []string `json:"tweaks"`
	ValidTestCases []struct {
		KeyIndices []int  `json:"key_indices"`
		Expected   string `json:"expected"`
	} `json:"valid_test_cases"`
	ErrorTestCases []struct {
		KeyIndices   []int  `json:"key_indices"`
		TweakIndices []int  `json:"tweak_indices"`
		IsXonly      []bool `json:"is_xonly"`
		Error        struct {
			Type    string `json:"type"`
			Signer  int    `json:"signer"`
			Contrib string `json:"contrib"`
			Message string `json:"message"`
		} `json:"error"`
		Comment string `json:"comment"`
	} `json:"error_test_cases"`
}

type SigAggVectors struct {
	Pubkeys        []string `json:"pubkeys"`
	PNonces        []string `json:"pnonces"`
	Tweaks         []string `json:"tweaks"`
	PSigs          []string `json:"psigs"`
	Msg            string   `json:"msg"`
	ValidTestCases []struct {
		AggNonce     string `json:"aggnonce"`
		NonceIndices []int  `json:"nonce_indices"`
		KeyIndices   []int  `json:"key_indices"`
		TweakIndices []int  `json:"tweak_indices"`
		IsXonly      []bool `json:"is_xonly"`
		PSigIndices  []int  `json:"psig_indices"`
		Expected     string `json:"expected"`
	} `json:"valid_test_cases"`
	ErrorTestCases []struct {
		AggNonce     string `json:"aggnonce"`
		NonceIndices []int  `json:"nonce_indices"`
		KeyIndices   []int  `json:"key_indices"`
		TweakIndices []int  `json:"tweak_indices"`
		IsXonly      []bool `json:"is_xonly"`
		PSigIndices  []int  `json:"psig_indices"`
		Error        struct {
			Type    string `json:"type"`
			Signer  int    `json:"signer"`
			Contrib string `json:"contrib"`
			Message string `json:"message"`
		} `json:"error"`
		Comment string `json:"comment"`
	} `json:"error_test_cases"`
}

func TestKeyAggregation(t *testing.T) {
	vectors, err := loadKeyAggVectors("key_agg_vectors.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, testCase := range vectors.ValidTestCases {
		t.Run(fmt.Sprintf("KeyIndices_%v", testCase.KeyIndices), func(t *testing.T) {
			// Collect the public keys
			var pubKeys []*btcec.PublicKey
			for _, idx := range testCase.KeyIndices {
				pubKeyBytes, err := hex.DecodeString(vectors.Pubkeys[idx])
				if err != nil {
					t.Fatalf("Failed to decode pubkey: %v", err)
				}
				pubKey, err := btcec.ParsePubKey(pubKeyBytes)
				if err != nil {
					t.Fatalf("Failed to parse pubkey: %v", err)
				}
				pubKeys = append(pubKeys, pubKey)
			}

			// Perform key aggregation
			aggKey, _, _, err := musig2.AggregateKeys(pubKeys, false)
			if err != nil {
				t.Fatalf("Key aggregation failed: %v", err)
			}

			// Compare the aggregated key with the expected value
			expectedAggKeyBytes, err := hex.DecodeString(testCase.Expected)
			if err != nil {
				t.Fatalf("Failed to decode expected aggregated key: %v", err)
			}

			// The expected aggregated key is an x-only public key.
			// Convert the aggregated key to x-only format.
			aggKeyBytes := aggKey.FinalKey.SerializeCompressed()[1:] // Remove the prefix byte

			if !bytes.Equal(aggKeyBytes, expectedAggKeyBytes) {
				t.Errorf("Aggregated key mismatch.\nExpected: %x\nGot:      %x",
					expectedAggKeyBytes, aggKeyBytes)
			} else {
				t.Logf("Success: Aggregated key matches expected value.")
			}
		})
	}
}

func TestSignatureAggregation(t *testing.T) {
	vectors, err := loadSigAggVectors("sig_agg_vectors.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, testCase := range vectors.ValidTestCases {
		t.Run(fmt.Sprintf("KeyIndices_%v_NonceIndices_%v", testCase.KeyIndices, testCase.NonceIndices), func(t *testing.T) {
			// Collect the public keys
			var pubKeys []*btcec.PublicKey
			for _, idx := range testCase.KeyIndices {
				pubKeyBytes, err := hex.DecodeString(vectors.Pubkeys[idx])
				if err != nil {
					t.Fatalf("Failed to decode pubkey: %v", err)
				}
				pubKey, err := btcec.ParsePubKey(pubKeyBytes)
				if err != nil {
					t.Fatalf("Failed to parse pubkey: %v", err)
				}
				pubKeys = append(pubKeys, pubKey)
			}

			// Compare the aggregated signature with the expected value
			expectedSigBytes, err := hex.DecodeString(testCase.Expected)
			if err != nil {
				t.Fatalf("Failed to decode expected signature: %v", err)
			}

			// For Musig2, the final signature is a 64-byte Schnorr signature.

			// Verify the aggregated signature
			msgBytes, err := hex.DecodeString(vectors.Msg)
			if err != nil {
				t.Fatalf("Failed to decode message: %v", err)
			}

			tweaks := make([]musig2.KeyTweakDesc, 0)
			for _, idx := range testCase.TweakIndices {
				tweakHex, err := hex.DecodeString(vectors.Tweaks[testCase.TweakIndices[idx]])
				if err != nil {
					t.Fatalf("Failed to decode tweak at index %d, %v", idx, err)
				}
				tweak := musig2.KeyTweakDesc{
					Tweak:   [32]byte(tweakHex),
					IsXOnly: testCase.IsXonly[idx],
				}
				tweaks = append(tweaks, tweak)
			}

			// Use the aggregated key from the key aggregation step
			aggKey, _, _, err := musig2.AggregateKeys(pubKeys, false, musig2.WithKeyTweaks(tweaks...))
			if err != nil {
				t.Fatalf("Key aggregation failed: %v", err)
			}

			// Verify the signature
			// Parse r (first 32 bytes) into a btcec.FieldVal
			var r btcec.FieldVal
			r.SetByteSlice(expectedSigBytes[:32])

			// Parse s (next 32 bytes) into a btcec.ModNScalar
			var s btcec.ModNScalar
			if s.SetByteSlice(expectedSigBytes[32:]) {
				t.Fatalf("Invalid s value in signature: overflow occurred")
			}

			// Create the signature using NewSignature
			finalSig := schnorr.NewSignature(&r, &s)

			if !finalSig.Verify(msgBytes, aggKey.FinalKey) {
				t.Errorf("Signature verification failed.")
			} else {
				t.Logf("Success: Signature verification passed.")
			}
		})
	}
}

func loadKeyAggVectors(filename string) (*KeyAggVectors, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var vectors KeyAggVectors
	err = json.Unmarshal(data, &vectors)
	if err != nil {
		return nil, err
	}
	return &vectors, nil
}

func loadSigAggVectors(filename string) (*SigAggVectors, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var vectors SigAggVectors
	err = json.Unmarshal(data, &vectors)
	if err != nil {
		return nil, err
	}
	return &vectors, nil
}

func TestSingleSigner(t *testing.T) {

	location := common.Location{0, 0}
	// ECDSA key
	key, err := crypto.HexToECDSA("345debf66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		t.Fatal(err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey, location)
	b, err := hex.DecodeString("345debf66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		t.Fatal(err)
	}

	// btcec key for schnorr use
	btcecKey, _ := btcec.PrivKeyFromBytes(b)

	coinbaseBlockHash := common.HexToHash("000000000000000000000000000000000000000000000000000012")
	coinbaseIndex := uint16(0)

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
		TxIn:  TxIns{in},
		TxOut: TxOuts{newOut},
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
		t.Fatal(err)
	}
	addr1 := crypto.PubkeyToAddress(key1.PublicKey, location)

	b1, err := hex.DecodeString("345debf66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		t.Fatal(err)
	}

	// btcec key for schnorr use
	btcecKey1, _ := btcec.PrivKeyFromBytes(b1)

	key2, err := crypto.HexToECDSA("000000f66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		t.Fatal(err)
	}

	b2, err := hex.DecodeString("000000f66bc68724062b236d3b0a6eb30f051e725ebb770f1dc367f2c569f003")
	if err != nil {
		t.Fatal(err)
	}

	btcecKey2, _ := btcec.PrivKeyFromBytes(b2)

	// Spendable out, could come from anywhere
	coinbaseOutput := &TxOut{
		Denomination: uint8(1),
		Address:      addr1.Bytes(),
	}

	fmt.Println(coinbaseOutput)

	coinbaseIndex := uint16(0)

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
					t.Errorf("unable to add public nonce, err %s", err)
				}

				if j == len(signers)-1 && !haveAll {
					t.Error("all public nonces should have been detected")
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

func TestVerify(t *testing.T) {
	// Test that we mark a signature as valid when it is and invalid when it is not valid
	testCases := []struct {
		name           string
		privateKey     string
		publicKey      string
		messageDigest  string
		signature      string
		expectedResult bool
		comment        string
		auxRand        string
	}{
		// https://github.com/bitcoin/bips/blob/master/bip-0340/test-vectors.csv
		{ // TEST CASE #3:
			name:           "Fails signature with public key not on the curve",
			publicKey:      "03eefdea4cdb677750a420fee807eacf21eb9898ae79b9768766e4faa04a2d4a34",
			messageDigest:  "4df3c3f68fcc83b27e9d42c90431a72499f17875c81a599b566c9889b9696703",
			signature:      "00000000000000000000003b78ce563f89a0ed9414f5aa28ad0d96d6795f9c6302a8dc32e64e86a333f20ef56eac9ba30b7246d6d25e22adb8c6be1aeb08d49d",
			expectedResult: false,
		},
		{ // TEST CASE #4: FAILING
			name:           "Fails signature with incorrect R residuosity",
			privateKey:     "c90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b14e5c7",
			publicKey:      "04fac2114c2fbb091527eb7c64ecb11f8021cb45e8e7809d3c0938e4b8c0e5f84bc655c2105c3c5c380f2c8b8ce2c0c25b0d57062d2d28187254f0deb802b8891f",
			messageDigest:  "243f6a8885a308d313198a2e03707344a4093822299f31d0082efa98ec4e6c89",
			signature:      "48a215e87777e4fa800d5d2a3d7b858414401727063f2c189355853b9d0f9a87f468606087da7f2373befefa1259e71cccbdc9bd75eadd1a73e346420fa75cf7",
			expectedResult: false,
		},
		{ // TEST CASE #5:
			name:           "Fails signature with negated message",
			privateKey:     "c90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b14e5c7",
			publicKey:      "04fac2114c2fbb091527eb7c64ecb11f8021cb45e8e7809d3c0938e4b8c0e5f84bc655c2105c3c5c380f2c8b8ce2c0c25b0d57062d2d28187254f0deb802b8891f",
			messageDigest:  "5e2d58d8b3bcdf1abadec7829054f90dda9805aab56c77333024b9d0a508b75c",
			signature:      "00da9b08172a9b6f0466a2defd817f2d7ab437e0d253cb5395a963866b3574bed092f9d860f1776a1f7412ad8a1eb50daccc222bc8c0e26b2056df2f273efdec",
			expectedResult: false,
		},
		{ // TEST CASE #6:
			name:           "Fails signature with negated s value",
			privateKey:     "c90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b14e5c7",
			publicKey:      "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
			messageDigest:  "0000000000000000000000000000000000000000000000000000000000000000",
			signature:      "b75dea1788881b057ff2a2d5c2847a7bebbfe8d8f9c0d3e76caa7ac462f065780b979f9f782580dc8c410105eda618e3334236428a1522e58c1cb9bdf058a308",
			expectedResult: false,
		},
		{
			name:           "Test vector #0",
			privateKey:     "0000000000000000000000000000000000000000000000000000000000000003",
			publicKey:      "F9308A019258C31049344F85F89D5229B531C845836F99B08601F113BCE036F9",
			auxRand:        "0000000000000000000000000000000000000000000000000000000000000000",
			messageDigest:  "0000000000000000000000000000000000000000000000000000000000000000",
			signature:      "E907831F80848D1069A5371B402410364BDF1C5F8307B0084C55F1CE2DCA821525F66A4A85EA8B71E482A74F382D2CE5EBEEE8FDB2172F477DF4900D310536C0",
			expectedResult: true,
			comment:        "",
		},
		{
			name:           "Test vector #1",
			privateKey:     "B7E151628AED2A6ABF7158809CF4F3C762E7160F38B4DA56A784D9045190CFEF",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			auxRand:        "0000000000000000000000000000000000000000000000000000000000000001",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "6896BD60EEAE296DB48A229FF71DFE071BDE413E6D43F917DC8DCF8C78DE33418906D11AC976ABCCB20B091292BFF4EA897EFCB639EA871CFA95F6DE339E4B0A",
			expectedResult: true,
			comment:        "",
		},
		{
			name:           "Test vector #2",
			privateKey:     "C90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B14E5C9",
			publicKey:      "DD308AFEC5777E13121FA72B9CC1B7CC0139715309B086C960E18FD969774EB8",
			auxRand:        "C87AA53824B4D7AE2EB035A2B5BBBCCC080E76CDC6D1692C4B0B62D798E6D906",
			messageDigest:  "7E2D58D8B3BCDF1ABADEC7829054F90DDA9805AAB56C77333024B9D0A508B75C",
			signature:      "5831AAEED7B44BB74E5EAB94BA9D4294C49BCF2A60728D8B4C200F50DD313C1BAB745879A5AD954A72C45A91C3A51D3C7ADEA98D82F8481E0E1E03674A6F3FB7",
			expectedResult: true,
			comment:        "",
		},
		{
			name:           "Test vector #3 - test fails if msg is reduced modulo p or n",
			privateKey:     "0B432B2677937381AEF05BB02A66ECD012773062CF3FA2549E44F58ED2401710",
			publicKey:      "25D1DFF95105F5253C4022F628A996AD3A0D95FBF21D468A1B33F8C160D8F517",
			auxRand:        "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF",
			messageDigest:  "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF",
			signature:      "7EB0509757E246F19449885651611CB965ECC1A187DD51B64FDA1EDC9637D5EC97582B9CB13DB3933705B32BA982AF5AF25FD78881EBB32771FC5922EFC66EA3",
			expectedResult: true,
			comment:        "test fails if msg is reduced modulo p or n",
		},
		{
			name:           "Test vector #4",
			publicKey:      "D69C3509BB99E412E68B0FE8544E72837DFA30746D8BE2AA65975F29D22DC7B9",
			messageDigest:  "4DF3C3F68FCC83B27E9D42C90431A72499F17875C81A599B566C9889B9696703",
			signature:      "00000000000000000000003B78CE563F89A0ED9414F5AA28AD0D96D6795F9C6376AFB1548AF603B3EB45C9F8207DEE1060CB71C04E80F593060B07D28308D7F4",
			expectedResult: true,
			comment:        "",
		},
		{
			name:           "Test vector #5 - public key not on the curve",
			publicKey:      "EEFDEA4CDB677750A420FEE807EACF21EB9898AE79B9768766E4FAA04A2D4A34",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "6CFF5C3BA86C69EA4B7376F31A9BCB4F74C1976089B2D9963DA2E5543E17776969E89B4C5564D00349106B8497785DD7D1D713A8AE82B32FA79D5F7FC407D39B",
			expectedResult: false,
			comment:        "public key not on the curve",
		},
		{
			name:           "Test vector #6 - has_even_y(R) is false",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "FFF97BD5755EEEA420453A14355235D382F6472F8568A18B2F057A14602975563CC27944640AC607CD107AE10923D9EF7A73C643E166BE5EBEAFA34B1AC553E2",
			expectedResult: false,
			comment:        "has_even_y(R) is false",
		},
		{
			name:           "Test vector #7 - negated message",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "1FA62E331EDBC21C394792D2AB1100A7B432B013DF3F6FF4F99FCB33E0E1515F28890B3EDB6E7189B630448B515CE4F8622A954CFE545735AAEA5134FCCDB2BD",
			expectedResult: false,
			comment:        "negated message",
		},
		{
			name:           "Test vector #8 - negated s value",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "6CFF5C3BA86C69EA4B7376F31A9BCB4F74C1976089B2D9963DA2E5543E177769961764B3AA9B2FFCB6EF947B6887A226E8D7C93E00C5ED0C1834FF0D0C2E6DA6",
			expectedResult: false,
			comment:        "negated s value",
		},
		{
			name:           "Test vector #9 - sG - eP is infinite",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "0000000000000000000000000000000000000000000000000000000000000000123DDA8328AF9C23A94C1FEECFD123BA4FB73476F0D594DCB65C6425BD186051",
			expectedResult: false,
			comment:        "sG - eP is infinite",
		},
		{
			name:           "Test vector #10 - sG - eP is infinite",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "00000000000000000000000000000000000000000000000000000000000000017615FBAF5AE28864013C099742DEADB4DBA87F11AC6754F93780D5A1837CF197",
			expectedResult: false,
			comment:        "sG - eP is infinite",
		},
		{
			name:           "Test vector #11 - sig[0:32] not X coordinate on curve",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "4A298DACAE57395A15D0795DDBFD1DCB564DA82B0F269BC70A74F8220429BA1D69E89B4C5564D00349106B8497785DD7D1D713A8AE82B32FA79D5F7FC407D39B",
			expectedResult: false,
			comment:        "sig[0:32] is not an X coordinate on the curve",
		},
		{
			name:           "Test vector #12 - sig[0:32] equals field size",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F69E89B4C5564D00349106B8497785DD7D1D713A8AE82B32FA79D5F7FC407D39B",
			expectedResult: false,
			comment:        "sig[0:32] is equal to field size",
		},
		{
			name:           "Test vector #13 - sig[32:64] equals curve order",
			publicKey:      "DFF1D77F2A671C5F36183726DB2341BE58FEAE1DA2DECED843240F7B502BA659",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "6CFF5C3BA86C69EA4B7376F31A9BCB4F74C1976089B2D9963DA2E5543E177769FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141",
			expectedResult: false,
			comment:        "sig[32:64] is equal to curve order",
		},
		{
			name:           "Test vector #14 - public key exceeds field size",
			publicKey:      "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC30",
			messageDigest:  "243F6A8885A308D313198A2E03707344A4093822299F31D0082EFA98EC4E6C89",
			signature:      "6CFF5C3BA86C69EA4B7376F31A9BCB4F74C1976089B2D9963DA2E5543E17776969E89B4C5564D00349106B8497785DD7D1D713A8AE82B32FA79D5F7FC407D39B",
			expectedResult: false,
			comment:        "public key exceeds field size",
		},
	}

	var signature [64]byte
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			publicBytes, err := hex.DecodeString(testCase.publicKey)
			if err != nil {
				t.Fatalf("Failed to decode public key: %s", err)
			}
			msgBytes, err := hex.DecodeString(testCase.messageDigest)
			if err != nil {
				t.Fatalf("Failed to decode message digest: %s", err)
			}
			sig, err := hex.DecodeString(testCase.signature)
			if err != nil {
				t.Fatalf("Failed to decode signature: %s", err)
			}
			copy(signature[:], sig)
			if len(publicBytes) == 32 {
				publicBytes = append([]byte{0x02}, publicBytes...)
			}
			public, err := btcec.ParsePubKey(publicBytes)
			if err != nil {
				errorMessage := err.Error()
				if strings.Contains(errorMessage, "invalid public key") && testCase.expectedResult == false {
					return
				}
				t.Fatalf("Failed to parse public key: %s", err)
			}
			var result bool
			sigFormatted, err := schnorr.ParseSignature(signature[:])
			if err != nil {
				if testCase.expectedResult == false {
					result = false
				} else {
					t.Fatalf("Failed to parse signature: %s", err)
				}
			} else {
				result = sigFormatted.Verify(msgBytes, public)
			}

			if result != testCase.expectedResult { // || err != nil
				t.Fatalf("Did not confirm/deny validity of signature as expected: Want: %t    Got: %t   Error: %s", testCase.expectedResult, result, err)
			} else {
				t.Logf("SUCCESS: Expected verify result. Schnorr Signature is valid: %t    Error: %s", result, err)
			}
		})
	}
}

// ModifyRSig takes a Schnorr signature and modifies its R component.
// signatureHex is the original signature in hexadecimal format.
// It returns the modified signature also in hexadecimal format.
func TestModifyRSig(t *testing.T) {
	signatureHex := "b75dea1788881b057ff2a2d5c2847a7bebbfe8d8f9c0d3e76caa7ac462f06578f468606087da7f2373befefa1259e71cccbdc9bd75eadd1a73e346420fa75cf7"
	// Decode the signature from hex format
	signatureBytes, err := hex.DecodeString(signatureHex)
	if err != nil {
		t.Fatalf("Failed to parse signature: %s", err)
	}

	// Check if the signature length is even
	if len(signatureBytes)%2 != 0 {
		t.Fatalf("Failed to parse signature: %s", err)
	}

	// Calculate the length of R and S components
	halfLength := len(signatureBytes) / 2

	// Modify the R component
	// Here, we simply invert the bytes of the R component
	// You can replace this logic with any other modification you need
	for i := 0; i < halfLength; i++ {
		signatureBytes[i] = ^signatureBytes[i]
	}

	// Encode the modified signature back to hex format
	modifiedSigHex := hex.EncodeToString(signatureBytes)
	t.Log("Modified Signature: ", modifiedSigHex)
}

// ModifySSig takes a Schnorr signature and modifies its S component.
// signatureHex is the original signature in hexadecimal format.
// It returns the modified signature also in hexadecimal format.
func TestModifySSig(t *testing.T) {
	signatureHex := "b75dea1788881b057ff2a2d5c2847a7bebbfe8d8f9c0d3e76caa7ac462f06578f468606087da7f2373befefa1259e71cccbdc9bd75eadd1a73e346420fa75cf7"
	// Decode the signature from hex format
	signatureBytes, err := hex.DecodeString(signatureHex)
	if err != nil {
		t.Fatalf("Failed to parse signature: %s", err)
	}

	// Check if the signature length is even
	if len(signatureBytes)%2 != 0 {
		t.Fatalf("Signature length is not even: %s", err)
	}

	// Calculate the length of R and S components
	halfLength := len(signatureBytes) / 2

	// Modify the S component
	// Here, we simply invert the bytes of the S component
	// You can replace this logic with any other modification you need
	for i := halfLength; i < len(signatureBytes); i++ {
		signatureBytes[i] = ^signatureBytes[i]
	}

	// Encode the modified signature back to hex format
	modifiedSigHex := hex.EncodeToString(signatureBytes)
	t.Log("Modified Signature: ", modifiedSigHex)
}
