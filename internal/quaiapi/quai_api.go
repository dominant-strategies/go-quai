// Copyright 2015 The go-ethereum Authors
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

package quaiapi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
	"github.com/dominant-strategies/go-quai/trie"
	"google.golang.org/protobuf/proto"
)

var (
	txPropagationMetrics = metrics_config.NewCounterVec("TxPropagation", "Transaction propagation counter")
	txEgressCounter      = txPropagationMetrics.WithLabelValues("egress")
	maxOutpointsRange    = uint32(1000)
)

// PublicQuaiAPI provides an API to access Quai related information.
// It offers only methods that operate on public data that is freely available to anyone.
type PublicQuaiAPI struct {
	b Backend
}

// NewPublicQuaiAPI creates a new Quai protocol API.
func NewPublicQuaiAPI(b Backend) *PublicQuaiAPI {
	return &PublicQuaiAPI{b}
}

// GasPrice returns a suggestion for a gas price for legacy transactions.
func (s *PublicQuaiAPI) GasPrice(ctx context.Context) (*hexutil.Big, error) {
	if s.b.NodeLocation().Context() != common.ZONE_CTX {
		return (*hexutil.Big)(big.NewInt(0)), errors.New("gasPrice call can only be made in zone chain")
	}
	blockBaseFee := s.b.CalcBaseFee(s.b.CurrentHeader())
	// increase this by 20% to get guaraneteed inclusion adjusting for the worst case difficulty adjustment
	blockBaseFee = new(big.Int).Mul(blockBaseFee, big.NewInt(120))
	blockBaseFee = new(big.Int).Div(blockBaseFee, big.NewInt(100))
	return (*hexutil.Big)(blockBaseFee), nil
}

// PublicBlockChainQuaiAPI provides an API to access the Quai blockchain.
// It offers only methods that operate on public data that is freely available to anyone.
type PublicBlockChainQuaiAPI struct {
	b Backend
}

// NewPublicBlockChainQuaiAPI creates a new Quai blockchain API.
func NewPublicBlockChainQuaiAPI(b Backend) *PublicBlockChainQuaiAPI {
	return &PublicBlockChainQuaiAPI{b}
}

// ChainId is the replay-protection chain id for the current Quai chain config.
func (api *PublicBlockChainQuaiAPI) ChainId() (*hexutil.Big, error) {
	return (*hexutil.Big)(api.b.ChainConfig().ChainID), nil
}

// NodeLocation is the access call to the location of the node.
func (api *PublicBlockChainQuaiAPI) NodeLocation() []hexutil.Uint64 {
	return api.b.NodeLocation().RPCMarshal()
}

// BlockNumber returns the block number of the chain head.
func (s *PublicBlockChainQuaiAPI) BlockNumber() hexutil.Uint64 {
	header, _ := s.b.HeaderByNumber(context.Background(), rpc.LatestBlockNumber) // latest header should always be available
	return hexutil.Uint64(header.NumberU64(s.b.NodeCtx()))

}

// GetBalance returns the amount of wei for the given address in the state of the
// given block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta
// block numbers are also allowed.
func (s *PublicBlockChainQuaiAPI) GetBalance(ctx context.Context, address common.MixedcaseAddress, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	if !address.ValidChecksum() {
		return nil, errors.New("address has invalid checksum")
	}
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getBalance call can only be made in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getBalance call can only be made on chain processing the state")
	}

	state, header, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}

	addr := common.Bytes20ToAddress(address.Address().Bytes20(), s.b.NodeLocation())
	if addr.IsInQiLedgerScope() {
		currHeader := s.b.CurrentHeader()
		if header.Hash() != currHeader.Hash() {
			return (*hexutil.Big)(big.NewInt(0)), errors.New("qi balance query is only supported for the current block")
		}

		utxos, err := s.b.AddressOutpoints(ctx, addr)
		if utxos == nil || err != nil {
			return (*hexutil.Big)(big.NewInt(0)), err
		}

		if len(utxos) == 0 {
			return (*hexutil.Big)(big.NewInt(0)), nil
		}

		balance := big.NewInt(0)
		for _, utxo := range utxos {
			if utxo.Lock != nil && currHeader.Number(nodeCtx).Cmp(utxo.Lock) < 0 {
				continue
			}
			value := types.Denominations[utxo.Denomination]
			balance.Add(balance, value)
		}
		return (*hexutil.Big)(balance), nil
	} else {
		internal, err := addr.InternalAndQuaiAddress()
		if err != nil {
			return nil, err
		}
		return (*hexutil.Big)(state.GetBalance(internal)), state.Error()
	}
}

func (s *PublicBlockChainQuaiAPI) GetLockedBalance(ctx context.Context, address common.MixedcaseAddress) (*hexutil.Big, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getBalance call can only be made in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getBalance call can only be made on chain processing the state")
	}

	addr := common.Bytes20ToAddress(address.Address().Bytes20(), s.b.NodeLocation())
	if addr.IsInQiLedgerScope() {
		currHeader := s.b.CurrentHeader()
		utxos, err := s.b.AddressOutpoints(ctx, addr)
		if utxos == nil || err != nil {
			return (*hexutil.Big)(big.NewInt(0)), err
		}
		if len(utxos) == 0 {
			return (*hexutil.Big)(big.NewInt(0)), nil
		}
		lockedBalance := big.NewInt(0)
		for _, utxo := range utxos {
			if utxo.Lock != nil && currHeader.Number(nodeCtx).Cmp(utxo.Lock) < 0 {
				value := types.Denominations[utxo.Denomination]
				lockedBalance.Add(lockedBalance, value)
			}
		}
		return (*hexutil.Big)(lockedBalance), nil
	} else if addr.IsInQuaiLedgerScope() {
		lockups, err := s.b.AddressLockups(ctx, addr)
		if lockups == nil || err != nil {
			return (*hexutil.Big)(big.NewInt(0)), err
		}
		lockedBalance := big.NewInt(0)
		for _, lockup := range lockups {
			lockedBalance.Add(lockedBalance, lockup.Value)
		}
		return (*hexutil.Big)(lockedBalance), nil
	}
	return nil, nil
}

func (s *PublicBlockChainQuaiAPI) GetOutpointsByAddress(ctx context.Context, address common.Address) ([]interface{}, error) {
	if address.IsInQuaiLedgerScope() {
		return nil, fmt.Errorf("address %s is in Quai ledger scope", address.Hex())
	}
	outpoints, err := s.b.AddressOutpoints(ctx, address)
	if err != nil {
		return nil, err
	}
	jsonOutpoints := make([]interface{}, 0, len(outpoints))
	for _, outpoint := range outpoints {
		if outpoint == nil {
			continue
		}
		if rawdb.GetUTXO(s.b.Database(), outpoint.TxHash, outpoint.Index) == nil {
			continue
		}
		lock := big.NewInt(0)
		if outpoint.Lock != nil {
			lock = outpoint.Lock
		}
		jsonOutpoint := map[string]interface{}{
			"txHash":       outpoint.TxHash.Hex(),
			"index":        hexutil.Uint64(outpoint.Index),
			"denomination": hexutil.Uint64(outpoint.Denomination),
			"lock":         hexutil.Big(*lock),
		}
		jsonOutpoints = append(jsonOutpoints, jsonOutpoint)
	}

	return jsonOutpoints, nil
}

func (s *PublicBlockChainQuaiAPI) GetLockupsByAddress(ctx context.Context, address common.Address) ([]interface{}, error) {
	if address.IsInQiLedgerScope() {
		return nil, fmt.Errorf("address %s is in Qi ledger scope", address.Hex())
	}
	lockups, err := s.b.AddressLockups(ctx, address)
	if err != nil {
		return nil, err
	}
	jsonLockups := make([]interface{}, 0, len(lockups))
	for _, lockup := range lockups {
		jsonLockup := map[string]interface{}{
			"value":        hexutil.Big(*lockup.Value),
			"unlockHeight": hexutil.Uint64(lockup.UnlockHeight),
		}
		jsonLockups = append(jsonLockups, jsonLockup)
	}
	return jsonLockups, nil
}

func (s *PublicBlockChainQuaiAPI) GetLockupsByAddressAndRange(ctx context.Context, address common.Address, start, end hexutil.Uint64) ([]interface{}, error) {
	if address.IsInQiLedgerScope() {
		return nil, fmt.Errorf("address %s is in Qi ledger scope", address.Hex())
	}
	if start > end {
		return nil, fmt.Errorf("start is greater than end")
	}
	if uint32(end)-uint32(start) > maxOutpointsRange {
		return nil, fmt.Errorf("range is too large, max range is %d", maxOutpointsRange)
	}
	lockups, err := s.b.GetLockupsByAddressAndRange(ctx, address, uint32(start), uint32(end))
	if err != nil {
		return nil, err
	}
	jsonLockups := make([]interface{}, 0, len(lockups))
	for _, lockup := range lockups {
		jsonLockup := map[string]interface{}{
			"value":        hexutil.Big(*lockup.Value),
			"unlockHeight": hexutil.Uint64(lockup.UnlockHeight),
		}
		jsonLockups = append(jsonLockups, jsonLockup)
	}
	return jsonLockups, nil
}

func (s *PublicBlockChainQuaiAPI) GetLockupsForContractAndMiner(ctx context.Context, ownerContract, beneficiaryMiner common.Address) (map[string]map[string][]interface{}, error) {
	_, err := ownerContract.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	_, err = beneficiaryMiner.InternalAddress()
	if err != nil {
		return nil, err
	}
	return vm.GetAllLockupData(s.b.Database(), ownerContract, beneficiaryMiner, s.b.NodeLocation(), s.b.Logger())
}

func (s *PublicBlockChainQuaiAPI) GetWrappedQiDeposit(ctx context.Context, ownerContract, beneficiaryMiner common.Address, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	_, err := ownerContract.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}
	_, err = beneficiaryMiner.InternalAddress()
	if err != nil {
		return nil, err
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	balance, err := vm.GetWrappedQiDeposit(state, ownerContract, beneficiaryMiner, s.b.NodeLocation())
	return (*hexutil.Big)(balance), err
}

func (s *PublicBlockChainQuaiAPI) GetOutPointsByAddressAndRange(ctx context.Context, address common.Address, start, end hexutil.Uint64) (map[string][]interface{}, error) {
	if start > end {
		return nil, fmt.Errorf("start is greater than end")
	}
	if uint32(end)-uint32(start) > maxOutpointsRange {
		return nil, fmt.Errorf("range is too large, max range is %d", maxOutpointsRange)
	}
	if address.IsInQuaiLedgerScope() {
		return nil, fmt.Errorf("address %s is in Quai ledger scope", address.Hex())
	}
	outpoints, err := s.b.GetOutpointsByAddressAndRange(ctx, address, uint32(start), uint32(end))
	if err != nil {
		return nil, err
	}
	txHashToOutpointsJson := make(map[string][]interface{})
	for _, outpoint := range outpoints {
		if outpoint == nil {
			continue
		}
		if rawdb.GetUTXO(s.b.Database(), outpoint.TxHash, outpoint.Index) == nil {
			continue
		}
		lock := big.NewInt(0)
		if outpoint.Lock != nil {
			lock = outpoint.Lock
		}
		jsonOutpoint := map[string]interface{}{
			"index":        hexutil.Uint64(outpoint.Index),
			"denomination": hexutil.Uint64(outpoint.Denomination),
			"lock":         hexutil.Big(*lock),
		}
		txHashToOutpointsJson[outpoint.TxHash.Hex()] = append(txHashToOutpointsJson[outpoint.TxHash.Hex()], jsonOutpoint)
	}

	return txHashToOutpointsJson, nil
}

func (s *PublicBlockChainQuaiAPI) GetOutpointDeltasForAddressesInRange(ctx context.Context, addresses []common.Address, from, to common.Hash) (map[string]map[string]map[string][]interface{}, error) {
	if s.b.NodeCtx() != common.ZONE_CTX {
		return nil, errors.New("getOutpointDeltasForAddressesInRange can only be called in a zone chain")
	}
	nodeCtx := common.ZONE_CTX
	if len(addresses) == 0 {
		return nil, fmt.Errorf("addresses cannot be empty")
	}
	blockFrom := s.b.BlockOrCandidateByHash(from)
	if blockFrom == nil {
		return nil, fmt.Errorf("block %s not found", from.Hex())
	}
	blockTo := s.b.BlockOrCandidateByHash(to)
	if blockTo == nil {
		return nil, fmt.Errorf("block %s not found", to.Hex())
	}
	if blockFrom.NumberU64(nodeCtx) > blockTo.NumberU64(nodeCtx) {
		return nil, fmt.Errorf("from block number is greater than to block number")
	}
	if uint32(blockTo.NumberU64(nodeCtx))-uint32(blockFrom.NumberU64(nodeCtx)) > maxOutpointsRange {
		return nil, fmt.Errorf("range is too large, max range is %d", maxOutpointsRange)
	}
	addressMap := make(map[common.AddressBytes]struct{})
	for _, address := range addresses {
		addressMap[address.Bytes20()] = struct{}{}
	}
	addressToCreatedDeletedToTxHashToOutputs := make(map[string]map[string]map[string][]interface{})
	for _, address := range addresses {
		addressToCreatedDeletedToTxHashToOutputs[address.String()] = make(map[string]map[string][]interface{})
		addressToCreatedDeletedToTxHashToOutputs[address.String()]["created"] = make(map[string][]interface{})
		addressToCreatedDeletedToTxHashToOutputs[address.String()]["deleted"] = make(map[string][]interface{})
	}
	commonBlock := blockFrom
	if blockFrom.Hash() != rawdb.ReadCanonicalHash(s.b.Database(), blockFrom.NumberU64(nodeCtx)) || blockTo.Hash() != rawdb.ReadCanonicalHash(s.b.Database(), blockTo.NumberU64(nodeCtx)) {
		// One (or both) of the blocks are not canonical, find common ancestor
		commonAncestor, err := rawdb.FindCommonAncestor(s.b.Database(), blockFrom, blockTo, nodeCtx)
		if err != nil {
			return nil, err
		}
		if commonAncestor == nil {
			return nil, fmt.Errorf("no common ancestor found")
		}
		commonAncestorBlock := s.b.BlockOrCandidateByHash(commonAncestor.Hash())
		if commonAncestorBlock == nil {
			return nil, fmt.Errorf("block %s not found", to.Hex())
		}
		commonBlock = commonAncestorBlock
	}
	currentBlock := blockTo
	for currentBlock.Hash() != commonBlock.Hash() { // Get deltas from blockTo to common ancestor
		err := GetDeltas(s, currentBlock, addressMap, addressToCreatedDeletedToTxHashToOutputs)
		if err != nil {
			return nil, err
		}
		currentBlock = s.b.BlockOrCandidateByHash(currentBlock.ParentHash(nodeCtx))
		if currentBlock == nil {
			return nil, fmt.Errorf("block %s not found", currentBlock.ParentHash(nodeCtx).Hex())
		}
	}
	if commonBlock.Hash() != blockFrom.Hash() { // Get deltas from blockFrom to common ancestor
		currentBlock = blockFrom
		for currentBlock.Hash() != commonBlock.Hash() {
			err := GetDeltas(s, currentBlock, addressMap, addressToCreatedDeletedToTxHashToOutputs)
			if err != nil {
				return nil, err
			}
			currentBlock = s.b.BlockOrCandidateByHash(currentBlock.ParentHash(nodeCtx))
			if currentBlock == nil {
				return nil, fmt.Errorf("block %s not found", currentBlock.ParentHash(nodeCtx).Hex())
			}
		}
	}
	err := GetDeltas(s, commonBlock, addressMap, addressToCreatedDeletedToTxHashToOutputs) // Get deltas from common ancestor
	if err != nil {
		return nil, err
	}

	return addressToCreatedDeletedToTxHashToOutputs, nil
}

func GetDeltas(s *PublicBlockChainQuaiAPI, currentBlock *types.WorkObject, addressMap map[common.AddressBytes]struct{}, addressToCreatedDeletedToTxHashToOutputs map[string]map[string]map[string][]interface{}) error {
	nodeCtx := common.ZONE_CTX
	// Grab spent UTXOs for this block
	// Eventually spent UTXOs should be pruned, so this data might not be available
	sutxos, err := rawdb.ReadSpentUTXOs(s.b.Database(), currentBlock.Hash())
	if err != nil {
		return err
	}
	trimmedUtxos, err := rawdb.ReadTrimmedUTXOs(s.b.Database(), currentBlock.Hash())
	if err != nil {
		return err
	}
	sutxos = append(sutxos, trimmedUtxos...)
	for _, sutxo := range sutxos {
		if _, ok := addressMap[common.AddressBytes(sutxo.Address)]; ok {
			lock := big.NewInt(0)
			if sutxo.Lock != nil {
				lock = sutxo.Lock
			}
			addressToCreatedDeletedToTxHashToOutputs[common.AddressBytes(sutxo.Address).String()]["deleted"][sutxo.TxHash.String()] =
				append(addressToCreatedDeletedToTxHashToOutputs[common.AddressBytes(sutxo.Address).String()]["deleted"][sutxo.TxHash.String()], map[string]interface{}{
					"index":        hexutil.Uint64(sutxo.Index),
					"denomination": hexutil.Uint64(sutxo.Denomination),
					"lock":         hexutil.Big(*lock),
				})
		}
	}

	for _, tx := range currentBlock.Transactions() {
		if tx.Type() == types.QiTxType {
			for i, out := range tx.TxOut() {
				if common.BytesToAddress(out.Address, common.Location{0, 0}).IsInQuaiLedgerScope() {
					// This is a conversion output
					continue
				}
				if _, ok := addressMap[common.AddressBytes(out.Address)]; !ok {
					continue
				}
				lock := big.NewInt(0)
				if out.Lock != nil {
					lock = out.Lock
				}
				addressToCreatedDeletedToTxHashToOutputs[common.AddressBytes(out.Address).String()]["created"][tx.Hash().String()] =
					append(addressToCreatedDeletedToTxHashToOutputs[common.AddressBytes(out.Address).String()]["created"][tx.Hash().String()], map[string]interface{}{
						"index":        hexutil.Uint64(i),
						"denomination": hexutil.Uint64(out.Denomination),
						"lock":         hexutil.Big(*lock),
					})
			}
		} else if tx.Type() == types.ExternalTxType && tx.EtxType() == types.CoinbaseType && tx.To().IsInQiLedgerScope() {
			if len(tx.Data()) == 0 {
				continue
			}
			if _, ok := addressMap[common.AddressBytes(tx.To().Bytes20())]; !ok {
				continue
			}
			lockupByte := tx.Data()[0]
			lockup := new(big.Int).SetUint64(params.LockupByteToBlockDepth[lockupByte])
			lockup.Add(lockup, currentBlock.Number(nodeCtx))
			value := params.CalculateCoinbaseValueWithLockup(tx.Value(), lockupByte, currentBlock.NumberU64(common.ZONE_CTX))
			denominations := misc.FindMinDenominations(value)
			outputIndex := uint16(0)
			// Iterate over the denominations in descending order
			for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
				// If the denomination count is zero, skip it
				if denominations[uint8(denomination)] == 0 {
					continue
				}
				for j := uint64(0); j < denominations[uint8(denomination)]; j++ {
					if outputIndex >= types.MaxOutputIndex {
						// No more gas, the rest of the denominations are lost but the tx is still valid
						break
					}

					addressToCreatedDeletedToTxHashToOutputs[tx.To().String()]["created"][tx.Hash().String()] =
						append(addressToCreatedDeletedToTxHashToOutputs[tx.To().String()]["created"][tx.Hash().String()], map[string]interface{}{
							"index":        hexutil.Uint64(outputIndex),
							"denomination": hexutil.Uint64(uint8(denomination)),
							"lock":         hexutil.Big(*lockup),
						})
					outputIndex++
				}
			}
		} else if tx.Type() == types.ExternalTxType && tx.EtxType() == types.ConversionType && tx.To().IsInQiLedgerScope() {
			if _, ok := addressMap[common.AddressBytes(tx.To().Bytes20())]; !ok {
				continue
			}
			lockup := new(big.Int).SetUint64(params.ConversionLockPeriod)
			lockup.Add(lockup, currentBlock.Number(nodeCtx))
			value := tx.Value()
			txGas := tx.Gas()
			if txGas < params.TxGas {
				continue
			}
			txGas -= params.TxGas
			denominations := misc.FindMinDenominations(value)
			outputIndex := uint16(0)
			// Iterate over the denominations in descending order
			for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
				// If the denomination count is zero, skip it
				if denominations[uint8(denomination)] == 0 {
					continue
				}
				for j := uint64(0); j < denominations[uint8(denomination)]; j++ {
					if txGas < params.CallValueTransferGas || outputIndex >= types.MaxOutputIndex {
						// No more gas, the rest of the denominations are lost but the tx is still valid
						break
					}
					txGas -= params.CallValueTransferGas
					// the ETX hash is guaranteed to be unique

					addressToCreatedDeletedToTxHashToOutputs[tx.To().String()]["created"][tx.Hash().String()] =
						append(addressToCreatedDeletedToTxHashToOutputs[tx.To().String()]["created"][tx.Hash().String()], map[string]interface{}{
							"index":        hexutil.Uint64(outputIndex),
							"denomination": hexutil.Uint64(uint8(denomination)),
							"lock":         hexutil.Big(*lockup),
						})

					outputIndex++
				}
			}
		}
	}
	return nil
}

func (s *PublicBlockChainQuaiAPI) GetUTXO(ctx context.Context, txHash common.Hash, index hexutil.Uint64) (map[string]interface{}, error) {
	utxo := rawdb.GetUTXO(s.b.Database(), txHash, uint16(index))
	if utxo == nil {
		return nil, nil
	}
	lock := big.NewInt(0)
	if utxo.Lock != nil {
		lock = utxo.Lock
	}
	jsonOutpoint := map[string]interface{}{
		"address":      common.BytesToAddress(utxo.Address, s.b.NodeLocation()).Hex(),
		"denomination": hexutil.Uint64(utxo.Denomination),
		"lock":         hexutil.Big(*lock),
	}
	return jsonOutpoint, nil
}

// GetProof returns the Merkle-proof for a given account and optionally some storage keys.
func (s *PublicBlockChainQuaiAPI) GetProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*AccountResult, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getProof call can only be made in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getProof call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return nil, err
	}

	storageTrie := state.StorageTrie(internal)
	storageHash := types.EmptyRootHash
	codeHash := state.GetCodeHash(internal)
	storageProof := make([]StorageResult, len(storageKeys))

	// if we have a storageTrie, (which means the account exists), we can update the storagehash
	if storageTrie != nil {
		storageHash = storageTrie.Hash()
	} else {
		// no storageTrie means the account does not exist, so the codeHash is the hash of an empty bytearray.
		codeHash = crypto.Keccak256Hash(nil)
	}

	// create the proof for the storageKeys
	for i, key := range storageKeys {
		if storageTrie != nil {
			proof, storageError := state.GetStorageProof(internal, common.HexToHash(key))
			if storageError != nil {
				return nil, storageError
			}
			storageProof[i] = StorageResult{key, (*hexutil.Big)(state.GetState(internal, common.HexToHash(key)).Big()), toHexSlice(proof)}
		} else {
			storageProof[i] = StorageResult{key, &hexutil.Big{}, []string{}}
		}
	}

	// create the accountProof
	accountProof, proofErr := state.GetProof(internal)
	if proofErr != nil {
		return nil, proofErr
	}

	return &AccountResult{
		Address:      address.MixedcaseAddress(),
		AccountProof: toHexSlice(accountProof),
		Balance:      (*hexutil.Big)(state.GetBalance(internal)),
		CodeHash:     codeHash,
		Nonce:        hexutil.Uint64(state.GetNonce(internal)),
		StorageHash:  storageHash,
		StorageProof: storageProof,
	}, state.Error()
}

// GetHeaderByNumber returns the requested canonical block header.
// * When blockNr is -1 the chain head is returned.
// * When blockNr is -2 the pending chain head is returned.
func (s *PublicBlockChainQuaiAPI) GetHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (map[string]interface{}, error) {
	header, err := s.b.HeaderByNumber(ctx, number)
	if header != nil && err == nil {
		response := header.RPCMarshalHeader()
		if number == rpc.PendingBlockNumber {
			// Pending header need to nil out a few fields
			for _, field := range []string{"hash", "nonce", "coinbase"} {
				response[field] = nil
			}
		}
		return response, err
	}
	return nil, err
}

// GetHeaderByHash returns the requested header by hash.
func (s *PublicBlockChainQuaiAPI) GetHeaderByHash(ctx context.Context, hash common.Hash) map[string]interface{} {
	header, _ := s.b.HeaderByHash(ctx, hash)
	if header != nil {
		return header.RPCMarshalHeader()
	}
	return nil
}

// GetBlockByNumber returns the requested canonical block.
//   - When blockNr is -1 the chain head is returned.
//   - When blockNr is -2 the pending chain head is returned.
//   - When fullTx is true all transactions in the block are returned, otherwise
//     only the transaction hash is returned.
func (s *PublicBlockChainQuaiAPI) GetBlockByNumber(ctx context.Context, number rpc.BlockNumber, fullTx bool) (map[string]interface{}, error) {
	block, err := s.b.BlockByNumber(ctx, number)
	if block != nil && err == nil {
		response, err := s.rpcMarshalBlock(ctx, block, true, fullTx)
		if err == nil && number == rpc.PendingBlockNumber {
			// Pending blocks need to nil out a few fields
			for _, field := range []string{"hash", "nonce", "coinbase"} {
				response[field] = nil
			}
		}
		return response, err
	}
	return nil, err
}

// GetBlockByHash returns the requested block. When fullTx is true all transactions in the block are returned in full
// detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainQuaiAPI) GetBlockByHash(ctx context.Context, hash common.Hash, fullTx bool) (map[string]interface{}, error) {
	block, err := s.b.BlockByHash(ctx, hash)
	if block != nil {
		return s.rpcMarshalBlock(ctx, block, true, fullTx)
	}
	if block == nil && err == nil {
		return nil, errors.New("block not found")
	}
	return nil, err
}

func (s *PublicBlockChainQuaiAPI) GetBlockOrCandidateByHash(ctx context.Context, hash common.Hash, fullTx bool) (map[string]interface{}, error) {
	if block := s.b.BlockOrCandidateByHash(hash); block != nil {
		return s.rpcMarshalBlock(ctx, block, true, fullTx)
	}
	return nil, nil
}

// GetUncleByBlockNumberAndIndex returns the uncle block for the given block hash and index. When fullTx is true
// all transactions in the block are returned in full detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainQuaiAPI) GetUncleByBlockNumberAndIndex(ctx context.Context, blockNr rpc.BlockNumber, index hexutil.Uint) (map[string]interface{}, error) {
	block, err := s.b.BlockByNumber(ctx, blockNr)
	if block != nil {
		uncles := block.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			s.b.Logger().WithFields(log.Fields{
				"number": block.Number(s.b.NodeCtx()),
				"hash":   block.Hash(),
				"index":  index,
			}).Debug("Requested uncle not found")
			return nil, nil
		}
		return uncles[index].RPCMarshalWorkObjectHeader(), nil
	}
	return nil, err
}

// GetUncleByBlockHashAndIndex returns the uncle block for the given block hash and index. When fullTx is true
// all transactions in the block are returned in full detail, otherwise only the transaction hash is returned.
func (s *PublicBlockChainQuaiAPI) GetUncleByBlockHashAndIndex(ctx context.Context, blockHash common.Hash, index hexutil.Uint) (map[string]interface{}, error) {
	block, err := s.b.BlockByHash(ctx, blockHash)
	if block != nil {
		uncles := block.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			s.b.Logger().WithFields(log.Fields{
				"number": block.Number(s.b.NodeCtx()),
				"hash":   blockHash,
				"index":  index,
			}).Debug("Requested uncle not found")
			return nil, nil
		}
		return uncles[index].RPCMarshalWorkObjectHeader(), nil
	}
	pendBlock, _ := s.b.PendingBlockAndReceipts()
	if pendBlock != nil && pendBlock.Hash() == blockHash {
		uncles := pendBlock.Uncles()
		if index >= hexutil.Uint(len(uncles)) {
			s.b.Logger().WithFields(log.Fields{
				"number": block.Number(s.b.NodeCtx()),
				"hash":   blockHash,
				"index":  index,
			}).Debug("Requested uncle not found in pending block")
			return nil, nil
		}
		return uncles[index].RPCMarshalWorkObjectHeader(), nil
	}
	return nil, err
}

// GetUncleCountByBlockNumber returns number of uncles in the block for the given block number
func (s *PublicBlockChainQuaiAPI) GetUncleCountByBlockNumber(ctx context.Context, blockNr rpc.BlockNumber) *hexutil.Uint {
	if block, _ := s.b.BlockByNumber(ctx, blockNr); block != nil {
		n := hexutil.Uint(len(block.Uncles()))
		return &n
	}
	return nil
}

// GetUncleCountByBlockHash returns number of uncles in the block for the given block hash
func (s *PublicBlockChainQuaiAPI) GetUncleCountByBlockHash(ctx context.Context, blockHash common.Hash) *hexutil.Uint {
	if block, _ := s.b.BlockByHash(ctx, blockHash); block != nil {
		n := hexutil.Uint(len(block.Uncles()))
		return &n
	}
	return nil
}

// GetCode returns the code stored at the given address in the state for the given block number.
func (s *PublicBlockChainQuaiAPI) GetCode(ctx context.Context, address common.MixedcaseAddress, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if !address.ValidChecksum() {
		return nil, errors.New("address has invalid checksum")
	}
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getCode can only called in a zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getCode call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := common.HexToAddress(address.Original(), s.b.NodeLocation()).InternalAddress()
	if err != nil {
		return nil, err
	}
	code := state.GetCode(internal)
	return code, state.Error()
}

// GetStorageAt returns the storage from the state at the given address, key and
// block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta block
// numbers are also allowed.
func (s *PublicBlockChainQuaiAPI) GetStorageAt(ctx context.Context, address common.MixedcaseAddress, key string, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if !address.ValidChecksum() {
		return nil, errors.New("address has invalid checksum")
	}
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("getStorageAt can only called in a zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("getStorageAt call can only be made on chain processing the state")
	}
	state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if state == nil || err != nil {
		return nil, err
	}
	internal, err := common.HexToAddress(address.Original(), s.b.NodeLocation()).InternalAddress()
	if err != nil {
		return nil, err
	}
	res := state.GetState(internal, common.HexToHash(key))
	return res[:], state.Error()
}

// Call executes the given transaction on the state for the given block number.
//
// Additionally, the caller can specify a batch of contract for fields overriding.
//
// Note, this function doesn't make and changes in the state/blockchain and is
// useful to execute and retrieve values.
func (s *PublicBlockChainQuaiAPI) Call(ctx context.Context, args TransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, overrides *StateOverride) (hexutil.Bytes, error) {
	result, err := DoCall(ctx, s.b, args, blockNrOrHash, overrides, 5*time.Second, s.b.RPCGasCap())
	if err != nil {
		return nil, err
	}
	// If the result contains a revert reason, try to unpack and return it.
	if len(result.Revert()) > 0 {
		return nil, newRevertError(result, s.b.NodeLocation())
	}
	return result.Return(), result.Err
}

// EstimateGas returns an estimate of the amount of gas needed to execute the
// given transaction against the current pending block.
func (s *PublicBlockChainQuaiAPI) EstimateGas(ctx context.Context, args TransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Uint64, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return 0, errors.New("estimateGas can only called in a zone chain")
	}
	if !s.b.ProcessingState() {
		return 0, errors.New("estimateGas call can only be made on chain processing the state")
	}
	bNrOrHash := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	if blockNrOrHash != nil {
		bNrOrHash = *blockNrOrHash
	}

	if args.from(s.b.NodeLocation()).IsInQuaiLedgerScope() && args.To != nil && args.To.IsInQiLedgerScope() {
		// Conversion transaction
		var header *types.WorkObject
		var err error
		if blockNr, ok := blockNrOrHash.Number(); ok {
			if blockNr == rpc.LatestBlockNumber {
				header = s.b.CurrentHeader()
			} else {
				header, err = s.b.HeaderByNumber(ctx, rpc.BlockNumber(blockNr))
			}
		} else if hash, ok := blockNrOrHash.Hash(); ok {
			header, err = s.b.HeaderByHash(ctx, hash)
		} else {
			return 0, errors.New("invalid block number or hash")
		}
		if err != nil {
			return 0, err
		}
		estimatedQiAmount := misc.QuaiToQi(header, args.Value.ToInt())
		usedGas := uint64(0)

		usedGas += params.TxGas
		denominations := misc.FindMinDenominations(estimatedQiAmount)
		outputIndex := uint16(0)
		// Iterate over the denominations in descending order
		for denomination := types.MaxDenomination; denomination >= 0; denomination-- {
			// If the denomination count is zero, skip it
			if denominations[uint8(denomination)] == 0 {
				continue
			}
			for j := uint64(0); j < denominations[uint8(denomination)]; j++ {
				usedGas += params.CallValueTransferGas
				outputIndex++
			}
		}
		return hexutil.Uint64(usedGas), nil
	}
	switch args.TxType {
	case types.QiTxType:
		block, err := s.b.BlockByNumberOrHash(ctx, bNrOrHash)
		if err != nil {
			return 0, err
		}
		if block == nil {
			return 0, errors.New("block not found: " + fmt.Sprintf("%v", bNrOrHash))
		}
		scalingFactor := math.Log(float64(rawdb.ReadUTXOSetSize(s.b.Database(), block.Hash())))
		return args.CalculateQiTxGas(scalingFactor, s.b.NodeLocation())
	case types.QuaiTxType:
		return DoEstimateGas(ctx, s.b, args, bNrOrHash, s.b.RPCGasCap())
	default:
		return 0, errors.New("unsupported tx type")
	}
}

// GetContractSize gives the size of the contract at the block hash or number
func (s *PublicBlockChainQuaiAPI) GetContractSize(ctx context.Context, address common.AddressBytes, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	addr := common.Bytes20ToAddress(address, s.b.NodeLocation())
	if addr.IsInQuaiLedgerScope() {
		state, _, err := s.b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
		if state == nil || err != nil {
			return nil, err
		}
		internal, err := addr.InternalAndQuaiAddress()
		if err != nil {
			return nil, err
		}
		return (*hexutil.Big)(state.GetSize(internal)), state.Error()
	} else {
		return nil, errors.New("getContractSize cannot be called on a Qi Address")
	}
}

// BaseFee returns the base fee for a tx to be included in the next block.
// If txType is set to "true" returns the Quai base fee in units of Wei.
// If txType is set to "false" returns the Qi base fee in units of Qit.
func (s *PublicBlockChainQuaiAPI) BaseFee(ctx context.Context, txType bool) (*hexutil.Big, error) {
	header := s.b.CurrentBlock()
	if header == nil {
		return nil, errors.New("no header available")
	}

	chainCfg := s.b.ChainConfig()
	if chainCfg == nil {
		return nil, errors.New("no chain config available")
	}

	if txType {
		return (*hexutil.Big)(s.b.CurrentBlock().BaseFee()), nil
	} else {
		quaiBaseFee := s.b.CurrentBlock().BaseFee()
		qiBaseFee := misc.QuaiToQi(header, quaiBaseFee)
		if qiBaseFee.Cmp(big.NewInt(0)) == 0 {
			// Minimum base fee is 1 qit or smallest unit
			return (*hexutil.Big)(types.Denominations[0]), nil
		} else {
			return (*hexutil.Big)(qiBaseFee), nil
		}
	}
}

// EstimateFeeForQi returns an estimate of the amount of Qi in qits needed to execute the
// given transaction against the current pending block.
func (s *PublicBlockChainQuaiAPI) EstimateFeeForQi(ctx context.Context, args TransactionArgs) (*hexutil.Big, error) {
	header := s.b.CurrentBlock()
	if header == nil {
		return nil, errors.New("no header available")
	}

	chainCfg := s.b.ChainConfig()
	if chainCfg == nil {
		return nil, errors.New("no chain config available")
	}
	scalingFactor := math.Log(float64(rawdb.ReadUTXOSetSize(s.b.Database(), header.Hash())))
	// Estimate the gas
	gas, err := args.CalculateQiTxGas(scalingFactor, s.b.NodeLocation())
	if err != nil {
		return nil, err
	}

	// Calculate the base fee
	currentBaseFee := s.b.CurrentBlock().BaseFee()
	// increase the base fee by 20% to account for the change in difficulty
	currentBaseFee = new(big.Int).Mul(currentBaseFee, big.NewInt(120))
	currentBaseFee = new(big.Int).Div(currentBaseFee, big.NewInt(100))
	feeInQuai := new(big.Int).Mul(new(big.Int).SetUint64(uint64(gas)), currentBaseFee)
	feeInQi := misc.QuaiToQi(header, feeInQuai)
	if feeInQi.Cmp(big.NewInt(0)) == 0 {
		// Minimum fee is 1 qit or smallest unit
		return (*hexutil.Big)(types.Denominations[0]), nil
	}
	log.Global.Infof("Estimated fee: %s\n", feeInQi.String())
	return (*hexutil.Big)(feeInQi), nil
}

func (s *PublicBlockChainQuaiAPI) GetLatestUTXOSetSize(ctx context.Context) (hexutil.Uint64, error) {
	return hexutil.Uint64(rawdb.ReadUTXOSetSize(s.b.Database(), s.b.CurrentBlock().Hash())), nil
}

// RPCMarshalBlock converts the given block to the RPC output which depends on fullTx. If inclTx is true transactions are
// returned. When fullTx is true the returned block contains full transaction details, otherwise it will only contain
// transaction hashes.
func RPCMarshalBlock(backend Backend, block *types.WorkObject, inclTx bool, fullTx bool, nodeLocation common.Location) (map[string]interface{}, error) {
	fields := make(map[string]interface{})

	fields["hash"] = block.Hash()
	fields["woHeader"] = block.WorkObjectHeader().RPCMarshalWorkObjectHeader()
	fields["header"] = block.Body().Header().RPCMarshalHeader()
	fields["size"] = hexutil.Uint64(block.Size())

	if inclTx {
		formatTx := func(tx *types.Transaction) (interface{}, error) {
			return tx.Hash(), nil
		}
		formatEtx := formatTx
		if fullTx {
			formatTx = func(tx *types.Transaction) (interface{}, error) {
				return newRPCTransactionFromBlockHash(block, tx.Hash(), false, nodeLocation), nil
			}
			formatEtx = func(tx *types.Transaction) (interface{}, error) {
				return newRPCTransactionFromBlockHash(block, tx.Hash(), true, nodeLocation), nil
			}
		}
		txs := block.Transactions()
		transactions := make([]interface{}, len(txs))
		var err error
		for i, tx := range txs {
			if transactions[i], err = formatTx(tx); err != nil {
				return nil, err
			}
		}
		fields["transactions"] = transactions
		etxs := block.OutboundEtxs()
		formatEtxs := make([]interface{}, len(etxs))
		for i, etx := range etxs {
			if formatEtxs[i], err = formatEtx(etx); err != nil {
				return nil, err
			}
		}
		fields["outboundEtxs"] = formatEtxs
	}

	marshalUncles := make([]map[string]interface{}, 0)
	marshalWorkShares := make([]map[string]interface{}, 0)
	for _, uncle := range block.Uncles() {
		rpcMarshalUncle := uncle.RPCMarshalWorkObjectHeader()
		_, err := backend.Engine().VerifySeal(uncle)
		if err != nil {
			marshalWorkShares = append(marshalWorkShares, rpcMarshalUncle)
		} else {
			marshalUncles = append(marshalUncles, rpcMarshalUncle)
		}
	}
	fields["uncles"] = marshalUncles
	fields["workshares"] = marshalWorkShares
	fields["subManifest"] = block.Manifest()
	fields["interlinkHashes"] = block.InterlinkHashes()

	return fields, nil
}

// RPCMarshalHash convert the hash into a the correct interface.
func RPCMarshalHash(hash common.Hash) (map[string]interface{}, error) {
	fields := map[string]interface{}{"Hash": hash}
	return fields, nil
}

// rpcMarshalBlock uses the generalized output filler, then adds the total difficulty field, which requires
// a `PublicBlockchainAPI`.
func (s *PublicBlockChainQuaiAPI) rpcMarshalBlock(ctx context.Context, b *types.WorkObject, inclTx bool, fullTx bool) (map[string]interface{}, error) {
	fields, err := RPCMarshalBlock(s.b, b, inclTx, fullTx, s.b.NodeLocation())
	if err != nil {
		return nil, err
	}
	fields["totalEntropy"] = (*hexutil.Big)(s.b.TotalLogEntropy(b))
	return fields, err
}

// CreateAccessList creates an AccessList for the given transaction.
// Reexec and BlockNrOrHash can be specified to create the accessList on top of a certain state.
func (s *PublicBlockChainQuaiAPI) CreateAccessList(ctx context.Context, args TransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (*accessListResult, error) {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return nil, errors.New("createAccessList can only be called in zone chain")
	}
	if !s.b.ProcessingState() {
		return nil, errors.New("createAccessList call can only be made on chain processing the state")
	}
	bNrOrHash := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	if blockNrOrHash != nil {
		bNrOrHash = *blockNrOrHash
	}
	acl, gasUsed, vmerr, err := AccessList(ctx, s.b, bNrOrHash, args)
	if err != nil {
		return nil, err
	}
	result := &accessListResult{Accesslist: acl.ConvertToMixedCase(), GasUsed: hexutil.Uint64(gasUsed)}
	if vmerr != nil {
		result.Error = vmerr.Error()
	}
	return result, nil
}

func (s *PublicBlockChainQuaiAPI) fillSubordinateManifest(b *types.WorkObject) (*types.WorkObject, error) {
	nodeCtx := s.b.NodeCtx()
	if b.ManifestHash(nodeCtx+1) == types.EmptyRootHash {
		return nil, errors.New("cannot fill empty subordinate manifest")
	} else if subManifestHash := types.DeriveSha(b.Manifest(), trie.NewStackTrie(nil)); subManifestHash == b.ManifestHash(nodeCtx+1) {
		// If the manifest hashes match, nothing to do
		return b, nil
	} else {
		subParentHash := b.ParentHash(nodeCtx + 1)
		var subManifest types.BlockManifest
		if subParent, err := s.b.BlockByHash(context.Background(), subParentHash); err == nil && subParent != nil {
			// If we have the the subordinate parent in our chain, that means that block
			// was also coincident. In this case, the subordinate manifest resets, and
			// only consists of the subordinate parent hash.
			subManifest = types.BlockManifest{subParentHash}
		} else {
			// Otherwise we need to reconstruct the sub manifest, by getting the
			// parent's sub manifest and appending the parent hash.
			subManifest, err = s.b.GetSubManifest(b.Location(), subParentHash)
			if err != nil {
				return nil, err
			}
		}
		if len(subManifest) == 0 {
			return nil, errors.New("reconstructed sub manifest is empty")
		}
		if subManifest == nil || b.ManifestHash(nodeCtx+1) != types.DeriveSha(subManifest, trie.NewStackTrie(nil)) {
			return nil, errors.New("reconstructed sub manifest does not match manifest hash")
		}
		return types.NewWorkObjectWithHeaderAndTx(b.WorkObjectHeader(), b.Tx()).WithBody(b.Header(), b.Transactions(), b.OutboundEtxs(), b.Uncles(), subManifest, b.InterlinkHashes()), nil
	}
}

// ReceiveMinedHeader will run checks on the block and add to canonical chain if valid.
func (s *PublicBlockChainQuaiAPI) ReceiveMinedHeader(ctx context.Context, raw hexutil.Bytes) error {
	nodeCtx := s.b.NodeCtx()
	protoWorkObject := &types.ProtoWorkObject{}
	err := proto.Unmarshal(raw, protoWorkObject)
	if err != nil {
		return err
	}

	woHeader := &types.WorkObject{}
	err = woHeader.ProtoDecode(protoWorkObject, s.b.NodeLocation(), types.PEtxObject)
	if err != nil {
		return err
	}
	block, err := s.b.ConstructLocalMinedBlock(woHeader)
	if err != nil && err.Error() == core.ErrBadSubManifest.Error() && nodeCtx < common.ZONE_CTX {
		s.b.Logger().Info("filling sub manifest")
		// If we just mined this block, and we have a subordinate chain, its possible
		// the subordinate manifest in our block body is incorrect. If so, ask our sub
		// for the correct manifest and reconstruct the block.
		var err error
		block, err = s.fillSubordinateManifest(block)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Broadcast the block and announce chain insertion event
	if block.Header() != nil {
		err := s.b.BroadcastBlock(block, s.b.NodeLocation())
		if err != nil {
			s.b.Logger().WithField("err", err).Error("Error broadcasting block")
		}
		if nodeCtx == common.ZONE_CTX {
			err = s.b.BroadcastHeader(block, s.b.NodeLocation())
			if err != nil {
				s.b.Logger().WithField("err", err).Error("Error broadcasting header")
			}
		}
	}
	s.b.Logger().WithFields(log.Fields{
		"number":   block.Number(s.b.NodeCtx()),
		"location": block.Location(),
	}).Info("Received mined header")

	return nil
}

func (s *PublicBlockChainQuaiAPI) ReceiveRawWorkShare(ctx context.Context, raw hexutil.Bytes) error {
	nodeCtx := s.b.NodeCtx()
	if nodeCtx != common.ZONE_CTX {
		return errors.New("work shares cannot be broadcasted in non-zone chain")
	}
	protoWorkShare := &types.ProtoWorkObjectHeader{}
	err := proto.Unmarshal(raw, protoWorkShare)
	if err != nil {
		return err
	}

	workShare := &types.WorkObjectHeader{}
	err = workShare.ProtoDecode(protoWorkShare, s.b.NodeLocation())
	if err != nil {
		return err
	}

	return s.ReceiveWorkShare(ctx, workShare)
}

func (s *PublicBlockChainQuaiAPI) ReceiveWorkShare(ctx context.Context, workShare *types.WorkObjectHeader) error {
	if workShare != nil {
		var isWorkShare, isSubShare bool
		threshold := s.b.GetWorkShareP2PThreshold()
		isSubShare = s.b.Engine().CheckWorkThreshold(workShare, threshold)
		if !isSubShare {
			return errors.New("workshare has less entropy than the workshare p2p threshold")
		}

		s.b.Logger().WithField("number", workShare.NumberU64()).Info("Received Work Share")
		pendingBlockBody := s.b.GetPendingBlockBody(workShare)
		txs, err := s.b.GetTxsFromBroadcastSet(workShare.TxHash())
		if err != nil {
			txs = types.Transactions{}
			if workShare.TxHash() != types.EmptyRootHash {
				s.b.Logger().Warn("Failed to get txs from the broadcastSetCache", "err", err)
			}
		}
		// If the share qualifies is not a workshare and there are no transactions,
		// there is no need to broadcast the share
		isWorkShare = s.b.Engine().CheckWorkThreshold(workShare, params.WorkSharesThresholdDiff)
		if !isWorkShare && len(txs) == 0 {
			return nil
		}
		if pendingBlockBody == nil {
			s.b.Logger().Warn("Could not get the pending Block body", "err", err)
			return nil
		}
		wo := types.NewWorkObject(workShare, pendingBlockBody.Body(), nil)
		shareView := wo.ConvertToWorkObjectShareView(txs)
		err = s.b.BroadcastWorkShare(shareView, s.b.NodeLocation())
		if err != nil {
			s.b.Logger().WithField("err", err).Error("Error broadcasting work share")
		}
		txEgressCounter.Add(float64(len(shareView.WorkObject.Transactions())))
		s.b.Logger().WithFields(log.Fields{"tx count": len(txs)}).Info("Broadcasted workshares with txs")
	}
	return nil
}

func (s *PublicBlockChainQuaiAPI) GetPendingHeader(ctx context.Context) (hexutil.Bytes, error) {
	if !s.b.ProcessingState() {
		return nil, errors.New("getPendingHeader call can only be made on chain processing the state")
	}
	pendingHeader, err := s.b.GetPendingHeader()
	if err != nil {
		return nil, err
	} else if pendingHeader == nil {
		return nil, errors.New("no pending header found")
	}
	// Only keep the Header in the body
	pendingHeaderForMining := pendingHeader.WithBody(pendingHeader.Header(), nil, nil, nil, nil, nil)
	// Marshal the response.
	protoWo, err := pendingHeaderForMining.ProtoEncode(types.PEtxObject)
	if err != nil {
		return nil, err
	}
	data, err := proto.Marshal(protoWo)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ListRunningChains returns the running locations where the node is serving data.
func (s *PublicBlockChainQuaiAPI) ListRunningChains() []common.Location {
	return s.b.GetSlicesRunning()
}

func (s *PublicBlockChainQuaiAPI) GetProtocolExpansionNumber() hexutil.Uint {
	return hexutil.Uint(s.b.GetExpansionNumber())
}

// Calculate the amount of Quai that Qi can be converted to. Expect the current Header and the Qi amount in "qits", returns the quai amount in "its"
func (s *PublicBlockChainQuaiAPI) QiToQuai(ctx context.Context, qiAmount hexutil.Big, blockNrOrHash rpc.BlockNumberOrHash) *hexutil.Big {
	var header *types.WorkObject
	var err error
	if blockNr, ok := blockNrOrHash.Number(); ok {
		if blockNr == rpc.LatestBlockNumber {
			header = s.b.CurrentHeader()
		} else {
			header, err = s.b.HeaderByNumber(ctx, rpc.BlockNumber(blockNr))
		}
	} else if hash, ok := blockNrOrHash.Hash(); ok {
		header, err = s.b.HeaderByHash(ctx, hash)
	} else {
		return nil
	}
	if err != nil {
		s.b.Logger().WithField("err", err).Error("Error calculating QiRateAtBlock")
		return nil
	} else if header == nil {
		return nil
	}
	return (*hexutil.Big)(misc.QiToQuai(header, qiAmount.ToInt()))
}

// Calculate the amount of Qi that Quai can be converted to. Expect the current Header and the Quai amount in "its", returns the Qi amount in "qits"
func (s *PublicBlockChainQuaiAPI) QuaiToQi(ctx context.Context, quaiAmount hexutil.Big, blockNrOrHash rpc.BlockNumberOrHash) *hexutil.Big {
	var header *types.WorkObject
	var err error
	if blockNr, ok := blockNrOrHash.Number(); ok {
		if blockNr == rpc.LatestBlockNumber {
			header = s.b.CurrentHeader()
		} else {
			header, err = s.b.HeaderByNumber(ctx, rpc.BlockNumber(blockNr))
		}
	} else if hash, ok := blockNrOrHash.Hash(); ok {
		header, err = s.b.HeaderByHash(ctx, hash)
	} else {
		return nil
	}
	if err != nil {
		s.b.Logger().WithField("err", err).Error("Error calculating QuaiRateAtBlock")
		return nil
	} else if header == nil {
		return nil
	}
	return (*hexutil.Big)(misc.QuaiToQi(header, quaiAmount.ToInt()))
}

func (s *PublicBlockChainQuaiAPI) CalcOrder(ctx context.Context, raw hexutil.Bytes) (hexutil.Uint, error) {
	protoWorkObject := &types.ProtoWorkObject{}
	err := proto.Unmarshal(raw, protoWorkObject)
	if err != nil {
		return 0, err
	}

	woHeader := &types.WorkObject{}
	err = woHeader.ProtoDecode(protoWorkObject, s.b.NodeLocation(), types.PEtxObject)
	if err != nil {
		return 0, err
	}
	_, order, err := s.b.CalcOrder(woHeader)
	if err != nil {
		return 0, fmt.Errorf("cannot calculate prime terminus order: %v", err)
	}
	return hexutil.Uint(order), nil
}

func (s *PublicBlockChainQuaiAPI) SuggestFinalityDepth(ctx context.Context, qiValue hexutil.Uint64, correlatedRisk hexutil.Uint64) (hexutil.Uint64, error) {

	depth, err := s.b.SuggestFinalityDepth(ctx, big.NewInt(int64(qiValue)), big.NewInt(int64(correlatedRisk)))
	if err != nil {
		return 0, err
	}
	return hexutil.Uint64(depth.Uint64()), nil
}

func (s *PublicBlockChainQuaiAPI) GetWorkShareP2PThreshold(ctx context.Context) hexutil.Uint64 {
	return hexutil.Uint64(s.b.GetWorkShareP2PThreshold())
}

func (s *PublicBlockChainQuaiAPI) SetWorkShareP2PThreshold(ctx context.Context, threshold hexutil.Uint64) error {
	if threshold < hexutil.Uint64(params.WorkSharesThresholdDiff) {
		return errors.New("the subshare threshold is less than the workshare threshold")
	}

	s.b.SetWorkShareP2PThreshold(int(threshold))

	return nil
}

// GetTransactionReceipt returns the transaction receipt for the given transaction hash.
// Moved from PublicTransactionPoolAPI
// quai_getTransactionReceipt
func (s *PublicBlockChainQuaiAPI) GetTransactionReceipt(ctx context.Context, hash common.Hash) (map[string]interface{}, error) {
	tx, blockHash, blockNumber, index, err := s.b.GetTransaction(ctx, hash)
	if err != nil {
		return nil, nil
	}
	receipts, err := s.b.GetReceipts(ctx, blockHash)
	if err != nil {
		return nil, err
	}
	receipt := &types.Receipt{}
	for _, r := range receipts {
		if r.TxHash == hash {
			receipt = r
		}
	}

	fields := map[string]interface{}{
		"blockHash":         blockHash,
		"blockNumber":       hexutil.Uint64(blockNumber),
		"transactionHash":   hash,
		"transactionIndex":  hexutil.Uint64(index),
		"gasUsed":           hexutil.Uint64(receipt.GasUsed),
		"cumulativeGasUsed": hexutil.Uint64(receipt.CumulativeGasUsed),
		"contractAddress":   nil,
		"logs":              receipt.Logs,
		"logsBloom":         receipt.Bloom,
		"type":              hexutil.Uint(tx.Type()),
	}

	if tx.Type() == types.QuaiTxType {
		if to := tx.To(); to != nil {
			fields["to"] = to.Hex()
		}
		// Derive the sender.
		bigblock := new(big.Int).SetUint64(blockNumber)
		signer := types.MakeSigner(s.b.ChainConfig(), bigblock)
		from, _ := types.Sender(signer, tx)
		fields["from"] = from.Hex()

	}

	if tx.Type() == types.ExternalTxType {
		fields["originatingTxHash"] = tx.OriginatingTxHash()
		fields["etxType"] = hexutil.Uint(tx.EtxType())
	}

	var outBoundEtxs []*RPCTransaction
	for _, tx := range receipt.OutboundEtxs {
		outBoundEtxs = append(outBoundEtxs, newRPCTransaction(tx, blockHash, blockNumber, index, big.NewInt(0), s.b.NodeLocation()))
	}
	if len(receipt.OutboundEtxs) > 0 {
		fields["outboundEtxs"] = outBoundEtxs
	}
	// Assign the effective gas price paid
	header, err := s.b.HeaderByHash(ctx, blockHash)
	if err != nil {
		return nil, err
	}
	if tx.Type() == types.QuaiTxType {
		gasPrice := new(big.Int).Set(tx.GasPrice())
		fields["effectiveGasPrice"] = hexutil.Uint64(gasPrice.Uint64())
	} else {
		// QiTx
		fields["effectiveGasPrice"] = hexutil.Uint64(header.BaseFee().Uint64())
	}

	// Assign receipt status or post state.
	if len(receipt.PostState) > 0 {
		fields["root"] = hexutil.Bytes(receipt.PostState)
	} else {
		fields["status"] = hexutil.Uint(receipt.Status)
	}
	if receipt.Logs == nil {
		fields["logs"] = [][]*types.Log{}
	}
	// If the ContractAddress is 20 0x0 bytes, assume it is not a contract creation
	if !receipt.ContractAddress.Equal(common.Zero) && !receipt.ContractAddress.Equal(common.Address{}) {
		fields["contractAddress"] = receipt.ContractAddress.Hex()
	}
	return fields, nil
}
