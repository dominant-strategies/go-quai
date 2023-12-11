package protocol

import (
	"time"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/network"

	"github.com/pkg/errors"
)

// Join the node to the quai p2p network
func JoinNetwork(p QuaiP2PNode) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	err := attemptToJoinNetwork(p)
	if err != nil {
		log.Infof("Initial attempt failed: %s. Trying to join again...", err)
	}

	// Start retrying
	for range ticker.C {
		err := attemptToJoinNetwork(p)
		if err != nil {
			log.Infof("Attempt failed: %s. Retrying in 5 seconds...", err)
			continue
		}
		return
	}
}

func attemptToJoinNetwork(p QuaiP2PNode) error {
	// bool to indicate if the node has been validated by at least one bootnode
	var validated bool

	// Open a stream to each connected peer using the Quai protocol
	for _, peerID := range p.Network().Peers() {
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
		// TODO: being validated by only one bootnode is enough?
		if !validated {
			validated = true
		}
	}
	if !validated {
		return errors.New("have not established a quai handshake with any other node")
	}
	return nil
}

// Send a request to join the Quai network to the bootstrap peer using a stream
func sendJoinRequest(stream network.Stream) error {
	// TODO: implement
	return nil
}
