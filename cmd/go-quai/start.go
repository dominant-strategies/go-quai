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
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/node"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/stratum"
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

	// Find a processing zone backend for stratum and dashboard
	var zoneBackend quaiapi.Backend
	for i := 0; i < int(common.MaxRegions); i++ {
		for j := 0; j < int(common.MaxZones); j++ {
			backend := hc.GetBackend(common.Location{byte(i), byte(j)})
			if backend != nil && backend.NodeCtx() == common.ZONE_CTX && backend.ProcessingState() {
				zoneBackend = backend
				break
			}
		}
		if zoneBackend != nil {
			break
		}
	}

	// Optionally start stratum TCP servers when enabled and a zone backend is available
	var stratumServer *stratum.Server
	if viper.GetBool(utils.StratumEnabledFlag.Name) {
		if zoneBackend == nil {
			log.Global.Warn("Stratum endpoint enabled but no processing zone backend found; skipping start")
		} else {
			stratumConfig := stratum.StratumConfig{
				SHAAddr:        viper.GetString(utils.StratumSHAAddrFlag.Name),
				ScryptAddr:     viper.GetString(utils.StratumScryptAddrFlag.Name),
				KawpowAddr:     viper.GetString(utils.StratumKawpowAddrFlag.Name),
				VarDiffEnabled: viper.GetBool(utils.StratumVarDiffFlag.Name),
				ProxyProtocol:  viper.GetBool(utils.StratumProxyProtocolFlag.Name),
			}
			stratumServer = stratum.NewServerWithConfig(stratumConfig, zoneBackend)
			if err := stratumServer.Start(); err != nil {
				log.Global.WithField("error", err).Error("failed to start stratum endpoints")
			} else {
				log.Global.WithFields(log.Fields{
					"sha":           stratumConfig.SHAAddr,
					"scrypt":        stratumConfig.ScryptAddr,
					"kawpow":        stratumConfig.KawpowAddr,
					"vardiff":       stratumConfig.VarDiffEnabled,
					"proxy-protocol": stratumConfig.ProxyProtocol,
				}).Info("Stratum TCP endpoints started")

				// Start the stratum HTTP API server for pool dashboard
				apiAddr := viper.GetString(utils.StratumAPIAddrFlag.Name)
				nodeName := viper.GetString(utils.StratumNameFlag.Name)
				if apiAddr != "" {
					stratumAPI := stratum.NewAPI(apiAddr, nodeName, stratumServer.Stats(), zoneBackend)
					if err := stratumAPI.Start(); err != nil {
						log.Global.WithField("error", err).Error("failed to start stratum API")
					} else {
						log.Global.WithFields(log.Fields{
							"addr":     apiAddr,
							"nodeName": nodeName,
						}).Info("Stratum HTTP API started")
					}
				}
			}
		}
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
