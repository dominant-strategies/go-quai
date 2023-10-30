package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dominant-strategies/go-quai/consensus/quai"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/spf13/cobra"

	p2pnode "github.com/dominant-strategies/go-quai/p2p/node"
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
	Example:                    `go-quai start -loglevel=debug`,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	log.Infof("Starting go-quai")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// create a new p2p node
	node, err := p2pnode.NewNode(ctx)
	if err != nil {
		log.Fatalf("error creating node: %s", err)
	}

	// create instance of consensus backend
	consensus, err := quai.NewQuaiBackend()
	if err != nil {
		log.Fatalf("error creating consensus backend: %s", err)
	}

	// start the consensus backend
	consensus.SetP2PClient(node)
	if err := consensus.Start(); err != nil {
		log.Fatalf("error starting consensus backend: %s", err)
	}

	// start the p2p node
	node.SetConsensusBackend(consensus)
	if err := node.Start(); err != nil {
		log.Fatalf("error starting node: %s", err)
	}

	// wait for a SIGINT or SIGTERM signal
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Warnf("Received 'stop' signal, shutting down gracefully...")
	cancel()
	if err := node.Shutdown(); err != nil {
		panic(err)
	}
	log.Warnf("Node is offline")
	return nil
}
