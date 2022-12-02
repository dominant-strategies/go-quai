package blake3pow

import (
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"time"

	mapset "github.com/deckarep/golang-set"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/trie"
	"lukechampine.com/blake3"
)

// Blake3pow proof-of-work protocol constants.
var (
	maxUncles                     = 2         // Maximum number of uncles allowed in a single block
	allowedFutureBlockTimeSeconds = int64(15) // Max seconds from current time allowed for blocks, before they're considered future blocks

	ContextTimeFactor = big10
	ZoneBlockReward   = big.NewInt(5e+18)
	RegionBlockReward = new(big.Int).Mul(ZoneBlockReward, big3)
	PrimeBlockReward  = new(big.Int).Mul(RegionBlockReward, big3)
)

// Some useful constants to avoid constant memory allocs for them.
var (
	expDiffPeriod = big.NewInt(100000)
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
	errInvalidPoW        = errors.New("invalid proof-of-work")
)

// Author implements consensus.Engine, returning the header's coinbase as the
// proof-of-work verified author of the block.
func (blake3pow *Blake3pow) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase(), nil
}

// VerifyHeader checks whether a header conforms to the consensus rules of the
// stock Ethereum blake3pow engine.
func (blake3pow *Blake3pow) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header, seal bool) error {
	// If we're running a full engine faking, accept any input as valid
	if blake3pow.config.PowMode == ModeFullFake {
		return nil
	}
	// Short circuit if the header is known, or its parent not
	number := header.NumberU64()
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	parent := chain.GetHeader(header.ParentHash(), number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	// Sanity checks passed, do a proper verification
	return blake3pow.verifyHeader(chain, header, parent, false, seal, time.Now().Unix())
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
// concurrently. The method returns a quit channel to abort the operations and
// a results channel to retrieve the async verifications.
func (blake3pow *Blake3pow) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	// If we're running a full engine faking, accept any input as valid
	if blake3pow.config.PowMode == ModeFullFake || len(headers) == 0 {
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
				errors[index] = blake3pow.verifyHeaderWorker(chain, headers, seals, index, unixNow)
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

func (blake3pow *Blake3pow) verifyHeaderWorker(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool, index int, unixNow int64) error {
	var parent *types.Header
	if index == 0 {
		parent = chain.GetHeader(headers[0].ParentHash(), headers[0].NumberU64()-1)
	} else if headers[index-1].Hash() == headers[index].ParentHash() {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	return blake3pow.verifyHeader(chain, headers[index], parent, false, seals[index], unixNow)
}

// VerifyUncles verifies that the given block's uncles conform to the consensus
// rules of the stock Ethereum blake3pow engine.
func (blake3pow *Blake3pow) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	// If we're running a full engine faking, accept any input as valid
	if blake3pow.config.PowMode == ModeFullFake {
		return nil
	}
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
		if ancestorHeader.UncleHash() != types.EmptyUncleHash {
			// Need to add those uncles to the banned list too
			ancestor := chain.GetBlock(parent, number)
			if ancestor == nil {
				break
			}
			for _, uncle := range ancestor.Uncles() {
				uncles.Add(uncle.Hash())
			}
		}
		parent, number = ancestorHeader.ParentHash(), number-1
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
		if ancestors[uncle.ParentHash()] == nil || uncle.ParentHash() == block.ParentHash() {
			return errDanglingUncle
		}
		if err := blake3pow.verifyHeader(chain, uncle, ancestors[uncle.ParentHash()], true, true, time.Now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// verifyHeader checks whether a header conforms to the consensus rules
func (blake3pow *Blake3pow) verifyHeader(chain consensus.ChainHeaderReader, header, parent *types.Header, uncle bool, seal bool, unixNow int64) error {
	// Ensure that the header's extra-data section is of a reasonable size
	if uint64(len(header.Extra())) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra-data too long: %d > %d", len(header.Extra()), params.MaximumExtraDataSize)
	}
	// Verify the header's timestamp
	if !uncle {
		if header.Time() > uint64(unixNow+allowedFutureBlockTimeSeconds) {
			return consensus.ErrFutureBlock
		}
	}

	if header.Time() < parent.Time() {
		return errOlderBlockTime
	}

	// Verify the block's difficulty based on its timestamp and parent's difficulty
	expected := blake3pow.CalcDifficulty(chain, parent)

	if expected.Cmp(header.Difficulty()) != 0 {
		return fmt.Errorf("invalid difficulty: have %v, want %v", header.Difficulty(), expected)
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
	// Verify the block's gas usage and (if applicable) verify the base fee.
	if !chain.Config().IsLondon(header.Number()) {
		// Verify BaseFee not present before EIP-1559 fork.
		if header.BaseFee() != nil {
			return fmt.Errorf("invalid baseFee before fork: have %d, expected 'nil'", header.BaseFee())
		}
		if err := misc.VerifyGaslimit(parent.GasLimit(), header.GasLimit()); err != nil {
			return err
		}
	} else if err := misc.VerifyEip1559Header(chain.Config(), parent, header); err != nil {
		// Verify the header's EIP-1559 attributes.
		return err
	}
	// Verify that the block number is parent's +1
	if diff := new(big.Int).Sub(header.Number(), parent.Number()); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}
	// Verify the engine specific seal securing the block
	if seal {
		if err := blake3pow.verifySeal(chain, header, false); err != nil {
			return err
		}
	}
	// If all checks passed, validate any special fields for hard forks
	if err := misc.VerifyDAOHeaderExtraData(chain.Config(), header); err != nil {
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
func (blake3pow *Blake3pow) CalcDifficulty(chain consensus.ChainHeaderReader, parent *types.Header) *big.Int {
	nodeCtx := common.NodeLocation.Context()

	// https://github.com/ethereum/EIPs/issues/100.
	// algorithm:
	// diff = (parent_diff +
	//         (parent_diff / 2048 * max((2 if len(parent.uncles) else 1) - ((timestamp - parent.timestamp) // 9), -99))
	//        ) + 2^(periodCount - 2)

	time := parent.Time()

	if parent.Hash() == chain.Config().GenesisHash {
		return parent.Difficulty()
	}

	parentOfParent := chain.GetHeaderByHash(parent.ParentHash())

	bigTime := new(big.Int).SetUint64(time)
	bigParentTime := new(big.Int).SetUint64(parentOfParent.Time())

	// holds intermediate values to make the algo easier to read & audit
	x := new(big.Int)
	y := new(big.Int)

	// (2 if len(parent_uncles) else 1) - (block_timestamp - parent_timestamp) // duration_limit
	x.Sub(bigTime, bigParentTime)
	x.Div(x, params.DurationLimit[nodeCtx])
	if parent.UncleHash() == types.EmptyUncleHash {
		x.Sub(big1, x)
	} else {
		x.Sub(big2, x)
	}
	// max((2 if len(parent_uncles) else 1) - (block_timestamp - parent_timestamp) // 9, -99)
	if x.Cmp(bigMinus99) < 0 {
		x.Set(bigMinus99)
	}
	// parent_diff + (parent_diff / 2048 * max((2 if len(parent.uncles) else 1) - ((timestamp - parent.timestamp) // 9), -99))
	y.Div(parent.Difficulty(), params.DifficultyBoundDivisor)
	x.Mul(y, x)
	x.Add(parent.Difficulty(), x)

	// minimum difficulty can ever be (before exponential factor)
	if x.Cmp(params.MinimumDifficulty[nodeCtx]) < 0 {
		x.Set(params.MinimumDifficulty[nodeCtx])
	}

	return x
}

func (blake3pow *Blake3pow) HasCoincidentDifficulty(header *types.Header) bool {
	nodeCtx := common.NodeLocation.Context()

	// Since the Prime chain is the highest order, it cannot have coincident blocks
	if nodeCtx > common.PRIME_CTX {
		domCtx := nodeCtx - 1
		domTarget := new(big.Int).Div(big2e256, header.Difficulty(domCtx))
		if new(big.Int).SetBytes(header.Hash().Bytes()).Cmp(domTarget) <= 0 {
			return true
		}
	}
	return false
}

// verifySeal checks whether a block satisfies the PoW difficulty requirements,
// either using the usual blake3pow cache for it, or alternatively using a full DAG
// to make remote mining fast.
func (blake3pow *Blake3pow) verifySeal(chain consensus.ChainHeaderReader, header *types.Header, fulldag bool) error {
	// If we're running a fake PoW, accept any seal as valid
	if blake3pow.config.PowMode == ModeFake || blake3pow.config.PowMode == ModeFullFake {
		time.Sleep(blake3pow.fakeDelay)
		if blake3pow.fakeFail == header.Number().Uint64() {
			return errInvalidPoW
		}
		return nil
	}
	// If we're running a shared PoW, delegate verification to it
	if blake3pow.shared != nil {
		return blake3pow.shared.verifySeal(chain, header, fulldag)
	}
	// Ensure that we have a valid difficulty for the block
	if header.Difficulty().Sign() <= 0 {
		return errInvalidDifficulty
	}
	// Check that SealHash meets the difficulty target
	target := new(big.Int).Div(big2e256, header.Difficulty())
	if new(big.Int).SetBytes(header.Hash().Bytes()).Cmp(target) > 0 {
		return errInvalidPoW
	}
	return nil
}

// Prepare implements consensus.Engine, initializing the difficulty field of a
// header to conform to the blake3pow protocol. The changes are done inline.
func (blake3pow *Blake3pow) Prepare(chain consensus.ChainHeaderReader, header *types.Header, parent *types.Header) error {
	header.SetDifficulty(blake3pow.CalcDifficulty(chain, parent))
	return nil
}

// Finalize implements consensus.Engine, accumulating the block and uncle rewards,
// setting the final state on the header
func (blake3pow *Blake3pow) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header) {
	// Accumulate any block and uncle rewards and commit the final state root
	accumulateRewards(chain.Config(), state, header, uncles)
	header.SetRoot(state.IntermediateRoot(chain.Config().IsEIP158(header.Number())))
}

// FinalizeAndAssemble implements consensus.Engine, accumulating the block and
// uncle rewards, setting the final state and assembling the block.
func (blake3pow *Blake3pow) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	// Finalize block
	blake3pow.Finalize(chain, header, state, txs, uncles)

	// Header seems complete, assemble into a block and return
	return types.NewBlock(header, txs, uncles, receipts, trie.NewStackTrie(nil)), nil
}

// headerData comprises all data fields of the header, excluding the nonce, so
// that the nonce may be independently adjusted in the work algorithm.
type headerData struct {
	ParentHash  []common.Hash
	UncleHash   []common.Hash
	Coinbase    []common.Address
	Root        []common.Hash
	TxHash      []common.Hash
	ReceiptHash []common.Hash
	Bloom       []types.Bloom
	Difficulty  []*big.Int
	Number      []*big.Int
	GasLimit    []uint64
	GasUsed     []uint64
	BaseFee     []*big.Int
	Location    common.Location
	Time        uint64
	Extra       []byte
	Nonce       types.BlockNonce
}

// SealHash returns the hash of a block prior to it being sealed.
func (blake3pow *Blake3pow) SealHash(header *types.Header) (hash common.Hash) {
	hasher := blake3.New(32, nil)
	hasher.Reset()
	hdata := headerData{
		ParentHash:  make([]common.Hash, common.HierarchyDepth),
		UncleHash:   make([]common.Hash, common.HierarchyDepth),
		Coinbase:    make([]common.Address, common.HierarchyDepth),
		Root:        make([]common.Hash, common.HierarchyDepth),
		TxHash:      make([]common.Hash, common.HierarchyDepth),
		ReceiptHash: make([]common.Hash, common.HierarchyDepth),
		Bloom:       make([]types.Bloom, common.HierarchyDepth),
		Difficulty:  make([]*big.Int, common.HierarchyDepth),
		Number:      make([]*big.Int, common.HierarchyDepth),
		GasLimit:    make([]uint64, common.HierarchyDepth),
		GasUsed:     make([]uint64, common.HierarchyDepth),
		BaseFee:     make([]*big.Int, common.HierarchyDepth),
		Location:    header.Location(),
		Time:        header.Time(),
		Extra:       header.Extra(),
	}
	for i := 0; i < common.HierarchyDepth; i++ {
		hdata.ParentHash[i] = header.ParentHash(i)
		hdata.UncleHash[i] = header.UncleHash(i)
		hdata.Coinbase[i] = header.Coinbase(i)
		hdata.Root[i] = header.Root(i)
		hdata.TxHash[i] = header.TxHash(i)
		hdata.ReceiptHash[i] = header.ReceiptHash(i)
		hdata.Bloom[i] = header.Bloom(i)
		hdata.Difficulty[i] = header.Difficulty(i)
		hdata.Number[i] = header.Number(i)
		hdata.GasLimit[i] = header.GasLimit(i)
		hdata.GasUsed[i] = header.GasUsed(i)
		hdata.BaseFee[i] = header.BaseFee(i)
	}
	rlp.Encode(hasher, hdata)
	hash.SetBytes(hasher.Sum(hash[:0]))
	return hash
}

// AccumulateRewards credits the coinbase of the given block with the mining
// reward. The total reward consists of the static block reward and rewards for
// included uncles. The coinbase of each uncle block is also rewarded.
func accumulateRewards(config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) {
	// Select the correct block reward based on chain progression
	blockReward := misc.CalculateReward()

	// Accumulate the rewards for the miner and any included uncles
	reward := new(big.Int).Set(blockReward)
	r := new(big.Int)
	for _, uncle := range uncles {
		r.Add(uncle.Number(), big8)
		r.Sub(r, header.Number())
		r.Mul(r, blockReward)
		r.Div(r, big8)
		state.AddBalance(uncle.Coinbase(), r)

		r.Div(blockReward, big32)
		reward.Add(reward, r)
	}
	state.AddBalance(header.Coinbase(), reward)
}
