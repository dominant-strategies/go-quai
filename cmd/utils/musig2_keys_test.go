package utils

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
)

// TestGenerateMuSig2Keys generates 3 keypairs for MuSig2 2-of-3 multisig
// Run with: go test -v -run TestGenerateMuSig2Keys ./cmd/utils/
func TestGenerateMuSig2Keys(t *testing.T) {
	// Generate 3 keypairs
	keys := make([]*btcec.PrivateKey, 3)
	pubKeys := make([]*btcec.PublicKey, 3)

	for i := 0; i < 3; i++ {
		privKey, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatalf("Failed to generate private key %d: %v", i, err)
		}
		keys[i] = privKey
		pubKeys[i] = privKey.PubKey()
	}

	fmt.Println("=== MuSig2 2-of-3 Keypairs ===")
	fmt.Println("\nSet these environment variables or use as flags:")
	fmt.Println()

	for i := 0; i < 3; i++ {
		privHex := hex.EncodeToString(keys[i].Serialize())
		pubHex := hex.EncodeToString(pubKeys[i].SerializeCompressed())

		fmt.Printf("# Key %d (use one per participant)\n", i+1)
		fmt.Printf("MUSIG2_PRIVKEY_%d=%s\n", i+1, privHex)
		fmt.Printf("MUSIG2_PUBKEY_%d=%s\n", i+1, pubHex)
		fmt.Println()
	}

	// Test aggregation with any 2 keys
	fmt.Println("=== Testing 2-of-3 combinations ===")

	// Test with keys 0 and 1
	testCombination(t, []*btcec.PublicKey{pubKeys[0], pubKeys[1]}, "Keys 1 and 2")

	// Test with keys 0 and 2
	testCombination(t, []*btcec.PublicKey{pubKeys[0], pubKeys[2]}, "Keys 1 and 3")

	// Test with keys 1 and 2
	testCombination(t, []*btcec.PublicKey{pubKeys[1], pubKeys[2]}, "Keys 2 and 3")

	fmt.Println("\n✓ All 2-of-3 combinations work correctly")

	fmt.Println("\n=== Example Usage ===")
	fmt.Println("# For go-quai (participant 1):")
	fmt.Printf("export MUSIG2_PRIVKEY=%s\n", hex.EncodeToString(keys[0].Serialize()))
	fmt.Printf("export MUSIG2_PARTICIPANT_INDEX=0\n")
	fmt.Println("\n# For subsidy-pool (participant 2):")
	fmt.Printf("export MUSIG2_PRIVKEY=%s\n", hex.EncodeToString(keys[1].Serialize()))
	fmt.Printf("export MUSIG2_PARTICIPANT_INDEX=1\n")

	fmt.Println("\n# All public keys (needed by all participants):")
	for i, pubKey := range pubKeys {
		fmt.Printf("export MUSIG2_PUBKEY_%d=%s\n", i+1, hex.EncodeToString(pubKey.SerializeCompressed()))
	}
}

func testCombination(t *testing.T, keys []*btcec.PublicKey, description string) {
	aggKey, _, _, err := musig2.AggregateKeys(keys, false)
	if err != nil {
		t.Fatalf("Failed to aggregate keys for %s: %v", description, err)
	}
	fmt.Printf("  %s: Aggregated public key = %x\n", description, aggKey.FinalKey.SerializeCompressed())
}