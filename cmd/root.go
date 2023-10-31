package cmd

import (
	"fmt"
	"strings"

	"github.com/adrg/xdg"
	"github.com/dominant-strategies/go-quai/config"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	PersistentPreRunE: runPersistenPreRunE,
}

func Execute() error {
	err := rootCmd.Execute()
	if err != nil {
		return err
	}
	return nil
}

func init() {

	// Location for default config directory
	defaultConfigDir := xdg.ConfigHome + "/" + config.APP_NAME + "/"
	rootCmd.PersistentFlags().StringP(config.CONFIG_DIR, "c", defaultConfigDir, "config directory"+generateEnvDoc(config.CONFIG_DIR))

	// Location for default runtime data directory
	defaultDataDir := xdg.DataHome + "/" + config.APP_NAME + "/"
	rootCmd.PersistentFlags().StringP(config.DATA_DIR, "d", defaultDataDir, "data directory"+generateEnvDoc(config.DATA_DIR))

	// Log level to use (trace, debug, info, warn, error, fatal, panic)
	rootCmd.PersistentFlags().StringP(config.LOG_LEVEL, "l", "info", "log level (trace, debug, info, warn, error, fatal, panic)"+generateEnvDoc(config.LOG_LEVEL))

	// When set to true saves or updates the config file with the current config parameters
	rootCmd.PersistentFlags().BoolP(config.SAVE_CONFIG_FILE, "S", false, "save/update config file with current config parameters"+generateEnvDoc(config.SAVE_CONFIG_FILE))

	// IP address for p2p networking
	rootCmd.PersistentFlags().StringP(config.IP_ADDR, "i", "0.0.0.0", "ip address to listen on"+generateEnvDoc(config.IP_ADDR))

	// p2p port for networking
	rootCmd.PersistentFlags().StringP(config.PORT, "p", "4001", "p2p port to listen on"+generateEnvDoc(config.PORT))

	// isBootNode when set to true starts p2p node as a DHT boostrap server (no static peers required).
	rootCmd.PersistentFlags().BoolP(config.BOOTNODE, "s", false, "start the node as a boot node (no static peers required)"+generateEnvDoc(config.BOOTNODE))

	// bootstrapPeers is a list of multiaddresses to bootstrap the DHT.
	rootCmd.PersistentFlags().StringSlice(config.BOOTSTRAP_PEERS, []string{}, "list of multiaddresses to bootstrap the DHT, i.e. /ip4/181.111.5.24/tcp/4001/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb"+generateEnvDoc(config.BOOTSTRAP_PEERS))

	// p2p options

	// enableNATService configures libp2p to provide a service to peers for determining
	// their reachability status. When enabled, the host will attempt to dial back to peers,
	// and then tell them if it was successful in making such connections.
	// See https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.31.0#EnableNATService
	rootCmd.PersistentFlags().Bool(config.NAT_SERVICE, true, "enable NAT service"+generateEnvDoc(config.NAT_SERVICE))

	// enableNATPortMap configures libp2p to open a port in network's firewall using UPnP.
	// See https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.31.0#NATPortMap
	rootCmd.PersistentFlags().Bool(config.NAT_PORTMAP, true, "enable NAT portmap"+generateEnvDoc(config.NAT_PORTMAP))

	// enableAutoRelayWithStaticRelays configures libp2p to enable the AutoRelay subsystem using the
	// provided relays as relay candidates. This subsystem performs automatic address rewriting to
	// advertise relay addresses when it detects that the node is publicly unreachable (e.g. behind a NAT).
	// See https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.31.0#EnableAutoRelayWithStaticRelays
	rootCmd.PersistentFlags().Bool(config.AUTO_RELAY_STATIC, false, "enable AutoRelay with Static Relays"+generateEnvDoc(config.AUTO_RELAY_STATIC))

	// enableHolePunching configures libp2p to enable NAT traversal by enabling NATT'd peers
	// to both initiate and respond to hole punching attempts to create direct/NAT-traversed
	// connections with other peers. NOTE: 'enableRelay' must be enabled for this option to work.
	// See https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.31.0#EnableHolePunching
	rootCmd.PersistentFlags().Bool(config.HOLE_PUNCHING, true, "enable Hole Punching"+generateEnvDoc(config.HOLE_PUNCHING))

	// ** Note: "EnableRelay" is enabled by default in libp2p.
	// enableRelay configures libp2p to enable the relay transport.
	// This option only configures libp2p to accept inbound connections from relays
	//  and make outbound connections_through_ relays when requested by the remote peer.
	// See https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.31.0#EnableRelay
	rootCmd.PersistentFlags().Bool(config.RELAY, true, "enable relay transport"+generateEnvDoc(config.RELAY))
}

func runPersistenPreRunE(cmd *cobra.Command, args []string) error {
	// set logger inmediately after parsing cobra flags
	logLevel := cmd.Flag(config.LOG_LEVEL).Value.String()
	log.ConfigureLogger(log.WithLevel(logLevel))
	// set config path to read config file
	configPath := cmd.Flag(config.CONFIG_DIR).Value.String()
	viper.SetConfigFile(configPath + config.CONFIG_FILE_NAME)
	viper.SetConfigType("yaml")
	// load config from file and environment variables
	config.InitConfig()
	// bind cobra flags to viper instance
	err := viper.BindPFlags(cmd.Flags())
	if err != nil {
		return fmt.Errorf("error binding flags: %s", err)
	}

	// if no bootstrap peers are provided, use the default ones defined in config/bootnodes.go
	bootstrapPeers := viper.GetStringSlice(config.BOOTSTRAP_PEERS)
	if len(bootstrapPeers) == 0 {
		log.Debugf("no bootstrap peers provided. Using default ones: %v", config.BootstrapPeers)
		viper.Set(config.BOOTSTRAP_PEERS, config.BootstrapPeers)
	}

	// save config file if SAVE_CONFIG_FILE flag is set to true
	saveConfigFile := viper.GetBool(config.SAVE_CONFIG_FILE)
	if saveConfigFile {
		err := config.SaveConfig()
		if err != nil {
			log.Errorf("error saving config file: %s . Skipping...", err)
		} else {
			log.Debugf("config file saved successfully")
		}
	}

	log.Tracef("config options loaded: %+v", viper.AllSettings())
	return nil
}

// helper function that given a cobra flag name, returns the corresponding
// help legend for the equivalent environment variable
func generateEnvDoc(flag string) string {
	envVar := config.ENV_PREFIX + "_" + strings.ReplaceAll(strings.ToUpper(flag), "-", "_")
	return fmt.Sprintf(" [%s]", envVar)
}
