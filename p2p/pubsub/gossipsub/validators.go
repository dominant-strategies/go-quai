package gossipsub

import (
	"context"

	gossip "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Register a validator for a topic.
// Validators help in ensuring that incoming Gossipsub messages meet certain criteria
// before they are processed or propagated.
func RegisterValidators(ps *gossip.PubSub) {
	// TODO: implement
	ps.RegisterTopicValidator("example-topic", exampleTopicValidator)
}

func exampleTopicValidator(ctx context.Context, peerID peer.ID, msg *gossip.Message) bool {
	// TODO: implement
	// Validate the message for the "example-topic"
	// Return true if valid, false otherwise
	return true
}
