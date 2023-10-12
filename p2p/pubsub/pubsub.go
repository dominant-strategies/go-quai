package pubsub

import (
	"context"

	"github.com/dominant-strategies/go-quai/log"
	gossip "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Base instance for the pub-sub service.
// It manages the Gossipsub instance and provides utility methods
type PubSubManager struct {
	ps *gossip.PubSub
}

func NewPubSubManager(ctx context.Context, h host.Host, opts []gossip.Option) (*PubSubManager, error) {
	ps, err := gossip.NewGossipSub(ctx, h, opts...)
	if err != nil {
		return nil, err
	}
	return &PubSubManager{ps: ps}, nil
}

// Join a topic
func (manager *PubSubManager) Join(topic string) (*gossip.Topic, error) {
	return manager.ps.Join(topic)
}

// Join a topic and subscribe to it
func (manager *PubSubManager) Subscribe(topic string) (*gossip.Subscription, error) {
	topicHandle, err := manager.ps.Join(topic)
	if err != nil {
		log.Errorf("error joining topic: %s", err)
		return nil, err
	}
	return topicHandle.Subscribe()
}

// Publish a message to a topic
func (manager *PubSubManager) Publish(topic string, data []byte) error {
	topicHandle, err := manager.ps.Join(topic)
	if err != nil {
		log.Errorf("error joining topic: %s", err)
		return err
	}
	return topicHandle.Publish(context.Background(), data)
}

func (manager *PubSubManager) ListPeers(topic string) []peer.ID {
	return manager.ps.ListPeers(topic)
}
