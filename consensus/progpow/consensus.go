package progpow

import (
	"bytes"
	"fmt"
	"math/big"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/multiset"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	"google.golang.org/protobuf/proto"
	"modernc.org/mathutil"
)

// Progpow proof-of-work protocol constants.
var (
	allowedFutureBlockTimeSeconds = int64(15) // Max seconds from current time allowed for blocks, before they're considered future blocks

	ContextTimeFactor = big10
	ZoneBlockReward   = big.NewInt(5e+18)
	RegionBlockReward = new(big.Int).Mul(ZoneBlockReward, big3)
	PrimeBlockReward  = new(big.Int).Mul(RegionBlockReward, big3)
)

// Some useful constants to avoid constant memory allocs for them.
var (
	expDiffPeriod = big.NewInt(100000)
	big0          = big.NewInt(0)
	big1          = big.NewInt(1)
	big2          = big.NewInt(2)
	big3          = big.NewInt(3)
	big8          = big.NewInt(8)
	big9          = big.NewInt(9)
	big10         = big.NewInt(10)
	big32         = big.NewInt(32)
	bigMinus99    = big.NewInt(-99)
	big2e256      = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0)) // 2^256
)

// Author implements consensus.Engine, returning the header's coinbase as the
// proof-of-work verified author of the block.
func (progpow *Progpow) Author(header *types.WorkObject) (common.Address, error) {
	return header.PrimaryCoinbase(), nil
}

// VerifyHeader checks whether a header conforms to the consensus rules of the
// stock Quai progpow engine.
func (progpow *Progpow) VerifyHeader(chain consensus.ChainHeaderReader, header *types.WorkObject) error {
	nodeCtx := progpow.NodeLocation().Context()
	// If we're running a full engine faking, accept any input as valid
	if progpow.config.PowMode == ModeFullFake {
		return nil
	}
	// Short circuit if the header is known, or its parent not
	if chain.GetHeaderByHash(header.Hash()) != nil {
		return nil
	}
	parent := chain.GetBlockByHash(header.ParentHash(nodeCtx))
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	// Sanity checks passed, do a proper verification
	return progpow.verifyHeader(chain, header, parent, false, time.Now().Unix())
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
// concurrently. The method returns a quit channel to abort the operations and
// a results channel to retrieve the async verifications.
func (progpow *Progpow) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.WorkObject) (chan<- struct{}, <-chan error) {
	// If we're running a full engine faking, accept any input as valid
	if progpow.config.PowMode == ModeFullFake || len(headers) == 0 {
		abort, results := make(chan struct{}), make(chan error, len(headers))
		for i := 0; i < len(headers); i++ {
			results <- nil
		}
		return abort, results
	}

	// Spawn as many workers as allowed threads
	workers := runtime.GOMAXPROCS(0)
	if len(headers) < workers {
		workers = len(headers)
	}

	// Create a task channel and spawn the verifiers
	var (
		inputs  = make(chan int)
		done    = make(chan int, workers)
		errors  = make([]error, len(headers))
		abort   = make(chan struct{})
		unixNow = time.Now().Unix()
	)
	for i := 0; i < workers; i++ {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					progpow.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Fatal("Go-Quai Panicked")
				}
			}()
			for index := range inputs {
				errors[index] = progpow.verifyHeaderWorker(chain, headers, index, unixNow)
				done <- index
			}
		}()
	}

	errorsOut := make(chan error, len(headers))
	go func() {
		defer func() {
			if r := recover(); r != nil {
				progpow.logger.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Fatal("Go-Quai Panicked")
			}
		}()
		defer close(inputs)
		var (
			in, out = 0, 0
			checked = make([]bool, len(headers))
			inputs  = inputs
		)
		for {
			select {
			case inputs <- in:
				if in++; in == len(headers) {
					// Reached end of headers. Stop sending to workers.
					inputs = nil
				}
			case index := <-done:
				for checked[index] = true; checked[out]; out++ {
					errorsOut <- errors[out]
					if out == len(headers)-1 {
						return
					}
				}
			case <-abort:
				return
			}
		}
	}()
	return abort, errorsOut
}

func (progpow *Progpow) verifyHeaderWorker(chain consensus.ChainHeaderReader, headers []*types.WorkObject, index int, unixNow int64) error {
	nodeCtx := progpow.NodeLocation().Context()
	var parent *types.WorkObject
	if index == 0 {
		parent = chain.GetHeaderByHash(headers[0].ParentHash(nodeCtx))
	} else if headers[index-1].Hash() == headers[index].ParentHash(nodeCtx) {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	return progpow.verifyHeader(chain, headers[index], parent, false, unixNow)
}

// VerifyUncles verifies that the given block's uncles conform to the consensus
// rules of the stock Quai progpow engine.
func (progpow *Progpow) VerifyUncles(chain consensus.ChainReader, block *types.WorkObject) error {
	nodeCtx := progpow.NodeLocation().Context()
	// If we're running a full engine faking, accept any input as valid
	if progpow.config.PowMode == ModeFullFake {
		return nil
	}
	// Verify that there are at most params.MaxWorkShareCount uncles included in this block
	if len(block.Uncles()) > params.MaxWorkShareCount {
		return consensus.ErrTooManyUncles
	}
	if len(block.Uncles()) == 0 {
		return nil
	}
	// Gather the set of past uncles and ancestors
	uncles, ancestors := mapset.NewSet(), make(map[common.Hash]*types.WorkObject)

	number, parent := block.NumberU64(nodeCtx)-1, block.ParentHash(nodeCtx)
	for i := 0; i < params.WorkSharesInclusionDepth; i++ {
		ancestorHeader := chain.GetHeaderByHash(parent)
		if ancestorHeader == nil {
			break
		}
		ancestors[parent] = ancestorHeader
		// If the ancestor doesn't have any uncles, we don't have to iterate them
		if ancestorHeader.UncleHash() != types.EmptyUncleHash {
			// Need to add those uncles to the banned list too
			ancestor := chain.GetWorkObjectWithWorkShares(parent)
			if ancestor == nil {
				break
			}
			for _, uncle := range ancestor.Uncles() {
				uncles.Add(uncle.Hash())
			}
		}
		parent, number = ancestorHeader.ParentHash(nodeCtx), number-1
	}
	ancestors[block.Hash()] = block
	uncles.Add(block.Hash())

	// Verify each of the uncles that it's recent, but not an ancestor
	for _, uncle := range block.Uncles() {
		// Make sure every uncle is rewarded only once
		hash := uncle.Hash()
		if uncles.Contains(hash) {
			return consensus.ErrDuplicateUncle
		}
		uncles.Add(hash)

		// Make sure the uncle has a valid ancestry
		if ancestors[hash] != nil {
			return consensus.ErrUncleIsAncestor
		}
		// Siblings are not allowed to be included in the workshares list if its an
		// uncle but can be if its a workshare
		var workShare bool
		_, err := progpow.VerifySeal(uncle)
		if err != nil {
			workShare = true
		}
		if ancestors[uncle.ParentHash()] == nil || (!workShare && (uncle.ParentHash() == block.ParentHash(nodeCtx))) {
			return consensus.ErrDanglingUncle
		}
		_, err = progpow.ComputePowHash(uncle)
		if err != nil {
			return err
		}

		_, err = chain.WorkShareDistance(block, uncle)
		if err != nil {
			return err
		}

		// Verify the block's difficulty based on its timestamp and parent's difficulty
		// difficulty adjustment can only be checked in zone
		if nodeCtx == common.ZONE_CTX {
			parent := chain.GetHeaderByHash(uncle.ParentHash())
			expected := progpow.CalcDifficulty(chain, parent.WorkObjectHeader(), parent.ExpansionNumber())
			if expected.Cmp(uncle.Difficulty()) != 0 {
				return fmt.Errorf("uncle has invalid difficulty: have %v, want %v", uncle.Difficulty(), expected)
			}

			// Verify that the work share number is parent's +1
			parentNumber := parent.Number(nodeCtx)
			if chain.IsGenesisHash(parent.Hash()) {
				parentNumber = big.NewInt(0)
			}
			if diff := new(big.Int).Sub(uncle.Number(), parentNumber); diff.Cmp(big.NewInt(1)) != 0 {
				return consensus.ErrInvalidNumber
			}
		}
	}
	return nil
}

// verifyHeader checks whether a header conforms to the consensus rules
func (progpow *Progpow) verifyHeader(chain consensus.ChainHeaderReader, header, parent *types.WorkObject, uncle bool, unixNow int64) error {
	nodeCtx := progpow.NodeLocation().Context()
	// Ensure that the header's extra-data section is of a reasonable size
	if uint64(len(header.Extra())) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra-data too long: %d > %d", len(header.Extra()), params.MaximumExtraDataSize)
	}
	// verify that the hash of the header in the Body matches the header hash specified in the work object header
	expectedHeaderHash := header.Body().Header().Hash()
	if header.HeaderHash() != expectedHeaderHash {
		return fmt.Errorf("invalid header hash: have %v, want %v", header.HeaderHash(), expectedHeaderHash)
	}
	// Verify the header's timestamp
	if !uncle {
		if header.Time() > uint64(unixNow+allowedFutureBlockTimeSeconds) {
			return consensus.ErrFutureBlock
		}
	}
	if header.Time() < parent.Time() {
		return consensus.ErrOlderBlockTime
	}
	// Verify the block's difficulty based on its timestamp and parent's difficulty
	// difficulty adjustment can only be checked in zone
	if nodeCtx == common.ZONE_CTX {
		expected := progpow.CalcDifficulty(chain, parent.WorkObjectHeader(), parent.ExpansionNumber())
		if expected.Cmp(header.Difficulty()) != 0 {
			return fmt.Errorf("invalid difficulty: have %v, want %v", header.Difficulty(), expected)
		}
	}
	// Verify the engine specific seal securing the block
	_, order, err := progpow.CalcOrder(chain, parent)
	if err != nil {
		return err
	}
	if order > nodeCtx {
		return fmt.Errorf("order of the block is greater than the context")
	}

	if !chain.Config().Location.InSameSliceAs(header.Location()) {
		return fmt.Errorf("block location is not in the same slice as the node location")
	}
	// Verify that the parent entropy is calculated correctly on the header
	parentEntropy := progpow.TotalLogEntropy(chain, parent)
	if parentEntropy.Cmp(header.ParentEntropy(nodeCtx)) != 0 {
		return fmt.Errorf("invalid parent entropy: have %v, want %v", header.ParentEntropy(nodeCtx), parentEntropy)
	}
	// If not prime, verify the parentDeltaEntropy field as well
	if nodeCtx > common.PRIME_CTX {
		_, parentOrder, _ := progpow.CalcOrder(chain, parent)
		// If parent was dom, deltaEntropy is zero and otherwise should be the calc delta entropy on the parent
		if parentOrder < nodeCtx {
			if common.Big0.Cmp(header.ParentDeltaEntropy(nodeCtx)) != 0 {
				return fmt.Errorf("invalid parent delta entropy: have %v, want %v", header.ParentDeltaEntropy(nodeCtx), common.Big0)
			}
		} else {
			parentDeltaEntropy := progpow.DeltaLogEntropy(chain, parent)
			if parentDeltaEntropy.Cmp(header.ParentDeltaEntropy(nodeCtx)) != 0 {
				return fmt.Errorf("invalid parent delta entropy: have %v, want %v", header.ParentDeltaEntropy(nodeCtx), parentDeltaEntropy)
			}
		}
	}
	// If not prime, verify the parentUncledDeltaEntropy field as well
	if nodeCtx > common.PRIME_CTX {
		_, parentOrder, _ := progpow.CalcOrder(chain, parent)
		// If parent was dom, parent uncled sub deltaEntropy is zero and otherwise should be the calc parent uncled sub delta entropy on the parent
		if parentOrder < nodeCtx {
			if common.Big0.Cmp(header.ParentUncledDeltaEntropy(nodeCtx)) != 0 {
				return fmt.Errorf("invalid parent uncled sub delta entropy: have %v, want %v", header.ParentUncledDeltaEntropy(nodeCtx), common.Big0)
			}
		} else {
			expectedParentUncledDeltaEntropy := progpow.UncledDeltaLogEntropy(chain, parent)
			if expectedParentUncledDeltaEntropy.Cmp(header.ParentUncledDeltaEntropy(nodeCtx)) != 0 {
				return fmt.Errorf("invalid parent uncled sub delta entropy: have %v, want %v", header.ParentUncledDeltaEntropy(nodeCtx), expectedParentUncledDeltaEntropy)
			}
		}
	}
	// verify efficiency score, threshold count and the expansion number, on every prime block
	if nodeCtx == common.PRIME_CTX {
		if parent.NumberU64(nodeCtx) == 0 {
			if header.EfficiencyScore() != 0 {
				return fmt.Errorf("invalid efficiency score: have %v, want %v", header.EfficiencyScore(), 0)
			}
			if header.ThresholdCount() != 0 {
				return fmt.Errorf("invalid threshold count: have %v, want %v", header.ThresholdCount(), 0)
			}
		} else {
			expectedEfficiencyScore := chain.ComputeEfficiencyScore(parent)
			if header.EfficiencyScore() != expectedEfficiencyScore {
				return fmt.Errorf("invalid efficiency score: have %v, want %v", header.EfficiencyScore(), expectedEfficiencyScore)
			}

			var expectedThresholdCount uint16
			if parent.ThresholdCount() == 0 {
				// If the threshold count is zero we have not started considering for the
				// expansion
				if expectedEfficiencyScore > params.TREE_EXPANSION_THRESHOLD {
					expectedThresholdCount = parent.ThresholdCount() + 1
				} else {
					expectedThresholdCount = 0
				}
			} else {
				// If the efficiency score goes below the threshold,  and we still have
				// not triggered the expansion, reset the threshold count or if we go
				// past the tree expansion trigger window we have to reset the
				// threshold count
				if (parent.ThresholdCount() < params.TREE_EXPANSION_TRIGGER_WINDOW && expectedEfficiencyScore < params.TREE_EXPANSION_THRESHOLD) ||
					parent.ThresholdCount() == params.TREE_EXPANSION_TRIGGER_WINDOW+params.TREE_EXPANSION_WAIT_COUNT {
					expectedThresholdCount = 0
				} else {
					expectedThresholdCount = parent.ThresholdCount() + 1
				}
			}
			if header.ThresholdCount() != expectedThresholdCount {
				return fmt.Errorf("invalid threshold count: have %v, want %v", header.ThresholdCount(), expectedThresholdCount)
			}

		}
	}
	// verify the etx eligible slices in zone and prime ctx
	if nodeCtx == common.PRIME_CTX {
		var expectedEtxEligibleSlices common.Hash
		if !chain.IsGenesisHash(parent.Hash()) {
			expectedEtxEligibleSlices = chain.UpdateEtxEligibleSlices(parent, parent.Location())
		} else {
			expectedEtxEligibleSlices = parent.EtxEligibleSlices()
		}
		if header.EtxEligibleSlices() != expectedEtxEligibleSlices {
			return fmt.Errorf("invalid etx eligible slices: have %v, want %v", header.EtxEligibleSlices(), expectedEtxEligibleSlices)
		}
	}

	if nodeCtx == common.ZONE_CTX {
		var expectedExpansionNumber uint8
		expectedExpansionNumber, err := chain.ComputeExpansionNumber(parent)
		if err != nil {
			return err
		}
		if header.ExpansionNumber() != expectedExpansionNumber {
			return fmt.Errorf("invalid expansion number: have %v, want %v", header.ExpansionNumber(), expectedExpansionNumber)
		}
	}

	if nodeCtx == common.ZONE_CTX {

		// check if the header coinbase is in scope
		_, err := header.PrimaryCoinbase().InternalAddress()
		if err != nil {
			return fmt.Errorf("out-of-scope primary coinbase in the header: %v location: %v nodeLocation: %v, err %s", header.PrimaryCoinbase(), header.Location(), progpow.config.NodeLocation, err)
		}
		_, err = header.SecondaryCoinbase().InternalAddress()
		if err != nil {
			return fmt.Errorf("out-of-scope secondary coinbase in the header: %v location: %v nodeLocation: %v, err %s", header.SecondaryCoinbase(), header.Location(), progpow.config.NodeLocation, err)
		}

		// One of the coinbases has to be Quai and the other one has to be Qi
		quaiAddress := header.PrimaryCoinbase().IsInQuaiLedgerScope()
		if quaiAddress {
			if !header.SecondaryCoinbase().IsInQiLedgerScope() {
				return fmt.Errorf("primary coinbase: %v is in quai ledger but secondary coinbase: %v is not in Qi ledger", header.PrimaryCoinbase(), header.SecondaryCoinbase())
			}
		} else {
			if !header.SecondaryCoinbase().IsInQuaiLedgerScope() {
				return fmt.Errorf("primary coinbase: %v is in qi ledger but secondary coinbase: %v is not in Quai ledger", header.PrimaryCoinbase(), header.SecondaryCoinbase())
			}
		}
		// Verify that the gas limit is <= 2^63-1
		cap := uint64(0x7fffffffffffffff)
		if header.GasLimit() > cap {
			return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit(), cap)
		}
		// Verify that the gasUsed is <= gasLimit
		if header.GasUsed() > header.GasLimit() {
			return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed(), header.GasLimit())
		}
		// Verify the block's gas usage and verify the base fee.
		// Verify that the gas limit remains within allowed bounds
		expectedGasLimit := core.CalcGasLimit(parent, progpow.config.GasCeil)
		if expectedGasLimit != header.GasLimit() {
			return fmt.Errorf("invalid gasLimit: have %d, want %d",
				header.GasLimit(), expectedGasLimit)
		}
		// Verify that the stateUsed is <= stateLimit
		if header.StateUsed() > header.StateLimit() {
			return fmt.Errorf("invalid stateUsed: have %d, stateLimit %d", header.StateUsed(), header.StateLimit())
		}
		// Verify the StateLimit is correct based on the parent header.
		expectedStateLimit := misc.CalcStateLimit(parent, params.StateCeil)
		if header.StateLimit() != expectedStateLimit {
			return fmt.Errorf("invalid StateLimit: have %d, want %d, parentStateLimit %d", expectedStateLimit, header.StateLimit(), parent.StateLimit())
		}
		var expectedPrimeTerminusHash common.Hash
		var expectedPrimeTerminusNumber *big.Int
		_, parentOrder, _ := progpow.CalcOrder(chain, parent)
		if parentOrder == common.PRIME_CTX {
			expectedPrimeTerminusHash = parent.Hash()
			expectedPrimeTerminusNumber = parent.Number(common.PRIME_CTX)
		} else {
			if chain.IsGenesisHash(parent.Hash()) {
				expectedPrimeTerminusHash = parent.Hash()
				expectedPrimeTerminusNumber = parent.Number(common.PRIME_CTX)
			} else {
				expectedPrimeTerminusHash = parent.PrimeTerminusHash()
				expectedPrimeTerminusNumber = parent.PrimeTerminusNumber()
			}
		}
		if header.PrimeTerminusHash() != expectedPrimeTerminusHash {
			return fmt.Errorf("invalid primeTerminusHash: have %v, want %v", header.PrimeTerminusHash(), expectedPrimeTerminusHash)
		}
		if header.PrimeTerminusNumber().Cmp(expectedPrimeTerminusNumber) != 0 {
			return fmt.Errorf("invalid primeTerminusNumber: have %v, want %v", header.PrimeTerminusNumber(), expectedPrimeTerminusNumber)
		}
	}
	// Verify that the block number is parent's +1
	parentNumber := parent.Number(nodeCtx)
	if chain.IsGenesisHash(parent.Hash()) {
		parentNumber = big.NewInt(0)
	}
	// Verify that the block number is parent's +1
	if diff := new(big.Int).Sub(header.Number(nodeCtx), parentNumber); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}
	return nil
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
func (progpow *Progpow) CalcDifficulty(chain consensus.ChainHeaderReader, parent *types.WorkObjectHeader, expansionNum uint8) *big.Int {
	nodeCtx := progpow.NodeLocation().Context()

	if nodeCtx != common.ZONE_CTX {
		progpow.logger.WithField("context", nodeCtx).Error("Cannot CalcDifficulty for non-zone context")
		return nil
	}

	///// Algorithm:
	///// e = (DurationLimit - (parent.Time() - parentOfParent.Time())) * parent.Difficulty()
	///// k = Floor(BinaryLog(parent.Difficulty()))/(DurationLimit*DifficultyAdjustmentFactor*AdjustmentPeriod)
	///// Difficulty = Max(parent.Difficulty() + e * k, MinimumDifficulty)

	if chain.IsGenesisHash(parent.Hash()) {
		// Divide the parent difficulty by the number of slices running at the time of expansion
		if expansionNum == 0 && parent.Location().Equal(common.Location{}) {
			// Base case: expansion number is 0 and the parent is the actual genesis block
			return parent.Difficulty()
		}
		genesisBlock := chain.GetHeaderByHash(parent.Hash())
		if genesisBlock.ExpansionNumber() > 0 && parent.Hash() == chain.Config().DefaultGenesisHash {
			return parent.Difficulty()
		}
		genesis := chain.GetHeaderByHash(parent.Hash())
		genesisTotalLogEntropy := progpow.TotalLogEntropy(chain, genesis)
		if genesisTotalLogEntropy.Cmp(genesis.ParentEntropy(common.PRIME_CTX)) < 0 { // prevent negative difficulty
			progpow.logger.Errorf("Genesis block has invalid parent entropy: %v", genesis.ParentEntropy(common.PRIME_CTX))
			return nil
		}
		differenceParentEntropy := new(big.Int).Sub(genesisTotalLogEntropy, genesis.ParentEntropy(common.PRIME_CTX))
		numBlocks := params.PrimeEntropyTarget(expansionNum)
		differenceParentEntropy.Div(differenceParentEntropy, numBlocks)
		return common.EntropyBigBitsToDifficultyBits(differenceParentEntropy)
	}
	parentOfParent := chain.GetHeaderByHash(parent.ParentHash())
	if parentOfParent == nil || chain.IsGenesisHash(parentOfParent.Hash()) {
		return parent.Difficulty()
	}

	time := parent.Time()
	bigTime := new(big.Int).SetUint64(time)
	bigParentTime := new(big.Int).SetUint64(parentOfParent.Time())

	// Bound the time diff so that the difficulty doesnt have huge discontinuity
	// in the values
	timeDiff := new(big.Int).Sub(bigTime, bigParentTime)
	if timeDiff.Cmp(big.NewInt(params.MaxTimeDiffBetweenBlocks)) > 0 {
		timeDiff = big.NewInt(params.MaxTimeDiffBetweenBlocks)
	}

	// holds intermediate values to make the algo easier to read & audit
	x := new(big.Int).Set(timeDiff)
	x.Sub(progpow.config.DurationLimit, x)
	x.Mul(x, parent.Difficulty())
	k, _ := mathutil.BinaryLog(new(big.Int).Set(parent.Difficulty()), 64)
	x.Mul(x, big.NewInt(int64(k)))
	x.Div(x, progpow.config.DurationLimit)
	x.Div(x, big.NewInt(params.DifficultyAdjustmentFactor))
	x.Div(x, params.DifficultyAdjustmentPeriod)
	x.Add(x, parent.Difficulty())

	// minimum difficulty can ever be (before exponential factor)
	if x.Cmp(progpow.config.MinDifficulty) < 0 {
		x.Set(progpow.config.MinDifficulty)
	}
	return x
}

func (progpow *Progpow) IsDomCoincident(chain consensus.ChainHeaderReader, header *types.WorkObject) bool {
	_, order, err := progpow.CalcOrder(chain, header)
	if err != nil {
		progpow.logger.WithField("err", err).Error("Error calculating order in IsDomCoincident")
		return false
	}
	return order < chain.Config().Location.Context()
}

func (progpow *Progpow) ComputePowLight(header *types.WorkObjectHeader) (mixHash, powHash common.Hash) {
	hashes, ok := progpow.hashCache.Peek(header.Hash())
	if ok {
		return common.Hash(hashes.mixHash), common.Hash(hashes.workHash)
	}
	powLight := func(size uint64, cache []uint32, hash common.Hash, nonce uint64, blockNumber uint64) ([]byte, []byte) {
		ethashCache := progpow.cache(blockNumber)
		if ethashCache.cDag == nil {
			cDag := make([]uint32, progpowCacheWords)
			generateCDag(cDag, ethashCache.cache, blockNumber/C_epochLength, progpow.logger)
			ethashCache.cDag = cDag
		}
		return progpowLight(size, cache, hash.Bytes(), nonce, blockNumber, ethashCache.cDag, progpow.lookupCache)
	}
	cache := progpow.cache(header.PrimeTerminusNumber().Uint64())
	size := datasetSize(header.PrimeTerminusNumber().Uint64())
	digest, result := powLight(size, cache.cache, header.SealHash(), header.NonceU64(), header.PrimeTerminusNumber().Uint64())
	mixHash = common.BytesToHash(digest)
	powHash = common.BytesToHash(result)
	header.PowDigest.Store(mixHash)
	header.PowHash.Store(powHash)

	// Cache the hash
	progpow.hashCache.Add(header.Hash(), mixHashWorkHash{mixHash: mixHash.Bytes(), workHash: powHash.Bytes()})
	// Caches are unmapped in a finalizer. Ensure that the cache stays alive
	// until after the call to hashimotoLight so it's not unmapped while being used.
	runtime.KeepAlive(cache)

	return mixHash, powHash
}

// VerifySeal returns the PowHash and the verifySeal output
func (progpow *Progpow) VerifySeal(header *types.WorkObjectHeader) (common.Hash, error) {
	return progpow.verifySeal(header)
}

// verifySeal checks whether a block satisfies the PoW difficulty requirements,
// either using the usual progpow cache for it, or alternatively using a full DAG
// to make remote mining fast.
func (progpow *Progpow) verifySeal(header *types.WorkObjectHeader) (common.Hash, error) {
	// If we're running a fake PoW, accept any seal as valid
	if progpow.config.PowMode == ModeFake || progpow.config.PowMode == ModeFullFake {
		time.Sleep(progpow.fakeDelay)
		if progpow.fakeFail == header.NumberU64() {
			return common.Hash{}, consensus.ErrInvalidPoW
		}
		return common.Hash{}, nil
	}
	// If we're running a shared PoW, delegate verification to it
	if progpow.shared != nil {
		return progpow.shared.verifySeal(header)
	}
	// Ensure that we have a valid difficulty for the block
	if header.Difficulty().Sign() <= 0 {
		return common.Hash{}, consensus.ErrInvalidDifficulty
	}
	powHash, err := progpow.ComputePowHash(header)
	if err != nil {
		return common.Hash{}, err
	}
	target := new(big.Int).Div(big2e256, header.Difficulty())
	if new(big.Int).SetBytes(powHash.Bytes()).Cmp(target) > 0 {
		return powHash, consensus.ErrInvalidPoW
	}
	return powHash, nil
}

func (progpow *Progpow) ComputePowHash(header *types.WorkObjectHeader) (common.Hash, error) {
	// Check progpow
	mixHash := header.PowDigest.Load()
	powHash := header.PowHash.Load()
	if powHash == nil || mixHash == nil {
		mixHash, powHash = progpow.ComputePowLight(header)
	}
	// Verify the calculated values against the ones provided in the header
	if !bytes.Equal(header.MixHash().Bytes(), mixHash.(common.Hash).Bytes()) {
		return common.Hash{}, consensus.ErrInvalidMixHash
	}
	return powHash.(common.Hash), nil
}

// Prepare implements consensus.Engine, initializing the difficulty field of a
// header to conform to the progpow protocol. The changes are done inline.
func (progpow *Progpow) Prepare(chain consensus.ChainHeaderReader, header *types.WorkObject, parent *types.WorkObject) error {
	header.WorkObjectHeader().SetDifficulty(progpow.CalcDifficulty(chain, parent.WorkObjectHeader(), parent.ExpansionNumber()))
	return nil
}

// Finalize implements consensus.Engine, accumulating the block and uncle rewards,
// setting the final state on the header
// Finalize returns the new MuHash of the UTXO set, the new size of the UTXO set and an error if any
func (progpow *Progpow) Finalize(chain consensus.ChainHeaderReader, batch ethdb.Batch, header *types.WorkObject, state *state.StateDB, setRoots bool, utxosCreate, utxosDelete []common.Hash) (*multiset.MultiSet, uint64, error) {
	nodeLocation := progpow.NodeLocation()
	nodeCtx := progpow.NodeLocation().Context()
	var multiSet *multiset.MultiSet
	var utxoSetSize uint64
	if chain.IsGenesisHash(header.ParentHash(nodeCtx)) {
		multiSet = multiset.New()
		// Create the lockup contract account
		lockupContract, err := vm.LockupContractAddresses[[2]byte{nodeLocation[0], nodeLocation[1]}].InternalAndQuaiAddress()
		if err != nil {
			return nil, 0, err
		}
		state.CreateAccount(lockupContract)
		state.SetNonce(lockupContract, 1) // so it's not considered "empty"

		alloc := core.ReadGenesisAlloc("genallocs/gen_alloc_quai_"+nodeLocation.Name()+".json", progpow.logger)
		progpow.logger.WithField("alloc", len(alloc)).Info("Allocating genesis accounts")

		for addressString, account := range alloc {
			addr := common.HexToAddress(addressString, nodeLocation)
			internal, err := addr.InternalAddress()
			if err != nil {
				progpow.logger.WithField("err", err).Error("Provided address in genesis block is out of scope")
				continue
			}
			if addr.IsInQuaiLedgerScope() {
				state.AddBalance(internal, account.Balance)
				state.SetCode(internal, account.Code)
				state.SetNonce(internal, account.Nonce)
				for key, value := range account.Storage {
					state.SetState(internal, key, value)
				}
			} else {
				progpow.logger.WithField("address", addr.String()).Error("Provided address in genesis block alloc is not in the Quai ledger scope")
				continue
			}
		}
		addressOutpointMap := make(map[string]map[string]*types.OutpointAndDenomination)
		core.AddGenesisUtxos(chain.Database(), &utxosCreate, nodeLocation, addressOutpointMap, progpow.logger)
		if chain.Config().IndexAddressUtxos {
			chain.WriteAddressOutpoints(addressOutpointMap)
			progpow.logger.Info("Indexed genesis utxos")
		}
	} else {
		multiSet = rawdb.ReadMultiSet(chain.Database(), header.ParentHash(nodeCtx))
		utxoSetSize = rawdb.ReadUTXOSetSize(chain.Database(), header.ParentHash(nodeCtx))
	}
	if multiSet == nil {
		return nil, 0, fmt.Errorf("Multiset is nil for block %s", header.ParentHash(nodeCtx).String())
	}
	utxoSetSize += uint64(len(utxosCreate))
	utxoSetSize -= uint64(len(utxosDelete))
	trimDepths := types.TrimDepths
	if utxoSetSize > params.SoftMaxUTXOSetSize/2 {
		var err error
		trimDepths, err = rawdb.ReadTrimDepths(chain.Database(), header.ParentHash(nodeCtx))
		if err != nil || trimDepths == nil {
			progpow.logger.Errorf("Failed to read trim depths for block %s: %+v", header.ParentHash(nodeCtx).String(), err)
			trimDepths = make(map[uint8]uint64, len(types.TrimDepths))
			for denomination, depth := range types.TrimDepths { // copy the default trim depths
				trimDepths[denomination] = depth
			}
		}
		if UpdateTrimDepths(trimDepths, utxoSetSize) {
			progpow.logger.Infof("Updated trim depths at height %d new depths: %+v", header.NumberU64(nodeCtx), trimDepths)
		}
		if !setRoots {
			rawdb.WriteTrimDepths(batch, header.Hash(), trimDepths)
		}
	}
	start := time.Now()
	collidingKeys, err := rawdb.ReadCollidingKeys(chain.Database(), header.ParentHash(nodeCtx))
	if err != nil {
		progpow.logger.Errorf("Failed to read colliding keys for block %s: %+v", header.ParentHash(nodeCtx).String(), err)
	}
	newCollidingKeys := make([][]byte, 0)
	trimmedUtxos := make([]*types.SpentUtxoEntry, 0)
	var wg sync.WaitGroup
	var lock sync.Mutex
	for denomination, depth := range trimDepths {
		if denomination <= types.MaxTrimDenomination && header.NumberU64(nodeCtx) > depth+1 {
			wg.Add(1)
			go func(denomination uint8, depth uint64) {
				defer func() {
					if r := recover(); r != nil {
						progpow.logger.WithFields(log.Fields{
							"error":      r,
							"stacktrace": string(debug.Stack()),
						}).Error("Go-Quai Panicked")
					}
				}()
				nextBlockToTrim := rawdb.ReadCanonicalHash(chain.Database(), header.NumberU64(nodeCtx)-depth)
				collisions := TrimBlock(chain, batch, denomination, true, header.NumberU64(nodeCtx)-depth, nextBlockToTrim, &utxosDelete, &trimmedUtxos, nil, &utxoSetSize, !setRoots, &lock, progpow.logger) // setRoots is false when we are processing the block
				if len(collisions) > 0 {
					lock.Lock()
					newCollidingKeys = append(newCollidingKeys, collisions...)
					lock.Unlock()
				}
				wg.Done()
			}(denomination, depth)
		}
	}
	if len(collidingKeys) > 0 {
		wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					progpow.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Error("Go-Quai Panicked")
				}
			}()
			// Trim colliding/duplicate keys here - an optimization could be to do this above in parallel with the other trims
			collisions := TrimBlock(chain, batch, 0, false, 0, common.Hash{}, &utxosDelete, &trimmedUtxos, collidingKeys, &utxoSetSize, !setRoots, &lock, progpow.logger)
			if len(collisions) > 0 {
				lock.Lock()
				newCollidingKeys = append(newCollidingKeys, collisions...)
				lock.Unlock()
			}
			wg.Done()
		}()
	}
	wg.Wait()
	progpow.logger.Infof("Trimmed %d UTXOs from db in %s", len(trimmedUtxos), common.PrettyDuration(time.Since(start)))
	if !setRoots {
		rawdb.WriteTrimmedUTXOs(batch, header.Hash(), trimmedUtxos)
		if len(newCollidingKeys) > 0 {
			rawdb.WriteCollidingKeys(batch, header.Hash(), newCollidingKeys)
		}
	}
	for _, hash := range utxosCreate {
		multiSet.Add(hash.Bytes())
	}
	for _, hash := range utxosDelete {
		multiSet.Remove(hash.Bytes())
	}
	progpow.logger.Infof("Parent hash: %s, header hash: %s, muhash: %s, block height: %d, setroots: %t, UtxosCreated: %d, UtxosDeleted: %d, UTXOs Trimmed from DB: %d, UTXO Set Size: %d", header.ParentHash(nodeCtx).String(), header.Hash().String(), multiSet.Hash().String(), header.NumberU64(nodeCtx), setRoots, len(utxosCreate), len(utxosDelete), len(trimmedUtxos), utxoSetSize)

	if utxoSetSize < uint64(len(utxosDelete)) {
		return nil, 0, fmt.Errorf("UTXO set size is less than the number of utxos to delete. This is a bug. UTXO set size: %d, UTXOs to delete: %d", utxoSetSize, len(utxosDelete))
	}

	if setRoots {
		header.Header().SetUTXORoot(multiSet.Hash())
		header.Header().SetEVMRoot(state.IntermediateRoot(true))
		header.Header().SetEtxSetRoot(state.ETXRoot())
		header.Header().SetQuaiStateSize(state.GetQuaiTrieSize())
	}
	return multiSet, utxoSetSize, nil
}

// TrimBlock trims all UTXOs of a given denomination that were created in a given block.
// In the event of an attacker intentionally creating too many 9-byte keys that collide, we return the colliding keys to be trimmed in the next block.
func TrimBlock(chain consensus.ChainHeaderReader, batch ethdb.Batch, denomination uint8, checkDenom bool, blockHeight uint64, blockHash common.Hash, utxosDelete *[]common.Hash, trimmedUtxos *[]*types.SpentUtxoEntry, collidingKeys [][]byte, utxoSetSize *uint64, deleteFromDb bool, lock *sync.Mutex, logger *log.Logger) [][]byte {
	utxosCreated, _ := rawdb.ReadPrunedUTXOKeys(chain.Database(), blockHeight)
	if len(utxosCreated) == 0 {
		// This should almost never happen, but we need to handle it
		utxosCreated, _ = rawdb.ReadCreatedUTXOKeys(chain.Database(), blockHash)
		logger.Infof("Reading non-pruned UTXOs for block %d", blockHeight)
		for i, key := range utxosCreated {
			if len(key) == rawdb.UtxoKeyWithDenominationLength {
				if key[len(key)-1] > types.MaxTrimDenomination {
					// Don't keep it if the denomination is not trimmed
					// The keys are sorted in order of denomination, so we can break here
					break
				}
				key[rawdb.PrunedUtxoKeyWithDenominationLength+len(rawdb.UtxoPrefix)-1] = key[len(key)-1] // place the denomination at the end of the pruned key (11th byte will become 9th byte)
			}
			// Reduce key size to 9 bytes and cut off the prefix
			key = key[len(rawdb.UtxoPrefix) : rawdb.PrunedUtxoKeyWithDenominationLength+len(rawdb.UtxoPrefix)]
			utxosCreated[i] = key
		}
	}

	logger.Infof("UTXOs created in block %d: %d", blockHeight, len(utxosCreated))
	if len(collidingKeys) > 0 {
		logger.Infof("Colliding keys: %d", len(collidingKeys))
		utxosCreated = append(utxosCreated, collidingKeys...)
		sort.Slice(utxosCreated, func(i, j int) bool {
			return utxosCreated[i][len(utxosCreated[i])-1] < utxosCreated[j][len(utxosCreated[j])-1]
		})
	}
	newCollisions := make([][]byte, 0)
	duplicateKeys := make(map[[36]byte]bool) // cannot use rawdb.UtxoKeyLength for map as it's not const
	// Start by grabbing all the UTXOs created in the block (that are still in the UTXO set)
	for _, key := range utxosCreated {
		if len(key) != rawdb.PrunedUtxoKeyWithDenominationLength {
			continue
		}
		if checkDenom {
			if key[len(key)-1] != denomination {
				if key[len(key)-1] > denomination {
					break // The keys are stored in order of denomination, so we can stop checking here
				} else {
					continue
				}
			} else {
				key = append(rawdb.UtxoPrefix, key...) // prepend the db prefix
				key = key[:len(key)-1]                 // remove the denomination byte
			}
		}
		// Check key in database
		i := 0
		it := chain.Database().NewIterator(key, nil)
		for it.Next() {
			data := it.Value()
			if len(data) == 0 {
				logger.Infof("Empty key found, denomination: %d", denomination)
				continue
			}
			// Check if the key is a duplicate
			if len(it.Key()) == rawdb.UtxoKeyLength {
				key36 := [36]byte(it.Key())
				if duplicateKeys[key36] {
					continue
				} else {
					duplicateKeys[key36] = true
				}
			}
			utxoProto := new(types.ProtoTxOut)
			if err := proto.Unmarshal(data, utxoProto); err != nil {
				logger.Errorf("Failed to unmarshal ProtoTxOut: %+v data: %+v key: %+v", err, data, key)
				continue
			}

			utxo := new(types.UtxoEntry)
			if err := utxo.ProtoDecode(utxoProto); err != nil {
				logger.WithFields(log.Fields{
					"key":  key,
					"data": data,
					"err":  err,
				}).Error("Invalid utxo Proto")
				continue
			}
			if checkDenom && utxo.Denomination != denomination {
				continue
			}
			txHash, index, err := rawdb.ReverseUtxoKey(it.Key())
			if err != nil {
				logger.WithField("err", err).Error("Failed to parse utxo key")
				continue
			}
			lock.Lock()
			*utxosDelete = append(*utxosDelete, types.UTXOHash(txHash, index, utxo))
			if deleteFromDb {
				batch.Delete(it.Key())
				*trimmedUtxos = append(*trimmedUtxos, &types.SpentUtxoEntry{OutPoint: types.OutPoint{txHash, index}, UtxoEntry: utxo})
			}
			*utxoSetSize--
			lock.Unlock()
			i++
			if i >= types.MaxTrimCollisionsPerKeyPerBlock {
				// This will rarely ever happen, but if it does, we should continue trimming this key in the next block
				logger.WithField("blockHeight", blockHeight).Error("MaxTrimCollisionsPerBlock exceeded")
				newCollisions = append(newCollisions, key)
				break
			}
		}
		it.Release()
	}
	return newCollisions
}

func UpdateTrimDepths(trimDepths map[uint8]uint64, utxoSetSize uint64) bool {
	switch {
	case utxoSetSize > params.SoftMaxUTXOSetSize/2 && trimDepths[255] == 0: // 50% full
		for denomination, depth := range trimDepths {
			trimDepths[denomination] = depth - (depth / 10) // decrease lifespan of this denomination by 10%
		}
		trimDepths[255] = 1 // level 1
	case utxoSetSize > params.SoftMaxUTXOSetSize-(params.SoftMaxUTXOSetSize/4) && trimDepths[255] == 1: // 75% full
		for denomination, depth := range trimDepths {
			trimDepths[denomination] = depth - (depth / 5) // decrease lifespan of this denomination by an additional 20%
		}
		trimDepths[255] = 2 // level 2
	case utxoSetSize > params.SoftMaxUTXOSetSize-(params.SoftMaxUTXOSetSize/10) && trimDepths[255] == 2: // 90% full
		for denomination, depth := range trimDepths {
			trimDepths[denomination] = depth - (depth / 2) // decrease lifespan of this denomination by an additional 50%
		}
		trimDepths[255] = 3 // level 3
	case utxoSetSize > params.SoftMaxUTXOSetSize && trimDepths[255] == 3:
		for denomination, depth := range trimDepths {
			trimDepths[denomination] = depth - (depth / 2) // decrease lifespan of this denomination by an additional 50%
		}
		trimDepths[255] = 4 // level 4

	// Resets
	case utxoSetSize <= params.SoftMaxUTXOSetSize/2 && trimDepths[255] == 1: // Below 50% full
		for denomination, depth := range types.TrimDepths { // reset to the default trim depths
			trimDepths[denomination] = depth
		}
		trimDepths[255] = 0 // level 0
	case utxoSetSize <= params.SoftMaxUTXOSetSize-(params.SoftMaxUTXOSetSize/4) && trimDepths[255] == 2: // Below 75% full
		for denomination, depth := range trimDepths {
			trimDepths[denomination] = depth + (depth / 5) // increase lifespan of this denomination by 20%
		}
		trimDepths[255] = 1 // level 1
	case utxoSetSize <= params.SoftMaxUTXOSetSize-(params.SoftMaxUTXOSetSize/10) && trimDepths[255] == 3: // Below 90% full
		for denomination, depth := range trimDepths {
			trimDepths[denomination] = depth + (depth / 2) // increase lifespan of this denomination by 50%
		}
		trimDepths[255] = 2 // level 2
	case utxoSetSize <= params.SoftMaxUTXOSetSize && trimDepths[255] == 4: // Below 100% full
		for denomination, depth := range trimDepths {
			trimDepths[denomination] = depth + (depth / 2) // increase lifespan of this denomination by 50%
		}
		trimDepths[255] = 3 // level 3
	default:
		return false
	}
	return true
}

// FinalizeAndAssemble implements consensus.Engine, accumulating the block and
// uncle rewards, setting the final state and assembling the block.
func (progpow *Progpow) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.WorkObject, state *state.StateDB, txs []*types.Transaction, uncles []*types.WorkObjectHeader, etxs []*types.Transaction, subManifest types.BlockManifest, receipts []*types.Receipt, parentUtxoSetSize uint64, utxosCreate, utxosDelete []common.Hash) (*types.WorkObject, error) {
	nodeCtx := progpow.config.NodeLocation.Context()
	if nodeCtx == common.ZONE_CTX && chain.ProcessingState() {
		// Finalize block
		if _, _, err := progpow.Finalize(chain, nil, header, state, true, utxosCreate, utxosDelete); err != nil {
			return nil, err
		}
	}

	woBody, err := types.NewWorkObjectBody(header.Header(), txs, etxs, uncles, subManifest, receipts, trie.NewStackTrie(nil), nodeCtx)
	if err != nil {
		return nil, err
	}
	// Header seems complete, assemble into a block and return
	return types.NewWorkObject(header.WorkObjectHeader(), woBody, nil), nil
}

func (progpow *Progpow) NodeLocation() common.Location {
	return progpow.config.NodeLocation
}
