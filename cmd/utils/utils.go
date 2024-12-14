package utils

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/constants"
	"github.com/dominant-strategies/go-quai/log"
)

// InitConfig initializes the viper config instance ensuring that environment variables
// take precedence over config file parameters.
// Environment variables should be prefixed with the application name (e.g. QUAI_LOG_LEVEL).
// If the flag SAVE_CONFIG_FILE is set to true, the config file will be saved or updated with the current config parameters.
// It panics if an error occurs while reading the config file.
func InitConfig() {
	// read in config file and merge with defaults
	log.Global.Infof("Loading config from file: %s", viper.ConfigFileUsed())
	err := viper.ReadInConfig()
	if err != nil {
		// if error is type ConfigFileNotFoundError or fs.PathError, ignore error
		if _, ok := err.(*fs.PathError); ok || errors.Is(err, viper.ConfigFileNotFoundError{}) {
			log.Global.Warnf("Config file not found: %s", viper.ConfigFileUsed())
		} else {
			log.Global.Errorf("Error reading config file: %s", err)
			// config file was found but another error was produced. Cannot continue
			panic(err)
		}
	}
	if !viper.IsSet(BootPeersFlag.Name) {
		network := viper.GetString(EnvironmentFlag.Name)
		bootpeers := common.BootstrapPeers[network]
		log.Global.Debugf("No bootpeers specified. Using defaults for %s: %s", network, bootpeers)
		viper.Set(BootPeersFlag.Name, bootpeers)
	}

	log.Global.Infof("Loading config from environment variables with prefix: '%s_'", constants.ENV_PREFIX)
	viper.SetEnvPrefix(constants.ENV_PREFIX)
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_")) // Replace hyphens with underscores for env variables
}
