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
	ps            *gossip.PubSub
	subscriptions []*gossip.Subscription
	topics        []*gossip.Topic
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
	log.Tracef("joining topic %s", topic)
	topicHandle, err := manager.ps.Join(topic)
	if err != nil {
		return nil, err
	}
	manager.topics = append(manager.topics, topicHandle)
	log.Tracef("joined topic %s", topic)
	return topicHandle, nil
}

// Join a topic and subscribe to it
func (manager *PubSubManager) Subscribe(topic string) (*gossip.Subscription, error) {
	topicHandle, err := manager.ps.Join(topic)
	if err != nil {
		return nil, err
	}
	sub, err := topicHandle.Subscribe()
	if err != nil {
		return nil, err
	}
	manager.subscriptions = append(manager.subscriptions, sub)
	return sub, nil
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

// ListPeers lists the peers we are connected to for a given topic
func (manager *PubSubManager) ListPeers(topic string) []peer.ID {
	return manager.ps.ListPeers(topic)
}

// Close the pubsub service by cancelling all active subscriptions
// and leaving all joined topics
func (manager *PubSubManager) close() {
	// Cancel all active subscriptions
	for _, sub := range manager.subscriptions {
		sub.Cancel()
	}

	// Leave all joined topics
	for _, topic := range manager.topics {
		topic.Close()
	}
}

// Wrapper for the close method that returns an error type (nil)
func (manager *PubSubManager) Stop() error {
	manager.close()
	return nil
}
