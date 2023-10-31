package node

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/dominant-strategies/go-quai/config"
	"github.com/dominant-strategies/go-quai/log"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/viper"
)

// starts discovery services for the libp2p node.
// DHT is bootstrapped if the node is not running as a bootstrap server.
// mDNS is started
func (p *P2PNode) Start() error {
	server := viper.GetBool(config.BOOTNODE)
	if server {
		log.Debugf("starting as a bootstrap server. Bypassing DHT bootstrap")
	} else {
		// bootstrap the DHT
		log.Infof("bootstrapping DHT...")
		bootstrapPeers := viper.GetStringSlice(config.BOOTSTRAP_PEERS)
		log.Debugf("using bootstrap peers: %v", bootstrapPeers)
		if err := p.bootstrapDHT(bootstrapPeers...); err != nil {
			return errors.Wrap(err, "error bootstrapping DHT")
		}
	}

	// start mDNS
	log.Debugf("starting mDNS discovery...")
	if err := p.mDNS.Start(); err != nil {
		return errors.Wrap(err, "error starting mDNS")
	}

	// Start the event handler
	go p.eventLoop()

	return nil
}

type stopFunc func() error

// Function to gracefully shtudown all running services
func (p *P2PNode) Shutdown() error {
	// TODO: refactor this to use a dynamic list of stop functions
	// define a list of functions to stop the services the node is running
	stopFuncs := []stopFunc{
		p.mDNS.Stop,
		p.dht.Stop,
		p.ps.Stop,
		p.Host.Close,
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

// ******* DHT methods ******* //

// FindPeer finds a peer within the DHT using the given peer ID.
func (p *P2PNode) FindPeer(ctx context.Context, peerID peer.ID) (peer.AddrInfo, error) {
	return p.dht.FindPeer(ctx, peerID)
}

// RoutingTable returns the routing table of the DHT.
func (p *P2PNode) GetPeers() []peer.ID {
	return p.dht.GetPeers()
}

// ******* PubSub methods ******* //

// Join a PubSub topic
func (p *P2PNode) Join(topic string) (*pubsub.Topic, error) {
	return p.ps.Join(topic)
}

// Subscribe to a PubSub topic
func (p *P2PNode) Subscribe(topic string) (*pubsub.Subscription, error) {
	return p.ps.Subscribe(topic)
}

// Publish a message to a PubSub topic
func (p *P2PNode) Publish(topic string, data []byte) error {
	return p.ps.Publish(topic, data)
}

// ListPeers lists the peers we are connected to for a given topic
func (p *P2PNode) ListPeers(topic string) []peer.ID {
	return p.ps.ListPeers(topic)
}
