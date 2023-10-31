package config

import (
	"os"
	"testing"

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

	// write LOG_LEVEL config to mock config.yaml file
	_, err := tempFile.WriteString(LOG_LEVEL + " : " + "debug\n")
	require.NoError(t, err)

	// Clear viper instance to simulate a fresh start
	viper.Reset()
	// Set config path to the temporary config directory
	viper.SetConfigFile(tempFile.Name())

	InitConfig()

	// Assert log level is set to "debug" as per the mock config file
	assert.Equal(t, "debug", viper.GetString(LOG_LEVEL))
}

// Verifies that the config file is saved or updated with the current config parameters.
func TestUpdateConfigFile(t *testing.T) {
	// set configPath to a mock temporary XDG config folder
	mockConfigPath := "/tmp/xdg_config_home/"
	tempFile := createMockXDGConfigFile(t, mockConfigPath)
	defer tempFile.Close()
	defer os.RemoveAll(mockConfigPath)
	// write LOG_LEVEL config to mock config.yaml file
	_, err := tempFile.WriteString(LOG_LEVEL + " : " + "debug\n")
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
	assert.Equal(t, "8080", viper.GetString(PORT))
	// Assert a .bak config file was created
	backupFile, err := os.Stat(mockConfigPath + CONFIG_FILE_NAME + ".bak")
	assert.False(t, os.IsNotExist(err))
	assert.Equal(t, CONFIG_FILE_NAME+".bak", backupFile.Name())
}

// testEnvironmentVariableConfigLoading tests the setting of log level from
// the environment variable and verifies the expected order of precedence of config loading
// (i.e. environment variable overrides config file).
// This test is nested within the TestCobraFlagConfigLoading test.
func testEnvironmentVariableConfigLoading(t *testing.T) {
	// Load XDG config
	testXDGConfigLoading(t)

	// Mock environment variable
	err := os.Setenv("GO_QUAI_LOG_LEVEL", "error")
	require.NoError(t, err)

	// Assert log level is set to "error" from the environment variable
	assert.Equal(t, "error", viper.GetString(LOG_LEVEL))
}

// TestCobraFlagConfigLoading tests the loading of the config file from the XDG config home,
// the loading of the environment variable and the loading of the cobra flag.
// It verifies the expected order of precedence of config loading.
func TestCobraFlagConfigLoading(t *testing.T) {
	// Load XDG config
	testXDGConfigLoading(t)
	// Assert log level is set to "debug" from the mock config file
	assert.Equal(t, "debug", viper.GetString(LOG_LEVEL))

	// Load environment variable config
	testEnvironmentVariableConfigLoading(t)

	// Assert log level is set to "error" from the environment variable
	assert.Equal(t, "error", viper.GetString(LOG_LEVEL))

	// Simulate a Cobra flag being set
	rootCmd := &cobra.Command{}
	rootCmd.PersistentFlags().StringP(LOG_LEVEL, "l", "warn", "log level (trace, debug, info, warn, error, fatal, panic")

	// Set cmd flag to override config file
	err := rootCmd.PersistentFlags().Set(LOG_LEVEL, "trace")
	require.NoError(t, err)
	viper.BindPFlags(rootCmd.PersistentFlags())

	// assert log level is set to "trace" from the cobra flag
	assert.Equal(t, "trace", viper.GetString(LOG_LEVEL))

	// Clear environment variable
	err = os.Unsetenv("GO_QUAI_LOG_LEVEL")
	require.NoError(t, err)

}

// helper function to create a mock XDG directory and config file
func createMockXDGConfigFile(t *testing.T, dir string) *os.File {
	t.Helper()
	err := os.MkdirAll(dir, 0755)
	require.NoError(t, err)
	tmpFile, err := os.Create(dir + CONFIG_FILE_NAME)
	require.NoError(t, err)
	return tmpFile
}
