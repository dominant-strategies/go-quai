package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

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
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
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
	// set config path to read config file
	configDir := cmd.Flag(utils.ConfigDirFlag.Name).Value.String()

	// Make sure configDir is a valid directory using path/filepath
	// filepath.Clean returns the shortest path name equivalent to path by purely lexical processing
	configDir = filepath.Clean(configDir)

	_, err := os.Stat(configDir)
	if err != nil && os.IsNotExist(err) {
		// If the directory does not exist, create it
		if err := os.MkdirAll(configDir, 0755); err != nil {
			log.Global.Fatalf("Failed to create config directory: %s, Error: %v", configDir, err)
		}
		log.Global.Debug("Config directory created: %s", configDir)
	} else if err != nil {
		log.Global.Fatalf("Error accessing config directory: %s, Error: %v", configDir, err)
	}

	viper.SetConfigFile(filepath.Join(configDir, constants.CONFIG_FILE_NAME))
	viper.SetConfigType(constants.CONFIG_FILE_TYPE)

	// Write default config file if it does not exist
	if _, err := os.Stat(filepath.Join(configDir, constants.CONFIG_FILE_NAME)); os.IsNotExist(err) {
		err := utils.WriteDefaultConfigFile(configDir, constants.CONFIG_FILE_NAME, constants.CONFIG_FILE_TYPE)
		if err != nil {
			return err
		}
		log.Global.WithField("path", filepath.Join(configDir, constants.CONFIG_FILE_NAME)).Info("Default config file created")
	}

	// load config from file and environment variables
	utils.InitConfig()

	// set logger inmediately after parsing cobra flags
	logLevel := viper.GetString(utils.LogLevelFlag.Name)
	log.SetGlobalLogger("", logLevel)

	// bind cobra flags to viper instance
	err = viper.BindPFlags(cmd.Flags())
	if err != nil {
		return fmt.Errorf("error binding flags: %s", err)
	}

	// Make sure data dir and config dir exist
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return err
		}
	}

	// Check that environment is local, colosseum, garden, lighthouse, dev, or orchard
	environment := viper.GetString(utils.EnvironmentFlag.Name)
	if !utils.IsValidEnvironment(environment) {
		log.Global.Fatalf("invalid environment: %s", environment)
	}

	log.Global.WithField("options", viper.AllSettings()).Debug("config options loaded")
	return nil
}
