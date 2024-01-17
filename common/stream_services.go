package common

import (
	"io"
	"time"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/pkg/errors"
)

const (
	// timeout in seconds before a read/write operation on the stream is considered failed
	// TODO: consider making this dynamic based on the network latency
	C_STREAM_TIMEOUT = 10 * time.Second

	// buffer size in bytes (1MB)
	C_BUFFER_SIZE = 1024 * 1024
)

// Reads the message from the stream and returns a byte of data.
func ReadMessageFromStream(stream network.Stream) ([]byte, error) {
	// Set a read deadline
	err := stream.SetReadDeadline(time.Now().Add(C_STREAM_TIMEOUT))
	if err != nil {
		return nil, errors.Wrap(err, "failed to set read deadline")
	}

	buffer := make([]byte, C_BUFFER_SIZE)
	var data []byte
	for {
		n, err := stream.Read(buffer)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			log.Errorf("error reading message from stream: %s", err)
			return nil, err
		}
		data = append(data, buffer[:n]...)
	}

	log.Tracef("succesfully read %d bytes from stream", len(data))

	return data, nil
}

// Writes the message to the stream.
func WriteMessageToStream(stream network.Stream, msg []byte) error {
	err := stream.SetWriteDeadline(time.Now().Add(C_STREAM_TIMEOUT))
	if err != nil {
		return errors.Wrap(err, "failed to set write deadline")
	}

	b, err := stream.Write(msg)
	if err != nil {
		return errors.Wrap(err, "failed to write message to stream")
	}
	log.Tracef("succesfully wrote %d bytes to stream", b)
	return err
}
