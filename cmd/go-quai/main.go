// Copyright 2014 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

// quai is the official command-line client for Quai.
package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/eth"
	"github.com/dominant-strategies/go-quai/eth/downloader"
	"github.com/dominant-strategies/go-quai/internal/debug"
	"github.com/dominant-strategies/go-quai/internal/flags"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics"
	"github.com/dominant-strategies/go-quai/node"

	"gopkg.in/urfave/cli.v1"
)

const (
	clientIdentifier = "quai" // Client identifier to advertise over the network
)

var (
	// Git SHA1 commit hash of the release (set via linker flags)
	gitCommit = ""
	gitDate   = ""
	// The app that holds all commands and flags.
	app = flags.NewApp(gitCommit, gitDate, "the go-quai command line interface")
	// flags that configure the node
	nodeFlags = []cli.Flag{
		configFileFlag,
		utils.AncientFlag,
		utils.BloomFilterSizeFlag,
		utils.BootnodesFlag,
		utils.CacheDatabaseFlag,
		utils.CacheFlag,
		utils.CacheGCFlag,
		utils.CacheNoPrefetchFlag,
		utils.CachePreimagesFlag,
		utils.CacheSnapshotFlag,
		utils.CacheTrieFlag,
		utils.CacheTrieJournalFlag,
		utils.CacheTrieRejournalFlag,
		utils.ColosseumFlag,
		utils.ConsensusEngineFlag,
		utils.DNSDiscoveryFlag,
		utils.DataDirFlag,
		utils.DBEngineFlag,
		utils.DeveloperFlag,
		utils.DeveloperPeriodFlag,
		utils.DiscoveryV5Flag,
		utils.DomUrl,
		utils.ExitWhenSyncedFlag,
		utils.ExternalSignerFlag,
		utils.FakePoWFlag,
		utils.GCModeFlag,
		utils.LighthouseFlag,
		utils.GardenFlag,
		utils.GenesisNonceFlag,
		utils.GpoBlocksFlag,
		utils.GpoIgnoreGasPriceFlag,
		utils.GpoMaxGasPriceFlag,
		utils.GpoPercentileFlag,
		utils.IdentityFlag,
		utils.KeyStoreDirFlag,
		utils.LightKDFFlag,
		utils.ListenPortFlag,
		utils.LocalFlag,
		utils.LogToStdOutFlag,
		utils.MaxPeersFlag,
		utils.MaxPendingPeersFlag,
		utils.MinFreeDiskSpaceFlag,
		utils.MinerEtherbaseFlag,
		utils.MinerGasPriceFlag,
		utils.NATFlag,
		utils.NetrestrictFlag,
		utils.NetworkIdFlag,
		utils.NoCompactionFlag,
		utils.NoDiscoverFlag,
		utils.NoUSBFlag,
		utils.NodeKeyFileFlag,
		utils.NodeKeyHexFlag,
		utils.OrchardFlag,
		utils.PasswordFileFlag,
		utils.QuaiStatsURLFlag,
		utils.SendFullStatsFlag,
		utils.RegionFlag,
		utils.ShowColorsFlag,
		utils.SlicesRunningFlag,
		utils.SnapshotFlag,
		utils.SubUrls,
		utils.SyncModeFlag,
		utils.TxLookupLimitFlag,
		utils.TxPoolAccountQueueFlag,
		utils.TxPoolAccountSlotsFlag,
		utils.TxPoolGlobalQueueFlag,
		utils.TxPoolGlobalSlotsFlag,
		utils.TxPoolJournalFlag,
		utils.TxPoolLifetimeFlag,
		utils.TxPoolLocalsFlag,
		utils.TxPoolNoLocalsFlag,
		utils.TxPoolPriceBumpFlag,
		utils.TxPoolPriceLimitFlag,
		utils.TxPoolRejournalFlag,
		utils.USBFlag,
		utils.UnlockedAccountFlag,
		utils.VMEnableDebugFlag,
		utils.WhitelistFlag,
		utils.ZoneFlag,
	}

	rpcFlags = []cli.Flag{
		utils.HTTPApiFlag,
		utils.HTTPCORSDomainFlag,
		utils.HTTPEnabledFlag,
		utils.HTTPListenAddrFlag,
		utils.HTTPPathPrefixFlag,
		utils.HTTPPortFlag,
		utils.HTTPVirtualHostsFlag,
		utils.InsecureUnlockAllowedFlag,
		utils.LegacyRPCApiFlag,
		utils.LegacyRPCCORSDomainFlag,
		utils.LegacyRPCEnabledFlag,
		utils.LegacyRPCListenAddrFlag,
		utils.LegacyRPCPortFlag,
		utils.LegacyRPCVirtualHostsFlag,
		utils.RPCGlobalGasCapFlag,
		utils.RPCGlobalTxFeeCapFlag,
		utils.WSAllowedOriginsFlag,
		utils.WSApiFlag,
		utils.WSEnabledFlag,
		utils.WSListenAddrFlag,
		utils.WSPathPrefixFlag,
		utils.WSPortFlag,
	}

	metricsFlags = []cli.Flag{
		utils.MetricsEnableInfluxDBFlag,
		utils.MetricsEnabledExpensiveFlag,
		utils.MetricsEnabledFlag,
		utils.MetricsHTTPFlag,
		utils.MetricsInfluxDBDatabaseFlag,
		utils.MetricsInfluxDBEndpointFlag,
		utils.MetricsInfluxDBPasswordFlag,
		utils.MetricsInfluxDBTagsFlag,
		utils.MetricsInfluxDBUsernameFlag,
		utils.MetricsPortFlag,
	}
)

func init() {
	// Initialize the CLI app and start Quai
	app.Action = quai
	app.HideVersion = true // we have a command to print the version
	app.Copyright = "Copyright 2013-2021 The go-ethereum Authors"
	app.Commands = []cli.Command{
		// See chaincmd.go:
		initCommand,
		importCommand,
		exportCommand,
		importPreimagesCommand,
		exportPreimagesCommand,
		dumpCommand,
		dumpGenesisCommand,
		// See misccmd.go:
		versionCommand,
		versionCheckCommand,
		licenseCommand,
		// See config.go
		dumpConfigCommand,
		// See snapshot.go
		snapshotCommand,
	}
	sort.Sort(cli.CommandsByName(app.Commands))

	app.Flags = append(app.Flags, nodeFlags...)
	app.Flags = append(app.Flags, rpcFlags...)
	app.Flags = append(app.Flags, debug.Flags...)
	app.Flags = append(app.Flags, metricsFlags...)

	app.Before = func(ctx *cli.Context) error {
		return debug.Setup(ctx)
	}
	app.After = func(ctx *cli.Context) error {
		debug.Exit()
		return nil
	}
}

func main() {
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// prepare manipulates memory cache allowance and setups metric system.
// This function should be called before launching devp2p stack.
func prepare(ctx *cli.Context) {
	// If we're running a known preset, log it for convenience.
	var netname string
	switch {
	case ctx.GlobalIsSet(utils.GardenFlag.Name):
		netname = utils.GardenFlag.Name + " testnet"
	case ctx.GlobalIsSet(utils.OrchardFlag.Name):
		netname = utils.OrchardFlag.Name + " testnet"
	case ctx.GlobalIsSet(utils.LocalFlag.Name):
		netname = utils.LocalFlag.Name + " testnet"
	case ctx.GlobalIsSet(utils.LighthouseFlag.Name):
		netname = utils.LighthouseFlag.Name + " testnet"
	case ctx.GlobalIsSet(utils.DeveloperFlag.Name):
		netname = utils.DeveloperFlag.Name + " ephemeral dev network"
	case !ctx.GlobalIsSet(utils.NetworkIdFlag.Name):
		netname = "Colosseum testnet"
	}

	welcome := fmt.Sprintf("Starting Quai %s on %s", ctx.App.Version, netname)
	log.Info(welcome)
	// If we're a full node on colosseum without --cache specified, bump default cache allowance
	if ctx.GlobalString(utils.SyncModeFlag.Name) != "light" && !ctx.GlobalIsSet(utils.CacheFlag.Name) && !ctx.GlobalIsSet(utils.NetworkIdFlag.Name) {
		// Make sure we're not on any supported preconfigured testnet either
		if !ctx.GlobalIsSet(utils.GardenFlag.Name) && !ctx.GlobalIsSet(utils.OrchardFlag.Name) && !ctx.GlobalIsSet(utils.LighthouseFlag.Name) && !ctx.GlobalIsSet(utils.LocalFlag.Name) && !ctx.GlobalIsSet(utils.DeveloperFlag.Name) {
			// Nope, we're really on colosseum. Bump that cache up!
			log.Info("Bumping default cache on colosseum", "provided", ctx.GlobalInt(utils.CacheFlag.Name), "updated", 4096)
			ctx.GlobalSet(utils.CacheFlag.Name, strconv.Itoa(4096))
		}
	}
	// If we're running a light client on any network, drop the cache to some meaningfully low amount
	if ctx.GlobalString(utils.SyncModeFlag.Name) == "light" && !ctx.GlobalIsSet(utils.CacheFlag.Name) {
		log.Info("Dropping default light client cache", "provided", ctx.GlobalInt(utils.CacheFlag.Name), "updated", 128)
		ctx.GlobalSet(utils.CacheFlag.Name, strconv.Itoa(128))
	}

	// Start metrics export if enabled
	utils.SetupMetrics(ctx)

	// Start system runtime metrics collection
	if ctx.GlobalBool(utils.MetricsEnabledFlag.Name) {
		go metrics.CollectProcessMetrics(3 * time.Second)
	}
}

// quai is the main entry point into the system if no special subcommand is ran.
// It creates a default node based on the command line arguments and runs it in
// blocking mode, waiting for it to be shut down.
func quai(ctx *cli.Context) error {
	if args := ctx.Args(); len(args) > 0 {
		return fmt.Errorf("invalid command: %q", args[0])
	}

	// Setup logger.
	log.ConfigureLogger(ctx)

	prepare(ctx)
	stack, backend := makeFullNode(ctx)
	defer stack.Close()

	startNode(ctx, stack, backend)
	stack.Wait()
	return nil
}

// startNode boots up the system node and all registered protocols, after which
// it unlocks any requested accounts, and starts the RPC interfaces and the
// miner.
func startNode(ctx *cli.Context, stack *node.Node, backend quaiapi.Backend) {
	debug.Memsize.Add("node", stack)

	// Start up the node itself
	utils.StartNode(ctx, stack)

	// Spawn a standalone goroutine for status synchronization monitoring,
	// close the node when synchronization is complete if user required.
	if ctx.GlobalBool(utils.ExitWhenSyncedFlag.Name) {
		go func() {
			sub := stack.EventMux().Subscribe(downloader.DoneEvent{})
			defer sub.Unsubscribe()
			for {
				event := <-sub.Chan()
				if event == nil {
					continue
				}
				done, ok := event.Data.(downloader.DoneEvent)
				if !ok {
					continue
				}
				if timestamp := time.Unix(int64(done.Latest.Time()), 0); time.Since(timestamp) < 10*time.Minute {
					log.Info("Synchronisation completed", "latestnum", done.Latest.Number(), "latesthash", done.Latest.Hash(),
						"age", common.PrettyAge(timestamp))
					stack.Close()
				}
			}
		}()
	}

	// Start auxiliary services if enabled
	if ctx.GlobalBool(utils.DeveloperFlag.Name) {

		var err error
		ethBackend, ok := backend.(*eth.QuaiAPIBackend)
		if !ok {
			utils.Fatalf("Quai service not running: %v", err)
		}
		nodeCtx := common.NodeLocation.Context()
		if nodeCtx == common.ZONE_CTX {
			// Set the gas price to the limits from the CLI and start mining
			gasprice := utils.GlobalBig(ctx, utils.MinerGasPriceFlag.Name)
			ethBackend.TxPool().SetGasPrice(gasprice)
		}
	}
}
