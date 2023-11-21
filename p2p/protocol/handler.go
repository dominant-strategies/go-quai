package protocol

import (
	"bufio"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/network"
)

const ProtocolVersion = "/quai/1.0.0"

func QuaiProtocolHandler(stream network.Stream) {
	defer stream.Close()

	log.Debugf("Received a new stream!")

	// if there is a protocol mismatch, close the stream
	if stream.Protocol() != ProtocolVersion {
		log.Warnf("Invalid protocol: %s", stream.Protocol())
		return
	}

	buf := bufio.NewReader(stream)
	request, err := buf.ReadString('\n')
	if err != nil {
		log.Errorf("error reading from stream: %s", err)
		return
	}
	log.Debugf("Received data: %s", request)

	// send a response
	response := "Hello from the other side!\n"

	// write response to stream
	_, err = stream.Write([]byte(response))

	if err != nil {
		log.Errorf("error writing to stream: %s", err)
		return
	}

	log.Debugf("Sent response: '%s'. Closing stream...", response)
}
