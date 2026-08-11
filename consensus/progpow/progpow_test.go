package progpow

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
)

func TestRejectsOversizedPrimeTerminusBeforeCacheAllocation(t *testing.T) {
	logger := log.NewLogger("test.log", "info", 100)
	engine := New(params.PowConfig{PowMode: params.ModeNormal}, nil, false, logger)
	attackValues := []*big.Int{
		new(big.Int).SetUint64(400_000_000),
		new(big.Int).SetUint64(4_000_000_000),
		new(big.Int).Lsh(big.NewInt(1), 80),
	}
	for _, attackValue := range attackValues {
		header := &types.WorkObjectHeader{}
		header.SetPrimeTerminusNumber(attackValue)
		if _, err := engine.ComputePowHash(header); err == nil {
			t.Fatalf("expected prime terminus number %s to be rejected", attackValue)
		}
		mixHash, powHash := engine.ComputePowLight(header)
		if mixHash != (common.Hash{}) || powHash != (common.Hash{}) {
			t.Fatalf("expected direct light computation to fail closed for %s", attackValue)
		}
	}

	header := &types.WorkObjectHeader{}
	header.SetPrimeTerminusNumber(new(big.Int).SetUint64(params.KawPowForkBlock + params.KawPowTransitionPeriod))
	if err := validateProgpowHeader(header); err != nil {
		t.Fatalf("expected final protocol-valid ProgPoW height to remain valid: %v", err)
	}
	if size := cacheSize(header.PrimeTerminusNumber().Uint64()); size > maxProgpowCacheBytes {
		t.Fatalf("valid ProgPoW height requires oversized cache: %d", size)
	}
}

func TestProgpowDatasetSizes(t *testing.T) {
	for epoch := 0; epoch < maxCachedEpoch; epoch++ {
		require.Equal(t, calcDatasetSize(epoch), datasetSizes[epoch], "failed epoch %d", epoch)
	}
}

func TestProgpowCacheSizes(t *testing.T) {
	for epoch := 0; epoch < maxCachedEpoch; epoch++ {
		require.Equal(t, calcCacheSize(epoch), cacheSizes[epoch])
	}
}

func printSizes(calcFunc func(epoch int) uint64, maxEpoch int, groupSize int, varName string) {
	var results []string

	for epoch := 0; epoch < maxEpoch; epoch++ {
		numberToBePrinted := calcFunc(epoch)
		results = append(results, fmt.Sprintf("%d", numberToBePrinted))
	}

	// Print the formatted output
	fmt.Printf("var %s = [%d]uint64{\n", varName, maxEpoch)

	for i := 0; i < len(results); i += groupSize { // Group by `groupSize`
		end := i + groupSize
		if end > len(results) {
			end = len(results)
		}
		fmt.Printf("\t%s,\n", strings.Join(results[i:end], ", "))
	}

	fmt.Println("}")
}
