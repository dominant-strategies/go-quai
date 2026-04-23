package node

import (
	"math/big"
	"reflect"
	"runtime/debug"
	"sync"
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
	propagationTimes      = metrics_config.NewHistogramVec("PropagationTimes", "message propagation times by type (sec)")
	blockPropagationHist  = propagationTimes.WithLabelValues("block propagation time (sec)")
	headerPropagationHist = propagationTimes.WithLabelValues("header propagation time (sec)")
)

const requestTimeout = 10 * time.Second

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

	p.startStaticPeerConnector()

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
		select {
		case <-p.ctx.Done():
			return
		default:
			desiredPeers := topic.GetRequestDegree()

			peers := make([]peer.ID, 0, desiredPeers)
			seen := make(map[peer.ID]struct{}, desiredPeers)

			// Prefer configured static peers first.
			// These are explicit dial targets and are typically more reliable
			// than opportunistically discovered peers in restricted networking
			// environments (Docker/K8s/firewalled NATs).
			for _, staticPeer := range p.staticPeers {
				if staticPeer.ID == "" || staticPeer.ID == p.peerManager.GetSelfID() {
					continue
				}
				if _, ok := seen[staticPeer.ID]; ok {
					continue
				}
				peers = append(peers, staticPeer.ID)
				seen[staticPeer.ID] = struct{}{}
			}

			// Optionally restrict request/response to static peers only.
			if !p.staticPeersOnly || len(peers) == 0 {
				// Use stream peers if the node has accumulated c_streamPeerThreshold number of
				// streams, otherwise look up peers from the database/DHT and create streams with them.
				candidates := p.peerManager.GetStreamPeers()
				if len(candidates) < c_streamPeerThreshold {
					peersMap := p.peerManager.GetPeers(topic)
					candidates = make([]peer.ID, 0, len(peersMap))
					for candidate := range peersMap {
						candidates = append(candidates, candidate)
					}
				}

				for _, candidate := range candidates {
					if len(peers) >= desiredPeers {
						break
					}
					if _, ok := seen[candidate]; ok {
						continue
					}
					peers = append(peers, candidate)
					seen[candidate] = struct{}{}
				}
			}

			if len(peers) > desiredPeers {
				peers = peers[:desiredPeers]
			}

			log.Global.WithFields(log.Fields{
				"peers": peers,
				"topic": topic,
			}).Debug("Requesting data from peers")

			var requestWg sync.WaitGroup
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
					p.requestAndWait(peerID, topic, requestData, respDataType, resultChan)
				}(peerID)
			}
			requestWg.Wait()
		}
	}()
}

func (p *P2PNode) requestAndWait(peerID peer.ID, topic *pubsubManager.Topic, reqData interface{}, respDataType interface{}, resultChan chan interface{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	var recvd interface{}
	var err error
	requestTimer := time.NewTimer(requestTimeout)
	defer requestTimer.Stop()
	if recvd, err = p.requestFromPeer(peerID, topic, reqData, respDataType); err == nil {
		log.Global.WithFields(log.Fields{
			"peerId": peerID,
			"topic":  topic.String(),
		}).Trace("Received data from peer")

		select {
		case resultChan <- recvd:
			// Data sent successfully
		case <-requestTimer.C:
			// Request timed out, return
			log.Global.WithFields(log.Fields{
				"peerId":  peerID,
				"message": "Request timed out, data not received",
			}).Info("Success Missed data request")
			// Mark this peer as not responding
			p.peerManager.AdjustPeerQuality(peerID, topic.String(), p2p.QualityAdjOnTimeout)
		default:
			// Optionally log the missed send or handle it in another way
			log.Global.WithFields(log.Fields{
				"peerId":  peerID,
				"message": "Channel is full, data not sent",
			}).Info("Success Missed data send")
		}
	} else {
		if err.Error() == streamManager.ErrStreamNotFound.Error() {
			return
		}
		log.Global.WithFields(log.Fields{
			"peerId": peerID,
			"topic":  topic.String(),
			"info":   err,
		}).Info("Success requesting the data from peer")
	}
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
