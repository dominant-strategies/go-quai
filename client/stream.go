package client

import (
	"github.com/dominant-strategies/go-quai/log"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const myProtocol = "/quai/0.1.0"

// This function is used by the node to handle incoming streams.
// When a message is received, it prints the message and sends an acknowlegdment back to the sender.
func (c *P2PClient) handleStream(s network.Stream) {
	defer s.Close()
	// Create a buffer to read the message
	buf := make([]byte, 1024)
	n, err := s.Read(buf)
	if err != nil {
		log.Errorf("Error reading from stream: %s", err)
		return
	}

	// Print the received message
	log.Infof("Received message: '%s'", string(buf[:n]))

	// send acknowlegdment back to the sender
	_, err = s.Write([]byte("Message received!"))
	if err != nil {
		log.Errorf("Error sending acknowlegdment: %s", err)
	}

	// Read the acknowledgment from the sender
	n, err = s.Read(buf)
	if err != nil {
		log.Errorf("Error reading acknowledgment from stream: %s", err)
		return
	}

	// Print the received acknowledgment
	log.Infof("Received acknowledgment: '%s'", string(buf[:n]))
}

// This function is used by the node to send a message to a peer using a stream
func (c *P2PClient) sendMessage(peerID peer.ID, message string) error {
	// Open a stream to the peer
	s, err := c.node.NewStream(c.ctx, peerID, myProtocol)
	if err != nil {
		log.Errorf("Error opening stream: %s", err)
		return err
	}
	defer s.Close()
	if err != nil {
		log.Errorf("Error opening stream: %s", err)
		return err
	}

	// Write the message to the stream
	_, err = s.Write([]byte(message))
	if err != nil {
		log.Errorf("Error writing to stream: %s", err)
		return err
	}
	log.Infof("Sent message: '%s' to peer %s", message, peerID.String())
	return nil
}
