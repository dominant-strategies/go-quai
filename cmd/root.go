package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/options"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/constants"
	"github.com/dominant-strategies/go-quai/log"
)

var rootCmd = &cobra.Command{
	PersistentPreRunE: rootCmdPreRun,
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
	defaultConfigDir := xdg.ConfigHome + "/" + constants.APP_NAME + "/"
	rootCmd.PersistentFlags().StringP(options.CONFIG_DIR, "c", defaultConfigDir, "config directory"+generateEnvDoc(options.CONFIG_DIR))

	// Location for default runtime data directory
	defaultDataDir := xdg.DataHome + "/" + constants.APP_NAME + "/"
	rootCmd.PersistentFlags().StringP(options.DATA_DIR, "d", defaultDataDir, "data directory"+generateEnvDoc(options.DATA_DIR))

	// Log level to use (trace, debug, info, warn, error, fatal, panic)
	rootCmd.PersistentFlags().StringP(options.LOG_LEVEL, "l", "info", "log level (trace, debug, info, warn, error, fatal, panic)"+generateEnvDoc(options.LOG_LEVEL))

	// When set to true saves or updates the config file with the current config parameters
	rootCmd.PersistentFlags().BoolP(options.SAVE_CONFIG_FILE, "S", false, "save/update config file with current config parameters"+generateEnvDoc(options.SAVE_CONFIG_FILE))
}

func rootCmdPreRun(cmd *cobra.Command, args []string) error {
	// set logger inmediately after parsing cobra flags
	logLevel := cmd.Flag(options.LOG_LEVEL).Value.String()
	log.ConfigureLogger(log.WithLevel(logLevel))
	// set config path to read config file
	configDir := cmd.Flag(options.CONFIG_DIR).Value.String()
	viper.SetConfigFile(configDir + constants.CONFIG_FILE_NAME)
	viper.SetConfigType("yaml")
	// load config from file and environment variables
	common.InitConfig()
	// bind cobra flags to viper instance
	err := viper.BindPFlags(cmd.Flags())
	if err != nil {
		return fmt.Errorf("error binding flags: %s", err)
	}

	// Make sure data dir and config dir exist
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return err
		}
	}

	// save config file if SAVE_CONFIG_FILE flag is set to true
	saveConfigFile := viper.GetBool(options.SAVE_CONFIG_FILE)
	if saveConfigFile {
		err := common.SaveConfig()
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
	envVar := constants.ENV_PREFIX + "_" + strings.ReplaceAll(strings.ToUpper(flag), "-", "_")
	return fmt.Sprintf(" [%s]", envVar)
}
