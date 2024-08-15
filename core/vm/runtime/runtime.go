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

package runtime

import (
	"math"
	"math/big"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/state/snapshot"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/sirupsen/logrus"
)

// Config is a basic type specifying certain configuration flags for running
// the EVM.
type Config struct {
	ChainConfig   *params.ChainConfig
	Difficulty    *big.Int
	Origin        common.Address
	Coinbase      common.Address
	BlockNumber   *big.Int
	Time          *big.Int
	GasLimit      uint64
	GasPrice      *big.Int
	Value         *big.Int
	Lock          *big.Int
	Debug         bool
	EVMConfig     vm.Config
	BaseFee       *big.Int
	QuaiStateSize *big.Int
	Logger        *logrus.Logger

	State     *state.StateDB
	GetHashFn func(n uint64) common.Hash
}

// sets defaults on the config
func setDefaults(cfg *Config) {
	if cfg.ChainConfig == nil {
		cfg.ChainConfig = &params.ChainConfig{
			ChainID: big.NewInt(1),
		}
	}

	if cfg.Difficulty == nil {
		cfg.Difficulty = new(big.Int)
	}
	if cfg.Time == nil {
		cfg.Time = big.NewInt(time.Now().Unix())
	}
	if cfg.GasLimit == 0 {
		cfg.GasLimit = math.MaxUint64
	}
	if cfg.QuaiStateSize == nil {
		cfg.QuaiStateSize = new(big.Int)
	}
	if cfg.GasPrice == nil {
		cfg.GasPrice = new(big.Int)
	}
	if cfg.Value == nil {
		cfg.Value = new(big.Int)
	}
	if cfg.Lock == nil {
		cfg.Lock = new(big.Int)
	}
	if cfg.BlockNumber == nil {
		cfg.BlockNumber = new(big.Int)
	}
	if cfg.GetHashFn == nil {
		cfg.GetHashFn = func(n uint64) common.Hash {
			return common.BytesToHash(crypto.Keccak256([]byte(new(big.Int).SetUint64(n).String())))
		}
	}
	if cfg.BaseFee == nil {
		cfg.BaseFee = big.NewInt(params.InitialBaseFee)
	}

	if cfg.Logger == nil {
		cfg.Logger = log.Global
	}
}

// Execute executes the code using the input as call data during the execution.
// It returns the EVM's return value, the new state and an error if it failed.
//
// Execute sets up an in-memory, temporary, environment for the execution of
// the given code. It makes sure that it's restored to its original state afterwards.
func Execute(code, input []byte, cfg *Config) ([]byte, *state.StateDB, error) {
	if cfg == nil {
		cfg = new(Config)
	}
	setDefaults(cfg)

	if cfg.State == nil {
		cfg.State, _ = state.New(common.Hash{}, common.Hash{}, common.Hash{}, new(big.Int), state.NewDatabase(rawdb.NewMemoryDatabase(cfg.Logger)), state.NewDatabase(rawdb.NewMemoryDatabase(cfg.Logger)), state.NewDatabase(rawdb.NewMemoryDatabase(cfg.Logger)), &snapshot.Tree{}, cfg.ChainConfig.Location, cfg.Logger)
	}
	var (
		address = common.BytesToAddress([]byte("contract"), cfg.ChainConfig.Location)
		vmenv   = NewEnv(cfg)
		sender  = vm.AccountRef(cfg.Origin)
	)
	internal, err := address.InternalAndQuaiAddress()
	if err != nil {
		return []byte{}, nil, err
	}
	rules := cfg.ChainConfig.Rules(vmenv.Context.BlockNumber)
	cfg.State.PrepareAccessList(cfg.Origin, &address, vm.ActivePrecompiles(rules, cfg.ChainConfig.Location), nil)

	cfg.State.CreateAccount(internal)
	// set the receiver's (the executing contract) code for execution.
	cfg.State.SetCode(internal, code)
	// Call the code with the given configuration.
	ret, _, err := vmenv.Call(
		sender,
		common.BytesToAddress([]byte("contract"), cfg.ChainConfig.Location),
		input,
		cfg.GasLimit,
		cfg.Value,
		cfg.Lock,
	)

	return ret, cfg.State, err
}

// Create executes the code using the EVM create method
func Create(input []byte, cfg *Config) ([]byte, common.Address, uint64, error) {
	if cfg == nil {
		cfg = new(Config)
	}
	setDefaults(cfg)

	if cfg.State == nil {
		cfg.State, _ = state.New(common.Hash{}, common.Hash{}, common.Hash{}, new(big.Int), state.NewDatabase(rawdb.NewMemoryDatabase(cfg.Logger)), state.NewDatabase(rawdb.NewMemoryDatabase(cfg.Logger)), state.NewDatabase(rawdb.NewMemoryDatabase(cfg.Logger)), &snapshot.Tree{}, cfg.ChainConfig.Location, cfg.Logger)
	}
	var (
		vmenv  = NewEnv(cfg)
		sender = vm.AccountRef(cfg.Origin)
	)
	rules := cfg.ChainConfig.Rules(vmenv.Context.BlockNumber)
	cfg.State.PrepareAccessList(cfg.Origin, nil, vm.ActivePrecompiles(rules, cfg.ChainConfig.Location), nil)

	// Call the code with the given configuration.
	code, address, leftOverGas, err := vmenv.Create(
		sender,
		input,
		cfg.GasLimit,
		cfg.Value,
	)
	return code, address, leftOverGas, err
}

// Call executes the code given by the contract's address. It will return the
// EVM's return value or an error if it failed.
//
// Call, unlike Execute, requires a config and also requires the State field to
// be set.
func Call(address common.Address, input []byte, cfg *Config) ([]byte, uint64, error) {
	setDefaults(cfg)

	vmenv := NewEnv(cfg)
	_, err := cfg.Origin.InternalAndQuaiAddress()
	if err != nil {
		return []byte{}, 0, err
	}

	statedb := cfg.State

	rules := cfg.ChainConfig.Rules(vmenv.Context.BlockNumber)
	statedb.PrepareAccessList(cfg.Origin, &address, vm.ActivePrecompiles(rules, cfg.ChainConfig.Location), nil)

	// Call the code with the given configuration.
	ret, leftOverGas, err := vmenv.Call(
		vm.AccountRef(cfg.Origin),
		address,
		input,
		cfg.GasLimit,
		cfg.Value,
		cfg.Lock,
	)
	return ret, leftOverGas, err
}
