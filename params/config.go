// Copyright 2016 The go-ethereum Authors
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

package params

import (
	"fmt"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
)

// Genesis hashes to enforce below configs on.
var (
	ColosseumGenesisHash = common.HexToHash("0xb1cba48a8d1ace79923edc4c03fc66a4990072b8c545783f94c85ad71d20b1e6")
	GardenGenesisHash    = common.HexToHash("0x6210219b7847439325215a2d17182b801d9cc8efc2b0004d9511d37ef623405b")
	OrchardGenesisHash   = common.HexToHash("0xc0fc30c7860b8f4ec7a8fe1a54dfcbca17167eca5bdcbdc077b6d50addf79763")
	LocalGenesisHash     = common.HexToHash("0xae8a1dc375464487ed130283bc1f4e809ec4eb4ab61354c48ce50bb0f88c42bd")
	GalenaGenesisHash    = common.HexToHash("0x708982eb69544dced7ca28e7e9ff1520f212e67fc138065d968f3a1b544408f7")
)

var (
	// ColosseumChainConfig is the chain parameters to run a node on the Colosseum network.
	ColosseumChainConfig = &ChainConfig{
		ChainID:     big.NewInt(9000),
		Blake3pow:   new(Blake3powConfig),
		GenesisHash: ColosseumGenesisHash,
	}

	// GardenChainConfig contains the chain parameters to run a node on the Garden test network.
	GardenChainConfig = &ChainConfig{
		ChainID:     big.NewInt(12000),
		Blake3pow:   new(Blake3powConfig),
		GenesisHash: GardenGenesisHash,
	}

	// OrchardChainConfig contains the chain parameters to run a node on the Orchard test network.
	OrchardChainConfig = &ChainConfig{
		ChainID:     big.NewInt(15000),
		Blake3pow:   new(Blake3powConfig),
		GenesisHash: OrchardGenesisHash,
	}

	// GalenaChainConfig contains the chain parameters to run a node on the Galena test network.
	GalenaChainConfig = &ChainConfig{
		ChainID:     big.NewInt(17000),
		Blake3pow:   new(Blake3powConfig),
		GenesisHash: GalenaGenesisHash,
	}

	// LocalChainConfig contains the chain parameters to run a node on the Local test network.
	LocalChainConfig = &ChainConfig{
		ChainID:     big.NewInt(1337),
		Blake3pow:   new(Blake3powConfig),
		GenesisHash: LocalGenesisHash,
	}

	// AllBlake3powProtocolChanges contains every protocol change introduced
	// and accepted by the Quai core developers into the Blake3pow consensus.
	//
	// This configuration is intentionally not using keyed fields to force anyone
	// adding flags to the config to also have to set these fields.
	AllBlake3powProtocolChanges = &ChainConfig{big.NewInt(1337), new(Blake3powConfig), common.Hash{}}

	TestChainConfig = &ChainConfig{big.NewInt(1), new(Blake3powConfig), common.Hash{}}
	TestRules       = TestChainConfig.Rules(new(big.Int))
)

// ChainConfig is the core config which determines the blockchain settings.
//
// ChainConfig is stored in the database on a per block basis. This means
// that any network, identified by its genesis block, can have its own
// set of configuration options.
type ChainConfig struct {
	ChainID *big.Int `json:"chainId"` // chainId identifies the current chain and is used for replay protection
	// Various consensus engines
	Blake3pow   *Blake3powConfig `json:"blake3pow,omitempty"`
	GenesisHash common.Hash
}

// Blake3powConfig is the consensus engine configs for proof-of-work based sealing.
type Blake3powConfig struct{}

// String implements the stringer interface, returning the consensus engine details.
func (c *Blake3powConfig) String() string {
	return "blake3pow"
}

// String implements the fmt.Stringer interface.
func (c *ChainConfig) String() string {
	var engine interface{}
	switch {
	case c.Blake3pow != nil:
		engine = c.Blake3pow
	default:
		engine = "unknown"
	}
	return fmt.Sprintf("{ChainID: %v, Engine: %v}",
		c.ChainID,
		engine,
	)
}

func configNumEqual(x, y *big.Int) bool {
	if x == nil {
		return y == nil
	}
	if y == nil {
		return x == nil
	}
	return x.Cmp(y) == 0
}

// ConfigCompatError is raised if the locally-stored blockchain is initialised with a
// ChainConfig that would alter the past.
type ConfigCompatError struct {
	What string
	// block numbers of the stored and new configurations
	StoredConfig, NewConfig *big.Int
	// the block number to which the local chain must be rewound to correct the error
	RewindTo uint64
}

func newCompatError(what string, storedblock, newblock *big.Int) *ConfigCompatError {
	var rew *big.Int
	switch {
	case storedblock == nil:
		rew = newblock
	case newblock == nil || storedblock.Cmp(newblock) < 0:
		rew = storedblock
	default:
		rew = newblock
	}
	err := &ConfigCompatError{what, storedblock, newblock, 0}
	if rew != nil && rew.Sign() > 0 {
		err.RewindTo = rew.Uint64() - 1
	}
	return err
}

func (err *ConfigCompatError) Error() string {
	return fmt.Sprintf("mismatching %s in database (have %d, want %d, rewindto %d)", err.What, err.StoredConfig, err.NewConfig, err.RewindTo)
}

// Rules wraps ChainConfig and is merely syntactic sugar or can be used for functions
// that do not have or require information about the block.
//
// Rules is a one time interface meaning that it shouldn't be used in between transition
// phases.
type Rules struct {
	ChainID *big.Int
}

// Rules ensures c's ChainID is not nil.
func (c *ChainConfig) Rules(num *big.Int) Rules {
	chainID := c.ChainID
	if chainID == nil {
		chainID = new(big.Int)
	}
	return Rules{
		ChainID: new(big.Int).Set(chainID),
	}
}
