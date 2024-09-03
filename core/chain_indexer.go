// Copyright 2017 The go-ethereum Authors
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

package core

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

var PruneDepth = uint64(1000000) // Number of blocks behind in which we begin pruning old block data

// ChainIndexerBackend defines the methods needed to process chain segments in
// the background and write the segment results into the database. These can be
// used to create filter blooms or CHTs.
type ChainIndexerBackend interface {
	// Reset initiates the processing of a new chain segment, potentially terminating
	// any partially completed operations (in case of a reorg).
	Reset(ctx context.Context, section uint64, prevHead common.Hash) error

	// Process crunches through the next header in the chain segment. The caller
	// will ensure a sequential order of headers.
	Process(ctx context.Context, header *types.WorkObject, bloom types.Bloom) error

	// Commit finalizes the section metadata and stores it into the database.
	Commit() error

	// Prune deletes the chain index older than the given threshold.
	Prune(threshold uint64) error
}

// ChainIndexerChain interface is used for connecting the indexer to a blockchain
type ChainIndexerChain interface {
	// CurrentHeader retrieves the latest locally known header.
	CurrentHeader() *types.WorkObject
	// GetBloom retrieves the bloom for the given block hash.
	GetBloom(blockhash common.Hash) (*types.Bloom, error)
	// SubscribeChainHeadEvent subscribes to new head header notifications.
	SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription
	// NodeCtx returns the context of the chain
	NodeCtx() int
	// StateAt returns the state for a state trie root and utxo root
	StateAt(root common.Hash, etxRoot common.Hash, quaiStateSize *big.Int) (*state.StateDB, error)
}

// ChainIndexer does a post-processing job for equally sized sections of the
// canonical chain (like BlooomBits and CHT structures). A ChainIndexer is
// connected to the blockchain through the event system by starting a
// ChainHeadEventLoop in a goroutine.
//
// Further child ChainIndexers can be added which use the output of the parent
// section indexer. These child indexers receive new head notifications only
// after an entire section has been finished or in case of rollbacks that might
// affect already finished sections.
type ChainIndexer struct {
	chainDb   ethdb.Database      // Chain database to index the data from
	indexDb   ethdb.Database      // Prefixed table-view of the db to write index metadata into
	backend   ChainIndexerBackend // Background processor generating the index data content
	children  []*ChainIndexer     // Child indexers to cascade chain updates to
	GetBloom  func(common.Hash) (*types.Bloom, error)
	StateAt   func(common.Hash, common.Hash, *big.Int) (*state.StateDB, error)
	active    uint32          // Flag whether the event loop was started
	update    chan struct{}   // Notification channel that headers should be processed
	quit      chan chan error // Quit channel to tear down running goroutines
	ctx       context.Context
	ctxCancel func()

	sectionSize uint64 // Number of blocks in a single chain segment to process
	confirmsReq uint64 // Number of confirmations before processing a completed segment

	storedSections uint64 // Number of sections successfully indexed into the database
	knownSections  uint64 // Number of sections known to be complete (block wise)
	cascadedHead   uint64 // Block number of the last completed section cascaded to subindexers

	throttling time.Duration // Disk throttling to prevent a heavy upgrade from hogging resources

	logger            *log.Logger
	lock              sync.Mutex
	pruneLock         sync.Mutex
	indexAddressUtxos bool
}

// NewChainIndexer creates a new chain indexer to do background processing on
// chain segments of a given size after certain number of confirmations passed.
// The throttling parameter might be used to prevent database thrashing.
func NewChainIndexer(chainDb ethdb.Database, indexDb ethdb.Database, backend ChainIndexerBackend, section, confirm uint64, throttling time.Duration, kind string, nodeCtx int, logger *log.Logger, indexAddressUtxos bool) *ChainIndexer {
	c := &ChainIndexer{
		chainDb:           chainDb,
		indexDb:           indexDb,
		backend:           backend,
		update:            make(chan struct{}, 1),
		quit:              make(chan chan error),
		sectionSize:       section,
		confirmsReq:       confirm,
		throttling:        throttling,
		logger:            logger,
		indexAddressUtxos: indexAddressUtxos,
	}
	// Initialize database dependent fields and start the updater
	c.loadValidSections()
	c.ctx, c.ctxCancel = context.WithCancel(context.Background())

	go c.updateLoop(nodeCtx)

	return c
}

// Start creates a goroutine to feed chain head events into the indexer for
// cascading background processing. Children do not need to be started, they
// are notified about new events by their parents.
func (c *ChainIndexer) Start(chain ChainIndexerChain, config params.ChainConfig) {
	events := make(chan ChainHeadEvent, 10)
	sub := chain.SubscribeChainHeadEvent(events)
	c.GetBloom = chain.GetBloom
	c.StateAt = chain.StateAt
	go c.eventLoop(chain.CurrentHeader(), events, sub, chain.NodeCtx(), config)
}

// Close tears down all goroutines belonging to the indexer and returns any error
// that might have occurred internally.
func (c *ChainIndexer) Close() error {
	var errs []error

	c.ctxCancel()

	// Tear down the primary update loop
	errc := make(chan error)
	c.quit <- errc
	if err := <-errc; err != nil {
		errs = append(errs, err)
	}
	// If needed, tear down the secondary event loop
	if atomic.LoadUint32(&c.active) != 0 {
		c.quit <- errc
		if err := <-errc; err != nil {
			errs = append(errs, err)
		}
	}
	// Close all children
	for _, child := range c.children {
		if err := child.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	// Return any failures
	switch {
	case len(errs) == 0:
		return nil

	case len(errs) == 1:
		return errs[0]

	default:
		return fmt.Errorf("%v", errs)
	}
}

// eventLoop is a secondary - optional - event loop of the indexer which is only
// started for the outermost indexer to push chain head events into a processing
// queue.
func (c *ChainIndexer) eventLoop(currentHeader *types.WorkObject, events chan ChainHeadEvent, sub event.Subscription, nodeCtx int, config params.ChainConfig) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	// Mark the chain indexer as active, requiring an additional teardown
	atomic.StoreUint32(&c.active, 1)

	defer sub.Unsubscribe()

	// Fire the initial new head event to start any outstanding processing
	c.newHead(currentHeader.NumberU64(nodeCtx), false)
	qiIndexerCh := make(chan *types.WorkObject, 10000)
	go c.indexerLoop(currentHeader, qiIndexerCh, nodeCtx, config)
	for {
		select {
		case errc := <-c.quit:
			// Chain indexer terminating, report no failure and abort
			errc <- nil
			return

		case ev, ok := <-events:
			// Received a new event, ensure it's not nil (closing) and update
			if !ok {
				errc := <-c.quit
				errc <- nil
				return
			}
			select {
			case qiIndexerCh <- ev.Block:
			default:
				c.logger.Warn("qiIndexerCh is full, dropping block")
			}
		}
	}
}

func (c *ChainIndexer) indexerLoop(currentHeader *types.WorkObject, qiIndexerCh chan *types.WorkObject, nodeCtx int, config params.ChainConfig) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	var (
		prevHeader = currentHeader
		prevHash   = currentHeader.Hash()
	)
	for {
		select {
		case errc := <-c.quit:
			// Chain indexer terminating, report no failure and abort
			errc <- nil
			return
		case block := <-qiIndexerCh:
			start := time.Now()
			if block.NumberU64(nodeCtx) > PruneDepth {
				// Ensure block is canonical before pruning
				if rawdb.ReadCanonicalHash(c.chainDb, block.NumberU64(nodeCtx)) != block.Hash() {
					if rawdb.ReadCanonicalHash(c.chainDb, block.NumberU64(nodeCtx)-1) != block.ParentHash(nodeCtx) {
						c.logger.Errorf("Block %d sent to ChainIndexer is not canonical, skipping hash %s", block.NumberU64(nodeCtx), block.Hash())
						return
					}
				}
				c.PruneOldBlockData(block.NumberU64(nodeCtx) - PruneDepth)
			}
			time1 := time.Since(start)
			var validUtxoIndex bool
			var addressOutpoints map[string]map[string]*types.OutpointAndDenomination
			if c.indexAddressUtxos {
				validUtxoIndex = true
				addressOutpoints = make(map[string]map[string]*types.OutpointAndDenomination)
			}
			time2 := time.Since(start)

			var time3, time4, time5 time.Duration
			if block.ParentHash(nodeCtx) != prevHash {
				// Reorg to the common ancestor if needed (might not exist in light sync mode, skip reorg then)
				// TODO: This seems a bit brittle, can we detect this case explicitly?

				if rawdb.ReadCanonicalHash(c.chainDb, prevHeader.NumberU64(nodeCtx)) != prevHash {
					h, err := rawdb.FindCommonAncestor(c.chainDb, prevHeader, block, nodeCtx)
					if h != nil {

						// If indexAddressUtxos flag is enabled, update the address utxo map
						// TODO: Need to be able to turn on/off indexer and fix corrupted state
						if c.indexAddressUtxos {
							reorgHeaders := make([]*types.WorkObject, 0)
							for prev := prevHeader; prev.Hash() != h.Hash(); {
								prevNumber := rawdb.ReadHeaderNumber(c.chainDb, prev.ParentHash(nodeCtx))
								if prevNumber == nil {
									break
								}
								prev = rawdb.ReadHeader(c.chainDb, *prevNumber, prev.ParentHash(nodeCtx))
								reorgHeaders = append(reorgHeaders, h)
							}

							c.logger.Warn("ChainIndexer: Reorging the utxo indexer of len", len(reorgHeaders))

							time3 = time.Since(start)

							// Reorg out all outpoints of the reorg headers
							err := c.reorgUtxoIndexer(reorgHeaders, addressOutpoints, nodeCtx, config)
							if err != nil {
								c.logger.Error("Failed to reorg utxo indexer", "err", err)
								validUtxoIndex = false
							}

							time4 = time.Since(start)

							// Add new blocks from current hash back to common ancestor
							for curr := block; curr.Hash() != h.Hash(); {
								prevNumber := rawdb.ReadHeaderNumber(c.chainDb, curr.ParentHash(nodeCtx))
								if prevNumber == nil {
									break
								}
								curr = rawdb.ReadHeader(c.chainDb, *prevNumber, curr.ParentHash(nodeCtx))
								block := rawdb.ReadWorkObject(c.chainDb, *prevNumber, curr.Hash(), types.BlockObject)
								c.addOutpointsToIndexer(addressOutpoints, nodeCtx, config, block)
							}
						}

						time5 = time.Since(start)

						c.newHead(h.NumberU64(nodeCtx), true)
					} else if err != nil {
						c.logger.WithField("err", err).Error("Failed to index: failed to find common ancestor")
						continue
					}
				}
			}

			time6 := time.Since(start)

			if c.indexAddressUtxos {
				c.addOutpointsToIndexer(addressOutpoints, nodeCtx, config, block)
			}

			time7 := time.Since(start)

			c.newHead(block.NumberU64(nodeCtx), false)

			time8 := time.Since(start)

			if c.indexAddressUtxos && validUtxoIndex {
				err := rawdb.WriteAddressOutpoints(c.chainDb, addressOutpoints)
				if err != nil {
					panic(err)
				}
			}

			time9 := time.Since(start)

			for key, _ := range addressOutpoints {
				addressOutpoints[key] = nil
			}
			addressOutpoints = nil

			time10 := time.Since(start)
			prevHeader, prevHash = block, block.Hash()

			c.logger.Info("ChainIndexer: setting the prevHeader and prevHash", prevHeader.NumberArray(), prevHash)
			c.logger.WithFields(log.Fields{
				"time1":  common.PrettyDuration(time1),
				"time2":  common.PrettyDuration(time2),
				"time3":  common.PrettyDuration(time3),
				"time4":  common.PrettyDuration(time4),
				"time5":  common.PrettyDuration(time5),
				"time6":  common.PrettyDuration(time6),
				"time7":  common.PrettyDuration(time7),
				"time8":  common.PrettyDuration(time8),
				"time9":  common.PrettyDuration(time9),
				"time10": common.PrettyDuration(time10),
			}).Info("Times in indexerLoop")
		}

	}
}

func (c *ChainIndexer) PruneOldBlockData(blockHeight uint64) {
	c.pruneLock.Lock()
	blockHash := rawdb.ReadCanonicalHash(c.chainDb, blockHeight)
	if rawdb.ReadAlreadyPruned(c.chainDb, blockHash) {
		return
	}
	rawdb.WriteAlreadyPruned(c.chainDb, blockHash) // Pruning can only happen once per block
	c.pruneLock.Unlock()

	rawdb.DeleteInboundEtxs(c.chainDb, blockHash)
	rawdb.DeletePendingEtxs(c.chainDb, blockHash)
	rawdb.DeletePendingEtxsRollup(c.chainDb, blockHash)
	rawdb.DeleteManifest(c.chainDb, blockHash)
	rawdb.DeletePbCacheBody(c.chainDb, blockHash)
	createdUtxos, _ := rawdb.ReadCreatedUTXOKeys(c.chainDb, blockHash)
	if len(createdUtxos) > 0 {
		createdUtxosToKeep := make([][]byte, 0, len(createdUtxos)/2)
		for _, key := range createdUtxos {
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
			createdUtxosToKeep = append(createdUtxosToKeep, key)
		}
		c.logger.Infof("Removed %d utxo keys from block %d", len(createdUtxos)-len(createdUtxosToKeep), blockHeight)
		rawdb.WritePrunedUTXOKeys(c.chainDb, blockHeight, createdUtxosToKeep)
	}
	rawdb.DeleteCreatedUTXOKeys(c.chainDb, blockHash)
	rawdb.DeleteSpentUTXOs(c.chainDb, blockHash)
	rawdb.DeleteTrimmedUTXOs(c.chainDb, blockHash)
	rawdb.DeleteTrimDepths(c.chainDb, blockHash)
	rawdb.DeleteCollidingKeys(c.chainDb, blockHash)
}

func compareMinLength(a, b []byte) bool {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}

	// Compare the slices up to the length of the pruned key
	// The 9th byte (position 8) is the denomination in the pruned utxo key
	for i := 0; i < rawdb.PrunedUtxoKeyWithDenominationLength-1; i++ {
		if a[i] != b[i] {
			return false
		}
	}

	// If the slices are identical up to the shorter length, return true
	return true
}

// newHead notifies the indexer about new chain heads and/or reorgs.
func (c *ChainIndexer) newHead(head uint64, reorg bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	// If a reorg happened, invalidate all sections until that point
	if reorg {
		// Revert the known section number to the reorg point
		known := (head + 1) / c.sectionSize
		stored := known
		if known < c.knownSections {
			c.knownSections = known
		}
		// Revert the stored sections from the database to the reorg point
		if stored < c.storedSections {
			c.setValidSections(stored)
		}
		// Update the new head number to the finalized section end and notify children
		head = known * c.sectionSize

		if head < c.cascadedHead {
			c.cascadedHead = head
			for _, child := range c.children {
				child.newHead(c.cascadedHead, true)
			}
		}
		return
	}
	// No reorg, calculate the number of newly known sections and update if high enough
	var sections uint64
	if head >= c.confirmsReq {
		sections = (head + 1 - c.confirmsReq) / c.sectionSize

		if sections > c.knownSections {
			c.knownSections = sections

			select {
			case c.update <- struct{}{}:
			default:
			}
		}
	}
}

// updateLoop is the main event loop of the indexer which pushes chain segments
// down into the processing backend.
func (c *ChainIndexer) updateLoop(nodeCtx int) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	var (
		updating bool
		updated  time.Time
	)

	for {
		select {
		case errc := <-c.quit:
			// Chain indexer terminating, report no failure and abort
			errc <- nil
			return

		case <-c.update:
			// Section headers completed (or rolled back), update the index
			c.lock.Lock()
			if c.knownSections > c.storedSections {
				// Periodically print an upgrade log message to the user
				if time.Since(updated) > 8*time.Second {
					if c.knownSections > c.storedSections+1 {
						updating = true
						c.logger.WithField("percentage", c.storedSections*100/c.knownSections).Info("Upgrading chain index")
					}
					updated = time.Now()
				}
				// Cache the current section count and head to allow unlocking the mutex
				section := c.storedSections
				var oldHead common.Hash
				if section > 0 {
					oldHead = c.SectionHead(section - 1)
				}
				// Process the newly defined section in the background
				c.lock.Unlock()
				newHead, err := c.processSection(section, oldHead, nodeCtx)
				if err != nil {
					select {
					case <-c.ctx.Done():
						<-c.quit <- nil
						return
					default:
					}
					c.logger.WithField("err", err).Warn("Section processing failed")
				}
				c.lock.Lock()

				// If processing succeeded and no reorgs occurred, mark the section completed
				if err == nil && (section == 0 || oldHead == c.SectionHead(section-1)) {
					c.setSectionHead(section, newHead)
					c.setValidSections(section + 1)
					if c.storedSections == c.knownSections && updating {
						updating = false
						c.logger.Info("Finished upgrading chain index")
					}
					c.cascadedHead = c.storedSections*c.sectionSize - 1
					for _, child := range c.children {
						c.logger.WithField("head", c.cascadedHead).Trace("Cascading chain index update")
						child.newHead(c.cascadedHead, false)
					}
				} else {
					// If processing failed, don't retry until further notification
					c.logger.WithFields(log.Fields{
						"section": section,
						"error":   err,
					}).Debug("Section processing failed")
					c.knownSections = c.storedSections
				}
			}
			// If there are still further sections to process, reschedule
			if c.knownSections > c.storedSections {
				time.AfterFunc(c.throttling, func() {
					select {
					case c.update <- struct{}{}:
					default:
					}
				})
			}
			c.lock.Unlock()
		}
	}
}

// processSection processes an entire section by calling backend functions while
// ensuring the continuity of the passed headers. Since the chain mutex is not
// held while processing, the continuity can be broken by a long reorg, in which
// case the function returns with an error.
func (c *ChainIndexer) processSection(section uint64, lastHead common.Hash, nodeCtx int) (common.Hash, error) {
	c.logger.WithField("section", section).Trace("Processing new chain section")

	// Reset and partial processing
	if err := c.backend.Reset(c.ctx, section, lastHead); err != nil {
		c.setValidSections(0)
		return common.Hash{}, err
	}

	for number := section * c.sectionSize; number < (section+1)*c.sectionSize; number++ {
		hash := rawdb.ReadCanonicalHash(c.chainDb, number)
		if hash == (common.Hash{}) {
			return common.Hash{}, fmt.Errorf("canonical block #%d unknown", number)
		}
		header := rawdb.ReadHeader(c.chainDb, number, hash)
		if header == nil {
			return common.Hash{}, fmt.Errorf("block #%d [%x..] not found", number, hash[:4])
		} else if header.ParentHash(nodeCtx) != lastHead {
			return common.Hash{}, fmt.Errorf("chain reorged during section processing")
		}
		bloom, err := c.GetBloom(header.Hash())
		if err != nil {
			return common.Hash{}, err
		}
		if err := c.backend.Process(c.ctx, header, *bloom); err != nil {
			return common.Hash{}, err
		}
		lastHead = header.Hash()
	}
	if err := c.backend.Commit(); err != nil {
		return common.Hash{}, err
	}
	return lastHead, nil
}

// Sections returns the number of processed sections maintained by the indexer
// and also the information about the last header indexed for potential canonical
// verifications.
func (c *ChainIndexer) Sections() (uint64, uint64, common.Hash) {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.storedSections, c.storedSections*c.sectionSize - 1, c.SectionHead(c.storedSections - 1)
}

// AddChildIndexer adds a child ChainIndexer that can use the output of this one
func (c *ChainIndexer) AddChildIndexer(indexer *ChainIndexer) {
	if indexer == c {
		panic("can't add indexer as a child of itself")
	}
	c.lock.Lock()
	defer c.lock.Unlock()

	c.children = append(c.children, indexer)

	// Cascade any pending updates to new children too
	sections := c.storedSections
	if c.knownSections < sections {
		sections = c.knownSections
	}
	if sections > 0 {
		indexer.newHead(sections*c.sectionSize-1, false)
	}
}

// Prune deletes all chain data older than given threshold.
func (c *ChainIndexer) Prune(threshold uint64) error {
	return c.backend.Prune(threshold)
}

// loadValidSections reads the number of valid sections from the index database
// and caches is into the local state.
func (c *ChainIndexer) loadValidSections() {
	data, _ := c.indexDb.Get([]byte("count"))
	if len(data) == 8 {
		c.storedSections = binary.BigEndian.Uint64(data)
	}
}

// setValidSections writes the number of valid sections to the index database
func (c *ChainIndexer) setValidSections(sections uint64) {
	// Set the current number of valid sections in the database
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], sections)
	c.indexDb.Put([]byte("count"), data[:])

	// Remove any reorged sections, caching the valids in the mean time
	for c.storedSections > sections {
		c.storedSections--
		c.removeSectionHead(c.storedSections)
	}
	c.storedSections = sections // needed if new > old
}

// SectionHead retrieves the last block hash of a processed section from the
// index database.
func (c *ChainIndexer) SectionHead(section uint64) common.Hash {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], section)

	hash, _ := c.indexDb.Get(append([]byte("shead"), data[:]...))
	if len(hash) == len(common.Hash{}) {
		return common.BytesToHash(hash)
	}
	return common.Hash{}
}

// setSectionHead writes the last block hash of a processed section to the index
// database.
func (c *ChainIndexer) setSectionHead(section uint64, hash common.Hash) {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], section)

	c.indexDb.Put(append([]byte("shead"), data[:]...), hash.Bytes())
}

// removeSectionHead removes the reference to a processed section from the index
// database.
func (c *ChainIndexer) removeSectionHead(section uint64) {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], section)

	c.indexDb.Delete(append([]byte("shead"), data[:]...))
}

// addOutpointsToIndexer removes the spent outpoints and adds new utxos to the indexer.
func (c *ChainIndexer) addOutpointsToIndexer(addressOutpoints map[string]map[string]*types.OutpointAndDenomination, nodeCtx int, config params.ChainConfig, block *types.WorkObject) {
	utxos := block.QiTransactions() // TODO: Need to add the coinbase outputs into the Indexer

	for _, tx := range utxos {
		for _, in := range tx.TxIn() {

			if types.IsCoinBaseTx(tx) {
				continue
			}

			outpoint := in.PreviousOutPoint

			address := crypto.PubkeyBytesToAddress(in.PubKey, config.Location).Hex()
			outpointsForAddress, exists := addressOutpoints[address]
			if !exists {
				outpointsForAddress = rawdb.ReadOutpointsForAddress(c.chainDb, address)
				addressOutpoints[address] = outpointsForAddress
			}

			delete(outpointsForAddress, outpoint.Key())
		}

		for i, out := range tx.TxOut() {

			addrBytes := out.Address
			outpoint := types.OutPoint{
				TxHash: tx.Hash(),
				Index:  uint16(i),
			}

			address := common.BytesToAddress(addrBytes, config.Location).Hex()

			outpointAndDenom := &types.OutpointAndDenomination{
				TxHash:       outpoint.TxHash,
				Index:        outpoint.Index,
				Denomination: out.Denomination,
			}

			if _, exists := addressOutpoints[address]; !exists {
				addressOutpoints[address] = rawdb.ReadOutpointsForAddress(c.chainDb, address)
			}
			addressOutpoints[address][outpointAndDenom.Key()] = outpointAndDenom
		}
	}
}

// reorgUtxoIndexer adds back previously removed outpoints and removes newly added outpoints.
// This is done in reverse order from the old header to the common ancestor.
func (c *ChainIndexer) reorgUtxoIndexer(headers []*types.WorkObject, addressOutpoints map[string]map[string]*types.OutpointAndDenomination, nodeCtx int, config params.ChainConfig) error {
	for _, header := range headers {
		block := rawdb.ReadWorkObject(c.chainDb, header.NumberU64(nodeCtx), header.Hash(), types.BlockObject)

		for _, tx := range block.QiTransactions() {
			for i, out := range tx.TxOut() {

				address := out.Address

				addr := common.BytesToAddress(address, config.Location).Hex()
				outpointsForAddress, exists := addressOutpoints[addr]
				if !exists {
					outpointsForAddress = rawdb.ReadOutpointsForAddress(c.chainDb, addr)
					addressOutpoints[addr] = outpointsForAddress
				}

				// reconstruct outpoint to remove it via outpoint.Key()
				outpoint := types.OutPoint{
					TxHash: tx.Hash(),
					Index:  uint16(i),
				}

				delete(outpointsForAddress, outpoint.Key())
			}

			if types.IsCoinBaseTx(tx) {
				continue
			}

			for _, in := range tx.TxIn() {
				outpoint := in.PreviousOutPoint
				address := crypto.PubkeyBytesToAddress(in.PubKey, config.Location).Hex()
				parentNumber := rawdb.ReadHeaderNumber(c.chainDb, block.ParentHash(nodeCtx))
				if parentNumber == nil {
					return errors.New("parent number cannot be found")
				}

				entry := rawdb.GetUTXO(c.chainDb, outpoint.TxHash, outpoint.Index)
				if entry == nil {
					// missing entry while tryig to add back outpoint
					continue
				}

				outpointAndDenom := &types.OutpointAndDenomination{
					TxHash:       outpoint.TxHash,
					Index:        outpoint.Index,
					Denomination: entry.Denomination,
				}

				if _, exists := addressOutpoints[address]; !exists {
					addressOutpoints[address] = rawdb.ReadOutpointsForAddress(c.chainDb, address)
				}
				addressOutpoints[address][outpointAndDenom.Key()] = outpointAndDenom
			}

		}
	}
	return nil
}
