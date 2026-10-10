package hdwallet

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
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

func TestDeriveAddressAtIndex_EthereumPath(t *testing.T) {
	w, err := NewHDWalletFromPhrase(testPhrase, "", CoinTypeEthereum)
	if err != nil {
		t.Fatal(err)
	}

	info, err := w.DeriveAddressAtIndex(0, false, 1)
	if err != nil {
		t.Fatal(err)
	}

	if info.Address != "0x6fac4d18c912343bf86fa7049364dd4e424ab9c0" {
		t.Fatalf("unexpected Base address: %s", info.Address)
	}
	if len(info.Zone) < 2 || info.Zone[0] != 0 || info.Zone[1] != 0 {
		t.Fatalf("expected zeroed EVM zone metadata, got %v", info.Zone)
	}
	if info.IsQi {
		t.Fatal("ethereum/base address must not be marked as qi")
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

func TestBenchmarkQiPaymentChannelAddressDerivation(t *testing.T) {
	const count = 100

	// Create two random wallets (Alice and Bob) with independent payment codes.
	alice, err := NewRandomHDWallet(CoinTypeQi)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := NewRandomHDWallet(CoinTypeQi)
	if err != nil {
		t.Fatal(err)
	}

	aliceCode, err := alice.GetQiPaymentCode(0)
	if err != nil {
		t.Fatal(err)
	}
	bobCode, err := bob.GetQiPaymentCode(0)
	if err != nil {
		t.Fatal(err)
	}

	if !ValidateQiPaymentCode(aliceCode) {
		t.Fatal("alice payment code is invalid")
	}
	if !ValidateQiPaymentCode(bobCode) {
		t.Fatal("bob payment code is invalid")
	}

	zone := common.Location{0, 0} // Cyprus1

	// Alice derives 24 send addresses (addresses she will send TO Bob).
	// These must match what Bob derives as receive addresses.
	t.Run("AliceSendToBob", func(t *testing.T) {
		start := time.Now()
		var totalAttempts uint32
		nextIndex := uint32(0)
		for i := 0; i < count; i++ {
			info, err := alice.DeriveQiPaymentChannelSendAddress(bobCode, zone, 0, nextIndex)
			if err != nil {
				t.Fatalf("alice send address %d: %v", i, err)
			}
			totalAttempts += info.DerivationAttempts
			nextIndex = info.Index + 1
		}
		elapsed := time.Since(start)
		perAddr := elapsed / count
		t.Logf("Alice->Bob SEND: %d addrs in %s (%.2f ms/addr, avg %.1f attempts/addr)",
			count, elapsed, float64(perAddr.Microseconds())/1000.0, float64(totalAttempts)/float64(count))
	})

	// Bob derives 24 receive addresses (addresses he receives AT from Alice).
	t.Run("BobReceiveFromAlice", func(t *testing.T) {
		start := time.Now()
		nextIndex := uint32(0)
		for i := 0; i < count; i++ {
			info, err := bob.DeriveQiPaymentChannelReceiveAddress(aliceCode, zone, 0, nextIndex)
			if err != nil {
				t.Fatalf("bob receive address %d: %v", i, err)
			}
			nextIndex = info.Index + 1
		}
		elapsed := time.Since(start)
		perAddr := elapsed / count
		t.Logf("Bob<-Alice RECV: %d addrs in %s (%.2f ms/addr)",
			count, elapsed, float64(perAddr.Microseconds())/1000.0)
	})

	// Verify determinism: Alice's send addresses must equal Bob's receive addresses.
	t.Run("Determinism", func(t *testing.T) {
		alice2, _ := NewHDWalletFromPhrase(alice.Phrase(), "", CoinTypeQi)
		bob2, _ := NewHDWalletFromPhrase(bob.Phrase(), "", CoinTypeQi)

		sendIdx := uint32(0)
		recvIdx := uint32(0)
		for i := 0; i < count; i++ {
			sendInfo, err := alice2.DeriveQiPaymentChannelSendAddress(bobCode, zone, 0, sendIdx)
			if err != nil {
				t.Fatalf("determinism send %d: %v", i, err)
			}
			recvInfo, err := bob2.DeriveQiPaymentChannelReceiveAddress(aliceCode, zone, 0, recvIdx)
			if err != nil {
				t.Fatalf("determinism recv %d: %v", i, err)
			}
			if sendInfo.Address != recvInfo.Address {
				t.Fatalf("address mismatch at channel index %d:\n  send: %s (idx=%d)\n  recv: %s (idx=%d)",
					i, sendInfo.Address, sendInfo.Index, recvInfo.Address, recvInfo.Index)
			}
			sendIdx = sendInfo.Index + 1
			recvIdx = recvInfo.Index + 1
		}
		t.Logf("All %d send/receive address pairs match", count)
	})

	// Benchmark the reverse direction: Bob sends to Alice.
	t.Run("BobSendToAlice", func(t *testing.T) {
		start := time.Now()
		var totalAttempts uint32
		nextIndex := uint32(0)
		for i := 0; i < count; i++ {
			info, err := bob.DeriveQiPaymentChannelSendAddress(aliceCode, zone, 0, nextIndex)
			if err != nil {
				t.Fatalf("bob send address %d: %v", i, err)
			}
			totalAttempts += info.DerivationAttempts
			nextIndex = info.Index + 1
		}
		elapsed := time.Since(start)
		perAddr := elapsed / count
		t.Logf("Bob->Alice SEND: %d addrs in %s (%.2f ms/addr, avg %.1f attempts/addr)",
			count, elapsed, float64(perAddr.Microseconds())/1000.0, float64(totalAttempts)/float64(count))
	})
}

// TestBenchmarkPerAttemptCost isolates the per-attempt cost of BIP44 vs BIP47
// derivation by running a fixed number of raw derivation iterations (no zone
// filtering) so the attempt count is identical for both paths.
func TestBenchmarkPerAttemptCost(t *testing.T) {
	const iterations = 2000

	alice, err := NewRandomHDWallet(CoinTypeQi)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := NewRandomHDWallet(CoinTypeQi)
	if err != nil {
		t.Fatal(err)
	}
	bobCode, err := bob.GetQiPaymentCode(0)
	if err != nil {
		t.Fatal(err)
	}

	// --- BIP44 per-attempt cost ---
	t.Run("BIP44_DeriveChild+AddressBytes", func(t *testing.T) {
		branchNode, err := alice.root.DerivePath("0'/0")
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		for i := uint32(0); i < iterations; i++ {
			child, err := branchNode.DeriveChild(i)
			if err != nil {
				continue
			}
			_, _ = child.AddressBytes()
		}
		elapsed := time.Since(start)
		t.Logf("BIP44 %d iterations in %s (%.3f ms/iter)",
			iterations, elapsed, float64(elapsed.Microseconds())/float64(iterations)/1000.0)
	})

	// --- BIP47 per-attempt cost, broken into steps ---
	t.Run("BIP47_StepByStep", func(t *testing.T) {
		accountNode, err := alice.bip47AccountNode(0)
		if err != nil {
			t.Fatal(err)
		}
		counterparty, err := decodePaymentCode(bobCode)
		if err != nil {
			t.Fatal(err)
		}
		notificationPrivEcdsa, err := derivePrivateKeyAt(accountNode, 0)
		if err != nil {
			t.Fatal(err)
		}
		btcNotifPriv, _ := btcec.PrivKeyFromBytes(crypto.FromECDSA(notificationPrivEcdsa))

		var durChildDerive, durECPubKey, durECDH, durKeccak time.Duration

		for i := uint32(0); i < iterations; i++ {
			// Step 1: HD child derivation from counterparty root
			t1 := time.Now()
			receiverNode, err := derivePaymentCodeChild(counterparty.root, i)
			durChildDerive += time.Since(t1)
			if err != nil {
				continue
			}

			// Step 2: Extract EC public key
			t2 := time.Now()
			receiverPub, err := receiverNode.ECPubKey()
			durECPubKey += time.Since(t2)
			if err != nil {
				continue
			}

			// Step 3: ECDH + SHA256 + ScalarBaseMult + Point Add
			t3 := time.Now()
			derivedPub, err := derivePaymentPublicKeyFromPrivate(receiverPub, btcNotifPriv)
			durECDH += time.Since(t3)
			if err != nil {
				continue
			}

			// Step 4: Keccak256 address
			t4 := time.Now()
			_ = crypto.Keccak256(crypto.FromECDSAPub(derivedPub)[1:])[12:]
			durKeccak += time.Since(t4)
		}

		total := durChildDerive + durECPubKey + durECDH + durKeccak
		t.Logf("BIP47 %d iterations in %s (%.3f ms/iter)", iterations, total,
			float64(total.Microseconds())/float64(iterations)/1000.0)
		t.Logf("  HD child derive:    %s (%.3f ms/iter, %.1f%%)",
			durChildDerive, float64(durChildDerive.Microseconds())/float64(iterations)/1000.0,
			float64(durChildDerive)*100/float64(total))
		t.Logf("  ECPubKey extract:   %s (%.3f ms/iter, %.1f%%)",
			durECPubKey, float64(durECPubKey.Microseconds())/float64(iterations)/1000.0,
			float64(durECPubKey)*100/float64(total))
		t.Logf("  ECDH+SHA256+Scalar: %s (%.3f ms/iter, %.1f%%)",
			durECDH, float64(durECDH.Microseconds())/float64(iterations)/1000.0,
			float64(durECDH)*100/float64(total))
		t.Logf("  Keccak256 address:  %s (%.3f ms/iter, %.1f%%)",
			durKeccak, float64(durKeccak.Microseconds())/float64(iterations)/1000.0,
			float64(durKeccak)*100/float64(total))
	})
}
