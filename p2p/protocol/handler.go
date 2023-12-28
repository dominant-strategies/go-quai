package protocol

import (
	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/network"
)

func QuaiProtocolHandler(stream network.Stream, node QuaiP2PNode) {
	defer stream.Close()

	log.Debugf("Received a new stream from %s", stream.Conn().RemotePeer())

	// if there is a protocol mismatch, close the stream
	if stream.Protocol() != ProtocolVersion {
		log.Warnf("Invalid protocol: %s", stream.Protocol())
		// TODO: add logic to drop the peer
		return
	}

	if err := processJoinRequest(stream); err != nil {
		log.Warnf("error processing join request: %s", err)
		return
	}

	// TODO: add logic to handle other requests

}
