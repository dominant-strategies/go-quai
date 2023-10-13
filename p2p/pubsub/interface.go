package pubsub

import (
	gossip "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

type PSManager interface {
	Join(topic string) (*gossip.Topic, error)
	Subscribe(topic string) (*gossip.Subscription, error)
	Publish(topic string, data []byte) error
	ListPeers(topic string) []peer.ID
	Stop() error
}
