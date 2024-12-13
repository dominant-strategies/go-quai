package utils

import (
	"os"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common/constants"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testXDGConfigLoading tests the loading of the config file from the XDG config home
// and verifies values are correctly set in viper.
// This test is nested within the TestCobraFlagConfigLoading test.
func testXDGConfigLoading(t *testing.T) {
	// set configPath to a mock temporary XDG config folder
	mockConfigPath := "/tmp/xdg_config_home/"
	tempFile := createMockXDGConfigFile(t, mockConfigPath)
	defer tempFile.Close()
	defer os.RemoveAll(mockConfigPath)

	// write 'log-level = debug' config to mock config.yaml file
	_, err := tempFile.WriteString(LogLevelFlag.Name + " = " + "\"debug\"\n")
	require.NoError(t, err)

	// Set config path to the temporary config directory
	viper.SetConfigFile(tempFile.Name())

	InitConfig()

	// Assert log level is set to "debug" as per the mock config file
	assert.Equal(t, "debug", viper.GetString(LogLevelFlag.Name))
}

// TestCobraFlagConfigLoading tests the loading of the config file from the XDG config home,
// the loading of the environment variable and the loading of the cobra flag.
// It verifies the expected order of precedence of config loading.
func TestCobraFlagConfigLoading(t *testing.T) {
	// Clear viper instance to simulate a fresh start
	viper.Reset()
	viper.AutomaticEnv()
	viper.SetEnvPrefix(constants.ENV_PREFIX)
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_")) // Replace hyphens with underscores

	// Test loading config from XDG config home
	testXDGConfigLoading(t)
	assert.Equal(t, "debug", viper.GetString(LogLevelFlag.Name))

	// Test loading config from environment variable
	err := os.Setenv(constants.ENV_PREFIX+"_"+"LOG_LEVEL", "error")
	defer os.Unsetenv(constants.ENV_PREFIX + "_" + "LOG_LEVEL")
	require.NoError(t, err)
	assert.Equal(t, "error", viper.GetString("LOG_LEVEL"))

	// Test loading config from cobra flag
	rootCmd := &cobra.Command{}
	rootCmd.PersistentFlags().StringP(LogLevelFlag.Name, "l", "warn", "log level (trace, debug, info, warn, error, fatal, panic")
	err = rootCmd.PersistentFlags().Set(LogLevelFlag.Name, "trace")
	require.NoError(t, err)
	viper.BindPFlags(rootCmd.PersistentFlags())
	assert.Equal(t, "trace", viper.GetString(LogLevelFlag.Name))

}

// helper function to create a mock XDG directory and config file
func createMockXDGConfigFile(t *testing.T, dir string) *os.File {
	t.Helper()
	err := os.MkdirAll(dir, 0755)
	require.NoError(t, err)
	tmpFile, err := os.Create(dir + constants.CONFIG_FILE_NAME)
	require.NoError(t, err)
	return tmpFile
}
