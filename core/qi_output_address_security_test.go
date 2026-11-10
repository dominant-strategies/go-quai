package core

import (
	"bytes"
	"crypto/ecdsa"
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

var qiOutputTestLocation = common.Location{0, 0}

// qiOutputTestChain returns the same prime terminus header for every lookup.
type qiOutputTestChain struct {
	ChainContext
	primeTerminus *types.WorkObject
}

func (c qiOutputTestChain) GetHeaderByHash(common.Hash) *types.WorkObject { return c.primeTerminus }

// qiKeyInCyprus1 returns a key whose address is a Qi address in zone [0,0].
func qiKeyInCyprus1(t *testing.T) (*ecdsa.PrivateKey, common.Address) {
	t.Helper()
	for i := 0; i < 100000; i++ {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		address := crypto.PubkeyToAddress(key.PublicKey, qiOutputTestLocation)
		if address.IsInQiLedgerScope() && address.Location().Equal(qiOutputTestLocation) {
			return key, address
		}
	}
	t.Fatal("no Qi key found for zone [0,0]")
	return nil, common.Address{}
}

// shortConversionAddress is 19 bytes and starts with the Cyprus1 prefix. When
// it is left-padded to 20 bytes, it is an internal Quai address in zone [0,0],
// so validation sends it down the Qi to Quai conversion branch.
func shortConversionAddress() []byte {
	address := make([]byte, common.AddressLength-1)
	for i := 1; i < len(address); i++ {
		address[i] = byte(i)
	}
	return address
}

// newShortAddressConversionTx returns a Qi to Quai conversion that spends one
// UTXO and pays to a 19 byte output address.
func newShortAddressConversionTx(key *ecdsa.PrivateKey, refund common.Address, input types.OutPoint, outputAddress []byte) *types.Transaction {
	data := append([]byte{0x00, 0x00}, refund.Bytes()...) // 2 byte slip + Qi refund address
	return types.NewTx(&types.QiTx{
		ChainID:   big.NewInt(1),
		TxIn:      types.TxIns{{PreviousOutPoint: input, PubKey: crypto.FromECDSAPub(&key.PublicKey)}},
		TxOut:     types.TxOuts{*types.NewTxOut(1, outputAddress, big.NewInt(0))},
		Signature: new(schnorr.Signature),
		Data:      data,
	})
}

func newQiOutputTestHeader() *types.WorkObject {
	wh := newTestWorkObjectHeader(params.KawPowForkBlock + params.KawPowTransitionPeriod + 1)
	bodyHeader := types.EmptyHeader()
	bodyHeader.SetGasLimit(30_000_000)
	bodyHeader.SetBaseFee(big.NewInt(1))
	body := types.EmptyWorkObjectBody()
	body.SetHeader(bodyHeader)
	return types.NewWorkObject(wh, body, nil)
}

// C3 crash: the Qi gas calculation converts every output address to [20]byte.
// A 19 byte address panics there. The worker calls this for every pending Qi
// transaction when it fills a block template.
func TestCalculateBlockQiTxGasPanicsOnShortOutputAddress(t *testing.T) {
	key, refund := qiKeyInCyprus1(t)
	tx := newShortAddressConversionTx(key, refund, types.OutPoint{TxHash: common.Hash{1}, Index: 0}, shortConversionAddress())

	address := common.BytesToAddress(tx.TxOut()[0].Address, qiOutputTestLocation)
	if !address.Location().Equal(qiOutputTestLocation) || !address.IsInQuaiLedgerScope() {
		t.Fatalf("test setup: short address must look like an internal Quai address, got %s", address)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected CalculateBlockQiTxGas to panic on a short output address")
		}
	}()
	types.CalculateBlockQiTxGas(tx, 1, qiOutputTestLocation)
}

// C3 fix, pool: the pool must reject any output address that is not 20 bytes,
// so the worker never sees such a transaction.
func TestValidateQiTxOutputsRejectsOutputAddressLength(t *testing.T) {
	key, refund := qiKeyInCyprus1(t)
	chain := qiOutputTestChain{primeTerminus: newQiOutputTestHeader()}
	signer := types.NewSigner(big.NewInt(1), qiOutputTestLocation)

	longAddress := append(refund.Bytes(), 0x01)
	for name, outputAddress := range map[string][]byte{"short": shortConversionAddress(), "long": longAddress} {
		tx := newShortAddressConversionTx(key, refund, types.OutPoint{TxHash: common.Hash{1}, Index: 0}, outputAddress)
		_, err := ValidateQiTxOutputsAndSignature(tx, chain, big.NewInt(1_000_000), newQiOutputTestHeader(), signer, qiOutputTestLocation, *big.NewInt(1), 1, 1_000_000, 1_000_000)
		if err == nil || !strings.Contains(err.Error(), "address length") {
			t.Fatalf("%s address: got %v, want an address length error", name, err)
		}
	}

	// A 20 byte address must pass the length check.
	tx := newShortAddressConversionTx(key, refund, types.OutPoint{TxHash: common.Hash{1}, Index: 0}, refund.Bytes())
	_, err := ValidateQiTxOutputsAndSignature(tx, chain, big.NewInt(1_000_000), newQiOutputTestHeader(), signer, qiOutputTestLocation, *big.NewInt(1), 1, 1_000_000, 1_000_000)
	if err != nil && strings.Contains(err.Error(), "address length") {
		t.Fatalf("20 byte address rejected: %v", err)
	}
}

// C3 fix, consensus: block processing must reject a short output address with
// an error, before the gas calculation that panics.
func TestProcessQiTxRejectsShortOutputAddress(t *testing.T) {
	key, refund := qiKeyInCyprus1(t)
	db := rawdb.NewMemoryDatabase(log.Global)
	input := types.OutPoint{TxHash: common.Hash{1}, Index: 0}
	if err := rawdb.CreateUTXO(db, input.TxHash, input.Index, types.NewUtxoEntry(types.NewTxOut(5, refund.Bytes(), nil))); err != nil {
		t.Fatal(err)
	}
	tx := newShortAddressConversionTx(key, refund, input, shortConversionAddress())

	currentHeader := newQiOutputTestHeader()
	chain := qiOutputTestChain{primeTerminus: currentHeader}
	usedGas := uint64(0)
	etxRLimit, etxPLimit := uint64(1_000_000), uint64(1_000_000)
	_, _, _, err, _ := ProcessQiTx(tx, chain, false, true, currentHeader, db.NewBatch(), db, new(types.GasPool).AddGas(30_000_000), &usedGas,
		types.NewSigner(big.NewInt(1), qiOutputTestLocation), qiOutputTestLocation, *big.NewInt(1), 1, &etxRLimit, &etxPLimit,
		&UtxosCreatedDeleted{}, big.NewInt(0), big.NewInt(0), false)
	if err == nil || !strings.Contains(err.Error(), "address length") {
		t.Fatalf("got %v, want an address length error", err)
	}
}

// A miner can put a Qi transaction directly in a block without pool admission.
// Keep old blocks replayable, then require exactly 20 address bytes at the fork.
func TestProcessQiTxLongOutputAddressFork(t *testing.T) {
	key, owner := qiKeyInCyprus1(t)
	_, recipient := qiKeyInCyprus1(t)
	longAddress := append([]byte{0x00, 0x80}, recipient.Bytes()...)
	if !common.BytesToAddress(longAddress, qiOutputTestLocation).Equal(recipient) {
		t.Fatal("test setup: long address must resolve to the recipient")
	}
	if common.AddressBytes(longAddress) == recipient.Bytes20() {
		t.Fatal("test setup: first 20 bytes must differ from the recipient")
	}

	for _, tc := range []struct {
		name       string
		primeBlock uint64
		address    []byte
		wantError  bool
	}{
		{"before fork", params.PolarisForkBlock - 1, longAddress, false},
		{"at fork", params.PolarisForkBlock, longAddress, true},
		{"valid at fork", params.PolarisForkBlock, recipient.Bytes(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := rawdb.NewMemoryDatabase(log.Global)
			input := types.OutPoint{TxHash: common.Hash{1}, Index: 0}
			if err := rawdb.CreateUTXO(db, input.TxHash, input.Index, types.NewUtxoEntry(types.NewTxOut(5, owner.Bytes(), nil))); err != nil {
				t.Fatal(err)
			}
			qiTx := &types.QiTx{
				ChainID:   big.NewInt(1),
				TxIn:      types.TxIns{{PreviousOutPoint: input, PubKey: crypto.FromECDSAPub(&key.PublicKey)}},
				TxOut:     types.TxOuts{*types.NewTxOut(1, tc.address, big.NewInt(0))},
				Signature: new(schnorr.Signature),
			}
			signer := types.NewSigner(big.NewInt(1), qiOutputTestLocation)
			digest, err := signer.Hash(types.NewTx(qiTx))
			if err != nil {
				t.Fatal(err)
			}
			privateKey, _ := btcec.PrivKeyFromBytes(crypto.FromECDSA(key))
			qiTx.Signature, err = schnorr.Sign(privateKey, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			tx := types.NewTx(qiTx)
			encoded, err := tx.ProtoEncode()
			if err != nil {
				t.Fatal(err)
			}
			decoded := new(types.Transaction)
			if err := decoded.ProtoDecode(encoded, qiOutputTestLocation); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded.TxOut()[0].Address, tc.address) {
				t.Fatalf("block transaction changed output address length: got %d, want %d", len(decoded.TxOut()[0].Address), len(tc.address))
			}
			tx = decoded
			currentHeader := newQiOutputTestHeader()
			currentHeader.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(tc.primeBlock))
			currentHeader.Header().SetBaseFee(big.NewInt(0))
			currentHeader.Header().SetExchangeRate(big.NewInt(1))
			chain := qiOutputTestChain{primeTerminus: currentHeader}
			batch := db.NewBatch()
			batch.SetPending(true)
			usedGas := uint64(0)
			etxRLimit, etxPLimit := uint64(1_000_000), uint64(1_000_000)
			changes := &UtxosCreatedDeleted{
				AddressOutpointsToAddMap:    make(map[[20]byte][]*types.OutpointAndDenomination),
				AddressOutpointsToRemoveMap: make(map[[20]byte][]*types.OutPoint),
			}
			_, _, _, err, _ = ProcessQiTx(tx, chain, true, false, currentHeader, batch, db, new(types.GasPool).AddGas(30_000_000), &usedGas,
				signer, qiOutputTestLocation, *big.NewInt(1), 1, &etxRLimit, &etxPLimit,
				changes, big.NewInt(0), big.NewInt(0), true)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "address length") {
					t.Fatalf("got %v, want an address length error", err)
				}
				if utxo := rawdb.GetUTXOWithBatch(db, batch, tx.Hash(), 0); utxo != nil {
					t.Fatalf("rejected output created a UTXO: %x", utxo.Address)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid historical output rejected: %v", err)
			}
			utxo := rawdb.GetUTXOWithBatch(db, batch, tx.Hash(), 0)
			if utxo == nil || !bytes.Equal(utxo.Address, tc.address) {
				t.Fatalf("UTXO address: got %v, want %x", utxo, tc.address)
			}
			if len(tc.address) > common.AddressLength {
				if len(changes.AddressOutpointsToAddMap[common.AddressBytes(tc.address)]) != 1 {
					t.Fatal("oversized address was not indexed by its first 20 bytes")
				}
			}
		})
	}
}
