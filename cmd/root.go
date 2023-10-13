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

var (
	cfgFile  string
	logLevel string
)

func Execute() error {
	err := rootCmd.Execute()
	if err != nil {
		return err
	}
	return nil
}

func init() {
	viper.SetDefault("loglevel", "info")

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "loglevel", "l", "", "log level (trace, debug, info, warn, error, fatal, panic")

	viper.BindPFlag("loglevel", rootCmd.PersistentFlags().Lookup("loglevel"))

	cobra.MarkFlagFilename(rootCmd.PersistentFlags(), "config")
	cobra.MarkFlagFilename(rootCmd.PersistentFlags(), "keystore")

	viper.BindPFlag("keystore", rootCmd.PersistentFlags().Lookup("keystore"))

	// config flag to load node's private key
	rootCmd.PersistentFlags().StringP("privkey", "k", "private.key", "private key file")
	viper.BindPFlag("privkey", rootCmd.PersistentFlags().Lookup("privkey"))

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

// loads go-quai configuration from a yaml file
func loadConfigFromFile() error {
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
