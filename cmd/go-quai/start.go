package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	godebug "runtime/debug"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/p2p/node"
	"github.com/dominant-strategies/go-quai/params"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "starts a go-quai p2p node",
	Long: `starts the go-quai daemon. The daemon will start a libp2p node and a http API.
By default the node will bootstrap to the public bootstrap nodes and port 4001. 
To bootstrap to a private node, use the --bootstrap flag.`,
	RunE:                       runStart,
	SilenceUsage:               true,
	SuggestionsMinimumDistance: 2,
	Example:                    `go-quai start -log-level=debug`,
	PreRunE:                    startCmdPreRun,
}

func init() {
	rootCmd.AddCommand(startCmd)

	// Create and bind all node flags to the start command
	for _, flag := range utils.NodeFlags {
		utils.CreateAndBindFlag(flag, startCmd)
	}

	for _, flag := range utils.TXPoolFlags {
		utils.CreateAndBindFlag(flag, startCmd)
	}

	// Create and bind all rpc flags to the start command
	for _, flag := range utils.RPCFlags {
		utils.CreateAndBindFlag(flag, startCmd)
	}

	// Create and bind all metrics flags to the start command
	for _, flag := range utils.MetricsFlags {
		utils.CreateAndBindFlag(flag, startCmd)
	}
}

func startCmdPreRun(cmd *cobra.Command, args []string) error {
	// set keyfile path
	if viper.GetString(utils.KeyFileFlag.Name) == "" {
		configDir := cmd.Flag(utils.ConfigDirFlag.Name).Value.String()
		viper.Set(utils.KeyFileFlag.Name, filepath.Join(configDir, "private.key"))
	}
	// Initialize the version info
	params.InitVersion()
	return nil
}

func runStart(cmd *cobra.Command, args []string) error {
	network := viper.GetString(utils.EnvironmentFlag.Name)
	log.Global.Infof("Starting %s on the %s network", params.VersionWithCommit(), network)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if viper.IsSet(utils.PprofFlag.Name) {
		EnablePprof()
	}

	// create a quit channel for services to signal for a clean shutdown
	quitCh := make(chan struct{})

	// create a new p2p node
	node, err := node.NewNode(ctx, quitCh)
	if err != nil {
		log.Global.WithField("error", err).Fatal("error creating node")
	}

	logLevel := viper.GetString(utils.NodeLogLevelFlag.Name)

	var startingExpansionNumber uint64
	if viper.IsSet(utils.StartingExpansionNumberFlag.Name) {
		startingExpansionNumber = viper.GetUint64(utils.StartingExpansionNumberFlag.Name)
	}
	// Start the  hierarchical co-ordinator
	var nodeWg sync.WaitGroup
	hc := utils.NewHierarchicalCoordinator(node, logLevel, &nodeWg, startingExpansionNumber, quitCh)
	err = hc.StartHierarchicalCoordinator()
	if err != nil {
		log.Global.WithField("error", err).Fatal("error starting hierarchical coordinator")
	}

	// start the p2p node
	if err := node.Start(); err != nil {
		log.Global.WithField("error", err).Fatal("error starting node")
	}

	if viper.IsSet(utils.MetricsEnabledFlag.Name) {
		log.Global.Info("Starting metrics")
		metrics_config.EnableMetrics()
		go metrics_config.StartProcessMetrics()
	}

	// wait for a SIGINT or SIGTERM signal
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Global.Warn("Received 'stop' signal, shutting down gracefully...")
	cancel()
	// stop the hierarchical co-ordinator
	hc.Stop()
	if err := node.Stop(); err != nil {
		panic(err)
	}
	log.Global.Warn("Node is offline")
	return nil
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
