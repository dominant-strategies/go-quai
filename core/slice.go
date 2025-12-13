package core

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state/snapshot"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
)

const (
	c_maxPendingEtxBatchesPrime       = 3000
	c_maxPendingEtxBatchesRegion      = 1000
	c_maxPendingEtxsRollup            = 256
	c_maxBloomFilters                 = 25
	c_pendingHeaderChacheBufferFactor = 2
	pendingHeaderGCTime               = 5
	c_terminusIndex                   = 3
	c_startingPrintLimit              = 10
	c_regionRelayProc                 = 3
	c_primeRelayProc                  = 10
	c_asyncPhUpdateChanSize           = 10
	c_phCacheSize                     = 50
	c_pEtxRetryThreshold              = 10 // Number of pEtxNotFound return on a dom block before asking for pEtx/Rollup from sub
	c_currentStateComputeWindow       = 20 // Number of blocks around the current header the state generation is always done
	c_inboundEtxCacheSize             = 10 // Number of inboundEtxs to keep in cache so that, we don't recompute it every time dom is processed
	c_appendTimeCacheSize             = 1000
)

// Core will implement the following interface to enable dom-sub communication
type CoreBackend interface {
	AddPendingEtxs(pEtxs types.PendingEtxs) error
	AddPendingEtxsRollup(pEtxRollup types.PendingEtxsRollup) error
	RequestDomToAppendOrFetch(hash common.Hash, entropy *big.Int, order int)
	Append(header *types.WorkObject, manifest types.BlockManifest, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, error)
	DownloadBlocksInManifest(hash common.Hash, manifest types.BlockManifest, entropy *big.Int)
	GenerateRecoveryPendingHeader(pendingHeader *types.WorkObject, checkpointHashes types.Termini) error
	GetPendingEtxsRollupFromSub(hash common.Hash, location common.Location) (types.PendingEtxsRollup, error)
	GetPendingEtxsFromSub(hash common.Hash, location common.Location) (types.PendingEtxs, error)
	NewGenesisPendingHeader(pendingHeader *types.WorkObject, domTerminus common.Hash, hash common.Hash) error
	GetManifest(blockHash common.Hash) (types.BlockManifest, error)
	GetPrimeBlock(blockHash common.Hash) *types.WorkObject
	GetKQuaiAndUpdateBit(blockHash common.Hash) (*big.Int, uint8, error)
	ReceiveMinedHeader(header *types.WorkObject) error
}

type pEtxRetry struct {
	hash    common.Hash
	retries uint64
}

type Slice struct {
	hc *HeaderChain

	txPool        *TxPool
	miner         *Miner
	expansionFeed event.Feed

	sliceDb ethdb.Database
	config  *params.ChainConfig
	engine  []consensus.Engine

	quit chan struct{} // slice quit channel

	domInterface CoreBackend
	subInterface []CoreBackend

	wg               sync.WaitGroup
	scope            event.SubscriptionScope
	missingBlockFeed event.Feed

	pEtxRetryCache *lru.Cache[common.Hash, pEtxRetry]
	asyncPhCh      chan *types.WorkObject
	asyncPhSub     event.Subscription

	inboundEtxsCache *lru.Cache[common.Hash, types.Transactions]

	validator Validator // Block and state validator interface

	badHashesCache map[common.Hash]bool
	logger         *log.Logger

	bestPh atomic.Value

	appendTimeCache *lru.Cache[common.Hash, time.Duration]

	recomputeRequired bool
}

func NewSlice(db ethdb.Database, config *Config, powConfig params.PowConfig, txConfig *TxPoolConfig, txLookupLimit *uint64, chainConfig *params.ChainConfig, slicesRunning []common.Location, currentExpansionNumber uint8, genesisBlock *types.WorkObject, engine []consensus.Engine, cacheConfig *CacheConfig, vmConfig vm.Config, genesis *Genesis, logger *log.Logger) (*Slice, error) {
	nodeCtx := chainConfig.Location.Context()
	sl := &Slice{
		config:            chainConfig,
		engine:            engine,
		sliceDb:           db,
		quit:              make(chan struct{}),
		badHashesCache:    make(map[common.Hash]bool),
		logger:            logger,
		recomputeRequired: false,
	}

	// This only happens during the expansion
	if genesisBlock != nil {
		sl.AddGenesisHash(genesisBlock.Hash())
	}
	var err error
	sl.hc, err = NewHeaderChain(db, powConfig, engine, sl.GetPEtxRollupAfterRetryThreshold, sl.GetPEtxAfterRetryThreshold, sl.GetPrimeBlock, sl.GetKQuaiAndUpdateBit, chainConfig, cacheConfig, txLookupLimit, vmConfig, slicesRunning, currentExpansionNumber, logger)
	if err != nil {
		return nil, err
	}

	sl.validator = NewBlockValidator(chainConfig, sl.hc, engine)

	// tx pool is only used in zone
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.txPool = NewTxPool(*txConfig, chainConfig, sl.hc, logger, sl.sliceDb)
		sl.hc.pool = sl.txPool
	}
	sl.miner = New(sl.hc, sl.txPool, config, db, chainConfig, engine, sl.ProcessingState(), sl.logger)

	pEtxRetryCache, _ := lru.New[common.Hash, pEtxRetry](c_pEtxRetryThreshold)
	sl.pEtxRetryCache = pEtxRetryCache

	inboundEtxsCache, _ := lru.New[common.Hash, types.Transactions](c_inboundEtxCacheSize)
	sl.inboundEtxsCache = inboundEtxsCache

	appendTimeCache, _ := lru.New[common.Hash, time.Duration](c_appendTimeCacheSize)
	sl.appendTimeCache = appendTimeCache

	sl.subInterface = make([]CoreBackend, common.MaxWidth)

	if err := sl.init(); err != nil {
		return nil, err
	}

	// This only happens during the expansion
	if genesisBlock != nil {
		sl.WriteGenesisBlock(genesisBlock, chainConfig.Location)
	}

	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		go sl.asyncPendingHeaderLoop()
		go sl.asyncWorkShareUpdateLoop()
	}

	return sl, nil
}

// GetEngineForPowID returns the consensus engine for the given PowID
func (sl *Slice) GetEngineForPowID(powID types.PowID) consensus.Engine {
	return sl.hc.GetEngineForPowID(powID)
}

// GetEngineForHeader returns the consensus engine for the given header
func (sl *Slice) GetEngineForHeader(header *types.WorkObjectHeader) consensus.Engine {
	return sl.hc.GetEngineForHeader(header)
}

func (sl *Slice) SetDomInterface(domInterface CoreBackend) {
	sl.domInterface = domInterface
}

// Append takes a proposed header and constructs a local block and attempts to hierarchically append it to the block graph.
// If this is called from a dominant context a domTerminus must be provided else a common.Hash{} should be used and domOrigin should be set to true.
// Return of this function is the Etxs generated in the Zone Block, subReorg bool that tells dom if should be mined on, setHead bool that determines if we should set the block as the current head and the error
func (sl *Slice) Append(header *types.WorkObject, domTerminus common.Hash, domOrigin bool, newInboundEtxs types.Transactions) (types.Transactions, error) {
	start := time.Now()
	nodeCtx := sl.NodeCtx()

	if sl.hc.IsGenesisHash(header.Hash()) {
		return nil, nil
	}

	if header.NumberU64(common.ZONE_CTX) > sl.hc.CurrentHeader().NumberU64(common.ZONE_CTX)+c_zoneHorizonThreshold {
		return nil, ErrSubNotSyncedToDom
	}

	// Only print in Info level if block is c_startingPrintLimit behind or less
	if sl.CurrentInfo(header) {
		sl.logger.WithFields(log.Fields{
			"number":      header.NumberArray(),
			"hash":        header.Hash(),
			"location":    header.Location(),
			"parent hash": header.ParentHash(nodeCtx),
		}).Info("Starting slice append")
	} else {
		sl.logger.WithFields(log.Fields{
			"number":      header.NumberArray(),
			"hash":        header.Hash(),
			"location":    header.Location(),
			"parent hash": header.ParentHash(nodeCtx),
		}).Debug("Starting slice append")
	}

	time0_1 := common.PrettyDuration(time.Since(start))
	// Check if the header hash exists in the BadHashes list
	if sl.IsBlockHashABadHash(header.Hash()) {
		return nil, ErrBadBlockHash
	}
	time0_2 := common.PrettyDuration(time.Since(start))

	location := header.Location()
	_, order, err := sl.CalcOrder(header)
	if err != nil {
		return nil, err
	}

	// Don't append the block which already exists in the database.
	if sl.hc.HasHeader(header.Hash(), header.NumberU64(nodeCtx)) && (sl.hc.GetTerminiByHash(header.Hash()) != nil) && !domOrigin {
		sl.logger.WithField("hash", header.Hash()).Debug("Block has already been appended")
		return nil, ErrKnownBlock
	}
	time1 := common.PrettyDuration(time.Since(start))

	batch := sl.sliceDb.NewBatch()

	// Run Previous Coincident Reference Check (PCRC)
	domTerminus, newTermini, err := sl.pcrc(batch, header, domTerminus, domOrigin)
	if err != nil {
		return nil, err
	}
	sl.logger.WithFields(log.Fields{
		"hash":    header.Hash(),
		"number":  header.NumberArray(),
		"termini": newTermini,
	}).Debug("PCRC done")

	time2 := common.PrettyDuration(time.Since(start))
	// Append the new block
	err = sl.hc.AppendHeader(header)
	if err != nil {
		return nil, err
	}
	time3 := common.PrettyDuration(time.Since(start))
	// Construct the block locally
	block, err := sl.ConstructLocalBlock(header)
	if err != nil {
		return nil, err
	}
	time4 := common.PrettyDuration(time.Since(start))

	sl.hc.CalculateManifest(block)
	if nodeCtx == common.PRIME_CTX {
		_, err = sl.hc.CalculateInterlink(block)
		if err != nil {
			return nil, err
		}
	}

	if order < nodeCtx {
		// Store the inbound etxs for all dom blocks and use
		// it in the future if dom switch happens
		// This should be pruned at the re-org tolerance depth
		rawdb.WriteInboundEtxs(sl.sliceDb, block.Hash(), newInboundEtxs)

		// After writing the inbound etxs, we need to just send the inbounds for
		// the given block location down in the region
		if nodeCtx == common.REGION_CTX {
			newInboundEtxs = newInboundEtxs.FilterToSub(block.Location(), nodeCtx, order)
		}
	}

	// If this was a coincident block, our dom will be passing us a set of newly
	// confirmed ETXs If this is not a coincident block, we need to build up the
	// list of confirmed ETXs using the subordinate manifest In either case, if
	// we are a dominant node, we need to collect the ETX rollup from our sub.
	if !domOrigin && nodeCtx != common.ZONE_CTX {
		cachedInboundEtxs, exists := sl.inboundEtxsCache.Get(block.Hash())
		if exists && cachedInboundEtxs != nil && nodeCtx != common.PRIME_CTX {
			newInboundEtxs = cachedInboundEtxs
		} else {
			newInboundEtxs, err = sl.CollectNewlyConfirmedEtxs(block, order)
			if err != nil {
				sl.logger.WithField("err", err).Trace("Error collecting newly confirmed etxs")
				// Keeping track of the number of times pending etx fails and if it crossed the retry threshold
				// ask the sub for the pending etx/rollup data
				val, exist := sl.pEtxRetryCache.Get(block.Hash())
				var retry uint64
				if exist {
					pEtxCurrent := val
					retry = pEtxCurrent.retries + 1
				}
				pEtxNew := pEtxRetry{hash: block.Hash(), retries: retry}
				sl.pEtxRetryCache.Add(block.Hash(), pEtxNew)
				return nil, ErrSubNotSyncedToDom
			}
			if nodeCtx == common.PRIME_CTX {
				// write the inbound etxs to the database
				rawdb.WriteInboundEtxs(batch, block.Hash(), newInboundEtxs)

				// Copy the newInboundEtxs before using
				newInboundEtxsCopy := make(types.Transactions, len(newInboundEtxs))
				for i, etx := range newInboundEtxs {
					newInboundEtxsCopy[i] = types.NewTx(etx.Inner())
				}
				newInboundEtxs = newInboundEtxsCopy
			} else {
				sl.inboundEtxsCache.Add(block.Hash(), newInboundEtxs)
			}
		}
	}

	if nodeCtx == common.PRIME_CTX {
		// Until the controller has kicked in, dont update any of the fields,
		// and make sure that the fields are unchanged from the default value
		if header.NumberU64(common.PRIME_CTX) <= params.ControllerKickInBlock {
			if header.KQuaiDiscount().Cmp(params.StartingKQuaiDiscount) != 0 {
				return nil, fmt.Errorf("invalid newKQuaiDiscount used (remote: %d local: %d)", block.KQuaiDiscount(), params.StartingKQuaiDiscount)
			}
			if header.ConversionFlowAmount().Cmp(params.StartingConversionFlowAmount) != 0 {
				return nil, fmt.Errorf("invalid conversion flow amount used (remote: %d local: %d)", block.ConversionFlowAmount(), params.StartingConversionFlowAmount)
			}
			if header.ExchangeRate().Cmp(params.ExchangeRate) != 0 {
				return nil, fmt.Errorf("invalid exchange rate used (remote: %d local: %d)", block.ExchangeRate(), params.ExchangeRate)
			}
		} else {

			// verify that the exchange written in the header is correct based
			// on the processing of the parent
			if nodeCtx == common.PRIME_CTX {
				err := sl.verifyParentExchangeRateAndFlowAmount(block)
				if err != nil {
					return nil, err
				}
			}

			parent := sl.hc.GetBlockByHash(header.ParentHash(common.PRIME_CTX))
			if parent == nil {
				return nil, errors.New("parent not found")
			}

			// Conversion Flows that happen in the direction of the exchange rate
			// controller adjustment, get the k quai discount,and the conversion
			// flows going against the exchange rate controller adjustment dont
			var exchangeRateIncreasing bool
			if block.NumberU64(common.PRIME_CTX) > params.MinerDifficultyWindow {
				prevBlock := sl.hc.GetBlockByNumber(block.NumberU64(common.PRIME_CTX) - params.MinerDifficultyWindow)
				if prevBlock == nil {
					return nil, errors.New("block minerdifficultywindow blocks behind not found")
				}

				if header.ExchangeRate().Cmp(prevBlock.ExchangeRate()) > 0 {
					exchangeRateIncreasing = true
				}
			}

			// sort the newInboundEtxs based on the decreasing order of the max slips
			sort.SliceStable(newInboundEtxs, func(i, j int) bool {
				etxi := newInboundEtxs[i]
				etxj := newInboundEtxs[j]

				var slipi *big.Int
				if etxi.EtxType() == types.ConversionType {
					// If no slip is mentioned it is set to 90%
					slipAmount := new(big.Int).Set(params.MaxSlip)
					if len(etxi.Data()) > 1 {
						slipAmount = new(big.Int).SetBytes(etxi.Data()[:2])
						if slipAmount.Cmp(params.MaxSlip) > 0 {
							slipAmount = new(big.Int).Set(params.MaxSlip)
						}
						if slipAmount.Cmp(params.MinSlip) < 0 {
							slipAmount = new(big.Int).Set(params.MinSlip)
						}
					}
					slipi = slipAmount
				} else {
					slipi = big.NewInt(0)
				}

				var slipj *big.Int
				if etxj.EtxType() == types.ConversionType {
					// If no slip is mentioned it is set to 90%
					slipAmount := new(big.Int).Set(params.MaxSlip)
					if len(etxj.Data()) > 1 {
						slipAmount = new(big.Int).SetBytes(etxj.Data()[:2])
						if slipAmount.Cmp(params.MaxSlip) > 0 {
							slipAmount = new(big.Int).Set(params.MaxSlip)
						}
						if slipAmount.Cmp(params.MinSlip) < 0 {
							slipAmount = new(big.Int).Set(params.MinSlip)
						}
					}
					slipj = slipAmount
				} else {
					slipj = big.NewInt(0)
				}

				return slipi.Cmp(slipj) > 0
			})

			kQuaiDiscount := header.KQuaiDiscount()

			// stores the original etx values before the conversion so that it can be used once the
			// new exchange rate is calculated
			etxValuesBeforeConversion := make([]*big.Int, len(newInboundEtxs))
			originalEtxValues := make([]*big.Int, len(newInboundEtxs))

			actualConversionAmountInQuai := big.NewInt(0)
			realizedConversionAmountInQuai := big.NewInt(0)

			for i, etx := range newInboundEtxs {
				// If the etx is conversion
				if types.IsConversionTx(etx) && etx.Value().Cmp(common.Big0) > 0 {
					value := new(big.Int).Set(etx.Value())
					originalValue := new(big.Int).Set(etx.Value())
					// original etx values are stored so that in the case of
					// refunds, the transaction can be reverted
					originalEtxValues[i] = new(big.Int).Set(etx.Value())

					// Keeping a temporary actual conversion amount in quai so
					// that, in case the transaction gets reverted, doesnt effect the
					// actualConversionAmountInQuai
					var tempActualConversionAmountInQuai *big.Int
					if etx.To().IsInQuaiLedgerScope() {
						tempActualConversionAmountInQuai = new(big.Int).Add(actualConversionAmountInQuai, misc.QiToQuai(header, header.ExchangeRate(), header.MinerDifficulty(), value))
					} else {
						tempActualConversionAmountInQuai = new(big.Int).Add(actualConversionAmountInQuai, value)
					}

					// If no slip is mentioned it is set to 90%, otherwise use
					// the slip specified in the transaction data with a max of
					// 90%
					slipAmount := new(big.Int).Set(params.MaxSlip)
					if len(etx.Data()) > 1 {
						slipAmount = new(big.Int).SetBytes(etx.Data()[:2])
						if slipAmount.Cmp(params.MaxSlip) > 0 {
							slipAmount = new(big.Int).Set(params.MaxSlip)
						}
						if slipAmount.Cmp(params.MinSlip) < 0 {
							slipAmount = new(big.Int).Set(params.MinSlip)
						}
					}

					// Apply the cubic discount based on the current tempActualConversionAmountInQuai
					discountedConversionAmount := misc.ApplyCubicDiscount(block.ConversionFlowAmount(), tempActualConversionAmountInQuai)
					if header.NumberU64(common.PRIME_CTX) > params.ConversionSlipChangeBlock {
						discountedConversionAmount = misc.ApplyCubicDiscount(tempActualConversionAmountInQuai, block.ConversionFlowAmount())
					}
					discountedConversionAmountInInt, _ := discountedConversionAmount.Int(nil)

					// Conversions going against the direction of the exchange
					// rate adjustment dont get any k quai discount applied
					conversionAmountAfterKQuaiDiscount := new(big.Int).Mul(discountedConversionAmountInInt, new(big.Int).Sub(big.NewInt(params.KQuaiDiscountMultiplier), kQuaiDiscount))
					conversionAmountAfterKQuaiDiscount = new(big.Int).Div(conversionAmountAfterKQuaiDiscount, big.NewInt(params.KQuaiDiscountMultiplier))

					// Apply the conversion flow discount to all transactions
					value = new(big.Int).Mul(value, discountedConversionAmountInInt)
					value = new(big.Int).Div(value, tempActualConversionAmountInQuai)

					// ten percent original value is calculated so that in case
					// the slip is more than this it can be reset
					tenPercentOriginalValue := new(big.Int).Mul(originalValue, common.Big10)
					tenPercentOriginalValue = new(big.Int).Div(tenPercentOriginalValue, common.Big100)

					// amount after max slip is calculated based on the
					// specified slip so that if the final value exceeds this,
					// the transaction can be reverted
					amountAfterMaxSlip := new(big.Int).Mul(originalValue, new(big.Int).Sub(params.SlipAmountRange, slipAmount))
					amountAfterMaxSlip = new(big.Int).Div(amountAfterMaxSlip, params.SlipAmountRange)

					// If to is in Qi, convert the value into Qi
					if etx.To().IsInQiLedgerScope() {
						// Apply the k quai discount only if the exchange rate is increasing
						if exchangeRateIncreasing && discountedConversionAmountInInt.Cmp(common.Big0) != 0 {
							value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
							value = new(big.Int).Div(value, discountedConversionAmountInInt)
						}
					}

					// If To is in Quai, convert the value into Quai
					if etx.To().IsInQuaiLedgerScope() {
						// Apply the k quai discount only if the exchange rate is decreasing
						if !exchangeRateIncreasing && discountedConversionAmountInInt.Cmp(common.Big0) != 0 {
							value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
							value = new(big.Int).Div(value, discountedConversionAmountInInt)
						}
					}

					// If the value is less than the ten percent of the
					// original, reset it to 10%
					if value.Cmp(tenPercentOriginalValue) < 0 {
						value = new(big.Int).Set(tenPercentOriginalValue)
					}

					// If the value is less than the amount after the slip,
					// reset the value to zero, so that the transactions
					// that will get reverted dont add anything weight, to
					// the exchange rate calculation
					if value.Cmp(amountAfterMaxSlip) < 0 {
						etx.SetValue(big.NewInt(0))
					} else {
						actualConversionAmountInQuai = new(big.Int).Set(tempActualConversionAmountInQuai)
					}
				}
			}

			// Once the transactions are filtered, computed the total quai amount
			actualConversionAmountInQuai = misc.ComputeConversionAmountInQuai(header, newInboundEtxs)

			// Once all the etxs are filtered, the calculation has to run again
			// to use the true actualConversionAmount and realizedConversionAmount
			// Apply the cubic discount based on the current tempActualConversionAmountInQuai
			discountedConversionAmount := misc.ApplyCubicDiscount(block.ConversionFlowAmount(), actualConversionAmountInQuai)
			if header.NumberU64(common.PRIME_CTX) > params.ConversionSlipChangeBlock {
				discountedConversionAmount = misc.ApplyCubicDiscount(actualConversionAmountInQuai, block.ConversionFlowAmount())
			}
			discountedConversionAmountInInt, _ := discountedConversionAmount.Int(nil)

			// Conversions going against the direction of the exchange
			// rate adjustment dont get any k quai discount applied
			conversionAmountAfterKQuaiDiscount := new(big.Int).Mul(discountedConversionAmountInInt, new(big.Int).Sub(big.NewInt(params.KQuaiDiscountMultiplier), kQuaiDiscount))
			conversionAmountAfterKQuaiDiscount = new(big.Int).Div(conversionAmountAfterKQuaiDiscount, big.NewInt(params.KQuaiDiscountMultiplier))

			for i, etx := range newInboundEtxs {
				if etx.EtxType() == types.ConversionType && etx.Value().Cmp(common.Big0) > 0 {
					value := new(big.Int).Set(originalEtxValues[i])
					originalValue := new(big.Int).Set(value)

					// Apply the conversion flow discount to all transactions
					value = new(big.Int).Mul(value, discountedConversionAmountInInt)
					value = new(big.Int).Div(value, actualConversionAmountInQuai)

					// ten percent original value is calculated so that in case
					// the slip is more than this it can be reset
					tenPercentOriginalValue := new(big.Int).Mul(originalValue, common.Big10)
					tenPercentOriginalValue = new(big.Int).Div(tenPercentOriginalValue, common.Big100)

					// If to is in Qi, convert the value into Qi
					if etx.To().IsInQiLedgerScope() {
						// Apply the k quai discount only if the exchange rate is increasing
						if exchangeRateIncreasing && discountedConversionAmountInInt.Cmp(common.Big0) != 0 {
							value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
							value = new(big.Int).Div(value, discountedConversionAmountInInt)
						}

						// If the value is less than the ten percent of the
						// original, reset it to 10%
						if value.Cmp(tenPercentOriginalValue) < 0 {
							value = new(big.Int).Set(tenPercentOriginalValue)
						}

						etxValuesBeforeConversion[i] = new(big.Int).Set(value)
						realizedConversionAmountInQuai = new(big.Int).Add(realizedConversionAmountInQuai, value)
						value = misc.QuaiToQi(header, header.ExchangeRate(), header.MinerDifficulty(), value)
					}
					// If To is in Quai, convert the value into Quai
					if etx.To().IsInQuaiLedgerScope() {
						// Apply the k quai discount only if the exchange rate is decreasing
						if !exchangeRateIncreasing && discountedConversionAmountInInt.Cmp(common.Big0) != 0 {
							value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
							value = new(big.Int).Div(value, discountedConversionAmountInInt)
						}

						// If the value is less than the ten percent of the
						// original, reset it to 10%
						if value.Cmp(tenPercentOriginalValue) < 0 {
							value = new(big.Int).Set(tenPercentOriginalValue)
						}

						etxValuesBeforeConversion[i] = new(big.Int).Set(value)
						value = misc.QiToQuai(header, header.ExchangeRate(), header.MinerDifficulty(), value)
						realizedConversionAmountInQuai = new(big.Int).Add(realizedConversionAmountInQuai, value)
					}
					etx.SetValue(value)
				}
			}

			minerDifficulty := block.MinerDifficulty()

			// calculate the token choice set and write it to the disk
			updatedTokenChoiceSet, err := CalculateTokenChoicesSet(sl.hc, block, parent, block.ExchangeRate(), newInboundEtxs, actualConversionAmountInQuai, realizedConversionAmountInQuai, minerDifficulty)
			if err != nil {
				return nil, err
			}
			err = rawdb.WriteTokenChoicesSet(batch, block.Hash(), &updatedTokenChoiceSet)
			if err != nil {
				return nil, err
			}

			// ask the zone for this k quai, need to change the hash that is used to request this info
			storedExchangeRate, updateBit, err := sl.hc.GetKQuaiAndUpdateBit(header.ParentHash(common.ZONE_CTX))
			if err != nil {
				return nil, err
			}
			var exchangeRate *big.Int
			// update is paused, use the written exchange rate
			if parent.NumberU64(common.ZONE_CTX) <= params.BlocksPerYear && updateBit == 0 {
				exchangeRate = storedExchangeRate
			} else {
				exchangeRate, err = CalculateBetaFromMiningChoiceAndConversions(sl.hc, parent, block.ExchangeRate(), updatedTokenChoiceSet)
				if err != nil {
					return nil, err
				}
			}

			conversionsReverted := 0
			totalConversions := 0

			// Apply the new exchange rate on all the transactions
			for i, etx := range newInboundEtxs {
				// If the etx is conversion
				if types.IsConversionTx(etx) {

					// If there is a negative value, set it to zero
					if etx.Value().Cmp(common.Big0) < 0 {
						etx.SetValue(common.Big0)
						continue
					}

					totalConversions++

					// the transactions that have the etx value set to zero have to be reverted
					if etx.Value().Cmp(common.Big0) == 0 {
						etx.SetValue(originalEtxValues[i])
						etx.SetEtxType(uint64(types.ConversionRevertType))

						conversionsReverted++
					} else {
						value := new(big.Int).Set(etxValuesBeforeConversion[i])
						// If to is in Qi, convert the value into Qi
						if etx.To().IsInQiLedgerScope() {
							value = misc.QuaiToQi(header, exchangeRate, header.MinerDifficulty(), value)
						}
						// If To is in Quai, convert the value into Quai
						if etx.To().IsInQuaiLedgerScope() {
							value = misc.QiToQuai(header, exchangeRate, header.MinerDifficulty(), value)
						}

						etx.SetValue(value)
					}
				}
			}

			sl.logger.WithFields(log.Fields{"Total conversions": totalConversions, "Number of Conversions Reverted": conversionsReverted, "Hash": header.Hash()}).Info("Conversion Stats")
		}
	}

	time5 := common.PrettyDuration(time.Since(start))
	var subPendingEtxs types.Transactions
	var time5_1 common.PrettyDuration
	var time5_2 common.PrettyDuration
	var time5_3 common.PrettyDuration
	// Call my sub to append the block, and collect the rolled up ETXs from that sub
	if nodeCtx != common.ZONE_CTX {
		// How to get the sub pending etxs if not running the full node?.
		if sl.subInterface[location.SubIndex(sl.NodeCtx())] != nil {
			subPendingEtxs, err = sl.subInterface[location.SubIndex(sl.NodeCtx())].Append(header, block.Manifest(), domTerminus, true, newInboundEtxs)
			if err != nil {
				return nil, err
			}
			time5_1 = common.PrettyDuration(time.Since(start))
			// Cache the subordinate's pending ETXs
			pEtxs := types.PendingEtxs{Header: header.ConvertToPEtxView(), OutboundEtxs: subPendingEtxs}
			time5_2 = common.PrettyDuration(time.Since(start))
			// Add the pending etx given by the sub in the rollup
			sl.AddPendingEtxs(pEtxs)
			// Only region has the rollup hashes for pendingEtxs
			if nodeCtx == common.REGION_CTX {
				crossPrimeRollup := types.Transactions{}
				subRollup, err := sl.hc.CollectSubRollup(block)
				if err != nil {
					return nil, err
				}
				for _, etx := range subRollup {
					to := etx.To().Location()
					if to.Region() != sl.NodeLocation().Region() || types.IsConversionTx(etx) || types.IsCoinBaseTx(etx) {
						crossPrimeRollup = append(crossPrimeRollup, etx)
					}
				}
				// Rolluphash is specifically for zone rollup, which can only be validated by region
				if nodeCtx == common.REGION_CTX {
					if etxRollupHash := types.DeriveSha(crossPrimeRollup, trie.NewStackTrie(nil)); etxRollupHash != block.EtxRollupHash() {
						return nil, errors.New("sub rollup does not match sub rollup hash")
					}
				}
				// We also need to store the pendingEtxRollup to the dom
				pEtxRollup := types.PendingEtxsRollup{Header: header.ConvertToPEtxView(), EtxsRollup: crossPrimeRollup}
				sl.AddPendingEtxsRollup(pEtxRollup)
			}
			time5_3 = common.PrettyDuration(time.Since(start))
		}
	}

	time6 := common.PrettyDuration(time.Since(start))

	// Append has succeeded write the batch
	if err := batch.Write(); err != nil {
		return nil, err
	}

	time7 := common.PrettyDuration(time.Since(start))

	if order == sl.NodeCtx() {
		sl.hc.bc.chainFeed.Send(ChainEvent{Block: block, Hash: block.Hash(), Order: order, Entropy: sl.hc.TotalLogEntropy(block)})
	}

	if sl.NodeCtx() == common.ZONE_CTX && sl.ProcessingState() {
		// Every Block that got removed from the canonical hash db is sent in the side feed to be
		// recorded as uncles
		go func() {
			defer func() {
				if r := recover(); r != nil {
					sl.logger.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Fatal("Go-Quai Panicked")
				}
			}()
			sl.hc.chainSideFeed.Send(ChainSideEvent{Blocks: []*types.WorkObject{block}})
		}()
	}

	time8 := common.PrettyDuration(time.Since(start))
	// If efficiency score changed on this block compared to the parent block
	// we trigger the expansion using the expansion feed
	if nodeCtx == common.PRIME_CTX {
		parent := sl.hc.GetHeaderByHash(block.ParentHash(nodeCtx))
		if block.ExpansionNumber() > parent.ExpansionNumber() {
			sl.expansionFeed.Send(ExpansionEvent{block})
		}
	}

	time9 := common.PrettyDuration(time.Since(start))
	sl.logger.WithFields(log.Fields{
		"t0_1": time0_1,
		"t0_2": time0_2,
		"t1":   time1,
		"t2":   time2,
		"t3":   time3,
		"t4":   time4,
		"t5":   time5,
		"t6":   time6,
		"t7":   time7,
		"t8":   time8,
		"t9":   time9,
	}).Info("Times during append")

	// store the append time for the block
	appendTime := time.Since(start)
	sl.appendTimeCache.Add(block.Hash(), appendTime)

	sl.logger.WithFields(log.Fields{
		"t5_1": time5_1,
		"t5_2": time5_2,
		"t5_3": time5_3,
	}).Info("Times during sub append")

	intrinsicS, _ := sl.hc.HeaderIntrinsicLogEntropy(block.WorkObjectHeader())
	workShare, err := sl.hc.WorkShareLogEntropy(block)
	if err != nil {
		sl.logger.WithField("err", err).Error("Error calculating the work share log entropy")
		workShare = big.NewInt(0)
	}

	if nodeCtx == common.ZONE_CTX {
		sl.logger.WithFields(block.TransactionsInfo()).Info("Transactions info for Block")
	}

	var coinbaseType string
	if block.PrimaryCoinbase().IsInQuaiLedgerScope() {
		coinbaseType = "QuaiCoinbase"
	} else {
		coinbaseType = "QiCoinbase"
	}

	_, shaDiff, scryptDiff := sl.hc.DifficultyByAlgo(block)
	kawpowShares, shaShares, shaUncled, scryptShares, scryptUncled := sl.hc.CountWorkSharesByAlgo(block)

	shaCount := big.NewInt(1)
	scryptCount := big.NewInt(1)
	shaTarget := big.NewInt(1)
	scryptTarget := big.NewInt(1)

	if shaDiffAndCount := block.ShaDiffAndCount(); shaDiffAndCount != nil && shaDiffAndCount.Count() != nil {
		shaCount = shaDiffAndCount.Count()
	}
	if scryptDiffAndCount := block.ScryptDiffAndCount(); scryptDiffAndCount != nil && scryptDiffAndCount.Count() != nil {
		scryptCount = scryptDiffAndCount.Count()
	}
	if shaShareTarget := block.ShaShareTarget(); shaShareTarget != nil {
		shaTarget = new(big.Int).Set(shaShareTarget)
	}
	if scryptShareTarget := block.ScryptShareTarget(); scryptShareTarget != nil {
		scryptTarget = new(big.Int).Set(scryptShareTarget)
	}

	var quaiDiffAsPercentOfRavencoin, quaiDiffAsPercentOfRavencoinInstantaneous *big.Int
	if header.AuxPow() != nil {
		// Compare the current kawpow difficulty with the subsidy chain difficulty
		subsidyChainDiff := common.GetDifficultyFromBits(header.AuxPow().Header().Bits())
		// Normalize the difficulty, to quai block time
		subsidyChainDiff = new(big.Int).Div(subsidyChainDiff, params.RavenQuaiBlockTimeRatio)
		quaiDiffAsPercentOfRavencoinInstantaneous = new(big.Int).Div(new(big.Int).Mul(header.Difficulty(), params.RavencoinDiffPercentage), subsidyChainDiff)
	}

	if header.KawpowDifficulty() != nil {
		quaiDiffAsPercentOfRavencoin = new(big.Int).Div(new(big.Int).Mul(header.Difficulty(), params.RavencoinDiffPercentage), header.KawpowDifficulty())
	}

	sl.logger.WithFields(log.Fields{
		"number":                        block.NumberArray(),
		"hash":                          block.Hash(),
		"difficulty":                    block.Difficulty(),
		"shaDiff":                       shaDiff,
		"scryptDiff":                    scryptDiff,
		"workshares":                    len(block.Uncles()),
		"kawpowShares":                  kawpowShares,
		"kawpowShareDiff":               CalculateKawpowShareDiff(block.WorkObjectHeader()),
		"shaShares":                     shaShares,
		"scryptShares":                  scryptShares,
		"shaUncled":                     shaUncled,
		"scryptUncled":                  scryptUncled,
		"shaAvgShares":                  new(big.Float).Quo(new(big.Float).SetInt(shaCount), new(big.Float).SetInt(common.Big2e32)),
		"scryptAvgShares":               new(big.Float).Quo(new(big.Float).SetInt(scryptCount), new(big.Float).SetInt(common.Big2e32)),
		"shaTarget":                     new(big.Float).Quo(new(big.Float).SetInt(shaTarget), new(big.Float).SetInt(common.Big2e32)),
		"scryptTarget":                  new(big.Float).Quo(new(big.Float).SetInt(scryptTarget), new(big.Float).SetInt(common.Big2e32)),
		"totalTxs":                      len(block.Transactions()),
		"iworkShare":                    common.BigBitsToBitsFloat(workShare),
		"intrinsicS":                    common.BigBitsToBits(intrinsicS),
		"inboundEtxs from dom":          len(newInboundEtxs),
		"quai diff % of ravencoin":      quaiDiffAsPercentOfRavencoin,
		"quai diff % of ravencoin inst": quaiDiffAsPercentOfRavencoinInstantaneous,
		"gas":                           block.GasUsed(),
		"gasLimit":                      block.GasLimit(),
		"evmRoot":                       block.EVMRoot(),
		"utxoRoot":                      block.UTXORoot(),
		"etxSetRoot":                    block.EtxSetRoot(),
		"order":                         order,
		"location":                      block.Location(),
		"elapsed":                       common.PrettyDuration(time.Since(start)),
		"coinbaseType":                  coinbaseType,
	}).Info("Appended new block")

	if nodeCtx == common.ZONE_CTX {
		return block.OutboundEtxs(), nil
	} else {
		return subPendingEtxs, nil
	}
}

func (sl *Slice) ProcessingState() bool {
	return sl.hc.ProcessingState()
}

func (sl *Slice) randomRelayArray() []int {
	length := len(sl.subInterface)
	nums := []int{}
	for i := 0; i < length; i++ {
		nums = append(nums, i)
	}
	for i := length - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		nums[i], nums[j] = nums[j], nums[i]
	}
	return nums
}

// asyncPendingHeaderLoop waits for the pendingheader updates from the worker and updates the phCache
func (sl *Slice) asyncPendingHeaderLoop() {
	defer func() {
		if r := recover(); r != nil {
			sl.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()

	// Subscribe to the AsyncPh updates from the worker
	sl.asyncPhCh = make(chan *types.WorkObject, c_asyncPhUpdateChanSize)
	sl.asyncPhSub = sl.miner.worker.SubscribeAsyncPendingHeader(sl.asyncPhCh)

	for {
		select {
		case asyncPh := <-sl.asyncPhCh:
			sl.hc.headermu.Lock()
			bestPh := sl.ReadBestPh()
			if asyncPh != nil && bestPh != nil && bestPh.ParentHash(common.ZONE_CTX) == asyncPh.ParentHash(common.ZONE_CTX) {
				combinedPendingHeader := sl.combinePendingHeader(asyncPh, bestPh, common.ZONE_CTX, true)
				sl.SetBestPh(combinedPendingHeader)
			}
			sl.hc.headermu.Unlock()
		case <-sl.asyncPhSub.Err():
			return
		case <-sl.quit:
			return
		}
	}
}

func (sl *Slice) SendAuxPowTemplate(auxTemplate *types.AuxTemplate) error {
	err := sl.miner.worker.AddAuxPowTemplate(auxTemplate)
	if err != nil {
		return err
	}
	return nil
}

func (sl *Slice) WriteBestPh(bestPh *types.WorkObject) {
	if bestPh == nil {
		return
	}
	bestPhCopy := types.CopyWorkObject(bestPh)
	sl.bestPh.Store(bestPhCopy)
}

func (sl *Slice) ReadBestPh() *types.WorkObject {
	bestPh := sl.bestPh.Load()
	if bestPh == nil {
		return nil
	}
	phCopy := types.CopyWorkObject(bestPh.(*types.WorkObject))
	return phCopy
}

// CollectNewlyConfirmedEtxs collects all newly confirmed ETXs since the last coincident with the given location
func (sl *Slice) CollectNewlyConfirmedEtxs(block *types.WorkObject, blockOrder int) (types.Transactions, error) {
	nodeCtx := sl.NodeCtx()
	blockLocation := block.Location()
	// Collect rollup of ETXs from the subordinate node's manifest
	subRollup := types.Transactions{}
	var err error
	if nodeCtx < common.ZONE_CTX {
		rollup, exists := sl.hc.subRollupCache.Get(block.Hash())
		if exists && rollup != nil {
			subRollup = rollup
			sl.logger.WithFields(log.Fields{
				"Hash": block.Hash(),
				"len":  len(subRollup),
			}).Debug("Found the rollup in cache")
		} else {
			subRollup, err = sl.hc.CollectSubRollup(block)
			if err != nil {
				return nil, err
			}
			sl.hc.subRollupCache.Add(block.Hash(), subRollup)
		}
	}
	// Filter for ETXs destined to this sub
	newInboundEtxs := subRollup.FilterToSub(blockLocation, sl.NodeCtx(), blockOrder)
	newlyConfirmedEtxs := newInboundEtxs
	for {
		ancHash := block.ParentHash(nodeCtx)
		ancNum := block.NumberU64(nodeCtx) - 1
		parent := sl.hc.GetBlock(ancHash, ancNum)
		if parent == nil {
			return nil, fmt.Errorf("unable to find parent, hash: %s", ancHash.String())
		}

		if sl.hc.IsGenesisHash(ancHash) {
			break
		}

		// Terminate the search if the slice to which the given block belongs was
		// not activated yet, expansion number is validated on prime block, so only
		// terminate on prime block, It is possible to make this check more
		// optimized but this is performant enough and in some cases might have to
		// go through few more region blocks than necessary
		var err error
		_, order, err := sl.CalcOrder(parent)
		if err != nil {
			return nil, err
		}
		regions, zones := common.GetHierarchySizeForExpansionNumber(parent.ExpansionNumber())
		if order == common.PRIME_CTX && (blockLocation.Region() > int(regions) || blockLocation.Zone() > int(zones)) {
			break
		}

		// Terminate the search when we find a block produced by the same sub
		if parent.Location().SubIndex(sl.NodeCtx()) == blockLocation.SubIndex(sl.NodeCtx()) && order == nodeCtx {
			break
		}

		subRollup := types.Transactions{}
		if nodeCtx < common.ZONE_CTX {
			rollup, exists := sl.hc.subRollupCache.Get(parent.Hash())
			if exists && rollup != nil {
				subRollup = rollup
				sl.logger.WithFields(log.Fields{
					"Hash": parent.Hash(),
					"len":  len(subRollup),
				}).Debug("Found the rollup in cache")
			} else {
				subRollup, err = sl.hc.CollectSubRollup(parent)
				if err != nil {
					return nil, err
				}
				sl.hc.subRollupCache.Add(parent.Hash(), subRollup)
			}
		}
		// change for the rolldown feature is that in the region when we go
		// back, if we find a prime block, we have to take the transactions that
		// are going towards the given zone
		if nodeCtx == common.REGION_CTX && order < nodeCtx && !blockLocation.Equal(parent.Location()) {
			inboundEtxs := rawdb.ReadInboundEtxs(sl.sliceDb, parent.Hash())
			// Filter the Conversion and Coinbase Txs that needs to go the blockLocation
			filteredInboundEtxs := inboundEtxs.FilterToSub(blockLocation, sl.NodeCtx(), common.PRIME_CTX)
			newlyConfirmedEtxs = append(newlyConfirmedEtxs, filteredInboundEtxs...)
		}

		ancInboundEtxs := subRollup.FilterToSub(blockLocation, sl.NodeCtx(), blockOrder)
		newlyConfirmedEtxs = append(newlyConfirmedEtxs, ancInboundEtxs...)
		block = parent
	}
	return newlyConfirmedEtxs, nil
}

// PCRC previous coincidence reference check makes sure there are not any cyclic references in the graph and calculates new termini and the block terminus
func (sl *Slice) pcrc(batch ethdb.Batch, header *types.WorkObject, domTerminus common.Hash, domOrigin bool) (common.Hash, types.Termini, error) {
	nodeLocation := sl.NodeLocation()
	nodeCtx := sl.NodeCtx()
	location := header.Location()

	sl.logger.WithFields(log.Fields{
		"parent hash": header.ParentHash(nodeCtx),
		"number":      header.NumberArray(),
		"location":    header.Location(),
	}).Debug("PCRC")
	termini := sl.hc.GetTerminiByHash(header.ParentHash(nodeCtx))

	if !termini.IsValid() {
		return common.Hash{}, types.EmptyTermini(), ErrSubNotSyncedToDom
	}

	newTermini := types.CopyTermini(*termini)
	// Set the subtermini
	if nodeCtx != common.ZONE_CTX {
		newTermini.SetSubTerminiAtIndex(header.Hash(), location.SubIndex(nodeCtx))
	}

	// Set the terminus
	if nodeCtx == common.PRIME_CTX || domOrigin {
		newTermini.SetDomTerminiAtIndex(header.Hash(), location.DomIndex(nodeLocation))
	} else {
		newTermini.SetDomTerminiAtIndex(termini.DomTerminus(nodeLocation), location.DomIndex(nodeLocation))
	}

	// Check for a graph cyclic reference
	if domOrigin {
		if !sl.hc.IsGenesisHash(termini.DomTerminus(nodeLocation)) && termini.DomTerminus(nodeLocation) != domTerminus {
			sl.logger.WithFields(log.Fields{
				"block number": header.NumberArray(),
				"hash":         header.Hash(),
				"terminus":     domTerminus,
				"termini":      termini.DomTerminus(nodeLocation),
			}).Warn("Cyclic block")
			return common.Hash{}, types.EmptyTermini(), errors.New("termini do not match, block rejected due to cyclic reference")
		}
	}

	//Save the termini
	rawdb.WriteTermini(batch, header.Hash(), newTermini)

	if nodeCtx == common.ZONE_CTX {
		return common.Hash{}, newTermini, nil
	}

	return termini.SubTerminiAtIndex(location.SubIndex(nodeCtx)), newTermini, nil
}

// GetPendingHeader is used by the miner to request the current pending header
func (sl *Slice) GetPendingHeader(powId types.PowID, coinbase common.Address) (*types.WorkObject, error) {
	phCopy := types.CopyWorkObject(sl.ReadBestPh())
	if phCopy == nil {
		return nil, errors.New("no pending header available")
	}
	// set the auxpow to nil if its progpow
	if powId == types.Progpow {
		// Set the coinbase for the progpow header
		if !coinbase.Equal(common.Address{}) {
			phCopy.WorkObjectHeader().SetPrimaryCoinbase(coinbase)
			sl.miner.worker.AddPendingWorkObjectBody(phCopy)
		}

		phCopy.WorkObjectHeader().SetAuxPow(nil)
	} else {
		// Only serve after the fork block
		if phCopy != nil && phCopy.PrimeTerminusNumber().Uint64() < params.KawPowForkBlock {
			return nil, errors.New("pending header for non progpow requested before kawpow fork")
		}
		auxTemplate := sl.miner.worker.GetBestAuxTemplate(powId)
		// If we have a KAWPOW template, we need to create a proper Ravencoin header
		switch powId {
		case types.Kawpow, types.SHA_BTC, types.SHA_BCH, types.Scrypt:
			// If we have an auxpow template, we need to create a proper Ravencoin header
			if sl.NodeCtx() == common.ZONE_CTX && auxTemplate != nil {

				// If the coinbase is set, update the pending header with the new coinbase
				if !coinbase.Equal(common.Address{}) {
					phCopy.WorkObjectHeader().SetPrimaryCoinbase(coinbase)
				}

				phCopy.WorkObjectHeader().SetTime(uint64(time.Now().Unix()))
				if powId == types.Scrypt || powId == types.SHA_BCH || powId == types.SHA_BTC {
					phCopy.WorkObjectHeader().SetTxHash(types.EmptyRootHash)
				}

				auxMerkleRoot := phCopy.SealHash()
				if powId == types.Scrypt {
					if len(auxTemplate.AuxPow2()) == 0 {
						return nil, errors.New("no auxpow2 available for scrypt mining")
					}
					dogeHash := common.Hash(auxTemplate.AuxPow2())
					auxMerkleRoot = types.CreateAuxMerkleRoot(dogeHash, phCopy.SealHash())
				}
				coinbaseTransaction := types.NewAuxPowCoinbaseTx(powId, auxTemplate.Height(), auxTemplate.CoinbaseOut(), auxMerkleRoot, auxTemplate.SignatureTime())

				merkleRoot := types.CalculateMerkleRoot(powId, coinbaseTransaction, auxTemplate.MerkleBranch())

				// Create a properly configured Ravencoin header for KAWPOW mining
				auxHeader := types.NewBlockHeader(powId, int32(auxTemplate.Version()), auxTemplate.PrevHash(), merkleRoot, auxTemplate.SignatureTime(), auxTemplate.Bits(), 0, auxTemplate.Height())

				// Dont have the actual hash of the block yet
				auxPow := types.NewAuxPow(powId, auxHeader, auxTemplate.AuxPow2(), auxTemplate.Sigs(), auxTemplate.MerkleBranch(), coinbaseTransaction)

				// Update the auxpow in the best pending header
				phCopy.WorkObjectHeader().SetAuxPow(auxPow)

				// Create composite cache key: hash(auxMerkleRoot || signatureTime)
				// This ensures each template with different signatureTime gets its own cache entry
				compositeKey := crypto.Keccak256Hash(auxMerkleRoot.Bytes(), common.BigToHash(big.NewInt(int64(auxTemplate.SignatureTime()))).Bytes())

				// Update the pending block body cache with the populated AuxPow
				// This ensures SubmitBlock can retrieve the complete AuxPow including merkle branch
				sl.miner.worker.AddPendingWorkObjectBody(phCopy)
				sl.miner.worker.AddPendingWorkObjectBodyWithKey(phCopy, compositeKey)
				sl.miner.worker.AddPendingAuxPow(powId, compositeKey, types.CopyAuxPow(auxPow))
				sl.miner.worker.AddPendingAuxPow(powId, phCopy.SealHash(), types.CopyAuxPow(auxPow))

			} else {
				return nil, errors.New("no auxpow template available for " + powId.String() + " mining")
			}
		default:
			return nil, errors.New("pending header requested for unknown pow id")
		}
	}

	return phCopy, nil
}

func (sl *Slice) SetBestPh(pendingHeader *types.WorkObject) {
	pendingHeader.WorkObjectHeader().SetLocation(sl.NodeLocation())
	pendingHeader.WorkObjectHeader().SetTime(uint64(time.Now().Unix()))
	pendingHeader.WorkObjectHeader().SetHeaderHash(pendingHeader.Header().Hash())

	sl.miner.worker.AddPendingWorkObjectBody(pendingHeader)
	rawdb.WriteBestPendingHeader(sl.sliceDb, pendingHeader)
	sl.WriteBestPh(pendingHeader)
	sl.logger.WithFields(log.Fields{"Number": pendingHeader.NumberArray(), "ParentHash": pendingHeader.ParentHashArray()}).Info("Best PH pick")
	sl.miner.worker.pendingHeaderFeed.Send(pendingHeader)
}

// GetManifest gathers the manifest of ancestor block hashes since the last
// coincident block.
func (sl *Slice) GetManifest(blockHash common.Hash) (types.BlockManifest, error) {
	manifest := rawdb.ReadManifest(sl.sliceDb, blockHash)
	if manifest != nil {
		return manifest, nil
	}
	return nil, errors.New("manifest not found in the disk")
}

// GetSubManifest gets the block manifest from the subordinate node which
// produced this block
func (sl *Slice) GetSubManifest(slice common.Location, blockHash common.Hash) (types.BlockManifest, error) {
	subIdx := slice.SubIndex(sl.NodeCtx())
	if sl.subInterface[subIdx] == nil {
		return nil, errors.New("missing requested subordinate node")
	}
	return sl.subInterface[subIdx].GetManifest(blockHash)
}

func (sl *Slice) GetKQuaiAndUpdateBit(hash common.Hash) (*big.Int, uint8, error) {
	// If in zone, read it directly from the database
	// otherwise, call the sub interface
	if sl.NodeCtx() != common.ZONE_CTX {
		// This only works with first expansion or when the first zone is updating the exchange rate
		kQuai, updateBit, err := sl.subInterface[0].GetKQuaiAndUpdateBit(hash)
		if err != nil && err.Error() == ErrSubNotSyncedToDom.Error() {
			block := sl.hc.GetBlockByHash(hash)
			if block != nil {
				// In the case of this block being a region and state is not
				// processed, send an update to the hierarchical coordinator
				_, order, calcOrderErr := sl.hc.CalcOrder(block)
				if calcOrderErr == nil {
					if order == sl.NodeCtx() {
						sl.hc.bc.chainFeed.Send(ChainEvent{Block: block, Hash: block.Hash(), Order: order, Entropy: sl.hc.TotalLogEntropy(block)})
					}
				}
			}
		}
		return kQuai, updateBit, err
	} else {
		block := sl.hc.GetBlockByHash(hash)
		if block == nil {
			return nil, 0, ErrSubNotSyncedToDom
		}
		kQuai, updateBit, err := sl.hc.bc.processor.GetKQuaiAndUpdateBit(block)
		if err != nil && err.Error() == ErrSubNotSyncedToDom.Error() {
			// send an update to the hierarchical coordinator
			_, order, calcOrderErr := sl.hc.CalcOrder(block)
			if calcOrderErr == nil {
				if order == sl.NodeCtx() {
					sl.hc.bc.chainFeed.Send(ChainEvent{Block: block, Hash: block.Hash(), Order: order, Entropy: sl.hc.TotalLogEntropy(block)})
				}
			}
		}
		return kQuai, updateBit, err
	}
}

// SendPendingEtxsToDom shares a set of pending ETXs with your dom, so he can reference them when a coincident block is found
func (sl *Slice) SendPendingEtxsToDom(pEtxs types.PendingEtxs) error {
	return sl.domInterface.AddPendingEtxs(pEtxs)
}

func (sl *Slice) GetPEtxRollupAfterRetryThreshold(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	pEtx, exists := sl.pEtxRetryCache.Get(blockHash)
	if !exists || pEtx.retries < c_pEtxRetryThreshold {
		// Keeping track of the number of times pending etx fails and if it crossed the retry threshold
		// ask the sub for the pending etx/rollup data
		val, exist := sl.pEtxRetryCache.Get(blockHash)
		var retry uint64
		if exist {
			pEtxCurrent := val
			retry = pEtxCurrent.retries + 1
		}
		pEtxNew := pEtxRetry{hash: blockHash, retries: retry}
		sl.pEtxRetryCache.Add(blockHash, pEtxNew)
		return types.PendingEtxsRollup{}, ErrPendingEtxNotFound
	}
	return sl.GetPendingEtxsRollupFromSub(hash, location)
}

// GetPendingEtxsRollupFromSub gets the pending etxs rollup from the appropriate prime
func (sl *Slice) GetPendingEtxsRollupFromSub(hash common.Hash, location common.Location) (types.PendingEtxsRollup, error) {
	nodeCtx := sl.NodeLocation().Context()
	if nodeCtx == common.PRIME_CTX {
		if sl.subInterface[location.SubIndex(sl.NodeCtx())] != nil {
			pEtxRollup, err := sl.subInterface[location.SubIndex(sl.NodeCtx())].GetPendingEtxsRollupFromSub(hash, location)
			if err != nil {
				return types.PendingEtxsRollup{}, err
			} else {
				sl.AddPendingEtxsRollup(pEtxRollup)
				return pEtxRollup, nil
			}
		}
	} else if nodeCtx == common.REGION_CTX {
		block := sl.hc.GetBlockByHash(hash)
		if block != nil {
			subRollup, err := sl.hc.CollectSubRollup(block)
			if err != nil {
				return types.PendingEtxsRollup{}, err
			}
			return types.PendingEtxsRollup{Header: block.ConvertToPEtxView(), EtxsRollup: subRollup}, nil
		}
	}
	return types.PendingEtxsRollup{}, ErrPendingEtxNotFound
}

func (sl *Slice) GetPEtxAfterRetryThreshold(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	pEtx, exists := sl.pEtxRetryCache.Get(blockHash)
	if !exists || pEtx.retries < c_pEtxRetryThreshold {
		// Keeping track of the number of times pending etx fails and if it crossed the retry threshold
		// ask the sub for the pending etx/rollup data
		val, exist := sl.pEtxRetryCache.Get(blockHash)
		var retry uint64
		if exist {
			pEtxCurrent := val
			retry = pEtxCurrent.retries + 1
		}
		pEtxNew := pEtxRetry{hash: blockHash, retries: retry}
		sl.pEtxRetryCache.Add(blockHash, pEtxNew)
		return types.PendingEtxs{}, ErrPendingEtxNotFound
	}
	return sl.GetPendingEtxsFromSub(hash, location)
}

// GetPendingEtxsFromSub gets the pending etxs from the appropriate prime
func (sl *Slice) GetPendingEtxsFromSub(hash common.Hash, location common.Location) (types.PendingEtxs, error) {
	nodeCtx := sl.NodeLocation().Context()
	if nodeCtx != common.ZONE_CTX {
		if sl.subInterface[location.SubIndex(sl.NodeCtx())] != nil {
			pEtx, err := sl.subInterface[location.SubIndex(sl.NodeCtx())].GetPendingEtxsFromSub(hash, location)
			if err != nil {
				return types.PendingEtxs{}, err
			} else {
				sl.AddPendingEtxs(pEtx)
				return pEtx, nil
			}
		}
	}
	block := sl.hc.GetBlockByHash(hash)
	if block != nil {
		return types.PendingEtxs{Header: block.ConvertToPEtxView(), OutboundEtxs: block.OutboundEtxs()}, nil
	}
	return types.PendingEtxs{}, ErrPendingEtxNotFound
}

func (sl *Slice) GetPrimaryCoinbase() common.Address {
	return sl.miner.worker.GetPrimaryCoinbase()
}

// init checks if the headerchain is empty and if it's empty appends the Knot
// otherwise loads the last stored state of the chain.
func (sl *Slice) init() error {

	// Even though the genesis block cannot have any ETXs, we still need an empty
	// pending ETX entry for that block hash, so that the state processor can build
	// on it
	genesisHashes := sl.hc.GetGenesisHashes()
	genesisHash := genesisHashes[0]
	genesisHeader := sl.hc.GetHeaderByHash(genesisHash)
	if genesisHeader == nil {
		return errors.New("failed to get genesis header")
	}

	// Loading the badHashes from the data base and storing it in the cache
	badHashes := rawdb.ReadBadHashesList(sl.sliceDb)
	sl.AddToBadHashesList(badHashes)

	// If the headerchain is empty start from genesis
	if sl.hc.Empty() {
		// Initialize slice state for genesis knot
		genesisTermini := types.EmptyTermini()
		for i := 0; i < len(genesisTermini.SubTermini()); i++ {
			genesisTermini.SetSubTerminiAtIndex(genesisHash, i)
		}
		for i := 0; i < len(genesisTermini.DomTermini()); i++ {
			genesisTermini.SetDomTerminiAtIndex(genesisHash, i)
		}

		newTokenChoiceSet := types.NewTokenChoiceSet()
		rawdb.WriteTokenChoicesSet(sl.sliceDb, genesisHash, &newTokenChoiceSet)

		rawdb.WriteTermini(sl.sliceDb, genesisHash, genesisTermini)
		rawdb.WriteManifest(sl.sliceDb, genesisHash, types.BlockManifest{genesisHash})

		// Create empty pending ETX entry for genesis block -- genesis may not emit ETXs
		emptyPendingEtxs := types.Transactions{}
		rawdb.WritePendingEtxs(sl.sliceDb, types.PendingEtxs{Header: genesisHeader, OutboundEtxs: emptyPendingEtxs})
		rawdb.WritePendingEtxsRollup(sl.sliceDb, types.PendingEtxsRollup{Header: genesisHeader, EtxsRollup: emptyPendingEtxs})
		err := sl.hc.AddBloom(types.Bloom{}, genesisHeader.Hash())
		if err != nil {
			return err
		}

		// This is just done for the startup process
		sl.hc.SetCurrentHeader(genesisHeader)

		if sl.NodeLocation().Context() == common.PRIME_CTX {
			go sl.NewGenesisPendingHeader(nil, genesisHash, genesisHash)
		}
	} else { // load the phCache and slice current pending header hash
		if err := sl.loadLastState(); err != nil {
			return err
		}
	}
	return nil
}

// constructLocalBlock takes a header and construct the Block locally by getting the body
// from the candidate body db. This method is used when peers give the block as a placeholder
// for the body.
func (sl *Slice) ConstructLocalBlock(header *types.WorkObject) (*types.WorkObject, error) {
	block := rawdb.ReadWorkObject(sl.sliceDb, header.NumberU64(sl.NodeCtx()), header.Hash(), types.BlockObject)
	if block == nil {
		return nil, ErrBodyNotFound
	}
	if err := sl.validator.ValidateBody(block); err != nil {
		return block, err
	} else {
		return block, nil
	}
}

// constructLocalMinedBlock takes a header and construct the Block locally by getting the block
// body from the workers pendingBlockBodyCache. This method is used when the miner sends in the
// header.
func (sl *Slice) ConstructLocalMinedBlock(wo *types.WorkObject) (*types.WorkObject, error) {
	nodeCtx := sl.NodeLocation().Context()
	var pendingBlockBody *types.WorkObject
	if nodeCtx == common.ZONE_CTX {
		// do not include the tx hash while storing the body
		woHeaderCopy := types.CopyWorkObjectHeader(wo.WorkObjectHeader())
		var powId types.PowID
		if woHeaderCopy.AuxPow() == nil {
			powId = types.Progpow
		} else {
			powId = woHeaderCopy.AuxPow().PowID()
		}

		pendingBlockBody = sl.GetPendingBlockBody(powId, woHeaderCopy.SealHash())
		if pendingBlockBody == nil {
			sl.logger.WithFields(log.Fields{"wo.Hash": wo.Hash(),
				"wo.Header":       wo.HeaderHash(),
				"wo.ParentHash":   wo.ParentHash(common.ZONE_CTX),
				"wo.Difficulty()": wo.Difficulty(),
				"wo.Location()":   wo.Location(),
			}).Error("Pending Block Body not found")
			// we need to recompute another body at the current state
			sl.hc.chainHeadFeed.Send(ChainHeadEvent{sl.hc.CurrentHeader()})
			return nil, ErrBodyNotFound
		}
		if wo.NumberU64(common.ZONE_CTX) > uint64(params.WorkSharesInclusionDepth) && len(pendingBlockBody.OutboundEtxs()) == 0 {
			sl.logger.WithFields(log.Fields{"wo.Hash": wo.Hash(),
				"wo.Header":       wo.HeaderHash(),
				"wo.ParentHash":   wo.ParentHash(common.ZONE_CTX),
				"wo.Difficulty()": wo.Difficulty(),
				"wo.Location()":   wo.Location(),
			}).Error("Pending Block Body has no transactions")
			return nil, ErrBodyNotFound
		}
	} else {
		// If the context is PRIME, there is the interlink hashes that needs to be returned from the database
		var interlinkHashes common.Hashes
		if nodeCtx == common.PRIME_CTX {
			interlinkHashes = rawdb.ReadInterlinkHashes(sl.sliceDb, wo.ParentHash(common.PRIME_CTX))
		}
		wo.Body().SetUncles(nil)
		wo.Body().SetTransactions(nil)
		wo.Body().SetOutboundEtxs(nil)
		wo.Body().SetInterlinkHashes(interlinkHashes)
		pendingBlockBody = types.NewWorkObject(wo.WorkObjectHeader(), wo.Body(), nil)
	}
	// Load uncles because they are not included in the block response.
	txs := make([]*types.Transaction, len(pendingBlockBody.Transactions()))
	for i, tx := range pendingBlockBody.Transactions() {
		txs[i] = tx
	}
	uncles := make([]*types.WorkObjectHeader, len(pendingBlockBody.Uncles()))
	for i, uncle := range pendingBlockBody.Uncles() {
		uncles[i] = uncle
		sl.logger.WithField("hash", uncle.Hash()).Debug("Pending Block uncle")
	}
	etxs := make(types.Transactions, len(pendingBlockBody.OutboundEtxs()))
	for i, etx := range pendingBlockBody.OutboundEtxs() {
		etxs[i] = etx
	}
	subManifest := make(types.BlockManifest, len(pendingBlockBody.Manifest()))
	for i, blockHash := range pendingBlockBody.Manifest() {
		subManifest[i] = blockHash
	}
	interlinkHashes := make(common.Hashes, len(pendingBlockBody.InterlinkHashes()))
	for i, interlinkhash := range pendingBlockBody.InterlinkHashes() {
		interlinkHashes[i] = interlinkhash
	}
	pendingBlockBody.Body().SetTransactions(txs)
	pendingBlockBody.Body().SetUncles(uncles)
	pendingBlockBody.Body().SetOutboundEtxs(etxs)
	pendingBlockBody.Body().SetManifest(subManifest)
	pendingBlockBody.Body().SetInterlinkHashes(interlinkHashes)
	block := types.NewWorkObject(wo.WorkObjectHeader(), pendingBlockBody.Body(), nil)
	if nodeCtx != common.ZONE_CTX {
		subManifestHash := types.DeriveSha(block.Manifest(), trie.NewStackTrie(nil))
		if subManifestHash == types.EmptyRootHash || subManifestHash != block.ManifestHash(nodeCtx+1) {
			// If we have a subordinate chain, it is impossible for the subordinate manifest to be empty
			return block, ErrBadSubManifest
		}
	}
	return block, nil
}

// combinePendingHeader updates the pending header at the given index with the value from given header.
func (sl *Slice) combinePendingHeader(header *types.WorkObject, slPendingHeader *types.WorkObject, index int, inSlice bool) *types.WorkObject {
	// copying the slPendingHeader and updating the copy to remove any shared memory access issues
	combinedPendingHeader := types.CopyWorkObject(slPendingHeader)

	combinedPendingHeader.SetParentHash(header.ParentHash(index), index)
	combinedPendingHeader.SetNumber(header.Number(index), index)
	combinedPendingHeader.Header().SetManifestHash(header.ManifestHash(index), index)
	combinedPendingHeader.Header().SetParentEntropy(header.ParentEntropy(index), index)
	combinedPendingHeader.Header().SetParentDeltaEntropy(header.ParentDeltaEntropy(index), index)
	combinedPendingHeader.Header().SetParentUncledDeltaEntropy(header.ParentUncledDeltaEntropy(index), index)

	if index == common.PRIME_CTX {
		combinedPendingHeader.Header().SetEfficiencyScore(header.EfficiencyScore())
		combinedPendingHeader.Header().SetThresholdCount(header.ThresholdCount())
		combinedPendingHeader.Header().SetEtxEligibleSlices(header.EtxEligibleSlices())
		combinedPendingHeader.Header().SetInterlinkRootHash(header.InterlinkRootHash())
		combinedPendingHeader.Header().SetExchangeRate(header.ExchangeRate())
		combinedPendingHeader.Header().SetKQuaiDiscount(header.KQuaiDiscount())
		combinedPendingHeader.Header().SetConversionFlowAmount(header.ConversionFlowAmount())
		combinedPendingHeader.Header().SetMinerDifficulty(header.MinerDifficulty())
		combinedPendingHeader.Header().SetPrimeStateRoot(header.PrimeStateRoot())
	}

	if index == common.REGION_CTX {
		combinedPendingHeader.Header().SetRegionStateRoot(header.RegionStateRoot())
	}

	if inSlice {
		combinedPendingHeader.WorkObjectHeader().SetDifficulty(header.Difficulty())
		combinedPendingHeader.WorkObjectHeader().SetTxHash(header.TxHash())
		combinedPendingHeader.WorkObjectHeader().SetPrimeTerminusNumber(header.PrimeTerminusNumber())
		combinedPendingHeader.WorkObjectHeader().SetLock(header.Lock())
		combinedPendingHeader.WorkObjectHeader().SetPrimaryCoinbase(header.PrimaryCoinbase())
		combinedPendingHeader.WorkObjectHeader().SetData(header.Data())

		// These are the fields that were added on the kawpow fork block, so
		// checking its not nil to preserve backwards compatibility
		if header.AuxPow() != nil {
			combinedPendingHeader.WorkObjectHeader().SetAuxPow(header.AuxPow())
		}
		if header.WorkObjectHeader().ShaDiffAndCount() != nil {
			combinedPendingHeader.WorkObjectHeader().SetShaDiffAndCount(header.WorkObjectHeader().ShaDiffAndCount())
		}
		if header.WorkObjectHeader().ScryptDiffAndCount() != nil {
			combinedPendingHeader.WorkObjectHeader().SetScryptDiffAndCount(header.WorkObjectHeader().ScryptDiffAndCount())
		}
		if header.WorkObjectHeader().ShaShareTarget() != nil {
			combinedPendingHeader.WorkObjectHeader().SetShaShareTarget(header.WorkObjectHeader().ShaShareTarget())
		}
		if header.WorkObjectHeader().ScryptShareTarget() != nil {
			combinedPendingHeader.WorkObjectHeader().SetScryptShareTarget(header.WorkObjectHeader().ScryptShareTarget())
		}
		if header.WorkObjectHeader().KawpowDifficulty() != nil {
			combinedPendingHeader.WorkObjectHeader().SetKawpowDifficulty(header.WorkObjectHeader().KawpowDifficulty())
		}

		combinedPendingHeader.Header().SetEtxRollupHash(header.EtxRollupHash())
		combinedPendingHeader.Header().SetUncledEntropy(header.Header().UncledEntropy())
		combinedPendingHeader.Header().SetQuaiStateSize(header.Header().QuaiStateSize())
		combinedPendingHeader.Header().SetUncleHash(header.UncleHash())
		combinedPendingHeader.Header().SetTxHash(header.Header().TxHash())
		combinedPendingHeader.Header().SetOutboundEtxHash(header.OutboundEtxHash())
		combinedPendingHeader.Header().SetEtxSetRoot(header.EtxSetRoot())
		combinedPendingHeader.Header().SetReceiptHash(header.ReceiptHash())
		combinedPendingHeader.Header().SetEVMRoot(header.EVMRoot())
		combinedPendingHeader.Header().SetUTXORoot(header.UTXORoot())
		combinedPendingHeader.Header().SetEtxSetRoot(header.EtxSetRoot())
		combinedPendingHeader.Header().SetBaseFee(header.BaseFee())
		combinedPendingHeader.Header().SetStateLimit(header.StateLimit())
		combinedPendingHeader.Header().SetStateUsed(header.StateUsed())
		combinedPendingHeader.Header().SetGasLimit(header.GasLimit())
		combinedPendingHeader.Header().SetGasUsed(header.GasUsed())
		combinedPendingHeader.Header().SetExtra(header.Extra())
		combinedPendingHeader.Header().SetPrimeTerminusHash(header.PrimeTerminusHash())
		combinedPendingHeader.Header().SetExpansionNumber(header.ExpansionNumber())
		combinedPendingHeader.Header().SetAvgTxFees(header.AvgTxFees())
		combinedPendingHeader.Header().SetTotalFees(header.TotalFees())

		combinedPendingHeader.Body().SetTransactions(header.Transactions())
		combinedPendingHeader.Body().SetOutboundEtxs(header.OutboundEtxs())
		combinedPendingHeader.Body().SetUncles(header.Uncles())
		combinedPendingHeader.Body().SetManifest(header.Manifest())
	}

	return combinedPendingHeader
}

func (sl *Slice) IsSubClientsEmpty() bool {
	activeRegions, _ := common.GetHierarchySizeForExpansionNumber(sl.hc.currentExpansionNumber)
	switch sl.NodeCtx() {
	case common.PRIME_CTX:
		for i := 0; i < int(activeRegions); i++ {
			if sl.subInterface[i] == nil {
				return true
			}
		}
	case common.REGION_CTX:
		for _, slice := range sl.ActiveSlices() {
			if sl.subInterface[slice.Zone()] == nil {
				return true
			}
		}
	}
	return false
}

// ActiveSlices returns the active slices for the current expansion number
func (sl *Slice) ActiveSlices() []common.Location {
	currentRegions, currentZones := common.GetHierarchySizeForExpansionNumber(sl.hc.currentExpansionNumber)
	activeSlices := []common.Location{}
	for i := 0; i < int(currentRegions); i++ {
		for j := 0; j < int(currentZones); j++ {
			activeSlices = append(activeSlices, common.Location{byte(i), byte(j)})
		}
	}
	return activeSlices
}

func (sl *Slice) WriteGenesisBlock(block *types.WorkObject, location common.Location) {
	rawdb.WriteManifest(sl.sliceDb, block.Hash(), types.BlockManifest{block.Hash()})
	// Create empty pending ETX entry for genesis block -- genesis may not emit ETXs
	emptyPendingEtxs := types.Transactions{}
	sl.hc.AddPendingEtxs(types.PendingEtxs{block, emptyPendingEtxs})
	sl.AddPendingEtxsRollup(types.PendingEtxsRollup{block, emptyPendingEtxs})
	sl.hc.AddBloom(types.Bloom{}, block.Hash())
	sl.hc.currentHeader.Store(block)
}

// NewGenesisPendingHeader creates a pending header on the genesis block
func (sl *Slice) NewGenesisPendingHeader(domPendingHeader *types.WorkObject, domTerminus common.Hash, genesisHash common.Hash) error {
	defer func() {
		if r := recover(); r != nil {
			sl.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	nodeCtx := sl.NodeLocation().Context()

	if nodeCtx == common.ZONE_CTX && !sl.hc.Empty() {
		return nil
	}
	// Wait until the subclients are all initialized
	if nodeCtx != common.ZONE_CTX {
		for sl.IsSubClientsEmpty() {
			if !sl.IsSubClientsEmpty() {
				break
			}
		}
	}

	// get the genesis block to start from
	genesisBlock := sl.hc.GetBlockByHash(genesisHash)
	genesisTermini := types.EmptyTermini()
	for i := 0; i < len(genesisTermini.SubTermini()); i++ {
		genesisTermini.SetSubTerminiAtIndex(genesisHash, i)
	}
	for i := 0; i < len(genesisTermini.DomTermini()); i++ {
		genesisTermini.SetDomTerminiAtIndex(genesisHash, i)
	}

	// Update the local pending header
	var localPendingHeader *types.WorkObject
	var err error
	var termini types.Termini
	sl.logger.Infof("NewGenesisPendingHeader location: %v, genesis hash %s", sl.NodeLocation(), genesisHash)
	if sl.hc.IsGenesisHash(genesisHash) {
		localPendingHeader, err = sl.miner.worker.GeneratePendingHeader(genesisBlock, false)
		if err != nil {
			sl.logger.WithFields(log.Fields{
				"err": err,
			}).Warn("Error generating the New Genesis Pending Header")
			return nil
		}
		termini = genesisTermini

		if nodeCtx != common.ZONE_CTX {
			localPendingHeader.WorkObjectHeader().SetPrimaryCoinbase(common.Zero)
		}

		if nodeCtx == common.PRIME_CTX {
			domPendingHeader = types.CopyWorkObject(localPendingHeader)
		} else {
			domPendingHeader = sl.combinePendingHeader(localPendingHeader, domPendingHeader, nodeCtx, true)
			domPendingHeader.WorkObjectHeader().SetLocation(sl.NodeLocation())
		}

		if nodeCtx != common.ZONE_CTX {
			for i, client := range sl.subInterface {
				if client != nil {
					go func(client CoreBackend) {
						err = client.NewGenesisPendingHeader(domPendingHeader, termini.SubTerminiAtIndex(i), genesisHash)
						if err != nil {
							sl.logger.Error("Error in NewGenesisPendingHeader, err", err)
						}
					}(client)
				}
			}
		}

		domPendingHeader.WorkObjectHeader().SetTime(uint64(time.Now().Unix()))
		sl.SetBestPh(domPendingHeader)
	}
	return nil
}

func (sl *Slice) MakeFullPendingHeader(primePendingHeader, regionPendingHeader, zonePendingHeader *types.WorkObject) *types.WorkObject {
	combinedPendingHeader := sl.combinePendingHeader(regionPendingHeader, primePendingHeader, common.REGION_CTX, true)
	combinedPendingHeader = sl.combinePendingHeader(zonePendingHeader, combinedPendingHeader, common.ZONE_CTX, true)
	sl.SetBestPh(combinedPendingHeader)
	return combinedPendingHeader
}

func (sl *Slice) verifyParentExchangeRateAndFlowAmount(header *types.WorkObject) error {

	nodeCtx := sl.NodeCtx()

	if nodeCtx == common.PRIME_CTX {
		// The exchange rate in the header has to be the exchange rate computed
		// on top the parent values
		parent := sl.hc.GetBlockByHash(header.ParentHash(common.PRIME_CTX))
		if parent == nil {
			return errors.New("parent not found in verifyParentExchangeRateAndFlowAmount")
		}

		// Read the conversion flow amount saved for the parent block hash
		var err error
		newInboundEtxs := rawdb.ReadInboundEtxs(sl.sliceDb, parent.Hash())
		if newInboundEtxs == nil {
			return errors.New("cannot find the inbound etxs for the parent")
		}

		// Conversion Flows that happen in the direction of the exchange rate
		// controller adjustment, get the k quai discount,and the conversion
		// flows going against the exchange rate controller adjustment dont
		var exchangeRateIncreasing bool
		if parent.NumberU64(common.PRIME_CTX) > params.MinerDifficultyWindow {
			prevBlock := sl.hc.GetBlockByNumber(parent.NumberU64(common.PRIME_CTX) - params.MinerDifficultyWindow)
			if prevBlock == nil {
				return errors.New("block minerdifficultywindow blocks behind not found")
			}

			if parent.ExchangeRate().Cmp(prevBlock.ExchangeRate()) > 0 {
				exchangeRateIncreasing = true
			}
		}

		// sort the newInboundEtxs based on the decreasing order of the max slips
		sort.SliceStable(newInboundEtxs, func(i, j int) bool {
			etxi := newInboundEtxs[i]
			etxj := newInboundEtxs[j]

			var slipi *big.Int
			if etxi.EtxType() == types.ConversionType {
				// If no slip is mentioned it is set to 90%
				slipAmount := new(big.Int).Set(params.MaxSlip)
				if len(etxi.Data()) > 1 {
					slipAmount = new(big.Int).SetBytes(etxi.Data()[:2])
					if slipAmount.Cmp(params.MaxSlip) > 0 {
						slipAmount = new(big.Int).Set(params.MaxSlip)
					}
					if slipAmount.Cmp(params.MinSlip) < 0 {
						slipAmount = new(big.Int).Set(params.MinSlip)
					}
				}
				slipi = slipAmount
			} else {
				slipi = big.NewInt(0)
			}

			var slipj *big.Int
			if etxj.EtxType() == types.ConversionType {
				// If no slip is mentioned it is set to 90%
				slipAmount := new(big.Int).Set(params.MaxSlip)
				if len(etxj.Data()) > 1 {
					slipAmount = new(big.Int).SetBytes(etxj.Data()[:2])
					if slipAmount.Cmp(params.MaxSlip) > 0 {
						slipAmount = new(big.Int).Set(params.MaxSlip)
					}
					if slipAmount.Cmp(params.MinSlip) < 0 {
						slipAmount = new(big.Int).Set(params.MinSlip)
					}
				}
				slipj = slipAmount
			} else {
				slipj = big.NewInt(0)
			}

			return slipi.Cmp(slipj) > 0
		})

		kQuaiDiscount := parent.KQuaiDiscount()

		originalEtxValues := make([]*big.Int, len(newInboundEtxs))
		actualConversionAmountInQuai := big.NewInt(0)
		realizedConversionAmountInQuai := big.NewInt(0)

		for i, etx := range newInboundEtxs {
			originalEtxValues[i] = new(big.Int).Set(etx.Value())
			// If the etx is conversion
			if types.IsConversionTx(etx) && etx.Value().Cmp(common.Big0) > 0 {
				value := new(big.Int).Set(etx.Value())
				originalValue := new(big.Int).Set(etx.Value())

				// Keeping a temporary actual conversion amount in quai so
				// that, in case the transaction gets reverted, doesnt effect the
				// actualConversionAmountInQuai
				var tempActualConversionAmountInQuai *big.Int
				if etx.To().IsInQuaiLedgerScope() {
					tempActualConversionAmountInQuai = new(big.Int).Add(actualConversionAmountInQuai, misc.QiToQuai(parent, parent.ExchangeRate(), parent.MinerDifficulty(), value))
				} else {
					tempActualConversionAmountInQuai = new(big.Int).Add(actualConversionAmountInQuai, value)
				}

				// If no slip is mentioned it is set to 90%, otherwise use
				// the slip specified in the transaction data with a max of
				// 90%
				slipAmount := new(big.Int).Set(params.MaxSlip)
				if len(etx.Data()) > 1 {
					slipAmount = new(big.Int).SetBytes(etx.Data()[:2])
					if slipAmount.Cmp(params.MaxSlip) > 0 {
						slipAmount = new(big.Int).Set(params.MaxSlip)
					}
					if slipAmount.Cmp(params.MinSlip) < 0 {
						slipAmount = new(big.Int).Set(params.MinSlip)
					}
				}

				// Apply the cubic discount based on the current tempActualConversionAmountInQuai
				discountedConversionAmount := misc.ApplyCubicDiscount(parent.ConversionFlowAmount(), tempActualConversionAmountInQuai)
				if parent.NumberU64(common.PRIME_CTX) > params.ConversionSlipChangeBlock {
					discountedConversionAmount = misc.ApplyCubicDiscount(tempActualConversionAmountInQuai, parent.ConversionFlowAmount())
				}
				discountedConversionAmountInInt, _ := discountedConversionAmount.Int(nil)

				// Conversions going against the direction of the exchange
				// rate adjustment dont get any k quai discount applied
				conversionAmountAfterKQuaiDiscount := new(big.Int).Mul(discountedConversionAmountInInt, new(big.Int).Sub(big.NewInt(params.KQuaiDiscountMultiplier), kQuaiDiscount))
				conversionAmountAfterKQuaiDiscount = new(big.Int).Div(conversionAmountAfterKQuaiDiscount, big.NewInt(params.KQuaiDiscountMultiplier))

				// Apply the conversion flow discount to all transactions
				value = new(big.Int).Mul(value, discountedConversionAmountInInt)
				value = new(big.Int).Div(value, tempActualConversionAmountInQuai)

				// ten percent original value is calculated so that in case
				// the slip is more than this it can be reset
				tenPercentOriginalValue := new(big.Int).Mul(originalValue, common.Big10)
				tenPercentOriginalValue = new(big.Int).Div(tenPercentOriginalValue, common.Big100)

				// amount after max slip is calculated based on the
				// specified slip so that if the final value exceeds this,
				// the transaction can be reverted
				amountAfterMaxSlip := new(big.Int).Mul(originalValue, new(big.Int).Sub(params.SlipAmountRange, slipAmount))
				amountAfterMaxSlip = new(big.Int).Div(amountAfterMaxSlip, params.SlipAmountRange)

				// If to is in Qi, convert the value into Qi
				if etx.To().IsInQiLedgerScope() {
					// Apply the k quai discount only if the exchange rate is increasing
					if exchangeRateIncreasing && discountedConversionAmountInInt.Cmp(common.Big0) != 0 {
						value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
						value = new(big.Int).Div(value, discountedConversionAmountInInt)
					}
				}
				// If To is in Quai, convert the value into Quai
				if etx.To().IsInQuaiLedgerScope() {
					// Apply the k quai discount only if the exchange rate is decreasing
					if !exchangeRateIncreasing && discountedConversionAmountInInt.Cmp(common.Big0) != 0 {
						value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
						value = new(big.Int).Div(value, discountedConversionAmountInInt)
					}
				}
				// If the value is less than the ten percent of the
				// original, reset it to 10%
				if value.Cmp(tenPercentOriginalValue) < 0 {
					value = new(big.Int).Set(tenPercentOriginalValue)
				}

				// If the value is less than the amount after the slip,
				// reset the value to zero, so that the transactions
				// that will get reverted dont add anything weight, to
				// the exchange rate calculation
				if value.Cmp(amountAfterMaxSlip) < 0 {
					etx.SetValue(big.NewInt(0))
				} else {
					actualConversionAmountInQuai = new(big.Int).Set(tempActualConversionAmountInQuai)
				}

			}
		}

		// Once the transactions are filtered, computed the total quai amount
		actualConversionAmountInQuai = misc.ComputeConversionAmountInQuai(parent, newInboundEtxs)

		// Once all the etxs are filtered, the calculation has to run again
		// to use the true actualConversionAmount and realizedConversionAmount
		// Apply the cubic discount based on the current tempActualConversionAmountInQuai
		discountedConversionAmount := misc.ApplyCubicDiscount(parent.ConversionFlowAmount(), actualConversionAmountInQuai)
		if parent.NumberU64(common.PRIME_CTX) > params.ConversionSlipChangeBlock {
			discountedConversionAmount = misc.ApplyCubicDiscount(actualConversionAmountInQuai, parent.ConversionFlowAmount())
		}
		discountedConversionAmountInInt, _ := discountedConversionAmount.Int(nil)

		// Conversions going against the direction of the exchange
		// rate adjustment dont get any k quai discount applied
		conversionAmountAfterKQuaiDiscount := new(big.Int).Mul(discountedConversionAmountInInt, new(big.Int).Sub(big.NewInt(params.KQuaiDiscountMultiplier), kQuaiDiscount))
		conversionAmountAfterKQuaiDiscount = new(big.Int).Div(conversionAmountAfterKQuaiDiscount, big.NewInt(params.KQuaiDiscountMultiplier))

		for i, etx := range newInboundEtxs {
			if etx.EtxType() == types.ConversionType && etx.Value().Cmp(common.Big0) > 0 {
				value := new(big.Int).Set(originalEtxValues[i])
				originalValue := new(big.Int).Set(value)

				// Apply the conversion flow discount to all transactions
				value = new(big.Int).Mul(value, discountedConversionAmountInInt)
				value = new(big.Int).Div(value, actualConversionAmountInQuai)

				// ten percent original value is calculated so that in case
				// the slip is more than this it can be reset
				tenPercentOriginalValue := new(big.Int).Mul(originalValue, common.Big10)
				tenPercentOriginalValue = new(big.Int).Div(tenPercentOriginalValue, common.Big100)

				// If to is in Qi, convert the value into Qi
				if etx.To().IsInQiLedgerScope() {
					// Apply the k quai discount only if the exchange rate is increasing
					if exchangeRateIncreasing && discountedConversionAmountInInt.Cmp(common.Big0) != 0 {
						value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
						value = new(big.Int).Div(value, discountedConversionAmountInInt)
					}

					// If the value is less than the ten percent of the
					// original, reset it to 10%
					if value.Cmp(tenPercentOriginalValue) < 0 {
						value = new(big.Int).Set(tenPercentOriginalValue)
					}

					realizedConversionAmountInQuai = new(big.Int).Add(realizedConversionAmountInQuai, value)
					value = misc.QuaiToQi(parent, parent.ExchangeRate(), parent.MinerDifficulty(), value)
				}
				// If To is in Quai, convert the value into Quai
				if etx.To().IsInQuaiLedgerScope() {
					// Apply the k quai discount only if the exchange rate is decreasing
					if !exchangeRateIncreasing && discountedConversionAmountInInt.Cmp(common.Big0) != 0 {
						value = new(big.Int).Mul(value, conversionAmountAfterKQuaiDiscount)
						value = new(big.Int).Div(value, discountedConversionAmountInInt)
					}

					// If the value is less than the ten percent of the
					// original, reset it to 10%
					if value.Cmp(tenPercentOriginalValue) < 0 {
						value = new(big.Int).Set(tenPercentOriginalValue)
					}

					value = misc.QiToQuai(parent, parent.ExchangeRate(), parent.MinerDifficulty(), value)
					realizedConversionAmountInQuai = new(big.Int).Add(realizedConversionAmountInQuai, value)
				}

				etx.SetValue(value)

			}
		}

		// compute and write the conversion flow amount based on the current block
		currentBlockConversionFlowAmount := sl.hc.ComputeConversionFlowAmount(parent, new(big.Int).Set(actualConversionAmountInQuai))
		if header.ConversionFlowAmount().Cmp(currentBlockConversionFlowAmount) != 0 {
			return fmt.Errorf("invalid conversion flow amount used (remote: %d local: %d)", header.ConversionFlowAmount(), currentBlockConversionFlowAmount)
		}

		//////// Step 3 /////////
		minerDifficulty := parent.MinerDifficulty()

		parentOfParent := sl.hc.GetBlockByHash(parent.ParentHash(common.PRIME_CTX))
		if parentOfParent == nil {
			return errors.New("parent of parent not found in verifyParentExchangeRateAndFlowAmount")
		}

		// calculate the token choice set and write it to the disk
		updatedTokenChoiceSet, err := CalculateTokenChoicesSet(sl.hc, parent, parentOfParent, parent.ExchangeRate(), newInboundEtxs, actualConversionAmountInQuai, realizedConversionAmountInQuai, minerDifficulty)
		if err != nil {
			return err
		}

		// ask the zone for this k quai, need to change the hash that is used to request this info
		storedExchangeRate, updateBit, err := sl.hc.GetKQuaiAndUpdateBit(parent.ParentHash(common.ZONE_CTX))
		if err != nil {
			return err
		}
		var exchangeRate *big.Int
		// update is paused, use the written exchange rate
		if parent.NumberU64(common.ZONE_CTX) <= params.BlocksPerYear && updateBit == 0 {
			exchangeRate = storedExchangeRate
		} else {
			exchangeRate, err = CalculateBetaFromMiningChoiceAndConversions(sl.hc, parent, parent.ExchangeRate(), updatedTokenChoiceSet)
			if err != nil {
				return err
			}
		}
		if header.ExchangeRate().Cmp(exchangeRate) != 0 {
			return fmt.Errorf("invalid exchange rate used (remote: %d local: %d)", header.ExchangeRate(), exchangeRate)
		}

		newkQuaiDiscount := sl.hc.ComputeKQuaiDiscount(parent, exchangeRate)
		if header.KQuaiDiscount().Cmp(newkQuaiDiscount) != 0 {
			return fmt.Errorf("invalid newKQuaiDiscount used (remote: %d local: %d)", header.KQuaiDiscount(), newkQuaiDiscount)
		}
	} else {
		return errors.New("verifyParentExchangeRate can only be called in prime context")
	}

	return nil
}

func (sl *Slice) GeneratePendingHeader(block *types.WorkObject, fill bool) (*types.WorkObject, error) {
	sl.hc.headermu.Lock()

	sl.logger.WithFields(log.Fields{
		"number": block.NumberArray(),
		"hash":   block.Hash(),
		"fill":   fill,
	}).Debug("GeneratePendingHeader")
	start := time.Now()

	// set the current header to this block
	err := sl.hc.SetCurrentHeader(block)
	if err != nil {
		sl.logger.WithFields(log.Fields{"hash": block.Hash(), "err": err}).Warn("Error setting current header")
		sl.recomputeRequired = true
		sl.hc.headermu.Unlock()
		return nil, err
	}
	stateProcessTime := time.Since(start)
	if sl.ReadBestPh() == nil {
		sl.hc.headermu.Unlock()
		return nil, errors.New("best ph is nil")
	}
	bestPhCopy := types.CopyWorkObject(sl.ReadBestPh())
	// If we are trying to recompute the pending header on the same parent block
	// we can return what we already have
	if !sl.recomputeRequired && bestPhCopy != nil && bestPhCopy.ParentHash(sl.NodeCtx()) == block.Hash() {
		sl.hc.headermu.Unlock()
		return bestPhCopy, nil
	}
	sl.recomputeRequired = false

	phStart := time.Now()
	pendingHeader, err := sl.miner.worker.GeneratePendingHeader(block, fill)
	if err != nil {
		sl.hc.headermu.Unlock()
		return nil, err
	}
	pendingHeaderCreationTime := time.Since(phStart)

	sl.hc.headermu.Unlock()
	if sl.NodeCtx() == common.ZONE_CTX {
		// Set the block processing times before sending the block in chain head
		// feed
		appendTime, exists := sl.appendTimeCache.Peek(block.Hash())
		if exists {
			block.SetAppendTime(appendTime)
		}
		block.SetStateProcessTime(stateProcessTime)
		block.SetPendingHeaderCreationTime(pendingHeaderCreationTime)

		sl.hc.chainHeadFeed.Send(ChainHeadEvent{block})
	}
	return pendingHeader, nil
}

func (sl *Slice) GetPendingBlockBody(powId types.PowID, sealHash common.Hash) *types.WorkObject {
	blockBody, _ := sl.miner.worker.GetPendingBlockBody(powId, sealHash)
	return blockBody
}

func (sl *Slice) SubscribeMissingBlockEvent(ch chan<- types.BlockRequest) event.Subscription {
	return sl.scope.Track(sl.missingBlockFeed.Subscribe(ch))
}

// SetSubClient sets the subClient for the given location
func (sl *Slice) SetSubInterface(subInterface CoreBackend, location common.Location) {
	switch sl.NodeCtx() {
	case common.PRIME_CTX:
		sl.subInterface[location.Region()] = subInterface
	case common.REGION_CTX:
		sl.subInterface[location.Zone()] = subInterface
	default:
		sl.logger.WithField("cannot set sub client in zone", location).Fatal("Invalid location")
	}
}

// loadLastState loads the phCache and the slice pending header hash from the db.
func (sl *Slice) loadLastState() error {
	if sl.ProcessingState() {
		sl.miner.worker.LoadPendingBlockBody()
	}
	bestPh := rawdb.ReadBestPendingHeader(sl.sliceDb)
	if bestPh != nil {
		sl.WriteBestPh(bestPh)
	}
	return nil
}

// Stop stores the phCache and the sl.pendingHeader hash value to the db.
func (sl *Slice) Stop() {
	nodeCtx := sl.NodeLocation().Context()

	var badHashes []common.Hash
	for hash := range sl.badHashesCache {
		badHashes = append(badHashes, hash)
	}
	rawdb.WriteBadHashesList(sl.sliceDb, badHashes)
	sl.miner.worker.StorePendingBlockBody()

	rawdb.WriteBestPendingHeader(sl.sliceDb, sl.ReadBestPh())

	sl.scope.Close()
	close(sl.quit)

	sl.hc.Stop()
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.asyncPhSub.Unsubscribe()
		sl.txPool.Stop()
	}
	sl.miner.Stop()
}

func (sl *Slice) Config() *params.ChainConfig { return sl.config }

func (sl *Slice) HeaderChain() *HeaderChain { return sl.hc }

func (sl *Slice) TxPool() *TxPool { return sl.txPool }

func (sl *Slice) Miner() *Miner { return sl.miner }

func (sl *Slice) CurrentInfo(header *types.WorkObject) bool {
	return sl.miner.worker.CurrentInfo(header)
}

func (sl *Slice) WriteBlock(block *types.WorkObject) {
	sl.hc.WriteBlock(block)
}

func (sl *Slice) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	if err := sl.hc.AddPendingEtxs(pEtxs); err != nil {
		return err
	}
	return nil
}

func (sl *Slice) AddPendingEtxsRollup(pEtxsRollup types.PendingEtxsRollup) error {
	if !pEtxsRollup.IsValid(trie.NewStackTrie(nil)) && !sl.hc.IsGenesisHash(pEtxsRollup.Header.Hash()) {
		sl.logger.Info("PendingEtxRollup is invalid")
		return ErrPendingEtxRollupNotValid
	}
	nodeCtx := sl.NodeLocation().Context()
	sl.logger.WithFields(log.Fields{
		"header": pEtxsRollup.Header.Hash(),
		"len":    len(pEtxsRollup.EtxsRollup),
	}).Info("Received pending ETXs Rollup")
	// Only write the pending ETXs if we have not seen them before
	if !sl.hc.pendingEtxsRollup.Contains(pEtxsRollup.Header.Hash()) {
		// Also write to cache for faster access
		sl.hc.pendingEtxsRollup.Add(pEtxsRollup.Header.Hash(), pEtxsRollup)
		// Write to pending ETX rollup database
		rawdb.WritePendingEtxsRollup(sl.sliceDb, pEtxsRollup)
		if nodeCtx == common.REGION_CTX {
			sl.domInterface.AddPendingEtxsRollup(pEtxsRollup)
		}
	}
	return nil
}

func (sl *Slice) CheckForBadHashAndRecover() {
	nodeCtx := sl.NodeLocation().Context()
	// Lookup the bad hashes list to see if we have it in the database
	for _, fork := range BadHashes {
		var badBlock *types.WorkObject
		var badHash common.Hash
		switch nodeCtx {
		case common.PRIME_CTX:
			badHash = fork.PrimeContext
		case common.REGION_CTX:
			badHash = fork.RegionContext[sl.NodeLocation().Region()]
		case common.ZONE_CTX:
			badHash = fork.ZoneContext[sl.NodeLocation().Region()][sl.NodeLocation().Zone()]
		}
		badBlock = sl.hc.GetBlockByHash(badHash)
		// Node has a bad block in the database
		if badBlock != nil {
			// Start from the current tip and delete every block from the database until this bad hash block
			sl.cleanCacheAndDatabaseTillBlock(badBlock.ParentHash(nodeCtx))
			if nodeCtx == common.PRIME_CTX {
				sl.SetHeadBackToRecoveryState(nil, badBlock.ParentHash(nodeCtx))
			}
		}
	}
}

// SetHeadBackToRecoveryState sets the heads of the whole hierarchy to the recovery state
func (sl *Slice) SetHeadBackToRecoveryState(pendingHeader *types.WorkObject, hash common.Hash) types.PendingHeader {
	nodeCtx := sl.NodeLocation().Context()
	if nodeCtx == common.PRIME_CTX {
		localPendingHeaderWithTermini := sl.ComputeRecoveryPendingHeader(hash)
		sl.GenerateRecoveryPendingHeader(localPendingHeaderWithTermini.WorkObject(), localPendingHeaderWithTermini.Termini())
		sl.SetBestPh(localPendingHeaderWithTermini.WorkObject())
	} else {
		localPendingHeaderWithTermini := sl.ComputeRecoveryPendingHeader(hash)
		localPendingHeaderWithTermini.SetHeader(sl.combinePendingHeader(localPendingHeaderWithTermini.WorkObject(), pendingHeader, nodeCtx, true))
		localPendingHeaderWithTermini.WorkObject().WorkObjectHeader().SetLocation(sl.NodeLocation())
		sl.SetBestPh(localPendingHeaderWithTermini.WorkObject())
		return localPendingHeaderWithTermini
	}
	return types.PendingHeader{}
}

// cleanCacheAndDatabaseTillBlock till delete all entries of header and other
// data structures around slice until the given block hash
func (sl *Slice) cleanCacheAndDatabaseTillBlock(hash common.Hash) {
	currentHeader := sl.hc.CurrentHeader()
	// If the hash is the current header hash, there is nothing to clean from the database
	if hash == currentHeader.Hash() {
		return
	}
	nodeCtx := sl.NodeCtx()
	// slice caches
	sl.miner.worker.pendingBlockBody.Purge()
	rawdb.DeleteBestPhKey(sl.sliceDb)
	// headerchain caches
	sl.hc.headerCache.Purge()
	sl.hc.numberCache.Purge()
	sl.hc.pendingEtxsRollup.Purge()
	sl.hc.pendingEtxs.Purge()
	rawdb.DeleteAllHeadsHashes(sl.sliceDb)
	// bodydb caches
	sl.hc.bc.blockCache.Purge()
	sl.hc.bc.bodyCache.Purge()
	sl.hc.bc.bodyProtoCache.Purge()

	var badHashes []common.Hash
	header := currentHeader
	for {
		rawdb.DeleteWorkObject(sl.sliceDb, header.Hash(), header.NumberU64(nodeCtx), types.BlockObject)
		rawdb.DeleteCanonicalHash(sl.sliceDb, header.NumberU64(nodeCtx))
		rawdb.DeleteHeaderNumber(sl.sliceDb, header.Hash())
		rawdb.DeleteTermini(sl.sliceDb, header.Hash())
		if nodeCtx != common.ZONE_CTX {
			rawdb.DeletePendingEtxs(sl.sliceDb, header.Hash())
			rawdb.DeletePendingEtxsRollup(sl.sliceDb, header.Hash())
		}
		// delete the trie node for a given root of the header
		rawdb.DeleteTrieNode(sl.sliceDb, header.EVMRoot())
		badHashes = append(badHashes, header.Hash())
		parent := sl.hc.GetHeaderByHash(header.ParentHash(nodeCtx))
		header = parent
		if header.Hash() == hash || sl.hc.IsGenesisHash(header.Hash()) {
			break
		}
	}

	sl.AddToBadHashesList(badHashes)
	// Set the current header
	currentHeader = sl.hc.GetHeaderByHash(hash)
	sl.hc.currentHeader.Store(currentHeader)
	rawdb.WriteHeadBlockHash(sl.sliceDb, currentHeader.Hash())

	// Recover the snaps
	if nodeCtx == common.ZONE_CTX && sl.ProcessingState() {
		sl.hc.bc.processor.snaps, _ = snapshot.New(sl.sliceDb, sl.hc.bc.processor.stateCache.TrieDB(), sl.hc.bc.processor.cacheConfig.SnapshotLimit, currentHeader.EVMRoot(), true, true, sl.logger)
	}
}

func (sl *Slice) GenerateRecoveryPendingHeader(pendingHeader *types.WorkObject, checkPointHashes types.Termini) error {
	nodeCtx := sl.NodeCtx()
	regions, zones := common.GetHierarchySizeForExpansionNumber(sl.hc.currentExpansionNumber)
	if nodeCtx == common.PRIME_CTX {
		for i := 0; i < int(regions); i++ {
			if sl.subInterface[i] != nil {
				sl.subInterface[i].GenerateRecoveryPendingHeader(pendingHeader, checkPointHashes)
			}
		}
	} else if nodeCtx == common.REGION_CTX {
		newPendingHeader := sl.SetHeadBackToRecoveryState(pendingHeader, checkPointHashes.SubTerminiAtIndex(sl.NodeLocation().Region()))
		for i := 0; i < int(zones); i++ {
			if sl.subInterface[i] != nil {
				sl.subInterface[i].GenerateRecoveryPendingHeader(newPendingHeader.WorkObject(), newPendingHeader.Termini())
			}
		}
	} else {
		sl.SetHeadBackToRecoveryState(pendingHeader, checkPointHashes.SubTerminiAtIndex(sl.NodeLocation().Zone()))
	}
	return nil
}

// ComputeRecoveryPendingHeader generates the pending header at a given hash
// and gets the termini from the database and returns the pending header with
// termini
func (sl *Slice) ComputeRecoveryPendingHeader(hash common.Hash) types.PendingHeader {
	block := sl.hc.GetBlockByHash(hash)
	pendingHeader, err := sl.miner.worker.GeneratePendingHeader(block, false)
	if err != nil {
		sl.logger.Error("Error generating pending header during the checkpoint recovery process")
		return types.PendingHeader{}
	}
	termini := sl.hc.GetTerminiByHash(hash)
	return types.NewPendingHeader(pendingHeader, *termini)
}

// AddToBadHashesList adds a given set of badHashes to the BadHashesList
func (sl *Slice) AddToBadHashesList(badHashes []common.Hash) {
	for _, hash := range badHashes {
		sl.badHashesCache[hash] = true
	}
}

// HashExistsInBadHashesList checks if the given hash exists in the badHashesCache
func (sl *Slice) HashExistsInBadHashesList(hash common.Hash) bool {
	// Look for pending ETXs first in pending ETX map, then in database
	_, ok := sl.badHashesCache[hash]
	return ok
}

// IsBlockHashABadHash checks if the given hash exists in BadHashes List
func (sl *Slice) IsBlockHashABadHash(hash common.Hash) bool {
	nodeCtx := sl.NodeCtx()
	switch nodeCtx {
	case common.PRIME_CTX:
		for _, fork := range BadHashes {
			if fork.PrimeContext == hash {
				return true
			}
		}
	case common.REGION_CTX:
		for _, fork := range BadHashes {
			if fork.RegionContext[sl.NodeLocation().Region()] == hash {
				return true
			}
		}
	case common.ZONE_CTX:
		for _, fork := range BadHashes {
			if fork.ZoneContext[sl.NodeLocation().Region()][sl.NodeLocation().Zone()] == hash {
				return true
			}
		}
	}
	return sl.HashExistsInBadHashesList(hash)
}

func (sl *Slice) NodeLocation() common.Location {
	return sl.hc.NodeLocation()
}

func (sl *Slice) NodeCtx() int {
	return sl.hc.NodeCtx()
}

func (sl *Slice) GetSlicesRunning() []common.Location {
	return sl.hc.SlicesRunning()
}

func (sl *Slice) asyncWorkShareUpdateLoop() {
	defer func() {
		if r := recover(); r != nil {
			sl.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	asyncTimer := time.NewTicker(c_asyncWorkShareTimer)
	defer asyncTimer.Stop()
	for {
		select {
		case <-asyncTimer.C:
			// Every time we read the broadcast set, get the next set of transactions and
			// update the phcache
			// Get the latest transactions to be broadcasted from the pool
			if len(sl.txPool.broadcastSet) > 0 {
				txsDirty := make(types.Transactions, len(sl.txPool.broadcastSet))
				copy(txsDirty, sl.txPool.broadcastSet)
				txs := make(types.Transactions, 0)
				for _, tx := range txsDirty {
					if tx != nil {
						txs = append(txs, tx)
					}
				}
				hash := types.DeriveSha(txs, trie.NewStackTrie(nil))
				bestPh := types.CopyWorkObject(sl.ReadBestPh())
				// Update the tx hash
				bestPh.WorkObjectHeader().SetTxHash(hash)
				sl.SetBestPh(bestPh)
				sl.txPool.broadcastSetCache.Add(hash, txs)
			}
		case <-sl.quit:
			return
		}
	}
}

func (sl *Slice) GetTxsFromBroadcastSet(hash common.Hash) (types.Transactions, error) {
	sl.txPool.broadcastSet = types.Transactions{}
	return sl.txPool.GetTxsFromBroadcastSet(hash)
}

func (sl *Slice) CalcOrder(header *types.WorkObject) (*big.Int, int, error) {
	return sl.hc.CalcOrder(header)
}

////// Expansion related logic

const (
	ExpansionNotTriggered = iota
	ExpansionTriggered
	ExpansionConfirmed
	ExpansionCompleted
)

func (sl *Slice) SetCurrentExpansionNumber(expansionNumber uint8) {
	sl.hc.SetCurrentExpansionNumber(expansionNumber)
}

func (sl *Slice) GetPrimeBlock(blockHash common.Hash) *types.WorkObject {
	switch sl.NodeCtx() {
	case common.PRIME_CTX:
		return sl.hc.GetBlockByHash(blockHash)
	case common.REGION_CTX:
		return sl.domInterface.GetPrimeBlock(blockHash)
	case common.ZONE_CTX:
		return sl.domInterface.GetPrimeBlock(blockHash)
	}
	return nil
}

// AddGenesisHash appends the given hash to the genesis hash list
func (sl *Slice) AddGenesisHash(hash common.Hash) {
	genesisHashes := rawdb.ReadGenesisHashes(sl.sliceDb)
	genesisHashes = append(genesisHashes, hash)

	// write the genesis hash to the database
	rawdb.WriteGenesisHashes(sl.sliceDb, genesisHashes)
}

// AddGenesisPendingEtxs adds the genesis pending etxs to the db
func (sl *Slice) AddGenesisPendingEtxs(block *types.WorkObject) {
	sl.hc.pendingEtxs.Add(block.Hash(), types.PendingEtxs{block, types.Transactions{}})
	rawdb.WritePendingEtxs(sl.sliceDb, types.PendingEtxs{block, types.Transactions{}})
}

// SubscribeExpansionEvent subscribes to the expansion feed
func (sl *Slice) SubscribeExpansionEvent(ch chan<- ExpansionEvent) event.Subscription {
	return sl.scope.Track(sl.expansionFeed.Subscribe(ch))
}

// This function returns bools for isBlock or isWorkShare.
// If this is a subWorkShare, it will not return an error, but isBlock and isWorkShare will be false.
// If an error is returned this means the workShare was invalid and/or did not meet the minimum p2p threshold.
func (sl *Slice) ReceiveWorkShare(workShare *types.WorkObjectHeader) (shareView *types.WorkObjectShareView, isBlock, isWorkShare bool, err error) {
	if workShare != nil {
		// If the workshares are from sha or scrypt, we have to validate them separately
		var isWorkShare, isSubShare bool
		if workShare.AuxPow() != nil {
			if workShare.AuxPow().PowID() == types.SHA_BCH ||
				workShare.AuxPow().PowID() == types.SHA_BTC ||
				workShare.AuxPow().PowID() == types.Scrypt {

				var workShareTarget *big.Int
				if workShare.AuxPow().PowID() == types.Scrypt {
					workShareTarget = new(big.Int).Div(common.Big2e256, workShare.ScryptDiffAndCount().Difficulty())
				} else {
					workShareTarget = new(big.Int).Div(common.Big2e256, workShare.ShaDiffAndCount().Difficulty())
				}
				powHash := workShare.AuxPow().Header().PowHash()
				powHashBigInt := new(big.Int).SetBytes(powHash.Bytes())

				// Check if satisfies workShareTarget
				if powHashBigInt.Cmp(workShareTarget) < 0 {
					wo := types.NewWorkObject(workShare, nil, nil)
					shareView := wo.ConvertToWorkObjectShareView([]*types.Transaction{})
					return shareView, false, true, nil
				} else {
					return nil, false, false, errors.New("workshare does not satisfy the target")
				}
			}
		}

		engine := sl.GetEngineForHeader(workShare)
		if engine == nil {
			return nil, false, false, errors.New("could not get engine for workshare")
		}

		isSubShare = sl.hc.CheckWorkThreshold(workShare, params.WorkShareP2PThresholdDiff)
		if !isSubShare {
			// This cannot be a block or a workshare or even a subWorkShare since it didn't pass the minimum workShare threshold.
			return nil, false, false, errors.New("workshare has less entropy than the workshare p2p threshold")
		}
		sl.logger.WithField("number", workShare.NumberU64()).Info("Received Work Share")

		// Now check if this subWorkShare is a full block.
		var isBlock bool
		_, err := sl.hc.VerifySeal(workShare)
		if err == nil {
			isBlock = true
		}

		txs, err := sl.GetTxsFromBroadcastSet(workShare.TxHash())
		if err != nil {
			txs = types.Transactions{}
			if workShare.TxHash() != types.EmptyRootHash {
				sl.logger.Warn("Failed to get txs from the broadcastSetCache", "err", err)
			}
		}
		// If the share qualifies is not a workshare and there are no transactions,
		// there is no need to broadcast the share
		isWorkShare = sl.hc.CheckIfValidWorkShare(workShare) == types.Valid
		if !isWorkShare && len(txs) == 0 {
			// This is a p2p workshare and has no transactions.
			return nil, false, false, nil
		}
		var wo *types.WorkObject
		// If progpow, since there is already a check in the p2p validation, to
		// keep backwards compatibility
		if workShare.PrimeTerminusNumber().Uint64() < params.KawPowForkBlock {
			pendingBlockBody := sl.GetPendingBlockBody(types.Progpow, workShare.SealHash())
			if pendingBlockBody == nil {
				return nil, false, false, errors.New("could not find pending block body for progpow workshare")
			}
			wo = types.NewWorkObject(workShare, pendingBlockBody.Body(), nil)
		} else {
			wo = types.NewWorkObject(workShare, nil, nil)
		}
		shareView := wo.ConvertToWorkObjectShareView(txs)
		return shareView, isBlock, isWorkShare, nil

	}
	return nil, false, false, errors.New("workshare is nil")
}

func (sl *Slice) ReceiveMinedHeader(woHeader *types.WorkObject) (*types.WorkObject, error) {

	block, err := sl.ConstructLocalMinedBlock(woHeader)
	if err != nil && err.Error() == ErrBadSubManifest.Error() && sl.NodeLocation().Context() < common.ZONE_CTX {
		sl.logger.Info("filling sub manifest")
		// If we just mined this block, and we have a subordinate chain, its possible
		// the subordinate manifest in our block body is incorrect. If so, ask our sub
		// for the correct manifest and reconstruct the block.
		var err error
		block, err = sl.fillSubordinateManifest(block)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	// Set the Auxpow
	block.WorkObjectHeader().SetAuxPow(woHeader.AuxPow())

	// Get the order of the block
	_, order, err := sl.CalcOrder(block)
	if err != nil {
		return nil, err
	}

	// If its a dom block, call the ReceiveMinedHeader on the dom interface
	if order < sl.NodeCtx() {
		sl.domInterface.ReceiveMinedHeader(types.CopyWorkObject(block))
	}

	sl.logger.WithFields(log.Fields{
		"number":   block.Number(sl.NodeCtx()),
		"location": block.Location(),
		"hash":     block.Hash(),
	}).Info("Received mined header")

	return block, nil
}

func (sl *Slice) fillSubordinateManifest(workObject *types.WorkObject) (*types.WorkObject, error) {
	nodeCtx := sl.NodeCtx()
	if workObject.ManifestHash(nodeCtx+1) == types.EmptyRootHash {
		return nil, errors.New("cannot fill empty subordinate manifest")
	} else if subManifestHash := types.DeriveSha(workObject.Manifest(), trie.NewStackTrie(nil)); subManifestHash == workObject.ManifestHash(nodeCtx+1) {
		// If the manifest hashes match, nothing to do
		return workObject, nil
	} else {
		subParentHash := workObject.ParentHash(nodeCtx + 1)
		var subManifest types.BlockManifest
		if subParent := sl.hc.GetBlockByHash(subParentHash); subParent != nil {
			// If we have the the subordinate parent in our chain, that means that block
			// was also coincident. In this case, the subordinate manifest resets, and
			// only consists of the subordinate parent hash.
			subManifest = types.BlockManifest{subParentHash}
		} else {
			// Otherwise we need to reconstruct the sub manifest, by getting the
			// parent's sub manifest and appending the parent hash.
			var err error
			subManifest, err = sl.GetSubManifest(workObject.Location(), subParentHash)
			if err != nil {
				return nil, err
			}
		}
		if len(subManifest) == 0 {
			return nil, errors.New("reconstructed sub manifest is empty")
		}
		if subManifest == nil || workObject.ManifestHash(nodeCtx+1) != types.DeriveSha(subManifest, trie.NewStackTrie(nil)) {
			return nil, errors.New("reconstructed sub manifest does not match manifest hash")
		}
		return types.NewWorkObjectWithHeaderAndTx(workObject.WorkObjectHeader(), workObject.Tx()).WithBody(workObject.Header(), workObject.Transactions(), workObject.OutboundEtxs(), workObject.Uncles(), subManifest, workObject.InterlinkHashes()), nil
	}
}
