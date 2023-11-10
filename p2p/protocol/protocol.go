package protocol

import (
	"bufio"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/network"
)

const ProtocolVersion = "/quai/1.0.0"

func QuaiProtocolHandler(stream network.Stream) {
	log.Warn("Received a new stream!")
	buf := bufio.NewReader(stream)
	str, err := buf.ReadString('\n')
	if err != nil {
		log.Errorf("Error reading from stream: %s", err)
		return
	}

	if stream.Protocol() != ProtocolVersion {
		log.Warnf("Invalid protocol: %s:", stream.Protocol())
		stream.Reset() // Reset closes the stream immediately, dropping the connection

		// peerID := stream.Conn().RemotePeer()

		// if err := h.Network().ClosePeer(peerID); err != nil {
		// 	log.Errorf("Failed to close connection with peer %s: %s", peerID, err)
		// } else {
		// 	log.Debugf("Disconnected from peer %s", peerID)
		// }

		return
	}

	log.Info("Received: %s", str)

	_, err = stream.Write([]byte("Hello from the other side!\n"))
	if err != nil {
		log.Errorf("Error writing to stream: %s", err)
		return
	}

	stream.Close()
}
