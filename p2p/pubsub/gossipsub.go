package pubsub

import (
	"context"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
)

const BlockTopicName = "quai-blocks"

type PubsubManager struct {
	*pubsub.PubSub
	ctx context.Context
}

// creates a new gossipsub instance
// TODO: what options do we need for quai network? See:
// See https://pkg.go.dev/github.com/libp2p/go-libp2p-pubsub@v0.10.0#Option
func NewGossipSubManager(ctx context.Context, h host.Host) (*PubsubManager, error) {
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, err
	}
	return &PubsubManager{PubSub: ps, ctx: ctx}, nil
}

// Broadcast a block to the network by publishing it to the block pubsub topic
func (g *PubsubManager) PublishBlock(data []byte) error {
	// join the block topic
	blockTopic, err := g.Join(BlockTopicName)
	if err != nil {
		return err
	}

	// publish the block
	if err := blockTopic.Publish(g.ctx, data); err != nil {
		return err
	}

	return nil
}

// Subscribe to the block pubsub topic
func (g *PubsubManager) SubscribeBlock() (*pubsub.Subscription, error) {
	// join the block topic
	blockTopic, err := g.Join(BlockTopicName)
	if err != nil {
		return nil, err
	}

	// subscribe to the block topic
	sub, err := blockTopic.Subscribe()
	if err != nil {
		return nil, err
	}

	return sub, nil
}
