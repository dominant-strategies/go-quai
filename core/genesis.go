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
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

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
	"github.com/dominant-strategies/go-quai/rlp"
	"github.com/dominant-strategies/go-quai/trie"
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
	GasLimit   []uint64            `json:"gasLimit"   gencodec:"required"`
	Difficulty *big.Int            `json:"difficulty" gencodec:"required"`
	Mixhash    common.Hash         `json:"mixHash"`
	Coinbase   []common.Address    `json:"coinbase"`
	Alloc      GenesisAlloc        `json:"alloc"      gencodec:"required"`

	// These fields are used for consensus tests. Please don't use them
	// in actual genesis blocks.
	Number     []uint64      `json:"number"`
	GasUsed    []uint64      `json:"gasUsed"`
	ParentHash []common.Hash `json:"parentHash"`
	BaseFee    []*big.Int    `json:"baseFeePerGas"`
}

// GenesisAlloc specifies the initial state that is part of the genesis block.
type GenesisAlloc map[common.Address]GenesisAccount

func (ga *GenesisAlloc) UnmarshalJSON(data []byte) error {
	m := make(map[common.UnprefixedAddress]GenesisAccount)
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*ga = make(GenesisAlloc)
	for addr, a := range m {
		internal := common.InternalAddress(addr)
		(*ga)[common.NewAddressFromData(&internal)] = a
	}
	return nil
}

// GenesisAccount is an account in the state of the genesis block.
type GenesisAccount struct {
	Code       []byte                      `json:"code,omitempty"`
	Storage    map[common.Hash]common.Hash `json:"storage,omitempty"`
	Balance    *big.Int                    `json:"balance" gencodec:"required"`
	Nonce      uint64                      `json:"nonce,omitempty"`
	PrivateKey []byte                      `json:"secretKey,omitempty"` // for tests
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
	Alloc      map[common.UnprefixedAddress]GenesisAccount
}

type genesisAccountMarshaling struct {
	Code       hexutil.Bytes
	Balance    *math.HexOrDecimal256
	Nonce      math.HexOrDecimal64
	Storage    map[storageJSON]storageJSON
	PrivateKey hexutil.Bytes
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
func SetupGenesisBlock(db ethdb.Database, genesis *Genesis) (*params.ChainConfig, common.Hash, error) {
	return SetupGenesisBlockWithOverride(db, genesis, nil)
}

func SetupGenesisBlockWithOverride(db ethdb.Database, genesis *Genesis, overrideLondon *big.Int) (*params.ChainConfig, common.Hash, error) {
	if genesis != nil && genesis.Config == nil {
		return params.AllBlake3powProtocolChanges, common.Hash{}, errGenesisNoConfig
	}
	// Just commit the new block if there is no stored genesis block.
	stored := rawdb.ReadCanonicalHash(db, 0)
	if (stored == common.Hash{}) {
		if genesis == nil {
			log.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock()
		} else {
			log.Info("Writing custom genesis block")
		}
		block, err := genesis.Commit(db)
		if err != nil {
			return genesis.Config, common.Hash{}, err
		}
		return genesis.Config, block.Hash(), nil
	}
	// We have the genesis block in database(perhaps in ancient database)
	// but the corresponding state is missing.
	header := rawdb.ReadHeader(db, stored, 0)
	if _, err := state.New(header.Root(), state.NewDatabaseWithConfig(db, nil), nil); err != nil {
		if genesis == nil {
			genesis = DefaultGenesisBlock()
		}
		// Ensure the stored genesis matches with the given one.
		hash := genesis.ToBlock(nil).Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
		block, err := genesis.Commit(db)
		if err != nil {
			return genesis.Config, hash, err
		}
		return genesis.Config, block.Hash(), nil
	}
	// Check whether the genesis block is already written.
	if genesis != nil {
		hash := genesis.ToBlock(nil).Hash()
		if hash != stored {
			return genesis.Config, hash, &GenesisMismatchError{stored, hash}
		}
	}
	// Get the existing chain configuration.
	newcfg := genesis.configOrDefault(stored)
	if overrideLondon != nil {
		newcfg.LondonBlock = overrideLondon
	}
	if err := newcfg.CheckConfigForkOrder(); err != nil {
		return newcfg, common.Hash{}, err
	}
	storedcfg := rawdb.ReadChainConfig(db, stored)
	if storedcfg == nil {
		log.Warn("Found genesis block without chain config")
		rawdb.WriteChainConfig(db, stored, newcfg)
		return newcfg, stored, nil
	}
	// Special case: don't change the existing config of a non-mainnet chain if no new
	// config is supplied. These chains would get AllProtocolChanges (and a compat error)
	// if we just continued here.
	if genesis == nil && stored != params.ColosseumGenesisHash {
		return storedcfg, stored, nil
	}
	// Check config compatibility and write the config. Compatibility errors
	// are returned to the caller unless we're already at block zero.
	height := rawdb.ReadHeaderNumber(db, rawdb.ReadHeadHeaderHash(db))
	if height == nil {
		return newcfg, stored, fmt.Errorf("missing block number for head header hash")
	}
	compatErr := storedcfg.CheckCompatible(newcfg, *height)
	if compatErr != nil && *height != 0 && compatErr.RewindTo != 0 {
		return newcfg, stored, compatErr
	}
	rawdb.WriteChainConfig(db, stored, newcfg)
	return newcfg, stored, nil
}

func (g *Genesis) configOrDefault(ghash common.Hash) *params.ChainConfig {
	switch {
	case g != nil:
		return g.Config
	case ghash == params.ColosseumGenesisHash:
		return params.ColosseumChainConfig
	case ghash == params.GardenGenesisHash:
		return params.GardenChainConfig
	case ghash == params.OrchardGenesisHash:
		return params.OrchardChainConfig
	case ghash == params.GalenaGenesisHash:
		return params.GalenaChainConfig
	case ghash == params.LocalGenesisHash:
		return params.LocalChainConfig
	default:
		return params.AllBlake3powProtocolChanges
	}
}

// ToBlock creates the genesis block and writes state of a genesis specification
// to the given database (or discards it if nil).
func (g *Genesis) ToBlock(db ethdb.Database) *types.Block {
	nodeCtx := common.NodeLocation.Context()
	if db == nil {
		db = rawdb.NewMemoryDatabase()
	}
	// We only allow genesis allocations in Prime chain. We need to calculate
	// the prime genesis state root, but all other chains will just have empty
	// state roots.
	primeStatedb, err := state.New(common.Hash{}, state.NewDatabase(db), nil)
	if err != nil {
		panic(err)
	}
	for addr, account := range g.Alloc {
		internal, err := addr.InternalAddress()
		if err != nil {
			fmt.Println("Provided address in genesis block is out of scope")
		}
		primeStatedb.AddBalance(internal, account.Balance)
		primeStatedb.SetCode(internal, account.Code)
		primeStatedb.SetNonce(internal, account.Nonce)
		for key, value := range account.Storage {
			primeStatedb.SetState(internal, key, value)
		}
	}
	primeRoot := primeStatedb.IntermediateRoot(false)
	head := types.EmptyHeader()
	head.SetNonce(types.EncodeNonce(g.Nonce))
	head.SetTime(g.Timestamp)
	head.SetExtra(g.ExtraData)
	head.SetRoot(primeRoot, common.PRIME_CTX)
	head.SetRoot(types.EmptyRootHash, common.REGION_CTX) // Not genesis allocs allowed
	head.SetRoot(types.EmptyRootHash, common.ZONE_CTX)   // Not genesis allocs allowed
	head.SetDifficulty(g.Difficulty)
	for i := 0; i < common.HierarchyDepth; i++ {
		head.SetNumber(big.NewInt(0), i)
		head.SetParentHash(common.Hash{}, i)
		head.SetGasLimit(g.GasLimit[i], i)
		head.SetGasUsed(0, i)
		head.SetBaseFee(new(big.Int).SetUint64(params.InitialBaseFee), i)
		head.SetCoinbase(common.ZeroAddr, i)
		if g.GasLimit[i] == 0 {
			head.SetGasLimit(params.GenesisGasLimit, i)
		}
	}

	// If we are a prime node, commit the Prime state. If not, create a new
	// empty state to be commited.
	var statedb state.StateDB
	if common.PRIME_CTX == nodeCtx {
		statedb = *primeStatedb
	} else {
		localStatedb, err := state.New(common.Hash{}, state.NewDatabase(db), nil)
		if err != nil {
			panic(err)
		}
		statedb = *localStatedb
	}
	statedb.Commit(false)
	statedb.Database().TrieDB().Commit(head.Root(), true, nil)

	return types.NewBlock(head, nil, nil, nil, nil, nil, trie.NewStackTrie(nil))
}

// Commit writes the block and state of a genesis specification to the database.
// The block is committed as the canonical head block.
func (g *Genesis) Commit(db ethdb.Database) (*types.Block, error) {
	block := g.ToBlock(db)
	if block.Number().Sign() != 0 {
		return nil, fmt.Errorf("can't commit genesis block with number > 0")
	}
	config := g.Config
	if config == nil {
		config = params.AllBlake3powProtocolChanges
	}
	if err := config.CheckConfigForkOrder(); err != nil {
		return nil, err
	}
	rawdb.WriteTermini(db, block.Hash(), nil)
	rawdb.WriteBlock(db, block)
	rawdb.WriteReceipts(db, block.Hash(), block.NumberU64(), nil)
	rawdb.WriteCanonicalHash(db, block.Hash(), block.NumberU64())
	rawdb.WriteHeadBlockHash(db, block.Hash())
	rawdb.WriteHeadHeaderHash(db, block.Hash())
	rawdb.WriteChainConfig(db, block.Hash(), config)
	return block, nil
}

// MustCommit writes the genesis block and state to db, panicking on error.
// The block is committed as the canonical head block.
func (g *Genesis) MustCommit(db ethdb.Database) *types.Block {
	block, err := g.Commit(db)
	if err != nil {
		panic(err)
	}
	return block
}

// GenesisBlockForTesting creates and writes a block in which addr has the given wei balance.
func GenesisBlockForTesting(db ethdb.Database, addr common.Address, balance *big.Int) *types.Block {
	g := Genesis{
		Alloc:   GenesisAlloc{addr: {Balance: balance}},
		BaseFee: []*big.Int{big.NewInt(params.InitialBaseFee), big.NewInt(params.InitialBaseFee), big.NewInt(params.InitialBaseFee)},
	}
	return g.MustCommit(db)
}

// DefaultGenesisBlock returns the Latest default Genesis block.
// Currently it returns the DefaultColosseumGenesisBlock.
func DefaultGenesisBlock() *Genesis {
	return DefaultColosseumGenesisBlock()
}

// DefaultColosseumGenesisBlock returns the Quai Colosseum testnet genesis block.
func DefaultColosseumGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.ColosseumChainConfig,
		Nonce:      66,
		ExtraData:  hexutil.MustDecode("0x11bbe8db4e347b4e8c937c1c8370e4b5ed33adb3db69cbdb7a38e1e50b1b82fa"),
		GasLimit:   []uint64{1000000, 1000000, 1000000},
		Difficulty: big.NewInt(2048576),
		Alloc:      decodePrealloc(colosseumAllocData),
	}
}

// DefaultGardenGenesisBlock returns the Garden testnet genesis block.
func DefaultGardenGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.GardenChainConfig,
		Nonce:      67,
		ExtraData:  hexutil.MustDecode("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:   []uint64{1000000, 1000000, 1000000},
		Difficulty: big.NewInt(441092),
		Alloc:      decodePrealloc(gardenAllocData),
	}
}

// DefaultOrchardGenesisBlock returns the Orchard testnet genesis block.
func DefaultOrchardGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.OrchardChainConfig,
		Nonce:      68,
		ExtraData:  hexutil.MustDecode("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:   []uint64{160000000, 160000000, 160000000},
		Difficulty: big.NewInt(2500000),
		Alloc:      decodePrealloc(orchardAllocData),
	}
}

// DefaultGalenaGenesisBlock returns the Galena testnet genesis block.
func DefaultGalenaGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.GalenaChainConfig,
		Nonce:      68,
		ExtraData:  hexutil.MustDecode("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:   []uint64{1000000, 1000000, 1000000},
		Difficulty: big.NewInt(8800000000),
		Alloc:      decodePrealloc(galenaAllocData),
	}
}

// DefaultLocalGenesisBlock returns the Local testnet genesis block.
func DefaultLocalGenesisBlock() *Genesis {
	return &Genesis{
		Config:     params.LocalChainConfig,
		Nonce:      67,
		ExtraData:  hexutil.MustDecode("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:   []uint64{160000000, 160000000, 160000000},
		Difficulty: big.NewInt(300000),
		Alloc:      decodePrealloc(localAllocData),
	}
}

// DeveloperGenesisBlock returns the 'geth --dev' genesis block.
func DeveloperGenesisBlock(period uint64, faucet common.Address) *Genesis {
	// Override the default period to the user requested one
	config := *params.AllBlake3powProtocolChanges
	// Assemble and return the genesis with the precompiles and faucet pre-funded
	return &Genesis{
		Config:     &config,
		ExtraData:  append(append(make([]byte, 32), faucet.Bytes()[:]...), make([]byte, crypto.SignatureLength)...),
		GasLimit:   []uint64{0x47b760, 0x47b760, 0x47b760},
		BaseFee:    []*big.Int{big.NewInt(params.InitialBaseFee), big.NewInt(params.InitialBaseFee), big.NewInt(params.InitialBaseFee)},
		Difficulty: big.NewInt(1),
		Alloc: map[common.Address]GenesisAccount{
			common.BytesToAddress([]byte{1}): {Balance: big.NewInt(1)}, // ECRecover
			common.BytesToAddress([]byte{2}): {Balance: big.NewInt(1)}, // SHA256
			common.BytesToAddress([]byte{3}): {Balance: big.NewInt(1)}, // RIPEMD
			common.BytesToAddress([]byte{4}): {Balance: big.NewInt(1)}, // Identity
			common.BytesToAddress([]byte{5}): {Balance: big.NewInt(1)}, // ModExp
			common.BytesToAddress([]byte{6}): {Balance: big.NewInt(1)}, // ECAdd
			common.BytesToAddress([]byte{7}): {Balance: big.NewInt(1)}, // ECScalarMul
			common.BytesToAddress([]byte{8}): {Balance: big.NewInt(1)}, // ECPairing
			common.BytesToAddress([]byte{9}): {Balance: big.NewInt(1)}, // BLAKE2b
			faucet:                           {Balance: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(9))},
		},
	}
}

func decodePrealloc(data string) GenesisAlloc {
	var p []struct{ Addr, Balance *big.Int }
	if err := rlp.NewStream(strings.NewReader(data), 0).Decode(&p); err != nil {
		panic(err)
	}
	ga := make(GenesisAlloc, len(p))
	for _, account := range p {
		ga[common.BigToAddress(account.Addr)] = GenesisAccount{Balance: account.Balance}
	}
	return ga
}
