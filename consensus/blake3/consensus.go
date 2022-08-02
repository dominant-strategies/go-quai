// Copyright 2018 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package blake3

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
	"github.com/spruce-solutions/go-quai/params"
	"github.com/spruce-solutions/go-quai/rlp"
	"github.com/spruce-solutions/go-quai/trie"
	blake3hash "lukechampine.com/blake3"
)

// Blake3 proof-of-work protocol constants.
var (
	BlockReward = big.NewInt(5e+18) // Block reward in wei for successfully mining a block

	allowedFutureBlockTimeSeconds = int64(15) // Max seconds from current time allowed for blocks, before they're considered future blocks
	maxUncles                     = 2         // Maximum number of uncles allowed in a single block
	fakeDifficulties              = []*big.Int{new(big.Int).Mul(params.MinimumDifficulty[params.PRIME], big.NewInt(4)), new(big.Int).Mul(params.MinimumDifficulty[params.REGION], big.NewInt(2)), params.MinimumDifficulty[params.ZONE]}
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

// Exported for fuzzing
var FrontierDifficultyCalulator = calcDifficultyFrontier

// Author implements consensus.Engine, returning the header's coinbase as the
// proof-of-work verified author of the block.
func (blake3 *Blake3) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase[types.QuaiNetworkContext], nil
}

// VerifyHeader checks whether a header conforms to the consensus rules of the Blake3 engine.
func (blake3 *Blake3) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header, seal bool) error {
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
	return blake3.verifyHeader(chain, header, parent, false, seal, time.Now().Unix())
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
// concurrently. The method returns a quit channel to abort the operations and
// a results channel to retrieve the async verifications.
func (blake3 *Blake3) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
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
				errors[index] = blake3.verifyHeaderWorker(chain, headers, seals, index, unixNow)
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

func (blake3 *Blake3) verifyHeaderWorker(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool, index int, unixNow int64) error {
	var parent *types.Header
	if index == 0 {
		parent = chain.GetHeader(headers[0].ParentHash[types.QuaiNetworkContext], headers[0].Number[types.QuaiNetworkContext].Uint64()-1)
	} else if headers[index-1].Hash() == headers[index].ParentHash[types.QuaiNetworkContext] {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	return blake3.verifyHeader(chain, headers[index], parent, false, seals[index], unixNow)
}

// VerifyUncles verifies that the given block's uncles conform to the consensus rules
func (blake3 *Blake3) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
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
		if ancestorHeader.UncleHash[types.QuaiNetworkContext] != types.EmptyUncleHash[types.QuaiNetworkContext] {
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
		if err := blake3.verifyHeader(chain, uncle, ancestors[uncle.ParentHash[types.QuaiNetworkContext]], true, true, time.Now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// verifyHeader checks whether a header conforms to the consensus rules
func (blake3 *Blake3) verifyHeader(chain consensus.ChainHeaderReader, header, parent *types.Header, uncle bool, seal bool, unixNow int64) error {
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
	expected := blake3.CalcDifficulty(chain, header.Time, parent, types.QuaiNetworkContext)
	if blake3.config.Fakepow {
		expected = fakeDifficulties[types.QuaiNetworkContext]
	}
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
		if err := blake3.verifySeal(header); err != nil {
			return err
		}
	}
	// Verify that Location is same as config
	if err := verifyLocation(header.Location, chain.Config().Location); err != nil {
		return err
	}

	// Verify Location is in ontology described by MapContext
	if err := verifyInsideLocation(header.Location, header.Number, chain.Config()); err != nil {
		return err
	}

	if err := misc.VerifyForkHashes(chain.Config(), header, uncle); err != nil {
		return err
	}
	return nil
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
func (blake3 *Blake3) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header, context int) *big.Int {
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
		return params.GenesisDifficulty[types.QuaiNetworkContext]
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
	if diff.Cmp(params.MinimumDifficulty[types.QuaiNetworkContext]) < 0 {
		diff.Set(params.MinimumDifficulty[types.QuaiNetworkContext])
	}

	periodCount := new(big.Int).Add(parent.Number[context], big1)
	periodCount.Div(periodCount, expDiffPeriod)
	if periodCount.Cmp(big1) > 0 {
		// diff = diff + 2^(periodCount - 2)
		expDiff := periodCount.Sub(periodCount, big2)
		expDiff.Exp(big2, expDiff, nil)
		diff.Add(diff, expDiff)
		diff = math.BigMax(diff, params.MinimumDifficulty[types.QuaiNetworkContext])
	}
	return diff
}

// verifySeal checks whether a block satisfies the PoW difficulty requirements,
func (blake3 *Blake3) verifySeal(header *types.Header) error {
	difficulty := header.Difficulty[types.QuaiNetworkContext]
	// If we are a faker, override the difficulty with the appropriate fake difficulty
	if blake3.config.Fakepow {
		difficulty = fakeDifficulties[types.QuaiNetworkContext]
	}
	// Ensure that we have a valid difficulty for the block
	if difficulty.Sign() <= 0 {
		return errInvalidDifficulty
	}
	// Check the SealHash meets the difficulty target
	target := new(big.Int).Div(big2e256, difficulty)
	if new(big.Int).SetBytes(blake3.SealHash(header).Bytes()).Cmp(target) > 0 {
		return errInvalidPoW
	}
	return nil
}

// Prepare implements consensus.Engine, initializing the difficulty field of a
// header to conform to the protocol. The changes are done inline.
func (blake3 *Blake3) Prepare(chain consensus.ChainHeaderReader, header *types.Header) error {
	parent := chain.GetHeader(header.ParentHash[types.QuaiNetworkContext], header.Number[types.QuaiNetworkContext].Uint64()-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Difficulty[types.QuaiNetworkContext] = blake3.CalcDifficulty(chain, header.Time, parent, types.QuaiNetworkContext)
	return nil
}

// Finalize implements consensus.Engine, accumulating the block and uncle rewards,
// setting the final state on the header
func (blake3 *Blake3) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header) {
	// Accumulate any block and uncle rewards and commit the final state root
	accumulateRewards(chain.Config(), state, header, uncles)
	header.Root[types.QuaiNetworkContext] = state.IntermediateRoot(chain.Config().IsEIP158(header.Number[types.QuaiNetworkContext]))
}

// FinalizeAndAssemble implements consensus.Engine, accumulating the block and
// uncle rewards, setting the final state and assembling the block.
func (blake3 *Blake3) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	// Finalize block
	blake3.Finalize(chain, header, state, txs, uncles)

	// Header seems complete, assemble into a block and return
	return types.NewBlock(header, txs, uncles, receipts, trie.NewStackTrie(nil)), nil
}

// SealHash returns the hash of a block prior to it being sealed.
func (blake3 *Blake3) SealHash(header *types.Header) (hash common.Hash) {
	hasher := blake3hash.New(32, nil)
	hasher.Reset()

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
	enc = append(enc, header.Nonce)
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
func verifyLocation(location []byte, configLocation []byte) error {
	switch types.QuaiNetworkContext {
	case 0:
		return nil
	case 1:
		if location[0] != configLocation[0] {
			return consensus.ErrInvalidLocation
		} else {
			return nil
		}
	case 2:
		if !bytes.Equal(location, configLocation) {
			return consensus.ErrInvalidLocation
		} else {
			return nil
		}
	default:
		return consensus.ErrInvalidLocation
	}
}

// Verifies that Location value is valid inside MapContext ontology.
// Returns MapContext for error handling purposes.
func verifyInsideLocation(location []byte, number []*big.Int, config *params.ChainConfig) error {
	regionLocation := int(location[0])
	zoneLocation := int(location[1])

	switch {
	case config.IsFuller(number[0]): // Fuller = [3,3]
		return checkInsideCurrent(regionLocation, zoneLocation, params.FullerOntology)
	default:
		return consensus.ErrInvalidOntology
	}
}

// Verifies that Location is valid inside current MapContext ontology.
func checkInsideCurrent(regionLoc int, zoneLoc int, ontology []int) error {
	if regionLoc < 1 || regionLoc > ontology[0] {
		return consensus.ErrInvalidOntology
	}
	if zoneLoc < 1 || zoneLoc > ontology[1] {
		return consensus.ErrInvalidOntology
	}
	return nil
}
