package protocol

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/p2p/node/requestManager"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	libp2pmetrics "github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2pprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

type lifecycleStream struct {
	network.Stream
	readErr error
	reads   int
	closed  bool
}

func (stream *lifecycleStream) Protocol() libp2pprotocol.ID { return ProtocolVersion }
func (stream *lifecycleStream) Conn() network.Conn          { return lifecycleConn{} }
func (stream *lifecycleStream) Read([]byte) (int, error) {
	stream.reads++
	if stream.reads == 1 {
		return 0, stream.readErr
	}
	return 0, io.EOF
}
func (stream *lifecycleStream) Close() error {
	stream.closed = true
	return nil
}

type lifecycleConn struct{ network.Conn }

func (lifecycleConn) RemotePeer() peer.ID { return peer.ID("lifecycle-peer") }

type lifecycleNode struct{ QuaiP2PNode }

func (lifecycleNode) GetBandwidthCounter() libp2pmetrics.Reporter { return nil }

func TestQuaiProtocolHandlerStopsWorkerWhenStreamCloses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		readErr error
	}{
		{name: "EOF", readErr: io.EOF},
		{name: "read error", readErr: errors.New("stream read failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Several streams make a worker that remains after Close visible even
			// if the scheduler has not run every new goroutine yet.
			for i := 0; i < 8; i++ {
				stream := &lifecycleStream{readErr: tc.readErr}
				QuaiProtocolHandler(ctx, stream, lifecycleNode{})
				if stream.reads != 1 || !stream.closed {
					t.Fatalf("handler did not stop after the read error: reads=%d closed=%v", stream.reads, stream.closed)
				}
			}

			waitForProtocolWorkers(t)
		})
	}
}

type lifecycleResponseStream struct {
	network.Stream
	reader *bytes.Reader
	closed bool
}

func (stream *lifecycleResponseStream) Protocol() libp2pprotocol.ID { return ProtocolVersion }
func (stream *lifecycleResponseStream) Conn() network.Conn          { return lifecycleConn{} }
func (stream *lifecycleResponseStream) Read(data []byte) (int, error) {
	return stream.reader.Read(data)
}
func (stream *lifecycleResponseStream) Close() error {
	stream.closed = true
	return nil
}

type lifecycleRequestManager struct{ response chan interface{} }

func (manager lifecycleRequestManager) CreateRequest() uint32 { return 1 }
func (manager lifecycleRequestManager) CloseRequest(uint32)   {}
func (manager lifecycleRequestManager) GetRequestChan(id uint32) (chan interface{}, error) {
	if id != 1 {
		return nil, errors.New("unknown request")
	}
	return manager.response, nil
}

type lifecycleResponseNode struct {
	lifecycleNode
	requests requestManager.RequestManager
}

func (node lifecycleResponseNode) GetRequestManager() requestManager.RequestManager {
	return node.requests
}

func TestQuaiProtocolHandlerDeliversQueuedResponseBeforeExit(t *testing.T) {
	hash := common.Hash{1}
	message, err := pb.EncodeQuaiResponse(1, common.Location{0, 0}, &common.Hash{}, hash)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 4+len(message))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(message)))
	copy(frame[4:], message)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 16; i++ {
		response := make(chan interface{}, 1)
		node := lifecycleResponseNode{requests: lifecycleRequestManager{response: response}}
		stream := &lifecycleResponseStream{reader: bytes.NewReader(frame)}
		QuaiProtocolHandler(ctx, stream, node)
		if !stream.closed {
			t.Fatal("handler did not close the stream")
		}
		select {
		case value := <-response:
			if got, ok := value.(common.Hash); !ok || got != hash {
				t.Fatalf("unexpected response: %v", value)
			}
		case <-time.After(time.Second):
			t.Fatal("queued response was lost when the stream closed")
		}
	}
	waitForProtocolWorkers(t)
}

func waitForProtocolWorkers(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	stack := make([]byte, 1<<20)
	for {
		size := runtime.Stack(stack, true)
		if !bytes.Contains(stack[:size], []byte("protocol.QuaiProtocolHandler.func")) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("protocol worker remained alive after its stream handler returned")
		}
		time.Sleep(time.Millisecond)
	}
}
