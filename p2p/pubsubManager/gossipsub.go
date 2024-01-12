package pubsubManager

import (
	"context"
	"errors"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

var (
	ErrUnsupportedType = errors.New("data type not supported")
)

type PubsubManager struct {
	*pubsub.PubSub
	ctx           context.Context
	subscriptions map[string]*pubsub.Subscription
	topics        map[string]*pubsub.Topic

	// Callback function to handle received data
	onReceived func(interface{})
}

// gets the name of the topic for the given type of data
func TopicName(slice types.SliceID, data interface{}) (string, error) {
	switch data.(type) {
	case types.Block:
		return slice.String() + "/blocks", nil
	default:
		return "", ErrUnsupportedType
	}
}

// creates a new gossipsub instance
// TODO: what options do we need for quai network? See:
// See https://pkg.go.dev/github.com/libp2p/go-libp2p-pubsub@v0.10.0#Option
func NewGossipSubManager(ctx context.Context, h host.Host) (*PubsubManager, error) {
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, err
	}
	return &PubsubManager{
		ps,
		ctx,
		make(map[string]*pubsub.Subscription),
		make(map[string]*pubsub.Topic),
		nil,
	}, nil
}

func (g *PubsubManager) Start(receiveCb func(interface{})) {
	g.onReceived = receiveCb
	go g.handleSubscriptions()
}

// subscribe to broadcasts of the given type of data
func (g *PubsubManager) Subscribe(slice types.SliceID, data interface{}) error {
	// build topic name
	topicName, err := TopicName(slice, data)
	if err != nil {
		return err
	}

	// join the topic
	topic, err := g.Join(topicName)
	if err != nil {
		return err
	}
	g.topics[topicName] = topic

	// subscribe to the topic
	subscription, err := topic.Subscribe()
	if err != nil {
		return err
	}
	g.subscriptions[topicName] = subscription

	return nil
}

// broadcasts data to subscribing peers
func (g *PubsubManager) Broadcast(slice types.SliceID, data interface{}) error {
	topicName, err := TopicName(slice, data)
	if err != nil {
		return err
	}
	return g.topics[topicName].Publish(g.ctx, pb.MarshalData(data))
}

// lists our peers which provide the associated topic
func (g *PubsubManager) PeersForTopic(slice types.SliceID, data interface{}) ([]peer.ID, error) {
	topicName, err := TopicName(slice, data)
	if err != nil {
		return nil, err
	}
	return g.topics[topicName].ListPeers(), nil
}

// handles any data received on any of our subscribed topics
func (g *PubsubManager) handleSubscriptions() {
	for {
		for _, sub := range g.subscriptions {
			msg, err := sub.Next(g.ctx)
			if err != nil {
				// if context was cancelled, then we are shutting down
				if g.ctx.Err() != nil {
					return
				}
				log.Errorf("error getting next message from subscription: %s", err)
				continue
			}

			// unmarshal the received data
			block, err := pb.UnmarshalBlock(msg.Data)
			if err != nil {
				log.Errorf("error unmarshalling block: %s", err)
				continue
			}

			// handle the received data
			if g.onReceived != nil {
				g.onReceived(block)
			}
		}
	}
}
