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

// Package rawdb contains a collection of low level database accessors.
package rawdb

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/dominant-strategies/go-quai/common"
)

// The fields below define the low level database schema prefixing.
var (
	// databaseVersionKey tracks the current database version.
	databaseVersionKey = []byte("DatabaseVersion")

	// headHeaderKey tracks the latest known header's hash.
	headHeaderKey = []byte("LastHeader")

	// headBlockKey tracks the latest known full block's hash.
	headWorkObjectKey = []byte("LastWorkObject")

	// headersHashKey tracks the latest known headers hash in Blockchain.
	headsHashesKey = []byte("HeadersHash")

	// phHead tracks the latest known pending headers hash in Blockchain.
	phHeadKey = []byte("PhHead")

	// snapshotDisabledKey flags that the snapshot should not be maintained due to initial sync.
	snapshotDisabledKey = []byte("SnapshotDisabled")

	// snapshotRootKey tracks the hash of the last snapshot.
	snapshotRootKey = []byte("SnapshotRoot")

	// snapshotJournalKey tracks the in-memory diff layers across restarts.
	snapshotJournalKey = []byte("SnapshotJournal")

	// snapshotGeneratorKey tracks the snapshot generation marker across restarts.
	snapshotGeneratorKey = []byte("SnapshotGenerator")

	// snapshotRecoveryKey tracks the snapshot recovery marker across restarts.
	snapshotRecoveryKey = []byte("SnapshotRecovery")

	// snapshotSyncStatusKey tracks the snapshot sync status across restarts.
	snapshotSyncStatusKey = []byte("SnapshotSyncStatus")

	// uncleanShutdownKey tracks the list of local crashes
	uncleanShutdownKey = []byte("unclean-shutdown") // config prefix for the db

	// genesisHashesKey tracks the list of genesis hashes
	genesisHashesKey = []byte("GenesisHashes")

	lastTrimmedBlockPrefix = []byte("ltb")

	// Data item prefixes (use single byte to avoid mixing data types, avoid `i`, used for indexes).
	headerPrefix       = []byte("h") // headerPrefix + num (uint64 big endian) + hash -> header
	headerTDSuffix     = []byte("t") // headerPrefix + num (uint64 big endian) + hash + headerTDSuffix -> td
	headerHashSuffix   = []byte("n") // headerPrefix + num (uint64 big endian) + headerHashSuffix -> hash
	headerNumberPrefix = []byte("H") // headerNumberPrefix + hash -> num (uint64 big endian)

	donorHashPrefix                = []byte("dh")     // donorHashPrefix + hash -> header
	workshareHashToBlockHashPrefix = []byte("wsh2bh") // workShareHashToBlockHashPrefix + hash -> block hash

	pendingHeaderPrefix             = []byte("ph")    // pendingHeaderPrefix + hash -> header
	pbBodyPrefix                    = []byte("pb")    // pbBodyPrefix + hash -> *types.Body
	pbBodyHashPrefix                = []byte("pbKey") // pbBodyPrefix -> []common.Hash
	terminiPrefix                   = []byte("tk")    //terminiPrefix + hash -> []common.Hash
	workObjectBodyPrefix            = []byte("wb")    //workObjectBodyPrefix + hash -> []common.Hash
	badHashesListPrefix             = []byte("bh")
	inboundEtxsPrefix               = []byte("ie")    // inboundEtxsPrefix + hash -> types.Transactions
	AddressUtxosPrefix              = []byte("au")    // addressUtxosPrefix + address -> []types.UtxoEntry
	AddressUtxosWithoutHeightPrefix = []byte("auwh")  // addressUTxosWithoutHeightPrefix + address -> []types.UtxoEntry
	AddressLockupsPrefix            = []byte("al")    // addressLockupsPrefix + address -> []types.Lockup
	utxoToBlockHeightPrefix         = []byte("ub")    // utxoToBlockHeightPrefix + hash -> uint64
	processedStatePrefix            = []byte("ps")    // processedStatePrefix + hash -> boolean
	multiSetPrefix                  = []byte("ms")    // multiSetPrefix + hash -> multiset
	UtxoPrefix                      = []byte("ut")    // outpointPrefix + hash -> types.Outpoint
	tokenChoicePrefix               = []byte("tc")    // tokenChoicePrefix + hash -> tokenChoices
	utxoPrefix                      = []byte("ut")    // outpointPrefix + hash -> types.Outpoint
	spentUTXOsPrefix                = []byte("sutxo") // spentUTXOsPrefix + hash -> []types.SpentTxOut
	trimmedUTXOsPrefix              = []byte("tutxo") // trimmedUTXOsPrefix + hash -> []types.SpentTxOut
	createdUTXOsPrefix              = []byte("cutxo") // createdUTXOsPrefix + hash -> []common.Hash
	prunedUTXOKeysPrefix            = []byte("putxo") // prunedUTXOKeysPrefix + num (uint64 big endian) -> hash
	prunedPrefix                    = []byte("pru")   // prunedPrefix + hash -> pruned
	utxoSetSizePrefix               = []byte("us")    // utxoSetSizePrefix + hash -> uint64
	blockReceiptsPrefix             = []byte("r")     // blockReceiptsPrefix + num (uint64 big endian) + hash -> block receipts
	pendingEtxsPrefix               = []byte("pe")    // pendingEtxsPrefix + hash -> PendingEtxs at block
	pendingEtxsRollupPrefix         = []byte("pr")    // pendingEtxsRollupPrefix + hash -> PendingEtxsRollup at block
	manifestPrefix                  = []byte("ma")    // manifestPrefix + hash -> Manifest at block
	interlinkPrefix                 = []byte("il")    // interlinkPrefix + hash -> Interlink at block
	bloomPrefix                     = []byte("bl")    // bloomPrefix + hash -> bloom at block

	txLookupPrefix        = []byte("l") // txLookupPrefix + hash -> transaction/receipt lookup metadata
	BloomBitsPrefix       = []byte("B") // bloomBitsPrefix + bit (uint16 big endian) + section (uint64 big endian) + hash -> bloom bits
	SnapshotAccountPrefix = []byte("a") // SnapshotAccountPrefix + account hash -> account trie value
	SnapshotStoragePrefix = []byte("o") // SnapshotStoragePrefix + account hash + storage hash -> storage trie value
	CodePrefix            = []byte("c") // CodePrefix + code hash -> account code

	preimagePrefix = []byte("secure-key-")  // preimagePrefix + hash -> preimage
	configPrefix   = []byte("quai-config-") // config prefix for the db

	// Chain index prefixes (use `i` + single byte to avoid mixing data types).
	BloomBitsIndexPrefix         = []byte("iB")  // BloomBitsIndexPrefix is the data table of a chain indexer to track its progress
	CoinbaseLockupPrefix         = []byte("cl")  // coinbaseLockupPrefix + ownerContract + beneficiaryMiner + lockupByte + epoch -> lockup
	createdCoinbaseLockupsPrefix = []byte("ccl") // createdCoinbaseLockupsPrefix + hash -> [][]byte
	deletedCoinbaseLockupsPrefix = []byte("dcl") // deletedCoinbaseLockupsPrefix + hash -> [][]byte
	supplyAnalyticsPrefix        = []byte("sa")  // supplyAnalyticsKey + hash -> SupplyAnalytics
	lockupDeltasPrefix           = []byte("ld")  // lockupDeltasPrefix + hash -> []types.LockupDelta
)

const (
	// freezerHashTable indicates the name of the freezer canonical hash table.
	freezerHashTable = "hashes"

	// freezerBodiesTable indicates the name of the freezer block body table.
	freezerBodiesTable = "bodies"

	// freezerReceiptTable indicates the name of the freezer receipts table.
	freezerReceiptTable = "receipts"

	// freezerDifficultyTable indicates the name of the freezer total difficulty table.
	freezerDifficultyTable = "diffs"
)

// FreezerNoSnappy configures whether compression is disabled for the ancient-tables.
// Hashes and difficulties don't compress well.
var FreezerNoSnappy = map[string]bool{
	freezerHashTable:       true,
	freezerBodiesTable:     false,
	freezerReceiptTable:    false,
	freezerDifficultyTable: true,
}

// LegacyTxLookupEntry is the legacy TxLookupEntry definition with some unnecessary
// fields.
type LegacyTxLookupEntry struct {
	BlockHash  common.Hash
	BlockIndex uint64
	Index      uint64
}

func (l LegacyTxLookupEntry) ProtoEncode() (ProtoLegacyTxLookupEntry, error) {
	blockHash := l.BlockHash.ProtoEncode()
	return ProtoLegacyTxLookupEntry{
		Hash:       blockHash,
		BlockIndex: l.BlockIndex,
		Index:      l.Index,
	}, nil
}

func (l *LegacyTxLookupEntry) ProtoDecode(data *ProtoLegacyTxLookupEntry) error {
	l.BlockHash = common.Hash{}
	l.BlockHash.ProtoDecode(data.Hash)
	l.BlockIndex = data.BlockIndex
	l.Index = data.Index
	return nil
}

// encodeBlockNumber encodes a block number as big endian uint64
func encodeBlockNumber(number uint64) []byte {
	enc := make([]byte, 8)
	binary.BigEndian.PutUint64(enc, number)
	return enc
}

// headerKeyPrefix = headerPrefix + num (uint64 big endian)
func headerKeyPrefix(number uint64) []byte {
	return append(headerPrefix, encodeBlockNumber(number)...)
}

// headerKey = headerPrefix + num (uint64 big endian) + hash
func headerKey(number uint64, hash common.Hash) []byte {
	return append(append(headerPrefix, encodeBlockNumber(number)...), hash.Bytes()...)
}

// donorHashKey = donorHashPrefix + hash
func donorHashKey(hash common.Hash) []byte {
	return append(donorHashPrefix, hash.Bytes()...)
}

// workShareHashToBlockHashKey = "wsh2bh" + hash
func workShareHashToBlockHashKey(hash common.Hash) []byte {
	return append(workshareHashToBlockHashPrefix, hash.Bytes()...)
}

// terminiKey = domPendingHeaderPrefix + hash
func terminiKey(hash common.Hash) []byte {
	return append(terminiPrefix, hash.Bytes()...)
}

// workObjectBodyKey = workObjectBodyPrefix + hash
func workObjectBodyKey(hash common.Hash) []byte {
	return append(workObjectBodyPrefix, hash.Bytes()...)
}

// pbBodyKey = pbBodyPrefix + hash
func pbBodyKey(hash common.Hash) []byte {
	return append(pbBodyPrefix, hash.Bytes()...)
}

// pbBodyHashKey = pbBodyPrefix
func pbBodyHashKey() []byte {
	return pbBodyHashPrefix
}

// headerHashKey = headerPrefix + num (uint64 big endian) + headerHashSuffix
func headerHashKey(number uint64) []byte {
	return append(append(headerPrefix, encodeBlockNumber(number)...), headerHashSuffix...)
}

// headerNumberKey = headerNumberPrefix + hash
func headerNumberKey(hash common.Hash) []byte {
	return append(headerNumberPrefix, hash.Bytes()...)
}

func processedStateKey(hash common.Hash) []byte {
	return append(processedStatePrefix, hash.Bytes()...)
}

// blockReceiptsKey = blockReceiptsPrefix + num (uint64 big endian) + hash
func blockReceiptsKey(number uint64, hash common.Hash) []byte {
	return append(append(blockReceiptsPrefix, encodeBlockNumber(number)...), hash.Bytes()...)
}

// txLookupKey = txLookupPrefix + hash
func txLookupKey(hash common.Hash) []byte {
	return append(txLookupPrefix, hash.Bytes()...)
}

// accountSnapshotKey = SnapshotAccountPrefix + hash
func accountSnapshotKey(hash common.Hash) []byte {
	return append(SnapshotAccountPrefix, hash.Bytes()...)
}

// storageSnapshotKey = SnapshotStoragePrefix + account hash + storage hash
func storageSnapshotKey(accountHash, storageHash common.Hash) []byte {
	return append(append(SnapshotStoragePrefix, accountHash.Bytes()...), storageHash.Bytes()...)
}

// storageSnapshotsKey = SnapshotStoragePrefix + account hash + storage hash
func storageSnapshotsKey(accountHash common.Hash) []byte {
	return append(SnapshotStoragePrefix, accountHash.Bytes()...)
}

var BloomBitsKeyLength = len(BloomBitsPrefix) + 2 + 8 + common.HashLength

// bloomBitsKey = bloomBitsPrefix + bit (uint16 big endian) + section (uint64 big endian) + hash
func bloomBitsKey(bit uint, section uint64, hash common.Hash) []byte {
	key := append(append(BloomBitsPrefix, make([]byte, 10)...), hash.Bytes()...)

	binary.BigEndian.PutUint16(key[1:], uint16(bit))
	binary.BigEndian.PutUint64(key[3:], section)

	return key
}

// preimageKey = preimagePrefix + hash
func preimageKey(hash common.Hash) []byte {
	return append(preimagePrefix, hash.Bytes()...)
}

// codeKey = CodePrefix + hash
func codeKey(hash common.Hash) []byte {
	return append(CodePrefix, hash.Bytes()...)
}

// IsCodeKey reports whether the given byte slice is the key of contract code,
// if so return the raw code hash as well.
func IsCodeKey(key []byte) (bool, []byte) {
	if bytes.HasPrefix(key, CodePrefix) && len(key) == common.HashLength+len(CodePrefix) {
		return true, key[len(CodePrefix):]
	}
	return false, nil
}

// configKey = configPrefix + hash
func configKey(hash common.Hash) []byte {
	return append(configPrefix, hash.Bytes()...)
}

// pendingEtxsKey = pendingEtxsPrefix + hash
func pendingEtxsKey(hash common.Hash) []byte {
	return append(pendingEtxsPrefix, hash.Bytes()...)
}

// pendingEtxsRollupKey = pendingEtxsRollupPrefix + hash
func pendingEtxsRollupKey(hash common.Hash) []byte {
	return append(pendingEtxsRollupPrefix, hash.Bytes()...)
}

// manifestKey = manifestPrefix + hash
func manifestKey(hash common.Hash) []byte {
	return append(manifestPrefix, hash.Bytes()...)
}

// interlinkHashKey = interlinkPrefix + hash
func interlinkHashKey(hash common.Hash) []byte {
	return append(interlinkPrefix, hash.Bytes()...)
}

func bloomKey(hash common.Hash) []byte {
	return append(bloomPrefix, hash.Bytes()...)
}

func inboundEtxsKey(hash common.Hash) []byte {
	return append(inboundEtxsPrefix, hash.Bytes()...)
}

func addressUtxosKey(address [20]byte) []byte {
	return append(AddressUtxosPrefix, address[:]...)
}

func addressUtxosWithoutHeightKey(address [20]byte) []byte {
	return append(AddressUtxosWithoutHeightPrefix, address[:]...)
}

func addressLockupsKey(address [20]byte) []byte {
	return append(AddressLockupsPrefix, address[:]...)
}

func lockupDeltasKey(hash common.Hash) []byte {
	return append(lockupDeltasPrefix, hash.Bytes()...)
}

var UtxoKeyLength = len(UtxoPrefix) + common.HashLength + 2

// This can be optimized via VLQ encoding as btcd has done
// this key is 36 bytes long and can probably be reduced to 32 bytes
func UtxoKey(hash common.Hash, index uint16) []byte {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, index)
	return append(UtxoPrefix, append(hash.Bytes(), indexBytes...)...)
}

var UtxoKeyWithDenominationLength = len(UtxoPrefix) + common.HashLength + 3
var PrunedUtxoKeyWithDenominationLength = 9

func UtxoKeyWithDenomination(hash common.Hash, index uint16, denomination uint8) []byte {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, index)
	return append(UtxoPrefix, append(hash.Bytes(), append(indexBytes, denomination)...)...)
}

func ReverseUtxoKey(key []byte) (common.Hash, uint16, error) {
	if len(key) != len(UtxoPrefix)+common.HashLength+2 {
		return common.Hash{}, 0, fmt.Errorf("invalid key length %d", len(key))
	}
	hash := common.BytesToHash(key[len(UtxoPrefix) : common.HashLength+len(UtxoPrefix)])
	index := binary.BigEndian.Uint16(key[common.HashLength+len(UtxoPrefix):])
	return hash, index, nil
}

func spentUTXOsKey(blockHash common.Hash) []byte {
	return append(spentUTXOsPrefix, blockHash[:]...)
}

func trimmedUTXOsKey(blockHash common.Hash) []byte {
	return append(trimmedUTXOsPrefix, blockHash[:]...)
}

func createdUTXOsKey(blockHash common.Hash) []byte {
	return append(createdUTXOsPrefix, blockHash[:]...)
}

func multiSetKey(hash common.Hash) []byte {
	return append(multiSetPrefix, hash.Bytes()...)
}

func utxoSetSizeKey(hash common.Hash) []byte {
	return append(utxoSetSizePrefix, hash.Bytes()...)
}

func prunedUTXOsKey(blockHeight uint64) []byte {
	return append(prunedUTXOKeysPrefix, encodeBlockNumber(blockHeight)...)
}

func lastTrimmedBlockKey(hash common.Hash) []byte {
	return append(lastTrimmedBlockPrefix, hash.Bytes()...)
}

func alreadyPrunedKey(hash common.Hash) []byte {
	return append(prunedPrefix, hash.Bytes()...)
}

func tokenChoiceSetKey(hash common.Hash) []byte {
	return append(tokenChoicePrefix, hash.Bytes()...)
}

func utxoToBlockHeightKey(txHash common.Hash, index uint16) []byte {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, index)
	txHash[common.HashLength-1] = indexBytes[0]
	txHash[common.HashLength-2] = indexBytes[1]
	return append(utxoToBlockHeightPrefix, txHash[:]...)
}

func supplyAnalyticsKey(hash common.Hash) []byte {
	return append(supplyAnalyticsPrefix, hash.Bytes()...)
}

const CoinbaseLockupKeyLength = 47 //len(CoinbaseLockupPrefix) + 2*common.AddressLength + 1 + 4

func CoinbaseLockupKey(ownerContract common.Address, beneficiaryMiner common.Address, lockupByte byte, epoch uint32) []byte {
	epochBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(epochBytes, epoch)
	ownerBytes := ownerContract.Bytes()
	beneficiaryBytes := beneficiaryMiner.Bytes()
	combined := append(ownerBytes, beneficiaryBytes...)
	combined = append(combined, lockupByte)
	combined = append(combined, epochBytes...)
	return append(CoinbaseLockupPrefix, combined...)
}

func createdCoinbaseLockupsKey(hash common.Hash) []byte {
	return append(createdCoinbaseLockupsPrefix, hash.Bytes()...)
}

func deletedCoinbaseLockupsKey(hash common.Hash) []byte {
	return append(deletedCoinbaseLockupsPrefix, hash.Bytes()...)
}

func ReverseCoinbaseLockupKey(data []byte, location common.Location) (common.Address, common.Address, byte, uint32, error) {

	epochLength := 4 // Length of the epoch in bytes
	prefixLength := len(CoinbaseLockupPrefix)

	// Ensure the data is long enough to contain all components
	if len(data) != CoinbaseLockupKeyLength {
		return common.Address{}, common.Address{}, 0, 0, fmt.Errorf("key is wrong length to parse")
	}

	// Check and remove the prefix
	if !bytes.HasPrefix(data, CoinbaseLockupPrefix) {
		return common.Address{}, common.Address{}, 0, 0, fmt.Errorf("key does not have the correct prefix")
	}
	data = data[prefixLength:] // Remove the prefix

	// Extract the owner contract address
	ownerContract := common.BytesToAddress(data[:common.AddressLength], location)
	data = data[common.AddressLength:] // Advance the slice

	// Extract the beneficiary miner address
	beneficiaryMiner := common.BytesToAddress(data[:common.AddressLength], location)
	data = data[common.AddressLength:] // Advance the slice

	// Extract the lockup byte
	lockupByte := data[0]
	data = data[1:] // Advance the slice

	// Extract the epoch
	epoch := binary.BigEndian.Uint32(data[:epochLength])

	return ownerContract, beneficiaryMiner, lockupByte, epoch, nil
}
