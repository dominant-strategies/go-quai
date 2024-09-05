package blake3pow

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
func (blake3pow *Blake3pow) CalcOrder(chain consensus.BlockReader, header *types.WorkObject) (*big.Int, int, error) {
	nodeCtx := blake3pow.config.NodeLocation.Context()
	if header.NumberU64(nodeCtx) == 0 {
		return big0, common.PRIME_CTX, nil
	}
	// Need to use the prime terminus expansion number
	primeTerminusBlock := chain.GetBlockByHash(header.PrimeTerminusHash())
	if primeTerminusBlock == nil {
		return big0, -1, errors.New("prime terminus cannot be found during the calc order")
	}
	expansionNum := primeTerminusBlock.ExpansionNumber()

	// Verify the seal and get the powHash for the given header
	err := blake3pow.verifySeal(header.WorkObjectHeader())
	if err != nil {
		return big0, -1, err
	}

	// Get entropy reduction of this header
	intrinsicEntropy := blake3pow.IntrinsicLogEntropy(header.Hash())
	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
	zoneThresholdS := blake3pow.IntrinsicLogEntropy(common.BytesToHash(target.Bytes()))

	// PRIME
	// PrimeEntropyThreshold number of zone blocks times the intrinsic logs of
	// the given header determines the prime block
	totalDeltaEntropyPrime := new(big.Int).Add(header.ParentDeltaEntropy(common.REGION_CTX), header.ParentDeltaEntropy(common.ZONE_CTX))
	totalDeltaEntropyPrime = new(big.Int).Add(totalDeltaEntropyPrime, intrinsicEntropy)
	primeDeltaEntropyTarget := new(big.Int).Div(params.PrimeEntropyTarget(expansionNum), big2)
	primeDeltaEntropyTarget = new(big.Int).Mul(zoneThresholdS, primeDeltaEntropyTarget)

	primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(params.PrimeEntropyTarget(expansionNum)))
	if intrinsicEntropy.Cmp(primeBlockEntropyThreshold) > 0 && totalDeltaEntropyPrime.Cmp(primeDeltaEntropyTarget) > 0 {
		return intrinsicEntropy, common.PRIME_CTX, nil
	}

	// REGION
	// Compute the total accumulated entropy since the last region block
	totalDeltaEntropyRegion := new(big.Int).Add(header.ParentDeltaEntropy(common.ZONE_CTX), intrinsicEntropy)
	regionDeltaEntropyTarget := new(big.Int).Div(params.RegionEntropyTarget(expansionNum), big2)
	regionDeltaEntropyTarget = new(big.Int).Mul(zoneThresholdS, regionDeltaEntropyTarget)
	regionBlockEntropyThreshold := new(big.Int).Add(zoneThresholdS, common.BitsToBigBits(params.RegionEntropyTarget(expansionNum)))
	if intrinsicEntropy.Cmp(regionBlockEntropyThreshold) > 0 && totalDeltaEntropyRegion.Cmp(regionDeltaEntropyTarget) > 0 {
		return intrinsicEntropy, common.REGION_CTX, nil
	}

	// Zone case
	return intrinsicEntropy, common.ZONE_CTX, nil
}

// IntrinsicLogEntropy returns the logarithm of the intrinsic entropy reduction of a PoW hash
func (blake3pow *Blake3pow) IntrinsicLogEntropy(powHash common.Hash) *big.Int {
	x := new(big.Int).SetBytes(powHash.Bytes())
	d := new(big.Int).Div(big2e256, x)
	c, m := mathutil.BinaryLog(d, mantBits)
	bigBits := new(big.Int).Mul(big.NewInt(int64(c)), new(big.Int).Exp(big.NewInt(2), big.NewInt(mantBits), nil))
	bigBits = new(big.Int).Add(bigBits, m)
	return bigBits
}

// TotalLogEntropy returns the total entropy reduction if the chain since genesis to the given header
func (blake3pow *Blake3pow) TotalLogEntropy(chain consensus.ChainHeaderReader, header *types.WorkObject) *big.Int {
	// Treating the genesis block differntly
	if chain.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	intrinsicS, order, err := blake3pow.CalcOrder(chain, header)
	if err != nil {
		return big.NewInt(0)
	}
	if blake3pow.NodeLocation().Context() == common.ZONE_CTX {
		workShareS, err := blake3pow.WorkShareLogEntropy(chain, header)
		if err != nil {
			return big.NewInt(0)
		}
		intrinsicS = new(big.Int).Add(intrinsicS, workShareS)
	}
	switch order {
	case common.PRIME_CTX:
		totalS := new(big.Int).Add(header.ParentEntropy(common.PRIME_CTX), header.ParentDeltaEntropy(common.REGION_CTX))
		totalS.Add(totalS, header.ParentDeltaEntropy(common.ZONE_CTX))
		totalS.Add(totalS, intrinsicS)
		return totalS
	case common.REGION_CTX:
		totalS := new(big.Int).Add(header.ParentEntropy(common.REGION_CTX), header.ParentDeltaEntropy(common.ZONE_CTX))
		totalS.Add(totalS, intrinsicS)
		return totalS
	case common.ZONE_CTX:
		totalS := new(big.Int).Add(header.ParentEntropy(common.ZONE_CTX), intrinsicS)
		return totalS
	}
	return big.NewInt(0)
}

func (blake3pow *Blake3pow) DeltaLogEntropy(chain consensus.ChainHeaderReader, header *types.WorkObject) *big.Int {
	// Treating the genesis block differntly
	if chain.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	intrinsicS, order, err := blake3pow.CalcOrder(chain, header)
	if err != nil {
		return big.NewInt(0)
	}
	if blake3pow.NodeLocation().Context() == common.ZONE_CTX {
		workShareS, err := blake3pow.WorkShareLogEntropy(chain, header)
		if err != nil {
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

func (blake3pow *Blake3pow) UncledLogEntropy(block *types.WorkObject) *big.Int {
	uncles := block.Uncles()
	totalUncledLogS := big.NewInt(0)
	for _, uncle := range uncles {
		// Verify the seal and get the powHash for the given header
		err := blake3pow.verifySeal(uncle)
		if err != nil {
			continue
		}
		// Get entropy reduction of this header
		intrinsicEntropy := blake3pow.IntrinsicLogEntropy(uncle.Hash())
		totalUncledLogS.Add(totalUncledLogS, intrinsicEntropy)
	}
	return totalUncledLogS
}

func (blake3pow *Blake3pow) WorkShareLogEntropy(chain consensus.ChainHeaderReader, wo *types.WorkObject) (*big.Int, error) {
	workShares := wo.Uncles()
	totalWsEntropy := big.NewInt(0)
	for _, ws := range workShares {
		powHash, err := blake3pow.ComputePowHash(ws)
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

			cBigBits := blake3pow.IntrinsicLogEntropy(powHash)
			thresholdBigBits := blake3pow.IntrinsicLogEntropy(common.BytesToHash(target.Bytes()))
			wsEntropy = new(big.Int).Sub(thresholdBigBits, cBigBits)
			extraBits := new(big.Float).Quo(new(big.Float).SetInt(wsEntropy), new(big.Float).SetInt(common.Big2e64))
			wsEntropyAdj := new(big.Float).Quo(new(big.Float).SetInt(common.Big2e64), bigMath.TwoToTheX(extraBits))
			wsEntropy, _ = wsEntropyAdj.Int(wsEntropy)
		} else {
			wsEntropy = new(big.Int).Set(blake3pow.IntrinsicLogEntropy(powHash))
		}
		// Discount 2) applies to all shares regardless of the weight
		// a workshare cannot reference another workshare, it has to be either a block or an uncle
		// check that the parent hash referenced by the workshare is an uncle or a canonical block
		// then if its an uncle, traverse back until we hit a canonical block, other wise, use that
		// as a reference to calculate the distance
		distance, err := chain.WorkShareDistance(wo, ws)
		if err != nil {
			return big.NewInt(0), err
		}
		wsEntropy = new(big.Int).Div(wsEntropy, new(big.Int).Exp(big.NewInt(2), distance, nil))
		// Add the entropy into the total entropy once the discount calculation is done
		totalWsEntropy.Add(totalWsEntropy, wsEntropy)

	}
	return totalWsEntropy, nil
}

func (blake3pow *Blake3pow) UncledDeltaLogEntropy(chain consensus.ChainHeaderReader, header *types.WorkObject) *big.Int {
	// Treating the genesis block differntly
	if chain.IsGenesisHash(header.Hash()) {
		return big.NewInt(0)
	}
	_, order, err := blake3pow.CalcOrder(chain, header)
	if err != nil {
		return big.NewInt(0)
	}
	uncledLogEntropy := header.UncledEntropy()
	switch order {
	case common.PRIME_CTX:
		return big.NewInt(0)
	case common.REGION_CTX:
		totalDeltaEntropy := new(big.Int).Add(header.ParentUncledDeltaEntropy(common.REGION_CTX), header.ParentUncledDeltaEntropy(common.ZONE_CTX))
		totalDeltaEntropy = new(big.Int).Add(totalDeltaEntropy, uncledLogEntropy)
		return totalDeltaEntropy
	case common.ZONE_CTX:
		totalDeltaEntropy := new(big.Int).Add(header.ParentUncledDeltaEntropy(common.ZONE_CTX), uncledLogEntropy)
		return totalDeltaEntropy
	}
	return big.NewInt(0)
}

// CalcRank returns the rank of the block within the hierarchy of chains, this
// determines the level of the interlink
func (blake3pow *Blake3pow) CalcRank(chain consensus.ChainHeaderReader, header *types.WorkObject) (int, error) {
	if chain.IsGenesisHash(header.Hash()) {
		return 0, nil
	}
	_, order, err := blake3pow.CalcOrder(chain, header)
	if err != nil {
		return 0, err
	}
	if order != common.PRIME_CTX {
		return 0, errors.New("rank cannot be computed for a non-prime block")
	}

	powHash := header.Hash()
	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
	zoneThresholdEntropy := blake3pow.IntrinsicLogEntropy(common.BytesToHash(target.Bytes()))

	intrinsicEntropy := blake3pow.IntrinsicLogEntropy(powHash)
	for i := common.InterlinkDepth; i > 0; i-- {
		extraBits := math.Pow(2, float64(i))
		primeBlockEntropyThreshold := new(big.Int).Add(zoneThresholdEntropy, common.BitsToBigBits(big.NewInt(int64(extraBits))))
		primeBlockEntropyThreshold = new(big.Int).Add(primeBlockEntropyThreshold, common.BitsToBigBits(params.PrimeEntropyTarget(header.ExpansionNumber())))
		if intrinsicEntropy.Cmp(primeBlockEntropyThreshold) > 0 {
			return i, nil
		}
	}

	return 0, nil
}

func (blake3pow *Blake3pow) CheckIfValidWorkShare(workShare *types.WorkObjectHeader) bool {
	// Extract some data from the header
	diff := new(big.Int).Set(workShare.Difficulty())
	c, _ := mathutil.BinaryLog(diff, mantBits)
	if c <= params.WorkSharesThresholdDiff {
		return false
	}
	workShareThreshold := c - params.WorkSharesThresholdDiff
	workShareDiff := new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(workShareThreshold)), nil)
	workShareMintarget := new(big.Int).Div(big2e256, workShareDiff)
	powHash, err := blake3pow.ComputePowHash(workShare)
	if err != nil {
		return false
	}
	return new(big.Int).SetBytes(powHash.Bytes()).Cmp(workShareMintarget) <= 0
}
