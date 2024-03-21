package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/p2p/node"
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

	// if no bootstrap peers are provided, use the default ones defined in config/bootnodes.go
	if bootstrapPeers := viper.GetStringSlice(utils.BootPeersFlag.Name); len(bootstrapPeers) == 0 {
		log.Global.Debugf("no bootstrap peers provided. Using default ones: %v", common.BootstrapPeers)
		viper.Set(utils.BootPeersFlag.Name, common.BootstrapPeers)
	}
	return nil
}

func runStart(cmd *cobra.Command, args []string) error {
	log.Global.Info("Starting go-quai")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if viper.IsSet(utils.PprofFlag.Name) {
		utils.EnablePprof()
	}

	// create a new p2p node
	node, err := node.NewNode(ctx)
	if err != nil {
		log.Global.WithField("error", err).Fatal("error creating node")
	}

	logLevel := cmd.Flag(utils.LogLevelFlag.Name).Value.String()
	// create instance of consensus backend
	var nodeWG sync.WaitGroup
	consensus, err := utils.StartQuaiBackend(ctx, node, logLevel, &nodeWG)
	if err != nil {
		log.Global.WithField("error", err).Fatal("error creating consensus backend")
	}

	// start the p2p node
	node.SetConsensusBackend(consensus)
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
	nodeWG.Wait()
	if err := node.Stop(); err != nil {
		panic(err)
	}
	log.Global.Warn("Node is offline")
	return nil
}
