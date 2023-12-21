package common

import (
	"io"
	"net"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/pkg/errors"
)

const (
	// timeout in seconds before a read/write operation on the stream is considered failed
	// TODO: consider making this dynamic based on the network latency
	STREAM_TIMEOUT = 10 * time.Second
)

// Reads the message from the stream and returns a byte of data.
// If the message is not received within STREAM_TIMEOUT, an error is returned.
func ReadMessageFromStream(stream network.Stream) ([]byte, error) {
	// Set a read deadline
	err := stream.SetReadDeadline(time.Now().Add(STREAM_TIMEOUT))
	if err != nil {
		return nil, errors.Wrap(err, "failed to set read deadline")
	}

	// Read the message from the stream
	msg, err := io.ReadAll(stream)
	if err != nil {
		if err == io.EOF {
			// the stream is closed
			return nil, errors.Wrap(err, "stream closed")
		}
		if err, ok := err.(net.Error); ok && err.Timeout() {
			// read deadline exceeded
			return nil, errors.Wrap(err, "read deadline exceeded")
		}
		// some other error
		return nil, errors.Wrap(err, "failed to read message from stream")
	}

	return msg, nil
}

// Writes the message to the stream.
// If the message is not written within STREAM_TIMEOUT, an error is returned.
func WriteMessageToStream(stream network.Stream, msg []byte) error {
	err := stream.SetWriteDeadline(time.Now().Add(STREAM_TIMEOUT))
	if err != nil {
		return errors.Wrap(err, "failed to set write deadline")
	}
	_, err = stream.Write(msg)
	if err != nil {
		if err, ok := err.(net.Error); ok && err.Timeout() {
			// write deadline exceeded
			return errors.Wrap(err, "write deadline exceeded")
		}
		// some other error
		return errors.Wrap(err, "failed to write message to stream")
	}
	return err
}
