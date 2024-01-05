package pubsubManager

import (
	"context"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"google.golang.org/protobuf/proto"
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
	return &PubsubManager{ps, ctx}, nil
}

// Broadcast a block to the network by publishing it to the block pubsub topic
func (g *PubsubManager) BroadcastBlock(topic *pubsub.Topic, block types.Block) error {
	// Convert block to protobuf format
	protoBlock := pb.ConvertToProtoBlock(block)

	// Serialize the protobuf block
	blockData, err := proto.Marshal(protoBlock)
	if err != nil {
		return err
	}

	// Use the pubsub package to publish the block
	return topic.Publish(g.ctx, blockData)
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
