package core

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
)

func TestCalcGasLimit(t *testing.T) {
	emptyWo := types.EmptyWorkObject(common.ZONE_CTX)

	blockNumbers := []uint64{0, 10, params.TimeToStartTx, params.BlocksPerWeek, params.BlocksPerMonth, 2 * params.BlocksPerMonth, 3 * params.BlocksPerMonth / 2, 4 * params.BlocksPerMonth}
	expectedGasLimit := []uint64{0, 0, 12000000, 12000000, 25000000, 50000000, 37500000, 50000000}

	for i := 0; i < len(blockNumbers); i++ {
		emptyWo.Header().SetGasLimit(params.MinGasLimit(blockNumbers[i]))
		emptyWo.SetNumber(new(big.Int).SetInt64(int64(blockNumbers[i])), common.ZONE_CTX)
		gasLimit := CalcGasLimit(emptyWo, params.GasCeil)
		require.Equal(t, expectedGasLimit[i], gasLimit)
	}
}
