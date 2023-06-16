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
	"bytes"
	"fmt"
	"net/http"
	"os"
	"runtime"
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
	"github.com/dominant-strategies/go-quai/params"

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
		utils.IdentityFlag,
		utils.UnlockedAccountFlag,
		utils.PasswordFileFlag,
		utils.BootnodesFlag,
		utils.DataDirFlag,
		utils.AncientFlag,
		utils.MinFreeDiskSpaceFlag,
		utils.KeyStoreDirFlag,
		utils.ExternalSignerFlag,
		utils.NoUSBFlag,
		utils.USBFlag,
		utils.TxPoolLocalsFlag,
		utils.TxPoolNoLocalsFlag,
		utils.TxPoolJournalFlag,
		utils.TxPoolRejournalFlag,
		utils.TxPoolPriceLimitFlag,
		utils.TxPoolPriceBumpFlag,
		utils.TxPoolAccountSlotsFlag,
		utils.TxPoolGlobalSlotsFlag,
		utils.TxPoolAccountQueueFlag,
		utils.TxPoolGlobalQueueFlag,
		utils.TxPoolLifetimeFlag,
		utils.SyncModeFlag,
		utils.ExitWhenSyncedFlag,
		utils.GCModeFlag,
		utils.SnapshotFlag,
		utils.TxLookupLimitFlag,
		utils.LightKDFFlag,
		utils.WhitelistFlag,
		utils.BloomFilterSizeFlag,
		utils.CacheFlag,
		utils.CacheDatabaseFlag,
		utils.CacheTrieFlag,
		utils.CacheTrieJournalFlag,
		utils.CacheTrieRejournalFlag,
		utils.CacheGCFlag,
		utils.CacheSnapshotFlag,
		utils.CacheNoPrefetchFlag,
		utils.CachePreimagesFlag,
		utils.ListenPortFlag,
		utils.MaxPeersFlag,
		utils.MaxPendingPeersFlag,
		utils.MinerGasPriceFlag,
		utils.MinerEtherbaseFlag,
		utils.NATFlag,
		utils.NoDiscoverFlag,
		utils.DiscoveryV5Flag,
		utils.NetrestrictFlag,
		utils.NodeKeyFileFlag,
		utils.NodeKeyHexFlag,
		utils.DNSDiscoveryFlag,
		utils.SlicesRunningFlag,
		utils.ColosseumFlag,
		utils.DeveloperFlag,
		utils.DeveloperPeriodFlag,
		utils.GardenFlag,
		utils.OrchardFlag,
		utils.GalenaFlag,
		utils.LocalFlag,
		utils.GenesisNonceFlag,
		utils.VMEnableDebugFlag,
		utils.NetworkIdFlag,
		utils.QuaiStatsURLFlag,
		utils.FakePoWFlag,
		utils.NoCompactionFlag,
		utils.GpoBlocksFlag,
		utils.GpoPercentileFlag,
		utils.GpoMaxGasPriceFlag,
		utils.GpoIgnoreGasPriceFlag,
		utils.ShowColorsFlag,
		configFileFlag,
		utils.RegionFlag,
		utils.ZoneFlag,
		utils.DomUrl,
		utils.SubUrls,
	}

	rpcFlags = []cli.Flag{
		utils.HTTPEnabledFlag,
		utils.HTTPListenAddrFlag,
		utils.HTTPPortFlag,
		utils.HTTPCORSDomainFlag,
		utils.HTTPVirtualHostsFlag,
		utils.LegacyRPCEnabledFlag,
		utils.LegacyRPCListenAddrFlag,
		utils.LegacyRPCPortFlag,
		utils.LegacyRPCCORSDomainFlag,
		utils.LegacyRPCVirtualHostsFlag,
		utils.LegacyRPCApiFlag,
		utils.HTTPApiFlag,
		utils.HTTPPathPrefixFlag,
		utils.WSEnabledFlag,
		utils.WSListenAddrFlag,
		utils.WSPortFlag,
		utils.WSApiFlag,
		utils.WSAllowedOriginsFlag,
		utils.WSPathPrefixFlag,
		utils.InsecureUnlockAllowedFlag,
		utils.RPCGlobalGasCapFlag,
		utils.RPCGlobalTxFeeCapFlag,
	}

	metricsFlags = []cli.Flag{
		utils.MetricsEnabledFlag,
		utils.MetricsEnabledExpensiveFlag,
		utils.MetricsHTTPFlag,
		utils.MetricsPortFlag,
		utils.MetricsEnableInfluxDBFlag,
		utils.MetricsInfluxDBEndpointFlag,
		utils.MetricsInfluxDBDatabaseFlag,
		utils.MetricsInfluxDBUsernameFlag,
		utils.MetricsInfluxDBPasswordFlag,
		utils.MetricsInfluxDBTagsFlag,
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
	runtime.SetBlockProfileRate(1)
	runtime.SetMutexProfileFraction(1)
	var port string
	myContext := common.NodeLocation
	switch {
	case bytes.Equal(myContext, []byte{}): // PRIME
		port = "8081"
	case bytes.Equal(myContext, []byte{0}): // Region 0
		port = "8090"
	case bytes.Equal(myContext, []byte{1}): // Region 1
		port = "8100"
	case bytes.Equal(myContext, []byte{2}): // Region 2
		port = "8110"
	case bytes.Equal(myContext, []byte{0, 0}): // Zone 0-0
		port = "8091"
	case bytes.Equal(myContext, []byte{0, 1}): // Zone 0-1
		port = "8092"
	case bytes.Equal(myContext, []byte{0, 2}): // Zone 0-2
		port = "8093"
	case bytes.Equal(myContext, []byte{1, 0}): // Zone 1-0
		port = "8101"
	case bytes.Equal(myContext, []byte{1, 1}): // Zone 1-1
		port = "8102"
	case bytes.Equal(myContext, []byte{1, 2}): // Zone 1-2
		port = "8103"
	case bytes.Equal(myContext, []byte{2, 0}): // Zone 2-0
		port = "8111"
	case bytes.Equal(myContext, []byte{2, 1}): // Zone 2-1
		port = "8112"
	case bytes.Equal(myContext, []byte{2, 2}): // Zone 2-2
		port = "8113"
	default:
		port = "8085"
	}
	go func() {
		_ = http.ListenAndServe("localhost:"+port, nil)
	}()
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
	case ctx.GlobalIsSet(utils.GalenaFlag.Name):
		netname = utils.GalenaFlag.Name + " testnet"
	case ctx.GlobalIsSet(utils.DeveloperFlag.Name):
		netname = utils.DeveloperFlag.Name + " ephemeral dev network"
	case !ctx.GlobalIsSet(utils.NetworkIdFlag.Name):
		netname = "Colosseum testnet"
	}

	welcome := fmt.Sprintf("Starting Quai %s on %s", params.Version.Full(), netname)
	log.Info(welcome)
	// If we're a full node on colosseum without --cache specified, bump default cache allowance
	if ctx.GlobalString(utils.SyncModeFlag.Name) != "light" && !ctx.GlobalIsSet(utils.CacheFlag.Name) && !ctx.GlobalIsSet(utils.NetworkIdFlag.Name) {
		// Make sure we're not on any supported preconfigured testnet either
		if !ctx.GlobalIsSet(utils.GardenFlag.Name) && !ctx.GlobalIsSet(utils.OrchardFlag.Name) && !ctx.GlobalIsSet(utils.GalenaFlag.Name) && !ctx.GlobalIsSet(utils.LocalFlag.Name) && !ctx.GlobalIsSet(utils.DeveloperFlag.Name) {
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
	go metrics.CollectProcessMetrics(3 * time.Second)
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
