package node

import (
	"context"
	"math/big"
	"math/rand"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	"github.com/dominant-strategies/go-quai/p2p/node/streamManager"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/quai"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/common"
)

var (
	propagationTimes         = metrics_config.NewHistogramVec("PropagationTimes", "message propagation times by type (sec)")
	blockPropagationHist     = propagationTimes.WithLabelValues("block propagation time (sec)")
	headerPropagationHist    = propagationTimes.WithLabelValues("header propagation time (sec)")
	peerRequestTraceEnabled  = os.Getenv("QUAI_PEER_TRACE") == "1"
	peerRequestTraceSequence atomic.Uint64
)

// Starts the node and all of its services
func (p *P2PNode) Start() error {
	log.Global.Infof("starting P2P node...")

	// Start any async processes belonging to this node
	log.Global.Debugf("starting node processes...")
	go p.eventLoop()
	go p.statsLoop()

	// Register the Quai protocol handler
	p.peerManager.GetHost().SetStreamHandler(quaiprotocol.ProtocolVersion, func(s network.Stream) {
		quaiprotocol.QuaiProtocolHandler(p.ctx, s, p)
	})

	// Start the pubsub manager
	p.pubsub.SetReceiveHandler(p.handleBroadcast)

	return nil
}

func (p *P2PNode) Subscribe(location common.Location, datatype interface{}) error {
	err := p.pubsub.SubscribeAndRegisterValidator(location, datatype, nil)
	if err != nil {
		return err
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		ticker := time.NewTicker(time.Second)
		timeout := time.NewTicker(60 * time.Second)
		for {
			select {
			case <-ticker.C:
				if err := p.peerManager.Provide(p.ctx, location, datatype); err == nil {
					log.Global.Infof("providing topic %s in %s", reflect.TypeOf(datatype), location.Name())
					return
				}
			case <-p.quitCh:
				return
			case <-timeout.C:
				log.Global.Errorf("unable to provide topic %s in %s", reflect.TypeOf(datatype), location.Name())
				return
			}
		}
	}()
	return nil
}

func (p *P2PNode) Unsubscribe(location common.Location, datatype interface{}) error {
	return p.pubsub.Unsubscribe(location, datatype)
}

func (p *P2PNode) Broadcast(location common.Location, data interface{}) error {
	return p.pubsub.Broadcast(location, data)
}

func (p *P2PNode) SetConsensusBackend(be quai.ConsensusAPI) {
	p.consensus = be
	p.pubsub.SetQuaiBackend(be)
}

type stopFunc func() error

// Function to gracefully shtudown all running services
func (p *P2PNode) Stop() error {
	// define a list of functions to stop the services the node is running
	stopFuncs := []stopFunc{
		p.peerManager.GetHost().Close,
		p.peerManager.Stop,
		p.pubsub.Stop,
	}
	// create a channel to collect errors
	errs := make(chan error, len(stopFuncs))
	// run each stop function in a goroutine
	for _, fn := range stopFuncs {
		go func(fn stopFunc) {
			defer func() {
				if r := recover(); r != nil {
					log.Global.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Error("Go-Quai Panicked")
				}
			}()
			errs <- fn()
		}(fn)
	}

	var allErrors []error
	for i := 0; i < len(stopFuncs); i++ {
		select {
		case err := <-errs:
			if err != nil {
				log.Global.Errorf("error during shutdown: %s", err)
				allErrors = append(allErrors, err)
			}
		case <-time.After(5 * time.Second):
			err := errors.New("timeout during shutdown")
			log.Global.Warnf("error: %s", err)
			allErrors = append(allErrors, err)
		}
	}

	close(errs)
	if len(allErrors) > 0 {
		return errors.Errorf("errors during shutdown: %v", allErrors)
	} else {
		return nil
	}
}

type requestPeerSource interface {
	GetPeersExcluding(topic *pubsubManager.Topic, excluded map[p2p.PeerID]struct{}) map[p2p.PeerID]struct{}
	GetStreamPeers() []peer.ID
}

type peerRequestTraceKey struct{}

func traceRequestRound(ctx context.Context, source requestPeerSource, topic *pubsubManager.Topic, stage string, round int, peers, topicPeers []peer.ID) {
	if !peerRequestTraceEnabled {
		return
	}
	traceID, ok := ctx.Value(peerRequestTraceKey{}).(uint64)
	if !ok {
		return
	}
	qualitySource, hasQuality := source.(interface{ GetPeerQuality(peer.ID) int })
	for _, peerID := range peers {
		quality := 0
		if hasQuality {
			quality = qualitySource.GetPeerQuality(peerID)
		}
		onTopic := false
		for _, topicPeer := range topicPeers {
			if peerID == topicPeer {
				onTopic = true
				break
			}
		}
		log.Global.WithFields(log.Fields{
			"trace_id":     traceID,
			"topic":        topic.String(),
			"stage":        stage,
			"round":        round,
			"peer":         peerID,
			"quality":      quality,
			"topic_member": onTopic,
		}).Info("Peer request trace selection")
	}
}

func selectRequestPeers(peers []peer.ID, requestDegree int, shuffle func(int, func(int, int))) []peer.ID {
	if requestDegree <= 0 {
		return nil
	}
	selected := append([]peer.ID(nil), peers...)
	shuffle(len(selected), func(i, j int) {
		selected[i], selected[j] = selected[j], selected[i]
	})
	if len(selected) > requestDegree {
		selected = selected[:requestDegree]
	}
	return selected
}

// orderLiveRequestPeers tries peers seen on the requested gossip topic first.
// The request stream itself is shared across topics, so topic membership is a
// preference rather than a requirement. Neither input slice is changed.
func orderLiveRequestPeers(streamPeers, topicPeers []peer.ID, shuffle func(int, func(int, int))) []peer.ID {
	topicSet := make(map[peer.ID]struct{}, len(topicPeers))
	for _, peerID := range topicPeers {
		topicSet[peerID] = struct{}{}
	}
	seen := make(map[peer.ID]struct{}, len(streamPeers))
	preferred := make([]peer.ID, 0, len(streamPeers))
	other := make([]peer.ID, 0, len(streamPeers))
	for _, peerID := range streamPeers {
		if _, duplicate := seen[peerID]; duplicate {
			continue
		}
		seen[peerID] = struct{}{}
		if _, onTopic := topicSet[peerID]; onTopic {
			preferred = append(preferred, peerID)
		} else {
			other = append(other, peerID)
		}
	}
	return append(selectRequestPeers(preferred, len(preferred), shuffle), selectRequestPeers(other, len(other), shuffle)...)
}

func topicRequestPeers(source requestPeerSource, topic *pubsubManager.Topic, attempted []peer.ID, requestDegree int, shuffle func(int, func(int, int))) []peer.ID {
	if requestDegree <= 0 {
		return nil
	}
	attemptedSet := make(map[peer.ID]struct{}, len(attempted))
	for _, peerID := range attempted {
		attemptedSet[peerID] = struct{}{}
	}
	topicPeers := source.GetPeersExcluding(topic, attemptedSet)
	selected := make([]peer.ID, 0, len(topicPeers))
	for peerID := range topicPeers {
		if _, tried := attemptedSet[peerID]; !tried {
			selected = append(selected, peerID)
		}
	}
	return selectRequestPeers(selected, requestDegree, shuffle)
}

// requestPeerRounds tries up to two distinct groups of live peers before a
// topic database lookup. The exclusion list lasts only for this request.
func requestPeerRounds(ctx context.Context, source requestPeerSource, topic *pubsubManager.Topic, topicPeers []peer.ID, requestDegree int, shuffle func(int, func(int, int)), attempt func([]peer.ID) bool) {
	if requestDegree <= 0 {
		return
	}
	live := orderLiveRequestPeers(source.GetStreamPeers(), topicPeers, shuffle)
	if peerRequestTraceEnabled {
		if traceID, ok := ctx.Value(peerRequestTraceKey{}).(uint64); ok {
			log.Global.WithFields(log.Fields{
				"trace_id":           traceID,
				"topic":              topic.String(),
				"live_streams":       len(live),
				"gossip_topic_peers": len(topicPeers),
			}).Info("Peer request trace candidates")
		}
	}
	attempted := make([]peer.ID, 0, 2*requestDegree)
	for start, round := 0, 0; start < len(live) && round < 2; start, round = start+requestDegree, round+1 {
		end := min(start+requestDegree, len(live))
		peers := live[start:end]
		attempted = append(attempted, peers...)
		traceRequestRound(ctx, source, topic, "live", round+1, peers, topicPeers)
		if attempt(peers) || ctx.Err() != nil {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	// The database can contain stale records and is expensive under load, so
	// query it only on bootstrap or after the live rounds have failed.
	stored := topicRequestPeers(source, topic, attempted, requestDegree, shuffle)
	if len(stored) > 0 && ctx.Err() == nil {
		traceRequestRound(ctx, source, topic, "stored", 1, stored, topicPeers)
		attempt(stored)
	}
}

func (p *P2PNode) requestFromPeers(topic *pubsubManager.Topic, requestData interface{}, respDataType interface{}, resultChan chan interface{}) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		defer close(resultChan)
		traceID := uint64(0)
		traceCtx := p.ctx
		started := time.Now()
		var delivered atomic.Int64
		if peerRequestTraceEnabled {
			traceID = peerRequestTraceSequence.Add(1)
			traceCtx = context.WithValue(p.ctx, peerRequestTraceKey{}, traceID)
			target := ""
			switch request := requestData.(type) {
			case *big.Int:
				target = request.String()
			case common.Hash:
				target = request.Hex()
			}
			log.Global.WithFields(log.Fields{
				"trace_id":     traceID,
				"topic":        topic.String(),
				"request_type": reflect.TypeOf(requestData),
				"target":       target,
			}).Info("Peer request trace start")
			defer func() {
				log.Global.WithFields(log.Fields{
					"trace_id":   traceID,
					"topic":      topic.String(),
					"delivered":  delivered.Load(),
					"elapsed_ms": time.Since(started).Milliseconds(),
				}).Info("Peer request trace complete")
			}()
		}
		select {
		case <-p.ctx.Done():
			return
		default:
			topicPeers, err := p.pubsub.PeersForTopic(topic)
			if err != nil {
				log.Global.WithField("topic", topic.String()).Debug("No gossip topic peers available for request")
			}
			requestPeerRounds(traceCtx, p.peerManager, topic, topicPeers, topic.GetRequestDegree(), rand.Shuffle, func(peers []peer.ID) bool {
				log.Global.WithFields(log.Fields{
					"peers": peers,
					"topic": topic,
				}).Debug("Requesting data from peers")

				var requestWg sync.WaitGroup
				var succeeded atomic.Bool
				for _, peerID := range peers {
					// if we have exceeded the outbound rate limit for this peer, skip them for now
					if err := protocol.ProcRequestRate(peerID, false); err != nil {
						log.Global.Warnf("Exceeded request rate to peer %s", peerID)
						continue
					}
					requestWg.Add(1)
					go func(peerID peer.ID) {
						defer func() {
							if r := recover(); r != nil {
								log.Global.WithFields(log.Fields{
									"error":      r,
									"stacktrace": string(debug.Stack()),
								}).Error("Go-Quai Panicked")
							}
						}()
						defer requestWg.Done()
						if p.requestAndWait(peerID, topic, requestData, respDataType, resultChan, traceID) {
							succeeded.Store(true)
							delivered.Add(1)
						}
					}(peerID)
				}
				requestWg.Wait()
				return succeeded.Load()
			})
		}
	}()
}

func (p *P2PNode) requestAndWait(peerID peer.ID, topic *pubsubManager.Topic, reqData interface{}, respDataType interface{}, resultChan chan interface{}, traceID uint64) (sent bool) {
	started := time.Now()
	outcome := "error"
	defer func() {
		if r := recover(); r != nil {
			outcome = "panic"
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
		if peerRequestTraceEnabled {
			log.Global.WithFields(log.Fields{
				"trace_id":   traceID,
				"topic":      topic.String(),
				"peer":       peerID,
				"outcome":    outcome,
				"elapsed_ms": time.Since(started).Milliseconds(),
			}).Info("Peer request trace outcome")
		}
	}()
	var recvd interface{}
	var err error
	if recvd, err = p.requestFromPeer(peerID, topic, reqData, respDataType); err == nil {
		// A negative response from one peer must not win the race against valid
		// data from another peer.
		if recvd == nil {
			outcome = "empty"
			return false
		}
		log.Global.WithFields(log.Fields{
			"peerId": peerID,
			"topic":  topic.String(),
		}).Trace("Received data from peer")

		// The dial and response wait have their own deadlines. A slow dial
		// must not discard a valid reply when sending to the result channel.
		select {
		case resultChan <- recvd:
			outcome = "delivered"
			return true
		default:
			outcome = "channel_full"
			// Optionally log the missed send or handle it in another way
			log.Global.WithFields(log.Fields{
				"peerId":  peerID,
				"message": "Channel is full, data not sent",
			}).Info("Success Missed data send")
		}
	} else {
		if errors.Is(err, streamManager.ErrStreamNotFound) {
			outcome = "no_stream"
			return false
		}
		if errors.Is(err, streamManager.ErrPeerDialCooldown) {
			outcome = "dial_cooldown"
			return false
		}
		if strings.Contains(err.Error(), "peer did not respond in time") {
			outcome = "peer_timeout"
		}
		log.Global.WithFields(log.Fields{
			"peerId": peerID,
			"topic":  topic.String(),
			"info":   err,
		}).Info("Success requesting the data from peer")
	}
	return false
}

// Request a data from the network for the specified slice
func (p *P2PNode) Request(location common.Location, requestData interface{}, responseDataType interface{}) chan interface{} {
	topic, err := pubsubManager.NewTopic(p.pubsub.GetGenesis(), location, responseDataType)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"location": location.Name(),
			"dataType": reflect.TypeOf(responseDataType),
			"err":      err,
		}).Error("Error getting topic name")
		panic(err)
	}

	resultChan := make(chan interface{}, 10)
	// If it is a hash, first check to see if it is contained in the caches
	if hash, ok := requestData.(common.Hash); ok {
		result, ok := p.cacheGet(hash, responseDataType, location)
		if ok {
			resultChan <- result
			return resultChan
		}
	}

	p.requestFromPeers(topic, requestData, responseDataType, resultChan)
	// TODO: optimize with waitgroups or a doneChan to only query if no peers responded
	// Right now this creates too many streams, so don't call this until we have a better solution
	// p.queryDHT(location, requestData, responseDataType, resultChan)

	return resultChan
}

func (p *P2PNode) AdjustPeerQuality(peer p2p.PeerID, topic string, adjFn func(int) int) {
	p.peerManager.AdjustPeerQuality(peer, topic, adjFn)
}

func (p *P2PNode) ProtectPeer(peer p2p.PeerID) {
	log.Global.WithFields(log.Fields{
		"peer": peer,
	}).Debug("Protecting peer connection from pruning")

	p.peerManager.ProtectPeer(peer)
}

func (p *P2PNode) UnprotectPeer(peer p2p.PeerID) {
	log.Global.WithFields(log.Fields{
		"peer": peer,
	}).Debug("Unprotecting peer connection from pruning")

	p.peerManager.UnprotectPeer(peer)
}

func (p *P2PNode) BanPeer(peer p2p.PeerID) {
	if p.peerManager.IsProtectedPeer(peer) {
		log.Global.WithFields(log.Fields{
			"peer": peer,
		}).Warn("Skipping ban and disconnect for protected peer")
		return
	}

	log.Global.WithFields(log.Fields{
		"peer": peer,
	}).Warn("Banning peer for misbehaving")

	p.peerManager.BanPeer(peer)
	p.peerManager.GetHost().Network().ClosePeer(peer)
}

// Opens a new stream to the given peer using the given protocol ID
func (p *P2PNode) GetStream(peerID peer.ID) (network.Stream, error) {
	return p.peerManager.GetStream(peerID)
}

// Connects to the given peer
func (p *P2PNode) Connect(pi peer.AddrInfo) error {
	return p.peerManager.GetHost().Connect(p.ctx, pi)
}

// Search for a block in the node's cache, or query the consensus backend if it's not found in cache.
// Returns nil if the block is not found.
func (p *P2PNode) GetWorkObject(hash common.Hash, location common.Location) *types.WorkObject {
	return p.consensus.LookupBlock(hash, location)
}

// Search for the block in the data base and get the count number of the next blocks
func (p *P2PNode) GetWorkObjectsFrom(hash common.Hash, location common.Location, count int) []*types.WorkObjectBlockView {
	response := []*types.WorkObjectBlockView{}
	block := p.consensus.LookupBlock(hash, location)
	if block == nil {
		return nil
	}
	response = append(response, block.ConvertToBlockView())
	for i := 1; i < count; i++ {
		nextNumber := block.NumberU64(location.Context()) + uint64(i)
		next := p.consensus.LookupBlockByNumber(big.NewInt(int64(nextNumber)), location)
		if next == nil {
			return nil
		}
		// The parent hash has to be continuous
		if next.ParentHash(location.Context()) != response[i-1].Hash() {
			return nil
		}
		response = append(response, next.ConvertToBlockView())
	}
	return response
}

func (p *P2PNode) GetHeight(location common.Location) uint64 {
	return p.consensus.GetHeight(location)
}

func (p *P2PNode) GetBlockHashByNumber(number *big.Int, location common.Location) *common.Hash {
	return p.consensus.LookupBlockHashByNumber(number, location)
}

func (p *P2PNode) GetBlockByNumber(number *big.Int, location common.Location) *types.WorkObject {
	return p.consensus.LookupBlockByNumber(number, location)
}

func (p *P2PNode) handleBroadcast(sourcePeer peer.ID, Id string, topic string, data interface{}, nodeLocation common.Location) {
	if _, ok := acceptableTypes[reflect.TypeOf(data)]; !ok {
		log.Global.WithFields(log.Fields{
			"peer":  sourcePeer,
			"topic": topic,
			"type":  reflect.TypeOf(data),
		}).Warn("Received unsupported broadcast")
		return
	}

	switch v := data.(type) {
	case types.WorkObjectHeaderView:
		dt := uint64(time.Now().Unix()) - v.Time()
		headerPropagationHist.Observe(float64(dt))
		p.cacheAdd(v.Hash(), &v, nodeLocation)
	case types.WorkObjectBlockView:
		dt := uint64(time.Now().Unix()) - v.Time()
		blockPropagationHist.Observe(float64(dt))
		p.cacheAdd(v.Hash(), &v, nodeLocation)
	case types.AuxTemplate:
		// AuxTemplate doesn't have a timestamp, so we skip time measurement
		// We also don't cache it as it doesn't have a Hash() method
		log.Global.WithFields(log.Fields{
			"chainID": v.PowID(),
			"nbits":   v.Bits(),
		}).Debug("Received AuxTemplate broadcast")
	}

	// If we made it here, pass the data on to the consensus backend
	if p.consensus != nil {
		p.consensus.OnNewBroadcast(sourcePeer, Id, topic, data, nodeLocation)
	}
}
