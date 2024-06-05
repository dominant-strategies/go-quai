package pubsubManager

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/quai"
)

const numWorkers = 10   // Number of workers per stream
const msgChanSize = 500 // 500 requests per subscription

var (
	ErrUnsupportedType = errors.New("data type not supported")
)

type PubsubManager struct {
	*pubsub.PubSub
	ctx           context.Context
	subscriptions *sync.Map
	topics        *sync.Map
	consensus     quai.ConsensusAPI
	genesis       common.Hash

	// Callback function to handle received data
	onReceived func(peer.ID, string, string, interface{}, common.Location)
}

// creates a new gossipsub instance
// TODO: what options do we need for quai network? See:
// See https://pkg.go.dev/github.com/libp2p/go-libp2p-pubsub@v0.10.0#Option
func NewGossipSubManager(ctx context.Context, h host.Host) (*PubsubManager, error) {
	cfg := pubsub.DefaultGossipSubParams()
	cfg.D = 30
	cfg.Dlo = 6
	cfg.Dhi = 45
	cfg.Dout = 20
	ps, err := pubsub.NewGossipSub(ctx, h, pubsub.WithGossipSubParams(cfg))
	if err != nil {
		return nil, err
	}
	return &PubsubManager{
		ps,
		ctx,
		new(sync.Map),
		new(sync.Map),
		nil,
		utils.MakeGenesis().ToBlock(0).Hash(),
		nil,
	}, nil
}

func (g *PubsubManager) GetGenesis() common.Hash {
	return g.genesis
}

func (g *PubsubManager) SetQuaiBackend(consensus quai.ConsensusAPI) {
	g.UnsubscribeAll()      // First unsubscribe from existing topics, if already registered
	g.consensus = consensus // Set new backend

}

func (g *PubsubManager) SetReceiveHandler(receiveCb func(peer.ID, string, string, interface{}, common.Location)) {
	g.onReceived = receiveCb
}

func (g *PubsubManager) Stop() error {
	g.UnsubscribeAll()
	return nil
}

func (g *PubsubManager) UnsubscribeAll() {
	g.subscriptions.Range(func(key, value any) bool {
		value.(*pubsub.Subscription).Cancel()
		g.subscriptions.Delete(key)
		return true
	})
	g.topics.Range(func(key, value any) bool {
		value.(*pubsub.Topic).Close()
		g.topics.Delete(key)
		return true
	})
}

// subscribe to broadcasts of the given type of data
func (g *PubsubManager) Subscribe(location common.Location, datatype interface{}) error {
	// build topic name
	topicSub, err := NewTopic(g.genesis, location, datatype)
	if err != nil {
		return err
	}

	// join the topic
	topic, err := g.Join(topicSub.String())
	if err != nil {
		return err
	}
	g.topics.Store(topicSub.String(), topic)
	g.PubSub.RegisterTopicValidator(topic.String(), g.consensus.ValidatorFunc())

	// subscribe to the topic
	subscription, err := topic.Subscribe()
	if err != nil {
		return err
	}
	g.subscriptions.Store(topic, subscription)

	go func(location common.Location, sub *pubsub.Subscription) {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
					"location":   location.Name(),
				}).Fatal("Go-Quai Panicked")
			}
		}()
		// Create a channel for messages
		msgChan := make(chan *pubsub.Message, msgChanSize)
		// close the msgChan if we exit this function
		defer close(msgChan)
		full := 0
		// maintain a number of worker threads to handle messages
		var msgWorker func(location common.Location)
		msgWorker = func(location common.Location) {
			defer func() {
				if r := recover(); r != nil {
					log.Global.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
						"location":   location.Name(),
					}).Errorf("Go-Quai Panicked")
				}
				go msgWorker(location) // If this worker exits, start a new one
			}()
			for msg := range msgChan { // This should exit when msgChan is closed
				data := types.ObjectPool.Get()
				data = nil
				// unmarshal the received data depending on the topic's type
				err = pb.UnmarshalAndConvert(msg.Data, location, &data, datatype)
				if err != nil {
					log.Global.Errorf("error unmarshalling data: %s", err)
					continue
				}

				// handle the received data
				if g.onReceived != nil {
					g.onReceived(msg.ReceivedFrom, msg.ID, *msg.Topic, data, location)
				}
			}
		}
		for i := 0; i < numWorkers; i++ {
			go msgWorker(location)
		}
		log.Global.WithField("topic", topic.String()).Debugf("Subscribed to topic")
		for {
			msg, err := sub.Next(g.ctx)
			if err != nil || msg == nil {
				// if context was cancelled, then we are shutting down
				if g.ctx.Err() != nil {
					return
				}
				log.Global.Errorf("error getting next message from subscription: %s", err)
				continue
			}
			log.Global.Tracef("received message on topic: %s", topicSub.String())

			// Send to worker goroutines
			select {
			case msgChan <- msg:
			default:
				if full%1000 == 0 {
					log.Global.WithField("topic", topicSub.String()).Warnf("message channel full. Lost messages: %d", full)
				}
				full++
			}
		}
	}(location, subscription)

	return nil
}

// unsubscribe from broadcasts of the given type of data
func (g *PubsubManager) Unsubscribe(location common.Location, datatype interface{}) error {
	if topic, err := NewTopic(g.genesis, location, datatype); err != nil {
		if value, ok := g.subscriptions.Load(topic); ok {
			value.(*pubsub.Subscription).Cancel()
			g.subscriptions.Delete(topic)
		}
		if value, ok := g.topics.Load(topic); ok {
			value.(*pubsub.Topic).Close()
			g.topics.Delete(topic)
		}
		return nil
	} else {
		return err
	}
}

// broadcasts data to subscribing peers
func (g *PubsubManager) Broadcast(location common.Location, datatype interface{}) error {
	topicName, err := NewTopic(g.genesis, location, datatype)
	if err != nil {
		return err
	}
	protoData, err := pb.ConvertAndMarshal(datatype)
	if err != nil {
		return err
	}
	if value, ok := g.topics.Load(topicName.String()); ok {
		return value.(*pubsub.Topic).Publish(g.ctx, protoData)
	}
	return errors.New("no topic for requested data")
}
