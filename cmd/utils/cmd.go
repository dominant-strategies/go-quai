package utils

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/node"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai"
	"github.com/dominant-strategies/go-quai/quai/quaiconfig"
	"github.com/dominant-strategies/go-quai/quaistats"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// Create a new instance of the QuaiBackend consensus service
func StartQuaiBackend(logLevel string) (*quai.QuaiBackend, error) {
	var logger *logrus.Logger
	// Make full node
	go func() {
		// Create the prime logger with the log file path
		logger = log.NewLogger("nodelogs/prime.log", logLevel)
		logger.Info("Starting Prime")
		stackPrime := makeFullNode(nil, logger)
		defer stackPrime.Close()
		StartNode(stackPrime)
		stackPrime.Wait()
	}()

	time.Sleep(2 * time.Second)

	go func() {
		// Create the prime logger with the log file path
		logger = log.NewLogger("nodelogs/region-0.log", logLevel)
		logger.Info("Starting Region")
		stackRegion := makeFullNode(common.Location{0}, logger)
		defer stackRegion.Close()
		StartNode(stackRegion)
		stackRegion.Wait()
	}()

	time.Sleep(2 * time.Second)

	go func() {
		// Create the prime logger with the log file path
		logger = log.NewLogger("nodelogs/zone-0-0.log", logLevel)
		log.Info("Starting Zone")
		stackZone := makeFullNode(common.Location{0, 0}, logger)
		defer stackZone.Close()
		StartNode(stackZone)
		stackZone.Wait()
	}()

	return &quai.QuaiBackend{}, nil
}

func StartNode(stack *node.Node) {
	if err := stack.Start(); err != nil {
		Fatalf("Error starting protocol stack: %v", err)
	}
	// TODO: Stop the node if the memory pressure
}

// makeConfigNode loads quai configuration and creates a blank node instance.
func makeConfigNode(nodeLocation common.Location, logger *logrus.Logger) (*node.Node, quaiconfig.QuaiConfig) {
	// Load defaults.
	cfg := quaiconfig.QuaiConfig{
		Quai: quaiconfig.Defaults,
		Node: defaultNodeConfig(),
	}

	// Apply flags.
	// set the node location
	logger.WithField("location", nodeLocation).Info("Node Location")
	cfg.Node.NodeLocation = nodeLocation

	SetNodeConfig(&cfg.Node, nodeLocation, logger)
	stack, err := node.New(&cfg.Node, logger)
	if err != nil {
		Fatalf("Failed to create the protocol stack: %v", err)
	}
	SetQuaiConfig(stack, &cfg.Quai, nodeLocation, logger)

	// TODO: Apply stats
	if viper.IsSet(QuaiStatsURLFlag.Name) {
		cfg.Quaistats.URL = viper.GetString(QuaiStatsURLFlag.Name)
	}

	nodeCtx := nodeLocation.Context()
	// Onlt initialize the precompile for the zone chain
	if nodeCtx == common.ZONE_CTX {
		vm.InitializePrecompiles(nodeLocation)
	}
	return stack, cfg
}

func defaultNodeConfig() node.Config {
	cfg := node.DefaultConfig
	cfg.Name = ""
	cfg.Version = params.VersionWithCommit("", "")
	cfg.HTTPModules = append(cfg.HTTPModules, "eth")
	cfg.WSModules = append(cfg.WSModules, "eth")
	return cfg
}

// makeFullNode loads quai configuration and creates the Quai backend.
func makeFullNode(nodeLocation common.Location, logger *logrus.Logger) *node.Node {
	stack, cfg := makeConfigNode(nodeLocation, logger)
	backend, _ := RegisterQuaiService(stack, cfg.Quai, cfg.Node.NodeLocation.Context(), logger)
	sendfullstats := viper.GetBool(SendFullStatsFlag.Name)
	// Add the Quai Stats daemon if requested.
	if cfg.Quaistats.URL != "" {
		RegisterQuaiStatsService(stack, backend, cfg.Quaistats.URL, sendfullstats)
	}
	return stack
}

// RegisterQuaiService adds a Quai client to the stack.
// The second return value is the full node instance, which may be nil if the
// node is running as a light client.
func RegisterQuaiService(stack *node.Node, cfg quaiconfig.Config, nodeCtx int, logger *logrus.Logger) (quaiapi.Backend, error) {
	backend, err := quai.New(stack, &cfg, nodeCtx, logger)
	if err != nil {
		Fatalf("Failed to register the Quai service: %v", err)
	}
	return backend.APIBackend, nil
}

// RegisterQuaiStatsService configures the Quai Stats daemon and adds it to
// the given node.
func RegisterQuaiStatsService(stack *node.Node, backend quaiapi.Backend, url string, sendfullstats bool) {
	if err := quaistats.New(stack, backend, backend.Engine(), url, sendfullstats); err != nil {
		Fatalf("Failed to register the Quai Stats service: %v", err)
	}
}

// Fatalf formats a message to standard error and exits the program.
// The message is also printed to standard output if standard error
// is redirected to a different file.
func Fatalf(format string, args ...interface{}) {
	w := io.MultiWriter(os.Stdout, os.Stderr)
	if runtime.GOOS == "windows" {
		// The SameFile check below doesn't work on Windows.
		// stdout is unlikely to get redirected though, so just print there.
		w = os.Stdout
	} else {
		outf, _ := os.Stdout.Stat()
		errf, _ := os.Stderr.Stat()
		if outf != nil && errf != nil && os.SameFile(outf, errf) {
			w = os.Stderr
		}
	}
	fmt.Fprintf(w, "Fatal: "+format+"\n", args...)
	os.Exit(1)
}
