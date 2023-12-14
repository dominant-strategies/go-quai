package utils

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/dominant-strategies/go-quai/common/constants"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var GlobalFlags = []Flag{
	ConfigDirFlag,
	DataDirFlag,
	AncientDirFlag,
	LogLevelFlag,
	SaveConfigFlag,
}

var NodeFlags = []Flag{
	IPAddrFlag,
	P2PPortFlag,
	BootNodeFlag,
	BootPeersFlag,
	PortMapFlag,
	KeyFileFlag,
	MinPeersFlag,
	MaxPeersFlag,
	LocationFlag,
	SoloFlag,
	CoinbaseAddressFlag,
	DBEngineFlag,
	NetworkIdFlag,
	SlicesRunningFlag,
	ColosseumFlag,
	GardenFlag,
	OrchardFlag,
	LighthouseFlag,
	LocalFlag,
	GenesisNonceFlag,
	DeveloperFlag,
	DevPeriodFlag,
	IdentityFlag,
	DocRootFlag,
	GCModeFlag,
	SnapshotFlag,
	TxLookupLimitFlag,
	WhitelistFlag,
	BloomFilterSizeFlag,
	TxPoolLocalsFlag,
	TxPoolNoLocalsFlag,
	TxPoolJournalFlag,
	TxPoolRejournalFlag,
	TxPoolPriceLimitFlag,
	TxPoolPriceBumpFlag,
	TxPoolAccountSlotsFlag,
	TxPoolGlobalSlotsFlag,
	TxPoolAccountQueueFlag,
	TxPoolGlobalQueueFlag,
	TxPoolLifetimeFlag,
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
	ExternalSignerFlag,
	VMEnableDebugFlag,
	InsecureUnlockAllowedFlag,
	GpoBlocksFlag,
	GpoPercentileFlag,
	GpoMaxGasPriceFlag,
	GpoIgnoreGasPriceFlag,
	DomUrl,
	SubUrls,
}

var RPCFlags = []Flag{
	HTTPEnabledFlag,
	HTTPListenAddrFlag,
	HTTPCORSDomainFlag,
	HTTPVirtualHostsFlag,
	HTTPApiFlag,
	HTTPPathPrefixFlag,
	WSEnabledFlag,
	WSListenAddrFlag,
	WSApiFlag,
	WSAllowedOriginsFlag,
	WSPathPrefixFlag,
	PreloadJSFlag,
	RPCGlobalGasCapFlag,
	QuaiStatsURLFlag,
	SendFullStatsFlag,
}

var (
	// ****************************************
	// **                                    **
	// **         LOCAL FLAGS                **
	// **                                    **
	// ****************************************
	IPAddrFlag = Flag{
		Name:         "ipaddr",
		Abbreviation: "i",
		Value:        "0.0.0.0",
		Usage:        "ip address to listen on" + generateEnvDoc("ipaddr"),
	}

	P2PPortFlag = Flag{
		Name:         "port",
		Abbreviation: "p",
		Value:        "4001",
		Usage:        "p2p port to listen on" + generateEnvDoc("port"),
	}

	BootNodeFlag = Flag{
		Name:         "bootnode",
		Abbreviation: "b",
		Value:        false,
		Usage:        "start the node as a boot node (no static peers required)" + generateEnvDoc("bootnode"),
	}

	BootPeersFlag = Flag{
		Name:  "bootpeers",
		Value: []string{},
		Usage: "list of bootstrap peers. Syntax: <multiaddress1>,<multiaddress2>,..." + generateEnvDoc("bootpeers"),
	}

	PortMapFlag = Flag{
		Name:  "portmap",
		Value: true,
		Usage: "enable NAT portmap" + generateEnvDoc("portmap"),
	}

	KeyFileFlag = Flag{
		Name:         "private.key",
		Abbreviation: "k",
		Value:        "",
		Usage:        "file containing node private key" + generateEnvDoc("keyfile"),
	}

	MinPeersFlag = Flag{
		Name:  "min-peers",
		Value: "5",
		Usage: "minimum number of peers to maintain connectivity with" + generateEnvDoc("min-peers"),
	}

	MaxPeersFlag = Flag{
		Name:  "max-peers",
		Value: "50",
		Usage: "maximum number of peers to maintain connectivity with" + generateEnvDoc("max-peers"),
	}

	LocationFlag = Flag{
		Name:  "location",
		Value: "",
		Usage: "region and zone location" + generateEnvDoc("location"),
	}

	SoloFlag = Flag{
		Name:         "solo",
		Abbreviation: "s",
		Value:        false,
		Usage:        "start the node as a solo node (will not reach out to bootstrap peers)" + generateEnvDoc("solo"),
	}

	// ****************************************
	// **                                    **
	// **         GLOBAL FLAGS               **
	// **                                    **
	// ****************************************
	ConfigDirFlag = Flag{
		Name:         "config-dir",
		Abbreviation: "c",
		Value:        xdg.ConfigHome + "/" + constants.APP_NAME + "/",
		Usage:        "config directory" + generateEnvDoc("config-dir"),
	}

	DataDirFlag = Flag{
		Name:         "data-dir",
		Abbreviation: "d",
		Value:        xdg.DataHome + "/" + constants.APP_NAME + "/",
		Usage:        "data directory" + generateEnvDoc("data-dir"),
	}

	AncientDirFlag = Flag{
		Name:  "datadir.ancient",
		Value: "",
		Usage: "Data directory for ancient chain segments (default = inside chaindata)" + generateEnvDoc("datadir.ancient"),
	}

	LogLevelFlag = Flag{
		Name:         "log-level",
		Abbreviation: "l",
		Value:        "info",
		Usage:        "log level (trace, debug, info, warn, error, fatal, panic)" + generateEnvDoc("log-level"),
	}

	SaveConfigFlag = Flag{
		Name:         "save-config",
		Abbreviation: "S",
		Value:        false,
		Usage:        "save/update config file with current config parameters" + generateEnvDoc("save-config"),
	}
	// ****************************************
	// ** 								     **
	// ** 	      IMPORTED FLAGS    		 **
	// ** 								     **
	// ****************************************
	DBEngineFlag = Flag{
		Name:  "db.engine",
		Value: "leveldb",
		Usage: "Backing database implementation to use ('leveldb' or 'pebble')" + generateEnvDoc("db.engine"),
	}

	NetworkIdFlag = Flag{
		Name:  "networkid",
		Value: 1,
		Usage: "Explicitly set network id (integer)(For testnets: use --garden)" + generateEnvDoc("networkid"),
	}

	SlicesRunningFlag = Flag{
		Name:  "slices",
		Value: "",
		Usage: "All the slices that are running on this node" + generateEnvDoc("slices"),
	}

	ColosseumFlag = Flag{
		Name:  "colosseum",
		Value: false,
		Usage: "Quai Colosseum testnet" + generateEnvDoc("colosseum"),
	}

	GardenFlag = Flag{
		Name:  "garden",
		Value: false,
		Usage: "Garden network: pre-configured proof-of-work test network" + generateEnvDoc("garden"),
	}
	OrchardFlag = Flag{
		Name:  "orchard",
		Value: false,
		Usage: "Orchard network: pre-configured proof-of-work test network" + generateEnvDoc("orchard"),
	}
	LighthouseFlag = Flag{
		Name:  "lighthouse",
		Value: false,
		Usage: "Lighthouse network: pre-configured proof-of-work test network" + generateEnvDoc("lighthouse"),
	}
	LocalFlag = Flag{
		Name:  "local",
		Value: false,
		Usage: "Local network: localhost proof-of-work node, will not attempt to connect to bootnode or any public network" + generateEnvDoc("local"),
	}
	GenesisNonceFlag = Flag{
		Name:  "nonce",
		Value: 0,
		Usage: "Nonce to use for the genesis block (integer)" + generateEnvDoc("nonce"),
	}
	DeveloperFlag = Flag{
		Name:  "dev",
		Value: false,
		Usage: "Ephemeral proof-of-authority network with a pre-funded developer account, mining enabled" + generateEnvDoc("dev"),
	}
	DevPeriodFlag = Flag{
		Name:  "dev.period",
		Value: 0,
		Usage: "Block period to use for the dev network (integer) (0 = mine only if transaction pending)" + generateEnvDoc("dev.period"),
	}
	IdentityFlag = Flag{
		Name:  "identity",
		Value: "",
		Usage: "Custom node name" + generateEnvDoc("identity"),
	}
	DocRootFlag = Flag{
		Name:  "docroot",
		Value: xdg.DataHome,
		Usage: "Document Root for HTTPClient file scheme" + generateEnvDoc("docroot"),
	}

	// ****************************************
	// ** 								     **
	// ** 	      PY FLAGS    				 **
	// ** 								     **
	// ****************************************
	GCModeFlag = Flag{
		Name:  "gcmode",
		Value: "full",
		Usage: `Blockchain garbage collection mode ("full", "archive")` + generateEnvDoc("gcmode"),
	}

	SnapshotFlag = Flag{
		Name:  "snapshot",
		Value: true,
		Usage: `Enables snapshot-database mode (default = true)` + generateEnvDoc("snapshot"),
	}

	TxLookupLimitFlag = Flag{
		Name:  "txlookuplimit",
		Usage: "Number of recent blocks to maintain transactions index for (default = about one year, 0 = entire chain)" + generateEnvDoc("txlookuplimit"),
	}

	WhitelistFlag = Flag{
		Name:  "whitelist",
		Value: "",
		Usage: "Comma separated block number-to-hash mappings to enforce (<number>=<hash>)" + generateEnvDoc("whitelist"),
	}

	BloomFilterSizeFlag = Flag{
		Name:  "bloomfilter.size",
		Value: 2048,
		Usage: "Megabytes of memory allocated to bloom-filter for pruning" + generateEnvDoc("bloomfilter.size"),
	}
	// Transaction pool settings
	TxPoolLocalsFlag = Flag{
		Name:  "txpool.locals",
		Value: "",
		Usage: "Comma separated accounts to treat as locals (no flush, priority inclusion)" + generateEnvDoc("txpool.locals"),
	}
	TxPoolNoLocalsFlag = Flag{
		Name:  "txpool.nolocals",
		Value: false,
		Usage: "Disables price exemptions for locally submitted transactions" + generateEnvDoc("txpool.nolocals"),
	}
	TxPoolJournalFlag = Flag{
		Name:  "txpool.journal",
		Usage: "Disk journal for local transaction to survive node restarts" + generateEnvDoc("txpool.journal"),
	}
	TxPoolRejournalFlag = Flag{
		Name:  "txpool.rejournal",
		Usage: "Time interval to regenerate the local transaction journal" + generateEnvDoc("txpool.rejournal"),
	}
	TxPoolPriceLimitFlag = Flag{
		Name:  "txpool.pricelimit",
		Usage: "Minimum gas price limit to enforce for acceptance into the pool" + generateEnvDoc("txpool.pricelimit"),
	}
	TxPoolPriceBumpFlag = Flag{
		Name:  "txpool.pricebump",
		Usage: "Price bump percentage to replace an already existing transaction" + generateEnvDoc("txpool.pricebump"),
	}
	TxPoolAccountSlotsFlag = Flag{
		Name:  "txpool.accountslots",
		Usage: "Minimum number of executable transaction slots guaranteed per account" + generateEnvDoc("txpool.accountslots"),
	}
	TxPoolGlobalSlotsFlag = Flag{
		Name:  "txpool.globalslots",
		Usage: "Maximum number of executable transaction slots for all accounts" + generateEnvDoc("txpool.globalslots"),
	}
	TxPoolAccountQueueFlag = Flag{
		Name:  "txpool.accountqueue",
		Usage: "Maximum number of non-executable transaction slots permitted per account" + generateEnvDoc("txpool.accountqueue"),
	}
	TxPoolGlobalQueueFlag = Flag{
		Name:  "txpool.globalqueue",
		Usage: "Maximum number of non-executable transaction slots for all accounts" + generateEnvDoc("txpool.globalqueue"),
	}
	TxPoolLifetimeFlag = Flag{
		Name:  "txpool.lifetime",
		Usage: "Maximum amount of time non-executable transaction are queued" + generateEnvDoc("txpool.lifetime"),
	}
	CacheFlag = Flag{
		Name:  "cache",
		Value: 1024,
		Usage: "Megabytes of memory allocated to internal caching (default = 4096 quai full node, 128 light mode)" + generateEnvDoc("cache"),
	}
	CacheDatabaseFlag = Flag{
		Name:  "cache.database",
		Value: 50,
		Usage: "Percentage of cache memory allowance to use for database io" + generateEnvDoc("cache.database"),
	}
	CacheTrieFlag = Flag{
		Name:  "cache.trie",
		Value: 15,
		Usage: "Percentage of cache memory allowance to use for trie caching (default = 15% full mode, 30% archive mode)" + generateEnvDoc("cache.trie"),
	}
	CacheTrieJournalFlag = Flag{
		Name:  "cache.trie.journal",
		Usage: "Disk journal directory for trie cache to survive node restarts" + generateEnvDoc("cache.trie.journal"),
	}
	CacheTrieRejournalFlag = Flag{
		Name:  "cache.trie.rejournal",
		Usage: "Time interval to regenerate the trie cache journal" + generateEnvDoc("cache.trie.rejournal"),
	}
	CacheGCFlag = Flag{
		Name:  "cache.gc",
		Value: 25,
		Usage: "Percentage of cache memory allowance to use for trie pruning (default = 25% full mode, 0% archive mode)" + generateEnvDoc("cache.gc"),
	}
	CacheSnapshotFlag = Flag{
		Name:  "cache.snapshot",
		Value: 10,
		Usage: "Percentage of cache memory allowance to use for snapshot caching (default = 10% full mode, 20% archive mode)" + generateEnvDoc("cache.snapshot"),
	}
	CacheNoPrefetchFlag = Flag{
		Name:  "cache.noprefetch",
		Value: false,
		Usage: "Disable heuristic state prefetch during block import (less CPU and disk IO, more time waiting for data)" + generateEnvDoc("cache.noprefetch"),
	}
	CachePreimagesFlag = Flag{
		Name:  "cache.preimages",
		Value: false,
		Usage: "Enable recording the SHA3/keccak preimages of trie keys" + generateEnvDoc("cache.preimages"),
	}
	// Consensus settings
	ConsensusEngineFlag = Flag{
		Name:  "consensus.engine",
		Value: "progpow",
		Usage: "Consensus engine that the blockchain will run and verify blocks using" + generateEnvDoc("consensus.engine"),
	}
	// Miner settings
	MinerGasPriceFlag = Flag{
		Name:  "miner.gasprice",
		Usage: "Minimum gas price for mining a transaction" + generateEnvDoc("miner.gasprice"),
	}
	// Account settings
	UnlockedAccountFlag = Flag{
		Name:  "unlock",
		Value: "",
		Usage: "Comma separated list of accounts to unlock" + generateEnvDoc("unlock"),
	}

	PasswordFileFlag = Flag{
		Name:  "password",
		Value: "",
		Usage: "Password file to use for non-interactive password input" + generateEnvDoc("password"),
	}

	ExternalSignerFlag = Flag{
		Name:  "signer",
		Value: "",
		Usage: "External signer (url or path to ipc file)" + generateEnvDoc("signer"),
	}

	VMEnableDebugFlag = Flag{
		Name:  "vmdebug",
		Value: false,
		Usage: "Record information useful for VM and contract debugging" + generateEnvDoc("vmdebug"),
	}
	InsecureUnlockAllowedFlag = Flag{
		Name:  "allow-insecure-unlock",
		Value: false,
		Usage: "Allow insecure account unlocking when account-related RPCs are exposed by http" + generateEnvDoc("allow-insecure-unlock"),
	}
	RPCGlobalTxFeeCapFlag = Flag{
		Name:  "rpc.txfeecap",
		Usage: "Sets a cap on transaction fee (in ether) that can be sent via the RPC APIs (0 = no cap)",
	}
	RPCGlobalGasCapFlag = Flag{
		Name:  "rpc.gascap",
		Usage: "Sets a cap on gas that can be used in eth_call/estimateGas (0=infinite)" + generateEnvDoc("vmdebug"),
	}
	QuaiStatsURLFlag = Flag{
		Name:  "quaistats",
		Value: "",
		Usage: "Reporting URL of a quaistats service (nodename:secret@host:port)" + generateEnvDoc("quaistats"),
	}
	SendFullStatsFlag = Flag{
		Name:  "sendfullstats",
		Value: false,
		Usage: "Send full stats boolean flag for quaistats" + generateEnvDoc("sendfullstats"),
	}
	// RPC settings
	HTTPEnabledFlag = Flag{
		Name:  "http",
		Value: false,
		Usage: "Enable the HTTP-RPC server" + generateEnvDoc("http"),
	}
	HTTPListenAddrFlag = Flag{
		Name:  "http.addr",
		Usage: "HTTP-RPC server listening interface" + generateEnvDoc("http.addr"),
	}
	HTTPCORSDomainFlag = Flag{
		Name:  "http.corsdomain",
		Value: "",
		Usage: "Comma separated list of domains from which to accept cross origin requests (browser enforced)" + generateEnvDoc("http.corsdomain"),
	}
	HTTPVirtualHostsFlag = Flag{
		Name:  "http.vhosts",
		Usage: "Comma separated list of virtual hostnames from which to accept requests (server enforced). Accepts '*' wildcard." + generateEnvDoc("http"),
	}
	HTTPApiFlag = Flag{
		Name:  "http.api",
		Value: "",
		Usage: "API's offered over the HTTP-RPC interface" + generateEnvDoc("http"),
	}
	HTTPPathPrefixFlag = Flag{
		Name:  "http.rpcprefix",
		Value: "",
		Usage: "HTTP path path prefix on which JSON-RPC is served. Use '/' to serve on all paths." + generateEnvDoc("http"),
	}

	WSEnabledFlag = Flag{
		Name:  "ws",
		Value: false,
		Usage: "Enable the WS-RPC server" + generateEnvDoc("ws"),
	}
	WSListenAddrFlag = Flag{
		Name:  "ws.addr",
		Usage: "WS-RPC server listening interface" + generateEnvDoc("ws"),
	}
	WSApiFlag = Flag{
		Name:  "ws.api",
		Value: "",
		Usage: "API's offered over the WS-RPC interface" + generateEnvDoc("ws"),
	}
	WSAllowedOriginsFlag = Flag{
		Name:  "ws.origins",
		Value: "",
		Usage: "Origins from which to accept websockets requests" + generateEnvDoc("ws"),
	}
	WSPathPrefixFlag = Flag{
		Name:  "ws.rpcprefix",
		Value: "",
		Usage: "HTTP path prefix on which JSON-RPC is served. Use '/' to serve on all paths." + generateEnvDoc("ws"),
	}
	PreloadJSFlag = Flag{
		Name:  "preload",
		Value: "",
		Usage: "Comma separated list of JavaScript files to preload into the console" + generateEnvDoc("preload"),
	}
	// Gas price oracle settings
	GpoBlocksFlag = Flag{
		Name:  "gpo.blocks",
		Usage: "Number of recent blocks to check for gas prices" + generateEnvDoc("gpo.blocks"),
	}
	GpoPercentileFlag = Flag{
		Name:  "gpo.percentile",
		Usage: "Suggested gas price is the given percentile of a set of recent transaction gas prices" + generateEnvDoc("gpo.percentile"),
	}
	GpoMaxGasPriceFlag = Flag{
		Name:  "gpo.maxprice",
		Usage: "Maximum gas price will be recommended by gpo" + generateEnvDoc("gpo.maxprice"),
	}
	GpoIgnoreGasPriceFlag = Flag{
		Name:  "gpo.ignoreprice",
		Usage: "Gas price below which gpo will ignore transactions" + generateEnvDoc("gpo.ignoreprice"),
	}

	DomUrl = Flag{
		Name:  "dom.url",
		Usage: "Dominant chain websocket url" + generateEnvDoc("dom.url"),
	}
	SubUrls = Flag{
		Name:  "sub.urls",
		Usage: "Subordinate chain websocket urls" + generateEnvDoc("sub.urls"),
	}
	CoinbaseAddressFlag = Flag{
		Name:  "coinbases",
		Value: "",
		Usage: "Input TOML string or path to TOML file" + generateEnvDoc("coinbase"),
	}
)

/*
ParseCoinbaseAddresses parses the coinbase addresses from different sources based on the user input.
It handles three scenarios:

 1. File Path Input:
    If the user specifies a file path, the function expects a TOML file containing the coinbase addresses.
    The file should have a 'coinbases' section with shard-address mappings.
    Example:
    Command: --coinbases /path/to/coinbases.toml
    TOML Content:
    [coinbases]
    "00" = "0xAddress00"
    "01" = "0xAddress01"
    ...

 2. Direct String Input:
    If the user doesn't specify a file path but provides a string, the function expects a string in the format 'shard=address'.
    Each shard-address pair should be separated by a comma.
    Example:
    Command: --coinbases "00=0xAddress00,01=0xAddress01"

 3. Default Config File:
    If no input is provided for the coinbase flag, the function falls back to the config file specified by the 'config-dir' flag.
    Example:
    In Config TOML:
    ...
    [coinbases]
    "00" = "0xDefaultAddress00"
    "01" = "0xDefaultAddress01"
    ...

The function reads the coinbase addresses and performs necessary validation as per the above scenarios.
*/
func ParseCoinbaseAddresses() (map[string]string, error) {
	coinbaseInput := viper.GetString("coinbases")
	coinbaseConfig := struct {
		Coinbases map[string]string `toml:"coinbases"`
	}{
		Coinbases: make(map[string]string),
	}

	// Scenario 1: User specifies a file path
	if _, err := os.Stat(coinbaseInput); err == nil {
		fileContent, err := os.ReadFile(coinbaseInput)
		if err != nil {
			log.Fatalf("Failed to read coinbase file: %s", err)
			return nil, err
		}
		if err := toml.Unmarshal(fileContent, &coinbaseConfig); err != nil {
			log.Fatalf("Invalid TOML in coinbase file: %s", err)
			return nil, err
		}
	} else if coinbaseInput != "" {
		// Scenario 2: User provides direct string input
		for _, coinbase := range strings.Split(coinbaseInput, ",") {
			parts := strings.Split(strings.TrimSpace(coinbase), "=")
			if len(parts) == 2 {
				coinbaseConfig.Coinbases[parts[0]] = parts[1]
			} else {
				log.Fatalf("Invalid coinbase format: %s", coinbase)
				return nil, fmt.Errorf("invalid coinbase format: %s", coinbase)
			}
		}
	} else {
		// Scenario 3: Fallback to default config file
		log.Info("No coinbases specified, falling back to node config file")
		defaultConfigPath := viper.GetString("config-dir") + "config.toml"
		if defaultConfigPath != "" {
			configFileContent, err := os.ReadFile(defaultConfigPath)
			if err != nil {
				log.Fatalf("Failed to read config file: %s", err)
				return nil, err
			}
			if err := toml.Unmarshal(configFileContent, &coinbaseConfig); err != nil {
				log.Fatalf("Invalid TOML in config file: %s", err)
				return nil, err
			}
		}
	}

	if len(coinbaseConfig.Coinbases) == 0 {
		log.Fatalf("No coinbase addresses provided")
		return nil, fmt.Errorf("no coinbase addresses provided")
	}

	// Validates all addresses in coinbaseMap
	for shard, address := range coinbaseConfig.Coinbases {
		if err := isValidAddress(address, shard); err != nil {
			return nil, err
		}
	}

	log.Infof("Coinbase Addresses: %v", coinbaseConfig.Coinbases)

	return coinbaseConfig.Coinbases, nil
}

func isValidAddress(address string, shard string) error {
	re := regexp.MustCompile(`^(0x)?[0-9a-fA-F]{40}$`)
	if !re.MatchString(address) {
		log.Fatalf("Invalid Ethereum address: %s", address)
		return fmt.Errorf("invalid Ethereum address: %s", address)
	}
	return nil
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
	case *TextMarshalerValue:
		cmd.PersistentFlags().VarP(val, flag.GetName(), flag.GetAbbreviation(), flag.GetUsage())
	case *BigIntValue:
		cmd.PersistentFlags().VarP(val, flag.GetName(), flag.GetAbbreviation(), flag.GetUsage())
	default:
		log.Error("Flag type not supported: " + flag.GetName() + ", " + fmt.Sprintf("%T", val))
	}
	viper.BindPFlag(flag.GetName(), cmd.PersistentFlags().Lookup(flag.GetName()))
}

// helper function that given a cobra flag name, returns the corresponding
// help legend for the equivalent environment variable
func generateEnvDoc(flag string) string {
	envVar := constants.ENV_PREFIX + "_" + strings.ReplaceAll(strings.ToUpper(flag), "-", "_")
	return fmt.Sprintf(" [%s]", envVar)
}
