package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common/constants"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "creates the default config file",
	Long: `creates the default config file in the location specified by the --config-dir flag. 
			The default config file will contain all the default values for the flags.
			Any flags passed in the command line here will also overwrite the default values in the config file.`,
	RunE:                       runConfig,
	SilenceUsage:               true,
	SuggestionsMinimumDistance: 2,
	Example:                    `go-quai start -log-level=debug`,
	PreRunE:                    startCmdPreRun,
}

func init() {
	rootCmd.AddCommand(configCmd)

	for _, flagGroup := range utils.Flags {
		for _, flag := range flagGroup {
			utils.CreateAndBindFlag(flag, configCmd)
		}
	}
}

func configCmdPreRun(cmd *cobra.Command, args []string) error {
	// set keyfile path
	if viper.GetString(utils.KeyFileFlag.Name) == "" {
		configDir := cmd.Flag(utils.ConfigDirFlag.Name).Value.String()
		viper.Set(utils.KeyFileFlag.Name, filepath.Join(configDir, "private.key"))
	}
	// Initialize the version info
	params.InitVersion()
	return nil
}

func runConfig(cmd *cobra.Command, args []string) error {
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
		log.Global.Debugf("Config directory created: %s", configDir)
	} else if err != nil {
		log.Global.Fatalf("Error accessing config directory: %s, Error: %v", configDir, err)
	}
	if _, err := os.Stat(filepath.Join(configDir, constants.CONFIG_FILE_NAME)); os.IsNotExist(err) {
		err := utils.WriteDefaultConfigFile(configDir, constants.CONFIG_FILE_NAME, constants.CONFIG_FILE_TYPE)
		if err != nil {
			return err
		}
		log.Global.WithField("path", filepath.Join(configDir, constants.CONFIG_FILE_NAME)).Info("Initialized new config file.")
	} else {
		log.Global.WithField("path", filepath.Join(configDir, constants.CONFIG_FILE_NAME)).Fatal("Cannot init config file. File already exists. Either remove this option to run with the existing config file, or delete the existing config file to re-initialize a new one.")
	}
	return nil
}
