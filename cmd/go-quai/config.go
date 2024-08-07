package main

import (
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
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
	log.Global.Info("Creating default config file")
	return nil
}
