package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/options"
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
	Example:                    `go-quai start -loglevel=debug`,
	PreRunE:                    startCmdPreRun,
}

func init() {
	rootCmd.AddCommand(startCmd)

	// IP address for p2p networking
	startCmd.PersistentFlags().StringP(options.IP_ADDR, "i", "0.0.0.0", "ip address to listen on"+generateEnvDoc(options.IP_ADDR))
	viper.BindPFlag(options.IP_ADDR, startCmd.PersistentFlags().Lookup(options.IP_ADDR))

	// p2p port for networking
	startCmd.PersistentFlags().StringP(options.PORT, "p", "4001", "p2p port to listen on"+generateEnvDoc(options.PORT))
	viper.BindPFlag(options.PORT, startCmd.PersistentFlags().Lookup(options.PORT))

	// isBootNode when set to true starts p2p node as a DHT boostrap server (no static peers required).
	startCmd.PersistentFlags().BoolP(options.BOOTNODE, "b", false, "start the node as a boot node (no static peers required)"+generateEnvDoc(options.BOOTNODE))
	viper.BindPFlag(options.BOOTNODE, startCmd.PersistentFlags().Lookup(options.BOOTNODE))

	// initial peers to connect to and use for bootstrapping purposes
	startCmd.PersistentFlags().StringSliceP(options.BOOTPEERS, "", []string{}, "list of bootstrap peers. Syntax: <multiaddress1>,<multiaddress2>,...")
	viper.BindPFlag(options.BOOTPEERS, startCmd.PersistentFlags().Lookup(options.BOOTPEERS))

	// enableNATPortMap configures libp2p to attempt to open a port in network's firewall using UPnP.
	// See https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.31.0#NATPortMap
	startCmd.PersistentFlags().Bool(options.PORTMAP, true, "enable NAT portmap"+generateEnvDoc(options.PORTMAP))
	viper.BindPFlag(options.PORTMAP, startCmd.PersistentFlags().Lookup(options.PORTMAP))

	// path to file containing node private key
	startCmd.PersistentFlags().StringP(options.KEYFILE, "k", "", "file containing node private key"+generateEnvDoc(options.KEYFILE))
	viper.BindPFlag(options.KEYFILE, startCmd.PersistentFlags().Lookup(options.KEYFILE))

	// look for more peers until we have at least min-peers
	startCmd.PersistentFlags().StringP(options.MIN_PEERS, "", "5", "minimum number of peers to maintain connectivity with"+generateEnvDoc(options.MIN_PEERS))
	viper.BindPFlag(options.MIN_PEERS, startCmd.PersistentFlags().Lookup(options.MIN_PEERS))

	// stop looking for more peers once we've reached max-peers
	startCmd.PersistentFlags().StringP(options.MAX_PEERS, "", "50", "maximum number of peers to maintain connectivity with"+generateEnvDoc(options.MAX_PEERS))
	viper.BindPFlag(options.MAX_PEERS, startCmd.PersistentFlags().Lookup(options.MAX_PEERS))
	
	// location ID
	startCmd.PersistentFlags().StringP(options.LOCATION, "", "", "region and zone location"+generateEnvDoc(options.LOCATION))
}

func startCmdPreRun(cmd *cobra.Command, args []string) error {
	// set keyfile path
	if "" == viper.GetString(options.KEYFILE) {
		configDir := cmd.Flag(options.CONFIG_DIR).Value.String()
		viper.Set(options.KEYFILE, configDir+"private.key")
	}

	// if no bootstrap peers are provided, use the default ones defined in config/bootnodes.go
	if bootstrapPeers := viper.GetStringSlice(options.BOOTPEERS); len(bootstrapPeers) == 0 {
		log.Debugf("no bootstrap peers provided. Using default ones: %v", common.BootstrapPeers)
		viper.Set(options.BOOTPEERS, common.BootstrapPeers)
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
