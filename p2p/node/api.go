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
	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/p2p/node/peerManager"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/quai"
	"github.com/dominant-strategies/go-quai/trie"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/common"
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
		quaiprotocol.QuaiProtocolHandler(s, p)
	})

	// Start the pubsub manager
	p.pubsub.SetReceiveHandler(p.handleBroadcast)

	return nil
}

func (p *P2PNode) Subscribe(location common.Location, datatype interface{}) error {
	err := p.pubsub.Subscribe(location, datatype)
	if err != nil {
		return err
	}

	return p.peerManager.Provide(p.ctx, location, datatype)
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

func (p *P2PNode) requestFromPeers(topic *pubsubManager.Topic, requestData interface{}, resultChan chan interface{}) {
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
		peers := p.peerManager.GetPeers(topic, peerManager.Best)
		log.Global.WithFields(log.Fields{
			"peers": peers,
			"topic": topic,
		}).Debug("Requesting data from peers")

		var requestWg sync.WaitGroup
		for peerID := range peers {
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
				p.requestAndWait(peerID, topic, requestData, resultChan)
			}(peerID)
		}
		requestWg.Wait()
	}()
}

func (p *P2PNode) requestAndWait(peerID peer.ID, topic *pubsubManager.Topic, reqData interface{}, resultChan chan interface{}) {
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
	if recvd, err = p.requestFromPeer(peerID, topic, reqData); err == nil {
		log.Global.WithFields(log.Fields{
			"peerId": peerID,
			"topic":  topic.String(),
		}).Trace("Received data from peer")

		// Mark this peer as behaving well
		p.peerManager.MarkResponsivePeer(peerID, topic)
		select {
		case resultChan <- recvd:
			// Data sent successfully
		default:
			// Optionally log the missed send or handle it in another way
			log.Global.WithFields(log.Fields{
				"peerId":  peerID,
				"message": "Channel is full, data not sent",
			}).Warning("Missed data send")
		}
	} else {
		log.Global.WithFields(log.Fields{
			"peerId": peerID,
			"topic":  topic.String(),
			"err":    err,
		}).Error("Error requesting the data from peer")
		// Mark this peer as not responding
		p.peerManager.MarkUnresponsivePeer(peerID, topic)
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

	p.requestFromPeers(topic, requestData, resultChan)
	// TODO: optimize with waitgroups or a doneChan to only query if no peers responded
	// Right now this creates too many streams, so don't call this until we have a better solution
	// p.queryDHT(location, requestData, responseDataType, resultChan)

	return resultChan
}

func (p *P2PNode) MarkLivelyPeer(peer p2p.PeerID, topic string) {
	log.Global.WithFields(log.Fields{
		"peer":  peer,
		"topic": topic,
	}).Debug("Recording well-behaving peer")

	t, err := pubsubManager.TopicFromString(p.pubsub.GetGenesis(), topic)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"topic": topic,
			"err":   err,
		}).Error("Error getting topic name")
		panic(err)
	}

	p.peerManager.MarkLivelyPeer(peer, t)
}

func (p *P2PNode) MarkLatentPeer(peer p2p.PeerID, topic string) {
	log.Global.WithFields(log.Fields{
		"peer":  peer,
		"topic": topic,
	}).Debug("Recording misbehaving peer")

	t, err := pubsubManager.TopicFromString(p.pubsub.GetGenesis(), topic)
	if err != nil {
		log.Global.WithFields(log.Fields{
			"topic": topic,
			"err":   err,
		}).Error("Error getting topic name")
		panic(err)
	}

	p.peerManager.MarkLatentPeer(peer, t)
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

// Returns the list of bootpeers
func (p *P2PNode) GetBootPeers() []peer.AddrInfo {
	return p.bootpeers
}

// Opens a new stream to the given peer using the given protocol ID
func (p *P2PNode) NewStream(peerID peer.ID) (network.Stream, error) {
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

func (p *P2PNode) GetBlockHashByNumber(number *big.Int, location common.Location) *common.Hash {
	return p.consensus.LookupBlockHashByNumber(number, location)
}

func (p *P2PNode) GetHeader(hash common.Hash, location common.Location) *types.WorkObject {
	panic("TODO: implement")
}

func (p *P2PNode) GetTrieNode(hash common.Hash, location common.Location) *trie.TrieNodeResponse {
	return p.consensus.GetTrieNode(hash, location)
}

func (p *P2PNode) handleBroadcast(sourcePeer peer.ID, topic string, data interface{}, nodeLocation common.Location) {
	switch v := data.(type) {
	case types.WorkObject:
		p.cacheAdd(v.Hash(), &v, nodeLocation)
		log.Global.WithField("context", v.Location().Context()).Print("")
	// TODO: send it to consensus
	case types.Transactions:
	case types.WorkObjectHeader:
	default:
		log.Global.Debugf("received unsupported block broadcast")
		// TODO: ban the peer which sent it?
		return
	}

	// If we made it here, pass the data on to the consensus backend
	if p.consensus != nil {
		p.consensus.OnNewBroadcast(sourcePeer, topic, data, nodeLocation)
	}
}
