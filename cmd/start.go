package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/client"
	p2pnode "github.com/dominant-strategies/go-quai/p2p/node"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "starts the go-quai daemon",
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

	// configure flag for http port
	startCmd.Flags().StringP("http-port", "t", "8080", "http port to listen on")
	viper.BindPFlag("http-port", startCmd.Flags().Lookup("http-port"))

}

func runStart(cmd *cobra.Command, args []string) error {
	log.Infof("Starting go-quai daemon")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// create a new p2p node
	node, err := p2pnode.NewNode(ctx)
	if err != nil {
		log.Fatalf("error creating node: %s", err)
	}

	// Start discovery services
	if err := node.Start(); err != nil {
		log.Fatalf("error starting node: %s", err)
	}

	// Start listening for events
	go node.ListenForEvents()

	client := client.NewClient(ctx, node)

	// start the http server
	go func() {
		httpPort := viper.GetString("http-port")
		if err := client.StartServer(httpPort); err != nil {
			log.Fatalf("error starting http server: %s", err)
			os.Exit(1)
		}
	}()

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
