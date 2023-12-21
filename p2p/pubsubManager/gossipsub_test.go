package pubsubManager

import (
	"context"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/p2p/pb"
	"google.golang.org/protobuf/proto"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishSubscribeBlock(t *testing.T) {
	ctx := context.Background()

	// Create a mock network
	mnet := mocknet.New()

	// Setup two nodes on the mock network
	host1, err := mnet.GenPeer()
	require.NoError(t, err)
	defer host1.Close()

	host2, err := mnet.GenPeer()
	require.NoError(t, err)
	defer host2.Close()

	// Connect the two nodes
	err = mnet.LinkAll()
	require.NoError(t, err)
	// err = mnet.ConnectAllButSelf()
	require.NoError(t, err)

	// Setup GossipSub
	ps1, err := NewGossipSubManager(ctx, host1)
	require.NoError(t, err)
	ps2, err := NewGossipSubManager(ctx, host2)
	require.NoError(t, err)

	// Subscribe to the topic on the second node
	sub, err := ps2.SubscribeBlock()
	assert.NoError(t, err)

	// Create a block and serialize it
	block := &pb.Block{
		Hash: "test",
	}
	data, err := proto.Marshal(block)
	assert.NoError(t, err)

	// Publish the block on the first node
	err = ps1.PublishBlock(data)
	assert.NoError(t, err)

	// Wait for the message to be received on the second node
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	msg, err := sub.Next(ctx)
	if err != nil {
		t.Fatalf("did not receive message before timeout")
	}

	// Deserialize the received data
	var receivedBlock pb.Block
	err = proto.Unmarshal(msg.Data, &receivedBlock)
	assert.NoError(t, err)

	// Assert that the received block is the same as the one sent
	assert.Equal(t, block.Hash, receivedBlock.Hash)
}
