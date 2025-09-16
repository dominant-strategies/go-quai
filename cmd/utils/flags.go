package utils

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	godebug "runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
	gopsutil "github.com/shirou/gopsutil/mem"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/constants"
	"github.com/dominant-strategies/go-quai/common/fdlimit"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/node"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai/quaiconfig"
)

const (
	c_GlobalFlagPrefix    = "global."
	c_NodeFlagPrefix      = "node."
	c_TXPoolPrefix        = "txpool."
	c_RPCFlagPrefix       = "rpc."
	c_WorkShareFlagPrefix = "workshare."
	c_PeersFlagPrefix     = "peers."
	c_MetricsFlagPrefix   = "metrics."

	c_regionPortOffset = 1
	c_zonePortOffset   = 199
)

var Flags = [][]Flag{
	GlobalFlags,
	NodeFlags,
	TXPoolFlags,
	WorkShareFlags,
	RPCFlags,
	PeersFlags,
	MetricsFlags,
}

var GlobalFlags = []Flag{
	ConfigDirFlag,
	DataDirFlag,
	InitConfigFlag,
	AncientDirFlag,
	LogLevelFlag,
	LogSizeFlag,
}

var NodeFlags = []Flag{
	IPAddrFlag,
	P2PPortFlag,
	BootPeersFlag,
	PortMapFlag,
	KeyFileFlag,
	MinPeersFlag,
	MaxPeersFlag,
	LocationFlag,
	SoloFlag,
	DBEngineFlag,
	NetworkIdFlag,
	SlicesRunningFlag,
	DevPeriodFlag,
	IdentityFlag,
	DocRootFlag,
	SnapshotFlag,
	TxLookupLimitFlag,
	WhitelistFlag,
	BloomFilterSizeFlag,
	CacheFlag,
	CacheDatabaseFlag,
	CacheTrieFlag,
	CacheTrieJournalFlag,
	CacheTrieRejournalFlag,
	CacheGCFlag,
	CacheSnapshotFlag,
	CacheNoPrefetchFlag,
	CachePreimagesFlag,
	ConsensusEngineFlag,
	MinerGasPriceFlag,
	UnlockedAccountFlag,
	PasswordFileFlag,
	VMEnableDebugFlag,
	PprofFlag,
	InsecureUnlockAllowedFlag,
	QuaiCoinbaseFlag,
	QiCoinbaseFlag,
	LockupContractAddressFlag,
	MinerPreferenceFlag,
	CoinbaseLockupFlag,
	EnvironmentFlag,
	QuaiStatsURLFlag,
	SendFullStatsFlag,
	IndexAddressUtxos,
	ReIndex,
	ValidateIndexer,
	StartingExpansionNumberFlag,
	NodeLogLevelFlag,
	GenesisNonce,
	Telemetry,
}

var TXPoolFlags = []Flag{
	TxPoolLocalsFlag,
	TxPoolNoLocalsFlag,
	TxPoolSyncTxWithReturnFlag,
	TxPoolJournalFlag,
	TxPoolRejournalFlag,
	TxPoolPriceLimitFlag,
	TxPoolPriceBumpFlag,
	TxPoolAccountSlotsFlag,
	TxPoolGlobalSlotsFlag,
	TxPoolAccountQueueFlag,
	TxPoolGlobalQueueFlag,
	TxPoolLifetimeFlag,
	TxPoolSharingClientsFlag,
}

var WorkShareFlags = []Flag{
	WorkShareMiningFlag,
	WorkShareThresholdFlag,
	WorkShareMinerEndpoints,
	WorkShareP2PThreshold,
}

var RPCFlags = []Flag{
	HTTPEnabledFlag,
	HTTPListenAddrFlag,
	HTTPCORSDomainFlag,
	HTTPVirtualHostsFlag,
	HTTPApiFlag,
	HTTPPathPrefixFlag,
	HTTPPortStartFlag,
	WSEnabledFlag,
	WSListenAddrFlag,
	WSMaxSubsFlag,
	WSApiFlag,
	WSAllowedOriginsFlag,
	WSPathPrefixFlag,
	WSPortStartFlag,
	PreloadJSFlag,
	RPCGlobalTxFeeCapFlag,
	RPCGlobalGasCapFlag,
	RpcVersion,
}

var PeersFlags = []Flag{
	PeersLogLevelFlag,
}

var MetricsFlags = []Flag{
	MetricsEnabledFlag,
	MetricsEnabledExpensiveFlag,
	MetricsHTTPFlag,
	MetricsPortFlag,
}

var (
	// ****************************************
	// **                                    **
	// **         GLOBAL FLAGS               **
	// **                                    **
	// ****************************************
	ConfigDirFlag = Flag{
		Name:         c_GlobalFlagPrefix + "config-dir",
		Abbreviation: "c",
		Value:        xdg.ConfigHome + "/" + constants.APP_NAME + "/",
		Usage:        "config directory" + generateEnvDoc(c_GlobalFlagPrefix+"config-dir"),
	}

	DataDirFlag = Flag{
		Name:         c_GlobalFlagPrefix + "data-dir",
		Abbreviation: "d",
		Value:        filepath.Join(xdg.DataHome, constants.APP_NAME),
		Usage:        "data directory" + generateEnvDoc(c_GlobalFlagPrefix+"data-dir"),
	}

	InitConfigFlag = Flag{
		Name:  c_GlobalFlagPrefix + "init-cfg",
		Value: false,
		Usage: "initialize a new config file, if one does not already exist",
	}

	AncientDirFlag = Flag{
		Name:  c_GlobalFlagPrefix + "datadir-ancient",
		Value: "",
		Usage: "Data directory for ancient chain segments (default = inside chaindata)" + generateEnvDoc(c_GlobalFlagPrefix+"datadir-ancient"),
	}

	LogLevelFlag = Flag{
		Name:         c_GlobalFlagPrefix + "log-level",
		Abbreviation: "l",
		Value:        "info",
		Usage:        "log level (trace, debug, info, warn, error, fatal, panic)" + generateEnvDoc(c_GlobalFlagPrefix+"log-level"),
	}

	LogSizeFlag = Flag{
		Name:  c_GlobalFlagPrefix + "log-size",
		Value: 500,
		Usage: "maximum log file size in MB before rolling (default = no limit)" + generateEnvDoc(c_GlobalFlagPrefix+"log-size"),
	}
)

var (
	// ****************************************
	// **                                    **
	// **         NODE FLAGS                 **
	// **                                    **
	// ****************************************
	IPAddrFlag = Flag{
		Name:         c_NodeFlagPrefix + "ipaddr",
		Abbreviation: "i",
		Value:        "0.0.0.0",
		Usage:        "ip address to listen on" + generateEnvDoc(c_NodeFlagPrefix+"ipaddr"),
	}

	P2PPortFlag = Flag{
		Name:         c_NodeFlagPrefix + "port",
		Abbreviation: "p",
		Value:        "4002",
		Usage:        "p2p port to listen on" + generateEnvDoc(c_NodeFlagPrefix+"port"),
	}

	BootPeersFlag = Flag{
		Name:  c_NodeFlagPrefix + "bootpeers",
		Value: []string{},
		Usage: "list of bootstrap peers. Syntax: <multiaddress1>,<multiaddress2>,..." + generateEnvDoc(c_NodeFlagPrefix+"bootpeers"),
	}

	PortMapFlag = Flag{
		Name:  c_NodeFlagPrefix + "portmap",
		Value: true,
		Usage: "enable NAT portmap" + generateEnvDoc(c_NodeFlagPrefix+"portmap"),
	}

	KeyFileFlag = Flag{
		Name:         c_NodeFlagPrefix + "private-key",
		Abbreviation: "k",
		Value:        "",
		Usage:        "file containing node private key" + generateEnvDoc(c_NodeFlagPrefix+"private-key"),
	}

	MinPeersFlag = Flag{
		Name:  c_NodeFlagPrefix + "min-peers",
		Value: "5",
		Usage: "minimum number of peers to maintain connectivity with" + generateEnvDoc(c_NodeFlagPrefix+"min-peers"),
	}

	MaxPeersFlag = Flag{
		Name:  c_NodeFlagPrefix + "max-peers",
		Value: "50",
		Usage: "maximum number of peers to maintain connectivity with" + generateEnvDoc(c_NodeFlagPrefix+"max-peers"),
	}

	LocationFlag = Flag{
		Name:  c_NodeFlagPrefix + "location",
		Value: "",
		Usage: "region and zone location" + generateEnvDoc(c_NodeFlagPrefix+"location"),
	}

	SoloFlag = Flag{
		Name:         c_NodeFlagPrefix + "solo",
		Abbreviation: "s",
		Value:        false,
		Usage:        "start the node as a solo node (will not reach out to bootstrap peers)" + generateEnvDoc(c_NodeFlagPrefix+"solo"),
	}

	DBEngineFlag = Flag{
		Name:  c_NodeFlagPrefix + "db-engine",
		Value: "leveldb",
		Usage: "Backing database implementation to use ('leveldb' or 'pebble')" + generateEnvDoc(c_NodeFlagPrefix+"db-engine"),
	}

	NetworkIdFlag = Flag{
		Name:  c_NodeFlagPrefix + "networkid",
		Value: 1,
		Usage: "Explicitly set network id (integer)(For testnets: use --garden)" + generateEnvDoc(c_NodeFlagPrefix+"networkid"),
	}

	SlicesRunningFlag = Flag{
		Name:  c_NodeFlagPrefix + "slices",
		Value: "[0 0]",
		Usage: "All the slices that are running on this node" + generateEnvDoc(c_NodeFlagPrefix+"slices"),
	}

	DevPeriodFlag = Flag{
		Name:  c_NodeFlagPrefix + "dev-period",
		Value: 0,
		Usage: "Block period to use for the dev network (integer) (0 = mine only if transaction pending)" + generateEnvDoc(c_NodeFlagPrefix+"dev-period"),
	}

	IdentityFlag = Flag{
		Name:  c_NodeFlagPrefix + "identity",
		Value: "",
		Usage: "Custom node name" + generateEnvDoc(c_NodeFlagPrefix+"identity"),
	}

	DocRootFlag = Flag{
		Name:  c_NodeFlagPrefix + "docroot",
		Value: xdg.DataHome,
		Usage: "Document Root for HTTPClient file scheme" + generateEnvDoc(c_NodeFlagPrefix+"docroot"),
	}

	SnapshotFlag = Flag{
		Name:  c_NodeFlagPrefix + "snapshot",
		Value: true,
		Usage: `Enables snapshot-database mode (default = true)` + generateEnvDoc(c_NodeFlagPrefix+"snapshot"),
	}

	TxLookupLimitFlag = Flag{
		Name:  c_NodeFlagPrefix + "txlookuplimit",
		Value: quaiconfig.Defaults.TxLookupLimit,
		Usage: "Number of recent blocks to maintain transactions index for (default = about one year, 0 = entire chain)" + generateEnvDoc(c_NodeFlagPrefix+"txlookuplimit"),
	}

	WhitelistFlag = Flag{
		Name:  c_NodeFlagPrefix + "whitelist",
		Value: "",
		Usage: "Comma separated block number-to-hash mappings to enforce (<number>=<hash>)" + generateEnvDoc(c_NodeFlagPrefix+"whitelist"),
	}

	BloomFilterSizeFlag = Flag{
		Name:  c_NodeFlagPrefix + "bloomfilter-size",
		Value: 2048,
		Usage: "Megabytes of memory allocated to bloom-filter for pruning" + generateEnvDoc(c_NodeFlagPrefix+"bloomfilter-size"),
	}

	TxPoolLocalsFlag = Flag{
		Name:  c_TXPoolPrefix + "locals",
		Value: "",
		Usage: "Comma separated accounts to treat as locals (no flush, priority inclusion)" + generateEnvDoc(c_TXPoolPrefix+"locals"),
	}

	TxPoolNoLocalsFlag = Flag{
		Name:  c_TXPoolPrefix + "nolocals",
		Value: false,
		Usage: "Disables price exemptions for locally submitted transactions" + generateEnvDoc(c_TXPoolPrefix+"nolocals"),
	}

	TxPoolSyncTxWithReturnFlag = Flag{
		Name:  c_TXPoolPrefix + "sync-tx-with-return",
		Value: true,
		Usage: "Shares the tx with the sharing client with syncronous return (also bypasses local pool, only use it with combination of sharing clients)" + generateEnvDoc(c_TXPoolPrefix+"sync-tx-with-return"),
	}

	TxPoolJournalFlag = Flag{
		Name:  c_TXPoolPrefix + "journal",
		Value: core.DefaultTxPoolConfig.Journal,
		Usage: "Disk journal for local transaction to survive node restarts" + generateEnvDoc(c_TXPoolPrefix+"journal"),
	}

	TxPoolRejournalFlag = Flag{
		Name:  c_TXPoolPrefix + "rejournal",
		Value: core.DefaultTxPoolConfig.Rejournal,
		Usage: "Time interval to regenerate the local transaction journal" + generateEnvDoc(c_TXPoolPrefix+"rejournal"),
	}

	TxPoolPriceLimitFlag = Flag{
		Name:  c_TXPoolPrefix + "pricelimit",
		Value: quaiconfig.Defaults.TxPool.PriceLimit,
		Usage: "Minimum gas price limit to enforce for acceptance into the pool" + generateEnvDoc(c_TXPoolPrefix+"pricelimit"),
	}

	TxPoolPriceBumpFlag = Flag{
		Name:  c_TXPoolPrefix + "pricebump",
		Value: quaiconfig.Defaults.TxPool.PriceBump,
		Usage: "Price bump percentage to replace an already existing transaction" + generateEnvDoc(c_TXPoolPrefix+"pricebump"),
	}

	TxPoolAccountSlotsFlag = Flag{
		Name:  c_TXPoolPrefix + "accountslots",
		Value: quaiconfig.Defaults.TxPool.AccountSlots,
		Usage: "Minimum number of executable transaction slots guaranteed per account" + generateEnvDoc(c_TXPoolPrefix+"accountslots"),
	}

	TxPoolGlobalSlotsFlag = Flag{
		Name:  c_TXPoolPrefix + "globalslots",
		Value: quaiconfig.Defaults.TxPool.GlobalSlots,
		Usage: "Maximum number of executable transaction slots for all accounts" + generateEnvDoc(c_TXPoolPrefix+"globalslots"),
	}

	TxPoolAccountQueueFlag = Flag{
		Name:  c_TXPoolPrefix + "accountqueue",
		Value: quaiconfig.Defaults.TxPool.AccountQueue,
		Usage: "Maximum number of non-executable transaction slots permitted per account" + generateEnvDoc(c_TXPoolPrefix+"accountqueue"),
	}

	TxPoolGlobalQueueFlag = Flag{
		Name:  c_TXPoolPrefix + "globalqueue",
		Value: quaiconfig.Defaults.TxPool.GlobalQueue,
		Usage: "Maximum number of non-executable transaction slots for all accounts" + generateEnvDoc(c_TXPoolPrefix+"globalqueue"),
	}

	TxPoolLifetimeFlag = Flag{
		Name:  c_TXPoolPrefix + "lifetime",
		Value: quaiconfig.Defaults.TxPool.Lifetime,
		Usage: "Maximum amount of time non-executable transaction are queued" + generateEnvDoc(c_TXPoolPrefix+"lifetime"),
	}

	TxPoolSharingClientsFlag = Flag{
		Name:  c_TXPoolPrefix + "sharing-clients",
		Value: "",
		Usage: "Comma separated list of clients end points that the node would directy share the transactions with" + generateEnvDoc(c_TXPoolPrefix+"sharing-clients"),
	}

	CacheFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache",
		Value: 1024,
		Usage: "Megabytes of memory allocated to internal caching (default = 4096 quai full node, 128 light mode)" + generateEnvDoc(c_NodeFlagPrefix+"cache"),
	}

	CacheDatabaseFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache-database",
		Value: 50,
		Usage: "Percentage of cache memory allowance to use for database io" + generateEnvDoc(c_NodeFlagPrefix+"cache-database"),
	}

	CacheTrieFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache-trie",
		Value: 15,
		Usage: "Percentage of cache memory allowance to use for trie caching (default = 15% full mode, 30% archive mode)" + generateEnvDoc(c_NodeFlagPrefix+"cache-trie"),
	}

	CacheTrieJournalFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache-trie-journal",
		Value: quaiconfig.Defaults.TrieCleanCacheJournal,
		Usage: "Disk journal directory for trie cache to survive node restarts" + generateEnvDoc(c_NodeFlagPrefix+"cache-trie-journal"),
	}

	CacheTrieRejournalFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache-trie-rejournal",
		Value: quaiconfig.Defaults.TrieCleanCacheRejournal,
		Usage: "Time interval to regenerate the trie cache journal" + generateEnvDoc(c_NodeFlagPrefix+"cache-trie-rejournal"),
	}

	CacheGCFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache-gc",
		Value: 25,
		Usage: "Percentage of cache memory allowance to use for trie pruning (default = 25% full mode, 0% archive mode)" + generateEnvDoc(c_NodeFlagPrefix+"cache-gc"),
	}

	CacheSnapshotFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache-snapshot",
		Value: 10,
		Usage: "Percentage of cache memory allowance to use for snapshot caching (default = 10% full mode, 20% archive mode)" + generateEnvDoc(c_NodeFlagPrefix+"cache-snapshot"),
	}

	CacheNoPrefetchFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache-noprefetch",
		Value: false,
		Usage: "Disable heuristic state prefetch during block import (less CPU and disk IO, more time waiting for data)" + generateEnvDoc(c_NodeFlagPrefix+"cache-noprefetch"),
	}

	CachePreimagesFlag = Flag{
		Name:  c_NodeFlagPrefix + "cache-preimages",
		Value: false,
		Usage: "Enable recording the SHA3/keccak preimages of trie keys" + generateEnvDoc(c_NodeFlagPrefix+"cache-preimages"),
	}

	ConsensusEngineFlag = Flag{
		Name:  c_NodeFlagPrefix + "consensus-engine",
		Value: "progpow",
		Usage: "Consensus engine that the blockchain will run and verify blocks using" + generateEnvDoc(c_NodeFlagPrefix+"consensus-engine"),
	}

	MinerGasPriceFlag = Flag{
		Name:  c_NodeFlagPrefix + "miner-gasprice",
		Value: newBigIntValue(quaiconfig.Defaults.Miner.GasPrice),
		Usage: "Minimum gas price for mining a transaction" + generateEnvDoc(c_NodeFlagPrefix+"miner-gasprice"),
	}

	UnlockedAccountFlag = Flag{
		Name:  c_NodeFlagPrefix + "unlock",
		Value: "",
		Usage: "Comma separated list of accounts to unlock" + generateEnvDoc(c_NodeFlagPrefix+"unlock"),
	}

	PasswordFileFlag = Flag{
		Name:  c_NodeFlagPrefix + "password",
		Value: "",
		Usage: "Password file to use for non-interactive password input" + generateEnvDoc(c_NodeFlagPrefix+"password"),
	}

	KeyStoreDirFlag = Flag{
		Name:  c_NodeFlagPrefix + "keystore",
		Value: "",
		Usage: "Directory for the keystore (default = inside the datadir)",
	}

	VMEnableDebugFlag = Flag{
		Name:  c_NodeFlagPrefix + "vmdebug",
		Value: false,
		Usage: "Record information useful for VM and contract debugging" + generateEnvDoc(c_NodeFlagPrefix+"vmdebug"),
	}

	PprofFlag = Flag{
		Name:  c_NodeFlagPrefix + "pprof",
		Value: false,
		Usage: "Enable the pprof HTTP server",
	}

	InsecureUnlockAllowedFlag = Flag{
		Name:  c_NodeFlagPrefix + "allow-insecure-unlock",
		Value: false,
		Usage: "Allow insecure account unlocking when account-related RPCs are exposed by http" + generateEnvDoc(c_NodeFlagPrefix+"allow-insecure-unlock"),
	}

	QuaiCoinbaseFlag = Flag{
		Name:  c_NodeFlagPrefix + "quai-coinbases",
		Value: "0x0000000000000000000000000000000000000001",
		Usage: "Input TOML string or path to TOML file" + generateEnvDoc(c_NodeFlagPrefix+"quai-coinbase"),
	}

	QiCoinbaseFlag = Flag{
		Name:  c_NodeFlagPrefix + "qi-coinbases",
		Value: "0x0080000000000000000000000000000000000001",
		Usage: "Input TOML string or path to TOML file" + generateEnvDoc(c_NodeFlagPrefix+"qi-coinbase"),
	}

	LockupContractAddressFlag = Flag{
		Name:  c_NodeFlagPrefix + "lockup-contract-address",
		Value: "",
		Usage: "Address of the lockup smart contract to send coinbase rewards to" + generateEnvDoc(c_NodeFlagPrefix+"lockup-contract-address"),
	}

	MinerPreferenceFlag = Flag{
		Name:  c_NodeFlagPrefix + "miner-preference",
		Value: 0.5,
		Usage: "Indicates preference towards mining Quai or Qi. Any value between 0 and 1 is valid. Neutral: 0.5, Quai only: 0, Qi only: 1" + generateEnvDoc(c_NodeFlagPrefix+"miner-preference"),
	}

	CoinbaseLockupFlag = Flag{
		Name:  c_NodeFlagPrefix + "coinbase-lockup",
		Value: 0,
		Usage: "Lockup byte used to determine the number of blocks that coinbase rewards are locked for" + generateEnvDoc(c_NodeFlagPrefix+"coinbase-lockup"),
	}

	IndexAddressUtxos = Flag{
		Name:  c_NodeFlagPrefix + "index-address-utxos",
		Value: false,
		Usage: "Index address utxos" + generateEnvDoc(c_NodeFlagPrefix+"index-address-utxos"),
	}

	ReIndex = Flag{
		Name:  c_NodeFlagPrefix + "reindex",
		Value: false,
		Usage: "Re-index the UTXO indexer. This will take a long time!" + generateEnvDoc(c_NodeFlagPrefix+"reindex"),
	}

	ValidateIndexer = Flag{
		Name:  c_NodeFlagPrefix + "validate-indexer",
		Value: false,
		Usage: "Validate the UTXO indexer. This will take a long time!" + generateEnvDoc(c_NodeFlagPrefix+"validate-index"),
	}

	EnvironmentFlag = Flag{
		Name:  c_NodeFlagPrefix + "environment",
		Value: params.ColosseumName,
		Usage: "environment to run in (local, colosseum, garden, orchard, lighthouse, dev)" + generateEnvDoc(c_NodeFlagPrefix+"environment"),
	}

	QuaiStatsURLFlag = Flag{
		Name:  c_NodeFlagPrefix + "quaistats",
		Value: "",
		Usage: "Reporting URL of a quaistats service (nodename:secret@host:port)" + generateEnvDoc(c_NodeFlagPrefix+"quaistats"),
	}

	SendFullStatsFlag = Flag{
		Name:  c_NodeFlagPrefix + "sendfullstats",
		Value: false,
		Usage: "Send full stats boolean flag for quaistats" + generateEnvDoc(c_NodeFlagPrefix+"sendfullstats"),
	}

	StartingExpansionNumberFlag = Flag{
		Name:  c_NodeFlagPrefix + "starting-expansion-num",
		Value: 0,
		Usage: "Start the node at the expansion number preferred" + generateEnvDoc(c_NodeFlagPrefix+"starting-expansion-num"),
	}

	NodeLogLevelFlag = Flag{
		Name:  c_NodeFlagPrefix + "log-level",
		Value: "info",
		Usage: "log level (trace, debug, info, warn, error, fatal, panic)" + generateEnvDoc(c_GlobalFlagPrefix+"log-level"),
	}

	GenesisNonce = Flag{
		Name:  c_NodeFlagPrefix + "genesis-nonce",
		Value: "",
		Usage: "Nonce hex string to use for the genesis block" + generateEnvDoc(c_NodeFlagPrefix+"genesis-nonce"),
	}

	Telemetry = Flag{
		Name:  c_NodeFlagPrefix + "telemetry",
		Value: true,
		Usage: "Enable telemetry reporting" + generateEnvDoc(c_NodeFlagPrefix+"telemetry"),
	}
)

var (
	// ****************************************
	// **                                    **
	// ** 	      RPC FLAGS                  **
	// **                                    **
	// ****************************************
	HTTPEnabledFlag = Flag{
		Name:  c_RPCFlagPrefix + "http",
		Value: true,
		Usage: "Enable the HTTP-RPC server" + generateEnvDoc(c_RPCFlagPrefix+"http"),
	}

	HTTPListenAddrFlag = Flag{
		Name:  c_RPCFlagPrefix + "http-addr",
		Value: node.DefaultHTTPHost,
		Usage: "HTTP-RPC server listening interface" + generateEnvDoc(c_RPCFlagPrefix+"http-addr"),
	}

	HTTPCORSDomainFlag = Flag{
		Name:  c_RPCFlagPrefix + "http-corsdomain",
		Value: "",
		Usage: "Comma separated list of domains from which to accept cross origin requests (browser enforced)" + generateEnvDoc(c_RPCFlagPrefix+"http-corsdomain"),
	}

	HTTPVirtualHostsFlag = Flag{
		Name:  c_RPCFlagPrefix + "http-vhosts",
		Value: strings.Join(node.DefaultConfig.HTTPVirtualHosts, ","),
		Usage: "Comma separated list of virtual hostnames from which to accept requests (server enforced). Accepts '*' wildcard." + generateEnvDoc(c_RPCFlagPrefix+"http-vhosts"),
	}

	HTTPApiFlag = Flag{
		Name:  c_RPCFlagPrefix + "http-api",
		Value: "",
		Usage: "API's offered over the HTTP-RPC interface" + generateEnvDoc(c_RPCFlagPrefix+"http-api"),
	}

	HTTPPathPrefixFlag = Flag{
		Name:  c_RPCFlagPrefix + "http-rpcprefix",
		Value: "",
		Usage: "HTTP path path prefix on which JSON-RPC is served. Use '/' to serve on all paths." + generateEnvDoc(c_RPCFlagPrefix+"http-rpcprefix"),
	}

	HTTPPortStartFlag = Flag{
		Name:  c_RPCFlagPrefix + "http-port",
		Value: 9001,
		Usage: "HTTP-RPC server listening port" + generateEnvDoc(c_RPCFlagPrefix+"http-port"),
	}

	WSEnabledFlag = Flag{
		Name:  c_RPCFlagPrefix + "ws",
		Value: true,
		Usage: "Enable the WS-RPC server" + generateEnvDoc(c_RPCFlagPrefix+"ws"),
	}

	WSListenAddrFlag = Flag{
		Name:  c_RPCFlagPrefix + "ws-addr",
		Value: node.DefaultWSHost,
		Usage: "WS-RPC server listening interface" + generateEnvDoc(c_RPCFlagPrefix+"ws-addr"),
	}

	WSMaxSubsFlag = Flag{
		Name:  c_RPCFlagPrefix + "ws-max-subs",
		Value: 1000,
		Usage: "maximum concurrent subscribers to the WS-RPC server",
	}

	WSApiFlag = Flag{
		Name:  c_RPCFlagPrefix + "ws-api",
		Value: "",
		Usage: "API's offered over the WS-RPC interface" + generateEnvDoc(c_RPCFlagPrefix+"ws-api"),
	}

	WSAllowedOriginsFlag = Flag{
		Name:  c_RPCFlagPrefix + "ws-origins",
		Value: "",
		Usage: "Origins from which to accept websockets requests" + generateEnvDoc(c_RPCFlagPrefix+"ws-origins"),
	}

	WSPathPrefixFlag = Flag{
		Name:  c_RPCFlagPrefix + "ws-rpcprefix",
		Value: "",
		Usage: "HTTP path prefix on which JSON-RPC is served. Use '/' to serve on all paths." + generateEnvDoc(c_RPCFlagPrefix+"ws-rpcprefix"),
	}

	WSPortStartFlag = Flag{
		Name:  c_RPCFlagPrefix + "ws-port",
		Value: 8001,
		Usage: "WS-RPC server listening port" + generateEnvDoc(c_RPCFlagPrefix+"ws-port"),
	}

	PreloadJSFlag = Flag{
		Name:  c_RPCFlagPrefix + "preload",
		Value: "",
		Usage: "Comma separated list of JavaScript files to preload into the console" + generateEnvDoc(c_RPCFlagPrefix+"preload"),
	}

	RPCGlobalTxFeeCapFlag = Flag{
		Name:  c_RPCFlagPrefix + "txfeecap",
		Value: 0,
		Usage: "Sets a cap on transaction fee (in ether) that can be sent via the RPC APIs (0 = no cap)",
	}

	RPCGlobalGasCapFlag = Flag{
		Name:  c_RPCFlagPrefix + "gascap",
		Value: quaiconfig.Defaults.RPCGasCap,
		Usage: "Sets a cap on gas that can be used in eth_call/estimateGas (0=infinite)" + generateEnvDoc(c_RPCFlagPrefix+"gascap"),
	}

	RpcVersion = Flag{
		Name:  c_RPCFlagPrefix + "version",
		Value: "v1",
		Usage: "RPC version to use (v1)" + generateEnvDoc(c_RPCFlagPrefix+"version"),
	}
)

var (
	// ****************************************
	// **                                    **
	// **         WORKSHARE FLAGS            **
	// **                                    **
	// ****************************************
	WorkShareMiningFlag = Flag{
		Name:  c_WorkShareFlagPrefix + "mining",
		Value: false,
		Usage: "Enable workshare mining" + generateEnvDoc(c_WorkShareFlagPrefix+"mining"),
	}

	WorkShareThresholdFlag = Flag{
		Name:  c_WorkShareFlagPrefix + "threshold",
		Value: 10,
		Usage: "Threshold for workshare" + generateEnvDoc(c_WorkShareFlagPrefix+"threshold"),
	}

	WorkShareMinerEndpoints = Flag{
		Name:  c_WorkShareFlagPrefix + "miners",
		Value: "",
		Usage: "RPC endpoint to send minimally mined transactions for further working" + generateEnvDoc(c_WorkShareFlagPrefix+"miners"),
	}
	WorkShareP2PThreshold = Flag{
		Name:  c_WorkShareFlagPrefix + "p2pthreshold",
		Value: 7,
		Usage: "This is the workshares to be accepted by the node through the p2p layer" + generateEnvDoc(c_WorkShareFlagPrefix+"ws-percent"),
	}
)

var (
	// ****************************************
	// **                                    **
	// **           PEERS FLAGS              **
	// **                                    **
	// ****************************************
	PeersLogLevelFlag = Flag{
		Name:  c_PeersFlagPrefix + "log-level",
		Value: "info",
		Usage: "log level (trace, debug, info, warn, error, fatal, panic)" + generateEnvDoc(c_GlobalFlagPrefix+"log-level"),
	}
)

var (
	// ****************************************
	// **                                    **
	// **         METRICS FLAGS              **
	// **                                    **
	// ****************************************
	MetricsEnabledFlag = Flag{
		Name:  c_MetricsFlagPrefix + "enabled",
		Value: false,
		Usage: "Enable metrics collection and reporting" + generateEnvDoc(c_MetricsFlagPrefix+"enabled"),
	}
	MetricsEnabledExpensiveFlag = Flag{
		Name:  c_MetricsFlagPrefix + "metrics-expensive",
		Value: false,
		Usage: "Enable expensive metrics collection and reporting" + generateEnvDoc(c_MetricsFlagPrefix+"metrics-expensive"),
	}
	MetricsHTTPFlag = Flag{
		Name:  c_MetricsFlagPrefix + "metrics-addr",
		Value: metrics_config.DefaultConfig.HTTP,
		Usage: "Enable stand-alone metrics HTTP server listening interface" + generateEnvDoc(c_MetricsFlagPrefix+"metrics-addr"),
	}
	MetricsPortFlag = Flag{
		Name:  c_MetricsFlagPrefix + "metrics-port",
		Value: metrics_config.DefaultConfig.Port,
		Usage: "Metrics HTTP server listening port" + generateEnvDoc(c_MetricsFlagPrefix+"metrics-port"),
	}
)

// ParseCoinbaseAddresses reads the coinbase addresses and performs necessary validation.
func ParseCoinbaseAddresses() (map[string]common.Address, error) {
	quaiCoinbases := viper.GetString(QuaiCoinbaseFlag.Name)
	qiCoinbases := viper.GetString(QiCoinbaseFlag.Name)
	coinbases := make(map[string]common.Address)

	if quaiCoinbases == "" || qiCoinbases == "" {
		missingCoinbaseErr := errors.New("must provide both Quai and Qi coinbase addresses")
		log.Global.Fatal(missingCoinbaseErr)
		return nil, missingCoinbaseErr
	}

	for _, quaiCoinbase := range strings.Split(quaiCoinbases, ",") {
		if quaiCoinbase == "0x0000000000000000000000000000000000000001" {
			log.Global.Warn("Default Quai coinbase address is being used. If you are not mining, you can ignore this message, otherwise please set --quai-coinbases.")
		}

		quaiAddr, err := isValidAddress(quaiCoinbase)
		if err != nil {
			log.Global.WithField("err", err).Fatalf("Error parsing quai address")
			return nil, err
		}
		quaiAddrCoinbaseKey := quaiAddr.Location().Name() + "quai"
		coinbases[quaiAddrCoinbaseKey] = quaiAddr
	}

	for _, qiCoinbase := range strings.Split(qiCoinbases, ",") {
		if qiCoinbase == "0x0080000000000000000000000000000000000001" {
			log.Global.Warn("Default Qi coinbase address is being used. If you are not mining, you can ignore this message, otherwise please set --qi-coinbases.")
		}
		qiAddr, err := isValidAddress(qiCoinbase)
		if err != nil {
			log.Global.WithField("err", err).Fatalf("Error parsing qi address")
			return nil, err
		}
		qiAddrCoinbaseKey := qiAddr.Location().Name() + "qi"
		coinbases[qiAddrCoinbaseKey] = qiAddr
	}

	return coinbases, nil
}

func isValidAddress(addressStr string) (common.Address, error) {
	addressStr = strings.TrimSpace(addressStr)
	address := common.HexToAddress(addressStr, common.Location{0, 0})
	re := regexp.MustCompile(`^(0x)?[0-9a-fA-F]{40}$`)
	if !re.MatchString(addressStr) {
		return common.Address{}, fmt.Errorf("invalid address: %s", address)
	}
	return address, nil
}

func CreateAndBindFlag(flag Flag, cmd *cobra.Command) {
	switch val := flag.Value.(type) {
	case string:
		cmd.PersistentFlags().StringP(flag.GetName(), flag.GetAbbreviation(), val, flag.GetUsage())
	case bool:
		cmd.PersistentFlags().BoolP(flag.GetName(), flag.GetAbbreviation(), val, flag.GetUsage())
	case []string:
		cmd.PersistentFlags().StringSliceP(flag.GetName(), flag.GetAbbreviation(), val, flag.GetUsage())
	case time.Duration:
		cmd.PersistentFlags().DurationP(flag.GetName(), flag.GetAbbreviation(), val, flag.GetUsage())
	case int:
		cmd.PersistentFlags().IntP(flag.GetName(), flag.GetAbbreviation(), val, flag.GetUsage())
	case int64:
		cmd.PersistentFlags().Int64P(flag.GetName(), flag.GetAbbreviation(), val, flag.GetUsage())
	case uint64:
		cmd.PersistentFlags().Uint64P(flag.GetName(), flag.GetAbbreviation(), val, flag.GetUsage())
	case float64:
		cmd.PersistentFlags().Float64P(flag.GetName(), flag.GetAbbreviation(), val, flag.GetUsage())
	case *TextMarshalerValue:
		cmd.PersistentFlags().VarP(val, flag.GetName(), flag.GetAbbreviation(), flag.GetUsage())
	case *BigIntValue:
		cmd.PersistentFlags().VarP(val, flag.GetName(), flag.GetAbbreviation(), flag.GetUsage())
	default:
		log.Global.WithFields(log.Fields{
			"flag": flag.GetName(),
			"type": fmt.Sprintf("%T", val),
		}).Error("Flag type not supported")
	}
	viper.BindPFlag(flag.GetName(), cmd.PersistentFlags().Lookup(flag.GetName()))
}

// helper function that given a cobra flag name, returns the corresponding
// help legend for the equivalent environment variable
func generateEnvDoc(flag string) string {
	envVar := constants.ENV_PREFIX + "_" + strings.ReplaceAll(strings.ToUpper(flag), "-", "_")
	return fmt.Sprintf(" [%s]", envVar)
}

// setNodeUserIdent creates the user identifier from CLI flags.
func setNodeUserIdent(cfg *node.Config) {
	if identity := viper.GetString(IdentityFlag.Name); len(identity) > 0 {
		cfg.UserIdent = identity
	}
}

// SplitAndTrim splits input separated by a comma
// and trims excessive white space from the substrings.
func SplitAndTrim(input string) (ret []string) {
	l := strings.Split(input, ",")
	for _, r := range l {
		if r = strings.TrimSpace(r); r != "" {
			ret = append(ret, r)
		}
	}
	return ret
}

// setHTTP creates the HTTP RPC listener interface string from the set
// command line flags, returning empty if the HTTP endpoint is disabled.
func setHTTP(cfg *node.Config, nodeLocation common.Location) {
	if viper.GetBool(HTTPEnabledFlag.Name) && cfg.HTTPHost == "" {
		cfg.HTTPHost = viper.GetString(HTTPListenAddrFlag.Name)
	}

	cfg.HTTPPort = GetHttpPort(nodeLocation)

	if viper.IsSet(HTTPCORSDomainFlag.Name) {
		cfg.HTTPCors = SplitAndTrim(viper.GetString(HTTPCORSDomainFlag.Name))
	}

	if viper.IsSet(HTTPApiFlag.Name) {
		cfg.HTTPModules = SplitAndTrim(viper.GetString(HTTPApiFlag.Name))
	}

	if viper.IsSet(HTTPVirtualHostsFlag.Name) {
		cfg.HTTPVirtualHosts = SplitAndTrim(viper.GetString(HTTPVirtualHostsFlag.Name))
	}

	if viper.IsSet(HTTPPathPrefixFlag.Name) {
		cfg.HTTPPathPrefix = viper.GetString(HTTPPathPrefixFlag.Name)
	}
}

func GetHttpPort(nodeLocation common.Location) int {
	var startPort int
	if viper.IsSet(HTTPPortStartFlag.Name) {
		startPort = viper.GetInt(HTTPPortStartFlag.Name)
	} else {
		startPort = HTTPPortStartFlag.Value.(int)
	}
	switch nodeLocation.Context() {
	case common.PRIME_CTX:
		return startPort
	case common.REGION_CTX:
		return (startPort + c_regionPortOffset) + nodeLocation.Region()
	case common.ZONE_CTX:
		return (startPort + c_zonePortOffset) + 20*nodeLocation.Region() + nodeLocation.Zone()
	}
	panic("node location is not valid")
}

// setWS creates the WebSocket RPC listener interface string from the set
// command line flags, returning empty if the HTTP endpoint is disabled.
func setWS(cfg *node.Config, nodeLocation common.Location) {
	if viper.GetBool(WSEnabledFlag.Name) && cfg.WSHost == "" {
		cfg.WSHost = "127.0.0.1"
		if viper.IsSet(WSListenAddrFlag.Name) {
			cfg.WSHost = viper.GetString(WSListenAddrFlag.Name)
		}
	}

	cfg.WSPort = GetWSPort(nodeLocation)

	cfg.WSOrigins = SplitAndTrim(viper.GetString(WSAllowedOriginsFlag.Name))

	cfg.WSModules = SplitAndTrim(viper.GetString(WSApiFlag.Name))

	cfg.WSPathPrefix = viper.GetString(WSPathPrefixFlag.Name)
}

func GetWSPort(nodeLocation common.Location) int {
	var startPort int
	if viper.IsSet(WSPortStartFlag.Name) {
		startPort = viper.GetInt(WSPortStartFlag.Name)
	} else {
		startPort = WSPortStartFlag.Value.(int)
	}
	switch nodeLocation.Context() {
	case common.PRIME_CTX:
		return startPort
	case common.REGION_CTX:
		return (startPort + c_regionPortOffset) + nodeLocation.Region()
	case common.ZONE_CTX:
		return (startPort + c_zonePortOffset) + 20*nodeLocation.Region() + nodeLocation.Zone()
	}
	panic("node location is not valid")
}

// setGasLimitCeil sets the gas limit ceils based on the network that is
// running
func setGasLimitCeil(cfg *quaiconfig.Config) {
	switch viper.GetString(EnvironmentFlag.Name) {
	case params.ColosseumName:
		cfg.Miner.GasCeil = params.ColosseumGasCeil
	case params.GardenName:
		cfg.Miner.GasCeil = params.GardenGasCeil
	case params.OrchardName:
		cfg.Miner.GasCeil = params.OrchardGasCeil
	case params.LighthouseName:
		cfg.Miner.GasCeil = params.LighthouseGasCeil
	case params.LocalName, params.DevName:
		cfg.Miner.GasCeil = params.LocalGasCeil
	default:
		cfg.Miner.GasCeil = params.ColosseumGasCeil
	}
}

// MakeDatabaseHandles raises out the number of allowed file handles per process
// for Quai and returns half of the allowance to assign to the database.
func MakeDatabaseHandles() int {
	limit, err := fdlimit.Maximum()
	if err != nil {
		Fatalf("Failed to retrieve file descriptor allowance: %v", err)
	}
	raised, err := fdlimit.Raise(uint64(limit))
	if err != nil {
		Fatalf("Failed to raise file descriptor allowance: %v", err)
	}
	return int(raised / 2) // Leave half for networking and other stuff
}

// HexAddress converts an account specified directly as a hex encoded string or
// a key index in the key store to an internal account representation.
func HexAddress(account string, nodeLocation common.Location) (common.Address, error) {
	// If the specified account is a valid address, return it
	if common.IsHexAddress(account) {
		return common.HexToAddress(account, nodeLocation), nil
	}
	return common.Address{}, errors.New("invalid account address")
}

// setCoinbase retrieves the etherbase either from the directly specified
// command line flags or from the keystore if CLI indexed.
func setCoinbase(cfg *quaiconfig.Config) {
	coinbaseMap, err := ParseCoinbaseAddresses()
	if err != nil {
		log.Global.Fatalf("error parsing coinbase addresses: %s", err)
	}
	quaiCoinbase, ok := coinbaseMap[cfg.NodeLocation.Name()+"quai"]
	if !ok {
		log.Global.Fatal("Missing Quai coinbase for this location")
	}
	qiCoinbase, ok := coinbaseMap[cfg.NodeLocation.Name()+"qi"]
	if !ok {
		log.Global.Fatal("Missing Qi coinbase for this location")
	}
	coinbaseLockupContractAddress := viper.GetString(LockupContractAddressFlag.Name)
	if coinbaseLockupContractAddress != "" {
		if !common.IsHexAddress(coinbaseLockupContractAddress) {
			log.Global.Fatalf("invalid coinbase lockup address: %s", coinbaseLockupContractAddress)
		}
		lockupContractAddress := common.HexToAddress(coinbaseLockupContractAddress, cfg.NodeLocation)
		_, err := lockupContractAddress.InternalAddress()
		if err != nil {
			log.Global.Fatalf("invalid coinbase lockup address: %s", coinbaseLockupContractAddress)
		}
		cfg.Miner.LockupContractAddress = &lockupContractAddress
	}
	cfg.Miner.QuaiCoinbase = quaiCoinbase
	cfg.Miner.QiCoinbase = qiCoinbase
}

// MakePasswordList reads password lines from the file specified by the global --password flag.
func MakePasswordList() []string {
	path := viper.GetString(PasswordFileFlag.Name)
	if path == "" {
		return nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		Fatalf("Failed to read password file: %v", err)
	}
	lines := strings.Split(string(text), "\n")
	// Sanitise DOS line endings.
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}

// SetNodeConfig applies node-related command line flags to the config.
func SetNodeConfig(cfg *node.Config, nodeLocation common.Location, logger *log.Logger) {
	setHTTP(cfg, nodeLocation)
	setWS(cfg, nodeLocation)
	setNodeUserIdent(cfg)
	setDataDir(cfg)

	if viper.IsSet(KeyStoreDirFlag.Name) {
		cfg.KeyStoreDir = viper.GetString(KeyStoreDirFlag.Name)
	}
	if viper.GetString(EnvironmentFlag.Name) == params.DevName {
		cfg.UseLightweightKDF = true
	}
	if viper.IsSet(InsecureUnlockAllowedFlag.Name) {
		cfg.InsecureUnlockAllowed = viper.GetBool(InsecureUnlockAllowedFlag.Name)
	}
	if viper.IsSet(DBEngineFlag.Name) {
		dbEngine := viper.GetString(DBEngineFlag.Name)
		if dbEngine != "leveldb" && dbEngine != "pebble" {
			Fatalf("Invalid choice for db-engine '%s', allowed 'leveldb' or 'pebble'", dbEngine)
		}
		logger.WithField("db-engine", dbEngine).Info("Using db engine")
		cfg.DBEngine = dbEngine
	}
}

func setDataDir(cfg *node.Config) {
	environment := viper.GetString(EnvironmentFlag.Name)
	switch {
	case viper.IsSet(DataDirFlag.Name):
		cfg.DataDir = viper.GetString(DataDirFlag.Name)
	case environment == params.DevName:
		cfg.DataDir = "" // unless explicitly requested, use memory databases
	case environment == params.GardenName && cfg.DataDir == xdg.DataHome:
		cfg.DataDir = filepath.Join(xdg.DataHome, params.GardenName)
	case environment == params.OrchardName && cfg.DataDir == xdg.DataHome:
		cfg.DataDir = filepath.Join(xdg.DataHome, params.OrchardName)
	case environment == params.LighthouseName && cfg.DataDir == xdg.DataHome:
		cfg.DataDir = filepath.Join(xdg.DataHome, params.LighthouseName)
	case environment == params.LocalName && cfg.DataDir == xdg.DataHome:
		cfg.DataDir = filepath.Join(xdg.DataHome, params.LocalName)
	}
	// Set specific directory for node location within the hierarchy
	switch cfg.NodeLocation.Context() {
	case common.PRIME_CTX:
		cfg.DataDir = filepath.Join(cfg.DataDir, "prime")
	case common.REGION_CTX:
		regionNum := strconv.Itoa(cfg.NodeLocation.Region())
		cfg.DataDir = filepath.Join(cfg.DataDir, "region-"+regionNum)
	case common.ZONE_CTX:
		regionNum := strconv.Itoa(cfg.NodeLocation.Region())
		zoneNum := strconv.Itoa(cfg.NodeLocation.Zone())
		cfg.DataDir = filepath.Join(cfg.DataDir, "zone-"+regionNum+"-"+zoneNum)
	}
}

func setTxPool(cfg *core.TxPoolConfig, nodeLocation common.Location) {
	if viper.IsSet(TxPoolLocalsFlag.Name) && viper.GetString(TxPoolLocalsFlag.Name) != "" {
		locals := strings.Split(viper.GetString(TxPoolLocalsFlag.Name), ",")
		for _, account := range locals {
			if trimmed := strings.TrimSpace(account); !common.IsHexAddress(trimmed) {
				Fatalf("Invalid account in --txpool.locals: %s", trimmed)
			} else {
				internal, err := common.HexToAddress(account, nodeLocation).InternalAddress()
				if err != nil {
					log.Global.Errorf("Error: NodeLocation %s, Invalid account in --txpool.locals: %s, err %s"+"\n", nodeLocation.Name(), account, err)
					continue
				}
				cfg.Locals = append(cfg.Locals, internal)
			}
		}
	}
	if viper.IsSet(TxPoolNoLocalsFlag.Name) {
		cfg.NoLocals = viper.GetBool(TxPoolNoLocalsFlag.Name)
	}
	cfg.SyncTxWithReturn = viper.GetBool(TxPoolSyncTxWithReturnFlag.Name)
	if viper.IsSet(TxPoolJournalFlag.Name) {
		cfg.Journal = viper.GetString(TxPoolJournalFlag.Name)
	}
	if viper.IsSet(TxPoolRejournalFlag.Name) {
		cfg.Rejournal = viper.GetDuration(TxPoolRejournalFlag.Name)
	}
	if viper.IsSet(TxPoolPriceLimitFlag.Name) {
		cfg.PriceLimit = viper.GetUint64(TxPoolPriceLimitFlag.Name)
	}
	if viper.IsSet(TxPoolPriceBumpFlag.Name) {
		cfg.PriceBump = viper.GetUint64(TxPoolPriceBumpFlag.Name)
	}
	if viper.IsSet(TxPoolAccountSlotsFlag.Name) {
		cfg.AccountSlots = viper.GetUint64(TxPoolAccountSlotsFlag.Name)
	}
	if viper.IsSet(TxPoolGlobalSlotsFlag.Name) {
		cfg.GlobalSlots = viper.GetUint64(TxPoolGlobalSlotsFlag.Name)
	}
	if viper.IsSet(TxPoolAccountQueueFlag.Name) {
		cfg.AccountQueue = viper.GetUint64(TxPoolAccountQueueFlag.Name)
	}
	if viper.IsSet(TxPoolGlobalQueueFlag.Name) {
		cfg.GlobalQueue = viper.GetUint64(TxPoolGlobalQueueFlag.Name)
	}
	if viper.IsSet(TxPoolLifetimeFlag.Name) {
		cfg.Lifetime = viper.GetDuration(TxPoolLifetimeFlag.Name)
	}

	cfg.SharingClientsEndpoints = SplitAndTrim(viper.GetString(TxPoolSharingClientsFlag.Name))
}

func setConsensusEngineConfig(cfg *quaiconfig.Config) {
	if cfg.ConsensusEngine == "blake3" {
		// Override any default configs for hard coded networks.
		switch viper.GetString(EnvironmentFlag.Name) {
		case params.ColosseumName:
			cfg.PowConfig.DurationLimit = params.DurationLimit
			cfg.PowConfig.GasCeil = params.ColosseumGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.GardenName:
			cfg.PowConfig.DurationLimit = params.GardenDurationLimit
			cfg.PowConfig.GasCeil = params.GardenGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultGardenGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.OrchardName:
			cfg.PowConfig.DurationLimit = params.OrchardDurationLimit
			cfg.PowConfig.GasCeil = params.OrchardGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultOrchardGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.LighthouseName:
			cfg.PowConfig.DurationLimit = params.LighthouseDurationLimit
			cfg.PowConfig.GasCeil = params.LighthouseGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultLighthouseGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.LocalName:
			cfg.PowConfig.DurationLimit = params.LocalDurationLimit
			cfg.PowConfig.GasCeil = params.LocalGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultLocalGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.DevName:
			cfg.PowConfig.DurationLimit = params.DurationLimit
			cfg.PowConfig.GasCeil = params.LocalGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultLocalGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		default:
			cfg.PowConfig.DurationLimit = params.DurationLimit
			cfg.PowConfig.GasCeil = params.GasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		}
	} else {
		// Override any default configs for hard coded networks.
		switch viper.GetString(EnvironmentFlag.Name) {
		case params.ColosseumName:
			cfg.PowConfig.DurationLimit = params.DurationLimit
			cfg.PowConfig.GasCeil = params.ColosseumGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.GardenName:
			cfg.PowConfig.DurationLimit = params.GardenDurationLimit
			cfg.PowConfig.GasCeil = params.GardenGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultGardenGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.OrchardName:
			cfg.PowConfig.DurationLimit = params.OrchardDurationLimit
			cfg.PowConfig.GasCeil = params.OrchardGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultOrchardGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.LighthouseName:
			cfg.PowConfig.DurationLimit = params.LighthouseDurationLimit
			cfg.PowConfig.GasCeil = params.LighthouseGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultLighthouseGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.LocalName:
			cfg.PowConfig.DurationLimit = params.LocalDurationLimit
			cfg.PowConfig.GasCeil = params.LocalGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultLocalGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		case params.DevName:
			cfg.PowConfig.DurationLimit = params.DurationLimit
			cfg.PowConfig.GasCeil = params.LocalGasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultLocalGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		default:
			cfg.PowConfig.DurationLimit = params.DurationLimit
			cfg.PowConfig.GasCeil = params.GasCeil
			cfg.PowConfig.MinDifficulty = new(big.Int).Div(core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra).Difficulty, common.Big2)
		}
	}
}

func setWhitelist(cfg *quaiconfig.Config) {
	whitelist := viper.GetString(WhitelistFlag.Name)
	if whitelist == "" {
		return
	}
	cfg.Whitelist = make(map[uint64]common.Hash)
	for _, entry := range strings.Split(whitelist, ",") {
		parts := strings.Split(entry, "=")
		if len(parts) != 2 {
			Fatalf("Invalid whitelist entry: %s", entry)
		}
		number, err := strconv.ParseUint(parts[0], 0, 64)
		if err != nil {
			Fatalf("Invalid whitelist block number %s: %v", parts[0], err)
		}
		var hash common.Hash
		if err = hash.UnmarshalText([]byte(parts[1])); err != nil {
			Fatalf("Invalid whitelist hash %s: %v", parts[1], err)
		}
		cfg.Whitelist[number] = hash
	}
}

// CheckExclusive verifies that only a single instance of the provided flags was
// set by the user. Each flag might optionally be followed by a string type to
// specialize it further.
func CheckExclusive(args ...interface{}) {
	set := make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		// Ensure the argument is a string (flag name)
		flag, ok := args[i].(Flag)
		if !ok {
			panic(fmt.Sprintf("invalid argument, not string type: %T", args[i]))
		}

		// Check if the next arg extends the current flag
		if i+1 < len(args) {
			switch extension := args[i+1].(type) {
			case string:
				// Extended flag check
				if viper.GetString(flag.Name) == extension {
					set = append(set, "--"+flag.Name+"="+extension)
				}
				i++ // skip the next argument as it's processed
				continue
			case Flag:
			default:
				panic(fmt.Sprintf("invalid argument, not string extension: %T", args[i+1]))
			}
		}

		// Check if the flag is set
		if viper.IsSet(flag.Name) {
			set = append(set, "--"+flag.Name)
		}
	}

	if len(set) > 1 {
		Fatalf("Flags %v can't be used at the same time", strings.Join(set, ", "))
	}
}

func EnablePprof() {
	runtime.SetBlockProfileRate(1)
	runtime.SetMutexProfileFraction(1)
	port := "8085"
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(godebug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		log.Global.Print(http.ListenAndServe("localhost:"+port, nil))
	}()
}

// SetQuaiConfig applies quai-related command line flags to the config.
func SetQuaiConfig(stack *node.Node, cfg *quaiconfig.Config, slicesRunning []common.Location, nodeLocation common.Location, currentExpansionNumber uint8, logger *log.Logger) {
	cfg.NodeLocation = nodeLocation
	cfg.SlicesRunning = slicesRunning

	// only set etherbase if its a zone chain
	if len(nodeLocation) == 2 {
		setCoinbase(cfg)
	}
	setTxPool(&cfg.TxPool, nodeLocation)

	// If blake3 consensus engine is specifically asked use the blake3 engine
	if viper.GetString(ConsensusEngineFlag.Name) == "blake3" {
		cfg.ConsensusEngine = "blake3"
	} else {
		cfg.ConsensusEngine = "progpow"
	}
	setConsensusEngineConfig(cfg)

	setWhitelist(cfg)

	// set the gas limit ceil
	setGasLimitCeil(cfg)

	// Cap the cache allowance and tune the garbage collector
	mem, err := gopsutil.VirtualMemory()
	if err == nil {
		if 32<<(^uintptr(0)>>63) == 32 && mem.Total > 2*1024*1024*1024 {
			logger.WithFields(log.Fields{
				"available":   mem.Total / 1024 / 1024,
				"addressable": 2 * 1024,
			}).Warn("Lowering memory allowance on 32bit arch")
			mem.Total = 2 * 1024 * 1024 * 1024
		}
		allowance := int(mem.Total / 1024 / 1024 / 3)
		if cache := viper.GetInt(CacheFlag.Name); cache > allowance {
			logger.WithFields(log.Fields{
				"provided": cache,
				"updated":  allowance,
			}).Warn("Sanitizing cache to Go's GC limits")
			viper.GetViper().Set(CacheFlag.Name, strconv.Itoa(allowance))
		}
	}
	// Ensure Go's GC ignores the database cache for trigger percentage
	cache := viper.GetInt(CacheFlag.Name)
	gogc := math.Max(20, math.Min(100, 100/(float64(cache)/1024)))

	logger.WithField("gogc", int(gogc)).Debug("Sanitizing Go's GC trigger")
	godebug.SetGCPercent(int(gogc))

	if viper.IsSet(NetworkIdFlag.Name) {
		cfg.NetworkId = viper.GetUint64(NetworkIdFlag.Name)
	}
	if viper.IsSet(CacheFlag.Name) || viper.IsSet(CacheDatabaseFlag.Name) {
		cfg.DatabaseCache = viper.GetInt(CacheFlag.Name) * viper.GetInt(CacheDatabaseFlag.Name) / 100
	}
	cfg.DatabaseHandles = MakeDatabaseHandles()
	if viper.IsSet(AncientDirFlag.Name) {
		cfg.DatabaseFreezer = viper.GetString(AncientDirFlag.Name)
	}

	if viper.IsSet(CacheNoPrefetchFlag.Name) {
		cfg.NoPrefetch = viper.GetBool(CacheNoPrefetchFlag.Name)
	}
	// Read the value from the flag no matter if it's set or not.
	cfg.Preimages = viper.GetBool(CachePreimagesFlag.Name)
	if cfg.NoPruning && !cfg.Preimages {
		cfg.Preimages = true
		logger.Info("Enabling recording of key preimages since archive mode is used")
	}
	if viper.IsSet(TxLookupLimitFlag.Name) {
		cfg.TxLookupLimit = viper.GetUint64(TxLookupLimitFlag.Name)
	}
	if viper.IsSet(CacheFlag.Name) || viper.IsSet(CacheTrieFlag.Name) {
		cfg.TrieCleanCache = viper.GetInt(CacheFlag.Name) * viper.GetInt(CacheTrieFlag.Name) / 100
	}
	if viper.IsSet(CacheTrieJournalFlag.Name) {
		cfg.TrieCleanCacheJournal = viper.GetString(CacheTrieJournalFlag.Name)
	}
	if viper.IsSet(CacheTrieRejournalFlag.Name) {
		cfg.TrieCleanCacheRejournal = viper.GetDuration(CacheTrieRejournalFlag.Name)
	}
	if viper.IsSet(CacheFlag.Name) || viper.IsSet(CacheGCFlag.Name) {
		cfg.TrieDirtyCache = viper.GetInt(CacheFlag.Name) * viper.GetInt(CacheGCFlag.Name) / 100
	}
	if viper.IsSet(CacheFlag.Name) || viper.IsSet(CacheSnapshotFlag.Name) {
		cfg.SnapshotCache = viper.GetInt(CacheFlag.Name) * viper.GetInt(CacheSnapshotFlag.Name) / 100
	}
	if !viper.GetBool(SnapshotFlag.Name) {
		cfg.TrieCleanCache += cfg.SnapshotCache
		cfg.SnapshotCache = 0 // Disabled
	}
	if viper.IsSet(DocRootFlag.Name) {
		cfg.DocRoot = viper.GetString(DocRootFlag.Name)
	}
	if viper.IsSet(VMEnableDebugFlag.Name) {
		// TODO(fjl): force-enable this in --dev mode
		cfg.EnablePreimageRecording = viper.GetBool(VMEnableDebugFlag.Name)
	}
	cfg.IndexAddressUtxos = viper.GetBool(IndexAddressUtxos.Name)

	cfg.TelemetryEnabled = viper.GetBool(Telemetry.Name)

	cfg.RpcVersion = viper.GetString(RpcVersion.Name)

	if viper.IsSet(RPCGlobalGasCapFlag.Name) {
		cfg.RPCGasCap = viper.GetUint64(RPCGlobalGasCapFlag.Name)
	}
	if cfg.RPCGasCap != 0 {
		logger.WithField("cap", cfg.RPCGasCap).Info("Global gas cap enabled")
	} else {
		logger.Info("Global gas cap disabled")
	}
	if viper.IsSet(RPCGlobalTxFeeCapFlag.Name) {
		cfg.RPCTxFeeCap = viper.GetFloat64(RPCGlobalTxFeeCapFlag.Name)
	}

	cfg.GenesisNonce, cfg.GenesisExtra = GetGenesisNonce()

	cfg.Miner.WorkShareMining = viper.GetBool(WorkShareMiningFlag.Name)
	cfg.Miner.WorkShareThreshold = params.WorkSharesThresholdDiff + viper.GetInt(WorkShareThresholdFlag.Name)
	if viper.GetString(WorkShareMinerEndpoints.Name) != "" {
		cfg.Miner.Endpoints = []string{viper.GetString(WorkShareMinerEndpoints.Name)}
	}

	cfg.WorkShareP2PThreshold = viper.GetInt(WorkShareP2PThreshold.Name)
	// workshare p2p threshold cannot be less than the workshare threshold diff
	if cfg.WorkShareP2PThreshold < params.WorkSharesThresholdDiff {
		cfg.WorkShareP2PThreshold = params.WorkSharesThresholdDiff
	}

	minerPreference := viper.GetFloat64(MinerPreferenceFlag.Name)
	if minerPreference < 0 || minerPreference > 1 {
		log.Global.WithField("MinerPreference", minerPreference).Fatal("Invalid MinerPreference field. Must be [0,1]")
	} else {
		cfg.Miner.MinerPreference = minerPreference
	}

	coinbaseLockup := viper.GetUint64(CoinbaseLockupFlag.Name)
	if coinbaseLockup < 0 || coinbaseLockup > uint64(len(params.LockupByteToBlockDepth))-1 {
		log.Global.WithField("CoinbaseLockup", coinbaseLockup).Error("Invalid CoinbaseLockup field. Must be [0,3]. Setting to default value 0")
		cfg.Miner.CoinbaseLockup = params.DefaultCoinbaseLockup
	} else {
		cfg.Miner.CoinbaseLockup = uint8(coinbaseLockup)
	}

	// Override any default configs for hard coded networks.
	switch viper.GetString(EnvironmentFlag.Name) {
	case params.ColosseumName:
		if !viper.IsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 1
		}
		cfg.Genesis = core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra)
		if cfg.ConsensusEngine == "progpow" {
			cfg.DefaultGenesisHash = params.ProgpowColosseumGenesisHash
		} else {
			cfg.DefaultGenesisHash = params.Blake3PowColosseumGenesisHash
		}

	case params.GardenName:
		if !viper.IsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 2
		}
		cfg.Genesis = core.DefaultGardenGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra)
		if cfg.ConsensusEngine == "progpow" {
			cfg.DefaultGenesisHash = params.ProgpowGardenGenesisHash
		} else {
			cfg.DefaultGenesisHash = params.Blake3PowGardenGenesisHash
		}
	case params.OrchardName:
		if !viper.IsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 3
		}
		cfg.Genesis = core.DefaultOrchardGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra)
		if cfg.ConsensusEngine == "progpow" {
			cfg.DefaultGenesisHash = params.ProgpowOrchardGenesisHash
		} else {
			cfg.DefaultGenesisHash = params.Blake3PowOrchardGenesisHash
		}
	case params.LocalName:
		if !viper.IsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 4
		}
		cfg.Genesis = core.DefaultLocalGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra)
		if cfg.ConsensusEngine == "progpow" {
			cfg.DefaultGenesisHash = params.ProgpowLocalGenesisHash
		} else {
			cfg.DefaultGenesisHash = params.Blake3PowLocalGenesisHash
		}
	case params.LighthouseName:
		if !viper.IsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 5
		}
		cfg.Genesis = core.DefaultLighthouseGenesisBlock(cfg.ConsensusEngine, cfg.GenesisNonce, cfg.GenesisExtra)
		if cfg.ConsensusEngine == "progpow" {
			cfg.DefaultGenesisHash = params.ProgpowLighthouseGenesisHash
		} else {
			cfg.DefaultGenesisHash = params.Blake3PowLighthouseGenesisHash
		}
	case params.DevName:
		if !viper.IsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 1337
		}

		if viper.IsSet(DataDirFlag.Name) {
			// Check if we have an already initialized chain and fall back to
			// that if so. Otherwise we need to generate a new genesis spec.
			chaindb := MakeChainDatabase(stack, false) // TODO (MariusVanDerWijden) make this read only
			if rawdb.ReadCanonicalHash(chaindb, 0) != (common.Hash{}) {
				cfg.Genesis = nil // fallback to db content
			}
			chaindb.Close()
		}
		if !viper.IsSet(MinerGasPriceFlag.Name) {
			cfg.Miner.GasPrice = big.NewInt(1)
		}
	}

	cfg.Genesis.AllocHash = params.AllocHash
	if nodeLocation.Equal(common.Location{0, 0}) {
		cfg.GenesisAllocs, err = params.VerifyGenesisAllocs("params/genesis_alloc.json", cfg.Genesis.AllocHash)
		if err != nil {
			if viper.GetString(EnvironmentFlag.Name) == params.LighthouseName {
				cfg.GenesisAllocs = []params.GenesisAccount{}
			} else {
				log.Global.WithField("err", err).Fatal("Unable to allocate genesis accounts")
			}
		}
	}

	cfg.Genesis.Config.Location = nodeLocation
}

func SplitTagsFlag(tagsFlag string) map[string]string {
	tags := strings.Split(tagsFlag, ",")
	tagsMap := map[string]string{}

	for _, t := range tags {
		if t != "" {
			kv := strings.Split(t, "=")

			if len(kv) == 2 {
				tagsMap[kv[0]] = kv[1]
			}
		}
	}

	return tagsMap
}

// MakeChainDatabase open an LevelDB using the flags passed to the client and will hard crash if it fails.
func MakeChainDatabase(stack *node.Node, readonly bool) ethdb.Database {
	var (
		cache   = viper.GetInt(CacheFlag.Name) * viper.GetInt(CacheDatabaseFlag.Name) / 100
		handles = MakeDatabaseHandles()

		err     error
		chainDb ethdb.Database
	)
	name := "chaindata"
	chainDb, err = stack.OpenDatabaseWithFreezer(name, cache, handles, viper.GetString(AncientDirFlag.Name), "", readonly, stack.Config().NodeLocation)
	if err != nil {
		Fatalf("Could not open database: %v", err)
	}
	return chainDb
}

func GetGenesisNonce() (uint64, []byte) {
	nonceBytes := common.FromHex(viper.GetString(GenesisNonce.Name))
	if len(nonceBytes) < 8 {
		log.Global.Error("Genesis nonce is too short, using default")
		nonceBytes = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	}
	nonce := binary.BigEndian.Uint64(nonceBytes[:8])
	return nonce, nonceBytes[8:]
}

func MakeGenesis() *core.Genesis {
	consensusEngine := viper.GetString(ConsensusEngineFlag.Name)
	nonce, extra := GetGenesisNonce()
	var genesis *core.Genesis
	switch viper.GetString(EnvironmentFlag.Name) {
	case params.ColosseumName:
		genesis = core.DefaultColosseumGenesisBlock(consensusEngine, nonce, extra)
	case params.GardenName:
		genesis = core.DefaultGardenGenesisBlock(consensusEngine, nonce, extra)
	case params.OrchardName:
		genesis = core.DefaultOrchardGenesisBlock(consensusEngine, nonce, extra)
	case params.LighthouseName:
		genesis = core.DefaultLighthouseGenesisBlock(consensusEngine, nonce, extra)
	case params.LocalName:
		genesis = core.DefaultLocalGenesisBlock(consensusEngine, 0, []byte{})
	case params.DevName:
		Fatalf("Developer chains are ephemeral")
	default:
		genesis = core.DefaultGenesisBlock(nonce, extra)
	}
	return genesis
}

// MakeConsolePreloads retrieves the absolute paths for the console JavaScript
// scripts to preload before starting.
func MakeConsolePreloads() []string {
	// Skip preloading if there's nothing to preload
	if viper.GetString(PreloadJSFlag.Name) == "" {
		return nil
	}
	// Otherwise resolve absolute paths and return them
	var preloads []string

	for _, file := range strings.Split(viper.GetString(PreloadJSFlag.Name), ",") {
		preloads = append(preloads, strings.TrimSpace(file))
	}
	return preloads
}

func IsValidEnvironment(env string) bool {
	switch env {
	case params.ColosseumName,
		params.GardenName,
		params.OrchardName,
		params.LighthouseName,
		params.LocalName,
		params.DevName:
		return true
	default:
		return false
	}
}

var configData = make(map[string]map[string]interface{})

func addFlagsToCategory(flagCategories [][]Flag) {
	for _, category := range flagCategories {
		for _, flag := range category {
			split := strings.Split(flag.Name, ".")
			if split[1] == "config-dir" {
				continue
			}
			key := split[0]

			if len(split) == 3 {
				key = split[0] + "." + split[1]
			}

			if configData[key] == nil {
				configData[key] = make(map[string]interface{})
			}

			if val, ok := flag.Value.(*BigIntValue); ok {
				configData[key][split[len(split)-1]] = val.String()
			} else {
				configData[key][split[len(split)-1]] = flag.Value
			}
		}
	}
}

// Write the default values of each flag to a file
func WriteDefaultConfigFile(configDir string, configFileName string, configType string) error {
	if configDir == "" {
		log.Global.Fatalf("No config file path provided")
	}

	// Check that dir exists, create if it doesn't
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		err := os.MkdirAll(configDir, 0755)
		if err != nil {
			log.Global.Fatalf("Failed to create config directory: %s", err)
		}
	}

	configPath := filepath.Join(configDir, configFileName)

	// Check if file exists, create if it doesn't
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		file, err := os.Create(configPath)
		if err != nil {
			log.Global.Fatalf("Failed to create config file: %s", err)
		}
		file.Close()
	}

	// Open the file
	f, err := os.OpenFile(configPath, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Global.Fatalf("Failed to open config file: %s", err)
	}
	defer f.Close()

	addFlagsToCategory(Flags)

	// Remove bootpeers data from the configData to be written.
	delete(configData["node"], "bootpeers")

	var output []byte
	var marshalErr error

	// Marshal data into the specified format
	switch strings.ToLower(configType) {
	case "json":
		output, marshalErr = json.MarshalIndent(configData, "", "  ")
	case "toml":
		output, marshalErr = toml.Marshal(configData)
	case "yaml":
		output, marshalErr = yaml.Marshal(configData)
	default:
		log.Global.Fatalf("Unsupported config type: %s", configType)
	}

	if marshalErr != nil {
		log.Global.Fatalf("Failed to marshal config data: %s", marshalErr)
	}

	// Write to the file
	if _, err := f.Write(output); err != nil {
		log.Global.Fatalf("Failed to write to config file: %s", err)
	}

	return nil
}
