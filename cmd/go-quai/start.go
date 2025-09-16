package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	godebug "runtime/debug"
	"strconv"
	"sync"
	"syscall"

	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/node"
	"github.com/dominant-strategies/go-quai/params"
)

var gitCommit, gitDate string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "starts a go-quai p2p node",
	Long: `starts the go-quai daemon. The daemon will start a libp2p node and a http API.
By default the node will bootstrap to the public bootstrap nodes and port 4002. 
To bootstrap to a private node, use the --bootstrap flag.`,
	RunE:                       runStart,
	SilenceUsage:               true,
	SuggestionsMinimumDistance: 2,
	Example:                    `go-quai start -log-level=debug`,
	PreRunE:                    startCmdPreRun,
}

func init() {
	rootCmd.AddCommand(startCmd)

	for _, flagGroup := range utils.Flags {
		for _, flag := range flagGroup {
			utils.CreateAndBindFlag(flag, startCmd)
		}
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
	log.Global.Infof("Starting %s on the %s network", params.Version.Full(), network)

	params.GitCommit = gitCommit
	log.Global.WithFields(log.Fields{"commit": gitCommit, "date": gitDate}).Info("node version")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if viper.GetBool(utils.PprofFlag.Name) {
		EnablePprof()
	}

	// create a quit channel for services to signal for a clean shutdown
	quitCh := make(chan struct{})

	common.SanityCheck(quitCh)
	// create a new p2p node
	node, err := node.NewNode(ctx, quitCh)
	if err != nil {
		log.Global.WithField("error", err).Fatal("error creating node")
	}

	logLevel := viper.GetString(utils.NodeLogLevelFlag.Name)

	startingExpansionNumber := viper.GetUint64(utils.StartingExpansionNumberFlag.Name)
	// Start the  hierarchical co-ordinator
	var nodeWg sync.WaitGroup
	hc := utils.NewHierarchicalCoordinator(node, logLevel, &nodeWg, startingExpansionNumber)
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
		var (
			addr string
			port int
		)

		addr = viper.GetString(utils.MetricsHTTPFlag.Name)
		port = viper.GetInt(utils.MetricsPortFlag.Name)

		if addr != "" && port != 0 {
			endpoint := addr + ":" + strconv.Itoa(port)
			metrics_config.EnableMetrics(endpoint)
			go metrics_config.StartProcessMetrics()
		}
	}

	// wait for a SIGINT or SIGTERM signal
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-ch:
		log.Global.Warn("Received 'stop' signal, shutting down gracefully...")
	case <-quitCh:
		log.Global.Warn("Received 'quit' signal from child, shutting down...")
	}

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
