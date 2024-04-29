/*
Copyright © 2023 NAME HERE <EMAIL ADDRESS>
*/
package main

import (
	"bufio"
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/pkg/errors"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/node"
	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"
)

// In your cmd package where you define Cobra commands

var sendCmd = &cobra.Command{
	Use:          "send",
	Short:        "Send a message using the Quai protocol",
	Long:         `Send a message to a specific peer using the Quai protocol for testing purposes.`,
	RunE:         runSend,
	PreRunE:      sendCmdPreRun,
	SilenceUsage: true,
}

var targetPeer string
var message string
var sendInterval time.Duration
var protocolID string

func init() {
	startCmd.AddCommand(sendCmd)

	sendCmd.Flags().StringVar(&targetPeer, "target", "", "Target peer multiaddress. If not provided, will use the default bootnode.")
	sendCmd.Flags().StringVarP(&message, "message", "m", "", "Message to send")
	sendCmd.Flags().DurationVar(&sendInterval, "interval", 0, "Interval between messages, i.e. '5s' (Set to 0 for a one-time send).")
	// add flag to read the protocol id from the command line
	sendCmd.Flags().StringVar(&protocolID, "protocol", string(quaiprotocol.ProtocolVersion), "Protocol ID, i.e. '/quai/1.0.0'")

	sendCmd.MarkFlagRequired("message") // Ensure that message flag is provided
}

func sendCmdPreRun(cmd *cobra.Command, args []string) error {
	// duplicated from cmd/start.go
	// set keyfile path
	if viper.GetString(utils.KeyFileFlag.Name) == "" {
		configDir := cmd.Flag(utils.ConfigDirFlag.Name).Value.String()
		viper.Set(utils.KeyFileFlag.Name, filepath.Join(configDir, "private.key"))
	}

	// if no bootstrap peers are provided, use the default ones defined in config/bootnodes.go
	if bootstrapPeers := viper.GetStringSlice(utils.BootPeersFlag.Name); len(bootstrapPeers) == 0 {
		log.Global.Debugf("no bootstrap peers provided. Using default ones: %v", common.BootstrapPeers)
		viper.Set(utils.BootPeersFlag.Name, common.BootstrapPeers)
	}
	return nil
}

func runSend(cmd *cobra.Command, args []string) error {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// create a new p2p node
	node, err := node.NewNode(ctx)
	if err != nil {
		log.Global.Fatalf("error creating node: %s", err)
	}

	// Ensure target peer is set, or use a default
	if targetPeer == "" {
		log.Global.Warnf("no target peer provided. Using default bootnode: %s", common.BootstrapPeers[0])
		targetPeer = common.BootstrapPeers[0]
	}

	// Convert targetPeer string to peer.AddrInfo
	targetAddrInfo, err := peer.AddrInfoFromString(targetPeer)
	if err != nil {
		return err
	}

	// start node
	if err := node.Start(); err != nil {
		log.Global.Fatalf("error starting node: %s", err)
	}

	// Connect to the target peer
	if err := node.GetPeerManager().GetHost().Connect(ctx, *targetAddrInfo); err != nil {
		log.Global.Errorf("error connecting to target peer: %+v, error: %s", targetAddrInfo.ID, err)
		return err
	}

	// Function to send a message
	sendMessage := func() error {
		stream, err := node.GetPeerManager().GetHost().NewStream(ctx, targetAddrInfo.ID, protocol.ID(protocolID))
		if err != nil {
			log.Global.Errorf("error opening stream: %s", err)
			return err
		}
		defer stream.Close()

		_, err = stream.Write([]byte(message + "\n"))
		if err != nil {
			log.Global.Errorf("error writing to stream: %s", err)
			return err
		}

		log.Global.Debugf("Sent message: '%s'. Waiting for response...", message)

		// Read the response from host2
		buf := bufio.NewReader(stream)
		// response, err := buf.ReadString('\n')
		response, err := readWithTimeout(buf, ctx)
		if err != nil {
			log.Global.Errorf("error reading from stream: %s", err)
			return err
		}
		log.Global.Debugf("Received response: '%s'", response)
		return nil
	}

	if sendInterval > 0 {
		// Send periodically
		ticker := time.NewTicker(sendInterval)
		defer ticker.Stop()
		// wait for a SIGINT or SIGTERM signal
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		for {
			select {
			case <-ticker.C:
				if err := sendMessage(); err != nil {
					log.Global.Errorf("Error sending message: %s", err)
				}
			case <-ch:
				log.Global.Warnf("Received 'stop' signal, shutting down gracefully...")
				cancel()
				if err := node.Stop(); err != nil {
					panic(err)
				}
				log.Global.Warnf("Node is offline")
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	} else {
		// Send once
		return sendMessage()
	}
}

// readWithTimeout reads from the bufio.Reader with a 5 seconds timeout.
func readWithTimeout(buf *bufio.Reader, ctx context.Context) (string, error) {
	resultChan := make(chan string)
	errChan := make(chan error)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Fatal("Go-Quai Panicked")
			}
		}()
		response, err := buf.ReadString('\n')
		if err != nil {
			errChan <- err
			return
		}
		resultChan <- response
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case response := <-resultChan:
		return response, nil
	case err := <-errChan:
		return "", err

	case <-ticker.C:
		return "", errors.New("timeout waiting for response")
	}
}
