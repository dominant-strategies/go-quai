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
}

// setBootstrapNodes creates a list of bootstrap nodes from the pre-configured
// ones if none have been specified.
func setBootstrapNodes(ctx *cli.Context, cfg *p2p.Config, nodeLocation common.Location) {
	urls := params.ColosseumBootnodes[nodeLocation.Name()]
	//TODO: fix bootnode parsing for other networks

	cfg.BootstrapNodes = make([]*enode.Node, 0, len(urls))
	for _, url := range urls {
		if url != "" {
			node, err := enode.Parse(enode.ValidSchemes, url+cfg.ListenAddr)
			if err != nil {
				log.Fatal("Bootstrap URL invalid", "enode", url, "err", err)
				continue
			}
			cfg.BootstrapNodes = append(cfg.BootstrapNodes, node)
		}
	}
}

		}
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

// makeSubUrls returns the subordinate chain urls
func makeSubUrls(ctx *cli.Context) []string {
	return strings.Split(ctx.GlobalString(SubUrls.Name), ",")
}

// setSlicesRunning sets the slices running flag
func setSlicesRunning(ctx *cli.Context, cfg *ethconfig.Config) {
	slices := strings.Split(ctx.GlobalString(SlicesRunningFlag.Name), ",")

	// Sanity checks
	if len(slices) == 0 {
		Fatalf("no slices are specified")
	}
	if len(slices) > common.NumRegionsInPrime*common.NumZonesInRegion {
		Fatalf("number of slices exceed the current ontology")
	}
	slicesRunning := []common.Location{}
	for _, slice := range slices {
		slicesRunning = append(slicesRunning, common.Location{slice[1] - 48, slice[3] - 48})
	}
	cfg.SlicesRunning = slicesRunning
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

// setEtherbase retrieves the etherbase either from the directly specified
// command line flags or from the keystore if CLI indexed.
func setEtherbase(ctx *cli.Context, cfg *ethconfig.Config) {
	// Extract the current etherbase
	var etherbase string
	if ctx.GlobalIsSet(MinerEtherbaseFlag.Name) {
		etherbase = ctx.GlobalString(MinerEtherbaseFlag.Name)
	}
	// Convert the etherbase into an address and configure it
	if etherbase != "" {
		account, err := HexAddress(etherbase, cfg.NodeLocation)
		if err != nil {
			Fatalf("Invalid miner etherbase: %v", err)
		}
		cfg.Miner.Etherbase = account
	}
}

// MakePasswordList reads password lines from the file specified by the global --password flag.
func MakePasswordList(ctx *cli.Context) []string {
	path := ctx.GlobalString(PasswordFileFlag.Name)
	if path == "" {
		return nil
	}
	text, err := ioutil.ReadFile(path)
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

func SetP2PConfig(ctx *cli.Context, cfg *p2p.Config, nodeLocation common.Location) {
	setNodeKey(ctx, cfg, nodeLocation)
	setNAT(ctx, cfg)
	setListenAddress(ctx, cfg)
	setBootstrapNodes(ctx, cfg, nodeLocation)
	setBootstrapNodesV5(ctx, cfg)

	if ctx.GlobalIsSet(MaxPeersFlag.Name) {
		cfg.MaxPeers = ctx.GlobalInt(MaxPeersFlag.Name)
	}
	ethPeers := cfg.MaxPeers

	log.Info("Maximum peer count", "ETH", ethPeers, "total", cfg.MaxPeers)

	if ctx.GlobalIsSet(MaxPendingPeersFlag.Name) {
		cfg.MaxPendingPeers = ctx.GlobalInt(MaxPendingPeersFlag.Name)
	}
	if ctx.GlobalIsSet(NoDiscoverFlag.Name) {
		cfg.NoDiscovery = true
	}

	// if we're running a light client or server, force enable the v5 peer discovery
	// unless it is explicitly disabled with --nodiscover note that explicitly specifying
	// --v5disc overrides --nodiscover, in which case the later only disables v4 discovery
	if ctx.GlobalIsSet(DiscoveryV5Flag.Name) {
		cfg.DiscoveryV5 = ctx.GlobalBool(DiscoveryV5Flag.Name)
	}

	if netrestrict := ctx.GlobalString(NetrestrictFlag.Name); netrestrict != "" {
		list, err := netutil.ParseNetlist(netrestrict)
		if err != nil {
			Fatalf("Option %q: %v", NetrestrictFlag.Name, err)
		}
		cfg.NetRestrict = list
	}

	if ctx.GlobalBool(DeveloperFlag.Name) {
		// --dev mode can't use p2p networking.
		cfg.MaxPeers = 0
		cfg.ListenAddr = ""
		cfg.NoDial = true
		cfg.NoDiscovery = true
		cfg.DiscoveryV5 = false
	}
}

// SetNodeConfig applies node-related command line flags to the config.
func SetNodeConfig(ctx *cli.Context, cfg *node.Config) {
	SetP2PConfig(ctx, &cfg.P2P, cfg.NodeLocation)
	setHTTP(ctx, cfg)
	setWS(ctx, cfg)
	setNodeUserIdent(ctx, cfg)
	setDataDir(ctx, cfg)

	if ctx.GlobalIsSet(ExternalSignerFlag.Name) {
		cfg.ExternalSigner = ctx.GlobalString(ExternalSignerFlag.Name)
	}

	if ctx.GlobalIsSet(KeyStoreDirFlag.Name) {
		cfg.KeyStoreDir = ctx.GlobalString(KeyStoreDirFlag.Name)
	}
	if ctx.GlobalIsSet(DeveloperFlag.Name) {
		cfg.UseLightweightKDF = true
	}
	if ctx.GlobalIsSet(NoUSBFlag.Name) || cfg.NoUSB {
		log.Warn("Option nousb is deprecated and USB is deactivated by default. Use --usb to enable")
	}
	if ctx.GlobalIsSet(USBFlag.Name) {
		cfg.USB = ctx.GlobalBool(USBFlag.Name)
	}
	if ctx.GlobalIsSet(InsecureUnlockAllowedFlag.Name) {
		cfg.InsecureUnlockAllowed = ctx.GlobalBool(InsecureUnlockAllowedFlag.Name)
	}
	if ctx.IsSet(DBEngineFlag.Name) {
		dbEngine := ctx.String(DBEngineFlag.Name)
		if dbEngine != "leveldb" && dbEngine != "pebble" {
			Fatalf("Invalid choice for db.engine '%s', allowed 'leveldb' or 'pebble'", dbEngine)
		}
		log.Info(fmt.Sprintf("Using %s as db engine", dbEngine))
		cfg.DBEngine = dbEngine
	}
}

func setDataDir(ctx *cli.Context, cfg *node.Config) {
	switch {
	case ctx.GlobalIsSet(DataDirFlag.Name):
		cfg.DataDir = ctx.GlobalString(DataDirFlag.Name)
	case ctx.GlobalBool(DeveloperFlag.Name):
		cfg.DataDir = "" // unless explicitly requested, use memory databases
	case ctx.GlobalBool(GardenFlag.Name) && cfg.DataDir == node.DefaultDataDir():
		cfg.DataDir = filepath.Join(node.DefaultDataDir(), "garden")
	case ctx.GlobalBool(OrchardFlag.Name) && cfg.DataDir == node.DefaultDataDir():
		cfg.DataDir = filepath.Join(node.DefaultDataDir(), "orchard")
	case ctx.GlobalBool(LighthouseFlag.Name) && cfg.DataDir == node.DefaultDataDir():
		cfg.DataDir = filepath.Join(node.DefaultDataDir(), "lighthouse")
	case ctx.GlobalBool(LocalFlag.Name) && cfg.DataDir == node.DefaultDataDir():
		cfg.DataDir = filepath.Join(node.DefaultDataDir(), "local")
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

func setGPO(ctx *cli.Context, cfg *gasprice.Config, light bool) {
	// If we are running the light client, apply another group
	// settings for gas oracle.
	if light {
		*cfg = ethconfig.LightClientGPO
	}
	if ctx.GlobalIsSet(GpoBlocksFlag.Name) {
		cfg.Blocks = ctx.GlobalInt(GpoBlocksFlag.Name)
	}
	if ctx.GlobalIsSet(GpoPercentileFlag.Name) {
		cfg.Percentile = ctx.GlobalInt(GpoPercentileFlag.Name)
	}
	if ctx.GlobalIsSet(GpoMaxGasPriceFlag.Name) {
		cfg.MaxPrice = big.NewInt(ctx.GlobalInt64(GpoMaxGasPriceFlag.Name))
	}
	if ctx.GlobalIsSet(GpoIgnoreGasPriceFlag.Name) {
		cfg.IgnorePrice = big.NewInt(ctx.GlobalInt64(GpoIgnoreGasPriceFlag.Name))
	}
}

func setTxPool(ctx *cli.Context, cfg *core.TxPoolConfig, nodeLocation common.Location) {
	if ctx.GlobalIsSet(TxPoolLocalsFlag.Name) {
		locals := strings.Split(ctx.GlobalString(TxPoolLocalsFlag.Name), ",")
		for _, account := range locals {
			if trimmed := strings.TrimSpace(account); !common.IsHexAddress(trimmed) {
				Fatalf("Invalid account in --txpool.locals: %s", trimmed)
			} else {
				internal, err := common.HexToAddress(account, nodeLocation).InternalAddress()
				if err != nil {
					Fatalf("Invalid account in --txpool.locals: %s", account)
				}
				cfg.Locals = append(cfg.Locals, internal)
			}
		}
	}
	if ctx.GlobalIsSet(TxPoolNoLocalsFlag.Name) {
		cfg.NoLocals = ctx.GlobalBool(TxPoolNoLocalsFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolJournalFlag.Name) {
		cfg.Journal = ctx.GlobalString(TxPoolJournalFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolRejournalFlag.Name) {
		cfg.Rejournal = ctx.GlobalDuration(TxPoolRejournalFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolPriceLimitFlag.Name) {
		cfg.PriceLimit = ctx.GlobalUint64(TxPoolPriceLimitFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolPriceBumpFlag.Name) {
		cfg.PriceBump = ctx.GlobalUint64(TxPoolPriceBumpFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolAccountSlotsFlag.Name) {
		cfg.AccountSlots = ctx.GlobalUint64(TxPoolAccountSlotsFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolGlobalSlotsFlag.Name) {
		cfg.GlobalSlots = ctx.GlobalUint64(TxPoolGlobalSlotsFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolAccountQueueFlag.Name) {
		cfg.AccountQueue = ctx.GlobalUint64(TxPoolAccountQueueFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolGlobalQueueFlag.Name) {
		cfg.GlobalQueue = ctx.GlobalUint64(TxPoolGlobalQueueFlag.Name)
	}
	if ctx.GlobalIsSet(TxPoolLifetimeFlag.Name) {
		cfg.Lifetime = ctx.GlobalDuration(TxPoolLifetimeFlag.Name)
	}
}

func setConsensusEngineConfig(ctx *cli.Context, cfg *ethconfig.Config) {
	if cfg.ConsensusEngine == "blake3" {
		// Override any default configs for hard coded networks.
		switch {
		case ctx.GlobalBool(ColosseumFlag.Name):
			cfg.Blake3Pow.DurationLimit = params.DurationLimit
			cfg.Blake3Pow.GasCeil = params.ColosseumGasCeil
			cfg.Blake3Pow.MinDifficulty = new(big.Int).Div(core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(GardenFlag.Name):
			cfg.Blake3Pow.DurationLimit = params.GardenDurationLimit
			cfg.Blake3Pow.GasCeil = params.GardenGasCeil
			cfg.Blake3Pow.MinDifficulty = new(big.Int).Div(core.DefaultGardenGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(OrchardFlag.Name):
			cfg.Blake3Pow.DurationLimit = params.OrchardDurationLimit
			cfg.Blake3Pow.GasCeil = params.OrchardGasCeil
			cfg.Blake3Pow.MinDifficulty = new(big.Int).Div(core.DefaultOrchardGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(LighthouseFlag.Name):
			cfg.Blake3Pow.DurationLimit = params.LighthouseDurationLimit
			cfg.Blake3Pow.GasCeil = params.LighthouseGasCeil
			cfg.Blake3Pow.MinDifficulty = new(big.Int).Div(core.DefaultLighthouseGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(LocalFlag.Name):
			cfg.Blake3Pow.DurationLimit = params.LocalDurationLimit
			cfg.Blake3Pow.GasCeil = params.LocalGasCeil
			cfg.Blake3Pow.MinDifficulty = new(big.Int).Div(core.DefaultLocalGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(DeveloperFlag.Name):
			cfg.Blake3Pow.DurationLimit = params.DurationLimit
			cfg.Blake3Pow.GasCeil = params.LocalGasCeil
			cfg.Blake3Pow.MinDifficulty = new(big.Int).Div(core.DefaultLocalGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		default:
			cfg.Blake3Pow.DurationLimit = params.DurationLimit
			cfg.Blake3Pow.GasCeil = params.GasCeil
			cfg.Blake3Pow.MinDifficulty = new(big.Int).Div(core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)

		}
	} else {
		// Override any default configs for hard coded networks.
		switch {
		case ctx.GlobalBool(ColosseumFlag.Name):
			cfg.Progpow.DurationLimit = params.DurationLimit
			cfg.Progpow.GasCeil = params.ColosseumGasCeil
			cfg.Progpow.MinDifficulty = new(big.Int).Div(core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(GardenFlag.Name):
			cfg.Progpow.DurationLimit = params.GardenDurationLimit
			cfg.Progpow.GasCeil = params.GardenGasCeil
			cfg.Progpow.MinDifficulty = new(big.Int).Div(core.DefaultGardenGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(OrchardFlag.Name):
			cfg.Progpow.DurationLimit = params.OrchardDurationLimit
			cfg.Progpow.GasCeil = params.OrchardGasCeil
			cfg.Progpow.GasCeil = params.ColosseumGasCeil
			cfg.Progpow.MinDifficulty = new(big.Int).Div(core.DefaultOrchardGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(LighthouseFlag.Name):
			cfg.Progpow.DurationLimit = params.LighthouseDurationLimit
			cfg.Progpow.GasCeil = params.LighthouseGasCeil
			cfg.Progpow.MinDifficulty = new(big.Int).Div(core.DefaultLighthouseGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(LocalFlag.Name):
			cfg.Progpow.DurationLimit = params.LocalDurationLimit
			cfg.Progpow.GasCeil = params.LocalGasCeil
			cfg.Progpow.MinDifficulty = new(big.Int).Div(core.DefaultLocalGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		case ctx.GlobalBool(DeveloperFlag.Name):
			cfg.Progpow.DurationLimit = params.DurationLimit
			cfg.Progpow.GasCeil = params.LocalGasCeil
			cfg.Progpow.MinDifficulty = new(big.Int).Div(core.DefaultLocalGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)
		default:
			cfg.Progpow.DurationLimit = params.DurationLimit
			cfg.Progpow.GasCeil = params.GasCeil
			cfg.Progpow.MinDifficulty = new(big.Int).Div(core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine).Difficulty, common.Big2)

		}
	}
}

func setWhitelist(ctx *cli.Context, cfg *ethconfig.Config) {
	whitelist := ctx.GlobalString(WhitelistFlag.Name)
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
func CheckExclusive(ctx *cli.Context, args ...interface{}) {
	set := make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		// Make sure the next argument is a flag and skip if not set
		flag, ok := args[i].(cli.Flag)
		if !ok {
			panic(fmt.Sprintf("invalid argument, not cli.Flag type: %T", args[i]))
		}
		// Check if next arg extends current and expand its name if so
		name := flag.GetName()

		if i+1 < len(args) {
			switch option := args[i+1].(type) {
			case string:
				// Extended flag check, make sure value set doesn't conflict with passed in option
				if ctx.GlobalString(flag.GetName()) == option {
					name += "=" + option
					set = append(set, "--"+name)
				}
				// shift arguments and continue
				i++
				continue

			case cli.Flag:
			default:
				panic(fmt.Sprintf("invalid argument, not cli.Flag or string extension: %T", args[i+1]))
			}
		}
		// Mark the flag if it's set
		if ctx.GlobalIsSet(flag.GetName()) {
			set = append(set, "--"+name)
		}
	}
	if len(set) > 1 {
		Fatalf("Flags %v can't be used at the same time", strings.Join(set, ", "))
	}
}

func GetNodeLocation(ctx *cli.Context) common.Location {
	// Configure global NodeLocation
	if !ctx.GlobalIsSet(RegionFlag.Name) && ctx.GlobalIsSet(ZoneFlag.Name) {
		log.Fatal("zone idx given, but missing region idx!")
	}
	nodeLocation := common.Location{}
	if ctx.GlobalIsSet(RegionFlag.Name) {
		region := ctx.GlobalInt(RegionFlag.Name)
		nodeLocation = append(nodeLocation, byte(region))
	}
	if ctx.GlobalIsSet(ZoneFlag.Name) {
		zone := ctx.GlobalInt(ZoneFlag.Name)
		nodeLocation = append(nodeLocation, byte(zone))
	}
	return nodeLocation
}

// SetEthConfig applies eth-related command line flags to the config.
func SetEthConfig(ctx *cli.Context, stack *node.Node, cfg *ethconfig.Config, nodeLocation common.Location) {
	// Avoid conflicting network flags
	CheckExclusive(ctx, ColosseumFlag, DeveloperFlag, GardenFlag, OrchardFlag, LocalFlag, LighthouseFlag)
	CheckExclusive(ctx, DeveloperFlag, ExternalSignerFlag) // Can't use both ephemeral unlocked and external signer

	if ctx.GlobalString(GCModeFlag.Name) == "archive" && ctx.GlobalUint64(TxLookupLimitFlag.Name) != 0 {
		ctx.GlobalSet(TxLookupLimitFlag.Name, "0")
		log.Warn("Disable transaction unindexing for archive node")
	}

	cfg.NodeLocation = nodeLocation
	// only set etherbase if its a zone chain
	if ctx.GlobalIsSet(RegionFlag.Name) && ctx.GlobalIsSet(ZoneFlag.Name) {
		setEtherbase(ctx, cfg)
	}
	setGPO(ctx, &cfg.GPO, ctx.GlobalString(SyncModeFlag.Name) == "light")
	setTxPool(ctx, &cfg.TxPool, nodeLocation)

	// If blake3 consensus engine is specifically asked use the blake3 engine
	if ctx.GlobalString(ConsensusEngineFlag.Name) == "blake3" {
		cfg.ConsensusEngine = "blake3"
	} else {
		cfg.ConsensusEngine = "progpow"
	}
	setConsensusEngineConfig(ctx, cfg)

	setWhitelist(ctx, cfg)

	// set the dominant chain websocket url
	setDomUrl(ctx, cfg)

	// set the subordinate chain websocket urls
	setSubUrls(ctx, cfg)

	// set the gas limit ceil
	setGasLimitCeil(ctx, cfg)

	// set the slices that the node is running
	setSlicesRunning(ctx, cfg)

	// Cap the cache allowance and tune the garbage collector
	mem, err := gopsutil.VirtualMemory()
	if err == nil {
		if 32<<(^uintptr(0)>>63) == 32 && mem.Total > 2*1024*1024*1024 {
			log.Warn("Lowering memory allowance on 32bit arch", "available", mem.Total/1024/1024, "addressable", 2*1024)
			mem.Total = 2 * 1024 * 1024 * 1024
		}
		allowance := int(mem.Total / 1024 / 1024 / 3)
		if cache := ctx.GlobalInt(CacheFlag.Name); cache > allowance {
			log.Warn("Sanitizing cache to Go's GC limits", "provided", cache, "updated", allowance)
			ctx.GlobalSet(CacheFlag.Name, strconv.Itoa(allowance))
		}
	}
	// Ensure Go's GC ignores the database cache for trigger percentage
	cache := ctx.GlobalInt(CacheFlag.Name)
	gogc := math.Max(20, math.Min(100, 100/(float64(cache)/1024)))

	log.Debug("Sanitizing Go's GC trigger", "percent", int(gogc))
	godebug.SetGCPercent(int(gogc))

	if ctx.GlobalIsSet(SyncModeFlag.Name) {
		cfg.SyncMode = *GlobalTextMarshaler(ctx, SyncModeFlag.Name).(*downloader.SyncMode)
	}
	if ctx.GlobalIsSet(NetworkIdFlag.Name) {
		cfg.NetworkId = ctx.GlobalUint64(NetworkIdFlag.Name)
	}
	if ctx.GlobalIsSet(CacheFlag.Name) || ctx.GlobalIsSet(CacheDatabaseFlag.Name) {
		cfg.DatabaseCache = ctx.GlobalInt(CacheFlag.Name) * ctx.GlobalInt(CacheDatabaseFlag.Name) / 100
	}
	cfg.DatabaseHandles = MakeDatabaseHandles()
	if ctx.GlobalIsSet(AncientFlag.Name) {
		cfg.DatabaseFreezer = ctx.GlobalString(AncientFlag.Name)
	}

	if gcmode := ctx.GlobalString(GCModeFlag.Name); gcmode != "full" && gcmode != "archive" {
		Fatalf("--%s must be either 'full' or 'archive'", GCModeFlag.Name)
	}
	if ctx.GlobalIsSet(GCModeFlag.Name) {
		cfg.NoPruning = ctx.GlobalString(GCModeFlag.Name) == "archive"
	}
	if ctx.GlobalIsSet(CacheNoPrefetchFlag.Name) {
		cfg.NoPrefetch = ctx.GlobalBool(CacheNoPrefetchFlag.Name)
	}
	// Read the value from the flag no matter if it's set or not.
	cfg.Preimages = ctx.GlobalBool(CachePreimagesFlag.Name)
	if cfg.NoPruning && !cfg.Preimages {
		cfg.Preimages = true
		log.Info("Enabling recording of key preimages since archive mode is used")
	}
	if ctx.GlobalIsSet(TxLookupLimitFlag.Name) {
		cfg.TxLookupLimit = ctx.GlobalUint64(TxLookupLimitFlag.Name)
	}
	if ctx.GlobalIsSet(CacheFlag.Name) || ctx.GlobalIsSet(CacheTrieFlag.Name) {
		cfg.TrieCleanCache = ctx.GlobalInt(CacheFlag.Name) * ctx.GlobalInt(CacheTrieFlag.Name) / 100
	}
	if ctx.GlobalIsSet(CacheTrieJournalFlag.Name) {
		cfg.TrieCleanCacheJournal = ctx.GlobalString(CacheTrieJournalFlag.Name)
	}
	if ctx.GlobalIsSet(CacheTrieRejournalFlag.Name) {
		cfg.TrieCleanCacheRejournal = ctx.GlobalDuration(CacheTrieRejournalFlag.Name)
	}
	if ctx.GlobalIsSet(CacheFlag.Name) || ctx.GlobalIsSet(CacheGCFlag.Name) {
		cfg.TrieDirtyCache = ctx.GlobalInt(CacheFlag.Name) * ctx.GlobalInt(CacheGCFlag.Name) / 100
	}
	if ctx.GlobalIsSet(CacheFlag.Name) || ctx.GlobalIsSet(CacheSnapshotFlag.Name) {
		cfg.SnapshotCache = ctx.GlobalInt(CacheFlag.Name) * ctx.GlobalInt(CacheSnapshotFlag.Name) / 100
	}
	if !ctx.GlobalBool(SnapshotFlag.Name) {
		cfg.TrieCleanCache += cfg.SnapshotCache
		cfg.SnapshotCache = 0 // Disabled
	}
	if ctx.GlobalIsSet(DocRootFlag.Name) {
		cfg.DocRoot = ctx.GlobalString(DocRootFlag.Name)
	}
	if ctx.GlobalIsSet(VMEnableDebugFlag.Name) {
		// TODO(fjl): force-enable this in --dev mode
		cfg.EnablePreimageRecording = ctx.GlobalBool(VMEnableDebugFlag.Name)
	}

	if ctx.GlobalIsSet(RPCGlobalGasCapFlag.Name) {
		cfg.RPCGasCap = ctx.GlobalUint64(RPCGlobalGasCapFlag.Name)
	}
	if cfg.RPCGasCap != 0 {
		log.Info("Set global gas cap", "cap", cfg.RPCGasCap)
	} else {
		log.Info("Global gas cap disabled")
	}
	if ctx.GlobalIsSet(RPCGlobalTxFeeCapFlag.Name) {
		cfg.RPCTxFeeCap = ctx.GlobalFloat64(RPCGlobalTxFeeCapFlag.Name)
	}
	if ctx.GlobalIsSet(NoDiscoverFlag.Name) {
		cfg.EthDiscoveryURLs, cfg.SnapDiscoveryURLs = []string{}, []string{}
	} else if ctx.GlobalIsSet(DNSDiscoveryFlag.Name) {
		urls := ctx.GlobalString(DNSDiscoveryFlag.Name)
		if urls == "" {
			cfg.EthDiscoveryURLs = []string{}
		} else {
			cfg.EthDiscoveryURLs = SplitAndTrim(urls)
		}
	}
	// Override any default configs for hard coded networks.
	switch {
	case ctx.GlobalBool(ColosseumFlag.Name):
		if !ctx.GlobalIsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 1
		}
		cfg.Genesis = core.DefaultColosseumGenesisBlock(cfg.ConsensusEngine)
		if cfg.ConsensusEngine == "blake3" {
			SetDNSDiscoveryDefaults(cfg, params.Blake3PowColosseumGenesisHash)
		} else {
			SetDNSDiscoveryDefaults(cfg, params.ProgpowColosseumGenesisHash)
		}
	case ctx.GlobalBool(GardenFlag.Name):
		if !ctx.GlobalIsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 2
		}
		cfg.Genesis = core.DefaultGardenGenesisBlock(cfg.ConsensusEngine)
		if cfg.ConsensusEngine == "blake3" {
			SetDNSDiscoveryDefaults(cfg, params.Blake3PowGardenGenesisHash)
		} else {
			SetDNSDiscoveryDefaults(cfg, params.ProgpowGardenGenesisHash)
		}
	case ctx.GlobalBool(OrchardFlag.Name):
		if !ctx.GlobalIsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 3
		}
		cfg.Genesis = core.DefaultOrchardGenesisBlock(cfg.ConsensusEngine)
		if cfg.ConsensusEngine == "blake3" {
			SetDNSDiscoveryDefaults(cfg, params.Blake3PowOrchardGenesisHash)
		} else {
			SetDNSDiscoveryDefaults(cfg, params.ProgpowOrchardGenesisHash)
		}
	case ctx.GlobalBool(LocalFlag.Name):
		if !ctx.GlobalIsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 4
		}
		cfg.Genesis = core.DefaultLocalGenesisBlock(cfg.ConsensusEngine)
		if cfg.ConsensusEngine == "blake3" {
			SetDNSDiscoveryDefaults(cfg, params.Blake3PowLocalGenesisHash)
		} else {
			SetDNSDiscoveryDefaults(cfg, params.ProgpowLocalGenesisHash)
		}
	case ctx.GlobalBool(LighthouseFlag.Name):
		if !ctx.GlobalIsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 5
		}
		cfg.Genesis = core.DefaultLighthouseGenesisBlock(cfg.ConsensusEngine)
		if cfg.ConsensusEngine == "blake3" {
			SetDNSDiscoveryDefaults(cfg, params.Blake3PowLighthouseGenesisHash)
		} else {
			SetDNSDiscoveryDefaults(cfg, params.ProgpowLighthouseGenesisHash)
		}
	case ctx.GlobalBool(DeveloperFlag.Name):
		if !ctx.GlobalIsSet(NetworkIdFlag.Name) {
			cfg.NetworkId = 1337
		}
		cfg.SyncMode = downloader.FullSync

		if ctx.GlobalIsSet(DataDirFlag.Name) {
			// Check if we have an already initialized chain and fall back to
			// that if so. Otherwise we need to generate a new genesis spec.
			chaindb := MakeChainDatabase(ctx, stack, false) // TODO (MariusVanDerWijden) make this read only
			if rawdb.ReadCanonicalHash(chaindb, 0) != (common.Hash{}) {
				cfg.Genesis = nil // fallback to db content
			}
			chaindb.Close()
		}
		if !ctx.GlobalIsSet(MinerGasPriceFlag.Name) {
			cfg.Miner.GasPrice = big.NewInt(1)
		}
	default:
		if cfg.NetworkId == 1 {
			SetDNSDiscoveryDefaults(cfg, params.ProgpowColosseumGenesisHash)
		}
	}
	if !ctx.GlobalBool(LocalFlag.Name) {
		cfg.Genesis.Nonce = ctx.GlobalUint64(GenesisNonceFlag.Name)
	}

	cfg.Genesis.Config.SetLocation(cfg.NodeLocation)
}

// SetDNSDiscoveryDefaults configures DNS discovery with the given URL if
// no URLs are set.
func SetDNSDiscoveryDefaults(cfg *ethconfig.Config, genesis common.Hash) {
	if cfg.EthDiscoveryURLs != nil && len(cfg.EthDiscoveryURLs) > 0 {
		return // already set through flags/config
	}
	protocol := "all"
	if url := params.KnownDNSNetwork(genesis, protocol, cfg.NodeLocation); url != "" {
		cfg.EthDiscoveryURLs = []string{url}
		cfg.SnapDiscoveryURLs = cfg.EthDiscoveryURLs
	}
}

// RegisterEthService adds a Quai client to the stack.
// The second return value is the full node instance, which may be nil if the
// node is running as a light client.
func RegisterEthService(stack *node.Node, cfg *ethconfig.Config, nodeCtx int) (quaiapi.Backend, *eth.Quai) {
	backend, err := eth.New(stack, cfg, nodeCtx)
	if err != nil {
		Fatalf("Failed to register the Quai service: %v", err)
	}
	return backend.APIBackend, backend
}

// RegisterQuaiStatsService configures the Quai Stats daemon and adds it to
// the given node.
func RegisterQuaiStatsService(stack *node.Node, backend quaiapi.Backend, url string, sendfullstats bool) {
	if err := quaistats.New(stack, backend, backend.Engine(), url, sendfullstats); err != nil {
		Fatalf("Failed to register the Quai Stats service: %v", err)
	}
}

func SetupMetrics(ctx *cli.Context) {
	if metrics.Enabled {
		log.Info("Enabling metrics collection")

		var (
			enableExport = ctx.GlobalBool(MetricsEnableInfluxDBFlag.Name)
			endpoint     = ctx.GlobalString(MetricsInfluxDBEndpointFlag.Name)
			database     = ctx.GlobalString(MetricsInfluxDBDatabaseFlag.Name)
			username     = ctx.GlobalString(MetricsInfluxDBUsernameFlag.Name)
			password     = ctx.GlobalString(MetricsInfluxDBPasswordFlag.Name)
		)

		if enableExport {
			tagsMap := SplitTagsFlag(ctx.GlobalString(MetricsInfluxDBTagsFlag.Name))

			log.Info("Enabling metrics export to InfluxDB")

			go influxdb.InfluxDBWithTags(metrics.DefaultRegistry, 10*time.Second, endpoint, database, username, password, "quai.", tagsMap)
		}

		if ctx.GlobalIsSet(MetricsHTTPFlag.Name) {
			address := fmt.Sprintf("%s:%d", ctx.GlobalString(MetricsHTTPFlag.Name), ctx.GlobalInt(MetricsPortFlag.Name))
			log.Info("Enabling stand-alone metrics HTTP endpoint", "address", address)
			exp.Setup(address)
		}
	}
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
func MakeChainDatabase(ctx *cli.Context, stack *node.Node, readonly bool) ethdb.Database {
	var (
		cache   = ctx.GlobalInt(CacheFlag.Name) * ctx.GlobalInt(CacheDatabaseFlag.Name) / 100
		handles = MakeDatabaseHandles()

		err     error
		chainDb ethdb.Database
	)
	name := "chaindata"
	chainDb, err = stack.OpenDatabaseWithFreezer(name, cache, handles, ctx.GlobalString(AncientFlag.Name), "", readonly)
	if err != nil {
		Fatalf("Could not open database: %v", err)
	}
	return chainDb
}

func MakeGenesis(ctx *cli.Context) *core.Genesis {
	var genesis *core.Genesis
	switch {
	case ctx.GlobalBool(ColosseumFlag.Name):
		genesis = core.DefaultColosseumGenesisBlock(ctx.GlobalString(ConsensusEngineFlag.Name))
	case ctx.GlobalBool(GardenFlag.Name):
		genesis = core.DefaultGardenGenesisBlock(ctx.GlobalString(ConsensusEngineFlag.Name))
	case ctx.GlobalBool(OrchardFlag.Name):
		genesis = core.DefaultOrchardGenesisBlock(ctx.GlobalString(ConsensusEngineFlag.Name))
	case ctx.GlobalBool(LighthouseFlag.Name):
		genesis = core.DefaultLighthouseGenesisBlock(ctx.GlobalString(ConsensusEngineFlag.Name))
	case ctx.GlobalBool(LocalFlag.Name):
		genesis = core.DefaultLocalGenesisBlock(ctx.GlobalString(ConsensusEngineFlag.Name))
	case ctx.GlobalBool(DeveloperFlag.Name):
		Fatalf("Developer chains are ephemeral")
	}
	return genesis
}

// MakeChain creates a chain manager from set command line flags.
func MakeChain(ctx *cli.Context, stack *node.Node) (*core.Core, ethdb.Database) {
	var err error
	chainDb := MakeChainDatabase(ctx, stack, false) // TODO(rjl493456442) support read-only database
	config, _, err := core.SetupGenesisBlock(chainDb, MakeGenesis(ctx), stack.Config().NodeLocation)
	if err != nil {
		Fatalf("%v", err)
	}
	var engine consensus.Engine

	engine = progpow.NewFaker()
	if !ctx.GlobalBool(FakePoWFlag.Name) {
		engine = progpow.New(progpow.Config{}, nil, false)
	}

	// If blake3 consensus engine is selected use the blake3 engine
	if ctx.GlobalString(ConsensusEngineFlag.Name) == "blake3" {
		engine = blake3pow.New(blake3pow.Config{}, nil, false)
	}

	if gcmode := ctx.GlobalString(GCModeFlag.Name); gcmode != "full" && gcmode != "archive" {
		Fatalf("--%s must be either 'full' or 'archive'", GCModeFlag.Name)
	}
	cache := &core.CacheConfig{
		TrieCleanLimit:      ethconfig.Defaults.TrieCleanCache,
		TrieCleanNoPrefetch: ctx.GlobalBool(CacheNoPrefetchFlag.Name),
		TrieDirtyLimit:      ethconfig.Defaults.TrieDirtyCache,
		TrieDirtyDisabled:   ctx.GlobalString(GCModeFlag.Name) == "archive",
		TrieTimeLimit:       ethconfig.Defaults.TrieTimeout,
		SnapshotLimit:       ethconfig.Defaults.SnapshotCache,
		Preimages:           ctx.GlobalBool(CachePreimagesFlag.Name),
	}
	if cache.TrieDirtyDisabled && !cache.Preimages {
		cache.Preimages = true
		log.Info("Enabling recording of key preimages since archive mode is used")
	}
	if !ctx.GlobalBool(SnapshotFlag.Name) {
		cache.SnapshotLimit = 0 // Disabled
	}
	if ctx.GlobalIsSet(CacheFlag.Name) || ctx.GlobalIsSet(CacheTrieFlag.Name) {
		cache.TrieCleanLimit = ctx.GlobalInt(CacheFlag.Name) * ctx.GlobalInt(CacheTrieFlag.Name) / 100
	}
	if ctx.GlobalIsSet(CacheFlag.Name) || ctx.GlobalIsSet(CacheGCFlag.Name) {
		cache.TrieDirtyLimit = ctx.GlobalInt(CacheFlag.Name) * ctx.GlobalInt(CacheGCFlag.Name) / 100
	}
	vmcfg := vm.Config{EnablePreimageRecording: ctx.GlobalBool(VMEnableDebugFlag.Name)}

	// TODO(rjl493456442) disable snapshot generation/wiping if the chain is read only.
	// Disable transaction indexing/unindexing by default.
	protocol, err := core.NewCore(chainDb, nil, nil, nil, nil, config, nil, ctx.GlobalString(DomUrl.Name), makeSubUrls(ctx), engine, cache, vmcfg, &core.Genesis{})
	if err != nil {
		Fatalf("Can't create BlockChain: %v", err)
	}
	return protocol, chainDb
}

// MakeConsolePreloads retrieves the absolute paths for the console JavaScript
// scripts to preload before starting.
func MakeConsolePreloads(ctx *cli.Context) []string {
	// Skip preloading if there's nothing to preload
	if ctx.GlobalString(PreloadJSFlag.Name) == "" {
		return nil
	}
	// Otherwise resolve absolute paths and return them
	var preloads []string

	for _, file := range strings.Split(ctx.GlobalString(PreloadJSFlag.Name), ",") {
		preloads = append(preloads, strings.TrimSpace(file))
	}
	return preloads
}

// MigrateFlags sets the global flag from a local flag when it's set.
// This is a temporary function used for migrating old command/flags to the
// new format.
//
// e.g. quai account new --keystore /tmp/mykeystore --lightkdf
//
// is equivalent after calling this method with:
//
// quai --keystore /tmp/mykeystore --lightkdf account new
//
// This allows the use of the existing configuration functionality.
// When all flags are migrated this function can be removed and the existing
// configuration functionality must be changed that is uses local flags
func MigrateFlags(action func(ctx *cli.Context) error) func(*cli.Context) error {
	return func(ctx *cli.Context) error {
		for _, name := range ctx.FlagNames() {
			if ctx.IsSet(name) {
				ctx.GlobalSet(name, ctx.String(name))
			}
		}
		return action(ctx)
	}
>>>>>>> 88c428a1 (Removing NodeLocation from common)
}
