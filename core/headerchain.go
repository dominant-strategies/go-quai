package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/internal/telemetry"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru/v2"
	"modernc.org/mathutil"
)

const (
	headerCacheLimit       = 25
	numberCacheLimit       = 2048
	c_subRollupCacheSize   = 50
	primeHorizonThreshold  = 20
	c_powCacheLimit        = 1000
	c_calcOrderCacheLimit  = 10000
	c_zoneHorizonThreshold = 60
)

var (
	errInvalidEfficiencyScore = errors.New("unable to compute efficiency score")
	errInvalidWorkShareDist   = errors.New("workshare is at distance more than WorkSharesInclusionDepth")
)

type calcOrderResponse struct {
	intrinsicEntropy *big.Int
	order            int
}

// getPendingEtxsRollup gets the pendingEtxsRollup rollup from appropriate Region
type getPendingEtxsRollup func(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxsRollup, error)

// getPendingEtxs gets the pendingEtxs from the appropriate Zone
type getPendingEtxs func(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxs, error)

type getPrimeBlock func(blockHash common.Hash) *types.WorkObject

type getKQuaiAndUpdateBit func(blockHash common.Hash) (*big.Int, uint8, error)

type HeaderChain struct {
	config    *params.ChainConfig
	powConfig params.PowConfig

	bc     *BodyDb
	engine []consensus.Engine
	pool   *TxPool

	currentExpansionNumber uint8

	chainHeadFeed event.Feed
	unlocksFeed   event.Feed
	chainSideFeed event.Feed
	workshareFeed event.Feed
	scope         event.SubscriptionScope

	headerDb      ethdb.Database
	genesisHeader *types.WorkObject

	currentHeader atomic.Value                              // Current head of the header chain (may be above the block chain!)
	headerCache   *lru.Cache[common.Hash, types.WorkObject] // Cache for the most recent block headers
	numberCache   *lru.Cache[common.Hash, uint64]           // Cache for the most recent block numbers

	fetchPEtxRollup        getPendingEtxsRollup
	fetchPEtx              getPendingEtxs
	fetchKQuaiAndUpdateBit getKQuaiAndUpdateBit

	fetchPrimeBlock getPrimeBlock

	pendingEtxsRollup *lru.Cache[common.Hash, types.PendingEtxsRollup]
	pendingEtxs       *lru.Cache[common.Hash, types.PendingEtxs]
	blooms            *lru.Cache[common.Hash, types.Bloom]
	subRollupCache    *lru.Cache[common.Hash, types.Transactions]

	wg            sync.WaitGroup // chain processing wait group for shutting down
	running       int32          // 0 if chain is running, 1 when stopped
	procInterrupt int32          // interrupt signaler for block processing

	headermu        sync.RWMutex
	heads           []*types.WorkObject
	slicesRunning   []common.Location
	processingState bool

	powHashCache *lru.Cache[common.Hash, common.Hash]

	calcOrderCache *lru.Cache[common.Hash, calcOrderResponse]

	logger *log.Logger
}

// Returns the HeaderChain struct for the use of the tests
func NewTestHeaderChain() *HeaderChain {
	return &HeaderChain{}
}

// NewHeaderChain creates a new HeaderChain structure. ProcInterrupt points
// to the parent's interrupt semaphore.
func NewHeaderChain(db ethdb.Database, powConfig params.PowConfig, engine []consensus.Engine, pEtxsRollupFetcher getPendingEtxsRollup, pEtxsFetcher getPendingEtxs, primeBlockFetcher getPrimeBlock, kQuaiAndUpdateBitGetter getKQuaiAndUpdateBit, chainConfig *params.ChainConfig, cacheConfig *CacheConfig, txLookupLimit *uint64, vmConfig vm.Config, slicesRunning []common.Location, currentExpansionNumber uint8, logger *log.Logger) (*HeaderChain, error) {

	nodeCtx := chainConfig.Location.Context()

	hc := &HeaderChain{
		config:                 chainConfig,
		powConfig:              powConfig,
		headerDb:               db,
		engine:                 engine,
		slicesRunning:          slicesRunning,
		fetchPEtxRollup:        pEtxsRollupFetcher,
		fetchPEtx:              pEtxsFetcher,
		fetchPrimeBlock:        primeBlockFetcher,
		fetchKQuaiAndUpdateBit: kQuaiAndUpdateBitGetter,
		logger:                 logger,
		currentExpansionNumber: currentExpansionNumber,
	}

	headerCache, _ := lru.New[common.Hash, types.WorkObject](headerCacheLimit)
	hc.headerCache = headerCache

	numberCache, _ := lru.New[common.Hash, uint64](numberCacheLimit)
	hc.numberCache = numberCache

	powHashCache, _ := lru.New[common.Hash, common.Hash](c_powCacheLimit)
	hc.powHashCache = powHashCache

	calcOrderCache, _ := lru.New[common.Hash, calcOrderResponse](c_calcOrderCacheLimit)
	hc.calcOrderCache = calcOrderCache

	genesisHash := hc.GetGenesisHashes()[0]
	genesisNumber := rawdb.ReadHeaderNumber(db, genesisHash)
	if genesisNumber == nil {
		return nil, ErrNoGenesis
	}
	hc.genesisHeader = rawdb.ReadWorkObject(db, *genesisNumber, genesisHash, types.BlockObject)
	if bytes.Equal(chainConfig.Location, common.Location{0, 0}) {
		if hc.genesisHeader == nil {
			return nil, ErrNoGenesis
		}
		if hc.genesisHeader.Hash() != hc.config.DefaultGenesisHash {
			return nil, fmt.Errorf("genesis hash mismatch: have %x, want %x", hc.genesisHeader.Hash(), chainConfig.DefaultGenesisHash)
		}
	}
	hc.logger.WithField("Hash", hc.genesisHeader.Hash()).Info("Genesis")
	//Load any state that is in our db
	if err := hc.loadLastState(); err != nil {
		return nil, err
	}

	var err error
	hc.bc, err = NewBodyDb(db, engine, hc, chainConfig, cacheConfig, txLookupLimit, vmConfig, slicesRunning)
	if err != nil {
		return nil, err
	}

	// Record if the chain is processing state
	hc.processingState = hc.setStateProcessing()

	pendingEtxsRollup, _ := lru.New[common.Hash, types.PendingEtxsRollup](c_maxPendingEtxsRollup)
	hc.pendingEtxsRollup = pendingEtxsRollup

	var pendingEtxs *lru.Cache[common.Hash, types.PendingEtxs]
	if nodeCtx == common.PRIME_CTX {
		pendingEtxs, _ = lru.New[common.Hash, types.PendingEtxs](c_maxPendingEtxBatchesPrime)
		hc.pendingEtxs = pendingEtxs
	} else {
		pendingEtxs, _ = lru.New[common.Hash, types.PendingEtxs](c_maxPendingEtxBatchesRegion)
		hc.pendingEtxs = pendingEtxs
	}

	blooms, _ := lru.New[common.Hash, types.Bloom](c_maxBloomFilters)
	hc.blooms = blooms

	subRollupCache, _ := lru.New[common.Hash, types.Transactions](c_subRollupCacheSize)
	hc.subRollupCache = subRollupCache

	// Initialize the heads slice
	heads := make([]*types.WorkObject, 0)
	hc.heads = heads

	return hc, nil
}

// GetEngineForPowID returns the consensus engine for the given PowID
func (hc *HeaderChain) GetEngineForPowID(powID types.PowID) consensus.Engine {
	// Engine mapping:
	// engine[0] = Progpow
	// engine[1] = Kawpow
	switch powID {
	case types.Progpow:
		if len(hc.engine) > 0 {
			return hc.engine[0]
		}
	case types.Kawpow:
		if len(hc.engine) > 1 {
			return hc.engine[1]
		}
	}
	// Default to first engine if not found
	if len(hc.engine) > 0 {
		return hc.engine[0]
	}
	return nil
}

// GetEngineForHeader returns the consensus engine for the given header
func (hc *HeaderChain) GetEngineForHeader(header *types.WorkObjectHeader) consensus.Engine {
	// Check if header has AuxPow to determine which engine to use
	if header.AuxPow() != nil && header.PrimeTerminusNumber().Uint64() >= params.KawPowForkBlock {
		return hc.GetEngineForPowID(types.Kawpow)
	}
	// Default to Progpow if no AuxPow
	return hc.GetEngineForPowID(types.Progpow)
}

// NOTE: This function is only used in the append print
func (hc *HeaderChain) HeaderIntrinsicLogEntropy(ws *types.WorkObjectHeader) (*big.Int, error) {
	// If auxpow is not nil and its not kawpow or progpow, we need to compute it directly as
	// we dont have engine interface for sha and scrypt
	if ws.AuxPow() == nil || ws.AuxPow().PowID() <= types.Kawpow {
		engine := hc.GetEngineForHeader(ws)
		powHash, err := engine.ComputePowHash(ws)
		if err != nil {
			hc.logger.WithField("workshare", ws.Hash()).Error("Failed to compute pow hash for workshare")
			return big.NewInt(0), err
		}
		return common.IntrinsicLogEntropy(powHash), nil
	}

	return big.NewInt(0), nil
}

func CalculateKawpowShareDiff(header *types.WorkObjectHeader) *big.Int {
	if header.PrimeTerminusNumber().Uint64() < params.KawPowForkBlock {
		return big.NewInt(0)
	}
	// kawpowsharetarget = max(0, 8 * 2^32 - (shaSharesAverage + scryptSharesAverage))
	// kawpowShareDiff = difficulty * 2^32 / kawpowShareTarget

	// If sha or scrypt target is less than the share target then use the count,
	// otherwise use the target
	shaSharesAverage := math.BigMin(header.ShaDiffAndCount().Count(), header.ShaShareTarget())
	scryptSharesAverage := math.BigMin(header.ScryptDiffAndCount().Count(), header.ScryptShareTarget())

	nonKawpowShareTarget := new(big.Int).Add(shaSharesAverage, scryptSharesAverage)

	maxTarget := new(big.Int).Mul(big.NewInt(int64(params.ExpectedWorksharesPerBlock)), common.Big2e32)

	// If the non-kawpow share target is greater than or equal to the max
	// target, then the kawpow share diff is the block difficulty
	if maxTarget.Cmp(nonKawpowShareTarget) <= 0 {
		return header.Difficulty()
	}

	// Calculate the kawpow share target by subtracting the non-kawpow share
	// target from the max target + 2^32 because on expectation to get x number
	// of shares that are not block, the difficulty has to be divided by (x+1),
	// so that on average the number of shares other than block is x and you
	// have a normal block
	kawpowShareTarget := new(big.Int).Sub(new(big.Int).Add(maxTarget, common.Big2e32), nonKawpowShareTarget)

	// Precision is 1/100th of percent
	// If the quai hash rate reaches 75% of the ravencoin hash rate then we
	// start applying a discount to the kawpow share target, linearly decreasing
	// it until it reaches 0 at 90%
	quaiDiffAsPercentOfRavencoin := new(big.Int).Div(new(big.Int).Mul(header.Difficulty(), params.RavencoinDiffPercentage), header.KawpowDifficulty())
	if quaiDiffAsPercentOfRavencoin.Cmp(params.RavencoinDiffCutoffStart) >= 0 {
		// At the 90% threshold, no kawpow shares are allowed
		var kawpowShareTargetWithDiscount *big.Int
		if quaiDiffAsPercentOfRavencoin.Cmp(params.RavencoinDiffCutoffEnd) >= 0 {
			kawpowShareTargetWithDiscount = new(big.Int).Set(common.Big2e32)
		} else {
			// Apply a linear discount
			errInDiff := new(big.Int).Sub(params.RavencoinDiffCutoffEnd, quaiDiffAsPercentOfRavencoin)
			kawpowShareTargetWithDiscount = new(big.Int).Mul(new(big.Int).Add(maxTarget, common.Big2e32), errInDiff)
			kawpowShareTargetWithDiscount = new(big.Int).Div(kawpowShareTargetWithDiscount, params.RavencoinDiffCutoffRange)
		}

		kawpowShareTarget = new(big.Int).Set(math.BigMin(kawpowShareTarget, kawpowShareTargetWithDiscount))
	}

	// If the kawpow share target is less than 1, then the kawpow share diff is
	// the block difficulty, This should never happen but adding it for sanity
	// check
	if kawpowShareTarget.Cmp(common.Big2e32) < 0 {
		return header.Difficulty()
	}

	kawpowShareDiff := new(big.Int).Mul(header.Difficulty(), common.Big2e32)
	kawpowShareDiff = new(big.Int).Div(kawpowShareDiff, kawpowShareTarget)

	return kawpowShareDiff
}

// CalculateKawpowDifficulty calculates the average ravencoin difficulty
// normalized to quai block time. This only updates if the auxpow used in the
// block is lively
func (hc *HeaderChain) CalculateKawpowDifficulty(parent, header *types.WorkObject) *big.Int {
	if header.PrimeTerminusNumber().Uint64() == params.KawPowForkBlock {
		return params.InitialKawpowDiff
	}

	if parent.AuxPow() != nil {
		// Also need to make sure that the header auxtemplate is lively, only
		// then update
		scritSig := types.ExtractScriptSigFromCoinbaseTx(parent.AuxPow().Transaction())
		signatureTime, err := types.ExtractSignatureTimeFromCoinbase(scritSig)
		// If the share is unlive, return the previous difficulty
		if err != nil || signatureTime+params.ShareLivenessTime < parent.AuxPow().Header().Timestamp() {
			return parent.KawpowDifficulty()
		}
		// Compare the current kawpow difficulty with the subsidy chain difficulty
		subsidyChainDiff := common.GetDifficultyFromBits(parent.AuxPow().Header().Bits())
		// Normalize the difficulty, to quai block time
		subsidyChainDiff = new(big.Int).Div(subsidyChainDiff, params.RavenQuaiBlockTimeRatio)

		// Taking a long term average
		newKawpowDiff := new(big.Int).Mul(parent.KawpowDifficulty(), new(big.Int).Sub(params.WorkShareEmaBlocks, common.Big1))
		newKawpowDiff = new(big.Int).Add(newKawpowDiff, subsidyChainDiff)
		newKawpowDiff = new(big.Int).Div(newKawpowDiff, params.WorkShareEmaBlocks)
		return newKawpowDiff

	} else {
		// If the parent is a transition progpow block, there is no information
		// to update the kawpow difficulty
		return parent.KawpowDifficulty()
	}
}

// WorkshareAllocation computes the number of shares available per algorithm
func (hc *HeaderChain) CalculateShareTarget(parent, header *types.WorkObject) (nonKawpowShares *big.Int) {
	if header.PrimeTerminusNumber().Uint64() == params.KawPowForkBlock {
		return params.TargetShaShares
	}

	var newShareTarget *big.Int
	// Compare the current kawpow difficulty with the subsidy chain difficulty
	subsidyChainDiff := parent.KawpowDifficulty()
	// maximum subsidy chain diff should be 75% of the parent difficulty
	maximumSubsidyChainDiff := new(big.Int).Div(new(big.Int).Mul(subsidyChainDiff, params.MaxSubsidyNumerator), params.MaxSubsidyDenominator)

	// calculate the difference
	difference := new(big.Int).Sub(parent.Difficulty(), maximumSubsidyChainDiff)
	// NOTE: Using shashare target in this calculation because sha and scrypt target
	// are the same
	newShareTarget = new(big.Int).Mul(difference, parent.ShaShareTarget())
	newShareTarget = newShareTarget.Div(newShareTarget, parent.Difficulty())
	newShareTarget = newShareTarget.Div(newShareTarget, new(big.Int).SetInt64(int64(params.BlocksPerDay)))
	newShareTarget = newShareTarget.Add(newShareTarget, parent.ShaShareTarget())

	// Make sure the new share target is within bounds
	newShareTarget = math.BigMax(newShareTarget, params.TargetShaShares)
	newShareTarget = math.BigMin(newShareTarget, params.MaxShaShares)

	return newShareTarget
}

// CalculatePowDiffAndCount calculates the new PoW difficulty and average number of work shares
func (hc *HeaderChain) CalculatePowDiffAndCount(parent *types.WorkObject, header *types.WorkObjectHeader, powId types.PowID) (newDiff, newAverageShares, newUncledShares *big.Int) {

	if header.PrimeTerminusNumber().Uint64() == params.KawPowForkBlock {
		quaiDiff := header.Difficulty()
		quaiHashRate := new(big.Int).Div(quaiDiff, params.DurationLimit)
		switch powId {
		case types.SHA_BTC, types.SHA_BCH:
			shaHashRate := new(big.Int).Mul(quaiHashRate, params.InitialShaDiffMultiple)
			shaDiff := new(big.Int).Mul(shaHashRate, params.ShaBlockTime)
			shaDiff = new(big.Int).Div(shaDiff, common.Big3) // Targeting 3 shares per block
			return shaDiff, params.TargetShaShares, big.NewInt(0)
		case types.Scrypt:
			scryptHashRate := new(big.Int).Mul(quaiHashRate, params.InitialScryptDiffMultiple)
			scryptDiff := new(big.Int).Mul(scryptHashRate, params.ScryptBlockTime)
			scryptDiff = new(big.Int).Div(scryptDiff, common.Big3) // Targeting 3 shares per block
			return scryptDiff, params.TargetShaShares, big.NewInt(0)
		default:
			return big.NewInt(0), big.NewInt(0), big.NewInt(0)
		}
	}

	// Get the sha and scrypt share counts from the tx pool
	_, countSha, uncledSha, countScrypt, uncledScrypt := hc.CountWorkSharesByAlgo(parent)

	var numShares, uncledShares *big.Int
	var shares *types.PowShareDiffAndCount

	switch powId {
	case types.SHA_BTC, types.SHA_BCH:
		shares = parent.ShaDiffAndCount()
		numShares = new(big.Int).Mul(big.NewInt(int64(countSha)), common.Big2e32)
		uncledShares = new(big.Int).Mul(big.NewInt(int64(uncledSha)), common.Big2e32)
	case types.Scrypt:
		shares = parent.ScryptDiffAndCount()
		numShares = new(big.Int).Mul(big.NewInt(int64(countScrypt)), common.Big2e32)
		uncledShares = new(big.Int).Mul(big.NewInt(int64(uncledScrypt)), common.Big2e32)
	default:
		return big.NewInt(0), big.NewInt(0), big.NewInt(0)
	}

	// numShares is the EMA of the number of work shares over last N blocks * 2^32
	// calculate the error between the target and actual number of work shares
	var error *big.Int
	switch powId {
	case types.SHA_BTC, types.SHA_BCH:
		error = new(big.Int).Sub(numShares, parent.ShaShareTarget())
	case types.Scrypt:
		error = new(big.Int).Sub(numShares, parent.ScryptShareTarget())
	default:
		return big.NewInt(0), big.NewInt(0), big.NewInt(0)
	}

	// Calculate the new difficulty based on the error
	// newDiff = prevDiff + (error * prevDiff)/(2^32 * c_difficultyAdjustDivisor)
	newDiff = new(big.Int).Mul(error, shares.Difficulty())

	// Multiplying by the binary log of the share diff, similar to the DAA, so
	// that, the gain is correct for a several magnitudes of share difficulty
	// Dividing by 30 here because the response of scrypt controller seems
	// stable, so its a noop for scrypt, but for sha it scales appropriately
	k, _ := mathutil.BinaryLog(new(big.Int).Set(shares.Difficulty()), common.MantBits)
	newDiff = new(big.Int).Mul(newDiff, big.NewInt(int64(k)))
	newDiff = new(big.Int).Div(newDiff, params.PowDiffAdjustmentFactor)

	newDiff = newDiff.Div(newDiff, common.Big2e32)
	newDiff = newDiff.Add(shares.Difficulty(), newDiff)

	// Calculate the new workshares
	newAverageShares = new(big.Int).Mul(shares.Count(), new(big.Int).Sub(params.WorkShareEmaBlocks, common.Big1))
	newAverageShares = newAverageShares.Add(newAverageShares, numShares)
	newAverageShares = newAverageShares.Div(newAverageShares, params.WorkShareEmaBlocks)

	newUncledShares = new(big.Int).Mul(shares.Uncled(), new(big.Int).Sub(params.WorkShareEmaBlocks, common.Big1))
	newUncledShares = newUncledShares.Add(newUncledShares, uncledShares)
	newUncledShares = newUncledShares.Div(newUncledShares, params.WorkShareEmaBlocks)

	return newDiff, newAverageShares, newUncledShares
}

// CountWorkSharesByAlgo counts the number of work shares by each algo in the given block
func (hc *HeaderChain) CountWorkSharesByAlgo(wo *types.WorkObject) (int, int, int, int, int) {
	// Need to calculate the long term average count for the number of shares
	uncles := wo.Body().Uncles()
	countKawPow := 0
	countSha := 0
	uncledShaCount := 0
	countScrypt := 0
	uncledScryptCount := 0
	for _, uncle := range uncles {
		if uncle.AuxPow() != nil {
			switch uncle.AuxPow().PowID() {
			case types.Kawpow:
				countKawPow++
			case types.SHA_BTC, types.SHA_BCH:
				countSha++
				if _, err := uncle.PrimaryCoinbase().InternalAddress(); err != nil {
					uncledShaCount++
				}
			case types.Scrypt:
				countScrypt++
				if _, err := uncle.PrimaryCoinbase().InternalAddress(); err != nil {
					uncledScryptCount++
				}
			}
		} else {
			countKawPow++ // Progpow doesnt have the auxpow field
		}
	}
	return countKawPow, countSha, uncledShaCount, countScrypt, uncledScryptCount
}

// DifficultyByAlgo(wo *types.WorkObject) returns the difficulty for each algo in the given block
func (hc *HeaderChain) DifficultyByAlgo(wo *types.WorkObject) (kawpow, sha, scrypt *big.Int) {
	kawpow = wo.Difficulty()
	if wo.WorkObjectHeader().ShaDiffAndCount() != nil {
		sha = wo.WorkObjectHeader().ShaDiffAndCount().Difficulty()
	} else {
		sha = big.NewInt(0)
	}
	if wo.WorkObjectHeader().ScryptDiffAndCount() != nil {
		scrypt = wo.WorkObjectHeader().ScryptDiffAndCount().Difficulty()
	} else {
		scrypt = big.NewInt(0)
	}
	return kawpow, sha, scrypt
}

// CollectSubRollup collects the rollup of ETXs emitted from the subordinate
// chain in the slice which emitted the given block.
func (hc *HeaderChain) CollectSubRollup(b *types.WorkObject) (types.Transactions, error) {
	nodeCtx := hc.NodeCtx()
	subRollup := types.Transactions{}
	if nodeCtx < common.ZONE_CTX {
		// Since in prime the pending etxs are stored in 2 parts, pendingEtxsRollup
		// consists of region header and subrollups
		for _, hash := range b.Manifest() {
			if nodeCtx == common.PRIME_CTX {
				pEtxRollup, err := hc.GetPendingEtxsRollup(hash, b.Location())
				if err == nil {
					subRollup = append(subRollup, pEtxRollup.EtxsRollup...)
				} else {
					// Try to get the pending etx from the Regions
					hc.fetchPEtxRollup(b.Hash(), hash, b.Location())
					return nil, ErrPendingEtxNotFound
				}
				// Region works normally as before collecting pendingEtxs for each hash in the manifest
			} else if nodeCtx == common.REGION_CTX {
				pendingEtxs, err := hc.GetPendingEtxs(hash)
				if err != nil {
					// Get the pendingEtx from the appropriate zone
					hc.fetchPEtx(b.Hash(), hash, b.Location())
					return nil, ErrPendingEtxNotFound
				}
				subRollup = append(subRollup, pendingEtxs.OutboundEtxs...)
			}
		}
	}
	return subRollup, nil
}

// GetPendingEtxs gets the pendingEtxs form the
func (hc *HeaderChain) GetPendingEtxs(hash common.Hash) (*types.PendingEtxs, error) {
	var pendingEtxs types.PendingEtxs
	// Look for pending ETXs first in pending ETX cache, then in database
	if res, ok := hc.pendingEtxs.Get(hash); ok {
		pendingEtxs = res
	} else if res := rawdb.ReadPendingEtxs(hc.headerDb, hash); res != nil {
		pendingEtxs = *res
	} else {
		hc.logger.WithField("hash", hash.String()).Trace("Unable to find pending etxs for hash in manifest")
		return nil, ErrPendingEtxNotFound
	}
	return &pendingEtxs, nil
}

func (hc *HeaderChain) GetPendingEtxsRollup(hash common.Hash, location common.Location) (*types.PendingEtxsRollup, error) {
	var rollups types.PendingEtxsRollup
	// Look for pending ETXs first in pending ETX cache, then in database
	if res, ok := hc.pendingEtxsRollup.Get(hash); ok {
		rollups = res
	} else if res := rawdb.ReadPendingEtxsRollup(hc.headerDb, hash); res != nil {
		rollups = *res
	} else {
		hc.logger.WithField("hash", hash.String()).Trace("Unable to find pending etx rollups for hash in manifest")
		return nil, ErrPendingEtxRollupNotFound
	}
	return &rollups, nil
}

// GetBloom gets the bloom from the cache or database
func (hc *HeaderChain) GetBloom(hash common.Hash) (*types.Bloom, error) {
	var bloom types.Bloom
	// Look for bloom first in bloom cache, then in database
	if res, ok := hc.blooms.Get(hash); ok {
		bloom = res
	} else if res := rawdb.ReadBloom(hc.headerDb, hash); res != nil {
		bloom = *res
	} else {
		hc.logger.WithField("hash", hash.String()).Trace("Unable to find bloom for hash in manifest")
		return nil, ErrBloomNotFound
	}
	return &bloom, nil
}

// Collect all emmitted ETXs since the last coincident block, but excluding
// those emitted in this block
func (hc *HeaderChain) CollectEtxRollup(b *types.WorkObject) (types.Transactions, error) {
	if hc.IsGenesisHash(b.Hash()) {
		return b.OutboundEtxs(), nil
	}
	parent := hc.GetBlock(b.ParentHash(hc.NodeCtx()), b.NumberU64(hc.NodeCtx())-1)
	if parent == nil {
		return nil, errors.New("parent not found")
	}
	return hc.collectInclusiveEtxRollup(parent)
}

func (hc *HeaderChain) collectInclusiveEtxRollup(b *types.WorkObject) (types.Transactions, error) {
	// Initialize the rollup with ETXs emitted by this block
	newEtxs := b.OutboundEtxs()
	// Terminate the search if we reached genesis
	if hc.IsGenesisHash(b.Hash()) {
		return newEtxs, nil
	}
	// Terminate the search on coincidence with dom chain
	if hc.IsDomCoincident(b) {
		return newEtxs, nil
	}
	// Recursively get the ancestor rollup, until a coincident ancestor is found
	ancestor := hc.GetBlock(b.ParentHash(hc.NodeCtx()), b.NumberU64(hc.NodeCtx())-1)
	if ancestor == nil {
		return nil, errors.New("ancestor not found")
	}
	etxRollup, err := hc.collectInclusiveEtxRollup(ancestor)
	if err != nil {
		return nil, err
	}
	etxRollup = append(etxRollup, newEtxs...)
	return etxRollup, nil
}

// CheckPowIdValidity checks the validity of the pow id for the given  block
// This check can only be used on a block/uncle
// 1) Before the kawpow fork, the pow id must be nil
// 2) After the kawpow fork and transition, the pow id must be kawpow
// 3) During the transition, the pow id has to be kawpow or auxpow has to be nil(progpow)
func (hc *HeaderChain) CheckPowIdValidity(wo *types.WorkObjectHeader) error {
	if wo == nil {
		return fmt.Errorf("wo is nil")
	}
	if wo.PrimeTerminusNumber().Uint64() < params.KawPowForkBlock {
		if wo.AuxPow() != nil {
			return fmt.Errorf("wo auxpow powid is not nil before kawpow fork")
		}
	}
	if wo.PrimeTerminusNumber().Uint64() >= params.KawPowForkBlock &&
		wo.AuxPow() != nil &&
		wo.AuxPow().PowID() != types.Kawpow {
		return fmt.Errorf("wo auxpow is not nil and not kawpow after the kawpow fork block")
	}
	if wo.PrimeTerminusNumber().Uint64() > params.KawPowForkBlock+params.KawPowTransitionPeriod {
		if wo.AuxPow() == nil {
			return fmt.Errorf("workshare auxpow powid is nil after kawpow transition")
		}
	}

	return nil
}

// Workshare can only be of
// 1) progpow pow before the kawpow fork
// 2) progpow, kawpow, btc, bch, litecoin in the transition period
// 3) kawpow, btc, bch, litecoin after the transition period
func (hc *HeaderChain) CheckPowIdValidityForWorkshare(wo *types.WorkObjectHeader) error {
	if wo == nil {
		return fmt.Errorf("wo is nil")
	}
	if wo.PrimeTerminusNumber().Uint64() < params.KawPowForkBlock {
		if wo.AuxPow() != nil {
			return fmt.Errorf("workshare auxpow powid is not progpow before kawpow fork")
		}
	}
	if wo.PrimeTerminusNumber().Uint64() >= params.KawPowForkBlock &&
		wo.PrimeTerminusNumber().Uint64() <= params.KawPowForkBlock+params.KawPowTransitionPeriod {
		if wo.AuxPow() != nil && wo.AuxPow().PowID() > types.Scrypt {
			return fmt.Errorf("workshare auxpow powid is not valid during kawpow transition")
		}
	}
	if wo.PrimeTerminusNumber().Uint64() > params.KawPowForkBlock+params.KawPowTransitionPeriod {
		if wo.AuxPow() == nil {
			return fmt.Errorf("workshare auxpow powid is nil after kawpow transition")
		}
		// This case is not possible but still dont want to allow progpow pow id
		if wo.AuxPow() != nil && wo.AuxPow().PowID() == types.Progpow {
			return fmt.Errorf("workshare auxpow powid is progpow after kawpow transition")
		}
		if wo.AuxPow() != nil && wo.AuxPow().PowID() > types.Scrypt {
			return fmt.Errorf("workshare auxpow powid is not valid during kawpow transition")
		}
	}
	return nil
}

// Append
func (hc *HeaderChain) AppendHeader(header *types.WorkObject) error {
	nodeCtx := hc.NodeCtx()
	hc.logger.WithFields(log.Fields{
		"Hash":     header.Hash(),
		"Number":   header.NumberArray(),
		"Location": header.Location,
		"Parent":   header.ParentHash(nodeCtx),
	}).Debug("Headerchain Append")

	err := hc.VerifyHeader(header)
	if err != nil {
		return err
	}

	// Verify the manifest matches expected
	// Load the manifest of headers preceding this header
	// note: prime manifest is non-existent, because a prime header cannot be
	// coincident with a higher order chain. So, this check is skipped for prime
	// nodes.
	if nodeCtx > common.PRIME_CTX {
		manifest := rawdb.ReadManifest(hc.headerDb, header.ParentHash(nodeCtx))
		if manifest == nil {
			return errors.New("manifest not found for parent")
		}
		if header.ManifestHash(nodeCtx) != types.DeriveSha(manifest, trie.NewStackTrie(nil)) {
			return errors.New("manifest does not match hash")
		}
	}

	// Verify the Interlink root hash matches the interlink
	if nodeCtx == common.PRIME_CTX {
		interlinkHashes := rawdb.ReadInterlinkHashes(hc.headerDb, header.ParentHash(nodeCtx))
		if interlinkHashes == nil {
			return errors.New("interlink hashes not found")
		}
		if header.InterlinkRootHash() != types.DeriveSha(interlinkHashes, trie.NewStackTrie(nil)) {
			return errors.New("interlink root hash does not match interlink")
		}
	}

	return nil
}

func (hc *HeaderChain) CalculateInterlink(block *types.WorkObject) (common.Hashes, error) {
	header := types.CopyWorkObject(block)
	var interlinkHashes common.Hashes
	if hc.IsGenesisHash(header.Hash()) {
		// On genesis, the interlink hashes are all the same and should start with genesis hash
		interlinkHashes = common.Hashes{header.Hash(), header.Hash(), header.Hash(), header.Hash()}
	} else {
		// check if parent belongs to any interlink level
		rank, err := hc.CalcRank(header)
		if err != nil {
			return nil, err
		}
		parentInterlinkHashes := rawdb.ReadInterlinkHashes(hc.headerDb, header.ParentHash(common.PRIME_CTX))
		if parentInterlinkHashes == nil {
			return nil, ErrSubNotSyncedToDom
		}
		if rank == 0 { // No change in the interlink hashes, so carry
			interlinkHashes = parentInterlinkHashes
		} else if rank > 0 && rank <= common.InterlinkDepth {
			interlinkHashes = parentInterlinkHashes
			// update the interlink hashes for each level below the rank
			for i := 0; i < rank; i++ {
				interlinkHashes[i] = header.Hash()
			}
		} else {
			hc.logger.Error("Not possible to find rank greater than the max interlink levels")
			return nil, errors.New("not possible to find rank greater than the max interlink levels")
		}
	}
	// Store the interlink hashes in the database
	rawdb.WriteInterlinkHashes(hc.headerDb, header.Hash(), interlinkHashes)
	return interlinkHashes, nil
}

func (hc *HeaderChain) CalculateManifest(header *types.WorkObject) types.BlockManifest {
	manifest := rawdb.ReadManifest(hc.headerDb, header.Hash())
	if manifest == nil {
		nodeCtx := hc.NodeCtx()
		// Compute and set manifest hash
		var manifest types.BlockManifest
		if nodeCtx == common.PRIME_CTX {
			// Nothing to do for prime chain
			manifest = types.BlockManifest{}
		} else if hc.IsDomCoincident(header) {
			manifest = types.BlockManifest{header.Hash()}
		} else {
			parentManifest := rawdb.ReadManifest(hc.headerDb, header.ParentHash(nodeCtx))
			manifest = append(parentManifest, header.Hash())
		}
		// write the manifest into the disk
		rawdb.WriteManifest(hc.headerDb, header.Hash(), manifest)
	}
	return manifest
}

func (hc *HeaderChain) ProcessingState() bool {
	return hc.processingState
}

func (hc *HeaderChain) setStateProcessing() bool {
	nodeCtx := hc.NodeCtx()
	for _, slice := range hc.slicesRunning {
		switch nodeCtx {
		case common.PRIME_CTX:
			return true
		case common.REGION_CTX:
			if slice.Region() == hc.NodeLocation().Region() {
				return true
			}
		case common.ZONE_CTX:
			if slice.Equal(hc.NodeLocation()) {
				return true
			}
		}
	}
	return false
}

// Append
func (hc *HeaderChain) AppendBlock(block *types.WorkObject) error {
	blockappend := time.Now()
	// Append block else revert header append
	logs, unlocks, err := hc.bc.Append(block)
	if err != nil {
		return err
	}

	if hc.NodeCtx() == common.ZONE_CTX {
		// Telemetry: move candidate workshares to 'candidate' LRU
		if hc.config.TelemetryEnabled {
			for _, u := range block.Uncles() {
				if hc.UncleWorkShareClassification(u) == types.Valid {
					telemetry.RemoveCandidateShare(u.Hash())
				}
			}
		}
	}

	if unlocks != nil && len(unlocks) > 0 {
		hc.unlocksFeed.Send(UnlocksEvent{
			Hash:    block.Hash(),
			Unlocks: unlocks,
		})
	}
	hc.logger.WithField("append block", common.PrettyDuration(time.Since(blockappend))).Debug("Time taken to")

	if len(logs) > 0 {
		hc.bc.logsFeed.Send(logs)
	}

	return nil
}

// SetCurrentHeader sets the current header based on the POEM choice
func (hc *HeaderChain) SetCurrentHeader(head *types.WorkObject) error {
	nodeCtx := hc.NodeCtx()

	prevHeader := hc.CurrentHeader()
	// if trying to set the same header, escape
	if prevHeader.Hash() == head.Hash() {
		return nil
	}

	// If head is the normal extension of canonical head, we can return by just wiring the canonical hash.
	if prevHeader.Hash() == head.ParentHash(hc.NodeCtx()) {
		rawdb.WriteCanonicalHash(hc.headerDb, head.Hash(), head.NumberU64(hc.NodeCtx()))
		if nodeCtx == common.ZONE_CTX {
			err := hc.AppendBlock(head)
			if err != nil {
				rawdb.DeleteCanonicalHash(hc.headerDb, head.NumberU64(hc.NodeCtx()))
				return err
			}
		}
		// write the head block hash to the db
		rawdb.WriteHeadBlockHash(hc.headerDb, head.Hash())
		hc.logger.WithFields(log.Fields{
			"Hash":   head.Hash(),
			"Number": head.NumberArray(),
		}).Info("Setting the current header")
		hc.currentHeader.Store(head)
		return nil
	}

	//Find a common header between the current header and the new head
	commonHeader, err := rawdb.FindCommonAncestor(hc.headerDb, prevHeader, head, nodeCtx)
	if err != nil {
		return err
	}
	// If common header is more than c_zoneHorizonThreshold blocks behind, dont reorg to it
	if prevHeader.NumberU64(common.ZONE_CTX) > params.MaxCodeSizeForkHeight && prevHeader.NumberU64(common.ZONE_CTX) > commonHeader.NumberU64(common.ZONE_CTX)+c_zoneHorizonThreshold {
		return errors.New("common header too old")
	}
	newHeader := types.CopyWorkObject(head)

	// Delete each header and rollback state processor until common header
	// Accumulate the hash slice stack
	var hashStack []*types.WorkObject
	for {
		if newHeader.Hash() == commonHeader.Hash() {
			break
		}
		hashStack = append(hashStack, newHeader)
		newHeader = hc.GetHeaderByHash(newHeader.ParentHash(hc.NodeCtx()))
		if newHeader == nil {
			return ErrSubNotSyncedToDom
		}
		// genesis check to not delete the genesis block
		if hc.IsGenesisHash(newHeader.Hash()) {
			break
		}
	}
	var prevHashStack []*types.WorkObject
	for {
		if prevHeader.Hash() == commonHeader.Hash() {
			break
		}
		hc.logger.Debugf("Rolling back header %s number %d", prevHeader.Hash(), prevHeader.NumberU64(hc.NodeCtx()))
		batch := hc.headerDb.NewBatch()
		prevHashStack = append(prevHashStack, prevHeader)
		rawdb.DeleteCanonicalHash(batch, prevHeader.NumberU64(hc.NodeCtx()))
		// UTXO Rollback logic: Recreate deleted UTXOs and delete created UTXOs
		if nodeCtx == common.ZONE_CTX && hc.ProcessingState() {

			sutxos, err := rawdb.ReadSpentUTXOs(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			trimmedUtxos, err := rawdb.ReadTrimmedUTXOs(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			sutxos = append(sutxos, trimmedUtxos...)
			for _, sutxo := range sutxos {
				rawdb.CreateUTXO(batch, sutxo.TxHash, sutxo.Index, sutxo.UtxoEntry)
			}
			if hc.config.IndexAddressUtxos {
				addressOutpointsToAddMap := make(map[[20]byte][]*types.OutpointAndDenomination)
				for _, sutxo := range sutxos {
					addressOutpointsToAddMap[common.AddressBytes(sutxo.Address)] = append(addressOutpointsToAddMap[common.AddressBytes(sutxo.Address)], &types.OutpointAndDenomination{
						TxHash:       sutxo.TxHash,
						Index:        sutxo.Index,
						Denomination: sutxo.Denomination,
						Lock:         sutxo.Lock,
					})
				}
				if err := rawdb.WriteAddressUTXOs(batch, hc.headerDb, addressOutpointsToAddMap); err != nil {
					hc.logger.Errorf("failed to write address utxos: %v", err)
				}
			}
			utxoKeys, err := rawdb.ReadCreatedUTXOKeys(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			for _, key := range utxoKeys {
				if len(key) == rawdb.UtxoKeyWithDenominationLength {
					key = key[:rawdb.UtxoKeyLength] // The last byte of the key is the denomination (but only in CreatedUTXOKeys)
				} else {
					hc.logger.Errorf("invalid created utxo key length: %d", len(key))
				}
				batch.Delete(key)
			}
			if hc.config.IndexAddressUtxos {
				addressOutpointsToRemoveMap := make(map[[20]byte][]*types.OutPoint)
				for _, key := range utxoKeys {
					if len(key) == rawdb.UtxoKeyWithDenominationLength {
						key = key[:rawdb.UtxoKeyLength] // The last byte of the key is the denomination (but only in CreatedUTXOKeys)
					} else {
						hc.logger.Errorf("invalid created utxo key length: %d", len(key))
					}
					txHash, index, err := rawdb.ReverseUtxoKey(key)
					if err != nil {
						hc.logger.Errorf("failed to reverse utxo key: %v", err)
						continue
					}
					utxo := rawdb.GetUTXO(hc.headerDb, txHash, index)
					if utxo == nil {
						hc.logger.Errorf("failed to get utxo for key: %v", key)
						continue
					}
					addressOutpointsToRemoveMap[common.AddressBytes(utxo.Address)] = append(addressOutpointsToRemoveMap[common.AddressBytes(utxo.Address)], &types.OutPoint{
						TxHash: txHash,
						Index:  index,
					})
				}
				if err := rawdb.DeleteAddressUTXOsWithBatch(batch, hc.headerDb, addressOutpointsToRemoveMap); err != nil {
					hc.logger.Errorf("failed to remove address utxos: %v", err)
				}
			}

			deletedCoinbases, err := rawdb.ReadDeletedCoinbaseLockups(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			coinbaseDeletedHashes := 0
			for i := len(deletedCoinbases) - 1; i >= 0; i-- { // Reapply the deleted states in reverse order to get to the original state by the end
				key := deletedCoinbases[i].Key
				data := deletedCoinbases[i].Value
				if len(key) != rawdb.CoinbaseLockupKeyLength {
					return fmt.Errorf("invalid deleted coinbase key length: %d", len(key))
				}
				batch.Put(key[:], data)
				coinbaseDeletedHashes++
			}
			hc.logger.Infof("Restored %d deleted coinbase lockups", coinbaseDeletedHashes)
			createdCoinbaseKeys, err := rawdb.ReadCreatedCoinbaseLockupKeys(hc.headerDb, prevHeader.Hash())
			if err != nil {
				return err
			}
			coinbaseCreatedHashes := 0
			for _, key := range createdCoinbaseKeys {
				if len(key) != rawdb.CoinbaseLockupKeyLength {
					return fmt.Errorf("invalid created coinbase key length: %d", len(key))
				}
				batch.Delete(key)
				coinbaseCreatedHashes++
			}
			hc.logger.Infof("Deleted %d created coinbase lockups", coinbaseCreatedHashes)
			if hc.config.IndexAddressUtxos {
				rawdb.UndoNewLockupsForBlock(batch, hc.headerDb, prevHeader.Hash())
			}
		}
		prevHeader = hc.GetHeaderByHash(prevHeader.ParentHash(hc.NodeCtx()))
		if prevHeader == nil {
			return errors.New("Could not find previously canonical header during reorg")
		}
		rawdb.WriteHeadBlockHash(batch, prevHeader.Hash())
		rawdb.WriteCanonicalHash(batch, prevHeader.Hash(), prevHeader.NumberU64(hc.NodeCtx()))
		if err := batch.Write(); err != nil {
			return err
		}
		hc.logger.WithFields(log.Fields{
			"Hash":   prevHeader.Hash(),
			"Number": prevHeader.NumberArray(),
		}).Info("Setting the current header")
		hc.currentHeader.Store(prevHeader)
		// genesis check to not delete the genesis block
		if hc.IsGenesisHash(prevHeader.Hash()) {
			break
		}
	}

	hc.logger.WithFields(log.Fields{
		"number":    newHeader.NumberArray(),
		"newHeader": newHeader.Hash(),
	}).Info("New Header")
	hc.logger.WithFields(log.Fields{
		"number":     prevHeader.NumberArray(),
		"prevHeader": prevHeader.Hash(),
	}).Info("Prev Header")
	hc.logger.WithFields(log.Fields{
		"number": commonHeader.NumberArray(),
		"hash":   commonHeader.Hash(),
	}).Info("Common Header")

	// Run through the hash stack to update canonicalHash and forward state processor
	for i := len(hashStack) - 1; i >= 0; i-- {
		hc.logger.Debugf("Rolling forward header %s number %d", hashStack[i].Hash(), hashStack[i].NumberU64(hc.NodeCtx()))
		rawdb.WriteCanonicalHash(hc.headerDb, hashStack[i].Hash(), hashStack[i].NumberU64(hc.NodeCtx()))
		setCurrent := true
		if nodeCtx == common.ZONE_CTX {
			block := hc.GetBlockOrCandidate(hashStack[i].Hash(), hashStack[i].NumberU64(nodeCtx))
			if block == nil {
				return errors.New("could not find block during SetCurrentState: " + hashStack[i].Hash().String())
			}
			err := hc.AppendBlock(block)
			if err != nil {
				hc.logger.WithFields(log.Fields{
					"error": err,
					"block": block.Hash(),
				}).Error("Error appending block during reorg")
				rawdb.DeleteCanonicalHash(hc.headerDb, hashStack[i].NumberU64(hc.NodeCtx()))
				setCurrent = false
			}
		}
		if setCurrent {
			rawdb.WriteHeadBlockHash(hc.headerDb, hashStack[i].Hash())
			hc.logger.WithFields(log.Fields{
				"Hash":   hashStack[i].Hash(),
				"Number": hashStack[i].NumberArray(),
			}).Info("Setting the current header")
			hc.currentHeader.Store(hashStack[i])
		}
	}
	return nil
}

// findCommonAncestor
func (hc *HeaderChain) findCommonAncestor(header *types.WorkObject) *types.WorkObject {
	current := types.CopyWorkObject(header)
	for {
		if current == nil {
			return nil
		}
		if hc.IsGenesisHash(current.Hash()) {
			return current
		}
		canonicalHash := rawdb.ReadCanonicalHash(hc.headerDb, current.NumberU64(hc.NodeCtx()))
		if canonicalHash == current.Hash() {
			return hc.GetHeaderByHash(canonicalHash)
		}
		current = hc.GetHeaderByHash(current.ParentHash(hc.NodeCtx()))
	}

}

// UncleWorkShareClassification checks if the workobject header is a workshare
// or uncle(block) or invalid and returns the appropriate validity
func (hc *HeaderChain) UncleWorkShareClassification(wo *types.WorkObjectHeader) types.WorkShareValidity {
	// If the kawpow activation hasnt happened, then if the pow is valid
	if !wo.KawpowActivationHappened() || wo.IsTransitionProgPowBlock() {
		// everything has to be progpow, also verify seal checks if the proof of
		// work meets the block difficulty target
		_, err := hc.VerifySeal(wo)
		if err != nil {
			return hc.CheckIfValidWorkShare(wo)
		} else {
			// Valid progpow block
			return types.Block
		}
	} else if wo.AuxPow() != nil {
		powId := wo.AuxPow().PowID()
		switch powId {
		case types.Kawpow:
			_, err := hc.VerifySeal(wo)
			if err != nil {
				return hc.CheckIfValidWorkShare(wo)
			} else {
				// Valid kawpow block
				return types.Block
			}
		case types.SHA_BCH, types.SHA_BTC:
			workShareTarget := new(big.Int).Div(common.Big2e256, wo.ShaDiffAndCount().Difficulty())
			powHash := wo.AuxPow().Header().PowHash()
			powHashBigInt := new(big.Int).SetBytes(powHash.Bytes())

			// Check if satisfies workShareTarget
			if powHashBigInt.Cmp(workShareTarget) < 0 {
				return types.Valid
			}

		case types.Scrypt:

			workShareTarget := new(big.Int).Div(common.Big2e256, wo.ScryptDiffAndCount().Difficulty())
			powHash := wo.AuxPow().Header().PowHash()
			powHashBigInt := new(big.Int).SetBytes(powHash.Bytes())

			// Check if satisfies workShareTarget
			if powHashBigInt.Cmp(workShareTarget) < 0 {
				return types.Valid
			}

		default:
		}
	}
	return types.Invalid
}

func (hc *HeaderChain) WorkShareDistance(wo *types.WorkObject, ws *types.WorkObjectHeader) (*big.Int, error) {
	current := wo
	// Create a list of ancestor blocks to the work object
	ancestors := make(map[common.Hash]struct{})
	for i := 0; i < params.WorkSharesInclusionDepth; i++ {
		parent := hc.GetBlockByHash(current.ParentHash(common.ZONE_CTX))
		if parent == nil {
			return big.NewInt(0), errors.New("error finding the parent")
		}
		ancestors[parent.Hash()] = struct{}{}
		current = parent
	}

	var distance int64 = 0
	// trace back from the workshare and check if any of the parents exist in
	// the ancestors list
	parentHash := ws.ParentHash()
	for {
		parent := hc.GetBlockByHash(parentHash)
		if parent == nil {
			return big.NewInt(0), errors.New("error finding the parent")
		}
		if _, exists := ancestors[parent.Hash()]; exists {
			distance += int64(wo.NumberU64(common.ZONE_CTX) - parent.NumberU64(common.ZONE_CTX) - 1)
			break
		}
		distance++
		// If distance is greater than the WorkSharesInclusionDepth, exit the for loop
		if distance > int64(params.WorkSharesInclusionDepth) {
			break
		}
		parentHash = parent.ParentHash(common.ZONE_CTX)
	}

	// If distance is greater than the WorkSharesInclusionDepth, reject the workshare
	if distance > int64(params.WorkSharesInclusionDepth) {
		return big.NewInt(0), errInvalidWorkShareDist
	}

	return big.NewInt(distance), nil
}

func (hc *HeaderChain) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	if !pEtxs.IsValid(trie.NewStackTrie(nil)) && !hc.IsGenesisHash(pEtxs.Header.Hash()) {
		hc.logger.Info("PendingEtx is not valid")
		return ErrPendingEtxNotValid
	}
	hc.logger.WithField("block", pEtxs.Header.Hash()).Debug("Received pending ETXs")
	// Only write the pending ETXs if we have not seen them before
	if !hc.pendingEtxs.Contains(pEtxs.Header.Hash()) {
		// Write to pending ETX database
		rawdb.WritePendingEtxs(hc.headerDb, pEtxs)
		// Also write to cache for faster access
		hc.pendingEtxs.Add(pEtxs.Header.Hash(), pEtxs)
	} else {
		return ErrPendingEtxAlreadyKnown
	}
	return nil
}

func (hc *HeaderChain) AddBloom(bloom types.Bloom, hash common.Hash) error {
	// Only write the bloom if we have not seen it before
	if !hc.blooms.Contains(hash) {
		// Write to bloom database
		rawdb.WriteBloom(hc.headerDb, hash, bloom)
		// Also write to cache for faster access
		hc.blooms.Add(hash, bloom)
	} else {
		return ErrBloomAlreadyKnown
	}
	return nil
}

// loadLastState loads the last known chain state from the database. This method
// assumes that the chain manager mutex is held.
func (hc *HeaderChain) loadLastState() error {
	// TODO: create function to find highest block number and fill Head FIFO
	headsHashes := rawdb.ReadHeadsHashes(hc.headerDb)

	if head := rawdb.ReadHeadBlockHash(hc.headerDb); head != (common.Hash{}) {
		if chead := hc.GetHeaderByHash(head); chead != nil {
			hc.currentHeader.Store(chead)
		} else {
			// This is only done if during the stop, currenthead hash was not stored
			// properly and it doesn't crash the nodes
			hc.currentHeader.Store(hc.genesisHeader)
		}
	} else {
		// Recover the current header
		hc.logger.Warn("Recovering Current Header")
		recoveredHeader := hc.RecoverCurrentHeader()
		rawdb.WriteHeadBlockHash(hc.headerDb, recoveredHeader.Hash())
		hc.currentHeader.Store(recoveredHeader)
	}

	heads := make([]*types.WorkObject, 0)
	for _, hash := range headsHashes {
		heads = append(heads, hc.GetHeaderByHash(hash))
	}
	hc.heads = heads

	return nil
}

// Stop stops the blockchain service. If any imports are currently in progress
// it will abort them using the procInterrupt.
func (hc *HeaderChain) Stop() {
	if !atomic.CompareAndSwapInt32(&hc.running, 0, 1) {
		return
	}

	hashes := make(common.Hashes, 0)
	for i := 0; i < len(hc.heads); i++ {
		hashes = append(hashes, hc.heads[i].Hash())
	}
	// Save the heads
	rawdb.WriteHeadsHashes(hc.headerDb, hashes)

	// Unsubscribe all subscriptions registered from blockchain
	hc.scope.Close()
	hc.bc.scope.Close()
	hc.wg.Wait()
	if hc.NodeCtx() == common.ZONE_CTX && hc.ProcessingState() {
		hc.bc.processor.Stop()
	}
	hc.logger.Info("headerchain stopped")
}

// CheckInCalcOrderCache checks if the hash exists in the calc order cache
// and if if the hash exists it returns the order value
func (hc *HeaderChain) CheckInCalcOrderCache(hash common.Hash) (*big.Int, int, bool) {
	calcOrderResponse, exists := hc.calcOrderCache.Peek(hash)
	if !exists || calcOrderResponse.intrinsicEntropy.Cmp(common.Big0) == 0 {
		return nil, -1, false
	} else {
		return calcOrderResponse.intrinsicEntropy, calcOrderResponse.order, true
	}
}

// AddToCalcOrderCache adds the hash and the order pair to the calcorder cache
func (hc *HeaderChain) AddToCalcOrderCache(hash common.Hash, order int, intrinsicS *big.Int) {
	if intrinsicS.Cmp(common.Big0) == 0 {
		hc.logger.Error("Tried to write intrinsicS of zero to CalcOrderCache")
		return
	}
	hc.calcOrderCache.Add(hash, calcOrderResponse{intrinsicEntropy: intrinsicS, order: order})
}

// Empty checks if the headerchain is empty.
func (hc *HeaderChain) Empty() bool {
	for _, hash := range []common.Hash{rawdb.ReadHeadBlockHash(hc.headerDb)} {
		if !hc.IsGenesisHash(hash) {
			return false
		}
	}
	return true
}

// GetBlockNumber retrieves the block number belonging to the given hash
// from the cache or database
func (hc *HeaderChain) GetBlockNumber(hash common.Hash) *uint64 {
	if cached, ok := hc.numberCache.Get(hash); ok {
		number := cached
		return &number
	}
	number := rawdb.ReadHeaderNumber(hc.headerDb, hash)
	if number != nil {
		hc.numberCache.Add(hash, *number)
	}
	return number
}

func (hc *HeaderChain) GetTerminiByHash(hash common.Hash) *types.Termini {
	termini := rawdb.ReadTermini(hc.headerDb, hash)
	return termini
}

// GetBlockHashesFromHash retrieves a number of block hashes starting at a given
// hash, fetching towards the genesis block.
func (hc *HeaderChain) GetBlockHashesFromHash(hash common.Hash, max uint64) []common.Hash {
	// Get the origin header from which to fetch
	header := hc.GetHeaderByHash(hash)
	if header == nil {
		return nil
	}
	// Iterate the headers until enough is collected or the genesis reached
	chain := make([]common.Hash, 0, max)
	for i := uint64(0); i < max; i++ {
		next := header.ParentHash(hc.NodeCtx())
		if header = hc.GetHeaderByHash(next); header == nil {
			break
		}
		chain = append(chain, next)
		if header.Number(hc.NodeCtx()).Sign() == 0 {
			break
		}
	}
	return chain
}

// GetAncestor retrieves the Nth ancestor of a given block. It assumes that either the given block or
// a close ancestor of it is canonical. maxNonCanonical points to a downwards counter limiting the
// number of blocks to be individually checked before we reach the canonical chain.
//
// Note: ancestor == 0 returns the same block, 1 returns its parent and so on.
func (hc *HeaderChain) GetAncestor(hash common.Hash, number, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	if ancestor > number {
		return common.Hash{}, 0
	}
	if ancestor == 1 {
		// in this case it is cheaper to just read the header
		if header := hc.GetHeaderByHash(hash); header != nil {
			return header.ParentHash(hc.NodeCtx()), number - 1
		}
		return common.Hash{}, 0
	}
	for ancestor != 0 {
		if rawdb.ReadCanonicalHash(hc.headerDb, number) == hash {
			ancestorHash := rawdb.ReadCanonicalHash(hc.headerDb, number-ancestor)
			if rawdb.ReadCanonicalHash(hc.headerDb, number) == hash {
				number -= ancestor
				return ancestorHash, number
			}
		}
		if *maxNonCanonical == 0 {
			return common.Hash{}, 0
		}
		*maxNonCanonical--
		ancestor--
		header := hc.GetHeaderByHash(hash)
		if header == nil {
			return common.Hash{}, 0
		}
		hash = header.ParentHash(hc.NodeCtx())
		number--
	}
	return hash, number
}

func (hc *HeaderChain) WriteBlock(block *types.WorkObject) {
	hc.bc.WriteBlock(block, hc.NodeCtx())
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (hc *HeaderChain) GetHeaderByHash(hash common.Hash) *types.WorkObject {
	termini := hc.GetTerminiByHash(hash)
	if termini == nil {
		return nil
	}

	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return &header
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	header := rawdb.ReadHeader(hc.headerDb, *number, hash)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, *header)
	return header
}

// GetHeaderOrCandidateByHash retrieves a block header from the database by hash,
// caching it if found.
func (hc *HeaderChain) GetHeaderOrCandidateByHash(hash common.Hash) *types.WorkObject {
	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return &header
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	header := rawdb.ReadHeader(hc.headerDb, *number, hash)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, *header)
	return header
}

// RecoverCurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache
func (hc *HeaderChain) RecoverCurrentHeader() *types.WorkObject {
	// Start logarithmic ascent to find the upper bound
	high := uint64(1)
	for hc.GetHeaderByNumber(high) != nil {
		high *= 2
	}
	// Run binary search to find the max header
	low := high / 2
	for low <= high {
		mid := (low + high) / 2
		if hc.GetHeaderByNumber(mid) != nil {
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	header := hc.GetHeaderByNumber(high)
	hc.logger.WithField("Hash", header.Hash().String()).Info("Header Recovered")

	return header
}

// HasHeader checks if a block header is present in the database or not.
// In theory, if header is present in the database, all relative components
// like td and hash->number should be present too.
func (hc *HeaderChain) HasHeader(hash common.Hash, number uint64) bool {
	return rawdb.HasHeader(hc.headerDb, hash, number)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (hc *HeaderChain) GetHeaderByNumber(number uint64) *types.WorkObject {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetHeaderByHash(hash)
}

func (hc *HeaderChain) GetCanonicalHash(number uint64) common.Hash {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	return hash
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (hc *HeaderChain) CurrentHeader() *types.WorkObject {
	return hc.currentHeader.Load().(*types.WorkObject)
}

// CurrentBlock returns the block for the current header.
func (hc *HeaderChain) CurrentBlock() *types.WorkObject {
	return hc.GetBlockOrCandidateByHash(hc.CurrentHeader().Hash())
}

// SetGenesis sets a new genesis block header for the chain
func (hc *HeaderChain) SetGenesis(head *types.WorkObject) {
	hc.genesisHeader = head
}

// Config retrieves the header chain's chain configuration.
func (hc *HeaderChain) Config() *params.ChainConfig { return hc.config }

// GetBlock implements consensus.ChainReader, and returns nil for every input as
// a header chain does not have blocks available for retrieval.
func (hc *HeaderChain) GetBlock(hash common.Hash, number uint64) *types.WorkObject {
	return hc.bc.GetBlock(hash, number)
}

func (hc *HeaderChain) GetWorkObject(hash common.Hash) *types.WorkObject {
	return hc.bc.GetWorkObject(hash)
}

func (hc *HeaderChain) GetWorkObjectWithWorkShares(hash common.Hash) *types.WorkObject {
	return hc.bc.GetWorkObjectWithWorkShares(hash)
}

// CheckContext checks to make sure the range of a context or order is valid
func (hc *HeaderChain) CheckContext(context int) error {
	if context < 0 || context > common.HierarchyDepth {
		return errors.New("the provided path is outside the allowable range")
	}
	return nil
}

// GasLimit returns the gas limit of the current HEAD block.
func (hc *HeaderChain) GasLimit() uint64 {
	return hc.CurrentHeader().GasLimit()
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetUnclesInChain(block *types.WorkObject, length int) []*types.WorkObjectHeader {
	uncles := []*types.WorkObjectHeader{}
	for i := 0; block != nil && i < length; i++ {
		uncles = append(uncles, block.Uncles()...)
		block = hc.GetBlock(block.ParentHash(hc.NodeCtx()), block.NumberU64(hc.NodeCtx())-1)
	}
	return uncles
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetGasUsedInChain(block *types.WorkObject, length int) int64 {
	gasUsed := 0
	for i := 0; block != nil && i < length; i++ {
		gasUsed += int(block.GasUsed())
		block = hc.GetBlock(block.ParentHash(hc.NodeCtx()), block.NumberU64(hc.NodeCtx())-1)
	}
	return int64(gasUsed)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) CalculateBaseFee(header *types.WorkObject) *big.Int {
	return header.BaseFee()
}

// Export writes the active chain to the given writer.
func (hc *HeaderChain) Export(w io.Writer) error {
	return hc.ExportN(w, uint64(0), hc.CurrentHeader().NumberU64(hc.NodeCtx()))
}

// ExportN writes a subset of the active chain to the given writer.
func (hc *HeaderChain) ExportN(w io.Writer, first uint64, last uint64) error {
	return nil
}

// GetBlockFromCacheOrDb looks up the body cache first and then checks the db
func (hc *HeaderChain) GetBlockFromCacheOrDb(hash common.Hash, number uint64) *types.WorkObject {
	// Short circuit if the block's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.blockCache.Get(hash); ok {
		block := cached
		return &block
	}
	return hc.GetBlock(hash, number)
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (hc *HeaderChain) GetBlockByHash(hash common.Hash) *types.WorkObject {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.GetBlock(hash, *number)
}

func (hc *HeaderChain) GetBlockOrCandidate(hash common.Hash, number uint64) *types.WorkObject {
	return hc.bc.GetBlockOrCandidate(hash, number)
}

// GetBlockOrCandidateByHash retrieves any block from the database by hash, caching it if found.
func (hc *HeaderChain) GetBlockOrCandidateByHash(hash common.Hash) *types.WorkObject {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.bc.GetBlockOrCandidate(hash, *number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (hc *HeaderChain) GetBlockByNumber(number uint64) *types.WorkObject {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetBlock(hash, number)
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (hc *HeaderChain) GetBody(hash common.Hash) *types.WorkObject {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.bodyCache.Get(hash); ok {
		body := cached
		return &body
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadWorkObject(hc.headerDb, *number, hash, types.BlockObject)
	if body == nil {
		return nil
	}
	// Cache the found body for next time and return
	hc.bc.bodyCache.Add(hash, *body)
	return body
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (hc *HeaderChain) GetBlocksFromHash(hash common.Hash, n int) (blocks types.WorkObjects) {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := hc.GetBlock(hash, *number)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash(hc.NodeCtx())
		*number--
	}
	return
}

func (hc *HeaderChain) NodeLocation() common.Location {
	return hc.bc.NodeLocation()
}

func (hc *HeaderChain) NodeCtx() int {
	return hc.bc.NodeCtx()
}

// Engine reterives the consensus engine.
func (hc *HeaderChain) Engine(header *types.WorkObjectHeader) consensus.Engine {
	return hc.GetEngineForHeader(header)
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (hc *HeaderChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return hc.scope.Track(hc.chainHeadFeed.Subscribe(ch))
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (hc *HeaderChain) SubscribeUnlocksEvent(ch chan<- UnlocksEvent) event.Subscription {
	return hc.scope.Track(hc.unlocksFeed.Subscribe(ch))
}

// SubscribeNewWorkshareEvent registers a subscription of NewWorkshareEvent.
func (hc *HeaderChain) SubscribeNewWorkshareEvent(ch chan<- NewWorkshareEvent) event.Subscription {
	return hc.scope.Track(hc.workshareFeed.Subscribe(ch))
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (hc *HeaderChain) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return hc.scope.Track(hc.chainSideFeed.Subscribe(ch))
}

func (hc *HeaderChain) StateAt(root, etxRoot common.Hash, quaiStateSize *big.Int) (*state.StateDB, error) {
	return hc.bc.processor.StateAt(root, etxRoot, quaiStateSize)
}

func (hc *HeaderChain) SlicesRunning() []common.Location {
	return hc.slicesRunning
}

func (hc *HeaderChain) ComputeExpansionNumber(parent *types.WorkObject) (uint8, error) {
	// If the parent is a prime block, prime terminus is the parent hash
	_, order, err := hc.CalcOrder(parent)
	if err != nil {
		return 0, err
	}
	var primeTerminusHash common.Hash
	if order == common.PRIME_CTX {
		primeTerminusHash = parent.Hash()
	} else {
		primeTerminusHash = parent.PrimeTerminusHash()
	}

	primeTerminus := hc.GetBlockByHash(primeTerminusHash)

	if primeTerminus == nil {
		return 0, errors.New("prime terminus is nil in compute expansion number")
	}

	// If the Prime Terminus is genesis the expansion number is the genesis expansion number
	if hc.IsGenesisHash(primeTerminusHash) && hc.NodeLocation().Equal(common.Location{0, 0}) {
		return primeTerminus.ExpansionNumber(), nil
	} else {
		// check if the prime terminus is the block where the threshold count
		// exceeds the trigger and expansion wait window Expansion happens when
		// the threshold count is greater than the expansion threshold and we
		// cross the tree expansion trigger window
		if primeTerminus.ThresholdCount() == params.TREE_EXPANSION_TRIGGER_WINDOW+params.TREE_EXPANSION_WAIT_COUNT {
			return primeTerminus.ExpansionNumber() + 1, nil
		}
		// get the parent of the prime terminus
		parentOfPrimeTerminus := hc.fetchPrimeBlock(primeTerminus.ParentHash(common.PRIME_CTX))
		if parentOfPrimeTerminus == nil {
			return 0, fmt.Errorf("parent of prime terminus is nil %v", primeTerminus.ParentHash(common.PRIME_CTX))
		}
		return parentOfPrimeTerminus.ExpansionNumber(), nil
	}
}

// ComputeEfficiencyScore calculates the efficiency score for the given header
func (hc *HeaderChain) ComputeEfficiencyScore(parent *types.WorkObject) (uint16, error) {
	deltaEntropy := new(big.Int).Add(parent.ParentDeltaEntropy(common.REGION_CTX), parent.ParentDeltaEntropy(common.ZONE_CTX))
	uncledDeltaEntropy := new(big.Int).Add(parent.ParentUncledDeltaEntropy(common.REGION_CTX), parent.ParentUncledDeltaEntropy(common.ZONE_CTX))

	// Take the ratio of deltaEntropy to the uncledDeltaEntropy in percentage
	efficiencyScore := uncledDeltaEntropy.Mul(uncledDeltaEntropy, big.NewInt(100))
	if hc.IsGenesisHash(parent.ParentHash(common.PRIME_CTX)) {
		efficiencyScore = common.Big0
	} else {
		if efficiencyScore.Cmp(common.Big0) == 0 {
			return parent.EfficiencyScore(), nil
		}
		efficiencyScore.Div(efficiencyScore, deltaEntropy)
	}

	// Calculate the exponential moving average
	ewma := (uint16(efficiencyScore.Uint64()) + parent.EfficiencyScore()*params.TREE_EXPANSION_FILTER_ALPHA) / 10
	return ewma, nil
}

// ComputeAverageTxFees computes the ema of the half of the total fees generated in quai over past 100 blocks
func (hc *HeaderChain) ComputeAverageTxFees(parent *types.WorkObject, totalTxFeesInQuai *big.Int) *big.Int {
	if rawdb.IsGenesisHash(hc.headerDb, parent.Hash()) {
		return big.NewInt(0)
	}
	newAvgTxFees := new(big.Int).Mul(parent.AvgTxFees(), big.NewInt(99))
	newAvgTxFees = new(big.Int).Add(newAvgTxFees, totalTxFeesInQuai)
	newAvgTxFees = new(big.Int).Div(newAvgTxFees, big.NewInt(100))
	return newAvgTxFees
}

// CalcBaseFee calculates the mininum base fee supplied by the transaction
// to get inclusion in the next block
func (hc *HeaderChain) CalcBaseFee(block *types.WorkObject) *big.Int {
	if hc.IsGenesisHash(block.Hash()) {
		return big.NewInt(0)
	} else {
		if block.NumberU64(common.ZONE_CTX) > params.OrchardBaseFeeChangeBlock {
			return new(big.Int).SetInt64(params.GWei)
		}
		var exchangeRate *big.Int
		if hc.IsGenesisHash(block.ParentHash(common.ZONE_CTX)) {
			exchangeRate = params.ExchangeRate
		} else {
			// If the base fee is calculated is less than the min base fee, then set
			// this to min base fee
			primeTerminus := hc.GetBlockByHash(block.PrimeTerminusHash())
			if primeTerminus == nil {
				return nil
			} else {
				exchangeRate = primeTerminus.ExchangeRate()
			}
		}
		minBaseFee := misc.QiToQuai(block, exchangeRate, block.Difficulty(), params.MinBaseFeeInQits)
		minBaseFee = new(big.Int).Div(minBaseFee, big.NewInt(int64(params.TxGas)))
		return minBaseFee
	}
}

// ComputeConversionFlowAmount computes a moving average conversion flow amount
func (hc *HeaderChain) ComputeConversionFlowAmount(parent *types.WorkObject, currentBlockConversionAmount *big.Int) *big.Int {
	prevBlockConversionFlowAmount := parent.ConversionFlowAmount()
	newConversionFlowAmount := new(big.Int).Mul(prevBlockConversionFlowAmount, big.NewInt(int64(params.MinerDifficultyWindow)-1))
	newConversionFlowAmount = new(big.Int).Add(newConversionFlowAmount, currentBlockConversionAmount)
	newConversionFlowAmount = new(big.Int).Div(newConversionFlowAmount, big.NewInt(int64(params.MinerDifficultyWindow)))
	// Make sure that the conversion flow amount doesnt go below the MinConversionFlowAmount
	if newConversionFlowAmount.Cmp(params.MinConversionFlowAmount) < 0 {
		return params.MinConversionFlowAmount
	}
	twoXPrevConversionFlowAmount := new(big.Int).Mul(prevBlockConversionFlowAmount, common.Big2)
	// cap the newConversionFlowAmount to 2x the prevBlockConversionFlowAmount
	if newConversionFlowAmount.Cmp(twoXPrevConversionFlowAmount) > 0 {
		return twoXPrevConversionFlowAmount
	}
	return newConversionFlowAmount
}

// ComputeMinerDifficulty computes long term moving average of the block difficulty
func (hc *HeaderChain) ComputeMinerDifficulty(parent *types.WorkObject) *big.Int {
	// If the block is the goldenage fork 3 block directly return the block difficulty
	if parent.NumberU64(common.PRIME_CTX) <= params.ControllerKickInBlock {
		return parent.Difficulty()
	}
	newMinerDifficulty := new(big.Int).Mul(parent.MinerDifficulty(), big.NewInt(int64(params.MinerDifficultyWindow)-1))
	newMinerDifficulty = new(big.Int).Add(newMinerDifficulty, parent.Difficulty())
	newMinerDifficulty = new(big.Int).Div(newMinerDifficulty, big.NewInt(int64(params.MinerDifficultyWindow)))
	return newMinerDifficulty
}

func (hc *HeaderChain) GetKQuaiAndUpdateBit(hash common.Hash) (*big.Int, uint8, error) {
	return hc.fetchKQuaiAndUpdateBit(hash)
}

// ComputeKQuaiDiscount calculates the change in the K Quai
func (hc *HeaderChain) ComputeKQuaiDiscount(block *types.WorkObject, exchangeRate *big.Int) *big.Int {
	if block.NumberU64(common.PRIME_CTX) <= params.ControllerKickInBlock {
		return params.StartingKQuaiDiscount
	}
	// The idea is that taking the derivative of the K Quai over a 1000 block
	// window and applying the discount on the conversion flow will offset the
	// amount of money that can be made by arbitraging the exchange rate
	// adjustment
	var kQuaiOld *big.Int
	if block.NumberU64(common.PRIME_CTX) <= params.MinerDifficultyWindow {
		return big.NewInt(0)
	} else {
		prevBlock := hc.GetBlockByNumber(block.NumberU64(common.PRIME_CTX) - params.MinerDifficultyWindow)
		kQuaiOld = prevBlock.ExchangeRate()
	}

	// Since kQuai is a big number to capture the change in magniture better,
	// KQuaiDiscountMultiplier is used
	kQuaiDiscount := new(big.Int).Sub(kQuaiOld, exchangeRate)
	kQuaiDiscount = new(big.Int).Mul(kQuaiDiscount, big.NewInt(params.KQuaiDiscountMultiplier))
	kQuaiDiscount = new(big.Int).Div(kQuaiDiscount, kQuaiOld)

	// Bound the kQuaiDiscount value between 0 and KQuaiDiscountMultiplier
	if kQuaiDiscount.Cmp(big.NewInt(0)) < 0 {
		if kQuaiDiscount.Cmp(big.NewInt(-params.KQuaiDiscountMultiplier)) < 0 {
			kQuaiDiscount = big.NewInt(params.KQuaiDiscountMultiplier)
		} else {
			kQuaiDiscount = new(big.Int).Mul(kQuaiDiscount, big.NewInt(-1))
		}
	} else {
		if kQuaiDiscount.Cmp(big.NewInt(params.KQuaiDiscountMultiplier)) > 0 {
			kQuaiDiscount = big.NewInt(params.KQuaiDiscountMultiplier)
		}
	}
	// KQuaiDiscount value that is used is also a ewma with the same frequency as
	// the miner difficulty window
	newKQuaiDiscount := new(big.Int).Mul(block.KQuaiDiscount(), big.NewInt(int64(params.MinerDifficultyWindow)-1))
	newKQuaiDiscount = new(big.Int).Add(newKQuaiDiscount, kQuaiDiscount)
	newKQuaiDiscount = new(big.Int).Div(newKQuaiDiscount, big.NewInt(int64(params.MinerDifficultyWindow)))
	return newKQuaiDiscount
}

// UpdateEtxEligibleSlices returns the updated etx eligible slices field
func (hc *HeaderChain) UpdateEtxEligibleSlices(header *types.WorkObject, location common.Location) common.Hash {
	// After 5 days of the start of a new chain, the chain becomes eligible to receive etxs
	position := location[0]*16 + location[1]
	byteIndex := position / 8      // Find the byte index within the array
	bitIndex := uint(position % 8) // Find the specific bit within the byte, cast to uint for bit operations
	newHash := header.EtxEligibleSlices()
	if header.NumberU64(common.ZONE_CTX) > params.TimeToStartTx {
		// Set the position bit to 1
		newHash[byteIndex] |= 1 << bitIndex
	} else {
		// Set the position bit to 0
		newHash[byteIndex] &^= 1 << bitIndex
	}
	return newHash
}

// CheckIfETXIsEligible checks if the given zone location is eligible to receive
// etx based on the etxEligibleSlices hash
func (hc *HeaderChain) CheckIfEtxIsEligible(etxEligibleSlices common.Hash, to common.Location) bool {
	position := to.Region()*16 + to.Zone()
	// Calculate the index of the byte and the specific bit within that byte
	byteIndex := position / 8      // Find the byte index within the array
	bitIndex := uint(position % 8) // Find the specific bit within the byte, cast to uint for bit operations

	// Check if the bit is set to 1
	return etxEligibleSlices[byteIndex]&(1<<bitIndex) != 0
}

// IsGenesisHash checks if a hash is a genesis hash
func (hc *HeaderChain) IsGenesisHash(hash common.Hash) bool {
	return rawdb.IsGenesisHash(hc.headerDb, hash)
}

// AddGenesisHash appends the given hash to the genesis hash list
func (hc *HeaderChain) AddGenesisHash(hash common.Hash) {
	genesisHashes := rawdb.ReadGenesisHashes(hc.headerDb)
	genesisHashes = append(genesisHashes, hash)

	// write the genesis hash to the database
	rawdb.WriteGenesisHashes(hc.headerDb, genesisHashes)
}

// GetGenesisHashes returns the genesis hashes stored
func (hc *HeaderChain) GetGenesisHashes() []common.Hash {
	return rawdb.ReadGenesisHashes(hc.headerDb)
}

func (hc *HeaderChain) SetCurrentExpansionNumber(expansionNumber uint8) {
	hc.currentExpansionNumber = expansionNumber
}

func (hc *HeaderChain) GetExpansionNumber() uint8 {
	return hc.currentExpansionNumber
}

func (hc *HeaderChain) GetPrimeTerminus(header *types.WorkObject) *types.WorkObject {
	return hc.GetHeaderByHash(header.PrimeTerminusHash())
}

func (hc *HeaderChain) WriteAddressOutpoints(outpoints map[[20]byte][]*types.OutpointAndDenomination) error {
	return rawdb.WriteAddressOutpoints(hc.bc.db, outpoints)
}

func (hc *HeaderChain) GetMaxTxInWorkShare() uint64 {
	return params.MaxTxInWorkShare
}

func (hc *HeaderChain) Database() ethdb.Database {
	return hc.headerDb
}
