package utils

import (
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/node"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai"
	"github.com/dominant-strategies/go-quai/quai/quaiconfig"
	"github.com/dominant-strategies/go-quai/quai/tracers"
	"github.com/dominant-strategies/go-quai/quaistats"
	"github.com/syndtr/goleveldb/leveldb"
)

func OpenBackendDB() (*leveldb.DB, error) {
	dataDir := viper.GetString(DataDirFlag.Name)
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		err := os.MkdirAll(dataDir, 0755)
		if err != nil {
			log.Global.Errorf("error creating data directory: %s", err)
			return nil, err
		}
	}
	dbPath := path.Join(dataDir, "quaibackend")

	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		return nil, err
	}
	return db, err
}

// GetRunningZones returns the slices that are processing state (which are only zones)
func GetRunningZones() []common.Location {
	slices := strings.Split(viper.GetString(SlicesRunningFlag.Name), ",")

	// Sanity checks
	if slices[0] == "" {
		Fatalf("no slices are specified")
	}
	runningSlices := []common.Location{}
	for _, slice := range slices {
		location := common.Location{slice[1] - 48, slice[3] - 48}
		if location.Region() > common.MaxRegions || location.Zone() > common.MaxZones {
			Fatalf("invalid slice: %s", location)
		}
		runningSlices = append(runningSlices, location)
	}
	return runningSlices
}

// getRegionsRunning returns the regions running
func GetRunningRegions(runningSlices []common.Location) []byte {
	runningRegions := []byte{}
	for _, slice := range runningSlices {
		if !slices.Contains(runningRegions, slice[0]) {
			runningRegions = append(runningRegions, slice[0])
		}
	}
	return runningRegions
}

func StartNode(stack *node.Node) {
	if err := stack.Start(); err != nil {
		Fatalf("Error starting protocol stack: %v", err)
	}
	// TODO: Stop the node if the memory pressure
}

// makeConfigNode loads quai configuration and creates a blank node instance.
func makeConfigNode(slicesRunning []common.Location, nodeLocation common.Location, currentExpansionNumber uint8, logger *log.Logger) (*node.Node, quaiconfig.QuaiConfig) {
	// Load defaults.
	cfg := quaiconfig.QuaiConfig{
		Quai:    quaiconfig.Defaults,
		Node:    defaultNodeConfig(),
		Metrics: metrics_config.DefaultConfig,
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
	SetQuaiConfig(stack, &cfg.Quai, slicesRunning, nodeLocation, currentExpansionNumber, logger)

	cfg.Quaistats.URL = viper.GetString(QuaiStatsURLFlag.Name)

	return stack, cfg
}

func defaultNodeConfig() node.Config {
	cfg := node.DefaultConfig
	cfg.Name = ""
	cfg.Version = params.VersionWithCommit("", "")
	cfg.HTTPModules = append(cfg.HTTPModules, "quai", "net")
	cfg.WSModules = append(cfg.WSModules, "quai")
	return cfg
}

// makeFullNode loads quai configuration and creates the Quai backend.
func makeFullNode(p2p quai.NetworkingAPI, nodeLocation common.Location, slicesRunning []common.Location, currentExpansionNumber uint8, genesisBlock *types.WorkObject, logger *log.Logger) (*node.Node, quaiapi.Backend) {
	stack, cfg := makeConfigNode(slicesRunning, nodeLocation, currentExpansionNumber, logger)
	startingExpansionNumber := viper.GetUint64(StartingExpansionNumberFlag.Name)
	backend, err := RegisterQuaiService(stack, p2p, cfg.Quai, cfg.Node.NodeLocation.Context(), currentExpansionNumber, startingExpansionNumber, genesisBlock, logger)
	if err != nil {
		log.Global.WithField("err", err).Error("Unable to create full node")
		return nil, nil
	}
	sendfullstats := viper.GetBool(SendFullStatsFlag.Name)
	// Add the Quai Stats daemon if requested.
	if cfg.Quaistats.URL != "" && backend.ProcessingState() {
		RegisterQuaiStatsService(stack, backend, cfg.Quaistats.URL, sendfullstats)
	}

	stack.RegisterAPIs(tracers.APIs(backend))
	return stack, backend
}

// RegisterQuaiService adds a Quai client to the stack.
// The second return value is the full node instance, which may be nil if the
// node is running as a light client.
func RegisterQuaiService(stack *node.Node, p2p quai.NetworkingAPI, cfg quaiconfig.Config, nodeCtx int, currentExpansionNumber uint8, startingExpansionNumber uint64, genesisBlock *types.WorkObject, logger *log.Logger) (quaiapi.Backend, error) {
	backend, err := quai.New(stack, p2p, &cfg, nodeCtx, currentExpansionNumber, startingExpansionNumber, genesisBlock, logger, viper.GetInt(WSMaxSubsFlag.Name))
	if err != nil {
		Fatalf("Failed to register the Quai service: %v", err)
	}
	return backend.APIBackend, nil
}

// RegisterQuaiStatsService configures the Quai Stats daemon and adds it to
// the given node.
func RegisterQuaiStatsService(stack *node.Node, backend quaiapi.Backend, url string, sendfullstats bool) {
	if err := quaistats.New(stack, backend, url, sendfullstats); err != nil {
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
