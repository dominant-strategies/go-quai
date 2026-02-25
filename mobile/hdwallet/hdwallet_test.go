package hdwallet

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
)

const testPhrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func findIndexForLedgerScope(t *testing.T, w *HDWallet, account uint32, change bool, wantQi bool) uint32 {
	t.Helper()

	changeBit := uint32(0)
	if change {
		changeBit = 1
	}
	branchNode, err := w.root.DerivePath(fmt.Sprintf("%d'/%d", account, changeBit))
	if err != nil {
		t.Fatalf("failed to derive branch node: %v", err)
	}

	for i := uint32(0); i < 4096; i++ {
		child, err := branchNode.DeriveChild(i)
		if err != nil {
			continue
		}
		addrBytes, err := child.AddressBytes()
		if err != nil {
			continue
		}
		if IsQiAddress(addrBytes) == wantQi {
			return i
		}
	}
	t.Fatalf("failed to find index for ledger scope (wantQi=%v)", wantQi)
	return 0
}

func TestNewHDWallet(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}
	if w.CoinType() != CoinTypeQuai {
		t.Fatalf("expected coin type %d, got %d", CoinTypeQuai, w.CoinType())
	}
	if w.Phrase() != testPhrase {
		t.Fatal("phrase mismatch")
	}
}

func TestNewHDWallet_InvalidCoinType(t *testing.T) {
	_, err := NewHDWalletFromPhrase(testPhrase, "", 999)
	if err == nil {
		t.Fatal("expected error for invalid coin type")
	}
}

func TestNewRandomHDWallet(t *testing.T) {
	w, err := NewRandomHDWallet(CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}
	if w.Phrase() == "" {
		t.Fatal("random wallet should have a phrase")
	}
	if !IsValidMnemonic(w.Phrase()) {
		t.Fatal("random wallet phrase should be valid")
	}
}

func TestDeriveAddress_Quai_Cyprus1(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}

	zone := common.Location{0, 0} // Cyprus1
	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	if info.IsQi {
		t.Error("should be a Quai address, not Qi")
	}
	if info.Account != 0 {
		t.Errorf("expected account 0, got %d", info.Account)
	}
	if len(info.Address) == 0 {
		t.Error("address should not be empty")
	}
	if len(info.PubKey) == 0 {
		t.Error("pubkey should not be empty")
	}

	t.Logf("Quai Cyprus1 address: %s (index=%d)", info.Address, info.Index)
}

func TestDeriveAddress_Qi_Cyprus1(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQi)
	if err != nil {
		t.Fatal(err)
	}

	zone := common.Location{0, 0}
	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	if !info.IsQi {
		t.Error("should be a Qi address")
	}
	t.Logf("Qi Cyprus1 address: %s (index=%d)", info.Address, info.Index)
}

func TestDeriveAddress_DifferentZones(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}

	zones := []common.Location{
		{0, 0}, // Cyprus1
		{0, 1}, // Cyprus2
		{1, 0}, // Paxos1
	}

	for _, zone := range zones {
		info, err := w.DeriveAddress(0, zone)
		if err != nil {
			t.Fatalf("failed to derive for zone %v: %v", zone, err)
		}
		t.Logf("Zone %v: %s (index=%d)", zone, info.Address, info.Index)
	}
}

func TestDeriveAddress_Deterministic(t *testing.T) {
	w1, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	w2, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)

	zone := common.Location{0, 0}

	info1, err := w1.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}
	info2, err := w2.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	if info1.Address != info2.Address {
		t.Fatalf("same phrase should produce same address:\n  w1: %s (index=%d)\n  w2: %s (index=%d)",
			info1.Address, info1.Index, info2.Address, info2.Index)
	}
	if info1.Index != info2.Index {
		t.Fatalf("same phrase should produce same index: %d vs %d", info1.Index, info2.Index)
	}
}

func TestDeriveAddress_SequentialCalls(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	zone := common.Location{0, 0}

	info1, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}
	info2, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	if info1.Address == info2.Address {
		t.Fatal("sequential calls should produce different addresses")
	}
	if info2.Index <= info1.Index {
		t.Fatalf("second index (%d) should be greater than first (%d)", info2.Index, info1.Index)
	}
	t.Logf("First: %s (index=%d), Second: %s (index=%d)", info1.Address, info1.Index, info2.Address, info2.Index)
}

func TestGetPrivateKeyForAddress(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	zone := common.Location{0, 0}

	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	privKey, err := w.GetPrivateKeyForAddress(info.Address)
	if err != nil {
		t.Fatal(err)
	}
	if privKey == nil {
		t.Fatal("private key should not be nil")
	}
}

func TestGetPrivateKeyForAddress_NotFound(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)

	_, err := w.GetPrivateKeyForAddress("0xdeadbeef")
	if err == nil {
		t.Fatal("expected error for unknown address")
	}
}

func TestGetAddressesForZone(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)

	zone00 := common.Location{0, 0}
	zone01 := common.Location{0, 1}

	// Derive 2 addresses in zone 0,0
	w.DeriveAddress(0, zone00)
	w.DeriveAddress(0, zone00)

	// Derive 1 in zone 0,1
	w.DeriveAddress(0, zone01)

	addrs00 := w.GetAddressesForZone(zone00)
	if len(addrs00) != 2 {
		t.Fatalf("expected 2 addresses in zone 0,0, got %d", len(addrs00))
	}

	addrs01 := w.GetAddressesForZone(zone01)
	if len(addrs01) != 1 {
		t.Fatalf("expected 1 address in zone 0,1, got %d", len(addrs01))
	}
}

func TestDeriveAddressAtIndex(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)

	index := findIndexForLedgerScope(t, w, 0, false, false)
	info, err := w.DeriveAddressAtIndex(0, false, index)
	if err != nil {
		t.Fatal(err)
	}
	if info.Index != index {
		t.Fatalf("expected index %d, got %d", index, info.Index)
	}
	if info.Account != 0 {
		t.Fatalf("expected account 0, got %d", info.Account)
	}
	if info.Change {
		t.Fatal("expected external (non-change) branch address")
	}
	t.Logf("Index 5 address: %s, zone: %v, isQi: %v", info.Address, info.Zone, info.IsQi)
}

func TestDeriveAddressAtIndex_RejectsWrongLedgerForWalletCoinType(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)

	qiIndex := findIndexForLedgerScope(t, w, 0, false, true)
	if _, err := w.DeriveAddressAtIndex(0, false, qiIndex); err == nil {
		t.Fatalf("expected error deriving Qi-ledger address for Quai wallet at index %d", qiIndex)
	}
}

func TestGetPrivateKeyForAddress_ChangeBranch(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)

	index := findIndexForLedgerScope(t, w, 0, true, false)
	info, err := w.DeriveAddressAtIndex(0, true, index)
	if err != nil {
		t.Fatal(err)
	}

	gotPriv, err := w.GetPrivateKeyForAddress(info.Address)
	if err != nil {
		t.Fatal(err)
	}
	wantNode, err := w.root.DerivePath(fmt.Sprintf("0'/1/%d", index))
	if err != nil {
		t.Fatal(err)
	}
	wantPriv, err := wantNode.PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(crypto.FromECDSA(gotPriv), crypto.FromECDSA(wantPriv)) {
		t.Fatal("change-branch private key lookup returned the wrong key")
	}
}

func TestGetPrivateKeyForAddress_MixedCaseLookup(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	zone := common.Location{0, 0}

	info, err := w.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}

	mixed := common.HexToAddressBytes(info.Address).Hex()
	if mixed == info.Address {
		for i := 2; i < len(mixed); i++ {
			if mixed[i] >= 'a' && mixed[i] <= 'f' {
				mixed = mixed[:i] + strings.ToUpper(string(mixed[i])) + mixed[i+1:]
				break
			}
		}
	}
	if _, err := w.GetPrivateKeyForAddress(mixed); err != nil {
		t.Fatalf("mixed-case lookup should succeed: %v", err)
	}
	if _, err := w.GetAddressInfo(mixed); err != nil {
		t.Fatalf("mixed-case address info lookup should succeed: %v", err)
	}
}

func TestAddresses(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	zone := common.Location{0, 0}

	if len(w.Addresses()) != 0 {
		t.Fatal("should start with no addresses")
	}

	w.DeriveAddress(0, zone)
	w.DeriveAddress(0, zone)

	if len(w.Addresses()) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(w.Addresses()))
	}
}

func TestBenchmark100Addresses(t *testing.T) {
	const count = 100
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	if err != nil {
		t.Fatal(err)
	}

	zone := common.Location{0, 0}
	start := time.Now()
	for i := 0; i < count; i++ {
		_, err := w.DeriveAddress(0, zone)
		if err != nil {
			t.Fatalf("failed at address %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	perAddr := elapsed / count

	t.Logf("Derived %d addresses in %s (%.2f ms/addr)", count, elapsed, float64(perAddr.Microseconds())/1000.0)
}
