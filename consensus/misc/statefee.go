package misc

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

func CalcStateLimit(parent *types.WorkObject, stateCeil uint64) uint64 {
	// No Gas for TimeToStartTx days worth of zone blocks, this gives enough time to
	// onboard new miners into the slice
	if parent.NumberU64(common.ZONE_CTX) < params.TimeToStartTx {
		return 0
	}

	// If parent gas is zero and we have passed the 5 day threshold, we can set the first block gas limit to min gas limit
	if parent.StateLimit() == 0 {
		return params.MinGasLimit
	}

	parentStateLimit := parent.StateLimit()

	delta := parentStateLimit/params.GasLimitBoundDivisor - 1
	limit := parentStateLimit

	var desiredLimit uint64
	percentGasUsed := parent.StateUsed() * 100 / parent.StateLimit()
	if percentGasUsed > params.PercentGasUsedThreshold {
		desiredLimit = stateCeil
		if limit+delta > desiredLimit {
			return desiredLimit
		} else {
			return limit + delta
		}
	} else {
		desiredLimit = params.MinGasLimit
		if limit-delta/2 < desiredLimit {
			return desiredLimit
		} else {
			return limit - delta/2
		}
	}
}
