package node

import (
	"bufio"
	"os"
	"runtime/debug"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common/constants"
	"github.com/dominant-strategies/go-quai/log"
)

// Utility function that asynchronously writes the provided "info" string to the node.info file.
// If the file doesn't exist, it creates it. Otherwise, it appends the new "info" as a new line.
func saveNodeInfo(info string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		// check if data directory exists. If not, create it
		dataDir := viper.GetString(utils.DataDirFlag.Name)
		if _, err := os.Stat(dataDir); os.IsNotExist(err) {
			err := os.MkdirAll(dataDir, 0755)
			if err != nil {
				log.Global.Errorf("error creating data directory: %s", err)
				return
			}
		}
		nodeFile := dataDir + constants.NODEINFO_FILE_NAME
		// Open file with O_APPEND flag to append data to the file or create the file if it doesn't exist.
		f, err := os.OpenFile(nodeFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Global.Errorf("error opening node info file: %s", err)
			return
		}
		defer f.Close()

		// Use bufio for efficient writing
		writer := bufio.NewWriter(f)
		defer writer.Flush()

		// Append new line and write to file
		log.Global.Tracef("writing node info to file: %s", nodeFile)
		writer.WriteString(info + "\n")
	}()
}

// utility function used to delete any existing node info file
func deleteNodeInfoFile() error {
	dataDir := viper.GetString(utils.DataDirFlag.Name)
	nodeFile := dataDir + constants.NODEINFO_FILE_NAME
	if _, err := os.Stat(nodeFile); !os.IsNotExist(err) {
		err := os.Remove(nodeFile)
		if err != nil {
			return err
		}
	}
	return nil
}

// Loads bootpeers addresses from the config and returns a list of peer.AddrInfo
func loadBootPeers() ([]peer.AddrInfo, error) {
	if viper.GetBool(utils.SoloFlag.Name) {
		return nil, nil
	}
	var bootpeers []peer.AddrInfo
	for _, p := range viper.GetStringSlice(utils.BootPeersFlag.Name) {
		addr, err := multiaddr.NewMultiaddr(p)
		if err != nil {
			return nil, err
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			return nil, err
		}
		bootpeers = append(bootpeers, *info)
	}
	return bootpeers, nil
}
