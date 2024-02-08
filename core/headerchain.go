package core

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru"
)

const (
	headerCacheLimit      = 512
	numberCacheLimit      = 2048
	c_subRollupCacheSize  = 50
	primeHorizonThreshold = 20
)

// getPendingEtxsRollup gets the pendingEtxsRollup rollup from appropriate Region
type getPendingEtxsRollup func(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxsRollup, error)

// getPendingEtxs gets the pendingEtxs from the appropriate Zone
type getPendingEtxs func(blockHash common.Hash, hash common.Hash, location common.Location) (types.PendingEtxs, error)

type HeaderChain struct {
	config *params.ChainConfig

	bc     *BodyDb
	engine consensus.Engine
	pool   *TxPool

	chainHeadFeed event.Feed
	chainSideFeed event.Feed
	scope         event.SubscriptionScope

	headerDb      ethdb.Database
	genesisHeader *types.Header

	currentHeader      atomic.Value // Current head of the header chain (may be above the block chain!)
	currentStateHeader atomic.Value // Current head of the header chain (may be different than currentHeader)
	headerCache        *lru.Cache   // Cache for the most recent block headers
	numberCache        *lru.Cache   // Cache for the most recent block numbers

	fetchPEtxRollup getPendingEtxsRollup
	fetchPEtx       getPendingEtxs

	pendingEtxsRollup *lru.Cache
	pendingEtxs       *lru.Cache
	blooms            *lru.Cache
	subRollupCache    *lru.Cache

	wg            sync.WaitGroup // chain processing wait group for shutting down
	running       int32          // 0 if chain is running, 1 when stopped
	procInterrupt int32          // interrupt signaler for block processing

	headermu      sync.RWMutex
	heads         []*types.Header
	slicesRunning []common.Location

	logger *log.Logger
}

// NewHeaderChain creates a new HeaderChain structure. ProcInterrupt points
// to the parent's interrupt semaphore.
func NewHeaderChain(db ethdb.Database, engine consensus.Engine, pEtxsRollupFetcher getPendingEtxsRollup, pEtxsFetcher getPendingEtxs, chainConfig *params.ChainConfig, cacheConfig *CacheConfig, txLookupLimit *uint64, vmConfig vm.Config, slicesRunning []common.Location, logger *log.Logger) (*HeaderChain, error) {
	headerCache, _ := lru.New(headerCacheLimit)
	numberCache, _ := lru.New(numberCacheLimit)
	nodeCtx := chainConfig.Location.Context()

	hc := &HeaderChain{
		config:          chainConfig,
		headerDb:        db,
		headerCache:     headerCache,
		numberCache:     numberCache,
		engine:          engine,
		slicesRunning:   slicesRunning,
		fetchPEtxRollup: pEtxsRollupFetcher,
		fetchPEtx:       pEtxsFetcher,
		logger:          logger,
	}

	pendingEtxsRollup, _ := lru.New(c_maxPendingEtxsRollup)
	hc.pendingEtxsRollup = pendingEtxsRollup

	if nodeCtx == common.PRIME_CTX {
		hc.pendingEtxs, _ = lru.New(c_maxPendingEtxBatchesPrime)
	} else {
		hc.pendingEtxs, _ = lru.New(c_maxPendingEtxBatchesRegion)
	}

	blooms, _ := lru.New(c_maxBloomFilters)
	hc.blooms = blooms

	subRollupCache, _ := lru.New(c_subRollupCacheSize)
	hc.subRollupCache = subRollupCache

	hc.genesisHeader = hc.GetHeaderByNumber(0)
	if hc.genesisHeader.Hash() != chainConfig.GenesisHash {
		return nil, fmt.Errorf("genesis block mismatch: have %x, want %x", hc.genesisHeader.Hash(), chainConfig.GenesisHash)
	}
	hc.logger.WithField("Hash", hc.genesisHeader.Hash()).Info("Genesis")
	if hc.genesisHeader == nil {
		return nil, ErrNoGenesis
	}
	//Load any state that is in our db
	if err := hc.loadLastState(); err != nil {
		return nil, err
	}

	var err error
	hc.bc, err = NewBodyDb(db, engine, hc, chainConfig, cacheConfig, txLookupLimit, vmConfig, slicesRunning)
	if err != nil {
		return nil, err
	}

	// Initialize the heads slice
	heads := make([]*types.Header, 0)
	hc.heads = heads

	return hc, nil
}

// CollectSubRollup collects the rollup of ETXs emitted from the subordinate
// chain in the slice which emitted the given block.
func (hc *HeaderChain) CollectSubRollup(b *types.Block) (types.Transactions, error) {
	nodeCtx := hc.NodeCtx()
	subRollup := types.Transactions{}
	if nodeCtx < common.ZONE_CTX {
		// Since in prime the pending etxs are stored in 2 parts, pendingEtxsRollup
		// consists of region header and subrollups
		for _, hash := range b.SubManifest() {
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
					hc.fetchPEtx(b.Hash(), hash, b.Header().Location())
					return nil, ErrPendingEtxNotFound
				}
				subRollup = append(subRollup, pendingEtxs.Etxs...)

			}
		}
		// Rolluphash is specifically for zone rollup, which can only be validated by region
		if nodeCtx == common.REGION_CTX {
			if subRollupHash := types.DeriveSha(subRollup, trie.NewStackTrie(nil)); subRollupHash != b.EtxRollupHash() {
				return nil, errors.New("sub rollup does not match sub rollup hash")
			}
		}
	}
	return subRollup, nil
}

// GetPendingEtxs gets the pendingEtxs form the
func (hc *HeaderChain) GetPendingEtxs(hash common.Hash) (*types.PendingEtxs, error) {
	var pendingEtxs types.PendingEtxs
	// Look for pending ETXs first in pending ETX cache, then in database
	if res, ok := hc.pendingEtxs.Get(hash); ok && res != nil {
		pendingEtxs = res.(types.PendingEtxs)
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
	if res, ok := hc.pendingEtxsRollup.Get(hash); ok && res != nil {
		rollups = res.(types.PendingEtxsRollup)
	} else if res := rawdb.ReadPendingEtxsRollup(hc.headerDb, hash, location); res != nil {
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
	if res, ok := hc.blooms.Get(hash); ok && res != nil {
		bloom = res.(types.Bloom)
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
func (hc *HeaderChain) CollectEtxRollup(b *types.Block) (types.Transactions, error) {
	if b.NumberU64(hc.NodeCtx()) == 0 && b.Hash() == hc.config.GenesisHash {
		return b.ExtTransactions(), nil
	}
	parent := hc.GetBlock(b.ParentHash(hc.NodeCtx()), b.NumberU64(hc.NodeCtx())-1)
	if parent == nil {
		return nil, errors.New("parent not found")
	}
	return hc.collectInclusiveEtxRollup(parent)
}

func (hc *HeaderChain) collectInclusiveEtxRollup(b *types.Block) (types.Transactions, error) {
	// Initialize the rollup with ETXs emitted by this block
	newEtxs := b.ExtTransactions()
	// Terminate the search if we reached genesis
	if b.NumberU64(hc.NodeCtx()) == 0 {
		if b.Hash() != hc.config.GenesisHash {
			return nil, fmt.Errorf("manifest builds on incorrect genesis, block0 hash: %s", b.Hash().String())
		} else {
			return newEtxs, nil
		}
	}
	// Terminate the search on coincidence with dom chain
	if hc.engine.IsDomCoincident(hc, b.Header()) {
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

// Append
func (hc *HeaderChain) AppendHeader(header *types.Header) error {
	nodeCtx := hc.NodeCtx()
	hc.logger.WithFields(log.Fields{
		"Hash":     header.Hash(),
		"Number":   header.NumberArray(),
		"Location": header.Location,
		"Parent":   header.ParentHash(nodeCtx),
	}).Debug("Headerchain Append")

	err := hc.engine.VerifyHeader(hc, header)
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

	return nil
}
func (hc *HeaderChain) ProcessingState() bool {
	return hc.bc.ProcessingState()
}

// Append
func (hc *HeaderChain) AppendBlock(block *types.Block, newInboundEtxs types.Transactions) error {
	blockappend := time.Now()
	// Append block else revert header append
	logs, err := hc.bc.Append(block, newInboundEtxs)
	if err != nil {
		return err
	}
	hc.logger.WithField("append block", common.PrettyDuration(time.Since(blockappend))).Debug("Time taken to")

	hc.bc.chainFeed.Send(ChainEvent{Block: block, Hash: block.Hash(), Logs: logs})
	if len(logs) > 0 {
		hc.bc.logsFeed.Send(logs)
	}

	return nil
}

// SetCurrentHeader sets the current header based on the POEM choice
func (hc *HeaderChain) SetCurrentHeader(head *types.Header) error {
	hc.headermu.Lock()
	defer hc.headermu.Unlock()

	prevHeader := hc.CurrentHeader()
	// if trying to set the same header, escape
	if prevHeader.Hash() == head.Hash() {
		return nil
	}

	// write the head block hash to the db
	rawdb.WriteHeadBlockHash(hc.headerDb, head.Hash())
	hc.logger.WithFields(log.Fields{
		"Hash":   head.Hash(),
		"Number": head.NumberArray(),
	}).Info("Setting the current header")
	hc.currentHeader.Store(head)

	// If head is the normal extension of canonical head, we can return by just wiring the canonical hash.
	if prevHeader.Hash() == head.ParentHash(hc.NodeCtx()) {
		rawdb.WriteCanonicalHash(hc.headerDb, head.Hash(), head.NumberU64(hc.NodeCtx()))
		return nil
	}

	//Find a common header
	commonHeader := hc.findCommonAncestor(head)
	newHeader := types.CopyHeader(head)

	// Delete each header and rollback state processor until common header
	// Accumulate the hash slice stack
	var hashStack []*types.Header
	for {
		if newHeader.Hash() == commonHeader.Hash() {
			break
		}
		hashStack = append(hashStack, newHeader)
		newHeader = hc.GetHeader(newHeader.ParentHash(hc.NodeCtx()), newHeader.NumberU64(hc.NodeCtx())-1)
		if newHeader == nil {
			return ErrSubNotSyncedToDom
		}
		// genesis check to not delete the genesis block
		if newHeader.Hash() == hc.config.GenesisHash {
			break
		}
	}
	var prevHashStack []*types.Header
	for {
		if prevHeader.Hash() == commonHeader.Hash() {
			break
		}
		prevHashStack = append(prevHashStack, prevHeader)
		rawdb.DeleteCanonicalHash(hc.headerDb, prevHeader.NumberU64(hc.NodeCtx()))
		prevHeader = hc.GetHeader(prevHeader.ParentHash(hc.NodeCtx()), prevHeader.NumberU64(hc.NodeCtx())-1)
		if prevHeader == nil {
			return errors.New("Could not find previously canonical header during reorg")
		}
		// genesis check to not delete the genesis block
		if prevHeader.Hash() == hc.config.GenesisHash {
			break
		}
	}

	// Run through the hash stack to update canonicalHash and forward state processor
	for i := len(hashStack) - 1; i >= 0; i-- {
		rawdb.WriteCanonicalHash(hc.headerDb, hashStack[i].Hash(), hashStack[i].NumberU64(hc.NodeCtx()))
	}

	if hc.NodeCtx() == common.ZONE_CTX && hc.ProcessingState() {
		// Every Block that got removed from the canonical hash db is sent in the side feed to be
		// recorded as uncles
		go func() {
			var blocks []*types.Block
			for i := len(prevHashStack) - 1; i >= 0; i-- {
				block := hc.bc.GetBlock(prevHashStack[i].Hash(), prevHashStack[i].NumberU64(hc.NodeCtx()))
				if block != nil {
					blocks = append(blocks, block)
				}
			}
			hc.chainSideFeed.Send(ChainSideEvent{Blocks: blocks, ResetUncles: true})
		}()
	}

	return nil
}

// SetCurrentState updates the current Quai state and Qi UTXO set upon which the current pending block is built
func (hc *HeaderChain) SetCurrentState(head *types.Header) error {
	hc.headermu.Lock()
	defer hc.headermu.Unlock()

	nodeCtx := hc.NodeCtx()
	if nodeCtx != common.ZONE_CTX || !hc.ProcessingState() {
		return nil
	}

	if err := hc.setCurrentUTXOSet(head); err != nil {
		return err
	}

	current := types.CopyHeader(head)
	var headersWithoutState []*types.Header
	for {
		headersWithoutState = append(headersWithoutState, current)
		header := hc.GetHeader(current.ParentHash(nodeCtx), current.NumberU64(nodeCtx)-1)
		if header == nil {
			return ErrSubNotSyncedToDom
		}
		// Checking of the Etx set exists makes sure that we have processed the
		// state of the parent block
		etxSet := rawdb.ReadEtxSet(hc.headerDb, header.Hash(), header.NumberU64(nodeCtx), hc.NodeLocation())
		if etxSet != nil {
			break
		}
		current = types.CopyHeader(header)
	}

	// Run through the hash stack to update canonicalHash and forward state processor
	for i := len(headersWithoutState) - 1; i >= 0; i-- {
		err := hc.ReadInboundEtxsAndAppendBlock(headersWithoutState[i])
		if err != nil {
			return err
		}
	}
	return nil
}

// This function performs all of the logic to update and/or re-org the UTXO set with the given header.
// Currently, the UTXO set follows the current state/pending header, not the canonical current header.
// An improvement could be to use a batch instead of the db in case of an error during re-org requiring a reset.
// This function MUST be called with the header mutex locked.
func (hc *HeaderChain) setCurrentUTXOSet(head *types.Header) error {
	nodeCtx := hc.NodeCtx()
	prevHeader := hc.CurrentStateHeader()
	// if trying to set the same header, escape
	if prevHeader.Hash() == head.Hash() {
		return nil
	}

	// If head is the normal extension of current state head, we can return by just updating the UTXO set.
	if prevHeader.Hash() == head.ParentHash(nodeCtx) {
		// Set up UTXO processing
		block := hc.GetBlockByHash(head.Hash())
		utxoView := types.NewUtxoViewpoint(hc.NodeLocation())
		stxos := make([]types.SpentTxOut, 0, types.CountSpentOutputs(block))
		err := hc.fetchInputUtxos(utxoView, block)
		if err != nil {
			return err
		}

		totalFees, err := hc.verifyInputUtxos(utxoView, block, hc.pool.signer)
		if err != nil {
			return err
		}
		for _, tx := range block.QiTransactions() {
			// Add all of the outputs for this transaction which are not
			// provably unspendable as available utxos.  Also, the passed
			// spent txos slice is updated to contain an entry for each
			// spent txout in the order each transaction spends them.
			err = utxoView.ConnectTransaction(tx, block.Header(), &stxos)
			if err != nil {
				return fmt.Errorf("could not apply tx %v: %w", tx.Hash().Hex(), err)
			}
		}
		if types.IsCoinBaseTx(block.QiTransactions()[0]) {
			totalCoinbaseOut := big.NewInt(0)
			for _, txOut := range block.QiTransactions()[0].TxOut() {
				totalCoinbaseOut.Add(totalCoinbaseOut, types.Denominations[txOut.Denomination])
			}
			maxCoinbaseOut := misc.CalculateRewardForQiWithFeesBigInt(block.Header(), totalFees)
			if totalCoinbaseOut.Cmp(maxCoinbaseOut) == 1 {
				return fmt.Errorf("coinbase output value of %v is higher than expected value of %v", totalCoinbaseOut, maxCoinbaseOut)
			}
		}
		// Write the updated utxo set and spent utxos to the database
		hc.WriteUtxoViewpoint(utxoView)
		rawdb.WriteSpentUTXOs(hc.bc.db, block.Hash(), &stxos)
		// Make sure to update the current state header
		rawdb.WriteCurrentStateHeaderHashByNumber(hc.headerDb, block.Hash(), block.NumberU64(nodeCtx))
		hc.currentStateHeader.Store(head)
		return nil
	}

	//Find a common header
	commonHeader := hc.findCommonStateHeadAncestor(head)
	newBlock := hc.GetBlockByHash(head.Hash())
	if newBlock == nil {
		return errors.New("Could not find block during reorg")
	}
	// Accumulate the new chain from the new head to the common header
	var newChain []*types.Block
	for {
		if newBlock.Hash() == commonHeader.Hash() {
			break
		}
		newChain = append(newChain, newBlock)
		newBlock = hc.GetBlock(newBlock.ParentHash(nodeCtx), newBlock.NumberU64(nodeCtx)-1)
		if newBlock == nil {
			return ErrSubNotSyncedToDom
		}
		// genesis check to not delete the genesis block
		if newBlock.Hash() == hc.config.GenesisHash {
			break
		}
	}

	// Delete each old header and rollback utxo set until common header
	for {
		if prevHeader.Hash() == commonHeader.Hash() {
			break
		}
		rawdb.DeleteCurrentStateHeaderHashByNumber(hc.headerDb, prevHeader.NumberU64(nodeCtx))
		// Unspend previously spent UTXOs and spend created UTXOs for this block
		hc.DeleteUtxoViewpoint(prevHeader.Hash())
		prevHeader = hc.GetHeader(prevHeader.ParentHash(nodeCtx), prevHeader.NumberU64(nodeCtx)-1)
		if prevHeader == nil {
			return errors.New("Could not find previously canonical header during reorg")
		}
		// genesis check to not delete the genesis block
		if prevHeader.Hash() == hc.config.GenesisHash {
			break
		}
	}

	// Run through the hash stack in order to update utxo set
	for _, block := range newChain {
		// Set up UTXO processing
		utxoView := types.NewUtxoViewpoint(hc.NodeLocation())
		stxos := make([]types.SpentTxOut, 0, types.CountSpentOutputs(block))
		err := hc.fetchInputUtxos(utxoView, block)
		if err != nil {
			return err
		}

		totalFees, err := hc.verifyInputUtxos(utxoView, block, hc.pool.signer)
		if err != nil {
			return err
		}
		for _, tx := range block.QiTransactions() {
			// Add all of the outputs for this transaction which are not
			// provably unspendable as available utxos.  Also, the passed
			// spent txos slice is updated to contain an entry for each
			// spent txout in the order each transaction spends them.
			err = utxoView.ConnectTransaction(tx, block.Header(), &stxos)
			if err != nil {
				return fmt.Errorf("could not apply tx %v: %w", tx.Hash().Hex(), err)
			}
		}
		if types.IsCoinBaseTx(block.QiTransactions()[0]) {
			totalCoinbaseOut := big.NewInt(0)
			for _, txOut := range block.QiTransactions()[0].TxOut() {
				// Should we limit the number of outputs?
				totalCoinbaseOut.Add(totalCoinbaseOut, types.Denominations[txOut.Denomination])
			}
			maxCoinbaseOut := misc.CalculateRewardForQiWithFeesBigInt(block.Header(), totalFees)
			if totalCoinbaseOut.Cmp(maxCoinbaseOut) == 1 {
				return fmt.Errorf("coinbase output value of %v is higher than expected value of %v", totalCoinbaseOut, maxCoinbaseOut)
			}
		}
		// Write the updated utxo set and spent utxos to the database
		hc.WriteUtxoViewpoint(utxoView)
		rawdb.WriteSpentUTXOs(hc.bc.db, block.Hash(), &stxos)
		rawdb.WriteCurrentStateHeaderHashByNumber(hc.headerDb, block.Hash(), block.NumberU64(nodeCtx))
	}

	// Reorg complete, update the current state header
	hc.currentStateHeader.Store(head)
	return nil
}

// ReadInboundEtxsAndAppendBlock reads the inbound etxs from database and appends the block
func (hc *HeaderChain) ReadInboundEtxsAndAppendBlock(header *types.Header) error {
	nodeCtx := hc.NodeCtx()
	block := hc.GetBlockOrCandidate(header.Hash(), header.NumberU64(nodeCtx))
	if block == nil {
		return errors.New("Could not find block during reorg")
	}
	_, order, err := hc.engine.CalcOrder(block.Header())
	if err != nil {
		return err
	}
	var inboundEtxs types.Transactions
	if order < nodeCtx {
		inboundEtxs = rawdb.ReadInboundEtxs(hc.headerDb, header.Hash(), hc.NodeLocation())
	}
	return hc.AppendBlock(block, inboundEtxs)
}

// findCommonAncestor
func (hc *HeaderChain) findCommonAncestor(header *types.Header) *types.Header {
	current := types.CopyHeader(header)
	for {
		if current == nil {
			return nil
		}
		canonicalHash := rawdb.ReadCanonicalHash(hc.headerDb, current.NumberU64(hc.NodeCtx()))
		if canonicalHash == current.Hash() {
			return hc.GetHeaderByHash(canonicalHash)
		}
		current = hc.GetHeader(current.ParentHash(hc.NodeCtx()), current.NumberU64(hc.NodeCtx())-1)
	}

}

// findCommonAncestor
func (hc *HeaderChain) findCommonStateHeadAncestor(header *types.Header) *types.Header {
	current := types.CopyHeader(header)
	for {
		if current == nil {
			return nil
		}
		currentStateHeaderHash := rawdb.ReadCurrentStateHeaderHashByNumber(hc.headerDb, current.NumberU64(hc.NodeCtx()))
		if currentStateHeaderHash == current.Hash() {
			return hc.GetHeaderByHash(currentStateHeaderHash)
		}
		current = hc.GetHeader(current.ParentHash(hc.NodeCtx()), current.NumberU64(hc.NodeCtx())-1)
	}

}

func (hc *HeaderChain) AddPendingEtxs(pEtxs types.PendingEtxs) error {
	if !pEtxs.IsValid(trie.NewStackTrie(nil)) {
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
			if hc.currentStateHeader.Load() == nil {
				hc.currentStateHeader.Store(hc.genesisHeader) // this may cause an issue (not consistent with ph cache)
			}
		}
	} else {
		// Recover the current header
		hc.logger.Warn("Recovering Current Header")
		recoveredHeader := hc.RecoverCurrentHeader()
		rawdb.WriteHeadBlockHash(hc.headerDb, recoveredHeader.Hash())
		hc.currentHeader.Store(recoveredHeader)
		if hc.currentStateHeader.Load() == nil {
			hc.currentStateHeader.Store(recoveredHeader) // this may cause an issue (not consistent with ph cache)
		}
	}

	heads := make([]*types.Header, 0)
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

// Empty checks if the headerchain is empty.
func (hc *HeaderChain) Empty() bool {
	genesis := hc.config.GenesisHash
	for _, hash := range []common.Hash{rawdb.ReadHeadBlockHash(hc.headerDb)} {
		if hash != genesis {
			return false
		}
	}
	return true
}

// GetBlockNumber retrieves the block number belonging to the given hash
// from the cache or database
func (hc *HeaderChain) GetBlockNumber(hash common.Hash) *uint64 {
	if cached, ok := hc.numberCache.Get(hash); ok {
		number := cached.(uint64)
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
		if header = hc.GetHeader(next, header.NumberU64(hc.NodeCtx())-1); header == nil {
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
		if header := hc.GetHeader(hash, number); header != nil {
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
		header := hc.GetHeader(hash, number)
		if header == nil {
			return common.Hash{}, 0
		}
		hash = header.ParentHash(hc.NodeCtx())
		number--
	}
	return hash, number
}

func (hc *HeaderChain) WriteBlock(block *types.Block) {
	hc.bc.WriteBlock(block)
}

// GetHeader retrieves a block header from the database by hash and number,
// caching it if found.
func (hc *HeaderChain) GetHeader(hash common.Hash, number uint64) *types.Header {
	termini := hc.GetTerminiByHash(hash)
	if termini == nil {
		return nil
	}
	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return header.(*types.Header)
	}
	header := rawdb.ReadHeader(hc.headerDb, hash, number)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, header)
	return header
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if
// found.
func (hc *HeaderChain) GetHeaderByHash(hash common.Hash) *types.Header {
	termini := hc.GetTerminiByHash(hash)
	if termini == nil {
		return nil
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}

	return hc.GetHeader(hash, *number)
}

// GetHeaderOrCandidate retrieves a block header from the database by hash and number,
// caching it if found.
func (hc *HeaderChain) GetHeaderOrCandidate(hash common.Hash, number uint64) *types.Header {
	// Short circuit if the header's already in the cache, retrieve otherwise
	if header, ok := hc.headerCache.Get(hash); ok {
		return header.(*types.Header)
	}
	header := rawdb.ReadHeader(hc.headerDb, hash, number)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, header)
	return header
}

// RecoverCurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache
func (hc *HeaderChain) RecoverCurrentHeader() *types.Header {
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

// GetHeaderOrCandidateByHash retrieves a block header from the database by hash, caching it if
// found.
func (hc *HeaderChain) GetHeaderOrCandidateByHash(hash common.Hash) *types.Header {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}

	return hc.GetHeaderOrCandidate(hash, *number)
}

// HasHeader checks if a block header is present in the database or not.
// In theory, if header is present in the database, all relative components
// like td and hash->number should be present too.
func (hc *HeaderChain) HasHeader(hash common.Hash, number uint64) bool {
	if hc.numberCache.Contains(hash) || hc.headerCache.Contains(hash) {
		return true
	}
	return rawdb.HasHeader(hc.headerDb, hash, number)
}

// GetHeaderByNumber retrieves a block header from the database by number,
// caching it (associated with its hash) if found.
func (hc *HeaderChain) GetHeaderByNumber(number uint64) *types.Header {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetHeader(hash, number)
}

func (hc *HeaderChain) GetCanonicalHash(number uint64) common.Hash {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	return hash
}

// CurrentHeader retrieves the current head header of the canonical chain. The
// header is retrieved from the HeaderChain's internal cache.
func (hc *HeaderChain) CurrentHeader() *types.Header {
	return hc.currentHeader.Load().(*types.Header)
}

// CurrentStateHeader retrieves the current state header of the chain that the pending block is based upon.
// The header is retrieved from the HeaderChain's internal cache.
// This is cached only and is not stored in the database.
// Upon node startup, the current state header is *always* the current canonical header.
func (hc *HeaderChain) CurrentStateHeader() *types.Header {
	return hc.currentStateHeader.Load().(*types.Header)
}

// CurrentBlock returns the block for the current header.
func (hc *HeaderChain) CurrentBlock() *types.Block {
	return hc.GetBlockOrCandidateByHash(hc.CurrentHeader().Hash())
}

// SetGenesis sets a new genesis block header for the chain
func (hc *HeaderChain) SetGenesis(head *types.Header) {
	hc.genesisHeader = head
}

// Config retrieves the header chain's chain configuration.
func (hc *HeaderChain) Config() *params.ChainConfig { return hc.config }

// GetBlock implements consensus.ChainReader, and returns nil for every input as
// a header chain does not have blocks available for retrieval.
func (hc *HeaderChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	return hc.bc.GetBlock(hash, number)
}

// CheckContext checks to make sure the range of a context or order is valid
func (hc *HeaderChain) CheckContext(context int) error {
	if context < 0 || context > common.HierarchyDepth {
		return errors.New("the provided path is outside the allowable range")
	}
	return nil
}

// CheckLocationRange checks to make sure the range of r and z are valid
func (hc *HeaderChain) CheckLocationRange(location []byte) error {
	if int(location[0]) < 1 || int(location[0]) > common.NumRegionsInPrime {
		return errors.New("the provided location is outside the allowable region range")
	}
	if int(location[1]) < 1 || int(location[1]) > common.NumZonesInRegion {
		return errors.New("the provided location is outside the allowable zone range")
	}
	return nil
}

// GasLimit returns the gas limit of the current HEAD block.
func (hc *HeaderChain) GasLimit() uint64 {
	return hc.CurrentHeader().GasLimit()
}

// GetUnclesInChain retrieves all the uncles from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	uncles := []*types.Header{}
	for i := 0; block != nil && i < length; i++ {
		uncles = append(uncles, block.Uncles()...)
		block = hc.GetBlock(block.ParentHash(hc.NodeCtx()), block.NumberU64(hc.NodeCtx())-1)
	}
	return uncles
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) GetGasUsedInChain(block *types.Block, length int) int64 {
	gasUsed := 0
	for i := 0; block != nil && i < length; i++ {
		gasUsed += int(block.GasUsed())
		block = hc.GetBlock(block.ParentHash(hc.NodeCtx()), block.NumberU64(hc.NodeCtx())-1)
	}
	return int64(gasUsed)
}

// GetGasUsedInChain retrieves all the gas used from a given block backwards until
// a specific distance is reached.
func (hc *HeaderChain) CalculateBaseFee(header *types.Header) *big.Int {
	return misc.CalcBaseFee(hc.Config(), header)
}

// Export writes the active chain to the given writer.
func (hc *HeaderChain) Export(w io.Writer) error {
	return hc.ExportN(w, uint64(0), hc.CurrentHeader().NumberU64(hc.NodeCtx()))
}

// ExportN writes a subset of the active chain to the given writer.
func (hc *HeaderChain) ExportN(w io.Writer, first uint64, last uint64) error {
	hc.headermu.RLock()
	defer hc.headermu.RUnlock()

	if first > last {
		return fmt.Errorf("export failed: first (%d) is greater than last (%d)", first, last)
	}
	hc.logger.WithField("count", last-first+1).Info("Exporting batch of blocks")

	for nr := first; nr <= last; nr++ {
		block := hc.GetBlockByNumber(nr)
		if block == nil {
			return fmt.Errorf("export failed on #%d: not found", nr)
		}
		if err := block.EncodeRLP(w); err != nil {
			return err
		}
	}
	return nil
}

// GetBlockFromCacheOrDb looks up the body cache first and then checks the db
func (hc *HeaderChain) GetBlockFromCacheOrDb(hash common.Hash, number uint64) *types.Block {
	// Short circuit if the block's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.blockCache.Get(hash); ok {
		block := cached.(*types.Block)
		return block
	}
	return hc.GetBlock(hash, number)
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (hc *HeaderChain) GetBlockByHash(hash common.Hash) *types.Block {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.GetBlock(hash, *number)
}

func (hc *HeaderChain) GetBlockOrCandidate(hash common.Hash, number uint64) *types.Block {
	return hc.bc.GetBlockOrCandidate(hash, number)
}

// GetBlockOrCandidateByHash retrieves any block from the database by hash, caching it if found.
func (hc *HeaderChain) GetBlockOrCandidateByHash(hash common.Hash) *types.Block {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.bc.GetBlockOrCandidate(hash, *number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it
// (associated with its hash) if found.
func (hc *HeaderChain) GetBlockByNumber(number uint64) *types.Block {
	hash := rawdb.ReadCanonicalHash(hc.headerDb, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return hc.GetBlock(hash, number)
}

// GetBody retrieves a block body (transactions and uncles) from the database by
// hash, caching it if found.
func (hc *HeaderChain) GetBody(hash common.Hash) *types.Body {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.bodyCache.Get(hash); ok {
		body := cached.(*types.Body)
		return body
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBody(hc.headerDb, hash, *number, hc.NodeLocation())
	if body == nil {
		return nil
	}
	// Cache the found body for next time and return
	hc.bc.bodyCache.Add(hash, body)
	return body
}

// GetBodyRLP retrieves a block body in RLP encoding from the database by hash,
// caching it if found.
func (hc *HeaderChain) GetBodyRLP(hash common.Hash) rlp.RawValue {
	// Short circuit if the body's already in the cache, retrieve otherwise
	if cached, ok := hc.bc.bodyProtoCache.Get(hash); ok {
		return cached.(rlp.RawValue)
	}
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBodyProto(hc.headerDb, hash, *number)
	if len(body) == 0 {
		return nil
	}
	// Cache the found body for next time and return
	hc.bc.bodyProtoCache.Add(hash, body)
	return body
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
// [deprecated by eth/62]
func (hc *HeaderChain) GetBlocksFromHash(hash common.Hash, n int) (blocks []*types.Block) {
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
func (hc *HeaderChain) Engine() consensus.Engine {
	return hc.engine
}

// SubscribeChainHeadEvent registers a subscription of ChainHeadEvent.
func (hc *HeaderChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return hc.scope.Track(hc.chainHeadFeed.Subscribe(ch))
}

// SubscribeChainSideEvent registers a subscription of ChainSideEvent.
func (hc *HeaderChain) SubscribeChainSideEvent(ch chan<- ChainSideEvent) event.Subscription {
	return hc.scope.Track(hc.chainSideFeed.Subscribe(ch))
}

func (hc *HeaderChain) StateAt(root common.Hash) (*state.StateDB, error) {
	return hc.bc.processor.StateAt(root)
}

func (hc *HeaderChain) SlicesRunning() []common.Location {
	return hc.slicesRunning
}

func (hc *HeaderChain) GetUtxo(hash common.Hash, index uint32) *types.UtxoEntry {
	return hc.bc.GetUtxo(hash, index)
}

// fetchUtxosMain fetches unspent transaction output data about the provided
// set of outpoints from the point of view of the end of the main chain at the
// time of the call.
//
// Upon completion of this function, the view will contain an entry for each
// requested outpoint.  Spent outputs, or those which otherwise don't exist,
// will result in a nil entry in the view.
func (hc *HeaderChain) FetchUtxosMain(view *types.UtxoViewpoint, outpoints []types.OutPoint) error {
	// Nothing to do if there are no requested outputs.
	if len(outpoints) == 0 {
		return nil
	}

	// Load the requested set of unspent transaction outputs from the point
	// of view of the end of the main chain.
	//
	// NOTE: Missing entries are not considered an error here and instead
	// will result in nil entries in the view.  This is intentionally done
	// so other code can use the presence of an entry in the store as a way
	// to unnecessarily avoid attempting to reload it from the database.
	for i := range outpoints {
		entry := hc.GetUtxo(outpoints[i].TxHash, outpoints[i].Index)
		if entry == nil {
			return nil
		}

		view.AddEntry(outpoints, i, entry)

		return nil
	}

	return nil
}

func (hc *HeaderChain) VerifyUTXOsForTx(tx *types.Transaction) error {
	if tx.Type() != types.QiTxType {
		return errors.New("invalid tx type")
	}
	for _, txIn := range tx.TxIn() {
		utxo := hc.GetUtxo(txIn.PreviousOutPoint.TxHash, txIn.PreviousOutPoint.Index)
		if utxo == nil {
			return errors.New("utxo not found")
		}
	}
	return nil
}

// fetchInputUtxos loads the unspent transaction outputs for the inputs
// referenced by the transactions in the given block into the view from the
// database as needed.  In particular, referenced entries that are earlier in
// the block are added to the view and entries that are already in the view are
// not modified.
func (hc *HeaderChain) fetchInputUtxos(view *types.UtxoViewpoint, block *types.Block) error {
	// Build a map of in-flight transactions because some of the inputs in
	// this block could be referencing other transactions earlier in this
	// block which are not yet in the chain.
	txInFlight := map[common.Hash]int{}
	transactions := block.QiTransactions()
	for i, tx := range transactions {
		txInFlight[tx.Hash()] = i
	}

	if types.IsCoinBaseTx(transactions[0]) {
		transactions = transactions[1:]
	}

	// Loop through all of the transaction inputs (except for the coinbase
	// which has no inputs) collecting them into sets of what is needed and
	// what is already known (in-flight).
	needed := make([]types.OutPoint, 0, len(transactions))
	for i, tx := range transactions {
		for _, txIn := range tx.TxIn() {
			// It is acceptable for a transaction input to reference
			// the output of another transaction in this block only
			// if the referenced transaction comes before the
			// current one in this block.  Add the outputs of the
			// referenced transaction as available utxos when this
			// is the case.  Otherwise, the utxo details are still
			// needed.
			//
			// NOTE: The >= is correct here because i is one less
			// than the actual position of the transaction within
			// the block due to skipping the coinbase.
			originHash := &txIn.PreviousOutPoint.TxHash
			if inFlightIndex, ok := txInFlight[*originHash]; ok &&
				i >= inFlightIndex {

				originTx := transactions[inFlightIndex]
				view.AddTxOuts(originTx, block.Header())
				continue
			}

			// Don't request entries that are already in the view
			// from the database.
			if _, ok := view.Entries[txIn.PreviousOutPoint]; ok {
				continue
			}

			needed = append(needed, txIn.PreviousOutPoint)
		}
	}

	// Request the input utxos from the database.
	return hc.FetchUtxosMain(view, needed)
}

func (hc *HeaderChain) verifyInputUtxos(view *types.UtxoViewpoint, block *types.Block, signer types.Signer) (*big.Int, error) { // should this be used instead of Verify

	transactions := block.QiTransactions()
	if types.IsCoinBaseTx(transactions[0]) {
		transactions = transactions[1:]
	}

	totalFees := big.NewInt(0)

	for _, tx := range transactions {

		fee, err := types.CheckTransactionInputs(tx, view)
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %v: %w", tx.Hash().Hex(), err)
		}
		totalFees.Add(totalFees, fee)

		pubKeys := make([]*btcec.PublicKey, 0)
		for _, txIn := range tx.TxIn() {

			entry := view.LookupEntry(txIn.PreviousOutPoint)
			if entry == nil {
				return nil, errors.New("utxo not found " + txIn.PreviousOutPoint.TxHash.String())
			}

			// Verify the pubkey
			address := pubKeyToAddress(txIn.PubKey, hc.NodeLocation())
			entryAddr := common.BytesToAddress(entry.Address, hc.NodeLocation())
			if !address.Equal(entryAddr) {
				return nil, errors.New("invalid address")
			}

			pubKey, err := btcec.ParsePubKey(txIn.PubKey)
			if err != nil {
				return nil, err
			}
			pubKeys = append(pubKeys, pubKey)
		}

		var finalKey *btcec.PublicKey
		if len(tx.TxIn()) > 1 {
			aggKey, _, _, err := musig2.AggregateKeys(
				pubKeys, false,
			)
			if err != nil {
				return nil, err
			}
			finalKey = aggKey.FinalKey
		} else {
			finalKey = pubKeys[0]
		}

		txDigestHash := signer.Hash(tx)
		if !tx.GetSchnorrSignature().Verify(txDigestHash[:], finalKey) {
			return nil, errors.New("invalid signature")
		}

	}

	return totalFees, nil
}

// writeUtxoViewpoint updates the utxo set in the database based on the provided utxo view contents and state.  In
// particular, only the entries that have been marked as modified are written
// to the database.
func (hc *HeaderChain) WriteUtxoViewpoint(view *types.UtxoViewpoint) error {
	for outpoint, entry := range view.Entries {
		// No need to update the database if the entry was not modified.
		if entry == nil || !entry.IsModified() {
			continue
		}

		// Remove the utxo entry if it is spent.
		if entry.IsSpent() {
			rawdb.DeleteUtxo(hc.bc.db, outpoint.TxHash, outpoint.Index)
			continue
		}
		rawdb.WriteUtxo(hc.bc.db, outpoint.TxHash, outpoint.Index, entry)
	}

	return nil
}

func (hc *HeaderChain) DeleteUtxoViewpoint(hash common.Hash) error {
	block := hc.GetBlockByHash(hash)
	if block == nil {
		return errors.New("block not found")
	}

	view := types.NewUtxoViewpoint(hc.NodeLocation())

	err := hc.fetchInputUtxos(view, block)
	if err != nil {
		return err
	}

	// Load all of the spent txos for the block from the spend
	// journal.
	stxos := rawdb.ReadSpentUTXOs(hc.bc.db, hash)
	// Unspend all of the previously spent UTXOs.
	hc.disconnectTransactions(view, block, stxos)
	err = hc.WriteUtxoViewpoint(view)
	if err != nil {
		return err
	}

	return nil
}

// createCoinbaseTxWithFees returns a coinbase transaction paying an appropriate subsidy
// based on the passed block height to the provided address.  When the address
// is nil, the coinbase transaction will instead be redeemable by anyone.
//
// See the comment for NewBlockTemplate for more information about why the nil
// address handling is useful.
func createCoinbaseTxWithFees(header *types.Header, fees *big.Int) (*types.Transaction, error) {
	parentHash := header.ParentHash(header.Location().Context()) // all blocks should have zone location and context
	in := types.TxIn{
		// Coinbase transactions have no inputs, so previous outpoint is
		// zero hash and max index.
		PreviousOutPoint: *types.NewOutPoint(&parentHash, types.MaxPrevOutIndex),
	}

	denominations := misc.CalculateRewardForQiWithFees(header, fees)
	fmt.Printf("denominations: %v\n", denominations)

	outs := make([]types.TxOut, 0, len(denominations))

	// Iterate over the denominations in descending order (by key)
	for i := 15; i >= 0; i-- {
		// If the denomination count is zero, skip it
		if denominations[uint8(i)] == 0 {
			continue
		}
		for j := uint8(0); j < denominations[uint8(i)]; j++ {
			// Create the output for the denomination
			out := types.TxOut{
				Denomination: uint8(i),
				Address:      header.Coinbase().Bytes(),
			}
			outs = append(outs, out)
		}
	}

	qiTx := &types.QiTx{
		TxIn:  []types.TxIn{in},
		TxOut: outs,
	}

	tx := types.NewTx(qiTx)
	fmt.Println("coinbase tx", tx.Hash().Hex())
	return tx, nil
}

// disconnectTransactions updates the view by removing all of the transactions
// created by the passed block, restoring all utxos the transactions spent by
// using the provided spent txo information, and setting the best hash for the
// view to the block before the passed block.
func (hc *HeaderChain) disconnectTransactions(view *types.UtxoViewpoint, block *types.Block, stxos []types.SpentTxOut) error {
	// Sanity check the correct number of stxos are provided.
	if len(stxos) != types.CountSpentOutputs(block) {
		return fmt.Errorf("disconnectTransactions: wrong number of")
	}

	// Loop backwards through all transactions so everything is unspent in
	// reverse order.  This is necessary since transactions later in a block
	// can spend from previous ones.
	stxoIdx := len(stxos) - 1

	transactions := block.QiTransactions()

	for txIdx := len(transactions) - 1; txIdx > -1; txIdx-- {

		tx := transactions[txIdx]
		// All entries will need to potentially be marked as a coinbase.
		var packedFlags types.TxoFlags
		outPointHash := tx.Hash()
		isCoinBase := txIdx == 0
		if isCoinBase {
			packedFlags |= types.TfCoinBase
			outPointHash = block.Hash()
		}
		// Mark all of the spendable outputs originally created by the
		// transaction as spent.  It is instructive to note that while
		// the outputs aren't actually being spent here, rather they no
		// longer exist, since a pruned utxo set is used, there is no
		// practical difference between a utxo that does not exist and
		// one that has been spent.
		//
		// When the utxo does not already exist in the view, add an
		// entry for it and then mark it spent.  This is done because
		// the code relies on its existence in the view in order to
		// signal modifications have happened.
		prevOut := types.OutPoint{TxHash: outPointHash}
		for txOutIdx, txOut := range tx.TxOut() {
			prevOut.Index = uint32(txOutIdx)
			entry := view.Entries[prevOut]
			if entry == nil {
				entry = &types.UtxoEntry{
					Denomination: txOut.Denomination,
					Address:      txOut.Address,
					BlockHeight:  block.NumberU64(hc.NodeCtx()),
					PackedFlags:  packedFlags,
				}

				view.Entries[prevOut] = entry
			}

			entry.Spend()
		}

		// Loop backwards through all of the transaction inputs (except
		// for the coinbase which has no inputs) and unspend the
		// referenced txos.  This is necessary to match the order of the
		// spent txout entries.
		if isCoinBase {
			continue
		}
		for txInIdx := len(tx.TxIn()) - 1; txInIdx > -1; txInIdx-- {
			// Ensure the spent txout index is decremented to stay
			// in sync with the transaction input.
			stxo := &stxos[stxoIdx]
			stxoIdx--

			// When there is not already an entry for the referenced
			// output in the view, it means it was previously spent,
			// so create a new utxo entry in order to resurrect it.
			originOut := &tx.TxIn()[txInIdx].PreviousOutPoint
			entry := view.Entries[*originOut]
			if entry == nil {
				entry = new(types.UtxoEntry)
				view.Entries[*originOut] = entry
			}

			// Restore the utxo using the stxo data from the spend
			// journal and mark it as modified.
			entry.Denomination = stxo.Denomination
			entry.Address = stxo.Address
			entry.BlockHeight = stxo.Height
			entry.PackedFlags = types.TfModified
			if stxo.IsCoinBase {
				entry.PackedFlags |= types.TfCoinBase
			}
		}
	}
	return nil
}

func pubKeyToAddress(pubKey []byte, location common.Location) common.Address {
	return common.BytesToAddress(crypto.Keccak256(pubKey[1:])[12:], location)
}
