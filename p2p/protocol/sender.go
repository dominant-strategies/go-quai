package protocol

import (
	"time"
	
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/options"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/network"
)

// Open streams to start exchanging data with peers
func OpenPeerStreams(p QuaiP2PNode) {
	// If this node is a bootnode or solo node, don't keep trying to open streams
	if viper.GetBool(options.SOLO) {
		return
	}

	log.Debugf("joining quaiprotocol network...")
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	logMsg := "Unable to open any streams with peers. Retrying in 5 seconds..."

	// attempt to open streams until successful
	for {
		select {
		case <-ticker.C:
			if attemptToOpenStreams(p) {
				log.Debugf("successfully opened streams with peers")
				return
			}
			log.Warnf(logMsg)
		}
	}
}

// Attempt to open a stream with every connected peer
// Returns true only if this peer has opened streams with at least 2 peers
func attemptToOpenStreams(p QuaiP2PNode) bool {
	// Indicate how many peers have validated this peers join request
	streamCount := 0

	// Cache the list of connected peers so it doesn't change while iterating
	connectedPeers := p.Network().Peers()

	// Open a stream to each connected peer using the Quai protocol
	for _, peerID := range(connectedPeers) {
		stream, err := p.NewStream(peerID, ProtocolVersion)
		if err != nil {
			log.Warnf("error opening stream to peer %s: %s", peerID, err)
			continue
		}
		defer stream.Close()

		// Send a join request through the stream
		if err := sendJoinRequest(stream); err != nil {
			log.Warnf("error sending join request to peer %s: %s", peerID, err)
			continue
		}

		streamCount += 1
		// We should connect to at least 2 peers to minimize the chance of partition upon restart
		if streamCount >= 2 {
			return true
		}
	}
	return false
}

// Send a request to join the Quai network to the bootstrap peer using a stream
func sendJoinRequest(stream network.Stream) error {
	// TODO: implement
	return nil
}
