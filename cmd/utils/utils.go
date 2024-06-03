package utils

import (
	"errors"
	"io/fs"

	"github.com/spf13/viper"

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

	log.Global.Infof("Loading config from environment variables with prefix: '%s_'", constants.ENV_PREFIX)
	viper.SetEnvPrefix(constants.ENV_PREFIX)
	viper.AutomaticEnv()
}
