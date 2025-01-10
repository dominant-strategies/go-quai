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

	minGasLimit := params.MinGasLimit(parent.NumberU64(common.ZONE_CTX))
	// If parent gas is zero and we have passed the 5 day threshold, we can set the first block gas limit to min gas limit
	if parent.StateLimit() == 0 {
		return minGasLimit
	}

	//  For the first two months increment the gas limit slowly, then just
	//  return the max gas limit
	if parent.NumberU64(common.ZONE_CTX) < 2*params.BlocksPerMonth {
		gasLimit := (parent.NumberU64(common.ZONE_CTX) * stateCeil) / (2 * params.BlocksPerMonth)
		if gasLimit < minGasLimit {
			return minGasLimit
		}
		return gasLimit
	} else {
		return stateCeil
	}
}
