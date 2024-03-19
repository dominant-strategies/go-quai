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
	nodeCtx := blake3pow.config.NodeLocation.Context()
	if header.NumberU64(nodeCtx) == 0 {
		return common.Big0, common.PRIME_CTX, nil
	}

	// Verify the seal and get the powHash for the given header
	err := blake3pow.verifySeal(header)
	if err != nil {
		return big0, -1, err
	}

	// Get entropy reduction of this header
	intrinsicS := blake3pow.IntrinsicLogS(header.Hash())
	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
	zoneThresholdS := blake3pow.IntrinsicLogS(common.BytesToHash(target.Bytes()))

	// PRIME
	// PrimeEntropyThreshold number of zone blocks times the intrinsic logs of
	// the given header determines the prime block
	totalDeltaSPrime := new(big.Int).Add(header.ParentDeltaS(common.REGION_CTX), header.ParentDeltaS(common.ZONE_CTX))
	totalDeltaSPrime = new(big.Int).Add(totalDeltaSPrime, intrinsicS)
	primeDeltaSTarget := new(big.Int).Div(params.PrimeEntropyTarget, big2)
	primeDeltaSTarget = new(big.Int).Mul(zoneThresholdS, primeDeltaSTarget)

	primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(params.PrimeEntropyTarget))
	if intrinsicS.Cmp(primeBlockEntropyThreshold) > 0 && totalDeltaSPrime.Cmp(primeDeltaSTarget) > 0 {
		return intrinsicS, common.PRIME_CTX, nil
	}

	// REGION
	// Compute the total accumulated entropy since the last region block
	totalDeltaSRegion := new(big.Int).Add(header.ParentDeltaS(common.ZONE_CTX), intrinsicS)
	regionDeltaSTarget := new(big.Int).Div(params.RegionEntropyTarget, big2)
	regionDeltaSTarget = new(big.Int).Mul(zoneThresholdS, regionDeltaSTarget)
	regionBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(params.RegionEntropyTarget))
	if intrinsicS.Cmp(regionBlockEntropyThreshold) > 0 && totalDeltaSRegion.Cmp(regionDeltaSTarget) > 0 {
		return intrinsicS, common.REGION_CTX, nil
	}

	// Zone case
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
	switch blake3pow.config.NodeLocation.Context() {
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

func (blake3pow *Blake3pow) UncledLogS(block *types.Block) *big.Int {
	uncles := block.Uncles()
	totalUncledLogS := big.NewInt(0)
	for _, uncle := range uncles {
		// Verify the seal and get the powHash for the given header
		err := blake3pow.verifySeal(uncle)
		if err != nil {
			continue
		}
		// Get entropy reduction of this header
		intrinsicS := blake3pow.IntrinsicLogS(uncle.Hash())
		totalUncledLogS.Add(totalUncledLogS, intrinsicS)
	}
	return totalUncledLogS
}

func (blake3pow *Blake3pow) UncledSubDeltaLogS(header *types.Header) *big.Int {
	_, order, err := blake3pow.CalcOrder(header)
	if err != nil {
		return big.NewInt(0)
	}
	uncledLogS := header.UncledS()
	switch order {
	case common.PRIME_CTX:
		return big.NewInt(0)
	case common.REGION_CTX:
		totalDeltaS := new(big.Int).Add(header.ParentUncledSubDeltaS(common.REGION_CTX), header.ParentUncledSubDeltaS(common.ZONE_CTX))
		totalDeltaS = new(big.Int).Add(totalDeltaS, uncledLogS)
		return totalDeltaS
	case common.ZONE_CTX:
		totalDeltaS := new(big.Int).Add(header.ParentUncledSubDeltaS(common.ZONE_CTX), uncledLogS)
		return totalDeltaS
	}
	return big.NewInt(0)
}
