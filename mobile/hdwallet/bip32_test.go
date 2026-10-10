package hdwallet

import (
	"encoding/hex"
	"testing"

	"github.com/dominant-strategies/go-quai/crypto"
)

// helper to get private key hex from node
func privKeyHex(t *testing.T, n *HDNode) string {
	t.Helper()
	priv, err := n.PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(crypto.FromECDSA(priv))
}

// BIP32 Test Vector 1 from https://github.com/bitcoin/bips/blob/master/bip-0032.mediawiki
func TestBIP32_Vector1_MasterNode(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	node, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	expectedPriv := "e8f32e723decf4051aefac8e2c93c9c5b214313817cdb01a1494b917c8436b35"
	gotPriv := privKeyHex(t, node)
	if gotPriv != expectedPriv {
		t.Fatalf("master private key mismatch:\n  got:  %s\n  want: %s", gotPriv, expectedPriv)
	}

	expectedCC := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"
	gotCC := hex.EncodeToString(node.ChainCode())
	if gotCC != expectedCC {
		t.Fatalf("master chain code mismatch:\n  got:  %s\n  want: %s", gotCC, expectedCC)
	}

	if node.Depth() != 0 {
		t.Fatalf("master depth should be 0, got %d", node.Depth())
	}
	if node.Path != "m" {
		t.Fatalf("master path should be 'm', got %q", node.Path)
	}
}

// BIP32 TV1: m/0'
func TestBIP32_Vector1_Hardened0(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	master, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	child, err := master.DeriveChild(HardenedBit + 0)
	if err != nil {
		t.Fatal(err)
	}

	expectedPriv := "edb2e14f9ee77d26dd93b4ecede8d16ed408ce149b6cd80b0715a2d911a0afea"
	gotPriv := privKeyHex(t, child)
	if gotPriv != expectedPriv {
		t.Fatalf("m/0' private key mismatch:\n  got:  %s\n  want: %s", gotPriv, expectedPriv)
	}

	expectedCC := "47fdacbd0f1097043b78c63c20c34ef4ed9a111d980047ad16282c7ae6236141"
	gotCC := hex.EncodeToString(child.ChainCode())
	if gotCC != expectedCC {
		t.Fatalf("m/0' chain code mismatch:\n  got:  %s\n  want: %s", gotCC, expectedCC)
	}

	if child.Depth() != 1 {
		t.Fatalf("expected depth 1, got %d", child.Depth())
	}
	if child.Path != "m/0'" {
		t.Fatalf("expected path m/0', got %q", child.Path)
	}
}

// BIP32 TV1: m/0'/1
func TestBIP32_Vector1_DerivePath(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	master, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	child, err := master.DerivePath("m/0'/1")
	if err != nil {
		t.Fatal(err)
	}

	expectedPriv := "3c6cb8d0f6a264c91ea8b5030fadaa8e538b020f0a387421a12de9319dc93368"
	gotPriv := privKeyHex(t, child)
	if gotPriv != expectedPriv {
		t.Fatalf("m/0'/1 private key mismatch:\n  got:  %s\n  want: %s", gotPriv, expectedPriv)
	}

	if child.Depth() != 2 {
		t.Fatalf("expected depth 2, got %d", child.Depth())
	}
	if child.Path != "m/0'/1" {
		t.Fatalf("expected path m/0'/1, got %q", child.Path)
	}
}

// BIP32 TV1: Full path m/0'/1/2'/2/1000000000
func TestBIP32_Vector1_FullPath(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	master, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	child, err := master.DerivePath("m/0'/1/2'/2/1000000000")
	if err != nil {
		t.Fatal(err)
	}

	expectedPriv := "471b76e389e528d6de6d816857e012c5455051cad6660850e58372a6c3e6e7c8"
	gotPriv := privKeyHex(t, child)
	if gotPriv != expectedPriv {
		t.Fatalf("full path private key mismatch:\n  got:  %s\n  want: %s", gotPriv, expectedPriv)
	}

	if child.Depth() != 5 {
		t.Fatalf("expected depth 5, got %d", child.Depth())
	}
}

// BIP32 Test Vector 2
func TestBIP32_Vector2_Master(t *testing.T) {
	seed, _ := hex.DecodeString("fffcf9f6f3f0edeae7e4e1dedbd8d5d2cfccc9c6c3c0bdbab7b4b1aeaba8a5a29f9c999693908d8a8784817e7b7875726f6c696663605d5a5754514e4b484542")
	master, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	expectedPriv := "4b03d6fc340455b363f51020ad3ecca4f0850280cf436c70c727923f6db46c3e"
	gotPriv := privKeyHex(t, master)
	if gotPriv != expectedPriv {
		t.Fatalf("TV2 master private key mismatch:\n  got:  %s\n  want: %s", gotPriv, expectedPriv)
	}
}

// BIP32 TV2: m/0/2147483647'/1/2147483646'/2
func TestBIP32_Vector2_FullPath(t *testing.T) {
	seed, _ := hex.DecodeString("fffcf9f6f3f0edeae7e4e1dedbd8d5d2cfccc9c6c3c0bdbab7b4b1aeaba8a5a29f9c999693908d8a8784817e7b7875726f6c696663605d5a5754514e4b484542")
	master, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	child, err := master.DerivePath("m/0/2147483647'/1/2147483646'/2")
	if err != nil {
		t.Fatal(err)
	}

	expectedPriv := "bb7d39bdb83ecf58f2fd82b6d918341cbef428661ef01ab97c28a4842125ac23"
	gotPriv := privKeyHex(t, child)
	if gotPriv != expectedPriv {
		t.Fatalf("TV2 full path private key mismatch:\n  got:  %s\n  want: %s", gotPriv, expectedPriv)
	}

	if child.Depth() != 5 {
		t.Fatalf("expected depth 5, got %d", child.Depth())
	}
}

func TestNeuter(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	master, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	neutered, err := master.Neuter()
	if err != nil {
		t.Fatal(err)
	}
	if neutered.IsPrivate() {
		t.Fatal("neutered node should not be private")
	}

	masterPub, _ := master.PublicKeyBytes()
	neuteredPub, _ := neutered.PublicKeyBytes()
	if hex.EncodeToString(neuteredPub) != hex.EncodeToString(masterPub) {
		t.Fatal("neutered node should have same public key")
	}
	if hex.EncodeToString(neutered.ChainCode()) != hex.EncodeToString(master.ChainCode()) {
		t.Fatal("neutered node should have same chain code")
	}
}

func TestAddressBytes(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	master, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	addr, err := master.AddressBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(addr) != 20 {
		t.Fatalf("expected 20-byte address, got %d", len(addr))
	}
}

func TestInvalidSeed(t *testing.T) {
	_, err := NewMasterNode([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for short seed")
	}
}

func TestInvalidPath(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	master, err := NewMasterNode(seed)
	if err != nil {
		t.Fatal(err)
	}

	_, err = master.DerivePath("m/abc")
	if err == nil {
		t.Fatal("expected error for invalid path component")
	}
}
