package core

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func generateRandomBlockFees(min, max, numBlocks, maxNumTxs int) (simpleBlockFees [][]int, bigBlockFees [][]*big.Int) {
	// Generate the number of txs in this block first.
	simpleBlockFees = make([][]int, numBlocks)
	bigBlockFees = make([][]*big.Int, numBlocks)
	for blockNum := 0; blockNum < numBlocks; blockNum++ {
		txLen := rand.Intn(maxNumTxs)

		simpleBlockFees[blockNum] = make([]int, txLen)
		bigBlockFees[blockNum] = make([]*big.Int, txLen)

		for i := 0; i < txLen; i++ {
			randNum := rand.Intn(max-min+1) + min
			simpleBlockFees[blockNum][i] = randNum
			bigBlockFees[blockNum][i] = big.NewInt(int64(randNum))
		}
	}

	return simpleBlockFees, bigBlockFees
}

func TestMultiBlockTxStats(t *testing.T) {
	simpleBlockFees, bigBlockFees := generateRandomBlockFees(0, 10000, 10000, 1000)

	var expectedRollingMin, expectedRollingMax, expectedRollingAvg int
	for blockNum, blockFees := range simpleBlockFees {
		var blockMin, blockMax, sum int
		for txCount, fee := range blockFees {
			if txCount == 0 {
				blockMin = fee
				blockMax = fee
				sum = 0
			} else {
				if fee < blockMin {
					blockMin = fee
				}

				if fee > blockMax {
					blockMax = fee
				}
			}
			sum += fee
		}

		if len(blockFees) > 0 {
			if blockMin < expectedRollingMin || blockNum == 0 {
				expectedRollingMin = blockMin
			} else {
				expectedRollingMin = expectedRollingMin * 101 / 100
			}

			if blockMax > expectedRollingMax || blockNum == 0 {
				expectedRollingMax = blockMax
			} else {
				expectedRollingMax = expectedRollingMax * 99 / 100
			}
			// Calculate a running average
			expectedRollingAvg = (blockNum*expectedRollingAvg + (sum / len(blockFees))) / (blockNum + 1)
		}
	}

	var blockMin, blockMax *big.Int
	var actualRollingMin, actualRollingMax, rollingAvg, rollingNumElements *big.Int
	for _, blockFees := range bigBlockFees {
		bigTxsProcessed := big.NewInt(0)
		blockTotal := big.NewInt(0)
		for _, fee := range blockFees {
			blockMin, blockMax = calcTxStats(blockMin, blockMax, fee, bigTxsProcessed)
			blockTotal.Add(blockTotal, fee)
		}

		actualRollingMin, actualRollingMax, rollingAvg, rollingNumElements = calcRollingFeeInfo(actualRollingMin, actualRollingMax, rollingAvg, rollingNumElements, blockMin, blockMax, blockTotal, bigTxsProcessed)
	}

	if expectedRollingMin != 0 || expectedRollingMax != 0 || expectedRollingAvg != 0 {
		// If any one of these values is non-zero, then there were fees, so compare.
		require.Equal(t, uint64(expectedRollingMin), actualRollingMin.Uint64(), "Expected min not equal")
		require.Equal(t, uint64(expectedRollingMax), actualRollingMax.Uint64(), "Expected max not equal")
		require.Equal(t, uint64(expectedRollingAvg), rollingAvg.Uint64(), "Expected average not equal")
	}
}
