package blake3pow

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"modernc.org/mathutil"
)

// CalcOrder returns the order of the block within the hierarchy of chains
func (blake3pow *Blake3pow) CalcOrder(header *types.Header) (*big.Int, int, error) {
	if header.NumberU64() == 0 {
		return common.Big0, common.PRIME_CTX, nil
	}

	// Verify the seal and get the powHash for the given header
	err := blake3pow.verifySeal(header)
	if err != nil {
		return big0, -1, err
	}

	// Get entropy reduction of this header
	intrinsicS := blake3pow.IntrinsicLogS(header.Hash())

	// This is the updated the threshold calculation based on the zone difficulty threshold
	target := new(big.Int).Div(big2e256, header.Difficulty()).Bytes()
	zoneThresholdS := blake3pow.IntrinsicLogS(common.BytesToHash(target))
	timeFactorHierarchyDepthMultiple := new(big.Int).Mul(params.TimeFactor, big.NewInt(common.HierarchyDepth))

	// //////////////
	// Prime case
	// /////////////
	// Compute the prime comulative entropy threshold based on the time factor of the hierarchy.

	primeEntropyThresholdFactor := new(big.Int).Mul(timeFactorHierarchyDepthMultiple, timeFactorHierarchyDepthMultiple)
	primeRandFactor := new(big.Int).Quo(primeEntropyThresholdFactor, big.NewInt(2))
	primeRandFactorBits, _ := mathutil.BinaryLog(primeRandFactor, mantBits)
	primeEntropyThresholdFactor = new(big.Int).Sub(primeEntropyThresholdFactor, big.NewInt(header.TerminusHash().GetTrailingBits(primeRandFactorBits)))
	primeEntropyThreshold := new(big.Int).Mul(primeEntropyThresholdFactor, zoneThresholdS)

	// Compute the total accumulated entropy since the last prime block
	totalDeltaSPrime := new(big.Int).Add(header.ParentDeltaS(common.REGION_CTX), header.ParentDeltaS(common.ZONE_CTX))
	totalDeltaSPrime.Add(totalDeltaSPrime, intrinsicS)

	if totalDeltaSPrime.Cmp(primeEntropyThreshold) > 0 {
		return intrinsicS, common.PRIME_CTX, nil
	}

	// ////////////
	// Region case
	// ////////////
	// Compute the region comulative entropy threshold based on the time factor of the hierarchy.
	regionEntropyThresholdFactor := timeFactorHierarchyDepthMultiple
	regionRandFactor := new(big.Int).Quo(regionEntropyThresholdFactor, big.NewInt(2))
	regionRandFactorBits, _ := mathutil.BinaryLog(regionRandFactor, mantBits)
	regionEntropyThresholdFactor = new(big.Int).Sub(regionEntropyThresholdFactor, big.NewInt(header.TerminusHash().GetTrailingBits(regionRandFactorBits)))
	regionEntropyThreshold := new(big.Int).Mul(regionEntropyThresholdFactor, zoneThresholdS)

	// Compute the total accumulated entropy since the last region block
	totalDeltaSRegion := new(big.Int).Add(header.ParentDeltaS(common.ZONE_CTX), intrinsicS)

	if totalDeltaSRegion.Cmp(regionEntropyThreshold) > 0 {
		return intrinsicS, common.REGION_CTX, nil
	}

	// ////////////
	// Zone case
	// ////////////
	return intrinsicS, common.ZONE_CTX, nil
}

// IntrinsicLogS returns the logarithm of the intrinsic entropy reduction of a PoW hash
func (blake3pow *Blake3pow) IntrinsicLogS(powHash common.Hash) *big.Int {
	x := new(big.Int).SetBytes(powHash.Bytes())
	d := new(big.Int).Div(big2e256, x)
	c, m := mathutil.BinaryLog(d, mantBits)
	bigBits := new(big.Int).Mul(big.NewInt(int64(c)), new(big.Int).Exp(big.NewInt(2), big.NewInt(mantBits), nil))
	bigBits = new(big.Int).Add(bigBits, m)
	return bigBits
}

// TotalLogS() returns the total entropy reduction if the chain since genesis to the given header
func (blake3pow *Blake3pow) TotalLogS(header *types.Header) *big.Int {
	intrinsicS, order, err := blake3pow.CalcOrder(header)
	if err != nil {
		return big.NewInt(0)
	}
	switch order {
	case common.PRIME_CTX:
		totalS := new(big.Int).Add(header.ParentEntropy(common.PRIME_CTX), header.ParentDeltaS(common.REGION_CTX))
		totalS.Add(totalS, header.ParentDeltaS(common.ZONE_CTX))
		totalS.Add(totalS, intrinsicS)
		return totalS
	case common.REGION_CTX:
		totalS := new(big.Int).Add(header.ParentEntropy(common.REGION_CTX), header.ParentDeltaS(common.ZONE_CTX))
		totalS.Add(totalS, intrinsicS)
		return totalS
	case common.ZONE_CTX:
		totalS := new(big.Int).Add(header.ParentEntropy(common.ZONE_CTX), intrinsicS)
		return totalS
	}
	return big.NewInt(0)
}

func (blake3pow *Blake3pow) TotalLogPhS(header *types.Header) *big.Int {
	switch common.NodeLocation.Context() {
	case common.PRIME_CTX:
		totalS := header.ParentEntropy(common.PRIME_CTX)
		return totalS
	case common.REGION_CTX:
		totalS := new(big.Int).Add(header.ParentEntropy(common.PRIME_CTX), header.ParentDeltaS(common.REGION_CTX))
		return totalS
	case common.ZONE_CTX:
		totalS := new(big.Int).Add(header.ParentEntropy(common.PRIME_CTX), header.ParentDeltaS(common.REGION_CTX))
		totalS.Add(totalS, header.ParentDeltaS(common.ZONE_CTX))
		return totalS
	}
	return big.NewInt(0)
}

func (blake3pow *Blake3pow) DeltaLogS(header *types.Header) *big.Int {
	intrinsicS, order, err := blake3pow.CalcOrder(header)
	if err != nil {
		return big.NewInt(0)
	}
	switch order {
	case common.PRIME_CTX:
		return big.NewInt(0)
	case common.REGION_CTX:
		totalDeltaS := new(big.Int).Add(header.ParentDeltaS(common.REGION_CTX), header.ParentDeltaS(common.ZONE_CTX))
		totalDeltaS = new(big.Int).Add(totalDeltaS, intrinsicS)
		return totalDeltaS
	case common.ZONE_CTX:
		totalDeltaS := new(big.Int).Add(header.ParentDeltaS(common.ZONE_CTX), intrinsicS)
		return totalDeltaS
	}
	return big.NewInt(0)
}
