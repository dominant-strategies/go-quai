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

package core

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"

	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

//go:generate gencodec -type Genesis -field-override genesisSpecMarshaling -out gen_genesis.go
//go:generate gencodec -type GenesisAccount -field-override genesisAccountMarshaling -out gen_genesis_account.go

var errGenesisNoConfig = errors.New("genesis has no chain configuration")

// Genesis specifies the header fields, state of a genesis block. It also defines hard
// fork switch-over blocks through the chain configuration.
type Genesis struct {
	Config     *params.ChainConfig `json:"config"`
	Nonce      uint64              `json:"nonce"`
	Timestamp  uint64              `json:"timestamp"`
	ExtraData  []byte              `json:"extraData"`
	GasLimit   uint64              `json:"gasLimit"`
	Difficulty *big.Int            `json:"difficulty"`
	Mixhash    common.Hash         `json:"mixHash"`
	Coinbase   common.Address      `json:"coinbase"`
	AllocHash  common.Hash         `json:"allocHash"`

	// These fields are used for consensus tests. Please don't use them
	// in actual genesis blocks.
	Number     []uint64      `json:"number"`
	GasUsed    uint64        `json:"gasUsed"`
	ParentHash []common.Hash `json:"parentHash"`
	BaseFee    *big.Int      `json:"baseFeePerGas"`
}

type GenesisUTXO struct {
	Denomination uint32 `json:"denomination"`
	Index        uint32 `json:"index"`
	Hash         string `json:"hash"`
}

// field type overrides for gencodec
type genesisSpecMarshaling struct {
	Nonce      math.HexOrDecimal64
	Timestamp  math.HexOrDecimal64
	ExtraData  hexutil.Bytes
	GasLimit   math.HexOrDecimal64
	GasUsed    math.HexOrDecimal64
	Number     math.HexOrDecimal64
	Difficulty *math.HexOrDecimal256
	BaseFee    *math.HexOrDecimal256
}

// storageJSON represents a 256 bit byte array, but allows less than 256 bits when
// unmarshaling from hex.
type storageJSON common.Hash

func (h *storageJSON) UnmarshalText(text []byte) error {
	text = bytes.TrimPrefix(text, []byte("0x"))
	if len(text) > 64 {
		return fmt.Errorf("too many hex characters in storage key/value %q", text)
	}
	offset := len(h) - len(text)/2 // pad on the left
	if _, err := hex.Decode(h[offset:], text); err != nil {
		fmt.Println(err)
		return fmt.Errorf("invalid hex storage key/value %q", text)
	}
	return nil
}

func (h storageJSON) MarshalText() ([]byte, error) {
	return hexutil.Bytes(h[:]).MarshalText()
}

// GenesisMismatchError is raised when trying to overwrite an existing
// genesis block with an incompatible one.
type GenesisMismatchError struct {
	Stored, New common.Hash
}

func (e *GenesisMismatchError) Error() string {
	return fmt.Sprintf("database contains incompatible genesis (have %x, new %x)", e.Stored, e.New)
}

// SetupGenesisBlock writes or updates the genesis block in db.
// The block that will be used is:
//
//	                     genesis == nil       genesis != nil
//	                  +------------------------------------------
//	db has no genesis |  main-net default  |  genesis
//	db has genesis    |  from DB           |  genesis (if compatible)
//
// The stored chain configuration will be updated if it is compatible (i.e. does not
// specify a fork block below the local head block). In case of a conflict, the
// error is a *params.ConfigCompatError and the new, unwritten config is returned.
//
// The returned chain configuration is never nil.
func SetupGenesisBlock(db ethdb.Database, genesis *Genesis, nonce uint64, extra []byte, nodeLocation common.Location, logger *log.Logger) (*params.ChainConfig, common.Hash, error) {
	return SetupGenesisBlockWithOverride(db, genesis, nonce, extra, nodeLocation, 0, logger)
}

func SetupGenesisBlockWithOverride(db ethdb.Database, genesis *Genesis, nonce uint64, extra []byte, nodeLocation common.Location, startingExpansionNumber uint64, logger *log.Logger) (*params.ChainConfig, common.Hash, error) {
	if genesis != nil && genesis.Config == nil {
		return params.AllProgpowProtocolChanges, common.Hash{}, errGenesisNoConfig
	}
	// Just commit the new block if there is no stored genesis block.
	stored := rawdb.ReadCanonicalHash(db, 0)
	if (stored == common.Hash{}) {
		if genesis == nil {
			logger.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock(nonce, extra)
		} else {
			logger.Info("Writing custom genesis block")
		}
		block, err := genesis.Commit(db, nodeLocation, startingExpansionNumber)
		if err != nil {
			return genesis.Config, common.Hash{}, err
		}
		return genesis.Config, block.Hash(), nil
	}
	// We have the genesis block in database(perhaps in ancient database)
	// but the corresponding state is missing.
	header := rawdb.ReadHeader(db, 0, stored)
	if _, err := state.New(header.EVMRoot(), header.EtxSetRoot(), header.QuaiStateSize(), state.NewDatabaseWithConfig(db, nil), state.NewDatabaseWithConfig(db, nil), nil, nodeLocation, logger); err != nil {
		if genesis == nil {
			genesis = DefaultGenesisBlock(nonce, extra)
		}
		// Ensure the stored genesis matches with the given one.
		hash := genesis.ToBlock(startingExpansionNumber).Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
		block, err := genesis.Commit(db, nodeLocation, startingExpansionNumber)
		if err != nil {
			return genesis.Config, hash, err
		}
		return genesis.Config, block.Hash(), nil
	}
	// Check whether the genesis block is already written.
	if genesis != nil {
		hash := genesis.ToBlock(startingExpansionNumber).Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
	}
	// Get the existing chain configuration.
	newcfg := genesis.configOrDefault(stored)
	storedcfg := rawdb.ReadChainConfig(db, stored)
	if storedcfg == nil {
		logger.Warn("Found genesis block without chain config")
		rawdb.WriteChainConfig(db, stored, newcfg)
		return newcfg, stored, nil
	}
	// Special case: don't change the existing config of a non-mainnet chain if no new
	// config is supplied. These chains would get AllProtocolChanges (and a compat error)
	// if we just continued here.
	if genesis == nil && stored != params.ProgpowColosseumGenesisHash {
		return storedcfg, stored, nil
	}
	// Check config compatibility and write the config. Compatibility errors
	// are returned to the caller unless we're already at block zero.
	height := rawdb.ReadHeaderNumber(db, rawdb.ReadHeadHeaderHash(db))
	if height == nil {
		return newcfg, stored, fmt.Errorf("missing block number for head header hash")
	}

	rawdb.WriteChainConfig(db, stored, newcfg)
	return newcfg, stored, nil
}

func (g *Genesis) configOrDefault(ghash common.Hash) *params.ChainConfig {
	switch {
	case g != nil:
		return g.Config
	case ghash == params.ProgpowColosseumGenesisHash:
		return params.ProgpowColosseumChainConfig
	case ghash == params.ProgpowGardenGenesisHash:
		return params.ProgpowGardenChainConfig
	case ghash == params.ProgpowOrchardGenesisHash:
		return params.ProgpowOrchardChainConfig
	case ghash == params.ProgpowLighthouseGenesisHash:
		return params.ProgpowLighthouseChainConfig
	case ghash == params.ProgpowLocalGenesisHash:
		return params.ProgpowLocalChainConfig
	// Blake3 chain configs
	case ghash == params.Blake3PowColosseumGenesisHash:
		return params.Blake3PowColosseumChainConfig
	case ghash == params.Blake3PowGardenGenesisHash:
		return params.Blake3PowGardenChainConfig
	case ghash == params.Blake3PowOrchardGenesisHash:
		return params.Blake3PowOrchardChainConfig
	case ghash == params.Blake3PowLighthouseGenesisHash:
		return params.Blake3PowLighthouseChainConfig
	case ghash == params.Blake3PowLocalGenesisHash:
		return params.Blake3PowLocalChainConfig

	default:
		return params.AllProgpowProtocolChanges
	}
}

// ToBlock creates the genesis block and writes state of a genesis specification
// to the given database (or discards it if nil).
func (g *Genesis) ToBlock(startingExpansionNumber uint64) *types.WorkObject {
	wo := types.EmptyWorkObject(g.Config.Location.Context())
	wo.WorkObjectHeader().SetNonce(types.EncodeNonce(g.Nonce))
	wo.WorkObjectHeader().SetDifficulty(g.Difficulty)
	wo.WorkObjectHeader().SetPrimeTerminusNumber(big.NewInt(0))
	wo.WorkObjectHeader().SetTime(g.Timestamp)
	wo.WorkObjectHeader().SetLock(0)
	wo.WorkObjectHeader().SetPrimaryCoinbase(common.Zero)
	wo.Header().SetExtra(append(g.ExtraData, g.AllocHash.Bytes()...))
	wo.Header().SetGasLimit(g.GasLimit)
	wo.Header().SetGasUsed(0)
	wo.Header().SetExpansionNumber(uint8(startingExpansionNumber))
	if startingExpansionNumber > 0 {
		// Fill each byte with 0xFF to set all bits to 1
		var etxEligibleSlices common.Hash
		for i := 0; i < common.HashLength; i++ {
			etxEligibleSlices[i] = 0xFF
		}
		wo.Header().SetEtxEligibleSlices(etxEligibleSlices)
	} else {
		wo.Header().SetEtxEligibleSlices(common.Hash{})
	}
	wo.Header().SetBaseFee(new(big.Int).SetUint64(params.InitialBaseFee))
	wo.Header().SetStateLimit(params.InitialStateLimit)
	wo.Header().SetStateUsed(0)
	wo.Header().SetEtxSetRoot(types.EmptyRootHash)
	wo.Header().SetExchangeRate(params.ExchangeRate)
	wo.Header().SetKQuaiDiscount(params.StartingKQuaiDiscount)
	wo.Header().SetConversionFlowAmount(params.StartingConversionFlowAmount)
	wo.Header().SetMinerDifficulty(g.Difficulty)

	if g.GasLimit == 0 {
		wo.Header().SetGasLimit(params.GenesisGasLimit)
	}
	for i := 0; i < common.HierarchyDepth; i++ {
		wo.SetNumber(big.NewInt(0), i)
		wo.SetParentHash(common.Hash{}, i)
	}

	wo.WorkObjectHeader().SetHeaderHash(wo.Header().Hash())

	return wo
}

// Commit writes the block and state of a genesis specification to the database.
// The block is committed as the canonical head block.
func (g *Genesis) Commit(db ethdb.Database, nodeLocation common.Location, startingExpansionNumber uint64) (*types.WorkObject, error) {
	nodeCtx := nodeLocation.Context()
	block := g.ToBlock(startingExpansionNumber)
	if block.Number(nodeCtx).Sign() != 0 {
		return nil, fmt.Errorf("can't commit genesis block with number > 0")
	}
	config := g.Config
	if config == nil {
		config = params.AllProgpowProtocolChanges
	}
	rawdb.WriteGenesisHashes(db, common.Hashes{block.Hash()})
	rawdb.WriteTermini(db, block.Hash(), types.EmptyTermini())
	rawdb.WriteWorkObject(db, block.Hash(), block, types.BlockObject, nodeCtx)
	rawdb.WriteReceipts(db, block.Hash(), block.NumberU64(nodeCtx), nil)
	rawdb.WriteCanonicalHash(db, block.Hash(), block.NumberU64(nodeCtx))
	rawdb.WriteHeadBlockHash(db, block.Hash())
	rawdb.WriteHeadHeaderHash(db, block.Hash())
	rawdb.WriteChainConfig(db, block.Hash(), config)
	return block, nil
}

// MustCommit writes the genesis block and state to db, panicking on error.
// The block is committed as the canonical head block.
func (g *Genesis) MustCommit(db ethdb.Database, nodeLocation common.Location) *types.WorkObject {
	block, err := g.Commit(db, nodeLocation, 0)
	if err != nil {
		panic(err)
	}
	return block
}

// GenesisBlockForTesting creates and writes a block in which addr has the given wei balance.
func GenesisBlockForTesting(db ethdb.Database, addr common.Address, balance *big.Int, nodeLocation common.Location) *types.WorkObject {
	g := Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	return g.MustCommit(db, nodeLocation)
}

// DefaultGenesisBlock returns the Latest default Genesis block.
// Currently it returns the DefaultColosseumGenesisBlock.
func DefaultGenesisBlock(nonce uint64, extra []byte) *Genesis {
	return DefaultColosseumGenesisBlock("progpow", nonce, extra)
}

// DefaultColosseumGenesisBlock returns the Quai Colosseum testnet genesis block.
func DefaultColosseumGenesisBlock(consensusEngine string, nonce uint64, extra []byte) *Genesis {
	genesis := &Genesis{
		Config:     params.ProgpowColosseumChainConfig,
		Nonce:      nonce,
		ExtraData:  extra,
		GasLimit:   12000000,
		Difficulty: big.NewInt(1500000000000),
	}
	if consensusEngine == "blake3" {
		genesis.Config = params.Blake3PowColosseumChainConfig
		genesis.Difficulty = big.NewInt(2000000)
	}
	return genesis
}

// DefaultGardenGenesisBlock returns the Garden testnet genesis block.
func DefaultGardenGenesisBlock(consensusEngine string, nonce uint64, extra []byte) *Genesis {
	genesis := &Genesis{
		Config:     params.ProgpowGardenChainConfig,
		Nonce:      nonce,
		ExtraData:  extra,
		GasLimit:   12000000,
		Difficulty: big.NewInt(300000001),
	}
	if consensusEngine == "blake3" {
		genesis.Config = params.Blake3PowGardenChainConfig
		genesis.Difficulty = big.NewInt(500000)
	}
	return genesis
}

// DefaultOrchardGenesisBlock returns the Orchard testnet genesis block.
func DefaultOrchardGenesisBlock(consensusEngine string, nonce uint64, extra []byte) *Genesis {
	genesis := &Genesis{
		Config:     params.ProgpowOrchardChainConfig,
		Nonce:      nonce,
		ExtraData:  extra,
		GasLimit:   12000000,
		Difficulty: big.NewInt(500000001),
	}
	if consensusEngine == "blake3" {
		genesis.Config = params.Blake3PowOrchardChainConfig
		genesis.Difficulty = big.NewInt(200000)
	}
	return genesis
}

// DefaultLighthouseGenesisBlock returns the Lighthouse testnet genesis block.
func DefaultLighthouseGenesisBlock(consensusEngine string, nonce uint64, extra []byte) *Genesis {
	genesis := &Genesis{
		Config:     params.ProgpowLighthouseChainConfig,
		Nonce:      nonce,
		ExtraData:  extra,
		GasLimit:   12000000,
		Difficulty: big.NewInt(750000000),
	}
	if consensusEngine == "blake3" {
		genesis.Config = params.Blake3PowLighthouseChainConfig
		genesis.Difficulty = big.NewInt(200000)
	}
	return genesis
}

// DefaultLocalGenesisBlock returns the Local testnet genesis block.
func DefaultLocalGenesisBlock(consensusEngine string, nonce uint64, extra []byte) *Genesis {
	genesis := &Genesis{
		Config:     params.ProgpowLocalChainConfig,
		Nonce:      0,
		ExtraData:  []byte{},
		GasLimit:   12000000,
		Difficulty: big.NewInt(100000000),
	}
	if consensusEngine == "blake3" {
		genesis.Config = params.Blake3PowLocalChainConfig
		genesis.Difficulty = big.NewInt(500000)
	}
	return genesis
}

// DeveloperGenesisBlock returns the 'quai --dev' genesis block.
func DeveloperGenesisBlock(period uint64, faucet common.Address) *Genesis {
	// Override the default period to the user requested one
	config := *params.AllProgpowProtocolChanges
	// Assemble and return the genesis with the precompiles and faucet pre-funded
	return &Genesis{
		Config:     &config,
		ExtraData:  append(append(make([]byte, 32), faucet.Bytes()[:]...), make([]byte, crypto.SignatureLength)...),
		GasLimit:   0x47b760,
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(1),
	}
}
