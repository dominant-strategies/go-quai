package progpow

import (
	"errors"
	"math"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	bigMath "github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"modernc.org/mathutil"
)

// CalcOrder returns the order of the block within the hierarchy of chains
func (progpow *Progpow) CalcOrder(header *types.WorkObject) (*big.Int, int, error) {
	nodeCtx := progpow.config.NodeLocation.Context()
	// Except for the slice [0,0] have to check if the header hash is the genesis hash
	if header.NumberU64(nodeCtx) == 0 {
		return big0, common.PRIME_CTX, nil
	}
	expansionNum := header.ExpansionNumber()

	// Verify the seal and get the powHash for the given header
	powHash, err := progpow.verifySeal(header.WorkObjectHeader())
	if err != nil {
		return big0, -1, err
	}

	// Get entropy reduction of this header
	intrinsicS := progpow.IntrinsicLogS(powHash)
	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
	zoneThresholdS := progpow.IntrinsicLogS(common.BytesToHash(target.Bytes()))

	// PRIME
	// PrimeEntropyThreshold number of zone blocks times the intrinsic logs of
	// the given header determines the prime block
	totalDeltaSPrime := new(big.Int).Add(header.ParentDeltaS(common.REGION_CTX), header.ParentDeltaS(common.ZONE_CTX))
	totalDeltaSPrime = new(big.Int).Add(totalDeltaSPrime, intrinsicS)
	primeDeltaSTarget := new(big.Int).Div(params.PrimeEntropyTarget(expansionNum), big2)
	primeDeltaSTarget = new(big.Int).Mul(zoneThresholdS, primeDeltaSTarget)

	primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(params.PrimeEntropyTarget(expansionNum)))
	if intrinsicS.Cmp(primeBlockEntropyThreshold) > 0 && totalDeltaSPrime.Cmp(primeDeltaSTarget) > 0 {
		return intrinsicS, common.PRIME_CTX, nil
	}

	// REGION
	// Compute the total accumulated entropy since the last region block
	totalDeltaSRegion := new(big.Int).Add(header.ParentDeltaS(common.ZONE_CTX), intrinsicS)
	regionDeltaSTarget := new(big.Int).Div(params.RegionEntropyTarget(expansionNum), big2)
	regionDeltaSTarget = new(big.Int).Mul(zoneThresholdS, regionDeltaSTarget)
	regionBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(params.RegionEntropyTarget(expansionNum)))
	if intrinsicS.Cmp(regionBlockEntropyThreshold) > 0 && totalDeltaSRegion.Cmp(regionDeltaSTarget) > 0 {
		return intrinsicS, common.REGION_CTX, nil
	}

	// Zone case
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
func (progpow *Progpow) TotalLogS(chain consensus.GenesisReader, header *types.WorkObject) *big.Int {
	if chain.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	intrinsicS, order, err := progpow.CalcOrder(header)
	if err != nil {
		return big.NewInt(0)
	}
	if progpow.NodeLocation().Context() == common.ZONE_CTX {
		workShareS, err := progpow.WorkShareLogS(header)
		if err != nil {
			return big.NewInt(0)
		}
		intrinsicS = new(big.Int).Add(intrinsicS, workShareS)
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

func (progpow *Progpow) TotalLogPhS(header *types.WorkObject) *big.Int {
	switch progpow.config.NodeLocation.Context() {
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

func (progpow *Progpow) DeltaLogS(chain consensus.GenesisReader, header *types.WorkObject) *big.Int {
	if chain.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	intrinsicS, order, err := progpow.CalcOrder(header)
	if err != nil {
		return big.NewInt(0)
	}
	if progpow.NodeLocation().Context() == common.ZONE_CTX {
		workShareS, err := progpow.WorkShareLogS(header)
		if err != nil {
			return big.NewInt(0)
		}
		intrinsicS = new(big.Int).Add(intrinsicS, workShareS)
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

func (progpow *Progpow) UncledLogS(block *types.WorkObject) *big.Int {
	uncles := block.Uncles()
	totalUncledLogS := big.NewInt(0)
	for _, uncle := range uncles {
		// Verify the seal and get the powHash for the given header
		powHash, err := progpow.verifySeal(uncle)
		if err != nil {
			continue
		}
		// Get entropy reduction of this header
		intrinsicS := progpow.IntrinsicLogS(powHash)
		totalUncledLogS.Add(totalUncledLogS, intrinsicS)
	}
	return totalUncledLogS
}

func (progpow *Progpow) WorkShareLogS(wo *types.WorkObject) (*big.Int, error) {
	workShares := wo.Uncles()
	totalWsEntropy := big.NewInt(0)
	for _, ws := range workShares {
		powHash, err := progpow.ComputePowHash(ws)
		if err != nil {
			return big.NewInt(0), err
		}
		// Two discounts need to be applied to the weight of each work share
		// 1) Discount based on the amount of number of other possible work
		// shares for the same entropy value
		// 2) Discount based on the staleness of inclusion, for every block
		// delay the weight gets reduced by the factor of 2

		// Discount 1) only applies if the workshare has less weight than the
		// work object threshold
		var wsEntropy *big.Int
		woDiff := new(big.Int).Set(wo.Difficulty())
		target := new(big.Int).Div(common.Big2e256, woDiff)
		if new(big.Int).SetBytes(powHash.Bytes()).Cmp(target) > 0 { // powHash > target
			// The work share that has less than threshold weight needs to add
			// an extra bit for each level
			// This is achieved using three steps
			// 1) Find the difference in entropy between the work share and
			// threshold in the 2^mantBits bits field because otherwise the precision
			// is lost due to integer division
			// 2) Divide this difference with the 2^mantBits to get the number
			// of bits of difference to discount the workshare entropy
			// 3) Divide the entropy difference with 2^(extraBits+1) to get the
			// actual work share weight here +1 is done to the extraBits because
			// of Quo and if the difference is less than 0, its within the first
			// level
			cBigBits := progpow.IntrinsicLogS(powHash)
			thresholdBigBits := progpow.IntrinsicLogS(common.BytesToHash(target.Bytes()))
			wsEntropy = new(big.Int).Sub(thresholdBigBits, cBigBits)
			extraBits := new(big.Float).Quo(new(big.Float).SetInt(wsEntropy), new(big.Float).SetInt(common.Big2e64))
			wsEntropyAdj := new(big.Float).Quo(new(big.Float).SetInt(common.Big2e64), bigMath.TwoToTheX(extraBits))
			wsEntropy, _ = wsEntropyAdj.Int(wsEntropy)
		} else {
			wsEntropy = new(big.Int).Set(progpow.IntrinsicLogS(powHash))
		}
		// Discount 2) applies to all shares regardless of the weight
		wsEntropy = new(big.Int).Div(wsEntropy, new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(wo.NumberU64(common.ZONE_CTX)-ws.NumberU64())), nil))
		// Add the entropy into the total entropy once the discount calculation is done
		totalWsEntropy.Add(totalWsEntropy, wsEntropy)
	}
	return totalWsEntropy, nil
}

func (progpow *Progpow) UncledSubDeltaLogS(chain consensus.GenesisReader, header *types.WorkObject) *big.Int {
	// Treating the genesis block differntly
	if chain.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	_, order, err := progpow.CalcOrder(header)
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

// CalcRank returns the rank of the block within the hierarchy of chains, this
// determines the level of the interlink
func (progpow *Progpow) CalcRank(chain consensus.GenesisReader, header *types.WorkObject) (int, error) {
	if chain.IsGenesisHash(header.Hash()) {
		return 0, nil
	}
	_, order, err := progpow.CalcOrder(header)
	if err != nil {
		return 0, err
	}
	if order != common.PRIME_CTX {
		return 0, errors.New("rank cannot be computed for a non-prime block")
	}

	// Verify the seal and get the powHash for the given header
	powHash, err := progpow.verifySeal(header.WorkObjectHeader())
	if err != nil {
		return 0, err
	}

	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
	zoneThresholdS := progpow.IntrinsicLogS(common.BytesToHash(target.Bytes()))

	intrinsicS := progpow.IntrinsicLogS(powHash)
	for i := common.InterlinkDepth; i > 0; i-- {
		extraBits := math.Pow(2, float64(i))
		primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(big.NewInt(int64(extraBits))))
		primeBlockEntropyThreshold = new(big.Int).Add(primeBlockEntropyThreshold, common.BitsToBigBits(params.PrimeEntropyTarget(header.ExpansionNumber())))
		if intrinsicS.Cmp(primeBlockEntropyThreshold) > 0 {
			return i, nil
		}
	}
	return 0, nil
}

func (progpow *Progpow) CheckIfValidWorkShare(workShare *types.WorkObjectHeader) bool {
	// Extract some data from the header
	diff := new(big.Int).Set(workShare.Difficulty())
	c, _ := mathutil.BinaryLog(diff, mantBits)
	if c <= params.WorkSharesThresholdDiff {
		return false
	}
	workShareThreshold := c - params.WorkSharesThresholdDiff
	workShareDiff := new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(workShareThreshold)), nil)
	workShareMintarget := new(big.Int).Div(big2e256, workShareDiff)
	powHash, err := progpow.ComputePowHash(workShare)
	if err != nil {
		return false
	}
	return new(big.Int).SetBytes(powHash.Bytes()).Cmp(workShareMintarget) <= 0
}
