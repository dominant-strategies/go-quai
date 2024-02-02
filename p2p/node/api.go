package node

import (
	"math/big"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/quai"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/dominant-strategies/go-quai/common"
)

// Starts the node and all of its services
func (p *P2PNode) Start() error {
	log.Global.Infof("starting P2P node...")

	// Start any async processes belonging to this node
	log.Global.Debugf("starting node processes...")
	go p.eventLoop()
	go p.statsLoop()

	// Is this node expected to have bootstrap peers to dial?
	if !viper.GetBool(utils.BootNodeFlag.Name) && !viper.GetBool(utils.SoloFlag.Name) && len(p.bootpeers) == 0 {
		err := errors.New("no bootpeers provided. Unable to join network")
		log.Global.Errorf("%s", err)
		return err
	}

	// Register the Quai protocol handler
	p.SetStreamHandler(quaiprotocol.ProtocolVersion, func(s network.Stream) {
		quaiprotocol.QuaiProtocolHandler(s, p)
	})

	// If the node is a bootnode, start the bootnode service
	if viper.GetBool(utils.BootNodeFlag.Name) {
		log.Global.Infof("starting node as a bootnode...")
		return nil
	}

	// Start the pubsub manager
	p.pubsub.Start(p.handleBroadcast)

	return nil
}

func (p *P2PNode) Subscribe(location common.Location, datatype interface{}) error {
	return p.pubsub.Subscribe(location, datatype)
}

func (p *P2PNode) Broadcast(location common.Location, data interface{}) error {
	return p.pubsub.Broadcast(location, data)
}

func (p *P2PNode) SetConsensusBackend(be quai.ConsensusAPI) {
	p.consensus = be
}

type stopFunc func() error

// Function to gracefully shtudown all running services
func (p *P2PNode) Stop() error {
	// define a list of functions to stop the services the node is running
	stopFuncs := []stopFunc{
		p.Host.Close,
		p.dht.Close,
		p.peerManager.Stop,
	}
	// create a channel to collect errors
	errs := make(chan error, len(stopFuncs))
	// run each stop function in a goroutine
	for _, fn := range stopFuncs {
		go func(fn stopFunc) {
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

// RequestByNumber returns the hash for the given block number
func (p *P2PNode) RequestByNumber(location common.Location, number *big.Int, datatype interface{}) chan interface{} {
	resultChan := make(chan interface{}, 1)
	go func() {
		defer close(resultChan)
		// 1. Query the topic peers for the data
		peers, err := p.pubsub.PeersForTopic(location, datatype)
		if err != nil {
			log.Global.Errorf("Error requesting data: ", err)
			return
		}
		var requestWg sync.WaitGroup
		for _, peerID := range peers {
			requestWg.Add(1)
			go func(peerID peer.ID) {
				defer requestWg.Done()
				p.requestAndWait(peerID, location, number, datatype, resultChan)
			}(peerID)
		}
		requestWg.Wait()

		// 2. If hash is not found, query the DHT for peers in the slice
		// TODO: evaluate making this configurable
		const (
			maxDHTQueryRetries    = 3  // Maximum number of retries for DHT queries
			peersPerDHTQuery      = 10 // Number of peers to query per DHT attempt
			dhtQueryRetryInterval = 5  // Time to wait between DHT query retries
		)
		// create a Cid from the slice location
		shardCid := locationToCid(location)
		for retries := 0; retries < maxDHTQueryRetries; retries++ {
			log.Global.Infof("Querying DHT for slice Cid %s (retry %d)", shardCid, retries)
			// query the DHT for peers in the slice
			// TODO: need to find providers of a topic, not a shard
			for peer := range p.dht.FindProvidersAsync(p.ctx, shardCid, peersPerDHTQuery) {
				go p.requestAndWait(peer.ID, location, number, datatype, resultChan)
			}
			// if the data is not found, wait for a bit and try again
			log.Global.Infof("Block %s not found in slice %s. Retrying...", number, location)
			time.Sleep(dhtQueryRetryInterval * time.Second)
		}
		log.Global.Infof("Block %s not found in slice %s", number, location)
	}()
	return resultChan
}

func (p *P2PNode) RequestByHash(location common.Location, hash common.Hash, datatype interface{}) chan interface{} {
	resultChan := make(chan interface{}, 1)
	go func() {
		defer close(resultChan)

		// 2. If not, query the topic peers for the data
		var requestWg sync.WaitGroup
		peerList, err := p.peerManager.GetBestPeers()
		if err != nil {
			log.Global.Errorf("error getting best peers: %s", err)
			resultChan <- err
			return
		}
		for _, peerID := range peerList {
			requestWg.Add(1)
			go func(peerID peer.ID) {
				defer requestWg.Done()
				p.requestAndWait(peerID, location, hash, datatype, resultChan)
			}(peerID)
		}

		// Wait for all the requests to finish before exiting the Request loop
		requestWg.Wait()

		// 3. If block is not found, query the DHT for peers in the slice
		// TODO: evaluate making this configurable
		const (
			maxDHTQueryRetries    = 3  // Maximum number of retries for DHT queries
			peersPerDHTQuery      = 10 // Number of peers to query per DHT attempt
			dhtQueryRetryInterval = 5  // Time to wait between DHT query retries
		)
		// create a Cid from the slice location
		shardCid := locationToCid(location)
		for retries := 0; retries < maxDHTQueryRetries; retries++ {
			log.Global.Debugf("Querying DHT for slice Cid %s (retry %d)", shardCid, retries)
			// query the DHT for peers in the slice
			// TODO: need to find providers of a topic, not a shard
			for peer := range p.dht.FindProvidersAsync(p.ctx, shardCid, peersPerDHTQuery) {
				go p.requestAndWait(peer.ID, location, hash, datatype, resultChan)
			}
			// if the data is not found, wait for a bit and try again
			log.Global.Debugf("Block %s not found in slice %s. Retrying...", hash, location)
			time.Sleep(dhtQueryRetryInterval * time.Second)
		}
		log.Global.Debugf("Block %s not found in slice %s", hash, location)
	}()
	return resultChan
}

func (p *P2PNode) requestAndWait(peerID peer.ID, location common.Location, data interface{}, datatype interface{}, resultChan chan interface{}) {
	// Ask peer and wait for response
	if recvd, err := p.requestFromPeer(peerID, location, data, datatype); err == nil {
		log.Global.WithFields(log.Fields{
			"data":   data,
			"peerId": peerID,
		}).Trace("Received data from peer")
		// send the block to the result channel
		resultChan <- recvd

		// Mark this peer as behaving well
		p.peerManager.MarkResponsivePeer(peerID)
	} else {
		log.Global.WithFields(log.Fields{
			"peerId":   peerID,
			"location": location.Name(),
			"data":     data,
			"datatype": datatype,
		}).Trace("Error requesting the data from peer")
		// Mark this peer as not responding
		p.peerManager.MarkUnresponsivePeer(peerID)
	}
}

// Request a data from the network for the specified slice
func (p *P2PNode) Request(location common.Location, requestData interface{}, responseDataType interface{}) chan interface{} {
	switch requestDataType := requestData.(type) {
	case common.Hash:
		return p.RequestByHash(location, requestData.(common.Hash), responseDataType)
	case *big.Int:
		return p.RequestByNumber(location, requestData.(*big.Int), responseDataType)
	default:
		log.Global.WithFields(log.Fields{
			"requestDataType": requestDataType,
		}).Error("Unsupported Request Type")
	}
	return nil
}

func (p *P2PNode) MarkLivelyPeer(peer p2p.PeerID) {
	log.Global.WithFields(log.Fields{
		"peer": peer,
	}).Debug("Recording well-behaving peer")

	p.peerManager.MarkLivelyPeer(peer)
}

func (p *P2PNode) MarkLatentPeer(peer p2p.PeerID) {
	log.Global.WithFields(log.Fields{
		"peer": peer,
	}).Debug("Recording misbehaving peer")

	p.peerManager.MarkLatentPeer(peer)
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
	p.Host.Network().ClosePeer(peer)
}

// Returns the list of bootpeers
func (p *P2PNode) GetBootPeers() []peer.AddrInfo {
	return p.bootpeers
}

// Opens a new stream to the given peer using the given protocol ID
func (p *P2PNode) NewStream(peerID peer.ID, protocolID protocol.ID) (network.Stream, error) {
	return p.Host.NewStream(p.ctx, peerID, protocolID)
}

// Connects to the given peer
func (p *P2PNode) Connect(pi peer.AddrInfo) error {
	return p.Host.Connect(p.ctx, pi)
}

// Search for a block in the node's cache, or query the consensus backend if it's not found in cache.
// Returns nil if the block is not found.
func (p *P2PNode) GetBlock(hash common.Hash, location common.Location) *types.Block {
	return p.consensus.LookupBlock(hash, location)
}

func (p *P2PNode) GetBlockHashByNumber(number *big.Int, location common.Location) *common.Hash {
	return p.consensus.LookupBlockHashByNumber(number, location)
}

func (p *P2PNode) GetHeader(hash common.Hash, location common.Location) *types.Header {
	panic("TODO: implement")
}

func (p *P2PNode) handleBroadcast(sourcePeer peer.ID, data interface{}, nodeLocation common.Location) {
	switch v := data.(type) {
	case types.Block:
		p.cacheAdd(v.Hash(), &v)
	// TODO: send it to consensus
	case types.Transaction:
	default:
		log.Global.Debugf("received unsupported block broadcast")
		// TODO: ban the peer which sent it?
		return
	}

	// If we made it here, pass the data on to the consensus backend
	if p.consensus != nil {
		p.consensus.OnNewBroadcast(sourcePeer, data, nodeLocation)
	}
}
