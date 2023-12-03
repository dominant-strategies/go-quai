package common

import (
	"os"
	"testing"

	"github.com/dominant-strategies/go-quai/cmd/options"
	"github.com/dominant-strategies/go-quai/common/constants"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that the config file is saved or updated with the current config parameters.
func TestSaveConfig(t *testing.T) {
	// set configPath to a temporary mocked XDG config folder
	mockConfigPath := "/tmp/xdg_config_home/"
	tempFile := createMockXDGConfigFile(t, mockConfigPath)
	defer tempFile.Close()
	defer os.RemoveAll(mockConfigPath)
	// write LOG_LEVEL config to mock config.yaml file
	_, err := tempFile.WriteString(options.LOG_LEVEL + " : " + "debug\n")
	require.NoError(t, err)
	// Clear viper instance to simulate a fresh start
	viper.Reset()

	// Set config path to the temporary config directory
	viper.SetConfigFile(tempFile.Name())

	// Set PORT to 8080 as environment variable
	err = os.Setenv("GO_QUAI_PORT", "8080")
	require.NoError(t, err)
	defer os.Unsetenv("GO_QUAI_PORT")
	InitConfig()
	// Save config file
	err = SaveConfig()
	require.NoError(t, err)
	// Load config from mock file into viper and assert that the new config parameters were saved
	err = viper.ReadInConfig()
	require.NoError(t, err)
	assert.Equal(t, "8080", viper.GetString(options.PORT))
	// Assert a .bak config file was created
	backupFile, err := os.Stat(mockConfigPath + constants.CONFIG_FILE_NAME + ".bak")
	assert.False(t, os.IsNotExist(err))
	assert.Equal(t, constants.CONFIG_FILE_NAME+".bak", backupFile.Name())
}

// testXDGConfigLoading tests the loading of the config file from the XDG config home
// and verifies values are correctly set in viper.
// This test is nested within the TestCobraFlagConfigLoading test.
func testXDGConfigLoading(t *testing.T) {
	// set configPath to a mock temporary XDG config folder
	mockConfigPath := "/tmp/xdg_config_home/"
	tempFile := createMockXDGConfigFile(t, mockConfigPath)
	defer tempFile.Close()
	defer os.RemoveAll(mockConfigPath)

	// write 'LOG_LEVEL=debug' config to mock config.yaml file
	_, err := tempFile.WriteString(options.LOG_LEVEL + " : " + "debug\n")
	require.NoError(t, err)

	// Set config path to the temporary config directory
	viper.SetConfigFile(tempFile.Name())

	InitConfig()

	// Assert log level is set to "debug" as per the mock config file
	assert.Equal(t, "debug", viper.GetString(options.LOG_LEVEL))
}

// TestCobraFlagConfigLoading tests the loading of the config file from the XDG config home,
// the loading of the environment variable and the loading of the cobra flag.
// It verifies the expected order of precedence of config loading.
func TestCobraFlagConfigLoading(t *testing.T) {

	// Clear viper instance to simulate a fresh start
	viper.Reset()

	// Test loading config from XDG config home
	testXDGConfigLoading(t)
	assert.Equal(t, "debug", viper.GetString(options.LOG_LEVEL))

	// Test loading config from environment variable
	err := os.Setenv(constants.ENV_PREFIX+"_"+"LOG-LEVEL", "error")
	defer os.Unsetenv(constants.ENV_PREFIX + "_" + "LOG-LEVEL")
	require.NoError(t, err)
	assert.Equal(t, "error", viper.GetString(options.LOG_LEVEL))

	// Test loading config from cobra flag
	rootCmd := &cobra.Command{}
	rootCmd.PersistentFlags().StringP(options.LOG_LEVEL, "l", "warn", "log level (trace, debug, info, warn, error, fatal, panic")
	err = rootCmd.PersistentFlags().Set(options.LOG_LEVEL, "trace")
	require.NoError(t, err)
	viper.BindPFlags(rootCmd.PersistentFlags())
	assert.Equal(t, "trace", viper.GetString(options.LOG_LEVEL))

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
