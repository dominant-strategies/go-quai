package node

import (
	pubsub "github.com/libp2p/go-libp2p-pubsub"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"google.golang.org/protobuf/proto"
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
		var pbBlock pb.Block
		if err := proto.Unmarshal(msg.Data, &pbBlock); err != nil {
			log.Errorf("error unmarshaling block: %s", err)
			continue
		}
		block := pb.ConvertFromProtoBlock(&pbBlock)

		// store the block in the cache
		evicted := p.blockCache.Add(block.Hash, &block)
		if evicted {
			// TODO: handle eviction
			log.Warnf("block cache eviction occurred")
		}
	}
}
