package node

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// Starts the node and all of its services
func (p *P2PNode) Start() error {
	log.Infof("starting P2P node...")

	// Start any async processes belonging to this node
	log.Debugf("starting node processes...")
	go p.eventLoop()
	go p.statsLoop()

	// Is this node expected to have bootstrap peers to dial?
	if !viper.GetBool(utils.BootNodeFlag.Name) && !viper.GetBool(utils.SoloFlag.Name) && len(p.bootpeers) == 0 {
		err := errors.New("no bootpeers provided. Unable to join network")
		log.Errorf("%s", err)
		return err
	}

	// Register the Quai protocol handler
	p.SetStreamHandler(quaiprotocol.ProtocolVersion, func(s network.Stream) {
		quaiprotocol.QuaiProtocolHandler(s, p)
	})

	// If the node is a bootnode, start the bootnode service
	if viper.GetBool(utils.BootNodeFlag.Name) {
		log.Infof("starting node as a bootnode...")
		return nil
	}

	// Open data streams with connected Quai peers
	go quaiprotocol.OpenPeerStreams(p)

	return nil
}

type stopFunc func() error

// Function to gracefully shtudown all running services
func (p *P2PNode) Stop() error {
	// define a list of functions to stop the services the node is running
	stopFuncs := []stopFunc{
		p.Host.Close,
		p.dht.Close,
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
				log.Errorf("error during shutdown: %s", err)
				allErrors = append(allErrors, err)
			}
		case <-time.After(5 * time.Second):
			err := errors.New("timeout during shutdown")
			log.Warnf("error: %s", err)
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

func (p *P2PNode) SetConsensusBackend(be common.ConsensusAPI) {
	p.consensus = be
}

func (p *P2PNode) BroadcastBlock(slice types.SliceID, block types.Block) error {
	return p.pubsub.BroadcastBlock(p.topics[slice][p2p.C_blockTopicName], block)
}

func (p *P2PNode) BroadcastTransaction(tx types.Transaction) error {
	panic("todo")
}

// Request a block from the network for the specified slice
func (p *P2PNode) RequestBlock(hash types.Hash, slice types.SliceID) chan *types.Block {
	resultChan := make(chan *types.Block, 1)
	go func() {
		defer close(resultChan)
		var block *types.Block
		// 1. Check if the block is in the local cache
		block, ok := p.blockCache.Get(hash)
		if ok {
			log.Debugf("Block %s found in cache", hash)
			resultChan <- block
			return
		}
		// 2. If not, query the topic peers for the block
		peers := p.topics[slice][p2p.C_blockTopicName].ListPeers()
		for _, peerID := range peers {
			block, err := p.requestBlockFromPeer(hash, peerID)
			if err == nil {
				log.Debugf("Received block %s from peer %s", block.Hash, peerID)
				// add the block to the cache
				p.blockCache.Add(hash, block)
				// send the block to the result channel
				resultChan <- block
				return
			}
		}

		// 3. If block is not found, query the DHT for peers in the slice
		// TODO: evaluate making this configurable
		const (
			maxDHTQueryRetries    = 3  // Maximum number of retries for DHT queries
			peersPerDHTQuery      = 10 // Number of peers to query per DHT attempt
			dhtQueryRetryInterval = 5  // Time to wait between DHT query retries
		)
		// create a Cid from the slice ID
		shardCid := shardToCid(slice)
		for retries := 0; retries < maxDHTQueryRetries; retries++ {
			log.Debugf("Querying DHT for slice Cid %s (retry %d)", shardCid, retries)
			// query the DHT for peers in the slice
			peerChan := p.dht.FindProvidersAsync(p.ctx, shardCid, peersPerDHTQuery)
			for peerInfo := range peerChan {
				block, err := p.requestBlockFromPeer(hash, peerInfo.ID)
				if err == nil {
					log.Debugf("Received block %s from peer %s", block.Hash, peerInfo.ID)
					p.blockCache.Add(hash, block)
					resultChan <- block
					return
				}
			}
			// if the block is not found, wait for a bit and try again
			log.Debugf("Block %s not found in slice %s. Retrying...", hash, slice)
			time.Sleep(dhtQueryRetryInterval * time.Second)
		}
		log.Debugf("Block %s not found in slice %s", hash, slice)
	}()
	return resultChan
}

func (p *P2PNode) RequestTransaction(hash types.Hash, loc types.SliceID) chan *types.Transaction {
	panic("todo")
}

func (p *P2PNode) ReportBadPeer(peer p2p.PeerID) {
	panic("todo")
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

// Start gossipsub protocol
func (p *P2PNode) StartGossipSub(ctx context.Context) error {
	for _, slice := range p.consensus.GetRunningSlices() {
		blockTopic, err := p.pubsub.Join(slice.SliceID.String() + "/" + p2p.C_blockTopicName)
		if err != nil {
			return err
		}
		sub, err := blockTopic.Subscribe()
		if err != nil {
			return err
		}

		go p.handleBlocksSubscription(sub)

		sliceTopics, exists := p.topics[slice.SliceID]
		if !exists {
			sliceTopics = make(map[string]*pubsub.Topic)
		}
		sliceTopics[p2p.C_blockTopicName] = blockTopic
		p.topics[slice.SliceID] = sliceTopics
	}
	return nil
}
