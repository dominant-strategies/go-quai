package mocks

import (
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

type Stream interface {
	MuxedStream

	// ID returns an identifier that uniquely identifies this Stream within this
	// host, during this run. Stream IDs may repeat across restarts.
	ID() string

	Protocol() protocol.ID
	SetProtocol(id protocol.ID) error

	// Stat returns metadata pertaining to this stream.
	Stat() network.Stats

	// Conn returns the connection this stream is part of.
	Conn() network.Conn

	// Scope returns the user's view of this stream's resource scope
	Scope() network.StreamScope
}

type MuxedStream interface {
	io.Reader
	io.Writer

	// Close closes the stream.
	//
	// * Any buffered data for writing will be flushed.
	// * Future reads will fail.
	// * Any in-progress reads/writes will be interrupted.
	//
	// Close may be asynchronous and _does not_ guarantee receipt of the
	// data.
	//
	// Close closes the stream for both reading and writing.
	// Close is equivalent to calling `CloseRead` and `CloseWrite`. Importantly, Close will not wait for any form of acknowledgment.
	// If acknowledgment is required, the caller must call `CloseWrite`, then wait on the stream for a response (or an EOF),
	// then call Close() to free the stream object.
	//
	// When done with a stream, the user must call either Close() or `Reset()` to discard the stream, even after calling `CloseRead` and/or `CloseWrite`.
	io.Closer

	// CloseWrite closes the stream for writing but leaves it open for
	// reading.
	//
	// CloseWrite does not free the stream, users must still call Close or
	// Reset.
	CloseWrite() error

	// CloseRead closes the stream for reading but leaves it open for
	// writing.
	//
	// When CloseRead is called, all in-progress Read calls are interrupted with a non-EOF error and
	// no further calls to Read will succeed.
	//
	// The handling of new incoming data on the stream after calling this function is implementation defined.
	//
	// CloseRead does not free the stream, users must still call Close or
	// Reset.
	CloseRead() error

	// Reset closes both ends of the stream. Use this to tell the remote
	// side to hang up and go away.
	Reset() error

	SetDeadline(time.Time) error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}
