package streamManager

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/pkg/errors"

	expireLru "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/libp2p/go-libp2p/core/host"
	libp2pmetrics "github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	// timeout in seconds before a read/write operation on the stream is considered failed
	// TODO: consider making this dynamic based on the network latency
	c_stream_timeout = 10 * time.Second

	// The amount of redundancy for open streams
	c_streamCacheSize = 30

	// c_newStreamRequestChanSize is the size of the channel to handle new stream request
	c_newStreamRequestChanSize = 10

	// The maximum number of concurrent requests before a stream is considered failed
	c_maxPendingRequests = 100
)

var (
	ErrStreamNotFound           = errors.New("stream not found")
	ErrStreamMismatch           = errors.New("stream mismatch")
	ErrorTooManyPendingRequests = errors.New("too many pending requests")
)

type StreamManager interface {
	// Set the host for the stream manager
	SetP2PBackend(quaiprotocol.QuaiP2PNode)

	// Get/Set the host for the stream manager
	GetHost() host.Host
	SetHost(host.Host)

	// GetStream returns a valid stream, either creating a new one or returning an existing one
	GetStream(peer.ID) (network.Stream, error)

	// CloseStream goes through all the steps to properly close and remove a stream's resources
	CloseStream(peer.ID) error

	// WriteMessageToStream writes the given message into the given stream
	WriteMessageToStream(peerID p2p.PeerID, stream network.Stream, msg []byte, protoversion protocol.ID, reporter libp2pmetrics.Reporter) error
}

type basicStreamManager struct {
	ctx         context.Context
	cancel      context.CancelFunc
	streamCache *expireLru.LRU[p2p.PeerID, streamWrapper]
	p2pBackend  quaiprotocol.QuaiP2PNode

	newStreamRequestChan chan p2p.PeerID

	host host.Host
	mu   sync.Mutex
}

type streamWrapper struct {
	stream                network.Stream
	cancelProtocolHandler context.CancelFunc
	semaphore             chan struct{}
	errCount              int
}

func NewStreamManager(node quaiprotocol.QuaiP2PNode, host host.Host) (*basicStreamManager, error) {
	lruCache := expireLru.NewLRU[p2p.PeerID, streamWrapper](
		c_streamCacheSize,
		severStream,
		0,
	)

	ctx, cancel := context.WithCancel(context.Background())

	sm := &basicStreamManager{
		ctx:                  ctx,
		cancel:               cancel,
		streamCache:          lruCache,
		p2pBackend:           node,
		host:                 host,
		newStreamRequestChan: make(chan p2p.PeerID, c_newStreamRequestChanSize),
	}

	return sm, nil

}

// Expects a key as peerID and value of *streamWrapper
func severStream(key p2p.PeerID, wrappedStream streamWrapper) {
	stream := wrappedStream.stream
	err := stream.Close()
	if err != nil {
		log.Global.WithField("err", err).Error("Failed to close stream")
	}
	if streamMetrics != nil {
		streamMetrics.WithLabelValues("NumStreams").Dec()
	}
	// Clean up the protocolHandler
	wrappedStream.cancelProtocolHandler()
}

func (sm *basicStreamManager) Start() {
	go sm.listenForNewStreamRequest()
}

func (sm *basicStreamManager) Stop() {
	sm.cancel()
}

func (sm *basicStreamManager) listenForNewStreamRequest() {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case peerID := <-sm.newStreamRequestChan:
			go func(peerID peer.ID) {
				err := sm.OpenStream(peerID)
				if err != nil {
					log.Global.WithFields(log.Fields{"peerId": peerID, "info": err}).Info("Success opening new stream into peer")
				}
			}(peerID)

		case <-sm.ctx.Done():
			return
		}
	}
}

func (sm *basicStreamManager) OpenStream(peerID p2p.PeerID) error {
	// Check if there is an existing stream
	if _, ok := sm.streamCache.Get(peerID); ok {
		return nil
	}

	streamCtx, streamCancel := context.WithTimeout(sm.ctx, c_stream_timeout)
	defer streamCancel()

	// Attempt to create the new stream to the peer
	stream, err := sm.host.NewStream(streamCtx, peerID, quaiprotocol.ProtocolVersion)
	if err != nil {
		if streamCtx.Err() == context.DeadlineExceeded {
			return errors.New("stream creation timeout")
		}
		return fmt.Errorf("error opening new stream with peer %s err: %s", peerID, err)
	}

	handlerCtx, handlerCancel := context.WithCancel(sm.ctx)

	wrappedStream := streamWrapper{
		stream:                stream,
		cancelProtocolHandler: handlerCancel,
		semaphore:             make(chan struct{}, c_maxPendingRequests),
		errCount:              0,
	}
	sm.streamCache.Add(peerID, wrappedStream)

	go quaiprotocol.QuaiProtocolHandler(handlerCtx, stream, sm.p2pBackend)
	log.Global.WithField("PeerID", peerID).Info("Had to create new stream")
	if streamMetrics != nil {
		streamMetrics.WithLabelValues("NumStreams").Inc()
	}

	return nil
}

func (sm *basicStreamManager) CloseStream(peerID p2p.PeerID) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	wrappedStream, ok := sm.streamCache.Get(peerID)
	if ok {
		severStream(peerID, wrappedStream)
		sm.streamCache.Remove(peerID)
		log.Global.WithField("peerID", peerID).Debug("Pruned connection with peer")
		return nil
	}
	return ErrStreamNotFound
}

func (sm *basicStreamManager) GetStream(peerID p2p.PeerID) (network.Stream, error) {
	wrappedStream, ok := sm.streamCache.Get(peerID)
	var err error
	if !ok {
		select {
		case sm.newStreamRequestChan <- peerID:
		default:
			log.Global.Error("sm.newPeers is full with new stream creation requests")
		}
		return nil, ErrStreamNotFound
	} else {
		log.Global.WithField("PeerID", peerID).Debug("Requested stream was found in cache")
	}

	return wrappedStream.stream, err
}

func (sm *basicStreamManager) SetP2PBackend(host quaiprotocol.QuaiP2PNode) {
	sm.p2pBackend = host
}

func (sm *basicStreamManager) SetHost(host host.Host) {
	sm.host = host
}

func (sm *basicStreamManager) GetHost() host.Host {
	return sm.host
}

// Writes the message to the stream.
func (sm *basicStreamManager) WriteMessageToStream(peerID p2p.PeerID, stream network.Stream, msg []byte, protoversion protocol.ID, reporter libp2pmetrics.Reporter) error {
	wrappedStream, found := sm.streamCache.Get(peerID)
	if !found {
		return ErrStreamNotFound
	}
	if stream != wrappedStream.stream {
		// Indicate an unexpected case where the stream we stored and the stream we are requested to write to are not the same.
		return ErrStreamMismatch
	}

	// Attempt to acquire semaphore before proceeding
	select {
	case wrappedStream.semaphore <- struct{}{}:
		// Acquired semaphore successfully
	default:
		wrappedStream.errCount += 1
		if wrappedStream.errCount > c_maxPendingRequests {
			log.Global.WithFields(log.Fields{
				"errCount": wrappedStream.errCount,
				"peerID":   peerID,
			}).Warn("Had to close malfunctioning stream")
			// If c_maxPendingRequests have been dropped, the stream is likely in a bad state
			sm.CloseStream(peerID)
			return ErrorTooManyPendingRequests
		}
		return ErrorTooManyPendingRequests
	}
	defer func() {
		<-wrappedStream.semaphore
	}()

	// Set the write deadline
	if err := stream.SetWriteDeadline(time.Now().Add(c_stream_timeout)); err != nil {
		return errors.Wrap(err, "failed to set write deadline")
	}

	// Get the length of the message and encode it
	msgLen := uint32(len(msg))
	lenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBytes, msgLen)

	// Prefix the message with the encoded length
	msg = append(lenBytes, msg...)

	// Then write the message
	numWritten, err := stream.Write(msg)
	if err != nil {
		return errors.Wrap(err, "failed to write message to stream")
	}
	if reporter != nil {
		reporter.LogSentMessageStream(int64(numWritten), protoversion, stream.Conn().RemotePeer())
	}

	return nil
}
