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
	ColosseumGenesisHash = common.HexToHash("0x6b3c28921a94aa0b17240fe8144e7936c870069143195438f2360521cb73f5a1")
	GardenGenesisHash    = common.HexToHash("0x511bf6bbaa9400c6ade6e0e4968d85e9ed65af5777513336317b95b89e9c2dbb")
	OrchardGenesisHash   = common.HexToHash("0xb5ed55d8f34de69077f9a794599f98f8961951f8ca0ad0d119337222a73dc8f9")
	LocalGenesisHash     = common.HexToHash("0x4c35b1216decc6aa2431fa2d2a1c68f15d70f83a309041ed1bfef5ad6592a3d4")
	GalenaGenesisHash    = common.HexToHash("0xce6c6393558ee45d8a38e1f04eb5ad33823ab9ea1122cdba747ce41d87ccdb92")
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
