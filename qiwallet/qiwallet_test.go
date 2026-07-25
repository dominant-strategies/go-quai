package qiwallet_test

import (
	"math/big"
	mrand "math/rand"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/qiwallet"
)

var testLocation = common.Location{0, 0}

// qiAddress fabricates a syntactically valid Qi-scope address for zone 0-0.
func qiAddress(seed byte) common.Address {
	var b [20]byte
	b[0] = 0x00        // region 0, zone 0
	b[1] = 0x80 | seed // Qi ledger scope
	for i := 2; i < 20; i++ {
		b[i] = seed
	}
	return common.BytesToAddress(b[:], testLocation)
}

func TestDecomposeGreedy(t *testing.T) {
	notes, err := qiwallet.DecomposeGreedy(big.NewInt(1234))
	if err != nil {
		t.Fatal(err)
	}
	if qiwallet.NotesValue(notes).Cmp(big.NewInt(1234)) != 0 {
		t.Fatalf("greedy decomposition value mismatch: %v", notes)
	}
	// 1234 = 1000 + 2x100 + 3x10 + 4x1
	if len(notes) != 10 {
		t.Fatalf("expected 10 notes, got %d: %v", len(notes), notes)
	}
	if _, err := qiwallet.DecomposeGreedy(big.NewInt(0)); err == nil {
		t.Fatal("expected error for zero value")
	}
}

func TestDecomposeRandomPreservesValue(t *testing.T) {
	rng := mrand.New(mrand.NewSource(42))
	for i := 0; i < 50; i++ {
		value := big.NewInt(int64(rng.Intn(5000000) + 1))
		notes, err := qiwallet.DecomposeRandom(value, 40, rng)
		if err != nil {
			// Values needing more than 40 greedy notes are legitimately rejected.
			continue
		}
		if qiwallet.NotesValue(notes).Cmp(value) != 0 {
			t.Fatalf("value %s not preserved by %v", value, notes)
		}
		if len(notes) > 40 {
			t.Fatalf("note budget exceeded: %d", len(notes))
		}
	}
}

func makeUTXO(denomination uint8, seed byte, creationHeight uint64) qiwallet.OwnedUTXO {
	var txHash common.Hash
	txHash[0] = seed
	txHash[1] = denomination
	return qiwallet.OwnedUTXO{
		OutPoint:       types.OutPoint{TxHash: txHash, Index: uint16(seed)},
		Denomination:   denomination,
		Address:        qiAddress(seed),
		CreationHeight: creationHeight,
	}
}

func testFee(numIn, numOut int) *big.Int {
	return big.NewInt(int64(10*numIn + 20*numOut))
}

func TestSelectUTXOs(t *testing.T) {
	rng := mrand.New(mrand.NewSource(7))
	utxos := []qiwallet.OwnedUTXO{
		makeUTXO(6, 1, 0), makeUTXO(6, 2, 0), makeUTXO(6, 3, 0),
		makeUTXO(4, 4, 0), makeUTXO(4, 5, 0), makeUTXO(4, 6, 0),
		makeUTXO(2, 7, 0), makeUTXO(0, 8, 0),
	}
	amount := big.NewInt(1500)
	sel, err := qiwallet.SelectUTXOs(utxos, amount, 1_000_000, testFee, 30, rng)
	if err != nil {
		t.Fatal(err)
	}

	// Payment must equal the requested amount.
	if qiwallet.NotesValue(sel.PaymentNotes).Cmp(amount) != 0 {
		t.Fatalf("payment notes %v do not sum to %s", sel.PaymentNotes, amount)
	}
	// Conservation: inputs = payment + change + fee.
	totalIn := big.NewInt(0)
	inputCounts := make(map[uint]uint64)
	for _, in := range sel.Inputs {
		totalIn.Add(totalIn, types.Denominations[in.Denomination])
		inputCounts[uint(in.Denomination)]++
	}
	spent := new(big.Int).Add(qiwallet.NotesValue(sel.PaymentNotes), qiwallet.NotesValue(sel.ChangeNotes))
	spent.Add(spent, sel.FeeQits)
	if totalIn.Cmp(spent) != 0 {
		t.Fatalf("value not conserved: in %s, out+fee %s", totalIn, spent)
	}
	// Fee must satisfy the fee function for the final shape.
	required := testFee(len(sel.Inputs), len(sel.PaymentNotes)+len(sel.ChangeNotes))
	if sel.FeeQits.Cmp(required) < 0 {
		t.Fatalf("fee %s below required %s", sel.FeeQits, required)
	}
	// Cross-check against the actual consensus rule.
	outputCounts := make(map[uint]uint64)
	for _, n := range sel.PaymentNotes {
		outputCounts[uint(n)]++
	}
	for _, n := range sel.ChangeNotes {
		outputCounts[uint(n)]++
	}
	if err := core.CheckDenominations(inputCounts, outputCounts); err != nil {
		t.Fatalf("consensus CheckDenominations rejected the selection: %v", err)
	}
}

func TestSelectUTXOsRescuesExpiring(t *testing.T) {
	currentHeight := uint64(1_000_000)
	// Expires 100 blocks from now: must be pulled in even though the big
	// note alone would cover the payment.
	expiringCreation := currentHeight - types.TrimDepths[4] + 100
	utxos := []qiwallet.OwnedUTXO{
		makeUTXO(6, 1, 0),
		makeUTXO(4, 2, expiringCreation),
	}
	sel, err := qiwallet.SelectUTXOs(utxos, big.NewInt(300), currentHeight, testFee, 30, mrand.New(mrand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, in := range sel.Inputs {
		if in.CreationHeight == expiringCreation {
			found = true
		}
	}
	if !found {
		t.Fatal("expiring note was not rescued")
	}
}

func TestSelectUTXOsInsufficient(t *testing.T) {
	utxos := []qiwallet.OwnedUTXO{makeUTXO(2, 1, 0)}
	if _, err := qiwallet.SelectUTXOs(utxos, big.NewInt(100000), 100, testFee, 30, mrand.New(mrand.NewSource(1))); err == nil {
		t.Fatal("expected insufficient funds error")
	}
}

func TestBuildQiTxAddressReuseBan(t *testing.T) {
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	input := makeUTXO(6, 1, 0)
	input.PubKey = key.PubKey().SerializeUncompressed()
	sel := &qiwallet.Selection{
		Inputs:       []qiwallet.OwnedUTXO{input},
		PaymentNotes: []uint8{5},
		ChangeNotes:  []uint8{4},
		FeeQits:      big.NewInt(400),
	}
	// Reusing the same address for payment and change must be rejected.
	dup := qiAddress(9)
	if _, err := qiwallet.BuildQiTx(big.NewInt(1337), testLocation, sel, []common.Address{dup}, []common.Address{dup}, mrand.New(mrand.NewSource(1))); err == nil {
		t.Fatal("expected address reuse to be rejected")
	}
	// Distinct addresses succeed.
	qiTx, err := qiwallet.BuildQiTx(big.NewInt(1337), testLocation, sel, []common.Address{qiAddress(9)}, []common.Address{qiAddress(10)}, mrand.New(mrand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(qiTx.TxIn) != 1 || len(qiTx.TxOut) != 2 {
		t.Fatalf("unexpected tx shape: %d in, %d out", len(qiTx.TxIn), len(qiTx.TxOut))
	}
}

// buildUnsignedTx constructs an n-input QiTx with the given keys.
func buildUnsignedTx(t *testing.T, keys []*btcec.PrivateKey) *types.QiTx {
	t.Helper()
	txIns := make([]types.TxIn, len(keys))
	for i, key := range keys {
		var prevHash common.Hash
		prevHash[0] = byte(i + 1)
		txIns[i] = types.TxIn{
			PreviousOutPoint: types.OutPoint{TxHash: prevHash, Index: 0},
			PubKey:           key.PubKey().SerializeUncompressed(),
		}
	}
	return &types.QiTx{
		ChainID: big.NewInt(1337),
		TxIn:    txIns,
		TxOut:   []types.TxOut{{Denomination: 5, Address: qiAddress(200).Bytes()}},
	}
}

// verifyLikeConsensus mirrors the verification in ValidateQiTxOutputsAndSignature.
func verifyLikeConsensus(t *testing.T, tx *types.Transaction, signer types.Signer) bool {
	t.Helper()
	pubKeys := make([]*btcec.PublicKey, 0, len(tx.TxIn()))
	for _, txIn := range tx.TxIn() {
		pubKey, err := btcec.ParsePubKey(txIn.PubKey)
		if err != nil {
			t.Fatal(err)
		}
		pubKeys = append(pubKeys, pubKey)
	}
	var finalKey *btcec.PublicKey
	if len(pubKeys) > 1 {
		aggKey, _, _, err := musig2.AggregateKeys(pubKeys, false)
		if err != nil {
			t.Fatal(err)
		}
		finalKey = aggKey.FinalKey
	} else {
		finalKey = pubKeys[0]
	}
	digest := signer.Hash(tx)
	return tx.GetSchnorrSignature().Verify(digest[:], finalKey)
}

func TestSignQiTxSingleInput(t *testing.T) {
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := types.LatestSignerForChainID(big.NewInt(1337), testLocation)
	signed, err := qiwallet.SignQiTx(buildUnsignedTx(t, []*btcec.PrivateKey{key}), signer, []*btcec.PrivateKey{key})
	if err != nil {
		t.Fatal(err)
	}
	if !verifyLikeConsensus(t, signed, signer) {
		t.Fatal("single-input signature failed consensus-style verification")
	}
}

func TestSignQiTxMultiInputMuSig2(t *testing.T) {
	keys := make([]*btcec.PrivateKey, 3)
	for i := range keys {
		key, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		keys[i] = key
	}
	signer := types.LatestSignerForChainID(big.NewInt(1337), testLocation)
	signed, err := qiwallet.SignQiTx(buildUnsignedTx(t, keys), signer, keys)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyLikeConsensus(t, signed, signer) {
		t.Fatal("multi-input MuSig2 signature failed consensus-style verification")
	}
	// Wrong key order must be rejected up front (aggregation is order-sensitive).
	swapped := []*btcec.PrivateKey{keys[1], keys[0], keys[2]}
	if _, err := qiwallet.SignQiTx(buildUnsignedTx(t, keys), signer, swapped); err == nil {
		t.Fatal("expected key/input mismatch to be rejected")
	}
}

func TestGrindQiAddress(t *testing.T) {
	key, addr, attempts, err := qiwallet.GrindQiAddress(testLocation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key == nil || attempts < 1 {
		t.Fatal("invalid grind result")
	}
	if !qiwallet.InQiScope(addr, testLocation) {
		t.Fatalf("ground address %s not in Qi scope for %v", addr, testLocation)
	}
	if got := qiwallet.QiAddressFromKey(key.PubKey(), testLocation); !got.Equal(addr) {
		t.Fatal("address does not round-trip from key")
	}
}
