package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
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
	for _, flag := range utils.GlobalFlags {
		utils.CreateAndBindFlag(flag, rootCmd)
	}
}

func rootCmdPreRun(cmd *cobra.Command, args []string) error {
	// set logger inmediately after parsing cobra flags
	logLevel := cmd.Flag(utils.LogLevelFlag.Name).Value.String()
	log.SetGlobalLogger("", logLevel)
	// set config path to read config file
	configDir := cmd.Flag(utils.ConfigDirFlag.Name).Value.String()
	viper.SetConfigFile(configDir + constants.CONFIG_FILE_NAME)
	viper.SetConfigType("yaml")
	// load config from file and environment variables
	utils.InitConfig()
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
	saveConfigFile := viper.GetBool(utils.SaveConfigFlag.Name)
	if saveConfigFile {
		err := utils.SaveConfig()
		if err != nil {
			log.WithField("error", err).Error("error saving config file. Skipping...")
		} else {
			log.Debug("config file saved successfully")
		}
	}
	log.WithField("options", viper.AllSettings()).Debug("config options loaded")
	return nil
}
