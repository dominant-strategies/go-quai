package progpow

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"modernc.org/mathutil"
)

// CalcOrder returns the order of the block within the hierarchy of chains
func (progpow *Progpow) CalcOrder(header *types.Header) (*big.Int, int, error) {
	if header.NumberU64() == 0 {
		return big0, common.PRIME_CTX, nil
	}

	// Verify the seal and get the powHash for the given header
	powHash, err := progpow.verifySeal(header)
	if err != nil {
		return big0, -1, err
	}

	// Get entropy reduction of this header
	intrinsicS := progpow.IntrinsicLogS(powHash)
	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
	zoneThresholdS := progpow.IntrinsicLogS(common.BytesToHash(target.Bytes()))

	// PRIME
	// Compute the total accumulated entropy since the last prime block
	totalDeltaSPrime := new(big.Int).Add(header.ParentDeltaS(common.REGION_CTX), header.ParentDeltaS(common.ZONE_CTX))
	totalDeltaSPrime.Add(totalDeltaSPrime, intrinsicS)

	// PrimeEntropyThreshold number of zone blocks times the intrinsic logs of the given header determines the prime block
	primeEntropyTarget := new(big.Int).Mul(big.NewInt(common.NumRegionsInPrime), big.NewInt(common.NumZonesInRegion))
	primeEntropyTarget = new(big.Int).Mul(primeEntropyTarget, params.TimeFactor)
	primeEntropyTarget = new(big.Int).Mul(primeEntropyTarget, params.TimeFactor)
	primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(primeEntropyTarget))
	if intrinsicS.Cmp(primeBlockEntropyThreshold) > 0 {
		return intrinsicS, common.PRIME_CTX, nil
	}

	// REGION
	// Compute the total accumulated entropy since the last region block
	regionEntropyTarget := new(big.Int).Mul(params.TimeFactor, big.NewInt(common.NumZonesInRegion))
	regionBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(regionEntropyTarget))
	if intrinsicS.Cmp(regionBlockEntropyThreshold) > 0 {
		return intrinsicS, common.REGION_CTX, nil
	}

	// ZONE
	return intrinsicS, common.ZONE_CTX, nil
}

// IntrinsicLogS returns the logarithm of the intrinsic entropy reduction of a PoW hash
func (progpow *Progpow) IntrinsicLogS(powHash common.Hash) *big.Int {
	x := new(big.Int).SetBytes(powHash.Bytes())
	d := new(big.Int).Div(big2e256, x)
	c, m := mathutil.BinaryLog(d, mantBits)
	bigBits := new(big.Int).Mul(big.NewInt(int64(c)), new(big.Int).Exp(big.NewInt(2), big.NewInt(mantBits), nil))
	bigBits = new(big.Int).Add(bigBits, m)
	return bigBits
}

// TotalLogS() returns the total entropy reduction if the chain since genesis to the given header
func (progpow *Progpow) TotalLogS(header *types.Header) *big.Int {
	intrinsicS, order, err := progpow.CalcOrder(header)
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

func (progpow *Progpow) TotalLogPhS(header *types.Header) *big.Int {
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

func (progpow *Progpow) DeltaLogS(header *types.Header) *big.Int {
	intrinsicS, order, err := progpow.CalcOrder(header)
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
