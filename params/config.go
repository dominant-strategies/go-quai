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
	ProgpowColosseumGenesisHash  = common.HexToHash("0x14f66d592a6ff6c44a855156c462f01bf0f1848c6f7cb002f97455a34c538e68")
	ProgpowGardenGenesisHash     = common.HexToHash("0x29f15dd1e704c5e9e0ef8776ee1899c6f5e211a06e9d687886d8dbd77fb6aa2b")
	ProgpowOrchardGenesisHash    = common.HexToHash("0x3ee7299e7593122120f2771644f01784978621c199a2411c9cd8830375a95df8")
	ProgpowLocalGenesisHash      = common.HexToHash("0x22fbb2ddcf3170adafa8c876921d6bcd3ce711f7ce7b41315c58c308d71c23c8")
	ProgpowLighthouseGenesisHash = common.HexToHash("0x00c30de71dad200fbefdc79e374f8d9eecdac5d1babaf9e1b6dbd1ae29bbc895")

	// Blake3GenesisHashes
	Blake3PowColosseumGenesisHash  = common.HexToHash("0xffcfe01a51422c137f12f609fc8d6e5963f698283eb4ed2ffc446a92511daa41")
	Blake3PowGardenGenesisHash     = common.HexToHash("0xffd62690f5a1d66060abe2af9038af3e9793cafc3f5245b82645f9c49e9abeec")
	Blake3PowOrchardGenesisHash    = common.HexToHash("0xdae422621de4820c01a5bf3864d20ab4143b621895237852152c30f2c274684f")
	Blake3PowLocalGenesisHash      = common.HexToHash("0xf1b62db0d12a8d817923fd1688891f85af37596af8f61ee7fd26e1b67cad910d")
	Blake3PowLighthouseGenesisHash = common.HexToHash("0x535ac2d02dac7d9be6038e1f0418c58ed769beddbb3aec4358374f6cc202c5b2")
)

// Different Network names
const (
	ColosseumName  = "colosseum"
	GardenName     = "garden"
	OrchardName    = "orchard"
	LighthouseName = "lighthouse"
	LocalName      = "local"
	DevName        = "dev"
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
