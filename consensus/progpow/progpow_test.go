package progpow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func printSizes(calcFunc func(epoch int) uint64, maxEpoch int, varName string) {
	var results []string
	groupSize := 4 // Number of elements to print per line

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
