package pubsubManager

import (
	"context"
	"errors"
	"strings"

	"github.com/dominant-strategies/go-quai/common"
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
func TopicName(location common.Location, datatype interface{}) (string, error) {
	switch datatype.(type) {
	case *types.Block:
		return location.Name() + "/blocks", nil
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
func (g *PubsubManager) Subscribe(location common.Location, datatype interface{}) error {
	// build topic name
	topicName, err := TopicName(location, datatype)
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
func (g *PubsubManager) Broadcast(location common.Location, datatype interface{}) error {
	topicName, err := TopicName(location, datatype)
	if err != nil {
		return err
	}
	protoData, err := pb.ConvertAndMarshal(datatype)
	if err != nil {
		return err
	}
	return g.topics[topicName].Publish(g.ctx, protoData)
}

// lists our peers which provide the associated topic
func (g *PubsubManager) PeersForTopic(location common.Location, datatype interface{}) ([]peer.ID, error) {
	topicName, err := TopicName(location, datatype)
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

			topic := msg.GetTopic()
			log.Debugf("received message on topic: %s", topic)

			// switch on the topic name to determine the type of data received
			var data interface{}
			switch {
			case strings.Contains(topic, "blocks"):
				data := new(types.Block)
				err = pb.UnmarshalAndConvert(msg.Data, data)
				if err != nil {
					log.Errorf("error unmarshalling block: %s", err)
					continue
				}
			default:
				log.Errorf("unsupported topic: %s", topic)
				continue
			}

			// handle the received data
			if g.onReceived != nil {
				g.onReceived(data)
			}
		}
	}
}
