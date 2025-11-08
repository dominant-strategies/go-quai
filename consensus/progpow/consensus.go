package progpow

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"runtime/debug"
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
	"github.com/dominant-strategies/go-quai/crypto/multiset"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	"google.golang.org/protobuf/proto"
	"modernc.org/mathutil"
)

// Progpow proof-of-work protocol constants.
var (
	allowedFutureBlockTimeSeconds = int64(15) // Max seconds from current time allowed for blocks, before they're considered future blocks
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
					}).Error("Go-Quai Panicked")
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

		if uncle.PrimaryCoinbase().IsInQiLedgerScope() && block.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
			return fmt.Errorf("uncle inclusion is not allowed before block %v", params.ControllerKickInBlock)
		}

		// Make sure the uncle has a valid ancestry
		if ancestors[hash] != nil {
			return consensus.ErrUncleIsAncestor
		}
		// Siblings are not allowed to be included in the workshares list if its an
		// uncle but can be if its a workshare
		var workShare bool
		validity := chain.UncleWorkShareClassification(uncle)
		switch validity {
		case types.Valid:
			workShare = true
		case types.Sub, types.Invalid:
			return errors.New("uncle in the block has invalid proof of work")
		}

		if workShare {
			err := chain.CheckPowIdValidityForWorkshare(uncle)
			if err != nil {
				return err
			}
		} else {
			err := chain.CheckPowIdValidity(uncle)
			if err != nil {
				return err
			}
		}

		if ancestors[uncle.ParentHash()] == nil || (!workShare && (uncle.ParentHash() == block.ParentHash(nodeCtx))) {
			return consensus.ErrDanglingUncle
		}

		_, err := chain.WorkShareDistance(block, uncle)
		if err != nil {
			return err
		}

		if uncle.NumberU64() < 2*params.BlocksPerMonth && uncle.Lock() != 0 {
			return fmt.Errorf("workshare lock byte: %v is not valid: it has to be %v for the first two months", uncle.Lock(), 0)
		}

		if uncle.PrimeTerminusNumber().Uint64() >= params.KawPowForkBlock && uncle.AuxPow() != nil {
			// Verify the merkle root as well
			expectedMerkleRoot := types.CalculateMerkleRoot(uncle.AuxPow().PowID(), uncle.AuxPow().Transaction(), uncle.AuxPow().MerkleBranch())
			if uncle.AuxPow().Header().MerkleRoot() != expectedMerkleRoot {
				return errors.New("invalid merkle root in auxpow")
			}

			if !uncle.AuxPow().ConvertToTemplate().VerifySignature() {
				return errors.New("invalid auxpow signature")
			}
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
			expectedEfficiencyScore, err := chain.ComputeEfficiencyScore(parent)
			if err != nil {
				return err
			}
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

	if nodeCtx == common.PRIME_CTX {
		if header.PrimeStateRoot() != types.EmptyRootHash {
			return fmt.Errorf("invalid prime state root: have %v, want %v", header.PrimeStateRoot(), types.EmptyRootHash)
		}
	}

	if nodeCtx == common.REGION_CTX {
		if header.RegionStateRoot() != types.EmptyRootHash {
			return fmt.Errorf("invalid region state root: have %v, want %v", header.RegionStateRoot(), types.EmptyRootHash)
		}
	}

	if nodeCtx == common.PRIME_CTX {
		expectedMinerDifficulty := chain.ComputeMinerDifficulty(parent)
		if header.MinerDifficulty().Cmp(expectedMinerDifficulty) != 0 {
			return fmt.Errorf("invalid miner difficulty: have %v, want %v", header.MinerDifficulty(), expectedMinerDifficulty)
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
		if header.PrimaryCoinbase().IsInQiLedgerScope() && header.PrimeTerminusNumber().Uint64() < params.ControllerKickInBlock {
			return fmt.Errorf("Qi coinbase is not allowed before block %v", params.ControllerKickInBlock)
		}
	}

	if nodeCtx == common.ZONE_CTX {

		// check if the header coinbase is in scope
		_, err := header.PrimaryCoinbase().InternalAddress()
		if err != nil {
			return fmt.Errorf("out-of-scope primary coinbase in the header: %v location: %v nodeLocation: %v, err %s", header.PrimaryCoinbase(), header.Location(), progpow.config.NodeLocation, err)
		}
		if header.NumberU64(common.ZONE_CTX) < 2*params.BlocksPerMonth && header.Lock() != 0 {
			return fmt.Errorf("header lock byte: %v is not valid: it has to be %v for the first two months", header.Lock(), 0)
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
		expectedBaseFee := chain.CalcBaseFee(parent)
		if header.BaseFee().Cmp(expectedBaseFee) != 0 {
			return fmt.Errorf("invalid baseFee: have %v, want %v, parentBaseFee %v", expectedBaseFee, header.BaseFee(), parent.BaseFee())
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

		if header.PrimeTerminusNumber().Uint64() >= params.KawPowForkBlock {

			// Get the sha and scrypt share counts from the tx pool
			_, countSha, countScrypt := chain.CountWorkSharesByAlgo(parent)

			//Convert counts to 2^32 base
			bigCountSha := new(big.Int).Mul(big.NewInt(int64(countSha)), common.Big2e32)
			bigCountScrypt := new(big.Int).Mul(big.NewInt(int64(countScrypt)), common.Big2e32)

			var newShaDiff *big.Int
			var newScryptDiff *big.Int
			var newShaCount *big.Int
			var newScryptCount *big.Int
			// Check if shares is nil
			if header.PrimeTerminusNumber().Uint64() == params.KawPowForkBlock { //parent.WorkObjectHeader().ScryptDiffAndCount().Difficulty() == nil || parent.WorkObjectHeader().ShaDiffAndCount().Difficulty() == nil || parent.WorkObjectHeader().ScryptDiffAndCount().Count() == nil || parent.WorkObjectHeader().ShaDiffAndCount().Count() == nil {

				//Initialize the diff and count values
				newShaDiff = params.InitialShaDiff
				newShaCount = params.TargetShaShares
				newScryptDiff = params.InitialScryptDiff
				newScryptCount = params.TargetScryptShares

			} else {
				//Calculate the new diff and count values
				newShaDiff, newShaCount = chain.CalculatePowDiffAndCount(parent.WorkObjectHeader().ShaDiffAndCount(), bigCountSha, types.SHA_BTC)
				newScryptDiff, newScryptCount = chain.CalculatePowDiffAndCount(parent.WorkObjectHeader().ScryptDiffAndCount(), bigCountScrypt, types.Scrypt)
			}

			if header.WorkObjectHeader().ShaDiffAndCount().Difficulty().Cmp(newShaDiff) != 0 {
				return fmt.Errorf("invalid sha difficulty: have %v, want %v", header.WorkObjectHeader().ShaDiffAndCount().Difficulty(), newShaDiff)
			}
			if header.WorkObjectHeader().ShaDiffAndCount().Count().Cmp(newShaCount) != 0 {
				return fmt.Errorf("invalid sha count: have %v, want %v", header.WorkObjectHeader().ShaDiffAndCount().Count(), newShaCount)
			}
			if header.WorkObjectHeader().ScryptDiffAndCount().Difficulty().Cmp(newScryptDiff) != 0 {
				return fmt.Errorf("invalid scrypt difficulty: have %v, want %v", header.WorkObjectHeader().ScryptDiffAndCount().Difficulty(), newScryptDiff)
			}
			if header.WorkObjectHeader().ScryptDiffAndCount().Count().Cmp(newScryptCount) != 0 {
				return fmt.Errorf("invalid scrypt count: have %v, want %v", header.WorkObjectHeader().ScryptDiffAndCount().Count(), newScryptCount)
			}
		} else {
			if header.WorkObjectHeader().ShaDiffAndCount().Difficulty() != nil || header.WorkObjectHeader().ShaDiffAndCount().Count() != nil || header.WorkObjectHeader().ScryptDiffAndCount().Difficulty() != nil || header.WorkObjectHeader().ScryptDiffAndCount().Count() != nil {
				return fmt.Errorf("sha and scrypt diff and count must be nil before kawpow fork block")
			}
		}

		if header.PrimeTerminusNumber().Uint64() >= params.KawPowForkBlock {
			if header.WorkObjectHeader().ShareTarget() != [4]byte{params.ProgpowShareTarget, params.KawpowShareTarget, params.ShaShareTarget, params.ScryptShareTarget} {
				return fmt.Errorf("invalid share target: have %v, want %v", header.WorkObjectHeader().ShareTarget(), [4]byte{params.ProgpowShareTarget, params.KawpowShareTarget, params.ShaShareTarget, params.ScryptShareTarget})
			}
		} else {
			if header.WorkObjectHeader().ShareTarget() != [4]byte{0, 0, 0, 0} {
				return fmt.Errorf("share target must be nil before kawpow fork block")
			}
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
		return progpowLight(size, cache, hash.Bytes(), nonce, blockNumber, ethashCache.cDag)
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
	target := new(big.Int).Div(common.Big2e256, header.Difficulty())
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
func (progpow *Progpow) Finalize(chain consensus.ChainHeaderReader, batch ethdb.Batch, header *types.WorkObject, state *state.StateDB, setRoots bool, utxoSetSize uint64, utxosCreate, utxosDelete []common.Hash, supplyRemovedQi *big.Int) (*multiset.MultiSet, uint64, []*types.SpentUtxoEntry, error) {
	nodeLocation := progpow.NodeLocation()
	nodeCtx := progpow.NodeLocation().Context()

	if nodeLocation.Equal(common.Location{0, 0}) {
		err := state.AddLockedBalances(header.Number(common.ZONE_CTX), progpow.config.GenAllocs, progpow.logger)
		if err != nil {
			log.Global.WithFields(log.Fields{
				"err":      err,
				"blockNum": header.Number(common.ZONE_CTX),
			}).Error("Unable to add state for genesis accounts")
			return nil, 0, nil, err
		}
	}

	var multiSet *multiset.MultiSet
	if chain.IsGenesisHash(header.ParentHash(nodeCtx)) {
		multiSet = multiset.New()
		internalLockupContract, err := vm.LockupContractAddresses[[2]byte{nodeLocation[0], nodeLocation[1]}].InternalAddress()
		if err != nil {
			return nil, 0, nil, fmt.Errorf("Error getting internal address for lockup contract, err %s", err)
		}
		state.SetNonce(internalLockupContract, 1)

		addressOutpointMap := make(map[[20]byte][]*types.OutpointAndDenomination)
		if chain.Config().IndexAddressUtxos {
			chain.WriteAddressOutpoints(addressOutpointMap)
		}

	} else {
		multiSet = rawdb.ReadMultiSet(chain.Database(), header.ParentHash(nodeCtx))
	}
	if multiSet == nil {
		return nil, 0, nil, fmt.Errorf("Multiset is nil for block %s", header.ParentHash(nodeCtx).String())
	}

	utxoSetSize += uint64(len(utxosCreate))
	if utxoSetSize < uint64(len(utxosDelete)) {
		return nil, 0, nil, fmt.Errorf("UTXO set size is less than the number of utxos to delete. This is a bug. UTXO set size: %d, UTXOs to delete: %d", utxoSetSize, len(utxosDelete))
	}
	utxoSetSize -= uint64(len(utxosDelete))

	trimmedUtxos := make([]*types.SpentUtxoEntry, 0)
	start := time.Now()
	var wg sync.WaitGroup
	var lock sync.Mutex
	for denomination, depth := range types.TrimDepths {
		if denomination <= types.MaxTrimDenomination && header.NumberU64(nodeCtx) > depth {
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
				TrimBlock(chain, batch, denomination, header.NumberU64(nodeCtx)-depth, nextBlockToTrim, &utxosDelete, &trimmedUtxos, supplyRemovedQi, &utxoSetSize, !setRoots, &lock, progpow.logger) // setRoots is false when we are processing the block
				wg.Done()
			}(denomination, depth)
		}
	}
	wg.Wait()
	if len(trimmedUtxos) > 0 {
		progpow.logger.Infof("Trimmed %d UTXOs from db in %s", len(trimmedUtxos), common.PrettyDuration(time.Since(start)))
	}
	if !setRoots {
		rawdb.WriteTrimmedUTXOs(batch, header.Hash(), trimmedUtxos)
	}
	for _, hash := range utxosCreate {
		multiSet.Add(hash.Bytes())
	}
	for _, hash := range utxosDelete {
		multiSet.Remove(hash.Bytes())
	}
	progpow.logger.Infof("Parent hash: %s, header hash: %s, muhash: %s, block height: %d, setroots: %t, UtxosCreated: %d, UtxosDeleted: %d, UTXOs Trimmed from DB: %d, UTXO Set Size: %d", header.ParentHash(nodeCtx).String(), header.Hash().String(), multiSet.Hash().String(), header.NumberU64(nodeCtx), setRoots, len(utxosCreate), len(utxosDelete), len(trimmedUtxos), utxoSetSize)

	if setRoots {
		header.Header().SetUTXORoot(multiSet.Hash())
		header.Header().SetEVMRoot(state.IntermediateRoot(true))
		header.Header().SetEtxSetRoot(state.ETXRoot())
		header.Header().SetQuaiStateSize(state.GetQuaiTrieSize())
	}
	return multiSet, utxoSetSize, trimmedUtxos, nil
}

// TrimBlock trims all UTXOs of a given denomination that were created in a given block.
// In the event of an attacker intentionally creating too many 9-byte keys that collide, we return the colliding keys to be trimmed in the next block.
func TrimBlock(chain consensus.ChainHeaderReader, batch ethdb.Batch, denomination uint8, blockHeight uint64, blockHash common.Hash, utxosDelete *[]common.Hash, trimmedUtxos *[]*types.SpentUtxoEntry, supplyRemovedQi *big.Int, utxoSetSize *uint64, deleteFromDb bool, lock *sync.Mutex, logger *log.Logger) {
	utxosCreated, _ := rawdb.ReadCreatedUTXOKeys(chain.Database(), blockHash)
	if len(utxosCreated) == 0 {
		logger.Infof("UTXOs created in block %d: %d", blockHeight, len(utxosCreated))
		return
	}

	logger.Infof("UTXOs created in block %d: %d", blockHeight, len(utxosCreated))
	// Start by grabbing all the UTXOs created in the block (that are still in the UTXO set)
	for _, key := range utxosCreated {

		if key[len(key)-1] != denomination {
			if key[len(key)-1] > denomination {
				break // The keys are stored in order of denomination, so we can stop checking here
			} else {
				continue
			}
		} else {
			key = key[:len(key)-1] // remove the denomination byte
		}

		data, _ := chain.Database().Get(key)
		if len(data) == 0 {
			logger.Infof("Empty key found, denomination: %d", denomination)
			continue
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
		if utxo.Denomination != denomination {
			continue
		}
		// Only the coinbase and conversion txs are allowed to have lockups that
		// is non zero
		if utxo.Lock.Sign() != 0 {
			continue
		}
		txHash, index, err := rawdb.ReverseUtxoKey(key)
		if err != nil {
			logger.WithField("err", err).Error("Failed to parse utxo key")
			continue
		}
		lock.Lock()
		*utxosDelete = append(*utxosDelete, types.UTXOHash(txHash, index, utxo))
		if deleteFromDb {
			batch.Delete(key)
			*trimmedUtxos = append(*trimmedUtxos, &types.SpentUtxoEntry{OutPoint: types.OutPoint{txHash, index}, UtxoEntry: utxo})
			if supplyRemovedQi != nil {
				supplyRemovedQi.Add(supplyRemovedQi, types.Denominations[denomination])
			}
		}
		*utxoSetSize--
		lock.Unlock()
	}
}

// FinalizeAndAssemble implements consensus.Engine, accumulating the block and
// uncle rewards, setting the final state and assembling the block.
func (progpow *Progpow) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.WorkObject, state *state.StateDB, txs []*types.Transaction, uncles []*types.WorkObjectHeader, etxs []*types.Transaction, subManifest types.BlockManifest, receipts []*types.Receipt, parentUtxoSetSize uint64, utxosCreate, utxosDelete []common.Hash) (*types.WorkObject, error) {
	nodeCtx := progpow.config.NodeLocation.Context()
	if nodeCtx == common.ZONE_CTX && chain.ProcessingState() {
		// Finalize block
		if _, _, _, err := progpow.Finalize(chain, nil, header, state, true, parentUtxoSetSize, utxosCreate, utxosDelete, nil); err != nil {
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
