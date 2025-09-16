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
	"github.com/dominant-strategies/go-quai/log"
)

// Genesis hashes to enforce below configs on.
var (
	// Progpow GenesisHashes
	ProgpowColosseumGenesisHash  = common.HexToHash("0xac81c28f1a72591b87b5f16c9793cdc0e87c45c6d426d1a364c3b8f6386b5b8b")
	ProgpowGardenGenesisHash     = common.HexToHash("0xe6d0f9327aae915db55ab0cf9da7841c64e4ae39baa251c17586d8d25b59afbc")
	ProgpowOrchardGenesisHash    = common.HexToHash("0x663a73416275109a01aad3a4c29ea9e310aded63c5eea491243b7312ad8cd16b")
	ProgpowLighthouseGenesisHash = common.HexToHash("0xf86f23baa7818c62b0d4bd5f2100230eab65392e0b115b91f446b299fddf38d8")
	ProgpowLocalGenesisHash      = common.HexToHash("0x8865466e6165e64353ed3dc5736ab0ead9d6ff7916872a198fa71d070a14473b")

	// Blake3GenesisHashes
	Blake3PowColosseumGenesisHash  = common.HexToHash("0x5d9b6827120512d5f5dcf102bc0228113a592c2da4852f38200a32c2798973b4")
	Blake3PowGardenGenesisHash     = common.HexToHash("0xfbfea88506b001ce3e7eb34f7b0dac311e3a67682c65869d7953d5fcc3607a35")
	Blake3PowOrchardGenesisHash    = common.HexToHash("0x7006882c75e8f445b754947f623d619716bac1cf8ab24005e396369d0d0a65e3")
	Blake3PowLighthouseGenesisHash = common.HexToHash("0x7006882c75e8f445b754947f623d619716bac1cf8ab24005e396369d0d0a65e3")
	Blake3PowLocalGenesisHash      = common.HexToHash("0x2facaeb87e9770b76880a2dcb2554013626aa937a2f089fa2c18bd55e7ba9354")

	AllocHash = common.HexToHash("0x9bbe29fd621de6817cbc92ff37e47bce280926cfa1b7bc36f015c658d8140601")
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
		ChainID: big.NewInt(9),
		Progpow: new(ProgpowConfig),
	}

	Blake3PowColosseumChainConfig = &ChainConfig{
		ChainID:   big.NewInt(9),
		Blake3Pow: new(Blake3powConfig),
	}

	// GardenChainConfig contains the chain parameters to run a node on the Garden test network.
	ProgpowGardenChainConfig = &ChainConfig{
		ChainID: big.NewInt(12000),
		Progpow: new(ProgpowConfig),
	}

	Blake3PowGardenChainConfig = &ChainConfig{
		ChainID:   big.NewInt(12000),
		Blake3Pow: new(Blake3powConfig),
	}

	// OrchardChainConfig contains the chain parameters to run a node on the Orchard test network.
	ProgpowOrchardChainConfig = &ChainConfig{
		ChainID: big.NewInt(15000),
		Progpow: new(ProgpowConfig),
	}

	Blake3PowOrchardChainConfig = &ChainConfig{
		ChainID:   big.NewInt(15000),
		Blake3Pow: new(Blake3powConfig),
	}

	// LighthouseChainConfig contains the chain parameters to run a node on the Lighthouse test network.
	ProgpowLighthouseChainConfig = &ChainConfig{
		ChainID:   big.NewInt(17000),
		Blake3Pow: new(Blake3powConfig),
		Progpow:   new(ProgpowConfig),
	}

	Blake3PowLighthouseChainConfig = &ChainConfig{
		ChainID:   big.NewInt(17000),
		Blake3Pow: new(Blake3powConfig),
	}

	// LocalChainConfig contains the chain parameters to run a node on the Local test network.
	ProgpowLocalChainConfig = &ChainConfig{
		ChainID: big.NewInt(1337),
		Progpow: new(ProgpowConfig),
	}

	Blake3PowLocalChainConfig = &ChainConfig{
		ChainID:   big.NewInt(1337),
		Blake3Pow: new(Blake3powConfig),
	}

	// AllProgpowProtocolChanges contains every protocol change introduced
	// and accepted by the Quai core developers into the Progpow consensus.
	//
	// This configuration is intentionally not using keyed fields to force anyone
	// adding flags to the config to also have to set these fields.
	AllProgpowProtocolChanges = &ChainConfig{big.NewInt(1337), "progpow", new(Blake3powConfig), new(ProgpowConfig), common.Location{}, common.Hash{}, false, false}

	TestChainConfig = &ChainConfig{big.NewInt(1), "progpow", new(Blake3powConfig), new(ProgpowConfig), common.Location{}, common.Hash{}, false, false}
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
	ConsensusEngine    string
	Blake3Pow          *Blake3powConfig `json:"blake3pow,omitempty"`
	Progpow            *ProgpowConfig   `json:"progpow,omitempty"`
	Location           common.Location
	DefaultGenesisHash common.Hash
	IndexAddressUtxos  bool
	TelemetryEnabled   bool
}

// Mode defines the type and amount of PoW verification a kawpow engine makes.
type Mode uint

const (
	ModeNormal Mode = iota
	ModeShared
	ModeTest
	ModeFake
	ModeFullFake
)

// PowConfig are the configuration parameters of pow.
type PowConfig struct {
	PowMode Mode

	CacheDir       string
	CachesInMem    int
	CachesOnDisk   int
	CachesLockMmap bool
	DurationLimit  *big.Int
	GasCeil        uint64
	MinDifficulty  *big.Int
	GenAllocs      []GenesisAccount

	NodeLocation common.Location

	WorkShareThreshold int

	// When set, notifications sent by the remote sealer will
	// be block header JSON objects instead of work package arrays.
	NotifyFull bool

	Log *log.Logger `toml:"-"`
	// Number of threads to mine on if mining
	NumThreads int
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
