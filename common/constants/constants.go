package constants

const (
	APP_NAME = "go-quai"
	// prefix used to read config parameters from environment variables
	ENV_PREFIX = "GO_QUAI"
	// private key file name
	PRIVATE_KEY_FILENAME = "private.key"
	// config file name
	CONFIG_FILE_NAME = "config.toml"
	// config file type
	CONFIG_FILE_TYPE = "toml"
	// file to dynamically store node's ID and listening addresses
	NODEINFO_FILE_NAME = "node.info"
	// file to read the host's IP address (used to replace Docker's internal IP)
	HOST_IP_FILE_NAME = "host.ip"
)
