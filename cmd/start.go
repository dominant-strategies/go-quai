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
	Args:                       cobra.RangeArgs(0, 2),
	Example:                    `go-quai start -loglevel=debug`,
}

func init() {
	rootCmd.AddCommand(startCmd)
	// configure flag for p2p port
	startCmd.Flags().StringP("p2p-port", "p", "4001", "p2p port to listen on")
	viper.BindPFlag("p2p-port", startCmd.Flags().Lookup("p2p-port"))
	// configure flag for http port
	startCmd.Flags().StringP("http-port", "t", "8080", "http port to listen on")
	viper.BindPFlag("http-port", startCmd.Flags().Lookup("http-port"))

	// configure flag to start as a boostrap server
	startCmd.Flags().BoolP("server", "s", false, "start as a bootstrap server")
	viper.BindPFlag("server", startCmd.Flags().Lookup("server"))
}

func runStart(cmd *cobra.Command, args []string) error {
	log.Infof("Starting go-quai daemon")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ipaddr := "0.0.0.0"
	p2pPort := viper.GetString("p2p-port")
	privKeyFile := viper.GetString("privkey")
	node, err := p2pnode.NewNode(ctx, ipaddr, p2pPort, privKeyFile)
	if err != nil {
		log.Fatalf("error creating node: %s", err)
	}

	// log the p2p node's ID
	log.Infof("node created: %s", node.ID().Pretty())
	// log the p2p node's listening addresses
	for _, addr := range node.Addrs() {
		log.Infof("listening on: %s", addr)
	}

	// initialize the DHT
	if err := node.InitializeDHT(); err != nil {
		log.Fatalf("error initializing DHT: %s", err)
		os.Exit(1)
	}

	// if the node is not a bootstrap server, bootstrap the DHT
	if !viper.GetBool("server") {
		log.Infof("bootstrapping DHT...")
		if err := node.BootstrapDHT(); err != nil {
			log.Fatalf("error bootstrapping DHT: %s", err)
			os.Exit(1)
		}
	} else {
		log.Infof("starting node as bootstrap server")
	}

	// initialize mDNS discovery
	if err := node.InitializeMDNS(); err != nil {
		log.Fatalf("error initializing mDNS discovery: %s", err)
		os.Exit(1)
	}

	client := client.NewClient(ctx, node)

	// start the http server
	go func() {
		httpPort := viper.GetString("http-port")
		if err := client.StartServer(httpPort); err != nil {
			log.Fatalf("error starting http server: %s", err)
			os.Exit(1)
		}
	}()

	// Start listening for events
	go client.ListenForEvents()

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
