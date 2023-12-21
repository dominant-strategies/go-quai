package node

import (
	pubsub "github.com/libp2p/go-libp2p-pubsub"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
)

// Waits for blocks to be published to the block topic, and then processes them
func (p *P2PNode) handleBlocksSubscription(sub *pubsub.Subscription) {
	for {
		msg, err := sub.Next(p.ctx)
		if err != nil {
			// if context was cancelled, then we are shutting down
			if p.ctx.Err() != nil {
				return
			}
			log.Errorf("error getting next message from subscription: %s", err)
			continue
		}

		block, err := pb.UnmarshalBlock(msg.Data)
		if err != nil {
			log.Errorf("error unmarshalling block: %s", err)
			continue
		}

		// store the block in the cache
		evicted := p.blockCache.Add(block.Hash, block)
		if evicted {
			// TODO: handle eviction
			log.Warnf("block cache eviction occurred")
		}
	}
}
