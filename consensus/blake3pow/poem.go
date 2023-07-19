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

	// Get entropy reduction of this header
	intrinsicS := blake3pow.IntrinsicLogS(header.Hash())

	// This is the updated the threshold calculation based on the zone difficulty threshold
	target := new(big.Int).Div(big2e256, header.Difficulty()).Bytes()
	zoneThresholdS := blake3pow.IntrinsicLogS(common.BytesToHash(target))
	timeFactorHierarchyDepthMultiple := new(big.Int).Mul(params.TimeFactor, big.NewInt(common.HierarchyDepth))

	// Prime case
	primeEntropyThreshold := new(big.Int).Mul(timeFactorHierarchyDepthMultiple, timeFactorHierarchyDepthMultiple)
	primeEntropyThreshold = new(big.Int).Mul(primeEntropyThreshold, zoneThresholdS)
	primeBlockThreshold := new(big.Int).Quo(primeEntropyThreshold, big.NewInt(2))
	primeEntropyThreshold = new(big.Int).Sub(primeEntropyThreshold, primeBlockThreshold)

	primeBlockEntropyThresholdAdder, _ := mathutil.BinaryLog(primeBlockThreshold, 8)
	primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, big.NewInt(int64(primeBlockEntropyThresholdAdder)))

	totalDeltaS := new(big.Int).Add(header.ParentDeltaS(common.REGION_CTX), header.ParentDeltaS(common.ZONE_CTX))
	totalDeltaS.Add(totalDeltaS, intrinsicS)
	if intrinsicS.Cmp(primeBlockEntropyThreshold) > 0 && totalDeltaS.Cmp(primeEntropyThreshold) > 0 {
		return intrinsicS, common.PRIME_CTX, nil
	}

	// Region case
	regionEntropyThreshold := new(big.Int).Mul(timeFactorHierarchyDepthMultiple, zoneThresholdS)
	regionBlockThreshold := new(big.Int).Quo(regionEntropyThreshold, big.NewInt(2))
	regionEntropyThreshold = new(big.Int).Sub(regionEntropyThreshold, regionBlockThreshold)

	regionBlockEntropyThresholdAdder, _ := mathutil.BinaryLog(regionBlockThreshold, 8)
	regionBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, big.NewInt(int64(regionBlockEntropyThresholdAdder)))

	totalDeltaS = new(big.Int).Add(header.ParentDeltaS(common.ZONE_CTX), intrinsicS)
	if intrinsicS.Cmp(regionBlockEntropyThreshold) > 0 && totalDeltaS.Cmp(regionEntropyThreshold) > 0 {
		return intrinsicS, common.REGION_CTX, nil
	}

	// Zone case
	if intrinsicS.Cmp(zoneThresholdS) >= 0 {
		return intrinsicS, common.ZONE_CTX, nil
	}

	return common.Big0, -1, nil
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
