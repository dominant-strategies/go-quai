package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/quai"
	"github.com/dominant-strategies/go-quai/log"
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

	// Create and bind all rpc flags to the start command
	for _, flag := range utils.RPCFlags {
		utils.CreateAndBindFlag(flag, startCmd)
	}

}

func startCmdPreRun(cmd *cobra.Command, args []string) error {
	// set keyfile path
	if viper.GetString(utils.KeyFileFlag.Name) == "" {
		configDir := cmd.Flag(utils.ConfigDirFlag.Name).Value.String()
		viper.Set(utils.KeyFileFlag.Name, configDir+"private.key")
	}

	// if no bootstrap peers are provided, use the default ones defined in config/bootnodes.go
	if bootstrapPeers := viper.GetStringSlice(utils.BootPeersFlag.Name); len(bootstrapPeers) == 0 {
		log.Debugf("no bootstrap peers provided. Using default ones: %v", common.BootstrapPeers)
		viper.Set(utils.BootPeersFlag.Name, common.BootstrapPeers)
	}
	return nil
}

func runStart(cmd *cobra.Command, args []string) error {
	log.Infof("Starting go-quai")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// create a new p2p node
	node, err := node.NewNode(ctx)
	if err != nil {
		log.Fatalf("error creating node: %s", err)
	}

	// create instance of consensus backend
	consensus, err := quai.NewQuaiBackend()
	if err != nil {
		log.Fatalf("error creating consensus backend: %s", err)
	}

	// start the consensus backend
	consensus.SetP2PNode(node)
	if err := consensus.Start(); err != nil {
		log.Fatalf("error starting consensus backend: %s", err)
	}

	// start the p2p node
	node.SetConsensusBackend(consensus)
	if err := node.Start(); err != nil {
		log.Fatalf("error starting node: %s", err)
	}

	// subscribe to necessary protocol events
	if err := node.StartGossipSub(ctx); err != nil {
		log.Fatalf("error starting gossipsub: %s", err)
	}

	// wait for a SIGINT or SIGTERM signal
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Warnf("Received 'stop' signal, shutting down gracefully...")
	cancel()
	if err := node.Stop(); err != nil {
		panic(err)
	}
	log.Warnf("Node is offline")
	return nil
}
