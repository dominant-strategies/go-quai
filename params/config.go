// Cojyright 2016 The go-ethereum Authors
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
	// Progpow GenesisHashes
	ProgpowColosseumGenesisHash  = common.HexToHash("0xcdaaf48cc057d97a14acbe76ce5a6e1ab377049059c74416cdfb0c6fd5d0b058")
	ProgpowGardenGenesisHash     = common.HexToHash("0x7823e58617576af80cc557595380b55c2d565b294e3b6438fc11c4410e48da5e")
	ProgpowOrchardGenesisHash    = common.HexToHash("0x4042b03aa7c4d6a58d4e6238b2da5c03dda97a0c7e7a7b1d237917a23ca4b0d6")
	ProgpowLocalGenesisHash      = common.HexToHash("0x53a10194f6b385aa037fe1911b52a56d4d0ac8f95f3e98ac4f546269429c288d")
	ProgpowLighthouseGenesisHash = common.HexToHash("0x888542dc6e379e29db4c86934b05cfd4a376e1c19261db3f44d6e4e9f6ce0495")

	// Blake3GenesisHashes
	Blake3PowColosseumGenesisHash  = common.HexToHash("0x40057f44be0809f939c7d70893d101abc74af1f1c535e406ec9909cfa2c20cbe")
	Blake3PowGardenGenesisHash     = common.HexToHash("0x0a942bb7fa04b80d658a64d0ca49c62199503a796abc169f05a1863421a22098")
	Blake3PowOrchardGenesisHash    = common.HexToHash("0x1f3743c323ec7a9a6a8150a33de81723abb70fa10ad49c35e365e48488430b56")
	Blake3PowLocalGenesisHash      = common.HexToHash("0x31ac78ff6abcd99ad1fd8cd1b8ef54cfb28bdc72f7a38311e67a9e34fc2adb45")
	Blake3PowLighthouseGenesisHash = common.HexToHash("0xaadedd7ed5a68a8885b54079e8193ae5be8f9b9bf4799ea9f1612bca835f33da")
)

var (
	// ColosseumChainConfig is the chain parameters to run a node on the Colosseum network.
	ProgpowColosseumChainConfig = &ChainConfig{
		ChainID:     big.NewInt(9000),
		Progpow:     new(ProgpowConfig),
		GenesisHash: ProgpowColosseumGenesisHash,
	}

	Blake3PowColosseumChainConfig = &ChainConfig{
		ChainID:     big.NewInt(9000),
		Blake3Pow:   new(Blake3powConfig),
		GenesisHash: Blake3PowColosseumGenesisHash,
	}

	// GardenChainConfig contains the chain parameters to run a node on the Garden test network.
	ProgpowGardenChainConfig = &ChainConfig{
		ChainID:     big.NewInt(12000),
		Progpow:     new(ProgpowConfig),
		GenesisHash: ProgpowGardenGenesisHash,
	}

	Blake3PowGardenChainConfig = &ChainConfig{
		ChainID:     big.NewInt(12000),
		Blake3Pow:   new(Blake3powConfig),
		GenesisHash: Blake3PowGardenGenesisHash,
	}

	// OrchardChainConfig contains the chain parameters to run a node on the Orchard test network.
	ProgpowOrchardChainConfig = &ChainConfig{
		ChainID:     big.NewInt(15000),
		Progpow:     new(ProgpowConfig),
		GenesisHash: ProgpowOrchardGenesisHash,
	}

	Blake3PowOrchardChainConfig = &ChainConfig{
		ChainID:     big.NewInt(15000),
		Blake3Pow:   new(Blake3powConfig),
		GenesisHash: Blake3PowOrchardGenesisHash,
	}

	// LighthouseChainConfig contains the chain parameters to run a node on the Lighthouse test network.
	ProgpowLighthouseChainConfig = &ChainConfig{
		ChainID:     big.NewInt(17000),
		Blake3Pow:   new(Blake3powConfig),
		Progpow:     new(ProgpowConfig),
		GenesisHash: ProgpowLighthouseGenesisHash,
	}

	Blake3PowLighthouseChainConfig = &ChainConfig{
		ChainID:     big.NewInt(17000),
		Blake3Pow:   new(Blake3powConfig),
		GenesisHash: Blake3PowLighthouseGenesisHash,
	}

	// LocalChainConfig contains the chain parameters to run a node on the Local test network.
	ProgpowLocalChainConfig = &ChainConfig{
		ChainID:     big.NewInt(1337),
		Progpow:     new(ProgpowConfig),
		GenesisHash: ProgpowLocalGenesisHash,
	}

	Blake3PowLocalChainConfig = &ChainConfig{
		ChainID:     big.NewInt(1337),
		Blake3Pow:   new(Blake3powConfig),
		GenesisHash: Blake3PowLocalGenesisHash,
	}

	// AllProgpowProtocolChanges contains every protocol change introduced
	// and accepted by the Quai core developers into the Progpow consensus.
	//
	// This configuration is intentionally not using keyed fields to force anyone
	// adding flags to the config to also have to set these fields.
	AllProgpowProtocolChanges = &ChainConfig{big.NewInt(1337), "progpow", new(Blake3powConfig), new(ProgpowConfig), common.Hash{}, common.Location{}}

	TestChainConfig = &ChainConfig{big.NewInt(1), "progpow", new(Blake3powConfig), new(ProgpowConfig), common.Hash{}, common.Location{}}
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
	ConsensusEngine string
	Blake3Pow       *Blake3powConfig `json:"blake3pow,omitempty"`
	Progpow         *ProgpowConfig   `json:"progpow,omitempty"`
	GenesisHash     common.Hash
	Location        common.Location
}

// SetLocation sets the location on the chain config
func (cfg *ChainConfig) SetLocation(location common.Location) {
	cfg.Location = location
}

// Blake3powConfig is the consensus engine configs for proof-of-work based sealing.
type Blake3powConfig struct{}

// String implements the stringer interface, returning the consensus engine details.
func (c *Blake3powConfig) String() string {
	return "blake3pow"
}

// ProgpowConfig is the consensus engine configs for proof-of-work based sealing.
type ProgpowConfig struct{}

// String implements the stringer interface, returning the consensus engine details.
func (c *ProgpowConfig) String() string {
	return "progpow"
}

// String implements the fmt.Stringer interface.
func (c *ChainConfig) String() string {
	var engine interface{}
	switch {
	case c.Blake3Pow != nil:
		engine = c.Blake3Pow
	case c.Progpow != nil:
		engine = c.Progpow
	default:
		engine = "unknown"
	}
	return fmt.Sprintf("{ChainID: %v, Engine: %v, Location: %v}",
		c.ChainID,
		engine,
		c.Location,
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
