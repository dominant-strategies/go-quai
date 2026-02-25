package hdwallet

import (
	"encoding/json"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
)

func TestSerializeDeserialize(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	zone := common.Location{0, 0}

	// Derive some addresses
	info1, _ := w.DeriveAddress(0, zone)
	info2, _ := w.DeriveAddress(0, zone)

	// Serialize
	data, err := w.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	// Verify it's valid JSON
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("serialized wallet is not valid JSON: %v", err)
	}

	// Check version
	if raw["version"].(float64) != 1 {
		t.Fatal("expected version 1")
	}

	// Deserialize
	w2, err := DeserializeWallet(data)
	if err != nil {
		t.Fatal(err)
	}

	// Check phrase
	if w2.Phrase() != testPhrase {
		t.Fatal("phrase mismatch after deserialization")
	}
	if w2.CoinType() != CoinTypeQuai {
		t.Fatal("coin type mismatch")
	}

	// Check addresses are preserved
	addrs := w2.Addresses()
	if len(addrs) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(addrs))
	}

	// Verify address info is accessible
	restored1, err := w2.GetAddressInfo(info1.Address)
	if err != nil {
		t.Fatalf("failed to get address info: %v", err)
	}
	if restored1.Index != info1.Index {
		t.Fatalf("index mismatch: got %d, want %d", restored1.Index, info1.Index)
	}

	restored2, err := w2.GetAddressInfo(info2.Address)
	if err != nil {
		t.Fatalf("failed to get address info: %v", err)
	}
	if restored2.Index != info2.Index {
		t.Fatalf("index mismatch: got %d, want %d", restored2.Index, info2.Index)
	}
}

func TestDeserialize_ContinuesFromLastIndex(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "", CoinTypeQuai)
	zone := common.Location{0, 0}

	// Derive two addresses
	info1, _ := w.DeriveAddress(0, zone)
	info2, _ := w.DeriveAddress(0, zone)

	// Serialize and restore
	data, _ := w.Serialize()
	w2, _ := DeserializeWallet(data)

	// Derive the next address — should NOT repeat
	info3, err := w2.DeriveAddress(0, zone)
	if err != nil {
		t.Fatal(err)
	}
	if info3.Address == info1.Address || info3.Address == info2.Address {
		t.Fatal("deserialized wallet should continue from last index, not repeat")
	}
	if info3.Index <= info2.Index {
		t.Fatalf("new index (%d) should be greater than previous (%d)", info3.Index, info2.Index)
	}
	t.Logf("After restore: info1.Index=%d, info2.Index=%d, info3.Index=%d", info1.Index, info2.Index, info3.Index)
}

func TestDeserialize_InvalidVersion(t *testing.T) {
	data := []byte(`{"version":99,"phrase":"abandon","coinType":994,"addresses":[]}`)
	_, err := DeserializeWallet(data)
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestDeserialize_InvalidJSON(t *testing.T) {
	_, err := DeserializeWallet([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSerialize_WithPassword(t *testing.T) {
	w, _ := NewHDWalletFromPhrase(testPhrase, "mypass", CoinTypeQuai)
	zone := common.Location{0, 0}
	w.DeriveAddress(0, zone)

	data, _ := w.Serialize()
	w2, err := DeserializeWallet(data)
	if err != nil {
		t.Fatal(err)
	}

	// Derive the same address from the restored wallet should use the password
	// Verify the restored wallet can derive private keys
	for _, info := range w2.Addresses() {
		_, err := w2.GetPrivateKeyForAddress(info.Address)
		if err != nil {
			t.Fatalf("failed to get private key from restored wallet: %v", err)
		}
	}
}
