package common

import (
	"encoding/binary"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/pkg/errors"
)

const (
	// timeout in seconds before a read/write operation on the stream is considered failed
	// TODO: consider making this dynamic based on the network latency
	C_STREAM_TIMEOUT = 10 * time.Second
)

// Reads the message from the stream and returns a byte of data.
func ReadMessageFromStream(stream network.Stream) ([]byte, error) {
	// First read the length of the incoming message
	lenBytes := make([]byte, 4)
	if _, err := io.ReadFull(stream, lenBytes); err != nil {
		return nil, errors.Wrap(err, "failed to read message length")
	}
	msgLen := binary.BigEndian.Uint32(lenBytes)

	// Now read the message
	data := make([]byte, msgLen)
	if _, err := io.ReadFull(stream, data); err != nil {
		return nil, errors.Wrap(err, "failed to read message")
	}

	if messageMetrics != nil {
		messageMetrics.WithLabelValues("received").Inc()
	}
	return data, nil
}

// Writes the message to the stream.
func WriteMessageToStream(stream network.Stream, msg []byte) error {
	// Set the write deadline
	if err := stream.SetWriteDeadline(time.Now().Add(C_STREAM_TIMEOUT)); err != nil {
		return errors.Wrap(err, "failed to set write deadline")
	}

	// Get the length of the message and encode it
	msgLen := uint32(len(msg))
	lenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBytes, msgLen)

	// Prefix the message with the encoded length
	msg = append(lenBytes, msg...)

	// Then write the message
	_, err := stream.Write(msg)
	if err != nil {
		return errors.Wrap(err, "failed to write message to stream")
	}

	if messageMetrics != nil {
		messageMetrics.WithLabelValues("sent").Inc()
	}
	return nil
}
