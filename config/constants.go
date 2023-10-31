package config

const (
	APP_NAME = "go-quai"
	// prefix used to read config parameters from environment variables
	ENV_PREFIX = "GO_QUAI"
	// default private key file name
	PRIVATE_KEY_FILENAME = "private.key"
	// default config file name
	CONFIG_FILE_NAME = "config.yaml"
	// file to dynamically store node's ID and listening addresses
	NODEINFO_FILE_NAME = "node.info"
	// file to read the host's IP address (used to replace Docker's internal IP)
	HOST_IP_FILE_NAME = "host.ip"

	// constants used to handle config parameters in the viper instance.
	// see cmd/root.go for documentation on each parameter.
	CONFIG_DIR        = "config-dir"
	DATA_DIR          = "data-dir"
	LOG_LEVEL         = "log-level"
	SAVE_CONFIG_FILE  = "save-config"
	IP_ADDR           = "ipaddr"
	PORT              = "port"
	BOOTNODE          = "bootstrap-node"
	BOOTSTRAP_PEERS   = "bootstrap-peers"
	NAT_SERVICE       = "nat-service"
	NAT_PORTMAP       = "nat-portmap"
	AUTO_RELAY_STATIC = "auto-relay-static"
	HOLE_PUNCHING     = "hole-punching"
	RELAY             = "relay"
)
