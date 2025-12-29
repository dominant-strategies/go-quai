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
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	quaimath "github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/crypto/musig2"
	"github.com/dominant-strategies/go-quai/internal/telemetry"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
	"google.golang.org/protobuf/proto"
)

var (
	txPropagationMetrics = metrics_config.NewCounterVec("TxPropagation", "Transaction propagation counter")
	txEgressCounter      = txPropagationMetrics.WithLabelValues("egress")
	maxOutpointsRange    = uint32(1000)

	// MuSig2 session management
	musig2Manager musig2.MuSig2Manager
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

func (s *PublicQuaiAPI) ClientVersion() string {
	gitCommit := params.GitCommit
	if len(gitCommit) > 8 {
		gitCommit = gitCommit[:8]
	}
	return "go-quai/" + params.Version.Full() + "-" + gitCommit
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
		return nil, fmt.Errorf("getBalance call can only be made in zone chain, current context is %d", nodeCtx)
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

		utxos, err := s.b.UTXOsByAddress(ctx, addr)
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
	addr := common.Bytes20ToAddress(address.Address().Bytes20(), s.b.NodeLocation())
	if internal, err := addr.InternalAndQuaiAddress(); err == nil {
		balance := rawdb.ReadLockedBalance(s.b.Database(), internal)
		return (*hexutil.Big)(balance), nil
	} else if _, err := addr.InternalAndQiAddress(); err == nil {
		utxos, err := s.b.UTXOsByAddress(ctx, addr)
		if utxos == nil || len(utxos) == 0 || err != nil {
			return (*hexutil.Big)(big.NewInt(0)), err
		}
		currentHeader := s.b.CurrentHeader()
		balance := big.NewInt(0)
		for _, utxo := range utxos {
			if utxo.Lock != nil && currentHeader.Number(s.b.NodeCtx()).Cmp(utxo.Lock) < 0 {
				value := types.Denominations[utxo.Denomination]
				balance.Add(balance, value)
			}
		}
		return (*hexutil.Big)(balance), nil
	}
	return nil, fmt.Errorf("address %s is invalid", addr.Hex())
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

func (s *PublicBlockChainQuaiAPI) GetSupplyAnalyticsForBlock(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (map[string]interface{}, error) {
	header, err := s.b.HeaderByNumberOrHash(ctx, blockNrOrHash)
	if header == nil || err != nil {
		return nil, err
	}
	supplyAddedQuai, supplyRemovedQuai, totalSupplyQuai, supplyAddedQi, supplyRemovedQi, totalSupplyQi, err := rawdb.ReadSupplyAnalyticsForBlock(s.b.Database(), header.Hash())
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"quaiSupplyAdded":   (*hexutil.Big)(supplyAddedQuai),
		"quaiSupplyRemoved": (*hexutil.Big)(supplyRemovedQuai),
		"quaiSupplyTotal":   (*hexutil.Big)(totalSupplyQuai),
		"qiSupplyAdded":     (*hexutil.Big)(supplyAddedQi),
		"qiSupplyRemoved":   (*hexutil.Big)(supplyRemovedQi),
		"qiSupplyTotal":     (*hexutil.Big)(totalSupplyQi),
	}, nil
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
			// This is the conversion reverts from a qi-quai conversion
		} else if tx.Type() == types.ExternalTxType && tx.EtxType() == types.ConversionRevertType && tx.To().IsInQuaiLedgerScope() {
			lockup := new(big.Int).SetUint64(params.ConversionLockPeriod)
			lockup.Add(lockup, currentBlock.Number(nodeCtx))
			value := tx.Value()

			addr20 := common.BytesToAddress(tx.Data()[2:22], currentBlock.Location()).Bytes20()
			binary.BigEndian.PutUint32(addr20[16:], uint32(currentBlock.NumberU64(nodeCtx)))

			if _, ok := addressMap[common.AddressBytes(addr20.Bytes())]; !ok {
				continue
			}

			txGas := tx.Gas()
			if txGas < params.TxGas {
				continue
			}
			txGas -= params.TxGas
			denominations := misc.FindMinDenominations(value)
			outputIndex := uint16(0)
			// Iterate over the denominations in descending order
			for denomination := types.MaxDenomination; denomination > types.MaxTrimDenomination; denomination-- {

				for j := uint64(0); j < denominations[uint8(denomination)]; j++ {
					if txGas < params.CallValueTransferGas || outputIndex >= types.MaxOutputIndex {
						// No more gas, the rest of the denominations are lost but the tx is still valid
						break
					}
					txGas -= params.CallValueTransferGas
					// the ETX hash is guaranteed to be unique
					addressToCreatedDeletedToTxHashToOutputs[addr20.String()]["created"][tx.Hash().String()] =
						append(addressToCreatedDeletedToTxHashToOutputs[addr20.String()]["created"][tx.Hash().String()], map[string]interface{}{
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
		response := header.RPCMarshalHeader(s.b.RpcVersion())
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
		return header.RPCMarshalHeader(s.b.RpcVersion())
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
		return uncles[index].RPCMarshalWorkObjectHeader(s.b.RpcVersion()), nil
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
		return uncles[index].RPCMarshalWorkObjectHeader(s.b.RpcVersion()), nil
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
		return uncles[index].RPCMarshalWorkObjectHeader(s.b.RpcVersion()), nil
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
		if blockNr, ok := bNrOrHash.Number(); ok {
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
		primeTerminus := s.b.GetBlockByHash(header.PrimeTerminusHash())
		estimatedQiAmount := misc.QuaiToQi(header, primeTerminus.ExchangeRate(), header.Difficulty(), args.Value.ToInt())
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
		primeTerminus := s.b.GetBlockByHash(s.b.CurrentBlock().PrimeTerminusHash())
		if primeTerminus == nil {
			return nil, errors.New("prime terminus not found")
		}
		qiBaseFee := misc.QuaiToQi(header, primeTerminus.ExchangeRate(), header.Difficulty(), quaiBaseFee)
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

	primeTerminus := s.b.GetBlockByHash(s.b.CurrentBlock().PrimeTerminusHash())
	if primeTerminus == nil {
		return nil, errors.New("cannot find prime terminus for the current block")
	}
	exchangeRate := primeTerminus.ExchangeRate()
	feeInQi := misc.QuaiToQi(header, exchangeRate, header.Difficulty(), feeInQuai)
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
	fields["woHeader"] = block.WorkObjectHeader().RPCMarshalWorkObjectHeader(backend.RpcVersion())
	fields["header"] = block.Body().Header().RPCMarshalHeader()
	fields["size"] = hexutil.Uint64(block.Size())
	_, order, err := backend.CalcOrder(block)
	if err != nil {
		return nil, err
	}
	fields["order"] = order

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
		rpcMarshalUncle := uncle.RPCMarshalWorkObjectHeader(backend.RpcVersion())
		validity := backend.UncleWorkShareClassification(uncle)

		switch validity {
		case types.Valid:
			marshalWorkShares = append(marshalWorkShares, rpcMarshalUncle)
		case types.Block:
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

// BlockTemplateRequest represents a getblocktemplate request
type BlockTemplateRequest struct {
	Rules       []string                `json:"rules,omitempty"`       // "kawpow", "sha", "scrypt"
	ExtraNonce1 string                  `json:"extranonce1,omitempty"` // 4 byte hex string
	ExtraNonce2 string                  `json:"extranonce2,omitempty"` // 8 byte hex string
	ExtraData   string                  `json:"extradata,omitempty"`   // string less than 30 bytes
	Coinbase    common.MixedcaseAddress `json:"coinbase,omitempty"`
}

// GetBlockTemplate retrieves a new block template to mine
// Supports Bitcoin-compatible getblocktemplate API with PoW algorithm selection via "rules" parameter
func (s *PublicBlockChainQuaiAPI) GetBlockTemplate(ctx context.Context, request *BlockTemplateRequest) (map[string]interface{}, error) {
	// Default to KAWPOW if no request or rules specified
	powId := types.Kawpow

	// Parse rules to determine PoW algorithm
	if request != nil && len(request.Rules) > 0 {
		for _, rule := range request.Rules {
			ruleLower := strings.ToLower(rule)
			switch ruleLower {
			case "kawpow":
				powId = types.Kawpow
			case "sha", "sha256d":
				powId = types.SHA_BCH
			case "scrypt":
				powId = types.Scrypt
			default:
				return nil, fmt.Errorf("unsupported rule: %s", rule)
			}
		}
	}

	var coinbase common.Address
	if request != nil && !request.Coinbase.Address().Equal(common.Address{}) {
		coinbase = common.Bytes20ToAddress(request.Coinbase.Address().Bytes20(), s.b.NodeLocation())

		_, err := coinbase.InternalAddress()
		if err != nil {
			return nil, fmt.Errorf("out of scope or invalid coinbase address: %w", err)
		}
	}

	// Get the pending header for the specified PoW algorithm
	pendingHeader, err := s.b.GetPendingHeader(powId, coinbase, []byte{})
	if err != nil {
		return nil, err
	}
	return s.marshalAuxPowTemplate(pendingHeader, request)
}

// marshalAuxPowTemplate is a method wrapper that calls the standalone MarshalAuxPowTemplate function
func (s *PublicBlockChainQuaiAPI) marshalAuxPowTemplate(wo *types.WorkObject, request *BlockTemplateRequest) (map[string]interface{}, error) {
	powID := wo.AuxPow().PowID()
	var extraNonce1, extraNonce2, extraData string
	if request != nil {
		extraNonce1 = request.ExtraNonce1
		extraNonce2 = request.ExtraNonce2
		extraData = request.ExtraData
	}
	return MarshalAuxPowTemplate(wo, powID, extraNonce1, extraNonce2, extraData)
}

// MarshalAuxPowTemplate formats a WorkObject as a SHA256d/Bitcoin getblocktemplate response.
// This is an exported standalone function so it can be used by both the RPC API and subscriptions.
// The powID parameter determines which algorithm's target to calculate.
// The extraNonce1, extraNonce2, and extraData parameters are optional hex strings for customizing the coinbase.
func MarshalAuxPowTemplate(wo *types.WorkObject, powID types.PowID, extraNonce1Hex, extraNonce2Hex, extraDataStr string) (map[string]interface{}, error) {

	auxPow := wo.AuxPow()
	if auxPow == nil {
		return nil, errors.New("no AuxPow in pending header")
	}

	// Get the header first
	auxHeader := auxPow.Header()
	if auxHeader == nil {
		return nil, errors.New("no header in AuxPow")
	}

	blockHeight := auxHeader.Height() // Default to header height

	if tx := auxPow.Transaction(); tx != nil {
		scriptSig := types.ExtractScriptSigFromCoinbaseTx(tx)
		// Extract the block height from the coinbase scriptSig
		extractedHeight, err := types.ExtractHeightFromCoinbase(scriptSig)
		if err == nil {
			// Use the extracted height from scriptSig if successful
			blockHeight = extractedHeight
		} else {
			return nil, fmt.Errorf("failed to extract height from coinbase scriptSig: %w", err)
		}

	}

	txBytes := auxPow.Transaction()

	// Build coinb1/coinb2 following Stratum's mining.notify convention:
	// - coinb1: entire start of the tx up to the start of the combined extranonce push data
	// <12 bytes of data>
	// - coinb2: everything after the combined extranonce push data (coinb2 includes the 32 bytes of extradata)
	coinb1, coinb2, err := types.ExtractCoinb1AndCoinb2FromAuxPowTx(txBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to extract coinb1 and coinb2 from AuxPowTx: %w", err)
	}

	if len(txBytes) == 0 {
		return nil, errors.New("empty coinbase tx bytes in AuxPow")
	}

	// extranonce1, extranonce2, and extradata are optional fields
	var extraNonce1 [4]byte
	var extraNonce2 [8]byte
	if len(extraNonce1Hex) > 0 {
		extraNonce1Bytes, err := hex.DecodeString(extraNonce1Hex)
		if err != nil {
			return nil, fmt.Errorf("invalid extranonce1: %w", err)
		}
		if len(extraNonce1Bytes) > 4 {
			return nil, fmt.Errorf("extranonce1 length exceeds 4 bytes")
		}
		copy(extraNonce1[:], extraNonce1Bytes)
	}
	if len(extraNonce2Hex) > 0 {
		extraNonce2Bytes, err := hex.DecodeString(extraNonce2Hex)
		if err != nil {
			return nil, fmt.Errorf("invalid extranonce2: %w", err)
		}
		if len(extraNonce2Bytes) > 8 {
			return nil, fmt.Errorf("extranonce2 length exceeds 8 bytes")
		}
		copy(extraNonce2[:], extraNonce2Bytes)
	}
	if len(extraDataStr) > 0 {
		extraDataBytes := []byte(extraDataStr)
		if len(extraDataBytes) > 30 {
			return nil, fmt.Errorf("input extraData length exceeds 30 bytes")
		}
		// update the first 30 bytes of the coinb2
		copy(coinb2[:30], extraDataBytes)
	}

	// Build the updated transaction bytes by concatenating coinb1, extraNonce1, extraNonce2, and coinb2
	updatedTxBytes := append(coinb1, extraNonce1[:]...)
	updatedTxBytes = append(updatedTxBytes, extraNonce2[:]...)
	updatedTxBytes = append(updatedTxBytes, coinb2...)

	// Get header fields using the typed accessor methods
	prevBlock := auxHeader.PrevBlock()
	// prevBlock needs to be big-endian for getblocktemplate
	for i := 0; i < len(prevBlock)/2; i++ {
		prevBlock[i], prevBlock[len(prevBlock)-1-i] = prevBlock[len(prevBlock)-1-i], prevBlock[i]
	}

	// Convert previousblockhash to hex without 0x prefix
	prevBlockHex := hex.EncodeToString(prevBlock[:])

	// Marshal merkle branch
	// Note: Internally stored in little-endian, but getblocktemplate expects big-endian (display order)
	merkleBranch := make([]string, len(auxPow.MerkleBranch()))
	for i, hash := range auxPow.MerkleBranch() {
		// Reverse bytes from little-endian to big-endian for getblocktemplate compatibility
		reversed := make([]byte, len(hash))
		for j := range hash {
			reversed[j] = hash[len(hash)-1-j]
		}
		merkleBranch[i] = hex.EncodeToString(reversed)
	}

	merkleRoot := types.CalculateMerkleRoot(auxPow.PowID(), updatedTxBytes, auxPow.MerkleBranch())

	// Convert bits to hex without 0x prefix
	bitsHex := fmt.Sprintf("%08x", auxHeader.Bits())

	var targetHex string
	// Get target hex from Quai difficulty (not AuxPow bits)
	// based on the powid need to use the correct target calculation
	switch powID {
	case types.SHA_BCH, types.SHA_BTC:
		// Bitcoin-style target calculation
		targetHex = common.GetTargetInHex(wo.WorkObjectHeader().ShaDiffAndCount().Difficulty())
	case types.Scrypt:
		targetHex = common.GetTargetInHex(wo.WorkObjectHeader().ScryptDiffAndCount().Difficulty())
	case types.Kawpow:
		// share diff
		kawpowDiff := core.CalculateKawpowShareDiff(wo.WorkObjectHeader())
		targetHex = common.GetTargetInHex(kawpowDiff)
	}

	if len(targetHex) > 2 && targetHex[:2] == "0x" {
		targetHex = targetHex[2:]
	}

	// Set the pendingheader time to empty
	// This ensures the sealHash is stable and doesn't change every second
	woCopy := types.CopyWorkObjectHeader(wo.WorkObjectHeader())
	woCopy.SetTime(0)

	sealHashString := hex.EncodeToString(woCopy.SealHash().Bytes()[:6])

	return map[string]interface{}{
		"version":           auxHeader.Version(),
		"previousblockhash": prevBlockHex,
		// Miner target aligned with AuxPow header nBits (BCH/BTC style)
		"target":            targetHex,
		"mintime":           auxHeader.Timestamp(),
		"noncerange":        "00000000ffffffff", // 32-bit nonce
		"sigoplimit":        80000,              // Bitcoin default
		"sizelimit":         1000000,            // 1MB
		"curtime":           auxHeader.Timestamp(),
		"bits":              bitsHex,
		"height":            uint64(blockHeight),
		"coinb1":            hex.EncodeToString(coinb1),
		"extranonce1Length": 4, // 4 bytes
		"extranonce2Length": 8, // 8 bytes
		// Keep the 30-byte aux extra data inside coinb2; miners do not fill it
		"coinbaseAuxExtraBytesLength": 30, // 32 bytes
		"coinb2":                      hex.EncodeToString(coinb2),
		"merklebranch":                merkleBranch,
		"merkleroot":                  hex.EncodeToString(common.Hash(merkleRoot).Reverse().Bytes()),
		"quairoot":                    sealHashString,
		"quaiheight":                  wo.WorkObjectHeader().NumberU64(),
		"quaidifficulty":              wo.WorkObjectHeader().Difficulty(),
	}, nil
}

func (s *PublicBlockChainQuaiAPI) SubmitScryptBlock(ctx context.Context, raw hexutil.Bytes) (map[string]interface{}, error) {
	hash, number, validity, err := s.b.SubmitBlock(raw, types.Scrypt)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]interface{})
	fields["number"] = hexutil.Uint64(number)
	fields["hash"] = hash.Hex()
	switch validity {
	case types.Sub:
		fields["status"] = hexutil.Uint64(0)
	case types.Valid:
		fields["status"] = hexutil.Uint64(1)
	case types.Block:
		fields["status"] = hexutil.Uint64(2)
	}
	return fields, nil
}

func (s *PublicBlockChainQuaiAPI) SubmitKawpowBlock(ctx context.Context, raw hexutil.Bytes) (map[string]interface{}, error) {
	hash, number, validity, err := s.b.SubmitBlock(raw, types.Kawpow)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]interface{})
	fields["number"] = hexutil.Uint64(number)
	fields["hash"] = hash.Hex()

	switch validity {
	case types.Sub:
		fields["status"] = hexutil.Uint64(0)
	case types.Valid:
		fields["status"] = hexutil.Uint64(1)
	case types.Block:
		fields["status"] = hexutil.Uint64(2)
	}
	return fields, nil
}

func (s *PublicBlockChainQuaiAPI) SubmitShaBlock(ctx context.Context, raw hexutil.Bytes) (map[string]interface{}, error) {
	hash, number, validity, err := s.b.SubmitBlock(raw, types.SHA_BCH)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]interface{})
	fields["number"] = hexutil.Uint64(number)
	fields["hash"] = hash.Hex()

	switch validity {
	case types.Sub:
		fields["status"] = hexutil.Uint64(0)
	case types.Valid:
		fields["status"] = hexutil.Uint64(1)
	case types.Block:
		fields["status"] = hexutil.Uint64(2)
	}
	return fields, nil
}

// ReceiveMinedHeader will run checks on the block and add to canonical chain if valid.
func (s *PublicBlockChainQuaiAPI) ReceiveMinedHeader(ctx context.Context, raw hexutil.Bytes) error {
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

	return s.b.ReceiveMinedHeader(woHeader)
}

// Receives a WorkObjectHeader in the form of bytes, decodes it, then calls ReceiveWorkShare.
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

	workShareHeader := &types.WorkObjectHeader{}
	err = workShareHeader.ProtoDecode(protoWorkShare, s.b.NodeLocation())
	if err != nil {
		return err
	}

	if nodeCtx == common.ZONE_CTX && s.b.ChainConfig().TelemetryEnabled {
		// Track mined workshare (mined LRU)
		telemetry.RecordMinedHeader(workShareHeader)
	}

	return s.ReceiveWorkShare(ctx, workShareHeader)
}

func (s *PublicBlockChainQuaiAPI) ReceiveWorkShare(ctx context.Context, workShare *types.WorkObjectHeader) error {
	// Ensure record in mined LRU even if called directly
	return s.b.ReceiveWorkShare(workShare)
}

func (s *PublicBlockChainQuaiAPI) GetPendingHeader(ctx context.Context) (hexutil.Bytes, error) {
	if !s.b.ProcessingState() {
		return nil, errors.New("getPendingHeader call can only be made on chain processing the state")
	}

	pendingHeader, err := s.b.GetPendingHeader(types.Progpow, common.Address{}, []byte{}) // 0 is default progpow
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
	primeTerminus := s.b.GetBlockByHash(header.PrimeTerminusHash())
	if primeTerminus == nil {
		return nil
	} else {
		return (*hexutil.Big)(misc.QiToQuai(header, primeTerminus.ExchangeRate(), primeTerminus.MinerDifficulty(), qiAmount.ToInt()))
	}
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
	primeTerminus := s.b.GetBlockByHash(s.b.CurrentBlock().PrimeTerminusHash())
	if primeTerminus == nil {
		return nil
	} else {
		return (*hexutil.Big)(misc.QuaiToQi(header, primeTerminus.ExchangeRate(), primeTerminus.MinerDifficulty(), quaiAmount.ToInt()))
	}
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

func (s *PublicBlockChainQuaiAPI) GetKQuaiAndUpdateBit(ctx context.Context, blockHash common.Hash) (map[string]interface{}, error) {
	if !s.b.NodeLocation().Equal(common.Location{0, 0}) {
		return nil, errors.New("cannot call GetKQuaiAndUpdateBit in any other chain than cyprus-1")
	}

	kQuai, updateBit, err := s.b.GetKQuaiAndUpdateBit(blockHash)
	if err != nil {
		return nil, err
	}

	fields := make(map[string]interface{})
	fields["kQuai"] = kQuai.String()
	fields["updateBit"] = updateBit

	return fields, nil
}

// CalculateConversionAmount returns the converted amount after applying the
// prime terminus exchange rate, and the conversion flow discount and k quai
// discount
func (s *PublicBlockChainQuaiAPI) CalculateConversionAmount(ctx context.Context, tx TransactionArgs) (*hexutil.Big, error) {
	// sanity checks to make sure that the inputs are correct for the api
	if tx.From == nil {
		return nil, errors.New("from field cannot be nil")
	}
	if tx.To == nil {
		return nil, errors.New("to field cannot be nil")
	}
	if tx.Value == nil {
		return nil, errors.New("cannot convert nil or empty value")
	}
	if tx.From.IsInQuaiLedgerScope() && tx.To.IsInQuaiLedgerScope() ||
		tx.From.IsInQiLedgerScope() && tx.To.IsInQiLedgerScope() {
		return nil, errors.New("from and to cannot be in the same ledger for conversion")
	}

	currentHeader := s.b.CurrentBlock()
	primeTerminus := s.b.GetBlockByHash(currentHeader.PrimeTerminusHash())
	if primeTerminus == nil {
		return nil, errors.New("cannot find the prime terminus of the current header")
	}

	exchangeRate := primeTerminus.ExchangeRate()
	minerDifficulty := primeTerminus.MinerDifficulty()
	value := tx.Value.ToInt()

	// calculate the 10% of the original value
	tenPercentOriginalValue := new(big.Int).Mul(value, common.Big10)
	tenPercentOriginalValue = new(big.Int).Div(tenPercentOriginalValue, common.Big100)

	// cubic discount calculation only works on quai value, so the qi value has
	// to be changed to quai value
	var quaiAmount *big.Int
	if tx.From.IsInQuaiLedgerScope() {
		quaiAmount = new(big.Int).Set(value)
	} else {
		quaiAmount = misc.QiToQuai(primeTerminus, exchangeRate, minerDifficulty, value)
	}

	// Apply the cubic conversion flow discount and k quai discount
	cubicDiscount := misc.ApplyCubicDiscount(quaiAmount, primeTerminus.ConversionFlowAmount())
	valueAfterCubicDiscount, _ := cubicDiscount.Int(nil)

	// Since rest of the calculation assumes the value is in the origin ledger,
	// we need to convert it back to the original ledger for Qi
	if tx.From.IsInQiLedgerScope() {
		value = misc.QuaiToQi(primeTerminus, exchangeRate, minerDifficulty, valueAfterCubicDiscount)
	} else {
		value = valueAfterCubicDiscount
	}

	kQuaiDiscount := primeTerminus.KQuaiDiscount()
	conversionAmountAfterKQuaiDiscount := new(big.Int).Mul(value, new(big.Int).Sub(big.NewInt(params.KQuaiDiscountMultiplier), kQuaiDiscount))
	conversionAmountAfterKQuaiDiscount = new(big.Int).Div(conversionAmountAfterKQuaiDiscount, big.NewInt(params.KQuaiDiscountMultiplier))

	// check if the exchange rate is increasing or decreasing
	var exchangeRateIncreasing bool
	if primeTerminus.NumberU64(common.PRIME_CTX) > params.MinerDifficultyWindow {
		prevBlock, err := s.b.BlockByNumber(ctx, rpc.BlockNumber(primeTerminus.NumberU64(common.PRIME_CTX)-params.MinerDifficultyWindow))
		if err != nil {
			return nil, errors.New("block minerdifficultywindow blocks behind not found")
		}

		if primeTerminus.ExchangeRate().Cmp(prevBlock.ExchangeRate()) > 0 {
			exchangeRateIncreasing = true
		}
	}

	// Quai to Qi conversion
	if tx.From.IsInQuaiLedgerScope() && tx.To.IsInQiLedgerScope() {

		// If the exchange rate is increasing then Quai to Qi conversion will
		// get the k quai discount
		if exchangeRateIncreasing && value.Cmp(common.Big0) != 0 {
			value = new(big.Int).Set(conversionAmountAfterKQuaiDiscount)
		}

		// If the value left is less than the ten percent of the original value,
		// reset it to the ten percent of the original value
		if value.Cmp(tenPercentOriginalValue) < 0 {
			value = new(big.Int).Set(tenPercentOriginalValue)
		}

		value = misc.QuaiToQi(primeTerminus, exchangeRate, minerDifficulty, value)

		return (*hexutil.Big)(value), nil
	}

	// Qi to Quai conversion
	if tx.From.IsInQiLedgerScope() && tx.To.IsInQuaiLedgerScope() {

		// If the exchange rate is decreasing then Qi to Quai conversion will
		// get the k quai discount
		if !exchangeRateIncreasing && value.Cmp(common.Big0) != 0 {
			value = new(big.Int).Set(conversionAmountAfterKQuaiDiscount)
		}

		// If the value left is less than the ten percent of the original value,
		// reset it to the ten percent of the original value
		if value.Cmp(tenPercentOriginalValue) < 0 {
			value = new(big.Int).Set(tenPercentOriginalValue)
		}

		value = misc.QiToQuai(primeTerminus, exchangeRate, minerDifficulty, value)

		return (*hexutil.Big)(value), nil
	}

	return nil, nil
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

// InitializeMuSig2Manager initializes the global MuSig2 manager
func InitializeMuSig2Manager(privKey *btcec.PrivateKey) error {
	var err error
	musig2Manager, err = musig2.NewManager(privKey)
	if err != nil {
		return fmt.Errorf("failed to initialize MuSig2 manager: %w", err)
	}
	return nil
}

// SignAuxTemplateResponse represents the response from signing an AuxTemplate
type SignAuxTemplateResponse struct {
	PublicNonce      string `json:"publicNonce"`
	PartialSignature string `json:"partialSignature"`
	MessageHash      string `json:"messageHash"`
	PublicKey        string `json:"publicKey"`        // hex-encoded compressed pubkey (33 bytes)
	ParticipantIndex int    `json:"participantIndex"` // local index in the agreed signer set
}

// SignAuxTemplate initiates MuSig2 signing for an AuxTemplate
// otherParticipantPubKey: compressed pubkey (33 bytes) of the other signer
func (s *PublicBlockChainQuaiAPI) SignAuxTemplate(ctx context.Context, templateData hexutil.Bytes, otherParticipantPubKey hexutil.Bytes, otherNonce hexutil.Bytes) (*SignAuxTemplateResponse, error) {
	// Try direct fmt.Println to ensure we're hitting this method
	fmt.Println("DEBUG: SignAuxTemplate called with dataSize:", len(templateData), "otherPubKeyLen:", len(otherParticipantPubKey), "nonceSize:", len(otherNonce))

	s.b.Logger().WithFields(log.Fields{
		"dataSize":       len(templateData),
		"otherPubKeyLen": len(otherParticipantPubKey),
		"nonceSize":      len(otherNonce),
	}).Info("API: SignAuxTemplate called - START")

	// Get the MuSig2 key manager
	keyManager, err := GetMuSig2Keys()
	if err != nil {
		s.b.Logger().WithField("error", err).Error("Failed to get MuSig2 keys")
		return nil, fmt.Errorf("failed to get MuSig2 keys: %w", err)
	}
	if keyManager == nil {
		return nil, fmt.Errorf("MuSig2 keys not configured - set MUSIG2_PRIVKEY environment variable")
	}

	// Decode the protobuf encoded AuxTemplate
	protoTemplate := &types.ProtoAuxTemplate{}
	err = proto.Unmarshal(templateData, protoTemplate)
	if err != nil {
		s.b.Logger().WithField("error", err).Error("Failed to unmarshal AuxTemplate")
		return nil, fmt.Errorf("failed to unmarshal AuxTemplate: %w", err)
	}

	// Convert ProtoAuxTemplate to AuxTemplate to use the Hash() method
	auxTemplate := &types.AuxTemplate{}
	if err := auxTemplate.ProtoDecode(protoTemplate); err != nil {
		return nil, fmt.Errorf("failed to decode AuxTemplate: %w", err)
	}
	messageHash := auxTemplate.Hash()
	message := messageHash[:]

	s.b.Logger().WithFields(log.Fields{
		"messageHash":  hex.EncodeToString(message[:]),
		"templateSize": len(templateData),
	}).Info("Creating signing message from AuxTemplate")

	// Parse other participant pubkey and derive index from configured set
	if len(otherParticipantPubKey) == 0 {
		return nil, fmt.Errorf("other participant pubkey is required")
	}
	parsedOtherPK, err := btcec.ParsePubKey(otherParticipantPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse other participant pubkey: %w", err)
	}
	otherParticipantIndex := -1
	for i := 0; i < len(keyManager.AllPublicKeys); i++ {
		if keyManager.AllPublicKeys[i] != nil && keyManager.AllPublicKeys[i].IsEqual(parsedOtherPK) {
			otherParticipantIndex = i
			break
		}
	}
	if otherParticipantIndex == -1 {
		return nil, fmt.Errorf("other participant pubkey not found in configured MuSig2 public keys")
	}
	if otherParticipantIndex == keyManager.ParticipantIndex {
		return nil, fmt.Errorf("other participant cannot be the same as this participant")
	}

	// Create a musig2.Manager from the key manager's private key
	signingManager, err := musig2.NewManager(keyManager.PrivateKey)
	if err != nil {
		s.b.Logger().WithField("error", err).Error("Failed to create signing manager")
		return nil, fmt.Errorf("failed to create signing manager: %w", err)
	}

	// Log the participant index to verify it's correct
	s.b.Logger().WithFields(log.Fields{
		"participantIndex": signingManager.GetParticipantIndex(),
		"expectedIndex":    keyManager.ParticipantIndex,
		"otherIndex":       otherParticipantIndex,
	}).Info("Created MuSig2 signing manager")

	// Create signing session
	session, err := signingManager.NewSigningSession(message[:], otherParticipantIndex)
	if err != nil {
		s.b.Logger().WithField("error", err).Error("Failed to create signing session")
		return nil, fmt.Errorf("failed to create signing session: %w", err)
	}

	// Get our public nonce
	ourNonce := session.GetPublicNonce()

	s.b.Logger().WithFields(log.Fields{
		"ourNonce": hex.EncodeToString(ourNonce),
	}).Info("Generated our nonce")

	// If other nonce is provided, complete the partial signature
	var partialSig []byte
	if len(otherNonce) > 0 {
		s.b.Logger().WithFields(log.Fields{
			"otherNonce": hex.EncodeToString(otherNonce),
		}).Info("Registering other party's nonce")

		// Register the other party's nonce
		err = session.RegisterOtherNonce(otherNonce)
		if err != nil {
			s.b.Logger().WithField("error", err).Error("Failed to register other nonce")
			return nil, fmt.Errorf("failed to register other nonce: %w", err)
		}

		// Create our partial signature
		partialSig, err = session.CreatePartialSignature()
		if err != nil {
			s.b.Logger().WithField("error", err).Error("Failed to create partial signature")
			return nil, fmt.Errorf("failed to create partial signature: %w", err)
		}

		s.b.Logger().WithFields(log.Fields{
			"partialSig": hex.EncodeToString(partialSig),
			"sigLen":     len(partialSig),
		}).Info("Created partial signature")
	}

	response := &SignAuxTemplateResponse{
		PublicNonce:      hex.EncodeToString(ourNonce),
		MessageHash:      hex.EncodeToString(message[:]),
		PartialSignature: "",
		// Expose our pubkey and local index to help the counterparty construct a canonical signer set
		PublicKey:        hex.EncodeToString(keyManager.PublicKey.SerializeCompressed()),
		ParticipantIndex: signingManager.GetParticipantIndex(),
	}
	if len(partialSig) > 0 {
		response.PartialSignature = hex.EncodeToString(partialSig)
	}

	s.b.Logger().WithFields(log.Fields{
		"nonceLen":      len(ourNonce),
		"hasPartialSig": len(partialSig) > 0,
	}).Info("SignAuxTemplate completed")

	return response, nil
}

// SubmitAuxTemplate receives and processes a fully signed AuxTemplate
func (s *PublicBlockChainQuaiAPI) SubmitAuxTemplate(ctx context.Context, templateData hexutil.Bytes) error {
	s.b.Logger().WithFields(log.Fields{
		"dataSize": len(templateData),
	}).Debug("API: SubmitAuxTemplate called")

	// Decode the protobuf encoded AuxTemplate
	protoTemplate := &types.ProtoAuxTemplate{}
	err := proto.Unmarshal(templateData, protoTemplate)
	if err != nil {
		s.b.Logger().WithField("error", err).Error("Failed to unmarshal AuxTemplate")
		return fmt.Errorf("failed to unmarshal AuxTemplate: %w", err)
	}

	// Create AuxTemplate from proto for logging and broadcast
	auxTemplate := &types.AuxTemplate{}
	err = auxTemplate.ProtoDecode(protoTemplate)
	if err != nil {
		s.b.Logger().WithField("error", err).Error("Failed to decode AuxTemplate")
		return fmt.Errorf("failed to decode AuxTemplate: %w", err)
	}

	// Verify the embedded MuSig2 composite signature in the AuxTemplate
	if !auxTemplate.VerifySignature() {
		s.b.Logger().Error("API: Invalid signature on AuxTemplate (embedded)")
		return fmt.Errorf("invalid signature on AuxTemplate (embedded)")
	}

	// For observability and signing message
	messageHash := auxTemplate.Hash()
	messageHashHex := hex.EncodeToString(messageHash[:])
	merkleBranch := make([]string, len(auxTemplate.MerkleBranch()))
	for i, hash := range auxTemplate.MerkleBranch() {
		merkleBranch[i] = hex.EncodeToString(hash)
	}
	s.b.Logger().WithFields(log.Fields{
		"powID":          auxTemplate.PowID(),
		"height":         auxTemplate.Height(),
		"nBits":          fmt.Sprintf("0x%08x", auxTemplate.Bits()),
		"prevHash":       fmt.Sprintf("%x", auxTemplate.PrevHash()),
		"auxPow2":        hex.EncodeToString(auxTemplate.AuxPow2()),
		"messageHash":    messageHashHex,
		"signature":      hex.EncodeToString(auxTemplate.Sigs()),
		"merkleBranch":   merkleBranch,
		"version":        auxTemplate.Version(),
		"signatureTime":  auxTemplate.SignatureTime(),
		"coinbaseOutLen": len(auxTemplate.CoinbaseOut()),
		"coinbaseOut":    hex.EncodeToString(auxTemplate.CoinbaseOut()),
	}).Info("✅ Received signed AuxTemplate with valid Signature")

	// Broadcast the template to the network. The final signature is embedded in the template.
	err = s.b.BroadcastAuxTemplate(auxTemplate, s.b.NodeLocation())
	if err != nil {
		s.b.Logger().WithField("error", err).Error("Failed to broadcast AuxTemplate")
		return fmt.Errorf("failed to broadcast AuxTemplate: %w", err)
	}

	s.b.Logger().Info("Successfully submitted and broadcast signed AuxTemplate")
	return nil
}

// HashesPerQits returns the number of hashes needed to mine the given number of Qits at a given block.
// This is calculated by multiplying the number of Qits by OneOverKqi (hashes per Qit).
//
// Parameters:
//   - qiAmount: The number of Qits (note: 1 Qi = 1000 Qit)
//   - blockNrOrHash: The block number or hash to query
//
// Returns:
//   - The total number of hashes required
//
// Example:
//
//	// How many hashes to mine 1 Qi (1000 Qit)?
//	hashes := HashesPerQits(ctx, 1000, "latest")
//
//	// How many hashes to mine 0.001 Qi (1 Qit)?
//	hashes := HashesPerQits(ctx, 1, "latest")
func (s *PublicBlockChainQuaiAPI) HashesPerQits(ctx context.Context, qiAmount hexutil.Big, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	// Handle zero or negative amounts
	if qiAmount.ToInt().Sign() <= 0 {
		return (*hexutil.Big)(big.NewInt(0)), nil
	}
	blockNumber, ok := blockNrOrHash.Number()
	if !ok {
		return nil, errors.New("invalid block number or hash")
	}

	// If "latest" (-1), get the current block number
	var actualBlockNumber uint64
	if blockNumber == rpc.LatestBlockNumber {
		actualBlockNumber = s.b.CurrentHeader().NumberU64(s.b.NodeCtx())
	} else {
		actualBlockNumber = uint64(blockNumber)
	}

	// Calculate: qiAmount * OneOverKqi(blockNumber)
	// OneOverKqi returns hashes per 1 Qit
	hashesPerQit := params.OneOverKqi(actualBlockNumber)
	totalHashes := new(big.Int).Mul(qiAmount.ToInt(), hashesPerQit)

	return (*hexutil.Big)(totalHashes), nil
}

func (s *PublicBlockChainQuaiAPI) GetQuaiHeaderForDonorHash(ctx context.Context, donorHash common.Hash) (map[string]interface{}, error) {
	header := s.b.GetQuaiHeaderForDonorHash(donorHash)
	if header == nil {
		return nil, errors.New("no quai header found for donor hash")
	}
	return header.RPCMarshalWorkObjectHeader("v2"), nil
}

func (s *PublicBlockChainQuaiAPI) GetCoinbaseTxForWorkShareHash(ctx context.Context, workShareHash common.Hash) (*RPCTransaction, error) {
	block := s.b.GetBlockForWorkShareHash(workShareHash)
	// go through the block and find the tx hash that has the workshare hash in the data
	if block == nil {
		return nil, errors.New("no block found for workshare hash")
	}
	for index, tx := range block.Transactions() {
		if tx.Type() == types.ExternalTxType && tx.EtxType() == types.CoinbaseType {
			if len(tx.Data()) < common.HashLength {
				continue
			}
			shareHash := common.BytesToHash(tx.Data()[len(tx.Data())-common.HashLength:])
			if shareHash == workShareHash {
				return newRPCTransaction(tx, block.Hash(), block.NumberU64(s.b.NodeCtx()), uint64(index), block.BaseFee(), s.b.NodeLocation()), nil
			}
		}
	}
	return nil, errors.New("no coinbase transaction found for workshare hash")
}

func (s *PublicBlockChainQuaiAPI) GetSubsidyChainHeight(ctx context.Context) (map[string]interface{}, error) {
	if !s.b.NodeLocation().Equal(common.Location{0, 0}) {
		return nil, errors.New("cannot call GetSubsidyChainHeight in any other chain than cyprus-1")
	}
	fields := make(map[string]interface{})
	at := s.b.GetBestAuxTemplate(types.Kawpow)
	if at != nil {
		fields["ravencoin"] = hexutil.Uint64(at.Height())
	}
	at = s.b.GetBestAuxTemplate(types.SHA_BCH)
	if at != nil {
		fields["bch"] = hexutil.Uint64(at.Height())
	}
	at = s.b.GetBestAuxTemplate(types.Scrypt)
	if at != nil {
		fields["ltc"] = hexutil.Uint64(at.Height())
	}
	if len(fields) == 0 {
		return nil, errors.New("no subsidy chain height found")
	}
	return fields, nil
}

// GetMiningInfo returns the current mining difficulty per algorithm and the reward per workshare.
// This is useful for miners to understand the current mining parameters.
// If decimal is true, big integer values are returned as decimal strings instead of hex.
func (s *PublicBlockChainQuaiAPI) GetMiningInfo(ctx context.Context, decimal *bool) (map[string]interface{}, error) {
	if s.b.NodeLocation().Context() != common.ZONE_CTX {
		return nil, errors.New("getMiningInfo can only be called in zone chain")
	}

	currentHeader := s.b.CurrentHeader()
	if currentHeader == nil {
		return nil, errors.New("cannot get current header")
	}

	// Check if we're past the KawPow fork
	if currentHeader.PrimeTerminusNumber().Uint64() < params.KawPowForkBlock {
		return nil, errors.New("getMiningInfo is only available after the KawPow fork")
	}

	// Helper to format big.Int as decimal string or hex based on decimal flag
	formatBigInt := func(n *big.Int) interface{} {
		if decimal != nil && *decimal {
			return n.String()
		}
		return (*hexutil.Big)(n)
	}

	fields := make(map[string]interface{})

	// Get difficulties per algorithm
	kawpowDiff := core.CalculateKawpowShareDiff(currentHeader.WorkObjectHeader())
	shaDiff := currentHeader.WorkObjectHeader().ShaDiffAndCount().Difficulty()
	scryptDiff := currentHeader.WorkObjectHeader().ScryptDiffAndCount().Difficulty()

	fields["kawpowDifficulty"] = formatBigInt(kawpowDiff)
	fields["shaDifficulty"] = formatBigInt(shaDiff)
	fields["scryptDifficulty"] = formatBigInt(scryptDiff)

	// Get exchange rate from prime terminus for reward calculation
	primeTerminus := s.b.GetBlockByHash(currentHeader.PrimeTerminusHash())
	if primeTerminus == nil {
		return nil, errors.New("cannot find the prime terminus")
	}
	exchangeRate := primeTerminus.ExchangeRate()

	// Calculate base block reward (in Quai) without fees
	baseBlockReward := misc.CalculateQuaiReward(currentHeader.WorkObjectHeader(), currentHeader.Difficulty(), exchangeRate)

	// Calculate average block time and total fees over the last 15 minutes
	const targetDuration = 15 * 60 // 15 minutes in seconds
	currentTime := currentHeader.Time()
	cutoffTime := currentTime - uint64(targetDuration)

	var blockCount int64
	var oldestBlockTime uint64

	var startKawpowTime, endKawpowTime uint64
	var startShaTime, endShaTime uint64
	var startScryptTime, endScryptTime uint64

	var totalKawpowShares, totalShaShares, totalScryptShares uint64

	block := currentHeader

	for block != nil && block.Time() > cutoffTime {
		blockCount++
		oldestBlockTime = block.Time()

		if startKawpowTime == 0 || block.Time() <= startKawpowTime {
			startKawpowTime = block.Time()
		}
		if endKawpowTime == 0 || block.Time() >= endKawpowTime {
			endKawpowTime = block.Time()
		}
		totalKawpowShares++
		for _, share := range block.Uncles() {
			if share.AuxPow() != nil {
				switch share.AuxPow().PowID() {
				case types.Kawpow:
					if startKawpowTime == 0 || share.Time() <= startKawpowTime {
						startKawpowTime = share.Time()
					}
					if endKawpowTime == 0 || share.Time() >= endKawpowTime {
						endKawpowTime = share.Time()
					}
					totalKawpowShares++
				case types.SHA_BCH, types.SHA_BTC:
					if startShaTime == 0 || share.Time() <= startShaTime {
						startShaTime = share.Time()
					}
					if endShaTime == 0 || share.Time() >= endShaTime {
						endShaTime = share.Time()
					}
					totalShaShares++
				case types.Scrypt:
					if startScryptTime == 0 || share.Time() <= startScryptTime {
						startScryptTime = share.Time()
					}
					if endScryptTime == 0 || share.Time() >= endScryptTime {
						endScryptTime = share.Time()
					}
					totalScryptShares++
				}
			}
		}

		// Get parent block
		parentHash := block.ParentHash(common.ZONE_CTX)
		if s.b.IsGenesisHash(parentHash) {
			break
		}
		block = s.b.GetBlockByHash(parentHash)

	}

	var avgBlockTime float64
	if blockCount > 1 {
		// Average block time = time span / (number of blocks - 1)
		timeSpan := currentTime - oldestBlockTime
		avgBlockTime = float64(timeSpan) / float64(blockCount-1)
	} else {
		avgBlockTime = 5.0 // Default to target block time
	}

	fields["avgBlockTime"] = math.Round(avgBlockTime*1000) / 1000 // Round to 3 decimal places
	fields["avgTxFees"] = formatBigInt(currentHeader.AvgTxFees())
	fields["blocksAnalyzed"] = blockCount

	// Calculate estimated workshare reward including average fees
	// Workshare reward = (baseReward + avgTxFees) / (ExpectedWorksharesPerBlock + 1)
	estimatedBlockReward := new(big.Int).Set(baseBlockReward)
	estimatedBlockReward = new(big.Int).Add(estimatedBlockReward, new(big.Int).Mul(currentHeader.AvgTxFees(), common.Big2))

	workshareReward := new(big.Int).Div(estimatedBlockReward, big.NewInt(int64(params.ExpectedWorksharesPerBlock+1)))

	fields["baseBlockReward"] = formatBigInt(baseBlockReward)
	fields["estimatedBlockReward"] = formatBigInt(estimatedBlockReward)
	fields["workshareReward"] = formatBigInt(workshareReward)

	// Calculate average share timing for each algorithm
	// SHA and Scrypt: Count is the EMA of shares per block in 2^32 units
	// avgShareTime = avgBlockTime / (Count / 2^32) = avgBlockTime * 2^32 / Count
	shaShareCount := currentHeader.WorkObjectHeader().ShaDiffAndCount().Count()
	scryptShareCount := currentHeader.WorkObjectHeader().ScryptDiffAndCount().Count()

	// SHA average share time
	if shaShareCount != nil && shaShareCount.Cmp(common.Big0) > 0 {
		shaSharesPerBlock := new(big.Float).Quo(new(big.Float).SetInt(shaShareCount), new(big.Float).SetInt(common.Big2e32))
		shaSharesPerBlockFloat, _ := shaSharesPerBlock.Float64()
		if shaSharesPerBlockFloat > 0 {
			fields["avgShaShareTime"] = math.Round(avgBlockTime/shaSharesPerBlockFloat*1000) / 1000
		} else {
			fields["avgShaShareTime"] = float64(0)
		}
	} else {
		fields["avgShaShareTime"] = float64(0)
	}

	// Scrypt average share time
	if scryptShareCount != nil && scryptShareCount.Cmp(common.Big0) > 0 {
		scryptSharesPerBlock := new(big.Float).Quo(new(big.Float).SetInt(scryptShareCount), new(big.Float).SetInt(common.Big2e32))
		scryptSharesPerBlockFloat, _ := scryptSharesPerBlock.Float64()
		if scryptSharesPerBlockFloat > 0 {
			fields["avgScryptShareTime"] = math.Round(avgBlockTime/scryptSharesPerBlockFloat*1000) / 1000
		} else {
			fields["avgScryptShareTime"] = float64(0)
		}
	} else {
		fields["avgScryptShareTime"] = float64(0)
	}

	// Get share targets and convert to expected shares per block (divide by 2^32)
	shaShareTarget := currentHeader.WorkObjectHeader().ShaShareTarget()
	scryptShareTarget := currentHeader.WorkObjectHeader().ScryptShareTarget()

	// KawPow average share time
	// Uses same logic as CalculateKawpowShareDiff in headerchain.go:
	// kawpowShareTarget = (ExpectedWorksharesPerBlock * 2^32 + 2^32) - min(shaCount, shaTarget) - min(scryptCount, scryptTarget)
	// expectedKawpowSharesPerBlock = kawpowShareTarget / 2^32
	shaSharesAvg := new(big.Int)
	scryptSharesAvg := new(big.Int)
	if shaShareCount != nil && shaShareTarget != nil {
		shaSharesAvg = quaimath.BigMin(shaShareCount, shaShareTarget)
	}
	if scryptShareCount != nil && scryptShareTarget != nil {
		scryptSharesAvg = quaimath.BigMin(scryptShareCount, scryptShareTarget)
	}

	maxTarget := new(big.Int).Mul(big.NewInt(int64(params.ExpectedWorksharesPerBlock)), common.Big2e32)
	nonKawpowShares := new(big.Int).Add(shaSharesAvg, scryptSharesAvg)

	// If non-kawpow shares >= maxTarget, kawpow only gets the block itself (1 share per block)
	// Since only KawPow can build blocks, there's always at least 1 KawPow "share" per block
	if maxTarget.Cmp(nonKawpowShares) <= 0 {
		fields["avgKawpowShareTime"] = math.Round(avgBlockTime*1000) / 1000
	} else {
		// kawpowShareTarget = maxTarget + 2^32 - nonKawpowShares
		// The +2^32 accounts for the block itself (to get x workshares, divide by x+1)
		kawpowShareTarget := new(big.Int).Sub(new(big.Int).Add(maxTarget, common.Big2e32), nonKawpowShares)
		kawpowSharesPerBlock := new(big.Float).Quo(new(big.Float).SetInt(kawpowShareTarget), new(big.Float).SetInt(common.Big2e32))
		kawpowSharesPerBlockFloat, _ := kawpowSharesPerBlock.Float64()
		if kawpowSharesPerBlockFloat > 0 {
			fields["avgKawpowShareTime"] = math.Round(avgBlockTime/kawpowSharesPerBlockFloat*1000) / 1000
		} else {
			fields["avgKawpowShareTime"] = math.Round(avgBlockTime*1000) / 1000
		}
	}

	// Hash rate = difficulty / average share time (hashes per second)
	// Returns string to avoid float64 overflow with petahash-scale values
	calcHashRate := func(diff *big.Int, totalShares uint64, timeDiff int64) string {
		if diff == nil || totalShares == 0 || timeDiff <= 0 {
			return "0"
		}
		diffFloat := new(big.Float).SetInt(diff)
		totalDiff := new(big.Float).Mul(diffFloat, new(big.Float).SetUint64(totalShares))
		timeFloat := new(big.Float).SetFloat64(float64(timeDiff))
		hashRate := new(big.Float).Quo(totalDiff, timeFloat)
		// Format as integer string (truncate decimals)
		intPart, _ := hashRate.Int(nil)
		return intPart.String()
	}

	fields["kawpowHashRate"] = calcHashRate(kawpowDiff, totalKawpowShares, int64(endKawpowTime)-int64(startKawpowTime))
	fields["shaHashRate"] = calcHashRate(shaDiff, totalShaShares, int64(endShaTime)-int64(startShaTime))
	fields["scryptHashRate"] = calcHashRate(scryptDiff, totalScryptShares, int64(endScryptTime)-int64(startScryptTime))

	// Include current block number and hash for reference
	if decimal != nil && *decimal {
		fields["blockNumber"] = currentHeader.NumberU64(common.ZONE_CTX)
	} else {
		fields["blockNumber"] = hexutil.Uint64(currentHeader.NumberU64(common.ZONE_CTX))
	}
	fields["blockHash"] = currentHeader.Hash()

	// Get quaiSupplyTotal from the database for the current block
	_, _, totalSupplyQuai, _, _, _, err := rawdb.ReadSupplyAnalyticsForBlock(s.b.Database(), currentHeader.Hash())
	if err == nil && totalSupplyQuai != nil {
		fields["quaiSupplyTotal"] = formatBigInt(totalSupplyQuai)
	}

	return fields, nil
}
