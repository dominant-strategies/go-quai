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

package utils

import (
	"database/sql/driver"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"
)

const (
	DefaultHTTPHost = "localhost" // Default host interface for the HTTP RPC server
	DefaultHTTPPort = 8545        // Default TCP port for the HTTP RPC server
	DefaultWSHost   = "localhost" // Default host interface for the websocket RPC server
	DefaultWSPort   = 8546        // Default TCP port for the websocket RPC server
)

var (
	DefaultMaxPrice    = big.NewInt(500 * 1e9)
	DefaultIgnorePrice = big.NewInt(2 * 1)
)

// **********************************
// **                			   **
// ** 	Eth Config Defaults		   **
// **	  & Empty Structs		   **
// **							   **
// **********************************

var EthConfigDefaults = EthConfig{
	SyncMode:                0,
	Progpow:                 ProgpowConfig{},
	NetworkId:               1,
	TxLookupLimit:           2350000,
	DatabaseCache:           512,
	TrieCleanCache:          154,
	TrieCleanCacheJournal:   "triecache",
	TrieCleanCacheRejournal: 60 * time.Minute,
	TrieDirtyCache:          256,
	TrieTimeout:             60 * time.Minute,
	SnapshotCache:           102,
	Miner: MinerConfig{
		GasCeil:  18000000,
		GasPrice: big.NewInt(1e9),
		Recommit: 3 * time.Second,
	},
	TxPool:      TxPoolConfig{},
	RPCGasCap:   50000000,
	GPO:         FullNodeGPOConfig,
	RPCTxFeeCap: 1, // 1 ether
	DomUrl:      "ws://127.0.0.1:8546",
	SubUrls:     []string{"ws://127.0.0.1:8546", "ws://127.0.0.1:8546", "ws://127.0.0.1:8546"},
}

type EthConfig struct {
	// The genesis block, which is inserted if the database is empty.
	// If nil, the Quai main net block is used.
	Genesis Genesis `toml:",omitempty"`

	// Protocol options
	NetworkId uint64 // Network ID to use for selecting peers to connect to
	SyncMode  SyncMode

	// This can be set to list of enrtree:// URLs which will be queried for
	// for nodes to connect to.
	EthDiscoveryURLs  []string
	SnapDiscoveryURLs []string

	NoPruning  bool // Whether to disable pruning and flush everything to disk
	NoPrefetch bool // Whether to disable prefetching and only load state on demand

	TxLookupLimit uint64 `toml:",omitempty"` // The maximum number of blocks from head whose tx indices are reserved.

	// Whitelist of required block number -> hash values to accept
	Whitelist map[uint64]Hash `toml:"-"`

	// Database options
	SkipBcVersionCheck bool `toml:"-"`
	DatabaseHandles    int  `toml:"-"`
	DatabaseCache      int
	DatabaseFreezer    string

	TrieCleanCache          int
	TrieCleanCacheJournal   string        `toml:",omitempty"` // Disk journal directory for trie cache to survive node restarts
	TrieCleanCacheRejournal time.Duration `toml:",omitempty"` // Time interval to regenerate the journal for clean cache
	TrieDirtyCache          int
	TrieTimeout             time.Duration
	SnapshotCache           int
	Preimages               bool

	// Mining options
	Miner MinerConfig

	// Consensus Engine
	ConsensusEngine string

	// Progpow options
	Progpow ProgpowConfig

	// Blake3 options
	Blake3Pow Blake3PowConfig

	// Transaction pool options
	TxPool TxPoolConfig

	// Gas Price Oracle options
	GPO GasPriceConfig

	// Enables tracking of SHA3 preimages in the VM
	EnablePreimageRecording bool

	// Miscellaneous options
	DocRoot string `toml:"-"`

	// RPCGasCap is the global gas cap for eth-call variants.
	RPCGasCap uint64

	// RPCTxFeeCap is the global transaction fee(price * gaslimit) cap for
	// send-transction variants. The unit is ether.
	RPCTxFeeCap float64

	// Region location options
	Region int

	// Zone location options
	Zone int

	// Dom node websocket url
	DomUrl string

	// Sub node websocket urls
	SubUrls []string

	// Slices running on the node
	SlicesRunning []Location
}

type SyncMode uint32

const (
	FullSync SyncMode = iota // Synchronise the entire blockchain history from full blocks
)

func (mode SyncMode) MarshalText() ([]byte, error) {
	switch mode {
	case FullSync:
		return []byte("full"), nil
	default:
		return nil, fmt.Errorf("unknown sync mode %d", mode)
	}
}

func (mode *SyncMode) UnmarshalText(text []byte) error {
	switch string(text) {
	case "full":
		*mode = FullSync
	default:
		return fmt.Errorf(`unknown sync mode %q, want "full", "fast" or "light"`, text)
	}
	return nil
}

type Genesis struct {
}

type MinerConfig struct {
	Etherbase  Address       `toml:",omitempty"` // Public address for block mining rewards (default = first account)
	Notify     []string      `toml:",omitempty"` // HTTP URL list to be notified of new work packages (only useful in ethash).
	NotifyFull bool          `toml:",omitempty"` // Notify with pending block headers instead of work packages
	ExtraData  Bytes         `toml:",omitempty"` // Block extra data set by the miner
	GasFloor   uint64        // Target gas floor for mined blocks.
	GasCeil    uint64        // Target gas ceiling for mined blocks.
	GasPrice   *big.Int      // Minimum gas price for mining a transaction
	Recommit   time.Duration // The time interval for miner to re-create mining work.
	Noverify   bool          // Disable remote mining solution verification(only useful in ethash).
}

type Address struct {
}

type AddressData interface {
	Bytes() []byte
	Hash() Hash
	Hex() string
	String() string
	checksumHex() []byte
	Format(s fmt.State, c rune)
	MarshalText() ([]byte, error)
	UnmarshalText(input []byte) error
	UnmarshalJSON(input []byte) error
	Scan(src interface{}) error
	Value() (driver.Value, error)
	Location() *Location
	setBytes(b []byte)
}

type Bytes []byte

type ProgpowConfig struct {
}

type Blake3PowConfig struct {
}

type TxPoolConfig struct {
	Locals    []InternalAddress // Addresses that should be treated by default as local
	NoLocals  bool              // Whether local transaction handling should be disabled
	Journal   string            // Journal of local transactions to survive node restarts
	Rejournal time.Duration     // Time interval to regenerate the local transaction journal

	PriceLimit uint64 // Minimum gas price to enforce for acceptance into the pool
	PriceBump  uint64 // Minimum price bump percentage to replace an already existing transaction (nonce)

	AccountSlots    uint64 // Number of executable transaction slots guaranteed per account
	GlobalSlots     uint64 // Maximum number of executable transaction slots for all accounts
	MaxSenders      uint64 // Maximum number of senders in the senders cache
	SendersChBuffer uint64 // Senders cache channel buffer size
	AccountQueue    uint64 // Maximum number of non-executable transaction slots permitted per account
	GlobalQueue     uint64 // Maximum number of non-executable transaction slots for all accounts

	Lifetime time.Duration // Maximum amount of time non-executable transaction are queued
}

// FullNodeGPO contains default gasprice oracle settings for full node.
var FullNodeGPOConfig = GasPriceConfig{
	Blocks:           20,
	Percentile:       60,
	MaxHeaderHistory: 0,
	MaxBlockHistory:  0,
	MaxPrice:         DefaultMaxPrice,
	IgnorePrice:      DefaultIgnorePrice,
}

type GasPriceConfig struct {
	Blocks           int
	Percentile       int
	MaxHeaderHistory int
	MaxBlockHistory  int
	Default          *big.Int `toml:",omitempty"`
	MaxPrice         *big.Int `toml:",omitempty"`
	IgnorePrice      *big.Int `toml:",omitempty"`
}

type Location []byte
type Hash [32]byte

// **********************************
// **                			   **
// ** 	Core Config Defaults	   **
// **	  & Empty Structs		   **
// **							   **
// **********************************

var CoreConfigDefaults = TxPoolConfig{
	Journal:   "transactions.rlp",
	Rejournal: time.Hour,

	PriceLimit: 1,
	PriceBump:  10,

	AccountSlots:    1,
	GlobalSlots:     9000 + 1024, // urgent + floating queue capacity with 4:1 ratio
	MaxSenders:      100000,      // 5 MB - at least 10 blocks worth of transactions in case of reorg or high production rate
	SendersChBuffer: 1024,        // at 500 TPS in zone, 2s buffer
	AccountQueue:    1,
	GlobalQueue:     2048,

	Lifetime: 3 * time.Hour,
}

type InternalAddress [20]byte

// **********************************
// **                			   **
// ** 	Metrics Config Defaults	   **
// **	  & Empty Structs		   **
// **							   **
// **********************************

var DefaultMetricsConfig = MetricsConfig{
	Enabled:          false,
	EnabledExpensive: false,
	HTTP:             "127.0.0.1",
	Port:             6060,
	EnableInfluxDB:   false,
	InfluxDBEndpoint: "http://localhost:8086",
	InfluxDBDatabase: "quai",
	InfluxDBUsername: "test",
	InfluxDBPassword: "test",
	InfluxDBTags:     "host=localhost",
}

type MetricsConfig struct {
	Enabled          bool   `toml:",omitempty"`
	EnabledExpensive bool   `toml:",omitempty"`
	HTTP             string `toml:",omitempty"`
	Port             int    `toml:",omitempty"`
	EnableInfluxDB   bool   `toml:",omitempty"`
	InfluxDBEndpoint string `toml:",omitempty"`
	InfluxDBDatabase string `toml:",omitempty"`
	InfluxDBUsername string `toml:",omitempty"`
	InfluxDBPassword string `toml:",omitempty"`
	InfluxDBTags     string `toml:",omitempty"`
}

// **********************************
// **                			   **
// ** 	Node Config Defaults	   **
// **	  & Empty Structs		   **
// **							   **
// **********************************

var NodeDefaultConfig = Config{
	DataDir:          DefaultDataDir(),
	HTTPPort:         DefaultHTTPPort,
	HTTPModules:      []string{"net", "web3"},
	HTTPVirtualHosts: []string{"localhost"},
	HTTPTimeouts:     DefaultHTTPTimeouts,
	WSPort:           DefaultWSPort,
	WSModules:        []string{"net", "web3"},
	P2P: P2PConfig{
		MaxPendingPeers: 50,
	},
	DBEngine: "",
}

type Config struct {
	// Name sets the instance name of the node. It must not contain the / character and is
	// used in the devp2p node identifier. The instance name of go-quai is "go-quai". If no
	// value is specified, the basename of the current executable is used.
	Name string `toml:"-"`

	// UserIdent, if set, is used as an additional component in the devp2p node identifier.
	UserIdent string `toml:",omitempty"`

	// Version should be set to the version number of the program. It is used
	// in the devp2p node identifier.
	Version string `toml:"-"`

	// DataDir is the file system folder the node should use for any data storage
	// requirements. The configured data directory will not be directly shared with
	// registered services, instead those can use utility methods to create/access
	// databases or flat files. This enables ephemeral nodes which can fully reside
	// in memory.
	DataDir string

	// Configuration of peer-to-peer networking.
	P2P P2PConfig

	// KeyStoreDir is the file system folder that contains private keys. The directory can
	// be specified as a relative path, in which case it is resolved relative to the
	// current directory.
	//
	// If KeyStoreDir is empty, the default location is the "keystore" subdirectory of
	// DataDir. If DataDir is unspecified and KeyStoreDir is empty, an ephemeral directory
	// is created by New and destroyed when the node is stopped.
	KeyStoreDir string `toml:",omitempty"`

	// ExternalSigner specifies an external URI for a clef-type signer
	ExternalSigner string `toml:",omitempty"`

	// UseLightweightKDF lowers the memory and CPU requirements of the key store
	// scrypt KDF at the expense of security.
	UseLightweightKDF bool `toml:",omitempty"`

	// InsecureUnlockAllowed allows user to unlock accounts in unsafe http environment.
	InsecureUnlockAllowed bool `toml:",omitempty"`

	// NoUSB disables hardware wallet monitoring and connectivity.
	NoUSB bool `toml:",omitempty"`

	// USB enables hardware wallet monitoring and connectivity.
	USB bool `toml:",omitempty"`

	// HTTPHost is the host interface on which to start the HTTP RPC server. If this
	// field is empty, no HTTP API endpoint will be started.
	HTTPHost string

	// HTTPPort is the TCP port number on which to start the HTTP RPC server. The
	// default zero value is/ valid and will pick a port number randomly (useful
	// for ephemeral nodes).
	HTTPPort int `toml:",omitempty"`

	// HTTPCors is the Cross-Origin Resource Sharing header to send to requesting
	// clients. Please be aware that CORS is a browser enforced security, it's fully
	// useless for custom HTTP clients.
	HTTPCors []string `toml:",omitempty"`

	// HTTPVirtualHosts is the list of virtual hostnames which are allowed on incoming requests.
	// This is by default {'localhost'}. Using this prevents attacks like
	// DNS rebinding, which bypasses SOP by simply masquerading as being within the same
	// origin. These attacks do not utilize CORS, since they are not cross-domain.
	// By explicitly checking the Host-header, the server will not allow requests
	// made against the server with a malicious host domain.
	// Requests using ip address directly are not affected
	HTTPVirtualHosts []string `toml:",omitempty"`

	// HTTPModules is a list of API modules to expose via the HTTP RPC interface.
	// If the module list is empty, all RPC API endpoints designated public will be
	// exposed.
	HTTPModules []string

	// HTTPTimeouts allows for customization of the timeout values used by the HTTP RPC
	// interface.
	HTTPTimeouts HTTPTimeouts

	// HTTPPathPrefix specifies a path prefix on which http-rpc is to be served.
	HTTPPathPrefix string `toml:",omitempty"`

	// WSHost is the host interface on which to start the websocket RPC server. If
	// this field is empty, no websocket API endpoint will be started.
	WSHost string

	// WSPort is the TCP port number on which to start the websocket RPC server. The
	// default zero value is/ valid and will pick a port number randomly (useful for
	// ephemeral nodes).
	WSPort int `toml:",omitempty"`

	// WSPathPrefix specifies a path prefix on which ws-rpc is to be served.
	WSPathPrefix string `toml:",omitempty"`

	// WSOrigins is the list of domain to accept websocket requests from. Please be
	// aware that the server can only act upon the HTTP request the client sends and
	// cannot verify the validity of the request header.
	WSOrigins []string `toml:",omitempty"`

	// WSModules is a list of API modules to expose via the websocket RPC interface.
	// If the module list is empty, all RPC API endpoints designated public will be
	// exposed.
	WSModules []string

	// WSExposeAll exposes all API modules via the WebSocket RPC interface rather
	// than just the public ones.
	//
	// *WARNING* Only set this if the node is running in a trusted network, exposing
	// private APIs to untrusted users is a major security risk.
	WSExposeAll bool `toml:",omitempty"`

	// Logger is a custom logger to use with the p2p.Server.
	Logger *log.Logger `toml:",omitempty"`

	// AllowUnprotectedTxs allows non EIP-155 protected transactions to be send over RPC.
	AllowUnprotectedTxs bool `toml:",omitempty"`

	// JWTSecret is the path to the hex-encoded jwt secret.
	JWTSecret string `toml:",omitempty"`

	// EnablePersonal enables the deprecated personal namespace.
	EnablePersonal bool `toml:"-"`

	DBEngine string `toml:",omitempty"`
}

type HTTPTimeouts struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

var DefaultHTTPTimeouts = HTTPTimeouts{
	ReadTimeout:  30 * time.Second,
	WriteTimeout: 30 * time.Second,
	IdleTimeout:  120 * time.Second,
}

type P2PConfig struct {
	// MaxPendingPeers is the maximum number of peers that can be pending in the
	// handshake phase, counted separately for inbound and outbound connections.
	// Zero defaults to preset values.
	MaxPendingPeers int `toml:",omitempty"`
}

// DefaultDataDir is the default data directory to use for the databases and other
// persistence requirements.
func DefaultDataDir() string {
	// Try to place the data folder in the user's home dir
	home := homeDir()
	if home != "" {
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(home, "Library", "Quai")
		case "windows":
			// We used to put everything in %HOME%\AppData\Roaming, but this caused
			// problems with non-typical setups. If this fallback location exists and
			// is non-empty, use it, otherwise DTRT and check %LOCALAPPDATA%.
			fallback := filepath.Join(home, "AppData", "Roaming", "Quai")
			appdata := windowsAppData()
			if appdata == "" || isNonEmptyDir(fallback) {
				return fallback
			}
			return filepath.Join(appdata, "Quai")
		default:
			return filepath.Join(home, ".quai")
		}
	}
	// As we cannot guess a stable location, return empty and handle later
	return ""
}

func windowsAppData() string {
	v := os.Getenv("LOCALAPPDATA")
	if v == "" {
		// Windows XP and below don't have LocalAppData. Crash here because
		// we don't support Windows XP and undefining the variable will cause
		// other issues.
		panic("environment variable LocalAppData is undefined")
	}
	return v
}

func isNonEmptyDir(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	names, _ := f.Readdir(1)
	f.Close()
	return len(names) > 0
}

func homeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	if usr, err := user.Current(); err == nil {
		return usr.HomeDir
	}
	return ""
}
