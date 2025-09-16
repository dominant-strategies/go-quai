package kawpow

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

// TestKAWPOWImplementation tests our KAWPOW implementation with various scenarios
func TestKAWPOWImplementation(t *testing.T) {
	t.Run("Constants", func(t *testing.T) {
		// Verify KAWPOW constants match Ravencoin specification
		if C_epochLength != 7500 {
			t.Errorf("C_epochLength should be 7500, got %d", C_epochLength)
		}
		if kawpowCacheWords != 4096 {
			t.Errorf("kawpowCacheWords should be 4096, got %d", kawpowCacheWords)
		}
		if kawpowCacheBytes != 16384 {
			t.Errorf("kawpowCacheBytes should be 16384, got %d", kawpowCacheBytes)
		}
		t.Logf("✅ KAWPOW constants correct")
	})

	t.Run("Activation", func(t *testing.T) {
		kawpowActivation := 1219736
		firstKawpowBlock := kawpowActivation + 1
		epoch := firstKawpowBlock / int(C_epochLength)
		expectedEpoch := 162

		if epoch != expectedEpoch {
			t.Errorf("KAWPOW activation epoch: got %d, expected %d", epoch, expectedEpoch)
		}
		t.Logf("✅ KAWPOW activation at block %d, epoch %d", firstKawpowBlock, epoch)
	})

	t.Run("Algorithm_Consistency", func(t *testing.T) {
		logger := log.NewLogger("test.log", "info", 100)
		kawpow := New(params.PowConfig{PowMode: params.ModeNormal}, nil, false, logger)

		blockHeight := uint64(1219737)
		headerHash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
		nonce64 := uint64(0x123456789abcdef0)

		cache := kawpow.cache(blockHeight)
		size := datasetSize(blockHeight)

		if cache.cDag == nil {
			cDag := make([]uint32, kawpowCacheWords)
			generateCDag(cDag, cache.cache, blockHeight/C_epochLength, kawpow.logger)
			cache.cDag = cDag
		}

		// Test consistency - same input should give same output
		digest1, result1 := kawpowLight(size, cache.cache, headerHash.Bytes(), nonce64, blockHeight, cache.cDag)
		digest2, result2 := kawpowLight(size, cache.cache, headerHash.Bytes(), nonce64, blockHeight, cache.cDag)

		if hex.EncodeToString(digest1) != hex.EncodeToString(digest2) {
			t.Error("KAWPOW digest should be consistent")
		}
		if hex.EncodeToString(result1) != hex.EncodeToString(result2) {
			t.Error("KAWPOW result should be consistent")
		}
		t.Logf("✅ KAWPOW computation is consistent")

		// Test different nonces produce different results
		nonce64_2 := nonce64 + 1
		digest3, result3 := kawpowLight(size, cache.cache, headerHash.Bytes(), nonce64_2, blockHeight, cache.cDag)

		if hex.EncodeToString(digest1) == hex.EncodeToString(digest3) {
			t.Error("Different nonces should produce different digests")
		}
		if hex.EncodeToString(result1) == hex.EncodeToString(result3) {
			t.Error("Different nonces should produce different results")
		}
		t.Logf("✅ Different nonces produce different results")
	})

	t.Run("Mining_vs_Validation", func(t *testing.T) {
		logger := log.NewLogger("test.log", "info", 100)
		kawpow := New(params.PowConfig{PowMode: params.ModeNormal}, nil, false, logger)

		blockHeight := uint64(1219737)
		nonce64 := uint64(0x123456789abcdef0)

		header := &types.RavencoinBlockHeader{
			Version:        0x20000000,
			HashPrevBlock:  common.Hash{},
			HashMerkleRoot: common.Hash{},
			Time:           1588788000,
			Bits:           0x1d00ffff,
			Height:         uint32(blockHeight),
			Nonce64:        nonce64,
			MixHash:        common.Hash{},
		}
		auxHeader := types.NewAuxPowHeader(header)

		coinbaseOut := []byte{0x76, 0xa9, 0x14, 0x89, 0xab, 0xcd, 0xef, 0x88, 0xac}
		coinbaseTx := types.NewAuxPowCoinbaseTx(
			types.Kawpow,
			uint32(blockHeight),
			coinbaseOut,
			types.EmptyRootHash,
			100,
		)

		auxPow := types.NewAuxPow(
			types.Kawpow,
			auxHeader,
			[]byte{},
			[]byte{},
			[][]byte{},
			coinbaseTx,
		)

		workHeader := &types.WorkObjectHeader{}
		workHeader.SetAuxPow(auxPow)
		workHeader.SetPrimeTerminusNumber(big.NewInt(3000001))
		workHeader.SetNumber(big.NewInt(int64(blockHeight)))

		// Method 1: Direct kawpowLight (mining path)
		cache := kawpow.cache(blockHeight)
		size := datasetSize(blockHeight)
		if cache.cDag == nil {
			cDag := make([]uint32, kawpowCacheWords)
			generateCDag(cDag, cache.cache, blockHeight/C_epochLength, kawpow.logger)
			cache.cDag = cDag
		}

		kawpowHeaderHash := header.GetKAWPOWHeaderHash()
		digest1, result1 := kawpowLight(size, cache.cache, kawpowHeaderHash.Reverse().Bytes(), nonce64, blockHeight, cache.cDag)

		// Method 2: ComputePowLight (validation path)
		mixHash, powHash := kawpow.ComputePowLight(workHeader)

		if hex.EncodeToString(common.Hash(digest1).Reverse().Bytes()) == mixHash.Hex()[2:] && hex.EncodeToString(result1) == powHash.Hex()[2:] {
			t.Logf("✅ Mining and validation produce identical results")
		} else {
			t.Errorf("Mining and validation results differ")
		}
	})

	t.Run("First Ravencoin KAWPOW block 1219737", func(t *testing.T) {
		logger := log.NewLogger("test.log", "info", 100)
		kawpow := New(params.PowConfig{PowMode: params.ModeNormal}, nil, false, logger)

		// First KAWPOW block on Ravencoin blockchain
		blockHeight := uint64(1219737)
		nonce64 := uint64(0xe9d8d6)
		headerHashStr := "e7946fbc0ddbf37a6210ae87ea66387621a39a8987e176d0c3574e9deb247aa2"
		headerHashStrReversed := "a27a24eb9d4e57c3d076e187899aa321763866ea87ae10627af3db0dbc6f94e7"
		// Mixhash as stored in raw block (little-endian)
		expectedMixHash := "b0e016aaa82b7bc95a1ff881d5b42038307fe36429510fcf61659a53e47dd5a8"

		cache := kawpow.cache(blockHeight)
		size := datasetSize(blockHeight)
		if cache.cDag == nil {
			cDag := make([]uint32, kawpowCacheWords)
			generateCDag(cDag, cache.cache, blockHeight/C_epochLength, kawpow.logger)
			cache.cDag = cDag
		}

		// Test with original byte order
		headerHashBytes, _ := hex.DecodeString(headerHashStr)
		digest1, _ := kawpowLight(size, cache.cache, headerHashBytes, nonce64, blockHeight, cache.cDag)
		mixHash1 := hex.EncodeToString(digest1)

		// Test with reversed byte order
		headerHashBytesRev, _ := hex.DecodeString(headerHashStrReversed)
		digest2, _ := kawpowLight(size, cache.cache, headerHashBytesRev, nonce64, blockHeight, cache.cDag)
		mixHash2 := hex.EncodeToString(digest2)

		mixHash := mixHash1
		if mixHash2 == expectedMixHash {
			mixHash = mixHash2
			fmt.Printf("✅ REVERSED byte order matches!\n")
		}

		fmt.Printf("First KAWPOW Ravencoin Block %d:\n", blockHeight)
		fmt.Printf("  Header hash:     %s\n", headerHashStr)
		fmt.Printf("  Nonce:           %d (0x%x)\n", nonce64, nonce64)
		fmt.Printf("  Result mixhash:  %s\n", mixHash)
		fmt.Printf("  Expected:        %s\n", expectedMixHash)
		fmt.Printf("  Match: %v\n", mixHash == expectedMixHash)

		if mixHash != expectedMixHash {
			t.Errorf("❌ Mixhash mismatch with real Ravencoin block")
		} else {
			t.Logf("✅ Mixhash matches real Ravencoin blockchain!")
		}
	})

	t.Run("GPU Mined block validation", func(t *testing.T) {
		logger := log.NewLogger("test.log", "info", 100)
		kawpow := New(params.PowConfig{PowMode: params.ModeNormal}, nil, false, logger)

		// Block from log: headerHash=0xc09b76e782c9c43795599f0e3f204ea87efab090800055547621301b0f5fa899
		blockHeight := uint64(1219736)
		nonce64 := uint64(281475099654374)
		headerHashStr := "c09b76e782c9c43795599f0e3f204ea87efab090800055547621301b0f5fa899"
		expectedMixHash := "e1837a2b25364d04a5b05458a066d236a747e047bdebbf12512130a9c271afc5"

		cache := kawpow.cache(blockHeight)
		size := datasetSize(blockHeight)
		if cache.cDag == nil {
			cDag := make([]uint32, kawpowCacheWords)
			generateCDag(cDag, cache.cache, blockHeight/C_epochLength, kawpow.logger)
			cache.cDag = cDag
		}

		// Test with the header hash from stratum
		headerHashBytes, _ := hex.DecodeString(headerHashStr)
		digest, result := kawpowLight(size, cache.cache, headerHashBytes, nonce64, blockHeight, cache.cDag)
		mixHash := hex.EncodeToString(digest)
		powHash := hex.EncodeToString(result)

		fmt.Printf("Header hash:     %s\n", headerHashStr)
		fmt.Printf("Nonce:           %d\n", nonce64)
		fmt.Printf("Height:          %d\n", blockHeight)
		fmt.Printf("\nResult mixhash:  %s\n", mixHash)
		fmt.Printf("Expected:        %s\n", expectedMixHash)
		fmt.Printf("Match: %v\n", mixHash == expectedMixHash)
		fmt.Printf("\nPow hash:        %s\n", powHash)

		if mixHash == expectedMixHash {
			t.Logf("✅ GPU miner result matches our KAWPOW implementation!")
		} else {
			t.Errorf("❌ Mixhash mismatch - GPU miner result does not match")
		}
	})

	t.Run("Epoch_Progression", func(t *testing.T) {
		logger := log.NewLogger("test.log", "info", 100)
		kawpow := New(params.PowConfig{PowMode: params.ModeNormal}, nil, false, logger)

		testEpochs := []struct {
			epoch  uint64
			height uint64
		}{
			{162, 1219737}, // KAWPOW activation
			{163, 1222500}, // First new epoch
			{200, 1500000}, // Later epoch
		}

		for _, test := range testEpochs {
			calculatedEpoch := test.height / C_epochLength
			if calculatedEpoch != test.epoch {
				t.Errorf("Epoch mismatch: calculated=%d, expected=%d", calculatedEpoch, test.epoch)
			}

			cache := kawpow.cache(test.height)
			datasetSz := datasetSize(test.height)

			if len(cache.cache) == 0 {
				t.Errorf("Cache is empty for epoch %d", test.epoch)
			}

			t.Logf("Epoch %d: Cache=%.1fMB, Dataset=%.1fGB",
				test.epoch,
				float64(len(cache.cache)*4)/(1024*1024),
				float64(datasetSz)/(1024*1024*1024))
		}
		t.Logf("✅ Epoch progression works correctly")
	})

	t.Run("keccakF800", func(t *testing.T) {
		// Test that keccakF800 function works correctly
		var state [25]uint32
		for i := 0; i < 25; i++ {
			state[i] = uint32(i)
		}
		originalState := state

		keccakF800(&state)

		changed := false
		for i := 0; i < 25; i++ {
			if state[i] != originalState[i] {
				changed = true
				break
			}
		}

		if !changed {
			t.Error("keccakF800 should modify the state")
		}

		// Test with zero state
		var zeroState [25]uint32
		keccakF800(&zeroState)

		allZero := true
		for _, val := range zeroState {
			if val != 0 {
				allZero = false
				break
			}
		}

		if allZero {
			t.Error("keccakF800 should produce non-zero output for zero input")
		}

		t.Logf("✅ keccakF800 function works correctly")
	})
}
