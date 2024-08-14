// Copyright 2014 The go-ethereum Authors
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

// Package state provides a caching layer atop the Quai state trie.
package state

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state/snapshot"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/trie"
)

type revision struct {
	id           int
	journalIndex int
}

var (
	// emptyRoot is the known root hash of an empty trie.
	emptyRoot    = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	newestEtxKey = common.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffff") // max hash
	oldestEtxKey = common.HexToHash("0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffe") // max hash - 1

)

type proofList [][]byte

func (n *proofList) Put(key []byte, value []byte) error {
	*n = append(*n, value)
	return nil
}

func (n *proofList) Delete(key []byte) error {
	panic("not supported")
}

func (n *proofList) Logger() *log.Logger {
	return log.Global
}

var (
	stateMetrics *prometheus.GaugeVec
)

func init() {
	registerMetrics()
}

func registerMetrics() {
	stateMetrics = metrics_config.NewGaugeVec("StateTimes", "Time spent doing state operations")
	stateMetrics.WithLabelValues("AccountReads")
	stateMetrics.WithLabelValues("AccountHashes")
	stateMetrics.WithLabelValues("AccountUpdates")
	stateMetrics.WithLabelValues("AccountCommits")
	stateMetrics.WithLabelValues("StorageReads")
	stateMetrics.WithLabelValues("StorageHashes")
	stateMetrics.WithLabelValues("StorageUpdates")
	stateMetrics.WithLabelValues("StorageCommits")
	stateMetrics.WithLabelValues("SnapshotAccountReads")
	stateMetrics.WithLabelValues("SnapshotStorageReads")
	stateMetrics.WithLabelValues("SnapshotCommits")
}

// StateDB structs within the Quai protocol are used to store anything
// within the merkle trie. StateDBs take care of caching and storing
// nested states. It's the general query interface to retrieve:
// * Contracts
// * Accounts
type StateDB struct {
	db           Database
	utxoDb       Database
	etxDb        Database
	prefetcher   *triePrefetcher
	originalRoot common.Hash // The pre-state root, before any changes were made
	trie         Trie
	utxoTrie     Trie
	etxTrie      Trie
	hasher       crypto.KeccakState

	newAccountsAdded map[common.AddressBytes]bool
	size             *big.Int

	logger *log.Logger

	nodeLocation common.Location

	snaps         *snapshot.Tree
	snap          snapshot.Snapshot
	snapDestructs map[common.Hash]struct{}
	snapAccounts  map[common.Hash][]byte
	snapStorage   map[common.Hash]map[common.Hash][]byte

	// This map holds 'live' objects, which will get modified while processing a state transition.
	stateObjects        map[common.InternalAddress]*stateObject
	stateObjectsPending map[common.InternalAddress]struct{} // State objects finalized but not yet written to the trie
	stateObjectsDirty   map[common.InternalAddress]struct{} // State objects modified in the current execution

	// DB error.
	// State objects are used by the consensus core and VM which are
	// unable to deal with database-level errors. Any error that occurs
	// during a database read is memoized here and will eventually be returned
	// by StateDB.Commit.
	dbErr error

	// The refund counter, also used by state transitioning.
	refund uint64

	thash   common.Hash
	txIndex int
	logs    map[common.Hash][]*types.Log
	logSize uint

	preimages map[common.Hash][]byte

	// Per-transaction access list
	accessList *accessList

	// Journal of state modifications. This is the backbone of
	// Snapshot and RevertToSnapshot.
	journal        *journal
	validRevisions []revision
	nextRevisionId int

	// Measurements gathered during execution for debugging purposes
	AccountReads         time.Duration
	AccountHashes        time.Duration
	AccountUpdates       time.Duration
	AccountCommits       time.Duration
	StorageReads         time.Duration
	StorageHashes        time.Duration
	StorageUpdates       time.Duration
	StorageCommits       time.Duration
	SnapshotAccountReads time.Duration
	SnapshotStorageReads time.Duration
	SnapshotCommits      time.Duration
}

// New creates a new state from a given trie.
func New(root common.Hash, utxoRoot common.Hash, etxRoot common.Hash, db Database, utxoDb Database, etxDb Database, snaps *snapshot.Tree, nodeLocation common.Location, logger *log.Logger) (*StateDB, error) {
	tr, err := db.OpenTrie(root)
	if err != nil {
		return nil, err
	}
	utxoTr, err := utxoDb.OpenTrie(utxoRoot)
	if err != nil {
		return nil, err
	}
	etxTr, err := etxDb.OpenTrie(etxRoot)
	if err != nil {
		return nil, err
	}
	sdb := &StateDB{
		db:                  db,
		utxoDb:              utxoDb,
		etxDb:               etxDb,
		trie:                tr,
		utxoTrie:            utxoTr,
		etxTrie:             etxTr,
		originalRoot:        root,
		snaps:               snaps,
		size:                common.Big0, //  TODO: this needs to be store for each block or commited to in the state
		newAccountsAdded:    make(map[common.AddressBytes]bool),
		logger:              logger,
		stateObjects:        make(map[common.InternalAddress]*stateObject),
		stateObjectsPending: make(map[common.InternalAddress]struct{}),
		stateObjectsDirty:   make(map[common.InternalAddress]struct{}),
		logs:                make(map[common.Hash][]*types.Log),
		preimages:           make(map[common.Hash][]byte),
		journal:             newJournal(),
		accessList:          newAccessList(),
		hasher:              crypto.NewKeccakState(),
		nodeLocation:        nodeLocation,
	}
	if sdb.snaps != nil {
		if sdb.snap = sdb.snaps.Snapshot(root); sdb.snap != nil {
			sdb.snapDestructs = make(map[common.Hash]struct{})
			sdb.snapAccounts = make(map[common.Hash][]byte)
			sdb.snapStorage = make(map[common.Hash]map[common.Hash][]byte)
		}
	}
	return sdb, nil
}

// StartPrefetcher initializes a new trie prefetcher to pull in nodes from the
// state trie concurrently while the state is mutated so that when we reach the
// commit phase, most of the needed data is already hot.
func (s *StateDB) StartPrefetcher(namespace string) {
	if s.prefetcher != nil {
		s.prefetcher.close()
		s.prefetcher = nil
	}
	if s.snap != nil {
		s.prefetcher = newTriePrefetcher(s.db, s.originalRoot, namespace)
	}
}

// StopPrefetcher terminates a running prefetcher and reports any leftover stats
// from the gathered metrics.
func (s *StateDB) StopPrefetcher() {
	if s.prefetcher != nil {
		s.prefetcher.close()
		s.prefetcher = nil
	}
}

// setError remembers the first non-nil error it is called with.
func (s *StateDB) setError(err error) {
	s.logger.Error("StateDB: " + err.Error())
	if s.dbErr == nil {
		s.dbErr = err
	}
}

func (s *StateDB) Error() error {
	return s.dbErr
}

func (s *StateDB) AddLog(log *types.Log) {
	s.journal.append(addLogChange{txhash: s.thash})

	log.TxHash = s.thash
	log.TxIndex = uint(s.txIndex)
	log.Index = s.logSize
	s.logs[s.thash] = append(s.logs[s.thash], log)
	s.logSize++
}

func (s *StateDB) GetLogs(hash common.Hash, blockHash common.Hash) []*types.Log {
	logs := s.logs[hash]
	for _, l := range logs {
		l.BlockHash = blockHash
	}
	return logs
}

func (s *StateDB) Logs() []*types.Log {
	var logs []*types.Log
	for _, lgs := range s.logs {
		logs = append(logs, lgs...)
	}
	return logs
}

// AddPreimage records a SHA3 preimage seen by the VM.
func (s *StateDB) AddPreimage(hash common.Hash, preimage []byte) {
	if _, ok := s.preimages[hash]; !ok {
		s.journal.append(addPreimageChange{hash: hash})
		pi := make([]byte, len(preimage))
		copy(pi, preimage)
		s.preimages[hash] = pi
	}
}

// Preimages returns a list of SHA3 preimages that have been submitted.
func (s *StateDB) Preimages() map[common.Hash][]byte {
	return s.preimages
}

// AddRefund adds gas to the refund counter
func (s *StateDB) AddRefund(gas uint64) {
	s.journal.append(refundChange{prev: s.refund})
	s.refund += gas
}

// SubRefund removes gas from the refund counter.
// This method will panic if the refund counter goes below zero
func (s *StateDB) SubRefund(gas uint64) {
	s.journal.append(refundChange{prev: s.refund})
	if gas > s.refund {
		panic(fmt.Sprintf("Refund counter below zero (gas: %d > refund: %d)", gas, s.refund))
	}
	s.refund -= gas
}

// AddSize increases the size variable for the statedb by 1
func (s *StateDB) AddSize() {
	s.size = new(big.Int).Add(s.size, common.Big1)
}

// SubSize decreases the size variable for the statedb by 1
func (s *StateDB) SubSize() {
	s.size = new(big.Int).Sub(s.size, common.Big1)
}

// Exist reports whether the given account address exists in the state.
// Notably this also returns true for suicided accounts.
func (s *StateDB) Exist(addr common.InternalAddress) bool {
	return s.getStateObject(addr) != nil
}

// Empty returns whether the state object is either non-existent
// or empty according to the specification (balance = nonce = code = 0)
func (s *StateDB) Empty(addr common.InternalAddress) bool {
	so := s.getStateObject(addr)
	return so == nil || so.empty()
}

// GetBalance retrieves the balance from the given address or 0 if object not found
func (s *StateDB) GetBalance(addr common.InternalAddress) *big.Int {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.Balance()
	}
	return common.Big0
}
func (s *StateDB) GetNonce(addr common.InternalAddress) uint64 {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.Nonce()
	}

	return 0
}
func (s *StateDB) GetSize(addr common.InternalAddress) *big.Int {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.Size()
	}
	return common.Big0
}

func (s *StateDB) GetQuaiTrieSize() *big.Int {
	return s.size
}

// TxIndex returns the current transaction index set by Prepare.
func (s *StateDB) TxIndex() int {
	return s.txIndex
}

func (s *StateDB) GetCode(addr common.InternalAddress) []byte {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.Code(s.db)
	}
	return nil
}

func (s *StateDB) GetCodeSize(addr common.InternalAddress) int {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.CodeSize(s.db)
	}
	return 0
}

func (s *StateDB) GetCodeHash(addr common.InternalAddress) common.Hash {
	stateObject := s.getStateObject(addr)
	if stateObject == nil {
		return common.Hash{}
	}
	return common.BytesToHash(stateObject.CodeHash())
}

// GetState retrieves a value from the given account's storage trie.
func (s *StateDB) GetState(addr common.InternalAddress, hash common.Hash) common.Hash {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.GetState(s.db, hash)
	}
	return common.Hash{}
}

// GetProof returns the Merkle proof for a given account.
func (s *StateDB) GetProof(addr common.InternalAddress) ([][]byte, error) {
	return s.GetProofByHash(crypto.Keccak256Hash(addr.Bytes()))
}

// GetProofByHash returns the Merkle proof for a given account.
func (s *StateDB) GetProofByHash(addrHash common.Hash) ([][]byte, error) {
	var proof proofList
	err := s.trie.Prove(addrHash[:], 0, &proof)
	return proof, err
}

// GetStorageProof returns the Merkle proof for given storage slot.
func (s *StateDB) GetStorageProof(a common.InternalAddress, key common.Hash) ([][]byte, error) {
	var proof proofList
	trie := s.StorageTrie(a)
	if trie == nil {
		return proof, errors.New("storage trie for requested address does not exist")
	}
	err := trie.Prove(crypto.Keccak256(key.Bytes()), 0, &proof)
	return proof, err
}

// GetCommittedState retrieves a value from the given account's committed storage trie.
func (s *StateDB) GetCommittedState(addr common.InternalAddress, hash common.Hash) common.Hash {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.GetCommittedState(s.db, hash)
	}
	return common.Hash{}
}

// Database retrieves the low level database supporting the lower level trie ops.
func (s *StateDB) Database() Database {
	return s.db
}

func (s *StateDB) UTXODatabase() Database {
	return s.utxoDb
}

func (s *StateDB) ETXDatabase() Database {
	return s.etxDb
}

// StorageTrie returns the storage trie of an account.
// The return value is a copy and is nil for non-existent accounts.
func (s *StateDB) StorageTrie(addr common.InternalAddress) Trie {
	stateObject := s.getStateObject(addr)
	if stateObject == nil {
		return nil
	}
	cpy := stateObject.deepCopy(s)
	cpy.updateTrie(s.db)
	return cpy.getTrie(s.db)
}

func (s *StateDB) HasSuicided(addr common.InternalAddress) bool {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.suicided
	}
	return false
}

/*
 * SETTERS
 */

// AddBalance adds amount to the account associated with addr.
func (s *StateDB) AddBalance(addr common.InternalAddress, amount *big.Int) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.AddBalance(amount, s.nodeLocation)
	}
}

// SubBalance subtracts amount from the account associated with addr.
func (s *StateDB) SubBalance(addr common.InternalAddress, amount *big.Int) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SubBalance(amount)
	}
}

func (s *StateDB) SetBalance(addr common.InternalAddress, amount *big.Int) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetBalance(amount)
	}
}

func (s *StateDB) SetNonce(addr common.InternalAddress, nonce uint64) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetNonce(nonce)
	}
}

func (s *StateDB) SetCode(addr common.InternalAddress, code []byte) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetCode(crypto.Keccak256Hash(code), code)
	}
}

func (s *StateDB) SetState(addr common.InternalAddress, key, value common.Hash) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetState(s.db, key, value)
	}
}

// SetStorage replaces the entire storage for the specified account with given
// storage. This function should only be used for debugging.
func (s *StateDB) SetStorage(addr common.InternalAddress, storage map[common.Hash]common.Hash) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetStorage(storage)
	}
}

// Suicide marks the given account as suicided.
// This clears the account balance.
//
// The account's state object is still available until the state is committed,
// getStateObject will return a non-nil account after Suicide.
func (s *StateDB) Suicide(addr common.InternalAddress) bool {
	stateObject := s.getStateObject(addr)
	if stateObject == nil {
		return false
	}
	s.journal.append(suicideChange{
		account:     &addr,
		prev:        stateObject.suicided,
		prevbalance: new(big.Int).Set(stateObject.Balance()),
	})
	stateObject.markSuicided()
	stateObject.data.Balance = new(big.Int)
	stateObject.data.Size = new(big.Int)

	return true
}

//
// Setting, updating & deleting state object methods.
//

// updateStateObject writes the given object to the trie.
func (s *StateDB) updateStateObject(obj *stateObject) {
	// Track the amount of time wasted on updating the account from the trie
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("AccountUpdates").Add(float64(time.Since(start))) }(time.Now())
	}
	// Encode the account and update the account trie
	addr := obj.Address()

	data, err := rlp.EncodeToBytes(obj)
	if err != nil {
		panic(fmt.Errorf("can't encode object at %x: %v", addr[:], err))
	}
	if err = s.trie.TryUpdate(addr[:], data); err != nil {
		s.setError(fmt.Errorf("updateStateObject (%x) error: %v", addr[:], err))
	}

	// If state snapshotting is active, cache the data til commit. Note, this
	// update mechanism is not symmetric to the deletion, because whereas it is
	// enough to track account updates at commit time, deletions need tracking
	// at transaction boundary level to ensure we capture state clearing.
	if s.snap != nil {
		s.snapAccounts[obj.addrHash] = snapshot.SlimAccountRLP(obj.data.Nonce, obj.data.Balance, obj.data.Root, obj.data.CodeHash, obj.data.Size)
	}
}

// deleteStateObject removes the given object from the state trie.
func (s *StateDB) deleteStateObject(obj *stateObject) {
	// Track the amount of time wasted on deleting the account from the trie
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("AccountUpdates").Add(float64(time.Since(start))) }(time.Now())
	}
	// Delete the account from the trie
	addr := obj.Address()
	if err := s.trie.TryDelete(addr[:]); err != nil {
		s.setError(fmt.Errorf("deleteStateObject (%x) error: %v", addr[:], err))
	}
}

// getStateObject retrieves a state object given by the address, returning nil if
// the object is not found or was deleted in this execution context. If you need
// to differentiate between non-existent/just-deleted, use getDeletedStateObject.
func (s *StateDB) getStateObject(addr common.InternalAddress) *stateObject {
	if obj := s.getDeletedStateObject(addr); obj != nil && !obj.deleted {
		return obj
	}
	return nil
}

// GetUTXO retrieves a UTXO entry given by the hash, returning nil if the object
// is not found or was deleted in this execution context.
func (s *StateDB) GetUTXO(txHash common.Hash, outputIndex uint16) *types.UtxoEntry {
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("GetUTXO").Add(float64(time.Since(start))) }(time.Now())
	}
	enc, err := s.utxoTrie.TryGet(utxoKey(txHash, outputIndex))
	if err != nil {
		s.setError(fmt.Errorf("getUTXO (%x) error: %v", txHash, err))
		return nil
	}
	if len(enc) == 0 {
		return nil
	}
	utxo := new(types.UtxoEntry)
	if err := rlp.DecodeBytes(enc, utxo); err != nil {
		s.logger.WithFields(log.Fields{
			"hash": txHash,
			"err":  err,
		}).Error("Failed to decode UTXO entry")
		return nil
	}
	return utxo
}

// DeleteUTXO removes the given utxo from the state trie.
func (s *StateDB) DeleteUTXO(txHash common.Hash, outputIndex uint16) {
	// Track the amount of time wasted on deleting the utxo from the trie
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("DeleteUTXO").Add(float64(time.Since(start))) }(time.Now())
	}
	// Delete the utxo from the trie
	if err := s.utxoTrie.TryDelete(utxoKey(txHash, outputIndex)); err != nil {
		s.setError(fmt.Errorf("deleteUTXO (%x) error: %v", txHash, err))
	}
}

// CreateUTXO explicitly creates a UTXO entry.
func (s *StateDB) CreateUTXO(txHash common.Hash, outputIndex uint16, utxo *types.UtxoEntry) error {
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("CreateUTXO").Add(float64(time.Since(start))) }(time.Now())
	}
	// This check is largely redundant, but it's a good sanity check. Might be removed in the future.
	if err := common.CheckIfBytesAreInternalAndQiAddress(utxo.Address, s.nodeLocation); err != nil {
		return err
	}
	if utxo.Denomination > types.MaxDenomination { // sanity check
		return fmt.Errorf("tx %032x emits UTXO with value %d greater than max denomination", txHash, utxo.Denomination)
	}
	data, err := rlp.EncodeToBytes(utxo)
	if err != nil {
		panic(fmt.Errorf("can't encode UTXO entry at %x: %v", txHash, err))
	}
	if err := s.utxoTrie.TryUpdate(utxoKey(txHash, outputIndex), data); err != nil {
		s.setError(fmt.Errorf("createUTXO (%x) error: %v", txHash, err))
	}
	return nil
}

func (s *StateDB) CommitUTXOs() (common.Hash, error) {
	// Track the amount of time wasted on committing the utxos to the trie
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("CommitUTXOs").Add(float64(time.Since(start))) }(time.Now())
	}
	if s.utxoTrie == nil {
		return common.Hash{}, errors.New("UTXO trie is not initialized")
	}
	root, err := s.utxoTrie.Commit(nil)
	if err != nil {
		s.setError(fmt.Errorf("commitUTXOs error: %v", err))
	}
	return root, err
}

func (s *StateDB) UTXORoot() common.Hash {
	return s.utxoTrie.Hash()
}

func (s *StateDB) GetUTXOProof(hash common.Hash, index uint16) ([][]byte, error) {
	var proof proofList
	err := s.utxoTrie.Prove(utxoKey(hash, index), 0, &proof)
	return proof, err
}

func (s *StateDB) PushETX(etx *types.Transaction) error {
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("AddETX").Add(float64(time.Since(start))) }(time.Now())
	}
	data, err := rlp.EncodeToBytes(etx)
	if err != nil {
		return err
	}
	newestIndex, err := s.GetNewestIndex()
	if err != nil {
		return err
	}
	if err := s.etxTrie.TryUpdate(newestIndex.Bytes(), data); err != nil {
		return err
	}
	newestIndex.Add(newestIndex, big.NewInt(1))
	if err := s.etxTrie.TryUpdate(newestEtxKey[:], newestIndex.Bytes()); err != nil {
		return err
	}
	return nil
}

func (s *StateDB) PushETXs(etxs []*types.Transaction) error {
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("AddETX").Add(float64(time.Since(start))) }(time.Now())
	}
	newestIndex, err := s.GetNewestIndex()
	if err != nil {
		return err
	}
	for _, etx := range etxs {
		data, err := rlp.EncodeToBytes(etx)
		if err != nil {
			return err
		}
		if err := s.etxTrie.TryUpdate(newestIndex.Bytes(), data); err != nil {
			return err
		}
		newestIndex.Add(newestIndex, big.NewInt(1))
	}
	if err := s.etxTrie.TryUpdate(newestEtxKey[:], newestIndex.Bytes()); err != nil {
		return err
	}
	return nil
}

func (s *StateDB) PopETX() (*types.Transaction, error) {
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("PopETX").Add(float64(time.Since(start))) }(time.Now())
	}
	oldestIndex, err := s.GetOldestIndex()
	if err != nil {
		return nil, err
	}
	enc, err := s.etxTrie.TryGet(oldestIndex.Bytes())
	if err != nil {
		return nil, err
	}
	if len(enc) == 0 {
		return nil, nil
	}
	etx := new(types.Transaction)
	if err := rlp.DecodeBytes(enc, etx); err != nil {
		return nil, err
	}
	etx.SetTo(common.BytesToAddress(etx.To().Bytes(), s.nodeLocation))
	if err := s.etxTrie.TryDelete(oldestIndex.Bytes()); err != nil {
		return nil, err
	}
	oldestIndex.Add(oldestIndex, big.NewInt(1))
	if err := s.etxTrie.TryUpdate(oldestEtxKey[:], oldestIndex.Bytes()); err != nil {
		return nil, err
	}
	return etx, nil
}

func (s *StateDB) ReadETX(index *big.Int) (*types.Transaction, error) {
	enc, err := s.etxTrie.TryGet(index.Bytes())
	if err != nil {
		return nil, err
	}
	if len(enc) == 0 {
		return nil, nil
	}
	etx := new(types.Transaction)
	if err := rlp.DecodeBytes(enc, etx); err != nil {
		return nil, err
	}
	etx.SetTo(common.BytesToAddress(etx.To().Bytes(), s.nodeLocation))
	return etx, nil
}

func (s *StateDB) GetNewestIndex() (*big.Int, error) {
	b, err := s.etxTrie.TryGet(newestEtxKey[:])
	if err != nil {
		s.setError(fmt.Errorf("getNewestIndex error: %v", err))
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

func (s *StateDB) GetOldestIndex() (*big.Int, error) {
	b, err := s.etxTrie.TryGet(oldestEtxKey[:])
	if err != nil {
		s.setError(fmt.Errorf("getOldestIndex error: %v", err))
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

func (s *StateDB) ETXRoot() common.Hash {
	return s.etxTrie.Hash()
}

func (s *StateDB) CommitETXs() (common.Hash, error) {
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("CommitETXs").Add(float64(time.Since(start))) }(time.Now())
	}
	if s.etxTrie == nil {
		return common.Hash{}, errors.New("ETX trie is not initialized")
	}
	root, err := s.etxTrie.Commit(nil)
	if err != nil {
		s.setError(fmt.Errorf("commitETXs error: %v", err))
	}
	return root, err
}

// getDeletedStateObject is similar to getStateObject, but instead of returning
// nil for a deleted state object, it returns the actual object with the deleted
// flag set. This is needed by the state journal to revert to the correct s-
// destructed object instead of wiping all knowledge about the state object.
func (s *StateDB) getDeletedStateObject(addr common.InternalAddress) *stateObject {
	// Prefer live objects if any is available
	if obj := s.stateObjects[addr]; obj != nil {
		return obj
	}
	// If no live objects are available, attempt to use snapshots
	var (
		data *Account
		err  error
	)
	if s.snap != nil {
		if metrics_config.MetricsEnabled() {
			defer func(start time.Time) {
				stateMetrics.WithLabelValues("SnapshotAccountReads").Add(float64(time.Since(start)))
			}(time.Now())
		}
		var acc *snapshot.Account
		if acc, err = s.snap.Account(crypto.HashData(s.hasher, addr.Bytes())); err == nil {
			if acc == nil {
				return nil
			}
			data = &Account{
				Nonce:    acc.Nonce,
				Balance:  acc.Balance,
				CodeHash: acc.CodeHash,
				Root:     common.BytesToHash(acc.Root),
				Size:     acc.Size,
			}
			if len(data.CodeHash) == 0 {
				data.CodeHash = emptyCodeHash
			}
			if data.Root == (common.Hash{}) {
				data.Root = emptyRoot
			}
		}
	}
	// If snapshot unavailable or reading from it failed, load from the database
	if s.snap == nil || err != nil {
		if metrics_config.MetricsEnabled() {
			defer func(start time.Time) { stateMetrics.WithLabelValues("AccountReads").Add(float64(time.Since(start))) }(time.Now())
		}
		enc, err := s.trie.TryGet(addr.Bytes())
		if err != nil {
			s.setError(fmt.Errorf("getDeleteStateObject (%x) error: %v", addr.Bytes(), err))
			return nil
		}
		if len(enc) == 0 {
			return nil
		}
		data = new(Account)
		if err := rlp.DecodeBytes(enc, data); err != nil {
			s.logger.WithFields(log.Fields{
				"addr": addr,
				"err":  err,
			}).Error("Failed to decode state object")
			return nil
		}
	}
	// Insert into the live set
	obj := newObject(s, addr, *data)
	s.setStateObject(obj)
	return obj
}

func (s *StateDB) setStateObject(object *stateObject) {
	s.stateObjects[object.Address()] = object
}

// GetOrNewStateObject retrieves a state object or create a new state object if nil.
func (s *StateDB) GetOrNewStateObject(addr common.InternalAddress) *stateObject {
	stateObject := s.getStateObject(addr)
	if stateObject == nil {
		stateObject, _ = s.createObject(addr)
	}
	return stateObject
}

// createObject creates a new state object. If there is an existing account with
// the given address, it is overwritten and returned as the second return value.
func (s *StateDB) createObject(addr common.InternalAddress) (newobj, prev *stateObject) {
	if !common.IsInChainScope(addr.Bytes(), s.nodeLocation) || !addr.IsInQuaiLedgerScope() {
		s.setError(fmt.Errorf("createObject (%x) error: %v", addr.Bytes(), common.ErrInvalidScope))
		return nil, nil
	}
	prev = s.getDeletedStateObject(addr) // Note, prev might have been deleted, we need that!

	var prevdestruct bool
	if s.snap != nil && prev != nil {
		_, prevdestruct = s.snapDestructs[prev.addrHash]
		if !prevdestruct {
			s.snapDestructs[prev.addrHash] = struct{}{}
		}
	}
	newobj = newObject(s, addr, Account{})
	if prev == nil {
		// Add this new account to the collection of accounts that might be
		// created during the execution of the state in this block
		s.newAccountsAdded[addr.Bytes20()] = true
		s.journal.append(createObjectChange{account: &addr})
	} else {
		s.journal.append(resetObjectChange{prev: prev, prevdestruct: prevdestruct})
	}
	s.setStateObject(newobj)
	if prev != nil && !prev.deleted {
		return newobj, prev
	}
	return newobj, nil
}

// CreateAccount explicitly creates a state object. If a state object with the address
// already exists the balance is carried over to the new account.
//
// CreateAccount is called during the EVM CREATE operation. The situation might arise that
// a contract does the following:
//
//  1. sends funds to sha(account ++ (nonce + 1))
//  2. tx_create(sha(account ++ nonce)) (note that this gets the address of 1)
//
// Carrying over the balance ensures that Ether doesn't disappear.
func (s *StateDB) CreateAccount(addr common.InternalAddress) {
	newObj, prev := s.createObject(addr)
	if prev != nil {
		newObj.setBalance(prev.data.Balance)
		newObj.setSize(prev.data.Size)
	}
}

func (db *StateDB) ForEachStorage(addr common.InternalAddress, cb func(key, value common.Hash) bool) error {
	so := db.getStateObject(addr)
	if so == nil {
		return nil
	}
	it := trie.NewIterator(so.getTrie(db.db).NodeIterator(nil))

	for it.Next() {
		key := common.BytesToHash(db.trie.GetKey(it.Key))
		if value, dirty := so.dirtyStorage[key]; dirty {
			if !cb(key, value) {
				return nil
			}
			continue
		}

		if len(it.Value) > 0 {
			_, content, _, err := rlp.Split(it.Value)
			if err != nil {
				return err
			}
			if !cb(key, common.BytesToHash(content)) {
				return nil
			}
		}
	}
	return nil
}

// Copy creates a deep, independent copy of the state.
// Snapshots of the copied state cannot be applied to the copy.
func (s *StateDB) Copy() *StateDB {
	// Copy all the basic fields, initialize the memory ones
	state := &StateDB{
		db:                  s.db,
		trie:                s.db.CopyTrie(s.trie),
		utxoTrie:            s.utxoDb.CopyTrie(s.utxoTrie),
		utxoDb:              s.utxoDb,
		etxTrie:             s.etxDb.CopyTrie(s.etxTrie),
		etxDb:               s.etxDb,
		size:                new(big.Int).Set(s.size),
		newAccountsAdded:    make(map[common.AddressBytes]bool, len(s.newAccountsAdded)),
		stateObjects:        make(map[common.InternalAddress]*stateObject, len(s.journal.dirties)),
		stateObjectsPending: make(map[common.InternalAddress]struct{}, len(s.stateObjectsPending)),
		stateObjectsDirty:   make(map[common.InternalAddress]struct{}, len(s.journal.dirties)),
		refund:              s.refund,
		logs:                make(map[common.Hash][]*types.Log, len(s.logs)),
		logSize:             s.logSize,
		preimages:           make(map[common.Hash][]byte, len(s.preimages)),
		journal:             newJournal(),
		hasher:              crypto.NewKeccakState(),
		nodeLocation:        s.nodeLocation,
		logger:              s.logger,
	}
	// Copy the dirty states, logs, and preimages
	for addr := range s.journal.dirties {
		// In the Finalise-method, there is a case where an object is in the journal but not
		// in the stateObjects: OOG after touch on ripeMD. Thus, we need to check for
		// nil
		if object, exist := s.stateObjects[addr]; exist {
			// Even though the original object is dirty, we are not copying the journal,
			// so we need to make sure that anyside effect the journal would have caused
			// during a commit (or similar op) is already applied to the copy.
			state.stateObjects[addr] = object.deepCopy(state)

			state.stateObjectsDirty[addr] = struct{}{}   // Mark the copy dirty to force internal (code/state) commits
			state.stateObjectsPending[addr] = struct{}{} // Mark the copy pending to force external (account) commits
		}
	}
	// Above, we don't copy the actual journal. This means that if the copy is copied, the
	// loop above will be a no-op, since the copy's journal is empty.
	// Thus, here we iterate over stateObjects, to enable copies of copies
	for addr := range s.stateObjectsPending {
		if _, exist := state.stateObjects[addr]; !exist {
			state.stateObjects[addr] = s.stateObjects[addr].deepCopy(state)
		}
		state.stateObjectsPending[addr] = struct{}{}
	}
	for addr := range s.stateObjectsDirty {
		if _, exist := state.stateObjects[addr]; !exist {
			state.stateObjects[addr] = s.stateObjects[addr].deepCopy(state)
		}
		state.stateObjectsDirty[addr] = struct{}{}
	}
	for hash, logs := range s.logs {
		cpy := make([]*types.Log, len(logs))
		for i, l := range logs {
			cpy[i] = new(types.Log)
			*cpy[i] = *l
		}
		state.logs[hash] = cpy
	}
	for hash, preimage := range s.preimages {
		state.preimages[hash] = preimage
	}
	// Do we need to copy the access list? In practice: No. At the start of a
	// transaction, the access list is empty. In practice, we only ever copy state
	// _between_ transactions/blocks, never in the middle of a transaction.
	// However, it doesn't cost us much to copy an empty list, so we do it anyway
	// to not blow up if we ever decide copy it in the middle of a transaction
	state.accessList = s.accessList.Copy()

	// If there's a prefetcher running, make an inactive copy of it that can
	// only access data but does not actively preload (since the user will not
	// know that they need to explicitly terminate an active copy).
	if s.prefetcher != nil {
		state.prefetcher = s.prefetcher.copy()
	}
	// If len of the new accounts added is non zero, we can copy the map into
	// the state variable
	if len(s.newAccountsAdded) > 0 {
		for acc, val := range s.newAccountsAdded {
			state.newAccountsAdded[acc] = val
		}
	}
	if s.snaps != nil {
		// In order for the miner to be able to use and make additions
		// to the snapshot tree, we need to copy that aswell.
		// Otherwise, any block mined by ourselves will cause gaps in the tree,
		// and force the miner to operate trie-backed only
		state.snaps = s.snaps
		state.snap = s.snap
		// deep copy needed
		state.snapDestructs = make(map[common.Hash]struct{})
		for k, v := range s.snapDestructs {
			state.snapDestructs[k] = v
		}
		state.snapAccounts = make(map[common.Hash][]byte)
		for k, v := range s.snapAccounts {
			state.snapAccounts[k] = v
		}
		state.snapStorage = make(map[common.Hash]map[common.Hash][]byte)
		for k, v := range s.snapStorage {
			temp := make(map[common.Hash][]byte)
			for kk, vv := range v {
				temp[kk] = vv
			}
			state.snapStorage[k] = temp
		}
	}
	return state
}

// Snapshot returns an identifier for the current revision of the state.
func (s *StateDB) Snapshot() int {
	id := s.nextRevisionId
	s.nextRevisionId++
	s.validRevisions = append(s.validRevisions, revision{id, s.journal.length()})
	return id
}

// RevertToSnapshot reverts all state changes made since the given revision.
func (s *StateDB) RevertToSnapshot(revid int) {
	// Find the snapshot in the stack of valid snapshots.
	idx := sort.Search(len(s.validRevisions), func(i int) bool {
		return s.validRevisions[i].id >= revid
	})
	if idx == len(s.validRevisions) || s.validRevisions[idx].id != revid {
		panic(fmt.Errorf("revision id %v cannot be reverted", revid))
	}
	snapshot := s.validRevisions[idx].journalIndex

	// Replay the journal to undo changes and remove invalidated snapshots
	s.journal.revert(s, snapshot)
	s.validRevisions = s.validRevisions[:idx]
}

// GetRefund returns the current value of the refund counter.
func (s *StateDB) GetRefund() uint64 {
	return s.refund
}

// Finalise finalises the state by removing the s destructed objects and clears
// the journal as well as the refunds. Finalise, however, will not push any updates
// into the tries just yet. Only IntermediateRoot or Commit will do that.
func (s *StateDB) Finalise(deleteEmptyObjects bool) {
	addressesToPrefetch := make([][]byte, 0, len(s.journal.dirties))
	for addr := range s.journal.dirties {
		obj, exist := s.stateObjects[addr]
		if !exist {
			// ripeMD is 'touched' at block 1714175, in tx 0x1237f737031e40bcde4a8b7e717b2d15e3ecadfe49bb1bbc71ee9deb09c6fcf2
			// That tx goes out of gas, and although the notion of 'touched' does not exist there, the
			// touch-event will still be recorded in the journal. Since ripeMD is a special snowflake,
			// it will persist in the journal even though the journal is reverted. In this special circumstance,
			// it may exist in `s.journal.dirties` but not in `s.stateObjects`.
			// Thus, we can safely ignore it here
			continue
		}
		if obj.suicided || (deleteEmptyObjects && obj.empty()) {
			obj.deleted = true

			// If state snapshotting is active, also mark the destruction there.
			// Note, we can't do this only at the end of a block because multiple
			// transactions within the same block might self destruct and then
			// ressurrect an account; but the snapshotter needs both events.
			if s.snap != nil {
				s.snapDestructs[obj.addrHash] = struct{}{} // We need to maintain account deletions explicitly (will remain set indefinitely)
				delete(s.snapAccounts, obj.addrHash)       // Clear out any previously updated account data (may be recreated via a ressurrect)
				delete(s.snapStorage, obj.addrHash)        // Clear out any previously updated storage data (may be recreated via a ressurrect)
			}
		} else {
			obj.finalise(true) // Prefetch slots in the background
		}
		s.stateObjectsPending[addr] = struct{}{}
		s.stateObjectsDirty[addr] = struct{}{}

		// At this point, also ship the address off to the precacher. The precacher
		// will start loading tries, and when the change is eventually committed,
		// the commit-phase will be a lot faster
		addressesToPrefetch = append(addressesToPrefetch, common.CopyBytes(addr[:])) // Copy needed for closure
	}
	if s.prefetcher != nil && len(addressesToPrefetch) > 0 {
		s.prefetcher.prefetch(s.originalRoot, addressesToPrefetch)
	}
	// Invalidate journal because reverting across transactions is not allowed.
	s.clearJournalAndRefund()
}

// IntermediateRoot computes the current root hash of the state trie.
// It is called in between transactions to get the root hash that
// goes into transaction receipts.
func (s *StateDB) IntermediateRoot(deleteEmptyObjects bool) common.Hash {
	// Finalise all the dirty storage states and write them into the tries
	s.Finalise(deleteEmptyObjects)

	// If there was a trie prefetcher operating, it gets aborted and irrevocably
	// modified after we start retrieving tries. Remove it from the statedb after
	// this round of use.
	prefetcher := s.prefetcher
	if s.prefetcher != nil {
		defer func() {
			s.prefetcher.close()
			s.prefetcher = nil
		}()
	}
	// Although naively it makes sense to retrieve the account trie and then do
	// the contract storage and account updates sequentially, that short circuits
	// the account prefetcher. Instead, let's process all the storage updates
	// first, giving the account prefeches just a few more milliseconds of time
	// to pull useful data from disk.
	for addr := range s.stateObjectsPending {
		if obj := s.stateObjects[addr]; !obj.deleted {
			obj.updateRoot(s.db)
		}
	}
	// Now we're about to start to write changes to the trie. The trie is so far
	// _untouched_. We can check with the prefetcher, if it can give us a trie
	// which has the same root, but also has some content loaded into it.
	if prefetcher != nil {
		if trie := prefetcher.trie(s.originalRoot); trie != nil {
			s.trie = trie
		}
	}
	usedAddrs := make([][]byte, 0, len(s.stateObjectsPending))
	for addr := range s.stateObjectsPending {
		if obj := s.stateObjects[addr]; obj.deleted {
			s.deleteStateObject(obj)
			// In the case of deletion because the balance was nil or that the
			// address was deleted using opSuicide we need to lower the size of
			// the quai state trie if this was not a newAccont created during the
			// execution of this block
			_, exists := s.newAccountsAdded[addr.Bytes20()]
			if !exists {
				s.SubSize()
			}
		} else {
			s.updateStateObject(obj)
			// If this address exists in the newAccountsAdded map, we can
			// increase the size of the statedb
			_, exists := s.newAccountsAdded[addr.Bytes20()]
			if exists {
				s.AddSize()
			}
		}
		usedAddrs = append(usedAddrs, common.CopyBytes(addr[:])) // Copy needed for closure
	}
	if prefetcher != nil {
		prefetcher.used(s.originalRoot, usedAddrs)
	}
	if len(s.stateObjectsPending) > 0 {
		s.stateObjectsPending = make(map[common.InternalAddress]struct{})
	}
	// Track the amount of time wasted on hashing the account trie
	if metrics_config.MetricsEnabled() {
		defer func(start time.Time) { stateMetrics.WithLabelValues("AccountHashes").Add(float64(time.Since(start))) }(time.Now())
	}

	log.Global.Error("No of new accounts added to the state", s.size)

	return s.trie.Hash()
}

// Prepare sets the current transaction hash and index which are
// used when the EVM emits new state logs.
func (s *StateDB) Prepare(thash common.Hash, ti int) {
	s.thash = thash
	s.txIndex = ti
	s.accessList = newAccessList()
}

func (s *StateDB) clearJournalAndRefund() {
	if len(s.journal.entries) > 0 {
		s.journal = newJournal()
		s.refund = 0
	}
	s.validRevisions = s.validRevisions[:0] // Snapshots can be created without journal entires
}

// Commit writes the state to the underlying in-memory trie database.
func (s *StateDB) Commit(deleteEmptyObjects bool) (common.Hash, error) {
	if s.dbErr != nil {
		return common.Hash{}, fmt.Errorf("commit aborted due to earlier error: %v", s.dbErr)
	}
	// Finalize any pending changes and merge everything into the tries
	s.IntermediateRoot(deleteEmptyObjects)

	// Commit objects to the trie, measuring the elapsed time
	codeWriter := s.db.TrieDB().DiskDB().NewBatch()
	for addr := range s.stateObjectsDirty {
		if obj := s.stateObjects[addr]; !obj.deleted {
			// Write any contract code associated with the state object
			if obj.code != nil && obj.dirtyCode {
				rawdb.WriteCode(codeWriter, common.BytesToHash(obj.CodeHash()), obj.code)
				obj.dirtyCode = false
			}
			// Write any storage changes in the state object to its storage trie
			if err := obj.CommitTrie(s.db); err != nil {
				return common.Hash{}, err
			}
		}
	}
	if len(s.stateObjectsDirty) > 0 {
		s.stateObjectsDirty = make(map[common.InternalAddress]struct{})
	}
	if codeWriter.ValueSize() > 0 {
		if err := codeWriter.Write(); err != nil {
			s.logger.WithField("err", err).Fatal("Failed to commit dirty codes")
		}
	}
	// Write the account trie changes, measuing the amount of wasted time
	var start time.Time
	if metrics_config.MetricsEnabled() {
		start = time.Now()
	}
	// Write the account trie changes, measuing the amount of wasted time
	// The onleaf func is called _serially_, so we can reuse the same account
	// for unmarshalling every time.
	var account Account
	root, err := s.trie.Commit(func(_ [][]byte, _ []byte, leaf []byte, parent common.Hash) error {
		if err := rlp.DecodeBytes(leaf, &account); err != nil {
			return nil
		}
		if account.Root != emptyRoot {
			s.db.TrieDB().Reference(account.Root, parent)
		}
		return nil
	})
	if metrics_config.MetricsEnabled() {
		stateMetrics.WithLabelValues("AccountCommits").Add(float64(time.Since(start)))
	}
	// If snapshotting is enabled, update the snapshot tree with this new version
	if s.snap != nil {
		if metrics_config.MetricsEnabled() {
			defer func(start time.Time) { stateMetrics.WithLabelValues("SnapshotCommits").Add(float64(time.Since(start))) }(time.Now())
		}
		// Only update if there's a state transition (skip empty Clique blocks)
		if parent := s.snap.Root(); parent != root {
			if err := s.snaps.Update(root, parent, s.snapDestructs, s.snapAccounts, s.snapStorage); err != nil {
				s.logger.WithFields(log.Fields{
					"root":   root,
					"parent": parent,
					"err":    err,
				}).Error("Failed to update snapshot tree")
			}
			// Keep 128 diff layers in the memory, persistent layer is 129th.
			// - head layer is paired with HEAD state
			// - head-1 layer is paired with HEAD-1 state
			// - head-127 layer(bottom-most diff layer) is paired with HEAD-127 state
			if err := s.snaps.Cap(root, 128); err != nil {
				s.logger.WithFields(log.Fields{
					"root":   root,
					"layers": 128,
					"err":    err,
				}).Warn("Failed to cap snapshot tree")
			}
		}
		s.snap, s.snapDestructs, s.snapAccounts, s.snapStorage = nil, nil, nil, nil
	}
	return root, err
}

// PrepareAccessList handles the preparatory steps for executing a state transition
//
// - Add sender to access list
// - Add destination to access list
// - Add precompiles to access list
// - Add the contents of the optional tx access list
func (s *StateDB) PrepareAccessList(sender common.Address, dst *common.Address, precompiles []common.Address, list types.AccessList) {
	s.AddAddressToAccessList(sender)
	if dst != nil {
		s.AddAddressToAccessList(*dst)
		// If it's a create-tx, the destination will be added inside evm.create
	}
	for _, addr := range precompiles {
		s.AddAddressToAccessList(addr)
	}
	for _, el := range list {
		s.AddAddressToAccessList(el.Address)
		for _, key := range el.StorageKeys {
			s.AddSlotToAccessList(el.Address, key)
		}
	}
}

// AddAddressToAccessList adds the given address to the access list
func (s *StateDB) AddAddressToAccessList(addr common.Address) {
	addrBytes := addr.Bytes20()
	if s.accessList.AddAddress(addrBytes) {
		s.journal.append(accessListAddAccountChange{&addrBytes})
	}
}

// AddSlotToAccessList adds the given (address, slot)-tuple to the access list
func (s *StateDB) AddSlotToAccessList(addr common.Address, slot common.Hash) {
	addrBytes := addr.Bytes20()
	addrMod, slotMod := s.accessList.AddSlot(addrBytes, slot)
	if addrMod {
		// In practice, this should not happen, since there is no way to enter the
		// scope of 'address' without having the 'address' become already added
		// to the access list (via call-variant, create, etc).
		// Better safe than sorry, though
		s.journal.append(accessListAddAccountChange{&addrBytes})
	}
	if slotMod {
		s.journal.append(accessListAddSlotChange{
			address: &addrBytes,
			slot:    &slot,
		})
	}
}

// AddressInAccessList returns true if the given address is in the access list.
func (s *StateDB) AddressInAccessList(addr common.Address) bool {
	return s.accessList.ContainsAddress(addr.Bytes20())
}

// SlotInAccessList returns true if the given (address, slot)-tuple is in the access list.
func (s *StateDB) SlotInAccessList(addr common.Address, slot common.Hash) (addressPresent bool, slotPresent bool) {
	return s.accessList.Contains(addr.Bytes20(), slot)
}

// This can be optimized via VLQ encoding as btcd has done
func utxoKey(hash common.Hash, index uint16) []byte {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, index)
	return append(indexBytes, hash.Bytes()...)
}
