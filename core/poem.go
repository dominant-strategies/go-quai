package core

import (
	"errors"
	"math"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	bigMath "github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

// CalcOrder returns the order of the block within the hierarchy of chains
func (hc *HeaderChain) CalcOrder(header *types.WorkObject) (*big.Int, int, error) {
	// check if the order for this block has already been computed
	intrinsicEntropy, order, exists := hc.CheckInCalcOrderCache(header.Hash())
	if exists {
		return intrinsicEntropy, order, nil
	}
	nodeCtx := hc.NodeLocation().Context()
	// Except for the slice [0,0] have to check if the header hash is the genesis hash
	if header.NumberU64(nodeCtx) == 0 {
		return big.NewInt(0), common.PRIME_CTX, nil
	}
	expansionNum := header.ExpansionNumber()

	// Verify the seal and get the powHash for the given header
	// get the engine and call verify seal
	powHash, err := hc.verifySeal(header.WorkObjectHeader())
	if err != nil {
		return big.NewInt(0), -1, err
	}

	// Get entropy reduction of this header
	intrinsicEntropy = common.IntrinsicLogEntropy(powHash)
	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
	zoneThresholdEntropy := common.IntrinsicLogEntropy(common.BytesToHash(target.Bytes()))

	// PRIME
	// PrimeEntropyThreshold number of zone blocks times the intrinsic logs of
	// the given header determines the prime block
	totalDeltaEntropyPrime := new(big.Int).Add(header.ParentDeltaEntropy(common.REGION_CTX), header.ParentDeltaEntropy(common.ZONE_CTX))
	totalDeltaEntropyPrime = new(big.Int).Add(totalDeltaEntropyPrime, intrinsicEntropy)

	primeDeltaEntropyTarget := new(big.Int).Mul(params.PrimeEntropyTarget(expansionNum), zoneThresholdEntropy)
	primeDeltaEntropyTarget = new(big.Int).Div(primeDeltaEntropyTarget, common.Big2)

	primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdEntropy, common.BitsToBigBits(params.PrimeEntropyTarget(expansionNum)))
	if intrinsicEntropy.Cmp(primeBlockEntropyThreshold) > 0 && totalDeltaEntropyPrime.Cmp(primeDeltaEntropyTarget) > 0 {
		hc.AddToCalcOrderCache(header.Hash(), common.PRIME_CTX, intrinsicEntropy)
		return intrinsicEntropy, common.PRIME_CTX, nil
	}

	// REGION
	// Compute the total accumulated entropy since the last region block
	totalDeltaSRegion := new(big.Int).Add(header.ParentDeltaEntropy(common.ZONE_CTX), intrinsicEntropy)

	regionDeltaSTarget := new(big.Int).Mul(zoneThresholdEntropy, params.RegionEntropyTarget(expansionNum))
	regionDeltaSTarget = new(big.Int).Div(regionDeltaSTarget, common.Big2)

	regionBlockEntropyThreshold := new(big.Int).Add(zoneThresholdEntropy, common.BitsToBigBits(params.RegionEntropyTarget(expansionNum)))
	if intrinsicEntropy.Cmp(regionBlockEntropyThreshold) > 0 && totalDeltaSRegion.Cmp(regionDeltaSTarget) > 0 {
		hc.AddToCalcOrderCache(header.Hash(), common.REGION_CTX, intrinsicEntropy)
		return intrinsicEntropy, common.REGION_CTX, nil
	}

	// Zone case
	hc.AddToCalcOrderCache(header.Hash(), common.ZONE_CTX, intrinsicEntropy)
	return intrinsicEntropy, common.ZONE_CTX, nil
}

// TotalLogEntropy returns the total entropy reduction if the chain since genesis to the given header
func (hc *HeaderChain) TotalLogEntropy(header *types.WorkObject) *big.Int {
	if hc.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	intrinsicEntropy, order, err := hc.CalcOrder(header)
	if err != nil {
		hc.logger.WithField("err", err).Error("Error calculating order in TotalLogEntropy")
		return big.NewInt(0)
	}
	if hc.NodeLocation().Context() == common.ZONE_CTX {
		workShareEntropy, err := hc.WorkShareLogEntropy(header)
		if err != nil {
			hc.logger.WithField("err", err).Error("Error calculating WorkShareLogEntropy in TotalLogEntropy")
			return big.NewInt(0)
		}
		intrinsicEntropy = new(big.Int).Add(intrinsicEntropy, workShareEntropy)
	}
	switch order {
	case common.PRIME_CTX:
		totalEntropy := new(big.Int).Add(header.ParentEntropy(common.PRIME_CTX), header.ParentDeltaEntropy(common.REGION_CTX))
		totalEntropy.Add(totalEntropy, header.ParentDeltaEntropy(common.ZONE_CTX))
		totalEntropy.Add(totalEntropy, intrinsicEntropy)
		return totalEntropy
	case common.REGION_CTX:
		totalEntropy := new(big.Int).Add(header.ParentEntropy(common.REGION_CTX), header.ParentDeltaEntropy(common.ZONE_CTX))
		totalEntropy.Add(totalEntropy, intrinsicEntropy)
		return totalEntropy
	case common.ZONE_CTX:
		totalEntropy := new(big.Int).Add(header.ParentEntropy(common.ZONE_CTX), intrinsicEntropy)
		return totalEntropy
	}
	return big.NewInt(0)
}

func (hc *HeaderChain) DeltaLogEntropy(header *types.WorkObject) *big.Int {
	if hc.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	intrinsicS, order, err := hc.CalcOrder(header)
	if err != nil {
		hc.logger.WithField("err", err).Error("Error calculating order in DeltaLogEntropy")
		return big.NewInt(0)
	}
	if hc.NodeLocation().Context() == common.ZONE_CTX {
		workShareS, err := hc.WorkShareLogEntropy(header)
		if err != nil {
			hc.logger.WithField("err", err).Error("Error calculating WorkShareLogEntropy in DeltaLogEntropy")
			return big.NewInt(0)
		}
		intrinsicS = new(big.Int).Add(intrinsicS, workShareS)
	}
	switch order {
	case common.PRIME_CTX:
		return big.NewInt(0)
	case common.REGION_CTX:
		totalDeltaEntropy := new(big.Int).Add(header.ParentDeltaEntropy(common.REGION_CTX), header.ParentDeltaEntropy(common.ZONE_CTX))
		totalDeltaEntropy = new(big.Int).Add(totalDeltaEntropy, intrinsicS)
		return totalDeltaEntropy
	case common.ZONE_CTX:
		totalDeltaEntropy := new(big.Int).Add(header.ParentDeltaEntropy(common.ZONE_CTX), intrinsicS)
		return totalDeltaEntropy
	}
	return big.NewInt(0)
}

func (hc *HeaderChain) UncledLogEntropy(block *types.WorkObject) *big.Int {
	// Calculate the uncles' log entropy for the given block
	totalUncledLogS := big.NewInt(0)
	for _, uncle := range block.Uncles() {
		// After the fork the sha and scrypt shares do not count for the uncled
		// log entropy, only kawpow
		if uncle.AuxPow() != nil && uncle.AuxPow().PowID() > types.Kawpow {
			continue
		}
		// Verify the seal and get the powHash for the given header
		powHash, err := hc.VerifySeal(uncle)
		if err != nil {
			continue
		}
		intrinsicEntropy := common.IntrinsicLogEntropy(powHash)
		totalUncledLogS = new(big.Int).Add(totalUncledLogS, intrinsicEntropy)
	}
	return totalUncledLogS
}

func (hc *HeaderChain) WorkShareLogEntropy(wo *types.WorkObject) (*big.Int, error) {
	workShares := wo.Uncles()
	totalWsEntropy := big.NewInt(0)
	if wo.PrimeTerminusNumber().Uint64() < params.KawPowForkBlock {
		for _, ws := range workShares {
			engine := hc.GetEngineForHeader(ws)
			powHash, err := engine.ComputePowHash(ws)
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
				cBigBits := common.IntrinsicLogEntropy(powHash)
				thresholdBigBits := common.IntrinsicLogEntropy(common.BytesToHash(target.Bytes()))
				wsEntropy = new(big.Int).Sub(thresholdBigBits, cBigBits)
				extraBits := new(big.Float).Quo(new(big.Float).SetInt(wsEntropy), new(big.Float).SetInt(common.Big2e64))
				wsEntropyAdj := new(big.Float).Quo(new(big.Float).SetInt(common.Big2e64), bigMath.TwoToTheX(extraBits))
				wsEntropy, _ = wsEntropyAdj.Int(wsEntropy)
			} else {
				cBigBits := common.IntrinsicLogEntropy(powHash)
				thresholdBigBits := common.IntrinsicLogEntropy(common.BytesToHash(target.Bytes()))
				wsEntropy = new(big.Int).Sub(cBigBits, thresholdBigBits)
			}
			// Discount 2) applies to all shares regardless of the weight
			// a workshare cannot reference another workshare, it has to be either a block or an uncle
			// check that the parent hash referenced by the workshare is an uncle or a canonical block
			// then if its an uncle, traverse back until we hit a canonical block, other wise, use that
			// as a reference to calculate the distance
			distance, err := hc.WorkShareDistance(wo, ws)
			if err != nil {
				return big.NewInt(0), err
			}
			wsEntropy = new(big.Int).Div(wsEntropy, new(big.Int).Exp(big.NewInt(2), distance, nil))
			// Add the entropy into the total entropy once the discount calculation is done
			totalWsEntropy.Add(totalWsEntropy, wsEntropy)
		}
	} else {
		var bigBitsTotalShares *big.Int
		if len(workShares) > 0 {
			totalShares := big.NewInt(int64(len(workShares)))
			bigBitsTotalShares = common.LogBig(totalShares)

		} else {
			bigBitsTotalShares = big.NewInt(0)
		}
		// WorkshareLogEntropy = Alpha*Log2(TotalShares)
		totalWsEntropy = new(big.Int).Div(bigBitsTotalShares, params.AlphaInverse)

	}
	return totalWsEntropy, nil
}

func (hc *HeaderChain) UncledDeltaLogEntropy(header *types.WorkObject) *big.Int {
	// Treating the genesis block differntly
	if hc.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	_, order, err := hc.CalcOrder(header)
	if err != nil {
		hc.logger.WithField("err", err).Error("Error calculating order in UncledDeltaLogEntropy")
		return big.NewInt(0)
	}
	uncledLogS := header.UncledEntropy()
	switch order {
	case common.PRIME_CTX:
		return big.NewInt(0)
	case common.REGION_CTX:
		totalDeltaEntropy := new(big.Int).Add(header.ParentUncledDeltaEntropy(common.REGION_CTX), header.ParentUncledDeltaEntropy(common.ZONE_CTX))
		totalDeltaEntropy = new(big.Int).Add(totalDeltaEntropy, uncledLogS)
		return totalDeltaEntropy
	case common.ZONE_CTX:
		totalDeltaEntropy := new(big.Int).Add(header.ParentUncledDeltaEntropy(common.ZONE_CTX), uncledLogS)
		return totalDeltaEntropy
	}
	return big.NewInt(0)
}

// CalcRank returns the rank of the block within the hierarchy of chains, this
// determines the level of the interlink
func (hc *HeaderChain) CalcRank(header *types.WorkObject) (int, error) {
	if hc.IsGenesisHash(header.Hash()) {
		return 0, nil
	}
	_, order, err := hc.CalcOrder(header)
	if err != nil {
		return 0, err
	}
	if order != common.PRIME_CTX {
		return 0, errors.New("rank cannot be computed for a non-prime block")
	}

	// Verify the seal and get the powHash for the given header
	powHash, err := hc.verifySeal(header.WorkObjectHeader())
	if err != nil {
		return 0, err
	}

	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
	zoneThresholdS := common.IntrinsicLogEntropy(common.BytesToHash(target.Bytes()))

	intrinsicS := common.IntrinsicLogEntropy(powHash)
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

func (hc *HeaderChain) CheckIfValidWorkShare(workShare *types.WorkObjectHeader) types.WorkShareValidity {

	if workShare.PrimeTerminusNumber().Uint64() < params.KawPowForkBlock {
		thresholdDiff := params.WorkSharesThresholdDiff
		if hc.CheckWorkThreshold(workShare, thresholdDiff) {
			return types.Valid
		} else if hc.CheckWorkThreshold(workShare, hc.powConfig.WorkShareThreshold) {
			return types.Sub
		} else {
			return types.Invalid
		}
	} else {
		// After the fork the workshare is determined by the sha and scrypt share targets
		workshareTarget := new(big.Int).Div(common.Big2e256, CalculateKawpowShareDiff(workShare))
		powHash, err := hc.ComputePowHash(workShare)
		if err != nil {
			return types.Invalid
		}
		if new(big.Int).SetBytes(powHash.Bytes()).Cmp(workshareTarget) <= 0 {
			return types.Valid
		} else if hc.CheckWorkThreshold(workShare, hc.powConfig.WorkShareThreshold) {
			return types.Sub
		} else {
			return types.Invalid
		}
	}
}

func (hc *HeaderChain) CheckWorkThreshold(workShare *types.WorkObjectHeader, workShareThresholdDiff int) bool {
	workShareMinTarget, err := consensus.CalcWorkShareThreshold(workShare, workShareThresholdDiff)
	if err != nil {
		return false
	}
	powHash, err := hc.ComputePowHash(workShare)
	if err != nil {
		return false
	}
	return new(big.Int).SetBytes(powHash.Bytes()).Cmp(workShareMinTarget) <= 0
}
