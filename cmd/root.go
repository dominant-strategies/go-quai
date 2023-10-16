package cmd

import (
	"os"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	// location for default config file
	defaultConfigFilePath = "../config.yaml"
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
	rootCmd.PersistentFlags().StringP("config", "c", "", "config file")
	rootCmd.PersistentFlags().StringP("loglevel", "l", "info", "log level (trace, debug, info, warn, error, fatal, panic")

	viper.BindPFlag("loglevel", rootCmd.PersistentFlags().Lookup("loglevel"))

	cobra.MarkFlagFilename(rootCmd.PersistentFlags(), "config")
	cobra.MarkFlagFilename(rootCmd.PersistentFlags(), "keystore")

	viper.BindPFlag("keystore", rootCmd.PersistentFlags().Lookup("keystore"))

	// flag to load node's ip address
	rootCmd.PersistentFlags().StringP("ipaddr", "i", "0.0.0.0", "ip address to listen on")
	viper.BindPFlag("ipaddr", rootCmd.PersistentFlags().Lookup("ipaddr"))

	// flag to load node's p2p port
	rootCmd.PersistentFlags().StringP("port", "p", "4001", "p2p port to listen on")
	viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port"))

	// flag to load node's private key
	rootCmd.PersistentFlags().StringP("privkey", "k", "private.key", "private key file")
	viper.BindPFlag("privkey", rootCmd.PersistentFlags().Lookup("privkey"))

	// flag to load list of bootstrap peers
	rootCmd.PersistentFlags().StringSliceP("bootstrap", "b", []string{}, "list of bootstrap peers. Syntax: <multiaddress1>,<multiaddress2>,...")
	viper.BindPFlag("bootstrap", rootCmd.PersistentFlags().Lookup("bootstrap"))

	// boolean flag to start p2p node as a DHT boostrap server
	rootCmd.PersistentFlags().BoolP("server", "s", false, "start as a bootstrap server")
	viper.BindPFlag("server", rootCmd.PersistentFlags().Lookup("server"))

}

func runPersistenPreRunE(cmd *cobra.Command, args []string) error {
	err := loadConfigFromFile()
	if err != nil {
		return err
	}
	viper.AutomaticEnv()
	// set log level
	log.ConfigureLogger(log.WithLevel(viper.GetString("loglevel")))
	return nil

}

// Loads go-quai configuration from a yaml file
func loadConfigFromFile() error {
	cfgFile := viper.GetString("config")
	var configFilePath string
	if cfgFile != "" {
		configFilePath = cfgFile
	} else {
		configFilePath = defaultConfigFilePath
	}
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		return nil
	}
	viper.SetConfigFile(configFilePath)
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		return errors.Wrap(err, "failed to read config file")
	}
	return nil
}
