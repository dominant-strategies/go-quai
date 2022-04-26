package randomx

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"time"

	mapset "github.com/deckarep/golang-set"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/common/math"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/consensus/misc"
	"github.com/spruce-solutions/go-quai/core/state"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/params"
	"github.com/spruce-solutions/go-quai/rlp"
	"github.com/spruce-solutions/go-quai/trie"
	"golang.org/x/crypto/sha3"
)

// Randomx proof-of-work protocol constants.
var (
	BlockReward = big.NewInt(5e+18) // Block reward in wei for successfully mining a block

	RandomxKeyPeriod = (3 * 24 * 60 * 60) / params.DurationLimits[0].Uint64() // Key rotates every ~3 days
	RandomxKeyLag    = (2 * 60 * 60) / params.DurationLimits[0].Uint64()      // Lag ~2hrs before applying new key

	allowedFutureBlockTimeSeconds = int64(15) // Max seconds from current time allowed for blocks, before they're considered future blocks
	maxUncles                     = 2         // Maximum number of uncles allowed in a single block
)

// Some weird constants to avoid constant memory allocs for them.
var (
	expDiffPeriod = big.NewInt(100000)
	big1          = big.NewInt(1)
	big2          = big.NewInt(2)
	big8          = big.NewInt(8)
	big9          = big.NewInt(9)
	big10         = big.NewInt(10)
	big32         = big.NewInt(32)
	bigMinus99    = big.NewInt(-99)
	big2e256      = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0)) // 2^256
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	errOlderBlockTime    = errors.New("timestamp older than parent")
	errTooManyUncles     = errors.New("too many uncles")
	errDuplicateUncle    = errors.New("duplicate uncle")
	errUncleIsAncestor   = errors.New("uncle is ancestor")
	errDanglingUncle     = errors.New("uncle's parent is not ancestor")
	errInvalidDifficulty = errors.New("non-positive difficulty")
	errInvalidMixDigest  = errors.New("invalid mix digest")
	errInvalidPoW        = errors.New("invalid proof-of-work")
	errExtBlockNotFound  = errors.New("external block not found")
)

// Author implements consensus.Engine, returning the header's coinbase as the
// proof-of-work verified author of the block.
func (rx *Randomx) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase[types.QuaiNetworkContext], nil
}

// VerifyHeader checks whether a header conforms to the consensus rules of the RandomX engine.
func (rx *Randomx) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header, seal bool) error {
	// Short circuit if the header is known, or its parent not
	number := header.Number[types.QuaiNetworkContext].Uint64()
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	parent := chain.GetHeader(header.ParentHash[types.QuaiNetworkContext], number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	// Sanity checks passed, do a proper verification
	return rx.verifyHeader(chain, header, parent, false, seal, time.Now().Unix())
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
// concurrently. The method returns a quit channel to abort the operations and
// a results channel to retrieve the async verifications.
func (rx *Randomx) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	// If we're running a full engine faking, accept any input as valid
	if len(headers) == 0 {
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
			for index := range inputs {
				errors[index] = rx.verifyHeaderWorker(chain, headers, seals, index, unixNow)
				done <- index
			}
		}()
	}

	errorsOut := make(chan error, len(headers))
	go func() {
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

func (rx *Randomx) verifyHeaderWorker(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool, index int, unixNow int64) error {
	var parent *types.Header
	if index == 0 {
		parent = chain.GetHeader(headers[0].ParentHash[types.QuaiNetworkContext], headers[0].Number[types.QuaiNetworkContext].Uint64()-1)
	} else if headers[index-1].Hash() == headers[index].ParentHash[types.QuaiNetworkContext] {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	return rx.verifyHeader(chain, headers[index], parent, false, seals[index], unixNow)
}

// VerifyUncles verifies that the given block's uncles conform to the consensus rules
func (rx *Randomx) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	// Verify that there are at most 2 uncles included in this block
	if len(block.Uncles()) > maxUncles {
		return errTooManyUncles
	}
	if len(block.Uncles()) == 0 {
		return nil
	}
	// Gather the set of past uncles and ancestors
	uncles, ancestors := mapset.NewSet(), make(map[common.Hash]*types.Header)

	number, parent := block.NumberU64()-1, block.ParentHash()
	for i := 0; i < 7; i++ {
		ancestorHeader := chain.GetHeader(parent, number)
		if ancestorHeader == nil {
			break
		}
		ancestors[parent] = ancestorHeader
		// If the ancestor doesn't have any uncles, we don't have to iterate them
		if !types.IsEqualHashSlice(ancestorHeader.UncleHash, types.EmptyUncleHash) {
			// Need to add those uncles to the banned list too
			ancestor := chain.GetBlock(parent, number)
			if ancestor == nil {
				break
			}
			for _, uncle := range ancestor.Uncles() {
				uncles.Add(uncle.Hash())
			}
		}
		parent, number = ancestorHeader.ParentHash[types.QuaiNetworkContext], number-1
	}
	ancestors[block.Hash()] = block.Header()
	uncles.Add(block.Hash())

	// Verify each of the uncles that it's recent, but not an ancestor
	for _, uncle := range block.Uncles() {
		// Make sure every uncle is rewarded only once
		hash := uncle.Hash()
		if uncles.Contains(hash) {
			return errDuplicateUncle
		}
		uncles.Add(hash)

		// Make sure the uncle has a valid ancestry
		if ancestors[hash] != nil {
			return errUncleIsAncestor
		}
		if ancestors[uncle.ParentHash[types.QuaiNetworkContext]] == nil || uncle.ParentHash[types.QuaiNetworkContext] == block.ParentHash() {
			return errDanglingUncle
		}
		if err := rx.verifyHeader(chain, uncle, ancestors[uncle.ParentHash[types.QuaiNetworkContext]], true, true, time.Now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// verifyHeader checks whether a header conforms to the consensus rules
func (rx *Randomx) verifyHeader(chain consensus.ChainHeaderReader, header, parent *types.Header, uncle bool, seal bool, unixNow int64) error {
	// Ensure that the header's extra-data section is of a reasonable size
	if uint64(len(header.Extra)) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra-data too long: %d > %d", len(header.Extra), params.MaximumExtraDataSize)
	}
	// Verify the header's timestamp
	if !uncle {
		if header.Time > uint64(unixNow+allowedFutureBlockTimeSeconds) {
			return consensus.ErrFutureBlock
		}
	}
	if header.Time <= parent.Time {
		return errOlderBlockTime
	}
	// Verify the block's difficulty based on its timestamp and parent's difficulty
	expected := rx.CalcDifficulty(chain, header.Time, parent, types.QuaiNetworkContext)
	if expected.Cmp(header.Difficulty[types.QuaiNetworkContext]) > 0 {
		return fmt.Errorf("invalid difficulty: have %v, want %v", header.Difficulty[types.QuaiNetworkContext], expected)
	}
	// Verify that the gas limit is <= 2^63-1
	cap := uint64(0x7fffffffffffffff)
	if header.GasLimit[types.QuaiNetworkContext] > cap {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, cap)
	}
	// Verify that the gasUsed is <= gasLimit
	if len(header.GasUsed) > 0 && header.GasUsed[types.QuaiNetworkContext] > header.GasLimit[types.QuaiNetworkContext] {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}
	// Verify the block's gas usage and base fee.
	if err := misc.VerifyHeaderGasAndFee(chain.Config(), parent, header, chain); err != nil {
		// Verify the header's EIP-1559 attributes.
		return err
	}
	// Verify that the block number is parent's +1
	if diff := new(big.Int).Sub(header.Number[types.QuaiNetworkContext], parent.Number[types.QuaiNetworkContext]); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}
	// Verify the engine specific seal securing the block
	if seal {
		if err := rx.verifySeal(header); err != nil {
			return err
		}
	}
	// Verify that Location is same as config
	if validLocation := verifyLocation(header.Location, chain.Config().Location); !validLocation {
		return fmt.Errorf("invalid location: Location %d not valid, expected %c", header.Location, chain.Config().Location)
	}

	if err := misc.VerifyForkHashes(chain.Config(), header, uncle); err != nil {
		return err
	}
	return nil
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
func (rx *Randomx) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header, context int) *big.Int {
	return CalcDifficulty(chain.Config(), time, parent, context)
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
func CalcDifficulty(config *params.ChainConfig, time uint64, parent *types.Header, context int) *big.Int {
	return calcDifficultyFrontier(time, parent, context)
}

// calcDifficultyFrontier is the difficulty adjustment algorithm. It returns the
// difficulty that a new block should have when created at time given the parent
// block's time and difficulty. The calculation uses the Frontier rules.
func calcDifficultyFrontier(time uint64, parent *types.Header, context int) *big.Int {
	diff := new(big.Int)
	parentDifficulty := parent.Difficulty[context]
	if parentDifficulty == nil {
		return params.GenesisDifficulty
	}

	adjust := new(big.Int).Div(parentDifficulty, params.DifficultyBoundDivisor[types.QuaiNetworkContext])
	bigTime := new(big.Int)
	bigParentTime := new(big.Int)

	bigTime.SetUint64(time)
	bigParentTime.SetUint64(parent.Time)

	duration := params.DurationLimits[types.QuaiNetworkContext]

	if bigTime.Sub(bigTime, bigParentTime).Cmp(duration) < 0 {
		diff.Add(parentDifficulty, adjust)
	} else {
		diff.Sub(parentDifficulty, adjust)
	}
	if diff.Cmp(params.MinimumDifficulty) < 0 {
		diff.Set(params.MinimumDifficulty)
	}

	periodCount := new(big.Int).Add(parent.Number[context], big1)
	periodCount.Div(periodCount, expDiffPeriod)
	if periodCount.Cmp(big1) > 0 {
		// diff = diff + 2^(periodCount - 2)
		expDiff := periodCount.Sub(periodCount, big2)
		expDiff.Exp(big2, expDiff, nil)
		diff.Add(diff, expDiff)
		diff = math.BigMax(diff, params.MinimumDifficulty)
	}
	return diff
}

// verifySeal checks whether a block satisfies the PoW difficulty requirements,
func (rx *Randomx) verifySeal(header *types.Header) error {
	difficulty := header.Difficulty[types.QuaiNetworkContext]

	// Ensure that we have a valid difficulty for the block
	if difficulty.Sign() <= 0 {
		return errInvalidDifficulty
	}

	// Compute the workhash as randomx(nonce | sealhash)
	workhash := rx.HashWithNonce(header.Nonce.Uint64(), rx.SealHash(header).Bytes())

	// Check the work hashes match
	if !bytes.Equal(header.MixDigest[:], workhash) {
		return errInvalidMixDigest
	}

	// Difficulty check for valid proof of work
	target := new(big.Int).Div(big2e256, difficulty)
	if new(big.Int).SetBytes(workhash).Cmp(target) > 0 {
		return errInvalidPoW
	}
	return nil
}

// This function determines the difficulty order of a block
func (rx *Randomx) GetDifficultyOrder(header *types.Header) (int, error) {
	if header == nil {
		return types.ContextDepth, errors.New("No header provided")
	}
	if err := rx.verifySeal(header); nil != err {
		return -1, err
	}
	for i, difficulty := range header.Difficulty {
		if difficulty == nil || big.NewInt(0).Cmp(difficulty) >= 0 {
			return -1, errors.New("Block uses invalid difficulty targets")
		}
		target := new(big.Int).Div(big2e256, difficulty)
		if new(big.Int).SetBytes(header.MixDigest[:]).Cmp(target) <= 0 {
			return i, nil
		}
	}
	return -1, errors.New("Block does not satisfy minimum difficulty")
}

// Iterate back through headers to find ones that exceed a given context.
func (rx *Randomx) GetCoincidentHeader(chain consensus.ChainHeaderReader, context int, header *types.Header) (*types.Header, int) {
	// If we are at the highest context, no coincident will include it.
	if context == 0 {
		return header, 0
	} else if context == 1 {
		difficultyContext, err := rx.GetDifficultyOrder(header)
		if err != nil {
			return header, context
		}

		return header, difficultyContext
	} else {
		for {
			// Check work of the header, if it has enough work we will move up in context.
			// difficultyContext is initially context since it could be a pending block w/o a nonce.
			difficultyContext, err := rx.GetDifficultyOrder(header)
			if err != nil {
				return header, context
			}

			// If block header is Genesis return it as coincident
			if header.Number[context].Cmp(big.NewInt(1)) <= 0 {
				return header, difficultyContext
			}

			// If we have reached a coincident block
			if difficultyContext < context {
				return header, difficultyContext
			} else if difficultyContext == 1 && context == 1 {
				return header, difficultyContext
			}

			// Get previous header on local chain by hash
			prevHeader := chain.GetHeaderByHash(header.ParentHash[context])

			// Increment previous header
			header = prevHeader
		}
	}
}

// Check difficulty of previous header in order to find traceability.
func (rx *Randomx) CheckPrevHeaderCoincident(chain consensus.ChainHeaderReader, context int, header *types.Header) (int, error) {
	// If we are at the highest context, no coincident will include it.
	difficultyContext, err := rx.GetDifficultyOrder(header)
	if err != nil {
		return difficultyContext, fmt.Errorf("difficulty not found")
	}
	return difficultyContext, nil
}

// GetStopHash returns the N-1 hash that is used to terminate on during TraceBranch.
func (rx *Randomx) GetStopHash(chain consensus.ChainHeaderReader, originalContext int, wantedDiffContext int, startingHeader *types.Header) (common.Hash, int) {
	header := startingHeader
	stopHash := common.Hash{}
	num := 0
	for {
		num++

		// Append the coincident and iterating header to the list
		if header.Number[originalContext].Cmp(big.NewInt(1)) == 0 {
			switch wantedDiffContext {
			case 0:
				stopHash = chain.Config().GenesisHashes[0]
			case 1:
				stopHash = chain.Config().GenesisHashes[1]
			case 2:
				stopHash = chain.Config().GenesisHashes[2]
			}
			break
		}

		prevHeader := chain.GetHeaderByHash(header.ParentHash[originalContext])
		if prevHeader == nil {
			log.Warn("Unable to get prevHeader in GetStopHash")
			return stopHash, num
		}
		header = prevHeader

		// Check work of the header, if it has enough work we will move up in context.
		// difficultyContext is initially context since it could be a pending block w/o a nonce.
		difficultyContext, err := rx.GetDifficultyOrder(header)
		if err != nil {
			break
		}

		sameLocation := false
		switch originalContext {
		case 0:
			sameLocation = true
		case 1:
			sameLocation = startingHeader.Location[0] == header.Location[0]
		case 2:
			sameLocation = bytes.Equal(startingHeader.Location, header.Location)
		}

		if difficultyContext == wantedDiffContext && sameLocation {
			stopHash = header.Hash()
			break
		}
	}

	return stopHash, num
}

// TraceBranch is the recursive function that returns all ExternalBlocks for a given header, stopHash, context, and location.
func (rx *Randomx) PrimeTraceBranch(chain consensus.ChainHeaderReader, header *types.Header, context int, stopHash common.Hash, originalContext int, originalLocation []byte) ([]*types.ExternalBlock, error) {
	extBlocks := make([]*types.ExternalBlock, 0)
	// startingHeader := header
	for {
		// If the header is genesis, return the current set of external blocks.
		if header.Number[context].Cmp(big.NewInt(0)) == 0 {
			// log.Info("Trace Branch: Stopping height == 0", "number", header.Number, "context", context, "location", header.Location, "hash", header.ParentHash[context])
			break
		}

		// If we have are stepping into a Region from Prime ensure it is now our original location.
		if context < types.ContextDepth-1 {
			if (header.Location[0] != originalLocation[0] && originalContext > 0) || originalContext == 0 {
				// log.Info("Trace Branch: Going down into trace fromPrime", "number", header.Number, "context", context, "location", header.Location, "hash", header.Hash())
				result, err := rx.PrimeTraceBranch(chain, header, context+1, stopHash, originalContext, originalLocation)
				if err != nil {
					return nil, err
				}
				extBlocks = append(extBlocks, result...)
			}
		}

		// If we are in Prime tracing Prime return.
		if originalContext == 0 && context == 0 {
			break
		}

		// Obtain the external block on the branch we are currently tracing.
		extBlock, err := chain.GetExternalBlock(header.Hash(), header.Number[context].Uint64(), uint64(context))
		if err != nil {
			log.Info("Trace Branch: External Block not found for header", "number", header.Number, "context", context, "hash", header.Hash(), "location", header.Location)
			return extBlocks, nil
		}
		extBlocks = append(extBlocks, extBlock)
		// log.Info("Trace Branch: PRIME Adding external block", "number", header.Number, "context", context, "location", header.Location, "hash", header.Hash())
		if header.ParentHash[context] == stopHash {
			// log.Info("Trace Branch: Stopping on stop hash or num is 1", "number", header.Number, "context", context, "location", header.Location, "hash", header.ParentHash[context])
			break
		}

		// Do not continue at header number == 1 since we are unable to obtain the Genesis as an external block.
		if header.Number[context].Cmp(big.NewInt(1)) == 0 {
			log.Info("Trace Branch: Stopping height == 1", "number", header.Number, "context", context, "location", header.Location, "hash", header.ParentHash[context])
			break
		}

		// Retrieve the previous header as an external block.
		prevHeader, err := chain.GetExternalBlock(header.ParentHash[context], header.Number[context].Uint64()-1, uint64(context))
		if err != nil {
			log.Info("Trace Branch: External Block not found for previous header", "number", header.Number[context].Int64()-1, "context", context, "hash", header.ParentHash[context], "location", header.Location)
			return extBlocks, nil
		}

		if bytes.Equal(originalLocation, prevHeader.Header().Location) && context == 0 {
			// log.Info("Trace Branch: Stopping in location equal", "original", originalLocation, "prev", prevHeader.Header().Location)
			break
		}

		header = prevHeader.Header()
		if header == nil {
			break
		}
		// Calculate the difficulty context in order to know if we have reached a coincident.
		// If we get a coincident, stop and return.
		difficultyContext, err := rx.GetDifficultyOrder(header)
		if err != nil {
			break
		}
		if difficultyContext < context {
			// log.Info("TraceBranch: Found Region coincident block in Zone", "number", header.Number, "context", context, "location", header.Location)
			break
		}
	}
	return extBlocks, nil
}

// TraceBranch is the recursive function that returns all ExternalBlocks for a given header, stopHash, context, and location.
func (rx *Randomx) RegionTraceBranch(chain consensus.ChainHeaderReader, header *types.Header, context int, stopHash common.Hash, originalContext int, originalLocation []byte) ([]*types.ExternalBlock, error) {
	extBlocks := make([]*types.ExternalBlock, 0)
	// startingHeader := header
	for {
		// If the header is genesis, return the current set of external blocks.
		if header.Number[context].Cmp(big.NewInt(0)) == 0 {
			// log.Info("Trace Branch: Stopping height == 0", "number", header.Number, "context", context, "location", header.Location, "hash", header.ParentHash[context])
			break
		}

		// If we are in a context that can trace a lower context, i.e Region tracing Zone.
		// Choose the allowed trace depending on the original context and stepping into location.
		// If we are in a Region node, we can trace down into any Zone.
		// If we are in a Zone node, we cannot trace down into our own Zone.
		if context < types.ContextDepth-1 {
			if originalContext == 1 {
				result, err := rx.RegionTraceBranch(chain, header, context+1, stopHash, originalContext, originalLocation)
				if err != nil {
					return nil, err
				}
				extBlocks = append(extBlocks, result...)
			} else if originalContext == 2 && !bytes.Equal(originalLocation, header.Location) {
				result, err := rx.RegionTraceBranch(chain, header, context+1, stopHash, originalContext, originalLocation)
				if err != nil {
					return nil, err
				}
				extBlocks = append(extBlocks, result...)
			}
		}

		// Obtain the sameLocation depending on our original and current context.
		sameLocation := false
		if originalContext == 1 && context == 1 {
			sameLocation = originalLocation[0] == header.Location[0]
		} else if originalContext == 2 && context == 2 {
			sameLocation = bytes.Equal(originalLocation, header.Location)
		}

		// If we are in the sameLocation, meaning our context and location aren't different we must stop.
		if sameLocation {
			break
		}

		// Obtain the external block on the branch we are currently tracing.
		extBlock, err := chain.GetExternalBlock(header.Hash(), header.Number[context].Uint64(), uint64(context))
		if err != nil {
			log.Info("Trace Branch: External Block not found for header", "number", header.Number, "context", context, "hash", header.Hash(), "location", header.Location)
			break
		}
		extBlocks = append(extBlocks, extBlock)
		// log.Info("Trace Branch: REGION Adding external block", "number", header.Number, "context", context, "location", header.Location, "hash", header.Hash())

		// Stop on the passed in stopHash
		// fmt.Println("Region stopHash", header.ParentHash[context], stopHash)
		if header.ParentHash[context] == stopHash {
			// log.Info("Trace Branch: Stopping on stop hash or num is 1", "number", header.Number, "context", context, "location", header.Location, "hash", header.ParentHash[context])
			break
		}

		// Do not continue at header number == 1 since we are unable to obtain the Genesis as an external block.
		if header.Number[context].Cmp(big.NewInt(1)) == 0 {
			// log.Info("Trace Branch: Stopping height == 1", "number", header.Number, "context", context, "location", header.Location, "hash", header.ParentHash[context])
			break
		}

		// Retrieve the previous header as an external block.
		prevHeader, err := chain.GetExternalBlock(header.ParentHash[context], header.Number[context].Uint64()-1, uint64(context))
		if err != nil {
			log.Info("Trace Branch: External Block not found for previous header", "number", header.Number[context].Int64()-1, "context", context, "hash", header.ParentHash[context], "location", header.Location)
			break
		}

		if bytes.Equal(originalLocation, prevHeader.Header().Location) && context == 1 {
			// log.Info("Trace Branch: Stopping in location equal", "original", originalLocation, "prev", prevHeader.Header().Location)
			break
		}

		header = prevHeader.Header()
		if header == nil {
			break
		}
		// Calculate the difficulty context in order to know if we have reached a coincident.
		// If we get a coincident, stop and return.
		difficultyContext, err := rx.GetDifficultyOrder(header)
		if err != nil {
			break
		}
		if difficultyContext < context && context == types.ContextDepth-1 {
			// log.Info("Trace Branch: Found Region coincident block in Zone", "number", header.Number, "context", context, "location", header.Location)
			break
		}
	}
	return extBlocks, nil
}

// GetExternalBlocks traces all available branches to find external blocks
func (rx *Randomx) GetExternalBlocks(chain consensus.ChainHeaderReader, header *types.Header, logging bool) ([]*types.ExternalBlock, error) {
	context := chain.Config().Context // Index that node is currently at
	externalBlocks := make([]*types.ExternalBlock, 0)
	log.Info("GetExternalBlocks: Getting trace for block", "num", header.Number, "context", context, "location", header.Location, "hash", header.Hash())
	start := time.Now()

	// Do not run on block 1
	if header.Number[context].Cmp(big.NewInt(1)) > 0 {
		// Skip pending block
		prevHeader := chain.GetHeaderByHash(header.ParentHash[context])
		difficultyContext, err := rx.CheckPrevHeaderCoincident(chain, context, prevHeader)
		if err != nil {
			return nil, err
		}

		log.Info("GetExternalBlocks: Retrieved difficultyContext", "difficultyContext", difficultyContext, "context", context)
		// Check if in Zone and PrevHeader is not a coincident header, no external blocks to trace.
		if context == 2 && difficultyContext == 2 {
			return externalBlocks, nil
		}

		// Get the Prime stopHash to be used in the Prime context. Go on to trace Prime once.
		primeStopHash, primeNum := rx.GetStopHash(chain, context, 0, prevHeader)
		if context == 0 {
			extBlockResult, extBlockErr := rx.PrimeTraceBranch(chain, prevHeader, difficultyContext, primeStopHash, context, header.Location)
			if extBlockErr != nil {
				log.Info("GetExternalBlocks: Returning with error", "len", len(externalBlocks), "time", time.Since(start), "err", extBlockErr)
				return nil, extBlockErr
			}
			externalBlocks = append(externalBlocks, extBlockResult...)
		}

		// If we are in a Region or Zone context, we may need to change our Prime stopHash since
		// a Region block might not yet have been found. Scenario: [2, 2, 2] mined before [1, 2, 2].
		if context == 1 || context == 2 {
			regionStopHash, regionNum := rx.GetStopHash(chain, context, 1, prevHeader)
			if difficultyContext == 0 {
				extBlockResult, extBlockErr := rx.PrimeTraceBranch(chain, prevHeader, difficultyContext, primeStopHash, context, header.Location)
				if extBlockErr != nil {
					log.Info("GetExternalBlocks: Returning with error", "len", len(externalBlocks), "time", time.Since(start), "err", extBlockErr)
					return nil, extBlockErr
				}
				externalBlocks = append(externalBlocks, extBlockResult...)
			}
			// If our Prime stopHash comes before our Region stopHash.
			if primeNum < regionNum {
				regionStopHash = primeStopHash
			}
			// If we have a Region block, trace it.
			if difficultyContext < 2 {
				extBlockResult, extBlockErr := rx.RegionTraceBranch(chain, prevHeader, 1, regionStopHash, context, header.Location)
				if extBlockErr != nil {
					log.Info("GetExternalBlocks: Returning with error", "len", len(externalBlocks), "time", time.Since(start), "err", extBlockErr)
					return nil, extBlockErr
				}
				externalBlocks = append(externalBlocks, extBlockResult...)
			}
		}
	}

	log.Info("GetExternalBlocks: Length of external blocks", "len", len(externalBlocks), "time", time.Since(start))

	// TODO: Impelement queue here, remove the above check for N+1.
	// return chain.QueueAndRetrieveExtBlocks(externalBlocks, header)
	return externalBlocks, nil
}

// Prepare implements consensus.Engine, initializing the difficulty field of a
// header to conform to the RandomX protocol. The changes are done inline.
func (rx *Randomx) Prepare(chain consensus.ChainHeaderReader, header *types.Header) error {
	parent := chain.GetHeader(header.ParentHash[types.QuaiNetworkContext], header.Number[types.QuaiNetworkContext].Uint64()-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Difficulty[types.QuaiNetworkContext] = rx.CalcDifficulty(chain, header.Time, parent, types.QuaiNetworkContext)
	currentTotal := big.NewInt(0)
	currentTotal.Add(parent.NetworkDifficulty[types.QuaiNetworkContext], header.Difficulty[types.QuaiNetworkContext])
	header.NetworkDifficulty[types.QuaiNetworkContext] = currentTotal
	return nil
}

// GetExternalBlocks traces all available branches to find external blocks
func (rx *Randomx) GetLinkExternalBlocks(chain consensus.ChainHeaderReader, header *types.Header, logging bool) ([]*types.ExternalBlock, error) {
	context := chain.Config().Context // Index that node is currently at
	externalBlocks := make([]*types.ExternalBlock, 0)
	rx.config.Log.Info("GetLinkExternalBlocks: Getting trace for block", "num", header.Number, "context", context, "location", header.Location, "hash", header.Hash())

	// Do not run on block 1
	if header.Number[context].Cmp(big.NewInt(1)) > 0 {
		difficultyContext, err := rx.GetDifficultyOrder(header)
		// Only run if we are the block immediately following the coincident block. Check below is to make sure we are N+1.
		if err != nil {
			return externalBlocks, nil
		}

		// Get the Prime stopHash to be used in the Prime context. Go on to trace Prime once.
		primeStopHash := header.ParentHash[0]
		if context == 0 {
			extBlockResult, extBlockErr := rx.PrimeTraceBranch(chain, header, difficultyContext, primeStopHash, context, header.Location)
			if extBlockErr != nil {
				return nil, extBlockErr
			}
			externalBlocks = append(externalBlocks, extBlockResult...)
		}

		// If we are in a Region or Zone context, we may need to change our Prime stopHash since
		// a Region block might not yet have been found. Scenario: [2, 2, 2] mined before [1, 2, 2].
		if context == 1 || context == 2 {
			primeStopHash, primeNum := rx.GetStopHash(chain, context, 0, header)

			regionStopHash, regionNum := rx.GetStopHash(chain, context, 1, header)
			if difficultyContext == 0 {
				extBlockResult, extBlockErr := rx.PrimeTraceBranch(chain, header, difficultyContext, primeStopHash, context, header.Location)
				if extBlockErr != nil {
					return nil, extBlockErr
				}
				externalBlocks = append(externalBlocks, extBlockResult...)
			}
			// If our Prime stopHash comes before our Region stopHash.
			if primeNum < regionNum {
				regionStopHash = primeStopHash
			}
			// If we have a Region block, trace it.
			if difficultyContext < 2 {
				extBlockResult, extBlockErr := rx.RegionTraceBranch(chain, header, 1, regionStopHash, context, header.Location)
				if extBlockErr != nil {
					return nil, extBlockErr
				}
				externalBlocks = append(externalBlocks, extBlockResult...)
			}
		}
	}

	return externalBlocks, nil
}

// Finalize implements consensus.Engine, accumulating the block and uncle rewards,
// setting the final state on the header
func (rx *Randomx) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header) {
	// Accumulate any block and uncle rewards and commit the final state root
	accumulateRewards(chain.Config(), state, header, uncles)
	header.Root[types.QuaiNetworkContext] = state.IntermediateRoot(chain.Config().IsEIP158(header.Number[types.QuaiNetworkContext]))
}

// FinalizeAndAssemble implements consensus.Engine, accumulating the block and
// uncle rewards, setting the final state and assembling the block.
func (rx *Randomx) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	// Finalize block
	rx.Finalize(chain, header, state, txs, uncles)

	// Header seems complete, assemble into a block and return
	return types.NewBlock(header, txs, uncles, receipts, trie.NewStackTrie(nil)), nil
}

// SealHash returns the hash of a block prior to it being sealed.
func (rx *Randomx) SealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()

	enc := []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra,
		header.Location,
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	rlp.Encode(hasher, enc)
	hasher.Sum(hash[:0])
	return hash
}

// AccumulateRewards credits the coinbase of the given block with the mining
// reward. The total reward consists of the static block reward and rewards for
// included uncles. The coinbase of each uncle block is also rewarded.
func accumulateRewards(config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) {
	// Skip block reward in catalyst mode
	if config.IsCatalyst(header.Number[types.QuaiNetworkContext]) {
		return
	}
	// Select the correct block reward based on chain progression
	blockReward := misc.CalculateReward()
	// Accumulate the rewards for the miner and any included uncles
	reward := new(big.Int).Set(blockReward)
	r := new(big.Int)
	for _, uncle := range uncles {
		r.Add(uncle.Number[types.QuaiNetworkContext], big8)
		r.Sub(r, header.Number[types.QuaiNetworkContext])
		r.Mul(r, blockReward)
		r.Div(r, big8)
		state.AddBalance(uncle.Coinbase[types.QuaiNetworkContext], r)

		r.Div(blockReward, big32)
		reward.Add(reward, r)
	}
	state.AddBalance(header.Coinbase[types.QuaiNetworkContext], reward)
}

// Verifies that a header location is valid for a specific config.
func verifyLocation(location []byte, configLocation []byte) bool {
	switch types.QuaiNetworkContext {
	case 0:
		return true
	case 1:
		if location[0] != configLocation[0] {
			return false
		} else {
			return true
		}
	case 2:
		if !bytes.Equal(location, configLocation) {
			return false
		} else {
			return true
		}
	default:
		return false
	}
}
